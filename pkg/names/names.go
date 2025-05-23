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

package names

import (
	"encoding/base32"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/klog/v2"
)

const (
	// NormalizedPrefix is added to device names that had to be encoded
	// because their original interface name was not DNS-1123 compliant.
	NormalizedPrefix = "normalized-"
)

// SetDeviceName determines the appropriate name for a device in Kubernetes.
// If the original interface name (ifName) is already a valid DNS-1123 label,
// it's returned as is. Otherwise, it's encoded using Base32, prefixed with
// NormalizedPrefix, and returned.
// Linux interface names (often limited by IFNAMSIZ, typically 16) plus the
// base32 encoding and the normalized prefix (11) are within the DNS-1123 label,
// which has a maximum length of 63.
func SetDeviceName(ifName string) string {
	if ifName == "" {
		return ""
	}
	if len(validation.IsDNS1123Label(ifName)) == 0 {
		return ifName
	}

	klog.V(4).Infof("Interface name '%s' is not DNS-1123 compliant, normalizing.", ifName)
	encodedPayload := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(ifName))
	normalizedName := NormalizedPrefix + strings.ToLower(encodedPayload)

	return normalizedName
}

// GetOriginalName retrieves the original interface name from a deviceName.
// If deviceName was prefixed with NormalizedPrefix (indicating it was encoded),
// it decodes the name. Otherwise, it assumes deviceName is the original name.
func GetOriginalName(deviceName string) string {
	if strings.HasPrefix(deviceName, NormalizedPrefix) {
		encodedPart := strings.TrimPrefix(deviceName, NormalizedPrefix)
		encodedPart = strings.ToUpper(encodedPart) // base32 uses uppercase only
		decodedBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(encodedPart)
		if err != nil {
			klog.Warningf("Failed to decode Base32 device name payload '%s' from full name '%s': %v. Returning the full deviceName as fallback.",
				encodedPart, deviceName, err)
			return deviceName
		}
		return string(decodedBytes)
	}
	return deviceName
}
