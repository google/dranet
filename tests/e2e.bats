#!/usr/bin/env bats

@test "dummy interface" {
  cat "$BATS_TEST_DIRNAME"/../examples/add_dummy_iface.sh | docker exec -i "$CLUSTER_NAME"-worker bash
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../examples/resourceclaim.yaml
  kubectl wait --timeout=2m --for=condition=ready pods -l app=pod
  kubectl exec -it pod1 -- ip link show dummy0
}
