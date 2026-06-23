#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-koordinator-bench}"
KOORDINATOR_VERSION="${KOORDINATOR_VERSION:-v1.5.0}"

echo "==> Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}"

echo "==> Installing kwok"
kubectl apply -f https://github.com/kubernetes-sigs/kwok/releases/latest/download/kwok.yaml
kubectl apply -f https://github.com/kubernetes-sigs/kwok/releases/latest/download/stage-fast.yaml

echo "==> Installing Koordinator (koord-scheduler + koord-manager)"
export MANAGER_IMG="ghcr.io/koordinator-sh/koord-manager:${KOORDINATOR_VERSION}"
export SCHEDULER_IMG="ghcr.io/koordinator-sh/koord-scheduler:${KOORDINATOR_VERSION}"

# Use the repo's existing kind deployment script.
# This script installs Koordinator components into the kind cluster.
if [ -f "../../hack/deploy_kind.sh" ]; then
    ../../hack/deploy_kind.sh
else
    echo "Warning: hack/deploy_kind.sh not found — install Koordinator manually"
fi

echo "==> Waiting for koord-scheduler to be ready"
kubectl wait --for=condition=ready pod \
    -l app=koord-scheduler \
    -n koordinator-system \
    --timeout=120s

echo "==> Cluster ready. Run: make benchmark"