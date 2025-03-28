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
	"fmt"
	"log"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/spf13/cobra"
	"google.golang.org/api/iterator"
)

var networkCmd = &cobra.Command{
	Use:   "network",
	Short: "Manage networks on GKE",
	Long:  `Allows creating, validating, and managing nodepools on GKE.`,
}

var listnetworkCmd = &cobra.Command{
	Use:   "list",
	Short: "List any existing networks",
	Run: func(cmd *cobra.Command, args []string) {
		projectID, err := cmd.Flags().GetString("project")
		if err != nil {
			log.Fatalf("Error getting project flag: %v", err)
		}
		// List subnetworks
		fmt.Println("\nListing Subnetworks:")
		networkReq := &computepb.ListNetworksRequest{
			Project: projectID,
		}
		networksIt := NetworksClient.List(cmd.Context(), networkReq)
		for {
			network, err := networksIt.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Fatalf("Failed to iterate: %v", err)
			}
			log.Printf("- Name: %s, URL: %#v\n", network.GetName(), network.GetSubnetworks())
		}
	},
}

var validatenetworkCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate an existing network",
	Run: func(cmd *cobra.Command, args []string) {
		// ... (rest of the code)
	},
}

func init() {

	networkCmd.AddCommand(listnetworkCmd)
	networkCmd.AddCommand(validatenetworkCmd)

	listnetworkCmd.Flags().String("project", "", "GCP project ID")
	listnetworkCmd.Flags().String("location", "", "GCP location (e.g., us-central1)")
	listnetworkCmd.Flags().String("cluster", "", "GKE cluster name")

	validatenetworkCmd.Flags().String("project", "", "GCP project ID")
	validatenetworkCmd.Flags().String("location", "", "GCP location (e.g., us-central1)")
	validatenetworkCmd.Flags().String("cluster", "", "GKE cluster name")
	validatenetworkCmd.Flags().String("nodepool", "", "Nodepool name")

}
