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
	"regexp"
	"strconv"
	"strings"

	"cloud.google.com/go/compute/metadata"

	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/google/dranet/pkg/cloudprovider"
	"github.com/google/dranet/pkg/cloudprovider/gce"
	"github.com/vishvananda/netlink"
	resourceapi "k8s.io/api/resource/v1beta1"
)

var (
	// gceGpuNicRegex is used to parse the GPU index from GCE NICs that follow the
	// gpu<index>rdma<index> naming convention.
	// Ref: https://github.com/GoogleCloudPlatform/guest-configs/pull/84
	gceGpuNicRegex = regexp.MustCompile(`^gpu(\d+)rdma`)
)

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

// getProviderAttributes retrieves cloud provider-specific attributes for a network interface
func getProviderAttributes(link netlink.Link, instance *cloudprovider.CloudInstance) map[resourceapi.QualifiedName]resourceapi.DeviceAttribute {
	if instance == nil {
		klog.Warningf("instance metadata is nil, cannot get provider attributes.")
		return nil
	}
	if instance.Provider != cloudprovider.CloudProviderGCE {
		klog.Warningf("cloud provider %q is not supported", instance.Provider)
		return nil
	}

	ifName := link.Attrs().Name
	mac := link.Attrs().HardwareAddr.String()

	var attributes map[resourceapi.QualifiedName]resourceapi.DeviceAttribute
	foundMac := false
	for _, cloudInterface := range instance.Interfaces {
		if cloudInterface.Mac == mac {
			attributes = gce.GetGCEAttributes(cloudInterface.Network, instance.Topology)
			foundMac = true
			break
		}
	}

	// GCE uses a naming convention for NICs associated with GPUs.
	// The format is gpu<GPU Index>rdma<RDMA NIC Index>.
	// We can extract the GPU index and expose it as an attribute for selection.
	matches := gceGpuNicRegex.FindStringSubmatch(ifName)
	if len(matches) == 2 {
		if gpuIndex, err := strconv.ParseInt(matches[1], 10, 32); err == nil {
			if attributes == nil {
				attributes = make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute)
			}
			// This is required for compatibility with the nvidia-gpu-dra-driver so that
			// NICs can be selected based on the same index as the GPUs.
			// TODO: migrate to resource.kubernetes.io/pcieRoot once is widely implemented.
			attributes["index"] = resourceapi.DeviceAttribute{IntValue: ptr.To(gpuIndex)}
		}
	}

	if !foundMac && len(attributes) == 0 {
		klog.Warningf("no matching cloud interface found for mac %s", mac)
	}

	if len(attributes) == 0 {
		return nil
	}
	return attributes
}

// getLastSegmentAndTruncate extracts the last segment from a path
// and truncates it to the specified maximum length.
func getLastSegmentAndTruncate(s string, maxLength int) string {
	segments := strings.Split(s, "/")
	if len(segments) == 0 {
		// This condition is technically unreachable because strings.Split always returns a slice with at least one element.
		// For an empty input string, segments will be []string{""}.
		return ""
	}
	lastSegment := segments[len(segments)-1]
	if len(lastSegment) > maxLength {
		return lastSegment[:maxLength]
	}
	return lastSegment
}
