---
title: PSI-Based Resource Intervention
authors:
- "@Wheat2018"
reviewers:
- "@koordinator-reviewers"
- "@saintube"
creation-date: 2025-06-03
last-updated: 2026-04-02
status: provisional
---

# PSI-Based Resource Intervention

## Glossary

| Term | Definition |
|---|---|
| PSI | Pressure Stall Information -- a Linux kernel feature (since 4.2) that tracks the time resources are stalled waiting for CPU, memory, or I/O |
| cgroup v2 | The second version of Linux control groups, required for per-cgroup PSI data |
| Request | The guaranteed resource amount specified in a Pod's resource specification |
| Limit | The maximum resource amount a Pod is allowed to consume |
| Burstable | A Kubernetes QoS class where at least one container has a resource request but not all requests equal limits |
| BestEffort | A Kubernetes QoS class where no containers have resource requests or limits |
| memory.high | A cgroup v2 interface that throttles memory allocation when usage exceeds the threshold |
| memory.limit_in_bytes | A cgroup interface that sets the hard memory limit; exceeding it triggers OOM-Killer |

## Summary

This proposal introduces a PSI (Pressure Stall Information) based resource intervention mechanism for Koordinator. By leveraging Linux PSI metrics, the system monitors workload resource pressure at the pod level and applies targeted interventions for CPU, I/O, and memory resources. The proposal covers three core mechanisms: group weight sharing for CPU/I/O, dynamic budget balancing for CPU/I/O, and memory waterline suppression. It also defines pod-level resource pressure conditions and status reporting to enable advanced scheduling behaviors such as VPA recommendations.

## Motivation

Linux 4.2 introduced Pressure Stall Information (PSI), which provides real-time monitoring of workloads that are unable to fully utilize their allocated CPU time slices due to resource pressure. By leveraging PSI, Koordinator can intervene on workloads when necessary, preventing excessive resource pressure and scheduling crises.

A node is not considered unhealthy simply because its resource utilization is high. Rather, it is unhealthy when utilization exceeds the total requested amount and cannot be compressed, making it difficult for newly scheduled workloads to obtain their guaranteed requests.

CPU and I/O are instantaneous bandwidth resources, meaning allocated bandwidth can be compressed at any time. Memory, on the other hand, is an allocatable resource where allocated portions can only be returned when workloads voluntarily release them (unless swap is enabled to force memory out). Therefore, discussing the health state of node memory resources is essential. When node memory resources are unhealthy, newly scheduled workloads cannot receive their guaranteed allocations, leading to scheduling crises. This may trigger the OOM-Killer to reclaim memory (under MemoryQoS) or cause workloads to stall due to insufficient resources.

### Goals

- Mitigate workload resource pressure as much as possible
- Maintain node resources in a healthy state
- Focus exclusively on pod-level CPU, memory, and I/O resources

### Non-Goals/Future Work

- Participate in the original resource allocation decisions for workloads
- Address container-level resource management
- Support resources beyond CPU, memory, and I/O
- Replace or modify the Kubernetes scheduler's resource allocation logic

## Proposal

This proposal introduces three intervention mechanisms and one observability mechanism:

1. **CPU/I/O Group Weight Sharing** -- allows pods in the same group to share idle weight with busy peers
2. **CPU/I/O Dynamic Budget Balancing** -- tracks resource usage over a pod's lifecycle, allowing bursty workloads to borrow weight from idle peers
3. **Memory Waterline Suppression** -- applies progressive pressure via `memory.high` when memory usage approaches the limit, giving pods grace time to release memory
4. **Resource Pressure Conditions** -- reports pod-level PSI-based conditions to enable advanced scheduling behaviors

### User Stories

#### Story 1: Pod-level Switches

Users can enable or disable each intervention mechanism independently via pod annotations. This provides fine-grained control over which workloads participate in which mechanisms, allowing users to opt in based on their workload characteristics and tolerance for resource variability.

#### Story 2: Batch Workload Optimization

