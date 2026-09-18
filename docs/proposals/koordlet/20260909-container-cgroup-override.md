---
title: Container Cgroup Override (precise runtime cgroup limits)
authors:
  - "@pingxin403"
reviewers:
  - TBD
creation-date: 2026-09-09
last-updated: 2026-09-09
status: provisional
see-also:
  - "/docs/proposals/koordlet/20220615-qos-manager.md"
  - "/docs/proposals/koordlet/20221018-koordlet-support-cgroups-v2.md"
---

# Container Cgroup Override

## Summary

Add an **open-source**, first-class way for Koordinator to set **exact container cgroup hard limits**
(CPU quota / memory max / optional cpuset) at runtime **without changing Pod Spec**, with a mandatory
**writeback-on-release** contract.

This fills the gap left by policy-driven plugins (CPUBurst, MemoryQoS / CgroupReconcile, BECPUSuppress)
which adjust cgroups by QoS class / heuristics, and avoids relying on vendor-private APIs such as
Alibaba Cloud `resources.alibabacloud.com/Cgroups`.

## Motivation

Operators need a slow-path control plane for:

- Temporary raise / lower of `memory.max` (OOM mitigation, pseudo in-place resize)
- Explicit CPU quota override when Burst is not appropriate
- Binding cpuset for isolation experiments

Today:

| Path | Open source? | Delete = restore Spec? |
|------|--------------|------------------------|
| CPUBurst / MemoryQoS / Suppress | Yes | N/A (policy) |
| ACK `Cgroups` CR + CgroupCRD plugin | **No** (vendor) | **No** (`memory.max` can residual) |
| External agents (e.g. NRC) | Out of tree | Often yes |

`NodeSLO.Spec.Extensions` exists as an opaque bag, and qos-manager already has
`RegisterQOSExtPlugin` / `--qos-extension-plugins`, but **no in-tree consumer** writes arbitrary
per-container hard limits.

### Goals

- Define CRD **`ContainerCgroupOverride`** under `slo.koordinator.sh` (namespaced).
- koordlet **qos-extension plugin** applies desired files via existing `resourceexecutor`.
- **One container → one owner**; last-writer-wins is a bug, not a feature.
- **Delete / disable ⇒ writeback baseline** (value captured on first successful apply), or rebuild Pod.
- Explicit **mutual exclusion** with CPUBurst / CgroupReconcile / external writers (skip annotation).
- Default **off** (`--qos-extension-plugins=ContainerCgroupOverride=false`).

### Non-Goals / Future Work

- Millisecond trading “borrow idle CPU” main loop (use CPUBurst first).
- Blkio device throttling (needs host `/dev` mount; separate proposal).
- Replacing InPlacePodVerticalScaling / VPA Spec updates.
- Copying ACK `resources.alibabacloud.com` API group into OSS.
- Full manager→NodeSLO.Extensions fan-out in v1 (optional later for multi-writer control plane).

## Proposal

### User Stories

1. **OOM operator**: Create override lowering `memory.max` on a noisy container; after pressure
   eases, delete CR and expect host cgroup restored to baseline.
2. **Platform**: Run Burst on most pods; mark NRC / override-managed pods with skip so Burst
   does not thrash the same `cpu.max`.

### API (v1alpha1)

```yaml
apiVersion: slo.koordinator.sh/v1alpha1
kind: ContainerCgroupOverride
metadata:
  name: oom-cap-foo
  namespace: app-ns
spec:
  target:
    podName: foo-xxxx
    containerName: app
    # recommended: podUID for strict match after recreate (NRC-style)
    # podUID: "...."
  # Nested by controller (ACK Cgroups style); extensible without flattening Spec.
  resources:
    memory:
      max: "512Mi"          # -> memory.max / memory.limit_in_bytes
    cpu:
      quota: "200m"         # -> cpu.max / cfs_quota_us (period from host unless set)
      # period: 100000
      # cpuset: "0-3"
    # blkio: {}             # schema-ready; not actuated in v1alpha1
  writebackOnDelete: true   # host baseline writeback (NOT PodSpec rollback)
status:
  phase: Pending|Applied|Writeback|Released|Failed
  nodeName: node-a
  observedGeneration: 1
  baseline:
    memory:
      max: "1073741824"
    cpu:
      quota: "50000"
  observedDesiredHash: ...
  lastAppliedTime: ...
  message: ...
  conditions: []
```

Rules:

- **FR1**: At most one non-terminal Override per `(ns, podName, containerName)`.
- **FR2**: First apply reads current cgroup value → `status.baseline` (nested; also copied into NodeSLO.Extensions).
- **FR3**: Finalizer blocks delete until writeback succeeds and phase=`Released` (or `writebackOnDelete=false`).
- **FR4**: Pod annotation `koordinator.sh/cgroup-override-skip=true` → plugin no-ops (external owner).

### Design lineage

| Borrow from | What | Reject |
|-------------|------|--------|
| ACK `Cgroups` | Nested `resources.{memory,cpu,blkio}` | Deployment/Job targeting; no writeback |
| NRC `PodResourceResize` | PodUID binding, phases/conditions, node delivery | PodSpec rollback; multi-container one CR; full allocated/actuated machine |
| CCO (this) | Host baseline + finalizer writeback; one CR ↔ one container | Flat top-level memoryMax/cpuQuota |

### Control plane options

| Phase | Desired state source | Notes |
|-------|----------------------|-------|
| **0** | Pod annotation `koordinator.sh/cgroup-override` JSON | Fallback / POC; flat or nested |
| **1 (this PR)** | CR → manager aggregator → `NodeSLO.Extensions["containerCgroupOverrides"]` → koordlet | Nested resources; baseline on CR.status |
| **2 (later)** | Validating webhook uniqueness + required podUID | Softens FR1 |

Delivery path (phase-1):

```text
ContainerCgroupOverride (CR)
        │  spec.target + spec.resources (nested)
        ▼
koord-manager containercgroup controller
        │  patches NodeSLO.Spec.Extensions
        ▼
NodeSLO.Extensions["containerCgroupOverrides"]
        │
        ▼
koordlet qos-extension plugin ContainerCgroupOverride
        │  apply / skip-noop / writeback
        ▼
CR.status (nested baseline, phase=Applied|Released)
```

### Plugin placement

Reuse qos-extension framework (`pkg/koordlet/qosmanager/framework/extension.go`):

```text
--qos-extension-plugins=ContainerCgroupOverride=true
```

**Not** a feature-gate under `--feature-gates` (vendor CgroupCRD taught that lesson).

### Mutual exclusion

| Writer | Conflict file | Mitigation |
|--------|---------------|------------|
| CPUBurst | `cpu.max` / cfs_quota | skip annotation / disable Burst for those pods |
| CgroupReconcile / MemoryQoS | `memory.min/low/high` (and related) | document; avoid fighting `memory.max` owners |
| External NRC | same | single owner; use skip |

### Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Residual hard limit after uninstall | Finalizer + residual scanner SOP |
| Dual writers thrash | skip annotation; e2e for Burst×Override |
| Annotation phase lacks admission | Document; CR phase adds validating webhook optional |
| NodeSLO reconciler overwrites Extensions | Copies `oldSpec.Extensions` then only updates registered extender keys — custom key survives |

## Alternatives

1. **Only document ACK Cgroups** — rejected for multi-cloud / white-box requirement.
2. **Fork ACK plugin into OSS under alibabacloud.com** — wrong API ownership.
3. **Always use NRC out-of-tree** — fine for some orgs; Koordinator still needs an upstream story.
4. **Koordlet watches CR directly** — workable but duplicates node fan-out; NodeSLO path reuses existing informer.

## Upgrade Strategy

- Default disabled; no behavior change on upgrade.
- Enabling requires RBAC + CRD install + manager `containercgroup` controller.

## Test Plan

- Unit: parse override, baseline capture, writeback order, Extensions apply, skip no-op.
- Unit: manager aggregator patches NodeSLO.Extensions.
- Integration: fake cgroup fs apply memory/cpu v1+v2.
- e2e (later): create CR → file changes → delete → baseline restored; Burst skip.

## Implementation History

- 2026-09-09: proposal + phase-0 annotation plugin scaffold in fork `pingxin403/koordinator`.
- 2026-09-09: POC hardening — fake-cgroup apply/writeback tests, period-aware CPU quota, array annotation, POC runbook.
- 2026-09-09: phase-1 — CRD `ContainerCgroupOverride`, manager aggregator → NodeSLO.Extensions, plugin prefers Extensions + CR status baseline/Released, skip no-op.
- 2026-09-10: CRD redesign — nested `spec.target` + `spec.resources.{memory,cpu,blkio}` (ACK layout + NRC UID/status cues); drop flat memoryMax/cpuQuota; keep host baseline writeback.
