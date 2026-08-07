# Koordinator Ten-Node Colocation Utilization Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a safe, repeatable repository-native toolkit that runs the approved same-pool staged colocation test against an explicitly selected ten-node Koordinator resource pool and produces a machine-readable acceptance report.

**Architecture:** Static Kubernetes manifests define the simulated online and BE workloads, while small Bash entry points manage pool preparation, SLO configuration, stage transitions, metric snapshots, abort checks, and cleanup. Pure shell-library functions perform validation and utilization/acceptance calculations so they can be tested without a cluster; all live mutations go through a dry-run-aware `kubectl` wrapper and require an explicit confirmation flag.

**Tech Stack:** Bash 4+, kubectl, jq 1.6+, curl, Kubernetes YAML, Koordinator `slo-controller-config`, Prometheus HTTP API.

## Global Constraints

- Target exactly 10 explicit node names; never infer all cluster nodes or accept a wildcard.
- Confine every workload to `koordinator.sh/test-pool=colocation-10` and namespace `koord-colocation-test`.
- Default to dry-run; live cluster mutations require `--execute` and `--confirm colocation-10`.
- Preserve and restore pre-existing node labels, taints, and `slo-controller-config` data.
- Never delete or drain resources outside the test namespace and explicit node list.
- Do not change Koordinator core code or install Koordinator/Prometheus automatically.
- Use `kubernetes.io/batch-cpu` and `kubernetes.io/batch-memory` for BE resource requests.

---

## File Structure

- `hack/colocation-test/lib.sh`: argument-independent validation, kubectl wrappers, JSON calculations, backup/restore, abort checks.
- `hack/colocation-test/run.sh`: top-level lifecycle and P0-P9 stage dispatcher.
- `hack/colocation-test/collect.sh`: Prometheus range-query collection and per-stage metadata capture.
- `hack/colocation-test/report.sh`: aggregate stage artifacts and evaluate acceptance rules.
- `hack/colocation-test/cleanup.sh`: idempotent workload, policy, label, and taint rollback.
- `hack/colocation-test/tests/lib_test.sh`: cluster-free unit tests for the shell library.
- `hack/colocation-test/tests/fixtures/`: deterministic node, metric, baseline, and stage JSON inputs.
- `test/colocation/config/test.env.example`: documented operator-provided settings.
- `test/colocation/config/colocation-config.json`: pool-scoped conservative reclaim configuration fragment.
- `test/colocation/expected/acceptance.json`: machine-readable thresholds and stop conditions.
- `test/colocation/manifests/`: namespace, RBAC-free workloads, Services, PodDisruptionBudget, and kustomization.
- `docs/manual-test/colocation-utilization.md`: operator runbook, prerequisites, commands, interpretation, and rollback.

### Task 1: Pure Validation and Calculation Library

**Files:**
- Create: `hack/colocation-test/lib.sh`
- Create: `hack/colocation-test/tests/lib_test.sh`
- Create: `hack/colocation-test/tests/fixtures/nodes-valid.json`
- Create: `hack/colocation-test/tests/fixtures/nodes-invalid.json`
- Create: `hack/colocation-test/tests/fixtures/acceptance-pass.json`
- Create: `hack/colocation-test/tests/fixtures/acceptance-fail.json`
- Create: `hack/colocation-test/tests/fixtures/thresholds.json`

**Interfaces:**
- Produces: `die(message)`, `require_commands(names...)`, `validate_node_file(path)`, `percent_delta(baseline,current)`, `percentage_points(baseline,current)`, `evaluate_acceptance(thresholds,result)`, `kubectl_mutate(args...)`.
- `validate_node_file` writes ten newline-delimited node names to stdout and fails unless the input contains exactly ten unique RFC-1123 names.
- `evaluate_acceptance` writes `{ "passed": bool, "checks": [...] }` JSON to stdout and returns 0 only when every required check passes.

- [ ] **Step 1: Write the failing library tests**

Create a self-contained test runner with assertions for valid nodes, duplicate nodes, fewer than ten nodes, percentage math, passing acceptance, failing acceptance, dry-run mutation output, and refusal to mutate without confirmation:

