#!/bin/bash

set -eu

function setup_suite {
  export BATS_TEST_TIMEOUT=150
  # Define the name of the kind cluster
  export CLUSTER_NAME="dranet-test-cluster"
  export IMAGE_NAME="ghcr.io/google/dranet"

  # Build the image
  docker build -t "$IMAGE_NAME":test -f Dockerfile "$BATS_TEST_DIRNAME"/.. --load

  # Define the kind arguments in an array
  kind_args=(
    create cluster
    --name "$CLUSTER_NAME"
    -v7 --wait 1m --retain
    --config="$BATS_TEST_DIRNAME"/../kind.yaml
  )

  if [[ "${USE_LATEST:-false}" == "true" ]]; then
    revision=$(curl --fail --silent --show-error --location https://dl.k8s.io/ci/fast/latest-fast.txt)
    kind_node_source="https://dl.k8s.io/ci/fast/$revision/kubernetes-server-linux-amd64.tar.gz"
    kind build node-image --image=dra/node:latest "${kind_node_source}"
    kind_args+=(--image dra/node:latest)
  fi

  mkdir -p _artifacts
  rm -rf _artifacts/*
  # create cluster

  kind "${kind_args[@]}"

  kind load docker-image "$IMAGE_NAME":test --name "$CLUSTER_NAME"

  # Creating BPF and cgroup mounts on the Kind nodes.
  NODES=$(kind get nodes --name ${CLUSTER_NAME})
  for node in $NODES; do
    docker exec "$node" mount -t bpf bpffs /sys/fs/bpf
    docker exec "$node" mount --make-shared /sys/fs/bpf
  done

  _install=$(sed s#"$IMAGE_NAME".*#"$IMAGE_NAME":test# < "$BATS_TEST_DIRNAME"/../install.yaml)
  printf '%s' "${_install}" | kubectl apply -f -
  kubectl wait --for=condition=ready pods --namespace=kube-system -l k8s-app=dranet

  # Expose a webserver in the default namespace
  kubectl run web --image=httpd:2 --labels="app=web" --expose --port=80

  # test depend on external connectivity that can be very flaky
  sleep 5
}

function teardown_suite {
    kind export logs "$BATS_TEST_DIRNAME"/../_artifacts --name "$CLUSTER_NAME"
    kind delete cluster --name "$CLUSTER_NAME"
}
