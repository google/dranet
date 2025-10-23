---
title: "Ray on GKE using DRANET"
date: 2025-07-14T10:10:40Z
---

To get started, follow the instructions to create a [GKE cluster with DRA
support and using DRANET](/docs/user/gke-rdma), it is important to follow the
instructions, since there are multiple dependencies on the Kubernetes API
version, the RDMA NCCL installer and the DRANET component.

The worker nodes in this configuration are a4-highgpu-8g instances, each equipped with eight NVIDIA B200 GPUs and eight RDMA-capable RoCE NICs.


### Deploy RayCluster

Install Ray CRDs and the KubeRay operator:

```sh
kubectl create -k "github.com/ray-project/kuberay/ray-operator/config/default?ref=v1.4.1"
```

We create one `ResourceClaimTemplate`, for the RDMA devices on the node, along
with a `DeviceClass` for the RDMA device.

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: dranet
spec:
  selectors:
    - cel:
        expression: device.driver == "dra.net"
---
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: all-nic
spec:
  spec:
    devices:
      requests:
      - name: nic
        exactly:
          deviceClassName: dranet
          count: 8
          selectors:
          - cel:
              expression: device.attributes["dra.net"].rdma == true
```

Until the official Ray images support NVIDIA B200 with CUDA capability sm_100
you need to build a custom image:

```dockerfile
FROM rayproject/ray:2.47.1-py39-cu128

USER root

RUN python -m pip install --upgrade pip
RUN pip uninstall cupy-cuda12x -y && conda install -c conda-forge cupy

RUN pip install --no-cache-dir --force-reinstall numpy==1.26.4
RUN pip install --no-cache-dir --force-reinstall scipy==1.11.4

RUN pip install --pre torch --index-url https://download.pytorch.org/whl/nightly/cu128

RUN apt-get update && apt-get -y install libnl-3-200 libnl-route-3-200

USER 1000
```

Install a RayCluster and use the RDMA NICs on the workers nodes, you need to
specify some NCCL environment variables for optimal performance on Google Cloud
RDMA network:

```yaml
apiVersion: ray.io/v1
kind: RayCluster
metadata:
  name: a4-ray-cluster
spec:
  headGroupSpec:
    rayStartParams:
      dashboard-host: '0.0.0.0'
    template:
      spec:
        containers:
        - name: ray-head
          image: aojea/ray:2.44.1-py39-cu128
          ports:
          - containerPort: 6379
            name: gcs-server
          - containerPort: 8265
            name: dashboard
          - containerPort: 10001
            name: client
  workerGroupSpecs:
  - replicas: 2
    minReplicas: 0
    maxReplicas: 4
    groupName: gpu-group
    rayStartParams: {}
    template:
      spec:
        containers:
        - name: ray-worker
          image: aojea/ray:2.44.1-py39-cu128
          resources:
            limits:
              cpu: "200"
              memory: "1600Gi"
              nvidia.com/gpu: "8"
            requests:
              cpu: "120"
              memory: "1600Gi"
              nvidia.com/gpu: "8"
          env:
          - name: LD_LIBRARY_PATH
            value: /usr/local/nvidia/lib64
          - name: TORCH_DISTRIBUTED_DEBUG
            value: "INFO"
          - name: NCCL_DEBUG
            value: INFO # Or "WARN", "DEBUG", "TRACE" for more verbosity
          - name: NCCL_DEBUG_SUBSYS
            value: INIT,NET,ENV,COLL,GRAPH
          - name: NCCL_NET
            value: gIB
          - name: NCCL_CROSS_NIC
            value: "0"
          - name: NCCL_NET_GDR_LEVEL
            value: "PIX"
          - name: NCCL_P2P_NET_CHUNKSIZE
            value: "131072"
          - name: NCCL_NVLS_CHUNKSIZE
            value: "524288"
          - name: NCCL_IB_ADAPTIVE_ROUTING
            value: "1"
          - name: NCCL_IB_QPS_PER_CONNECTION
            value: "4"
          - name: NCCL_IB_TC
            value: "52"
          - name: NCCL_IB_FIFO_TC
            value: "84"
          - name: NCCL_TUNER_CONFIG_PATH
            value: "/usr/local/gib/configs/tuner_config_a4.txtpb"
          volumeMounts:
          - name: library-dir-host
            mountPath: /usr/local/nvidia
          - name: gib
            mountPath: /usr/local/gib
          - name: shared-memory
            mountPath: /dev/shm
        resourceClaims:
          - name: nics
            resourceClaimTemplateName: all-nic
        tolerations:
          - key: "nvidia.com/gpu"
            operator: "Exists"
            effect: "NoSchedule"
        volumes:
          - name: library-dir-host
            hostPath:
              path: /home/kubernetes/bin/nvidia
          - name: gib
            hostPath:
              path: /home/kubernetes/bin/gib
          - name: shared-memory
            emptyDir:
              medium: "Memory"
              sizeLimit: 250Gi
