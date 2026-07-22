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
- [kwok](https://github.com/kubernetes-sigs/kwok)

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

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | yes | — | Scenario name; must match a registered scenario (currently only `basic`) |
| `description` | string | no | — | Free-text note, not used by the engine |
| `schedulerName` | string | no | `koord-scheduler` | Scheduler that processes the benchmark pods |
| `namespace` | string | no | `benchmark` | Namespace pods and (for `basic`) the namespace itself are created in |
| `nodeCount` | int | yes (>0) | — | Number of simulated kwok nodes to create |
| `podCount` | int | yes (>0) | — | Number of pods fired in the burst |
| `concurrency` | int | yes (>0) | — | Max in-flight pod-create requests at once |
| `clientQPS` | float | yes (>0) | — | client-go QPS for the k8s client used by the engine |
| `clientBurst` | int | yes (>0) | — | client-go burst for the same client |
| `qosClass` | string | no | — | If set, applied as a `koordinator.sh/qosClass` pod label |
| `resourceRequests` | map[string]string | no | none | e.g. `{cpu: "100m", memory: "128Mi"}` — applied as both requests and limits |
| `labels` | map[string]string | no | none | Extra labels merged onto every benchmark pod |
| `annotations` | map[string]string | no | none | Extra annotations applied to every benchmark pod |
| `nodeTemplateFile` | string | no | built-in default (32 CPU / 256Gi / 110 pods) | Path to a full `corev1.Node` YAML to base simulated nodes on |
| `nodeCreationWorkers` | int | no (>=0) | 20 | Parallelism for node creation |
| `timeout` | string (Go duration, e.g. `"10m"`) | no | `10m` | Hard ceiling on total run time; the run is aborted and reported with `timedOut: true` if exceeded |
| `thresholds.throughputDropPct` | float | no (0-100) | 0 | Max allowed throughput drop vs baseline, as a percentage |
| `thresholds.p99IncreasePct` | float | no (>=0) | 0 | Max allowed P99 latency increase vs baseline, as a percentage |
| `gangSize` / `minMember` | int | no | 0 | Reserved for the Phase 2 gang-scheduling scenario; unused by `basic` |
| `extra` | map[string]interface{} | no | none | Free-form field reserved for future scenario-specific options |

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

| JSON field | Meaning |
|---|---|
| `name` | Scenario name |
| `runID` | UUID identifying this run; also the label value used to tag every pod/node it creates |
| `timestamp` | UTC timestamp the run completed (or aborted) |
| `koordinatorVersion` | Git tag or short commit SHA the binary was built from (`-dirty` suffix if uncommitted changes were present) |
| `nodeCount` / `podCount` | Echoed from the config |
| `throughputPodsPerSec` | `podCount / totalDurationSec` |
| `apiCreationDurationSec` | Time to POST all pods to the API server |
| `totalDurationSec` | Time from burst start until every pod was observed scheduled |
| `latencyP50Sec` / `latencyP90Sec` / `latencyP99Sec` | Pod scheduling latency percentiles (pod creation timestamp → `PodScheduled` condition) |
| `thresholdBreached` | `true` if `--baseline` was given and throughput/P99 exceeded the configured thresholds |
| `timedOut` | `true` if the run was aborted by `timeout` — treat other numeric fields as partial, not a completed run, when this is `true` |
| `schedulingFailureCount` | Total `FailedScheduling` events seen across all pods (a pod can retry and emit several) |
| `schedulingFailureRate` | Fraction (0.0-1.0) of pods that received at least one `FailedScheduling` event |
| `gangCompletionP50Sec` / `gangCompletionP99Sec` | Reserved for the Phase 2 gang-scheduling scenario; always `null` for `basic` |
| `pprofCPUArtifact` / `pprofHeapArtifact` | Reserved for future pprof capture; always empty for now |

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

**`make: *** No rule to make target 'benchmark'`**
Make sure you're running `make` from `test/perf/`, or use `make -C
test/perf <target>` from the repo root.

---

## Adding a new scenario

Every scenario implements the `Scenario` interface in
`pkg/scenarios/scenario.go` (`Name`, `Setup`, `Pods`, `Teardown`) and
self-registers via an `init()` call to `scenarios.Register(...)`. Use
`pkg/scenarios/basic/basic.go` as the reference implementation — it covers
namespace handling, resource-quantity parsing, and pod-labeling patterns
that any new scenario should follow. Once written, add a blank import for
your new package in `cmd/benchmark/main.go` and a matching config under
`configs/scenarios/`.
