---
title: Native Gang Scheduling Compatibility
authors:
  - "@aclfe"
reviewers:
  - TBD
creation-date: 2026-06-26
last-updated: 2026-06-26
status: provisional
see-also:
  - "/docs/proposals/scheduling/20220901-gang-scheduling.md"
---

# Native Gang Scheduling Compatibility

## Table of Contents
 
- [Native Gang Scheduling Compatibility](#native-gang-scheduling-compatibility)
  - [Table of Contents](#table-of-contents)
  - [Glossary](#glossary)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
  - [Proposal](#proposal)
    - [Concept Mapping](#concept-mapping)
    - [Compatibility Matrix](#compatibility-matrix)
    - [Coexistence Strategy](#coexistence-strategy)
      - [Ownership Resolution](#ownership-resolution)
      - [Gang Source Abstraction](#gang-source-abstraction)
      - [Internal-Engine Convergence](#internal-engine-convergence)
    - [Preemption Forward-Compatibility](#preemption-forward-compatibility)
    - [Koordinator-only Extensions](#koordinator-only-extensions)
    - [Risks and Mitigations](#risks-and-mitigations)
  - [Migration and Deprecation Planning](#migration-and-deprecation-planning)
  - [Alternatives](#alternatives)
  - [Implementation History](#implementation-history)
  - [References](#references)

## Glossary

- **Native Gang**: the upstream `GangScheduling` plugin introduced in Kubernetes 1.35,
  driven by the `scheduling.k8s.io/v1alpha1` `Workload` API. Two feature gates are
  involved, both alpha and off by default: `GenericWorkload` gates the `Workload` API and
  the `pod.Spec.WorkloadRef` field (`core/v1` `WorkloadReference`, tagged
  `+featureGate=GenericWorkload`); `GangScheduling` gates the gang plugin's enforcement
  (and requires `GenericWorkload` as a prerequisite -- `GangScheduling: {GenericWorkload}`
  in `kube_features.go`). A pod is gang-scheduled natively only when **both** are enabled.
- **Koord Gang**: Koordinator's existing `Coscheduling` plugin, driven by the PodGroup
  CRD (`scheduling.sigs.k8s.io`), the lightweight `pod-group.scheduling.sigs.k8s.io/*`
  labels, and `gang.scheduling.koordinator.sh/*` annotations.
- **Workload**: upstream object grouping up to 8 `PodGroup`s; the analog of a Koord GangGroup.
- **PodGroup (native)**: a named member of a Workload with a `Basic` or `Gang{MinCount}` policy.
- **GangGroup**: Koordinator concept coordinating all-or-nothing across multiple PodGroups.
- **DisruptionMode**: KEP-5710 `+union` struct proposed on the decoupled `PodGroupSpec`
  (not present in the 1.35 alpha types in the dependency tree) selecting whether a
  PodGroup's pods can be preempted independently (`Single`, default) or only together
  (`All`).
- **OpportunisticBatching**: an upstream scheduler feature gate
  (`OpportunisticBatching`, alpha, off, present in `kube_features.go@v1.35.2`) that lets
  the scheduler batch scheduling attempts across a workload's pods. It is orthogonal to
  gang enforcement and out of scope here; noted only because it ships alongside the
  `GangScheduling` gate and is easy to conflate with it.
- **WorkloadManager**: the upstream scheduler backend
  (`pkg/scheduler/backend/workloadmanager`) that is the central source of truth for a
  gang's *runtime* pod state (all / unscheduled / assumed / assigned) and per-group
  scheduling deadline. The `Workload` API object itself carries no such state.

## Summary

Kubernetes 1.35 ships a native gang-scheduling stack (`Workload` API +
`GangScheduling` plugin) that overlaps conceptually with Koordinator's `Coscheduling`
plugin but is a completely separate code path and API surface. The upstream API is
alpha and still moving quickly (the `Workload` shape was reworked again for 1.36, and
workload-aware preemption only arrives in 1.36 via KEP-5710). Per the community
decision on [#2856](https://github.com/koordinator-sh/koordinator/issues/2856),
Koordinator will **not** adopt the alpha API into production now.

This proposal is therefore a design-only document. It defines (1) a concept and
compatibility mapping between Koord Gang and Native Gang, (2) a coexistence strategy
that lets both run in one scheduler without interfering, (3) the changes that keep
Koordinator's gang preemption forward-compatible with KEP-5710 without
depending on the alpha API today, and (4) the gap analysis and migration plan that
gate any real integration on the upstream API reaching Beta.

## Motivation

After the k8s 1.35.2 dependency bump ([#2822](https://github.com/koordinator-sh/koordinator/issues/2822)),
the native `Workload` types, the `GangScheduling` plugin, and the `GangScheduling` /
`OpportunisticBatching` feature gates are all present in the vendored dependency but
unused by Koordinator. The umbrella [#2851](https://github.com/koordinator-sh/koordinator/issues/2851)
tracks aligning Koordinator with these capabilities.

The risk of doing nothing is divergence: if Koordinator continues to evolve its gang
semantics in isolation, adopting the native API later (at Beta) becomes a breaking,
high-cost migration. The risk of adopting now is churn against an unstable alpha API.
This proposal threads the two by aligning the internal model and preemption semantics
now, while deferring any wire-level dependency on the alpha API.

### Goals

- Define a stable concept mapping between Koord Gang and Native Gang.
- Define a coexistence strategy so both plugins can be enabled in one koord-scheduler
  without double-gating or conflicting on the same pod.
- Make Koordinator's gang preemption forward-compatible with KEP-5710 (PodGroup-level
  priority, minimize-unnecessary-preemption) at the semantic level, and correctly scope
  out upstream concepts that don't map onto Koord's model (e.g. `DisruptionMode`).
- Produce a gap analysis classifying every Koord gang feature as `bridgeable`,
  `koord-only`, or `blocked-on-upstream`.
- Define the migration and deprecation plan, explicitly gated on the upstream API
  reaching Beta.

### Non-Goals/Future Work

- Implementing a `Workload`-sourced gang path in production code (deferred to Beta).
- Enabling the `GangScheduling` / `OpportunisticBatching` feature gates by default.
- Any change to the default behavior of the existing Coscheduling plugin.
- Migrating existing PodGroup CRD users; the CRD path remains first-class indefinitely.

## Proposal

### Concept Mapping

![native gang concept mapping](/docs/images/native-gang-concept-mapping.svg)

| Concept | Koord Gang | Native Gang (1.35) |
|---|---|---|
| Gang identity | `pod-group.scheduling.sigs.k8s.io/name` label, `gang.scheduling.koordinator.sh/*` annotations, or PodGroup CRD | `pod.Spec.WorkloadRef{Name, PodGroup, PodGroupReplicaKey}` |
| Membership owner | PodGroup CRD / annotations | `Workload.spec.podGroups[]` (max 8) |
| Min quorum | `min-available` / `gang.scheduling.koordinator.sh/min-available` | `PodGroupPolicy.Gang.MinCount` (per PodGroup) |
| Cross-group coordination | GangGroup (N PodGroups, all-or-nothing) | Workload (<= 8 PodGroups) |
| Replica fan-out | not modeled explicitly | `WorkloadRef.PodGroupReplicaKey` (policy applied per replica; deprecated in 1.36) |
| Gate mechanism | `PreEnqueue` + `PreFilter` reject + `Permit` wait | `PreEnqueue` gate + `Reserve` count + `Permit` wait |
| Preemption | `PostFilter`, GangGroup-aware, strict/non-strict | none in 1.35; KEP-5710 in 1.36 |
| Controller ref | owner via labels | `WorkloadSpec.ControllerRef` |

Two structural mismatches matter most:

1. **Workload caps at 8 PodGroups; GangGroup does not.** A Koord GangGroup that spans
   more than 8 PodGroups has no native representation.
2. **`PodGroupReplicaKey` has no Koord analog.** Native applies the gang policy
   independently per replica key; Koord has no per-replica subdivision today.

A Koord GangGroup of two gangs maps to a native Workload with two PodGroups:

```yaml
# Gang A: ps pods, Gang B: worker pods
apiVersion: v1
kind: Pod
metadata:
  name: ps-0
  labels:
    pod-group.scheduling.sigs.k8s.io/name: gang-a
  annotations:
    gang.scheduling.koordinator.sh/groups: '["gang-a", "gang-b"]'
spec:
  schedulerName: koord-scheduler
  containers: ...
```

```yaml
# Upstream native way (proposed for Beta+)
apiVersion: scheduling.k8s.io/v1alpha1
kind: Workload
metadata:
  name: training-job
  namespace: default
spec:
  podGroups:
    - name: ps
      policy:
        gang:
          minCount: 2
    - name: worker
      policy:
        gang:
          minCount: 4
---
apiVersion: v1
kind: Pod
metadata:
  name: ps-0
  namespace: default
spec:
  schedulerName: koord-scheduler
  workloadRef:
    name: training-job
    podGroup: ps
  containers: ...
```

The concrete API surface (`Workload{Spec{ControllerRef, PodGroups[]}}`, `PodGroup{Name,
Policy}`, `PodGroupPolicy{Basic|Gang}`, `GangSchedulingPolicy{MinCount}`,
`WorkloadMaxPodGroups = 8`, `pod.Spec.WorkloadRef{Name, PodGroup, PodGroupReplicaKey}`) is
not reproduced here -- it is directly readable in the vendored `k8s.io/api@v0.35.2`, and
zero Go files in the project import it today (`pkg/descheduler/informers/generic.go` is
the only file importing `k8s.io/api/scheduling/v1alpha1`, and only for the unrelated
`PriorityClass` informer).

What matters for this proposal is that the API is mid-rework, concretely, not
hypothetically: the 1.36 decoupling proposal ("Decoupled PodGroup and Workload API",
implemented in 1.36) renames `WorkloadSpec.PodGroups` to `PodGroupTemplates`, renames
`Policy` to `SchedulingPolicy`, introduces a separate runtime `PodGroup` object, and
deprecates `PodGroupReplicaKey` -- while the reference-vs-inline policy decision for that
runtime object is still open upstream. This is exactly why no proposal here should bind
Koordinator's core to today's 1.35 field names: any future integration point must isolate
`Workload`/`PodGroup` access behind a single seam (see
[Gang Source Abstraction](#gang-source-abstraction)), and the concrete adapter code isn't
worth writing until the shape stops moving.

### Compatibility Matrix

| Koord feature | Classification | Notes |
|---|---|---|
| PodGroup CRD gang | bridgeable | Maps to a single-PodGroup Workload |
| Annotation/lightweight-label gang | bridgeable | Maps to a single-PodGroup Workload |
| `MinCount` quorum | bridgeable | Direct field mapping |
| GangGroup (<= 8 members) | bridgeable | Maps to a multi-PodGroup Workload |
| GangGroup (> 8 members) | blocked-on-upstream | Exceeds `WorkloadMaxPodGroups` |
| Strict / non-strict mode | koord-only | No native analog. Governs *scheduling-cycle rollback of not-yet-bound pods*; KEP-5710 `DisruptionMode` governs *disruption of already-running pods* -- a different axis, not an equivalence. See [Preemption Forward-Compatibility](#preemption-forward-compatibility). |
| Gang match policy (`only-waiting` / `waiting-and-running` / `once-satisfied`) | koord-only | Client-facing gang protocol; selects which pod states count toward quorum. `GangSchedulingPolicy.MinCount` has no equivalent notion, so a bridged gang would silently lose match-policy semantics. |
| Gang wait time | partial / mismatch | Koord `WaitTime` is per-gang and annotation-configurable; upstream `WorkloadManager.SchedulingTimeout()` is a fixed 5-minute deadline. A bridged gang cannot preserve a custom wait time. |
| GangGroup preemption invalidation | blocked-on-upstream | Native preemption unit is a single PodGroup; multi-PodGroup units await `CompositePodGroup` (kubernetes/enhancements#6012) |
| Network topology (`FindOneNode`) | koord-only | No upstream path |
| Reservation integration (`PreBindReservation`) | koord-only | No upstream path |
| ElasticQuota interaction | koord-only | No upstream path |
| `PodGroupReplicaKey` | do-not-bridge | No Koord analog, and deprecated upstream in 1.36 |

### Coexistence Strategy

The two plugins must never both gate the same pod. Coexistence is resolved per pod by a
single ownership rule, behind a feature gate (`NativeGangCompatibility`, alpha, off).

![native gang coexistence and ownership resolution](/docs/images/native-gang-coexistence.svg)

#### Ownership Resolution

For each pod, exactly one gang owner is selected:

1. If `pod.Spec.WorkloadRef != nil` **and both** the `GenericWorkload` and `GangScheduling`
   feature gates are enabled, the pod is owned by the Native Gang plugin. Koord Coscheduling
   treats it as a non-gang pod (skip). Both gates are required: `GenericWorkload` is what
   makes the apiserver persist `WorkloadRef` and what enables the Workload informer, while
   `GangScheduling` is what makes the native plugin actually enforce the gang. With only the
   former, the field is set but never gang-scheduled; with only the latter, the field is
   always nil. A more robust implementation keys off the native plugin being present in the
   active scheduler profile rather than reading the gates directly.
2. Otherwise, if the pod carries any Koord gang identity (label/annotation/PodGroup),
   it is owned by Koord Coscheduling, exactly as today.
3. A pod carrying both a `WorkloadRef` and Koord gang identity is a configuration error;
   `WorkloadRef` wins and a warning event is emitted. Validation may reject this in a
   webhook later.

This rule is intentionally one-directional and additive: with the feature gate off
(the default), rule 1 never fires and behavior is byte-for-byte identical to today.

A clarification on where this rule lives. Koord does **not** dispatch between an
"annotation source" and a "PodGroup-CRD source" per pod: `GangCache`
(`pkg/scheduler/plugins/coscheduling/core/gang_cache.go`) builds a single `Gang` object
keyed by gang name, and the annotation config and PodGroup CRD are *complementary inputs
that co-initialize that same `Gang`* (`onPodAddInternal` calls `tryInitByPodConfig` only
when the pod carries no `PodGroupLabel`; otherwise the PodGroup CRD drives init). The
natural insertion point for a Workload source is therefore the **`GangCache` / informer
layer** -- a new informer feeding gangs from `Workload`/`WorkloadManager` events -- not a
per-pod resolver bolted onto `core.Manager`. The ownership rule below is a filter applied
during that ingestion: it decides whether Koord builds a `Gang` for the pod at all.

The rule as pseudo-code (`HasPlugin` is illustrative; the real check inspects the active
profile via `handle.ListPlugins()`):

```go
// pseudo-code
type GangOwner int

const (
    GangOwnerNone   GangOwner = iota
    GangOwnerKoord
    GangOwnerNative
)

func resolveGangOwner(pod *corev1.Pod, handle framework.Handle) GangOwner {
    // Native Gang wins if pod carries WorkloadRef AND the native plugin is
    // present in the active scheduler profile.
    if pod.Spec.WorkloadRef != nil && nativeGangPluginEnabled(handle) {
        if extension.IsGangPod(pod) {
            // misconfiguration: both native and Koord gang identity present;
            // native wins, emit a warning event.
            emitMisconfigEvent(pod, "pod has both WorkloadRef and Koord gang identity")
        }
        return GangOwnerNative
    }
    if extension.IsGangPod(pod) { // extension.IsGangPod exists today
        return GangOwnerKoord
    }
    return GangOwnerNone
}
```

When `resolveGangOwner` returns `GangOwnerNative`, `GangCache` skips building a Koord
`Gang` for the pod entirely -- the pod's gang state is owned by the upstream
`WorkloadManager`, never tracked by both. Both plugins still register `PreEnqueue` /
`Reserve` / `Permit` / `Unreserve` and both fire on a `WorkloadRef` pod, so Koord
Coscheduling's hooks must early-return for `GangOwnerNative` pods -- including the
`PreFilter` reject in the "Gate mechanism" row of [Concept Mapping](#concept-mapping) --
so the native plugin's own `Reserve`/`Permit` gate is the only one acting on that pod.

#### Gang Source Abstraction

The mechanism is a source-agnostic `GangInfo` contract that `GangCache` populates,
following the abstraction spirit of the workload auditor
([#2872](https://github.com/koordinator-sh/koordinator/pull/2872)) and the nextPod
abstraction ([#2417](https://github.com/koordinator-sh/koordinator/pull/2417)). There are
two source *families*, not three interchangeable ones:

- The existing **Koord gang** source, built by `GangCache` from the annotation config and
  the PodGroup CRD *together* (they co-initialize one `Gang`; they are not per-pod
  alternatives). This is unchanged.
- A new **Workload** source, `workloadGangInfo` (**design only**, compiled behind the
  feature gate, not wired into the default profile until Beta).

`workloadGangInfo` is the single seam where the alpha API touches Koordinator. Isolating it
means a breaking upstream API change (such as the 1.36 `Workload` rework) is contained to
one file. But the seam has two distinct data sources that must not be conflated:

1. **Static config** (gang identity, group membership, `MinCount`) comes from the
   `Workload` API object via the lister.
2. **Runtime state** (which pods exist, and whether quorum is met) comes from the upstream
   `WorkloadManager` backend, *not* from the `Workload` object -- `Workload` is spec-only
   in 1.35 (no `Status`) and carries no pod state at all. The upstream `GangScheduling`
   plugin itself reads this via `handle.WorkloadManager().PodGroupInfo(...)`
   (`k8s.io/kube-scheduler/framework`), keyed by
   `(namespace, workloadName, podGroupName, replicaKey)`.

The contract must also express the **GangGroup**, because a single gang is not Koord's
scheduling unit -- the GangGroup (N gangs, all-or-nothing) is. This is not a new concept to
design: Koordinator already has it, as `core.GangGroupInfo` (`ganggroup.go`), tracked by
`GangCache.gangGroupInfoMap` and consumed by Permit/PostFilter today. `GangInfo` just needs
an accessor onto it:

```go
// GangInfo abstracts one gang's identity, membership, and quorum, regardless of source.
type GangInfo interface {
    Identity() string
    Members() []*corev1.Pod
    MinQuorum() int32
    Group() *GangGroupInfo // existing type (ganggroup.go), not a new one
}
```

A future `workloadGangInfo` would populate `Identity`/`MinQuorum` from the `Workload`
object and `Members`/quorum-satisfaction from `WorkloadManager`, and register itself into
`GangGroupInfo` via the existing `GangCache.getGangGroupInfo(groupID, siblingIDs, true)` --
the same path annotation/PodGroup-CRD gangs already use. **A Workload maps to exactly one
GangGroup**: its `podGroups[]` (up to 8) are the sibling gangs. Two Koord GangGroup shapes
have no single-Workload representation and stay `blocked-on-upstream`: a GangGroup with
more than 8 members, and a GangGroup used as a *preemption* unit (native's preemption unit
is a single PodGroup).

The exact `workloadGangInfo` struct is intentionally not specified further. Per the
community decision on [#2856](https://github.com/koordinator-sh/koordinator/issues/2856),
Koordinator isn't adopting the alpha API now, and the 1.36 rework (`PodGroups` ->
`PodGroupTemplates`, a new runtime `PodGroup` object) means code written against the 1.35
shape today would need rewriting before it could ship -- so there is little value in
designing it further until the upstream shape stabilizes at Beta. The commitment this
proposal makes now is narrower and durable regardless of that churn: keep the `Workload`
API isolated behind this one `GangInfo` seam, and never let a pod be tracked by both
`GangCache` and `WorkloadManager` at once.

![native gang single-seam isolation](/docs/images/native-gang-seam-isolation.svg)

#### Internal-Engine Convergence

A related question raised in review: could Koordinator's own gang engine (`GangCache`,
`GangGroupInfo`, the scheduling-cycle rollback in `gang_context.go`/`core.go`) evolve
toward the upstream `WorkloadManager`/`WorkloadSchedulingCycle` factoring? Plausibly, at
Beta or later, and only for that internal state model -- it does not extend to the
client-facing gang protocol, the preemption algorithm, the network-topology algorithm
(`FindOneNode`), or the `WorkloadAuditor`; those stay Koord's own regardless (see
[Preemption Forward-Compatibility](#preemption-forward-compatibility) and
[Koordinator-only Extensions](#koordinator-only-extensions)). `WorkloadSchedulingCycle`
itself is a 1.36 addition, not in the 1.35 dependency tree, so there is nothing to converge
against yet -- this is recorded as a direction, not a commitment.

### Preemption Forward-Compatibility

This is the area the maintainers flagged as most important, because upstream preemption
semantics (KEP-5710, alpha 1.36) introduce API concepts Koordinator must not diverge from.
The field shapes below are taken from KEP-5710 and are **not** present in the 1.35 tree in
the dependency graph (`scheduling.k8s.io/v1alpha1` PodGroup has only `Name` + `Policy`);
they are alpha and subject to change, so this section aligns on semantics, not on exact
field names. The preemption algorithm itself stays Koord-native (a maintainer directive);
this section aligns *inputs and invariants*, not the algorithm.

Koordinator's `PostFilter` supports GangGroup-aware preemption with strict and non-strict
modes. Note this is opt-in: it is gated behind `EnablePreemption` (default off) and is
recent (job-level preemption, [#2622](https://github.com/koordinator-sh/koordinator/pull/2622)).

One thing this section deliberately does *not* do: map Koord strict/non-strict mode onto
KEP-5710's `DisruptionMode` (`Single`/`All`). An earlier draft proposed that analogy; a
reviewer correctly flagged that they aren't equivalent. Strict/non-strict governs
*scheduling-cycle rollback of not-yet-bound pods*; `DisruptionMode` governs *disruption of
already-running, already-bound pods* -- a later lifecycle stage entirely, so a gang can be
strict yet `Single`, or non-strict yet `All`. There's no mapping to make, so it's classified
`koord-only` in the [Compatibility Matrix](#compatibility-matrix) and not discussed further
here.

The forward-compatible alignment that *does* apply, achievable today without the alpha
API, is:

1. **PodGroup-level priority (a behavior change, called out as such).** KEP-5710 adds
   `PriorityClassName *string` plus a derived `Priority *int32` to `PodGroupSpec` (and
   `PodGroupTemplate`). When set, the PodGroup priority is authoritative and the individual
   Pod priority is ignored; Beta will reject a PodGroup whose pods diverge from the group
   priority. Koord's `PostFilter` today selects victims by *per-pod* priority
   (`corev1helpers.PodPriority` in `preemption.go`). Deriving an effective gang-level
   priority across the GangGroup and using it in victim selection would change that
   behavior -- it is **not** a no-op. Because the maintainer directive is to keep the
   preemption algorithm consistent with the current approach, this proposal records the
   requirement and its behavioral impact but does not adopt it now; it is a Beta-gated
   change to be validated against real workloads, not an unconditional alignment.
2. **GangGroup as the preemption unit.** Native preemption keeps the preemption unit no
   larger than the scheduling unit, which is a single PodGroup; Koord's GangGroup spans
   several. The invariant to preserve: if any member of a GangGroup is preempted, the whole
   group's scheduling cycle is invalidated. This stays `PostFilter`-enforced and is
   documented as a Koord-only superset. The upstream path for a multi-PodGroup preemption
   unit is the future `CompositePodGroup` concept (kubernetes/enhancements#6012); until it
   lands, GangGroup-level preemption has no native equivalent and must not be routed to the
   native path.
3. **Avoiding unnecessary preemption.** KEP-5710 (not KEP-4671, which is the native gang
    plugin KEP itself) treats minimizing preemption as a primary goal: a preemption
    nomination can turn out invalid if the whole PodGroup later fails to schedule, so for
    alpha the upstream default-preemption path is disabled to avoid binding on an
    unnecessary disruption. This is enforced via the `DelayedPreemption` feature gate
    (alpha 1.36, [kubernetes#137203](https://github.com/kubernetes/kubernetes/pull/137203)).
    Koordinator already defers work to the gang cycle; this proposal records the
    requirement that gang preemption must not fire on the first unschedulable member when
    later members may invalidate the nomination, keeping it aligned with the upstream
    "minimize preemptions" model.

None of the three requires the alpha API. Items 2 and 3 are invariants the existing
`PostFilter` already honors and that this proposal records so they are not lost; item 1
is explicitly *not* adopted now -- a behavior change deferred to Beta. The goal is to
keep the eventual switch to native preemption tractable, not to claim it is already a
behavioral no-op.

### Koordinator-only Extensions

These have no upstream equivalent and remain on the CRD/annotation path regardless of
native adoption. They are explicitly out of any bridge-and-migrate flow:

- GangGroup cross-gang dependencies (beyond 8 PodGroups, and as a preemption unit).
- Network-topology-aware gang placement (`FindOneNode`).
- Reservation integration (`PreBindReservation`).
- ElasticQuota interaction.
- `WorkloadAuditor` ([#2872](https://github.com/koordinator-sh/koordinator/pull/2872)):
  Koord-specific gang audit/observability. It stays on the Koord path and is not aligned
  to upstream (a maintainer named it explicitly among the layers to keep consistent).
- Gang match policy (`only-waiting` / `waiting-and-running` / `once-satisfied`).

The design must guarantee that a pod using any of these features is never silently routed
to the native path. The guardrail is enforced at the `GangCache` ingestion point: before
`resolveGangOwner` may return `GangOwnerNative`, the candidate is checked against these
Koord-only markers, and any match forces `GangOwnerKoord`. Two of the checks require
group-level information and so depend on the `Group()` accessor added above -- GangGroup
size `> 8` and GangGroup-as-preemption-unit are only knowable after the group is assembled
in the cache, not from a single pod. The remaining checks (topology / reservation / quota
annotations, match-policy) are per-pod and can be evaluated at ingestion directly.

### Risks and Mitigations

- **Alpha API churn.** Mitigated by isolating all `Workload` access in
  `workloadGangInfo` and compiling it behind a default-off feature gate.
- **Double gating.** Mitigated by the single-owner resolution rule; default-off means no
  behavior change until explicitly enabled.
- **Silent loss of Koord-only semantics.** Mitigated by the ownership rule routing any
  pod that needs GangGroup / topology / reservation / quota features to Koord.
- **Preemption divergence.** Mitigated by keeping the preemption algorithm Koord-native and
  recording the KEP-5710 invariants (GangGroup-as-unit, minimize unnecessary preemption)
  that `PostFilter` already honors. Strict/non-strict is classified koord-only (not mapped
  to `DisruptionMode`), and gang-level priority is deferred to Beta as a flagged behavior
  change rather than adopted now.
- **Double state tracking.** Mitigated by making `WorkloadManager` authoritative for
  native-owned pods and having Koord `GangCache` skip them entirely, so no pod is counted
  by two state machines.

## Migration and Deprecation Planning

Staged, and explicitly gated on upstream maturity. Note "upstream Beta" is not a single
milestone: KEP-4671 (gang scheduling) and KEP-5710 (workload-aware preemption) are both
`stage/beta` targeting 1.37. The decoupled PodGroup API (PodGroupTemplates + v1beta1 group)
landed as alphav2 in 1.36; the runtime `PodGroup` object may reach Beta later. The stages
below gate on whichever surface a given integration depends on.

1. **Now (alpha upstream):** land this design and the concept mapping; align preemption
    semantics in `PostFilter`; keep `workloadGangInfo` as design/skeleton only.
2. **KEP-4671 Beta / KEP-5710 Beta (~1.37):** wire `workloadGangInfo` against the
    stabilized `Workload`/`PodGroupTemplate` shape, into an opt-in profile behind the
    `NativeGangCompatibility` gate; publish a compatibility test suite; document the
    coexistence rule for cluster operators. Bind against whichever surface reaches Beta
    first -- the decoupled PodGroupTemplate will land at `scheduling.k8s.io/v1beta1`;
    the runtime `PodGroup` object may trail. Do not depend on the runtime object until
    it also reaches Beta.
3. **Upstream GA + Koord validation:** offer a migration guide (PodGroup CRD ->
    Workload) and a conversion tool for the bridgeable subset; mark the bridgeable Koord
    surface as "compatibility mode" but do not deprecate the CRD path while Koord-only
    features depend on it.

No deprecation of the PodGroup CRD is proposed. It remains the only path for the
Koord-only extensions.

## Alternatives

- **Adopt the alpha API now.** Rejected per [#2856](https://github.com/koordinator-sh/koordinator/issues/2856):
  the API is unstable (reworked again in 1.36) and would impose ongoing breaking-change
  maintenance.
- **Do nothing until Beta.** Rejected: it risks semantic divergence (especially in
  preemption) that makes the eventual migration breaking. This proposal does the
  zero-dependency alignment now and defers only the wire-level integration.
- **Translate PodGroup CRD <-> Workload via a controller.** Deferred to the migration
  phase; it does not address preemption alignment, which is the urgent part.

## Implementation History

- 2026-06-26: initial design draft.

## References

- **Upstream Gang Scheduling plugin** (vendored):
  `k8s.io/kubernetes/pkg/scheduler/framework/plugins/gangscheduling/gangscheduling.go`
  (`v1.35.2`). Implements `PreEnqueue`, `Reserve`, `Permit`, `Unreserve`. No `PostFilter`
  or preemption logic. Uses `pl.handle.WorkloadManager().PodGroupInfo()` for runtime pod
  state (assumed/assigned/unscheduled sets) and `pl.workloadLister` (`Scheduling().V1alpha1().Workloads()`)
  for the Workload API object.
- **Upstream WorkloadManager** (vendored):
  `k8s.io/kubernetes/pkg/scheduler/backend/workloadmanager/`. Central source of truth
  for Workload pod state; driven explicitly by scheduler event handlers. `PodGroupInfo`
  tracks `allPods`, `assumedPods`, `assignedPods`, `unscheduledPods` per
  `(namespace, workloadName, podGroupName, replicaKey)`. Has a `SchedulingTimeout()` of 5
  minutes. Called by both the GangScheduling plugin and the framework.
- **Workload API types** (vendored): `k8s.io/api/scheduling/v1alpha1@v0.35.2`. `Workload`
  has `Spec` only (no `Status`). `PodGroup` has `Name` + `Policy`. `PodGroupPolicy` has
  `Basic` | `Gang`. `GangSchedulingPolicy` has `MinCount`. `WorkloadMaxPodGroups = 8`.
  `core/v1.WorkloadReference` has `Name` + `PodGroup` + `PodGroupReplicaKey` (no
  `PodGroupKind`).
- **Feature gates** (vendored): `k8s.io/kubernetes/pkg/features/kube_features.go@v1.35.2`.
  `GenericWorkload` (alpha, off), `GangScheduling` (alpha, off, requires
  `GenericWorkload`). `GangScheduling` depends on `GenericWorkload` as a prerequisite
  (`GangScheduling: {GenericWorkload}`).
- **KEP-4671**: Gang Scheduling using Workload Object. Alpha 1.35 (api PR
  [kubernetes#134564](https://github.com/kubernetes/kubernetes/pull/134564), plugin PR
  [kubernetes#134722](https://github.com/kubernetes/kubernetes/pull/134722)). Alphav2
  1.36: decoupled PodGroup API ([enhancements#5893](https://github.com/kubernetes/enhancements/pull/5893)),
  Workload Scheduling Cycle ([kubernetes#136618](https://github.com/kubernetes/kubernetes/pull/136618)),
  DelayedPreemption feature gate ([kubernetes#137203](https://github.com/kubernetes/kubernetes/pull/137203)).
  Beta target 1.37 ([enhancements#6076](https://github.com/kubernetes/enhancements/pull/6076)).
- **KEP-5710**: Workload-aware preemption. Alpha 1.36 (feature gate
  [kubernetes#137200](https://github.com/kubernetes/kubernetes/pull/137200), core impl
  [kubernetes#137606](https://github.com/kubernetes/kubernetes/pull/137606), API fields
  [kubernetes#136589](https://github.com/kubernetes/kubernetes/pull/136589)). Beta target
  1.37 (PodGroupPostFilter extension point
  [kubernetes#139674](https://github.com/kubernetes/kubernetes/pull/139674), priority
  validation [kubernetes#139920](https://github.com/kubernetes/kubernetes/pull/139920),
  in-place filter reprieval
  [kubernetes#139980](https://github.com/kubernetes/kubernetes/pull/139980)).
- **CompositePodGroup API** (future multi-PodGroup scheduling/preemption unit):
  kubernetes/enhancements#6012. Alpha 1.37 (feature gate
  [kubernetes#139407](https://github.com/kubernetes/kubernetes/pull/139407), core API
  [kubernetes#139596](https://github.com/kubernetes/kubernetes/pull/139596)).
- **1.36 decoupling proposal**: "Decoupled PodGroup and Workload API" (status: implemented
  alphav2 in 1.36). Renames `PodGroups` to `PodGroupTemplates`, `Policy` to
  `SchedulingPolicy`, introduces a runtime `PodGroup` object and a
  `WorkloadReference.PodGroupKind` discriminator, deprecates `PodGroupReplicaKey`.
  The group moves to `scheduling.k8s.io/v1beta1` at Beta. The reference-vs-inline
  policy decision for the runtime object is still open upstream.
- **Koordinator coscheduling**: `/docs/proposals/scheduling/20220901-gang-scheduling.md`
  (original design). `pkg/scheduler/plugins/coscheduling/core/` (current implementation).
  Relevant PRs: job-level preemption [#2622](https://github.com/koordinator-sh/koordinator/pull/2622),
  workload auditor [#2872](https://github.com/koordinator-sh/koordinator/pull/2872),
  nextPod abstraction [#2417](https://github.com/koordinator-sh/koordinator/pull/2417),
  network-topology coscheduling [#2638](https://github.com/koordinator-sh/koordinator/pull/2638),
  gang match-policy support [#1380](https://github.com/koordinator-sh/koordinator/pull/1380).
- **Koordinator issues**: [#2856](https://github.com/koordinator-sh/koordinator/issues/2856)
  (Native Gang), umbrella [#2851](https://github.com/koordinator-sh/koordinator/issues/2851).
