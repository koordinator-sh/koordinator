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
  (not present in the vendored 1.35 alpha types) selecting whether a PodGroup's pods can
  be preempted independently (`Single`, default) or only together (`All`).

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
- Make Koordinator's gang preemption forward-compatible with KEP-5710 (`DisruptionMode`,
  PodGroup-level priority, minimize-unnecessary-preemption) at the semantic level.
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

The concrete API surface as vendored in `k8s.io/api@v0.35.2` (present in the project
dependency tree but unused by Koordinator today):

```go
// scheduling.k8s.io/v1alpha1 (vendored, unused by Koordinator)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Workload struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec WorkloadSpec `json:"spec"` // no Status in 1.35
}

type WorkloadSpec struct {
    ControllerRef *TypedLocalObjectReference `json:"controllerRef,omitempty"`
    PodGroups     []PodGroup                 `json:"podGroups"` // max 8
}

type PodGroup struct {
    Name   string          `json:"name"`
    Policy PodGroupPolicy  `json:"policy"`
}

type PodGroupPolicy struct {
    Basic *BasicSchedulingPolicy `json:"basic,omitempty"`
    Gang  *GangSchedulingPolicy  `json:"gang,omitempty"`
}

type BasicSchedulingPolicy struct{} // intentionally empty

type GangSchedulingPolicy struct {
    MinCount int32 `json:"minCount"`
}

const WorkloadMaxPodGroups = 8
```

```go
// core/v1 (pod.Spec.WorkloadRef)

// +featureGate=GenericWorkload
// +optional
WorkloadRef *WorkloadReference `json:"workloadRef,omitempty"`

type WorkloadReference struct {
    Name               string `json:"name"`
    PodGroup           string `json:"podGroup"`
    PodGroupReplicaKey string `json:"podGroupReplicaKey"`
}
```

The only Go file that currently imports `k8s.io/api/scheduling/v1alpha1` in the
project is `pkg/descheduler/informers/generic.go`, and it uses the group solely for
the `PriorityClass` informer -- not for `Workload` or `PodGroup`. Zero Go files in
the project import the upstream `GangScheduling` plugin or `WorkloadManager`.

Also note the API is mid-rework, and the rework is concrete, not hypothetical. In 1.35 a
PodGroup is an inline member of `Workload.spec.podGroups[]` with a `PodGroupPolicy`. The
1.36 decoupling proposal ("Decoupled PodGroup and Workload API", implemented in 1.36)
renames `WorkloadSpec.PodGroups` to `PodGroupTemplates []PodGroupTemplate`, renames the
inline `Policy` to `SchedulingPolicy`, renames the type `PodGroupPolicy` to
`PodGroupSchedulingPolicy`, and moves the group to `scheduling.k8s.io/v1beta1`. A separate
new alpha runtime object `PodGroup` (`scheduling.k8s.io/v1alpha1`, with `PodGroupSpec`
{`WorkloadRef`, `TemplateRef`, `SchedulingPolicy`} and `PodGroupStatus` {`Phase`,
`Scheduled`, `Conditions`}) is introduced, and `WorkloadReference` gains a `PodGroupKind`
discriminator (`PodGroupTemplate` default, or `PodGroup` for the runtime object) while
`PodGroupReplicaKey` is deprecated. The reference-vs-copy/inline policy decision for the
runtime object is still open upstream. This churn -- field renames, type renames, a new
object, and an unresolved wire-format decision -- is exactly what motivates isolating all
`Workload`/`PodGroup` access behind a single seam.

### Compatibility Matrix

| Koord feature | Classification | Notes |
|---|---|---|
| PodGroup CRD gang | bridgeable | Maps to a single-PodGroup Workload |
| Annotation/lightweight-label gang | bridgeable | Maps to a single-PodGroup Workload |
| `MinCount` quorum | bridgeable | Direct field mapping |
| GangGroup (<= 8 members) | bridgeable | Maps to a multi-PodGroup Workload |
| GangGroup (> 8 members) | blocked-on-upstream | Exceeds `WorkloadMaxPodGroups` |
| Strict mode | partial / analogy | Proposed analogy to `DisruptionMode.All` (KEP-5710); see caveat below -- different axis |
| Non-strict mode | partial / analogy | Proposed analogy to `DisruptionMode.Single` (KEP-5710); see caveat below |
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

The logic, sketched in the existing `core.Manager` who already dispatches between
annotation and PodGroup-CRD sources, becomes:

