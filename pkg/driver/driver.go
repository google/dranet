/*
Copyright 2024 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/dranet/pkg/apis"
	"github.com/google/dranet/pkg/filter"
	"github.com/google/dranet/pkg/inventory"
	"github.com/vishvananda/netlink"

	"github.com/Mellanox/rdmamap"
	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"

	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	resourceapply "k8s.io/client-go/applyconfigurations/resource/v1beta1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"
)

const (
	kubeletPluginRegistryPath = "/var/lib/kubelet/plugins_registry"
	kubeletPluginPath         = "/var/lib/kubelet/plugins"
)

const (
	// podUIDIndex is the lookup name for the most common index function, which is to index by the pod UID field.
	podUIDIndex string = "podUID"

	// maxAttempts indicates the number of times the driver will try to recover itself before failing
	maxAttempts = 5

	rdmaCmPath = "/dev/infiniband/rdma_cm"
)

// podUIDIndexFunc is a default index function that indexes based on an pod UID
func podUIDIndexFunc(obj interface{}) ([]string, error) {
	claim, ok := obj.(*resourceapi.ResourceClaim)
	if !ok {
		return []string{}, nil
	}

	result := []string{}
	for _, reserved := range claim.Status.ReservedFor {
		if reserved.Resource != "pods" || reserved.APIGroup != "" {
			continue
		}
		result = append(result, string(reserved.UID))
	}
	return result, nil
}

// WithFilter
func WithFilter(filter cel.Program) Option {
	return func(o *NetworkDriver) {
		o.celProgram = filter
	}
}

type NetworkDriver struct {
	driverName string
	nodeName   string
	kubeClient kubernetes.Interface
	draPlugin  *kubeletplugin.Helper
	nriPlugin  stub.Stub

	claimAllocations cache.Indexer // claims indexed by Claim UID to run on the Kubelet/DRA hooks
	// contains the host interfaces
	netdb *inventory.DB
	// options
	celProgram cel.Program
	// local copy of current devices
	muDevices sync.Mutex
	devices   []resourceapi.Device
	// An RDMA device can only be assigned to a network namespace when the RDMA subsystem is set to an "exclusive" network namespace mode
	// Do not publish or process RDMA devices in shared mode.
	rdmaSharedMode bool
}

type Option func(*NetworkDriver)

func Start(ctx context.Context, driverName string, kubeClient kubernetes.Interface, nodeName string, opts ...Option) (*NetworkDriver, error) {
	store := cache.NewIndexer(cache.MetaNamespaceKeyFunc,
		cache.Indexers{
			cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
			podUIDIndex:          podUIDIndexFunc,
		})

	rdmaNetnsMode, err := netlink.RdmaSystemGetNetnsMode()
	if err != nil {
		klog.Infof("failed to determine the RDMA subsystem's network namespace mode, assume shared mode: %v", err)
		rdmaNetnsMode = apis.RdmaNetnsModeShared
	} else {
		klog.Infof("RDMA subsystem in mode: %s", rdmaNetnsMode)
	}

	plugin := &NetworkDriver{
		driverName:       driverName,
		nodeName:         nodeName,
		kubeClient:       kubeClient,
		claimAllocations: store,
		devices:          []resourceapi.Device{},
		rdmaSharedMode:   rdmaNetnsMode == apis.RdmaNetnsModeShared,
	}

	for _, o := range opts {
		o(plugin)
	}

	driverPluginPath := filepath.Join(kubeletPluginPath, driverName)
	err = os.MkdirAll(driverPluginPath, 0750)
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin path %s: %v", driverPluginPath, err)
	}

	kubeletOpts := []kubeletplugin.Option{
		kubeletplugin.DriverName(driverName),
		kubeletplugin.NodeName(nodeName),
		kubeletplugin.KubeClient(kubeClient),
	}
	d, err := kubeletplugin.Start(ctx, plugin, kubeletOpts...)
	if err != nil {
		return nil, fmt.Errorf("start kubelet plugin: %w", err)
	}
	plugin.draPlugin = d
	err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 30*time.Second, true, func(context.Context) (bool, error) {
		status := plugin.draPlugin.RegistrationStatus()
		if status == nil {
			return false, nil
		}
		return status.PluginRegistered, nil
	})
	if err != nil {
		return nil, err
	}

	// register the NRI plugin
	nriOpts := []stub.Option{
		stub.WithPluginName(driverName),
		stub.WithPluginIdx("00"),
		// https://github.com/containerd/nri/pull/173
		// Otherwise it silently exits the progrms
		stub.WithOnClose(func() {
			klog.Infof("%s NRI plugin closed", driverName)
		}),
	}
	stub, err := stub.New(plugin, nriOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin stub: %v", err)
	}
	plugin.nriPlugin = stub

	go func() {
		for i := 0; i < maxAttempts; i++ {
			err = plugin.nriPlugin.Run(ctx)
			if err != nil {
				klog.Infof("NRI plugin failed with error %v", err)
			}
			select {
			case <-ctx.Done():
				return
			default:
				klog.Infof("Restarting NRI plugin %d out of %d", i, maxAttempts)
			}
		}
		klog.Fatalf("NRI plugin failed for %d times to be restarted", maxAttempts)
	}()

	// register the host network interfaces
	plugin.netdb = inventory.New()
	go func() {
		for i := 0; i < maxAttempts; i++ {
			err = plugin.netdb.Run(ctx)
			if err != nil {
				klog.Infof("Network Device DB failed with error %v", err)
			}
			select {
			case <-ctx.Done():
				return
			default:
				klog.Infof("Restarting Network Device DB %d out of %d", i, maxAttempts)
			}
		}
		klog.Fatalf("Network Device DB failed for %d times to be restarted", maxAttempts)
	}()

	// publish available resources
	go plugin.PublishResources(ctx)

	return plugin, nil
}

func (np *NetworkDriver) Stop() {
	np.nriPlugin.Stop()
	np.draPlugin.Stop()
}

func (np *NetworkDriver) getDevice(deviceName string) *resourceapi.Device {
	np.muDevices.Lock()
	defer np.muDevices.Unlock()
	for _, device := range np.devices {
		if device.Name == deviceName {
			return &device
		}
	}
	return nil
}

func (np *NetworkDriver) Synchronize(_ context.Context, pods []*api.PodSandbox, containers []*api.Container) ([]*api.ContainerUpdate, error) {
	klog.Infof("Synchronized state with the runtime (%d pods, %d containers)...",
		len(pods), len(containers))

	for _, pod := range pods {
		klog.Infof("Synchronize Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
		klog.V(2).Infof("pod %s/%s: namespace=%s ips=%v", pod.GetNamespace(), pod.GetName(), getNetworkNamespace(pod), pod.GetIps())
		// get the pod network namespace
		ns := getNetworkNamespace(pod)
		// host network pods are skipped
		if ns != "" {
			// store the Pod metadata in the db
			np.netdb.AddPodNetns(podKey(pod), ns)
		}
	}

	return nil, nil
}

func (np *NetworkDriver) Shutdown(_ context.Context) {
	klog.Info("Runtime shutting down...")
}

// CreateContainer handles container creation requests.
func (np *NetworkDriver) CreateContainer(_ context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	klog.V(2).Infof("CreateContainer Pod %s/%s UID %s Container %s", pod.Namespace, pod.Name, pod.Uid, ctr.Name)
	// get the pod network namespace
	ns := getNetworkNamespace(pod)
	// host network pods are skipped
	if ns == "" {
		klog.V(2).Infof("CreateContainer pod %s/%s using host network, skipping", pod.Namespace, pod.Name)
		return nil, nil, nil
	}

	objs, err := np.claimAllocations.ByIndex(podUIDIndex, pod.Uid)
	if err != nil || len(objs) == 0 {
		klog.V(4).Infof("RunPodSandbox Pod %s/%s does not have an associated ResourceClaim", pod.Namespace, pod.Name)
		return nil, nil, nil
	}

	// Process the configurations of the ResourceClaim
	errorList := []error{}
	adjust := &api.ContainerAdjustment{}
	rdmaCM := false

	for _, obj := range objs {
		claim, ok := obj.(*resourceapi.ResourceClaim)
		if !ok {
			continue
		}

		if claim.Status.Allocation == nil {
			continue
		}
		// final resourceClaim Status
		// resourceClaimStatus := resourceapply.ResourceClaimStatus()
		for _, result := range claim.Status.Allocation.Devices.Results {
			if result.Driver != np.driverName {
				continue
			}

			ifName := result.Device
			localDevice := np.getDevice(result.Device)
			if localDevice != nil {
				ifName = getDeviceName(localDevice)
			} else {
				klog.Infof("local device for %s not found", ifName)
			}

			// default to network device
			kind := apis.NetworkKind
			if v, ok := localDevice.Basic.Attributes["dra.net/kind"]; ok && v.StringValue != nil {
				kind = *v.StringValue
			}

			devices := []string{}
			if kind == apis.RdmaKind {
				klog.V(2).Infof("RunPodSandbox processing RDMA device: %s", ifName)
				devices = rdmamap.GetRdmaCharDevices(ifName)
			} else if rdmaDev, _ := rdmamap.GetRdmaDeviceForNetdevice(result.Device); rdmaDev != "" {
				klog.V(2).Infof("RunPodSandbox processing RDMA device: %s", rdmaDev)
				devices = rdmamap.GetRdmaCharDevices(rdmaDev)
			}
			for _, device := range devices {
				adjust.AddMount(&api.Mount{
					Source:      device,
					Destination: device,
					Type:        "bind",
					Options:     []string{"bind", "rw"},
				})
				if device == rdmaCmPath {
					rdmaCM = true
				}
			}
		}
	}
	// only mount it once
	if !rdmaCM {
		adjust.AddMount(&api.Mount{
			Source:      rdmaCmPath,
			Destination: rdmaCmPath,
			Type:        "bind",
			Options:     []string{"bind", "rw"},
		})
	}
	return adjust, nil, errors.Join(errorList...)
}

func (np *NetworkDriver) RunPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("RunPodSandbox Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
	start := time.Now()
	defer func() {
		klog.V(2).Infof("RunPodSandbox Pod %s/%s UID %s took %v", pod.Namespace, pod.Name, pod.Uid, time.Since(start))
	}()
	objs, err := np.claimAllocations.ByIndex(podUIDIndex, pod.Uid)
	if err != nil || len(objs) == 0 {
		klog.V(4).Infof("RunPodSandbox Pod %s/%s does not have an associated ResourceClaim", pod.Namespace, pod.Name)
		return nil
	}

	// get the pod network namespace
	ns := getNetworkNamespace(pod)
	// host network pods are skipped
	if ns == "" {
		klog.V(2).Infof("RunPodSandbox pod %s/%s using host network, skipping", pod.Namespace, pod.Name)
		return nil
	}
	// store the Pod metadata in the db
	np.netdb.AddPodNetns(podKey(pod), ns)

	// Process the configurations of the ResourceClaim
	errorList := []error{}
	for _, obj := range objs {
		claim, ok := obj.(*resourceapi.ResourceClaim)
		if !ok {
			continue
		}

		if claim.Status.Allocation == nil {
			continue
		}
		// final resourceClaim Status
		resourceClaimStatus := resourceapply.ResourceClaimStatus()
		var netconf apis.NetworkConfig
		for _, result := range claim.Status.Allocation.Devices.Results {
			if result.Driver != np.driverName {
				continue
			}
			// Process the configurations of the ResourceClaim
			for _, config := range claim.Status.Allocation.Devices.Config {
				if config.Opaque == nil {
					continue
				}
				if len(config.Requests) > 0 && !slices.Contains(config.Requests, result.Request) {
					continue
				}
				// TODO: handle the case with multiple configurations (is that possible, should we merge them?)
				conf, err := apis.ValidateConfig(&config.Opaque.Parameters)
				if err != nil {
					klog.Infof("podStartHook Configuration %+v error: %v", netconf, err)
					return err
				}
				// TODO: define a strategy for multiple configs
				if conf != nil {
					netconf = *conf
					break
				}
			}
			klog.V(4).Infof("podStartHook final Configuration %+v", netconf)

			// resourceClaim status for this specific device
			resourceClaimStatusDevice := resourceapply.
				AllocatedDeviceStatus().
				WithDevice(result.Device).
				WithDriver(result.Driver).
				WithPool(result.Pool)

			ifName := result.Device
			localDevice := np.getDevice(result.Device)
			if localDevice != nil {
				ifName = getDeviceName(localDevice)
			} else {
				klog.Infof("local device for %s not found", ifName)
			}

			// default to network device
			kind := apis.NetworkKind
			if v, ok := localDevice.Basic.Attributes["dra.net/kind"]; ok && v.StringValue != nil {
				kind = *v.StringValue
			}

			if kind == apis.RdmaKind {
				klog.V(2).Infof("RunPodSandbox processing RDMA device: %s", ifName)
				if !np.rdmaSharedMode {
					err := nsAttachRdmadev(ifName, ns)
					if err != nil {
						klog.Infof("RunPodSandbox error getting RDMA device %s to namespace %s: %v", result.Device, ns, err)
						return err
					}
				}
				// no more procesing
				continue
			}

			klog.V(2).Infof("RunPodSandbox processing Network device: %s", ifName)
			// only process RDMA devices associated to the network interface if in exclusive mode.
			if !np.rdmaSharedMode {
				if rdmaDev, _ := rdmamap.GetRdmaDeviceForNetdevice(result.Device); rdmaDev != "" {
					klog.V(2).Infof("RunPodSandbox processing RDMA device: %s", rdmaDev)
					err := nsAttachRdmadev(rdmaDev, ns)
					if err != nil {
						klog.Infof("RunPodSandbox error getting RDMA device %s to namespace %s: %v", result.Device, ns, err)
					}
				}
			}

			// configure routes
			err = netnsRouting(ns, netconf.Routes)
			if err != nil {
				klog.Infof("RunPodSandbox error configuring device %s namespace %s routing: %v", result.Device, ns, err)
				resourceClaimStatusDevice.WithConditions(
					metav1apply.Condition().
						WithType("NetworkReady").
						WithStatus(metav1.ConditionFalse).
						WithReason("NetworkReadyError").
						WithMessage(err.Error()).
						WithLastTransitionTime(metav1.Now()),
				)
				errorList = append(errorList, err)
			} else {
				resourceClaimStatusDevice.WithConditions(
					metav1apply.Condition().
						WithType("NetworkReady").
						WithStatus(metav1.ConditionTrue).
						WithReason("NetworkReady").
						WithLastTransitionTime(metav1.Now()),
				)
			}

			// TODO config options to rename the device and pass parameters
			// use https://github.com/opencontainers/runtime-spec/pull/1271
			networkData, err := nsAttachNetdev(ifName, ns, netconf.Interface)
			if err != nil {
				klog.Infof("RunPodSandbox error moving device %s to namespace %s: %v", result.Device, ns, err)
				resourceClaimStatusDevice.WithConditions(
					metav1apply.Condition().
						WithType("Ready").
						WithStatus(metav1.ConditionFalse).
						WithReason("NetworkDeviceError").
						WithMessage(err.Error()).
						WithLastTransitionTime(metav1.Now()),
				)
				errorList = append(errorList, err)
			} else {
				resourceClaimStatusDevice.WithConditions(
					metav1apply.Condition().
						WithType("Ready").
						WithReason("NetworkDeviceReady").
						WithStatus(metav1.ConditionTrue).
						WithLastTransitionTime(metav1.Now()),
				).WithNetworkData(resourceapply.NetworkDeviceData().
					WithInterfaceName(networkData.InterfaceName).
					WithHardwareAddress(networkData.HardwareAddress).
					WithIPs(networkData.IPs...),
				)
			}
			resourceClaimStatus.WithDevices(resourceClaimStatusDevice)
		}
		resourceClaimApply := resourceapply.ResourceClaim(claim.Name, claim.Namespace).WithStatus(resourceClaimStatus)
		_, err = np.kubeClient.ResourceV1beta1().ResourceClaims(claim.Namespace).ApplyStatus(ctx,
			resourceClaimApply,
			metav1.ApplyOptions{FieldManager: np.driverName, Force: true},
		)
		// do not fail hard
		if err != nil {
			klog.Infof("failed to update status for claim %s/%s : %v", claim.Namespace, claim.Name, err)
		} else {
			klog.V(2).Infof("update status for claim %s/%s", claim.Namespace, claim.Name)
		}
	}
	return errors.Join(errorList...)
}

func (np *NetworkDriver) StopPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("StopPodSandbox Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
	start := time.Now()
	defer func() {
		klog.V(2).Infof("StopPodSandbox Pod %s/%s UID %s took %v", pod.Namespace, pod.Name, pod.Uid, time.Since(start))
	}()
	defer np.netdb.RemovePodNetns(podKey(pod))

	objs, err := np.claimAllocations.ByIndex(podUIDIndex, pod.Uid)
	if err != nil || len(objs) == 0 {
		klog.V(2).Infof("StopPodSandbox pod %s/%s does not have allocations", pod.Namespace, pod.Name)
		return nil
	}

	// get the pod network namespace
	ns := getNetworkNamespace(pod)
	if ns == "" {
		klog.V(2).Infof("StopPodSandbox pod %s/%s using host network, skipping", pod.Namespace, pod.Name)
		// check if is stored in our db, containerd old versions with NRI does not pass this information
		ns = np.netdb.GetPodNamespace(podKey(pod))
		if ns == "" {
			return nil
		}
	}
	// Process the configurations of the ResourceClaim
	for _, obj := range objs {
		claim, ok := obj.(*resourceapi.ResourceClaim)
		if !ok {
			continue
		}

		if claim.Status.Allocation == nil {
			continue
		}

		for _, result := range claim.Status.Allocation.Devices.Results {
			if result.Driver != np.driverName {
				continue
			}

			var netconf *apis.NetworkConfig
			for _, config := range claim.Status.Allocation.Devices.Config {
				if config.Opaque == nil {
					continue
				}
				if len(config.Requests) > 0 && !slices.Contains(config.Requests, result.Request) {
					continue
				}
				// TODO: handle the case with multiple configurations (is that possible, should we merge them?)
				netconf, err = apis.ValidateConfig(&config.Opaque.Parameters)
				if err != nil {
					return err
				}
				if netconf != nil {
					klog.V(4).Infof("Configuration %#v", netconf)
					break
				}
			}

			klog.V(4).Infof("podStopHook Device %s", result.Device)
			// TODO config options to rename the device and pass parameters
			// use https://github.com/opencontainers/runtime-spec/pull/1271
			ifName := result.Device
			outName := ""
			if netconf.Interface.Name != "" {
				ifName = netconf.Interface.Name
				outName = result.Device
			}
			err := nsDetachNetdev(ns, ifName, outName)
			if err != nil {
				klog.Infof("StopPodSandbox error moving device %s to namespace %s: %v", result.Device, ns, err)
				continue
			}
		}
	}
	return nil
}

func (np *NetworkDriver) RemovePodSandbox(_ context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("RemovePodSandbox Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
	defer np.netdb.RemovePodNetns(podKey(pod))
	return nil
}

func (np *NetworkDriver) PublishResources(ctx context.Context) {
	klog.V(2).Infof("Publishing resources")
	for {
		select {
		case devices := <-np.netdb.GetResources(ctx):
			klog.V(4).Infof("Received %d devices", len(devices))
			devices = filter.FilterDevices(np.celProgram, devices)
			if np.rdmaSharedMode {
				// do not publish RDMA devices in shared mode
				n := 0
				for _, device := range devices {
					if kind, ok := device.Basic.Attributes["dra.net/kind"]; ok &&
						kind.StringValue != nil &&
						*kind.StringValue == "rdma" {
						continue
					}
					devices[n] = device
					n++
				}
				devices = devices[:n]
			}
			resources := resourceslice.DriverResources{
				Pools: map[string]resourceslice.Pool{
					np.nodeName: {
						Slices: []resourceslice.Slice{
							{
								Devices: devices,
							},
						},
					},
				},
			}
			err := np.draPlugin.PublishResources(ctx, resources)
			if err != nil {
				klog.Error(err, "unexpected error trying to publish resources")
			} else {
				// keep a local copy of the published device if we need to operate
				// with some of the exported attributes, typically dra.net/ifName in
				// in case the device name has to be normalized.
				np.muDevices.Lock()
				np.devices = devices
				np.muDevices.Unlock()
			}
		case <-ctx.Done():
			klog.Error(ctx.Err(), "context canceled")
			return
		}
		// poor man rate limit
		time.Sleep(3 * time.Second)
	}
}

func (np *NetworkDriver) PrepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
	klog.V(2).Infof("PrepareResourceClaims is called: number of claims: %d", len(claims))
	if len(claims) == 0 {
		return nil, nil
	}
	result := make(map[types.UID]kubeletplugin.PrepareResult)

	for _, claim := range claims {
		klog.V(2).Infof("NodePrepareResources: Claim Request %s/%s", claim.Namespace, claim.Name)
		result[claim.UID] = np.prepareResourceClaim(ctx, claim)
	}
	return result, nil
}

// TODO define better what is passed at the podStartHook
// Filter out the allocations not required for this Pod
func (np *NetworkDriver) prepareResourceClaim(_ context.Context, claim *resourceapi.ResourceClaim) kubeletplugin.PrepareResult {
	err := np.claimAllocations.Add(claim)
	if err != nil {
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("failed to add claim %s to local cache:: %w", claim.UID, err),
		}
	}

	for _, reserved := range claim.Status.ReservedFor {
		if reserved.Resource != "pods" || reserved.APIGroup != "" {
			klog.Infof("Driver only supports Pods, unsupported reference %#v", reserved)
			continue
		}
	}

	var errorList []error
	var devices []kubeletplugin.Device
	for _, result := range claim.Status.Allocation.Devices.Results {
		requestName := result.Request
		for _, config := range claim.Status.Allocation.Devices.Config {
			if config.Opaque == nil ||
				config.Opaque.Driver != np.driverName ||
				len(config.Requests) > 0 && !slices.Contains(config.Requests, requestName) {
				continue
			}
			_, err := apis.ValidateConfig(&config.Opaque.Parameters)
			if err != nil {
				errorList = append(errorList, err)
			}
		}
		device := kubeletplugin.Device{
			Requests:   []string{result.Request},
			PoolName:   result.Pool,
			DeviceName: result.Device,
		}
		devices = append(devices, device)
	}
	if len(errorList) > 0 {
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("claim %s contain errors: %w", claim.UID, errors.Join(errorList...)),
		}
	}
	return kubeletplugin.PrepareResult{Devices: devices}
}

func (np *NetworkDriver) UnprepareResourceClaims(ctx context.Context, claims []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
	klog.V(2).Infof("UnprepareResourceClaims is called: number of claims: %d", len(claims))
	if len(claims) == 0 {
		return nil, nil
	}

	result := make(map[types.UID]error)

	for _, claim := range claims {
		err := np.unprepareResourceClaim(ctx, claim)
		result[claim.UID] = err
		if err != nil {
			klog.Infof("error unpreparing ressources for claim %s/%s : %v", claim.Namespace, claim.Name, err)
		}
	}
	return result, nil
}

func (np *NetworkDriver) unprepareResourceClaim(_ context.Context, claim kubeletplugin.NamespacedObject) error {
	objs, err := np.claimAllocations.ByIndex(cache.NamespaceIndex, fmt.Sprintf("%s/%s", claim.Namespace, claim.Name))
	if err != nil || len(objs) == 0 {
		klog.Infof("Claim %s/%s does not have an associated cached ResourceClaim: %v", claim.Namespace, claim.Name, err)
		return nil
	}

	for _, obj := range objs {
		claim, ok := obj.(*resourceapi.ResourceClaim)
		if !ok {
			continue
		}
		defer func() {
			err := np.claimAllocations.Delete(obj)
			if err != nil {
				klog.Infof("Claim %s/%s can not be deleted from cache: %v", claim.Namespace, claim.Name, err)
			}
		}()

		if claim.Status.Allocation == nil {
			continue
		}

		for _, result := range claim.Status.Allocation.Devices.Results {
			if result.Driver != np.driverName {
				continue
			}

			for _, config := range claim.Status.Allocation.Devices.Config {
				if config.Opaque == nil {
					continue
				}
				klog.V(4).Infof("nodeUnprepareResource Configuration %s", string(config.Opaque.Parameters.String()))
				// TODO get config options here, it can add ips or commands
				// to add routes, run dhcp, rename the interface ... whatever
			}
			klog.Infof("nodeUnprepareResource claim %s/%s with allocation result %#v", claim.Namespace, claim.Name, result)

		}
	}
	return nil
}

func getNetworkNamespace(pod *api.PodSandbox) string {
	// get the pod network namespace
	for _, namespace := range pod.Linux.GetNamespaces() {
		if namespace.Type == "network" {
			return namespace.Path
		}
	}
	return ""
}

func podKey(pod *api.PodSandbox) string {
	return fmt.Sprintf("%s/%s", pod.GetNamespace(), pod.GetName())
}

// getDeviceName returns the actual device name since Kubernetes validation does not allow
// some device interface names, the device name is normalized and the real interface name
// is passed as an attribute.
func getDeviceName(device *resourceapi.Device) string {
	if device == nil {
		klog.V(5).Info("GetDeviceName called with nil device")
		return ""
	}
	if device.Basic == nil || device.Basic.Attributes == nil {
		klog.V(5).Infof("GetDeviceName: device %s has no Basic attributes, returning normalized name", device.Name)
		return device.Name // Fallback to normalized name
	}

	kindAttr, ok := device.Basic.Attributes["dra.net/kind"]
	if !ok || kindAttr.StringValue == nil {
		klog.V(4).Infof("GetDeviceName: 'dra.net/kind' attribute not found or not a string for device %s, returning normalized name", device.Name)
		return device.Name // Fallback to normalized name
	}

	var nameAttrKey resourceapi.QualifiedName
	switch *kindAttr.StringValue {
	case apis.NetworkKind:
		nameAttrKey = "dra.net/ifName"
	case apis.RdmaKind:
		nameAttrKey = "dra.net/rdmaDevName"
	default:
		klog.V(4).Infof("GetDeviceName: unknown kind '%s' for device %s, returning normalized name", *kindAttr.StringValue, device.Name)
		return device.Name // Fallback to normalized name
	}

	actualNameAttr, ok := device.Basic.Attributes[nameAttrKey]
	if !ok || actualNameAttr.StringValue == nil {
		klog.V(4).Infof("GetDeviceName: attribute '%s' not found or not a string for device %s (kind: %s), returning normalized name", nameAttrKey, device.Name, *kindAttr.StringValue)
		return device.Name // Fallback to normalized name
	}
	return *actualNameAttr.StringValue
}
