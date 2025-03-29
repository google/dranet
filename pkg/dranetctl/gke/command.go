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
	"os"

	compute "cloud.google.com/go/compute/apiv1"
	container "cloud.google.com/go/container/apiv1"
	"github.com/spf13/cobra"
	"google.golang.org/api/option"
)

var (
	ContainersClient  *container.ClusterManagerClient // handle GKE Clusters
	NetworksClient    *compute.NetworksClient         // handle GCE Networks
	SubnetworksClient *compute.SubnetworksClient      // handle GCE Subnets
	FirewallsClient   *compute.FirewallsClient        // handle GCE Firewalls

	projectID   string
	location    string
	clusterName string
	dryRun      bool
)

func init() {
	GkeCmd.AddCommand(acceleratorpodCmd)
	GkeCmd.AddCommand(networksCmd)

	GkeCmd.PersistentFlags().String("auth-file", "", "Path to the Google Cloud service account JSON file")
	GkeCmd.PersistentFlags().StringVar(&projectID, "project", "", "Google Cloud Project ID")
	GkeCmd.PersistentFlags().StringVar(&location, "location", "-", "Google Cloud region or zone to operate in")
	GkeCmd.PersistentFlags().StringVar(&clusterName, "cluster", "", "The name of the target GKE cluster")
	GkeCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "The command will print the write actions without executing them")
}

var GkeCmd = &cobra.Command{
	Use:   "gke",
	Short: "Manage resources on Google Kubernetes Engine (GKE)",
	Long:  `This command allows you to manage resources on GKE.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// This function runs before any subcommand of gke
		if projectID == "" {
			projectID = os.Getenv("GCP_PROJECT_ID")
			if projectID == "" {
				return fmt.Errorf("missing project")
			}
		}

		authFile, err := cmd.Flags().GetString("auth-file")
		if err != nil {
			return err
		}
		ctx := context.Background()

		opts := []option.ClientOption{}
		if authFile != "" {
			opts = append(opts, option.WithCredentialsFile(authFile))
		}

		containerClient, err := container.NewClusterManagerClient(ctx, opts...)
		if err != nil {
			return err
		}
		ContainersClient = containerClient

		networksClient, err := compute.NewNetworksRESTClient(ctx, opts...)
		if err != nil {
			return err
		}
		NetworksClient = networksClient

		subnetworksClient, err := compute.NewSubnetworksRESTClient(ctx, opts...)
		if err != nil {
			return err
		}
		SubnetworksClient = subnetworksClient

		firewallsClient, err := compute.NewFirewallsRESTClient(ctx, opts...)
		if err != nil {
			return fmt.Errorf("NewFirewallsClient: %w", err)
		}
		FirewallsClient = firewallsClient

		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if ContainersClient != nil {
			ContainersClient.Close()
		}
		if NetworksClient != nil {
			NetworksClient.Close()
		}
		if SubnetworksClient != nil {
			SubnetworksClient.Close()
		}
		if FirewallsClient != nil {
			FirewallsClient.Close()
		}
	},
}
