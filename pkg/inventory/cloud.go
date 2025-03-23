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

package inventory

import (
	"context"

	"cloud.google.com/go/compute/metadata"

	"k8s.io/klog/v2"

	"github.com/google/dranet/pkg/cloudprovider"
	"github.com/google/dranet/pkg/cloudprovider/gce"
)

var (
	// cloud provider specific

	// https://cloud.google.com/compute/docs/accelerator-optimized-machines#network-protocol
	// machine types have a one to one mapping to a network protocol in google cloud
	gceNetworkProtocolMap = map[string]string{
		"a3-highgpu-1g":  "GPUDirect-TCPX",
		"a3-highgpu-2g":  "GPUDirect-TCPX",
		"a3-highgpu-4g":  "GPUDirect-TCPX",
		"a3-highgpu-8g":  "GPUDirect-TCPX",
		"a3-edgegpu-8g":  "GPUDirect-TCPX",
		"a3-megagpu-8g":  "GPUDirect-TCPXO",
		"a3-ultragpu-8g": "GPUDirect-RDMA",
		"a4-highgpu-8g":  "GPUDirect-RDMA",
	}
)

type cloudInstance struct {
	Name                string
	Type                string
	AcceleratorProtocol string
	Interfaces          []networkInterface
}

type networkInterface struct {
	IPv4    string   `json:"ip,omitempty"`
	IPv6    []string `json:"ipv6,omitempty"`
	Mac     string   `json:"mac,omitempty"`
	MTU     int      `json:"mtu,omitempty"`
	Network string   `json:"network,omitempty"`
}

// getInstanceProperties get the instace properties and stores them in a global variable to be used in discovery
// TODO(aojea) support more cloud providers
func getInstanceProperties(ctx context.Context) *cloudprovider.CloudInstance {
	var err error
	var instance *cloudprovider.CloudInstance
	if metadata.OnGCE() {
		// Get google compute instance metadata for network interfaces
		// https://cloud.google.com/compute/docs/metadata/predefined-metadata-keys
		klog.Infof("running on GCE")
		instance, err = gce.GetInstance(ctx)
	}
	if err != nil {
		klog.Infof("could not get instance properties: %v", err)
		return nil
	}
	return instance
}

func cloudNetwork(mac string, instance *cloudprovider.CloudInstance) string {
	if instance == nil {
		return ""
	}
	for _, cloudInterface := range instance.Interfaces {
		if cloudInterface.Mac == mac {
			return cloudInterface.Network
		}
	}
	return ""
}
