---
title: Shared Plugin Cache and Unified Event Dispatcher for Scheduler Profiles
authors:
  - "@mdryaan"
reviewers:
  - "@saintube"
  - "@ZiMengSheng"
creation-date: 2026-06-23
last-updated: 2026-07-02
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
            - [DeviceShare migration example](#deviceshare-migration-example)
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

1. **Reconciliation on informer event.** When an authoritative pod informer event arrives
   for a pod in the assumed set, the cache reconciles rather than skips: it rolls back the
   state `AssumePod` recorded (subtracting the assumed allocation on the assumed node),
   applies the event's authoritative state, and clears the assumed marker. When the event
   and the assumed result agree (the common case) this is a net-zero operation; when they
   diverge — the pod was bound to a different node than Reserve assumed, an admission
   webhook mutated the annotations after Reserve, or the pod was deleted before binding —
   the cache converges on the event's state. Silently skipping the event would leave a
   stale phantom allocation on the assumed node; naïvely re-applying it would double-count
   on the same node. This mirrors how kube-scheduler's `schedulerCache.addPod` reconciles
   an assumed pod against the authoritative informer event rather than skipping it.
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

#### DeviceShare migration example

To validate that `SharedPluginCache` and `CacheReserver` are workable in practice, this
section sketches DeviceShare's `nodeDeviceCache` refactored onto both interfaces. It shows
the full method set, how `Start()` absorbs the informer-handler registration and GC
goroutine, how Reserve and Unreserve integrate with the assume/forget contract, and how
`OnPodAdd`/`OnPodUpdate` handle the assumed-pod case so the informer event does not
double-count Reserve's write.

**Today.** `nodeDeviceCache` is created fresh in each profile's `New()`. Pod and Device
informer handlers are registered independently. A GC goroutine is started per profile.

```go
// pkg/scheduler/plugins/deviceshare/plugin.go — current code (excerpt from New)
deviceCache := newNodeDeviceCache()
registerDeviceEventHandler(deviceCache, extHandle.KoordinatorSharedInformerFactory())
registerPodEventHandler(deviceCache, handle.SharedInformerFactory(),
    extHandle.KoordinatorSharedInformerFactory())
extHandle.RegisterForgetPodHandler(deviceCache.deletePod)
go deviceCache.gcNodeDevice(ctx, handle.SharedInformerFactory(), defaultGCPeriod)
```

**After migration.** `nodeDeviceCache` implements both `SharedPluginCache` and
`CacheReserver`. `New()` registers it via `GetOrRegisterSharedCache`; every profile
after the first receives the same instance. Handler registration and the GC goroutine
move into `Start()`. Reserve and Unreserve gain `AssumePod`/`ForgetPod` calls.

**1) `nodeDeviceCache` implements `SharedPluginCache` + `CacheReserver`:**

