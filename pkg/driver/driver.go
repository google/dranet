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
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/dranet/pkg/filter"
	"github.com/google/dranet/pkg/inventory"

	"github.com/Mellanox/rdmamap"
	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"

	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
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
}

type Option func(*NetworkDriver)

func Start(ctx context.Context, driverName string, kubeClient kubernetes.Interface, nodeName string, opts ...Option) (*NetworkDriver, error) {
	store := cache.NewIndexer(cache.MetaNamespaceKeyFunc,
		cache.Indexers{
			cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
			podUIDIndex:          podUIDIndexFunc,
		})

	plugin := &NetworkDriver{
		driverName:       driverName,
		nodeName:         nodeName,
		kubeClient:       kubeClient,
		claimAllocations: store,
	}

	for _, o := range opts {
		o(plugin)
	}

	driverPluginPath := filepath.Join(kubeletPluginPath, driverName)
	err := os.MkdirAll(driverPluginPath, 0750)
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
	}
	stub, err := stub.New(plugin, nriOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin stub: %v", err)
	}
	plugin.nriPlugin = stub

	go func() {
		err = plugin.nriPlugin.Run(ctx)
		if err != nil {
			klog.Infof("NRI plugin failed with error %v", err)
		}
	}()

	// register the host network interfaces
	plugin.netdb = inventory.New()
	go func() {
		err = plugin.netdb.Run(ctx)
		if err != nil {
			klog.Infof("Network Device DB failed with error %v", err)
		}
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
		klog.Infof("pod %s/%s: namespace=%s ips=%v", pod.GetNamespace(), pod.GetName(), getNetworkNamespace(pod), pod.GetIps())
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

func (np *NetworkDriver) RunPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("RunPodSandbox Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
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

			// Process the configurations of the ResourceClaim
			for _, config := range claim.Status.Allocation.Devices.Config {
				if config.Opaque == nil {
					continue
				}
				if len(config.Requests) > 0 && !slices.Contains(config.Requests, result.Request) {
					continue
				}
				klog.V(4).Infof("podStartHook Configuration %s", string(config.Opaque.Parameters.String()))
				// TODO get config options here, it can add ips or commands
				// to add routes, run dhcp, rename the interface ... whatever
			}

			klog.Infof("RunPodSandbox allocation.Devices.Result: %#v", result)
			// TODO signal this via DRA
			if rdmaDev, _ := rdmamap.GetRdmaDeviceForNetdevice(result.Device); rdmaDev != "" {
				err := nsAttachRdmadev(rdmaDev, ns)
				if err != nil {
					klog.Infof("RunPodSandbox error getting RDMA device %s to namespace %s: %v", result.Device, ns, err)
					continue
				}
			}

			// TODO config options to rename the device and pass parameters
			// use https://github.com/opencontainers/runtime-spec/pull/1271
			err := nsAttachNetdev(result.Device, ns, result.Device)
			if err != nil {
				klog.Infof("RunPodSandbox error moving device %s to namespace %s: %v", result.Device, ns, err)
				return err
			}
		}
	}
	return nil
}

func (np *NetworkDriver) StopPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("StopPodSandbox pod %s/%s", pod.Namespace, pod.Name)
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
		return nil
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

			for _, config := range claim.Status.Allocation.Devices.Config {
				if config.Opaque == nil {
					continue
				}
				klog.V(4).Infof("podStopHook Configuration %s", string(config.Opaque.Parameters.String()))
				// TODO get config options here, it can add ips or commands
				// to add routes, run dhcp, rename the interface ... whatever
			}

			klog.V(4).Infof("podStopHook Device %s", result.Device)
			// TODO config options to rename the device and pass parameters
			// use https://github.com/opencontainers/runtime-spec/pull/1271
			err := nsDetachNetdev(ns, result.Device)
			if err != nil {
				klog.Infof("StopPodSandbox error moving device %s to namespace %s: %v", result.Device, ns, err)
				continue
			}
		}
	}
	return nil
}

func (np *NetworkDriver) RemovePodSandbox(_ context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("RemovePodSandbox pod %s/%s: ips=%v", pod.GetNamespace(), pod.GetName(), pod.GetIps())
	// get the pod network namespace
	ns := getNetworkNamespace(pod)
	if ns == "" {
		klog.V(2).Infof("RemovePodSandbox pod %s/%s using host network, skipping", pod.Namespace, pod.Name)
		return nil
	}
	return nil
}

func (np *NetworkDriver) PublishResources(ctx context.Context) {
	klog.V(2).Infof("Publishing resources")
	for {
		select {
		case devices := <-np.netdb.GetResources(ctx):
			klog.V(4).Infof("Received %d devices", len(devices))
			devices = filter.FilterDevices(np.celProgram, devices)
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

	var devices []kubeletplugin.Device
	for _, result := range claim.Status.Allocation.Devices.Results {
		requestName := result.Request
		for _, config := range claim.Status.Allocation.Devices.Config {
			if config.Opaque == nil ||
				config.Opaque.Driver != np.driverName ||
				len(config.Requests) > 0 && !slices.Contains(config.Requests, requestName) {
				continue
			}
		}
		device := kubeletplugin.Device{
			Requests:   []string{result.Request},
			PoolName:   result.Pool,
			DeviceName: result.Device,
		}
		devices = append(devices, device)
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
