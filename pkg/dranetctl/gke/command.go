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

	"github.com/google/dranet/pkg/cloudprovider/gce"
	"github.com/spf13/cobra"

	compute "cloud.google.com/go/compute/apiv1"
	container "cloud.google.com/go/container/apiv1"
	"google.golang.org/api/option"
)

var GkeCmd = &cobra.Command{
	Use:   "gke",
	Short: "Manage resources on Google Kubernetes Engine (GKE)",
	Long:  `This command allows you to manage resources on GKE.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// This function runs before any subcommand of gke
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

		gce.ContainersClient = containerClient

		networksClient, err := compute.NewNetworksClient(ctx, opts...)
		if err != nil {
			return err
		}

		gce.NetworksClient = networksClient

		subnetworksClient, err := compute.NewSubnetworksClient(ctx, opts...)
		if err != nil {
			return err
		}

		gce.SubnetworksClient = subnetworksClient

		return nil
	},
}

func init() {
	GkeCmd.AddCommand(nodepoolCmd)
	GkeCmd.PersistentFlags().String("auth-file", "", "Path to the Google Cloud service account JSON file")

}
