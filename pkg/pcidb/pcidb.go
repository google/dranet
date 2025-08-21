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

package pcidb

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/dranet/third_party"
	"k8s.io/klog/v2"
)

var (
	pcidb = third_party.PCIDBGZ
)

func Setup() error {
	if value, exists := os.LookupEnv("PCIDB_PATH"); exists {
		// If an explicit path has been configured for PCI DB, use that and
		// don't extract the embedded db.
		klog.Infof("Using pre-configured value for PCIDB_PATH=%q", value)
		return nil
	}

	// PCIDB_PATH was not set, which means we should attempt to use the embedded
	// file as the db source.
	tempDir, err := os.MkdirTemp("", "pcidb")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory for PCI DB: %v", err)
	}
	filePath := filepath.Join(tempDir, "pci.ids.gz")
	err = os.WriteFile(filePath, pcidb, 0644)
	if err != nil {
		return fmt.Errorf("failed to write pci.ids.gz file: %v", err)
	}

	if err := os.Setenv("PCIDB_PATH", filePath); err != nil {
		return fmt.Errorf("failed to set PCIDB_PATH environment variable: %v", err)
	}
	klog.Infof("Successfuly set value of PCIDB_PATH=%q", filePath)
	return nil
}
