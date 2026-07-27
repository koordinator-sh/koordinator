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

# ── test/perf-only patch: disable ElasticQuota for benchmark environment ─────
# config/manager/scheduler-config.yaml ships with ElasticQuota enabled (correct
# for normal deployments). For the benchmark cluster we disable it at runtime
# to avoid any plugin-config issues in the test environment. This patch is
# intentionally scoped to test/perf and never touches the shared source file.
echo "==> Patching scheduler config: disabling ElasticQuota (benchmark-only)"
kubectl apply -f - <<'CONFIGEOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: koord-scheduler-config
  namespace: koordinator-system
data:
  koord-scheduler-config: |
    apiVersion: kubescheduler.config.k8s.io/v1
    kind: KubeSchedulerConfiguration
    leaderElection:
      leaderElect: true
      resourceLock: leases
      resourceName: koord-scheduler
      resourceNamespace: koordinator-system
    profiles:
      - pluginConfig:
        - name: NodeResourcesFit
          args:
            apiVersion: kubescheduler.config.k8s.io/v1
            kind: NodeResourcesFitArgs
            scoringStrategy:
              type: LeastAllocated
              resources:
                - name: cpu
                  weight: 1
                - name: memory
                  weight: 1
                - name: "kubernetes.io/batch-cpu"
                  weight: 1
                - name: "kubernetes.io/batch-memory"
                  weight: 1
        - name: LoadAwareScheduling
          args:
            apiVersion: kubescheduler.config.k8s.io/v1
            kind: LoadAwareSchedulingArgs
            filterExpiredNodeMetrics: false
            nodeMetricExpirationSeconds: 300
            resourceWeights:
              cpu: 1
              memory: 1
            usageThresholds:
              cpu: 0
              memory: 0
            estimatedScalingFactors:
              cpu: 85
              memory: 70
        plugins:
          preEnqueue:
            enabled:
              - name: Coscheduling
          queueSort:
            disabled:
              - name: "*"
            enabled:
              - name: PrioritySort
          preFilter:
            enabled:
              - name: SchedulingHint
              - name: Reservation
              - name: NodeNUMAResource
              - name: DeviceShare
              - name: Coscheduling
          filter:
            enabled:
              - name: LoadAwareScheduling
              - name: NodeNUMAResource
              - name: DeviceShare
              - name: Reservation
          postFilter:
            disabled:
              - name: "*"
            enabled:
              - name: Reservation
              - name: Coscheduling
              - name: DefaultPreemption
          preScore:
            enabled:
              - name: Reservation
              - name: Coscheduling
          score:
            enabled:
              - name: LoadAwareScheduling
                weight: 1
              - name: NodeNUMAResource
                weight: 1
              - name: DeviceShare
                weight: 1
              - name: Reservation
                weight: 5000
              - name: Coscheduling
                weight: 1
          reserve:
            enabled:
              - name: LoadAwareScheduling
              - name: NodeNUMAResource
              - name: DeviceShare
              - name: Reservation
              - name: Coscheduling
          permit:
            enabled:
              - name: Coscheduling
          preBind:
            enabled:
              - name: NodeNUMAResource
              - name: DeviceShare
              - name: Reservation
              - name: Coscheduling
              - name: DefaultPreBind
          bind:
            disabled:
              - name: "*"
            enabled:
              - name: Reservation
              - name: DefaultBinder
          postBind:
            enabled:
              - name: Coscheduling
        schedulerName: koord-scheduler
CONFIGEOF

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
echo "==> Scaling down non-benchmark components"
kubectl scale deployment/koord-manager    -n koordinator-system --replicas=0
kubectl scale deployment/koord-descheduler -n koordinator-system --replicas=0

echo "==> Waiting for koord-scheduler to be ready"
kubectl rollout status deployment/koord-scheduler \
  -n koordinator-system --timeout=300s

echo "==> Done. Run: make -C test/perf benchmark"
