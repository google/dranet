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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/spf13/cobra"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/proto"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	// assume total ownership of these networks by dranet
	wellKnownPrefix = "dranetctl"
)

var (
	// extract region and subnet name from URL
	reSubnets              = regexp.MustCompile(`/regions/([^/]+)/subnetworks/([^/]+)$`)
	acceleratorPodNameFlag string
)

// getRegion get the region part from a location
func getRegion(locationStr string) string {
	parts := strings.Split(locationStr, "-")
	if len(parts) == 3 {
		return strings.Join(parts[:2], "-")
	}
	return locationStr
}

// obtainHexHash to get an unique string
func obtainHexHash(input string) string {
	hasher := sha256.New()
	hasher.Write([]byte(input))
	hashBytes := hasher.Sum(nil)
	hexHash := hex.EncodeToString(hashBytes)

	return hexHash[:16]
}

func createAcceleratorNetworks(ctx context.Context, acceleratorpodName string, networkInterfaces int) ([]*containerpb.AdditionalNodeNetworkConfig, error) {
	klog.Infof("Creating %d additional networks and subnetworks...\n", additionalNetworkInterfaces)
	additionalNetworkConfigs := make([]*containerpb.AdditionalNodeNetworkConfig, 0, networkInterfaces)
	for i := 1; i <= networkInterfaces; i++ {
		// networkName has to be unique
		networkName := fmt.Sprintf("%s-net-%s-%d", wellKnownPrefix, obtainHexHash(acceleratorpodName), i)
		subnetworkName := fmt.Sprintf("%s-subnet-%s-%d", wellKnownPrefix, obtainHexHash(acceleratorpodName), i)
		subnetRegion := getRegion(location) // subnets are in the same region as the cluster

		// Create Network
		insertNetworkReq := &computepb.InsertNetworkRequest{
			Project: projectID,
			NetworkResource: &computepb.Network{
				Name:                  &networkName,
				AutoCreateSubnetworks: proto.Bool(false), // We'll create subnet explicitly
				Mtu:                   ptr.To[int32](8244),
			},
		}

		klog.V(2).Infof("Creating network: %s\n", networkName)
		opNetwork, err := NetworksClient.Insert(ctx, insertNetworkReq)
		if err != nil {
			return nil, fmt.Errorf("failed to create network '%s': %w", networkName, err)
		}
		if err := opNetwork.Wait(ctx); err != nil {
			return nil, fmt.Errorf("waiting for network '%s' creation: %w", networkName, err)
		}

		// Create Subnetwork
		// get a non overlaping range from the Class E
		// TODO: this needs to be handled better
		networkURL := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/networks/%s", projectID, networkName)
		cidr := fmt.Sprintf("255.255.%d.0/24", 20+i)
		insertSubnetReq := &computepb.InsertSubnetworkRequest{
			Project: projectID,
			Region:  subnetRegion,
			SubnetworkResource: &computepb.Subnetwork{
				Name:        &subnetworkName,
				Network:     &networkURL,
				IpCidrRange: &cidr,
				Region:      &subnetRegion,
			},
		}

		klog.Infof("Creating subnetwork: %s in %s\n", subnetworkName, subnetRegion)
		opSubnet, err := SubnetworksClient.Insert(ctx, insertSubnetReq)
		if err != nil {
			return nil, fmt.Errorf("failed to create subnetwork '%s': %w", subnetworkName, err)
		}
		if err := opSubnet.Wait(ctx); err != nil {
			return nil, fmt.Errorf("waiting for subnetwork '%s' creation: %w", subnetworkName, err)
		}

		additionalNetworkConfigs = append(additionalNetworkConfigs, &containerpb.AdditionalNodeNetworkConfig{
			Network:    networkName,
			Subnetwork: subnetworkName,
		})
	}

	return additionalNetworkConfigs, nil
}

