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
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/google/dranet/pkg/cloudprovider/gce"
	"github.com/spf13/cobra"

	"k8s.io/klog/v2"
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
	acceleratorpodCmd.AddCommand(acceleratorpodListCmd)
}

var (
	machineType                 string
	nodeCount                   int
	additionalNetworkInterfaces int
)

// acceleratorpodListCmd represents the list command for accelerator pods (node pools)
var acceleratorpodListCmd = &cobra.Command{
	Use:   "list",
	Short: "List accelerator node pools in a GKE cluster",
	Long: `Lists all GKE node pools that were created and tagged by dranetctl
as accelerator pods. It identifies these node pools by looking for the
'dra.net/acceleratorpod: "true"' label.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if clusterName == "" {
			return fmt.Errorf("cluster name not explicitly provided")
		}
		// Try to get the nodepool from the cluster
		if location == "-" {
			return fmt.Errorf("location for cluster %s not specified", clusterName)
		}
		ctx := context.Background()

		// Get the cluster to list the node pools
		req := &containerpb.GetClusterRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectID, location, clusterName),
		}

		cluster, err := ContainersClient.GetCluster(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to get cluster: %w", err)
		}

		var acceleratorNodePools []string
		for _, np := range cluster.NodePools {
			if np.Config != nil && np.Config.Labels != nil {
				if val, ok := np.Config.Labels["dra.net/acceleratorpod"]; ok && val == "true" {
					acceleratorNodePools = append(acceleratorNodePools, np.Name)
				}
			}
		}

		if len(acceleratorNodePools) == 0 {
			fmt.Printf("No accelerator node pools found in cluster %s with label dra.net/acceleratorpod: \"true\".\n", clusterName)
			return nil
		}

		fmt.Printf("There are %d dranet accelerator node pools in cluster %s:\n", len(acceleratorNodePools), clusterName)
		fmt.Println("---")
		for _, name := range acceleratorNodePools {
			fmt.Println(name)
		}

		return nil
	},
}

// acceleratorpodCreateCmd represents the create subcommand for acceleratorpod
var acceleratorpodCreateCmd = &cobra.Command{
	Use:   "create <acceleratorpod_name>",
	Short: "Create a new accelerator pod (GPU-Direct enabled node pool)",
	Long: `Creates a new GPU-Direct enabled node pool on the specified GKE cluster,
creating necessary network and subnet resources and configuring
network-aware placement. This group of machines is referred to as an accelerator pod.`,
	Args: cobra.ExactArgs(1), // Expects the acceleratorpod name as an argument
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		acceleratorpodName := args[0]
		if clusterName == "" {
			return fmt.Errorf("cluster name not explicitly provided")
		}
		// Try to get the nodepool from the cluster
		if location == "-" {
			return fmt.Errorf("location for accelerator pod %s not specified", acceleratorpodName)
		}
		parts := strings.Split(location, "-")
		if len(parts) < 2 {
			return fmt.Errorf("onle zonal node pools allowed")
		}

		protocol, ok := gce.NetworkProtocolMap[machineType]
		// if is not an accelerator machine type it requires multiple networks to use dranet
		if !ok && additionalNetworkInterfaces == 0 {
			return fmt.Errorf("dranet require multiple interfaces to worker")
		}

		var additionalNetworkConfigs []*containerpb.AdditionalNodeNetworkConfig
		var err error
		switch protocol {
		case gce.GPUDirectTCPX:
			additionalNetworkConfigs, err = createAcceleratorNetworks(ctx, acceleratorpodName, 4)
		case gce.GPUDirectTCPXO:
			additionalNetworkConfigs, err = createAcceleratorNetworks(ctx, acceleratorpodName, 8)
		case gce.GPUDirectRDMA:
			additionalNetworkConfigs, err = createHPCAcceleratorNetwork(ctx, acceleratorpodName, 8) //
		default:
			additionalNetworkConfigs, err = createAcceleratorNetworks(ctx, acceleratorpodName, additionalNetworkInterfaces)
		}
		if err != nil {
			return fmt.Errorf("fail to create networks %v", err)
		}

		klog.Infof("Creating acceleratorpod '%s'...\n", acceleratorpodName)
		klog.Infof("  Project: %s\n", projectID)
		klog.Infof("  Location: %s\n", location)
		klog.Infof("  Cluster: %s\n", clusterName)
		klog.Infof("  Machine Type: %s\n", machineType)
		klog.Infof("  Node Count: %d\n", nodeCount)
		klog.Infof("  Node Pool Name: %s\n", acceleratorpodName)

		nodePool := &containerpb.NodePool{
			Name:             acceleratorpodName,
			InitialNodeCount: int32(nodeCount),
			Locations:        []string{location},
			Config: &containerpb.NodeConfig{
				MachineType: machineType,
				// TODO allow to set labels and taints
				Labels:         map[string]string{"dra.net/acceleratorpod": "true"},
				ResourceLabels: map[string]string{"dra.net/acceleratorpod": "true"},
			},
			NetworkConfig: &containerpb.NodeNetworkConfig{
				AdditionalNodeNetworkConfigs: additionalNetworkConfigs,
			},
			PlacementPolicy: &containerpb.NodePool_PlacementPolicy{
				Type: compactPlacement(machineType),
			},
		}

		createReq := &containerpb.CreateNodePoolRequest{
			Parent:   fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectID, location, clusterName),
			NodePool: nodePool,
		}

		klog.Infof("Creating node pool '%s' in cluster '%s'...\n", acceleratorpodName, clusterName)
		op, err := ContainersClient.CreateNodePool(ctx, createReq)
		if err != nil {
			return fmt.Errorf("failed to create node pool: %w", err)
		}

		if err := waitForOperation(ctx, location, op.GetName()); err != nil {
			return fmt.Errorf("waiting for node pool creation: %w", err)
		}

		klog.Infof("Node pool '%s' created successfully.\n", acceleratorpodName)
		// TODO Installing dranet and required components
		return nil
	},
}

func compactPlacement(machineType string) containerpb.NodePool_PlacementPolicy_Type {
	// https://cloud.google.com/kubernetes-engine/docs/how-to/compact-placement
	// Available only on A2, A3, A4, C2, C2D, C3, C3D, C4, G2, H3, N2, and N2D machine types
	for _, prefix := range []string{"a2", "a3", "a4", "c2", "c2d", "c3", "c3d", "c4", "g2", "h3", "n2", "n2d"} {
		if strings.HasPrefix(prefix+"-", machineType) {
			return containerpb.NodePool_PlacementPolicy_COMPACT
		}
	}
	return containerpb.NodePool_PlacementPolicy_TYPE_UNSPECIFIED
}

func init() {
	// Flags for the 'acceleratorpod create' command
	acceleratorpodCreateCmd.Flags().StringVar(&machineType, "machine-type", "", "The Google Compute Engine machine type for the nodes (required)")
	acceleratorpodCreateCmd.Flags().IntVar(&nodeCount, "node-count", 0, "The number of VMs (nodes) to create in the node pool (required)")
	acceleratorpodCreateCmd.Flags().IntVar(&additionalNetworkInterfaces, "additional-network-interfaces", 0, "The number of additional network interfaces for each node (optional)")

	// TODO Placement and Nodepool Flags
	// Mark required flags for the create command
	_ = acceleratorpodCreateCmd.MarkFlagRequired("machine-type")
	_ = acceleratorpodCreateCmd.MarkFlagRequired("node-count")
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
					fmt.Printf("      Placement Policy Type: %s\n", nodepool.PlacementPolicy.Type)
					if nodepool.PlacementPolicy.TpuTopology != "" {
						fmt.Printf("      Placement TPU Topology: %s\n", nodepool.PlacementPolicy.TpuTopology)
					}
					if nodepool.PlacementPolicy.PolicyName != "" {
						fmt.Printf("      Placement Policy Name: %s\n", nodepool.PlacementPolicy.PolicyName)
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
		ctx := cmd.Context()
		acceleratorpodName := args[0]
		if clusterName == "" {
			return fmt.Errorf("cluster name not explicitly provided")
		}
		// Try to get the nodepool from the cluster
		if location == "-" {
			return fmt.Errorf("location for accelerator pod %s not specified", acceleratorpodName)
		}

		klog.Infof("Deleting acceleratorpod '%s'...\n", acceleratorpodName)
		klog.Infof("  Project: %s\n", projectID)
		klog.Infof("  Target Cluster: %s\n", clusterName)

		req := &containerpb.GetNodePoolRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", projectID, location, clusterName, acceleratorpodName),
		}

		nodePool, err := ContainersClient.GetNodePool(ctx, req)
		if err != nil {
			return fmt.Errorf("error trying to get AcceleratorPod %s: %w", acceleratorpodName, err)
		}

		if dryRun {
			klog.Infof("Deleting AcceleratorPod %s", nodePool.String())
			return nil
		}

		reqNodePoolDel := &containerpb.DeleteNodePoolRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", projectID, location, clusterName, acceleratorpodName),
		}
		op, err := ContainersClient.DeleteNodePool(ctx, reqNodePoolDel)
		if err != nil {
			return fmt.Errorf("error trying to get AcceleratorPod %s: %w", acceleratorpodName, err)
		}

		err = waitForOperation(ctx, location, op.Name)
		if err != nil {
			return fmt.Errorf("delete Nodepool Wait: %w", err)
		}

		// Cleanup the networks if those were created by us
		for _, networkConfig := range nodePool.NetworkConfig.AdditionalNodeNetworkConfigs {
			if !strings.HasPrefix(networkConfig.Network, wellKnownPrefix) {
				klog.V(2).Infof("Skipping network %s", networkConfig.Network)
				continue
			}

			err := deleteNetwork(ctx, networkConfig.Network)
			if err != nil {
				return err
			}
		}

		return nil
	},
}

func waitForOperation(ctx context.Context, operationLocation, operationName string) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	klog.V(2).Infof("Waiting for operation to complete: %s\n", operationName)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for operation %s: %w", operationName, ctx.Err())
		case <-ticker.C:
			o, err := ContainersClient.GetOperation(ctx, &containerpb.GetOperationRequest{
				Name: fmt.Sprintf("projects/%s/locations/%s/operations/%s", projectID, operationLocation, operationName),
			})
			if err != nil {
				return fmt.Errorf("failed to get operation %s: %w", operationName, err)
			}
			if o.GetStatus() == containerpb.Operation_DONE {
				klog.V(2).Info("Operation complete!")
				if status := o.GetError(); status != nil {
					return fmt.Errorf("operation %s failed: code = %d, message = %s", operationName, status.GetCode(), status.GetMessage())
				}
				return nil
			}
			klog.V(2).Infof("Operation not complete yet, status: %v\n", o.GetStatus())
		}
	}
}