```bash
#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
source "${ROOT}/hack/colocation-test/lib.sh"
pass=0
fail=0
assert_ok() { if "$@"; then pass=$((pass + 1)); else fail=$((fail + 1)); fi; }
assert_fail() { if "$@"; then fail=$((fail + 1)); else pass=$((pass + 1)); fi; }
assert_eq() { if [[ "$1" == "$2" ]]; then pass=$((pass + 1)); else echo "want=$1 got=$2" >&2; fail=$((fail + 1)); fi; }

assert_eq 10 "$(validate_node_file "${ROOT}/hack/colocation-test/tests/fixtures/nodes-valid.json" | wc -l | tr -d ' ')"
assert_fail validate_node_file "${ROOT}/hack/colocation-test/tests/fixtures/nodes-invalid.json"
assert_eq 20 "$(percentage_points 30 50)"
assert_eq 10 "$(percent_delta 100 110)"
assert_ok evaluate_acceptance "${ROOT}/hack/colocation-test/tests/fixtures/thresholds.json" "${ROOT}/hack/colocation-test/tests/fixtures/acceptance-pass.json"
assert_fail evaluate_acceptance "${ROOT}/hack/colocation-test/tests/fixtures/thresholds.json" "${ROOT}/hack/colocation-test/tests/fixtures/acceptance-fail.json"
[[ ${fail} -eq 0 ]] || exit 1
echo "PASS: ${pass} assertions"
```

- [ ] **Step 2: Run the tests and verify the expected failure**

Run: `bash hack/colocation-test/tests/lib_test.sh`

Expected: FAIL because `hack/colocation-test/lib.sh` does not exist.

- [ ] **Step 3: Implement the minimal pure functions and safe mutation wrapper**

Implement strict JSON validation with `jq`, arithmetic with `awk`, and this live-mutation gate:

```bash
kubectl_mutate() {
  if [[ "${EXECUTE:-false}" != true ]]; then
    printf 'DRY-RUN: kubectl'; printf ' %q' "$@"; printf '\n'
    return 0
  fi
  [[ "${CONFIRM:-}" == colocation-10 ]] || die "live mutation requires --confirm colocation-10"
  command kubectl "${KUBECTL_CONTEXT_ARGS[@]:-}" "$@"
}
```

`evaluate_acceptance` must evaluate all eight approved criteria from `acceptance.json`, not exit after the first failure, and include measured, operator, threshold, and pass fields for each check.

- [ ] **Step 4: Run unit and syntax tests**

Run:

```bash
bash hack/colocation-test/tests/lib_test.sh
bash -n hack/colocation-test/lib.sh hack/colocation-test/tests/lib_test.sh
```

Expected: both commands PASS; unit output ends with `PASS:`.

- [ ] **Step 5: Commit Task 1**

```bash
git add hack/colocation-test/lib.sh hack/colocation-test/tests
git commit -m "test: add colocation validation library"
```

### Task 2: Test Configuration and Acceptance Contract

**Files:**
- Create: `test/colocation/config/test.env.example`
- Create: `test/colocation/config/colocation-config.json`
- Create: `test/colocation/expected/acceptance.json`
- Modify: `hack/colocation-test/tests/lib_test.sh`

**Interfaces:**
- Consumes: `validate_node_file`, `evaluate_acceptance` from Task 1.
- Produces: stable environment variable names `NODE_FILE`, `PROMETHEUS_URL`, `KUBE_CONTEXT`, `ONLINE_BASE_QPS`, `STAGE_DURATION`, `WARMUP_DURATION`, and the acceptance JSON schema.

- [ ] **Step 1: Add failing schema assertions**

Add assertions that the strategy has global `enable: false`, one node config selecting `koordinator.sh/test-pool=colocation-10`, and pool values 70/70/usage/5. Add acceptance assertions for CPU `20`, memory `15`, P99 `10`, error rate `0.001`, throughput loss `0.02`, recovery `300`, node CPU stop `0.95`, and node memory stop `0.90`.

Run: `bash hack/colocation-test/tests/lib_test.sh`

Expected: FAIL because the configuration files are absent.

- [ ] **Step 2: Add the pool-scoped colocation strategy**

Create `colocation-config.json` with the exact structure:

```json
{
  "enable": false,
  "nodeConfigs": [
    {
      "name": "colocation-utilization-test",
      "nodeSelector": {
        "matchLabels": {
          "koordinator.sh/test-pool": "colocation-10"
        }
      },
      "enable": true,
      "cpuReclaimThresholdPercent": 70,
      "memoryReclaimThresholdPercent": 70,
      "memoryCalculatePolicy": "usage",
      "resourceDiffThreshold": 0.05
    }
  ]
}
```

The global value remains disabled so unlabeled nodes cannot accidentally inherit test reclaim settings.

- [ ] **Step 3: Add the operator config and acceptance schema**

Document every environment value without secrets. Define the acceptance contract as numeric fractions and percentage points, including data completeness `0.95`, minimum repeats `3`, and baseline drift `0.10`.

- [ ] **Step 4: Run JSON and unit validation**

Run:

```bash
jq -e . test/colocation/config/colocation-config.json test/colocation/expected/acceptance.json
bash hack/colocation-test/tests/lib_test.sh
```

Expected: valid JSON and all assertions PASS.

- [ ] **Step 5: Commit Task 2**

```bash
git add test/colocation/config test/colocation/expected hack/colocation-test/tests/lib_test.sh
git commit -m "test: define colocation test policy and acceptance"
```

### Task 3: Pool-Confined Online and BE Manifests

**Files:**
- Create: `test/colocation/manifests/kustomization.yaml`
- Create: `test/colocation/manifests/namespace.yaml`
- Create: `test/colocation/manifests/online.yaml`
- Create: `test/colocation/manifests/loadgen.yaml`
- Create: `test/colocation/manifests/be-cpu.yaml`
- Create: `test/colocation/manifests/be-memory.yaml`
- Create: `test/colocation/manifests/be-mixed.yaml`
- Create: `test/colocation/manifests/pdb.yaml`
- Create: `hack/colocation-test/tests/manifests_test.sh`

**Interfaces:**
- Produces labels `app.kubernetes.io/part-of=koord-colocation-test`, `workload-class=online|be`, and `test-stage`.
- Online service exposes port 8080 and `/healthz`, `/work`, `/metrics`.
- BE Deployments start with zero replicas; the runner patches replicas and per-Pod BatchCPU/BatchMemory from generated overlays.

- [ ] **Step 1: Write failing manifest contract tests**

The test must run `kubectl kustomize`, parse YAML through `kubectl apply --dry-run=client`, and assert that every Pod template has required pool affinity and taint toleration. It must also assert:

- online replicas equal 20 and topology spread uses `kubernetes.io/hostname`;
- BE templates have QoS `BE`, scheduler `koord-scheduler`, priority `koord-batch`, Batch resources, and replicas 0;
- PDB has `minAvailable: 95%` for online Pods;
- no object uses `hostNetwork`, `hostPID`, privileged mode, hostPath, or a NodePort.

Run: `bash hack/colocation-test/tests/manifests_test.sh`

Expected: FAIL because the manifests do not exist.

- [ ] **Step 2: Implement namespace, online Deployment, Service, and PDB**

Use an image configurable through Kustomize and pinned by digest in the operator config. The online container request/limit must be equal for memory, use a bounded CPU limit, expose readiness/liveness probes, and run as non-root with a read-only root filesystem. Use `maxSkew: 1` and `DoNotSchedule` topology spreading.

- [ ] **Step 3: Implement the in-cluster constant-rate load generator**

Create a zero-replica Deployment whose command accepts QPS, duration, and output path through environment variables. It must emit a JSON summary containing request count, success count, error rate, requests/second, P50/P95/P99, start time, and end time. The runner activates exactly one load-generator Pod per stage.

- [ ] **Step 4: Implement zero-replica BE templates**

Each BE Pod must handle `SIGTERM`, use no persistent volume, and expose progress through logs. CPU uses a bounded compute loop, memory allocates and touches a bounded working set, and mixed combines both. Requests and limits use BatchCPU/BatchMemory and never ordinary CPU/memory values for reclaimed capacity.

- [ ] **Step 5: Validate rendered manifests**

Run:

