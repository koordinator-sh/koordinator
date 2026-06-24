#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-koordinator-bench}"
KOORDINATOR_VERSION="${KOORDINATOR_VERSION:-v1.5.0}"
# Pin kwok version for reproducible CI runs.
# Update this deliberately when upgrading kwok.
KWOK_VERSION="${KWOK_VERSION:-v0.6.0}"

echo "==> Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}"

echo "==> Installing kwok ${KWOK_VERSION}"
KWOK_BASE="https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}"
kubectl apply -f "${KWOK_BASE}/kwok.yaml"
kubectl apply -f "${KWOK_BASE}/stage-fast.yaml"

echo "==> Installing Koordinator ${KOORDINATOR_VERSION}"
export MANAGER_IMG="ghcr.io/koordinator-sh/koord-manager:${KOORDINATOR_VERSION}"
export SCHEDULER_IMG="ghcr.io/koordinator-sh/koord-scheduler:${KOORDINATOR_VERSION}"
SCRIPT="$(dirname "$0")/../../hack/deploy_kind.sh"
if [ -f "$SCRIPT" ]; then
  bash "$SCRIPT"
else
  echo "Warning: hack/deploy_kind.sh not found. Install Koordinator manually."
fi

echo "==> Waiting for koord-scheduler to be ready"
kubectl wait --for=condition=ready pod \
  -l app=koord-scheduler \
  -n koordinator-system \
  --timeout=120s || true

echo "==> Done. Run: make -C test/perf benchmark"