```go
type GangOwner int

const (
    GangOwnerNone   GangOwner = iota
    GangOwnerKoord
    GangOwnerNative
)

func resolveGangOwner(pod *corev1.Pod, handle framework.Handle) GangOwner {
    // Native Gang wins if pod carries WorkloadRef AND the native plugin is
    // present in the active scheduler profile.
    if pod.Spec.WorkloadRef != nil && handle.HasPlugin(upstreamnames.GangScheduling) {
        if extension.IsGangPod(pod) {
            // misconfiguration: both native and Koord gang identity present;
            // native wins, emit a warning event.
            emitMisconfigEvent(pod, "pod has both WorkloadRef and Koord gang identity")
        }
        return GangOwnerNative
    }
    if extension.IsGangPod(pod) {
        return GangOwnerKoord
    }
    return GangOwnerNone
}
```

#### Gang Source Abstraction

The mechanism is a source-agnostic gang resolver in `core.Manager`, following the
same abstraction pattern established by the workload auditor
([#2872](https://github.com/koordinator-sh/koordinator/pull/2872)) and the nextPod
abstraction ([#2417](https://github.com/koordinator-sh/koordinator/pull/2417)).
A `GangInfo` interface yields the gang identity, member set, and min quorum from whichever
source owns the pod:

- `annotationGangInfo` (existing behavior)
- `podGroupGangInfo` (existing behavior)
- `workloadGangInfo` (new; **design only**, compiled behind the feature gate, not wired
  into the default profile until Beta)

The `workloadGangInfo` implementation is the single seam where the alpha API touches
Koordinator. Keeping it isolated means a breaking upstream API change (such as the 1.36
`Workload` rework) is contained to one file rather than spread across the plugin.

```go
// Three implementations exist:
//   - annotationGangInfo (existing; parses gang.scheduling.koordinator.sh/*)
//   - podGroupGangInfo  (existing; derives from PodGroup CRD)
//   - workloadGangInfo  (new; backed by upstream Workload/PodGroup API)
type GangInfo interface {
    Identity() string
    Members() []*corev1.Pod
    MinQuorum() int32
}

// workloadGangInfo translates a native Workload/PodGroup into GangInfo.
// When upstream renames fields or moves API groups (e.g. the 1.36 rework),
// only this file changes; the GangInfo contract and the Koord core do not
type workloadGangInfo struct {
    workload *schedulingv1alpha1.Workload
    pgName   string                       
    members  []*corev1.Pod
}

func (g *workloadGangInfo) Identity() string {
    return g.workload.Namespace + "/" + g.pgName
}

func (g *workloadGangInfo) Members() []*corev1.Pod {
    return g.members
}

func (g *workloadGangInfo) MinQuorum() int32 {
    for _, pg := range g.workload.Spec.PodGroups {
        if pg.Name == g.pgName && pg.Policy.Gang != nil {
            return pg.Policy.Gang.MinCount
        }
    }
    return 0
}
```

The 1.36 break is illustrative: when `WorkloadSpec.PodGroups` becomes `PodGroupTemplates`
and the field type renames to `PodGroupSchedulingPolicy`, every access site in the
scheduler core would need a new `if` branch. With `workloadGangInfo`, only this one
file adapts the type-assert; GangCache, Permit, PostFilter never touch the upstream
types.

![native gang single-seam isolation](/docs/images/native-gang-seam-isolation.svg)

The diagram above makes the invariant explicit: every moving part of the upstream API
(the 1.35 inline shape, the 1.36 renames and new runtime object, and the still-open
reference-vs-inline decision) enters Koordinator only through `workloadGangInfo`, which
exposes the same stable `GangInfo` contract as the existing annotation and PodGroup-CRD
sources. The Koord Coscheduling core that consumes `GangInfo` -- GangCache, Permit quorum,
`PostFilter` GangGroup preemption, and the Koord-only topology/reservation/quota features --
does not change when upstream changes.

### Preemption Forward-Compatibility

This is the area the maintainers flagged as most important, because upstream preemption
semantics (KEP-5710, alpha 1.36) introduce API concepts Koordinator must not diverge from.
The field shapes below are taken from KEP-5710 and are **not** present in the vendored
1.35 tree (`scheduling.k8s.io/v1alpha1` PodGroup has only `Name` + `Policy`); they are alpha and
subject to change, so this section aligns on semantics, not on exact field names.

Koordinator's `PostFilter` already does GangGroup-aware preemption with strict and
non-strict modes. The forward-compatible alignment, achievable today without the alpha
API, is:

1. **Disruption granularity.** KEP-5710 (not yet in the vendored 1.35 types) proposes a
    `DisruptionMode` `+union` struct on the decoupled `PodGroupSpec` with two members:
    `Single` (children may be disrupted independently, the default) and `All` (children may
    only be disrupted together). It is not a Pod-vs-PodGroup enum. Propose mapping Koord
    strict mode to `DisruptionMode.All` and non-strict mode to `DisruptionMode.Single`. This
    is an analogy, not an equivalence: Koord strict/non-strict governs *scheduling-cycle
    rollback* (via `AfterPostFilter` clearing waiting gangs and `rejectGangGroupById`
    rejecting all waiting GangGroup pods in Permit; `Unreserve` does the same unless the
    gang is `OnceSatisfied`), whereas `DisruptionMode` governs *disruption of
    already-running pods*. The `scheduleCycle`/`scheduleCycleValid` fields from the 2022
    proposal were removed from the current implementation; the `SkipCheckScheduleCycle`
    config is marked `[Deprecated]`. The mapping is directionally correct (both express
    "all-or-nothing-ness") but the translation is not a behavioral no-op and must be
    validated, not assumed, at Beta. Note `All` is invalid for a `BasicSchedulingPolicy`
    PodGroup.
2. **PodGroup-level priority.** KEP-5710 adds `PriorityClassName *string` plus a derived
   `Priority *int32` to `PodGroupSpec` (and `PodGroupTemplate`). When set, the PodGroup
   priority is authoritative and the individual Pod priority is ignored; Beta will reject
   a PodGroup whose pods diverge from the group priority. Koordinator should derive an
   effective gang priority across the GangGroup and use it consistently in `PostFilter`
   victim selection, so the decision is already a function of a single gang-level priority.
3. **GangGroup as the preemption unit.** Native preemption keeps the preemption unit no
   larger than the scheduling unit, which is a single PodGroup; Koord's GangGroup spans
   several. The invariant to preserve: if any member of a GangGroup is preempted, the whole
   group's scheduling cycle is invalidated. This stays `PostFilter`-enforced and is
   documented as a Koord-only superset. The upstream path for a multi-PodGroup preemption
   unit is the future `CompositePodGroup` concept (kubernetes/enhancements#6012); until it
   lands, GangGroup-level preemption has no native equivalent and must not be routed to the
   native path.
4. **Avoiding unnecessary preemption.** KEP-5710 (not KEP-4671, which is the native gang
    plugin KEP itself) treats minimizing preemption as a primary goal: a preemption
    nomination can turn out invalid if the whole PodGroup later fails to schedule, so for
    alpha the upstream default-preemption path is disabled to avoid binding on an
    unnecessary disruption. This is enforced via the `DelayedPreemption` feature gate
    (alpha 1.36, [kubernetes#137203](https://github.com/kubernetes/kubernetes/pull/137203)).
    Koordinator already defers work to the gang cycle; this proposal records the
    requirement that gang preemption must not fire on the first unschedulable member when
    later members may invalidate the nomination, keeping it aligned with the upstream
    "minimize preemptions" model.

None of the four requires the alpha API. They constrain how the existing `PostFilter`
behaves so that the eventual switch to native preemption is a semantic no-op.

### Koordinator-only Extensions

These have no upstream equivalent and remain on the CRD/annotation path regardless of
native adoption. They are explicitly out of any bridge-and-migrate flow:

- GangGroup cross-gang dependencies (beyond 8 PodGroups, and as a preemption unit).
- Network-topology-aware gang placement (`FindOneNode`).
- Reservation integration (`PreBindReservation`).
- ElasticQuota interaction.

The migration plan must guarantee that a pod using any of these features is never
silently routed to the native path.

### Risks and Mitigations

- **Alpha API churn.** Mitigated by isolating all `Workload` access in
  `workloadGangInfo` and compiling it behind a default-off feature gate.
- **Double gating.** Mitigated by the single-owner resolution rule; default-off means no
  behavior change until explicitly enabled.
- **Silent loss of Koord-only semantics.** Mitigated by the ownership rule routing any
  pod that needs GangGroup / topology / reservation / quota features to Koord.
- **Preemption divergence.** Mitigated by proposing a strict/non-strict to `DisruptionMode`
  analogy (to be validated, not assumed) and introducing a gang-level effective priority now.

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
