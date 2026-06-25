---
title: Shared Plugin Cache and Unified Event Dispatcher for Scheduler Profiles
authors:
  - "@mdryaan"
reviewers:
  - "@saintube"
  - "@ZiMengSheng"
creation-date: 2026-06-23
last-updated: 2026-06-25
status: provisional
---

<!-- TOC -->

- [Shared Plugin Cache and Unified Event Dispatcher for Scheduler Profiles](#shared-plugin-cache-and-unified-event-dispatcher-for-scheduler-profiles)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [User Stories](#user-stories)
            - [Story 1 — Per-profile cache duplication wastes resources and causes divergent views](#story-1--per-profile-cache-duplication-wastes-resources-and-causes-divergent-views)
            - [Story 2 — Independent event handlers cause timing races between in-tree and out-of-tree caches](#story-2--independent-event-handlers-cause-timing-races-between-in-tree-and-out-of-tree-caches)
            - [Story 3 — No snapshot causes lock contention and inconsistent reads under high throughput](#story-3--no-snapshot-causes-lock-contention-and-inconsistent-reads-under-high-throughput)
        - [Design Details](#design-details)
            - [SharedPluginCache interface](#sharedplugincache-interface)
            - [Registration and lifecycle](#registration-and-lifecycle)
            - [Initialization order](#initialization-order)
            - [Event dispatch](#event-dispatch)
            - [Reserve and Unreserve](#reserve-and-unreserve)
            - [Plugin migration plan](#plugin-migration-plan)
            - [Snapshot (Phase 3, bonus)](#snapshot-phase-3-bonus)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

# Shared Plugin Cache and Unified Event Dispatcher for Scheduler Profiles

## Summary

Koordinator's out-of-tree scheduler plugins each maintain their own cache of cluster state. When
multiple scheduling profiles are configured, each plugin's `New()` is called once per profile,
creating N independent cache instances that all track the same cluster-wide state. Additionally,
these per-plugin caches register their own informer event handlers independently, which fires
asynchronously from in-tree handlers and from each other, causing brief inconsistencies between
what the in-tree scheduler cache and out-of-tree plugin caches believe about the cluster.

This proposal introduces a `SharedPluginCache` interface that allows plugins to opt into a
single shared cache instance across all scheduler profiles, with lifecycle management and event
dispatch centralized in the framework extender.

## Motivation

The kube-scheduler supports multiple scheduling profiles, each with its own plugin set. Koordinator
builds on this with `FrameworkExtender` and `PluginFactoryProxy`, which call each plugin's `New()`
once per profile. This creates three concrete problems:

**P1 — Per-profile cache duplication.** With N profiles, DeviceShare's `nodeDeviceCache`,
NodeNUMAResource's `resourceManager`/`topologyOptionsManager`, and Reservation's `reservationCache`
are each instantiated N times, all tracking the same cluster-wide state. Since a single scheduler
instance serializes scheduling cycles across profiles, this duplication serves no correctness
purpose — it wastes memory, CPU, and spawns redundant GC goroutines and event handler sets.

**P2 — Event handler timing inconsistency.** Each plugin registers its own informer handlers in
`New()`, independent from the in-tree scheduler cache handlers and from other plugins. When a pod
is deleted, the in-tree cache may already reflect the deletion while a Koordinator plugin is still
processing an update for the same pod. This race causes double-counted device resources, incorrect
quota accounting, and preemption failures.

**P3 — No snapshot (bonus).** In-tree plugins read a per-cycle immutable `NodeInfo` snapshot.
Out-of-tree plugins read live maps protected by `sync.RWMutex`. Under high scheduling throughput
(~200 pods/s), this causes reader lock contention and inconsistent reads within a single scheduling
cycle.

### Goals

1. Define a `SharedPluginCache` interface that plugins can opt into, giving the framework
   structured control over cache lifecycle and event dispatch.
2. Ensure each opted-in plugin cache is created exactly once per scheduler instance and shared
   across all profiles.
3. Route pod and node events to all registered caches through a centralized dispatcher, ensuring
   consistent ordering relative to the in-tree scheduler cache.
4. Migrate DeviceShare, NodeNUMAResource, and Reservation caches onto the new mechanism.
5. No upstream Kubernetes changes — all work stays within `koordinator-sh/koordinator`.
6. Opt-in only — plugins that do not implement `SharedPluginCache` continue to work unchanged.
7. Each phase is independently mergeable.

### Non-Goals/Future Work

- Migrating ElasticQuota (`GroupQuotaManager`) and LoadAware in this phase; they are a follow-up
  after the mechanism is stable.
- Fixing issue #1959 (nominated-pod Nominator) — that is a separate effort that will build on
  top of the shared cache foundation.
- Modifying kube-scheduler's internal event handler ordering or any upstream code.
- Full snapshot implementation for all plugins (P3 targets DeviceShare as a prototype only).

## Proposal

### User Stories

#### Story 1 — Per-profile cache duplication wastes resources and causes divergent views

A cluster operator runs koord-scheduler with three scheduling profiles (e.g., one for
latency-sensitive workloads, one for batch, one for GPU jobs). DeviceShare's `nodeDeviceCache` is
instantiated three times, each listening on the same Device and Pod informers, each running its own
GC goroutine. A `Reserve` call on profile A records a GPU allocation in profile A's cache. Until
the pod informer event arrives (typically milliseconds later), profile B and C caches do not see
the allocation. In a burst-scheduling scenario this assume-window gap allows a pod scheduled by
profile B to be placed on a node that profile A has already fully allocated.

#### Story 2 — Independent event handlers cause timing races between in-tree and out-of-tree caches

When a pod is deleted, the in-tree `schedulerCache` processes the `OnDelete` handler first.
DeviceShare's `OnDelete` handler is registered on the same informer but fires independently — there
is no ordering guarantee. During the window between the two handlers, a scheduling cycle can read
the in-tree cache (pod gone) and the DeviceShare cache (pod still present), resulting in
double-counted device allocations and incorrect scheduling decisions.

#### Story 3 — No snapshot causes lock contention and inconsistent reads under high throughput

At ~200 pods/s, the Filter phase of multiple concurrent scheduling cycles contends on
`nodeDeviceCache`'s per-node `sync.RWMutex` while background informer handlers hold write locks.
Additionally, a single scheduling cycle may read a node's device state at the start of Filter and
again in Score, with a background update arriving in between, causing the cycle to make decisions
based on inconsistent state.

### Design Details

#### SharedPluginCache interface

We introduce a `SharedPluginCache` interface in `pkg/scheduler/frameworkext`. Any plugin whose
cache should be shared across profiles implements this interface. The exact method signatures will
be refined during PR review; the sketch below captures the intended structure:

```go
// SharedPluginCache is implemented by a plugin's cache object when the cache should be shared
// as a single instance across all scheduler profiles. The framework extender manages its
// lifecycle and routes pod/node events to it centrally.
type SharedPluginCache interface {
    // Start initializes background goroutines (e.g., GC, periodic sync).
    // Called exactly once per scheduler instance, before the first scheduling cycle.
    // Plugin-specific informer handlers for non-pod/node resources (e.g., Device CRDs) are
    // registered here, since the framework only dispatches common pod/node events centrally.
    Start(ctx context.Context)

    // The following methods are called by the framework's unified event dispatcher when
    // pod/node events arrive from the shared informer. They replace per-plugin informer handler
    // registration for pod and node events, ensuring consistent ordering.

    OnPodAdd(pod *corev1.Pod)
    OnPodUpdate(oldPod, newPod *corev1.Pod)
    OnPodDelete(pod *corev1.Pod)

    OnNodeAdd(node *corev1.Node)
    OnNodeUpdate(oldNode, newNode *corev1.Node)
    OnNodeDelete(node *corev1.Node)
}
```

The interface is intentionally kept at the common-denominator level (pod and node events). Plugins
that need Koordinator-specific CRD events (e.g., DeviceShare needing `Device` CRD events) register
those handlers themselves inside `Start()`, since those events are plugin-specific and cannot be
centralized in the common interface without breaking the abstraction.

#### Registration and lifecycle

`FrameworkExtenderFactory` maintains a registry of `SharedPluginCache` instances keyed by a
plugin-supplied string. A plugin registers its cache from within `New()`:

```go
// In FrameworkExtenderFactory — registration point available to all plugins via ExtendedHandle.
// If a cache for the given key already exists (registered by an earlier profile), the existing
// instance is returned and create() is not called. This guarantees exactly-once initialization.
// ExtendedHandle is passed to create() so dependencies are explicit at the call site.
// Plugins access informer factories through handle (e.g., handle.KoordinatorSharedInformerFactory(),
// handle.SharedInformerFactory()) rather than receiving them as separate parameters, since
// ExtendedHandle already exposes the full set of shared factories.
func (f *FrameworkExtenderFactory) GetOrRegisterSharedCache(
    key string,
    create func(handle ExtendedHandle) SharedPluginCache,
) SharedPluginCache
```

After all profiles have been initialized (all `New()` calls complete), the factory calls `Start()`
on each registered cache exactly once. This is the point at which GC goroutines are started and
plugin-specific CRD informer handlers are registered.

```go
// Called once by the scheduler after all profiles are built, before scheduling begins.
func (f *FrameworkExtenderFactory) StartSharedCaches(ctx context.Context)
```

Plugin usage in `New()`:

```go
// DeviceShare New()
cache := extHandle.GetOrRegisterSharedCache("deviceshare",
    func(handle frameworkext.ExtendedHandle) frameworkext.SharedPluginCache {
        // handle.KoordinatorSharedInformerFactory() provides Device CRD informers;
        // Device CRD handlers are registered inside Start().
        return newNodeDeviceCache(handle)
    })
p.nodeDeviceCache = cache.(*nodeDeviceCache)
```

No per-plugin wiring in `server.go`. No plugin-specific types in `frameworkext`. Plugins that do
not call `GetOrRegisterSharedCache` are completely unaffected.

#### Initialization order

The lifecycle proceeds in four phases to ensure all handlers are registered before any events
arrive:

1. **Profile construction.** `FrameworkExtenderFactory.NewFrameworkExtender` is called once per
   profile. Each profile's plugins call `New()`, which calls `GetOrRegisterSharedCache`. The first
   call for a given key invokes `create(handle)` and stores the result; subsequent profiles return
   the stored instance. No goroutines are started and no informer handlers are registered yet.

2. **Cache start.** After all profiles are built, `StartSharedCaches(ctx)` is called. This invokes
   `Start(ctx)` on each registered cache exactly once. Inside `Start`, each cache registers its
   plugin-specific CRD informer handlers (e.g., DeviceShare registers its `Device` object handlers)
   and starts background goroutines (GC, periodic sync).

3. **Informer factory start.** The shared informer factory is started by the scheduler framework
   after `StartSharedCaches` returns. From this point, informers begin emitting add/update/delete
   events to all registered handlers, including those registered in phase 2.

4. **Scheduling begins.** The first scheduling cycle runs. The unified dispatcher's pod/node
   handlers (registered once by `FrameworkExtenderFactory` at construction) are already active.

This ordering guarantees that all plugin-specific CRD handlers are registered before the informer
factory starts, eliminating initial-event races where an event arrives before a handler is wired.

#### Event dispatch

The factory's unified dispatcher registers a single set of pod and node informer handlers. When
events arrive, the dispatcher calls the corresponding method on every registered `SharedPluginCache`
in registration order (serial by default; configurable for parallel where safe):

```
Pod informer OnDelete
  └─ dispatcher.OnPodDelete(pod)
        ├─ nodeDeviceCache.OnPodDelete(pod)     // DeviceShare
        ├─ resourceManager.OnPodDelete(pod)     // NodeNUMAResource
        └─ reservationCache.OnPodDelete(pod)    // Reservation
```

This replaces the independent per-plugin informer handler registrations that exist today, ensuring
all Koordinator plugin caches see pod/node events in a defined order. Coordination with the in-tree
scheduler cache leverages existing extension points (scheduler cache, scheduling queue, informers)
without modifying any upstream kube-scheduler code.

#### Reserve and Unreserve

Reserve and Unreserve are scheduling-cycle operations, not informer events, and are not
dispatched centrally through `SharedPluginCache`. However, plugins that write assumed
allocations during Reserve introduce a consistency risk with the shared cache model: an
informer event arriving after pod binding also writes allocation state from pod annotations,
creating a double-count risk if the assumed state is not tracked and cleared. Without an
explicit contract, a Reserve that writes to the cache and an informer event for the same pod
can both apply their allocations, oversubscribing the node.

To address this, we introduce an optional `CacheReserver` interface for caches that modify
state during Reserve. Implementing it is required for any shared cache that touches allocation
state in its plugin's Reserve extension point:

```go
// CacheReserver is an optional interface for SharedPluginCache implementations that
// write assumed allocations during the Reserve scheduling phase. Any shared cache
// modified during Reserve must implement this interface to uphold the assume/forget
// contract and prevent double-counting with informer events.
type CacheReserver interface {
    SharedPluginCache
    // AssumePod marks pod as having an assumed allocation on nodeName. Called after
    // the plugin's Reserve writes allocation state to the cache. The cache must track
    // this pod in an internal assumed set until ForgetPod removes it or an authoritative
    // informer event supersedes it.
    AssumePod(pod *corev1.Pod, nodeName string) error
    // ForgetPod removes the assumed entry for pod. Called on Unreserve to roll back
    // assumed allocation state. Must be idempotent — safe to call even if AssumePod
    // was never called for this pod.
    ForgetPod(pod *corev1.Pod) error
}
```

**Invariants that `CacheReserver` implementations must uphold:**

1. **No double-counting.** `OnPodAdd` (informer event) must check the assumed set before
   applying event-driven state. If the pod is already assumed, the handler merges or replaces
   the assumed state with authoritative event data and calls `ForgetPod` — it does not add a
   second allocation entry.
2. **Complete rollback.** `ForgetPod` must fully revert all state written by `AssumePod` and
   the preceding Reserve, regardless of how far the Reserve phase progressed.
3. **Lock consistency.** The per-node lock that serializes informer event writes must also
   cover `AssumePod` and `ForgetPod`. This prevents a Reserve and an `OnPodAdd` for the same
   node from interleaving.

Of the three initial migration targets, DeviceShare and Reservation write assumed state during
Reserve and must implement `CacheReserver`. NodeNUMAResource requires analysis during migration
to determine whether its `resourceManager` writes assumed state during Reserve.

A key correctness benefit: since Reserve writes to the single shared cache, assumed allocations
are immediately visible across all profiles, eliminating the assume-window gap from Story 1.

`AddPod` and `RemovePod` (used during preemption via `RunFilterPluginsWithNominatedPods`) are
not assumed allocations and do not require the assume/forget contract; they operate directly on
the shared cache under per-node locks.

#### Plugin migration plan

Migration proceeds plugin by plugin. Each migration is independently mergeable:

| Plugin | Cache | Notes |
|---|---|---|
| DeviceShare | `nodeDeviceCache` | First migration; establishes the pattern. `Device` CRD handlers stay in `Start()`. |
| NodeNUMAResource | `resourceManager`, `topologyOptionsManager` | Uses existing `Option` seam in `NewWithOptions`. |
| Reservation | `reservationCache` | Formalizes the existing ad-hoc `reservationCacheMap` global (`reservation_cache.go:37`). |

ElasticQuota (`GroupQuotaManager`) and LoadAware are explicitly deferred to a follow-up phase after
the mechanism proves stable across the three initial plugins.

#### Snapshot (Phase 3, bonus)

As a bonus phase after the shared cache migration is complete, we will add snapshot support.
A `Snapshottable` optional interface extends `SharedPluginCache`:

```go
type Snapshottable interface {
    SharedPluginCache
    // Snapshot returns an immutable point-in-time copy of the cache for use during a
    // single scheduling cycle. The returned snapshot is safe to read concurrently without locks.
    Snapshot() CacheSnapshot
}
```

The framework extender takes a snapshot of each `Snapshottable` cache at the start of each
scheduling cycle, making it available to plugins via the cycle-scoped handle. This eliminates
per-cycle read-lock contention on the live cache.

The primary design challenge is not defining `Snapshot()` itself but designing the snapshot
data structure. A useful snapshot must reflect not only steady-state informer events but also
assumed allocations written by `Reserve` and the AddPod/RemovePod mutations applied during
preemption. This requires a layered state model — a base snapshot taken at cycle start,
extended with assumed allocations for the current cycle — analogous to how kube-scheduler's
`schedulerCache` layers assumed pod state on top of its node snapshots. Designing this
structure, and ensuring it correctly replaces the live cache for all read paths (Filter, Score,
AddPod, RemovePod), is the core work of Phase 3. The full data structure design is deferred to
a separate design document. A DeviceShare prototype will be the first implementation.

## Alternatives

**Per-plugin typed accessors on `ExtendedHandle`** (e.g., `GetNodeDeviceCache() NodeDeviceCache`).
Rejected because it requires `frameworkext` to import plugin-specific types, creating tight
coupling and circular import risks. Every new plugin cache would require a new accessor — the
interface grows unboundedly.

**Purely generic keyed registry** (e.g., `GetOrCreateSharedCache(key string, create func() interface{}) interface{}`).
Rejected because it provides no structure for lifecycle or event dispatch. The framework cannot
call `OnPodAdd` on an `interface{}` — it must know the type. A structured interface is needed for
the centralized dispatcher to work.

**Following the `ReservationCache` or `CrossSchedulerPodNominator` pattern** (explicit `With...`
option + typed accessor per object). Rejected for regular plugin caches because those are special
cases: `CrossSchedulerPodNominator` is not a plugin, and `ReservationCache` requires
framework-level visibility for reservation event handlers. Regular plugin caches do not need
framework-level visibility and should not break the plugin abstraction by appearing as first-class
members of `ExtendedHandle`.

## Implementation History

- 2026-06-23: Initial proposal draft.
- 2026-06-24: Address review feedback — add Reserve/Unreserve discussion, initialization order
  section, explicit handle/factory parameters for GetOrRegisterSharedCache, and snapshot data
  structure design challenge acknowledgment.
- 2026-06-25: Address review feedback — simplify GetOrRegisterSharedCache to pass ExtendedHandle
  only (factory accessible via handle.KoordinatorSharedInformerFactory()); replace Reserve/Unreserve
  direct-call model with explicit CacheReserver optional interface and assume/forget contract.
