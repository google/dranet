---
title: "Quick Start"
date: 2024-12-17T14:47:05Z
weight: 1
---

`DRANET` depends on the Kubernetes feature [Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/), that is beta (disabled by default in Kubernetes ∂v1.32).

In order to enable DRA you need to enable both the [feature gates and the API groups](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#enabling-dynamic-resource-allocation).

## Kubernetes cluster with DRA

### KIND

Install [KIND](https://github.com/kubernetes-sigs/kind?tab=readme-ov-file#installation-and-usage).

Create a cluster using the following configuration.

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  image: kindest/node:v1.33.1
- role: worker
  image: kindest/node:v1.33.1
- role: worker
  image: kindest/node:v1.33.1
featureGates:
  # Enable the corresponding DRA feature gates
  DynamicResourceAllocation: true
  DRAResourceClaimDeviceStatus: true
runtimeConfig:
  api/beta : true
```

```
kind create cluster --config kind.yaml --name dra
```

### Google Cloud

For instructions on setting up DRA on GKE, refer to the official documentation:
[Set up Dynamic Resource Allocation](https://cloud.google.com/kubernetes-engine/docs/how-to/set-up-dra)

A quick and easy way to find if DRA is enabled is by checking the metrics in the kube-apiserver

```sh
kubectl get --raw /metrics | grep kubernetes_feature_enabled | grep DynamicResourceAllocation

kubernetes_feature_enabled{name="DynamicResourceAllocation",stage="BETA"} 1
```

## Installation

You can install the latest stable version of `DRANET` using the provided manifest:

```
kubectl apply -f https://raw.githubusercontent.com/google/dranet/refs/heads/main/install.yaml
```

### How to use it

Once the Kubernetes Network Driver is running you can see the list of Network Interfaces and its attributes published by the drivers using `kubectl get resourceslices -o yaml`:

```
apiVersion: resource.k8s.io/v1
kind: ResourceSlice
metadata:
  creationTimestamp: "2024-12-15T23:41:51Z"
  generateName: gke-aojea-dra-multi-nic-985b8c20-jg5l-dra.net-
  generation: 1
  name: gke-aojea-dra-multi-nic-985b8c20-jg5l-dra.net-8nq9c
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
          dra.net/alias:
            string: ""
          dra.net/cloudNetwork:
            string: dra-1-vpc
          dra.net/encapsulation:
            string: ether
          dra.net/ifName:
            string: gpu7rdma0
          dra.net/ipv4:
            string: 10.0.8.8
          dra.net/mac:
            string: 9a:41:2e:4f:86:16
          dra.net/mtu:
            int: 8896
          dra.net/numaNode:
            int: 1
          dra.net/pciAddressBus:
            string: c8
          dra.net/pciAddressDevice:
            string: "00"
          dra.net/pciAddressDomain:
            string: "0000"
          dra.net/pciAddressFunction:
            string: "0"
          dra.net/pciVendor:
            string: Mellanox Technologies
          dra.net/rdma:
            bool: true
          dra.net/sriov:
            bool: false
          dra.net/state:
            string: up
          dra.net/type:
            string: device
          dra.net/virtual:
            bool: false
      name: gpu7rdma0
...
```

Once the resources are available, users can create `DeviceClasses`, `ResourceClaims` and/or `ResourceClaimTemplates` to schedule pods.

Define a `DeviceClass` that selects all the network interfaces that are connected to a `GCP Network`

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: dranet-cloud
spec:
  selectors:
    - cel:
        expression: device.driver == "dra.net"
    - cel:
        expression: has(device.attributes["dra.net"].cloudNetwork) 
```

Now you can create a `ResourceClaim` that connects to a specific network, in this case `dra-1-vpc` and reference that claim in a `Pod`:

```yaml
apiVersion: resource.k8s.io/v1
kind:  ResourceClaim
metadata:
  name: cloud-network-dra-net-1
spec:
  devices:
    requests:
    - name: req-cloud-net-1
      exactly:
        deviceClassName: dranet-cloud
        selectors:
          - cel:
              expression: device.attributes["dra.net"].cloudNetwork == "dra-1-vpc"
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-dra-net1
  labels:
    app: pod-dra-net1
spec:
  containers:
  - name: ctr1
    image: registry.k8s.io/e2e-test-images/agnhost:2.39
  resourceClaims:
  - name: net-1
    resourceClaimName: cloud-network-dra-net-1
```

Kubernetes schedules the `Pod` to the corresponding `Node` and attach the network interface to the `Pod`:

```sh
kubectl get pods -o wide
NAME           READY   STATUS    RESTARTS   AGE   IP            NODE                                    NOMINATED NODE   READINESS GATES
pod-dra-net1  1/1     Running   0          5s    10.52.3.108   gke-dra-multi-nic-985b8c20-jg5l   <none>           <none>
```

If we execute inside the `Pod` we can see the network interface now is attached:
```sh
kubectl exec -it pod-dra-net1 ip a
kubectl exec [POD] [COMMAND] is DEPRECATED and will be removed in a future version. Use kubectl exec [POD] -- [COMMAND] instead.
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
2: eth0@if1124: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1460 qdisc noqueue state UP group default 
    link/ether 86:dc:58:24:55:1a brd ff:ff:ff:ff:ff:ff link-netnsid 0
    inet 10.52.3.108/24 brd 10.52.3.255 scope global eth0
       valid_lft forever preferred_lft forever
5: gpu7rdma0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8244 qdisc fq state UP group default qlen 1000
    link/ether 42:01:c0:a8:03:02 brd ff:ff:ff:ff:ff:ff
```

Deleting the Pod restores the interface and makes it available again.
