#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-koordinator-bench}"
KWOK_VERSION="${KWOK_VERSION:-v0.6.0}"
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.28.7}"
# Derive the minor version string used by deploy_kind.sh from KIND_NODE_IMAGE
# so the two don't drift when the image tag is bumped (e.g. v1.29.x → "1.29").
KUBERNETES_VERSION="${KUBERNETES_VERSION:-$(echo "${KIND_NODE_IMAGE##*:v}" | cut -d. -f1,2)}"
# Namespace shared by both scenario configs and the ElasticQuota in this script.
# Change BENCHMARK_NS if you rename the namespace in configs/scenarios/*.yaml.
BENCHMARK_NS="${BENCHMARK_NS:-benchmark}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

# Use the run ID as the image tag so that the binary and config always come
# from the same commit — version skew between a released binary and HEAD
# config is the primary source of plugin-args type-mismatch crashes.
#
# Only the scheduler image is built from source: koord-manager is scaled to 0
# immediately after deployment, and koordlet is patched onto a nodeSelector
# that matches no node, so neither image is ever pulled. Skipping their builds
# saves two full Go compile + Docker build cycles from the CI budget.
IMG_TAG="bench-${GITHUB_RUN_ID:-local}"
SCHEDULER_IMG="koordinator-sh/koord-scheduler:${IMG_TAG}"

echo "==> Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}"

echo "==> Installing kwok ${KWOK_VERSION}"
KWOK_BASE="https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}"
kubectl apply -f "${KWOK_BASE}/kwok.yaml"
kubectl apply -f "${KWOK_BASE}/stage-fast.yaml"

echo "==> Building koord-scheduler image from source (tag: ${IMG_TAG})"
(cd "${REPO_ROOT}" && \
  docker build . -t "${SCHEDULER_IMG}" -f docker/koord-scheduler.dockerfile)

echo "==> Loading koord-scheduler image into kind node"
kind load docker-image "${SCHEDULER_IMG}" --name "${CLUSTER_NAME}"

echo "==> Installing Koordinator (full stack via deploy_kind.sh)"
export SCHEDULER_IMG KUBERNETES_VERSION
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

# Wait for the scaled-down pods to actually terminate before checking namespace
# health later — `kubectl scale` is async and returns immediately, but pods take
# a moment to receive SIGTERM and exit. Without this, the health-check below can
# catch them mid-shutdown and false-positive on pods behaving exactly as intended.
kubectl wait --for=delete pod -l koord-app=koord-manager     -n koordinator-system --timeout=30s 2>/dev/null || true
kubectl wait --for=delete pod -l koord-app=koord-descheduler -n koordinator-system --timeout=30s 2>/dev/null || true
# koordlet DaemonSet pods start terminating once the nodeSelector patch (above)
# propagates; wait for them to be gone before the health check below.
kubectl wait --for=delete pod -l koord-app=koordlet          -n koordinator-system --timeout=30s 2>/dev/null || true

echo "==> Waiting for koord-scheduler to be ready"
kubectl rollout status deployment/koord-scheduler \
  -n koordinator-system --timeout=300s

echo "==> Verifying full koordinator-system namespace pod state"
kubectl get pods -n koordinator-system -o wide
# Exclude terminating pods (deletionTimestamp set) — they are intentionally
# shutting down after the scale-to-0 above and are not errors.
# For remaining pods, fail if any is not Running/Succeeded, has a container
# that is not ready, or has restarted more than twice.
UNHEALTHY=$(kubectl get pods -n koordinator-system -o json | jq -r '
  .items[]
  | select(.metadata.deletionTimestamp == null)
  | select(
      (.status.phase != "Running" and .status.phase != "Succeeded")
      or (.status.phase == "Running" and (
            ([.status.containerStatuses[]? | select(.ready == false)] | length) > 0
            or ([.status.containerStatuses[]? | select(.restartCount > 2)] | length) > 0
          ))
    )
  | .metadata.name
')
if [[ -n "$UNHEALTHY" ]]; then
  echo "ERROR: unhealthy pods in koordinator-system:" >&2
  echo "$UNHEALTHY" >&2
  kubectl describe pods -n koordinator-system || true
  exit 1
fi

# Pre-create the koordinator-default-quota and a benchmark child quota.
#
# "koordinator-default-quota" (extension.DefaultQuotaName) is the fallback quota
# that koord-manager normally creates in koordinator-system — pods that don't
# match any explicitly named quota are placed here.  We scale koord-manager to 0
# above before it has a chance to create it.  Empirically, without this object
# the ElasticQuota plugin produces FailedScheduling events even when the
# benchmark quota's min/max are set generously.  The exact causal path through
# GroupQuotaManager has not been verified; treat this as empirically required.
#
# The benchmark quota sets min=max=2000 CPU.  Actual demand is 1,000 × 500m =
# 500 CPU, so the limit is never reached and quota is never the bottleneck.
# 2,000 is within the cluster's total allocatable (100 nodes), unlike the
# previous 10,000 which exceeded it and made the "quota is never the bottleneck"
# claim unverifiable.
echo "==> Pre-creating koordinator-default-quota and benchmark ElasticQuota"
kubectl apply -f - <<'EOF'
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: ElasticQuota
metadata:
  name: koordinator-default-quota
  namespace: koordinator-system
spec:
  max:
    cpu: "1000000"
    memory: 1000Ti
  min:
    cpu: "0"
    memory: "0"
EOF
kubectl create namespace "${BENCHMARK_NS}" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f - <<EOF
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: ElasticQuota
metadata:
  name: ${BENCHMARK_NS}
  namespace: ${BENCHMARK_NS}
spec:
  max:
    cpu: "2000"
    memory: 20Ti
  min:
    cpu: "2000"
    memory: 20Ti
EOF

echo "==> Done — cluster is ready. Run: make -C test/perf benchmark"
