---
title: "GKE and GPUDirect RDMA with DRA"
date: 2025-05-27T11:30:40Z
---

On Google Cloud A3 Ultra and A4 machine types, you can utilize GPUDirect RDMA to run distributed AI workloads that require high performance networking support. To get started, create a [GKE cluster with DRA support](https://cloud.google.com/kubernetes-engine/docs/how-to/set-up-dra) and the corresponding [VPC and subnets](https://cloud.google.com/ai-hypercomputer/docs/create/gke-ai-hypercompute-custom#create-vpcs-and-subnets) for the RDMA network for the A3Ultra or A4 Node Pools, the `gcloud` commands should be something like:

```sh
PROJECT="gke-dranet"
CLUSTER="dranet-dranet"
REGION="us-west8"
ZONE="us-west8-c"
GVNIC_NETWORK_PREFIX="dranet-gvnic"
RDMA_NETWORK_PREFIX="dranet-rdma"
VERSION="1.34"

gcloud container clusters create "${CLUSTER}" \
    --cluster-version="${VERSION}" \
    --enable-multi-networking \
    --enable-dataplane-v2 \
    --no-enable-autorepair \
    --no-enable-autoupgrade \
    --zone="${ZONE}" \
    --project="${PROJECT}"

# Create a VPC for the additional Google Titanium CPU NIC
gcloud compute --project=${PROJECT?} \
  networks create \
  ${GVNIC_NETWORK_PREFIX?}-net \
  --subnet-mode=custom

gcloud compute --project=${PROJECT?} \
  networks subnets create \
  ${GVNIC_NETWORK_PREFIX?}-sub \
  --network=${GVNIC_NETWORK_PREFIX?}-net \
  --region=${REGION?} \
  --range=192.168.0.0/24

gcloud compute --project=${PROJECT?} \
  firewall-rules create \
  ${GVNIC_NETWORK_PREFIX?}-internal \
  --network=${GVNIC_NETWORK_PREFIX?}-net \
  --action=ALLOW \
  --rules=tcp:0-65535,udp:0-65535,icmp \
  --source-ranges=192.168.0.0/16

# Create HPC VPC for the RDMA NICs with 8 subnets.
gcloudcompute --project=${PROJECT?} \
  networks create ${RDMA_NETWORK_PREFIX?}-net \
  --network-profile=${ZONE?}-vpc-roce \
  --subnet-mode=custom

# Create subnets for the HPC VPC.
for N in $(seq 0 7); do
  gcloud compute --project=${PROJECT?} \
    networks subnets create \
    ${RDMA_NETWORK_PREFIX?}-sub-$N \
    --network=${RDMA_NETWORK_PREFIX?}-net \
    --region=${REGION?} \
    --range=192.168.$((N+1)).0/24 &  # offset to avoid overlap with gvnics
done

gcloud container node-pools create dranet-a4 \
  --cluster ${CLUSTER} \
  --project ${PROJECT} \
  --zone ${ZONE} \
  --node-locations ${ZONE} \
  --machine-type a4-highgpu-8g\
  --accelerator "type=nvidia-b200,count=8,gpu-driver-version=default" --num-nodes "2" \
  --additional-node-network network=${GVNIC_NETWORK_PREFIX}-net,subnetwork=${GVNIC_NETWORK_PREFIX}-sub \
  --additional-node-network network=${RDMA_NETWORK_PREFIX}-net,subnetwork=${RDMA_NETWORK_PREFIX}-sub-0 \
  --additional-node-network network=${RDMA_NETWORK_PREFIX}-net,subnetwork=${RDMA_NETWORK_PREFIX}-sub-1 \
  --additional-node-network network=${RDMA_NETWORK_PREFIX}-net,subnetwork=${RDMA_NETWORK_PREFIX}-sub-2 \
  --additional-node-network network=${RDMA_NETWORK_PREFIX}-net,subnetwork=${RDMA_NETWORK_PREFIX}-sub-3 \
  --additional-node-network network=${RDMA_NETWORK_PREFIX}-net,subnetwork=${RDMA_NETWORK_PREFIX}-sub-4 \
  --additional-node-network network=${RDMA_NETWORK_PREFIX}-net,subnetwork=${RDMA_NETWORK_PREFIX}-sub-5 \
  --additional-node-network network=${RDMA_NETWORK_PREFIX}-net,subnetwork=${RDMA_NETWORK_PREFIX}-sub-6 \
  --additional-node-network network=${RDMA_NETWORK_PREFIX}-net,subnetwork=${RDMA_NETWORK_PREFIX}-sub-7
```

Apply the following DaemonSet to install the RDMA binaries and the NCCL library on the node. The RDMA binaries are stored in `/home/kubernetes/bin/gib` directory and the NCCL library is stored in `/home/kubernetes/bin/nvidia/lib64` directory on the VM:

```sh
kubectl apply -f https://raw.githubusercontent.com/GoogleCloudPlatform/container-engine-accelerators/refs/heads/master/gpudirect-rdma/nccl-rdma-installer.yaml
```

Apply the following manifest to install DRANET:

```sh
kubectl apply -f https://raw.githubusercontent.com/google/dranet/refs/heads/main/install.yaml
```

Once DRANET is running you'll be able to obtain the network resources exposed via the daemonsets, per example, this specific node has 8 RDMA nics as per the machine specification:

```sh
 kubectl get resourceslices --field-selector spec.nodeName=gke-dra-1-gpu-nodes-2-e5f6f579-7je4 -o yaml | grep rdma
          dra.net/rdma:
            string: gpu0rdma0
          dra.net/rdma:
      name: gpu0rdma0
            string: gpu1rdma0
          dra.net/rdma:
      name: gpu1rdma0
            string: gpu2rdma0
          dra.net/rdma:
      name: gpu2rdma0
            string: gpu3rdma0
          dra.net/rdma:
      name: gpu3rdma0
            string: gpu4rdma0
          dra.net/rdma:
      name: gpu4rdma0
            string: gpu5rdma0
          dra.net/rdma:
      name: gpu5rdma0
            string: gpu6rdma0
          dra.net/rdma:
      name: gpu6rdma0
            string: gpu7rdma0
          dra.net/rdma:
      name: gpu7rdma0
          dra.net/rdma:
```


#### Defining Resources for DRANET

First, we tell DRANET what kind of NICs we're interested in and how Pods can claim them. In order to simplify our workloads we can create a `DeviceClass` that matches only the resources exposed by DRANET.

**DeviceClass (dranet):** This selects NICs managed by DRANET.

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: dranet
spec:
  selectors:
    - cel:
        expression: device.driver == "dra.net"
```

**ResourceClaimTemplate (worker-rdma-nic-template):** This will request 8 RDMA NICs.

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: rdma-net-template-gib
spec:
  spec:
    devices:
      requests:
      - name: rdma-net-interface
        exactly:
          deviceClassName: dranet
          count: 8
          selectors:
          - cel:
              expression: device.attributes["dra.net"].rdma == true
```

#### Creating the workload

We'll define a Statefulset with two workers, each getting the 8 GPUs and NICs from the VM. A headless Service will allow us to use DNS for autodiscovery.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: nccl-gib-test
spec:
  selector:
    name: nccl-gib-test
  clusterIP: None
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: nccl-gib-test
  labels:
    name: nccl-gib-test
spec:
  replicas: 2
  serviceName: nccl-gib-test
  selector:
    matchLabels:
      name: nccl-gib-test
  template:
    metadata:
      labels:
        name: nccl-gib-test
    spec:
      containers:
      - image: us-docker.pkg.dev/gce-ai-infra/gpudirect-gib/nccl-plugin-gib-diagnostic:v1.0.6
        name: test
        securityContext:
          capabilities:
            add: ["IPC_LOCK"]
        resources:
          limits:
            nvidia.com/gpu: 8
        volumeMounts:
          - name: library-dir-host
            mountPath: /usr/local/nvidia
          - name: gib
            mountPath: /usr/local/gib
          - name: shared-memory
            mountPath: /dev/shm
        env:
          - name: LD_LIBRARY_PATH
            value: /usr/local/nvidia/lib64
        command: ["/bin/bash", "-c"]
        args:
          - |
            # we use a headless service to identify the workers that has the format <hostname>.<service>.<ns>.svc.<zone>
            # hence we need to allow to resolve fqdn 
            echo -e "\norte_keep_fqdn_hostnames=t" >> /etc/openmpi/openmpi-mca-params.conf
            /scripts/container_entry.sh shell
            source /usr/local/gib/scripts/set_nccl_env.sh
            sleep infinity
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
      resourceClaims:
      - name: rdma-net-interface
        resourceClaimTemplateName: rdma-net-template-gib
```

#### Running and Observing

Once deployed, we can see how the pods are scheduled

```sh
kubectl get pods
NAME                                                               READY   STATUS      RESTARTS   AGE
nccl-gib-test-0                                                    1/1     Running     0          6h35m
nccl-gib-test-1                                                    1/1     Running     0          4h26m
```

and all the NICs are attached

```sh
kubectl get resourceclaims
NAME                                       STATE                AGE
nccl-gib-test-0-rdma-net-interface-mdsfv   allocated,reserved   6h37m
nccl-gib-test-1-rdma-net-interface-t6jn8   allocated,reserved   4h28m
```

we can see all NICs on the node are connected to the Pod

```sh
kubectl get resourceclaim -o yaml
apiVersion: v1
items:
- apiVersion: resource.k8s.io/v1
  kind: ResourceClaim
  metadata:
    annotations:
      resource.kubernetes.io/pod-claim-name: rdma-net-interface
    creationTimestamp: "2025-06-12T17:01:57Z"
    finalizers:
    - resource.kubernetes.io/delete-protection
    generateName: nccl-gib-test-0-rdma-net-interface-
    name: nccl-gib-test-0-rdma-net-interface-mdsfv
    namespace: default
    ownerReferences:
    - apiVersion: v1
      blockOwnerDeletion: true
      controller: true
      kind: Pod
      name: nccl-gib-test-0
      uid: 9af7b491-9342-412d-8fc2-0eedbc36a7b8
    resourceVersion: "53060805"
    uid: fda69ec2-9847-4f64-9540-432cba2489c7
  spec:
    devices:
      requests:
      - name: rdma-net-interface
        exactly:
          allocationMode: All
          deviceClassName: dranet
          selectors:
          - cel:
              expression: device.attributes["dra.net"].rdma == true
  status:
    allocation:
      devices:
        results:
        - adminAccess: null
          device: gpu0rdma0
          driver: dra.net
          pool: gke-dra-1-gpu-nodes-2-e5f6f579-7je4
          request: rdma-net-interface
        - adminAccess: null
          device: gpu1rdma0
          driver: dra.net
          pool: gke-dra-1-gpu-nodes-2-e5f6f579-7je4
          request: rdma-net-interface
        - adminAccess: null
          device: gpu2rdma0
          driver: dra.net
          pool: gke-dra-1-gpu-nodes-2-e5f6f579-7je4
          request: rdma-net-interface
        - adminAccess: null
          device: gpu3rdma0
          driver: dra.net
          pool: gke-dra-1-gpu-nodes-2-e5f6f579-7je4
          request: rdma-net-interface
        - adminAccess: null
          device: gpu4rdma0
          driver: dra.net
          pool: gke-dra-1-gpu-nodes-2-e5f6f579-7je4
          request: rdma-net-interface
        - adminAccess: null
          device: gpu5rdma0
          driver: dra.net
          pool: gke-dra-1-gpu-nodes-2-e5f6f579-7je4
          request: rdma-net-interface
        - adminAccess: null
          device: gpu6rdma0
          driver: dra.net
          pool: gke-dra-1-gpu-nodes-2-e5f6f579-7je4
          request: rdma-net-interface
        - adminAccess: null
          device: gpu7rdma0
          driver: dra.net
          pool: gke-dra-1-gpu-nodes-2-e5f6f579-7je4
          request: rdma-net-interface
```

To test the performance we will run the NCCL-tests, that are already installed in the existing workloads:

```sh
kubectl exec -it nccl-gib-test-0 -- /usr/local/gib/scripts/run_nccl_tests.sh -t all_gather -b 1K -e 8G nccl-gib-test-0.nccl-gib-test nccl-gib-test-1.nccl-gib-test
Initializing SSH...
Warning: Permanently added '[nccl-gib-test-0.nccl-gib-test]:222' (ED25519) to the list of known hosts.
Hello from nccl-gib-test-0.nccl-gib-test
Hello from nccl-gib-test-1.nccl-gib-test
Generating hostfiles for 2 hosts:
nccl-gib-test-0.nccl-gib-test
nccl-gib-test-1.nccl-gib-test
# nThread 1 nGpus 1 minBytes 1024 maxBytes 8589934592 step: 2(factor) warmup iters: 50 iters: 100 agg iters: 1 validation: 1 graph: 0
#
# Using devices
#  Rank  0 Group  0 Pid  28616 on nccl-gib-test-0 device  0 [0000:8f:00] NVIDIA B200
#  Rank  1 Group  0 Pid  28617 on nccl-gib-test-0 device  1 [0000:90:00] NVIDIA B200
#  Rank  2 Group  0 Pid  28620 on nccl-gib-test-0 device  2 [0000:96:00] NVIDIA B200
#  Rank  3 Group  0 Pid  28629 on nccl-gib-test-0 device  3 [0000:97:00] NVIDIA B200
#  Rank  4 Group  0 Pid  28635 on nccl-gib-test-0 device  4 [0000:c4:00] NVIDIA B200
#  Rank  5 Group  0 Pid  28644 on nccl-gib-test-0 device  5 [0000:c5:00] NVIDIA B200
#  Rank  6 Group  0 Pid  28655 on nccl-gib-test-0 device  6 [0000:cb:00] NVIDIA B200
#  Rank  7 Group  0 Pid  28667 on nccl-gib-test-0 device  7 [0000:cc:00] NVIDIA B200
#  Rank  8 Group  0 Pid  22707 on nccl-gib-test-1 device  0 [0000:8f:00] NVIDIA B200
#  Rank  9 Group  0 Pid  22708 on nccl-gib-test-1 device  1 [0000:90:00] NVIDIA B200
#  Rank 10 Group  0 Pid  22711 on nccl-gib-test-1 device  2 [0000:96:00] NVIDIA B200
#  Rank 11 Group  0 Pid  22720 on nccl-gib-test-1 device  3 [0000:97:00] NVIDIA B200
#  Rank 12 Group  0 Pid  22727 on nccl-gib-test-1 device  4 [0000:c4:00] NVIDIA B200
#  Rank 13 Group  0 Pid  22739 on nccl-gib-test-1 device  5 [0000:c5:00] NVIDIA B200
#  Rank 14 Group  0 Pid  22749 on nccl-gib-test-1 device  6 [0000:cb:00] NVIDIA B200
#  Rank 15 Group  0 Pid  22763 on nccl-gib-test-1 device  7 [0000:cc:00] NVIDIA B200
NCCL version 2.26.6+cuda12.8
#
#                                                              out-of-place                       in-place
#       size         count      type   redop    root     time   algbw   busbw #wrong     time   algbw   busbw #wrong
#        (B)    (elements)                               (us)  (GB/s)  (GB/s)            (us)  (GB/s)  (GB/s)
        1024            16     float    none      -1    280.4    0.00    0.00      0    233.9    0.00    0.00      0
        2048            32     float    none      -1    268.2    0.01    0.01      0    303.2    0.01    0.01      0
        4096            64     float    none      -1    317.7    0.01    0.01      0    292.7    0.01    0.01      0
        8192           128     float    none      -1    320.0    0.03    0.02      0    274.0    0.03    0.03      0
       16384           256     float    none      -1    290.3    0.06    0.05      0    283.4    0.06    0.05      0
       32768           512     float    none      -1    246.2    0.13    0.12      0    328.1    0.10    0.09      0
       65536          1024     float    none      -1    245.9    0.27    0.25      0    290.5    0.23    0.21      0
      131072          2048     float    none      -1    287.1    0.46    0.43      0    281.7    0.47    0.44      0
      262144          4096     float    none      -1    379.6    0.69    0.65      0    483.9    0.54    0.51      0
      524288          8192     float    none      -1    549.9    0.95    0.89      0    575.9    0.91    0.85      0
     1048576         16384     float    none      -1    998.2    1.05    0.98      0    972.4    1.08    1.01      0
     2097152         32768     float    none      -1   1753.7    1.20    1.12      0   1557.5    1.35    1.26      0
     4194304         65536     float    none      -1    933.6    4.49    4.21      0    958.6    4.38    4.10      0
     8388608        131072     float    none      -1    981.2    8.55    8.02      0   1114.4    7.53    7.06      0
    16777216        262144     float    none      -1   1479.0   11.34   10.63      0   1507.2   11.13   10.44      0
    33554432        524288     float    none      -1   1240.7   27.05   25.36      0   1490.0   22.52   21.11      0
    67108864       1048576     float    none      -1   1621.8   41.38   38.79      0   1524.0   44.03   41.28      0
   134217728       2097152     float    none      -1   1877.8   71.48   67.01      0   1628.0   82.44   77.29      0
   268435456       4194304     float    none      -1   3160.2   84.94   79.63      0   2993.9   89.66   84.06      0
   536870912       8388608     float    none      -1   3332.1  161.12  151.05      0   3138.5  171.06  160.37      0
  1073741824      16777216     float    none      -1   4057.5  264.63  248.09      0   4372.7  245.56  230.21      0
  2147483648      33554432     float    none      -1   7753.4  276.97  259.66      0   7495.6  286.50  268.59      0
  4294967296      67108864     float    none      -1    15394  279.00  261.57      0    15332  280.13  262.62      0
  8589934592     134217728     float    none      -1    30970  277.36  260.03      0    31063  276.53  259.25      0
# Out of bounds values : 0 OK
# Avg bus bandwidth    : 59.3638

```

#### Running a Workload with 4 Workers, 4 GPUs, and 4 NICs Each in 2 Nodes

While using fewer, larger instances (e.g., 2 workers with 8 GPUs/NICs) can simplify deployment, distributed AI workloads may benefit from a more granular approach, utilizing more workers with fewer resources each. This can lead to better resource utilization, increased parallelism, and improved fault tolerance. Here, we'll configure our workload to use 4 workers, with each worker claiming 4 GPUs and 4 corresponding DRANET-managed RDMA NICs.

For optimal performance in distributed AI, it's crucial to ensure the network interfaces are topologically closest to their associated GPUs. We can determine the host topology using `nvidia-smi topo -m`:

```sh
        GPU0    GPU1    GPU2    GPU3    GPU4    GPU5    GPU6    GPU7    NIC0    NIC1    NIC2    NIC3    NIC4    NIC5    NIC6    NIC7    CPU Affinity    NUMA Affinity      GPU NUMA ID
GPU0     X      NV18    NV18    NV18    NV18    NV18    NV18    NV18    PIX     PIX     NODE    NODE    SYS     SYS     SYS     SYS     0-55,112-167    0 N/A
GPU1    NV18     X      NV18    NV18    NV18    NV18    NV18    NV18    PIX     PIX     NODE    NODE    SYS     SYS     SYS     SYS     0-55,112-167    0 N/A
GPU2    NV18    NV18     X      NV18    NV18    NV18    NV18    NV18    NODE    NODE    PIX     PIX     SYS     SYS     SYS     SYS     0-55,112-167    0 N/A
GPU3    NV18    NV18    NV18     X      NV18    NV18    NV18    NV18    NODE    NODE    PIX     PIX     SYS     SYS     SYS     SYS     0-55,112-167    0 N/A
GPU4    NV18    NV18    NV18    NV18     X      NV18    NV18    NV18    SYS     SYS     SYS     SYS     PIX     PIX     NODE    NODE    56-111,168-223  1 N/A
GPU5    NV18    NV18    NV18    NV18    NV18     X      NV18    NV18    SYS     SYS     SYS     SYS     PIX     PIX     NODE    NODE    56-111,168-223  1 N/A
GPU6    NV18    NV18    NV18    NV18    NV18    NV18     X      NV18    SYS     SYS     SYS     SYS     NODE    NODE    PIX     PIX     56-111,168-223  1 N/A
GPU7    NV18    NV18    NV18    NV18    NV18    NV18    NV18     X      SYS     SYS     SYS     SYS     NODE    NODE    PIX     PIX     56-111,168-223  1 N/A
NIC0    PIX     PIX     NODE    NODE    SYS     SYS     SYS     SYS      X      PIX     NODE    NODE    SYS     SYS     SYS     SYS
NIC1    PIX     PIX     NODE    NODE    SYS     SYS     SYS     SYS     PIX      X      NODE    NODE    SYS     SYS     SYS     SYS
NIC2    NODE    NODE    PIX     PIX     SYS     SYS     SYS     SYS     NODE    NODE     X      PIX     SYS     SYS     SYS     SYS
NIC3    NODE    NODE    PIX     PIX     SYS     SYS     SYS     SYS     NODE    NODE    PIX      X      SYS     SYS     SYS     SYS
NIC4    SYS     SYS     SYS     SYS     PIX     PIX     NODE    NODE    SYS     SYS     SYS     SYS      X      PIX     NODE    NODE
NIC5    SYS     SYS     SYS     SYS     PIX     PIX     NODE    NODE    SYS     SYS     SYS     SYS     PIX      X      NODE    NODE
NIC6    SYS     SYS     SYS     SYS     NODE    NODE    PIX     PIX     SYS     SYS     SYS     SYS     NODE    NODE     X      PIX
NIC7    SYS     SYS     SYS     SYS     NODE    NODE    PIX     PIX     SYS     SYS     SYS     SYS     NODE    NODE    PIX      X

Legend:

  X    = Self
  SYS  = Connection traversing PCIe as well as the SMP interconnect between NUMA nodes (e.g., QPI/UPI)
  NODE = Connection traversing PCIe as well as the interconnect between PCIe Host Bridges within a NUMA node
  PHB  = Connection traversing PCIe as well as a PCIe Host Bridge (typically the CPU)
  PXB  = Connection traversing multiple PCIe bridges (without traversing the PCIe Host Bridge)
  PIX  = Connection traversing at most a single PCIe bridge
  NV#  = Connection traversing a bonded set of # NVLinks

NIC Legend:

  NIC0: mlx5_0
  NIC1: mlx5_1
  NIC2: mlx5_2
  NIC3: mlx5_3
  NIC4: mlx5_4
  NIC5: mlx5_5
  NIC6: mlx5_6
  NIC7: mlx5_7
```

To facilitate this configuration, we define a `ResourceClaimTemplate` that requests 4 RDMA NICs per worker. GKE A4 machines follow a naming convention where RDMA NICs are named `gpuXrdma0`, with X corresponding to the associated GPU index.

While a dedicated GPU DRA driver could leverage topological alignment using constraints and the standardized `resource.kubernetes.io/pcieRoot` attribute for optimal grouping (as discussed in [NVIDIA/k8s-dra-driver-gpu#213](https://github.com/NVIDIA/k8s-dra-driver-gpu/issues/213)), just for this example and to show the flexibility of DRANET, we'll assume the GPU driver will implicitly provide the correct GPU device.

We are going to explicitly leverage the GKE naming convention to ensure each worker is allocated either the lower block of 4 NICs (gpu0-3rdma0) or the higher block of 4 NICs (gpu4-7rdma0). This selection logic is embedded directly within the selectors of our `ResourceClaimTemplate` using a CEL expression.

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: rdma-net-template-gib-flexible4nics
spec:
  spec:
    devices:
      requests:
      - name: rdma-net-interface
        exactly:
          deviceClassName: dranet
          count: 4 # Requesting 4 NICs per worker
          selectors:
          - cel:
              expression: |
                device.attributes["dra.net"].rdma == true &&
                (
                  (device.attributes["dra.net"].ifName.startsWith("gpu") &&
                  device.attributes["dra.net"].ifName.endsWith("rdma0") &&
                  int(device.attributes["dra.net"].ifName.substring(3, 4)) < 4)
                ||
                  (device.attributes["dra.net"].ifName.startsWith("gpu") &&
                  device.attributes["dra.net"].ifName.endsWith("rdma0") &&
                  int(device.attributes["dra.net"].ifName.substring(3, 4)) >= 4)
                )
```

We'll define a StatefulSet with four replicas, each configured to receive 4 GPUs and the 4 requested RDMA NICs.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: nccl-gib-test-4w
spec:
  selector:
    name: nccl-gib-test-4w
  clusterIP: None
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: nccl-gib-test-4w
  labels:
    name: nccl-gib-test-4w
spec:
  replicas: 4
  serviceName: nccl-gib-test-4w
  selector:
    matchLabels:
      name: nccl-gib-test-4w
  template:
    metadata:
      labels:
        name: nccl-gib-test-4w
    spec:
      containers:
      - image: us-docker.pkg.dev/gce-ai-infra/gpudirect-gib/nccl-plugin-gib-diagnostic:v1.0.6
        name: test
        securityContext:
          capabilities:
            add: ["IPC_LOCK"]
        resources:
          limits:
            nvidia.com/gpu: 4
        volumeMounts:
          - name: library-dir-host
            mountPath: /usr/local/nvidia
          - name: gib
            mountPath: /usr/local/gib
          - name: shared-memory
            mountPath: /dev/shm
        env:
          - name: LD_LIBRARY_PATH
            value: /usr/local/nvidia/lib64
        command: ["/bin/bash", "-c"]
        args:
          - |
            # we use a headless service to identify the workers that has the format <hostname>.<service>.<ns>.svc.<zone>
            # hence we need to allow to resolve fqdn 
            echo -e "\norte_keep_fqdn_hostnames=t" >> /etc/openmpi/openmpi-mca-params.conf
            /scripts/container_entry.sh shell
            source /usr/local/gib/scripts/set_nccl_env.sh
            sleep infinity
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
            sizeLimit: 125Gi
      resourceClaims:
      - name: rdma-net-interface
        resourceClaimTemplateName: rdma-net-template-gib-flexible4nics
```

The pods will be scheduled with the corresponding interfaces:

```sh
kubectl get pods -o wide
NAME                 READY   STATUS    RESTARTS   AGE     IP          NODE                                                  NOMINATED NODE   READINESS GATES
nccl-gib-test-4w-0   1/1     Running   0          2m51s   10.68.5.7   gke-dranet-maspinwal-dranet-maspinwal-d3003787-lp53   <none>           <none>
nccl-gib-test-4w-1   1/1     Running   0          109s    10.68.3.7   gke-dranet-maspinwal-dranet-maspinwal-d3003787-zwtv   <none>           <none>
nccl-gib-test-4w-2   1/1     Running   0          51s     10.68.5.8   gke-dranet-maspinwal-dranet-maspinwal-d3003787-lp53   <none>           <none>
nccl-gib-test-4w-3   1/1     Running   0          48s     10.68.3.8   gke-dranet-maspinwal-dranet-maspinwal-d3003787-zwtv   <none>           <none>
```

And we can confirm that each Pod gets either the lower or higher block of NICs:

```sh
kubectl exec -it nccl-gib-test-4w-0  -- ip a
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
2: eth0@if20: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1460 qdisc noqueue state UP group default qlen 1000
    link/ether 8e:ea:cf:24:93:50 brd ff:ff:ff:ff:ff:ff link-netnsid 0
    inet 10.68.5.7/24 brd 10.68.5.255 scope global eth0
       valid_lft forever preferred_lft forever
4: gpu0rdma0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8896 qdisc mq state UP group default qlen 1000
    link/ether 7e:75:3c:80:88:01 brd ff:ff:ff:ff:ff:ff
    inet 192.168.1.4/32 scope global gpu0rdma0
       valid_lft forever preferred_lft forever
5: gpu1rdma0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8896 qdisc mq state UP group default qlen 1000
    link/ether ca:18:5a:64:f9:04 brd ff:ff:ff:ff:ff:ff
    inet 192.168.2.4/32 scope global gpu1rdma0
       valid_lft forever preferred_lft forever
6: gpu2rdma0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8896 qdisc mq state UP group default qlen 1000
    link/ether d2:c2:50:6e:7c:07 brd ff:ff:ff:ff:ff:ff
    inet 192.168.3.4/32 scope global gpu2rdma0
       valid_lft forever preferred_lft forever
7: gpu3rdma0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8896 qdisc mq state UP group default qlen 1000
    link/ether 02:8a:b8:7e:0a:0a brd ff:ff:ff:ff:ff:ff
    inet 192.168.4.4/32 scope global gpu3rdma0
       valid_lft forever preferred_lft forever
```

Now lets's run the NCCL-tests on this new 4 worker configuration:

```sh
kubectl exec -it nccl-gib-test-4w-0 -- /usr/local/gib/scripts/run_nccl_tests.sh -t all_gather -b 1K -g 4 -e 8G nccl-gib-test-4w-0.nccl-gib-test-4w nccl-gib-test-4w-1.nccl-gib-test-4w nccl-gib-test-4w-2.nccl-gib-test-4w nccl-gib-test-4w-3.nccl-gib-test-4w
Initializing SSH...
Hello from nccl-gib-test-4w-0.nccl-gib-test-4w
Hello from nccl-gib-test-4w-1.nccl-gib-test-4w
Hello from nccl-gib-test-4w-2.nccl-gib-test-4w
Hello from nccl-gib-test-4w-3.nccl-gib-test-4w
Generating hostfiles for 4 hosts:
nccl-gib-test-4w-0.nccl-gib-test-4w
nccl-gib-test-4w-1.nccl-gib-test-4w
nccl-gib-test-4w-2.nccl-gib-test-4w
nccl-gib-test-4w-3.nccl-gib-test-4w
# nThread 1 nGpus 1 minBytes 1024 maxBytes 8589934592 step: 2(factor) warmup iters: 50 iters: 100 agg iters: 1 validation: 1 graph: 0
#
# Using devices
#  Rank  0 Group  0 Pid  19517 on nccl-gib-test-4w-0 device  0 [0000:8f:00] NVIDIA B200
#  Rank  1 Group  0 Pid  19514 on nccl-gib-test-4w-0 device  1 [0000:90:00] NVIDIA B200
#  Rank  2 Group  0 Pid  19523 on nccl-gib-test-4w-0 device  2 [0000:cb:00] NVIDIA B200
#  Rank  3 Group  0 Pid  19534 on nccl-gib-test-4w-0 device  3 [0000:cc:00] NVIDIA B200
#  Rank  4 Group  0 Pid  19353 on nccl-gib-test-4w-1 device  0 [0000:8f:00] NVIDIA B200
#  Rank  5 Group  0 Pid  19374 on nccl-gib-test-4w-1 device  1 [0000:90:00] NVIDIA B200
#  Rank  6 Group  0 Pid  19383 on nccl-gib-test-4w-1 device  2 [0000:96:00] NVIDIA B200
#  Rank  7 Group  0 Pid  19399 on nccl-gib-test-4w-1 device  3 [0000:cc:00] NVIDIA B200
#  Rank  8 Group  0 Pid  19345 on nccl-gib-test-4w-2 device  0 [0000:96:00] NVIDIA B200
#  Rank  9 Group  0 Pid  19387 on nccl-gib-test-4w-2 device  1 [0000:97:00] NVIDIA B200
#  Rank 10 Group  0 Pid  19379 on nccl-gib-test-4w-2 device  2 [0000:c4:00] NVIDIA B200
#  Rank 11 Group  0 Pid  19398 on nccl-gib-test-4w-2 device  3 [0000:c5:00] NVIDIA B200
#  Rank 12 Group  0 Pid  19383 on nccl-gib-test-4w-3 device  0 [0000:97:00] NVIDIA B200
#  Rank 13 Group  0 Pid  19347 on nccl-gib-test-4w-3 device  1 [0000:c4:00] NVIDIA B200
#  Rank 14 Group  0 Pid  19390 on nccl-gib-test-4w-3 device  2 [0000:c5:00] NVIDIA B200
#  Rank 15 Group  0 Pid  19396 on nccl-gib-test-4w-3 device  3 [0000:cb:00] NVIDIA B200

# ... (truncated NCCL output for brevity) ...
# Out of bounds values : 0 OK
# Avg bus bandwidth    : 22.2173
#

```

This is just an illustrative example, there is no GPU and NIC alignment. Also, for collective operations like `all_gather` that benefit immensely from high-bandwidth, low-latency intra-node communication (like NVLink) in this case, consolidating more GPUs into fewer workers (i.e., making each worker span an entire physical node's GPU complement) results in significantly better performance. The 2-worker (8 GPUs/worker) configuration achieved much higher `all_gather` bandwidth compared to the 4-worker (4 GPUs/worker) configuration, despite both using the same total number of GPUs across the same two physical nodes.


To highlight the importance of topological alignment, we compare the previous results with a scenario where the GPUs and NICs are even more misaligned , the same `all_gather` test with 4 pods setup gives half of the performance than before.

```sh
 kubectl exec -it nccl-gib-test-4w-0 -- /usr/local/gib/scripts/run_nccl_tests.sh -t all_gather -b 1K -g 4 -e 8G nccl-gib-test-4w-0.nccl-gib-test-4w nccl-gib-test-4w-1.nccl-gib-test-4w nccl-gib-test-4w-2.nccl-gib-test-4w nccl-gib-test-4w-3.nccl-gib-test-4w
Initializing SSH...
Warning: Permanently added '[nccl-gib-test-4w-0.nccl-gib-test-4w]:222,[10.68.3.18]:222' (ECDSA) to the list of known hosts.
Hello from nccl-gib-test-4w-0.nccl-gib-test-4w
Warning: Permanently added '[nccl-gib-test-4w-1.nccl-gib-test-4w]:222,[10.68.3.19]:222' (ECDSA) to the list of known hosts.
Hello from nccl-gib-test-4w-1.nccl-gib-test-4w
Warning: Permanently added '[nccl-gib-test-4w-2.nccl-gib-test-4w]:222,[10.68.5.18]:222' (ECDSA) to the list of known hosts.
Hello from nccl-gib-test-4w-2.nccl-gib-test-4w
Warning: Permanently added '[nccl-gib-test-4w-3.nccl-gib-test-4w]:222,[10.68.5.19]:222' (ECDSA) to the list of known hosts.
Hello from nccl-gib-test-4w-3.nccl-gib-test-4w
Generating hostfiles for 4 hosts:
nccl-gib-test-4w-0.nccl-gib-test-4w
nccl-gib-test-4w-1.nccl-gib-test-4w
nccl-gib-test-4w-2.nccl-gib-test-4w
nccl-gib-test-4w-3.nccl-gib-test-4w
# nThread 1 nGpus 1 minBytes 1024 maxBytes 8589934592 step: 2(factor) warmup iters: 50 iters: 100 agg iters: 1 validation: 1 graph: 0
#
# Using devices
#  Rank  0 Group  0 Pid   3351 on nccl-gib-test-4w-0 device  0 [0000:97:00] NVIDIA B200
#  Rank  1 Group  0 Pid   3366 on nccl-gib-test-4w-0 device  1 [0000:c4:00] NVIDIA B200
#  Rank  2 Group  0 Pid   3392 on nccl-gib-test-4w-0 device  2 [0000:c5:00] NVIDIA B200
#  Rank  3 Group  0 Pid   3393 on nccl-gib-test-4w-0 device  3 [0000:cb:00] NVIDIA B200
#  Rank  4 Group  0 Pid   3317 on nccl-gib-test-4w-1 device  0 [0000:8f:00] NVIDIA B200
#  Rank  5 Group  0 Pid   3350 on nccl-gib-test-4w-1 device  1 [0000:90:00] NVIDIA B200
#  Rank  6 Group  0 Pid   3349 on nccl-gib-test-4w-1 device  2 [0000:96:00] NVIDIA B200
#  Rank  7 Group  0 Pid   3358 on nccl-gib-test-4w-1 device  3 [0000:cc:00] NVIDIA B200
#  Rank  8 Group  0 Pid   3321 on nccl-gib-test-4w-2 device  0 [0000:8f:00] NVIDIA B200
#  Rank  9 Group  0 Pid   3359 on nccl-gib-test-4w-2 device  1 [0000:c5:00] NVIDIA B200
#  Rank 10 Group  0 Pid   3358 on nccl-gib-test-4w-2 device  2 [0000:cb:00] NVIDIA B200
#  Rank 11 Group  0 Pid   3335 on nccl-gib-test-4w-2 device  3 [0000:cc:00] NVIDIA B200
#  Rank 12 Group  0 Pid   3316 on nccl-gib-test-4w-3 device  0 [0000:90:00] NVIDIA B200
#  Rank 13 Group  0 Pid   3355 on nccl-gib-test-4w-3 device  1 [0000:96:00] NVIDIA B200
#  Rank 14 Group  0 Pid   3359 on nccl-gib-test-4w-3 device  2 [0000:97:00] NVIDIA B200
#  Rank 15 Group  0 Pid   3341 on nccl-gib-test-4w-3 device  3 [0000:c4:00] NVIDIA B200

# ... (truncated NCCL output for brevity) ...
# Out of bounds values : 0 OK
# Avg bus bandwidth    : 11.2291
#
```

#### Troubleshooting Notes

If you encounter network failures during testing, indicated by errors such as:

```sh
nccl-gib-test-4w-3: Test CUDA failure common.cu:1030 'invalid device ordinal'

nccl-gib-test-4w-1: transport/nvls.cc:598 NCCL WARN Cuda failure 1 'invalid argument'
```

A common resolution is to reboot the affected virtual machines.

**References**:

https://github.com/NVIDIA/nccl/issues/1672

https://github.com/NVIDIA/nccl/issues/1562
