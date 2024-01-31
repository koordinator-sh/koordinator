---
title: Pod Level numa topology policy
authors:
  - "@kunwuluan"
reviewers:
  - "@eahydra" 
  - "@zwzhang0107"
  - "hormes"
creation-date: 2024-01-31
last-updated: 2024-01-31
---

# Pod Level numa topology policy

## Table of Contents

- [Pod Level numa topology policy](#pod-level-numa-topology-policy)
  - [Table of Contents](#table-of-contents)
  - [Summary](#summary)
  - [Motivation](#motivation)
  - [Proposal](#proposal)
    - [Goal](#goal)
    - [Non-Goal](#non-goal)
    - [Use Cases](#use-cases)
    - [API](#api)
      - [SingleNUMANodeExclusive](#singlenumanodeexclusive)
      - [Examples](#examples)
    - [Work with Node-Level Policy](#work-with-node-level-policy)
    - [CPU Policy for Different Policy](#cpu-policy-for-different-policy)
    - [Work with Device Joint Allocation](#work-with-device-joint-allocation)
    - [Changes In Scheduler](#changes-in-scheduler)
      - [NUMA Aware Resource](#numa-aware-resource)
        - [Arguments](#arguments)
        - [Filter](#filter)
        - [Hint](#hint)
        - [Score](#score)
  - [Graduation criteria](#graduation-criteria)
  - [Alternatives](#alternatives)
  - [Implementation History](#implementation-history)

## Summary

Introduce new api for pod level numa topology policy. With the new api, users can specify numa topology policy for each 
pod, so that pods that are more sensitive to latency can decide how they need to be orchestrated, rather than being passively 
scheduled according to the numa topology policy on the node.

## Motivation

With numa topology policy set on node, users need to set nodeSelector or nodeAffinity to place the application under the 
same numa. This means to split the nodes in cluster into static groups. In large clusters, with lots of nodes that can serve 
different purposes, it's not unreasonable to dedicate a node (or set of nodes) to a certain numa topology policy, and 
direct pods to those nodes using a nodeSelector. It's mostly problematic in smaller clusters where you don't have the 
luxury of special-purposing nodes like this and you want to binpack pods onto nodes as tightly as possible (while still 
reaping the benefits of topology-alignment). So we need a way to specify numa topology policy for each pod.

## Proposal

### Goal

- Allow users to specify numa topology policy on workloads. 
- Protect the QoS requirement for workloads with `SingleNUMANode` policy by preventing pods that cross
numa and pods with `SingleNUMANode` policy from being deployed on the same numa.
- New API should be compatible with numa topology policy on nodes. This means if users don't specify NUMA scheduling 
policy on workloads, numa topology policy on nodes should still work.

### Non-Goal

- Change the device and CPU, Memory allocation rules during NUMA-aware scheduling.

### Use Cases

- As a cluster user, I don't have privileges to set the numa topology policy on the node, and I want to set `SingleNUMANode` 
for my pods so that they can be binpacked as tightly as possible.
- As a cluster admin, split my cluster into 2 small cluster according to numa topology policy may reduce gpu utilization.
- As a cluster user, I hope there is a method for me to indicate that my workload do not want to be placed with a cross
numa workload because these workloads may use too much memory and result in my inability to obtain the requested memory 
resources.

[//]: # (- As a cluster admin, I hope there is no inference between jobs with different numa topology policy. This option should be a global )

[//]: # (setting so that I can enable and disable it easily.)

### API

We will introduce a new property in pod `scheduling.koordinator.sh/resource-spec`: `numaTopologyPolicy`. 
The value of this label can be `""`, `Restricted`, `SingleNUMANode`, `BestEffort`. The default value of this label is `""`. 
The meaning of these values is same with the value of policy on node.

#### SingleNUMANodeExclusive

To protect the QoS for the pod with `SingleNUMANode` policy, pods that cross numa should not be placed on the same numa 
with the pod being scheduled on one numa. Sometimes this may lead to situations where workloads that use multiple NUMAs 
could never be scheduled. Therefore, we allow some critical workloads to break this restriction.

We will introduce a new property in pod `scheduling.koordinator.sh/resource-spec`: 
`singleNUMANodeExclusive` to reach the goal, the value of this property can be:
- `preferred`: a numa with a SingleNUMANode pod will not be scheduled another pod with multi-numa if there is another idle numa.
- `required`: a numa with a SingleNUMANode pod can not be scheduled another pod with multi-numa.

If `SingleNUMANodeExclusive` not set by user, it will be treated as if 'exclusive' were used to ensure that 'SingleNUMANode' 
is not affected by other policies.

[//]: # (We will introduce a new argument in scheudler's config: `SingleNUMANodeExclusive`, the value of this property 
can be:)

[//]: # (- `none`: a numa with a SingleNUMANode pod can be scheduled another pod with multi-numa.)

[//]: # (- `besteffort`: a numa with a SingleNUMANode pod will not be scheduled another pod with multi-numa if there is 
another idle numa.)

[//]: # (- `exclusive`: a numa with a SingleNUMANode pod can not be scheduled another pod with multi-numa.)

[//]: # (`SingleNUMANodeExclusive` should not be set by users, because some users may set all his/her pods as exclusive 
so that all pods with restricted policy cannot be scheduled.)

#### Examples
``` yaml
metadata:
  annotations:|-
      {
        "numaTopologyPolicy": "SingleNUMANode",
      }
spec:
  containers:
    resource:
      requests:
        cpu: 1
      limits:
        cpu: 1
```
This pod will be allocated with an exclusive cpu.

``` yaml
metadata:
  annotations:|-
      {
        "numaTopologyPolicy": "SingleNUMANode",
      }
spec:
  containers:
    resource:
      requests:
        cpu: 1
      limits:
        cpu: 2
```
This pod will be allocated in shared pool.

``` yaml
metadata:
  annotations:|-
      {
        "numaTopologyPolicy": "SingleNUMANode",
      }
spec:
  containers:
    resource:
      requests:
        cpu: 1
        nvidia.com/gpu: 1
      limits:
        cpu: 1
        nvidia.com/gpu: 1
```
GPU and cpu will be aligned in one NUMA node for this pod.

``` yaml
spec:
  containers:
    resource:
      requests:
        cpu: 1
        nvidia.com/gpu: 1
      limits:
        cpu: 1
        nvidia.com/gpu: 1
```
We will not bind align gpu and cpu for this pod in one numa, because there is no
numa topology policy on pod.

### Work with Node-Level Policy

We want to make the new API compatible with the numa topology policy on node. So if the policy on pod and on node are 
different, we will not place the pod on node. If the policy on node is `""`, means the node is able to place any workload,
then we can schedule pod as we like. On the other hand, `""` in workload's policy means the pod do not have any requirement
for scheduling, so it can be placed on any node. Just schedule it as we what we do before.

So we have these rules:

- If the policy on pod is not `""`, the scheduler will use the policy set on pod.
Pod with a not-none policy can be scheduled on a node only if the policy
on the node is `""` or the same as the policy on pod.

- If the policy on pod is not set or is `""`, then the scheduler will use the policy set on node.

|                | SingleNUMANode node | Restricted node | Besteffort node | none node | 
|----------------|-----------------------|-----------------|-----------------|-----------|
| SingleNUMANode | ✅                     | ❌               | ❌               | ✅         |
| Restricted     | ❌                     | ✅               | ❌               | ✅         |
| Besteffort     | ❌                     | ❌               | ✅               | ✅         |
| none           | ✅                     | ✅               | ✅               | ✅         |



### CPU Policy for Different Policy

First, we should maintain consistent behavior when schedule a pod without numa topology policy in annotations. For a pod 
with numa topology policy in annotations on a node without numa topology policy, our behavior will be consistent with that 
of the nodes with labels.

### Work with Device Joint Allocation

This should be compatible with Device Joint Allocation, because this feature should maintain consistent behavior with 
policy on node.

### Changes In Scheduler

#### NUMA Aware Resource

##### Arguments

Will add a property in arguments. Like the following:

``` go
// CPUBindPolicy defines the CPU binding policy
type NUMAExclusivePolicy = string

const (
    NUMAExclusiveBesteffort = "Preferred"
    NUMAExclusiveExclusive = "Reuqired"
)

type NodeNUMAResourceArgs struct {
    ...
    SingleNUMANodeExclusive NUMAExclusivePolicy
}
```

This argument only work on pods with new label, so we can maintain the consistant behavior as before for the users who 
do not use the new label.

##### Filter

Topology manager should find the policy on pod and check if the policy on node is same as the policy on pod. If not, the 
node will be marked as `UnschedulableAndUnresolvable`.

If there is no numa topology policy on pod, we should maintain consistent behavior as before.

##### Hint

We will get policy from pod prior than node.

When try to find available hints, if the hint contains a SingleNUMANode pod and `SingleNUMANodeExclusive=Required`, 
we will skip the hint.

When calculate the scores for hints, if the hint contains a SingleNUMANode pod and `SingleNUMANodeExclusive=Preferred`, 
score for the hint will be set as 0.

##### Score

By default, scheduler will place pods with spread policy, pods that need to placed span multi numas can schedule failed
when every numa node is placed with one SingleNUMANode pod. So we will prioritize placing SingleNUMANode pods together.
Score will be calculated in the following way:
`score=(100-(num-of-numanode)*10)+10*(requested/allocated)`

`(100-(num-of-numanode)*10)` is the base score, nodes that require multiple numas to satisfy will always score lower 
than nodes that can be satisfied with just one numa.

`10*(requested/allocated)` means nodes with high utilization score higher than those with low utilization.

## Graduation criteria

This plugin will not be enabled only when users enable it in scheduler framework and add a label in pods.
So it is safe to be beta.

* Beta
- [ ] Add E2E tests.

## Alternatives

## Implementation History