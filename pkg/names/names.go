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
	// NormalizedInterfacePrefix is prefix used when normalizing a network
	// interface.
	NormalizedInterfacePrefix = "net"
	// NormalizedPCIPrefix is the prefix used when normalizing a PCI Address.
	NormalizedPCIPrefix = "pci"
)

// NormalizeInterfaceName determines the appropriate name for an interface in
// Kubernetes. If the original interface name (ifName) is already a valid
// DNS-1123 label, it's returned as is. Otherwise, it's encoded using Base32,
// prefixed with NormalizedPrefix, and returned.
//
// Linux interface names (often limited by IFNAMSIZ, typically 16) plus the
// base32 encoding and the normalized prefix (11) are within the DNS-1123 label,
// which has a maximum length of 63.
func NormalizeInterfaceName(ifName string) string {
	if ifName == "" {
		return ""
	}
	if len(validation.IsDNS1123Label(ifName)) == 0 {
		return ifName
	}

	klog.V(4).Infof("Interface name '%s' is not DNS-1123 compliant, normalizing.", ifName)
	encodedPayload := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(ifName))
	normalizedName := NormalizedInterfacePrefix + strings.ToLower(encodedPayload)

	return normalizedName
}

// NormalizePCIAddress takes a PCI address and converts it into a DNS-1123
// acceptable format.
func NormalizePCIAddress(pciAddress string) string {
	// Replace ":" and "." with "-" to make it DNS-1123 compliant.
	// A PCI address like "0000:8a:00.0" becomes "0000-8a-00-0".
	r := strings.NewReplacer(":", "-", ".", "-")
	return NormalizedPCIPrefix + "-" + r.Replace(pciAddress)
}
