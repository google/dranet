# GKE RDMA Demo

```
kubectl get nodes -o wide
NAME                                            STATUS   ROLES    AGE     VERSION               INTERNAL-IP     EXTERNAL-IP     OS-IMAGE                             KERNEL-VERSION   CONTAINER-RUNTIME
gke-gauravkg-dra-1-default-pool-88088fb4-r05v   Ready    <none>   6d14h   v1.32.4-gke.1106000   10.146.104.15   104.199.28.19   Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
gke-gauravkg-dra-1-default-pool-9d7a355f-a888   Ready    <none>   6d14h   v1.32.4-gke.1106000   10.146.104.14   34.140.103.51   Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
gke-gauravkg-dra-1-default-pool-fd12aa9f-cwpt   Ready    <none>   6d15h   v1.32.4-gke.1106000   10.146.104.13   34.22.153.148   Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
gke-gauravkg-dra-1-gpu-nodes-2-e5f6f579-73tg    Ready    <none>   4d16h   v1.32.4-gke.1297000   10.146.104.17   34.76.64.49     Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
gke-gauravkg-dra-1-gpu-nodes-2-e5f6f579-f5pj    Ready    <none>   4d16h   v1.32.4-gke.1297000   10.146.104.18   35.195.169.72   Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
```


```
 - basic:
        attributes:
          dra.net/alias:
            string: ""
          dra.net/cloudNetwork:
            string: gauravkg-dra-1-vpc-additional
          dra.net/encapsulation:
            string: ether
          dra.net/ifName:
            string: gpu6rdma0
          dra.net/ipv4:
            string: 10.0.7.8
          dra.net/kind:
            string: network
          dra.net/mac:
            string: 92:b7:77:2d:5b:13
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
      name: gpu6rdma0
```


## Install the RDMA binary and configure NCCL

This Daemonset does the following:
Installs RDMA binaries and the NCCL library on the node.
Stores the library and the binary in the /home/kubernetes/bin/nvidia/lib64 and the  /home/kubernetes/bin/gib directory on the VM.

```
kubectl apply -f https://raw.githubusercontent.com/GoogleCloudPlatform/container-engine-accelerators/refs/heads/master/gpudirect-rdma/nccl-rdma-installer.yaml
```


```
 kubectl get pods -o wide
NAME                            READY   STATUS    RESTARTS   AGE     IP            NODE                                            NOMINATED NODE   READINESS GATES
demo-c8577459c-g74tn            2/2     Running   0          6d16h   10.180.5.2    gke-gauravkg-dra-1-default-pool-9d7a355f-a888   <none>           <none>
nccl-workload-b585465b6-mm8nf   1/1     Running   0          20s     10.180.3.11   gke-gauravkg-dra-1-gpu-nodes-2-e5f6f579-f5pj    <none>           <none>
nccl-workload-b585465b6-xlw27   1/1     Running   0          20s     10.180.0.11   gke-gauravkg-dra-1-gpu-nodes-2-e5f6f579-73tg    <none>           <none>

```

```
kubectl get resourceclaims
NAME                                                     STATE                AGE
claim-any-rdma-nic                                       pending              18d
cpu-with-aligned-nic                                     pending              41h
nccl-workload-b585465b6-mm8nf-rdma-net-interface-kwh66   allocated,reserved   54s
nccl-workload-b585465b6-xlw27-rdma-net-interface-wgcbk   allocated,reserved   54s

```


