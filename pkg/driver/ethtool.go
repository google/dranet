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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/dranet/pkg/apis"

	"github.com/mdlayher/genetlink"
	"github.com/mdlayher/netlink"

	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"
)

// off_flag_def
// https://git.kernel.org/pub/scm/network/ethtool/ethtool.git/tree/common.c#n51
// OffloadFlagDefinition is the Go equivalent of the C struct off_flag_def.
// We keep this struct as the source of truth for building the map.
type OffloadFlagDefinition struct {
	ShortName     string
	LongName      string
	KernelPattern string
}

// offloadFlagDefs is the translated slice of legacy feature definitions.
var offloadFlagDefs = []OffloadFlagDefinition{
	{"rx", "rx-checksumming", "rx-checksum"},
	{"tx", "tx-checksumming", "tx-checksum-*"},
	{"sg", "scatter-gather", "tx-scatter-gather*"},
	{"tso", "tcp-segmentation-offload", "tx-tcp*-segmentation"},
	{"ufo", "udp-fragmentation-offload", "tx-udp-fragmentation"},
	{"gso", "generic-segmentation-offload", "tx-generic-segmentation"},
	{"gro", "generic-receive-offload", "rx-gro"},
	{"lro", "large-receive-offload", "rx-lro"},
	{"rxvlan", "rx-vlan-offload", "rx-vlan-hw-parse"},
	{"txvlan", "tx-vlan-offload", "tx-vlan-hw-insert"},
	{"ntuple", "ntuple-filters", "rx-ntuple-filter"},
	{"rxhash", "receive-hashing", "rx-hashing"},
}

// It maps both short and long aliases to their corresponding match pattern.
var legacyFeaturePatterns map[string]string

// build the map
func init() {
	legacyFeaturePatterns = make(map[string]string)
	for _, def := range offloadFlagDefs {
		// Note: The pattern is a shell-style "glob", not a true regex.
		legacyFeaturePatterns[def.ShortName] = def.KernelPattern
		legacyFeaturePatterns[def.LongName] = def.KernelPattern
	}
}

// https://docs.kernel.org/networking/ethtool-netlink.html#features-get
// ETHTOOL_A_FEATURES_HW
// ETHTOOL_A_FEATURES_WANTED
// ETHTOOL_A_FEATURES_ACTIVE
// ETHTOOL_A_FEATURES_NOCHANGE
type ethtoolFeatures struct {
	hardware map[string]bool
	wanted   map[string]bool
	active   map[string]bool
	nochange map[string]bool
}

func (e ethtoolFeatures) Get(name string) []string {
	// check if it exists and is not an alias
	if _, ok := e.hardware[name]; ok {
		return []string{name}
	}
	// it can be an alias or multiple features
	matchedFeatures := []string{}
	pattern, ok := legacyFeaturePatterns[name]
	if !ok {
		return matchedFeatures
	}
	for featureName := range e.hardware {
		matched, _ := filepath.Match(pattern, featureName)
		if matched {
			matchedFeatures = append(matchedFeatures, featureName)
		}
	}
	return matchedFeatures
}

