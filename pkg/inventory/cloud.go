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
	"strings"

	"cloud.google.com/go/compute/metadata"

	"k8s.io/klog/v2"

	"github.com/google/dranet/pkg/cloudprovider"
	"github.com/google/dranet/pkg/cloudprovider/gce"
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

func cloudNetwork(mac string, instance *cloudprovider.CloudInstance) string {
	if instance == nil {
		return ""
	}
	for _, cloudInterface := range instance.Interfaces {
		if cloudInterface.Mac == mac {
			return getLastSegmentAndTruncate(cloudInterface.Network, 64) // max size for an attribute value
		}
	}
	return ""
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
