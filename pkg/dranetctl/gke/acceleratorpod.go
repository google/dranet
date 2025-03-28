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

	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/spf13/cobra"
)

// acceleratorpodCmd represents the acceleratorpod command
var acceleratorpodCmd = &cobra.Command{
	Use:   "acceleratorpod",
	Short: "Manage accelerator pods (GPU-Direct enabled node pools)",
	Long: `The 'acceleratorpod' command allows you to create and manage
GPU-Direct enabled node pools on GKE, which we refer to as accelerator pods.`,
}

func init() {
	acceleratorpodCmd.AddCommand(acceleratorpodCreateCmd)
	acceleratorpodCmd.AddCommand(acceleratorpodGetCmd)
	acceleratorpodCmd.AddCommand(acceleratorpodDeleteCmd)
}

var (
	machineType                 string
	nodeCount                   int32
	additionalNetworkInterfaces int32
	networkName                 string
	subnetName                  string
	createNetwork               bool
	newNetworkName              string
	newNetworkIPRange           string
	createSubnet                bool
	newSubnetName               string
	newSubnetRegion             string
	newSubnetIPRange            string
	placementPolicy             string
	placementPolicyCreate       bool
	newPlacementPolicyName      string
	placementPolicyScope        string
	placementPolicyType         string
	nodePoolName                string
	labels                      []string
	nodeTaints                  []string
	diskSizeGB                  int32
	diskType                    string
	imageType                   string
)

// acceleratorpodCreateCmd represents the create subcommand for acceleratorpod
var acceleratorpodCreateCmd = &cobra.Command{
	Use:   "create <acceleratorpod_name>",
	Short: "Create a new accelerator pod (GPU-Direct enabled node pool)",
	Long: `Creates a new GPU-Direct enabled node pool on the specified GKE cluster,
creating necessary network and subnet resources and configuring
network-aware placement. This group of machines is referred to as an accelerator pod.`,
	Args: cobra.ExactArgs(1), // Expects the acceleratorpod name as an argument
	Run: func(cmd *cobra.Command, args []string) {
		acceleratorpodName := args[0]
		fmt.Printf("Creating acceleratorpod '%s'...\n", acceleratorpodName)
		fmt.Printf("  Project: %s\n", projectID)
		fmt.Printf("  Location: %s\n", location)
		fmt.Printf("  Cluster: %s\n", clusterName)
		fmt.Printf("  Machine Type: %s\n", machineType)
		fmt.Printf("  Node Count: %d\n", nodeCount)
		fmt.Printf("  Network: %s (create: %t, new name: %s, new range: %s)\n", networkName, createNetwork, newNetworkName, newNetworkIPRange)
		fmt.Printf("  Subnet: %s (create: %t, new name: %s, new region: %s, new range: %s)\n", subnetName, createSubnet, newSubnetName, newSubnetRegion, newSubnetIPRange)
		fmt.Printf("  Placement Policy: %s (create: %t, new name: %s, scope: %s, type: %s)\n", placementPolicy, placementPolicyCreate, newPlacementPolicyName, placementPolicyScope, placementPolicyType)
		fmt.Printf("  Node Pool Name: %s\n", nodePoolName)
		fmt.Printf("  Labels: %v\n", labels)
		fmt.Printf("  Taints: %v\n", nodeTaints)
		fmt.Printf("  Disk Size: %dGB, Type: %s\n", diskSizeGB, diskType)
		fmt.Printf("  Image Type: %s\n", imageType)

		// TODO: Implement the actual logic to create the network, subnet,
		// placement policy, and GKE node pool here.
	},
}

func init() {
	// Flags for the 'acceleratorpod create' command
	acceleratorpodCreateCmd.Flags().StringVar(&machineType, "machine-type", "", "The Google Compute Engine machine type for the nodes (required)")
	acceleratorpodCreateCmd.Flags().Int32Var(&nodeCount, "node-count", 0, "The number of VMs (nodes) to create in the node pool (required)")
	acceleratorpodCreateCmd.Flags().Int32Var(&additionalNetworkInterfaces, "additional-network-interfaces", 0, "The number of additional network interfaces for each node (optional)")

	// Network Configuration Flags
	acceleratorpodCreateCmd.Flags().StringVar(&networkName, "network", "", "The name of an existing Google Cloud network to use")
	acceleratorpodCreateCmd.Flags().StringVar(&subnetName, "subnet", "", "The name of an existing subnet to use within the specified network")
	acceleratorpodCreateCmd.Flags().BoolVar(&createNetwork, "create-network", false, "If specified, the tool will create a new network")
	acceleratorpodCreateCmd.Flags().StringVar(&newNetworkName, "new-network-name", "", "The name to use if --create-network is specified")
	acceleratorpodCreateCmd.Flags().StringVar(&newNetworkIPRange, "new-network-ip-range", "", "The IP range for the new network if --create-network is used (e.g., 10.10.0.0/20)")
	acceleratorpodCreateCmd.Flags().BoolVar(&createSubnet, "create-subnet", false, "If specified, the tool will create a new subnet")
	acceleratorpodCreateCmd.Flags().StringVar(&newSubnetName, "new-subnet-name", "", "The name to use if --create-subnet is specified")
	acceleratorpodCreateCmd.Flags().StringVar(&newSubnetRegion, "new-subnet-region", "", "The region for the new subnet if --create-subnet is used (defaults to --region)")
	acceleratorpodCreateCmd.Flags().StringVar(&newSubnetIPRange, "new-subnet-ip-range", "", "The IP range for the new subnet if --create-subnet is used (e.g., 10.10.0.0/24)")

	// Placement Configuration Flags
	acceleratorpodCreateCmd.Flags().StringVar(&placementPolicy, "placement-policy", "", "The name of an existing Compute Engine placement policy to use")
	acceleratorpodCreateCmd.Flags().BoolVar(&placementPolicyCreate, "placement-policy-create", false, "If specified, the tool will attempt to create a placement policy")
	acceleratorpodCreateCmd.Flags().StringVar(&newPlacementPolicyName, "new-placement-policy-name", "", "The name to use if --placement-policy-create is specified")
	acceleratorpodCreateCmd.Flags().StringVar(&placementPolicyScope, "placement-policy-scope", "region", "The scope of the placement policy (region, zone)")
	acceleratorpodCreateCmd.Flags().StringVar(&placementPolicyType, "placement-policy-type", "COLLOCATED", "The type of placement policy to create (COLLOCATED)")

	// Nodepool Specific Options
	acceleratorpodCreateCmd.Flags().StringVar(&nodePoolName, "node-pool-name", "", "A custom name for the GKE node pool")
	acceleratorpodCreateCmd.Flags().StringSliceVar(&labels, "labels", []string{}, "Labels to apply to the nodes in the node pool (key=value,...)")
	acceleratorpodCreateCmd.Flags().StringSliceVar(&nodeTaints, "node-taints", []string{}, "Taints to apply to the nodes in the node pool (key=value:EFFECT,...)")
	acceleratorpodCreateCmd.Flags().Int32Var(&diskSizeGB, "disk-size", 100, "The size of the boot disk for the nodes in GB")
	acceleratorpodCreateCmd.Flags().StringVar(&diskType, "disk-type", "pd-standard", "The type of the boot disk (pd-standard, pd-ssd)")
	acceleratorpodCreateCmd.Flags().StringVar(&imageType, "image-type", "COS_CONTAINERD", "The OS image type for the nodes")

	// Mark required flags for the create command
	acceleratorpodCreateCmd.MarkFlagRequired("cluster")
	acceleratorpodCreateCmd.MarkFlagRequired("machine-type")
	acceleratorpodCreateCmd.MarkFlagRequired("node-count")
}

