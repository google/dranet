#!/usr/bin/env bats

load 'test_helper/bats-support/load'
load 'test_helper/bats-assert/load'

# ---- GLOBAL CLEANUP ----

teardown() {
  if [[ -z "$BATS_TEST_COMPLETED" || "$BATS_TEST_COMPLETED" -ne 1 ]] && [[ -z "$BATS_TEST_SKIPPED" ]]; then
    dump_debug_info_on_failure
  fi
  cleanup_k8s_resources
  cleanup_dummy_interfaces
  cleanup_bpf_programs
  # The driver is rate limited to updates with interval of atleast 5 seconds. So
  # we need to sleep for an equivalent amount of time to ensure state from a
  # previous test is cleared up and old (non-existent) devices have been removed
  # from the ResourceSlice. This seems to only be an an issue of the test where
  # we create "dummy" interfaces which disappear if the network namespace is
  # deleted.
  sleep 5
}

dump_debug_info_on_failure() {
  echo "--- Test failed. Dumping debug information ---"

  echo "--- DeviceClasses ---"
  for dc in $(kubectl get deviceclass -o name); do
    echo "--- $dc ---"
    kubectl get "$dc" -o yaml
  done

  echo "--- ResourceSlices ---"
  for rs in $(kubectl get resourceslice -o name); do
    echo "--- $rs ---"
    kubectl get "$rs" -o yaml
  done

  echo "--- ResourceClaims ---"
  for rc in $(kubectl get resourceclaim -o name); do
    echo "--- $rc ---"
    kubectl get "$rc" -o yaml
  done

  echo "--- Pods Description ---"
  for pod in $(kubectl get pods -o name); do
    echo "--- $pod ---"
    kubectl describe "$pod"
  done

  echo "--- End of debug information ---"
}

cleanup_k8s_resources() {
  kubectl delete -f "$BATS_TEST_DIRNAME"/../tests/manifests --ignore-not-found --recursive || true
}

cleanup_dummy_interfaces() {
  for node in "$CLUSTER_NAME"-worker "$CLUSTER_NAME"-worker2; do
    docker exec "$node" bash -c '
      for dev in $(ip -br link show type dummy | awk "{print \$1}"); do
        ip link delete "$dev" || echo "Failed to delete $dev"
      done
    '
  done
}

cleanup_bpf_programs() {
  docker exec "$CLUSTER_NAME"-worker2 bash -c "rm -rf /sys/fs/bpf/* || true"
}

# ---- SETUP HELPERS ----

setup_bpf_device() {
  docker cp "$BATS_TEST_DIRNAME"/dummy_bpf.o "$CLUSTER_NAME"-worker2:/dummy_bpf.o
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link add dummy0 type dummy"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "tc qdisc add dev dummy0 clsact"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "tc filter add dev dummy0 ingress bpf direct-action obj dummy_bpf.o sec classifier"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link set up dev dummy0"
}

setup_tcx_filter() {
  docker cp "$BATS_TEST_DIRNAME"/dummy_bpf_tcx.o "$CLUSTER_NAME"-worker2:/dummy_bpf_tcx.o
  docker exec "$CLUSTER_NAME"-worker2 bash -c "curl --connect-timeout 5 --retry 3 -L https://github.com/libbpf/bpftool/releases/download/v7.5.0/bpftool-v7.5.0-amd64.tar.gz | tar -xz"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "chmod +x bpftool"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "./bpftool prog load dummy_bpf_tcx.o /sys/fs/bpf/dummy_prog_tcx"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "./bpftool net attach tcx_ingress pinned /sys/fs/bpf/dummy_prog_tcx dev dummy0"
  # We update the interface to trigger a DRANET driver notification, which
  # speeds up the test. Otherwise, DRANET would need to wait for a full resync
  # (1 minute) to detect changes to attached BPF programs, as netlink
  # notifications don't cover them.
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link set down dev dummy0"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link set up dev dummy0"
}

# ---- TESTS ----

@test "dummy interface with IP addresses ResourceClaim" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy0 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy0"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/resourceclaim.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=pod

  run kubectl exec pod1 -- ip addr show eth99
  assert_success
  assert_output --partial "169.254.169.13"

  run kubectl get resourceclaims dummy-interface-static-ip -o=jsonpath='{.status.devices[0].networkData.ips[*]}'
  assert_success
  assert_output --partial "169.254.169.13"
}

