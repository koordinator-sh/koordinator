#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-koordinator-bench}"
KOORDINATOR_VERSION="${KOORDINATOR_VERSION:-v1.5.0}"
# Pin versions for reproducible CI runs. Update deliberately when upgrading.
KWOK_VERSION="${KWOK_VERSION:-v0.6.0}"
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.28.0}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

echo "==> Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}"

echo "==> Installing kwok ${KWOK_VERSION}"
KWOK_BASE="https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}"
kubectl apply -f "${KWOK_BASE}/kwok.yaml"
kubectl apply -f "${KWOK_BASE}/stage-fast.yaml"

echo "==> Installing PodGroup CRD (required for gang scenario)"
kubectl apply -f "${REPO_ROOT}/config/crd/bases/scheduling.sigs.k8s.io_podgroups.yaml"

echo "==> Deploying koord-scheduler ${KOORDINATOR_VERSION}"
# Substitute the image tag so KOORDINATOR_VERSION controls which image is pulled.
sed "s|ghcr.io/koordinator-sh/koord-scheduler:v1.5.0|ghcr.io/koordinator-sh/koord-scheduler:${KOORDINATOR_VERSION}|g" \
  "${SCRIPT_DIR}/koord-scheduler.yaml" | kubectl apply -f -

echo "==> Waiting for koord-scheduler to be ready"
kubectl rollout status deployment/koord-scheduler \
  -n koordinator-system \
  --timeout=180s

echo "==> Done. Run: make -C test/perf benchmark"
