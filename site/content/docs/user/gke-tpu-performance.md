---
title: "GKE and Cloud TPU v6e (Trillium)"
date: 2025-05-27T11:30:40Z
---

If you use TPU Trillium and you want to improve the network performance of your Pods you can balance your network traffic over the VM NICs.

The `ct6e-standard-4t` machine type is backed by two physical NICs, since the main interface of the VM is used for all the applications and Pods on the host, you can create two additional vNICs on the VM that will be attached to each of the physical NICs, and pass them to the Pod directly, so you can multiplex your traffic to consume the total capacity of the physical NICs.

```sh
# Create two additional VPC networks
gcloud compute --project=${PROJECT?} \
  networks create \
  tpu-net-1 \
  --mtu=8896 \
  --subnet-mode=custom

gcloud compute --project=${PROJECT?} \
  networks subnets create \
  tpu-net-1-sub \
  --network=tpu-net-1 \
  --region=${REGION?} \
  --range=192.168.0.0/24

gcloud compute --project=${PROJECT?} \
  networks create \
  tpu-net-2 \
  --mtu=8896 \
  --subnet-mode=custom

gcloud compute --project=${PROJECT?} \
  networks subnets create \
  tpu-net-2-sub \
  --network=tpu-net-2 \
  --region=${REGION?} \
  --range=192.168.1.0/24

gcloud container node-pools create POOL_NAME \
    --location=${LOCATION} \
    --cluster=${CLUSTER_NAME} \
    --node-locations=${NODE_ZONES} \
    --machine-type=${MACHINE_TYPE} \
    --tpu-topology=${TPU_TOPOLOGY} \
    --additional-node-network network=tpu-net-1,subnetwork=tpu-net-1-sub \
    --additional-node-network network=tpu-net-2,subnetwork=tpu-net-2-sub \
    --enable-gvnic
```

Apply the following manifest to install DRANET:

```sh
kubectl apply -f https://raw.githubusercontent.com/google/dranet/refs/heads/main/install.yaml
```

Once DRANET is running you'll be able to obtain the network resources exposed by the dranet Pods, in order to avoid noise, DRANET has a flag that allow to set client side filter to control the exposed resources, in this case, we can set the flag to ignore network devices that are `virtual`, the manifest will look like:

```yaml
      containers:
      - args:
        - /dranet
        - --v=4
        - --filter=attributes["dra.net/virtual"].BoolValue == false
       image: ghcr.io/google/dranet:stable
```

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

**ResourceClaimTemplate (worker-rdma-nic-template):** This will request the two additional NICs, since we created the additiona networks with the prefix `tpu-net` we can levarage the powerful CEL expressions to match on that prefix.

