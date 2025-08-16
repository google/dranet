# GKE Multiple Networks

This demo shows how to create GCE VM with multiple NICs and spawn Pods that
connect to these GCE Networks.

1. Create a cluster

DRA is beta in 1.32, so it requires to explicitly enable the feature.

```sh
PROJECT="test-project"
CLUSTER="test-cluster"
ZONE="us-central1-c"
VERSION="1.34"

gcloud container clusters create "${CLUSTER}" \
    --cluster-version="${VERSION}" \
    --enable-multi-networking \
    --enable-dataplane-v2 \
    --no-enable-autorepair \
    --no-enable-autoupgrade \
    --zone="${ZONE}" \
    --project="${PROJECT}" # Explicitly set the project
```

2. Once the cluster has been created we need to create a Node Pool with multiple networks, `dranetctl` is an opinionanted tool that will help us
to set up the necessary network infrastructure.

```sh
dranetctl gke acceleratorpod create dranet1 \
    --additional-network-interfaces 2 \
    --machine-type e2-standard-16 \
    --node-count 2 \
    --cluster dranet-test \
    --location us-central1-c -v 2
```

3. We install dranet on the nodes created by `dranetctl`, that are labeled with `dra.net/acceleratorpod: "true"`.

```sh
kubectl apply -f ./examples/dranetctl-install.yaml
```

4. Wait until the pods are ready and running

```sh
kubectl get pods -l k8s-app=dranet -n kube-system
NAME           READY   STATUS    RESTARTS   AGE
dranet-459mh   1/1     Running   0          39m
dranet-ds5j6   1/1     Running   0          40m
```

5. Check the resource slices generated to identify the 

5. Once we are finished we can cleanup the nodepool with `dranetctl`.

```sh
dranetctl gke acceleratorpod delete dranet1
```