// String provides a pretty-printed, sorted list of all feature maps.
func (e ethtoolFeatures) String() string {
	var output strings.Builder

	// This helper function formats and appends a single map to the output string.
	appendMap := func(title string, features map[string]bool) {
		if len(features) == 0 {
			return
		}

		// Get and sort the keys for consistent output order.
		keys := make([]string, 0, len(features))
		for k := range features {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// Write the formatted output for this map.
		fmt.Fprintf(&output, "%s:\n", title)
		for _, key := range keys {
			msg := fmt.Sprintf("  %s: %t", key, features[key])

			// Annotate if the feature is marked as fixed.
			if _, ok := e.nochange[key]; ok {
				msg += " [fixed]"
			}
			fmt.Fprintln(&output, msg)
		}
		fmt.Fprintln(&output) // Add a blank line for spacing between maps.
	}

	// Print each map in a structured and sorted way.
	appendMap("Hardware-supported features", e.hardware)
	appendMap("Wanted features", e.wanted)
	appendMap("Active features", e.active)
	appendMap("No change features", e.nochange)

	return output.String()
}

type ethtoolClient struct {
	conn     *genetlink.Conn
	familyID uint16
}

// newEthtoolClient handles the initial setup and validation.
func newEthtoolClient(netNS int) (*ethtoolClient, error) {
	c, err := genetlink.Dial(&netlink.Config{
		Strict: true,
		NetNS:  netNS,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to dial generic netlink: %w", err)
	}

	family, err := c.GetFamily(unix.ETHTOOL_GENL_NAME)
	if err != nil {
		// Clean up the connection if family check fails.
		_ = c.Close()
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%q family not available", unix.ETHTOOL_GENL_NAME)
		}
		return nil, fmt.Errorf("failed to query for family: %w", err)
	}

	return &ethtoolClient{
		conn:     c,
		familyID: family.ID,
	}, nil
}

// Close wraps the underlying connection's Close method.
func (c *ethtoolClient) Close() {
	c.conn.Close()
}

// GetFeatures retrieves the device features for a given interface.
func (c *ethtoolClient) GetFeatures(ifaceName string) (*ethtoolFeatures, error) {
	msgs, err := c.execute(
		unix.ETHTOOL_MSG_FEATURES_GET,
		unix.ETHTOOL_A_FEATURES_HEADER,
		ifaceName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to execute FEATURES_GET command: %w", err)
	}

	ethFeatures := &ethtoolFeatures{}
	// The feature flags are nested inside ETHTOOL_A_FEATURES_HARDWARE.
	// We need to parse the response to find it.
	for _, msg := range msgs {
		ad, err := netlink.NewAttributeDecoder(msg.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to create attribute decoder: %w", err)
		}
		var parseErr error
		// Iterate through top-level attributes.
		for ad.Next() {
			switch ad.Type() {
			case unix.ETHTOOL_A_FEATURES_HW:
				ad.Nested(func(innerAd *netlink.AttributeDecoder) error {
					ethFeatures.hardware, parseErr = parseBitset(innerAd)
					return parseErr
				})
			case unix.ETHTOOL_A_FEATURES_WANTED:
				ad.Nested(func(innerAd *netlink.AttributeDecoder) error {
					ethFeatures.wanted, parseErr = parseBitset(innerAd)
					return parseErr
				})
			case unix.ETHTOOL_A_FEATURES_ACTIVE:
				ad.Nested(func(innerAd *netlink.AttributeDecoder) error {
					ethFeatures.active, parseErr = parseBitset(innerAd)
					return parseErr
				})
			case unix.ETHTOOL_A_FEATURES_NOCHANGE:
				ad.Nested(func(innerAd *netlink.AttributeDecoder) error {
					ethFeatures.nochange, parseErr = parseBitset(innerAd)
					return parseErr
				})
			default:
				continue
			}
		}
		if err := ad.Err(); err != nil {
			return nil, fmt.Errorf("feature attribute decoder error: %w", err)
		}
	}
	return ethFeatures, nil
}

// GetPrivateFlags retrieves the device-specific private flags.
func (c *ethtoolClient) GetPrivateFlags(ifaceName string) (map[string]bool, error) {
	// For private flags, the ETHTOOL_A_PRIVFLAGS_FLAGS attribute directly contains the bitset.
	// The structure is: Message -> ETHTOOL_A_PRIVFLAGS_FLAGS (nested) -> Bitset attributes
	msgs, err := c.execute(
		unix.ETHTOOL_MSG_PRIVFLAGS_GET,
		unix.ETHTOOL_A_PRIVFLAGS_HEADER,
		ifaceName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to execute PRIVFLAGS_GET command: %w", err)
	}
	if len(msgs) == 0 {
		return nil, errors.New("no private flag data in response")
	}

	allFlags := make(map[string]bool)

	for _, msg := range msgs {
		ad, err := netlink.NewAttributeDecoder(msg.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to create attribute decoder for a message: %w", err)
		}

		// Iterate through top-level attributes in the message.
		for ad.Next() {
			if ad.Type() == unix.ETHTOOL_A_PRIVFLAGS_FLAGS {
				// The bitset is nested inside ETHTOOL_A_PRIVFLAGS_FLAGS.
				ad.Nested(func(innerAd *netlink.AttributeDecoder) error {
					var parseErr error
					allFlags, parseErr = parseBitset(innerAd)
					return parseErr
				})
				return allFlags, nil
			}
		}
		if err := ad.Err(); err != nil {
			return nil, fmt.Errorf("private flags attribute decoder error: %w", err)
		}
	}
	// do not fail if there are not private flags
	return allFlags, nil
}

// SetFeatures sets the device features for a given interface.
func (c *ethtoolClient) SetFeatures(ifaceName string, featuresToSet map[string]bool) error {
	features, err := c.executeSet(
		unix.ETHTOOL_MSG_FEATURES_SET,
		unix.ETHTOOL_A_FEATURES_HEADER,
		ifaceName,
		unix.ETHTOOL_A_FEATURES_WANTED,
		featuresToSet,
	)
	if err != nil {
		return err
	}
	klog.V(4).Infof("SetFeatures for %s result %s", ifaceName, features)

	// ETHTOOL_A_FEATURES_WANTED reports the difference between client request and actual result: mask consists of bits which differ between requested features and result (dev->features after the operation)
	// value consists of values of these bits in the request (i.e. negated values from resulting features)
	if len(features.wanted) > 0 {
		return fmt.Errorf("could not set the following features: %#v", features.wanted)
	}
	// ETHTOOL_A_FEATURES_ACTIVE reports the difference between old and new dev->features: mask
	// consists of bits which have changed, values are their values in new dev->features (after the operation).
	if len(features.active) != len(featuresToSet) {
		klog.V(2).Infof("not all features changed, desired: %#v active: %#v", featuresToSet, features.active)
	}
	return nil
}

// SetPrivateFlags sets the device-specific private flags.
func (c *ethtoolClient) SetPrivateFlags(ifaceName string, flagsToSet map[string]bool) error {
	_, err := c.executeSet(
		unix.ETHTOOL_MSG_PRIVFLAGS_SET,
		unix.ETHTOOL_A_PRIVFLAGS_HEADER,
		ifaceName,
		unix.ETHTOOL_A_PRIVFLAGS_FLAGS,
		flagsToSet,
	)
	return err
}

// executeSet handles commands that set flags.
// It encodes a header with the interface name and a data payload containing the bitset of flags.
func (c *ethtoolClient) executeSet(cmd uint8, headerAttributeType uint16, ifaceName string, dataPayloadAttributeType uint16, flagsToSet map[string]bool) (*ethtoolFeatures, error) {
	ae := netlink.NewAttributeEncoder()

	// Encode the header (e.g., ETHTOOL_A_FEATURES_HEADER or ETHTOOL_A_PRIVFLAGS_HEADER)
	ae.Nested(headerAttributeType, func(nae *netlink.AttributeEncoder) error {
		nae.String(unix.ETHTOOL_A_HEADER_DEV_NAME, ifaceName)
		return nil
	})

	// Encode the data payload (e.g., ETHTOOL_A_FEATURES_WANTED or ETHTOOL_A_PRIVFLAGS_FLAGS)
	ae.Nested(dataPayloadAttributeType, func(nae *netlink.AttributeEncoder) error {
		nae.Flag(unix.ETHTOOL_A_BITSET_NOMASK, false)
		nae.Nested(unix.ETHTOOL_A_BITSET_BITS, func(nnae *netlink.AttributeEncoder) error {
			for name, active := range flagsToSet {
				nnae.Nested(unix.ETHTOOL_A_BITSET_BITS_BIT, func(bitEncoder *netlink.AttributeEncoder) error {
					bitEncoder.String(unix.ETHTOOL_A_BITSET_BIT_NAME, name)
					bitEncoder.Flag(unix.ETHTOOL_A_BITSET_BIT_VALUE, active)
					return nil
				})
			}
			return nil
		})
		return nil
	})

	reqData, err := ae.Encode()
	if err != nil {
		return nil, fmt.Errorf("failed to encode attributes for set operation: %w", err)
	}

	req := genetlink.Message{
		Header: genetlink.Header{Command: cmd, Version: unix.ETHTOOL_GENL_VERSION},
		Data:   reqData,
	}

	msgs, err := c.conn.Execute(req, c.familyID, netlink.Request|netlink.Acknowledge)
	if err != nil {
		return nil, fmt.Errorf("failed to execute set command %d: %w", cmd, err)
	}
	// ETHTOOL_MSG_PRIVFLAGS_SET does not return anything
	if cmd == unix.ETHTOOL_MSG_PRIVFLAGS_SET {
		return nil, nil
	}
	ethFeatures := &ethtoolFeatures{}
	// The feature flags are nested inside ETHTOOL_A_FEATURES_HARDWARE.
	// We need to parse the response to find it.
	for _, msg := range msgs {
		ad, err := netlink.NewAttributeDecoder(msg.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to create attribute decoder: %w", err)
		}
		var parseErr error
		// Iterate through top-level attributes.
		for ad.Next() {
			switch ad.Type() {
			case unix.ETHTOOL_A_FEATURES_HW:
				ad.Nested(func(innerAd *netlink.AttributeDecoder) error {
					ethFeatures.hardware, parseErr = parseBitset(innerAd)
					return parseErr
				})
			case unix.ETHTOOL_A_FEATURES_WANTED:
				ad.Nested(func(innerAd *netlink.AttributeDecoder) error {
					ethFeatures.wanted, parseErr = parseBitset(innerAd)
					return parseErr
				})
			case unix.ETHTOOL_A_FEATURES_ACTIVE:
				ad.Nested(func(innerAd *netlink.AttributeDecoder) error {
					ethFeatures.active, parseErr = parseBitset(innerAd)
					return parseErr
				})
			case unix.ETHTOOL_A_FEATURES_NOCHANGE:
				ad.Nested(func(innerAd *netlink.AttributeDecoder) error {
					ethFeatures.nochange, parseErr = parseBitset(innerAd)
					return parseErr
				})
			}
		}
		if err := ad.Err(); err != nil {
			return nil, fmt.Errorf("feature attribute decoder error: %w", err)
		}
	}
	return ethFeatures, nil
}

// 4. A single, generic execute method to avoid code duplication.
// It builds and sends the request, returning the kernel's response.
func (c *ethtoolClient) execute(cmd uint8, headerType uint16, ifaceName string) ([]genetlink.Message, error) {
	ae := netlink.NewAttributeEncoder()
	ae.Nested(headerType, func(nae *netlink.AttributeEncoder) error {
		nae.String(unix.ETHTOOL_A_HEADER_DEV_NAME, ifaceName)
		return nil
	})

	reqData, err := ae.Encode()
	if err != nil {
		return nil, fmt.Errorf("failed to encode attributes: %w", err)
	}

	req := genetlink.Message{
		Header: genetlink.Header{
			Command: cmd,
			Version: unix.ETHTOOL_GENL_VERSION,
		},
		Data: reqData,
	}

	return c.conn.Execute(req, c.familyID, netlink.Request)
}

// parseBitset decodes a complete set of ethtool bitset attributes.
func parseBitset(ad *netlink.AttributeDecoder) (map[string]bool, error) {
	flags := make(map[string]bool)
	for ad.Next() {
		// The actual flags are nested inside the ETHTOOL_A_BITSET_BITS attribute.
		if ad.Type() == unix.ETHTOOL_A_BITSET_BITS {
			// Pass the nested decoder to the next level of parsing.
			ad.Nested(func(nad *netlink.AttributeDecoder) error {
				for nad.Next() {
					if nad.Type() == unix.ETHTOOL_A_BITSET_BITS_BIT {
						name, active, err := parseBit(nad)
						if err != nil {
							return err
						}
						if name != "" {
							flags[name] = active
						}
					}
				}
				return nad.Err()
			})
			return flags, nil
		}
	}
	return flags, ad.Err()
}

// parseBit decodes a single flag (a bit) from the bitset.
func parseBit(ad *netlink.AttributeDecoder) (name string, active bool, err error) {
	ad.Nested(func(nnad *netlink.AttributeDecoder) error {
		for nnad.Next() {
			switch nnad.Type() {
			case unix.ETHTOOL_A_BITSET_BIT_NAME:
				name = nnad.String()
			case unix.ETHTOOL_A_BITSET_BIT_VALUE:
				// The presence of this attribute indicates the flag is active.
				active = true
			}
		}
		return nnad.Err()
	})
	return name, active, err
}

// applyEthtoolConfig applies ethtool configurations (features, private flags) to an interface
// within a specified network namespace.
func applyEthtoolConfig(containerNsPath string, ifName string, config *apis.EthtoolConfig) error {
	if config == nil {
		klog.V(2).Infof("No ethtool configuration to apply for %s in ns %s", ifName, containerNsPath)
		return nil
	}

	hasFeatures := len(config.Features) > 0
	hasPrivateFlags := len(config.PrivateFlags) > 0
	if !hasFeatures && !hasPrivateFlags {
		klog.V(2).Infof("Ethtool configuration for %s in ns %s is empty (no features or private flags).", ifName, containerNsPath)
		return nil
	}

	targetNs, err := netns.GetFromPath(containerNsPath)
	if err != nil {
		return fmt.Errorf("failed to get target network namespace from path %s: %w", containerNsPath, err)
	}
	defer targetNs.Close()

	client, err := newEthtoolClient(int(targetNs))
	if err != nil {
		return fmt.Errorf("failed to create ethtool client in namespace %s: %w", containerNsPath, err)
	}
	defer client.Close()

	var errorList []error

	if hasFeatures {
		klog.V(2).Infof("Applying ethtool features for %s in ns %s: %v", ifName, containerNsPath, config.Features)
		if err := client.SetFeatures(ifName, config.Features); err != nil {
			errorList = append(errorList, fmt.Errorf("failed to set ethtool features for %s: %w", ifName, err))
		}
	}

	if hasPrivateFlags {
		klog.V(2).Infof("Applying ethtool private flags for %s in ns %s: %v", ifName, containerNsPath, config.PrivateFlags)
		if err := client.SetPrivateFlags(ifName, config.PrivateFlags); err != nil {
			errorList = append(errorList, fmt.Errorf("failed to set ethtool private flags for %s: %w", ifName, err))
		}
	}

	return errors.Join(errorList...)
}