Another important factor is the capacity of DRANET to pass Interface configuration options that allow to tune the interfaces for maximum performance, per example, [Big TCP](https://lwn.net/Articles/884104/).

In addition, if you have GVNIC enabled you can use some private ethtool flags that improve the performance for TCP like [enable-max-rx-buffer-size](enable-max-rx-buffer-size).

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: tpu-net-interfaces
spec:
  spec:
    devices:
      requests:
      - name: tpu-net-interface
        exactly:
          deviceClassName: dranet
          count: 2
          selectors:
          - cel:
              expression: device.attributes["gce.dra.net"].networkName.startsWith("tpu-net")
      config:
      - opaque:
          driver: dra.net
          parameters:
            interface:
              mtu: 8896
              gsoMaxSize: 65536
              groMaxSize: 65536
              gsoIPv4MaxSize: 65536
              groIPv4MaxSize: 65536
              disableEbpfPrograms: true
            ethtool:
              privateFlags:
                enable-max-rx-buffer-size: true
```

To test the network performance we'll use [neper](https://github.com/google/neper), a tool created by the Google kernel teams to test network performance.

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: neper
spec:
  selector:
    matchLabels:
      app: neper
  serviceName: neper
  replicas: 2
  template:
    metadata:
      labels:
        app: neper
    spec:
      nodeSelector:
        cloud.google.com/gke-tpu-accelerator: tpu-v6e-slice
        cloud.google.com/gke-tpu-topology: 4x4
      initContainers:
      - name: "network-optimization-sysctls"
        image: "busybox"
        securityContext:
          privileged: true
        command:
        - sh
        - -c
        - |
          echo 5000 > /proc/sys/net/ipv4/tcp_rto_min_us
          echo 1 > /proc/sys/net/ipv4/tcp_no_metrics_save
          echo 0 > /proc/sys/net/ipv4/tcp_slow_start_after_idle
          echo 131072 > /proc/sys/net/core/optmem_max
          echo "4096 41943040 314572800" > /proc/sys/net/ipv4/tcp_rmem
      containers:
      - name: neper
        image: ghcr.io/google/neper:stable
        securityContext:
          privileged: true
        resources:
          requests:
            google.com/tpu: 4
          limits:
            google.com/tpu: 4
      resourceClaims:
      - name: tpu-net-interface
        resourceClaimTemplateName: tpu-net-interfaces
```

We'll get two pods running:

```sh
$ kubectl get pods
NAME      READY   STATUS    RESTARTS   AGE
neper-0   1/1     Running   0          10m
neper-1   1/1     Running   0          22s
```

Using neper-1 as a server `kubectl exec -it neper-1 -- sh`, checks first the additional IPs assigned, in this case these IPs are 10.9.9.11 and 10.10.0.11

```sh
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
2: eth0@if13: <BROADCAST,MULTICAST,UP,LOWER_UP,M-DOWN> mtu 1460 qdisc noqueue state UP qlen 1000
    link/ether 16:41:72:68:11:67 brd ff:ff:ff:ff:ff:ff
    inet 10.68.2.12/24 brd 10.68.2.255 scope global eth0
       valid_lft forever preferred_lft forever
3: eth1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8896 qdisc mq state UP qlen 1000
    link/ether 42:01:0a:09:09:0b brd ff:ff:ff:ff:ff:ff
    inet 10.9.9.11/32 scope global eth1
       valid_lft forever preferred_lft forever
4: eth2: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 8896 qdisc mq state UP qlen 1000
    link/ether 42:01:0a:0a:00:0b brd ff:ff:ff:ff:ff:ff
    inet 10.10.0.11/32 scope global eth2
       valid_lft forever preferred_lft forever
```

then run one TCP stream server per NIC:

```sh
for i in 0 1; do
  tcp_stream -C$((52279 + i)) --port=$((38339 + i)) --skip-rx-copy -rw -Z -B16384 --test-length=60 --suicide-length=120 -F100 --num-threads=16 --num-flows=32 -D0 --logtostderr &> test$i.log &
done
```

and neper-0 as a client `kubectl exec -it neper-0 -- sh` to connect to each TCP server:

```sh
tcp_stream -C52279 --port=38339 --skip-rx-copy -rw -Z -B16384 --test-length=60 --suicide-length=70 -F100 --num-threads=16 --num-flows=32 --client -H 10.9.9.11 -D0 --logtostderr &> test0.log &
tcp_stream -C52280 --port=38340 --skip-rx-copy -rw -Z -B16384 --test-length=60 --suicide-length=70 -F100 --num-threads=16 --num-flows=32 --client -H 10.10.0.11 -D0 --logtostderr &> test1.log &
```

The first test instance recorded a throughput of ~180.17 Gbps, and the second instance simultaneously achieved ~174.73 Gbps. 

```sh
grep throughput test*
test0.log:throughput_opt=Mb
test0.log:throughput=180165.51
test0.log:throughput_units=Mbit/s
test0.log:local_throughput=180165511242
test0.log:remote_throughput=177503231653
test1.log:throughput_opt=Mb
test1.log:throughput=174727.08
test1.log:throughput_units=Mbit/s
test1.log:local_throughput=174727081480
test1.log:remote_throughput=175469311719
```

The sum of these two independent tests gives the total aggregated throughput of 354.9 Gbps.