```bash
bash hack/colocation-test/tests/manifests_test.sh
kubectl kustomize test/colocation/manifests | kubectl apply --dry-run=client -f -
```

Expected: all contract assertions PASS and every rendered object is accepted client-side.

- [ ] **Step 6: Commit Task 3**

```bash
git add test/colocation/manifests hack/colocation-test/tests/manifests_test.sh
git commit -m "test: add isolated online and batch workloads"
```

### Task 4: Preflight, Pool Preparation, and Reversible SLO Configuration

**Files:**
- Modify: `hack/colocation-test/lib.sh`
- Create: `hack/colocation-test/tests/lifecycle_test.sh`
- Create: `hack/colocation-test/tests/fixtures/node-before.json`
- Create: `hack/colocation-test/tests/fixtures/slo-config-before.json`

**Interfaces:**
- Produces: `preflight`, `backup_cluster_state`, `prepare_pool`, `apply_colocation_config`, `restore_cluster_state`.
- Backup directory layout: `${RUN_DIR}/backup/nodes/<node>.json`, `${RUN_DIR}/backup/slo-controller-config.json`, and `${RUN_DIR}/backup/inventory.json`.

- [ ] **Step 1: Add failing lifecycle tests with a fake kubectl**

The fake must record arguments and return fixtures. Assert that preflight checks exactly ten Ready, schedulable nodes; verifies Koordinator Deployments/DaemonSet, one ready koordlet per node, fresh NodeMetric, and nonzero BatchCPU/BatchMemory after policy application. Assert that restore commands target only recorded nodes and the backed-up ConfigMap.

Run: `bash hack/colocation-test/tests/lifecycle_test.sh`

Expected: FAIL because lifecycle functions are undefined.

- [ ] **Step 2: Implement preflight and immutable inventory**

Reject a node if it is missing, not Ready, unschedulable, or already has a conflicting `koordinator.sh/test-pool` value. Record UID with node name so cleanup refuses a recreated node with the same name but different UID.

- [ ] **Step 3: Implement backup and pool preparation**

Back up full relevant metadata before applying the label and `NoSchedule` taint. Use one explicit kubectl call per validated node; never use `--all` or a label selector for mutation.

- [ ] **Step 4: Implement ConfigMap merge and restore**

Read the existing `colocation-config` JSON, append or replace only the named test nodeConfig, and preserve all unrelated keys and nodeConfigs. Store the full original ConfigMap JSON. On restore, use resourceVersion-aware update; if another actor changed the ConfigMap after the test patch, stop and print a three-way diff instead of overwriting it.

- [ ] **Step 5: Run lifecycle, library, and syntax tests**

Run:

```bash
bash hack/colocation-test/tests/lifecycle_test.sh
bash hack/colocation-test/tests/lib_test.sh
bash -n hack/colocation-test/lib.sh hack/colocation-test/tests/lifecycle_test.sh
```

Expected: all tests PASS.

- [ ] **Step 6: Commit Task 4**

```bash
git add hack/colocation-test/lib.sh hack/colocation-test/tests
git commit -m "test: add reversible colocation pool lifecycle"
```

### Task 5: Stage Runner, Metrics Collection, and Safety Abort

**Files:**
- Create: `hack/colocation-test/run.sh`
- Create: `hack/colocation-test/collect.sh`
- Modify: `hack/colocation-test/lib.sh`
- Create: `hack/colocation-test/tests/runner_test.sh`
- Create: `hack/colocation-test/tests/fixtures/prometheus-success.json`
- Create: `hack/colocation-test/tests/fixtures/prometheus-missing.json`

**Interfaces:**
- `run.sh --config FILE --nodes FILE [--execute --confirm colocation-10] [--from P0 --to P9]`.
- Stage artifact layout: `${RUN_DIR}/stages/P2/{metadata.json,online-summary.json,prometheus/*.json,pods.json,nodes.json,nodemetrics.json,events.txt}`.
- `collect.sh snapshot STAGE` and `collect.sh range STAGE START END`.

- [ ] **Step 1: Write failing runner tests using fake kubectl, curl, and clock commands**