// acceleratorpodGetCmd represents the get subcommand for acceleratorpod
var acceleratorpodGetCmd = &cobra.Command{
	Use:   "get [acceleratorpod_name]",
	Short: "Get details about an accelerator pod",
	Long: `Retrieves and displays detailed information about the specified accelerator pod
(GKE node pool). You must provide the name of the accelerator pod. You can
optionally specify the cluster if needed.`,
	Args: cobra.MaximumNArgs(1), // Expects the acceleratorpod name as an  optional argument
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		req := &containerpb.ListClustersRequest{
			Parent: fmt.Sprintf("projects/%s/locations/%s", projectID, location),
		}
		var acceleratorpodName string
		if len(args) > 0 {
			acceleratorpodName = args[0]
		}

		resp, err := ContainersClient.ListClusters(ctx, req)
		if err != nil {
			return fmt.Errorf("can not list clusters for %s : %w", req.Parent, err)
		}
		for _, cluster := range resp.GetClusters() {
			// TODO(aojea) check if this can be done server side
			if clusterName != "" && cluster.Name != clusterName {
				continue
			}

			fmt.Printf("Cluster Name: %s\n", cluster.Name)
			fmt.Printf("  Location: %s\n", cluster.Location)
			fmt.Printf("  Node Pools:\n")

			for _, nodepool := range cluster.GetNodePools() {
				if acceleratorpodName != "" && nodepool.Name != acceleratorpodName {
					continue
				}
				fmt.Printf("    - Name: %s\n", nodepool.Name)
				fmt.Printf("      Node Count: %d\n", nodepool.InitialNodeCount) // Or npResp.Autoscaling.MinNodeCount/MaxNodeCount if autoscaling is enabled
				fmt.Printf("      Machine Type: %s\n", nodepool.Config.MachineType)
				fmt.Printf("      Additional Networks: %d\n", len(nodepool.NetworkConfig.AdditionalNodeNetworkConfigs))
				if nodepool.PlacementPolicy != nil {
					fmt.Printf("      Placement Policy Type: %s\n", *&nodepool.PlacementPolicy.Type)
					if nodepool.PlacementPolicy.TpuTopology != "" {
						fmt.Printf("      Placement TPU Topology: %s\n", *&nodepool.PlacementPolicy.TpuTopology)
					}
					if nodepool.PlacementPolicy.PolicyName != "" {
						fmt.Printf("      Placement Policy Name: %s\n", *&nodepool.PlacementPolicy.PolicyName)
					}
				}
				fmt.Println("      ---")
			}
			fmt.Println()
		}
		return nil
	},
}

// acceleratorpodDeleteCmd represents the delete subcommand for acceleratorpod
var acceleratorpodDeleteCmd = &cobra.Command{
	Use:   "delete <acceleratorpod_name>",
	Short: "Delete an accelerator pod (node pool)",
	Long: `Deletes the specified accelerator pod (which corresponds to a GKE node pool).
You must specify the name of the accelerator pod to delete. Optionally, you can
specify the cluster if the accelerator pod name is not unique across clusters
(though it's recommended to have unique naming).`,
	Args: cobra.ExactArgs(1), // Expects the acceleratorpod name as an argument
	RunE: func(cmd *cobra.Command, args []string) error {
		acceleratorpodName := args[0]
		fmt.Printf("Deleting acceleratorpod '%s'...\n", acceleratorpodName)
		fmt.Printf("  Project: %s\n", projectID)
		if clusterName == "" {
			return fmt.Errorf("Warning: Cluster name not explicitly provided.")

		}
		fmt.Printf("  Target Cluster: %s\n", clusterName)
		return nil
	},
}
