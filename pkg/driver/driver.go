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
	"sync"
	"time"

	"github.com/Mellanox/rdmamap"
	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/google/dranet/pkg/inventory"

	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"
	drapb "k8s.io/kubelet/pkg/apis/dra/v1beta1"
)

const (
	kubeletPluginRegistryPath = "/var/lib/kubelet/plugins_registry"
	kubeletPluginPath         = "/var/lib/kubelet/plugins"
)

// storage allows to
type storage struct {
	mu    sync.RWMutex
	cache map[types.UID]resourceapi.AllocationResult
}

func (s *storage) Add(uid types.UID, allocation resourceapi.AllocationResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[uid] = allocation
}

func (s *storage) Get(uid types.UID) (resourceapi.AllocationResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	allocation, ok := s.cache[uid]
	return allocation, ok
}

func (s *storage) Remove(uid types.UID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, uid)
}

var _ drapb.DRAPluginServer = &NetworkDriver{}

type NetworkDriver struct {
	driverName string
	kubeClient kubernetes.Interface
	draPlugin  kubeletplugin.DRAPlugin
	nriPlugin  stub.Stub

	podAllocations   storage
	claimAllocations storage
	netdb            *inventory.DB
}

type Option func(*NetworkDriver)

func Start(ctx context.Context, driverName string, kubeClient kubernetes.Interface, nodeName string, opts ...Option) (*NetworkDriver, error) {
	plugin := &NetworkDriver{
		driverName:       driverName,
		kubeClient:       kubeClient,
		podAllocations:   storage{cache: make(map[types.UID]resourceapi.AllocationResult)},
		claimAllocations: storage{cache: make(map[types.UID]resourceapi.AllocationResult)},
	}

	for _, o := range opts {
		o(plugin)
	}

	// register the DRA driver
	pluginRegistrationPath := filepath.Join(kubeletPluginRegistryPath, driverName+".sock")
	driverPluginPath := filepath.Join(kubeletPluginPath, driverName)
	err := os.MkdirAll(driverPluginPath, 0750)
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin path %s: %v", driverPluginPath, err)
	}
	driverPluginSocketPath := filepath.Join(driverPluginPath, "/plugin.sock")

	kubeletOpts := []kubeletplugin.Option{
		kubeletplugin.DriverName(driverName),
		kubeletplugin.NodeName(nodeName),
		kubeletplugin.KubeClient(kubeClient),
		kubeletplugin.RegistrarSocketPath(pluginRegistrationPath),
		kubeletplugin.PluginSocketPath(driverPluginSocketPath),
		kubeletplugin.KubeletPluginSocketPath(driverPluginSocketPath),
	}
	d, err := kubeletplugin.Start(ctx, []any{plugin}, kubeletOpts...)
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
	allocation, ok := np.podAllocations.Get(types.UID(pod.Uid))
	if !ok {
		klog.V(4).Infof("RunPodSandbox Pod %s/%s does not have an associated claims", pod.Namespace, pod.Name)
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
	for _, result := range allocation.Devices.Results {
		if result.Driver != np.driverName {
			continue
		}

		// Process the configurations of the ResourceClaim
		for _, config := range allocation.Devices.Config {
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
	return nil
}

func (np *NetworkDriver) StopPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("StopPodSandbox pod %s/%s", pod.Namespace, pod.Name)
	defer np.netdb.RemovePodNetns(podKey(pod))
	allocation, ok := np.podAllocations.Get(types.UID(pod.Uid))
	if !ok {
		klog.V(2).Infof("StopPodSandbox pod %s/%s does not have allocations", pod.Namespace, pod.Name)
		return nil
	}
	defer np.podAllocations.Remove(types.UID(pod.Uid))

	// get the pod network namespace
	ns := getNetworkNamespace(pod)
	if ns == "" {
		klog.V(2).Infof("StopPodSandbox pod %s/%s using host network, skipping", pod.Namespace, pod.Name)
		return nil
	}

	for _, config := range allocation.Devices.Config {
		if config.Opaque == nil {
			continue
		}
		klog.V(4).Infof("podStopHook Configuration %s", string(config.Opaque.Parameters.String()))
		// TODO get config options here, it can add ips or commands
		// to add routes, run dhcp, rename the interface ... whatever
	}
	// Process the configurations of the ResourceClaim
	for _, result := range allocation.Devices.Results {
		if result.Driver != np.driverName {
			continue
		}
		klog.V(4).Infof("podStopHook Device %s", result.Device)
		// TODO config options to rename the device and pass parameters
		// use https://github.com/opencontainers/runtime-spec/pull/1271
		err := nsDetachNetdev(ns, result.Device)
		if err != nil {
			klog.Infof("RunPodSandbox error moving device %s to namespace %s: %v", result.Device, ns, err)
			continue
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
			resources := kubeletplugin.Resources{
				Devices: devices,
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

// NodePrepareResources filter the Claim requested for this driver
func (np *NetworkDriver) NodePrepareResources(ctx context.Context, request *drapb.NodePrepareResourcesRequest) (*drapb.NodePrepareResourcesResponse, error) {
	if request == nil {
		return nil, nil
	}
	resp := &drapb.NodePrepareResourcesResponse{
		Claims: make(map[string]*drapb.NodePrepareResourceResponse),
	}

	for _, claimReq := range request.GetClaims() {
		klog.V(2).Infof("NodePrepareResources: Claim Request %s/%s", claimReq.Namespace, claimReq.Name)
		devices, err := np.nodePrepareResource(ctx, claimReq)
		if err != nil {
			resp.Claims[claimReq.UID] = &drapb.NodePrepareResourceResponse{
				Error: err.Error(),
			}
		} else {
			r := &drapb.NodePrepareResourceResponse{}
			for _, device := range devices {
				pbDevice := &drapb.Device{
					PoolName:   device.PoolName,
					DeviceName: device.DeviceName,
				}
				r.Devices = append(r.Devices, pbDevice)
			}
			resp.Claims[claimReq.UID] = r
		}
	}
	return resp, nil
}

// TODO define better what is passed at the podStartHook
// Filter out the allocations not required for this Pod
func (np *NetworkDriver) nodePrepareResource(ctx context.Context, claimReq *drapb.Claim) ([]drapb.Device, error) {
	// The plugin must retrieve the claim itself to get it in the version that it understands.
	claim, err := np.kubeClient.ResourceV1beta1().ResourceClaims(claimReq.Namespace).Get(ctx, claimReq.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("retrieve claim %s/%s: %w", claimReq.Namespace, claimReq.Name, err)
	}
	if claim.Status.Allocation == nil {
		return nil, fmt.Errorf("claim %s/%s not allocated", claimReq.Namespace, claimReq.Name)
	}
	if claim.UID != types.UID(claim.UID) {
		return nil, fmt.Errorf("claim %s/%s got replaced", claimReq.Namespace, claimReq.Name)
	}
	np.claimAllocations.Add(claim.UID, *claim.Status.Allocation)

	for _, reserved := range claim.Status.ReservedFor {
		if reserved.Resource != "pods" || reserved.APIGroup != "" {
			klog.Infof("Driver only supports Pods, unsupported reference %#v", reserved)
			continue
		}
		// TODO define better what is passed at the podStartHook
		np.podAllocations.Add(reserved.UID, *claim.Status.Allocation)
	}

	var devices []drapb.Device
	for _, result := range claim.Status.Allocation.Devices.Results {
		requestName := result.Request
		for _, config := range claim.Status.Allocation.Devices.Config {
			if config.Opaque == nil ||
				config.Opaque.Driver != np.driverName ||
				len(config.Requests) > 0 && !slices.Contains(config.Requests, requestName) {
				continue
			}
		}
		device := drapb.Device{
			PoolName:   result.Pool,
			DeviceName: result.Device,
		}
		devices = append(devices, device)
	}

	return devices, nil
}

func (np *NetworkDriver) NodeUnprepareResources(ctx context.Context, request *drapb.NodeUnprepareResourcesRequest) (*drapb.NodeUnprepareResourcesResponse, error) {
	if request == nil {
		return nil, nil
	}
	resp := &drapb.NodeUnprepareResourcesResponse{
		Claims: make(map[string]*drapb.NodeUnprepareResourceResponse),
	}

	for _, claimReq := range request.Claims {
		err := np.nodeUnprepareResource(ctx, claimReq)
		if err != nil {
			klog.Infof("error unpreparing ressources for claim %s/%s : %v", claimReq.Namespace, claimReq.Name, err)
			resp.Claims[claimReq.UID] = &drapb.NodeUnprepareResourceResponse{
				Error: err.Error(),
			}
		} else {
			resp.Claims[claimReq.UID] = &drapb.NodeUnprepareResourceResponse{}
		}
	}
	return resp, nil
}

func (np *NetworkDriver) nodeUnprepareResource(ctx context.Context, claimReq *drapb.Claim) error {
	allocation, ok := np.claimAllocations.Get(types.UID(claimReq.UID))
	if !ok {
		klog.Infof("claim request does not exist %s/%s %s", claimReq.Namespace, claimReq.Name, claimReq.UID)
		return nil
	}
	defer np.claimAllocations.Remove(types.UID(claimReq.UID))
	klog.Infof("claim %s/%s with allocation %#v", claimReq.Namespace, claimReq.Name, allocation)
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
