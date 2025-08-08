/*
Copyright 2025 Google LLC

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

package gce

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/google/dranet/pkg/cloudprovider"
	resourceapi "k8s.io/api/resource/v1"
)

// GPUDirectSupport represents the type of GPUDirect support for a given machine type.
type GPUDirectSupport string

const (
	GPUDirectTCPX  GPUDirectSupport = "GPUDirect-TCPX"
	GPUDirectTCPXO GPUDirectSupport = "GPUDirect-TCPXO"
	GPUDirectRDMA  GPUDirectSupport = "GPUDirect-RDMA"
)

const (
	GCEAttrPrefix = "gce.dra.net"

	AttrGCEBlock                = GCEAttrPrefix + "/" + "block"
	AttrGCESubblock             = GCEAttrPrefix + "/" + "subblock"
	AttrGCEHost                 = GCEAttrPrefix + "/" + "host"
	AttrGCENetworkName          = GCEAttrPrefix + "/" + "networkName"
	AttrGCENetworkProjectNumber = GCEAttrPrefix + "/" + "networkProjectNumber"
)

var (
	// https://cloud.google.com/compute/docs/accelerator-optimized-machines#network-protocol
	// machine types have a one to one mapping to a network protocol in google cloud
	NetworkProtocolMap = map[string]GPUDirectSupport{
		"a3-highgpu-1g":  GPUDirectTCPX,  // 8 GPU 4 accelerator NICs
		"a3-highgpu-2g":  GPUDirectTCPX,  // "
		"a3-highgpu-4g":  GPUDirectTCPX,  // "
		"a3-highgpu-8g":  GPUDirectTCPX,  // "
		"a3-edgegpu-8g":  GPUDirectTCPX,  // "
		"a3-megagpu-8g":  GPUDirectTCPXO, // 8 GPUs 8 NICs
		"a3-ultragpu-8g": GPUDirectRDMA,  // 8 GPUs 8 NICs
		"a4-highgpu-8g":  GPUDirectRDMA,  // 8 GPUs 8 NICs
	}
	// Network Technology
	// GPUDirect-TCPX: one VPCs for GPU NICs, one subnet per VPC 8244MTU
	// GPUDirect-TCPXO: one VPCs for GPU NICs, one subnet per VPC 8244MTU
	// GPUDirect-RDMA: one HPC VPC, one subnet per NIC, 8896MTU
)

// GetInstance retrieves GCE instance properties by querying the metadata server.
func GetInstance(ctx context.Context) (*cloudprovider.CloudInstance, error) {
	var instance *cloudprovider.CloudInstance
	// metadata server can not be available during startup
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 15*time.Second, true, func(ctx context.Context) (done bool, err error) {
		instanceName, err := metadata.InstanceNameWithContext(ctx)
		if err != nil {
			klog.Infof("could not get instance name on GCE ... retrying: %v", err)
			return false, nil
		}

		instanceType, err := metadata.GetWithContext(ctx, "instance/machine-type")
		if err != nil {
			klog.Infof("could not get instance type on VM %s GCE ... retrying: %v", instanceName, err)
			return false, nil
		}

		//  curl "http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/?recursive=true" -H "Metadata-Flavor: Google"
		// [{"accessConfigs":[{"externalIp":"35.225.164.134","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"10.128.0.1","ip":"10.128.0.70","ipAliases":["10.24.3.0/24"],"mac":"42:01:0a:80:00:46","mtu":1460,"network":"projects/628944397724/networks/default","subnetmask":"255.255.240.0","targetInstanceIps":[]},{"accessConfigs":[{"externalIp":"","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"192.168.1.1","ip":"192.168.1.2","ipAliases":[],"mac":"42:01:c0:a8:01:02","mtu":8244,"network":"projects/628944397724/networks/aojea-dra-net-1","subnetmask":"255.255.255.0","targetInstanceIps":[]},{"accessConfigs":[{"externalIp":"","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"192.168.2.1","ip":"192.168.2.2","ipAliases":[],"mac":"42:01:c0:a8:02:02","mtu":8244,"network":"projects/628944397724/networks/aojea-dra-net-2","subnetmask":"255.255.255.0","targetInstanceIps":[]},{"accessConfigs":[{"externalIp":"","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"192.168.3.1","ip":"192.168.3.2","ipAliases":[],"mac":"42:01:c0:a8:03:02","mtu":8244,"network":"projects/628944397724/networks/aojea-dra-net-3","subnetmask":"255.255.255.0","targetInstanceIps":[]},{"accessConfigs":[{"externalIp":"","type":"ONE_TO_ONE_NAT"}],"dnsServers":["169.254.169.254"],"forwardedIps":[],"gateway":"192.168.4.1","ip":"192.168.4.2","ipAliases":[],"mac":"42:01:c0:a8:04:02","mtu":8244,"network":"projects/628944397724/networks/aojea-dra-net-4","subnetmask":"255.255.255.0","targetInstanceIps":[]}]
		gceInterfacesRaw, err := metadata.GetWithContext(ctx, "instance/network-interfaces/?recursive=true&alt=json")
		if err != nil {
			klog.Infof("could not get network interfaces on GCE ... retrying: %v", err)
			return false, nil
		}
		protocol := NetworkProtocolMap[instanceType]
		instance = &cloudprovider.CloudInstance{
			Name:                instanceName,
			Type:                instanceType,
			Provider:            cloudprovider.CloudProviderGCE,
			AcceleratorProtocol: string(protocol),
		}
		if err = json.Unmarshal([]byte(gceInterfacesRaw), &instance.Interfaces); err != nil {
			klog.Infof("could not get network interfaces on GCE ... retrying: %v", err)
			return false, nil
		}
		// Physical location of VM is not always available. We don't fail if
		// it's not available.
		//
		// Ref. https://cloud.google.com/compute/docs/instances/use-compact-placement-policies#verify-vm-location
		gceTopologyAttributes, err := metadata.GetWithContext(ctx, "instance/attributes/physical_host")
		if err != nil {
			klog.Warningf("Failed to retrieve physical host for GCE VM %q, this maybe normal since not all VMs and VM types have this populated: %v", instanceName, err)
		} else {
			instance.Topology = gceTopologyAttributes
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return instance, nil
}

// GetGCEAttributes fetches all attributes related to the provided GCP network.
func GetGCEAttributes(network, topology string) map[resourceapi.QualifiedName]resourceapi.DeviceAttribute {
	attributes := make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute)
	var projectNumber int64
	var name string
	// Use custom parsing because the network path is
	// different from the format expected by k8s-cloud-provider
	_, err := fmt.Sscanf(network, "projects/%d/networks/%s", &projectNumber, &name)
	if err != nil {
		klog.Warningf("Error parsing network %q : %v", network, err)
		return nil
	}
	topologyParts := strings.SplitN(strings.TrimPrefix(topology, "/"), "/", 3)
	// topology may not be always available
	if len(topologyParts) == 3 {
		attributes["gce.dra.net/block"] = resourceapi.DeviceAttribute{StringValue: &topologyParts[0]}
		attributes["gce.dra.net/subblock"] = resourceapi.DeviceAttribute{StringValue: &topologyParts[1]}
		attributes["gce.dra.net/host"] = resourceapi.DeviceAttribute{StringValue: &topologyParts[2]}
	} else {
		klog.Warningf("Error parsing host topology, may be unsupported on VM %q : %v", topology, err)
	}
	attributes["gce.dra.net/networkName"] = resourceapi.DeviceAttribute{StringValue: &name}
	attributes["gce.dra.net/networkProjectNumber"] = resourceapi.DeviceAttribute{IntValue: &projectNumber}
	klog.Info(attributes)
	return attributes
}
