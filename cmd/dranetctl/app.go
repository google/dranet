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

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/dranet/pkg/dranetctl/gke"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "dranetctl",
	Short: "A tool to manage Kubernetes clusters advanced networking across cloud providers",
	Long:  `This tool allows you to manage Kubernetes clusters advanced networking use cases.`,
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("\nReceived signal: %v. Shutting down...\n", sig)
		cancel()
	}()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func init() {
	// TODO(aojea) add other cloud providers
	// GKE subcommand
	rootCmd.AddCommand(gke.GkeCmd)
}