@test "dummy interface with IP addresses ResourceClaim and normalized name" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add mlx5_6 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev mlx5_6"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/resourceclaim.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=pod

  run kubectl exec pod1 -- ip addr show eth99
  assert_success
  assert_output --partial "169.254.169.13"

  run kubectl get resourceclaims dummy-interface-static-ip -o=jsonpath='{.status.devices[0].networkData.ips[*]}'
  assert_success
  assert_output --partial "169.254.169.13"
}

@test "dummy interface with IP addresses ResourceClaimTemplate" {
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link add dummy0 type dummy"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip addr add 169.254.169.14/32 dev dummy0"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/resourceclaimtemplate.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=MyApp
  POD_NAME=$(kubectl get pods -l app=MyApp -o name)
  run kubectl exec $POD_NAME -- ip addr show dummy0
  assert_success
  assert_output --partial "169.254.169.14"
  # TODO list the specific resourceclaim and the networkdata
  run kubectl get resourceclaims -o yaml
  assert_success
  assert_output --partial "169.254.169.14"
}

@test "dummy interface with IP addresses ResourceClaim and routes" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy0 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy0"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/resourceclaim_route.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=pod

  run kubectl exec pod3 -- ip addr show eth99
  assert_success
  assert_output --partial "169.254.169.13"

  run kubectl exec pod3 -- ip route show
  assert_success
  assert_output --partial "169.254.169.0/24 via 169.254.169.1"

  run kubectl get resourceclaims dummy-interface-static-ip-route -o=jsonpath='{.status.devices[0].networkData.ips[*]}'
  assert_success
  assert_output --partial "169.254.169.1"
}

@test "test metric server is up and operating on host" {
  output=$(kubectl \
    run -i test-metrics \
    --image registry.k8s.io/e2e-test-images/agnhost:2.54 \
    --overrides='{"spec": {"hostNetwork": true}}' \
    --restart=Never \
    --command \
    -- sh -c "curl --silent localhost:9177/metrics | grep process_start_time_seconds >/dev/null && echo ok || echo fail")
  assert_equal "$output" "ok"
}


@test "validate advanced network configurations with dummy" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy0 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy0"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/resourceclaim_advanced.yaml

  # Wait for the pod to become ready
  kubectl wait --for=condition=ready pod/pod-advanced-cfg --timeout=30s

  # Validate mtu and hardware address
  run kubectl exec pod-advanced-cfg -- ip addr show dranet0
  assert_success
  assert_output --partial "169.254.169.14/24"
  assert_output --partial "mtu 4321"
  assert_output --partial "00:11:22:33:44:55"

  # Validate ethtool settings inside the pod for interface dranet0
  run kubectl exec pod-advanced-cfg -- ash -c "apk add ethtool && ethtool -k dranet0"
  assert_success
  assert_output --partial "tcp-segmentation-offload: off"
  assert_output --partial "generic-receive-offload: off"
  assert_output --partial "large-receive-offload: off"
}

# Test case for validating Big TCP configurations.
@test "validate big tcp network configurations on dummy interface" {
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link add dummy0 type dummy"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link set up dev dummy0"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/resourceclaim_bigtcp.yaml
  kubectl wait --for=condition=ready pod/pod-bigtcp-test --timeout=120s

  run kubectl exec pod-bigtcp-test -- ip -d link show dranet1
  assert_success

  assert_output --partial "mtu 8896"
  assert_output --partial "gso_max_size 65536"
  assert_output --partial "gro_max_size 65536"
  assert_output --partial "gso_ipv4_max_size 65536"
  assert_output --partial "gro_ipv4_max_size 65536"

  run kubectl exec pod-bigtcp-test -- ash -c "apk add ethtool && ethtool -k dranet1"
  assert_success
  assert_output --partial "tcp-segmentation-offload: on"
  assert_output --partial "generic-receive-offload: on"
  assert_output --partial "large-receive-offload: off"
}


