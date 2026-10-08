---
title: Native Support for AI Agent Orchestration and Sandbox Scheduling
authors:
  - "@tan90github"
reviewers:
  - "@saintube"
  - "@ZiMengSheng"
creation-date: 2026-06-26
last-updated: 2026-07-01
status: provisional
see-also:
  - "https://github.com/koordinator-sh/koordinator/issues/2879"
  - "/docs/proposals/scheduling/20220609-resource-reservation.md"
  - "/docs/proposals/scheduling/20221227-node-resource-reservation.md"
  - "/docs/proposals/koordlet/20220615-qos-manager.md"
---

# Native Support for AI Agent Orchestration and Sandbox Scheduling

## Table of Contents

- [Native Support for AI Agent Orchestration and Sandbox Scheduling](#native-support-for-ai-agent-orchestration-and-sandbox-scheduling)
  - [Table of Contents](#table-of-contents)
  - [Glossary](#glossary)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
      - [Non-Goals](#non-goals)
      - [Future Work](#future-work)
  - [Proposal](#proposal)
    - [User Stories](#user-stories)
      - [Story 1](#story-1)
      - [Story 2](#story-2)
      - [Story 3](#story-3)
      - [Story 4](#story-4)
    - [Requirements (Optional)](#requirements-optional)
      - [Functional Requirements](#functional-requirements)
        - [FR1](#fr1)
        - [FR2](#fr2)
        - [FR3](#fr3)
      - [Non-Functional Requirements](#non-functional-requirements)
        - [NFR1](#nfr1)
        - [NFR2](#nfr2)
    - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
      - [Sandbox Integration and Adaptation](#sandbox-integration-and-adaptation)
        - [Integration Boundary](#integration-boundary)
        - [Pod-shaped Sandbox](#pod-shaped-sandbox)
        - [Non-Pod Sandbox Accounting](#non-pod-sandbox-accounting)
      - [Sandbox Scheduling Performance Optimization](#sandbox-scheduling-performance-optimization)
        - [Dedicated Sandbox Scheduling Framework](#dedicated-sandbox-scheduling-framework)
        - [High-Throughput Sandbox Scheduling Bottleneck Analysis](#high-throughput-sandbox-scheduling-bottleneck-analysis)
        - [Benchmark Instrumentation](#benchmark-instrumentation)
        - [Benchmark Targets](#benchmark-targets)
        - [Benchmark Metrics and PromQL](#benchmark-metrics-and-promql)
    - [Implementation Phases](#implementation-phases)
    - [Risks and Mitigations](#risks-and-mitigations)
  - [Alternatives](#alternatives)
    - [Define a Koordinator Sandbox CRD](#define-a-koordinator-sandbox-crd)
    - [Use NodeReservation for All Non-Pod Sandboxes](#use-nodereservation-for-all-non-pod-sandboxes)
    - [Only Use the Existing Koord-Scheduler Plugin Path](#only-use-the-existing-koord-scheduler-plugin-path)
    - [Run a Separate Sandbox Scheduler](#run-a-separate-sandbox-scheduler)
  - [Upgrade Strategy](#upgrade-strategy)
  - [Additional Details](#additional-details)
    - [Test Plan [optional]](#test-plan-optional)
    - [Open Questions](#open-questions)
    - [References](#references)
  - [Implementation History](#implementation-history)

## Glossary

- **Sandbox**: An isolated environment used by an agent to run code, invoke tools, or execute evaluation tasks. A sandbox can be a Kubernetes Pod, a node-local microVM, a host process, or an external runtime instance.
- **Pod-shaped sandbox**: A sandbox that is ultimately represented by a Kubernetes Pod, including PodTemplate-based sandbox APIs, gVisor, Kata, Wasm RuntimeClass, and similar runtimes.
- **Non-Pod sandbox**: A sandbox that consumes resources on Koordinator-managed nodes but does not create a Kubernetes Pod.
- **Accounting-only Reservation**: A Reservation that only represents resource usage by an external runtime and cannot be claimed by ordinary Pods.
- **Sandbox scheduling framework**: A dedicated scheduling framework selected by Koordinator based on sandbox labels to provide efficient sandbox scheduling.

## Summary

AI Agent systems frequently create short-lived sandboxes for tool calls, code execution, multi-agent collaboration, and SWE-bench-style evaluations. These sandboxes usually have similar templates, are created in bursts, have short lifetimes, and are sensitive to startup latency.

Koordinator does not introduce a Sandbox CRD and does not replace upstream sandbox APIs. This proposal defines only two integration paths:

- Pod-shaped sandboxes: sandboxes that eventually create Kubernetes Pods. They opt in through labels on the Pod or PodTemplate and enter the sandbox scheduling framework.
- Non-Pod sandboxes: sandboxes that run on Koordinator-managed nodes but do not create Pods. An adapter creates an accounting-only Reservation, and the Reservation represents the resources consumed by the sandbox.

The implementation order is to complete the integration loop first, optimize scheduling performance next, and then improve surrounding sandbox capabilities.

Overall workflow:

```text
                  AI Agent / Sandbox API
                            |
          +-----------------+-----------------+
          |                                   |
          v                                   v
  Pod-shaped sandbox                  Non-Pod sandbox
          |                         external runtime request
          |                                   |
          |                                   v
          |                            Non-Pod adapter
          |                                   |
          |                                   v
          |                           accounting-only Reservation
          |                           scheduling.koordinator.sh/sandbox=true label
          |                                   |
          +-----------------+-----------------+
                            |
                            v
             Koordinator sandbox scheduling framework
                    (high-throughput path)
                            |
              +-------------+-------------+
              |                           |
              v                           v
        bind sandbox Pod          select node for Reservation
                                          |
                                          v
                              adapter watches Reservation status
                              and notifies external sandbox controller
                              to start sandbox on selected node
```

## Motivation

Issue #2879 asks Koordinator to provide native support for AI Agent scenarios. The focus is not to redesign the AI Agent orchestration model, but to make Koordinator's existing scheduling, Reservation, and isolation capabilities available to sandboxes.

Koordinator can provide three main benefits:

- **Visible resource usage**: Non-Pod sandboxes enter the scheduler cache through Reservations, so node-local resource usage is visible to the scheduler.
- **Faster scheduling**: A dedicated sandbox scheduling framework can optimize queues, plugin chains, candidate nodes, concurrent scheduling cycles, and the binding path for high-concurrency sandbox workloads.
- **QoS guarantees**: Koordinator has strong QoS management capabilities and can prevent low-priority sandboxes from interfering with high-priority sandboxes.

### Goals

- Define the integration boundary between sandboxes and Koordinator.
- Define the conversion protocol from Non-Pod sandboxes to accounting-only Reservations.
- Complete the minimum loop for sandbox creation, scheduling or resource accounting, startup, exit, and release.
- Introduce a dedicated sandbox scheduling framework to support efficient sandbox scheduling.
- Define a reproducible benchmark profile, use sustained bind throughput of 2k sandboxes/s on 1000 fake nodes as the first-stage target, and use later benchmarks to drive toward a 5k sandboxes/s long-term target.

### Non-Goals/Future Work

#### Non-Goals

- Do not introduce a Koordinator Sandbox CRD.
- Do not implement a sandbox runtime, network isolation, filesystem isolation, or VM lifecycle management.
- Do not manage sandboxes that run outside cluster nodes.

#### Future Work

- Deep integration between sandboxes and Koordinator QoS.
- Sandbox failure awareness and automatic diagnosis.
- Compatibility between Reservations and upstream sandbox prewarming APIs.

## Proposal

### User Stories

#### Story 1

**Fast scheduling for short-lived Sandbox Pods on Kubernetes:** As an AI Agent platform developer, I have already integrated the sandbox lifecycle with Kubernetes. The upper layer creates Pods through `kubernetes-sigs/agent-sandbox`. A single user interaction, SWE-bench evaluation, or multi-agent collaboration can create tens to hundreds of sandbox Pods within a short time window. These Pods usually share similar images, resource shapes, runtimes, and placement constraints, and differ only in input data, code repositories, or task context.

The existing Kubernetes scheduling path treats each sandbox Pod as an ordinary long-running workload. Under high-concurrency creation, repeated Filter / Score calculations, binding writes, and status updates amplify pressure on the scheduler and APIServer, making it difficult for sandbox startup latency to meet the requirements of interactive agents.

#### Story 2

**Non-Pod sandbox resource usage is invisible in Kubernetes clusters:** As a platform engineer, my sandbox runtime does not always create Pods. It may be a node-local Firecracker microVM, a host process, a prewarmed process pool, or an external runtime instance. These sandboxes run on Kubernetes nodes managed by Koordinator and consume CPU, memory, disk, or device resources, but kube-scheduler and koord-scheduler cannot see this usage by default.

If the external runtime selects a node and starts the sandbox directly, later ordinary Pods or other sandboxes may still be scheduled to the same node, causing resource overcommitment and affecting node stability.

#### Story 3

**Sandbox QoS and runtime overhead need to be included in resource guarantees:** As the SRE of a multi-tenant Agent platform, I need to run long-lived, latency-sensitive Agent Brain workloads and short-lived, bursty Skill / Tool sandboxes on the same set of nodes. Brain workloads and interactive sandboxes are sensitive to tail latency, while background tasks and batch sandboxes care more about throughput and cost. They cannot be placed under a single priority and QoS model without distinction.

Existing Pod requests / limits usually cover only the workload resources inside a sandbox and may not include runtime overhead from gVisor `runsc`, Kata VM / shim, Wasm runtimes, and similar components. In high-density deployments, this overhead accumulates and causes actual node resource usage to be underestimated. At the same time, low-priority sandbox bursts may interfere with high-priority Brain workloads or interactive sandboxes.

#### Story 4

**Prewarmed sandbox capacity must stay consistent with scheduler-side resource accounting:** As the owner of an interactive Agent platform, I maintain a warm pool or warm process pool to reduce sandbox cold-start latency.

If warm capacity is maintained only by the external runtime, Koordinator cannot determine whether those resources are already used or reserved. This can cause duplicate allocation, uncontrolled fallback to cold starts, or inconsistency between scheduling results and actual available capacity. This capability can be designed separately after the integration loop is stable.

### Requirements (Optional)

The first version focuses on explicit sandbox identification, Pod-shaped sandbox scheduling, Non-Pod sandbox accounting through Reservations, and benchmark-driven scheduler optimization. Requirements are derived from the user stories above and should be traceable to implementation details.

#### Functional Requirements

##### FR1

Pod-shaped sandboxes MUST opt in through the `scheduling.koordinator.sh/sandbox=true` label on the final Pod or PodTemplate. If an upstream sandbox API, controller, admission webhook, or adapter eventually creates a Kubernetes Pod, that final Pod is the scheduling object consumed by Koordinator. Koordinator MUST NOT introduce a separate Sandbox CRD for this path or consume upstream sandbox APIs directly as first-class scheduling APIs.

##### FR2

For Non-Pod sandboxes running on Koordinator-managed nodes, the adapter MUST use a Reservation-first protocol: create an accounting-only Reservation before starting the external runtime, wait until the Reservation is `Available` with a selected node, and delete the Reservation when the external sandbox exits, fails, is cancelled, or no longer consumes node resources. The adapter-created accounting-only Reservation MUST carry the `scheduling.koordinator.sh/sandbox=true` label so it enters the sandbox scheduling framework. Accounting-only Reservations MUST NOT be claimable by ordinary Pods.

##### FR3

Koordinator MUST provide a dedicated sandbox scheduling framework or pipeline for sandbox workloads instead of relying only on ordinary Kubernetes scheduler out-of-tree plugins. The framework MUST preserve correctness-critical checks and be isolated from the default scheduling path so that non-sandbox Pods keep existing koord-scheduler behavior unless explicitly configured otherwise.

#### Non-Functional Requirements

##### NFR1

High-throughput sandbox scheduling is a primary requirement for production adoption. The implementation SHOULD use explicit benchmark profiles and staged performance targets. The first concrete optimization target is 2k sandboxes/s bind throughput in a 1000 fake-node homogeneous sandbox burst benchmark, so 2k sandboxes/s is the first-stage baseline and 5k sandboxes/s is the long-term target. The 5k target requires separate benchmark evidence and correctness validation.

##### NFR2

Sandbox scheduling optimization MUST be evaluated against a baseline benchmark before being treated as an optimization. The benchmark MUST report create-to-bound p99, scheduler stage p99, binding / Reservation availability p99, Pod binding / Reservation write throughput, API write p99, APIServer flow-control rejections, non-2xx API write responses, and at least one repeatable bottleneck improvement.

### Implementation Details/Notes/Constraints

#### Sandbox Integration and Adaptation

##### Integration Boundary

The first integration decision is whether the sandbox consumes resources on nodes managed by Koordinator.

| Scenario | Example | Integration path |
|---|---|---|
| Does not run on Koordinator-managed nodes | Managed sandbox service, standalone VM / Nomad platform | Do not integrate. Koordinator cannot observe or deduct resources from the corresponding nodes. |
| Runs on Koordinator-managed nodes and eventually creates a Pod | `kubernetes-sigs/agent-sandbox`, PodTemplate-based API, gVisor / Kata / Wasm / runc Pod | Keep the sandbox label on the final Pod or PodTemplate and enter the sandbox scheduling framework. |
| Runs on Koordinator-managed nodes but does not create a Pod | Node-local Firecracker VM, host process sandbox, external runtime instance | The adapter creates an accounting-only Reservation to represent resource usage. |

Koordinator does not consume every upstream sandbox API as a first-class scheduling API. Instead, it defines a small scheduling integration contract.

For Pod-shaped sandboxes, the integration point is the final Pod or PodTemplate. The upstream sandbox controller, user-provided PodTemplate, admission webhook, or adapter only needs to ensure that the final Pod carries the `scheduling.koordinator.sh/sandbox=true` label. Koordinator can then route that Pod into the sandbox scheduling framework.

For Non-Pod sandboxes, the integration point is the adapter. The adapter converts the upstream sandbox request into an accounting-only Reservation, waits for Koordinator to select or confirm a node, and then notifies the external sandbox controller or runtime to start the sandbox on that node. After the sandbox no longer consumes resources, the adapter deletes the Reservation.

##### Pod-shaped Sandbox

The source of truth for a Pod-shaped sandbox is the final Pod.

- sandbox label: `scheduling.koordinator.sh/sandbox=true`

Koordinator does not calculate runtime overhead. Additional overhead from gVisor, Kata, Wasm, and similar runtimes is configured by users through the Kubernetes RuntimeClass overhead field.

##### Non-Pod Sandbox Accounting

Non-Pod sandboxes are converted by an adapter into accounting-only Reservations. This path only represents resource usage and does not take over network, filesystem, or VM lifecycle implementation for the external runtime.

Adapter workflow:

1. The adapter creates an accounting-only Reservation from the external sandbox request and writes information such as runtime family, external sandbox ID, resource shape, and template hash.
2. If the external system has already selected a node, the adapter sets the target node in `spec.template.spec.nodeName`; otherwise, Koordinator selects a node.
3. The adapter waits until Reservation scheduling is complete and verifies that `status.nodeName` is not empty.
4. The adapter notifies the external sandbox controller or runtime to start the sandbox based on `status.nodeName`, and associates the external instance ID / status with the Reservation through annotations, events, or an external status store.
5. When the sandbox exits, fails to start, or is cancelled, the adapter stops the external runtime first and then deletes the Reservation. Both stop and delete operations must be idempotent.
6. The adapter periodically reconciles state: keep the Reservation while the sandbox exists; delete the Reservation when it exists but the sandbox does not. If deletion fails, the adapter must retry and report the error.

```text
External sandbox request
        |
        v
Adapter creates accounting-only Reservation
        |
        +-- if external system selected node:
        |       set spec.template.spec.nodeName
        |
        +-- otherwise:
        |       koordinator selects node
        |
        v
Reservation Scheduled + status.nodeName set
        |
        +-- if Reservation Failed:
        |       do not start runtime
        |       report scheduling error
        |
        v
Adapter notifies external sandbox controller
to start sandbox on status.nodeName
        |
        +-- if runtime start failed:
        |       delete Reservation
        |       report start error
        |
        v
External sandbox running
        |
        v
Sandbox exits / fails / is cancelled
        |
        v
Adapter stops runtime and deletes Reservation

Periodic reconciliation loop:

Sandbox exists
        -> keep Reservation

Reservation exists, sandbox missing
        -> delete Reservation

Reservation delete failed
        -> retry deletion and report error

Sandbox exists, Reservation missing
        -> report accounting error and recreate Reservation if needed
```

**Template adaptation.** Different systems use different sandbox API protocols, but Koordinator only needs the same resource accounting semantics: resource shape, target node, external sandbox ID, and audit information. The adapter reads external system requests in one direction and translates these fields into the `spec.template` and annotations of an accounting-only Reservation. The resource shape determines the resource usage represented by the Reservation; the other fields are only used for auditability.

**Lifecycle adaptation.** Different systems have different status enums, such as [E2B](https://e2b.dev/) and [Daytona](https://www.daytona.io/), but resource accounting only needs one criterion: whether physical resources are being consumed. The adapter reduces external statuses into a common lifecycle that drives Reservation behavior:

| Common state | Reservation action |
|---|---|
| Pending (no node selected) | Create Reservation and wait for it to become `Available` (do not start runtime yet) |
| Running (consumes resources) | Keep |
| Suspended (suspended but still consumes resources) | Keep |
| Released (suspended and resources released) | Delete |
| Terminated / Failed | Delete |

One detail is important: "suspended" must distinguish whether resources are released. The same pause semantics can have different effects in different runtimes. The reduced lifecycle keeps only the state boundaries that change node resource usage; other transitional states are folded into adjacent stable states.

Accounting-only Reservations need three layers of protection:

- Set `spec.unschedulable: true` so they are not allocatable by default.
- Set `spec.owners` to an object reference derived from the external sandbox instance identity, with `object.name` set to the sandbox name and `object.uid` set to the sandbox UID. The sandbox UID MUST be unique per sandbox instance and MUST NOT be derived only from the sandbox template hash. This makes the Reservation valid while preventing ordinary Pods from satisfying the owner matcher.
- Add admission validation for accounting-only Reservations to ensure `spec.unschedulable` and `spec.owners` follow the constraints above.

#### Sandbox Scheduling Performance Optimization

##### Dedicated Sandbox Scheduling Framework

Koordinator will introduce a dedicated sandbox scheduling framework for sandbox workloads instead of relying only on ordinary Kubernetes scheduler out-of-tree plugins. The first implementation assumes this framework runs inside the existing koord-scheduler process and reuses its scheduler cache, Reservation integration, events, metrics, and operational surface.

The framework is selected only for workloads explicitly labeled with `scheduling.koordinator.sh/sandbox=true`; non-sandbox Pods continue to use the existing koord-scheduler path.

The framework applies to both Pod-shaped sandbox Pods and accounting-only Reservations created for Non-Pod sandboxes. A Non-Pod sandbox adapter MUST set `scheduling.koordinator.sh/sandbox=true` on the corresponding Reservation so the Reservation also enters the sandbox scheduling framework.

Implementation order for the framework itself: first land a pass-through sandbox framework that behaves identically to the default scheduling path and confirm that ordinary Pods keep their existing behavior, then apply candidate-node reduction and plugin trimming on top of that baseline, and only after these low-risk optimizations run the benchmark breakdown to decide the next optimization. Pruning comes first; deeper performance analysis follows. A framework wrapper by itself does not increase throughput, so it must not be treated as an optimization until the benchmark shows a measurable improvement over the pass-through baseline.

Whether this framework should eventually run as a separate scheduler binary or deployment is still an open question. The answer should be based on benchmark results, fault-isolation requirements, cache / Reservation integration complexity, and operational cost.

##### High-Throughput Sandbox Scheduling Bottleneck Analysis

Sandbox workloads are bursty and short-lived. In SWE-bench, tool execution, and multi-agent workflows, many sandbox Pods can be created within a short time window. The scheduler must optimize both decision latency and binding / write throughput.

The bottlenecks are usually in two layers:

- **Scheduling decision path**: Filter, Score, Reserve, and assume decide where a sandbox should run. Re-running the full plugin chain for many near-identical sandbox Pods limits scheduling throughput.
- **Binding and API write path**: PreBind patches, Reservation status updates, Pod binding, retries, and watch fanout can saturate APIServer or etcd even when scheduling decisions are fast.

The sandbox scheduling framework should optimize in this order:

1. Trim unnecessary plugins, reduce candidate nodes, and tune the scoring percentage for sandbox workloads. These are the first low-risk optimizations after correctness checks are fixed.
2. Use the benchmark instrumentation below to identify whether the next bottleneck is in the scheduling decision path or the binding / API write path.
3. If bind or APIServer writes dominate, reduce unnecessary patches / status updates first, then evaluate binder queue or binding pipeline optimization.
4. If scheduling decision cost dominates, evaluate equivalence-class reuse, node pools, optimized snapshots, and concurrent scheduling. These optimizations must preserve scheduling correctness.

##### Benchmark Instrumentation

The benchmark must make bottlenecks observable before any optimization is accepted. At minimum, it should distinguish scheduler decision cost, binding / Reservation availability cost, and APIServer / etcd write pressure.

Pod-shaped sandboxes should report create-to-bound latency. Non-Pod sandboxes should report Reservation-created-to-Available latency. `observed-running` can be recorded as an optional startup signal, but it is reported separately because it includes controller, kubelet, runtime, image pull, CNI, and external sandbox startup costs.

Detailed scheduler stage metrics, API write metrics, and pprof are listed in the benchmark metrics section below.

##### Benchmark Targets

The first concrete performance target is defined for a reproducible fake-cluster benchmark:

- Cluster shape: use KubeMark to create 1000 virtual nodes as Hollow Node Pods on dedicated benchmark nodes.
- Load generator: use [ClusterLoader2](https://github.com/kubernetes/perf-tests/blob/master/clusterloader2/README.md) to create Pod-shaped sandboxes at scale and at controlled rates.
- Workload shape: homogeneous Pod-shaped sandbox burst, using the same image, resource shape, runtime class, and sandbox label.
- First-stage target: sustained bind throughput reaches 2k sandboxes/s.
- Long-term target: sustained bind throughput reaches 5k sandboxes/s.
- Required reports: create-to-bound p50 / p95 / p99, Reservation-created-to-Available p50 / p95 / p99, scheduler stage p50 / p95 / p99, binding / Reservation availability p50 / p95 / p99, Pod binding / Reservation write throughput, API write p99, APIServer flow-control rejections, non-2xx API write responses, scheduler CPU / heap pprof, APIServer CPU / heap pprof when API writes dominate, and etcd saturation metrics when available.

Accounting-only Reservations should be tested with the same benchmark approach.

The 2k sandboxes/s first-stage target and the 5k sandboxes/s long-term target are stage gates for the sandbox scheduling framework and binding path, not the only success criteria. A result is accepted only if correctness tests pass, non-sandbox Pods are not routed into the sandbox framework, and any APIServer throttling or API write bottleneck is reported with the benchmark environment and required control-plane configuration.

##### Benchmark Metrics and PromQL

The benchmark should publish a small fixed PromQL report so different runs are comparable. The report should focus on signals that decide whether throughput is limited by the scheduler decision path, Pod / Reservation writes, or control-plane throttling. Koordinator sandbox-specific metrics in this table are proposed metrics; existing Kubernetes scheduler and APIServer metrics are the fallback when sandbox-specific metrics are not yet available.

| Signal | Purpose | PromQL example |
|---|---|---|
| Sandbox scheduling throughput | Measures successful sandbox scheduling QPS. | `sum(rate(koordinator_sandbox_scheduling_attempts_total{result="scheduled"}[1m]))` |
| Scheduling failures | Verifies failed or unschedulable attempts do not hide throughput problems. | `sum(rate(koordinator_sandbox_scheduling_attempts_total{result!="scheduled"}[1m])) by (result)` |
| E2E scheduling p99 | Measures scheduling latency seen by the scheduler. | `histogram_quantile(0.99, sum(rate(scheduler_e2e_scheduling_duration_seconds_bucket[5m])) by (le))` |
| Scheduling stage p99 | Finds slow framework extension points such as Filter, Score, Reserve, PreBind, and Bind. | `histogram_quantile(0.99, sum(rate(scheduler_framework_extension_point_duration_seconds_bucket[5m])) by (extension_point, le))` |
| Pod binding write rate | Measures Pod-shaped sandbox binding throughput. | `sum(rate(apiserver_request_total{resource="pods",subresource="binding",verb="POST",code=~"2.."}[1m]))` |
| Reservation write rate | Measures Non-Pod sandbox Reservation create / update throughput. | `sum(rate(apiserver_request_total{group="scheduling.koordinator.sh",resource="reservations",verb=~"POST|PUT|PATCH",code=~"2.."}[1m])) by (verb, subresource)` |
| API write p99 | Finds slow Pod binding or Reservation writes. | `histogram_quantile(0.99, sum(rate(apiserver_request_duration_seconds_bucket{verb=~"POST|PUT|PATCH",resource=~"pods|reservations"}[5m])) by (verb, resource, subresource, le))` |
| API write failures | Detects failed Pod binding or Reservation writes. | `sum(rate(apiserver_request_total{verb=~"POST|PUT|PATCH",resource=~"pods|reservations",code!~"2.."}[1m])) by (verb, resource, subresource, code)` |
| APIServer flow-control rejections | Detects APIServer throttling. | `sum(rate(apiserver_flowcontrol_rejected_requests_total[1m])) by (priority_level, flow_schema)` |
| Scheduler CPU | Correlates pprof with scheduler CPU saturation. | `sum(rate(container_cpu_usage_seconds_total{pod=~"koord-scheduler.*",container!="POD",container!=""}[1m])) by (pod)` |

### Implementation Phases

Phase 1 completes the integration loop:

- Pod-shaped sandboxes opt in through the `scheduling.koordinator.sh/sandbox=true` label.
- Confirm the Non-Pod sandbox types that need compatibility.
- Complete the Reservation-first flow for Non-Pod sandboxes through accounting-only Reservations: create before runtime startup, start after the Reservation becomes Available, and release after exit.
- Establish the baseline benchmark, stage metrics, API write metrics, and pprof, and verify that non-sandbox Pods are not affected.

Phase 2 builds the basic optimizations for the dedicated sandbox scheduling framework:

- Implement candidate-node reduction and scheduling parameter tuning.
- Use the sandbox burst benchmark profile to break down queue, filter / score, reserve, prebind, bind, and APIServer write latency.
- Compare results with the baseline, confirm optimization benefits, and identify the next bottleneck.

Phase 3 adds pipeline-level optimizations required for the long-term 5k sandboxes/s target:

- If APIServer writes or bind is the bottleneck, reduce unnecessary patches / status updates first, then evaluate internal binder queue or binding pipeline optimization.
- If Filter / Score is the bottleneck, define equivalence keys, node-pool dimensions, optimized snapshots, and reusable plugin scopes.
- Node pools, homogeneous scheduling, optimized snapshots, and concurrent scheduling need separate validation and must not be prerequisites for the first-stage integration loop.

Phase 4 covers other future work:

- Adapt one or two Non-Pod sandbox implementations.
- Add sandbox runtime guarantees and integrate with Koordinator QoS classes.
- Integrate upstream sandbox prewarming with Reservations.

### Risks and Mitigations

- **Risk: high-throughput optimization breaks scheduling correctness.** Plugin-chain trimming, candidate-node reduction, equivalence-class reuse, optimized snapshots, and concurrent scheduling can skip required checks if the model is incomplete. The framework must keep correctness-critical checks, gate risky optimizations independently, and validate them with benchmark and integration tests.
- **Risk: APIServer or etcd becomes the bottleneck before scheduler CPU.** Even if scheduling decisions are fast, Pod binding, Reservation status updates, watch fanout, and retry storms can cap throughput. Benchmarks must report Pod binding / Reservation write throughput, API write p99, APIServer flow-control rejections, non-2xx API write responses, binding / Reservation availability p99, and scheduler stage latency separately.

## Alternatives

### Define a Koordinator Sandbox CRD

Koordinator would get a complete API surface, but it would duplicate upstream sandbox APIs and push Koordinator from a scheduling system toward a sandbox orchestration system. This proposal does not use this approach.

### Use NodeReservation for All Non-Pod Sandboxes

NodeReservation is suitable for coarse-grained and relatively static node-level deductions. It has low write cost and does not create an independent object for each sandbox.

The problem is that it lacks a per-sandbox lifecycle. In short-lived, high-churn scenarios, asynchronous updates can easily create accounting gaps, such as adding accounting only after startup or failing to release resources after exit.

### Only Use the Existing Koord-Scheduler Plugin Path

This approach keeps all sandbox optimization inside ordinary scheduler plugins and the existing koord-scheduler pipeline. It has a smaller first implementation scope and can reuse existing extension points directly.

The limitation is that ordinary out-of-tree plugins cannot fully control queueing, scoring percentage, scheduling-cycle concurrency, snapshot reuse, binding pipeline, or API write behavior. These pipeline-level controls are required for the long-term 5k sandboxes/s scheduling target. Therefore, this proposal uses a dedicated sandbox scheduling framework as the target architecture, while still allowing the first implementation to run inside the existing koord-scheduler process for cache and Reservation integration.

### Run a Separate Sandbox Scheduler

A separate sandbox scheduler binary or deployment could provide stronger pipeline isolation and independent scaling. The cost is higher integration complexity: it must share or duplicate scheduler cache state, Reservation accounting, events, metrics, leader election, configuration, and operational procedures with koord-scheduler.

The first version keeps sandbox scheduling inside the existing koord-scheduler process to minimize integration risk, but this trade-off needs validation. A separate scheduler should remain an option if benchmark results show that in-process isolation cannot meet throughput or fault-isolation goals, or if the sandbox pipeline needs independent scaling and operations.

## Upgrade Strategy

TBD

## Additional Details

### Test Plan [optional]

TBD

### Open Questions

- Should the sandbox scheduling framework run inside the existing koord-scheduler process, or should it be split into a separate sandbox scheduler? The decision criteria include throughput targets, fault isolation, scheduler cache / Reservation reuse, deployment complexity, and operational cost.
- Does accounting-only Reservation need explicit new API / plugin semantics to guarantee that it can never be claimed by Pods?
- If benchmarks prove that Filter / Score is the main bottleneck, which scheduler plugins can safely enable equivalence-class reuse in the first version?
- Which Non-Pod sandbox implementations should be supported first?

### References

- [Issue #2879: Native support for AI Agent orchestration and high-concurrency Sandbox scheduling](https://github.com/koordinator-sh/koordinator/issues/2879)
- [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox)
- [Volcano Agent Scheduler design](https://github.com/volcano-sh/volcano/blob/v1.14.0/docs/design/agent-scheduler.md)
- [koordinator Reservation proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20220609-resource-reservation.md)
- [koordinator QoS Manager proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/koordlet/20220615-qos-manager.md)
- [E2B Sandbox API reference](https://e2b.dev/docs/api-reference)

## Implementation History

TBD
