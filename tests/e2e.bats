#!/usr/bin/env bats

load 'test_helper/bats-support/load'
load 'test_helper/bats-assert/load'

teardown() {
  # Cleanup all dummy devices on worker nodes
  docker exec "$CLUSTER_NAME"-worker bash -c "ip -br link show type fake_iface | awk '{print \$1}' | xargs -I {} ip link delete {}"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip -br link show type fake_iface | awk '{print \$1}' | xargs -I {} ip link delete {}"

  sleep 5
}

setup_bpf_device() {
  local iface_name="$1"
  docker cp "$BATS_TEST_DIRNAME"/dummy_bpf.o "$CLUSTER_NAME"-worker2:/dummy_bpf.o
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link add ${iface_name} type fake_iface"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "tc qdisc add dev ${iface_name} clsact"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "tc filter add dev ${iface_name} ingress bpf direct-action obj dummy_bpf.o sec classifier"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link set up dev ${iface_name}"
}

setup_tcx_filter() {
  local iface_name="$1"
  docker cp "$BATS_TEST_DIRNAME"/dummy_bpf_tcx.o "$CLUSTER_NAME"-worker2:/dummy_bpf_tcx.o
  docker exec "$CLUSTER_NAME"-worker2 bash -c "curl --connect-timeout 5 --retry 3 -L https://github.com/libbpf/bpftool/releases/download/v7.5.0/bpftool-v7.5.0-amd64.tar.gz | tar -xz"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "chmod +x bpftool"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "./bpftool prog load dummy_bpf_tcx.o /sys/fs/bpf/dummy_prog_tcx"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "./bpftool net attach tcx_ingress pinned /sys/fs/bpf/dummy_prog_tcx dev ${iface_name}"
}

@test "dummy interface with IP addresses ResourceClaim" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy0 type fake_iface"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy0"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=pod
  run kubectl exec pod1 -- ip addr show eth99
  assert_success
  assert_output --partial "169.254.169.13"
  run kubectl get resourceclaims dummy-interface-static-ip  -o=jsonpath='{.status.devices[0].networkData.ips[*]}'
  assert_success
  assert_output --partial "169.254.169.13"

  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim.yaml
}

@test "dummy interface with IP addresses ResourceClaimTemplate" {
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link add dummy-template type fake_iface"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip addr add 169.254.169.14/32 dev dummy-template"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaimtemplate.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=MyApp
  POD_NAME=$(kubectl get pods -l app=MyApp -o name)
  run kubectl exec $POD_NAME -- ip addr show dummy-template
  assert_success
  assert_output --partial "169.254.169.14"
  # TODO list the specific resourceclaim and the networkdata
  run kubectl get resourceclaims -o yaml
  assert_success
  assert_output --partial "169.254.169.14"

  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaimtemplate.yaml
}

@test "dummy interface with IP addresses ResourceClaim and routes" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy-route type fake_iface"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy-route"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_route.yaml
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
  assert_equal "$output" "ok"
}


@test "validate advanced network configurations with dummy" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy-adv type fake_iface"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy-adv"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_advanced.yaml

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

  # Cleanup the resources for this test
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_advanced.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
}

# Test case for validating Big TCP configurations.
@test "validate big tcp network configurations on dummy interface" {
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link add bigtcp type fake_iface"
  docker exec "$CLUSTER_NAME"-worker2 bash -c "ip link set up dev bigtcp"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_bigtcp.yaml
  kubectl wait --for=condition=ready pod/pod-bigtcp-test --timeout=300s

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

  # Cleanup the resources for this test
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_bigtcp.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
}


# Test case for validating ebpf attributes are exposed via resource slice.
@test "validate bpf filter attributes" {
  setup_bpf_device dummy0

  run docker exec "$CLUSTER_NAME"-worker2 bash -c "tc filter show dev dummy0 ingress"
  assert_success
  assert_output --partial "dummy_bpf.o:[classifier] direct-action"

  perm_mac=$(docker exec "$CLUSTER_NAME"-worker2 cat /sys/class/net/dummy0/address)

  for attempt in {1..4}; do
    run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath="{.items[0].spec.devices[?(@.attributes.dra\.net/permMac.string=='"$perm_mac"')].attributes.dra\.net/ebpf.bool}"
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
  run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath="{.items[0].spec.devices[?(@.attributes.dra\.net/permMac.string=='"$perm_mac"')].attributes.dra\.net/tcFilterNames.string}"
  assert_success
  assert_output "dummy_bpf.o:[classifier]"
}