```

If in a future we want to create smaller workers that use a subset of GPUs in
the Node we should use also the [NVIDIA GPU DRA Driver](./nvidia-dranet.md) to
ensure the allocated GPUs and NICs on the node are aligned for optimal
performance.

Validate the deployment is working checking the Pods status:

```sh
 kubectl get pods -o wide
NAME                                                           READY   STATUS      RESTARTS   AGE     IP              NODE                                          NOMINATED NODE   READINESS GATES
a4-ray-cluster-gpu-group-worker-gzzt6                    1/1     Running     0          8m11s   10.48.4.6       gke-dranet-aojea-dranet-a4-54bd557d-1blr      <none>           <none>
a4-ray-cluster-gpu-group-worker-hnsvx                    1/1     Running     0          8m11s   10.48.3.6       gke-dranet-aojea-dranet-a4-54bd557d-5w4l      <none>           <none>
a4-ray-cluster-head                                      1/1     Running     0          8m11s   10.48.2.6       gke-dranet-aojea-default-pool-7abaddc3-n287   <none>           <none>
```

Check if `a4-ray-cluster-head-svc` Service has been created successfully:

```sh
kubectl get services a4-ray-cluster-head-svc
NAME                            TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)                                AGE
a4-ray-cluster-head-svc   ClusterIP   None         <none>        10001/TCP,8265/TCP,6379/TCP,8080/TCP   13m
```

Identify your RayCluster’s head pod:

```sh
$ export HEAD_POD=$(kubectl get pods --selector=ray.io/node-type=head -o custom-columns=POD:metadata.name --no-headers)
$ echo $HEAD_POD
a4-ray-cluster-head
```

Print the cluster resources:

```sh
$ kubectl exec -it $HEAD_POD -- python -c "import pprint; import ray; ray.init(); pprint.pprint(ray.cluster_resources(), sort_dicts=True)"

2025-07-14 10:44:41,326 INFO worker.py:1520 -- Using address 127.0.0.1:6379 set in the environment variable RAY_ADDRESS
2025-07-14 10:44:41,327 INFO worker.py:1660 -- Connecting to existing Ray cluster at address: 10.48.2.6:6379...
2025-07-14 10:44:41,343 INFO worker.py:1843 -- Connected to Ray cluster. View the dashboard at 10.48.2.6:8265
{'CPU': 402.0,
 'GPU': 16.0,
 'accelerator_type:B200': 2.0,
 'memory': 3438653071770.0,
 'node:10.48.2.6': 1.0,
 'node:10.48.3.6': 1.0,
 'node:10.48.4.6': 1.0,
 'node:__internal_head__': 1.0,
 'object_store_memory': 401148243558.0}
```

Forward the port and check Ray dashboard:

```sh
kubectl port-forward svc/a4-ray-cluster-head-svc 8265:8265
Forwarding from 127.0.0.1:8265 -> 8265
Forwarding from [::1]:8265 -> 8265
Handling connection for 8265
```

#### GPU-to-GPU using Ray Collective Communication Library

Create a python file with the following code named `nccl_allreduce_multigpu.py`:

```python
import ray
import torch
import os

