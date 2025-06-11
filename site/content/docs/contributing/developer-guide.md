---
title: "Developer Guide"
date: 2025-05-25T11:30:40Z
---


## Develop locally

Use [KIND](https://kind.sigs.k8s.io/)

1. Create kind cluster with the recommended config

```
make kind-cluster
```

2. Do your changess to the codebase and rollout the custom version to the kind
   cluster

```
make kind-image
```

3. Test your changes locally, use the [examples folder](./examples) for dropping manifests
and README of the scenarios you are testing.

4. Once finish the development add an e2e test in bash using the `bats`
   framework in [the tests folder](./tests)

You can run your tests locally using `bats tests/`


## Develop in a cluster


1. Build and push the image to a registry

```
docker build . --tag aojea/dranet:test --push
```

2. Install dranet

```
kubect apply -f ./install.yaml
```

3. When developing new features update the image of the `dranet` daemonset and set the Daemonset image with the current hash

```sh
$ kubectl set image ds/dranet -n kube-system dranet=docker.io/aojea/dranet:test@sha256:0e3ded3f62041a71e5589ffce92ff110be95ed772b2eb23ad5f285bb0147a425
daemonset.apps/dranet image updated
$ kubectl rollout status ds/dranet -n kube-system
Waiting for daemon set "dranet" rollout to finish: 1 out of 5 new pods have been
updated...
```

## Accesing nodes

When developing it may be also useful to log into the nodes to be able to debug problems. Just obtain the list of nodes with `kubectl get nodes -o wide`

For Kind clusters use `docker exec -it <name of the node> bash`

```sh
kubectl get nodes -o wide
NAME                                            STATUS   ROLES    AGE   VERSION               INTERNAL-IP   EXTERNAL-IP      OS-IMAGE                             KERNEL-VERSION   CONTAINER-RUNTIME
gke-cluster-tpu-v6-default-pool-3f96de9d-hr87   Ready    <none>   8d    v1.33.1-gke.1107000   10.202.0.21   34.162.194.173   Container-Optimized OS from Google   6.6.87+          containerd://2.0.4
gke-cluster-tpu-v6-default-pool-955df71d-zd7b   Ready    <none>   8d    v1.33.1-gke.1107000   10.202.0.15   34.162.239.241   Container-Optimized OS from Google   6.6.87+          containerd://2.0.4
gke-cluster-tpu-v6-default-pool-e1c69e67-k0jw   Ready    <none>   8d    v1.33.1-gke.1107000   10.202.0.20   34.162.209.25    Container-Optimized OS from Google   6.6.87+          containerd://2.0.4
gke-tpu-de8b9feb-kgdj                           Ready    <none>   8d    v1.33.1-gke.1107000   10.202.0.26   34.162.163.69    Container-Optimized OS from Google   6.6.87+          containerd://2.0.4
gke-tpu-de8b9feb-prgf                           Ready    <none>   8d    v1.33.1-gke.1107000   10.202.0.24   34.162.144.5     Container-Optimized OS from Google   6.6.87+          containerd://2.0.4
gke-tpu-de8b9feb-sjcp                           Ready    <none>   8d    v1.33.1-gke.1107000   10.202.0.27   34.162.117.62    Container-Optimized OS from Google   6.6.87+          containerd://2.0.4
gke-tpu-de8b9feb-z8g1                           Ready    <none>   8d    v1.33.1-gke.1107000   10.202.0.25   34.162.239.83    Container-Optimized OS from Google   6.6.87+          containerd://2.0.4
```

For GKE or other clusters, you may have restrictions on ssh, so you can use

```sh
 kubectl debug -it node/gke-tpu-de8b9feb-kgdj --image busybox -- chroot /host
--profile=legacy is deprecated and will be removed in the future. It is recommended to explicitly specify a profile, for example "--profile=general".
Creating debugging pod node-debugger-gke-tpu-de8b9feb-kgdj-94xdx with container debugger on node gke-tpu-de8b9feb-kgdj.
If you don't see a command prompt, try pressing enter.
gke-tpu-de8b9feb-kgdj / #
```

If you want to upload some binary, per example `bpftrace` or `pwru` , you can use the streaming capabilities for that:

```


## Troubleshooting

To get the list of `dranet` Pods use the label:

```
kubectl -n kube-system get pods -l app=dranet -o wide
NAME           READY   STATUS             RESTARTS         AGE   IP              NODE                                            NOMINATED NODE   READINESS GATES
dranet-9z66b   0/1     CrashLoopBackOff   12 (4m54s ago)   42m   10.146.104.1
```

To identify the version running you can find the git commit is in the first line of logging

```
kubectl -n kube-system logs dranet-9z66b
Defaulted container "dranet" out of: dranet, enable-nri (init)
I0520 09:21:02.486329 1027992 app.go:181] dranet go go1.24.3 build: 3058756228b78265819e96963afae4dfd9497849 time: 2025-05-19T22:57:49Z
I0520 09:21:02.486404 1027992 app.go:75] FLAG: --add_dir_header="false"
I0520 09:21:02.486409 1027992 app.go:75] FLAG: --alsologtostderr="false"
I0520 09:21:02.486411 1027992 app.go:75] FLAG: --bind-address=":9177"
I0520 09:21:02.486413 1027992 app.go:75] FLAG: --filter="attributes[\"dra.net/type\"].StringValue  != \"veth\""
I0520 09:21:02.486415 1027992 app.go:75] FLAG: --hostname-override=""
I0520 09:21:02.486417 1027992 app.go:75] FLAG: --kubeconfig=""
I0520 09:21:02.486418 1027992 app.go:75] FLAG: --log_backtrace_at=":0"
I0520 09:21:02.486423 1027992 app.go:75] FLAG: --log_dir=""
I0520 09:21:02.486424 1027992 app.go:75] FLAG: --log_file=""
I0520 09:21:02.486425 1027992 app.go:75] FLAG: --log_file_max_size="1800"
I0520 09:21:02.486427 1027992 app.go:75] FLAG: --logtostderr="true"
I0520 09:21:02.486429 1027992 app.go:75] FLAG: --one_output="false"
I0520 09:21:02.486430 1027992 app.go:75] FLAG: --skip_headers="false"
I0520 09:21:02.486435 1027992 app.go:75] FLAG: --skip_log_headers="false"
I0520 09:21:02.486436 1027992 app.go:75] FLAG: --stderrthreshold="2"
I0520 09:21:02.486440 1027992 app.go:75] FLAG: --v="4"
I0520 09:21:02.486442 1027992 app.go:75] FLAG: --vmodule=""
I0520 09:21:02.486599 1027992 envvar.go:172] "Feature gate default state" feature="ClientsAllowCBOR" enabled=false
I0520 09:21:02.486609 1027992 envvar.go:172] "Feature gate default state" feature="ClientsPreferCBOR" enabled=false
I0520 09:21:02.486611 1027992 envvar.go:172] "Feature gate default state" feature="InformerResourceVersion" enabled=false
I0520 09:21:02.486614 1027992 envvar.go:172] "Feature gate default state" feature="InOrderInformers" enabled=true
I0520 09:21:02.486616 1027992 envvar.go:172] "Feature gate default state" feature="WatchListClient" enabled=false
I0520 09:21:02.491702 1027992 draplugin.go:486] "Starting"
I0520 09:21:02.491855 1027992 nonblockinggrpcserver.go:88] "GRPC server started" logger="dra"
I0520 09:21:02.491919 1027992 nonblockinggrpcserver.go:88] "GRPC server started" logger="registrar"
time="2025-05-20T09:21:04Z" level=info msg="Created plugin 00-dra.net (dranet, handles RunPodSandbox,StopPodSandbox,RemovePodSandbox)"
I0520 09:21:04.492764 1027992 app.go:157] driver started
I0520 09:21:04.492786 1027992 driver.go:430] Publishing resources
time="2025-05-20T09:21:04Z" level=info msg="Registering plugin 00-dra.net..."
I0520 09:21:04.493135 1027992 cloud.go:38] running on GCE
time="2025-05-20T09:21:04Z" level=info msg="Configuring plugin 00-dra.net for runtime containerd/1.7.24..."
```
