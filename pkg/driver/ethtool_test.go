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

package driver

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"testing"

	"github.com/google/dranet/internal/nlwrap"
	"github.com/google/dranet/pkg/apis"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func Test_applyEthtoolConfig(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Test requires root privileges.")
	}

	origns, err := netns.Get()
	if err != nil {
		t.Fatalf("unexpected error trying to get namespace: %v", err)
	}
	defer origns.Close()

	rndString := make([]byte, 4)
	_, err = rand.Read(rndString)
	if err != nil {
		t.Errorf("fail to generate random name: %v", err)
	}
	nsName := fmt.Sprintf("ns%x", rndString)
	testNS, err := netns.NewNamed(nsName)
	if err != nil {
		t.Fatalf("Failed to create network namespace: %v", err)
	}
	defer netns.DeleteNamed(nsName)
	defer testNS.Close()

	// Switch back to the original namespace
	netns.Set(origns)

	// Create a dummy interface in the test namespace
	nhNs, err := nlwrap.NewHandleAt(testNS)
	if err != nil {
		t.Fatalf("fail to open netlink handle: %v", err)
	}
	defer nhNs.Close()

	loLink, err := nhNs.LinkByName("lo")
	if err != nil {
		t.Fatalf("Failed to get loopback interface: %v", err)
	}
	if err := nhNs.LinkSetUp(loLink); err != nil {
		t.Fatalf("Failed to set up loopback interface: %v", err)
	}

	ifaceName := "dummy0"
	// Create a veth pair
	la := netlink.NewLinkAttrs()
	la.Name = ifaceName
	la.Namespace = netlink.NsFd(int(testNS))
	link := &netlink.Dummy{
		LinkAttrs: la,
	}
	if err := nhNs.LinkAdd(link); err != nil {
		t.Fatalf("Failed to add dummy link %s  in ns %s: %v", ifaceName, nsName, err)
	}

	if err := nhNs.LinkSetUp(link); err != nil {
		t.Fatalf("Failed to add veth link %s in ns %s: %v", ifaceName, nsName, err)
	}

	client, err := newEthtoolClient(int(testNS))
	if err != nil {
		t.Fatalf("failed to create ethtool client in namespace %s: %v", nsName, err)
	}
	defer client.Close()

	deviceFeatures, err := client.GetFeatures(ifaceName)
	if err != nil {
		t.Fatalf("can not get features: %v", err)
	}

	// t.Logf("Features: %s", features)

	// check against ethtool -k
	var ethtoolCmdFeatures map[string]bool
	func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := netns.Set(testNS)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("ethtool", "-k", ifaceName)

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("not able to use ethtool from namespace: %v", err)
		}
		ethtoolCmdFeatures = ParseEthtoolFeatures(string(output))

		// Switch back to the original namespace
		err = netns.Set(origns)
		if err != nil {
			t.Fatal(err)
		}
	}()

	/*
		// does not work with dummy interface
		privateFlags, err := client.GetPrivateFlags(ifaceName)
		if err != nil {
			t.Logf("can not get privateFlags: %v", err)
		}
	*/

	// flip the config for the following config
	highdmaValue := ethtoolCmdFeatures["highdma"]
	rxGroListValue := ethtoolCmdFeatures["rx-gro-list"]

	// Define the ethtool configuration to apply
	config := &apis.EthtoolConfig{
		Features: map[string]bool{
			"highdma":                  !highdmaValue,
			"rx-gro-list":              !rxGroListValue,
			"tcp-segmentation-offload": false,
			"generic-receive-offload":  false,
			"large-receive-offload":    false,
		},
	}

	// translate features to the actual kernel names
	ethtoolFeatures := map[string]bool{}
	for feature, value := range config.Features {
		aliases := deviceFeatures.Get(feature)
		if len(aliases) == 0 {
			t.Errorf("feature %s not supported by interface", feature)
			continue
		}
		for _, alias := range aliases {
			ethtoolFeatures[alias] = value
		}
	}
	config.Features = ethtoolFeatures

	t.Logf("EthtoolConfig %#v", config.Features)

	// Apply the ethtool configuration
	err = applyEthtoolConfig(path.Join("/run/netns", nsName), ifaceName, config)
	if err != nil {
		t.Fatalf("applyEthtoolConfig failed: %v", err)
	}

	// Check features
	_, err = client.GetFeatures(ifaceName)
	if err != nil {
		t.Fatalf("Failed to get features after applying config: %v", err)
	}

	// check against ethtool -k
	func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := netns.Set(testNS)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("ethtool", "-k", ifaceName)

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("not able to use ethtool from namespace: %v", err)
		}
		ethtoolCmdFeatures = ParseEthtoolFeatures(string(output))
		// Switch back to the original namespace
		err = netns.Set(origns)
		if err != nil {
			t.Fatal(err)
		}
	}()

	for name, expectedState := range config.Features {
		actualState := ethtoolCmdFeatures[name]
		if actualState != expectedState {
			t.Errorf("Feature %s: expected %v, got %v", name, expectedState, actualState)
		}
	}

	/*
		// does not work with dummy interface
		appliedPrivateFlags, err := client.GetPrivateFlags(ifaceName)
		if err != nil {
			t.Errorf("Failed to get private flags after applying config: %v", err)
		}
	*/

	// Fail to update fixed features
	config = &apis.EthtoolConfig{
		Features: map[string]bool{
			"rx-vlan-filter":  true,
			"hsr-dup-offload": true,
		},
	}

	// Apply the ethtool configuration
	err = applyEthtoolConfig(path.Join("/run/netns", nsName), ifaceName, config)
	if err == nil {
		t.Fatalf("applyEthtoolConfig expected to fail: %v", err)
	}
}

func ParseEthtoolFeatures(output string) map[string]bool {
	features := make(map[string]bool)
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		// Split the line at the colon to separate the feature name from its value.
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			// Skip lines that don't contain a colon, like the header or empty lines.
			continue
		}

		// Clean up the feature name by trimming whitespace.
		name := strings.TrimSpace(parts[0])

		// The value part may contain the state ("on"/"off") and a "[fixed]" tag.
		// We can split by space and just take the first element.
		valuePart := strings.TrimSpace(parts[1])
		valueFields := strings.Fields(valuePart)
		if len(valueFields) == 0 {
			continue
		}
		valueStr := valueFields[0]

		// Convert the string state "on" or "off" to a boolean.
		var state bool
		switch valueStr {
		case "on":
			state = true
		case "off":
			state = false
		default:
			continue
		}

		features[name] = state
	}

	return features
}
