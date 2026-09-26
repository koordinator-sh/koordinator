---
title: Koordlet Async Memory Reclamation on Standard Cgroups V2
authors:
  - "@pingxin403"
reviewers:
  - TBD
creation-date: 2026-08-28
last-updated: 2026-08-28
status: implementable
---

# Koordlet Async Memory Reclamation on Standard Cgroups V2

## Table of Contents

<!--ts-->
* [Koordlet Async Memory Reclamation on Standard Cgroups V2](#koordlet-async-memory-reclamation-on-standard-cgroups-v2)
   * [Table of Contents](#table-of-contents)
   * [Glossary](#glossary)
   * [Summary](#summary)
   * [Motivation](#motivation)
      * [Goals](#goals)
      * [Non-Goals/Future Work](#non-goalsfuture-work)
   * [Proposal](#proposal)
      * [User Stories](#user-stories)
      * [Design](#design)
      * [API](#api)
   * [Alternatives](#alternatives)
   * [Implementation History](#implementation-history)
<!--te-->

## Glossary

- **Async memory reclamation**: reclamation of reclaimable memory (file cache, and optionally anonymous pages) that runs in the background without blocking the faulting/allocation path of the workload.
- **memory.reclaim**: the cgroups-v2 write-only interface introduced in Linux 5.19 ([kernel docs](https://docs.kernel.org/admin-guide/cgroup-v2.html#memory)) that proactively reclaims a requested number of bytes from a memory cgroup.
- **memory.wmark_ratio**: an Alibaba Cloud Linux (Alinux) kernel extension that arms kernel-side watermark-based async reclamation per memcg.

## Summary

The koordlet Memory QoS feature (`CgroupReconcile`) currently implements async memory reclamation only through the Alinux-specific `memory.wmark_ratio` interface. On standard cgroups-v2 kernels (e.g. AWS AL2023, Ubuntu, vanilla 5.19+) the interface file does not exist, so the feature silently degrades: koordlet writes are skipped and no async reclamation happens.

This proposal adds a fallback implementation for standard cgroups-v2 kernels based on the kernel-native `memory.reclaim` interface. A user-space loop running in koordlet periodically reads `memory.current`, compares it against a configured watermark, and writes a small reclaim target to `memory.reclaim` when the threshold is exceeded. The loop targets BE (BestEffort) cgroups only, keeping CPU cost bounded.

## Motivation

The Alinux `memory.wmark_ratio` interface arms a kernel watermark scanner that reclaims memory asynchronously via kswapd. This is the primary mechanism koordlet uses to relieve memory pressure from BE workloads (see `makeCgroupResources()` in `pkg/koordlet/qosmanager/plugins/cgreconcile/cgroup_reconcile.go`). All related resource definitions (`memory.wmark_ratio`, `memory.wmark_scale_factor`, `memory.wmark_min_adj`) are gated by `SupportedIfFileExists` / `SupportedIfFileExistsInKubepods`: when the file is absent, the write is silently skipped.

On standard kernels the file never exists, so:

1. BE workloads are not proactively reclaimed; pressure relief falls back entirely to pod eviction (`memoryevict` plugin), which is a much coarser and slower reaction.
2. The gap grows as more managed Kubernetes distributions (EKS, TKE, vanilla kubeadm) move to cgroups-v2, where koordlet already runs but loses this capability.

`memory.high` is already written by koordlet and does trigger kernel reclamation, but its semantics differ: it throttles the allocating process synchronously (the faulting task pays the reclaim cost), and it is a hard ceiling on memory usage rather than a watermark-driven background reclaim. It is not a substitute for the wmark behavior.

### Goals

- Provide async memory reclamation for BE cgroups on standard cgroups-v2 kernels (Linux 5.19+) using `memory.reclaim`.
- Reuse the existing `CgroupReconcile` reconcile loop and resource-executor write path; no new plugin.
- Auto-detect: on Alinux (wmark file present) keep the current kernel-armed path; on standard v2 use the user-space loop. On unsupported kernels both paths no-op gracefully.
- Keep the user-space loop cheap: fixed small reclaim batch per iteration, BE-only, configurable interval.

### Non-Goals/Future Work

- Not replacing `memory.wmark_ratio` on Alinux; the kernel-armed path is strictly better there.
- Not implementing page-cache limiting, cold-page tracking, or memcg reaper equivalents.
- Not changing the NodeSLO API surface unless required; prefer reusing existing `MemoryQoS` config fields where semantics map.
- O_NONBLOCK optimization for `memory.max`/`memory.high` writes is out of scope for this proposal.

## Proposal

### User Stories

#### Story 1

As a cluster administrator running koordinator on EKS (AL2023, kernel 6.1, cgroups-v2), I want BE workloads to be proactively reclaimed before node memory pressure triggers eviction, matching the behavior available on ACK Alinux nodes.

#### Story 2

As a koordinator developer, I want a single code path that arms the best available reclamation mechanism per kernel: Alinux wmark when present, `memory.reclaim` loop otherwise.

### Design

The loop lives inside the `CgroupReconcile` plugin and is gated by:

1. `CgroupReconcile` feature-gate (existing).
2. cgroups-v2 detection (`IsUsingCgroupsV2()`).
3. `memory.reclaim` file existence in the BE root cgroup path (probe once, cache result).

Per reconcile tick, for each BE cgroup in scope:

1. Read `memory.current`.
2. If usage exceeds the watermark (derived from the same `MemoryQoS` config used for wmark, i.e. `wmarkRatio` semantics mapped onto `memory.current / memory.max`), write a bounded reclaim amount to `memory.reclaim`, e.g. `min(usage - watermark, maxBatch)` bytes.
3. Respect `-EAGAIN` (kernel reclaimed less than requested): treat as success, do not error, back off naturally on the next tick.
4. Loop period is a fixed configurable interval (e.g. seconds), small batch keeps per-iteration CPU bounded (a single `memory.reclaim` write is synchronous and pays reclaim cost in koordlet's own context; keeping the batch small bounds that cost).

The reclaim write goes through the existing resource executor (`pkg/koordlet/resourceexecutor`) so the cgroup path resolution, v2 detection, and updater semantics stay consistent. A new `memory.reclaim` resource type is registered only for cgroups-v2 with `SupportedIfFileExists` gating, mirroring how wmark resources are declared.

### API

No NodeSLO API change is proposed in the first iteration. The loop reuses the existing Memory QoS configuration fields:

- Enablement: existing `MemoryQoS.Enable` (via the `CgroupReconcile` feature gate and per-pod QoS config).
- Watermark: reuse `wmarkRatio` (percentage of the memory limit at which reclamation triggers), interpreted for `memory.reclaim` as `memory.current / memory.max`.
- Optional new tunables (deferred unless review asks): reclaim batch size and loop interval, as koordlet config or constants with sane defaults.

## Alternatives

1. **Kernel-side only (status quo).** Keep relying on `memory.wmark_ratio`; accept that standard kernels get eviction-only pressure relief. Rejected: the gap is the motivation for this proposal.
2. **Use `memory.high` only.** Already written by koordlet; throttles synchronously and caps usage rather than reclaiming proactively. Not a behavioral substitute; keep as complementary.
3. **eBPF-based reclaim.** Can observe pressure precisely but adds a heavy dependency (bpf toolchain, privileged helpers) for a small loop; out of scope.
4. **New standalone plugin.** Cleaner separation but duplicates the cgroup path discovery, feature-gate, and config plumbing that `CgroupReconcile` already owns; rejected in favor of extending the existing plugin.

## Implementation History

- 2026-08-28: Initial proposal. Tracks issue #3186.