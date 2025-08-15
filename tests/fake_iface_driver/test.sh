#!/bin/bash

set -e
set x

# Get the directory where the script is located.
script_dir=$(dirname "$(realpath "$0")")
test_passed=false

# Function to display a failure message.
# This is triggered by the ERR trap.
handle_failure() {
  echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
  echo "!!!         TEST FAILED            !!!"
  echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
  echo "Error on line $1"
}

# Trap ERR signals and call the failure handler.
trap 'handle_failure $LINENO' ERR

# Cleanup function to be called on script exit.
cleanup() {
  echo "Cleaning up..."
  # Delete the network namespace if it exists.
  sudo ip netns list | grep -q temp-ns && sudo ip netns del temp-ns
  # Unload the kernel module if it's loaded.
  lsmod | grep -q fake_iface && sudo rmmod fake_iface
  # Clean the build artifacts.
  make -C "${script_dir}" clean
  echo "Cleanup complete."

  if [ "$test_passed" = true ]; then
    echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
    echo "!!!         TEST PASSED            !!!"
    echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
  else
    echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
    echo "!!!      FINAL STATUS: FAILED      !!!"
    echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
  fi
}

# Register the cleanup function to be called on EXIT.
trap cleanup EXIT

# Build the kernel module.
make -C "${script_dir}"

# Load the kernel module.

sudo rmmod "${script_dir}/build/fake_iface.ko" || true
sudo insmod "${script_dir}/build/fake_iface.ko"

# Create the fake interface.
sudo ip link add test0 type fake_iface

# Configure the IP address and MAC address.
sudo ip addr add 10.0.0.5/32 dev test0
sudo ip link set dev test0 address aa:bb:cc:dd:ee:ff

# Verify that the MAC address was set correctly.
ip addr show dev test0 | grep -q "link/ether aa:bb:cc:dd:ee:ff"
echo "MAC address successfully set."

# Get and verify the initial permanent MAC address.
initial_mac_address=$(ethtool -P test0 | awk '{print $3}')
if [[ -z "${initial_mac_address}" ]]; then
  echo "Error: Failed to retrieve initial permanent MAC address."
  exit 1
fi
echo "Initial permanent MAC address is: ${initial_mac_address}"

# Create a new network namespace.
sudo ip netns add temp-ns

# Move the interface to the new namespace.
sudo ip link set dev test0 netns temp-ns

# Verify the permanent MAC address using ethtool inside the namespace.
mac_address_in_ns=$(sudo ip netns exec temp-ns ethtool -P test0 | awk '{print $3}')
if [[ "${mac_address_in_ns}" != "${initial_mac_address}" ]]; then
  echo "Error: MAC address mismatch between host and namespace."
  echo "Host MAC: ${initial_mac_address}, Namespace MAC: ${mac_address_in_ns}"
  exit 1
fi
echo "Permanent MAC address verified to be consistent."

# Bring the interface up inside the namespace.
sudo ip netns exec temp-ns ip link set up dev test0

# Verify that the interface is not down.
if sudo ip netns exec temp-ns ip link show dev test0 | grep -q "state DOWN"; then
  echo "Error: Interface is in DOWN state after being set up."
  echo "Current interface details:"
  sudo ip netns exec temp-ns ip link show dev test0
  exit 1
fi
echo "Interface is not in DOWN state."

echo "Test completed successfully!"
test_passed=true