Assert the exact P0-P9 order, 5-minute warmup exclusion, P3/P4/P5 BE targets 30/50/70%, P6 QPS 150%, P7 memory 85%, P8 refusal when non-test workloads exist, P9 BE scale-to-zero, resumption from a completed stage, and abort order `loadgen stop -> BE zero -> snapshot`.

Run: `bash hack/colocation-test/tests/runner_test.sh`

Expected: FAIL because runner and collector do not exist.

- [ ] **Step 2: Implement run identity and resumable state**

Create an immutable run ID from UTC timestamp and configuration hash. Write a stage state atomically as `pending`, `running`, `completed`, `failed`, or `aborted`. Refuse to resume if node inventory, manifest digest, acceptance config, or Kube context differs.

- [ ] **Step 3: Implement Batch capacity-driven stage scaling**

Read each pool node's allocatable BatchCPU/BatchMemory, subtract current test BE requests, and generate stage overlays whose summed requests do not exceed the selected fraction. Spread each BE class across nodes and round down so no stage overcommits due to integer BatchCPU units.

- [ ] **Step 4: Implement Prometheus collection**

Use `/api/v1/query_range`, save raw responses, validate status and sample coverage, and collect pool/node CPU, working-set memory, online latency/error/throughput, throttling, restarts, BE use, and Koordinator Batch allocatable data. URL-encode queries and set connect/overall timeouts.

- [ ] **Step 5: Implement safety polling and abort path**

Poll every 15 seconds. Abort when an approved stop rule holds for its full duration: node CPU over 95% for 3 minutes, memory over 90% for 3 minutes, system OOM/NotReady, or online error rate over 1% for 2 minutes. Missing safety metrics also abort after two consecutive polls.

- [ ] **Step 6: Implement P0-P9 stage functions**

Each stage writes its intended and actual configuration before load begins. P8 must list all Pods on the selected node and refuse cordon/drain unless every evictable non-DaemonSet Pod belongs to `koord-colocation-test`; drain must include `--pod-selector app.kubernetes.io/part-of=koord-colocation-test` and a bounded timeout.

- [ ] **Step 7: Run runner and regression tests**

Run:

```bash
bash hack/colocation-test/tests/runner_test.sh
for test in hack/colocation-test/tests/*_test.sh; do bash "$test"; done
bash -n hack/colocation-test/*.sh
```

Expected: all fake-cluster stage and abort assertions PASS.

- [ ] **Step 8: Commit Task 5**

```bash
git add hack/colocation-test/run.sh hack/colocation-test/collect.sh hack/colocation-test/lib.sh hack/colocation-test/tests
git commit -m "test: orchestrate staged colocation utilization run"
```

### Task 6: Acceptance Report and Idempotent Cleanup

**Files:**
- Create: `hack/colocation-test/report.sh`
- Create: `hack/colocation-test/cleanup.sh`
- Modify: `hack/colocation-test/tests/lib_test.sh`
- Create: `hack/colocation-test/tests/report_test.sh`
- Create: `hack/colocation-test/tests/cleanup_test.sh`

**Interfaces:**
- `report.sh --run-dir DIR [--output DIR/report.json]` returns 0 for PASS, 1 for FAIL, and 2 for INVALID DATA.
- `cleanup.sh --run-dir DIR [--execute --confirm colocation-10]` is idempotent and writes `cleanup-result.json`.

- [ ] **Step 1: Write failing report tests**

Cover pass, threshold failure, less than 95% Prometheus coverage, fewer than three repeats, and more than 10% start/end baseline drift. Assert that invalid data is not reported as an SLO failure.

Run: `bash hack/colocation-test/tests/report_test.sh`

Expected: FAIL because `report.sh` does not exist.

- [ ] **Step 2: Implement aggregation and the eight acceptance checks**

Use the median of three P2/P4 repeats for formal benefit checks and retain the worst repeat for SLO checks. Report P2/P4 pool weighted values, per-node P50/P95/max, P6 recovery, P7 node health, and P9 restoration. Include raw artifact paths and configuration digests for auditability.

- [ ] **Step 3: Write failing cleanup tests**

Assert dry-run behavior, namespace-only workload deletion, UID checks, ConfigMap conflict refusal, exact label/taint restoration, node uncordon when P8 changed it, and successful second cleanup with no extra mutation.

