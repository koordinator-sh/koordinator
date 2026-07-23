---
title: ScaleDownBinPack Descheduler Plugin
authors:
  - "@Vatsalpatni73"
reviewers:
  - "@songtao98"
  - "@ZiMengSheng"
creation-date: 2026-07-23
last-updated: 2026-07-23
status: implementable
---

# ScaleDownBinPack Descheduler Plugin

## Table of Contents

- [ScaleDownBinPack Descheduler Plugin](#scaledownbinpack-descheduler-plugin)
  - [Table of Contents](#table-of-contents)
  - [Summary](#summary)
  - [Motivation](#motivation)
  - [How It Works](#how-it-works)
    - [Algorithm Overview](#algorithm-overview)
    - [Node Scoring (Evacuation Score)](#node-scoring-evacuation-score)
    - [Pod Ranking (Intra-Node)](#pod-ranking-intra-node)
    - [Global Ranking](#global-ranking)
  - [Strategies](#strategies)
    - [CalculateOnly (Recommended)](#calculateonly-recommended)
    - [EvictDirectly](#evictdirectly)
  - [Configuration Reference](#configuration-reference)
  - [Examples](#examples)
    - [CalculateOnly with Pod Deletion Cost](#calculateonly-with-pod-deletion-cost)
    - [EvictDirectly with maxPodsToEvict](#evictdirectly-with-maxpodstoevict)
  - [Operational Notes](#operational-notes)

## Summary

The ScaleDownBinPack plugin is a Balance-type descheduler plugin that improves
cluster packing during workload scale-down. When a workload (e.g., a Deployment)
reduces replicas, the default Kubernetes behavior terminates pods without
considering the impact on overall node utilization. This can leave many nodes
partially occupied, preventing the cluster autoscaler from reclaiming them.

ScaleDownBinPack addresses this by ranking target pods so that removal
concentrates surviving pods onto fewer nodes. More nodes become fully idle and
eligible for scale-down, reducing resource fragmentation and infrastructure cost.

## Motivation

In a cluster where a Deployment is scaled from 10 replicas to 7, Kubernetes
selects 3 pods for deletion. The default selection may scatter deletions across
many nodes, leaving each partially utilized. The cluster autoscaler cannot remove
a node unless all its pods are gone or relocatable.

ScaleDownBinPack solves this by choosing which pods to remove so that remaining
pods pack tightly onto fewer nodes. Nodes that lose all their target pods become
empty and eligible for removal.

## How It Works

### Algorithm Overview

1. **Constraints Pre-Filtering**: Nodes with zero target pods are skipped. Target
   pods that cannot be legally evicted (e.g., those protected by topology spread
   constraints, pod anti-affinity, or high-availability requirements) are marked as
   *skipped* and excluded from the evacuation ranking.

2. **Node Scoring (Tier 1)**: Each candidate node receives an Evacuation Score
   based on its non-target workload burden (the "Non-Target Tax").

3. **Pod Ranking (Tier 2)**: Within each node, target pods are sorted by resource
   size and creation timestamp.

4. **Global Ranking**: Nodes and their pods are flattened into a single ordered
   list. Each pod receives a sequentially increasing integer rank starting at 0.

### Node Scoring (Evacuation Score)

For each node *j*, the Evacuation Score quantifies how burdened the node is with
non-target workloads:

```
Score(j) = Σ(w_r × U_nt_r) / Σ(w_r × U_t_r)
```

Where:
- `U_nt_r = NT_r / C_r` — fraction of allocatable resource *r* consumed by
  non-target pods (including skipped target pods).
- `U_t_r = T_r / C_r` — fraction of allocatable resource *r* consumed by
  eligible target pods.
- `w_r` — configurable weight for resource *r* (default: 1.0).

**Interpretation**:
- **Score = 0**: The node has no non-target pods. Evacuating its target pods
  makes the node fully empty — an ideal candidate.
- **Score > 0**: The node carries non-target workload. Higher scores mean the
  node cannot be fully emptied even after all target pods are removed.

Nodes with lower Evacuation Scores are prioritized: evacuating them first
maximizes the chance of creating fully idle nodes.

### Pod Ranking (Intra-Node)

When the scale-down budget is exhausted partway through a node's pods, the
intra-node sort maximizes capacity reclamation:

1. **Largest pod first** — pods with a higher weighted resource size are ranked
   earlier.
2. **Newest pod first (LIFO)** — among equal-size pods, the one created most
   recently is ranked first, aligning with the Kubernetes default intuition.
3. **Namespace, then Name** — deterministic tiebreaker.

### Global Ranking

The final ranked list is produced by iterating through sorted nodes and their
sorted pods, assigning monotonically increasing integer ranks (0, 1, 2, …).

**Rank 0** is the first pod to delete; **Rank N−1** is the last.

## Strategies

### CalculateOnly (Recommended)

The default and recommended strategy for production use.

ScaleDownBinPack patches each target pod's
`controller.kubernetes.io/pod-deletion-cost` annotation with the pod's computed
rank. **A lower deletion cost means the pod is deleted first** during a
scale-down event. Native Kubernetes controllers (Deployment, ReplicaSet,
StatefulSet) natively respect this annotation: when choosing victims, they delete
pods with the lowest deletion cost first.

| Pod type | Annotation value |
|---|---|
| Eligible target pod (rank *k*) | `controller.kubernetes.io/pod-deletion-cost: "k"` |
| Skipped target pod | `controller.kubernetes.io/pod-deletion-cost: "2147483647"` (MaxInt32) |

**Skipped target pods** are target pods that match the configured `podSelectors`
but cannot be legally evicted (e.g., due to PDB constraints or topology spread
requirements). They receive the maximum possible deletion cost (`2147483647`) so
that native controllers delete them only as a last resort.

This is the true *scale-down* path: the plugin does not terminate pods itself.
It annotates pods to guide the native controller's victim selection during an
actual replica reduction. Because the controller manages the lifecycle,
terminated pods are not recreated beyond the new replica count.

### EvictDirectly

ScaleDownBinPack actively evicts pods in rank order (starting from rank 0) up to
`maxPodsToEvict`.

> **Warning**: EvictDirectly is active deletion. If the workload controller's
> desired replica count has not been reduced, deleted pods are recreated
> immediately. Use EvictDirectly only when you are simultaneously reducing
> replicas, or when you intentionally want to force pod redistribution (e.g., to
> trigger rescheduling onto fewer nodes). For a clean scale-down, use
> CalculateOnly.

## Configuration Reference

```yaml
apiVersion: descheduler/v1alpha2
kind: ScaleDownBinPackArgs
```

| Field | Type | Default | Description |
|---|---|---|---|
| `paused` | `bool` | `false` | Disables the plugin when `true`. |
| `strategy` | `string` | `CalculateOnly` | `CalculateOnly` or `EvictDirectly`. |
| `maxPodsToEvict` | `int32` | — | Required when `strategy: EvictDirectly`. Maximum pods to evict per cycle. |
| `nodeSelector` | `LabelSelector` | — | Restricts evaluation to matching nodes. |
| `podSelectors` | `[]PodSelector` | — | Restricts target pods to those matching at least one selector. When empty, all pods are targets. |
| `evictableNamespaces` | `Namespaces` | — | Include or exclude namespaces (mutually exclusive). |
| `resources` | `[]ResourceName` | `[cpu, memory]` | Resources considered in scoring. |
| `resourceWeights` | `map[ResourceName]float64` | `{cpu: 1.0, memory: 1.0}` | Per-resource weight in the scoring formula. |

### PodSelector

| Field | Type | Description |
|---|---|---|
| `name` | `string` | Human-readable identifier. |
| `selector` | `LabelSelector` | Standard Kubernetes label selector. |

### Namespaces

| Field | Type | Description |
|---|---|---|
| `include` | `[]string` | Namespaces to include. |
| `exclude` | `[]string` | Namespaces to exclude. Cannot be combined with `include`. |

## Examples

### CalculateOnly with Pod Deletion Cost

This configuration enables ScaleDownBinPack in `CalculateOnly` mode. The plugin
periodically evaluates cluster state and patches target pods with the
`controller.kubernetes.io/pod-deletion-cost` annotation. When you scale down a
Deployment, the ReplicaSet controller deletes pods with the lowest cost first,
achieving optimal node consolidation.

```yaml
apiVersion: descheduler/v1alpha2
kind: DeschedulerConfiguration
deschedulingInterval: 30s
dryRun: false
profiles:
- name: koord-descheduler
  plugins:
    balance:
      enabled:
      - name: ScaleDownBinPack
    deschedule:
      disabled:
      - name: "*"
    evict:
      disabled:
      - name: "*"
      enabled:
      - name: MigrationController
  pluginConfig:
  - name: MigrationController
    args:
      apiVersion: descheduler/v1alpha2
      kind: MigrationControllerArgs
      evictionPolicy: Eviction
      namespaces:
        exclude:
        - kube-system
        - koordinator-system
      evictQPS: "10"
      evictBurst: 1
  - name: ScaleDownBinPack
    args:
      apiVersion: descheduler/v1alpha2
      kind: ScaleDownBinPackArgs
      # CalculateOnly is the default. The plugin patches pods with
      # controller.kubernetes.io/pod-deletion-cost but does not evict them.
      strategy: CalculateOnly
      # Target only pods matching this selector.
      podSelectors:
      - name: my-app
        selector:
          matchLabels:
            app: my-app
      # Exclude system namespaces from evaluation.
      evictableNamespaces:
        exclude:
        - kube-system
        - koordinator-system
      # Resources considered in the evacuation score formula.
      # Default is [cpu, memory] with equal weights.
      resources:
      - cpu
      - memory
      resourceWeights:
        cpu: 1.0
        memory: 1.0
```

**Usage**:

1. Apply the descheduler configuration above.
2. The descheduler periodically computes evacuation ranks and patches pods.
3. Scale down the workload:
   ```bash
   kubectl scale deployment my-app --replicas=7
   ```
4. The ReplicaSet controller selects the 3 pods with the lowest
   `controller.kubernetes.io/pod-deletion-cost` values for termination.
5. These are the pods whose removal best consolidates remaining pods onto fewer
   nodes, maximizing idle-node candidates for the cluster autoscaler.

### EvictDirectly with maxPodsToEvict

This configuration enables ScaleDownBinPack in `EvictDirectly` mode. The plugin
actively evicts up to `maxPodsToEvict` pods per descheduling cycle, starting with
the pods whose removal most improves node packing.

```yaml
apiVersion: descheduler/v1alpha2
kind: DeschedulerConfiguration
deschedulingInterval: 30s
dryRun: false
profiles:
- name: koord-descheduler
  plugins:
    balance:
      enabled:
      - name: ScaleDownBinPack
    deschedule:
      disabled:
      - name: "*"
    evict:
      disabled:
      - name: "*"
      enabled:
      - name: MigrationController
  pluginConfig:
  - name: MigrationController
    args:
      apiVersion: descheduler/v1alpha2
      kind: MigrationControllerArgs
      evictionPolicy: Eviction
      namespaces:
        exclude:
        - kube-system
        - koordinator-system
      evictQPS: "10"
      evictBurst: 1
  - name: ScaleDownBinPack
    args:
      apiVersion: descheduler/v1alpha2
      kind: ScaleDownBinPackArgs
      # EvictDirectly actively evicts pods instead of annotating them.
      strategy: EvictDirectly
      # Required for EvictDirectly. Maximum pods to evict per cycle.
      maxPodsToEvict: 3
      # Target only pods matching this selector.
      podSelectors:
      - name: batch-job
        selector:
          matchLabels:
            app: batch-processor
      # Restrict evaluation to specific nodes.
      nodeSelector:
        matchLabels:
          node-pool: scale-down-eligible
      # Exclude system namespaces from evaluation.
      evictableNamespaces:
        exclude:
        - kube-system
        - koordinator-system
      # Weight CPU higher than memory in the evacuation score.
      resources:
      - cpu
      - memory
      resourceWeights:
        cpu: 2.0
        memory: 1.0
```

> **Important**: Because EvictDirectly actively deletes pods, the workload
> controller recreates them unless you have already reduced the desired replica
> count. To perform a true scale-down, either reduce replicas before or at the
> same time as the descheduler runs, or use `CalculateOnly` instead.

## Operational Notes

1. **Lower deletion cost means deleted first.** The
   `controller.kubernetes.io/pod-deletion-cost` annotation is an integer. Native
   Kubernetes controllers (Deployment, ReplicaSet) sort candidate pods by this
   value in ascending order and delete the lowest-cost pods first. Rank 0 is
   deleted first; rank *N−1* is deleted last.

2. **Skipped target pods receive the maximum deletion cost.** Target pods that
   match the `podSelectors` but cannot be evicted (due to PDB, topology spread
   constraints, or anti-affinity rules) are assigned a deletion cost of
   `2147483647` (MaxInt32). This ensures they are selected for deletion only as
   an absolute last resort.

3. **Target pod counts come from the descheduler cache.** The list of target pods
   on each node is obtained via `handle.GetPodsAssignedToNodeFunc()`, which reads
   from the descheduler's cached pod state. This cache is periodically
   synchronized with the API server. The plugin does not query the API server
   directly for pod listings; the cached view determines which pods exist, their
   node assignments, and their resource requests.

4. **CalculateOnly is the true scale-down path.** The `CalculateOnly` strategy
   annotates pods to guide the native controller's victim selection. Because the
   controller manages pod termination as part of a replica count reduction,
   terminated pods are not recreated. This is the recommended mode for
   production scale-down workflows.

5. **EvictDirectly is active deletion and may recreate pods.** The
   `EvictDirectly` strategy calls the descheduler's evictor, which terminates
   pods immediately. If the workload controller's desired replica count has not
   been reduced, evicted pods are rescheduled and recreated. Use this mode only
   when replica reduction is already in progress or when forced redistribution is
   the explicit goal.

6. **Do not modify the default deployment configuration.** The example configs
   above show plugin-specific configuration. The base descheduler deployment
   (`config/manager/descheduler-config.yaml`) should not be altered for
   ScaleDownBinPack. Add the plugin to a custom profile or an existing profile's
   plugin list.
