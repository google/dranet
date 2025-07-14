---
title: "GKE with NVIDIA DRA and DraNet"
date: 2025-06-20T10:10:40Z
---


To get started, create a [GKE cluster with DRA support](https://cloud.google.com/kubernetes-engine/docs/how-to/set-up-dra) and the corresponding [VPC and subnets](https://cloud.google.com/ai-hypercomputer/docs/create/gke-ai-hypercompute-custom#create-vpcs-and-subnets)

It should look like 

```sh
PROJECT="gke-dranet"
CLUSTER="dranet-dranet"
REGION="us-west8"
ZONE="us-west8-c"
GVNIC_NETWORK_PREFIX="dranet-gvnic"
RDMA_NETWORK_PREFIX="dranet-rdma"
VERSION="1.33"

gcloud container clusters create "${CLUSTER}" \
    --cluster-version="${VERSION}" \
    --enable-multi-networking \
    --enable-dataplane-v2 \
    --enable-kubernetes-unstable-apis=resource.k8s.io/v1beta1/deviceclasses,resource.k8s.io/v1beta1/resourceclaims,resource.k8s.io/v1beta1/resourceclaimtemplates,resource.k8s.io/v1beta1/resourceslices \
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

Install DraNet
```sh
kubectl apply -f https://raw.githubusercontent.com/google/dranet/refs/heads/main/install.yaml
```

#### Installing Nvidia DRA Drivers

In order to install the NVIDIA DRA Drivers you will need to clone the [NVIDIA DRA](https://github.com/NVIDIA/k8s-dra-driver-gpu) repo. Ensure you have [helm](https://helm.sh/docs/intro/install/) installed.

```
helm upgrade -i --create-namespace --namespace nvidia-dra-driver-gpu nvidia-dra-driver-gpu ./k8s-dra-driver-gpu/deployments/helm/nvidia-dra-driver-gpu --set gpuResourcesEnabledOverride=true --values https://raw.githubusercontent.com/google/dranet/refs/heads/main/examples/demo_nvidia_dranet/values.yaml --wait 
```

The values.yaml adds some additional tolerations and removes some priorities that need to be done in order to work nicely with GKE.

Once this is done, you can run 

```sh
kubectl get pods -n nvidia-dra-driver-gpu
NAME                                                READY   STATUS     RESTARTS   AGE
nvidia-dra-driver-gpu-controller-66696889cd-86m8f   1/1     Running    0          13m
```
If you only see the controller like above, you will need to label the nodes with GPUs on them in order to get the kubelet plugin running.

```sh
kubectl label node -l cloud.google.com/gke-gpu=true --overwrite nvidia.com/gpu.present=true

kubectl get pods -n nvidia-dra-driver-gpu
NAME                                                READY   STATUS     RESTARTS   AGE
nvidia-dra-driver-gpu-controller-66696889cd-86m8f   1/1     Running    0          12m
nvidia-dra-driver-gpu-kubelet-plugin-ffzgx          2/2     Running    0          34s
nvidia-dra-driver-gpu-kubelet-plugin-qsp2d          2/2     Running    0          33s
```

Once you see all these pods, the NVIDIA DRA plugin is working as expected

#### PCI Attributes and GPU Indices

[KEP #4381](https://github.com/kubernetes/enhancements/pull/5316) proposes the standard PCI Root attribute. This is an important field to have for devices since  the alignment of multiple devices on the PCI bus can have major implications of how fast the devices can communicate with each other.

At the moment since the KEP just got merged, many drivers do not implement it. In the meantime, the GPU Index can be used for NVIDIA and GKE also provides a numerical index on the NIC itself to show whether it is aligned with the GPU or not. 

#### Creating a GPU workload

We can create a `ResourceClaimTemplate` to specify what GPUs we want. We currently don't have PCI attributes yet in the NVIDIA driver library so we will want to specify the index for the time being. This isn't too important for this section but will come into relevance once we start pairing NICs to the nodes.

```yaml
apiVersion: resource.k8s.io/v1beta1
kind: ResourceClaimTemplate
metadata:
  name: 2-gpu
spec:
  spec:
    devices:
      requests:
      - name: gpu
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

Note how unlike the other examples, we don't use the resources field in the spec to allocate GPUs, nor do we manually mount the Nvidia libraries. This is all handled by the DRA driver that Nvidia provides. Execing into one of these nodes and listing the gpus shows that two B200 GPUs were allocated.

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

Uh oh! We can gather the info between the pods but we can't run data? Running `ip a` shows us the issue.

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

There are no NICs to transmit the data. This is where DraNet can help!

#### Nvidia DRA + DraNet

We create one more `ResourceClaimTemplate`, for the RDMA devices on the node, along with a `DeviceClass` for the RDMA device.

```yaml
apiVersion: resource.k8s.io/v1beta1
kind: DeviceClass
metadata:
  name: dranet
spec:
  selectors:
    - cel:
        expression: device.driver == "dra.net"
```

```yaml
apiVersion: resource.k8s.io/v1beta1
kind: ResourceClaimTemplate
metadata:
  name: 2-nic
spec:
  spec:
    devices:
      requests:
      - name: nic
        deviceClassName: dranet
        count: 2
        selectors:
        - cel:
            expression: device.attributes["dra.net"].rdma == true &&
              (
                (device.attributes["dra.net"].ifName.startsWith("gpu") &&
                 device.attributes["dra.net"].ifName.endsWith("rdma0") &&
                 int(device.attributes["dra.net"].ifName.substring(3, 4)) < 2)
```

Add this resourceclaim to the statefulset

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
          # - name: library-dir-host
          #   mountPath: /usr/local/nvidia
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
        - name: nic
          resourceClaimTemplateName: 2-nic
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
root@nccl-gib-test-0:/diagnostic# /usr/local/gib/scripts/run_nccl_tests.sh   -t all_gather -b 1K -e 8G   nccl-gib-test-0 10.68.5.40 -g 2
Initializing SSH...
Hello from nccl-gib-test-0
Warning: Permanently added '[10.68.5.40]:222' (ECDSA) to the list of known hosts.
Hello from 10.68.5.40
Generating hostfiles for 2 hosts:
nccl-gib-test-0
10.68.5.40
# nThread 1 nGpus 1 minBytes 1024 maxBytes 8589934592 step: 2(factor) warmup iters: 50 iters: 100 agg iters: 1 validation: 1 graph: 0
#
# Using devices
#  Rank  0 Group  0 Pid   3521 on nccl-gib-test-0 device  0 [0000:8f:00] NVIDIA B200
#  Rank  1 Group  0 Pid   3514 on nccl-gib-test-0 device  1 [0000:90:00] NVIDIA B200
#  Rank  2 Group  0 Pid   2071 on nccl-gib-test-1 device  0 [0000:8f:00] NVIDIA B200
#  Rank  3 Group  0 Pid   2076 on nccl-gib-test-1 device  1 [0000:90:00] NVIDIA B200
NCCL version 2.26.6+cuda12.8
#
#                                                              out-of-place                       in-place
#       size         count      type   redop    root     time   algbw   busbw #wrong     time   algbw   busbw #wrong
#        (B)    (elements)                               (us)  (GB/s)  (GB/s)            (us)  (GB/s)  (GB/s)
...
   268435456      16777216     float    none      -1   3059.9   87.73   65.80      0   3134.1   85.65   64.24      0
   536870912      33554432     float    none      -1   5886.9   91.20   68.40      0   6082.2   88.27   66.20      0
  1073741824      67108864     float    none      -1    12452   86.23   64.67      0    11550   92.97   69.73      0
  2147483648     134217728     float    none      -1    24426   87.92   65.94      0    23694   90.63   67.97      0
  4294967296     268435456     float    none      -1    47420   90.57   67.93      0    47000   91.38   68.54      0

  8589934592     536870912     float    none      -1    96100   89.39   67.04      0    94047   91.34   68.50      0
```

They now connect!

#### Conclusion

Using both DraNet and the Nvidia DRA libraries in combination is a way to quickly allocate both GPUs and RDMA devices in order to create interconnected workloads that can span multiple nodes. This can be used the create workloads that span multiple nodes and take advantage of spare resources on nodes. 

For instance, consider that you have 2 nodes with 8 GPUs apiece. If you ran 2 training jobs that took 6 GPUs each then you would have 4 GPUs idle. By enabling DraNet you could take advantage of those remaining 4 for another training job. Without providing the RDMA devics, these GPUs would only be able to communicate within the same node.