# Test case for validating ebpf attributes are exposed via resource slice.
@test "validate bpf filter attributes" {
  setup_bpf_device

  run docker exec "$CLUSTER_NAME"-worker2 bash -c "tc filter show dev dummy0 ingress"
  assert_success
  assert_output --partial "dummy_bpf.o:[classifier] direct-action"

  for attempt in {1..4}; do
    run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath='{.items[0].spec.devices[?(@.name=="dummy0")].attributes.dra\.net\/ebpf.bool}'
    if [ "$status" -eq 0 ] && [[ "$output" == "true" ]]; then
      break
    fi
    if (( attempt < 4 )); then
      sleep 5
    fi
  done
  assert_success
  assert_output "true"

  # Validate bpfName attribute
  run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath='{.items[0].spec.devices[?(@.name=="dummy0")].attributes.dra\.net\/tcFilterNames.string}'
  assert_success
  assert_output "dummy_bpf.o:[classifier]"
}

@test "validate tcx bpf filter attributes" {
  setup_bpf_device

  setup_tcx_filter

  run docker exec "$CLUSTER_NAME"-worker2 bash -c "./bpftool net show dev dummy0"
  assert_success
  assert_output --partial "tcx/ingress handle_ingress prog_id"

  # Wait for the interface to be discovered
  sleep 5

  # Validate bpf attribute is true
  run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath='{.items[0].spec.devices[?(@.name=="dummy0")].attributes.dra\.net\/ebpf.bool}'
  assert_success
  assert_output "true"

  # Validate bpfName attribute
  run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath='{.items[0].spec.devices[?(@.name=="dummy0")].attributes.dra\.net\/tcxProgramNames.string}'
  assert_success
  assert_output "handle_ingress"
}

@test "validate bpf programs are removed" {
  setup_bpf_device

  setup_tcx_filter

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/resourceclaim_disable_ebpf.yaml
  kubectl wait --for=condition=ready pod/pod-ebpf --timeout=120s

  run kubectl exec pod-ebpf -- ash -c "curl --connect-timeout 5 --retry 3 -L https://github.com/libbpf/bpftool/releases/download/v7.5.0/bpftool-v7.5.0-amd64.tar.gz | tar -xz && chmod +x bpftool"
  assert_success

  run kubectl exec pod-ebpf -- ash -c "./bpftool net show dev dummy0"
  assert_success
  refute_output --partial "tcx/ingress handle_ingress prog_id"
  refute_output --partial "dummy_bpf.o:[classifier]"
}

# Test case for validating multiple devices allocated to the same pod.
@test "2 dummy interfaces with IP addresses ResourceClaimTemplate" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy0 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy0"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip addr add 169.254.169.13/32 dev dummy0"

  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy1 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy1"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip addr add 169.254.169.14/32 dev dummy1"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/resourceclaimtemplate_double.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=MyApp
  POD_NAME=$(kubectl get pods -l app=MyApp -o name)
  run kubectl exec $POD_NAME -- ip addr show dummy0
  assert_success
  assert_output --partial "169.254.169.13"
  run kubectl exec $POD_NAME -- ip addr show dummy1
  assert_success
  assert_output --partial "169.254.169.14"
  run kubectl get resourceclaims -o=jsonpath='{.items[0].status.devices[*]}'
  assert_success
  assert_output --partial "169.254.169.13"
  assert_output --partial "169.254.169.14"
}

@test "reapply pod with dummy resource claim" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy8 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dummy8"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip addr add 169.254.169.14/32 dev dummy8"

  # Apply the resource claim template and deployment
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/repeatresourceclaimtemplate.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=reapplyApp
  POD_NAME=$(kubectl get pods -l app=reapplyApp -o name)
  run kubectl exec $POD_NAME -- ip addr show dummy8
  assert_success
  assert_output --partial "169.254.169.14"
  # TODO list the specific resourceclaim and the networkdata
  run kubectl get resourceclaims -o yaml
  assert_success
  assert_output --partial "169.254.169.14"

  # Delete the deployment and wait for the resource claims to be removed
  kubectl delete deployment/server-deployment-reapply --wait --timeout=30s
  kubectl wait --for delete pod -l app=reapplyApp

  # Reapply the IP, dummy devices do not have the ability to reclaim the IP
  # when moved back into host NS.
  docker exec "$CLUSTER_NAME"-worker bash -c "ip addr add 169.254.169.14/32 dev dummy8"

  # Reapply the deployment, should reclaim the device
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/repeatresourceclaimtemplate.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=reapplyApp
  POD_NAME=$(kubectl get pods -l app=reapplyApp -o name)
  run kubectl exec $POD_NAME -- ip addr show dummy8
  assert_success
  assert_output --partial "169.254.169.14"
  # TODO list the specific resourceclaim and the networkdata
  run kubectl get resourceclaims -o yaml
  assert_success
  assert_output --partial "169.254.169.14"

}

