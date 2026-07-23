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

# Remove webhook configurations. The MutatingWebhookConfiguration routes
# pod admission through koord-manager, which we scale to 0 to free CI
# resources. Without deleting the webhook, any pod created during the
# benchmark would block indefinitely waiting for an admission response that
# never comes.
#
# Note: the scheduler pod itself is admitted BEFORE the webhook configuration
# is created (kustomize applies Deployments before WebhookConfigurations), so
# the scheduler's own pod is not affected by this deletion.
echo "==> Removing webhook configurations"
kubectl delete mutatingwebhookconfiguration \
  koordinator-mutating-webhook-configuration --ignore-not-found
kubectl delete validatingwebhookconfiguration \
  koordinator-validating-webhook-configuration --ignore-not-found

# Scale down components not needed for benchmarks to free CI resources.
echo "==> Scaling down non-benchmark components"
kubectl scale deployment/koord-manager    -n koordinator-system --replicas=0
kubectl scale deployment/koord-descheduler -n koordinator-system --replicas=0

# Wait for the scheduler pod that deploy_kind.sh already created. We do NOT
# restart the deployment here: the initial pod was admitted before the webhook
# was active and starts with the correct ConfigMap. A rollout restart would
# create a second pod competing for the leader lease — the new pod blocks on
# leader election, never serves /healthz, and can't become Ready, which in
# turn prevents Kubernetes from terminating the old pod (maxUnavailable: 0)
# — a deadlock that exhausts the timeout.
echo "==> Waiting for koord-scheduler to be ready"
kubectl rollout status deployment/koord-scheduler \
  -n koordinator-system --timeout=300s

echo "==> Done. Run: make -C test/perf benchmark"
