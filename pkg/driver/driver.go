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
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/dranet/pkg/apis"
	"github.com/google/dranet/pkg/inventory"

	"github.com/containerd/nri/pkg/stub"
	"github.com/vishvananda/netlink"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"
	registerapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

const (
	kubeletPluginRegistryPath = "/var/lib/kubelet/plugins_registry"
	kubeletPluginPath         = "/var/lib/kubelet/plugins"
)

const (
	// maxAttempts indicates the number of times the driver will try to recover itself before failing
	maxAttempts = 5
)

// This interface is our internal contract for the behavior we need from a *kubeletplugin.Helper, created specifically so we can fake it in tests.
type pluginHelper interface {
	PublishResources(context.Context, resourceslice.DriverResources) error
	Stop()
	RegistrationStatus() *registerapi.RegistrationStatus
}

// This interface is our internal contract for the behavior we need from a *inventory.DB, created specifically so we can fake it in tests.
type inventoryDB interface {
	Run(context.Context) error
	GetResources(context.Context) <-chan []resourceapi.Device
	GetNetInterfaceName(string) (string, error)
	AddPodNetNs(podKey string, netNs string)
	RemovePodNetNs(podKey string)
	GetPodNetNs(podKey string) (netNs string)
}

// WithFilter
func WithFilter(filter cel.Program) Option {
	return func(o *NetworkDriver) {
		o.celProgram = filter
	}
}

// WithInventory sets the inventory database for the driver.
func WithInventory(db inventoryDB) Option {
	return func(o *NetworkDriver) {
		o.netdb = db
	}
}

type NetworkDriver struct {
	driverName string
	nodeName   string
	kubeClient kubernetes.Interface
	draPlugin  pluginHelper
	nriPlugin  stub.Stub

	// contains the host interfaces
	netdb      inventoryDB
	celProgram cel.Program

	// Cache the rdma shared mode state
	rdmaSharedMode bool
	podConfigStore *PodConfigStore
}

type Option func(*NetworkDriver)

func Start(ctx context.Context, driverName string, kubeClient kubernetes.Interface, nodeName string, opts ...Option) (*NetworkDriver, error) {
	registerMetrics()

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
	if plugin.netdb == nil {
		plugin.netdb = inventory.New()
	}
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
	// Stop NRI Plugin (it's expected that it returns when fully stopped).
	np.nriPlugin.Stop()
	// Stop DRA Plugin (returns only after it has fully stopped).
	np.draPlugin.Stop()
}
