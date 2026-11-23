---
title: Sandbox Equivalence Scheduling
authors:
  - "@tan90github"
reviewers:
  - "@saintube"
  - "@ZiMengSheng"
  - "@KunWuLuan"
creation-date: 2026-11-23
last-updated: 2026-11-25
status: provisional
---

# Sandbox Equivalence Scheduling

## Table of Contents

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals/Future Work](#non-goalsfuture-work)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
  - [Design Details](#design-details)
    - [Overview](#overview)
    - [Before Scheduling: Lookup and Validation](#before-scheduling-lookup-and-validation)
    - [After Scoring: Recording the Ranking](#after-scoring-recording-the-ranking)
    - [Cache Management](#cache-management)
    - [Framework Extension](#framework-extension)
  - [Comparison with Upstream OpportunisticBatching](#comparison-with-upstream-opportunisticbatching)
  - [Configuration](#configuration)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Alternatives](#alternatives)
- [Upgrade Strategy](#upgrade-strategy)
- [Implementation History](#implementation-history)

## Summary

This proposal adds a `SandboxEquivalence` plugin to koord-scheduler. Sandbox pods created in bulk from the same template have identical scheduling requirements, so the plugin caches the node ranking produced by one full scheduling cycle and later pods of the same template take nodes straight from it, running Filter on that single node and skipping the full Filter and Score. When the check fails, the pod falls back to full scheduling in the same cycle.

## Motivation

Sandbox pods (AI agent sandboxes, for example) start and stop quickly and come in large numbers, and the platform wants them scheduled as soon as they are created. Sandboxes are normally created in bulk by a controller from a template, so pods of the same batch share identical scheduling inputs: resource requests, affinities, tolerations and so on.

The scheduler runs one scheduling cycle per pod, and every pod filters all nodes and scores the feasible ones. For a batch of sandbox pods from the same template, most of that work is repeated. Treating pods of the same template as one equivalence class and reusing the previous ranking removes the repetition:

```
 Disabled: every pod runs the full computation

   Pod-1  Filter(all N nodes) → Score(feasible nodes) → node-A
   Pod-2  Filter(all N nodes) → Score(feasible nodes) → node-B
   Pod-3  Filter(all N nodes) → Score(feasible nodes) → node-C
   ...

 Enabled: the first pod runs a full cycle and caches the ranking, the rest take nodes from it

   Pod-1  Filter(all N nodes) → Score(feasible nodes) → node-A    cache [B, C, D, ...]
   Pod-2  Filter(node-B)                              → node-B
   Pod-3  Filter(node-C)                              → node-C
   ...
```

### Goals

- Skip the full Filter and Score for a pod of an equivalence class when the cache hits.
- Fall back to full scheduling within the same cycle when a cached node fails validation, so the cache never makes a pod unschedulable.
- Keep the caches of different templates independent when their pods are scheduled interleaved.
- Build on the Koordinator frameworkext extension points without changing upstream interfaces; behavior stays unchanged when the plugin is disabled.

### Non-Goals/Future Work

- Rescoring after a cache hit, i.e. the upstream v1.37 rescoring.
- Incremental cache updates, such as removing only the invalid nodes instead of dropping the whole entry.
- A spread placement policy by default; bin packing can follow.

## Proposal

### User Stories

#### Story 1

As a sandbox platform, I create 5000 pods from the same template in one batch and label them all with the same `sandbox-template-hash`. I want the scheduler to run full scheduling for only a few of them and reuse the node ranking for the rest, which raises scheduling throughput.

#### Story 2

As a sandbox platform, I create pods from different templates for several tenants, and those pods show up interleaved in the scheduling queue. I want each template's ranking cached separately, so that pods of other templates in between do not invalidate it.

#### Story 3

As a cluster administrator, node resources and taints change at any time, and a cached node may no longer be able to host a new pod. I want the pod to still find another node through regular scheduling instead of failing because the cache went stale.

### Design Details

#### Overview

The plugin hooks into three points of the scheduling cycle; the rest of the cycle is unchanged:

```
 PreFilter ──► Filter ──► PreScore ──► Score ──► NormalizeScore ──► Reserve ──► Permit ──► Bind ...
     │                                                  │               │
     ①                                                  ②               ③
```

- ① Before scheduling (PreFilter): identify the equivalence class, look up and validate the cache, and narrow the candidates on a hit.
- ② After scoring (on Score success): store this cycle's node ranking in the cache and evict expired entries.
- ③ Reserve: remove the node that was finally chosen from the cache, so a later pod of the same class does not take it again.

#### Before Scheduling: Lookup and Validation

The lookup has four steps. The first three only read memory and the snapshot; the fourth runs Filter on one node:

```
  Pod enters the scheduling cycle
  │
  ▼
  [1] Equivalence class: does the pod carry the template label?
  │     no  ──► not eligible, run the regular scheduling cycle
  │     yes, key = namespace/label value
  ▼
  [2] Cache: does the class have a cache entry?
  │     no  ──► run the regular scheduling cycle
  │            the same for a pod with NominatedNodeName, the upstream tries that node first
  │     yes
  ▼
  [3] Is the head node A still trustworthy?
  │     · stored less than cacheTTL ago, and the entry is not empty
  │     · A is in the PreFilterResult
  │     · A still exists and its ResourceVersion matches the cached one
  │     no  ──► delete the whole entry of the class, run full scheduling
  │     yes ──► pop A from the cache
  ▼
  [4] Does A pass Filter?
  │     no  ──► fall back to full scheduling in the same cycle
  │     yes
  ▼
  hit: the PreFilterResult narrows to {A}
       only one feasible node is left, so the upstream picks it and skips PreScore and Score
  │
  ▼
  Reserve(A) → Permit → Bind
```

#### After Scoring: Recording the Ranking

After a full cycle that scored successfully, `RecordScoreResult` takes the top `maxCachedNodes` nodes by total score, records the node name, ResourceVersion and store time, and replaces the cache entry of the class. Writing also evicts every expired class.

#### Cache Management

The cache lives in the scheduler process, one per profile. It is read and written only inside the scheduling cycle, which runs serially, so no lock is needed.

```
  key (namespace/hash)      cacheEntry
  ────────────────────      ──────────────────────────────────────────────
  tenant-a/7f9c2       ──►  storedAt = 10:00:00.120
                            nodes    = [A rv=812] [B rv=530] [C rv=977] ...
                                        ▲ head, the next pod takes it first   at most maxCachedNodes
  tenant-a/1d3e8       ──►  storedAt = ...   nodes = [...]
  tenant-b/7f9c2       ──►  independent of tenant-a/7f9c2
```

The following example walks through the cache updates:

| Round | Pod (template) | Scheduling | h1 cache | h2 cache |
| --- | --- | --- | --- | --- |
| 1 | p1 (h1) | no cache, full scheduling, ranking [A,B,C,D,...], picks A | [B,C,D,...] | - |
| 2 | p2 (h1) | hit, takes B | [C,D,...] | - |
| 3 | q1 (h2) | no cache, full scheduling, ranking [X,C,Y,Z,...], picks X | [C,D,...] | [C,Y,Z,...] |
| 4 | p3 (h1) | hit, takes C | [D,...] | [C,Y,Z,...] |
| 5 | q2 (h2) | takes C, but C already hosts p3, Filter(C) fails, falls back to full scheduling, new ranking [Y,Z,W,...], picks Y | [D,...] | [Z,W,...] |
| 6 | p4 (h1) | older than cacheTTL, entry deleted, full scheduling, new ranking [P,Q,R,...], picks P | [Q,R,...] | [Z,W,...] |

Caches of different equivalence classes are not synchronized:

- The same node can appear in the caches of several equivalence classes at once.
- Once one class uses a node, that node is neither removed from nor reordered in the other caches.
- When the node is later handed to a pod of another class, the Filter in step [4] decides whether it still fits. If it does, the pod is bound there, though the ranking may no longer be optimal; if not, the pod falls back to full scheduling.
- No cross-class synchronization is done: it would mean walking every class on each Reserve, and placing a pod on a node also changes the relative ranking of the other nodes, so removing this single node would not make the ranking accurate again.

#### Framework Extension

frameworkext needs two changes, neither of which touches upstream code.

- Change 1: PreferNodes supports multiple plugins (the interface is unchanged, only the way it is called). Only PreferNodes plugins enabled at the `preFilter` extension point take effect; they are called in the `preFilter` order, and the first plugin that does not return Skip decides the preferred node.
- Change 2: a new `ScoreResultRecorder` interface, called by frameworkext after Score succeeds and registered automatically when the plugin is instantiated:

```go
// pkg/scheduler/frameworkext/interface.go

// ScoreResultRecorder is notified with the node scores after the Score phase succeeds.
// The scheduler reorders the scores afterwards, so they must not be retained or modified.
type ScoreResultRecorder interface {
	fwktype.Plugin
	RecordScoreResult(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, scores []fwktype.NodePluginScores)
}
```

### Comparison with Upstream OpportunisticBatching

The upstream [KEP-5598 OpportunisticBatching](https://github.com/kubernetes/enhancements/tree/master/keps/sig-scheduling/5598-opportunistic-batching) also reuses the node ranking of the previous pod of the same kind. It has been Beta and on by default since 1.35. Koordinator's Kubernetes baseline is v1.35.6, so the table also lists the 1.37 implementation:

| Dimension | Upstream v1.35.6 | Upstream v1.37 | SandboxEquivalence |
| --- | --- | --- | --- |
| Equivalence class | Pod signature assembled from each plugin's `SignPod`. A signature is produced only when every PreFilter, Filter, PreScore and Score plugin implements `SignPlugin` and no extender is configured | Same as v1.35 | namespace + the `sandbox-template-hash` label. Applies as soon as the pod carries the label |
| Cache granularity | One per profile; pods with the same signature must be scheduled consecutively | Same as v1.35 | One per equivalence class; classes may be scheduled interleaved |
| Taking a node on a hit | Drops the whole cache entry when the last chosen node can still host the pod | Rescores the last chosen node and puts it back into the candidate list when it can still host the pod | A cached node is removed once it is used, so the next pod takes the next node in the ranking |
| Invalidation | Last cycle failed, signature differs, non-consecutive cycle, older than 500ms, last chosen node missing or still able to host the pod | Last cycle failed, pod has NominatedNodeName, signature differs, non-consecutive cycle, older than 500ms, last chosen or cached node missing, rescoring failed | Older than cacheTTL (500ms by default), head node deleted, node updated, or head node not in the PreFilterResult |
| Validation on a hit | Runs Filter on the hinted node and falls back to full scheduling if it fails | Same as v1.35 | Same |

The upstream feature cannot be used for sandboxes as it stands:

- Koordinator's scheduling plugins do not implement `SignPlugin`, so koord-scheduler produces no signature and the feature is inactive on both v1.35 and v1.37.
- The v1.35 rule "drop the cache once the last chosen node can still host the pod" targets one-pod-per-node placements, such as whole-GPU pods. A sandbox node usually hosts many pods, so the entry would be dropped on almost every cycle.
- v1.37 solves the multi-pod-per-node case with rescoring, but it relies on `RunRawScorePlugins` and `NodePluginScores.RawScores`, which do not exist in v1.35.6. Each profile still keeps a single cache entry, so interleaved templates drop each other's entry.

Caching per equivalence class allows interleaved scheduling and offers more reuse, at the cost of a ranking that can go stale. Filter guarantees the node is feasible, and cacheTTL bounds how long a cached ranking is used.

### Configuration

The plugin is not part of the default profile and has to be enabled explicitly:

1. Enable `SandboxEquivalence` at the `preFilter` and `reserve` extension points of the profile. The score callback is registered by frameworkext automatically, so nothing needs to be configured at the `score` extension point.
2. When SchedulingHint is enabled as well, put it before `SandboxEquivalence` under `preFilter`, so a hint set on the pod takes precedence over the cached ranking.
3. `pluginConfig` can be omitted; the defaults apply when it is.

```yaml
apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration
profiles:
  - schedulerName: koord-scheduler
    plugins:
      preFilter:
        enabled:
          - name: SchedulingHint     # optional, put before SandboxEquivalence when enabled
          - name: SandboxEquivalence
      reserve:
        enabled:
          - name: SandboxEquivalence
    pluginConfig:
      - name: SandboxEquivalence
        args:
          apiVersion: kubescheduler.config.k8s.io/v1
          kind: SandboxEquivalenceArgs
          maxCachedNodes: 100        # nodes cached per equivalence class at most
          cacheTTL: 500ms            # how long an entry is trusted after it is stored
          templateHashLabel: scheduling.koordinator.sh/sandbox-template-hash  # optional
```

Pods of the same template need to carry the same template label, usually generated by the sandbox controller from the template:

```yaml
metadata:
  labels:
    scheduling.koordinator.sh/sandbox-template-hash: <template-hash>
```

Arguments:

- `maxCachedNodes`: defaults to 100. After one full scheduling cycle, the entry can serve at most `maxCachedNodes - 1` further pods of the same class.
- `cacheTTL`: defaults to 500ms, the same cache lifetime as the upstream OpportunisticBatching. Raising it improves the hit rate but makes the ranking staler.
- `templateHashLabel`: the pod label that identifies the equivalence class, defaulting to `scheduling.koordinator.sh/sandbox-template-hash`, so an existing template hash label can be reused as is.

### Risks and Mitigations

- The node is not necessarily the best: scheduling of other pods within cacheTTL changes node state, and the caches of different equivalence classes are not synchronized (see [Cache Management](#cache-management)). Filter only guarantees the node is feasible, not that it is still the best.
- Fallback cost: when nodes change frequently the hit rate drops, every miss adds one single-node Filter, and the worst case degrades to full scheduling.
- Label misuse: different templates that share a hash share a ranking. Filter still checks feasibility, but the scoring preference may not match, so platforms should generate the label per template.
- The cache lives in memory only: after a scheduler restart or a leader change pods are scheduled fully again, which does not affect correctness.

## Alternatives

- Use the upstream OpportunisticBatching directly: the plugin signature and multi-pod-per-node limits have to be solved first, see [the comparison above](#comparison-with-upstream-opportunisticbatching).
- Override the upstream `GetNodeHint` / `StoreScheduleResults`: a smaller change, but it alters the semantics of upstream batching and adds maintenance cost on later upgrades.
- Port the v1.37 rescoring: v1.35.6 lacks `RunRawScorePlugins` and `NodePluginScores.RawScores`; revisit after the Kubernetes baseline is upgraded.
- A background goroutine that periodically evicts expired entries: eviction does not affect correctness, since `hintable` checks the TTL on every call. A periodic sweep would make the cache concurrently accessed, so the hot paths would need a lock, and it also adds goroutine lifecycle and leader-change handling. The current design evicts while writing the cache, so it only runs when there is scheduling traffic, at the cost of keeping expired entries when the scheduler is idle.

## Upgrade Strategy

This proposal changes no Kubernetes API or CRD. The plugin is disabled by default and takes effect only when explicitly enabled, so existing scheduling behavior is unchanged by default.

## Implementation History

- [x] 11/18/2026: Design discussion
- [x] 11/23/2026: Initial proposal
