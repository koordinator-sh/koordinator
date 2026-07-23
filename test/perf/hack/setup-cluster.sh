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

echo "==> Installing Koordinator ${KOORDINATOR_VERSION} (full stack via deploy_kind.sh)"
export MANAGER_IMG="ghcr.io/koordinator-sh/koord-manager:${KOORDINATOR_VERSION}"
export SCHEDULER_IMG="ghcr.io/koordinator-sh/koord-scheduler:${KOORDINATOR_VERSION}"
SCRIPT="${REPO_ROOT}/hack/deploy_kind.sh"
if [ -f "$SCRIPT" ]; then
  # deploy_kind.sh calls `make kustomize` which must run from the repo root.
  (cd "${REPO_ROOT}" && bash "$SCRIPT")
else
  echo "ERROR: hack/deploy_kind.sh not found at ${SCRIPT}" >&2
  exit 1
fi

# ── Workaround A ─────────────────────────────────────────────────────────────
# The v1.5.0 manifest renders --feature-gates with a trailing comma, which
# Kubernetes' flag parser splits into an empty token → fatal parse error →
# CrashLoopBackOff. Strip any trailing comma from every container arg.
# This is a no-op when a future release fixes the upstream manifest.
echo "==> [workaround A] Stripping trailing comma from feature-gates args"
for target in "deployment/koord-scheduler" "daemonset/koordlet"; do
  kubectl get "$target" -n koordinator-system -o json \
    | jq 'del(.metadata.resourceVersion, .metadata.uid, .status)
          | (.spec.template.spec.containers[].args) |= (if . then map(sub(",$"; "")) else . end)' \
    | kubectl apply -f -
done

# ── Workaround B ─────────────────────────────────────────────────────────────
# The v1.5.0 koord-scheduler-config ships an ElasticQuota pluginConfig entry
# whose args type doesn't match the plugin's registered factory, crash-looping
# the scheduler. Replace the ConfigMap with the original profile minus the
# ElasticQuota entries. All other plugin configs (NodeResourcesFit,
# LoadAwareScheduling, Coscheduling default args) are preserved as-is.
echo "==> [workaround B] Removing broken ElasticQuota pluginConfig from scheduler config"
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

# Drop webhook configs before waiting for pods. The MutatingWebhookConfiguration
# and ValidatingWebhookConfiguration route admission through koord-manager, which
# isn't ready yet — creating a circular deadlock. Benchmarks don't need webhooks.
echo "==> Removing webhook configurations to avoid bootstrap deadlock"
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
  -n koordinator-system --timeout=180s

echo "==> Done. Run: make -C test/perf benchmark"
