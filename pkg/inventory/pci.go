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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jaypipes/ghw"
	"k8s.io/klog/v2"
)

const (
	pciClassNetwork = "02"
	// The digit 1 indicates the first versioned naming scheme, allowing
	// different future naming schemes.
	normalizedNamePrefix = "net1"
)

func isNetworkDevice(dev *ghw.PCIDevice) bool {
	return strings.HasPrefix(dev.Class.ID, pciClassNetwork)
}

// GetNetworkInterface returns the network interface name for a given PCI address.
func GetNetworkInterface(pciAddress string) (string, error) {
	pciPath := filepath.Join(sysBusPciDevicesPath, pciAddress, "net")
	if _, err := os.Stat(pciPath); os.IsNotExist(err) {
		return "", fmt.Errorf("no net directory for pci device %s", pciAddress)
	}
	files, err := os.ReadDir(pciPath)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no interface found for pci device %s", pciAddress)
	}
	klog.V(3).Infof("found interface %s for pci device %s", files[0].Name(), pciAddress)
	return files[0].Name(), nil
}

// NormalizePCIAddress takes a PCI address and converts it into a DNS-1123
// acceptable format.
func NormalizePCIAddress(pciAddress string) string {
	if pciAddress == "" {
		return ""
	}
	// Replace ":" and "." with "-" to make it DNS-1123 compliant.
	// A PCI address like "0000:8a:00.0" becomes "0000-8a-00-0".
	r := strings.NewReplacer(":", "-", ".", "-")
	return normalizedNamePrefix + "-" + r.Replace(pciAddress)
}

// DeNormalizePCIAddress takes a normalized PCI address and converts it back to
// a standard PCI address format.
func DeNormalizePCIAddress(normalizedAddress string) string {
	if normalizedAddress == "" {
		return ""
	}
	if !strings.HasPrefix(normalizedAddress, normalizedNamePrefix+"-") {
		klog.Errorf("invalid normalized PCI address format: missing '%v-' prefix: %s", normalizedNamePrefix, normalizedAddress)
		return ""
	}
	pciAddress := strings.TrimPrefix(normalizedAddress, normalizedNamePrefix+"-")
	parts := strings.Split(pciAddress, "-")
	if len(parts) != 4 {
		klog.Errorf("invalid normalized PCI address format: expected 4 parts, got %d for %s", len(parts), normalizedAddress)
		return ""
	}
	return fmt.Sprintf("%s:%s:%s.%s", parts[0], parts[1], parts[2], parts[3])
}
