---
title: Fragmentation-Aware Rescheduling
authors:
  - "@Vatsalpatni73"
reviewers:
  - "@songtao98"
creation-date: 2026-07-31
last-updated: 2026-07-31
status: implementable
---

# Fragmentation-Aware Rescheduling

## Table of Contents

<!-- TOC -->

- [Fragmentation-Aware Rescheduling](#fragmentation-aware-rescheduling)
    - [Table of Contents](#table-of-contents)
    - [Glossary](#glossary)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [User Stories](#user-stories)
            - [Story 1](#story-1)
            - [Story 2](#story-2)
        - [How It Works](#how-it-works)
            - [Step 1 – Detect Imbalanced Nodes](#step-1--detect-imbalanced-nodes)
            - [Step 2 – Select the Best Eviction Candidate](#step-2--select-the-best-eviction-candidate)
            - [Step 3 – Evict via ReservationFirst](#step-3--evict-via-reservationfirst)
        - [Imbalance Score Calculation](#imbalance-score-calculation)
        - [Interaction with the Scheduler](#interaction-with-the-scheduler)
        - [Configuration Reference](#configuration-reference)
        - [Example Descheduler Configuration](#example-descheduler-configuration)
            - [Minimal Configuration](#minimal-configuration)
            - [Production Configuration](#production-configuration)
        - [Recommended Thresholds](#recommended-thresholds)
    - [Known Limitations](#known-limitations)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

## Glossary

| Term | Definition |
|------|-----------|
| Allocation fraction | `requested / allocatable` for a single resource type on a node. |
| Imbalance score | Population standard deviation of allocation fractions across configured resource types. |
| Gain | Reduction in imbalance score after removing a pod: `stdBefore − stdAfter`. |
| ReservationFirst | Default PodMigrationJob mode; reserves capacity on the destination node before evicting the source pod. |

## Summary

The `FragmentationAware` descheduler plugin detects nodes whose per-resource allocation is imbalanced and selectively evicts the single pod whose removal most effectively reduces that imbalance. Evicted pods are rescheduled by the scheduler's `NodeResourcesBalancedAllocation` scoring plugin, which steers them toward better-balanced nodes.

## Motivation

Resource fragmentation within a Kubernetes node occurs when allocation levels differ significantly between resource types. A node may reach 90% CPU allocation while only 50% of its memory is allocated. Once CPU becomes the limiting resource, no additional workloads can be scheduled despite significant memory headroom. This intra-node imbalance reduces overall cluster utilization.

Koordinator's existing descheduler plugins address workload placement quality (load-aware rescheduling) and bin-packing for scale-down, but none target per-resource allocation imbalance directly.

### Goals

1. Detect nodes where the standard deviation of per-resource allocation fractions exceeds a configurable threshold.
2. Evict one pod per imbalanced node per descheduling cycle — the pod whose removal yields the largest imbalance reduction.
3. Gate eviction on a minimum improvement threshold to avoid unnecessary disruption.
4. Use Koordinator's `ReservationFirst` eviction mode to reserve capacity before evicting the source pod.
5. Cooperate with the scheduler's `NodeResourcesBalancedAllocation` plugin so evicted pods land on better-balanced nodes.

### Non-Goals/Future Work

1. Multi-pod eviction strategies (batch eviction within a single cycle).
2. Real-time utilization awareness — the plugin operates on allocation (requests), not live usage.
3. Automatic tuning of thresholds.
4. Cross-node optimization (global rebalancing).

## Proposal

### User Stories

#### Story 1

A platform team runs a mixed workload cluster. Several nodes have CPUs nearly saturated while memory sits half-empty. The team enables `FragmentationAware` with the default thresholds. On each descheduling cycle, the plugin identifies imbalanced nodes and evicts one CPU-heavy pod per node. The scheduler reschedules those pods to nodes with more balanced resource profiles, freeing CPU headroom on the original nodes and improving overall schedulability.

#### Story 2

A GPU cluster tracks three resource types: `cpu`, `memory`, and `nvidia.com/gpu`. Some nodes have high GPU allocation but low CPU/memory allocation. The team configures `resources: ["cpu", "memory", "nvidia.com/gpu"]` and sets `imbalanceThreshold: 0.20`. The plugin detects GPU-skewed nodes and migrates pods to reduce the three-way allocation imbalance.

### How It Works

The plugin implements the `BalancePlugin` interface and runs during each descheduling cycle. Its operation consists of three steps.

#### Step 1 – Detect Imbalanced Nodes

For each candidate node (filtered by `nodeSelector` if configured), the plugin computes the imbalance score using all pods assigned to the node (including non-evictable pods such as DaemonSet pods and system-critical pods). If the score is at or below `imbalanceThreshold`, the node is skipped.

#### Step 2 – Select the Best Eviction Candidate

Among evictable pods on the node (filtered by `podSelectors`, `evictableNamespaces`, and the framework's own eviction filter), the plugin simulates removing each pod and recomputes the imbalance score. The pod with the highest gain (`stdBefore − stdAfter`) is selected, provided the gain exceeds `minImprovementThreshold`.

When `nodeFit` is enabled (the default), only pods that can be scheduled on at least one other candidate node are considered.

#### Step 3 – Evict via ReservationFirst

The selected pod is evicted through the descheduler framework's `Evictor` interface. When the default `MigrationController` evict plugin is active, eviction creates a `PodMigrationJob` in `ReservationFirst` mode:

1. A `Reservation` is created on a destination node, holding capacity for the evicted pod.
2. Once the Reservation becomes `Available`, the source pod is deleted.
3. The pod's controller creates a replacement that binds to the reserved capacity.

This sequence eliminates the window where evicted workloads compete for scheduling, reducing disruption compared to direct eviction.

### Imbalance Score Calculation

The imbalance score is the **population standard deviation** of allocation fractions across the configured resource types, consistent with the Kubernetes scheduler's `NodeResourcesBalancedAllocation` scoring approach.

**Calculation steps:**

1. For each resource `r` in the configured `resources` list, compute the allocation fraction:

   ```
   fraction(r) = totalRequested(r) / allocatable(r)
   ```

   Resources with zero `allocatable` are skipped to avoid division by zero.

2. Compute the mean of all fractions:

   ```
   mean = sum(fractions) / len(fractions)
   ```

3. Compute the population standard deviation:

   ```
   stddev = sqrt( sum((f - mean)^2 for f in fractions) / len(fractions) )
   ```

**Worked example:**

A node has 4000m CPU allocatable and 8Gi memory allocatable. Pods on the node request a total of 3600m CPU and 2Gi memory.

```
fraction(cpu) = 3600 / 4000 = 0.90
fraction(mem) = 2048 / 8192 = 0.25   (2Gi / 8Gi in MiB)
mean          = (0.90 + 0.25) / 2 = 0.575
variance      = ((0.90 - 0.575)^2 + (0.25 - 0.575)^2) / 2
              = (0.105625 + 0.105625) / 2
              = 0.105625
stddev        = sqrt(0.105625) ≈ 0.325
```

With the default `imbalanceThreshold` of `0.15`, this node qualifies for pod eviction (0.325 > 0.15).

### Interaction with the Scheduler

The `FragmentationAware` plugin handles only the eviction side. For evicted pods to land on better-balanced nodes, the scheduler must score using `NodeResourcesBalancedAllocation`. This is the recommended configuration:

1. **Descheduler**: Enable `FragmentationAware` as a `balance` plugin.
2. **Scheduler**: Enable `NodeResourcesBalancedAllocation` as a `score` plugin in the koord-scheduler profile. This plugin gives higher scores to nodes where adding the pod keeps resource allocation balanced.

Without `NodeResourcesBalancedAllocation` enabled in the scheduler, evicted pods may be rescheduled to equally or more imbalanced nodes, negating the benefit.

### Configuration Reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `paused` | `bool` | `false` | Disables the plugin without removing it from the profile. |
| `dryRun` | `bool` | `false` | Logs eviction decisions without performing eviction. |
| `nodeSelector` | `LabelSelector` | `nil` (all nodes) | Restricts candidate nodes to those matching the selector. |
| `evictableNamespaces` | `{include, exclude}` | `nil` (all namespaces) | Limits which namespaces' pods may be evicted. Only one of `include` or `exclude` may be set. |
| `podSelectors` | `[]FragmentationAwarePodSelector` | `[]` (all pods) | Restricts eviction candidates to pods matching at least one selector. An empty list means all evictable pods are candidates. |
| `nodeFit` | `bool` | `true` | Checks that a candidate pod can fit on at least one other node before evicting. |
| `resources` | `[]ResourceName` | `["cpu", "memory"]` | Resource types used to calculate the imbalance score. Custom and extended resources (e.g., `nvidia.com/gpu`) are supported. |
| `imbalanceThreshold` | `float64` | `0.15` | Minimum imbalance score for a node to be considered for eviction. Must be ≥ 0. |
| `minImprovementThreshold` | `float64` | `0.02` | Minimum gain required to proceed with eviction. Must be ≥ 0. |

### Example Descheduler Configuration

#### Minimal Configuration

Uses all defaults: CPU and memory resources, `imbalanceThreshold: 0.15`, `minImprovementThreshold: 0.02`, `nodeFit: true`.

```yaml
apiVersion: descheduler/v1alpha2
kind: DeschedulerConfiguration
profiles:
  - name: koord-descheduler
    plugins:
      balance:
        enabled:
          - name: FragmentationAware
    pluginConfig:
      - name: FragmentationAware
        args:
          apiVersion: descheduler/v1alpha2
          kind: FragmentationAwareArgs
```

#### Production Configuration

Targets a specific node pool, excludes system namespaces, tracks three resource types, and uses dry-run mode for initial validation.

```yaml
apiVersion: descheduler/v1alpha2
kind: DeschedulerConfiguration
profiles:
  - name: koord-descheduler
    plugins:
      balance:
        enabled:
          - name: FragmentationAware
      evict:
        enabled:
          - name: MigrationController
    pluginConfig:
      - name: FragmentationAware
        args:
          apiVersion: descheduler/v1alpha2
          kind: FragmentationAwareArgs
          dryRun: false
          nodeSelector:
            matchLabels:
              node-pool: compute
          evictableNamespaces:
            exclude:
              - kube-system
              - koordinator-system
          podSelectors:
            - selector:
                matchLabels:
                  app.kubernetes.io/managed-by: "helm"
          nodeFit: true
          resources:
            - cpu
            - memory
            - nvidia.com/gpu
          imbalanceThreshold: 0.20
          minImprovementThreshold: 0.03
      - name: MigrationController
        args:
          apiVersion: descheduler/v1alpha2
          kind: MigrationControllerArgs
          defaultJobMode: ReservationFirst
          maxMigratingPerNode: 1
```

### Recommended Thresholds

| Scenario | `imbalanceThreshold` | `minImprovementThreshold` | Notes |
|----------|---------------------|--------------------------|-------|
| Conservative (production) | `0.20` – `0.25` | `0.03` – `0.05` | Fewer evictions, only corrects pronounced imbalance. |
| Moderate (default) | `0.15` | `0.02` | Balances correction frequency against disruption. |
| Aggressive (dev/test) | `0.08` – `0.10` | `0.01` | Triggers more evictions; useful for validating behavior. |

**Guidance:**

- Start with `dryRun: true` and monitor logs to evaluate eviction candidates before enabling live eviction.
- Lower `imbalanceThreshold` increases the number of nodes eligible for eviction. Lower `minImprovementThreshold` permits evictions with smaller gains.
- When tracking more than two resource types, the standard deviation naturally becomes smaller. Consider lowering both thresholds proportionally.
- Pair with `MigrationController.maxMigratingPerNode: 1` to limit disruption to one migration per node per cycle.

## Known Limitations

1. **Allocation-based, not utilization-based.** The plugin uses pod resource requests (`requests`), not actual runtime utilization. A node may appear balanced by requests but imbalanced by usage (or vice versa). For utilization-based rebalancing, use the load-aware rescheduling plugin.

2. **Single pod eviction per node per cycle.** The plugin evicts at most one pod per node per descheduling cycle. Severely imbalanced nodes may require multiple cycles to converge.

3. **No global optimization.** Each node is evaluated independently. The plugin does not consider whether eviction from node A improves the global cluster balance.

4. **Depends on scheduler cooperation.** Evicted pods are rescheduled by the scheduler. Without `NodeResourcesBalancedAllocation` enabled as a scoring plugin, evicted pods may land on equally or more imbalanced nodes.

5. **Standard deviation sensitivity to resource count.** With only two resource types (the default), stddev can reach a theoretical maximum of 0.5 (one resource at 100%, the other at 0%). With three or more resource types, the maximum stddev decreases. Thresholds should be adjusted when changing the number of tracked resources.

6. **No PDB awareness beyond the framework.** The plugin relies on the descheduler framework's `Filter` and `PreEvictionFilter` interfaces to respect PodDisruptionBudgets. It does not perform independent PDB checks.

## Implementation History

- 2026-07-31: Initial proposal
