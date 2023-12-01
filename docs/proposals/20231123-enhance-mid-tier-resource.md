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

## Table of contents

<!--ts-->
- [Enhance Mid-tier resources](#enhance-mid-tier-resources)
  - [Table of contents](#table-of-contents)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-goals/Future work](#non-goalsfuture-work)
  - [Proposal](#proposal)
    - [Terminology](#terminology)
    - [User stories](#user-stories)
      - [Story 1](#story-1)
      - [Story 2](#story-2)
    - [Prerequisites](#prerequisites)
    - [Design principles](#design-principles)
    - [Implementation details/Notes/Constraints](#implementation-detailsnotesconstraints)
      - [Cgroup basic configuration](#cgroup-basic-configuration)
      - [QoS policy](#qos-policy)
      - [Node QoS](#node-qos)
    - [Risks and mitigations](#risks-and-mitigations)
  - [Alternatives](#alternatives)
  - [Upgrade strategy](#upgrade-strategy)
  - [Additional details](#additional-details)
  - [Implementation history](#implementation-history)
<!--te-->

## Summary

The *Mid-tier resources* is proposed to both improve the node utilization and avoid overloading, which rely on [node prediction](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/20230613-node-prediction.md). 

While *node prediction* clarifies how the Mid-tier resources are calculated with the prediction, and this proposal clarifies Mid-tier cgroup and QoS design, suppression and eviction policy.

## Motivation

This proposal introduces Mid+LS and Mid+BE to close the gap in Prod+LS and Batch+BE and fulfil the requirements of different task types.

### Goals

- How to calculate and update the mid-tier resource amount of a node.
- Clarification of Mid-tier cgroup and QoS
- Clarification of Mid-tier suppression and eviction policy
- Necessary optimization of the scheduler/descheduler

### Non-goals

- Replace Batch-tier resources
- Add new QoS type

## Proposal

### Terminology

Here I would like to explain some concepts:
1. koord-QoS

Quality of Service, we assume that the same QoS level has similar operational performance, operational quality.

2. koord-priority

Scheduling priority, high priority can preempt low priority by default.

3. Resource models

We now have four resource models: prod, mid, batch and free. The resource model takes care of whether the resource is oversold and whether it is stable, which affects pod eviction.

Note that koordinator bind koord-priority and the resource model; different priorities have different resource models.

### User stories

#### Story 1

There are low priority, latency-sensitive service tasks whose performance requirements match those of Prod+LS, but which should not be suppressed but can tolerate being evictied as machine utilisation increases.

Mid+LS can handle them.

#### Story 2

There are latency-insensitive tasks such as AI or stream computing, e.g. Apache Spark, that can consume a lot of resources. They require stable resources, can be suppressed and do not want to be evicted.

Mid+BE can handle them.

### Prerequisites

Must use koordinator node reservation if someone wants to use Mid+LS

### Design principles

**QoS**

By default, the performance of same QoS should be similar, Prod+LS and Mid+LS should be *basically* the same, also suitable to Mid+BE and Batch+BE.

The adaptation of the QoS policy must be specific. In general, if the QoS is the same and the priorities are different, there is no need to make a big difference, such as the priority of eviction, and some advanced configurations in memory reclamation, usually no configuration is required.
However, there is no finer configuration granularity for CPU group identity, so no additional customization is required for the time being.
In the future, advanced features such as core schedule and CPU idle need to be considered.

Also, you can introduce a QoS level, such as Mid between LS and BE to implement functions such as QoS detail customization.

**resource oversale**

Mid resource is calculated by
```
Allocatable[Mid] := min(Reclaimable[Mid], NodeAllocatable * thresholdRatio)
```
at the moment we need to change this to

```
Allocatable[Mid] := min(Reclaimable[Mid], NodeAllocatable * thresholdRatio) + Unallocated[Mid]
Unallocated[Mid] = max(NodeAllocatable - Allocated[Prod], 0)
```

reference [mid-tier-overcommitment](https://koordinator.sh/docs/designs/node-prediction/#mid-tier-overcommitment)

in the long term, non-overselling can be supported by Feature-Gate.
```
Unallocated * thresholdRatio
```

**native resource or extended resource**

*native resource*:
hijack node update, change `node.Status.allocatable`, mid pod also use native resource, in this situation, Mid is equivalent to a sub-priority in prod, resource quota need to make adaptive modification.
for Mid+BE pods, it can be located burstable, even guaranteed to disobey the QoS level policy.

*extended resource*:
add mid-cpu/mid-memory, insert "extended resource" field by webhook.
for Mid+BE pods it is localized in besteffort.
for Mid+LS pods, it is located in burstable by reserving limits.cpu/limits.memory.

**share resource account or not**

Let us look at the scenario without overselling.

if prod and mid pods share resource account, a preempt is required for an upcoming prod pod. koord-scheduler needs filter and preept plugins to handle this.

if prod and mid pods do not share a resource account, a mechanism for standalone or descheduler is needed to migrate mid pods in necessary cases, e.g. hot spots.

In this scenario, Mid+LS can affect Prod+LS; this depends on the calculation strategy for the Mid-tier resource and the degree of interference from mid-pods.
the calculation of the Mid-tier resource should be relatively conservative in the given target scenario and the application type should not cause too much interference.

**trade-off-result**
- retain Mid+LS and Mid+BE，do not introduce a new QoS tier.
- change the calculation method for mid resources, add unallocated resources to mid resources.
- use of the mid-cpu/mid-memory
- no shared resource account

### Implementation Details/Notes/Constraints

#### Cgroup basic configuration

**cfsQuota/memoryLimit configuration**

Configured according limits.mid-cpu and limits.mid-memory.

**cpuShares**

Configured according requests.mid-cpu
- for Mid+LS, same as Prod+LS
- for Mid+BE, same as Batch+BE

**cgroup hierarchy**

- Mid+LS, injects limits.cpu/limits.memory through webhoook, so that it can be located in Burstable.
- Mid+BE, is located in Besteffort by default.

*Notification*
Burstable cpuShares/memoryLimit can be updated regularly by kubelet.

#### QoS policy

Configured according to koord-QoS
- LS for Mid+LS
- BE for Mid+BE

#### Node QoS

**CPU Suppress**

- Mid+LS are not suppressed by default, if task performance does not meet SLA, eviction is accepted.
- Mid+BE can be suppressed, and do not want to be evicted frequently.

Batch+BE and Mid+BE should be considered for CPU suppression.

**CPU Evicton**

CPU eviction is currently linked to pod satisfaction.
in the long term, however, it should be done from the perspective of the operating system, like memory eviction.

**Memory Evict**

Eviction is sorted by priority and resource model
- Batch first and then Mid.
- Mid+LS first and then Mid+BE, for Mid pods, request and usage should be taken into account when evicting for fairness reasons.

### Risks and mitigations

- Burstable cpuShares can be periodically updated by kubelet, leading to conflicts with koordlet update. [reference](https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/cm/qos_container_manager_linux.go#L170)

We cannot update burstable cpuShares, then Prod+LS and Mid+LS can only interfere with each other under heavy load.

- Burstable memory limit can be periodically updated by kubelet, which conflicts with koordlet update. [reference](https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/cm/qos_container_manager_linux.go#L343)

We should disable kubefeatures.QOSReserved for memory resources, see [Prerequisites](#prerequisites).

## Alternatives

**Add Mid QoS**

Introduction of a new QoS level that can be used to fine-tune the QoS of the mid-tier pod.

## Upgrade Strategy

- [ ] add midresource runtimehook, configure cgroup
- [ ] update Mid-tier calculate policy
- [ ] update BE suppression to support Mid+BE
- [ ] update CPU/Memory eviction to support Mid-tier
- [ ] scheduler/descheduler filters and policies, e.g. no over-allocation for mid+prod resources when a feature gate is enabled, migration of some mid-pods to hotspots for load balancing.

## Additional Details

With the improved mid-resource we have the following panorama:

koor-priority | resource model | koord-QoS | k8s-QoS | scenario |
 -- | -- | -- | -- | -- |
koord-prod | cpu/memory | LSE | guaranteed | middleware |
koord-prod | cpu/memory | LSR | guaranteed | high-priority latency-sensitive, CPU bind |
koord-prod | cpu/memory | LS | guaranteed | high-priority, latency-sensitive |
koord-prod | cpu/memory | LS | burstable | high-priority latency-sensitive |
koord-mid | mid-cpu/mid-memory | LS | burstable | low-priority latency-insensitive |
koord-mid | mid-cpu/mid-memory | BE | besteffort | AI/Flink jobs |
koord-batch | batch-cpu/batch-memory | BE | besteffort | big data jobs |
koord-free | TBD | TBD | TBD | TBD |

## Implementation History
- [ ] 11/28/2023: Open proposal PR
