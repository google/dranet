#!/bin/bash

# Copyright 2025 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Default values
export VERSION="1.34"

# Initialize variables for flags
PROJECT=""
CLUSTER=""
ZONE=""

# Use getopt to parse command-line arguments
while getopts "z:n:p:" opt; do
  case "$opt" in
    z)
      ZONE="$OPTARG"
      ;;
    n)
      CLUSTER="$OPTARG"
      ;;
    p)
      PROJECT="$OPTARG"
      ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      echo "Usage: $0 -n <cluster_name> -z <zone> -p <project_id>" >&2
      exit 1
      ;;
  esac
done

# Check if required flags are provided
if [ -z "$CLUSTER" ] || [ -z "$ZONE" ] || [ -z "$PROJECT" ]; then
  echo "Error: Cluster name (-n), zone (-z), and project ID (-p) are required." >&2
  echo "Usage: $0 -n <cluster_name> -z <zone> -p <project_id>" >&2
  exit 1
fi

# DRA is beta so it requires to explicitly enable those APIs
echo "Creating GKE cluster: ${CLUSTER} in zone: ${ZONE} and project: ${PROJECT}"
gcloud container clusters create "${CLUSTER}" \
    --cluster-version="${VERSION}" \
    --enable-multi-networking \
    --enable-dataplane-v2 \
    --enable-kubernetes-unstable-apis=resource.k8s.io/v1beta1/deviceclasses,resource.k8s.io/v1beta1/resourceclaims,resource.k8s.io/v1beta1/resourceclaimtemplates,resource.k8s.io/v1beta1/resourceslices \
    --no-enable-autorepair \
    --no-enable-autoupgrade \
    --zone="${ZONE}" \
    --project="${PROJECT}" # Explicitly set the project

echo "GKE cluster '${CLUSTER}' creation initiated."
echo "You can check the status using: gcloud container clusters describe ${CLUSTER} --zone=${ZONE} --project=${PROJECT}"
