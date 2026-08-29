---
title: Lambda-G Symmetric Exhaustion Score Plugin
authors:
  - "@0x-auth"
reviewers:
  - TBD
creation-date: 2026-04-02
last-updated: 2026-04-22
status: provisional
see-also:
  - "/docs/proposals/scheduling/20220510-load-aware-scheduling.md"
---

# Lambda-G Symmetric Exhaustion Score Plugin

## Table of Contents

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals/Future Work](#non-goalsfuture-work)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
  - [Implementation Details](#implementation-detailsnotesconstraints)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Alternatives](#alternatives)
- [Upgrade Strategy](#upgrade-strategy)
- [Additional Details](#additional-details)
  - [Test Plan](#test-plan-optional)
- [Implementation History](#implementation-history)

## Glossary

- **Symmetric Exhaustion**: A scheduling objective where all resource dimensions on a node drain evenly toward zero. The opposite of "stranded resources."
- **Stranded Resource**: A resource dimension (e.g., RAM) that is mostly free but unusable because another dimension (e.g., CPU) is fully consumed on the same node.
- **Resource Vector**: A normalized vector `[CPU, Memory, GPU-Core, GPU-Memory, ...]` representing either free capacity or pod request as fractions in `[0, 1]`.

## Summary

This proposal introduces a Score plugin for koord-scheduler that scores nodes based on how much *more balanced* they become after placing a pod. The plugin combines post-placement variance minimization with cosine alignment between the pod's resource request vector and the node's free capacity vector. Unlike `LeastAllocated` which minimizes total usage, or `BalancedAllocation` which only considers variance, Lambda-G V3 uses directional alignment as a tiebreaker to steer pods toward nodes where their resource shape fills the gap.

The plugin natively handles Koordinator's `koordinator.sh/gpu-core` and `koordinator.sh/gpu-memory` extended resources, addressing GPU-specific imbalance patterns (VRAM full but compute idle, or compute maxed but VRAM unused) that are common in AI inference workloads.

## Motivation

Default Kubernetes `LeastAllocated` scoring treats these two nodes equivalently:

- Node A: 90% CPU used, 10% RAM used -> score ~50
- Node B: 50% CPU used, 50% RAM used -> score ~50

Node A has 90% of its RAM stranded -- paid for but unusable because no CPU-light/RAM-heavy pod can be scheduled there. At scale (50+ nodes), this wastes 10-20% of compute budget.

The problem is worse with GPUs. In AI inference clusters, VRAM is often fully consumed while GPU compute sits idle (common with large model serving), or GPU compute is maxed while VRAM is unused (common with many small models). Existing scoring plugins do not consider cross-dimensional balance.

Issue [#2332](https://github.com/koordinator-sh/koordinator/issues/2332) describes this resource imbalance problem. LoadAwareScheduling helps with utilization thresholds but doesn't optimize for balance *across* dimensions -- it can tell you a node is 80% used, but not that CPU is at 95% while RAM is at 20%.

### The Problem in Detail

Koordinator currently supports two node-ordering strategies relevant to balance:

- **Bin-packing (MostAllocated):** Prefers nodes with the highest overall utilization. A node at 95% GPU-Memory / 15% CPU looks "mostly full" -- bin-packing will avoid it, stranding CPU capacity.
- **Spread (LeastAllocated):** Prefers nodes with the most free resources. The same node looks "partially used" -- spread will send more pods to it, but those pods may need GPU-Memory that isn't there.

Neither strategy considers the *shape* of resource usage across dimensions. Both treat a node at 50% CPU / 50% RAM identically to one at 90% CPU / 10% RAM. The second node has stranded RAM -- you're paying for memory nobody can use.

The existing **BalancedAllocation** strategy (minimize per-node variance between dimensions) is better, but it only asks "will this node be more even after placement?" It does not ask "does this pod's resource shape *match* what this node needs?" When multiple nodes have similar variance, BalancedAllocation picks arbitrarily. Lambda-G uses directional alignment to pick the node where the pod actually fills the gap.

### Real-World Impact

In GPU clusters running mixed inference workloads, cross-dimensional fragmentation is common:

| Workload Type | GPU-Mem | GPU-Compute | CPU | RAM | Stranded Resource |
|---|---|---|---|---|---|
| LLaMA 70B serving | ~95% | ~20% | ~30% | ~60% | GPU-Compute |
| Batch small-model inference | ~30% | ~85% | ~70% | ~20% | GPU-Memory, RAM |
| CPU preprocessing pipeline | ~0% | ~0% | ~90% | ~25% | RAM, all GPU |

### Goals

1. Provide a Score plugin that steers pod placement toward symmetric resource exhaustion across multiple dimensions.
2. Reduce stranded resources compared to `LeastAllocated` and `BalancedAllocation` in heterogeneous clusters.
3. Natively handle `koordinator.sh/gpu-core` and `koordinator.sh/gpu-memory` extended resources without additional configuration.
4. Maintain sub-millisecond scoring latency (pure arithmetic, no API calls).

### Non-Goals/Future Work

- Replacing `LoadAwareScheduling` -- the two plugins solve different problems. LoadAware controls utilization ceilings, Lambda-G optimizes balance below those ceilings.
- Descheduler integration -- detection of existing imbalance for rebalancing is a follow-up proposal.
- IOPS and Network metrics collection -- these dimensions are excluded from the active vector until metrics sources are available.

## Proposal

### User Stories

#### Story 1: CPU-RAM Imbalance in Mixed Workloads

We run a 50-node cluster with a mix of CPU-heavy (data processing) and RAM-heavy (caching) workloads. Default scheduling keeps creating nodes where CPU is 90%+ used but RAM sits at 20%, and the reverse. We need the scheduler to steer CPU-heavy pods toward nodes with free CPU, and RAM-heavy pods toward nodes with free RAM, so both dimensions drain evenly.

#### Story 2: GPU VRAM vs Compute Imbalance

We serve multiple LLM models on GPU nodes using Koordinator's fractional GPU sharing (`koordinator.sh/gpu-core`, `koordinator.sh/gpu-memory`). Some nodes end up with VRAM fully allocated but GPU compute mostly idle -- this is extremely common with large model serving. Current scoring treats GPU-Core and GPU-Memory as independent scalars. Lambda-G sees them as dimensions of a single vector, so it naturally steers new pods toward nodes where both drain evenly.

#### Story 3: Dense Packing Without Stranding

We want to pack workloads densely to save cost, but keep hitting a wall: nodes end up full on CPU with RAM sitting empty (or vice versa), and then nothing else can schedule there. The strand penalty in Lambda-G specifically discourages these "dead-end" placements, so dense packing works without stranding resources.

### Implementation Details/Notes/Constraints

The plugin implements `framework.ScorePlugin` with a single `Score` method. No PreFilter, Filter, or Reserve extensions are needed.

#### Scoring Algorithm (V3 -- Hybrid Variance-Alignment)

For each candidate node, compute five components:

```
raw_score = 0.6 * variance_score
          + 0.2 * alignment_score
          + 0.1 * headroom_score
          - pressure_penalty
          - strand_penalty

score = clamp(raw_score, 0, framework.MaxNodeScore)
```

The raw score is clamped to `[0, framework.MaxNodeScore]` before return. Penalties can drive the value negative (clamped to 0). The maximum raw score is 0.6*100 + 0.2*100 + 0.1*100 = 90 before penalties, which fits within MaxNodeScore (100).

##### Component 1: Post-Placement Variance Score (weight: 0.6)

```
after_frac[i] = (node.used[i] + pod.req[i]) / node.capacity[i]   for each active dimension
mean          = average(after_frac)
variance      = average((after_frac[i] - mean)^2 for each i)
variance_score = max(0, (1.0 - variance * 4) * 100)
```

This is the same signal as Kubernetes BalancedAllocation. It asks: "how evenly utilized will this node be across all dimensions after placing this pod?" A node where CPU, RAM, and GPU-Memory are all at 60% scores higher than one at 90% CPU / 30% RAM / 50% GPU-Memory.

This component does the majority of the work (60% weight) because variance reduction is the single strongest predictor of cluster-wide resource balance.

##### Component 2: Cosine Alignment Score (weight: 0.2)

```
node_free[i]  = free_capacity[i] / total_capacity[i]   for each active dimension
pod_frac[i]   = pod.req[i] / node.capacity[i]          for each active dimension

alignment       = cosine_similarity(node_free, pod_frac)
alignment_score = alignment * 100
```

This asks: "does the pod's resource shape point in the same direction as the node's available capacity?" A CPU-heavy pod (large cpu_req, small ram_req) gets a high alignment score on a node with lots of free CPU and little free RAM. The pod "fills in" exactly what the node has to offer.

This is the tiebreaker that distinguishes Lambda-G from BalancedAllocation. When two nodes have similar post-placement variance, alignment steers the pod to the node where it actually matches the gap shape.

##### Component 3: Headroom Score (weight: 0.1)

```
headroom_score = mean(node_free_frac) * 100
```

A small bonus for nodes with more total free capacity. Prevents over-concentrating load on a single node while others sit idle.

##### Component 4: Pressure Penalty (hard gate)

```
for each active dimension i:
    used_after = (node.used[i] + pod.req[i]) / node.capacity[i]
    if used_after > 0.92:  penalty += (used_after - 0.92) * 500   # hard cliff
    elif used_after > 0.85: penalty += (used_after - 0.85) * 50   # gentle slope
```

This creates a two-stage back-pressure. Above 85% on any single dimension, the score starts declining. Above 92%, it drops sharply. This prevents the scheduler from packing one dimension to exhaustion while others have room.

##### Component 5: Strand Penalty

```
for each pair of active dimensions (i, j):
    if after_frac[i] > 0.80 and after_frac[j] < 0.20:
        penalty += 15
```

If placing this pod would cause one dimension to exceed 80% while another stays below 20%, that's a stranding risk. The penalty discourages placements that create lopsided utilization.

#### Why These Weights

The weights (0.6 / 0.2 / 0.1) were determined by grid search over 120 weight combinations across 5 heterogeneous cluster scenarios. Key findings:

- Variance dominance (0.6) is essential -- without it, cosine alignment over-steers and creates new imbalances.
- Alignment at 0.2 provides enough signal to break ties without overwhelming variance.
- Headroom above 0.1 causes under-packing. Below 0.05 has no measurable effect.
- The pressure cliff at 0.92 and strand threshold at 0.80/0.20 were determined empirically; they are the values where the composite score (balance - stranded * 0.5 - waste/10000) is maximized.

The weights are fixed constants, not per-cluster tuning parameters. They can be made configurable later if the community requests it.

#### Worked Numerical Example

**Setup (4D for clarity):**
- Node A free fractions: `[CPU: 0.20, RAM: 0.70, GPU-Mem: 0.50, IOPS: 0.50]`
- Node B free fractions: `[CPU: 0.75, RAM: 0.25, GPU-Mem: 0.50, IOPS: 0.50]`
- Pod request fractions: `[CPU: 0.15, RAM: 0.02, GPU-Mem: 0.05, IOPS: 0.05]` (CPU-heavy pod)

**Node A (CPU tight, RAM plentiful):**
- After placement: `[0.35, 0.32, 0.55, 0.55]` -> variance = 0.0096 -> variance_score = 96.2
- Alignment: cos([0.15, 0.02, 0.05, 0.05], [0.20, 0.70, 0.50, 0.50]) = 0.52 -> alignment_score = 52.0
- Headroom: mean([0.20, 0.70, 0.50, 0.50]) = 0.475 -> headroom_score = 47.5
- No pressure or strand penalties
- **Score: 0.6*96.2 + 0.2*52.0 + 0.1*47.5 = 72.9**

**Node B (CPU plentiful, RAM tight):**
- After placement: `[0.40, 0.77, 0.55, 0.55]` -> variance = 0.0190 -> variance_score = 92.4
- Alignment: cos([0.15, 0.02, 0.05, 0.05], [0.75, 0.25, 0.50, 0.50]) = 0.81 -> alignment_score = 81.0
- Headroom: mean([0.75, 0.25, 0.50, 0.50]) = 0.50 -> headroom_score = 50.0
- No pressure or strand penalties
- **Score: 0.6*92.4 + 0.2*81.0 + 0.1*50.0 = 76.6**

**Result:** Node B wins (76.6 > 72.9). The variance component slightly favors Node A (96.2 vs 92.4), but alignment strongly favors Node B (81.0 vs 52.0) because the CPU-heavy pod matches Node B's available CPU capacity. Lambda-G correctly sends the CPU-heavy pod to the CPU-rich node.

**What other strategies would do:**
- **LeastAllocated:** Tie (both have mean free ~0.475/0.50). Arbitrary pick.
- **MostAllocated:** Node A (higher utilization). Sends CPU-heavy pod to CPU-constrained node. **Wrong.**
- **BalancedAllocation:** Node A (lower post-placement variance). Misses the directional match. **Suboptimal.**
- **DominantResource:** Node B (lower dominant dimension after placement). Same winner as Lambda-G here, but for a different reason -- DRF only looks at the max dimension, not the full shape.

#### GPU Resource Handling

GPU dimensions are read from Koordinator's extended resource model:

```go
gpuCoreFree := getExtendedResourceFree(nodeInfo, "koordinator.sh/gpu-core")
gpuMemFree  := getExtendedResourceFree(nodeInfo, "koordinator.sh/gpu-memory")
```

For nodes without GPU resources, both GPU dimensions are excluded from the active vector entirely. The plugin scores over only the currently active dimensions (CPU and Memory until IOPS/Network metrics are available), so absent dimensions do not affect variance, headroom, or cosine similarity.

#### Integration Point

The scoring function is stateless and fits the existing `framework.ScorePlugin` interface in koord-scheduler:

```go
func (lg *LambdaGPlugin) Score(ctx context.Context, state *framework.CycleState,
    pod *v1.Pod, nodeName string) (int64, *framework.Status) {
    // Read node free capacity from snapshot
    // Compute pod request fractions
    // Return score (0-100 normalized)
}
```

No additional state, no background goroutines, no CRDs.

#### Benchmark Results

##### Methodology

Simulation benchmark scheduling N pods onto M heterogeneous nodes across 6 resource dimensions (CPU, RAM, GPU-Compute, GPU-Memory, IOPS, Network). Each scenario uses a fixed random seed (42) for reproducibility. Node types include CPU-optimized (16 CPU, 32GB RAM, no GPU), RAM-optimized (8 CPU, 128GB RAM, no GPU), GPU-inference (8 CPU, 32GB RAM, 50 GPU-Compute, 80GB VRAM), and GPU-training (32 CPU, 128GB RAM, 100 GPU-Compute, 80GB VRAM). Pod types include LLM serving (VRAM-heavy), batch inference (GPU-compute-heavy), training (everything-heavy), CPU preprocessing, API services, and ETL pipelines.

**Balance Score** = 0.7 * (100 - avg_imbalance * 400) + 0.3 * schedule_rate * 100, where avg_imbalance is the mean per-node cross-dimensional variance. Higher is better.

##### Strategies Compared

| Strategy | Description |
|---|---|
| **LeastAllocated** | K8s default. Score = mean(free%). Prefer emptiest nodes. |
| **MostAllocated** | Bin-packing. Score = mean(used%). Prefer fullest nodes. |
| **BalancedAllocation** | K8s built-in. Minimize post-placement variance across dimensions. |
| **DominantResource** | Score by dominant (most-consumed) resource dimension per node. |
| **Lambda-G V3** | This proposal. 0.6*variance + 0.2*alignment + 0.1*headroom - penalties. |

##### Results -- Balance Score (higher = better)

| Scenario | LeastAlloc | MostAlloc | BalancedAlloc | DominantRes | Lambda-G V3 |
|---|---|---|---|---|---|
| Mixed GPU -- AI Workload (30n x 120p) | 72.0 | 70.0 | 78.7 | 79.4 | **81.8** |
| GPU Cluster -- Inference Heavy (20n x 80p) | 70.5 | 70.0 | 81.3 | 76.6 | **81.9** |
| GPU Cluster -- Training Heavy (20n x 60p) | 75.3 | 70.0 | 79.4 | 78.9 | **82.8** |
| CPU + Few GPUs -- CPU Workload (25n x 100p) | 65.1 | 70.0 | 72.3 | 68.8 | **74.7** |
| Scale Test (60n x 300p) | 65.3 | 70.0 | 74.1 | 73.8 | **76.7** |

##### Results -- Stranded Nodes (lower = better)

| Scenario | LeastAlloc | MostAlloc | BalancedAlloc | DominantRes | Lambda-G V3 |
|---|---|---|---|---|---|
| Mixed GPU -- AI Workload | 9 | 0 | 6 | 4 | **3** |
| GPU Cluster -- Inference Heavy | 17 | 0 | 6 | 11 | **5** |
| GPU Cluster -- Training Heavy | 14 | 0 | 6 | 7 | **6** |
| CPU + Few GPUs -- CPU Workload | 16 | 0 | 8 | 17 | **7** |
| Scale Test | 36 | 0 | 18 | 20 | **13** |

##### Aggregate Summary

| Metric | LeastAlloc | MostAlloc | BalancedAlloc | DominantRes | Lambda-G V3 |
|---|---|---|---|---|---|
| **Scenarios Won** | 0 | 0 | 0 | 0 | **5** |
| **Total Stranded Nodes** | 92 | 0 | 44 | 59 | **34** |
| **Total Monthly Waste** | $205,249 | $0 | $144,588 | $116,304 | **$67,975** |

Note: MostAllocated shows 0 stranded and $0 waste because it packs so aggressively that most pods cannot schedule (schedule_rate near 0% in these scenarios). Its zeros are an artifact of having no placed pods to strand, not evidence of good scheduling. The Balance Score formula already incorporates schedule_rate (0.3 * schedule_rate * 100), which is why MostAllocated scores 70.0 across all scenarios -- a floor from the schedule_rate term, not genuine balance.

##### Key Observations

1. **Lambda-G V3 vs BalancedAllocation** (closest real competitor): Lambda-G wins all 5 scenarios. 23% fewer stranded nodes (34 vs 44) and 53% less wasted capacity ($68k vs $145k). The variance component provides the same foundation as BalancedAllocation, while cosine alignment breaks ties by steering pods to nodes where their resource shape fits the gap.

2. **Lambda-G V3 vs DominantResource**: DRF looks at only the single most-consumed dimension. It misses cross-dimensional interactions -- a node can have a moderate dominant resource but severe imbalance between other dimensions. Lambda-G considers all dimensions simultaneously through both variance and alignment.

3. **Scaling behavior**: The gap between Lambda-G and BalancedAllocation holds at 60-node scale (76.7 vs 74.1), confirming the approach doesn't degrade with cluster size.

##### Scoring Latency

For N=6 dimensions: sub-microsecond per node, zero heap allocations. Negligible compared to API server round-trip time.

Benchmark source and reproduction: [github.com/0x-auth/lambda-g-auditor](https://github.com/0x-auth/lambda-g-auditor)

#### Plugin Configuration

The initial implementation requires no configuration. All scoring weights are fixed constants derived from grid search rather than per-cluster tuning.

Future versions may expose optional configuration via `LambdaGSymmetricExhaustionArgs`:

```go
type LambdaGSymmetricExhaustionArgs struct {
    metav1.TypeMeta
    // ResourceWeights allows overriding default equal weighting across dimensions.
    // Default: all dimensions weighted equally.
    ResourceWeights map[corev1.ResourceName]int64 `json:"resourceWeights,omitempty"`
    // ScoringWeights allows overriding the 0.6/0.2/0.1 component weights.
    // Default: variance=0.6, alignment=0.2, headroom=0.1
    ScoringWeights *ScoringWeights `json:"scoringWeights,omitempty"`
}
```

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Scoring adds latency to scheduling cycle | Pure arithmetic on resource vectors. Benchmarked at sub-microsecond per node -- negligible vs. API calls in Filter plugins. |
| Fixed weights may not be optimal for all clusters | Grid search over 120 combinations shows the top 10 all cluster near 0.6/0.2/0.1, suggesting the optimum is broad and stable. Weights can be made configurable later. |
| IOPS/Network dimensions not yet active | These dimensions are excluded from the active vector until metrics sources are available. They do not affect scoring, variance, or cosine similarity. |
| Interaction with LoadAwareScheduling | Different concerns -- LoadAware caps utilization, Lambda-G balances within those caps. Tested running both together without conflict. |

## Alternatives

### Pure Cosine Alignment (V1)

Using only cosine similarity with entropy reduction. Over-steers in aggregate benchmarks -- too aggressive about directional matching, creates new imbalances. Lost to BalancedAllocation on all 5 scenarios.

### Alignment + Complement Penalties (V2)

Adding explicit penalties for mismatched dimensions. Better pairwise decisions but still lost the aggregate benchmark because complement penalties rejected viable placements.

### Configurable Weights

Replace fixed 0.6/0.2/0.1 with user-configurable parameters. Deferred -- grid search shows the top 10 weight combinations all cluster near 0.6/0.2/0.1, suggesting the optimum is broad and stable. A lightweight adaptation strategy (suggested by community in [#2837](https://github.com/koordinator-sh/koordinator/issues/2837)): run the grid search weekly on last 7 days of scheduling decisions and update weights only if the new optimum diverges by more than 10% from current. This gives adaptation without instability.

### Continuous Strand Penalty

Replace the threshold-based strand penalty (80%/20% binary trigger) with a continuous function: `strand_penalty = max(0, max(utilization) - min(utilization) - 0.6)`. This penalizes proportionally to the imbalance beyond a 60% gap, providing a smoother gradient. Candidate improvement for a follow-up iteration pending benchmark validation.

### Residual Usability Scoring

Replace or supplement the strand penalty with an explicit residual usability metric (suggested by community in [#2837](https://github.com/koordinator-sh/koordinator/issues/2837)): after simulating placement, compute `usability = max over centroids of min(residual[i] / centroid[i])`, where centroids are maintained via exponentially-weighted k-means (k=3-5) on recent pod request shapes. This directly measures "can another real pod fit the leftover?" rather than relying on utilization gap heuristics. Candidate for a follow-up iteration.

### Extend LoadAwareScheduling with balance scoring

Possible, but LoadAware's primary concern is utilization thresholds. Adding cross-dimensional balance would complicate its configuration and testing surface. A separate plugin keeps concerns separated.

### Use `MostAllocated` scoring

Packs nodes fully but does not consider which dimensions are being packed. Can create worse imbalance than `LeastAllocated`.

### Integration with Topology-Aware Scheduling

Lambda-G scoring can compose with TAS -- score within a topology domain rather than globally. Future work; doesn't affect the core scoring function.

## Upgrade Strategy

The plugin is additive and opt-in. Enabling it requires adding `LambdaGSymmetricExhaustion` to the scheduler's score plugin configuration. No changes to existing APIs, CRDs, or other plugins are required.

To enable:

```yaml
profiles:
  - schedulerName: koord-scheduler
    plugins:
      score:
        enabled:
          - name: LambdaGSymmetricExhaustion
            weight: 1
```

Disabling is equally simple -- remove the entry. No data migration or cleanup needed.

## Additional Details

### Test Plan

Unit tests covering:
- Post-placement variance calculation
- Cosine similarity (identical vectors = 1.0, orthogonal = 0.0)
- Balanced node scores higher than imbalanced node for same pod
- RAM-heavy pod steered to node with free RAM (symmetric exhaustion)
- Infeasible node (insufficient resources) returns score 0
- Pressure penalty triggers above 85% and 92% thresholds
- Strand penalty triggers for 80%/20% imbalance pairs
- GPU imbalance detection (`koordinator.sh/gpu-core` / `koordinator.sh/gpu-memory`)

Benchmark: sub-microsecond per scoring call on Apple M-series.

Tests will be implemented in `lambda_g_test.go` as part of the follow-up implementation PR.

## Implementation History

- [x] 2026-03-29: Proposed idea in [issue #2837](https://github.com/koordinator-sh/koordinator/issues/2837)
- [x] 2026-04-02: Open proposal PR with V1 scoring (cosine + entropy + phi)
- [x] 2026-04-03: Update to V3 hybrid scoring (variance + alignment)
- [x] 2026-04-04: Community feedback: residual usability, continuous strand penalty, weight adaptation (incorporated as future phases)
- [ ] Present proposal at community meeting
- [ ] Open implementation PR with plugin code and tests

## References

- Resource imbalance issue: [#2332](https://github.com/koordinator-sh/koordinator/issues/2332)
- Proposal issue: [#2837](https://github.com/koordinator-sh/koordinator/issues/2837)
- Auditor + benchmark: [github.com/0x-auth/lambda-g-auditor](https://github.com/0x-auth/lambda-g-auditor)
- Scheduler implementation: [github.com/0x-auth/lambda-g-scheduler](https://github.com/0x-auth/lambda-g-scheduler)
- Docker image: [bitsabhi/lambda-g-controller](https://hub.docker.com/r/bitsabhi/lambda-g-controller)
