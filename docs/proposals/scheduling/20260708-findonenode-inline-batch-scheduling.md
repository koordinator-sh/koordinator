# FindOneNode: Whole-Job Placement Plan and Inline Batch Scheduling

## Summary

This proposal introduces a new scheduling extension point, **`FindOneNode`**, that lets a plugin
compute a deterministic placement plan for a **whole job** (all member pods and their target
nodes) in one shot, and a new **inline batch scheduler** that schedules and binds all of those
pods together within the triggering pod's scheduling cycle.

Previously the scheduler could only decide one node for one pod at a time. For gang/topology
workloads this is inefficient and can be incorrect: the topology algorithm already knows the best
placement for the entire `GangGroup`, but the framework forced it to be replayed pod-by-pod.
`FindOneNode` returns the full plan up front, and the inline batch scheduler
reserves/assumes/binds every member pod according to that plan, with per-pod binding running
asynchronously.

This is gated behind feature gates and defaults to the legacy single-node behavior, so it is
opt-in and safe.

## Motivation

- The network-topology / gang algorithm computes a placement for all pods of a job at once and
  caches it (`gangSchedulingContext.networkTopologyPlannedNodes`), but the standard
  `PreFilter → Filter → Reserve` flow only consumes one node for the current pod. The plan is not
  recomputed for siblings — each sibling's `PreFilter` just looks up its cached planned node — but
  every sibling must still be popped from the queue and run its own full scheduling cycle
  (`Filter → Reserve → Bind`) across separate cycles. Re-validating the cached plan against a
  snapshot that changes between those cycles is slow and can let the realized placement diverge
  from the original plan.
- We want a single, authoritative "schedule the whole job now" path that reuses a shared whole-job
  scheduling engine, keeps binding asynchronous (so the scheduling cycle stays fast), and
  never regresses the default single-pod behavior when disabled.

### Goals

- A `FindOneNode` extension point that returns a whole-job placement plan (`BatchScheduleResult`).
- An inline batch scheduler that, on a successful plan, runs PreFilter/Filter/Reserve/Assume for
  every member pod and then binds them asynchronously (one goroutine per pod).
- Correct interaction with gang `Permit`/`WaitOnPermit`, preemption, and the scheduling queue
  (only the triggering pod is dequeued; siblings stay queued and are skipped once assumed).
- Feature-gated and off by default; when off, behavior is identical to today.

### Non-Goals

- Changing the topology/gang placement algorithm itself (the plugin still computes the plan).
- Decoupling package dependencies (tracked separately).
- Synchronous binding of the whole job inside the scheduling cycle.

## Proposal

### New extension point: `FindOneNode`

`pkg/scheduler/frameworkext/interface.go`:

```go
// FindOneNodePlugin computes a placement plan for a whole job.
type FindOneNodePlugin interface {
    framework.Plugin
    // On Success: returns a BatchScheduleResult (plan for all member pods incl. the trigger pod).
    // On Skip:    the plugin does not intervene; fall back to standard multi-node filtering.
    FindOneNode(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod,
        result *framework.PreFilterResult) (*BatchScheduleResult, *framework.Status)
}

// BatchScheduleResult is the whole-job placement plan.
type BatchScheduleResult struct {
    Pods          []*corev1.Pod     // all member pods, including the triggering pod
    PodToNodeName map[string]string // pod key "namespace/name" -> planned node name
}
```

The `FrameworkExtender` exposes `RunFindOneNodePlugin(...)`, invoked during the **PreFilter**
phase. A plugin registers by implementing `FindOneNodePluginProvider`. The reference
implementation is coscheduling's network-topology workflow
(`PodGroupManager.FindOneNode` → `buildBatchScheduleResult` → `BatchScheduleResult{Pods,
PodToNodeName}`).

### Inline batch scheduler

`pkg/scheduler/frameworkext/interface.go`:

```go
// BatchScheduler runs a scheduling + binding cycle for a whole job.
// Implemented outside frameworkext (in pkg/scheduler/batch) and registered on the extender
// to avoid an import cycle.
type BatchScheduler interface {
    BatchSchedule(ctx context.Context, ext FrameworkExtender, cycleState *framework.CycleState,
        triggerPod *corev1.Pod, plan *BatchScheduleResult) *framework.Status
}
```

The implementation lives in `pkg/scheduler/batch` and reuses the shared `Engine`
(`RunSchedulingCycle` + async binding). It is wired in
`cmd/koord-scheduler/app/server.go`:

```go
frameworkExtenderFactory.SetBatchScheduler(batch.NewBatchScheduler(sched.Cache, sched.FailureHandler))
```

### Control flow (in `RunPreFilterPlugins` / `framework_extender.go`)

1. Run `RunFindOneNodePlugin`.
2. `Skip` (or no plugin) → standard multi-node filtering (unchanged behavior).
3. `Success` with a plan:
   - **Re-entrancy / fallback guard**: if the cycle is already engine-driven
     (`hinter.IsBatchSchedulingCycle`), or the batch scheduler is not registered, or
     `EnableInlineBatchSchedule` is off, or the plan is `nil`/has no node for the pod → fall back
     to the single-node result (`PreFilterResult{NodeNames: {planned node}}`); an empty node name
     surfaces as an `Error` status instead of restricting to node `""`.
   - Otherwise (top-level cycle): call `BatchSchedule(...)` for the whole plan.
     - On success: all pods are reserved/assumed/permitted; their bind cycles run
       asynchronously. The trigger pod's own cycle returns
       `Unschedulable(BatchScheduledReason)` to short-circuit — this is a sentinel "already
       handled", not a real failure, and its failure handler is suppressed.
     - On failure: assumed pods are unreserved and forgotten; a plain `Unschedulable` is upgraded
       to `UnschedulableAndUnresolvable` so a batch failure never triggers preemption for the
       trigger pod.

