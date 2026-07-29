#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-koordinator-bench}"
KWOK_VERSION="${KWOK_VERSION:-v0.6.0}"
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.28.7}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

# Use the run ID as the image tag so that the binary and config always come
# from the same commit — version skew between a released binary and HEAD
# config is the primary source of plugin-args type-mismatch crashes.
IMG_TAG="bench-${GITHUB_RUN_ID:-local}"
MANAGER_IMG="koordinator-sh/koord-manager:${IMG_TAG}"
SCHEDULER_IMG="koordinator-sh/koord-scheduler:${IMG_TAG}"
KOORDLET_IMG="koordinator-sh/koordlet:${IMG_TAG}"

echo "==> Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}"

echo "==> Installing kwok ${KWOK_VERSION}"
KWOK_BASE="https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}"
kubectl apply -f "${KWOK_BASE}/kwok.yaml"
kubectl apply -f "${KWOK_BASE}/stage-fast.yaml"

# Build all three images from the current checkout. This guarantees the binary
# and the config/manifests come from the same commit, matching the pattern
# used by the project's e2e workflows.
echo "==> Building Koordinator images from source (tag: ${IMG_TAG})"
(cd "${REPO_ROOT}" && \
  docker build --pull . -t "${MANAGER_IMG}"   -f docker/koord-manager.dockerfile && \
  docker build        . -t "${SCHEDULER_IMG}" -f docker/koord-scheduler.dockerfile && \
  docker build        . -t "${KOORDLET_IMG}"  -f docker/koordlet.dockerfile)

echo "==> Loading images into kind node"
kind load docker-image "${MANAGER_IMG}"   --name "${CLUSTER_NAME}"
kind load docker-image "${SCHEDULER_IMG}" --name "${CLUSTER_NAME}"
kind load docker-image "${KOORDLET_IMG}"  --name "${CLUSTER_NAME}"

echo "==> Installing Koordinator (full stack via deploy_kind.sh)"
export MANAGER_IMG SCHEDULER_IMG KOORDLET_IMG
DEPLOY_SCRIPT="${REPO_ROOT}/hack/deploy_kind.sh"
if [ ! -f "${DEPLOY_SCRIPT}" ]; then
  echo "ERROR: hack/deploy_kind.sh not found at ${DEPLOY_SCRIPT}" >&2
  exit 1
fi
# deploy_kind.sh calls `make kustomize` so it must run from the repo root.
(cd "${REPO_ROOT}" && bash "${DEPLOY_SCRIPT}")

# Remove webhook configurations. The MutatingWebhookConfiguration routes
# pod admission through koord-manager, which we scale to 0 to free CI
# resources. Without deleting the webhook, any pod created during the
# benchmark would block indefinitely waiting for an admission response that
# never comes.
echo "==> Removing webhook configurations"
kubectl delete mutatingwebhookconfiguration \
  koordinator-mutating-webhook-configuration --ignore-not-found
kubectl delete validatingwebhookconfiguration \
  koordinator-validating-webhook-configuration --ignore-not-found

# Scale down components not needed for benchmarks to free CI resources.
# koord-manager and koord-descheduler are Deployments; koordlet is a
# DaemonSet and uses a nodeSelector patch to achieve the same effect.
# koordlet on a kind node would CrashLoopBackOff anyway (it needs a real
# kubelet's /var/lib/kubelet and cgroup hierarchy), and the benchmark only
# measures scheduler throughput — NodeMetric data from koordlet is not
# required (filterExpiredNodeMetrics=false means absent metrics are safe).
echo "==> Scaling down non-benchmark components"
kubectl scale deployment/koord-manager     -n koordinator-system --replicas=0
kubectl scale deployment/koord-descheduler -n koordinator-system --replicas=0
kubectl patch daemonset koordlet -n koordinator-system \
  -p '{"spec":{"template":{"spec":{"nodeSelector":{"koordinator-benchmark/skip":"true"}}}}}'

echo "==> Waiting for koord-scheduler to be ready"
kubectl rollout status deployment/koord-scheduler \
  -n koordinator-system --timeout=300s

echo "==> Verifying full koordinator-system namespace pod state"
kubectl get pods -n koordinator-system -o wide
# Check container readiness, not pod phase. A CrashLoopBackOff pod's
# .status.phase is "Running", so a phase-based check passes silently while
# the container is restarting. Instead, fail if any container reports
# ready=false or has a non-zero restart count.
NOT_READY=$(kubectl get pods -n koordinator-system -o json \
  | jq -r '
      .items[] |
      .metadata.name as $pod |
      .status.containerStatuses[]? |
      select(.ready == false or .restartCount > 0) |
      "\($pod)/\(.name) ready=\(.ready) restarts=\(.restartCount)"
    ' 2>/dev/null || true)
if [[ -n "$NOT_READY" ]]; then
  echo "ERROR: unhealthy containers in koordinator-system:" >&2
  echo "$NOT_READY" >&2
  kubectl describe pods -n koordinator-system || true
  exit 1
fi

echo "==> Done. Run: make -C test/perf benchmark"