@test "validate tcx bpf filter attributes" {
  setup_bpf_device test0

  setup_tcx_filter test0

  run docker exec "$CLUSTER_NAME"-worker2 bash -c "./bpftool net show dev test0"
  assert_success
  assert_output --partial "tcx/ingress handle_ingress prog_id"

  # Wait for the interface to be discovered
  sleep 5

  perm_mac=$(docker exec "$CLUSTER_NAME"-worker2 cat /sys/class/net/test0/address)

  # Validate bpf attribute is true
  run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath="{.items[0].spec.devices[?(@.attributes.dra\.net/permMac.string=='"$perm_mac"')].attributes.dra\.net/ebpf.bool}"
  assert_success
  assert_output "true"

  # Validate bpfName attribute
  run kubectl get resourceslices --field-selector spec.nodeName="$CLUSTER_NAME"-worker2 -o jsonpath="{.items[0].spec.devices[?(@.attributes.dra\.net/permMac.string=='"$perm_mac"')].attributes.dra\.net/tcxProgramNames.string}"
  assert_success
  assert_output "handle_ingress"

  # Unpin the bpf program
  docker exec "$CLUSTER_NAME"-worker2 bash -c "rm -Rf /sys/fs/bpf/dummy_prog_tcx"
}


@test "validate bpf programs are removed" {
  setup_bpf_device ebpf-remove

  setup_tcx_filter ebpf-remove

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_disable_ebpf.yaml
  kubectl wait --for=condition=ready pod/pod-ebpf --timeout=300s

  run kubectl exec pod-ebpf -- ash -c "curl --connect-timeout 5 --retry 3 -L https://github.com/libbpf/bpftool/releases/download/v7.5.0/bpftool-v7.5.0-amd64.tar.gz | tar -xz && chmod +x bpftool"
  assert_success

  run kubectl exec pod-ebpf -- ash -c "./bpftool net show dev ebpf-remove"
  assert_success
  refute_output --partial "tcx/ingress handle_ingress prog_id"
  refute_output --partial "dummy_bpf.o:[classifier]"

  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim_disable_ebpf.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  # Unpin the bpf program
  docker exec "$CLUSTER_NAME"-worker2 bash -c "rm -Rf /sys/fs/bpf/dummy_prog_tcx"
}

# Test case for validating multiple devices allocated to the same pod.
@test "2 dummy interfaces with IP addresses ResourceClaimTemplate" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy-pair-0 type fake_iface"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy-pair-0"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip addr add 169.254.169.13/32 dev dummy-pair-0"

  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy-pair-1 type fake_iface"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dev dummy-pair-1"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip addr add 169.254.169.14/32 dev dummy-pair-1"

  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaimtemplate_double.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=MyApp
  POD_NAME=$(kubectl get pods -l app=MyApp -o name)
  run kubectl exec $POD_NAME -- ip addr show dummy-pair-0
  assert_success
  assert_output --partial "169.254.169.13"
  run kubectl exec $POD_NAME -- ip addr show dummy-pair-1
  assert_success
  assert_output --partial "169.254.169.14"
  run kubectl get resourceclaims -o=jsonpath='{.items[0].status.devices[*]}'
  assert_success
  assert_output --partial "169.254.169.13"
  assert_output --partial "169.254.169.14"

  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/resourceclaimtemplate_double.yaml
}

@test "reapply pod with dummy resource claim" {
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link add dummy8 type fake_iface"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip link set up dummy8"
  docker exec "$CLUSTER_NAME"-worker bash -c "ip addr add 169.254.169.14/32 dev dummy8"

  # Apply the resource claim template and deployment
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/repeatresourceclaimtemplate.yaml
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
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/repeatresourceclaimtemplate.yaml
  kubectl wait --timeout=30s --for=condition=ready pods -l app=reapplyApp
  POD_NAME=$(kubectl get pods -l app=reapplyApp -o name)
  run kubectl exec $POD_NAME -- ip addr show dummy8
  assert_success
  assert_output --partial "169.254.169.14"
  # TODO list the specific resourceclaim and the networkdata
  run kubectl get resourceclaims -o yaml
  assert_success
  assert_output --partial "169.254.169.14"

  kubectl delete -f "$BATS_TEST_DIRNAME"/../examples/repeatresourceclaimtemplate.yaml
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

  # Remove affinity from DraNet DaemonSet to revert it back to original
  kubectl patch daemonset dranet -n kube-system --type='merge' --patch-file=<(cat <<EOF
spec:
  template:
    spec:
      affinity:
EOF
)
  kubectl rollout status ds/dranet --namespace=kube-system
}