### Re-entrancy marker

`pkg/scheduler/frameworkext/hinter/batch_cycle.go` adds a dedicated `CycleState` marker
(`MarkBatchSchedulingCycle` / `IsBatchSchedulingCycle`). The engine stamps every per-pod cycle
state before running the nested scheduling cycle, so the nested `RunPreFilterPlugins` does not
recursively re-trigger the top-level inline batch schedule. It is intentionally separate from the
scheduling-hint state (which may be set from pod annotations on a top-level cycle).

### Async binding model

`pkg/scheduler/batch` (`batch_scheduler.go`, `engine.go`) runs the scheduling cycle
(PreFilter/Filter/Reserve/Assume) synchronously for all pods, runs gang `Permit`, then launches
one binding goroutine per pod (`WaitOnPermit → PreBind → Bind → PostBind`) on a decoupled
context. Key invariants:

- Only the trigger pod is dequeued; siblings remain in the queue and are skipped once assumed
  (their `NominatedNode` is cleared at Assume time).
- Bind success calls `queue.Done`; bind failure goes through `handleBindingCycleError`
  (Unreserve + ForgetPod + requeue via the scheduler's failure handler), which also closes the
  trigger pod's in-flight accounting.
- Rollback on scheduling failure is complete (cache Unreserve + ForgetPod; nominator cleaned).

### Node snapshot injection (optional)

The engine can inject a per-node snapshot lister for the scheduling/binding phases (needed for
reservation restore). This is internal machinery: `snapshot.go` defines overridable package-level
hooks that default to no-ops, and an optional build may replace them via `init()`, gated by
`EnableBatchScheduleNodeSnapshot`. The callers work whether or not the override is present in the
build.

### Feature gates

`pkg/features/scheduler_features.go`:

- `EnableInlineBatchSchedule` (default **false**): enables the inline batch scheduling path. When
  off, a `FindOneNode` success just restricts the current pod to its planned node (legacy
  single-node behavior).
- `EnableBatchScheduleNodeSnapshot` (default **true**): enables the internal per-node snapshot
  injection described above.

### Metrics

`pkg/scheduler/metrics/metrics.go` adds `InlineBatchScheduleDuration` (labeled
success/failure). The engine emits its own `batch_*` per-node/per-pod latency and bind/cleanup
counters (defined in `pkg/scheduler/batch/metrics`).

## Changes in this commit

- **`pkg/scheduler/frameworkext/interface.go`**: `FindOneNodePlugin` / `FindOneNode`,
  `BatchScheduleResult`, `BatchScheduler`, `FindOneNodePluginProvider`, and
  `RunFindOneNodePlugin` on the extender.
- **`framework_extender.go` / `framework_extender_factory.go`**: `RunFindOneNodePlugin`, the
  inline-batch trigger + fallback/guard logic, `BatchScheduledReason`, and `SetBatchScheduler`
  registration.
- **`hinter/batch_cycle.go`**: dedicated batch-cycle re-entrancy marker.
- **`pkg/scheduler/batch/`**: `engine.go` (shared scheduling + async binding engine),
  `batch_scheduler.go` (`BatchScheduler` impl), `snapshot.go` (optional snapshot injection hooks),
  plus tests.
- **`pkg/features/scheduler_features.go`**: `EnableInlineBatchSchedule`,
  `EnableBatchScheduleNodeSnapshot`.
- **`pkg/scheduler/metrics/metrics.go`**: `InlineBatchScheduleDuration`.
- **`pkg/scheduler/plugins/coscheduling/...`**: `FindOneNode` reference implementation returning a
  `BatchScheduleResult` from the network-topology plan.
- **`cmd/koord-scheduler/app/server.go`**: wires the `BatchScheduler` onto the extender factory.

## Risks and Mitigations

- **Preemption on batch failure**: prevented by upgrading `Unschedulable` →
  `UnschedulableAndUnresolvable` for the trigger pod's PreFilter status.
- **Inaccurate plan cannot fall back to preemption**: because the inline batch cycle runs only
  PreFilter/Filter/Reserve/Assume and never invokes PostFilter/preemption, a stale or infeasible
  plan makes the whole job fast-fail and roll back with no preemption attempt. If the plan is
  persistently inaccurate the job can keep failing without ever preempting to make room. This is
  by design — preempting victims for a job that may not fully fit is worse than retrying — so plan
  feasibility (including any preemption needed to realize it) is the `FindOneNode` planner's
  responsibility, not the batch scheduler's. When the planner returns `Skip`, pods fall back to the
  standard single-pod path where existing preemption applies unchanged.
- **Recursion**: prevented by the dedicated `BatchSchedulingCycle` marker.
- **Sibling double-scheduling**: assume runs synchronously before binding; queued siblings are
  skipped via the assumed-pod guard.
- **Gang Permit stalls**: guarded by the permit rollback condition (all-waiting after all pods
  permitted returns a non-success status reporting the waiting pod count).
- **Off-by-default**: `EnableInlineBatchSchedule=false` preserves current behavior exactly.

## Alternatives Considered

- **Keep scheduling pod-by-pod and cache the plan**: still replays filters per pod and risks plan
  divergence; does not exploit the whole-job placement the algorithm already computed.
- **Synchronous whole-job binding inside the scheduling cycle**: would block the scheduling loop
  for the duration of all binds; async per-pod binding keeps the cycle fast and mirrors upstream
  `schedule_one`.