Run: `bash hack/colocation-test/tests/cleanup_test.sh`

Expected: FAIL because `cleanup.sh` does not exist.

- [ ] **Step 4: Implement cleanup in dependency order**

Stop loadgen, scale BE to zero, delete the test namespace, uncordon the recorded P8 node, restore ConfigMap, then restore exact per-node metadata. Continue independent cleanup steps after a failure, collect every error, and return nonzero with manual recovery commands.

- [ ] **Step 5: Run all offline verification**

Run:

```bash
for test in hack/colocation-test/tests/*_test.sh; do bash "$test"; done
bash -n hack/colocation-test/*.sh hack/colocation-test/tests/*.sh
jq -e . test/colocation/config/*.json test/colocation/expected/*.json
kubectl kustomize test/colocation/manifests | kubectl apply --dry-run=client -f -
```

Expected: all tests PASS, JSON parses, and rendered manifests pass client validation.

- [ ] **Step 6: Commit Task 6**

```bash
git add hack/colocation-test/report.sh hack/colocation-test/cleanup.sh hack/colocation-test/tests
git commit -m "test: report and clean up colocation experiments"
```

### Task 7: Operator Runbook and Final End-to-End Dry Run

**Files:**
- Create: `docs/manual-test/colocation-utilization.md`
- Create: `hack/colocation-test/tests/e2e_dry_run.sh`

**Interfaces:**
- Documents exact prerequisites, dry-run, execution, monitoring, abort, resume, report, and cleanup commands.

- [ ] **Step 1: Write the runbook with explicit operator gates**

Include these commands with expected outcomes:

```bash
cp test/colocation/config/test.env.example /tmp/colocation-test.env
$EDITOR /tmp/colocation-test.env
hack/colocation-test/run.sh --config /tmp/colocation-test.env --nodes /tmp/pool-nodes.json
hack/colocation-test/run.sh --config /tmp/colocation-test.env --nodes /tmp/pool-nodes.json --execute --confirm colocation-10
hack/colocation-test/report.sh --run-dir /tmp/koord-colocation-runs/<run-id>
hack/colocation-test/cleanup.sh --run-dir /tmp/koord-colocation-runs/<run-id> --execute --confirm colocation-10
```

Explain that the first command sequence is non-mutating, P8 can be skipped, thresholds are test gates rather than production recommendations, and cleanup conflicts require human review.

- [ ] **Step 2: Execute the complete cluster-free dry run**

Use the fake kubectl/Prometheus fixtures to run P0-P9, produce `report.json`, then clean up twice.

Run:

```bash
hack/colocation-test/tests/e2e_dry_run.sh
```

Expected: P0-P9 complete, report status PASS, first cleanup succeeds, second cleanup reports `already-clean` and succeeds.

- [ ] **Step 3: Run repository hygiene checks**

Run:

```bash
git diff --check
for test in hack/colocation-test/tests/*_test.sh; do bash "$test"; done
bash -n hack/colocation-test/*.sh hack/colocation-test/tests/*.sh
kubectl kustomize test/colocation/manifests | kubectl apply --dry-run=client -f -
```

Expected: no whitespace errors; all offline tests and manifest validations PASS. Do not claim a real-cluster utilization result because no live cluster test has been authorized or run.

- [ ] **Step 4: Commit Task 7**

```bash
git add docs/manual-test/colocation-utilization.md hack/colocation-test/tests/e2e_dry_run.sh
git commit -m "docs: add colocation utilization test runbook"
```

## Final Review Checklist

- Every mutating path defaults to dry-run and requires both execution flags.
- Exactly ten unique explicit nodes are validated before mutation.
- All workloads have required pool affinity and toleration.
- SLO configuration is pool-scoped and merged without dropping unrelated data.
- P2 and P4 repeat three times; benefit uses medians and protection uses worst runs.
- Stop conditions work when metrics show danger and when safety metrics disappear.
- Report distinguishes FAIL from INVALID DATA.
- Cleanup is UID-aware, conflict-aware, bounded to task-owned resources, and idempotent.
- Offline tests do not require a Kubernetes cluster or network access.
