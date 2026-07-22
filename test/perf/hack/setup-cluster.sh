#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-koordinator-bench}"
KOORDINATOR_VERSION="${KOORDINATOR_VERSION:-v1.5.0}"
# Pin versions for reproducible CI runs. Update deliberately when upgrading.
KWOK_VERSION="${KWOK_VERSION:-v0.6.0}"
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.28.0}"

echo "==> Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}"

echo "==> Installing kwok ${KWOK_VERSION}"
KWOK_BASE="https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}"
kubectl apply -f "${KWOK_BASE}/kwok.yaml"
kubectl apply -f "${KWOK_BASE}/stage-fast.yaml"

echo "==> Installing Koordinator ${KOORDINATOR_VERSION}"
export MANAGER_IMG="ghcr.io/koordinator-sh/koord-manager:${KOORDINATOR_VERSION}"
export SCHEDULER_IMG="ghcr.io/koordinator-sh/koord-scheduler:${KOORDINATOR_VERSION}"
REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
SCRIPT="${REPO_ROOT}/hack/deploy_kind.sh"
if [ -f "$SCRIPT" ]; then
  # deploy_kind.sh calls `make kustomize` which must run from the repo root.
  (cd "${REPO_ROOT}" && bash "$SCRIPT")
else
  echo "Warning: hack/deploy_kind.sh not found. Install Koordinator manually."
fi

echo "==> Waiting for koord-scheduler to be ready"
kubectl wait --for=condition=ready pod \
  -l app=koord-scheduler \
  -n koordinator-system \
  --timeout=120s

echo "==> Done. Run: make -C test/perf benchmark"
