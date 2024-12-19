---
title: "Quick Start"
date: 2024-12-17T14:47:05Z
weight: 1
---

DRANET depends on the Kubernetes feature [Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/), that is beta (disabled by default in v1.32).

In order to enable DRA you need to enable both the [feature gates and the API groups](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#enabling-dynamic-resource-allocation).

## Kubernetes cluster with DRA

### KIND

Install [KIND](https://github.com/kubernetes-sigs/kind?tab=readme-ov-file#installation-and-usage).

Create a cluster using the following configuration.

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
  # Enable NRI plugins
- |-
  [plugins."io.containerd.nri.v1.nri"]
    disable = false
nodes:
- role: control-plane
  image: kindest/node:v1.32.0
- role: worker
  image: kindest/node:v1.32.0
- role: worker
  image: kindest/node:v1.32.0
featureGates:
  # Enable the corresponding DRA feature gates
  DynamicResourceAllocation: true
runtimeConfig:
  api/beta : true
```

```
kind create cluster --config kind.yaml --name dra
```

### Google Cloud

You can [enable the DRA beta APIs in GKE](https://cloud.google.com/kubernetes-engine/docs/how-to/use-beta-apis) and it automatically turns on the feature gates.

You need to check that a v1.32 version exist in your zone:

```sh
$ gcloud container get-server-config  | grep 1.32
Fetching server config for us-central1-c
  - 1.32.0-gke.1358000
  minorVersion: '1.32'
- 1.32.0-gke.1358000
- 1.32.0-gke.1358000
```

And using the version obtained you can create a cluster

```sh
export PROJECT=dra-proj
export REGION=us-central1
export ZONE=us-central1-c
export CLUSTER=dra-cluster
export VERSION=1.32.0-gke.1358000

gcloud beta container clusters create ${CLUSTER} \
    --cluster-version=${VERSION} \
    --enable-multi-networking \
    --enable-dataplane-v2 \
    --enable-kubernetes-unstable-apis=resource.k8s.io/v1beta1/deviceclasses,resource.k8s.io/v1beta1/resourceclaims,resource.k8s.io/v1beta1/resourceclaimtemplates,resource.k8s.io/v1beta1/resourceslices \
    --no-enable-autorepair \
    --no-enable-autoupgrade \
    --zone=${ZONE}

To inspect the contents of your cluster, go to: https://console.cloud.google.com/kubernetes/workload_/gcloud/us-central1-c/aojea-dra?project=aojea-gke-dev
kubeconfig entry generated for aojea-dra.
NAME       LOCATION       MASTER_VERSION      MASTER_IP     MACHINE_TYPE  NODE_VERSION        NUM_NODES  STATUS
aojea-dra  us-central1-c  1.32.0-gke.1358000  X.X.X.X  e2-medium     1.32.0-gke.1358000  3          RUNNING
```

A quick and easy way to find if DRA is enabled is by checking the metrics in the kube-apiserver

```sh
kubectl get --raw /metrics | grep kubernetes_feature_enabled | grep DynamicResourceAllocation

kubernetes_feature_enabled{name="DynamicResourceAllocation",stage="BETA"} 1
```

### Installation

You can install the latest stable version using the provided manifest:

```
kubectl apply -f https://raw.githubusercontent.com/google/dranet/refs/heads/main/install.yaml
```

### How to use it

Once the Kubernetes Network Driver is running you can see the list of Network Interfaces and its attributes published by the drivers:

```
apiVersion: resource.k8s.io/v1beta1
kind: ResourceSlice
metadata:
  creationTimestamp: "2024-12-15T23:41:51Z"
  generateName: gke-aojea-dra-multi-nic-985b8c20-jg5l-dranet.gke.io-
  generation: 1
  name: gke-aojea-dra-multi-nic-985b8c20-jg5l-dranet.gke.io-8nq9c
  ownerReferences:
  - apiVersion: v1
    controller: true
    kind: Node
    name: gke-aojea-dra-multi-nic-985b8c20-jg5l
    uid: 0146a07e-df67-401d-b3a5-dddb02f50b6e
  resourceVersion: "1471803"
  uid: 535724d7-a573-49e1-8f3b-4e644405375a
spec:
  devices:
  - basic:
      attributes:
        alias:
          string: ""
        cloud_network:
          string: projects/961828715260/networks/aojea-dra-net-1
        encapsulation:
          string: ether
        ip:
          string: 192.168.1.2
        kind:
          string: network
        mac:
          string: 42:01:c0:a8:01:02
        mtu:
          int: 8244
        name:
          string: eth1
        numa_node:
          int: -1
        pci_address_bus:
          string: "00"
        pci_address_device:
          string: "05"
        pci_address_domain:
          string: "0000"
        pci_address_function:
          string: "0"
        pci_vendor:
          string: Google, Inc.
        rdma:
          bool: false
        sriov:
          bool: false
        state:
          string: up
        type:
          string: device
        virtual:
          bool: false
    name: eth1
  - basic:
      attributes:
        alias:
          string: ""
        cloud_network:
          string: projects/961828715260/networks/aojea-dra-net-2
        encapsulation:
          string: ether
        ip:
          string: 192.168.2.2
        kind:
          string: network
        mac:
          string: 42:01:c0:a8:02:02
        mtu:
          int: 8244
        name:
          string: eth2
        numa_node:
          int: -1
        pci_address_bus:
          string: "00"
        pci_address_device:
          string: "06"
        pci_address_domain:
          string: "0000"
        pci_address_function:
          string: "0"
        pci_vendor:
          string: Google, Inc.
        rdma:
          bool: false
        sriov:
          bool: false
        state:
          string: up
        type:
          string: device
        virtual:
          bool: false
    name: eth2
  - basic:
      attributes:
        alias:
          string: ""
        cloud_network:
          string: projects/961828715260/networks/aojea-dra-net-3
        encapsulation:
          string: ether
        ip:
          string: 192.168.3.2
        kind:
          string: network
        mac:
          string: 42:01:c0:a8:03:02
        mtu:
          int: 8244
        name:
          string: eth3
        numa_node:
          int: -1
        pci_address_bus:
          string: "00"
        pci_address_device:
          string: "07"
        pci_address_domain:
          string: "0000"
        pci_address_function:
          string: "0"
        pci_vendor:
          string: Google, Inc.
        rdma:
          bool: false
        sriov:
          bool: false
        state:
          string: up
        type:
          string: device
        virtual:
          bool: false
    name: eth3
...
```

Once the resources are available, users can create DeviceClasses, ResourceClaims and/or ResourceClaimTemplates to schedule pods, see some [examples](https://github.com/google/dranet/tree/main/examples).

Define a `DeviceClass` that selects all the network interfaces that are connected to a `GCP Network`

```yaml
apiVersion: resource.k8s.io/v1beta1
kind: DeviceClass
metadata:
  name: dranet-cloud
spec:
  selectors:
    - cel:
        expression: device.driver == "dranet.gke.io"
    - cel:
        expression: has(device.attributes["dranet.gke.io"].cloud_network) 
  config:
  - opaque:
      driver: dranet.gke.io
      parameters:
        nccl: "true"
```

Now you can create a `ResourceClaim` that connects to a specific network, in this case `projects/961828715260/networks/aojea-dra-net-3` and reference that claim in a `Pod`:

```yaml
apiVersion: resource.k8s.io/v1beta1
kind:  ResourceClaim
metadata:
  name: cloud-network-dra-net-3
spec:
  devices:
    requests:
    - name: req-cloud-net-3
      deviceClassName: dranet-cloud
      selectors:
        - cel:
            expression: device.attributes["dranet.gke.io"].cloud_network == "projects/961828715260/networks/aojea-dra-net-3"
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-dra-net3
  labels:
    app: pod-dra-net3
spec:
  containers:
  - name: ctr1
    image: registry.k8s.io/e2e-test-images/agnhost:2.39
  resourceClaims:
  - name: net-3
    resourceClaimName: cloud-network-dra-net-3
```

Kubernetes schedules the `Pod` to the corresponding `Node` and attach the network interface to the `Pod`:

```sh
kubectl get pods -o wide
NAME           READY   STATUS    RESTARTS   AGE   IP            NODE                                    NOMINATED NODE   READINESS GATES
pod-dra-net3   1/1     Running   0          5s    10.52.3.108   gke-dra-multi-nic-985b8c20-jg5l   <none>           <none>
```

If we execute inside the `Pod` we can see the network interface now is attached:
```sh
kubectl exec -it pod-dra-net3 ip a
kubectl exec [POD] [COMMAND] is DEPRECATED and will be removed in a future version. Use kubectl exec [POD] -- [COMMAND] instead.
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
2: eth0@if1124: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1460 qdisc noqueue state UP group default 
    link/ether 86:dc:58:24:55:1a brd ff:ff:ff:ff:ff:ff link-netnsid 0
    inet 10.52.3.108/24 brd 10.52.3.255 scope global eth0
       valid_lft forever preferred_lft forever
5: eth3: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8244 qdisc fq state UP group default qlen 1000
    link/ether 42:01:c0:a8:03:02 brd ff:ff:ff:ff:ff:ff
```

Deleting the Pod restores the interface and makes it available again.
