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

REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

cd $REPO_ROOT

IMAGE="$1"

if [ -z "${IMAGE}" ]; then
  echo "Error: The IMAGE environment variable is not set."
  exit 1
fi

echo "Using IMAGE: ${IMAGE}"

docker build . -t "${IMAGE}" --push
kubectl set image ds/dranet dranet="${IMAGE}" -n kube-system
kubectl rollout status ds/dranet -n kube-system
kubectl rollout restart ds dranet -n kube-system
kubectl get pods -l k8s-app=dranet -n kube-system