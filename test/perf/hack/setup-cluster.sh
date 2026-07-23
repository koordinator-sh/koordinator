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

# Pre-pull the scheduler image on the Docker host, then load it directly into
# the kind node's containerd. This eliminates image-pull latency from the
# rollout critical path: without this, the pod waits 2-3 min for a cold pull
# from ghcr.io, burning the entire rollout timeout budget.
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

# ── Workaround: ElasticQuota pluginConfig type mismatch (v1.5.0 bug) ─────────
# The shipped koord-scheduler-config ConfigMap contains an ElasticQuota
# pluginConfig entry whose args type doesn't match the plugin's registered
# factory (want ElasticQuotaArgs, got *runtime.Unknown) → scheduler panics on
# startup. Replace the ConfigMap with the original profile minus the three
# ElasticQuota references (pluginConfig, preFilter, postFilter, reserve).
# All other entries are preserved verbatim from config/manager/scheduler-config.yaml.
echo "==> Patching ConfigMap: removing broken ElasticQuota pluginConfig"
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

# Drop webhook configs to prevent the bootstrap deadlock: the
# MutatingWebhookConfiguration routes admission through koord-manager, which
# isn't ready yet, so the scheduler pod itself can't be admitted. Benchmarks
# don't need admission webhooks.
echo "==> Removing webhook configurations to avoid bootstrap deadlock"
kubectl delete mutatingwebhookconfiguration \
  koordinator-mutating-webhook-configuration --ignore-not-found
kubectl delete validatingwebhookconfiguration \
  koordinator-validating-webhook-configuration --ignore-not-found

# Scale down components not needed for benchmarks to free CI resources.
echo "==> Scaling down non-benchmark components"
kubectl scale deployment/koord-manager    -n koordinator-system --replicas=0
kubectl scale deployment/koord-descheduler -n koordinator-system --replicas=0

# Force a fresh rollout so the new pod reads the patched ConfigMap above.
# The pod that deploy_kind.sh created may have already started with the old
# ConfigMap (ElasticQuota present). A rollout restart guarantees the pod we
# wait on sees the fixed config. Since the image is pre-loaded, this restart
# takes ~30s instead of the 2-3 min pull that caused the previous timeout.
echo "==> Restarting scheduler to pick up patched ConfigMap"
kubectl rollout restart deployment/koord-scheduler -n koordinator-system

echo "==> Waiting for koord-scheduler to be ready"
kubectl rollout status deployment/koord-scheduler \
  -n koordinator-system --timeout=300s

echo "==> Done. Run: make -C test/perf benchmark"
