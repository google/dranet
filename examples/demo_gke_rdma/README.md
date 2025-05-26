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

```sh
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

**NOTE:** unset the library path so rping can find the actual device.

```
 kubectl exec -it rdma-perftest-1 -- bash
root@rdma-perftest-1:/# LD_LIBRARY_PATH="" rping -c -a 10.0.4.7 -C 3 -v -V
ping data: rdma-ping-0: ABCDEFGHIJKLMNOPQRSTUVWXYZ[\]^_`abcdefghijklmnopqr
ping data: rdma-ping-1: BCDEFGHIJKLMNOPQRSTUVWXYZ[\]^_`abcdefghijklmnopqrs
ping data: rdma-ping-2: CDEFGHIJKLMNOPQRSTUVWXYZ[\]^_`abcdefghijklmnopqrst
client DISCONNECT EVENT...
root@rdma-perftest-1:/#
```

We can also use the perftests, run in one pod the 

```sh
root@rdma-perftest-0:/# ib_write_bw
```

```sh
root@rdma-perftest-1:/# ib_write_bw 10.0.4.7 -a --report_gbits
 ib_write_bw 10.0.4.8 -a --report_gbits
---------------------------------------------------------------------------------------
                    RDMA_Write BW Test
 Dual-port       : OFF          Device         : mlx5_3
 Number of qps   : 1            Transport type : IB
 Connection type : RC           Using SRQ      : OFF
 PCIe relax order: ON           Lock-free      : OFF
 ibv_wr* API     : ON           Using DDP      : OFF
 TX depth        : 128
 CQ Moderation   : 100
 CQE Poll Batch  : 16
 Mtu             : 4096[B]
 Link type       : Ethernet
 GID index       : 3
 Max inline data : 0[B]
 rdma_cm QPs     : OFF
 Data ex. method : Ethernet
---------------------------------------------------------------------------------------
 local address: LID 0000 QPN 0x029d PSN 0x85dc13 RKey 0x020f00 VAddr 0x0078cecebed000
 GID: 00:00:00:00:00:00:00:00:00:00:255:255:10:00:04:07
 remote address: LID 0000 QPN 0x02a3 PSN 0xa645ce RKey 0x021400 VAddr 0x007a2e150d7000
 GID: 00:00:00:00:00:00:00:00:00:00:255:255:10:00:04:08
---------------------------------------------------------------------------------------
 #bytes     #iterations    BW peak[Gb/sec]    BW average[Gb/sec]   MsgRate[Mpps]
 2          5000           0.050986            0.048851            3.053158
 4          5000             0.10               0.10                 3.274080
 8          5000             0.21               0.21                 3.203698
 16         5000             0.40               0.40                 3.119732
 32         5000             0.83               0.83                 3.250371
 64         5000             1.64               1.63                 3.188603
 128        5000             3.20               3.18                 3.105230
 256        5000             6.33               6.29                 3.070249
 512        5000             13.01              13.00                3.174636
 1024       5000             25.75              25.65                3.130913
 2048       5000             49.51              49.38                3.013817
 4096       5000             81.05              80.69                2.462356
 8192       5000             163.65             163.47               2.494365
 16384      5000             282.31             280.96               2.143528
 32768      5000             376.54             376.29               1.435442
 65536      5000             381.50             381.41               0.727473
 Completion with error at client
 Failed status 10: wr_id 0 syndrom 0x88
scnt=128, ccnt=0
 Failed to complete run_iter_bw function successfully

```

For testing with GPUDirect we need the `nvidia-peermem` kernel module that is still not available in GKE, but you can use ubuntu images with that module or use DMABUF.

Any wrong config on these things will probabaly raise errors with the message
```
Couldn't allocate MR
```

To find the RDMA device associated to the network device we can use `rdma link`

```sh
/# rdma link
link mlx5_0/1 state ACTIVE physical_state LINK_UP
link mlx5_1/1 state ACTIVE physical_state LINK_UP
link mlx5_2/1 state ACTIVE physical_state LINK_UP
link mlx5_3/1 state ACTIVE physical_state LINK_UP netdev gpu3rdma0
link mlx5_4/1 state ACTIVE physical_state LINK_UP
link mlx5_5/1 state ACTIVE physical_state LINK_UP
link mlx5_6/1 state ACTIVE physical_state LINK_UP
link mlx5_7/1 state ACTIVE physical_state LINK_UP
```

For finding the existing GPUs in the container and its index:

```sh
/# /usr/local/nvidia/bin/nvidia-smi -L
GPU 0: NVIDIA H200 (UUID: GPU-0c37e717-0699-77ee-9b44-bd80680d3cf2)
```

And now the commands will look like (use `--use_cuda_dmabuf` if your host does not have support for the nvidia-peermem kernel module )

```sh
root@rdma-perftest-1:/# ib_write_bw -d mlx5_3 --use_cuda=0 -a --use_cuda_dmabuf
```

and in the other side

