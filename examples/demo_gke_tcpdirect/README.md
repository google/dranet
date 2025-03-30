# GKE TCP Direct


1. Create a cluster

DRA is beta in 1.32, so it requires to explicitly enable the feature.

```sh
PROJECT="test-project"
CLUSTER="test-cluster"
ZONE="us-central1-c"
VERSION="1.32"

gcloud container clusters create "${CLUSTER}" \
    --cluster-version="${VERSION}" \
    --enable-multi-networking \
    --enable-dataplane-v2 \
    --enable-kubernetes-unstable-apis=resource.k8s.io/v1beta1/deviceclasses,resource.k8s.io/v1beta1/resourceclaims,resource.k8s.io/v1beta1/resourceclaimtemplates,resource.k8s.io/v1beta1/resourceslices \
    --no-enable-autorepair \
    --no-enable-autoupgrade \
    --zone="${ZONE}" \
    --project="${PROJECT}" # Explicitly set the project
```

2. Once the cluster has been created we need to create a Node Pool with A3 machines, `dranetctl` is an opinionanted tool that will set the necessary
values for an optimal performance.

```sh