func createHPCAcceleratorNetwork(ctx context.Context, acceleratorpodName string, networkInterfaces int) ([]*containerpb.AdditionalNodeNetworkConfig, error) {
	klog.Infof("Creating %d additional networks and subnetworks...\n", additionalNetworkInterfaces)

	networkName := fmt.Sprintf("%s-rdma-%s", wellKnownPrefix, obtainHexHash(acceleratorpodName))

	additionalNetworkConfigs := make([]*containerpb.AdditionalNodeNetworkConfig, 0, networkInterfaces)

	client, err := compute.NewNetworkProfilesRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("can not create NewNetworkProfilesRESTClient client: %v", err)
	}
	defer client.Close()

	req := &computepb.ListNetworkProfilesRequest{
		Filter:  ptr.To(fmt.Sprintf("location.name=%s", location)),
		Project: projectID,
	}
	var networkProfile string
	it := client.List(ctx, req)
	for {
		resp, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("can not iterate Network Profiles: %v", err)
		}
		for _, ifType := range resp.GetFeatures().InterfaceTypes {
			if ifType == "MRDMA" {
				networkProfile = resp.GetSelfLink()
			}
		}
	}
	if networkProfile == "" {
		return nil, fmt.Errorf("could not find Network Profile")
	}
	klog.V(2).Infof("Successfully obtained RDMA network profile %s", networkProfile)
	// Create Network
	insertNetworkReq := &computepb.InsertNetworkRequest{
		Project: projectID,
		NetworkResource: &computepb.Network{
			Name:                  &networkName,
			AutoCreateSubnetworks: proto.Bool(false), // We'll create subnet explicitly
			NetworkProfile:        &networkProfile,
			Mtu:                   ptr.To[int32](8896),
		},
	}
	klog.V(2).Infof("Creating network: %s\n", networkName)
	opNetwork, err := NetworksClient.Insert(ctx, insertNetworkReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create network '%s': %w", networkName, err)
	}
	if err := opNetwork.Wait(ctx); err != nil {
		return nil, fmt.Errorf("waiting for network '%s' creation: %w", networkName, err)
	}

	for i := 1; i <= networkInterfaces; i++ {
		subnetworkName := fmt.Sprintf("%s-subnet-%s-%d", wellKnownPrefix, obtainHexHash(acceleratorpodName), i)
		subnetRegion := getRegion(location) // subnets are in the same region as the cluster
		// Create Subnetwork
		// get a non overllaping range from the Class E
		// TODO: this needs to be handled better
		cidr := fmt.Sprintf("255.255.%d.0/24", 20+i)
		insertSubnetReq := &computepb.InsertSubnetworkRequest{
			Project: projectID,
			Region:  subnetRegion,
			SubnetworkResource: &computepb.Subnetwork{
				Name:        &subnetworkName,
				Network:     &networkName, // Use the name, the API will resolve it
				Region:      &subnetRegion,
				IpCidrRange: &cidr,
			},
		}

		klog.V(2).Infof("Creating subnetwork: %s in %s\n", subnetworkName, subnetRegion)
		opSubnet, err := SubnetworksClient.Insert(ctx, insertSubnetReq)
		if err != nil {
			return nil, fmt.Errorf("failed to create subnetwork '%s': %w", subnetworkName, err)
		}
		if err := opSubnet.Wait(ctx); err != nil {
			return nil, fmt.Errorf("waiting for subnetwork '%s' creation: %w", subnetworkName, err)
		}

		additionalNetworkConfigs = append(additionalNetworkConfigs, &containerpb.AdditionalNodeNetworkConfig{
			Network:    networkName,
			Subnetwork: subnetworkName,
		})

	}
	return additionalNetworkConfigs, nil
}

