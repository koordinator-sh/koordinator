#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-koordinator-bench}"
KOORDINATOR_VERSION="${KOORDINATOR_VERSION:-v1.5.0}"
# Pin versions for reproducible CI runs. Update deliberately when upgrading.
KWOK_VERSION="${KWOK_VERSION:-v0.6.0}"
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.28.0}"

echo "==> Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}"

echo "==> Installing kwok ${KWOK_VERSION}"
KWOK_BASE="https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}"
kubectl apply -f "${KWOK_BASE}/kwok.yaml"
kubectl apply -f "${KWOK_BASE}/stage-fast.yaml"

echo "==> Installing Koordinator ${KOORDINATOR_VERSION}"
export MANAGER_IMG="ghcr.io/koordinator-sh/koord-manager:${KOORDINATOR_VERSION}"
export SCHEDULER_IMG="ghcr.io/koordinator-sh/koord-scheduler:${KOORDINATOR_VERSION}"
REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
SCRIPT="${REPO_ROOT}/hack/deploy_kind.sh"
if [ -f "$SCRIPT" ]; then
  # deploy_kind.sh calls `make kustomize` which must run from the repo root.
  (cd "${REPO_ROOT}" && bash "$SCRIPT")
else
  echo "Warning: hack/deploy_kind.sh not found. Install Koordinator manually."
fi

# Workaround: the v1.5.0 koord-scheduler-config ConfigMap has an ElasticQuota
# pluginConfig entry whose args type doesn't match the plugin's registered
# factory (GangSchedulingArgs vs ElasticQuotaArgs), crash-looping the
# scheduler at startup. Replace the ConfigMap with a minimal profile that
# keeps only Coscheduling (needed for gang benchmarks) and NodeResourcesFit.
# Must happen BEFORE the deployment rollout below so new pods read this config.
echo "==> Patching koord-scheduler-config to remove crash-looping plugins"
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
      leaderElect: false
    profiles:
      - schedulerName: koord-scheduler
        pluginConfig:
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
          - name: Coscheduling
            args:
              apiVersion: kubescheduler.config.k8s.io/v1
              kind: CoschedulingArgs
              defaultTimeout: 600s
        plugins:
          preEnqueue:
            enabled:
              - name: Coscheduling
          queueSort:
            disabled:
              - name: "*"
            enabled:
              - name: PrioritySort
          postFilter:
            disabled:
              - name: "*"
            enabled:
              - name: Coscheduling
              - name: DefaultPreemption
          preScore:
            enabled:
              - name: Coscheduling
          score:
            enabled:
              - name: Coscheduling
                weight: 1
          reserve:
            enabled:
              - name: Coscheduling
          permit:
            enabled:
              - name: Coscheduling
          preBind:
            enabled:
              - name: DefaultPreBind
          bind:
            disabled:
              - name: "*"
            enabled:
              - name: DefaultBinder
          postBind:
            enabled:
              - name: Coscheduling
CONFIGEOF

# Workaround: the v1.5.0 manifest renders --feature-gates with a trailing
# comma, which fails Kubernetes' flag parser and crash-loops both
# koord-scheduler and koordlet. Strip any trailing comma from every
# container arg. Safe no-op if a future version fixes this upstream.
# This patch also triggers the rollout that picks up the ConfigMap above.
echo "==> Working around trailing-comma feature-gates bug in v${KOORDINATOR_VERSION#v} manifests"
for target in "deployment/koord-scheduler" "daemonset/koordlet"; do
  kubectl get "$target" -n koordinator-system -o json \
    | jq 'del(.metadata.resourceVersion, .metadata.uid, .status)
          | (.spec.template.spec.containers[].args) |= (if . then map(sub(",$"; "")) else . end)' \
    | kubectl apply -f -
done

# Drop webhook configs before waiting for pods. koord-manager handles cert
# injection and webhook admission, but its own pods cannot be admitted until
# the webhook endpoint is reachable — a circular deadlock. Benchmarks only
# require the scheduler, so removing these configs is safe here.
echo "==> Removing webhook configurations to avoid bootstrap deadlock"
kubectl delete mutatingwebhookconfiguration \
  koordinator-mutating-webhook-configuration --ignore-not-found
kubectl delete validatingwebhookconfiguration \
  koordinator-validating-webhook-configuration --ignore-not-found

# Scale down components not needed for benchmarks to save CI resources.
echo "==> Scaling down non-benchmark components"
kubectl scale deployment/koord-manager    -n koordinator-system --replicas=0
kubectl scale deployment/koord-descheduler -n koordinator-system --replicas=0

echo "==> Waiting for koord-scheduler to be ready"
kubectl rollout status deployment/koord-scheduler -n koordinator-system --timeout=180s

echo "==> Done. Run: make -C test/perf benchmark"
