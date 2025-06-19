#!/usr/bin/env bats

# Define global variables (for use in teardown)
setup() {
  namespace="kube-system"
  ds_name="dranet"
  node_selector_key="disable"
  node_selector_value="true"

  # Save the original nodeSelector
  original_selector=$(kubectl get ds "$ds_name" -n "$namespace" -o jsonpath='{.spec.template.spec.nodeSelector}' | jq -c .)
}

# Always run this after the test (pass or fail)
teardown() {
  # Restore the original nodeSelector
  kubectl patch ds "$ds_name" -n "$namespace" \
    --type='merge' \
    -p="{\"spec\": {\"template\": {\"spec\": {\"nodeSelector\": $original_selector}}}}"
}

@test "dummy interface with IP addresses ResourceClaim" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy0 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy0"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=pod
  run kubectl exec pod1 -- ip addr show eth99
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.13"* ]]
  run kubectl get resourceclaims dummy-interface-static-ip  -o=jsonpath='{.status.devices[0].networkData.ips[*]}'
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.13"* ]]

  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim.yaml
}


@test "dummy interface with IP addresses ResourceClaim and normalized name" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add mlx5_6 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev mlx5_6"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=pod
  run kubectl exec pod1 -- ip addr show eth99
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.13"* ]]
  run kubectl get resourceclaims dummy-interface-static-ip  -o=jsonpath='{.status.devices[0].networkData.ips[*]}'
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.13"* ]]

  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim.yaml
}

@test "dummy interface with IP addresses ResourceClaimTemplate" {
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link add dummy1 type dummy"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip addr add 169.254.169.14/32 dev dummy1"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaimtemplate.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=MyApp
  POD_NAME=$(kubectl get pods -l app=MyApp -o name)
  run kubectl exec $POD_NAME -- ip addr show dummy1
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.14"* ]]
  # TODO list the specific resourceclaim and the networkdata
  run kubectl get resourceclaims -o yaml
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.14"* ]]

  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaimtemplate.yaml
}

@test "dummy interface with IP addresses ResourceClaim and routes" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy2 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy2"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_route.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=pod
  run kubectl exec pod3 -- ip addr show eth99
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.13"* ]]

  run kubectl exec pod3 -- ip route show
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.0/24 via 169.254.169.1"* ]]

  run kubectl get resourceclaims dummy-interface-static-ip-route -o=jsonpath='{.status.devices[0].networkData.ips[*]}'
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.1"* ]]

  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_route.yaml
}


@test "test metric server is up and operating on host" {
  output=$(kubectl \
    run -i test-metrics \
    --image registry.k8s.io/e2e-test-images/agnhost:2.54 \
    --overrides='{"spec": {"hostNetwork": true}}' \
    --restart=Never \
    --command \
    -- sh -c "curl --silent localhost:9177/metrics | grep process_start_time_seconds >/dev/null && echo ok || echo fail")
  test "$output" = "ok"
}


@test "validate advanced network configurations with dummy" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy3 type dummy"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy3"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_advanced.yaml

  # Wait for the pod to become ready
  kubectl wait --for=condition=ready pod/pod-advanced-cfg --timeout=30s

  # Validate mtu and hardware address
  run kubectl exec pod-advanced-cfg -- ip addr show dranet0
  [ "$status" -eq 0 ]
  [[ "$output" == *"169.254.169.14/24"* ]]
  [[ "$output" == *"mtu 4321"* ]]
  [[ "$output" == *"00:11:22:33:44:55"* ]]

  # Validate ethtool settings inside the pod for interface dranet0
  run kubectl exec pod-advanced-cfg -- ash -c "apk add ethtool && ethtool -k dranet0"
  [ "$status" -eq 0 ]
  [[ "$output" == *"tcp-segmentation-offload: off"* ]]
  [[ "$output" == *"generic-receive-offload: off"* ]]
  [[ "$output" == *"large-receive-offload: off"* ]]

  # Cleanup the resources for this test
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_advanced.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
}

# Test case for validating Big TCP configurations.
@test "validate big tcp network configurations on dummy interface" {
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link add dummy4 type dummy"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link set up dev dummy4"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_bigtcp.yaml
  kubectl wait --for=condition=ready pod/pod-bigtcp-test --timeout=300s

  run kubectl exec pod-bigtcp-test -- ip -d link show dranet1
  [ "$status" -eq 0 ]
  [[ "$output" == *"mtu 8896"* ]]
  [[ "$output" == *"gso_max_size 65536"* ]]
  [[ "$output" == *"gro_max_size 65536"* ]]
  [[ "$output" == *"gso_ipv4_max_size 65536"* ]]
  [[ "$output" == *"gro_ipv4_max_size 65536"* ]]

  run kubectl exec pod-bigtcp-test -- ash -c "apk add ethtool && ethtool -k dranet1"
  [ "$status" -eq 0 ]
  [[ "$output" == *"tcp-segmentation-offload: on"* ]]
  [[ "$output" == *"generic-receive-offload: on"* ]]
  [[ "$output" == *"large-receive-offload: off"* ]]

  # Cleanup the resources for this test
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_bigtcp.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
}


# Test case for validating ebpf attributes are exposed via resource slice.
# TODO use tcx hooks too
@test "validate bpf filter attributes" {
  docker cp "$BATS_TEST_DIRNAME"/dummy_bpf.o "$CLUSTER_NAME"-worker2:/dummy_bpf.o
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link add dummy5 type dummy"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link set up dev dummy5"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "tc qdisc add dev dummy5 clsact"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "tc filter add dev dummy5 ingress bpf direct-action obj dummy_bpf.o sec classifier"

  run docker exec "$CLUSTER_NAME"-worker2 bash -c "tc filter show dev dummy5 ingress"
  [ "$status" -eq 0 ]
  [[ "$output" == *"dummy_bpf.o:[classifier] direct-action"* ]]

  # Wait for the interface to be discovered
  sleep 5

  # Validate bpf attribute is true
  run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath='{.items[0].spec.devices[?(@.name=="dummy5")].attributes.dra\.net\/ebpf.bool}'
  [ "$status" -eq 0 ]
  [[ "$output" == "true" ]]

  # Validate bpfName attribute
  run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath='{.items[0].spec.devices[?(@.name=="dummy5")].attributes.dra\.net\/tcFilterNames.string}'
  [ "$status" -eq 0 ]
  [[ "$output" == "dummy_bpf.o:[classifier]" ]]
}


@test "verify resourceslices are cleaned up after driver removed" {
  # Patch the DaemonSet with a dummy nodeSelector to disable it
  run kubectl patch ds "$ds_name" -n "$namespace" \
    --type='merge' \
    -p="{\"spec\": {\"template\": {\"spec\": {\"nodeSelector\": {\"$node_selector_key\": \"$node_selector_value\"}}}}}"
  [ "$status" -eq 0 ]

  # Wait for pods to be deleted
  run kubectl wait --for=delete pod -l app=dranet -n "$namespace" --timeout=60s
  [ "$status" -eq 0 ]

  # Confirm no DraNet pods left
  run kubectl get pods -n "$namespace" -l app=dranet --no-headers
  [ "$status" -ne 0 ] || [ -z "$output" ]

  # Confirm ResourceSlices are gone
  run kubectl get resourceslices -A --no-headers
  [[ "$output" != *"dranet"* ]]
}