```go
// pkg/scheduler/plugins/deviceshare/device_cache.go — after migration

var (
    _ frameworkext.SharedPluginCache = &nodeDeviceCache{}
    _ frameworkext.CacheReserver     = &nodeDeviceCache{}
)

// assumedAllocation snapshots what Plugin.Reserve wrote to the cache for one pod: the
// node the write went to and the allocation itself. Held only until the authoritative
// pod informer event arrives (or Unreserve fires). Used to reconcile the cache when the
// event's pod object diverges from what Reserve assumed (different node, mutated
// annotations, or deletion before binding).
type assumedAllocation struct {
    nodeName    string
    allocations apiext.DeviceAllocations
}

type nodeDeviceCache struct {
    lock            sync.RWMutex
    nodeDeviceInfos map[string]*nodeDevice
    // assumedPods records what Reserve wrote to nodeDeviceInfos, keyed by pod UID. Used
    // by OnPodAdd/OnPodUpdate/OnPodDelete to reconcile with the authoritative informer
    // event: the assumed write is rolled back and the event's state is applied. If the
    // event agrees with Reserve, the operation is net-zero; if it diverges, the cache
    // converges on the event's state.
    assumedPods map[types.UID]*assumedAllocation
    handle      frameworkext.ExtendedHandle
}

func newNodeDeviceCache(handle frameworkext.ExtendedHandle) *nodeDeviceCache {
    return &nodeDeviceCache{
        nodeDeviceInfos: make(map[string]*nodeDevice),
        assumedPods:     make(map[types.UID]*assumedAllocation),
        handle:          handle,
    }
}

// Start implements SharedPluginCache. Called exactly once per scheduler instance, after
// all profiles are built and before the shared informer factory starts.
func (n *nodeDeviceCache) Start(ctx context.Context) {
    // Device CRD handlers stay here — they are plugin-specific and cannot be centralized.
    registerDeviceEventHandler(n, n.handle.KoordinatorSharedInformerFactory())
    // GC goroutine runs once per scheduler instance, not once per profile.
    go n.gcNodeDevice(ctx, n.handle.SharedInformerFactory(), defaultGCPeriod)
}

// OnPodAdd implements SharedPluginCache. Called by the framework's unified dispatcher.
// If the pod was previously assumed, reconciles against the authoritative event rather
// than skipping — see invariant 1 in the Reserve/Unreserve section.
func (n *nodeDeviceCache) OnPodAdd(pod *corev1.Pod) {
    if assumed, ok := n.takeAssumed(pod.UID); ok {
        n.reconcileAssumed(assumed, pod)
        return
    }
    n.updatePod(nil, pod)
}

// OnPodUpdate implements SharedPluginCache. Called by the framework's unified dispatcher.
func (n *nodeDeviceCache) OnPodUpdate(oldPod, newPod *corev1.Pod) {
    if assumed, ok := n.takeAssumed(newPod.UID); ok {
        // Prior state in the cache is Reserve's assumed write, not oldPod's annotations,
        // so reconcile against the assumed snapshot rather than treating oldPod as truth.
        n.reconcileAssumed(assumed, newPod)
        return
    }
    n.updatePod(oldPod, newPod)
}

// OnPodDelete implements SharedPluginCache. Called by the framework's unified dispatcher.
func (n *nodeDeviceCache) OnPodDelete(pod *corev1.Pod) {
    if assumed, ok := n.takeAssumed(pod.UID); ok {
        // Pod was assumed but got deleted before the informer add/update ever landed.
        // Roll back the assumed write; the annotations at this point may not carry the
        // allocation Reserve wrote, so deletePod cannot be trusted to undo it.
        n.rollbackAssumed(assumed, pod)
        return
    }
    n.deletePod(pod)
}

// Node events are no-ops for DeviceShare: nodeDeviceInfos is keyed by node name and
// populated lazily. Stale entries are removed by the GC goroutine's node lister.
func (n *nodeDeviceCache) OnNodeAdd(*corev1.Node)         {}
func (n *nodeDeviceCache) OnNodeUpdate(_, _ *corev1.Node) {}
func (n *nodeDeviceCache) OnNodeDelete(*corev1.Node)      {}

// AssumePod implements CacheReserver. Called by Plugin.Reserve after allocations have
// been written to the per-node cache. Snapshots what Reserve wrote so a later informer
// event or Unreserve can reconcile against it — the naive "just remember the UID"
// approach cannot roll back Reserve's write if the informer event's pod object turns
// out to disagree with what Reserve assumed.
func (n *nodeDeviceCache) AssumePod(pod *corev1.Pod, nodeName string) error {
    info := n.getNodeDevice(nodeName, false)
    if info == nil {
        return fmt.Errorf("nodeDevice for %s is missing when assuming pod %s", nodeName, klog.KObj(pod))
    }
    info.lock.RLock()
    allocations := info.copyPodAllocations(pod) // read what Reserve just wrote from allocateSet
    info.lock.RUnlock()

    n.lock.Lock()
    n.assumedPods[pod.UID] = &assumedAllocation{nodeName: nodeName, allocations: allocations}
    n.lock.Unlock()
    return nil
}

// ForgetPod implements CacheReserver. Called by Plugin.Unreserve. Idempotent — safe to
// call even if AssumePod was never called for this pod. Only clears the assumed marker;
// the actual cache rollback (subtracting the allocation) is handled by Plugin.Unreserve's
// existing updateCacheUsed(..., add=false) call so behavior for non-CacheReserver caches
// is unchanged.
func (n *nodeDeviceCache) ForgetPod(pod *corev1.Pod) error {
    n.lock.Lock()
    defer n.lock.Unlock()
    delete(n.assumedPods, pod.UID)
    return nil
}

// takeAssumed atomically removes and returns the assumed snapshot for uid.
func (n *nodeDeviceCache) takeAssumed(uid types.UID) (*assumedAllocation, bool) {
    n.lock.Lock()
    defer n.lock.Unlock()
    assumed, ok := n.assumedPods[uid]
    if !ok {
        return nil, false
    }
    delete(n.assumedPods, uid)
    return assumed, true
}

// reconcileAssumed rolls back what AssumePod recorded on assumed.nodeName, then applies
// the informer event's authoritative state from pod.Spec.NodeName / pod.Annotations.
// Two cases matter:
//   - Event agrees with Reserve (same node, same allocation): subtract-then-add on the
//     same node is net-zero. The cache is unchanged; only the assumed marker was cleared.
//   - Event diverges (different node, mutated annotations, or empty allocation): the
//     phantom write on assumed.nodeName is removed, and the event's state — which may be
//     on a different node or empty — becomes truth. The cache converges on the event.
func (n *nodeDeviceCache) reconcileAssumed(assumed *assumedAllocation, pod *corev1.Pod) {
    n.rollbackAssumed(assumed, pod)
    n.updatePod(nil, pod)
}

// rollbackAssumed subtracts the assumed allocation from assumed.nodeName. Extracted so
// OnPodDelete (which has nothing to apply afterward) can reuse it.
func (n *nodeDeviceCache) rollbackAssumed(assumed *assumedAllocation, pod *corev1.Pod) {
    if len(assumed.allocations) == 0 {
        return
    }
    info := n.getNodeDevice(assumed.nodeName, false)
    if info == nil {
        return
    }
    info.lock.Lock()
    defer info.lock.Unlock()
    info.updateCacheUsed(assumed.allocations, pod, false)
}
```

