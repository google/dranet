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

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	"github.com/google/dranet/pkg/driver"
	"github.com/google/dranet/pkg/inventory"
	"github.com/google/dranet/pkg/pcidb"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"

	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	nodeutil "k8s.io/component-helpers/node/util"
	"k8s.io/klog/v2"
)

const (
	driverName = "dra.net"
)

var (
	hostnameOverride string
	kubeconfig       string
	bindAddress      string
	celExpression    string
	minPollInterval  time.Duration
	maxPollInterval  time.Duration
	pollBurst        int

	ready atomic.Bool
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&bindAddress, "bind-address", ":9177", "The IP address and port for the metrics and healthz server to serve on")
	flag.StringVar(&hostnameOverride, "hostname-override", "", "If non-empty, will be used as the name of the Node that kube-network-policies is running on. If unset, the node name is assumed to be the same as the node's hostname.")
	flag.StringVar(&celExpression, "filter", `!("dra.net/type" in attributes) || attributes["dra.net/type"].StringValue  != "veth"`, "CEL expression to filter network interface attributes (v1.DeviceAttribute).")
	flag.DurationVar(&minPollInterval, "inventory-min-poll-interval", 2*time.Second, "The minimum interval between two consecutive polls of the inventory.")
	flag.DurationVar(&maxPollInterval, "inventory-max-poll-interval", 1*time.Minute, "The maximum interval between two consecutive polls of the inventory.")
	flag.IntVar(&pollBurst, "inventory-poll-burst", 5, "The number of polls that can be run in a burst.")

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: dranet [options]\n\n")
		flag.PrintDefaults()
	}
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	printVersion()
	flag.VisitAll(func(f *flag.Flag) {
		klog.Infof("FLAG: --%s=%q", f.Name, f.Value)
	})

	mux := http.NewServeMux()
	// Add healthz handler
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	})
	// Add metrics handler
	mux.Handle("/metrics", promhttp.Handler())
	go func() {
		_ = http.ListenAndServe(bindAddress, mux)
	}()

	if err := pcidb.Setup(); err != nil {
		klog.Fatalf("Failed to setup PCI DB: %v", err)
	}

	var config *rest.Config
	var err error
	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		// creates the in-cluster config
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		klog.Fatalf("can not create client-go configuration: %v", err)
	}

	// use protobuf for better performance at scale
	// https://kubernetes.io/docs/reference/using-api/api-concepts/#alternate-representations-of-resources
	config.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	config.ContentType = "application/vnd.kubernetes.protobuf"

	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("can not create client-go client: %v", err)
	}

	nodeName, err := nodeutil.GetHostname(hostnameOverride)
	if err != nil {
		klog.Fatalf("can not obtain the node name, use the hostname-override flag if you want to set it to a specific value: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Trap signals for graceful shutdown.
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	opts := []driver.Option{}
	if celExpression != "" {
		env, err := cel.NewEnv(
			ext.NativeTypes(
				reflect.ValueOf(resourcev1.DeviceAttribute{}),
			),
			cel.Variable("attributes", cel.MapType(cel.StringType, cel.ObjectType("v1.DeviceAttribute"))),
		)
		if err != nil {
			klog.Fatalf("error creating CEL environment: %v", err)
		}
		ast, issues := env.Compile(celExpression)
		if issues != nil && issues.Err() != nil {
			klog.Fatalf("type-check error: %s", issues.Err())
		}
		prg, err := env.Program(ast)
		if err != nil {
			klog.Fatalf("program construction error: %s", err)
		}
		opts = append(opts, driver.WithFilter(prg))
	}
	db := inventory.New(
		inventory.WithRateLimiter(rate.NewLimiter(rate.Every(minPollInterval), pollBurst)),
		inventory.WithMaxPollInterval(maxPollInterval),
	)
	opts = append(opts, driver.WithInventory(db))
	dranet, err := driver.Start(ctx, driverName, clientset, nodeName, opts...)
	if err != nil {
		klog.Fatalf("driver failed to start: %v", err)
	}
	defer dranet.Stop() // Gracefully shutdown at the end.

	ready.Store(true)
	klog.Info("driver started")

	select {
	case sig := <-signalCh:
		klog.Infof("Received shutdown signal: %q. Initiating graceful shutdown...", sig)
		cancel()
	case <-ctx.Done():
		klog.Info("Context cancelled. Initiating graceful shutdown...")
	}
}

func printVersion() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var vcsRevision, vcsTime string
	for _, f := range info.Settings {
		switch f.Key {
		case "vcs.revision":
			vcsRevision = f.Value
		case "vcs.time":
			vcsTime = f.Value
		}
	}
	klog.Infof("dranet go %s build: %s time: %s", info.GoVersion, vcsRevision, vcsTime)
}
