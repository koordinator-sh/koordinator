#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[@]}")/..

CODEGEN_PKG=${CODEGEN_PKG:-$(cd "${SCRIPT_ROOT}"; ls -d -1 ./vendor/k8s.io/code-generator 2>/dev/null || echo ../code-generator)}

bash "${CODEGEN_PKG}"/generate-internal-groups.sh \
  "deepcopy,conversion,defaulter" \
  github.com/koordinator-sh/koordinator/pkg/scheduler/apis/generated \
  github.com/koordinator-sh/koordinator/pkg/scheduler/apis \
  github.com/koordinator-sh/koordinator/pkg/scheduler/apis \
  "config:v1beta1" \
  --go-header-file "${SCRIPT_ROOT}"/hack/boilerplate.go.txt
