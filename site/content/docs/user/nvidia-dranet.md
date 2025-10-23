---
title: "GKE with NVIDIA DRA and DRANET"
date: 2025-06-20T10:10:40Z
---


To get started, create a [GKE cluster with DRA
support](https://cloud.google.com/kubernetes-engine/docs/how-to/set-up-dra) and
the corresponding [VPC and
subnets](https://cloud.google.com/ai-hypercomputer/docs/create/gke-ai-hypercompute-custom#create-vpcs-and-subnets)

It should look like 

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

Apply the following DaemonSet to install the RDMA binaries and the NCCL library
on the node. The RDMA binaries are stored in `/home/kubernetes/bin/gib`
directory and the NCCL library is stored in `/home/kubernetes/bin/nvidia/lib64`
directory on the VM:

```sh
kubectl apply -f https://raw.githubusercontent.com/GoogleCloudPlatform/container-engine-accelerators/refs/heads/master/gpudirect-rdma/nccl-rdma-installer.yaml
```

Install DRANET
```sh
kubectl apply -f https://raw.githubusercontent.com/google/dranet/refs/heads/main/install.yaml
```

#### Installing Nvidia DRA Drivers

In order to install the NVIDIA DRA Drivers you will need to clone the [NVIDIA
DRA](https://github.com/NVIDIA/k8s-dra-driver-gpu) repo. Ensure you have
[helm](https://helm.sh/docs/intro/install/) installed.

[KEP #4381](https://github.com/kubernetes/enhancements/pull/5316) proposes the
standard PCI Root attribute. This is an important field to have for devices
since  the alignment of multiple devices on the PCI bus can have major
implications of how fast the devices can communicate with each other.

Please ensure the GPU Driver image [includes the standard attribute
`resources.kubernetes.io/pcieRoot`](https://github.com/NVIDIA/k8s-dra-driver-gpu/pull/429)
so both GPU DRA driver and DRANET can use it for NIC alignment.

```
helm upgrade -i --create-namespace --namespace nvidia-dra-driver-gpu nvidia-dra-driver-gpu ./k8s-dra-driver-gpu/deployments/helm/nvidia-dra-driver-gpu --values https://raw.githubusercontent.com/google/dranet/refs/heads/main/examples/demo_nvidia_dranet/values.yaml --wait
```

The values.yaml adds some additional tolerations and removes some priorities
that need to be done in order to work nicely with GKE.

Once this is done, you can run 

```sh
kubectl get pods -n nvidia-dra-driver-gpu
NAME                                                READY   STATUS     RESTARTS   AGE
nvidia-dra-driver-gpu-controller-66696889cd-86m8f   1/1     Running    0          13m
```
If you only see the controller like above, you will need to label the nodes with
GPUs on them in order to get the kubelet plugin running.

```sh
kubectl label node -l cloud.google.com/gke-gpu=true --overwrite nvidia.com/gpu.present=true

kubectl get pods -n nvidia-dra-driver-gpu
NAME                                                READY   STATUS     RESTARTS   AGE
nvidia-dra-driver-gpu-controller-66696889cd-86m8f   1/1     Running    0          12m
nvidia-dra-driver-gpu-kubelet-plugin-ffzgx          2/2     Running    0          34s
nvidia-dra-driver-gpu-kubelet-plugin-qsp2d          2/2     Running    0          33s
```

Once you see all these pods, the NVIDIA DRA plugin is working as expected

#### Creating a GPU workload

We can create a `ResourceClaimTemplate` to specify what GPUs we want. We
currently don't have PCI attributes yet in the NVIDIA driver library so we will
want to specify the index for the time being. This isn't too important for this
section but will come into relevance once we start pairing NICs to the nodes.

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: 2-gpu
spec:
  spec:
    devices:
      requests:
      - name: gpu
        exactly:
          deviceClassName: gpu.nvidia.com
          count: 2
          selectors:
          - cel:
              expression: |
                  device.attributes["gpu.nvidia.com"].index < 2
```

Create a statefulset which claims these resources.

```yaml
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
            nvidia-smi -L
            echo -e "\norte_keep_fqdn_hostnames=t" >> /etc/openmpi/openmpi-mca-params.conf
            /scripts/container_entry.sh shell
            source /usr/local/gib/scripts/set_nccl_env.sh
            sleep infinity
        resources:
          claims:
          - name: gpu            
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
        - name: gpu
          resourceClaimTemplateName: 2-gpu
      tolerations:
      - key: "nvidia.com/gpu"
        operator: "Equal"
        value: "present"
        effect: "NoSchedule"
```

Note how unlike the other examples, we don't use the resources field in the spec
to allocate GPUs, nor do we manually mount the Nvidia libraries. This is all
handled by the DRA driver that Nvidia provides. Execing into one of these nodes
and listing the gpus shows that two B200 GPUs were allocated.

```sh
root@nccl-gib-test-0:/usr/bin# nvidia-smi -L
GPU 0: NVIDIA B200 (UUID: GPU-00261f28-8bd7-afb7-c2d9-897ff3f13706)
GPU 1: NVIDIA B200 (UUID: GPU-f538682c-7be3-18c8-91b6-5a3fc69143d0)
```

Let's try running NCCL!

```sh
root@nccl-gib-test-0:/diagnostic# /usr/local/gib/scripts/run_nccl_tests.sh   -t all_gather -b 1K -e 8G   nccl-gib-test-0  -g 2
Initializing SSH...
Warning: Permanently added '[nccl-gib-test-0]:222,[10.68.3.42]:222' (ECDSA) to the list of known hosts.
Hello from nccl-gib-test-0
Generating hostfiles for 1 hosts:
nccl-gib-test-0
# nThread 1 nGpus 1 minBytes 1024 maxBytes 8589934592 step: 2(factor) warmup iters: 50 iters: 100 agg iters: 1 validation: 1 graph: 0
#
# Using devices
#  Rank  0 Group  0 Pid   2114 on nccl-gib-test-0 device  0 [0000:8f:00] NVIDIA B200
#  Rank  1 Group  0 Pid   2113 on nccl-gib-test-0 device  1 [0000:90:00] NVIDIA B200
NCCL version 2.26.6+cuda12.8
#
#                                                              out-of-place                       in-place
#       size         count      type   redop    root     time   algbw   busbw #wrong     time   algbw   busbw #wrong
#        (B)    (elements)                               (us)  (GB/s)  (GB/s)            (us)  (GB/s)  (GB/s)
...
    67108864       8388608     float    none      -1    100.1  670.29  335.14      0    92.39  726.37  363.19      0
   134217728      16777216     float    none      -1    179.7  746.86  373.43      0    168.0  798.94  399.47      0
   268435456      33554432     float    none      -1    334.1  803.41  401.71      0    300.3  893.87  446.94      0
   536870912      67108864     float    none      -1    626.8  856.57  428.29      0    568.4  944.47  472.23      0
  1073741824     134217728     float    none      -1   1186.1  905.28  452.64      0   1079.9  994.30  497.15      0
  2147483648     268435456     float    none      -1   2287.5  938.78  469.39      0   2045.4  1049.90  524.95      0
  4294967296     536870912     float    none      -1   4490.1  956.53  478.27      0   3920.0  1095.65  547.83      0
  8589934592    1073741824     float    none      -1   8897.6  965.42  482.71      0   7613.3  1128.28  564.14      0
```

It works on the single pod. Now let's try between the two pods.

```sh
root@nccl-gib-test-0:/usr/bin# /usr/local/gib/scripts/run_nccl_tests.sh   -t all_gather -b 1K -e 8G   nccl-gib-test-0 10.68.5.39 -g 2
Initializing SSH...
Hello from nccl-gib-test-0
Warning: Permanently added '[10.68.5.39]:222' (ECDSA) to the list of known hosts.
Hello from 10.68.5.39
Generating hostfiles for 2 hosts:
nccl-gib-test-0
10.68.5.39
# nThread 1 nGpus 1 minBytes 1024 maxBytes 8589934592 step: 2(factor) warmup iters: 50 iters: 100 agg iters: 1 validation: 1 graph: 0
#
# Using devices
#  Rank  0 Group  0 Pid  25060 on nccl-gib-test-0 device  0 [0000:cb:00] NVIDIA B200
#  Rank  1 Group  0 Pid  25055 on nccl-gib-test-0 device  1 [0000:cc:00] NVIDIA B200
#  Rank  2 Group  0 Pid   2078 on nccl-gib-test-1 device  0 [0000:97:00] NVIDIA B200
#  Rank  3 Group  0 Pid   2055 on nccl-gib-test-1 device  1 [0000:c4:00] NVIDIA B200
NCCL version 2.26.6+cuda12.8
#
#                                                              out-of-place                       in-place
#       size         count      type   redop    root     time   algbw   busbw #wrong     time   algbw   busbw #wrong
#        (B)    (elements)                               (us)  (GB/s)  (GB/s)            (us)  (GB/s)  (GB/s)
```

Uh oh! We can gather the info between the pods but we can't run data? Running
`ip a` shows us the issue.

```sh
root@nccl-gib-test-0:/diagnostic# ip a
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
2: eth0@if44: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1460 qdisc noqueue state UP group default qlen 1000
    link/ether b6:76:b9:08:9c:e5 brd ff:ff:ff:ff:ff:ff link-netnsid 0
    inet 10.68.3.40/24 brd 10.68.3.255 scope global eth0
       valid_lft forever preferred_lft forever
```

There are no NICs to transmit the data. This is where DRANET can help!

#### Nvidia DRA + DRANET

We create one more `ResourceClaimTemplate`, for the RDMA devices on the node,
along with a `DeviceClass` for the RDMA device.

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

The `ResourceClaimTemplate` allows to specify multiple devices, in this case 2
GPUs and 2 NICs and also apply a constraint so the NICs and the GPUs share the
same pcie root, avoiding the penalty of suboptimal topologies.

It is important to indicate that each Pod will obtain a `ResourceClaim` from the
`ResourceClaimTemplate`, and since your servers may be connected in a [rail
optimized
architecture](https://docs.nvidia.com/networking/display/infinibandclusterbringupprocedure/setting+the+infiniband+cluster+topology),
the GPUs requested need to be also aligned across the different servers. In this
example, we will request GPU0 and GPU1 of each node.

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: 2-gpu-nic-aligned
spec:
  spec:
    devices:
      requests:
      - name: gpu
        exactly:
          deviceClassName: gpu.nvidia.com
          count: 2
          selectors:
          - cel:
              expression: device.attributes["gpu.nvidia.com"].index <= 2
      - name: nic
        exactly:
          deviceClassName: dranet
          count: 2
          selectors:
          - cel:
              expression: device.attributes["dra.net"].rdma == true
      constraints:
      - matchAttribute: "resource.kubernetes.io/pcieRoot"
```

Add this resourceclaim to the statefulset

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
            nvidia-smi -L
            echo -e "\norte_keep_fqdn_hostnames=t" >> /etc/openmpi/openmpi-mca-params.conf
            /scripts/container_entry.sh shell
            source /usr/local/gib/scripts/set_nccl_env.sh
            sleep infinity
        resources:
          claims:
          - name: gpu
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
        - name: gpu
          resourceClaimTemplateName: 2-gpu-nic-aligned
      tolerations:
      - key: "nvidia.com/gpu"
        operator: "Equal"
        value: "present"
        effect: "NoSchedule"
```

Now exec into the same pod. 

```sh
root@nccl-gib-test-0:/usr/bin# ip a
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
2: eth0@if45: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1460 qdisc noqueue state UP group default qlen 1000
    link/ether 26:20:2c:53:5e:20 brd ff:ff:ff:ff:ff:ff link-netnsid 0
    inet 10.68.3.41/24 brd 10.68.3.255 scope global eth0
       valid_lft forever preferred_lft forever
4: gpu0rdma0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8896 qdisc mq state UP group default qlen 1000
    link/ether 06:c4:a0:25:7e:01 brd ff:ff:ff:ff:ff:ff
    inet 192.168.1.5/32 scope global gpu0rdma0
       valid_lft forever preferred_lft forever
5: gpu1rdma0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8896 qdisc mq state UP group default qlen 1000
    link/ether e2:f1:94:78:7e:04 brd ff:ff:ff:ff:ff:ff
    inet 192.168.2.5/32 scope global gpu1rdma0
       valid_lft forever preferred_lft forever
```

And now we run NCCL again.

```sh
$ kubectl apply -f statefulset.yaml && kubectl rollout status --watch --timeout=600s statefulset/nccl-gib-test

statefulset.apps/nccl-gib-test created
Waiting for 2 pods to be ready...
Waiting for 2 pods to be ready...
Waiting for 1 pods to be ready...
Waiting for 1 pods to be ready...
partitioned roll out complete: 2 new pods have been updated...
```

```sh
$ kubectl exec nccl-gib-test-0 -it -- /usr/local/gib/scripts/run_nccl_tests.sh -t all_gather -b 8 -e 1G -f 2 -g 1 -n 100 -w 50 nccl-gib-test-0.nccl-gib-test nccl-gib-test-1.nccl-gib-test

Initializing SSH...
Warning: Permanently added '[nccl-gib-test-0.nccl-gib-test]:222,[10.44.3.37]:222' (ECDSA) to the list of known hosts.
Hello from nccl-gib-test-0.nccl-gib-test
Warning: Permanently added '[nccl-gib-test-1.nccl-gib-test]:222,[10.44.4.37]:222' (ECDSA) to the list of known hosts.
Hello from nccl-gib-test-1.nccl-gib-test
Generating hostfiles for 2 hosts:
nccl-gib-test-0.nccl-gib-test
nccl-gib-test-1.nccl-gib-test
# nThread 1 nGpus 1 minBytes 8 maxBytes 1073741824 step: 2(factor) warmup iters: 50 iters: 100 agg iters: 1 validation: 1 graph: 0
#
# Using devices
#  Rank  0 Group  0 Pid   1444 on nccl-gib-test-0 device  0 [0000:8f:00] NVIDIA B200
#  Rank  1 Group  0 Pid   1415 on nccl-gib-test-1 device  0 [0000:8f:00] NVIDIA B200
NCCL version 2.26.6+cuda12.8
#
#                                                              out-of-place                       in-place
#       size         count      type   redop    root     time   algbw   busbw #wrong     time   algbw   busbw #wrong
#        (B)    (elements)                               (us)  (GB/s)  (GB/s)            (us)  (GB/s)  (GB/s)
           0             0     float    none      -1     0.06    0.00    0.00      0     0.06    0.00    0.00      0
           0             0     float    none      -1     0.06    0.00    0.00      0     0.06    0.00    0.00      0
          32             4     float    none      -1    14.18    0.00    0.00      0    14.12    0.00    0.00      0
          64             8     float    none      -1    14.30    0.00    0.00      0    14.12    0.00    0.00      0
         128            16     float    none      -1    14.16    0.01    0.00      0    14.14    0.01    0.00      0
         256            32     float    none      -1    14.32    0.02    0.01      0    14.37    0.02    0.01      0
         512            64     float    none      -1    14.46    0.04    0.02      0    14.25    0.04    0.02      0
        1024           128     float    none      -1    14.44    0.07    0.04      0    14.49    0.07    0.04      0
        2048           256     float    none      -1    14.89    0.14    0.07      0    14.53    0.14    0.07      0
        4096           512     float    none      -1    15.35    0.27    0.13      0    15.15    0.27    0.14      0
        8192          1024     float    none      -1    17.06    0.48    0.24      0    16.80    0.49    0.24      0
       16384          2048     float    none      -1    18.65    0.88    0.44      0    18.15    0.90    0.45      0
       32768          4096     float    none      -1    19.29    1.70    0.85      0    19.22    1.70    0.85      0
       65536          8192     float    none      -1    22.30    2.94    1.47      0    22.05    2.97    1.49      0
      131072         16384     float    none      -1    28.69    4.57    2.28      0    28.35    4.62    2.31      0
      262144         32768     float    none      -1    30.96    8.47    4.23      0    30.25    8.67    4.33      0
      524288         65536     float    none      -1    37.04   14.16    7.08      0    34.90   15.02    7.51      0
     1048576        131072     float    none      -1    46.45   22.58   11.29      0    43.78   23.95   11.98      0
     2097152        262144     float    none      -1    63.16   33.21   16.60      0    59.59   35.19   17.60      0
     4194304        524288     float    none      -1    101.5   41.31   20.66      0    93.90   44.67   22.33      0
     8388608       1048576     float    none      -1    150.1   55.87   27.93      0    142.9   58.68   29.34      0
    16777216       2097152     float    none      -1    268.2   62.56   31.28      0    252.5   66.43   33.22      0
    33554432       4194304     float    none      -1    519.5   64.59   32.29      0    484.5   69.26   34.63      0
    67108864       8388608     float    none      -1   1019.6   65.82   32.91      0    931.9   72.02   36.01      0
   134217728      16777216     float    none      -1   1989.8   67.45   33.73      0   1746.0   76.87   38.44      0
   268435456      33554432     float    none      -1   3842.6   69.86   34.93      0   3208.5   83.66   41.83      0
   536870912      67108864     float    none      -1   7502.0   71.56   35.78      0   6146.5   87.35   43.67      0
  1073741824     134217728     float    none      -1    14640   73.35   36.67      0    11892   90.29   45.14      0
# Out of bounds values : 0 OK
# Avg bus bandwidth    : 12.5463
#
```

They now connect!

#### Conclusion

Using both DRANET and the Nvidia DRA libraries in combination is a way to
quickly allocate both GPUs and RDMA devices in order to create interconnected
workloads that can span multiple nodes. This can be used to create workloads
that span multiple nodes and take advantage of spare resources on nodes.

For instance, consider that you have 2 nodes with 8 GPUs apiece. If you ran 2
training jobs that took 6 GPUs each then you would have 4 GPUs idle. By enabling
DRANET you could take advantage of those remaining 4 for another training job.
Without providing the RDMA devices, these GPUs would only be able to communicate
within the same node.
