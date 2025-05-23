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
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/dranet/pkg/apis"
	"github.com/google/dranet/pkg/filter"
	"github.com/google/dranet/pkg/inventory"
	"github.com/google/dranet/pkg/names"

	"github.com/Mellanox/rdmamap"
	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	resourceapply "k8s.io/client-go/applyconfigurations/resource/v1beta1"
	"k8s.io/client-go/kubernetes"
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

	// contains the host interfaces
	netdb      *inventory.DB
	celProgram cel.Program

	// Cache the rdma shared mode state
	rdmaSharedMode bool
	podConfigStore *PodConfigStore
}

type Option func(*NetworkDriver)

func Start(ctx context.Context, driverName string, kubeClient kubernetes.Interface, nodeName string, opts ...Option) (*NetworkDriver, error) {
	rdmaNetnsMode, err := netlink.RdmaSystemGetNetnsMode()
	if err != nil {
		klog.Infof("failed to determine the RDMA subsystem's network namespace mode, assume shared mode: %v", err)
		rdmaNetnsMode = apis.RdmaNetnsModeShared
	} else {
		klog.Infof("RDMA subsystem in mode: %s", rdmaNetnsMode)
	}

	plugin := &NetworkDriver{
		driverName:     driverName,
		nodeName:       nodeName,
		kubeClient:     kubeClient,
		rdmaSharedMode: rdmaNetnsMode == apis.RdmaNetnsModeShared,
		podConfigStore: NewPodConfigStore(),
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
		// Otherwise it silently exits the program
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
	podConfig, ok := np.podConfigStore.GetPodConfigs(types.UID(pod.GetUid()))
	if !ok {
		return nil, nil, nil
	}
	// Containers only cares about the RDMA char devices
	adjust := &api.ContainerAdjustment{}
	for _, config := range podConfig {
		for _, devpath := range config.RDMADevice.DevChars {
			adjust.AddMount(&api.Mount{
				Source:      devpath,
				Destination: devpath,
				Type:        "bind",
				Options:     []string{"bind", "rw"},
			})
		}
	}
	return adjust, nil, nil
}

func (np *NetworkDriver) RunPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("RunPodSandbox Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
	start := time.Now()
	defer func() {
		klog.V(2).Infof("RunPodSandbox Pod %s/%s UID %s took %v", pod.Namespace, pod.Name, pod.Uid, time.Since(start))
	}()
	podConfig, ok := np.podConfigStore.GetPodConfigs(types.UID(pod.GetUid()))
	if !ok {
		return nil
	}
	// get the pod network namespace
	ns := getNetworkNamespace(pod)
	// host network pods are skipped
	if ns == "" {
		return fmt.Errorf("RunPodSandbox pod %s/%s using host network can not claim host devices", pod.Namespace, pod.Name)
	}
	// store the Pod metadata in the db
	np.netdb.AddPodNetns(podKey(pod), ns)

	// Process the configurations of the ResourceClaim
	errorList := []error{}
	// List if char devices associated to the RDMA selected devices
	for deviceName, config := range podConfig {
		klog.V(4).Infof("RunPodSandbox processing device: %s with config: %#v", deviceName, config)
		resourceClaimStatus := resourceapply.ResourceClaimStatus()
		// resourceClaim status for this specific device
		resourceClaimStatusDevice := resourceapply.
			AllocatedDeviceStatus().
			WithDevice(deviceName).
			WithDriver(np.driverName).
			WithPool(np.nodeName)

		ifName := names.GetOriginalName(deviceName)

		// Move the RDMA device to the namespace in exclusive mode
		if !np.rdmaSharedMode {
			klog.V(2).Infof("RunPodSandbox processing RDMA device: %s", ifName)
			err := nsAttachRdmadev(config.RDMADevice.LinkDev, ns)
			if err != nil {
				klog.Infof("RunPodSandbox error getting RDMA device %s to namespace %s: %v", deviceName, ns, err)
				return err
			}
		}

		klog.V(2).Infof("RunPodSandbox processing Network device: %s", ifName)

		// TODO config options to rename the device and pass parameters
		// use https://github.com/opencontainers/runtime-spec/pull/1271
		networkData, err := nsAttachNetdev(ifName, ns, config.NetDevice)
		if err != nil {
			klog.Infof("RunPodSandbox error moving device %s to namespace %s: %v", deviceName, ns, err)
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
			// configure routes
			err := netnsRouting(ns, config.NetNamespaceRoutes)
			if err != nil {
				klog.Infof("RunPodSandbox error configuring device %s namespace %s routing: %v", deviceName, ns, err)
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
		}
		resourceClaimStatus.WithDevices(resourceClaimStatusDevice)
		resourceClaimApply := resourceapply.ResourceClaim(config.Claim.Name, config.Claim.Namespace).WithStatus(resourceClaimStatus)
		// do not block the handler to update the status
		go func() {
			_, err = np.kubeClient.ResourceV1beta1().ResourceClaims(config.Claim.Namespace).ApplyStatus(ctx,
				resourceClaimApply,
				metav1.ApplyOptions{FieldManager: np.driverName, Force: true},
			)
			if err != nil {
				klog.Infof("failed to update status for claim %s/%s : %v", config.Claim.Namespace, config.Claim.Name, err)
			} else {
				klog.V(2).Infof("update status for claim %s/%s", config.Claim.Namespace, config.Claim.Name)
			}
		}()
	}

	return errors.Join(errorList...)
}

func (np *NetworkDriver) StopPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("StopPodSandbox Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
	start := time.Now()
	defer func() {
		np.netdb.RemovePodNetns(podKey(pod))
		klog.V(2).Infof("StopPodSandbox Pod %s/%s UID %s took %v", pod.Namespace, pod.Name, pod.Uid, time.Since(start))
	}()

	return nil
}

func (np *NetworkDriver) RemovePodSandbox(_ context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("RemovePodSandbox Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
	np.netdb.RemovePodNetns(podKey(pod))
	return nil
}

func (np *NetworkDriver) PublishResources(ctx context.Context) {
	klog.V(2).Infof("Publishing resources")
	for {
		select {
		case devices := <-np.netdb.GetResources(ctx):
			klog.V(4).Infof("Received %d devices", len(devices))
			devices = filter.FilterDevices(np.celProgram, devices)
			klog.V(4).Infof("After filtering %d devices", len(devices))
			resources := resourceslice.DriverResources{
				Pools: map[string]resourceslice.Pool{
					np.nodeName: {Slices: []resourceslice.Slice{{Devices: devices}}}},
			}
			err := np.draPlugin.PublishResources(ctx, resources)
			if err != nil {
				klog.Error(err, "unexpected error trying to publish resources")
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

// prepareResourceClaim gets all the configuration required to be applied at runtime and passes it downs to the handlers.
// This happens in the kubelet so it can be a "slow" operation, so we can execute fast in RunPodsandbox, that happens in the
// container runtime and has strong expectactions to be executed fast (default hook timeout is 2 seconds).
func (np *NetworkDriver) prepareResourceClaim(ctx context.Context, claim *resourceapi.ResourceClaim) kubeletplugin.PrepareResult {
	// TODO: shared devices may allocate the same device to multiple pods, i.e. macvlan, ipvlan, ...
	podUIDs := []types.UID{}
	for _, reserved := range claim.Status.ReservedFor {
		if reserved.Resource != "pods" || reserved.APIGroup != "" {
			klog.Infof("Driver only supports Pods, unsupported reference %#v", reserved)
			continue
		}
		podUIDs = append(podUIDs, reserved.UID)
	}
	if len(podUIDs) == 0 {
		klog.Infof("no pods allocated to claim %s/%s", claim.Namespace, claim.Name)
		return kubeletplugin.PrepareResult{}
	}

	nlHandle, err := netlink.NewHandle()
	if err != nil {
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("error creating netlink handle %v", err),
		}
	}

	var errorList []error
	var netconf apis.NetworkConfig
	charDevices := sets.New[string]()
	for _, result := range claim.Status.Allocation.Devices.Results {
		requestName := result.Request
		for _, config := range claim.Status.Allocation.Devices.Config {
			// Check there is a config associated to this device
			if config.Opaque == nil ||
				config.Opaque.Driver != np.driverName ||
				len(config.Requests) > 0 && !slices.Contains(config.Requests, requestName) {
				continue
			}
			// Check if there is a custom configuration
			conf, err := apis.ValidateConfig(&config.Opaque.Parameters)
			if err != nil {
				errorList = append(errorList, err)
				continue
			}
			// TODO: define a strategy for multiple configs
			if conf != nil {
				netconf = *conf
				break
			}
		}
		klog.V(4).Infof("PrepareResourceClaim %s/%s final Configuration %#v", claim.Namespace, claim.Name, netconf)
		podCfg := PodConfig{
			Claim: types.NamespacedName{
				Namespace: claim.Namespace,
				Name:      claim.Name,
			},
		}
		ifName := names.GetOriginalName(result.Device)
		// Get Network configuration and merge it
		link, err := nlHandle.LinkByName(ifName)
		if err != nil {
			errorList = append(errorList, fmt.Errorf("fail to get network interface %s", ifName))
			continue
		}

		// If there is no custom addresses then use the existing ones
		if len(netconf.Interface.Addresses) == 0 {
			// get the existing IP addresses
			nlAddresses, err := nlHandle.AddrList(link, netlink.FAMILY_ALL)
			if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
				klog.Infof("fail to get ip addresses for interface %s : %v", ifName, err)
			}
			for _, address := range nlAddresses {
				// Only move IP addresses with global scope because those are not host-specific, auto-configured,
				// or have limited network scope, making them unsuitable inside the container namespace.
				// Ref: https://www.ietf.org/rfc/rfc3549.txt
				if address.Scope != unix.RT_SCOPE_UNIVERSE {
					continue
				}
				// remove the interface attribute of the original address
				// to avoid issues when the interface is renamed.
				netconf.Interface.Addresses = append(netconf.Interface.Addresses, address.IPNet.String())
			}
		}

		// If there are no addresses configured on the interface and the user is not setting them
		// this may be an interface that uses DHCP, so we bring it up if necessary and do a DHCP
		// request to gather the network parameters (IPs and Routes) ... but we DO NOT apply them
		// in the root namespace
		if len(netconf.Interface.Addresses) == 0 {
			ip, routes, err := getDHCP(ifName)
			if err != nil {
				klog.Infof("fail to get configuration via DHCP: %v", err)
			} else {
				netconf.Interface.Addresses = []string{ip}
				netconf.Routes = append(netconf.Routes, routes...)
			}
		}

		// Obtain the routes associated to the interface
		// TODO: only considers outgoing traffic
		filter := &netlink.Route{
			LinkIndex: link.Attrs().Index,
		}
		routes, err := nlHandle.RouteListFiltered(netlink.FAMILY_ALL, filter, netlink.RT_FILTER_OIF)
		if err != nil {
			klog.Infof("fail to get ip routes for interface %s : %v", ifName, err)
		}
		for _, route := range routes {
			routeCfg := apis.RouteConfig{}
			if route.Src != nil {
				routeCfg.Source = route.Src.String()
			}
			if route.Dst != nil {
				routeCfg.Destination = route.Dst.String()
			}
			if route.Gw != nil {
				routeCfg.Gateway = route.Gw.String()
			}
			podCfg.NetNamespaceRoutes = append(podCfg.NetNamespaceRoutes, routeCfg)
		}

		// Get RDMA configuration: link and char devices
		if rdmaDev, _ := rdmamap.GetRdmaDeviceForNetdevice(ifName); rdmaDev != "" {
			klog.V(2).Infof("RunPodSandbox processing RDMA device: %s", rdmaDev)
			podCfg.RDMADevice.LinkDev = rdmaDev
			// Obtain the char devices associated to the rdma device
			charDevices.Insert(rdmaCmPath)
			charDevices.Insert(rdmamap.GetRdmaCharDevices(rdmaDev)...)
			podCfg.RDMADevice.DevChars = charDevices.UnsortedList()
		}

		device := kubeletplugin.Device{
			Requests:   []string{result.Request},
			PoolName:   result.Pool,
			DeviceName: result.Device,
		}
		// TODO: support for multiple pods sharing the same device
		// we'll create the subinterface here
		for _, uid := range podUIDs {
			np.podConfigStore.Set(uid, device.DeviceName, podCfg)
		}
	}

	if len(errorList) > 0 {
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("claim %s contain errors: %w", claim.UID, errors.Join(errorList...)),
		}
	}
	return kubeletplugin.PrepareResult{}
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
	np.podConfigStore.DeleteClaim(claim.NamespacedName)
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
