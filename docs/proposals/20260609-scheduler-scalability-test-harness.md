---
title: End-to-End Performance and Scalability Test Harness for Koordinator Scheduler
authors:
  - "@Shivam-1827"
reviewers:
  - "@ZiMengSheng"
  - "@saintube"
creation-date: 2026-06-09
last-updated: 2026-06-18
status: provisional
---

# End-to-End Performance and Scalability Test Harness for Koordinator Scheduler

## Table of Contents

- [End-to-End Performance and Scalability Test Harness for Koordinator Scheduler](#end-to-end-performance-and-scalability-test-harness-for-koordinator-scheduler)
  - [Table of Contents](#table-of-contents)
  - [Glossary](#glossary)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals / Future Work](#non-goals--future-work)
  - [Proposal](#proposal)
    - [User Stories](#user-stories)
    - [Key Challenges](#key-challenges)
    - [Overall Workflow](#overall-workflow)
    - [Design](#design)
      - [WorkloadProfile — Declarative Benchmark Definition](#workloadprofile--declarative-benchmark-definition)
      - [SetupOrchestrator — Pre-Test Prerequisites](#setuporchestrator--pre-test-prerequisites)
      - [Concurrent Worker-Pool Generator](#concurrent-worker-pool-generator)
      - [Scheduling Watcher and Latency Measurement](#scheduling-watcher-and-latency-measurement)
      - [Gang Scheduling Simulation](#gang-scheduling-simulation)
      - [Structured JSON Output](#structured-json-output)
      - [CI Integration and API Server](#ci-integration-and-api-server)
      - [CI Regression Pipeline](#ci-regression-pipeline)
      - [Regression Detection](#regression-detection)
      - [pprof Integration](#pprof-integration)
    - [Proof of Concept Results](#proof-of-concept-results)
    - [Risks and Mitigations](#risks-and-mitigations)
  - [Alternatives](#alternatives)
  - [Implementation History](#implementation-history)

---

## Glossary

| Term | Definition |
|---|---|
| **kwok** | Kubernetes WithOut Kubelet. Simulates Kubernetes nodes at the API level without running a real kubelet or container runtime. The scheduler sees real node objects with real capacity and topology; only the downstream runtime is absent. See: https://github.com/kubernetes-sigs/kwok |
| **scheduler_perf** | The upstream Kubernetes benchmarking framework for vanilla kube-scheduler. Uses JSON workload templates and Go's `testing.B`. See: `kubernetes/kubernetes/test/integration/scheduler_perf` |
| **PodScheduled condition** | A pod status condition set to `True` by the scheduler when it successfully binds a pod to a node. The `LastTransitionTime` is set by the scheduler process (its own local clock) when it updates pod status after binding — not an API-server timestamp. |
| **PodGroup** | A Koordinator CRD (and upstream `scheduling.sigs.k8s.io` API) that defines a set of pods that must all be co-scheduled. Managed by the Coscheduling plugin. |
| **LSR / LS / BE** | Koordinator QoS classes. LSR (Latency-Sensitive Reserved): fully reserved CPU/memory, highest priority. LS (Latency-Sensitive): latency-sensitive without strict reservation. BE (Best-Effort): no resource requests, lowest priority. |
| **P50 / P90 / P99** | 50th, 90th, and 99th percentile latency values. P99 (tail latency) is the primary regression signal — it reflects worst-case pod placement time under scheduling queue pressure. |
| **WorkloadProfile** | The YAML schema proposed here as the single contributor extension point for defining new benchmark scenarios. |

---

## Summary

Koordinator's scheduler has grown significantly beyond vanilla kube-scheduler, adding plugins for gang scheduling (Coscheduling), QoS-aware resource placement (LSR/LS/BE), device sharing, workload Reservations, and ElasticQuota. Each of these plugins modifies the scheduling hot path in ways that can degrade throughput or increase tail latency under concurrent load — and currently there is no continuous, automated harness that measures these effects and catches regressions before they reach users.

This proposal describes a **kwok-based, end-to-end performance and scalability test harness** for the Koordinator scheduler. The harness:

- Simulates large Kubernetes clusters (up to 10,000 nodes locally, up to 5,000 nodes in CI) using kwok, removing kubelet and container runtime overhead from measurements entirely.
- Drives realistic, plugin-specific workload mixes through the live Koordinator scheduler using a concurrent bounded worker-pool generator controlled by declarative `WorkloadProfile` YAML files.
- Measures per-pod and per-gang scheduling latency (P50/P90/P99) using `PodScheduled` condition timestamps (API-server-written `CreationTimestamp` and scheduler-written `LastTransitionTime`) — avoids dependence on the benchmark client's clock.
- Produces structured JSON output per run for baseline storage and regression comparison.
- Integrates with GitHub Actions to run benchmarks automatically on release branches, failing CI when throughput drops or P99 latency exceeds configurable thresholds.
- Captures pprof CPU and heap profiles from the scheduler during every benchmark run so that when a regression is detected, the profiling data is immediately available for triage without needing to reproduce the run.

A working proof of concept is available at https://github.com/Shivam-1827/kwok-bench. It demonstrates 53–55 pods/sec scheduling throughput on a 100-node kwok cluster with P50=5s, P90=9s, P99=10s at 1,000 pods — including gang workload simulation with 100 gangs × 10 pods each.

---

## Motivation

### The Current Gap

The upstream Kubernetes project solves continuous scheduler benchmarking with `scheduler_perf`. It runs against vanilla kube-scheduler, covers basic pod creation workloads, and catches regressions in the core scheduling loop. Koordinator inherits parts of this infrastructure but has not extended it to cover its own plugin-specific scheduling patterns.

The consequence is a real operational gap: a Coscheduling plugin change that adds 200 microseconds of overhead per scheduling decision is invisible at 100 pods. At 10,000 pods under concurrent burst creation, that same overhead accumulates into a multi-second increase in P99 tail latency. Without a baseline and automated comparison on release branches, this kind of regression accumulates silently until it manifests as a production slowdown in GPU training or batch processing clusters.

The Koordinator 2026 roadmap milestone explicitly targets scalability validation for the scheduler. This proposal delivers the infrastructure that makes that validation continuous and automatic rather than a one-off manual exercise before each release.

### Why Koordinator's Plugins Specifically Need Their Own Workload Patterns

Koordinator's plugins introduce scheduling state and coordination that `scheduler_perf`'s vanilla workload models do not express:

- **Coscheduling** tracks per-gang satisfaction counts and coordinates all-or-nothing placement across potentially hundreds of pods simultaneously. This introduces per-group state that grows with concurrent gang count.
- **Reservation** adds a lookup step to every scheduling decision — before falling through to normal placement, the scheduler must check whether each incoming pod matches an available Reservation object.
- **QoS-aware scheduling** changes priority queue ordering and preemption logic based on which classes of pods are competing simultaneously. The interaction between LSR, LS, and BE pods under burst load produces different tail latency profiles than any single-class workload.

Each of these creates scheduling dynamics where a subtle implementation change — a new lock, a changed iteration order, an additional status update — can silently shift P99 tail latency. The only reliable way to catch this class of regression is to measure under realistic, plugin-specific workload mixes continuously.

---

### Goals

- Build a kwok-based scheduler benchmarking harness that runs against the Koordinator scheduler with its plugins active.
- Implement a `WorkloadProfile` YAML abstraction so contributors can add new benchmark scenarios without modifying harness code.
- Measure per-pod scheduling latency (P50/P90/P99) and throughput (pods/sec) per profile.
- Implement gang-level completion latency measurement for Coscheduling profiles.
- Support workload profiles for: gang scheduling (Coscheduling / PodGroup CRDs), QoS-class mixes (LSR/LS/BE), and Reservation scenarios.
- Integrate with GitHub Actions CI to run benchmarks on release branches and detect regressions automatically.
- Integrate pprof scraping so CPU and heap profiles are captured alongside benchmark results.
- Produce a final baseline benchmark report at 1k, 5k, and 10k pod counts.
- Write contributor documentation making it straightforward to add new workload profiles and run the harness locally.

---

### Non-Goals / Future Work

- **NUMA-aware workload profiles**: requires kwok node topology annotation setup that adds significant complexity beyond the core harness timeline. Scoped as post-mentorship.
- **ElasticQuota workload profiles**: same constraint — quota tree CRD setup is feasible but out of scope for the initial harness.
- **Device sharing / GPU simulation profiles**: requires mock DevicePlugin setup; post-mentorship.
- **Replacing scheduler_perf**: the harness complements scheduler_perf for Koordinator-specific workloads; vanilla scheduling baselines are still best measured with the upstream tool.
- **Real-node benchmarking**: the harness is explicitly kwok-based. Real infrastructure benchmarking is a separate concern.
- **Modifying the Koordinator scheduler itself**: the harness is a test tool only.

---

## Proposal

### User Stories

#### Story 1 — Scheduler contributor adding a new plugin

A contributor is writing a new scheduling plugin that modifies how pods are scored during placement. Before opening a PR, they want to know whether the plugin adds meaningful scheduling latency overhead.

They run `make bench PROFILE=mixed-qos-1k` locally. The output shows the plugin adds 120ms to P99 compared to the stored baseline — a 1.2% increase that is within the acceptable threshold. They include the benchmark output in their PR description. The CI pipeline confirms the same delta when reviewers run it.

#### Story 2 — Release manager validating a release branch

A release manager is preparing the Koordinator v1.6 release and needs to confirm there are no scheduling performance regressions compared to v1.5.

The GitHub Actions workflow runs automatically on the release branch, executes all workload profiles, compares against v1.5 baselines stored in `ci/baselines/v1.5/`, and posts a Markdown summary to the release tracking issue. All profiles pass within threshold. The release proceeds with confidence.

#### Story 3 — Developer investigating a reported production slowdown

A user reports that AI training jobs on their cluster are taking noticeably longer to acquire all pods after a recent Koordinator upgrade. A developer suspects the Coscheduling plugin's gang satisfaction path regressed.

The developer runs the gang scheduling profile against both Koordinator versions and compares the resulting JSON files. Gang completion P99 is 40% higher in the newer version. They open the pprof CPU profile captured during the benchmark run and identify the hot function in the PodGroup status update path. The regression is confirmed and localized in under 30 minutes — without needing to reproduce the issue in production.

#### Story 4 — Performance engineer profiling the scheduler under extreme load

A performance engineer wants to understand how the scheduler's internal queue behaves when 5,000 LSR pods burst simultaneously — a scenario that cannot easily be tested on a real cluster.

They run `make bench PROFILE=lsr-5k` locally with pprof enabled. The scheduler CPU profile shows lock contention in the priority queue drain path that does not appear at 1k pods. They file an issue with the profile attached as evidence.

---

### Key Challenges

#### Challenge 1 — Client-side API throttling drowns out the real signal

**Problem**: Standard client-go rate limits default to 5 QPS. Sequential pod creation at these limits produces about 5 pods/sec — which measures client throttling, not the scheduler. Conversely, spawning unbounded goroutines floods the API server and produces a different kind of noise.

**Proof from PoC**: Iteration 1 (sequential, default rate limits) produced 5 pods/sec. Iteration 2 (50-worker bounded pool, QPS=100, Burst=200) produced 53.55 pods/sec — a 10× improvement that came entirely from removing client-side bottlenecks. The scheduler was available to measure from iteration 2 onward.

**Solution**: A bounded worker-pool using a buffered channel as a counting semaphore. Pool size, QPS, and Burst are explicit parameters in the `WorkloadProfile` schema rather than hardcoded constants. The correct values are those that keep the scheduler's input queue saturated without making the API server the bottleneck — this is profiled per scale level during the Week 1 calibration phase.

#### Challenge 2 — Throughput alone is an incomplete regression signal

**Problem**: Average throughput (total pods / total seconds) can appear stable while the tail latency distribution degrades significantly. A scheduler that processes 900 pods in 10 seconds and the last 100 pods in 30 seconds shows similar average throughput to one that distributes load evenly — but the former's P99 is 3× worse. This is a meaningful regression for batch and AI workloads where all-pods-ready time determines job start.

**Proof from PoC**: At 1,000 pods, P50=5s and P99=10s. The P99/P50 ratio of 2.0 is not measurement noise — it reflects the scheduling queue filling faster than it drains during the burst creation phase. The first pods are scheduled quickly; the last pods wait in queue while the scheduler processes the backlog. This ratio is a more sensitive regression signal than throughput alone: if a plugin change causes queue drain to slow, P99 shifts before throughput visibly degrades.

**Solution**: Per-pod latency is collected for every pod in every run, sorted, and indexed at P50/P90/P99 positions. CI regression thresholds are defined independently on throughput drop and P99 increase, so either kind of degradation triggers a failure.

#### Challenge 3 — Plugin-specific workloads require different simulation patterns

**Problem**: Gang scheduling cannot be benchmarked meaningfully using plain pod creation. The Coscheduling plugin identifies a pod's gang via pod labels/annotations (the `pod-group.scheduling.sigs.k8s.io` label), but without creating the corresponding `PodGroup` CRD the benchmark will not exercise the real PodGroup-driven satisfaction logic (pending counts, all-or-nothing coordination, timeouts). The result looks like gang scheduling but measures something different.

Similarly, Reservation workloads need `Reservation` objects to exist before pods are created. QoS-class workloads need the correct Koordinator labels per pod class (e.g. `koordinator.sh/qosClass`), and each class affects the priority queue differently.

**Solution**: The `WorkloadProfile` schema captures all plugin-specific parameters. For gang workloads, the generator creates real `PodGroup` CRDs via the dynamic client before the pod burst begins. For reservation workloads, `Reservation` objects are created with matching owner selectors. For QoS profiles, per-class labels are injected deterministically during pod construction. Plugin-specific prerequisites are handled entirely inside the generator — contributors only write YAML.

#### Challenge 4 — CI infrastructure constraints limit pod counts

**Problem**: A 10,000-pod benchmark run involves cluster bootstrap (1–2 minutes), pod creation burst (8+ seconds), and scheduling completion (20+ seconds at current scale), plus JSON reporting and artifact upload. On GitHub Actions standard runners with limited CPU and memory, this risks hitting the job timeout and making CI flaky.

**Solution**: CI profiles are explicitly designed for the ≤5,000 pod range, which completes within a 15-minute job budget on standard runners. The same `WorkloadProfile` YAML supports both scales via different parameter values — contributors use `PROFILE=gang-ls-5k` in CI and `PROFILE=gang-ls-10k` locally. The 10,000-node characterization study is documented as a local-only run with self-hosted runner instructions for teams that want it in CI.

---

### Overall Workflow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                             HARNESS WORKFLOW                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  1. CONFIGURE                                                               │
│     WorkloadProfile YAML ──► Profile Parser ──► validated Go struct         │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  2. CLUSTER SETUP                          (self-contained or external)     │
│                                                                             │
│     Mode A  ──  CI / local                                                  │
│       hack/deploy_kind.sh bootstraps kind + Koordinator inside the job      │
│       Harness owns its own temporary API server                             │
│                                                                             │
│     Mode B  ──  External cluster                                            │
│       Harness connects via --kubeconfig to a pre-existing cluster           │
│       Koordinator already running with plugins registered                   │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  3. SCENARIO SETUP              (SetupOrchestrator — sequential + readiness)│
│                                                                             │
│     Step 1  createNodes              ──► wait: all nodes Ready              │
│     Step 2a createReservation        ──► wait: Reservation.Phase==Available │
│     Step 2b createElasticQuota       ──► wait: quota in scheduler cache     │
│     Step 2c createClusterNetTopo     ──► wait: topology nodes schedulable   │
│     Step 3  createPodGroups          ──► wait: PodGroups exist in API server│
│                                                                             │
│     ── Measurement window begins only after all steps reach readiness ──    │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  4. GENERATE                                                                │
│     Concurrent bounded worker pool fires pod burst                          │
│     API creation phase: ~8s for 1,000 pods at concurrency=50               │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  5. OBSERVE                                                                 │
│     client-go Watch stream filtered by benchmark run-ID label               │
│                                                                             │
│     Per-pod  latency  =  PodScheduled.LastTransitionTime                    │
│                        − pod.CreationTimestamp                              │
│                                                                             │
│     Per-gang latency  =  max(pod LastTransitionTime across gang)            │
│                        − min(pod CreationTimestamp across gang)             │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  6. COMPUTE                                                                 │
│     Sort latency slice ──► index at 50 / 90 / 99 percentile positions      │
│     Throughput = podCount / totalSchedulingDuration                         │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  7. EMIT                                                                    │
│     Structured JSON output file per run                                     │
│     pprof CPU + heap profiles scraped from scheduler via port-forward       │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  8. COMPARE                                        (CI pipeline only)       │
│     Load stored baseline ──► compute deltas ──► check thresholds           │
│     Generate Markdown report ──► post to PR + upload as artifact            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

### Design

#### WorkloadProfile — Declarative Benchmark Definition

Every benchmark scenario is defined by a single `WorkloadProfile` YAML file. This is the only file a contributor writes to add a new scenario — no harness code changes are needed.

```yaml
# profiles/gang-ls-1k.yaml
name: gang-ls-1000
description: "1k pod gang workload, LS QoS class, 100 gangs of 10 pods each"
nodeCount:    100
podCount:     1000
concurrency:  50
clientQPS:    100
clientBurst:  200
qosClass:     LS          # injects koordinator.sh/qosClass label
gangSize:     10          # 0 = no gangs; >0 triggers PodGroup CRD creation
minMember:    10          # PodGroup minMember (set < gangSize for partial satisfaction tests)
resourceRequests:
  cpu:    "2"
  memory: "4Gi"
labels:
  koordinator.sh/priority: "100"
thresholds:
  throughputDropPct: 10   # CI failure if throughput drops  > 10% vs baseline
  p99IncreasePct:    20   # CI failure if P99 latency rises > 20% vs baseline
```

The corresponding Go struct:

```go
type WorkloadProfile struct {
    Name             string               `yaml:"name"`
    Description      string               `yaml:"description"`
    NodeCount        int                  `yaml:"nodeCount"`
    PodCount         int                  `yaml:"podCount"`
    Concurrency      int                  `yaml:"concurrency"`
    ClientQPS        float32              `yaml:"clientQPS"`
    ClientBurst      int                  `yaml:"clientBurst"`
    QoSClass         string               `yaml:"qosClass"`         // LSR | LS | BE
    GangSize         int                  `yaml:"gangSize"`
    MinMember        int                  `yaml:"minMember"`
    ResourceRequests map[string]string    `yaml:"resourceRequests"`
    Labels           map[string]string    `yaml:"labels"`
    Annotations      map[string]string    `yaml:"annotations"`
    Thresholds       RegressionThresholds `yaml:"thresholds"`
    Setup            []SetupStepConfig    `yaml:"setup"`
}

type RegressionThresholds struct {
    ThroughputDropPct float64 `yaml:"throughputDropPct"`
    P99IncreasePct    float64 `yaml:"p99IncreasePct"`
}
```

`QoSClass` and `GangSize` are higher-level knobs used to derive scheduling labels during pod construction (e.g. `koordinator.sh/qosClass` and `pod-group.scheduling.sigs.k8s.io`). `Labels` carries additional scheduling-relevant keys (for example `koordinator.sh/priority`) that should be applied verbatim as pod labels. `Annotations` remains available for Koordinator features that are genuinely annotation-driven, such as `gang.scheduling.koordinator.sh/groups` and the network-topology-spec annotation on `PodGroup` objects.

**Initial set of profiles shipped with the harness:**

| Profile File           | Description                           | Key Plugin       |
|------------------------|---------------------------------------|------------------|
| `baseline-1k.yaml`     | 1,000 plain LS pods, no gang          | Vanilla          |
| `gang-ls-1k.yaml`      | 100 gangs × 10 LS pods                | Coscheduling     |
| `gang-ls-5k.yaml`      | 500 gangs × 10 LS pods  (CI scale)    | Coscheduling     |
| `mixed-qos-1k.yaml`    | 200 LSR + 500 LS + 300 BE pods        | QoS scheduling   |
| `reservation-1k.yaml`  | 1,000 pods bound via Reservations     | Reservation      |
| `lsr-1k.yaml`          | 1,000 fully-reserved LSR pods         | NodeResourcesFit |

---

#### SetupOrchestrator — Pre-Test Prerequisites

For simple profiles (plain pod bursts), setup means creating kwok nodes. For Reservation, Quota, and NetworkTopology scenarios, setup is a multi-step sequence where each step has a readiness condition that must be verified before the next step begins. Running the pod burst against a partially-initialized cluster would produce meaningless latency numbers — the scheduler might not yet have the Reservation objects in its informer cache, or the ElasticQuota tree might not be reconciled yet.

The `WorkloadProfile` YAML has an optional `setup` section listing an ordered sequence of `SetupStep` entries. The `SetupOrchestrator` executes these steps sequentially, waiting for the specified readiness condition before advancing. **The measurement window starts only after all steps report ready.**

**SetupStep schema (Reservation example):**

```yaml
# Extended profile — Reservation scenario
name:        reservation-1k
description: "1k pods scheduled via pre-created Reservations"
nodeCount:   100
podCount:    1000
concurrency: 50
clientQPS:   100
clientBurst: 200
setup:
  - type:        createNodes
    count:       100
    readiness:   allNodesReady          # all node objects have Ready=True

  - type:        createReservation
    manifestDir: profiles/fixtures/reservation-1k/
    readiness:   reservationAvailable   # Reservation.Status.Phase == Available
    timeoutSec:  120

  - type:        createElasticQuota
    manifestDir: profiles/fixtures/quota-tree/
    readiness:   quotaInformerSynced    # ElasticQuota visible in scheduler cache
    timeoutSec:  60

  - type:        createClusterNetworkTopology
    manifestDir: profiles/fixtures/network-topology/
    readiness:   nodesWithTopologyLabels
    timeoutSec:  60
```

**SetupStep types and readiness conditions:**

| `type`                        | What it creates                                    | Readiness condition        | How it is verified                                                       |
|-------------------------------|----------------------------------------------------|----------------------------|--------------------------------------------------------------------------|
| `createNodes`                 | kwok node objects                                  | `allNodesReady`            | All node objects have `Ready=True` condition in the API server           |
| `createPodGroups`             | PodGroup CRDs                                      | `podGroupsExist`           | All PodGroup objects returned by List call                               |
| `createReservation`           | Reservation CRDs                                   | `reservationAvailable`     | `Reservation.Status.Phase == Available` for all created Reservations     |
| `createElasticQuota`          | ElasticQuota tree CRDs                             | `quotaInformerSynced`      | Short dry-run probe pod scheduled under quota namespace succeeds         |
| `createClusterNetworkTopology`| ClusterNetworkTopology CR + topology-labeled nodes | `nodesWithTopologyLabels`  | All nodes carry expected topology labels and are schedulable             |

**Go interface:**

```go
// SetupStep is the interface all setup step types implement.
type SetupStep interface {
    // Execute creates the prerequisite objects.
    Execute(ctx context.Context, client kubernetes.Interface, dynClient dynamic.Interface) error
    // Ready returns true once the readiness condition is met.
    Ready(ctx context.Context, client kubernetes.Interface, dynClient dynamic.Interface) (bool, error)
    // Timeout is how long to poll Ready() before failing the run.
    Timeout() time.Duration
}

// SetupOrchestrator runs steps sequentially, polling Ready() until
// each step reports true or its timeout fires.
type SetupOrchestrator struct {
    Steps []SetupStep
}

func (o *SetupOrchestrator) Run(ctx context.Context) error {
    for _, step := range o.Steps {
        if err := step.Execute(ctx, ...); err != nil {
            return fmt.Errorf("setup step %T failed: %w", step, err)
        }
        deadline := time.Now().Add(step.Timeout())
        for time.Now().Before(deadline) {
            ok, err := step.Ready(ctx, ...)
            if err != nil {
                return err
            }
            if ok {
                break
            }
            time.Sleep(2 * time.Second)
        }
    }
    return nil // all steps ready — measurement window begins
}
```

**Cleanup:**

After the benchmark run completes (or fails), the `SetupOrchestrator` runs cleanup in reverse step order — deleting Reservations, ElasticQuota objects, ClusterNetworkTopology CRs, and kwok nodes. All objects created by the orchestrator carry the run-ID label (`benchmark.koordinator.sh/run-id`) so cleanup is selective by label selector and never touches unrelated cluster objects.

---

#### Concurrent Worker-Pool Generator

The generator reads a validated `WorkloadProfile` and fires pods using a bounded worker pool. A buffered channel of size `profile.Concurrency` acts as a counting semaphore — the same pattern proven in the PoC. This prevents unbounded goroutine creation while maintaining sustained API pressure.

For gang workloads (`GangSize > 0`), `PodGroup` CRDs are created via the dynamic client before the pod burst begins. Each pod is deterministically assigned to a gang: `gangID = podIdx / profile.GangSize`.

Every benchmark run gets a unique UUID injected as a label (`benchmark.koordinator.sh/run-id`) into all pods and PodGroups. This makes cleanup deterministic and ensures residual objects from a crashed run cannot contaminate subsequent measurements.

```go
// Core worker pool — actual implementation also handles PodGroup
// pre-creation, QoS label injection, and per-pod error collection.
semaphore := make(chan struct{}, profile.Concurrency)
var wg sync.WaitGroup

for i := 0; i < profile.PodCount; i++ {
    wg.Add(1)
    semaphore <- struct{}{}
    go func(podIdx int) {
        defer wg.Done()
        defer func() { <-semaphore }()
        pod := buildPod(podIdx, profile, runID)
        clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
    }(i)
}
wg.Wait()
```

---

#### Scheduling Watcher and Latency Measurement

The production harness uses a client-go `Watch` stream filtered by the benchmark run ID label. The PoC used a polling loop for simplicity; the full harness switches to Watch to reduce overhead at 10,000+ pod counts where polling becomes expensive.

**Per-pod scheduling latency:**

```
latency(pod) = PodScheduled.LastTransitionTime − pod.CreationTimestamp
```

Both timestamps are written by Kubernetes components rather than the benchmark client, but they come from different clocks:

- `CreationTimestamp` — set by the **API server** (its own clock) when the pod write is accepted into etcd.
- `PodScheduled.LastTransitionTime` — set by the **scheduler process** (its own local clock) when it completes binding and updates pod status.

No benchmark client clock is involved. The measurement removes the most common noise source in client-driven benchmarks. It still assumes the API server and scheduler clocks are reasonably in sync — it is not immune to API-server-vs-scheduler clock skew, but in practice both run with NTP-synced clocks in the same cluster.

One methodological note: `CreationTimestamp` precedes the scheduler seeing the pod by the informer cache propagation delay. At high creation rates, this adds a systematic floor to measured latency — pods sit in etcd before the scheduler's informer cache delivers them to the scheduling queue. The harness documents this floor and includes an optional warmup phase to measure and subtract it for analytical runs where absolute latency values matter more than relative regression deltas.

---

#### Gang Scheduling Simulation

The PoC's label injection approach (e.g. `pod-group.scheduling.sigs.k8s.io: <podGroupName>`) creates realistic creation pressure but does not trigger the Coscheduling plugin's full gang satisfaction logic unless the corresponding `PodGroup` CRD is also created.

The full harness creates real `PodGroup` objects before the pod burst:

```go
podGroup := &unstructured.Unstructured{
    Object: map[string]interface{}{
        "apiVersion": "scheduling.sigs.k8s.io/v1alpha1",
        "kind":       "PodGroup",
        "metadata": map[string]interface{}{
            "name":      gangName,
            "namespace": namespace,
        },
        "spec": map[string]interface{}{
            "minMember":              profile.MinMember,
            "scheduleTimeoutSeconds": 300,
        },
    },
}
dynamicClient.Resource(podGroupGVR).Namespace(namespace).Create(ctx, podGroup, metav1.CreateOptions{})
```

For gang workloads the watcher tracks two distinct latency signals:

- **Per-pod latency** — same as non-gang measurement: time for each individual pod to be bound.
- **Gang completion latency** — `max(LastTransitionTime across gang) − min(CreationTimestamp across gang)`: the total time a workload waits before all its constituent pods have placement. This is the operationally meaningful metric for AI training jobs.

Gang completion P99 can degrade even when per-pod P99 looks acceptable — for example, if one pod in each gang consistently waits longer than others due to resource contention on specific nodes. Tracking both signals is necessary for complete regression coverage.

**Gang stress scenarios:**

| Scenario             | Config                               | Purpose                                       |
|----------------------|--------------------------------------|-----------------------------------------------|
| Standard             | 100 gangs × 10 pods, minMember=10    | Baseline throughput and latency               |
| Large gangs          | 10 gangs × 100 pods, minMember=100   | Gang satisfaction under resource pressure     |
| Partial satisfaction | 50 gangs × 20 pods,  minMember=16    | Coscheduling's partial satisfaction path      |

---

#### Structured JSON Output

After each run the harness writes a JSON file to `results/<profileName>-<runID>.json`:

```json
{
  "profile":                "gang-ls-1000",
  "runID":                  "a3f9c2e1-7b4d-4e8a-9c1f-2d3e4f5a6b7c",
  "timestamp":              "2026-06-09T10:30:00Z",
  "koordinatorVersion":     "v1.5.0",
  "nodeCount":              100,
  "podCount":               1000,
  "throughputPodsPerSec":   54.88,
  "apiCreationDurationSec": 8.22,
  "totalDurationSec":       18.22,
  "latencyP50Sec":          5.0,
  "latencyP90Sec":          9.0,
  "latencyP99Sec":          10.0,
  "gangCompletionP50Sec":   6.0,
  "gangCompletionP99Sec":   11.0,
  "pprofCPUArtifact":       "results/a3f9c2e1-cpu.pprof",
  "pprofHeapArtifact":      "results/a3f9c2e1-heap.pprof"
}
```

Baseline files are versioned in the repository at `ci/baselines/<koordinatorVersion>/<profileName>.json` so comparison is always against a specific known-good state rather than an implicit global baseline.

---

#### CI Integration and API Server

The harness supports two deployment modes. The active mode is selected at runtime by the `--kubeconfig` flag — the benchmark code is identical in both cases.

**Mode A — Self-contained (default for CI and local runs)**

The CI job bootstraps its own kind cluster and API server using `./hack/deploy_kind.sh`, installs the Koordinator scheduler and manager, provisions kwok nodes via the SetupOrchestrator, runs the benchmark, and tears down the cluster when done. Nothing external is needed.

```
┌────────────────────────────────────────────────┐
│               GitHub Actions Job               │
│                                                │
│  kind create cluster                           │
│    └─► API server created                      │
│                                                │
│  hack/deploy_kind.sh                           │
│    └─► Koordinator scheduler + manager         │
│                                                │
│  SetupOrchestrator                             │
│    └─► kwok nodes + scenario prerequisites     │
│                                                │
│  kwok-bench run-all                            │
│    └─► benchmark loop                          │
│    └─► JSON output + pprof artifacts           │
│                                                │
│  kind delete cluster   (if: always)            │
└────────────────────────────────────────────────┘
```

**Mode B — External API server**

If the project maintains a dedicated benchmark cluster (or has an existing CI environment), the harness connects to it via `--kubeconfig`. The harness assumes on entry:

- The Koordinator scheduler and manager are already deployed with all plugins registered.
- The relevant CRDs (`PodGroup`, `Reservation`, `ElasticQuota`, `ClusterNetworkTopology`) are registered.
- No residual benchmark pods or PodGroups from prior runs exist (or run `kwok-bench cleanup` first).

```bash
kwok-bench run-all \
  --kubeconfig /path/to/kubeconfig \
  --profiles   profiles/ci/ \
  --output     results/
```

**RBAC requirements (both modes):**

```yaml
rules:
  - apiGroups: [""]
    resources: ["pods", "nodes"]
    verbs:     ["create", "delete", "get", "list", "watch"]
  - apiGroups: ["scheduling.sigs.k8s.io"]
    resources: ["podgroups", "elasticquotas"]
    verbs:     ["create", "delete", "get", "list", "watch"]
  - apiGroups: ["scheduling.koordinator.sh"]
    resources: ["reservations", "clusternetworktopologies"]
    verbs:     ["create", "delete", "get", "list", "watch"]
```

> **Note:** The right integration mode will be confirmed with mentors during Week 1 before any CI wiring is implemented. If Koordinator already has an e2e test cluster or a standard pattern for providing an API server to test jobs, the harness will slot into that via Mode B.

---

#### CI Regression Pipeline

The GitHub Actions workflow runs on two triggers:

1. **Nightly schedule** — catches regressions from commits accumulated over recent days.
2. **Push to release branches** (`release-*`) — catches regressions before a release tag is cut.

It does not run on every PR — cluster bootstrap and benchmark execution take 10–15 minutes, which is too slow for standard code review cycles.

```yaml
# .github/workflows/scheduler-bench.yaml
# Mode A: self-contained — bootstraps its own kind cluster per run

on:
  schedule:
    - cron: "0 2 * * *"
  push:
    branches: ["release-*"]

jobs:
  scheduler-bench:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Create kind cluster and install Koordinator
        run: |
          kind create cluster --name koordinator-bench
          export MANAGER_IMG=ghcr.io/koordinator-sh/koord-manager:${{ env.KOORDINATOR_VERSION }}
          export SCHEDULER_IMG=ghcr.io/koordinator-sh/koord-scheduler:${{ env.KOORDINATOR_VERSION }}
          ./hack/deploy_kind.sh
          # KUBECONFIG now points at the kind cluster created above

      - name: Run all CI-scale profiles
        # SetupOrchestrator handles node + scenario prerequisite creation internally
        run: go run ./cmd/kwok-bench run-all --profiles profiles/ci/ --output results/

      - name: Compare against baseline and report
        run: |
          go run ./cmd/kwok-bench compare               \
            --results        results/                   \
            --baseline       ci/baselines/${{ env.KOORDINATOR_VERSION }}/ \
            --threshold-config ci/thresholds.yaml       \
            --format         markdown >> $GITHUB_STEP_SUMMARY

      - name: Upload results and pprof profiles
        uses: actions/upload-artifact@v4
        with:
          name:            bench-${{ github.sha }}
          path:            results/
          retention-days:  30

      - name: Cleanup kind cluster
        if:  always()
        run: kind delete cluster --name koordinator-bench
```

---

#### Regression Detection

Thresholds are configured in `ci/thresholds.yaml`:

```yaml
defaults:
  throughputDropPct: 10
  p99IncreasePct:    20

overrides:
  gang-ls-1000:
    p99IncreasePct: 30   # gang profiles have higher natural P99 variance
```

When a threshold is breached, the workflow fails with a structured message identifying the profile, the metric, the baseline value, the current value, and the percentage delta. This message is also posted as a GitHub comment on any open PR touching the `pkg/scheduler/` path, so the regression is visible to the author without requiring log inspection.

Baselines are updated by a separate `update-baselines` job that runs on successful main-branch benchmark runs and opens a PR with the updated JSON files for maintainer review before merging.

---

#### pprof Integration

The harness accesses the Koordinator scheduler's pprof endpoint via `kubectl port-forward`, since the scheduler runs in-cluster (not on the harness process's localhost):

```bash
kubectl -n koordinator-system port-forward deploy/koord-scheduler 10251:10251
```

The harness then scrapes `http://127.0.0.1:10251/debug/pprof/{profile,heap}` over the forwarded port:

- **CPU profile** — port-forward established and CPU profiling started immediately before the pod burst begins; stopped after the scheduling watcher reports all pods scheduled.
- **Heap profile** — downloaded after the burst completes to capture peak memory state during scheduling.

> **Note:** Port 10251 is currently the scheduler's insecure pprof/metrics port in Koordinator's manifests. If this port is removed or secured in a future release, the harness will need to switch to the secure metrics port with appropriate authentication. This is called out as a dependency to confirm with mentors during Week 1.

Both profile files are saved alongside the JSON output and uploaded as CI artifacts. The Markdown regression report links directly to the pprof artifacts from the failing run, so the engineer investigating the regression can open the profile in `go tool pprof` immediately without needing to reproduce the run.

---

### Proof of Concept Results

A working PoC is available at https://github.com/Shivam-1827/kwok-bench. It validates the core measurement approach across four iterations on a local kind cluster with 100 kwok nodes:

| Iteration | Description                                       | Throughput        | P50 / P90 / P99 |
|-----------|---------------------------------------------------|-------------------|-----------------|
| 1         | Sequential baseline, default client-go rate limits| 5.00 pods/sec     | N/A             |
| 2         | Concurrent, 50-worker bounded pool, QPS=100/B=200 | 53.55 pods/sec    | N/A             |
| 3         | Per-pod latency tracking via PodScheduled         | 53.04 pods/sec    | 5s / 9s / 10s   |
| 4         | Gang workload simulation (100 gangs × 10 pods)    | 54.88 pods/sec    | 5s / — / 10s    |

**Key observations:**

1. **Client-side throttling completely swamps the measurement** without explicit rate limit tuning. The 10× throughput jump from Iteration 1 to Iteration 2 comes entirely from removing client-side bottlenecks, not from any scheduler change.

2. **The P99/P50 ratio of 2.0 at 1,000 pods reflects scheduling queue dynamics**, not noise. The creation burst fills the queue faster than the scheduler drains it; the last pods wait proportionally longer. This ratio is the primary regression signal the harness tracks.

3. **Gang label injection does not degrade throughput.** The 100-gang workload shows the same throughput and latency as plain pod creation (54.88 vs 53.04 pods/sec). This is expected since the Coscheduling plugin requires `PodGroup` CRDs, not labels. The full harness targets real CRD creation to exercise actual plugin logic.

4. **API creation phase and scheduling phase are separable.** The API burst completes in ~8 seconds; scheduling completes at ~18 seconds. These have different bottlenecks. The harness reports both phase durations separately so contributors can distinguish API server pressure from scheduler throughput limits.

---

### Risks and Mitigations

| Risk                                                               | Likelihood | Impact | Mitigation                                                                                                                    |
|--------------------------------------------------------------------|------------|--------|-------------------------------------------------------------------------------------------------------------------------------|
| kwok simulation diverges from real scheduler behaviour at scale    | Medium     | Medium | Cross-validate a subset of profiles against a real kind cluster with real nodes; document measured divergence in contributor guide |
| GitHub Actions runners cannot complete 10k-pod runs within timeout | High       | Low    | CI profiles explicitly capped at 5k pods by design; 10k+ documented as local-only                                            |
| Koordinator plugin APIs change mid-implementation                  | Low        | High   | Harness pinned to specific Koordinator release tag; adapter pattern isolates API surface; weekly upstream PR tracking         |
| PodGroup CRD dynamic client integration harder than expected       | Medium     | Medium | Label-based simulation already works as verified fallback; CRD upgrade is Week 5 and not on the critical path                |
| Informer cache propagation lag adds systematic measurement floor   | Medium     | Low    | Documented in methodology; warmup phase isolates it; subtracted for absolute latency analysis                                 |
| scheduler_perf compatibility adapter not feasible                  | Medium     | Low    | Adapter is a stretch goal; core harness is fully independent of scheduler_perf                                                |

---

## Alternatives

### Alternative 1 — Extend scheduler_perf Directly

Rather than building a new harness, extend the existing `scheduler_perf` framework with Koordinator-specific workload templates.

**Reason not chosen**: `scheduler_perf` runs against vanilla kube-scheduler internals using `testing.B`. Running it against a separately deployed Koordinator scheduler requires either forking the framework or maintaining a parallel integration test binary. More critically, `scheduler_perf`'s `OpTemplate` workload model does not natively express `PodGroup` CRD creation, QoS-class labels, or Reservation prerequisites — adding these would require either patching the upstream framework or wrapping it with enough custom code that a new harness is cleaner.

The harness includes a compatibility adapter (Week 8) that translates `WorkloadProfile` YAML to scheduler_perf format for vanilla baseline comparison — this enables side-by-side comparison without the maintenance cost of forking scheduler_perf.

### Alternative 2 — Generic HTTP Load Testing Tools (k6, Vegeta)

Use an existing load testing tool pointed at the Kubernetes API server.

**Reason not chosen**: Generic load testers cannot express Kubernetes-native scheduling semantics (`PodGroup` CRD creation before pods, QoS label injection, Reservation affinity matching) and cannot consume `PodScheduled` condition events for accurate latency measurement. The amount of Kubernetes-specific logic needed on top of a generic tool would recreate most of what this harness builds anyway, without the type safety or reusability of the `WorkloadProfile` abstraction.

### Alternative 3 — Kubemark

Kubemark simulates hollow nodes and is used by the SIG-scalability team for cluster-wide scalability testing.

**Reason not chosen**: Kubemark runs hollow kubelets, which adds significant process management overhead to the simulation environment and makes the setup substantially more complex than kwok. For the specific goal of isolating and measuring scheduler latency, kwok's approach — simulating only the node API objects that the scheduler reads, without any kubelet process — is both more appropriate and more accessible for CI environments where resource overhead matters.

---

## Implementation History

- [x] 2026-05-01: Proof of concept started — sequential baseline established at 5 pods/sec
- [x] 2026-05-10: Concurrent worker-pool generation implemented; throughput baseline 53 pods/sec on 100 kwok nodes
- [x] 2026-05-15: Percentile latency tracking implemented; P50=5s, P90=9s, P99=10s at 1,000 pods
- [x] 2026-05-19: Gang workload simulation implemented (100 gangs × 10 pods)
- [x] 2026-06-09: Proposal submitted as PR to docs/proposals/
- [x] 2026-06-09: Addressed Copilot review — PodScheduled timestamp accuracy, CI deployment mechanism, insecure-port caveat, pprof access via port-forward, label vs annotation corrections
- [x] 2026-06-17: Addressed maintainer review — added SetupOrchestrator design with per-step readiness conditions (Q3), added CI Mode A/B with RBAC spec (Q1+Q2), expanded workflow diagram to show setup and cluster-setup stages
- [x] 2026-06-18: Alignment and formatting pass — tables, workflow diagram, code blocks, notes, and section separators