func deleteNetwork(ctx context.Context, networkName string) error {
	reqNet := &computepb.GetNetworkRequest{
		Project: projectID,
		Network: networkName,
	}

	klog.V(2).InfoS("get network %s", networkName)
	network, err := NetworksClient.Get(ctx, reqNet)
	if err != nil {
		return fmt.Errorf("getting network '%s': %w", networkName, err)
	}

	klog.V(2).InfoS("get firewalls associated", "network", network)
	reqFw := &computepb.GetEffectiveFirewallsNetworkRequest{
		Project: projectID,
		Network: networkName,
	}

	respFw, err := NetworksClient.GetEffectiveFirewalls(ctx, reqFw)
	if err != nil {
		return fmt.Errorf("getting firewalls for network %s: %w", networkName, err)
	}
	klog.V(2).InfoS("get firewall", "network", respFw)
	for _, firewall := range respFw.GetFirewalls() {
		req := &computepb.DeleteFirewallRequest{
			Project:  projectID,
			Firewall: firewall.GetName(),
		}

		if dryRun {
			klog.Infof("dry-run: deleting firewall %s", firewall.GetName())
			continue
		}
		op, err := FirewallsClient.Delete(ctx, req)
		if err != nil {
			return fmt.Errorf("delete Firewall: %w", err)
		}

		err = op.Wait(ctx)
		if err != nil {
			return fmt.Errorf("delete Firewall Wait: %w", err)
		}
	}

	for _, subnet := range network.Subnetworks {
		if dryRun {
			klog.Infof("dry-run: deleting subnet %s", subnet)
			continue
		}
		match := reSubnets.FindStringSubmatch(subnet)
		if len(match) != 3 {
			klog.Infof("could not get subnet region and name from %s", subnet)
			continue
		}

		region := getRegion(match[1])
		subnetName := match[2]

		req := &computepb.DeleteSubnetworkRequest{
			Project:    projectID,
			Region:     region,
			Subnetwork: subnetName,
		}
		op, err := SubnetworksClient.Delete(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to delete subnet '%s' in region '%s': %v", subnet, location, err)
		}
		err = op.Wait(ctx)
		if err != nil {
			return fmt.Errorf("delete Subnet Wait: %w", err)
		}
	}

	if dryRun {
		klog.Infof("dry-run: deleting network %s", networkName)
		return nil
	}
	// once firewalls are deleted we can delete the network
	reqNetDel := &computepb.DeleteNetworkRequest{
		Project: projectID,
		Network: networkName,
	}
	op, err := NetworksClient.Delete(ctx, reqNetDel)
	if err != nil {
		return fmt.Errorf("deleting network '%s': %w", networkName, err)
	}
	err = op.Wait(ctx)
	if err != nil {
		return fmt.Errorf("delete network Wait: %w", err)
	}
	return nil
}

// listNetworks list all dranet networks
func listNetworks(ctx context.Context, acceleratorPodName string) []string {
	output := []string{}
	// Prepare the List request.
	req := &computepb.ListNetworksRequest{
		Project: projectID,
	}

	// List networks in the specified project.
	it := NetworksClient.List(ctx, req)
	for {
		network, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			klog.Infof("Failed to list networks: %v", err)
			return output
		}

		// it assumes ownership via the well known prefix
		if !strings.HasPrefix(*network.Name, wellKnownPrefix) {
			continue
		}
		// filter by accelerator pod name if exist
		if acceleratorPodName != "" &&
			!strings.Contains(*network.Name, obtainHexHash(acceleratorPodName)) {
			continue
		}

		output = append(output, *network.Name)

		klog.V(2).Infof("Name: %s\n", *network.Name)
		klog.V(2).Infof("  ID: %d\n", network.Id)
		klog.V(2).Infof("  SelfLink: %s\n", *network.SelfLink)
		if len(network.Subnetworks) > 0 {
			klog.V(2).Infoln("  Subnetworks:")
			for _, subnet := range network.Subnetworks {
				klog.V(2).Infof("    %s\n", subnet)
			}
		}
		klog.V(2).Infoln("---")
	}
	return output
}

var networksCmd = &cobra.Command{
	Use:   "networks",
	Short: "Manage Google Cloud networks",
	Long:  `Provides commands to manage Google Cloud networks.`,
}

var cleanupNetworksCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Deletes all Google Cloud networks labeled as managed by DRA-Net",
	Long: `This command lists all Google Cloud networks in the specified project and deletes those created by dranetctl.
Use with caution, as this action is irreversible.`,
	Args: cobra.MaximumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		networks := listNetworks(ctx, acceleratorPodNameFlag)
		for _, network := range networks {
			klog.Infof("deleting network %s\n", network)
			err := deleteNetwork(ctx, network)
			if err != nil {
				klog.Infof("Failed to delete network %s: %v", network, err)
			}
		}
	},
}

var listNetworksCmd = &cobra.Command{
	Use:   "list",
	Short: "Lists all Google Cloud networks in a project",
	Args:  cobra.MaximumNArgs(0), // optional the acceleratorpod name as an argument
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		networks := listNetworks(ctx, acceleratorPodNameFlag)
		fmt.Printf("There are %d dranet networks\n", len(networks))
		fmt.Println("---")
		for _, network := range networks {
			fmt.Println(network)
		}
	},
}

func init() {
	networksCmd.AddCommand(cleanupNetworksCmd)
	networksCmd.AddCommand(listNetworksCmd)
	networksCmd.PersistentFlags().StringVar(&acceleratorPodNameFlag, "acceleratorpod", "", "Name of the accelerator pod to filter networks")
}