Existing methods (`getNodeDevice`, `updatePod`, `deletePod`, `gcNodeDevice`, and the
per-node `nodeDevice.lock` / `updateCacheUsed` path) are unchanged.

**2) `Plugin.New()` becomes profile-independent:**

```go
// pkg/scheduler/plugins/deviceshare/plugin.go — after migration (excerpt from New)
cache := extHandle.GetOrRegisterSharedCache("deviceshare",
    func(h frameworkext.ExtendedHandle) frameworkext.SharedPluginCache {
        return newNodeDeviceCache(h)
    })
deviceCache := cache.(*nodeDeviceCache)
// ...
return &Plugin{nodeDeviceCache: deviceCache, ...}, nil
```

The four registration lines (`registerDeviceEventHandler`, `registerPodEventHandler`,
`RegisterForgetPodHandler`, `go gcNodeDevice`) are gone from `New()` — they now live in
`Start()` and run exactly once per scheduler instance.

**3) `Reserve` and `Unreserve` participate in the assume/forget contract:**

```go
// pkg/scheduler/plugins/deviceshare/plugin.go — after migration
func (p *Plugin) Reserve(ctx context.Context, cs fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
    // ... existing allocation logic (updateCacheUsed(..., add=true) under nodeDevice.lock) ...
    if err := p.nodeDeviceCache.AssumePod(pod, nodeName); err != nil {
        return fwktype.AsStatus(err)
    }
    return nil
}

func (p *Plugin) Unreserve(ctx context.Context, cs fwktype.CycleState, pod *corev1.Pod, nodeName string) {
    // ... existing rollback logic (updateCacheUsed(..., add=false) under nodeDevice.lock) ...
    _ = p.nodeDeviceCache.ForgetPod(pod)
}
```

**What this example validates:**

- `SharedPluginCache`'s method set is sufficient for DeviceShare — pod events flow through
  the dispatcher, Device CRD events stay in `Start()`, node events are no-ops.
- `CacheReserver` closes the assume-window gap without adding methods to
  `SharedPluginCache`. A hypothetical read-only cache simply does not implement it.
- The reconciliation path is small and localized: one snapshot per assumed pod, two
  helpers (`takeAssumed`, `rollbackAssumed`) plus one composed helper (`reconcileAssumed`),
  and a single conditional at the top of `OnPodAdd`/`OnPodUpdate`/`OnPodDelete`. The
  existing `updatePod`/`deletePod` paths are untouched, so plugins that do not implement
  `CacheReserver` are not affected.
- The interfaces fit the existing per-node lock model. No global write lock is introduced;
  `nodeDevice.lock` continues to serialize allocation writes, and the top-level
  `nodeDeviceCache.lock` protects only the node map and the assumed set. `AssumePod`
  releases `nodeDevice.lock` before acquiring `nodeDeviceCache.lock` so lock order matches
  the rest of the cache (top-level first, per-node second) and there is no deadlock.
- The invariants from the previous subsection are visibly upheld: reconciliation on
  informer event (guaranteed by `reconcileAssumed` — rollback-then-apply is net-zero when
  the event agrees, and convergent when it diverges), complete rollback (`ForgetPod`
  clears the marker and `Unreserve`'s existing `updateCacheUsed(..., add=false)` reverts
  the cache write; `OnPodDelete` handles the delete-before-bind case via `rollbackAssumed`),
  and lock consistency (`AssumePod`/`ForgetPod` and every reconcile path acquire
  `nodeDeviceCache.lock` for the assumed set).

Reservation follows the same pattern with its own per-reservation lock. NodeNUMAResource
is analyzed during PR-2 to determine whether its `resourceManager` writes assumed state
during Reserve; if so it implements `CacheReserver`, if not it only implements
`SharedPluginCache`.

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
- 2026-07-01: Address review feedback — add a full DeviceShare migration example demonstrating
  SharedPluginCache + CacheReserver on nodeDeviceCache (Start, event handlers, AssumePod/ForgetPod,
  and Plugin.Reserve/Unreserve integration) to validate the interface shape.
- 2026-07-02: Address review feedback — replace the naive "skip if assumed" strategy in
  OnPodAdd/OnPodUpdate with a reconcile-on-event pattern that handles the case where the
  informer event's pod object differs from what Reserve assumed (different node, mutated
  annotations, or deletion before binding). AssumePod now snapshots (nodeName + allocations)
  rather than just nodeName; reconcileAssumed rolls the snapshot back and applies the
  event's authoritative state. Invariant 1 rewritten from "no double-count" to
  "reconciliation on informer event." Mirrors kube-scheduler's schedulerCache.addPod.