import ray.util.collective as collective


@ray.remote(num_gpus=8)
class Worker:
    def __init__(self):
        self.send_tensors = []
        self.send_tensors.append(torch.ones((4,), dtype=torch.float32, device='cuda:0'))
        self.send_tensors.append(torch.ones((4,), dtype=torch.float32, device='cuda:1') * 2)
        self.send_tensors.append(torch.ones((4,), dtype=torch.float32, device='cuda:2'))
        self.send_tensors.append(torch.ones((4,), dtype=torch.float32, device='cuda:3') * 2)
        self.send_tensors.append(torch.ones((4,), dtype=torch.float32, device='cuda:4'))
        self.send_tensors.append(torch.ones((4,), dtype=torch.float32, device='cuda:5') * 2)
        self.send_tensors.append(torch.ones((4,), dtype=torch.float32, device='cuda:6'))
        self.send_tensors.append(torch.ones((4,), dtype=torch.float32, device='cuda:7') * 2)

        self.recv = torch.zeros((4,), dtype=torch.float32, device='cuda:0')

    def setup(self, world_size, rank):
        collective.init_collective_group(world_size, rank, "nccl", "177")
        return True

    def compute(self):
        collective.allreduce_multigpu(self.send_tensors, "177")
        
        cpu_tensors = [t.cpu() for t in self.send_tensors]

        return (
            cpu_tensors,
            self.send_tensors[0].device,
            self.send_tensors[1].device,
            self.send_tensors[2].device,
            self.send_tensors[3].device,
            self.send_tensors[4].device,
            self.send_tensors[5].device,
            self.send_tensors[6].device,
            self.send_tensors[7].device,
        )

    def destroy(self):
        collective.destroy_collective_group("177")


if __name__ == "__main__":
    ray.init(address="auto")

    num_workers = 2 
    workers = []
    init_rets = []

    for i in range(num_workers):
        w = Worker.remote()
        workers.append(w)
        init_rets.append(w.setup.remote(num_workers, i))
    
    ray.get(init_rets)
    print("Collective groups initialized.")

    results = ray.get([w.compute.remote() for w in workers])
    
    print("\n--- Allreduce Results ---")
    for i, (tensors_list, *devices) in enumerate(results):
        print(f"Worker {i} results:")
        for j, tensor in enumerate(tensors_list):
            print(f"  Tensor {j} (originally on {devices[j]}): {tensor}") 

    ray.get([w.destroy.remote() for w in workers])
    print("\nCollective groups destroyed.")

    ray.shutdown()
```

Create Ray job (should be created with the previously port forwarded, in this
case 8265):

```sh
$ ray job submit --address="http://localhost:8265" --runtime-env-json='{"working_dir": ".", "pip": ["torch"]}' -- python nccl_allreduce_multigpu.py
Job submission server address: http://localhost:8265
2025-07-14 17:32:08,731 INFO dashboard_sdk.py:338 -- Uploading package gcs://_ray_pkg_ec361f13f7b82502.zip.
2025-07-14 17:32:08,733 INFO packaging.py:588 -- Creating a file package for local module '.'.

-------------------------------------------------------
Job 'raysubmit_QQTKZQDTDA3ifPMW' submitted successfully
-------------------------------------------------------

Next steps
  Query the logs of the job:
    ray job logs raysubmit_QQTKZQDTDA3ifPMW
  Query the status of the job:
    ray job status raysubmit_QQTKZQDTDA3ifPMW
  Request the job to be stopped:
    ray job stop raysubmit_QQTKZQDTDA3ifPMW

Tailing logs until the job exits (disable with --no-wait):

<snipped>

