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

package inventory

import (
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func TestDB_AddPodNetns(t *testing.T) {
	db := New()
	podName := "test-pod"
	netnsPath := "/proc/self/ns/net" // Using the current process's network namespace for testing

	// Simulate adding a pod network namespace
	db.AddPodNetns(podName, netnsPath)

	// Check if the pod network namespace was added correctly
	db.mu.RLock()
	defer db.mu.RUnlock()
	if len(db.podStore) != 1 {
		t.Errorf("Expected 1 pod in podStore, got %d", len(db.podStore))
	}

	// Get the current process's network namespace ID
	ns, err := netns.GetFromPath(netnsPath)
	if err != nil {
		t.Fatalf("Failed to get network namespace from path: %v", err)
	}
	id, err := netlink.GetNetNsIdByFd(int(ns))
	if err != nil {
		t.Fatalf("Failed to get network namespace ID: %v", err)
	}

	if db.podStore[id] != podName {
		t.Errorf("Expected podName %s for netnsid %d, got %s", podName, id, db.podStore[id])
	}
}

func TestDB_RemovePodNetns(t *testing.T) {
	db := New()
	podName := "test-pod"
	netnsPath := "/proc/self/ns/net"

	// Simulate adding a pod network namespace
	db.AddPodNetns(podName, netnsPath)

	// Remove the pod network namespace
	db.RemovePodNetns(podName)

	// Check if the pod network namespace was removed correctly
	db.mu.RLock()
	defer db.mu.RUnlock()
	if len(db.podStore) != 0 {
		t.Errorf("Expected 0 pods in podStore, got %d", len(db.podStore))
	}
}

func TestDB_GetPodName(t *testing.T) {
	db := New()
	podName := "test-pod"
	netnsPath := "/proc/self/ns/net"

	// Simulate adding a pod network namespace
	db.AddPodNetns(podName, netnsPath)

	// Get the current process's network namespace ID
	ns, err := netns.GetFromPath(netnsPath)
	if err != nil {
		t.Fatalf("Failed to get network namespace from path: %v", err)
	}
	id, err := netlink.GetNetNsIdByFd(int(ns))
	if err != nil {
		t.Fatalf("Failed to get network namespace ID: %v", err)
	}

	// Get the pod name using the network namespace ID
	retrievedPodName := db.GetPodName(id)

	// Check if the retrieved pod name matches the original pod name
	if retrievedPodName != podName {
		t.Errorf("Expected podName %s, got %s", podName, retrievedPodName)
	}
}

func TestDB_GetPodNetNs(t *testing.T) {
	db := New()
	podName := "test-pod"
	netnsPath := "/proc/self/ns/net"

	// Simulate adding a pod network namespace
	db.AddPodNetns(podName, netnsPath)

	// Get the current process's network namespace ID
	ns, err := netns.GetFromPath(netnsPath)
	if err != nil {
		t.Fatalf("Failed to get network namespace from path: %v", err)
	}
	expectedID, err := netlink.GetNetNsIdByFd(int(ns))
	if err != nil {
		t.Fatalf("Failed to get network namespace ID: %v", err)
	}

	// Get the network namespace ID using the pod name
	retrievedID := db.GetPodNetNs(podName)

	// Check if the retrieved ID matches the expected ID
	if retrievedID != expectedID {
		t.Errorf("Expected netnsid %d, got %d", expectedID, retrievedID)
	}
}

func TestDB_GetPodNetNs_NotFound(t *testing.T) {
	db := New()
	podName := "test-pod"

	// Get the network namespace ID using the pod name
	retrievedID := db.GetPodNetNs(podName)

	// Check if the retrieved ID matches the expected ID
	if retrievedID != -1 {
		t.Errorf("Expected netnsid -1, got %d", retrievedID)
	}
}

func TestDB_netdevToDRAdev(t *testing.T) {
	db := New()
	ifaceName := "lo" // loopback interface

	device, err := db.netdevToDRAdev(ifaceName)
	if err != nil {
		t.Fatalf("netdevToDRAdev failed: %v", err)
	}

	if device.Name != ifaceName {
		t.Errorf("Expected device name %s, got %s", ifaceName, device.Name)
	}

	if device.Basic.Attributes["kind"].StringValue == nil || *device.Basic.Attributes["kind"].StringValue != networkKind {
		t.Errorf("Expected kind %s, got %v", networkKind, device.Basic.Attributes["kind"].StringValue)
	}

	if device.Basic.Attributes["name"].StringValue == nil || *device.Basic.Attributes["name"].StringValue != ifaceName {
		t.Errorf("Expected name %s, got %v", ifaceName, device.Basic.Attributes["name"].StringValue)
	}

	if device.Basic.Attributes["virtual"].BoolValue == nil || !*device.Basic.Attributes["virtual"].BoolValue {
		t.Errorf("Expected virtual true, got %v", device.Basic.Attributes["virtual"].BoolValue)
	}
}
