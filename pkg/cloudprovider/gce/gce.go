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
	"time"

	"cloud.google.com/go/compute/metadata"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/google/dranet/pkg/cloudprovider"
)

var (
	// https://cloud.google.com/compute/docs/accelerator-optimized-machines#network-protocol
	// machine types have a one to one mapping to a network protocol in google cloud
	NetworkProtocolMap = map[string]string{
		"a3-highgpu-1g": "GPUDirect-TCPX",
		"a3-highgpu-2g": "GPUDirect-TCPX",
		"a3-highgpu-4g": "GPUDirect-TCPX",
		"a3-highgpu-8g": "GPUDirect-TCPX",
		"a3-edgegpu-8g": "GPUDirect-TCPX",
		"a3-megagpu-8g": "GPUDirect-TCPXO",
	}
)

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
		instance = &cloudprovider.CloudInstance{
			Name:                instanceName,
			Type:                instanceType,
			AcceleratorProtocol: NetworkProtocolMap[instanceType],
		}
		if err = json.Unmarshal([]byte(gceInterfacesRaw), &instance.Interfaces); err != nil {
			klog.Infof("could not get network interfaces on GCE ... retrying: %v", err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return instance, nil
}