A user runs a batch processing pipeline with multiple stages on the same node. Some stages are idle while others are busy. By configuring group weight sharing, idle stage resources are automatically redirected to busy stages, improving overall pipeline throughput without manual intervention.

#### Story 3: Bursty Workload Handling

A user runs a web service that experiences periodic traffic spikes. By enabling dynamic budget balancing, the service accumulates budget during low-traffic periods and spends it during spikes, obtaining more resources than its request would normally allow, while accepting reduced guarantees during sustained high load.

#### Story 4: Memory Spike Tolerance

A user runs a data processing job that occasionally experiences memory spikes due to large input batches. By enabling memory waterline suppression, the job is given more time to release memory before being killed, reducing unnecessary restarts and improving job completion rates.

## Implementation Details/Notes/Constraints

### API Definitions

#### Annotations

| Annotation | Type | Description |
|---|---|---|
| `koordinator.sh/group-hash` | string | User-specified group hash for weight sharing. Pods with the same hash belong to the same group. |
| `koordinator.sh/budget-balance` | string | Set to `"true"` to enable dynamic budget balancing for the pod. Only applies to Burstable pods. |
| `koordinator.sh/memory-suppress` | string | Set to `"true"` to enable memory waterline suppression for the pod. Applies to Burstable and BestEffort pods. |

#### Pod Conditions

Three new pod condition types are introduced:

```go
const (
	PodCpuInPressure    PodConditionType = "PodCpuInPressure"
	PodMemoryInPressure PodConditionType = "PodMemoryInPressure"
	PodIOInPressure     PodConditionType = "PodIOInPressure"
)
```

#### PSI Status Structure

```go
type PSIStatus struct {
	Cpu       StallInformation
	Memory    StallInformation
	IO        StallInformation
	Timestamp metav1.Time
}

type StallInformation struct {
	Avg10  float64
	Avg60  float64
	Avg300 float64
}
```

The `PSIStatus` is serialized as JSON and stored in the pod condition's `message` field.

#### Configuration Parameters

| Parameter | Default | Description |
|---|---|---|
| `psi-threshold` | configurable | Stall time percentage threshold within the monitoring period to trigger pressure condition |
| `period` | configurable | Time window for evaluating PSI threshold |
| `MinSpot` | 0.5 | Starting pressure point for memory suppression (relative to request-limit range) |
| `MaxSpot` | 0.9 | Maximum pressure point for memory suppression (relative to request-limit range) |
| `KillPeriod` | 60 | Number of sampling cycles after `MaxSpot` before allowing memory to reach limit |
| `LowerBound` | 0.5 | Minimum weight percentage below which weight is not shared in group sharing |
| `theta` | 0.5 | Minimum weight guarantee factor in budget balancing |

### CPU/I/O Group Weight Sharing

Pods are grouped by a user-specified group hash `koordinator.sh/group-hash` or by user UID (in multi-tenant scenarios). Within pod resources, the portion above current usage but below the request is shared into the group at a rate of $(1 - Pressure) \cdot 100\%$. When any pod in the group experiences pressure, it calculates the additional resources needed based on pressure and attempts to acquire them from the shared pool. When multiple pods in the group require additional resources and the shared pool cannot fully satisfy all demands, allocation is proportional to the requested amounts (pods currently below their request are guaranteed at least their request). Optionally, each pod has a minimum weight percentage `LowerBound` (typically $0.5$), below which weight is not shared.

### CPU/I/O Dynamic Budget Balancing

Pods with the annotation `koordinator.sh/budget-balance: "true"` participate in budget balancing. Only Burstable pods are supported.

**Principles:**
1. Budget is continuously calculated throughout the pod's lifecycle. Going below the request earns budget; going above the request consumes budget.
2. When a pod experiences pressure, it consumes budget to increase its weight. Consumption rate is proportional to weight increment, which is proportional to pressure magnitude.
3. When a pod has low pressure or insufficient budget, its weight may be "borrowed" by other pods, causing its weight to fall below the original value.
4. For all pods participating in dynamic budget balancing, the sum of weights remains constant.

