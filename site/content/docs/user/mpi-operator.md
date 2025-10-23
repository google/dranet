---
title: "MPI Operator on GKE and GPUDirect RDMA"
date: 2025-05-27T11:30:40Z
---

Running distributed applications, such as those using the Message Passing Interface (MPI) or NVIDIA's Collective Communications Library (NCCL) for GPU communication, often requires each participating process (or Pod, in Kubernetes terms) to have access to high-speed, low-latency interconnects. Simply sharing a generic network interface among many high-performance jobs can lead to contention, unpredictable performance, and underutilization of expensive hardware.

The goal is resource compartmentalization: ensuring that each part of your distributed job gets dedicated access to the specific resources it needs – for instance, one GPU and one dedicated RDMA-capable NIC per worker.

## DRANET + MPI Operator: A Powerful Combination

- DRANET: Provides the mechanism to discover RDMA-capable NICs on your Kubernetes nodes and make them available for Pods to claim. Through DRA, Pods can request a specific NIC, and DRANET, via NRI hooks, will configure it within the Pod's namespace, [even naming it predictably (e.g., dranet0)](google/dranet/dranet-dcd98f563b1a24f4800cf3d2d502ec5b2f488ddc/site/content/docs/user/interface-configuration.md)

- [Kubeflow MPI Operator](https://github.com/kubeflow/mpi-operator): Simplifies the deployment and management of MPI-based applications on Kubernetes. It handles the setup of MPI ranks, hostfiles, and the execution of mpirun.

By using them together, we can create MPIJob definitions where each worker Pod explicitly claims a dedicated RDMA NIC managed by DRANET, alongside its GPU

### Example: Running NCCL Tests for Distributed Workload Validation

A common and reliable way to validate that that our distributed setup is performing optimally is by running an [NVIDIA's Collective Communications Library (NCCL) All-Reduce test](https://github.com/NVIDIA/nccl-tests). This benchmark is designed to exercise the high-speed interconnects between nodes, helping you confirm that the RDMA fabric (like InfiniBand or RoCE) is operating correctly and ready to support your distributed workloads with expected efficiency.

Let's see how we can run this with DRANET and the MPI Operator, focusing on a 1 GPU and 1 NIC per worker configuration.

#### Defining Resources for DRANET

First, we tell DRANET what kind of NICs we're interested in and how Pods can claim them.

**DeviceClass (dranet-rdma-for-mpi):** This selects RDMA-capable NICs managed by DRANET.

```yaml
apiVersion: resource.k8s.io/v1
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

**ResourceClaimTemplate (mpi-worker-rdma-nic-template):** MPI worker Pods will use this to request one RDMA NIC. DRANET will be instructed to name this interface dranet0 inside the Pod.

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: mpi-worker-rdma-nic-template
spec:
  spec:
    devices:
      requests:
        - name: rdma-nic-for-mpi
          exactly:
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

#### Install the GKE optimized RDMA dependencies

GKE automatically install on the VM some optimized RDMA and NCCL libraries for Google Cloud infrastructure, that can be installed following the instructions on:

https://cloud.google.com/ai-hypercomputer/docs/create/gke-ai-hypercompute-custom#install-rdma-configure-nccl

In order to use them you need to mount the following volumes

```yaml
spec:
  volumes:
    - name: library-dir-host
      hostPath:
        path: /home/kubernetes/bin/nvidia
    - name: gib
      hostPath:
        path: /home/kubernetes/bin/gib
```

in your workloads:

```yaml
containers:
  - name: my-container
    volumeMounts:
      - name: library-dir-host
        mountPath: /usr/local/nvidia
      - name: gib
        mountPath: /usr/local/gib
    env:
      - name: LD_LIBRARY_PATH
        value: /usr/local/nvidia/lib64
```

#### Crafting the MPIJob

The MPIJob specification is where we tie everything together. We'll define a job with two workers, each getting one GPU and one DRANET-managed RDMA NIC.

```yaml
apiVersion: kubeflow.org/v2beta1
kind: MPIJob
metadata:
  name: nccl-test-dranet-1gpu-1nic
spec:
  slotsPerWorker: 1 # 1 MPI rank per worker Pod
  mpiReplicaSpecs:
    Launcher:
      replicas: 1
      template:
        spec:
          containers:
          - image: mpioperator/openmpi:v0.6.0
            name: mpi-launcher
            command: ["/bin/bash", "-c"]
            args:
            - |
              set -ex
              mpirun \
                --allow-run-as-root \
                --prefix /opt/openmpi \
                -np 2 \
                -bind-to none \
                -map-by slot \
                -mca routed direct \
                -x LD_LIBRARY_PATH=/usr/local/nvidia/lib64 \
                bash -c \
                  "source /usr/local/gib/scripts/set_nccl_env.sh; \
                  /usr/local/bin/all_reduce_perf \
                    -g 1 -b 1K -e 8G -f 2 \
                    -w 5 -n 20;"
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
          - image: ghcr.io/google/dranet-rdma-perftest:sha-fb3f932
            name: mpi-worker
            securityContext:
              capabilities:
                add: ["IPC_LOCK"]
            resources:
              limits:
                nvidia.com/gpu: 1 # Each worker gets 1 GPU
            volumeMounts:
              - name: library-dir-host
                mountPath: /usr/local/nvidia
              - name: gib
                mountPath: /usr/local/gib
          volumes:
            - name: library-dir-host
              hostPath:
                path: /home/kubernetes/bin/nvidia
            - name: gib
              hostPath:
                path: /home/kubernetes/bin/gib
```

#### Running and Observing

Once deployed, the MPI Operator will launch the job. The launcher Pod will execute mpirun, which starts the all_reduce_perf test across the two worker Pods. Each worker Pod will use its dedicated GPU and its dedicated dranet0 (RDMA NIC) for NCCL communications.

You can monitor the launcher's logs to see the NCCL benchmark results, including the achieved bus bandwidth.

```sh
kubectl logs $(kubectl get pods | grep launcher | awk '{ print $1}') -f
+ mpirun --allow-run-as-root --prefix /opt/openmpi -np 2 -bind-to none -map-by slot -mca routed direct -x LD_LIBRARY_PATH=/usr/local/nvidia/lib64 bash -c 'source /usr/local/gib/scripts/set_nccl_env.sh;     /usr/local/bin/all_reduce_perf       -g 1 -b 1K -e 8G -f 2       -w 5 -n 20;'
Warning: Permanently added '[nccl-test-dranet-1gpu-1nic-worker-1.nccl-test-dranet-1gpu-1nic.default.svc]:2222' (ED25519) to the list of known hosts.
Warning: Permanently added '[nccl-test-dranet-1gpu-1nic-worker-0.nccl-test-dranet-1gpu-1nic.default.svc]:2222' (ED25519) to the list of known hosts.
--------------------------------------------------------------------------
WARNING: No preset parameters were found for the device that Open MPI
detected:

  Local host:            nccl-test-dranet-1gpu-1nic-worker-0
  Device name:           mlx5_2
  Device vendor ID:      0x02c9
  Device vendor part ID: 4126

Default device parameters will be used, which may result in lower
performance.  You can edit any of the files specified by the
btl_openib_device_param_files MCA parameter to set values for your
device.

NOTE: You can turn off this warning by setting the MCA parameter
      btl_openib_warn_no_device_params_found to 0.
--------------------------------------------------------------------------
--------------------------------------------------------------------------
No OpenFabrics connection schemes reported that they were able to be
used on a specific port.  As such, the openib BTL (OpenFabrics
support) will be disabled for this port.

  Local host:           nccl-test-dranet-1gpu-1nic-worker-0
  Local device:         mlx5_2
  Local port:           1
  CPCs attempted:       rdmacm, udcm
--------------------------------------------------------------------------
# nThread 1 nGpus 1 minBytes 1024 maxBytes 8589934592 step: 2(factor) warmup iters: 5 iters: 20 agg iters: 1 validation: 1 graph: 0
#
# Using devices
#  Rank  0 Group  0 Pid     23 on nccl-test-dranet-1gpu-1nic-worker-0 device  0 [0000:cc:00] NVIDIA H200
#  Rank  1 Group  0 Pid     21 on nccl-test-dranet-1gpu-1nic-worker-1 device  0 [0000:97:00] NVIDIA H200
#
#                                                              out-of-place                       in-place
#       size         count      type   redop    root     time   algbw   busbw #wrong     time   algbw   busbw #wrong
#        (B)    (elements)                               (us)  (GB/s)  (GB/s)            (us)  (GB/s)  (GB/s)
        1024           256     float     sum      -1    35.59    0.03    0.03      0    30.61    0.03    0.03      0
        2048           512     float     sum      -1    31.80    0.06    0.06      0    31.90    0.06    0.06      0
        4096          1024     float     sum      -1    33.56    0.12    0.12      0    33.33    0.12    0.12      0
        8192          2048     float     sum      -1    39.33    0.21    0.21      0    39.24    0.21    0.21      0
       16384          4096     float     sum      -1    41.89    0.39    0.39      0    40.31    0.41    0.41      0
       32768          8192     float     sum      -1    45.47    0.72    0.72      0    42.92    0.76    0.76      0
       65536         16384     float     sum      -1    54.03    1.21    1.21      0    51.81    1.26    1.26      0
      131072         32768     float     sum      -1    51.86    2.53    2.53      0    52.60    2.49    2.49      0
      262144         65536     float     sum      -1    79.10    3.31    3.31      0    68.36    3.83    3.83      0
      524288        131072     float     sum      -1    76.88    6.82    6.82      0    76.38    6.86    6.86      0
     1048576        262144     float     sum      -1    98.57   10.64   10.64      0    93.72   11.19   11.19      0
     2097152        524288     float     sum      -1    131.9   15.90   15.90      0    131.8   15.91   15.91      0
     4194304       1048576     float     sum      -1    227.5   18.44   18.44      0    227.4   18.45   18.45      0
     8388608       2097152     float     sum      -1    415.7   20.18   20.18      0    416.7   20.13   20.13      0
    16777216       4194304     float     sum      -1    811.3   20.68   20.68      0    808.5   20.75   20.75      0
    33554432       8388608     float     sum      -1   1609.7   20.84   20.84      0   1607.6   20.87   20.87      0
    67108864      16777216     float     sum      -1   2250.8   29.82   29.82      0   2253.3   29.78   29.78      0
   134217728      33554432     float     sum      -1   4440.0   30.23   30.23      0   4444.3   30.20   30.20      0
   268435456      67108864     float     sum      -1   8635.4   31.09   31.09      0   8653.9   31.02   31.02      0
   536870912     134217728     float     sum      -1    17077   31.44   31.44      0    17081   31.43   31.43      0
  1073741824     268435456     float     sum      -1    33860   31.71   31.71      0    33896   31.68   31.68      0
  2147483648     536870912     float     sum      -1    67521   31.80   31.80      0    67503   31.81   31.81      0
  4294967296    1073741824     float     sum      -1   134734   31.88   31.88      0   135069   31.80   31.80      0
  8589934592    2147483648     float     sum      -1   269368   31.89   31.89      0   269407   31.88   31.88      0
# Out of bounds values : 0 OK
# Avg bus bandwidth    : 15.5188
```