@test "driver should gracefully shutdown when terminated" {
  # node1 will be labeled such that it stops running the dranet pod.
  node1=$(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o jsonpath='{.items[0].metadata.name}')
  kubectl label node "${node1}" e2e-test-do-not-schedule=true
  # node 2 will continue to run the dranet pod.
  node2=$(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o jsonpath='{.items[1].metadata.name}')

  # Add affinity to only schedule on nodes without the
  # "e2e-test-do-not-schedule" label. This allows the pods on the specific node
  # to be deleted (and prevents automatic recreation on it)
  kubectl patch daemonset dranet -n kube-system --type='merge' --patch-file=<(cat <<EOF
spec:
  template:
    spec:
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: e2e-test-do-not-schedule
                operator: DoesNotExist
EOF
)
  kubectl rollout status ds/dranet --namespace=kube-system

  # After graceful shutdown of the driver from node1, the DRA plugin socket
  # files should have been deleted.
  run docker exec "${node1}" test -S /var/lib/kubelet/plugins/dra.net/dra.sock
  assert_failure
  run docker exec "${node1}" test -S /var/lib/kubelet/plugins_registry/dra.net-reg.sock
  assert_failure

  # For comparison, node2 should have the files present since the dranet pod is
  # still runnning on it.
  docker exec "${node2}" test -S /var/lib/kubelet/plugins/dra.net/dra.sock
  docker exec "${node2}" test -S /var/lib/kubelet/plugins_registry/dra.net-reg.sock

  # Remove affinity from DRANET DaemonSet to revert it back to original
  kubectl patch daemonset dranet -n kube-system --type='merge' --patch-file=<(cat <<EOF
spec:
  template:
    spec:
      affinity:
EOF
)
  kubectl rollout status ds/dranet --namespace=kube-system
}

@test "permanent neighbor entry is copied to pod namespace" {
  local NODE_NAME="$CLUSTER_NAME"-worker
  local DUMMY_IFACE="dummy-neigh"
  local NEIGH_IP="192.168.1.1"
  local NEIGH_MAC="00:11:22:33:44:55"
  local NEIGH_IPV6="2001:db8::1"
  local NEIGH_MAC_IPV6="00:aa:bb:cc:dd:ee"

  # Create a dummy interface on the worker node
  docker exec "$NODE_NAME" bash -c "ip link add $DUMMY_IFACE type dummy"
  docker exec "$NODE_NAME" bash -c "ip link set up dev $DUMMY_IFACE"
  docker exec "$NODE_NAME" bash -c "ip addr add 169.254.169.15/32 dev $DUMMY_IFACE"

  # Add a permanent neighbor entry on the worker node
  docker exec "$NODE_NAME" bash -c "ip neigh add $NEIGH_IP lladdr $NEIGH_MAC dev $DUMMY_IFACE nud permanent"
  docker exec "$NODE_NAME" bash -c "ip -6 neigh add $NEIGH_IPV6 lladdr $NEIGH_MAC_IPV6 dev $DUMMY_IFACE nud permanent"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/resourceclaim.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=pod

  # Get the pod name
  POD_NAME=$(kubectl get pods -l app=pod -o name)

  # Verify the neighbor entry inside the pod's network namespace
  run kubectl exec "$POD_NAME" -- ip neigh show
  assert_success
  assert_output --partial "$NEIGH_IP dev eth99 lladdr $NEIGH_MAC PERM"
  assert_output --partial "$NEIGH_IPV6 dev eth99 lladdr $NEIGH_MAC_IPV6 PERM"
}

