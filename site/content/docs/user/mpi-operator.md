---
title: "MPI Operator"
date: 2025-05-27T11:30:40Z
---

Running distributed applications, such as those using the Message Passing Interface (MPI) or NVIDIA's Collective Communications Library (NCCL) for GPU communication, often requires each participating process (or Pod, in Kubernetes terms) to have access to high-speed, low-latency interconnects. Simply sharing a generic network interface among many high-performance jobs can lead to contention, unpredictable performance, and underutilization of expensive hardware.

The goal is resource compartmentalization: ensuring that each part of your distributed job gets dedicated access to the specific resources it needs – for instance, one GPU and one dedicated RDMA-capable NIC per worker.

## DraNet + MPI Operator: A Powerful Combination

- DraNet: Provides the mechanism to discover RDMA-capable NICs on your Kubernetes nodes and make them available for Pods to claim. Through DRA, Pods can request a specific NIC, and DraNet, via NRI hooks, will configure it within the Pod's namespace, [even naming it predictably (e.g., dranet0)](google/dranet/dranet-dcd98f563b1a24f4800cf3d2d502ec5b2f488ddc/site/content/docs/user/interface-configuration.md)

- [Kubeflow MPI Operator](https://github.com/kubeflow/mpi-operator): Simplifies the deployment and management of MPI-based applications on Kubernetes. It handles the setup of MPI ranks, hostfiles, and the execution of mpirun.

By using them together, we can create MPIJob definitions where each worker Pod explicitly claims a dedicated RDMA NIC managed by DraNet, alongside its GPU

### Example: Running NCCL Tests for Distributed Workload Validation

A common and reliable way to validate that that our distributed setup is performing optimally is by running an [NVIDIA's Collective Communications Library (NCCL) All-Reduce test](https://github.com/NVIDIA/nccl-tests). This benchmark is designed to exercise the high-speed interconnects between nodes, helping you confirm that the RDMA fabric (like InfiniBand or RoCE) is operating correctly and ready to support your distributed workloads with expected efficiency.

Let's see how we can run this with DraNet and the MPI Operator, focusing on a 1 GPU and 1 NIC per worker configuration.

1. Defining Resources for DraNet:

First, we tell DraNet what kind of NICs we're interested in and how Pods can claim them.

**DeviceClass (dranet-rdma-for-mpi):** This selects RDMA-capable NICs managed by DraNet.

```yaml
apiVersion: resource.k8s.io/v1beta1
kind: DeviceClass
metadata:
  name: dranet-rdma-for-mpi
spec:
  selectors:
    - cel:
        expression: device.driver == "dra.net"
    - cel:
        expression: device.attributes["dra.net"].rdma == true
```

**ResourceClaimTemplate (mpi-worker-rdma-nic-template):** MPI worker Pods will use this to request one RDMA NIC. DraNet will be instructed to name this interface dranet0 inside the Pod.

```yaml
apiVersion: resource.k8s.io/v1beta1
kind: ResourceClaimTemplate
metadata:
  name: mpi-worker-rdma-nic-template
spec:
  spec:
    devices:
      requests:
        - name: rdma-nic-for-mpi
          deviceClassName: dranet-rdma-for-mpi
          selectors:
          - cel:
              expression: device.attributes["dra.net"].ifName == "gpu2rdma0"
    config:
    - opaque:
        driver: dra.net
        parameters:
          interface:
            name: "dranet0" # NCCL will use this interface
```

1. Crafting the MPIJob:

The MPIJob specification is where we tie everything together. We'll define a job with two workers, each getting one GPU and one DraNet-managed RDMA NIC.

```yaml
apiVersion: kubeflow.org/v2beta1
kind: MPIJob
metadata:
  name: nccl-test-dranet-1gpu-1nic
spec:
  slotsPerWorker: 1 # 1 MPI rank per worker Pod
  mpiImplementation: OpenMPI # Or your preferred MPI
  mpiReplicaSpecs:
    Launcher:
      replicas: 1
      template:
        spec:
          containers:
          - image: us-docker.pkg.dev/gce-ai-infra/gpudirect-gib/nccl-plugin-gib-diagnostic:v1.0.5
            env:
              - name: NCCL_DEBUG
                value: "INFO"
              - name: OMPI_MCA_pml
                value: "ucx"
            command:
            - mpirun
            - /third_party/nccl-tests/build/all_reduce_perf
            - -b 8K -e 128M -g 1 # Benchmark params: 1 GPU per process
            securityContext:
              capabilities:
                add: ["IPC_LOCK"]
    Worker:
      replicas: 2
      template:
        spec:
          resourceClaims:
          - name: worker-rdma-nic
            resourceClaimTemplateName: mpi-worker-rdma-nic-template
          containers:
          - image: us-docker.pkg.dev/gce-ai-infra/gpudirect-gib/nccl-plugin-gib-diagnostic:v1.0.5
            name: mpi-worker
            securityContext:
              capabilities:
                add: ["IPC_LOCK"]
            resources:
              limits:
                nvidia.com/gpu: 1 # Each worker gets 1 GPU
```

### Key Aspects of this Configuration:

- **slotsPerWorker: 1:** Each worker Pod hosts a single MPI rank.

- **Worker.replicas: 2:** We run a 2-rank MPI job.

- **Worker.template.spec.resourceClaims:** Each worker Pod claims its own RDMA NIC via the template, which DraNet will configure as dranet0.

- **Worker.template.spec.containers[0].resources.limits["nvidia.com/gpu"]: 1:** Each worker gets one GPU.

- **Launcher.template.spec.containers[0].env.NCCL_SOCKET_IFNAME: "dranet0":** This environment variable explicitly tells NCCL to use the dranet0 interface for its network operations.

- **MPI MCA Parameters (e.g., UCX_NET_DEVICES="dranet0"):** These guide the MPI library itself to use the specified RDMA interface.

3. Running and Observing:

Once deployed, the MPI Operator will launch the job. The launcher Pod will execute mpirun, which starts the all_reduce_perf test across the two worker Pods. Each worker Pod will use its dedicated GPU and its dedicated dranet0 (RDMA NIC) for NCCL communications.

You can monitor the launcher's logs to see the NCCL benchmark results, including the achieved bus bandwidth. The NCCL_DEBUG=INFO logs will also confirm that NCCL is indeed using the dranet0 interface.

## The Power of Compartmentalization with DraNet

This setup beautifully illustrates the benefits of resource compartmentalization:

- Dedicated Performance: Each MPI worker in this job has exclusive use of one GPU and one high-speed RDMA NIC. This ensures that its communication performance is not impacted by other workloads on the same node.

- Efficient Resource Utilization: If your nodes are powerful (e.g., 8 GPUs and 8 RDMA NICs), running this 2-worker job (consuming 1 GPU/1 NIC on two separate nodes) leaves the remaining resources on those nodes (and other nodes) fully available.

- Concurrent High-Performance Jobs: You can run multiple independent MPI jobs or other DraNet-aware distributed workloads simultaneously. Each job can claim its own subset of GPUs and RDMA NICs, and DraNet ensures that their network traffic is isolated at the NIC level, preventing contention and guaranteeing predictable performance.

By leveraging DraNet with tools like the MPI Operator, teams can confidently deploy network-intensive distributed applications on Kubernetes, achieving performance comparable to bare-metal HPC clusters while benefiting from Kubernetes' orchestration capabilities.