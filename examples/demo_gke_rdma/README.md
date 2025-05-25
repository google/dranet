# GKE RDMA Demo

Run a NodePool with GKE VMs using RDMA (A4Mega ...)

```
kubectl get nodes -o wide
NAME                                            STATUS   ROLES    AGE     VERSION               INTERNAL-IP     EXTERNAL-IP     OS-IMAGE                             KERNEL-VERSION   CONTAINER-RUNTIME
gke-gauravkg-dra-1-default-pool-88088fb4-r05v   Ready    <none>   6d14h   v1.32.4-gke.1106000   10.146.104.15   104.199.28.19   Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
gke-gauravkg-dra-1-default-pool-9d7a355f-a888   Ready    <none>   6d14h   v1.32.4-gke.1106000   10.146.104.14   34.140.103.51   Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
gke-gauravkg-dra-1-default-pool-fd12aa9f-cwpt   Ready    <none>   6d15h   v1.32.4-gke.1106000   10.146.104.13   34.22.153.148   Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
gke-gauravkg-dra-1-gpu-nodes-2-e5f6f579-73tg    Ready    <none>   4d16h   v1.32.4-gke.1297000   10.146.104.17   34.76.64.49     Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
gke-gauravkg-dra-1-gpu-nodes-2-e5f6f579-f5pj    Ready    <none>   4d16h   v1.32.4-gke.1297000   10.146.104.18   35.195.169.72   Container-Optimized OS from Google   6.6.72+          containerd://1.7.24
```

Install DRANET, once it starts running it will start to expose the RDMA NICs.
You can validate this by using `kubectl get resourceslices -o yaml` and checking the attribute `dra.net/rdma: true`.

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


## Deploy perf-tests RDMA Pods

Use the following manifest to install two Pods in the same RDMA network,
using just 1 GPU an 1 NIC.
```
kubectl apply -f ./examples/demo_gke_rdma/rdma-perftest.yaml
```


```
$ kubectl get pods -o wide
NAME                   READY   STATUS    RESTARTS   AGE     IP              NODE                                            NOMINATED NODE   READINESS GATES
rdma-perftest-0        1/1     Running   0          2m47s   10.180.0.155    gke-gauravkg-dra-1-gpu-nodes-2-e5f6f579-73tg    <none>           <none>
rdma-perftest-1        1/1     Running   0          3m25s   10.180.3.153    gke-gauravkg-dra-1-gpu-nodes-2-e5f6f579-f5pj    <none>           <none>
```

Once the Pod are running you can check that the ResourceClaim for the NICs are allocated.

```
kubectl get resourceclaims
NAME                                                     STATE                AGE
rdma-perftest-0-rdma-net-interface-jr7mf   allocated,reserved   3m54s
rdma-perftest-1-rdma-net-interface-hbpg5   allocated,reserved   4m32s
```

You can exec in each of the Pods to test the RDMA connection

```
kubectl exec -it rdma-perftest-0 -- bash
/# ip a
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
2: eth0@if163: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1460 qdisc noqueue state UP group default
    link/ether aa:ea:6c:d6:a6:7a brd ff:ff:ff:ff:ff:ff link-netnsid 0
    inet 10.180.0.155/24 brd 10.180.0.255 scope global eth0
       valid_lft forever preferred_lft forever
7: gpu3rdma0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8896 qdisc mq state UP group default qlen 1000
    link/ether 56:4d:70:aa:79:0a brd ff:ff:ff:ff:ff:ff
    inet 10.0.4.7/32 scope global gpu3rdma0
       valid_lft forever preferred_lft forever
root@rdma-perftest-0:/# ls /dev/infiniband/
rdma_cm  uverbs3
```

Only the NIC and the character devices are mounted in the Pod, however, if you use shared mode, all the RDMA devices are available in all namespaces

```
root@rdma-perftest-0:/# rdma system
netns shared copy-on-fork on

root@rdma-perftest-0:/# rdma link
link mlx5_0/1 state ACTIVE physical_state LINK_UP
link mlx5_1/1 state ACTIVE physical_state LINK_UP
link mlx5_2/1 state ACTIVE physical_state LINK_UP
link mlx5_3/1 state ACTIVE physical_state LINK_UP netdev gpu3rdma0
link mlx5_4/1 state ACTIVE physical_state LINK_UP
link mlx5_5/1 state ACTIVE physical_state LINK_UP
link mlx5_6/1 state ACTIVE physical_state LINK_UP
link mlx5_7/1 state ACTIVE physical_state LINK_UP
```

Run `rping -s` in one of the Pods and connect from the other to validate the connectivity:

```
 kubectl exec -it rdma-perftest-1 -- bash
root@rdma-perftest-1:/# rping -c -a 10.0.4.7 -C 3 -v -V
ping data: rdma-ping-0: ABCDEFGHIJKLMNOPQRSTUVWXYZ[\]^_`abcdefghijklmnopqr
ping data: rdma-ping-1: BCDEFGHIJKLMNOPQRSTUVWXYZ[\]^_`abcdefghijklmnopqrs
ping data: rdma-ping-2: CDEFGHIJKLMNOPQRSTUVWXYZ[\]^_`abcdefghijklmnopqrst
client DISCONNECT EVENT...
root@rdma-perftest-1:/#
```

We can also use the perftests

```
root@rdma-perftest-1:/# LD_LIBRARY_PATH=/usr/local/cuda/lib64:/usr/local/nvidia/lib64:/usr/local/cuda/lib64  ib_write_bw  10.0.4.7 --report-both
---------------------------------------------------------------------------------------
                    RDMA_Write BW Test
 Dual-port       : OFF          Device         : mlx5_3
 Number of qps   : 1            Transport type : IB
 Connection type : RC           Using SRQ      : OFF
 PCIe relax order: ON           Lock-free      : OFF
 ibv_wr* API     : ON           Using DDP      : OFF
 TX depth        : 128
 CQ Moderation   : 1
 CQE Poll Batch  : 16
 Mtu             : 4096[B]
 Link type       : Ethernet
 GID index       : 3
 Max inline data : 0[B]
 rdma_cm QPs     : OFF
 Data ex. method : Ethernet
---------------------------------------------------------------------------------------
 local address: LID 0000 QPN 0x0291 PSN 0x1386ce RKey 0x021300 VAddr 0x007e7956146000
 GID: 00:00:00:00:00:00:00:00:00:00:255:255:10:00:04:08
 remote address: LID 0000 QPN 0x028f PSN 0x85fed8 RKey 0x021200 VAddr 0x007d1feb0de000
 GID: 00:00:00:00:00:00:00:00:00:00:255:255:10:00:04:07
---------------------------------------------------------------------------------------
 #bytes     #iterations    BW peak[MiB/sec]    BW average[MiB/sec]   MsgRate[Mpps]
 65536      5000             45446.68            45428.46                    0.726855
---------------------------------------------------------------------------------------
```