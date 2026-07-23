#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-koordinator-bench}"
KOORDINATOR_VERSION="${KOORDINATOR_VERSION:-v1.5.0}"
# Pin versions for reproducible CI runs. Update deliberately when upgrading.
KWOK_VERSION="${KWOK_VERSION:-v0.6.0}"
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.28.0}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

SCHEDULER_IMAGE="ghcr.io/koordinator-sh/koord-scheduler:${KOORDINATOR_VERSION}"

echo "==> Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}"

echo "==> Installing kwok ${KWOK_VERSION}"
KWOK_BASE="https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}"
kubectl apply -f "${KWOK_BASE}/kwok.yaml"
kubectl apply -f "${KWOK_BASE}/stage-fast.yaml"

# Pre-pull the scheduler image on the Docker host then load it into the kind
# node's containerd. The pod uses imagePullPolicy: IfNotPresent and finds the
# image locally — startup takes seconds rather than the 2-3 min cold pull from
# ghcr.io that burned the entire rollout timeout budget.
echo "==> Pre-pulling ${SCHEDULER_IMAGE} into kind node"
docker pull "${SCHEDULER_IMAGE}"
kind load docker-image "${SCHEDULER_IMAGE}" --name "${CLUSTER_NAME}"

echo "==> Installing Koordinator ${KOORDINATOR_VERSION} (full stack via deploy_kind.sh)"
export MANAGER_IMG="ghcr.io/koordinator-sh/koord-manager:${KOORDINATOR_VERSION}"
export SCHEDULER_IMG="${SCHEDULER_IMAGE}"
SCRIPT="${REPO_ROOT}/hack/deploy_kind.sh"
if [ -f "$SCRIPT" ]; then
  # deploy_kind.sh calls `make kustomize` which must run from the repo root.
  (cd "${REPO_ROOT}" && bash "$SCRIPT")
else
  echo "ERROR: hack/deploy_kind.sh not found at ${SCRIPT}" >&2
  exit 1
fi

# Remove webhook configurations before restarting the scheduler. The
# MutatingWebhookConfiguration routes pod admission through koord-manager, but
# we scale koord-manager to 0 to free CI resources. If the webhook is present
# when the scheduler pod is created, admission blocks indefinitely.
echo "==> Removing webhook configurations"
kubectl delete mutatingwebhookconfiguration \
  koordinator-mutating-webhook-configuration --ignore-not-found
kubectl delete validatingwebhookconfiguration \
  koordinator-validating-webhook-configuration --ignore-not-found

# Scale down components not needed for benchmarks to free CI resources.
echo "==> Scaling down non-benchmark components"
kubectl scale deployment/koord-manager    -n koordinator-system --replicas=0
kubectl scale deployment/koord-descheduler -n koordinator-system --replicas=0

# Restart the scheduler so the new pod is created after the webhooks are gone.
# The initial pod from deploy_kind.sh was created while webhooks were present
# and may have been blocked in admission. With the image already in containerd,
# the restarted pod starts in seconds.
echo "==> Restarting scheduler (webhooks now gone, image pre-loaded)"
kubectl rollout restart deployment/koord-scheduler -n koordinator-system

echo "==> Waiting for koord-scheduler to be ready"
kubectl rollout status deployment/koord-scheduler \
  -n koordinator-system --timeout=300s

echo "==> Done. Run: make -C test/perf benchmark"
