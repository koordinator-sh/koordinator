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

if [ -z "$IMG" ]; then
  echo "no found IMG env"
  exit 1
fi

K8S_VERSION=""
if [ -z "$KUBERNETES_VERSION" ]; then
  K8S_VERSION="latest"
else
  K8S_VERSION=$KUBERNETES_VERSION
fi

set -e

make kustomize
KUSTOMIZE=$(pwd)/bin/kustomize
(cd config/manager && "${KUSTOMIZE}" edit set image manager="${IMG}")

if [[ "$K8S_VERSION" == "1.22" ]]; then
  sed "s/feature-gates=/feature-gates=CompatibleCSIStorageCapacity=true/g" $(pwd)/config/manager/scheduler.yaml > /tmp/scheduler.yaml && mv /tmp/scheduler.yaml $(pwd)/config/manager/scheduler.yaml
  $(pwd)/hack/kustomize.sh "${KUSTOMIZE}" | sed -e 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/g' > /tmp/koordinator-kustomization.yaml
else
  $(pwd)/hack/kustomize.sh "${KUSTOMIZE}" | sed -e 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/g' > /tmp/koordinator-kustomization.yaml
fi

kubectl apply -f /tmp/koordinator-kustomization.yaml
