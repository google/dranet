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
	"time"

	"github.com/containerd/nri/pkg/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	resourceapply "k8s.io/client-go/applyconfigurations/resource/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/set"
)

// NRI hooks into the container runtime, the lifecycle of the Pod seen here is local to the runtime
// and is not the same as the Pod lifecycle for kubernetes, per example, a Pod that can fail to start
// is retried locally multiple times, so the hooks need to be idempotent to all operations on the Pod.
// The NRI hooks are time sensitive, any slow operation needs to be added on the DRA hooks and only
// the information necessary should passed to the NRI hooks via the np.podConfigStore so it can be executed
// quickly.

func (np *NetworkDriver) Synchronize(_ context.Context, pods []*api.PodSandbox, containers []*api.Container) ([]*api.ContainerUpdate, error) {
	klog.Infof("Synchronizing state with the runtime (%d pods, %d containers)...", len(pods), len(containers))

	for _, pod := range pods {
		klog.Infof("Synchronize Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
		netNs := getNetworkNamespace(pod)
		klog.V(2).Infof("pod %s/%s: namespace=%s ips=%v", pod.GetNamespace(), pod.GetName(), netNs, pod.GetIps())
		// get the pod network namespace
		// host network pods are skipped
		if netNs != "" {
			// store the Pod metadata in the db
			np.netdb.AddPodNetns(podKey(pod), netNs)
		}
	}

	klog.Info("NRI plugin finished synchronizing.")
	np.nriSyncOnce.Do(func() {
		close(np.nriSyncCh)
	})

	return nil, nil
}

// CreateContainer handles container creation requests.
func (np *NetworkDriver) CreateContainer(_ context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	klog.V(2).Infof("CreateContainer Pod %s/%s UID %s Container %s", pod.Namespace, pod.Name, pod.Uid, ctr.Name)
	podConfig, ok := np.podConfigStore.GetPodConfigs(types.UID(pod.GetUid()))
	if !ok {
		return nil, nil, nil
	}
	// Containers only cares about the RDMA char devices
	devPaths := set.Set[string]{}
	adjust := &api.ContainerAdjustment{}
	for _, config := range podConfig {
		for _, dev := range config.RDMADevice.DevChars {
			// do not insert the same path multiple times
			if devPaths.Has(dev.Path) {
				continue
			}
			devPaths.Insert(dev.Path)
			// TODO check the file permissions and uid and gid fields
			adjust.AddDevice(&api.LinuxDevice{
				Path:  dev.Path,
				Type:  dev.Type,
				Major: dev.Major,
				Minor: dev.Minor,
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
	// get the devices associated to this Pod
	podConfig, ok := np.podConfigStore.GetPodConfigs(types.UID(pod.GetUid()))
	if !ok {
		return nil
	}
	// get the pod network namespace
	ns := getNetworkNamespace(pod)
	// host network pods can not allocate network devices because it impact the host
	if ns == "" {
		return fmt.Errorf("RunPodSandbox pod %s/%s using host network can not claim host devices", pod.Namespace, pod.Name)
	}
	// store the Pod metadata in the db
	np.netdb.AddPodNetns(podKey(pod), ns)

	// Track all the status updates needed for the resource claims of the pod.
	statusUpdates := map[types.NamespacedName]*resourceapply.ResourceClaimStatusApplyConfiguration{}
	// Process the configurations of the ResourceClaim
	for deviceName, config := range podConfig {
		klog.V(4).Infof("RunPodSandbox processing device: %s with config: %#v", deviceName, config)
		resourceClaim := types.NamespacedName{Name: config.Claim.Name, Namespace: config.Claim.Namespace}
		resourceClaimStatus := statusUpdates[resourceClaim]
		if statusUpdates[resourceClaim] == nil {
			resourceClaimStatus = resourceapply.ResourceClaimStatus()
			statusUpdates[resourceClaim] = resourceClaimStatus
		}
		// resourceClaim status for this specific device
		resourceClaimStatusDevice := resourceapply.
			AllocatedDeviceStatus().
			WithDevice(deviceName).
			WithDriver(np.driverName).
			WithPool(np.nodeName)

		ifName, ok := np.netdb.GetInterfaceName(deviceName)
		if !ok {
			return fmt.Errorf("failed to get interface name for device %s", deviceName)
		}

		klog.V(2).Infof("RunPodSandbox processing Network device: %s", ifName)
		// TODO config options to rename the device and pass parameters
		// use https://github.com/opencontainers/runtime-spec/pull/1271
		networkData, err := nsAttachNetdev(ifName, ns, config.Network.Interface)
		if err != nil {
			err = fmt.Errorf("error moving network device %s (interface %s) to namespace %s: %w", deviceName, ifName, ns, err)
			klog.Infof("RunPodSandbox: %v", err)
			return err
		}

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
		) // End of WithNetworkData

		// The interface name inside the container's namespace.
		ifNameInNs := networkData.InterfaceName

		// Apply Ethtool configurations
		if config.Network.Ethtool != nil {
			err = applyEthtoolConfig(ns, ifNameInNs, config.Network.Ethtool)
			if err != nil {
				klog.Infof("RunPodSandbox error applying ethtool config for %s in ns %s: %v", ifNameInNs, ns, err)
				return fmt.Errorf("error applying ethtool config for %s in ns %s: %v", ifNameInNs, ns, err)
			}
		}

		// Check if the ebpf programs should be disabled
		if config.Network.Interface.DisableEBPFPrograms != nil &&
			*config.Network.Interface.DisableEBPFPrograms {
			err := detachEBPFPrograms(ns, ifNameInNs)
			if err != nil {
				klog.Infof("error disabling ebpf programs for %s in ns %s: %v", ifNameInNs, ns, err)
				return fmt.Errorf("error disabling ebpf programs for %s in ns %s: %v", ifNameInNs, ns, err)
			}
		}

		// Configure routes
		err = applyRoutingConfig(ns, ifNameInNs, config.Network.Routes)
		if err != nil {
			klog.Infof("RunPodSandbox error configuring device %s namespace %s routing: %v", deviceName, ns, err)
			return fmt.Errorf("error configuring device %s routes on namespace %s: %v", deviceName, ns, err)
		}
		resourceClaimStatusDevice.WithConditions(
			metav1apply.Condition().
				WithType("NetworkReady").
				WithStatus(metav1.ConditionTrue).
				WithReason("NetworkReady").
				WithLastTransitionTime(metav1.Now()),
		)

		// Move the RDMA device to the namespace if the host is in exclusive mode
		if !np.rdmaSharedMode && config.RDMADevice.LinkDev != "" {
			klog.V(2).Infof("RunPodSandbox processing RDMA device: %s", config.RDMADevice.LinkDev)
			err := nsAttachRdmadev(config.RDMADevice.LinkDev, ns)
			if err != nil {
				klog.Infof("RunPodSandbox error getting RDMA device %s to namespace %s: %v", config.RDMADevice.LinkDev, ns, err)
				return fmt.Errorf("error moving RDMA device %s to namespace %s: %v", config.RDMADevice.LinkDev, ns, err)
			}
			resourceClaimStatusDevice.WithConditions(
				metav1apply.Condition().
					WithType("RDMALinkReady").
					WithStatus(metav1.ConditionTrue).
					WithReason("RDMALinkReady").
					WithLastTransitionTime(metav1.Now()),
			)
		}
		// Ok
		resourceClaimStatus.WithDevices(resourceClaimStatusDevice)
	}
	// do not block the handler to update the status
	for claim, status := range statusUpdates {
		resourceClaimApply := resourceapply.ResourceClaim(claim.Name, claim.Namespace).WithStatus(status)
		go func() {
			ctxStatus, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := np.kubeClient.ResourceV1().ResourceClaims(claim.Namespace).ApplyStatus(ctxStatus,
				resourceClaimApply,
				metav1.ApplyOptions{FieldManager: np.driverName, Force: true},
			)
			if err != nil {
				klog.Infof("failed to update status for claim %s/%s : %v", claim.Namespace, claim.Name, err)
			} else {
				klog.V(4).Infof("updated status for claim %s/%s", claim.Namespace, claim.Name)
			}
		}()
	}

	return nil
}

// StopPodSandbox tries to move back the devices to the rootnamespace but does not fail
// to avoid disrupting the pod shutdown. The kernel will do the cleanup once the namespace
// is deleted.
func (np *NetworkDriver) StopPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("StopPodSandbox Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
	start := time.Now()
	defer func() {
		np.netdb.RemovePodNetns(podKey(pod))
		klog.V(2).Infof("StopPodSandbox Pod %s/%s UID %s took %v", pod.Namespace, pod.Name, pod.Uid, time.Since(start))
	}()

	// get the devices associated to this Pod
	podConfig, ok := np.podConfigStore.GetPodConfigs(types.UID(pod.GetUid()))
	if !ok {
		return nil
	}
	// get the pod network namespace
	ns := getNetworkNamespace(pod)
	if ns == "" {
		// some version of containerd does not send the network namespace information on this hook so
		// we workaround it using the local copy we have in the db to associate interfaces with Pods via
		// the network namespace id.
		ns = np.netdb.GetPodNetNamespace(podKey(pod))
		if ns == "" {
			klog.Infof("StopPodSandbox pod %s/%s using host network ... skipping", pod.Namespace, pod.Name)
			return nil
		}
	}

	for deviceName, config := range podConfig {
		ifName, ok := np.netdb.GetInterfaceName(deviceName)
		if !ok {
			klog.Infof("Failed to get interface name for device %q, expect kernel to do the cleanup", deviceName)
			continue
		}

		if err := nsDetachNetdev(ns, config.Network.Interface.Name, ifName); err != nil {
			klog.Infof("fail to return network device %s : %v", deviceName, err)
		}

		if !np.rdmaSharedMode && config.RDMADevice.LinkDev != "" {
			if err := nsDetachRdmadev(ns, config.RDMADevice.LinkDev); err != nil {
				klog.Infof("fail to return rdma device %s : %v", deviceName, err)
			}
		}
	}
	return nil
}

func (np *NetworkDriver) RemovePodSandbox(_ context.Context, pod *api.PodSandbox) error {
	klog.V(2).Infof("RemovePodSandbox Pod %s/%s UID %s", pod.Namespace, pod.Name, pod.Uid)
	np.netdb.RemovePodNetns(podKey(pod))
	return nil
}

func (np *NetworkDriver) Shutdown(_ context.Context) {
	klog.Info("Runtime shutting down...")
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
