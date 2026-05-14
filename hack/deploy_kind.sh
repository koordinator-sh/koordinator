#!/usr/bin/env bash

#
# Copyright 2023 The Koordinator Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

set -e

make kustomize
KUSTOMIZE=$(pwd)/bin/kustomize

if [[ -n "$MANAGER_IMG" ]]; then
  (cd config/manager && "${KUSTOMIZE}" edit set image manager="${MANAGER_IMG}")
fi
if [[ -n "$SCHEDULER_IMG" ]]; then
  (cd config/manager && "${KUSTOMIZE}" edit set image scheduler="${SCHEDULER_IMG}")
fi
if [[ -n "$DESCHEDULER_IMG" ]]; then
  (cd config/manager && "${KUSTOMIZE}" edit set image descheduler="${DESCHEDULER_IMG}")
fi
if [[ -n "$KOORDLET_IMG" ]]; then
  (cd config/manager && "${KUSTOMIZE}" edit set image koordlet="${KOORDLET_IMG}")
fi

if [[ "$KUBERNETES_VERSION" == "1.22" ]]; then
  sed "s/feature-gates=/feature-gates=CompatibleCSIStorageCapacity=true/g" $(pwd)/config/manager/scheduler.yaml > /tmp/scheduler.yaml && mv /tmp/scheduler.yaml $(pwd)/config/manager/scheduler.yaml
fi

# Kubernetes < 1.34 does not have resource.k8s.io/v1 APIs (ResourceClaims, ResourceSlices,
# DeviceClasses). Since k8s 1.35 locks DynamicResourceAllocation=true, the scheduler would
# try to watch these APIs and fail. Disable the DRA informers for older clusters.
# Also disable WatchListClient because k8s < 1.30 API servers do not support watchlist semantics
# (resourceVersionMatch=NotOlderThan), which causes noisy retries on every informer startup.
if [[ -n "$KUBERNETES_VERSION" ]] && [[ "$(printf '%s\n%s\n' "1.34" "$KUBERNETES_VERSION" | sort -V | head -1)" == "$KUBERNETES_VERSION" ]] && [[ "$KUBERNETES_VERSION" != "1.34" ]]; then
  sed "s/feature-gates=/feature-gates=DisableDynamicResourceAllocationInformer=true,WatchListClient=false,/g" $(pwd)/config/manager/scheduler.yaml > /tmp/scheduler.yaml && mv /tmp/scheduler.yaml $(pwd)/config/manager/scheduler.yaml
fi

$(pwd)/hack/kustomize.sh "${KUSTOMIZE}" | sed -e 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/g' > /tmp/koordinator-kustomization.yaml

kubectl apply -f /tmp/koordinator-kustomization.yaml