```sh
root@rdma-perftest-0:/# ib_write_bw -d mlx5_3 --use_cuda=0 -a 10.0.4.8 --report_gbits --use_cuda_dmabuf
Perftest doesn't supports CUDA tests with inline messages: inline size set to 0
initializing CUDA
Listing all CUDA devices in system:
CUDA device 0: PCIe address is 8F:00

Picking device No. 0
[pid = 29, dev = 0] device name = [NVIDIA H200]
creating CUDA Ctx
making it the current CUDA Ctx
CUDA device integrated: 0
using DMA-BUF for GPU buffer address at 0x78e9f8800000 aligned at 0x78e9f8800000 with aligned size 16777216
allocated GPU buffer of a 16777216 address at 0x5d07a20e9300 for type CUDA_MEM_DEVICE
Calling ibv_reg_dmabuf_mr(offset=0, size=16777216, addr=0x78e9f8800000, fd=39) for QP #0
---------------------------------------------------------------------------------------
                    RDMA_Write BW Test
 Dual-port       : OFF          Device         : mlx5_3
 Number of qps   : 1            Transport type : IB
 Connection type : RC           Using SRQ      : OFF
 PCIe relax order: ON           Lock-free      : OFF
 ibv_wr* API     : ON           Using DDP      : OFF
 TX depth        : 128
 CQ Moderation   : 100
 CQE Poll Batch  : 16
 Mtu             : 4096[B]
 Link type       : Ethernet
 GID index       : 3
 Max inline data : 0[B]
 rdma_cm QPs     : OFF
 Data ex. method : Ethernet
---------------------------------------------------------------------------------------
 local address: LID 0000 QPN 0x029e PSN 0x73226b RKey 0x021400 VAddr 0x0078e9f9000000
 GID: 00:00:00:00:00:00:00:00:00:00:255:255:10:00:04:07
 remote address: LID 0000 QPN 0x02a4 PSN 0xf27096 RKey 0x020f00 VAddr 0x007acbe5000000
 GID: 00:00:00:00:00:00:00:00:00:00:255:255:10:00:04:08
---------------------------------------------------------------------------------------
 #bytes     #iterations    BW peak[Gb/sec]    BW average[Gb/sec]   MsgRate[Mpps]
 2          5000           0.057240            0.056633            3.539560
 4          5000             0.12               0.11                 3.582327
 8          5000             0.22               0.22                 3.388360
 16         5000             0.46               0.46                 3.563361
 32         5000             0.93               0.93                 3.626692
 64         5000             1.82               1.80                 3.521377
 128        5000             3.66               3.65                 3.564271
 256        5000             7.33               7.29                 3.561101
 512        5000             14.34              14.24                3.477770
 1024       5000             28.62              28.56                3.486621
 2048       5000             52.45              52.07                3.177910
 4096       5000             86.67              86.29                2.633375
 8192       5000             119.26             119.02               1.816064
 16384      5000             135.46             135.43               1.033275
 32768      5000             138.18             138.14               0.526974
 65536      5000             140.90             140.88               0.268711
 131072     5000             140.96             140.95               0.134422
 262144     5000             140.81             140.81               0.067141
 524288     5000             140.63             140.63               0.033528
 1048576    5000             140.49             140.29               0.016723
 2097152    5000             140.40             140.27               0.008361
 4194304    5000             140.57             140.34               0.004182
 8388608    5000             140.55             140.40               0.002092
---------------------------------------------------------------------------------------
deallocating GPU buffer 000078e9f8800000
destroying current CUDA Ctx
```

## GKE NCCL

Based on https://cloud.google.com/ai-hypercomputer/docs/create/gke-ai-hypercompute-custom but using only 1 NIC and 1 GPU per Pod to demonstrate how to split workloads to allocate individual resources.


### Install the RDMA binary and configure NCCL

This Daemonset does the following:

* Installs RDMA binaries and the NCCL library on the node.
* Stores the library and the binary in the /home/kubernetes/bin/nvidia/lib64 and the  /home/kubernetes/bin/gib directory on the VM.

```sh
kubectl apply -f https://raw.githubusercontent.com/GoogleCloudPlatform/container-engine-accelerators/refs/heads/master/gpudirect-rdma/nccl-rdma-installer.yaml
```

### Deploy the test workload

The manifest deploys two test pods, each of which runs in a A3 Ultra node.

```sh
kubectl apply -f examples/demo_gke_rdma/nccl-gib-test.yaml
```

It should run two pods in a statefulset, modify the number of replicas to suit your environment.

```sh
deviceclass.resource.k8s.io/rdma created
resourceclaimtemplate.resource.k8s.io/rdma-net-template-gib created
service/nccl-gib-test created
statefulset.apps/nccl-gib-test created
$ kubectl get pods
NAME                   READY   STATUS    RESTARTS   AGE
nccl-gib-test-0        1/1     Running   0          3s
nccl-gib-test-1        1/1     Running   0          1s
```



### Run the tests

It is important to pass the right parameters, in this specific example we need to indicate to only use one GPU per node `[-g <gpus_per_node>]`.

