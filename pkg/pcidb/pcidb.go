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

package pci

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

// file obtained on Dec 13 2024

//go:embed pci.ids.gz
var pciids []byte

var (
	// Vendor entries start with a 4-digit hexadecimal vendor ID,
	// followed by one or more spaces, and the name of the vendor
	// extending to the end of the line.
	reVendor = regexp.MustCompile(`(^[a-f0-9]{4})\s+(.*)$`)
	// Each device entry consists of a single TAB character, a 4-digit hexadecimal
	// device ID, followed by one or more spaces, and the name of the
	// device extending to the end of the line.
	reDevice = regexp.MustCompile(`^\t([a-f0-9]{4})\s+(.*)$`)
	// Subsystem entries are placed below the device entry. They start
	// with two TAB characters, a 4-digit hexadecimal vendor ID (which
	// must be defined elsewhere in the list), a single space, a 4-digit
	// hexadecimal subsystem ID, one or more spaces, and the name of the
	// subsystem extending to the end of the line.
	reSubsystem = regexp.MustCompile(`^\t{2}([a-f0-9]{4})\s([a-f0-9]{4})\s+(.*)$`)
)

// Entry for the OCI ID database
// https://man7.org/linux/man-pages/man5/pci.ids.5.html
type Entry struct {
	Vendor    string
	Device    string
	Subsystem string
}

// getPCI iterates over the file until it finds the associated entry
// and returns the names it finds.
// Expect values in hexadecimal format without the 0x prefix
// Vendor: 025e  --> Solidigm
// Device: 0b60  --> NVMe DC SSD [Sentinel Rock Plus controller]
// SubVendor: 025e , SubDevice: 8008  --> NVMe DC SSD U.2 15mm [D7-P5510]
func GetDevice(vendor, device, subvendor, subdevice string) (*Entry, error) {
	// we require at least a vendor
	if len(vendor) != 4 {
		return nil, fmt.Errorf("vendor ID must be 4-digit hexadecimal")
	}

	if len(device) > 0 && len(device) != 4 {
		return nil, fmt.Errorf("device ID must be 4-digit hexadecimal")
	}

	if len(device) == 0 &&
		(len(subvendor) > 0 || len(subdevice) > 0) {
		return nil, fmt.Errorf("device ID must be set if subvendor or subdevice are specified")
	}

	if len(subdevice) != len(subvendor) {
		return nil, fmt.Errorf("both subvendor and subdevice must be specified if one is specified")
	}

	if len(subvendor) > 0 && len(subvendor) != 4 {
		return nil, fmt.Errorf("subvendor ID must be 4-digit hexadecimal")
	}

	if len(subdevice) > 0 && len(subdevice) != 4 {
		return nil, fmt.Errorf("subdevice ID must be 4-digit hexadecimal")
	}

	gzReader, err := gzip.NewReader(bytes.NewReader(pciids))
	if err != nil {
		return nil, err
	}
	defer gzReader.Close()

	entry := &Entry{}
	scanner := bufio.NewScanner(gzReader)
	// # Syntax:
	// # vendor  vendor_name
	// #   device  device_name				<-- single tab
	// #     subvendor subdevice  subsystem_name	<-- two tabs
	for scanner.Scan() {
		line := scanner.Text()
		// find first the vendor
		if entry.Vendor == "" {
			matches := reVendor.FindStringSubmatch(line)
			if len(matches) != 3 {
				continue
			}
			if matches[1] != strings.ToLower(vendor) {
				continue
			}
			entry.Vendor = matches[2]
			continue
		}
		// finish if we need only the vendor
		if len(device) == 0 {
			return entry, nil
		}
		// find the device
		if entry.Device == "" {
			matches := reDevice.FindStringSubmatch(line)
			if len(matches) != 3 {
				continue
			}
			if matches[1] != strings.ToLower(device) {
				continue
			}
			entry.Device = matches[2]
			continue
		}
		// finish if we need only the vendor and the device
		if len(subdevice) == 0 && len(subvendor) == 0 {
			return entry, nil
		}
		// finally find the subsystem
		if entry.Subsystem == "" {
			matches := reSubsystem.FindStringSubmatch(line)
			if len(matches) != 4 {
				continue
			}
			if matches[1] != strings.ToLower(subvendor) {
				continue
			}
			if matches[2] != strings.ToLower(subdevice) {
				continue
			}
			entry.Subsystem = matches[3]
			// nothing else
			return entry, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return entry, err
	}
	return entry, fmt.Errorf("entry not found")
}