**Budget Allocation Algorithm**

The budget increment for each pod per calculation cycle is:

$$\Delta_{Budget_i} = Price \cdot (2 \cdot Request_i - Current_i - Promise_i)$$

Where $Promise$ is the absolute value of the resource commitment derived from weight normalization.

The price factor scales with aggregate demand:

$$Price = \sum_i Pressure_i$$

The weight increment requested by pod $i$ is:

$$\Delta_{Promise_i} = \begin{cases} 0 & \text{if } Budget_i \le 0 \\ Pressure_i \cdot (Limit_i - Request_i) & \text{otherwise} \end{cases}$$

Additional rules:
1. $\sum_i \Delta_{Promise_i} = 0$ (sum of weights remains constant)
2. $\theta \cdot Request_i \le Promise_i \le Limit_i$ (no pod has too much weight borrowed)
3. $\theta$ defaults to $0.5$

### Memory Waterline Suppression

Pods with the annotation `koordinator.sh/memory-suppress: "true"` enable memory waterline suppression. Burstable and BestEffort pods are supported.

Starting from the pressure point `MinSpot` (default $0.5$), the workload gradually experiences increasing memory pressure, manifested as longer allocation times for memory requests as usage increases. Beyond `MaxSpot` (default $0.9$), memory allocation pressure reaches its maximum. After exceeding `MaxSpot`, pressure continues to accumulate. The controller tracks the duration above `MaxSpot`, and if it exceeds `KillPeriod` (default 60 sampling cycles), the controller stops increasing `memory.high`, allowing the pod to allocate up to its limit, at which point the kernel's OOM-Killer may be triggered if the limit is exceeded.

Optionally, `MinSpot` shifts forward as node memory utilization increases. Memory allocation pressure is implemented via the cgroup interface `memory.high`. After exceeding `MaxSpot`, `memory.high` no longer increases. BestEffort pods have an implicit limit equal to the node's currently remaining allocatable memory.

> **Note:** This mechanism is mutually exclusive with the existing MemoryQoS feature.

### Resource Pressure Conditions

When a pod's pressure stall reaches the threshold `psi-threshold` within a time period `period`, the pod's resource pressure condition is set to `True`. The `PSIStatus` data is stored in the condition's `message` field as a serialized JSON string.

### PSI Data Collection

The agent component on each node reads PSI data at the pod level via cgroup v2 interfaces: `cpu.pressure`, `memory.pressure`, and `io.pressure` within each pod's cgroup directory.

> **Note:** Node-level PSI data is available under `/proc/pressure/{cpu,memory,io}`, while pod-level data requires cgroup v2 and is exposed per-cgroup.

### Component Architecture

```
+-------------------------------------------------------------+
|                      Koordinator Agent                       |
|  +--------------+  +--------------+  +------------------+    |
|  | PSI Collector|  |   Condition  |  |  Weight Manager  |    |
|  |              |  |   Reporter   |  |                  |    |
|  +------+-------+  +------+-------+  +--------+---------+    |
|         |                 |                    |              |
|  +------v-----------------v--------------------v---------+    |
|  |              Intervention Controller                   |   |
|  |  +------------+ +------------+ +------------------+    |   |
|  |  |   Group    | |   Budget   | |    Memory        |    |   |
|  |  |  Sharing   | | Balancing  | |   Suppression    |    |   |
|  |  +------------+ +------------+ +------------------+    |   |
|  +--------------------------------------------------------+   |
+-------------------------------------------------------------+
```

### Compatibility with Existing Koordinator Features

| Feature | Compatibility | Notes |
|---|---|---|
| CPUQoS | Compatible | Both operate on cgroup `cpu.weight`; coordination needed to avoid conflicts |
| MemoryQoS | Mutually Exclusive | Memory waterline suppression uses `memory.high` which conflicts with MemoryQoS |
| Resource Reclaim | Compatible | PSI-based conditions can complement reclaim decisions |
| Colocation | Compatible | Group weight sharing can enhance colocation efficiency |

### Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Dynamic budget balancing causes unpredictable resource allocation | Participating pods may not receive their full request | Clear documentation; opt-in via annotation; minimum weight guarantee via `theta` |
| Memory waterline suppression conflicts with MemoryQoS | Cannot use both features simultaneously | Document mutual exclusivity; validate annotations at admission |
| PSI data accuracy depends on kernel version | Pod-level PSI requires Linux 4.20+ with cgroup v2 | Document kernel requirement; graceful degradation on unsupported nodes |
| Budget allocation algorithm parameters require tuning | Suboptimal performance without proper tuning | Provide sensible defaults; expose configurable parameters; document tuning guide |
| Additional complexity in resource management stack | Increased maintenance burden | Modular design; clear separation of concerns; comprehensive tests |
| Group hash collision in multi-tenant environments | Unintended weight sharing across tenants | Validate hash format; recommend namespace-prefixed hashes |

## Alternatives

### Alternative 1: Metrics-based Pressure Reporting

Instead of using pod conditions, pressure status could be reported exclusively through Prometheus metrics. This approach is simpler to implement but has lower reliability for scheduling decisions, as metrics are not tightly bound to pod lifecycle and may experience delays or drops.

### Alternative 2: Node-level Intervention Only

Rather than pod-level intervention, apply pressure mitigation at the node level. This simplifies implementation but lacks granularity and cannot address per-workload pressure differences.

### Alternative 3: Integration with Existing QoS Mechanisms

Extend the existing MemoryQoS and CPUQoS mechanisms rather than introducing new controllers. This would reduce code duplication but may complicate the existing QoS logic and make independent evolution harder.

## Upgrade Strategy

### Upgrade

- The feature is disabled by default via feature gate
- Existing workloads are unaffected until annotations are added
- No data migration required; all state is ephemeral and computed at runtime

### Downgrade

- Removing the feature gate disables all intervention mechanisms
- Weight adjustments are reverted to original cgroup values on agent restart
- Pod conditions are cleared when the feature is disabled
- No persistent state requires cleanup

### Version Skew

- The Koordinator agent must be at least the same version as the controller for full functionality
- Older agents on some nodes will simply not report PSI data or apply interventions; this is safe and degrades gracefully
- Pod conditions from newer agents are backward compatible with older Kubernetes API servers (standard condition format)

## Additional Details

### Dependencies

- Linux kernel 4.20 or later for full pod-level PSI support via cgroup v2
- cgroup v2 enabled on worker nodes
- Koordinator agent deployed on all nodes where intervention is desired
- Compatible with Kubernetes 1.24+

## Test Plan

### Unit Tests

- PSI data parsing and aggregation from cgroup v2 interfaces
- Budget allocation algorithm correctness (budget delta computation, weight redistribution)
- Group weight sharing logic (pool calculation, proportional allocation)
- Memory waterline suppression controller (pressure curve, `memory.high` adjustment)
- Condition reporter (threshold evaluation, condition state transitions)

### Integration Tests

- End-to-end PSI data collection on a test node with synthetic workloads
- Group weight sharing with multiple pods in the same group under varying load patterns
- Dynamic budget balancing with bursty workloads and idle pods
- Memory waterline suppression with controlled memory spike workloads
- Pod condition reporting under sustained resource pressure

### E2E Tests

- Full lifecycle test: deploy workloads, apply annotations, verify intervention behavior
- Multi-node test: verify group weight sharing across pods scheduled on the same node
- Stress test: verify controller stability under high pod density (100+ pods per node)
- Failover test: verify graceful recovery after agent restart

### Performance Tests

- Measure CPU overhead of PSI collection and intervention controllers
- Measure latency from pressure detection to condition update
- Measure impact of weight adjustments on workload throughput

## Implementation History

- [ ] 2026-04-02: Initial proposal drafted
