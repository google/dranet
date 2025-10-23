# DRANET: DRA Kubernetes Network Driver

DRANET is a Kubernetes Network Driver that uses Dynamic Resource Allocation
(DRA) to deliver high-performance networking for demanding applications in
Kubernetes.

## Key Features

- **DRA Integration:** Leverages the power of Kubernetes' Dynamic Resource
  Allocation.
- **High-Performance Networking:** Designed for demanding workloads like AI/ML
  applications.
- **Simplified Management:** Easy to deploy and manage.
- **Enhanced Efficiency:** Optimizes resource utilization for improved overall
  performance.
- **Cluster-Wide Scalability:**  Effectively manages network resources across a
  large number of nodes for seamless operation in Kubernetes deployments.

Our research paper, **"[The Kubernetes Network Driver Model: A Composable Architecture for High-Performance Networking](/site/static/docs/kubernetes_network_driver_model_dranet_paper.pdf)"**, provides a deep dive into the DRANET model and its impact.

<p align="center">
<img src="site/static/images/nccl_all_gather_results.png" width="400" height="300">   <img src="site/static/images/nccl_all_reduce_results.png" width="400" height="300">
</p>

The key findings include:

- **Up to 60% Bandwidth Increase:** By enabling topology-aware scheduling of GPUs and NICs, DRANET boosts bus bandwidth by up to 59.6% for `all_gather` and 58.1% for `all_reduce` operations in distributed AI/ML workloads.
- **Operational Simplicity:** The paper demonstrates how the KND model used by DRANET drastically simplifies the management of high-performance hardware, replacing fragile, multi-component chains with a clean, composable architecture.

## How It Works

The DRANET driver communicates with the Kubelet through the [DRA
API](https://github.com/kubernetes/kubernetes/blob/3bec2450efd29787df0f27415de4e8049979654f/staging/src/k8s.io/kubelet/pkg/apis/dra/v1beta1/api.proto)
and with the Container Runtime via [NRI](https://github.com/containerd/nri).
This architectural approach ensures robust supportability and minimizes
complexity, making it fully compatible with existing CNI plugins in your
cluster.

Upon the creation of a Pod's network namespaces, the Container Runtime initiates
a GRPC call to DRANET via NRI to execute the necessary network configurations.

A more detailed diagram illustrating this process can be found in our
documentation: [How It
Works](https://google.github.io/dranet/docs/concepts/howitworks/).

## Quick Start

To get started with DRANET, your Kubernetes cluster needs to have [Dynamic
Resource Allocation (DRA)
enabled](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/).
DRA is beta and is disabled by default in Kubernetes v1.32. You will need to
enable both the [feature gates and the API
groups](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#enabling-dynamic-resource-allocation)
for DRA until it reaches GA.

![](site/static/images/dranet.gif)

### Kubernetes Cluster with DRA

#### KIND

If you are using
[KIND](https://github.com/kubernetes-sigs/kind?tab=readme-ov-file#installation-and-usage),
you can create a cluster with the following configuration:

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  image: kindest/node:v1.34.0
- role: worker
  image: kindest/node:v1.34.0
- role: worker
  image: kindest/node:v1.34.0
```

Then to create the cluster:

```sh
kind create cluster --config kind.yaml
```

#### Google Cloud (GKE)

For instructions on setting up DRA on GKE, refer to the official documentation:
[Set up Dynamic Resource
Allocation](https://cloud.google.com/kubernetes-engine/docs/how-to/set-up-dra)

### Installation

Install the latest stable version of DRANET using the provided manifest:

```sh
kubectl apply -f https://raw.githubusercontent.com/google/dranet/refs/heads/main/install.yaml
```

### How to Use It

Once DRANET is running, you can inspect the network interfaces and their
attributes published by the drivers. Users can then create `DeviceClasses`,
`ResourceClaims`, and/or `ResourceClaimTemplates` to schedule pods and allocate
network devices.

For examples of how to use DRANET with `DeviceClas`s and `ResourceClaim` to
attach network interfaces to pods, please refer to the [Quick Start
guide](https://google.github.io/dranet/docs/quick-start).


## Contributing

We welcome your contributions! Please review our [Contributor License
Agreement](https://cla.developers.google.com/about) and [Google's Open Source
Community Guidelines](https://opensource.google/conduct/) before you begin. All
submissions require review via [GitHub pull
requests](https://docs.github.com/articles/about-pull-requests).

For detailed development instructions, including local development with KIND and
troubleshooting tips, see our [Developer
Guide](https://google.github.io/dranet/docs/contributing/developer-guide.md).

## Further Reading

Explore more concepts and advanced topics:

* **Design:** Understand the architectural choices behind DRANET:
  [Design](https://google.github.io/dranet/docs/concepts/howitworks)
* **RDMA:** Learn about RDMA components in Linux and their interplay:
  [RDMA](https://google.github.io/dranet/docs/concepts/rdma)
* **References:** A list of relevant Kubernetes Enhancement Proposals (KEPs) and
  presentations:
  [References](https://google.github.io/dranet/docs/concepts/references)

## Disclaimer

This is not an officially supported Google product. This project is not eligible
for the [Google Open Source Software Vulnerability Rewards
Program](https://bughunters.google.com/open-source-security).
