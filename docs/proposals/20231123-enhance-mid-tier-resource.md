---
title: Enhance Mid-tier resources
authors:
  - "@j4ckstraw"
  - "@jiasheng55"
reviewers:
  - "@zwzhang0107"
  - "@hormes"
  - "@eahydra"
  - "@FillZpp"
  - "@jasonliu747"
creation-date: 2023-11-23
last-updated: 2023-11-28
status: implementable
see-also:
  - "/docs/proposals/20230613-node-prediction.md"
---

# Enhance Mid-tier resources

## Table of Contents

<!--ts-->
- [Enhance Mid-tier resources](#enhance-mid-tier-resources)
  - [Table of Contents](#table-of-contents)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
  - [Proposal](#proposal)
    - [User Stories](#user-stories)
      - [Story 1](#story-1)
      - [Story 2](#story-2)
    - [Prerequisites](#prerequisites)
    - [Design Principles](#design-principles)
    - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
      - [Cgroup basic configuration](#cgroup-basic-configuration)
      - [QoS policy](#qos-policy)
      - [Node QoS](#node-qos)
    - [Risks and Mitigations](#risks-and-mitigations)
  - [Alternatives](#alternatives)
  - [Upgrade Strategy](#upgrade-strategy)
  - [Additional Details](#additional-details)
  - [Implementation History](#implementation-history)
<!--te-->

## Summary

The *Mid-tier resources* is proposed to both improve the node utilization and avoid overloading, which rely on [node prediction](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/20230613-node-prediction.md). 

While *node prediction* clarify how the Mid-tier resources are calculated with the prediction, and this proposal will clarify Mid-tier cgroup and QoS design, suppress and eviction policy.

## Motivation

Here I want to explain some concepts:
1. koord-QoS

Quality of Service, we assue the same QoS level has similar operating performance, operating quality.

2. koord-priority

Scheduling priority, high priority can preempt low priority by default.

3. Resource Type

We have four resource type now, prod, mid, batch and free resource
resource type care about whether the resource is oversold, whether it is stable, which affects pod eviction.

koordinator bind koord-priority and resource type, different priority has different resource type.

This proposal introduce Mid+LS and Mid+BE fill the gap in Prod+LS and Batch+BE
meet the requirements of different types of tasks.

### Goals

- How to calculate and update mid resource amount of node.
- Clarify Mid-tier cgroup and QoS
- Clarify Mid-tier suppress and eviction policy
- The optimization needed in scheduler/descheduler

### Non-Goals

- Replace Batch-tier resources
- Add new QoS type

## Proposal

### User Stories

#### Story 1

There are low-priority online-service tasks, which performance requirements is same as Prod+LS while it do not want to be suppressed but can tolerate being evicted, when the machine usage spike. 

Mid+LS can conquer it.

#### Story 2

There are resource consumption tasks, AI or stream computing, such as Apache Spark, which may consume a lot of resources. It need stable resource and it can be suppressed and do not want to be evicted.

Mid+BE can conquer it.

### Prerequisites

Must use koordinator node reservation if anyone want to use Mid+LS

### Design Principles

**QoS**

By default, the performance of same QoS should be similar, Prod+LS and Mid+LS should be *basically* the same, also suitable to Mid+BE and Batch+BE.

QoS policy adaptation needs to be specific. when QoS is the same and priorities are different, generally speaking, there is no need to make a big difference, such as the Priority of eviction, and some advanced configurations in memory reclamation,normally, no configuration is required.
But as CPU Group Identity, there is no finer configuration granularity, so no additional adaptation is required for the time being.
In the future, advanced capabilities such as Core Schedule and CPU idle need to be considered.

Alao you can introduce a QoS level, such as MID, between LS and BE, to implement features such as QoS detail adjustment.

**resource oversale**

Mid resource is calculated by
```
Allocatable[Mid] := min(Reclaimable[Mid], NodeAllocatable * thresholdRatio)
```
at present, we need to change it to

```
Allocatable[Mid] := min(Reclaimable[Mid], NodeAllocatable * thresholdRatio) + Unallocated[Mid]
Unallocated[Mid] = max(NodeAllocatable - Allocated[Prod], 0)
```

reference [mid-tier-overcommitment](https://koordinator.sh/docs/designs/node-prediction/#mid-tier-overcommitment)

in the long term it can be support non-oversale by feature-gate.
```
Unallocated * thresholdRatio
```

**native resource or extend resource**

*native resource*:
hijack node update, change `node.Status.allocatable`, mid pod also use native resource, in this situation, Mid is equivalent to a sub-priority in prod, resource quota need to make adaptive modification.
for Mid+BE pods, it can be located burstable, even guaranteed, which disobey QoS level policy.

*extend resource*:
add mid-cpu/mid-memory, inject extend resource field by webhook.
for Mid+BE pods it will be located in besteffort.
for Mid+LS pods it will be located in burstable by reserve limits.cpu/limits.memory.

**share resource account or not**

Let's consider the non-oversold scenario.

if prod and mid pods share resource account, when prod pod pending, preemption is needed. koord-scheduler need filter and preept plugins to handle it.

if prod and mid pods do not share resource account, mechanism is needed for standalone or descheduler to migrate mid pods in necessary cases, such as hot spots.

In this scenario, Mid+LS may affect Prod+LS, it depends on the calculation strategy of Mid-tier resource and the degree of interference brought by mid pods.
the calculation of Mid-tier resource should be relatively conservative in the preset target scenario, and the application type should not introduce too much interference.

**trade-off-result**
- keep Mid+LS and Mid+BE，do not introduce new QoS level.
- change mid resource calculate method, add unallocated resource into mid resource.
- use extend-resource mid-cpu/mid-memory
- do not share resource account

### Implementation Details/Notes/Constraints

#### Cgroup basic configuration

**cfsQuota/memoryLimit configuration**

Configured according limits.mid-cpu and limits.mid-memory.

**cpuShares**

Configured according requests.mid-cpu
- for Mid+LS, same as Prod+LS
- for Mid+BE, same as Batch+BE

**cgroup hierarchy**

- Mid+LS, inject limits.cpu/limits.memory by webhoook, so it can be located in Burstable.
- Mid+BE, located in Besteffort by default.

*Notification*
Burstable cpuShares/memoryLimit may be update by kubelet periodically.

#### QoS Policy

Configured according koord-QoS
- LS for Mid+LS
- BE for Mid+BE

#### Node QoS

**CPU Suppress**

- Mid+LS do not be suppressed by default, if task performance do not meet SLA, eviction is accepted.
- Mid+BE can be suppressed, and do not want to be evicted frequently.

CPU suppress should consider Batch+BE and Mid+BE.

**CPU Evicton**

CPU eviction is related to pod satisfaction at present.
but in the long term, it should be done from the perspective of OS, like memory eviction.

**Memory Evict**

The eviction is sorted according to the priority and resource type
- Batch first and then Mid.
- Mid+LS first and then Mid+BE, for Mid pods, request and usage should be consideration when evict for fairness.

### Risks and Mitigations

- Burstable cpuShares may be updated by kubelet periodically, which is confict with koordlet update. [reference](https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/cm/qos_container_manager_linux.go#L170)

We can do not update burstable cpuShares, then Prod+LS and Mid+LS may mutual interference each other only when high load.

- Burstable memory limit may be updated by kubelet periodically, which is confict with koordlet update. [reference](https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/cm/qos_container_manager_linux.go#L343)

We should disable kubefeatures.QOSReserved for memory resource,
see [Prerequisites](#prerequisites).

## Alternatives

**Add Mid QoS**

Introduce new QoS level, which can adjust Mid-tier pod QoS finely.

## Upgrade Strategy

- [ ] add midresource runtimehook, configure cgroup
- [ ] update Mid-tier calculate policy
- [ ] update BE suppress to support Mid+BE
- [ ] update CPU/Memory eviction to support Mid-tier
- [ ] scheduler/descheduler filter and policy, for example do not over-allocate for mid+prod resource if some feature-gate enabled, migrate some mid pod on hotspots for load-balance.

## Additional Details

With mid resource enhanced, we have panorama as follow:

koor-priority | resource type | koord-QoS | k8s-QoS | scenario |
 -- | -- | -- | -- | -- |
koord-prod | cpu/memory | LSE | guaranteed | middleware |
koord-prod | cpu/memory | LSR | guaranteed | high-priority online-service,CPU bind |
koord-prod | cpu/memory | LS | guaranteed | high-priority online-service,微服务工作负载 |
koord-prod | cpu/memory | LS | burstable | high-priority online-service,微服务工作负载 |
koord-mid | mid-cpu/mid-memory | LS | burstable | low-priority online-service |
koord-mid | mid-cpu/mid-memory | BE | besteffort | AI/Flink jobs |
koord-batch | batch-cpu/batch-memory | BE | besteffort | big data jobs |
koord-free | TBD | TBD | TBD | TBD |

## Implementation History
- [ ] 11/28/2023: Open proposal PR