```sh
 kubectl exec nccl-gib-test-0 -it -- /usr/local/gib/scripts/run_nccl_tests.sh -t all_gather -b 1K -g 1 -e 8G nccl-gib-test-0.nccl-gib-test nccl-gib-test-1.nccl-gib-test
```

It should return something like:

```sh

Initializing SSH...
Hello from nccl-gib-test-0.nccl-gib-test
Hello from nccl-gib-test-1.nccl-gib-test
+ /usr/local/gib/scripts/gen_hostfiles.sh -p 222 nccl-gib-test-0.nccl-gib-test nccl-gib-test-1.nccl-gib-test
Generating hostfiles for 2 hosts:
nccl-gib-test-0.nccl-gib-test
nccl-gib-test-1.nccl-gib-test
+ mpirun --allow-run-as-root --mca btl tcp,self --mca btl_tcp_if_include eth0 --bind-to none -np 2 --hostfile /tmp/hostfiles/hostfile1 -x PATH -x LD_LIBRARY_PATH=/usr/local/gib/lib64:/usr/local/nvidia/lib64 -x NCCL_DEBUG=WARN -x NCCL_DEBUG_SUBSYS=INIT,NET -x NCCL_TESTS_SPLIT_MASK=0x0 bash -c 'source /usr/local/gib/scripts/set_nccl_env.sh;      /third_party/nccl-tests/build/all_gather_perf        -b 1K -e 8G -f 2        -w 50 -n 100;'
# nThread 1 nGpus 1 minBytes 1024 maxBytes 8589934592 step: 2(factor) warmup iters: 50 iters: 100 agg iters: 1 validation: 1 graph: 0
#
# Using devices
#  Rank  0 Group  0 Pid    235 on nccl-gib-test-0 device  0 [0000:90:00] NVIDIA H200
#  Rank  1 Group  0 Pid    161 on nccl-gib-test-1 device  0 [0000:90:00] NVIDIA H200
NCCL version 2.25.1+cuda12.8
#
#                                                              out-of-place                       in-place
#       size         count      type   redop    root     time   algbw   busbw #wrong     time   algbw   busbw #wrong
#        (B)    (elements)                               (us)  (GB/s)  (GB/s)            (us)  (GB/s)  (GB/s)
        1024           128     float    none      -1    20.89    0.05    0.02      0    20.40    0.05    0.03      0
        2048           256     float    none      -1    20.55    0.10    0.05      0    20.48    0.10    0.05      0
        4096           512     float    none      -1    20.71    0.20    0.10      0    20.75    0.20    0.10      0
        8192          1024     float    none      -1    21.49    0.38    0.19      0    21.62    0.38    0.19      0
       16384          2048     float    none      -1    24.56    0.67    0.33      0    24.55    0.67    0.33      0
       32768          4096     float    none      -1    25.04    1.31    0.65      0    24.59    1.33    0.67      0
       65536          8192     float    none      -1    28.59    2.29    1.15      0    28.04    2.34    1.17      0
      131072         16384     float    none      -1    33.46    3.92    1.96      0    36.83    3.56    1.78      0
      262144         32768     float    none      -1    47.72    5.49    2.75      0    45.11    5.81    2.91      0
      524288         65536     float    none      -1    79.13    6.63    3.31      0    76.17    6.88    3.44      0
     1048576        131072     float    none      -1    71.48   14.67    7.33      0    70.06   14.97    7.48      0
     2097152        262144     float    none      -1    76.40   27.45   13.72      0    76.41   27.44   13.72      0
     4194304        524288     float    none      -1    117.9   35.58   17.79      0    117.3   35.77   17.88      0
     8388608       1048576     float    none      -1    203.4   41.24   20.62      0    204.7   40.98   20.49      0
    16777216       2097152     float    none      -1    375.1   44.73   22.37      0    371.6   45.14   22.57      0
    33554432       4194304     float    none      -1    729.7   45.98   22.99      0    728.9   46.04   23.02      0
    67108864       8388608     float    none      -1   1447.6   46.36   23.18      0   1443.5   46.49   23.25      0
   134217728      16777216     float    none      -1   2871.7   46.74   23.37      0   2854.1   47.03   23.51      0
   268435456      33554432     float    none      -1   5699.0   47.10   23.55      0   5666.1   47.38   23.69      0
   536870912      67108864     float    none      -1    11382   47.17   23.58      0    11026   48.69   24.35      0
  1073741824     134217728     float    none      -1    22474   47.78   23.89      0    21049   51.01   25.51      0
  2147483648     268435456     float    none      -1    44241   48.54   24.27      0    39256   54.70   27.35      0
  4294967296     536870912     float    none      -1    86470   49.67   24.83      0    75081   57.20   28.60      0
  8589934592    1073741824     float    none      -1   166030   51.74   25.87      0   141444   60.73   30.37      0
# Out of bounds values : 0 OK
# Avg bus bandwidth    : 13.132

```