--- Allreduce Results ---
Worker 0 results:
(Worker pid=3590, ip=10.48.4.17) id=0x15b3, options=0x0, comp_mask=0x0}
(Worker pid=3590, ip=10.48.4.17) a4-ray-cluster-gpu-group-worker-pbkpw:3590:3778 [6] NCCL INFO NET/gIB: IbDev 6 Port 1 qpn 2440 se
  Tensor 0 (originally on cuda:0): tensor([24., 24., 24., 24.])
  Tensor 1 (originally on cuda:1): tensor([24., 24., 24., 24.])
  Tensor 2 (originally on cuda:2): tensor([24., 24., 24., 24.])
  Tensor 3 (originally on cuda:3): tensor([24., 24., 24., 24.])
  Tensor 4 (originally on cuda:4): tensor([24., 24., 24., 24.])
  Tensor 5 (originally on cuda:5): tensor([24., 24., 24., 24.])
  Tensor 6 (originally on cuda:6): tensor([24., 24., 24., 24.])
  Tensor 7 (originally on cuda:7): tensor([24., 24., 24., 24.])
Worker 1 results:
  Tensor 0 (originally on cuda:0): tensor([24., 24., 24., 24.])
  Tensor 1 (originally on cuda:1): tensor([24., 24., 24., 24.])
  Tensor 2 (originally on cuda:2): tensor([24., 24., 24., 24.])
  Tensor 3 (originally on cuda:3): tensor([24., 24., 24., 24.])
  Tensor 4 (originally on cuda:4): tensor([24., 24., 24., 24.])
  Tensor 5 (originally on cuda:5): tensor([24., 24., 24., 24.])
  Tensor 6 (originally on cuda:6): tensor([24., 24., 24., 24.])
  Tensor 7 (originally on cuda:7): tensor([24., 24., 24., 24.])

<snipped>
```

Since we are setting the informational NCCL environment variables NCCL_DEBUG and
NCCL_DEBUG_SUBSYS we can verify in the logs that RDMA GPUDirect is being used:

```sh
# [... snipped ...]
# The gIB (InfiniBand) plugin is initialized
[cite_start][... (Worker pid=3590, ip=10.48.4.17) [0m a4-ray-cluster-gpu-group-worker-pbkpw:3590:3753 [2] NCCL INFO NET/gIB : Initializing gIB v1.0.6 [cite: 1887]
[cite_start][... (Worker pid=3590, ip=10.48.4.17) [0m a4-ray-cluster-gpu-group-worker-pbkpw:3590:3753 [2] NCCL INFO Initialized NET plugin gIB [cite: 1889]

# Environment variable for GPU Direct RDMA level is detected
[cite_start][... (Worker pid=3590, ip=10.48.4.17) [0m a4-ray-cluster-gpu-group-worker-pbkpw:3590:3754 [3] NCCL INFO NCCL_NET_GDR_LEVEL set by environment to PIX [cite: 59]

# NCCL confirms that GPU Direct RDMA is enabled for each HCA (NIC) and GPU pairing
[cite_start][... (Worker pid=3590, ip=10.48.4.17) [0m a4-ray-cluster-gpu-group-worker-pbkpw:3590:3758 [7] NCCL INFO NET/gIB : GPU Direct RDMA Enabled for HCA 0 'mlx5_0' [cite: 41]
[cite_start][... (Worker pid=3590, ip=10.48.4.17) [0m a4-ray-cluster-gpu-group-worker-pbkpw:3590:3754 [3] NCCL INFO GPU Direct RDMA Enabled for GPU 7 / HCA 0 (distance 4 <= 4), read 0 mode Default [cite: 66]

# Finally, communication channels are established using GDRDMA
[cite_start][... (Worker pid=3590, ip=10.48.4.17) [0m a4-ray-cluster-gpu-group-worker-pbkpw:3590:3799 [2] NCCL INFO Channel 02/0 : 10[2] -> 2[2] [receive] via NET/gIB/2/GDRDMA [cite: 1734]
[cite_start][... (Worker pid=3590, ip=10.48.4.17) [0m a4-ray-cluster-gpu-group-worker-pbkpw:3590:3799 [2] NCCL INFO Channel 02/0 : 2[2] -> 10[2] [send] via NET/gIB/2/GDRDMA [cite: 1739]
# [... snipped ...]
```
