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

package gke

import (
	"github.com/spf13/cobra"
)

var GkeCmd = &cobra.Command{
	Use:   "gke",
	Short: "Manage resources on Google Kubernetes Engine (GKE)",
	Long:  `This command allows you to manage resources on GKE.`,
}

// nodepoolCmd represents the nodepool command
var nodepoolCmd = &cobra.Command{
	Use:   "nodepool",
	Short: "Manage nodepools on GKE",
	Long:  `Allows creating, validating, and managing nodepools on GKE.`,
}

// ... (createnodepoolCmd and validatenodepoolCmd remain largely the same, but adapt the package)
var createnodepoolCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new nodepool",
	Run: func(cmd *cobra.Command, args []string) {
		// ... (rest of the code)
	},
}

var validatenodepoolCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate an existing nodepool",
	Run: func(cmd *cobra.Command, args []string) {
		// ... (rest of the code)
	},
}

func init() {
	GkeCmd.AddCommand(nodepoolCmd)

	nodepoolCmd.AddCommand(createnodepoolCmd)
	nodepoolCmd.AddCommand(validatenodepoolCmd)

	createnodepoolCmd.Flags().String("project", "", "GCP project ID")
	createnodepoolCmd.Flags().String("location", "", "GCP location (e.g., us-central1)")
	createnodepoolCmd.Flags().String("cluster", "", "GKE cluster name")
	createnodepoolCmd.Flags().String("nodepool", "", "Nodepool name")
	createnodepoolCmd.Flags().String("machine-type", "", "Machine type for nodes")
	createnodepoolCmd.Flags().Int64("node-count", 0, "Number of nodes")
	createnodepoolCmd.Flags().Bool("run-nccl", false, "Run nccl tests after dranet installation")

	validatenodepoolCmd.Flags().String("project", "", "GCP project ID")
	validatenodepoolCmd.Flags().String("location", "", "GCP location (e.g., us-central1)")
	validatenodepoolCmd.Flags().String("cluster", "", "GKE cluster name")
	validatenodepoolCmd.Flags().String("nodepool", "", "Nodepool name")

}
