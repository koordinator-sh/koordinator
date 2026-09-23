# Koordinator Scheduler Benchmark

A lightweight scheduler benchmark tool for `koord-scheduler` that runs in
GitHub Actions CI or local dev, producing reproducible throughput and
latency metrics against simulated ([kwok](https://github.com/kubernetes-sigs/kwok))
nodes.

## Quick Start

### Prerequisites

- Go 1.21+
- Docker
- [kind](https://kind.sigs.k8s.io/)

The setup script downloads the kwok release manifests automatically; a
separate kwok CLI installation is not required.

### Run locally

```bash
# From the koordinator repo root:

# 1. Create a local cluster with kwok + Koordinator installed
make -C test/perf setup

# 2. Run the basic benchmark scenario
make -C test/perf benchmark

# 3. Tear down the cluster when done
make -C test/perf teardown
```

**Expected time:** roughly 3-5 minutes end to end on a typical dev laptop —
about 2-3 minutes for `setup` (kind cluster + kwok + Koordinator install),
under a minute for `benchmark` itself with the default 1,000-pod scenario,
and a few seconds for `teardown`. Slower machines or a cold Docker image
cache will push `setup` higher; that step dominates the total time.

### Run a specific config

```bash
make -C test/perf benchmark CONFIG=configs/scenarios/basic-1k.yaml
```

`CONFIG` takes any path under `configs/scenarios/`. (`SCENARIO=<name>` still
works and expands to `configs/scenarios/<name>-1k.yaml` for convenience, but
`CONFIG` is the one to reach for once more than one config per scenario
exists.)

### Compare against a baseline

```bash
make -C test/perf benchmark BASELINE=baselines/basic-1k.json
```

Compares the run's throughput and P99 latency against a previously captured
baseline JSON. If throughput drops or P99 rises beyond the thresholds set in
the scenario config, the result's `thresholdBreached` field is `true`.

### Run unit tests

```bash
make -C test/perf test
```

---

## Scenario configuration reference

Every scenario is driven by a YAML file under `configs/scenarios/`. All
fields below live under `pkg/types.ScenarioConfig`.

| Field                          | Type                               | Required   | Default                                      | Description                                                                                                                                                                                   |
| ------------------------------ | ---------------------------------- | ---------- | -------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`                         | string                             | yes        | —                                            | Scenario name; must match a registered scenario (`basic`, `gang`, `elasticquota`, `reservation`, or `loadaware`)                                                                              |
| `description`                  | string                             | no         | —                                            | Free-text note, not used by the engine                                                                                                                                                        |
| `schedulerName`                | string                             | no         | `koord-scheduler`                            | Scheduler that processes the benchmark pods                                                                                                                                                   |
| `namespace`                    | string                             | no         | `benchmark`                                  | Namespace for pods; `elasticquota` requires this to be set explicitly (no implicit default)                                                                                                   |
| `nodeCount`                    | int                                | yes (>0)   | —                                            | Number of simulated kwok nodes to create                                                                                                                                                      |
| `podCount`                     | int                                | yes (>0)   | —                                            | Number of pods fired in the burst                                                                                                                                                             |
| `concurrency`                  | int                                | yes (>0)   | —                                            | Max in-flight pod-create requests at once                                                                                                                                                     |
| `clientQPS`                    | float                              | yes (>0)   | —                                            | client-go QPS for the k8s client used by the engine                                                                                                                                           |
| `clientBurst`                  | int                                | yes (>0)   | —                                            | client-go burst for the same client                                                                                                                                                           |
| `qosClass`                     | string                             | no         | —                                            | If set, applied as a `koordinator.sh/qosClass` pod label                                                                                                                                      |
| `quotaCPU`                     | string                             | no         | —                                            | ElasticQuota max/min CPU (required for `elasticquota`), e.g. `"150"`                                                                                                                          |
| `quotaMemory`                  | string                             | no         | —                                            | ElasticQuota max/min memory (required for `elasticquota`), e.g. `"300Gi"`                                                                                                                     |
| `expectedScheduledPodCount`    | int                                | no         | `podCount`                                   | How many pods Watcher waits for; use when a subset is expected to stay Pending (elasticquota). Must match `floor(min(quotaCPU/cpu, quotaMemory/memory))` — `Validate()` enforces consistency. |
| `resourceRequests`             | map[string]string                  | no         | none                                         | e.g. `{cpu: "100m", memory: "128Mi"}` — applied as both requests and limits                                                                                                                   |
| `labels`                       | map[string]string                  | no         | none                                         | Extra labels merged onto every benchmark pod                                                                                                                                                  |
| `annotations`                  | map[string]string                  | no         | none                                         | Extra annotations applied to every benchmark pod                                                                                                                                              |
| `nodeTemplateFile`             | string                             | no         | built-in default (32 CPU / 256Gi / 110 pods) | Path to a full `corev1.Node` YAML to base simulated nodes on                                                                                                                                  |
| `nodeCreationWorkers`          | int                                | no (>=0)   | 20                                           | Parallelism for node creation                                                                                                                                                                 |
| `reservationCount`             | int                                | no         | 0                                            | Number of Reservations to create for the `reservation` scenario; the first `reservationCount` pods are labeled to consume them                                                                |
| `highUtilNodeCount`            | int                                | no         | 0                                            | Number of nodes seeded with high-utilization NodeMetrics for the `loadaware` scenario                                                                                                         |
| `highUtilCPUPct`               | int                                | no (0-100) | 0                                            | CPU utilization percentage for high-utilization nodes; defaults to 80 when `highUtilNodeCount` is greater than zero                                                                           |
| `timeout`                      | string (Go duration, e.g. `"10m"`) | no         | `10m`                                        | Hard ceiling on total run time; the run is aborted and reported with `timedOut: true` if exceeded                                                                                             |
| `thresholds.throughputDropPct` | float                              | no (0-100) | 0                                            | Max allowed throughput drop vs baseline, as a percentage                                                                                                                                      |
| `thresholds.p99IncreasePct`    | float                              | no (>=0)   | 0                                            | Max allowed P99 latency increase vs baseline, as a percentage                                                                                                                                 |
| `thresholds.failureRatePct`    | float                              | no (0-100) | 1                                            | Max allowed scheduling-failure rate as a percentage. Override for scenarios that deliberately produce throttling (e.g. `elasticquota` uses 75).                                               |
| `gangSize` / `minMember`       | int                                | no         | 0                                            | PodGroup size and minimum member count for the `gang` scenario; unused by `basic`, `elasticquota`, `reservation`, and `loadaware`                                                             |
| `extra`                        | map[string]interface{}             | no         | none                                         | Free-form field reserved for future scenario-specific options                                                                                                                                 |

`Validate()` is called immediately after parsing and rejects missing
required fields, non-positive counts, out-of-range thresholds, and an
unparseable `timeout` before the run ever touches the cluster.

---

## CLI reference

```
go run ./cmd/benchmark/main.go \
  --config <path>       (required) path to a scenario YAML config
  --output <path>       (default "results/result.json") where to write the JSON result
  --kubeconfig <path>   (default "~/.kube/config") kubeconfig to use
  --baseline <path>     (optional) baseline JSON to compare this run against
```

---

## Output reference

Every run writes structured JSON (`pkg/types.BenchmarkResult`) to `--output`,
plus a human-readable summary to stdout.

| JSON field                                          | Meaning                                                                                                                                                                                                                                                                                                                                                                              |
| --------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `name`                                              | Scenario name                                                                                                                                                                                                                                                                                                                                                                        |
| `runID`                                             | UUID identifying this run; also the label value used to tag every pod/node it creates                                                                                                                                                                                                                                                                                                |
| `timestamp`                                         | UTC timestamp the run completed (or aborted)                                                                                                                                                                                                                                                                                                                                         |
| `koordinatorVersion`                                | Short Git commit SHA used to build the benchmark binary                                                                                                                                                                                                                                                                                                                              |
| `nodeCount` / `podCount`                            | Echoed from the config                                                                                                                                                                                                                                                                                                                                                               |
| `throughputPodsPerSec`                              | Pods scheduled per second, measured over the actual scheduling window of the recorded pods (earliest `creationTimestamp` → latest `PodScheduled` transition among the admitted set). For `basic`/`gang` this window coincides with `totalDurationSec`. Note: this is bounded by `clientQPS`/`clientBurst`, not the scheduler's own ceiling — see `apiCreationDurationSec` to verify. |
| `apiCreationDurationSec`                            | Time to POST all `podCount` pods to the API server. When `throughputPodsPerSec ≈ clientQPS`, the client rate-limit rather than the scheduler is the bottleneck.                                                                                                                                                                                                                      |
| `totalDurationSec`                                  | Time from burst start until the settle condition was satisfied (all pods accounted for, or quiet period elapsed)                                                                                                                                                                                                                                                                     |
| `latencyP50Sec` / `latencyP90Sec` / `latencyP99Sec` | Per-pod scheduling latency percentiles (`creationTimestamp` → `PodScheduled` condition)                                                                                                                                                                                                                                                                                              |
| `thresholdBreached`                                 | `true` if `--baseline` was given and throughput/P99/failure-rate exceeded the configured thresholds                                                                                                                                                                                                                                                                                  |
| `timedOut`                                          | `true` if the run was aborted by `timeout` — treat other numeric fields as partial when this is `true`                                                                                                                                                                                                                                                                               |
| `schedulingFailureCount`                            | Total `FailedScheduling` events seen across all pods (a pod can retry and emit several)                                                                                                                                                                                                                                                                                              |
| `schedulingFailureRate`                             | Fraction (0.0-1.0) of pods that received at least one `FailedScheduling` event                                                                                                                                                                                                                                                                                                       |
| `gangCompletionP50Sec` / `gangCompletionP99Sec`     | Populated for the `gang` scenario (time for an entire PodGroup to complete admission); `null` for `basic`/`elasticquota`                                                                                                                                                                                                                                                             |
| `quotaBlockedPodCount`                              | Populated for `elasticquota` (distinct pods that received an `Insufficient quotas` event); `null` for scenarios with no quota configured                                                                                                                                                                                                                                             |
| `reservationBindCount`                              | Number of pods that successfully consumed a Reservation; `null` when the scenario does not configure Reservations                                                                                                                                                                                                                                                                    |
| `loadAwareRoutedPodCount`                           | Number of pods scheduled onto low-utilization nodes in the `loadaware` scenario; `null` when the scenario does not seed NodeMetrics                                                                                                                                                                                                                                                  |
| `createFailureCount`                                | Pods whose Create failed after retries and were excluded from the run rather than aborting it; usually 0                                                                                                                                                                                                                                                                             |
| `pprofCPUArtifact` / `pprofHeapArtifact`            | Reserved for future pprof capture; always empty for now                                                                                                                                                                                                                                                                                                                              |

---

## Troubleshooting

**`Error: --config is required`**
You need to pass `--config` (or, via `make`, set `CONFIG=` / `SCENARIO=`).

**`Failed to create engine: ... connection refused`**
Your kubeconfig isn't pointing at a live cluster. Run `make -C test/perf
setup` first, or check `kubectl get nodes` works with the kubeconfig you're
passing via `--kubeconfig` / `KUBECONFIG=`.

**Benchmark hangs and eventually reports `timedOut: true`**
Usually means `koord-scheduler` either isn't running in the target cluster,
or is running under a different name than `schedulerName` in your config
(default is `koord-scheduler`). Check `kubectl get pods -A | grep
scheduler` and confirm the scenario's `schedulerName` matches.

**All nodes/pods from a previous run are still in the cluster**
Benchmark objects are labeled `benchmark.koordinator.sh/run-id=<runID>`.
Clean up manually with:

```bash
kubectl delete nodes,pods -A -l benchmark.koordinator.sh/run-id --all-namespaces
```

Normally `Teardown`/`DeleteNodes` handle this automatically, even if the run
fails or times out — this is only needed if something more severe (e.g. a
killed process) interrupted cleanup itself.

**`make: \*** No rule to make target 'benchmark'`**
Make sure you're running `make`from`test/perf/`, or use `make -C
test/perf <target>` from the repo root.

## Architecture

The benchmark is an end-to-end test harness. It sends real Pod objects to a
live Kubernetes API server and measures the behavior of the Koordinator
scheduler. Nodes are simulated by kwok, so the test exercises scheduling and
API interactions without requiring real worker nodes or containers.

The main components are:

| Component               | Responsibility                                                                                 |
| ----------------------- | ---------------------------------------------------------------------------------------------- |
| `cmd/benchmark/main.go` | Parses CLI flags, loads YAML, and starts one run                                               |
| `pkg/framework`         | Creates nodes, runs the scenario, watches scheduling events, computes metrics, and writes JSON |
| `pkg/nodeprovider/kwok` | Creates and deletes simulated nodes                                                            |
| `pkg/scenarios`         | Defines the scenario contract and registry                                                     |
| `pkg/scenarios/<name>`  | Creates scenario-specific prerequisites and Pod objects                                        |
| `pkg/types`             | Defines configuration, validation, failure statistics, and result schemas                      |
| `configs/scenarios`     | Stores checked-in workload definitions                                                         |
| `baselines`             | Stores checked-in comparison results                                                           |
| `results`               | Stores local run output; ignored by Git                                                        |

For each run, the engine performs this sequence:

1. Validate the scenario configuration before contacting the cluster.
2. Check API-server connectivity and create the configured kwok nodes.
3. Run scenario setup, such as creating a namespace, ElasticQuota,
   Reservation, or NodeMetric objects.
4. Start watches for `PodScheduled` and `FailedScheduling` events.
5. Create the configured Pod burst with bounded concurrency and client-side
   QPS and burst limits.
6. Wait for the expected scheduled count and for the run to settle.
7. Calculate throughput, latency percentiles, failure statistics, and
   scenario-specific result fields.
8. Run scenario and node cleanup even when the run fails or times out.
9. Write a JSON report to the requested output path.

Objects created by a run carry the label
`benchmark.koordinator.sh/run-id=<runID>`. This makes interrupted-run cleanup
possible without selecting objects from another benchmark run.

## Available scenarios

The checked-in configurations are under `configs/scenarios/`:

| Scenario       | Scheduling path exercised                          | Scenario-specific signal                         |
| -------------- | -------------------------------------------------- | ------------------------------------------------ |
| `basic`        | Normal Koordinator scheduling and node bin-packing | Baseline throughput and latency                  |
| `gang`         | Coscheduling and PodGroup admission                | Gang completion latency                          |
| `elasticquota` | ElasticQuota enforcement                           | `quotaBlockedPodCount` must be greater than zero |
| `reservation`  | Reservation availability and binding               | `reservationBindCount`                           |
| `loadaware`    | LoadAware scoring using seeded NodeMetrics         | `loadAwareRoutedPodCount`                        |

The scenarios intentionally use different namespaces and prerequisites where
needed. The workflow drains namespaces between runs because terminating Pods
can continue to affect scheduler-cache accounting and distort the next
scenario's measurements.

## CI workflow

The GitHub Actions workflow is
[`scheduler-benchmark.yaml`](../../../.github/workflows/scheduler-benchmark.yaml).
It runs on manual dispatch and nightly at 03:00 UTC. It is not a required PR
check yet because kind, kwok, and Koordinator runtime behavior can be flaky on
shared GitHub-hosted runners.

The workflow:

1. Checks out the repository and installs the Go version declared by `go.mod`.
2. Frees unused runner disk space and installs the kind CLI.
3. Runs `make -C test/perf setup`, which creates the kind cluster, installs
   kwok, builds and deploys the scheduler image from the checked-out source,
   and scales down components not needed by the benchmark.
4. Runs `basic`, `gang`, `elasticquota`, `reservation`, and `loadaware` with
   their committed baselines.
5. Checks `thresholdBreached` and scenario-specific health signals.
6. Uploads all JSON results as the `benchmark-results` artifact.
7. Collects cluster diagnostics before teardown when a benchmark step fails.
8. Always runs `make -C test/perf teardown` last.

The regression step fails the job when any available result has
`thresholdBreached: true`, when no result was produced, or when a scenario's
own signal indicates that its plugin path was not exercised:

| Scenario       | CI requirement                  |
| -------------- | ------------------------------- |
| `elasticquota` | `quotaBlockedPodCount > 0`      |
| `reservation`  | `reservationBindCount >= 285`   |
| `loadaware`    | `loadAwareRoutedPodCount > 600` |

These checks complement, rather than replace, baseline comparison. A run can
have acceptable latency while still failing because the scenario silently
failed to exercise its intended scheduling path.

## Reading results and diagnosing failures

Start with the JSON artifact. `thresholdBreached` identifies a comparison
failure, while `timedOut` identifies an incomplete run whose numeric values
should be treated as partial. Compare `apiCreationDurationSec` with
`throughputPodsPerSec` before attributing low throughput to the scheduler: a
low client QPS or burst can be the limiting factor.

For local investigation, use the following commands:

```bash
kubectl get pods -A -o wide
kubectl get events -A --sort-by=.lastTimestamp
kubectl get events -A --field-selector=reason=FailedScheduling \
  --sort-by=.lastTimestamp
kubectl logs -n koordinator-system -l koord-app=koord-scheduler
kubectl get reservations -o wide
kubectl get nodemetrics
```

For a leftover run, delete only objects carrying the benchmark label:

```bash
kubectl delete nodes,pods -A \
  -l benchmark.koordinator.sh/run-id --all-namespaces
```

The workflow's `Debug on failure` step performs a broader version of this
inspection before the cluster is deleted. It includes kind and Docker state,
scheduler and koordlet logs, Pod descriptions, cluster events, failed
scheduling events, Reservations, NodeMetrics, and the scheduler ConfigMap.

Common causes include:

| Symptom                        | Check                                                                                                           |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------- |
| API connection refused         | Run `make -C test/perf setup` and verify `kubectl get nodes`                                                    |
| `timedOut: true`               | Confirm that `schedulerName` matches the deployed scheduler and inspect scheduler logs                          |
| Zero `quotaBlockedPodCount`    | Confirm that the settle phase ran and that the ElasticQuota limits are below total demand                       |
| Zero `reservationBindCount`    | Confirm the Reservation template uses `schedulerName: koord-scheduler` and that Reservations become `Available` |
| Low `loadAwareRoutedPodCount`  | Confirm every seeded NodeMetric has a non-nil `status.updateTime` and inspect NodeMetrics                       |
| Low throughput near client QPS | Increase `clientQPS` and `clientBurst` only after confirming the API server is not overloaded                   |

## Adding a scenario

Use `pkg/scenarios/basic` as the reference implementation. A scenario must:

1. Implement `Scenario` in a new package under `pkg/scenarios/<name>/`:
   `Name`, `Setup`, `Pods`, and `Teardown`.
2. Register itself with `scenarios.Register` from an `init` function.
3. Put all run-created objects under the run-ID label.
4. Add a blank import for the package in `cmd/benchmark/main.go`.
5. Add a YAML configuration under `configs/scenarios/`.
6. Add unit tests for configuration, setup, generated Pods, and cleanup.
7. Add a baseline only after the scenario produces stable measurements.
8. Add the scenario to the CI workflow, including any required drain and
   scenario-specific sanity check.

If the scenario needs a result field that the generic engine cannot derive,
implement `scenarios.ResultAugmenter`. This keeps scenario-specific metrics in
the scenario package while preserving the common result format.

Run the focused checks before submitting changes:

```bash
make -C test/perf test
make -C test/perf setup
make -C test/perf benchmark \
  CONFIG=configs/scenarios/basic-1k.yaml \
  BASELINE=baselines/basic-1k.json
make -C test/perf teardown
```

The final three commands require Docker, kind, kwok access, and enough local
resources for a kind cluster. Always tear down a locally created cluster,
including after a failed benchmark.

## Source of truth

The implementation is authoritative for behavior. These files define the
main contracts:

- `pkg/framework/engine.go`: run orchestration and report generation
- `pkg/types/types.go`: YAML configuration and JSON result schemas
- `pkg/scenarios/scenario.go`: scenario and result-augmentation interfaces
- `pkg/scenarios/*`: scenario-specific setup, Pod generation, metrics, and cleanup
- `hack/setup-cluster.sh`: local and CI cluster setup
- `.github/workflows/scheduler-benchmark.yaml`: CI ordering and failure policy

This README is the contributor and operator guide. The design rationale and
historical proposal remain in
[`docs/proposals/20260609-scheduler-scalability-test-harness.md`](../../../docs/proposals/20260609-scheduler-scalability-test-harness.md).
