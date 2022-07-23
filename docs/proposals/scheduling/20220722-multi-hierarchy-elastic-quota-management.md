---
title: Multi-hierarchy-elastic-quota-management
authors:
- "@buptcozy"
- "@eahydra"
reviewers:
- "@eahydra"
- "@hormes"
- "@yihuifeng"
- "@honpey"
- "@zwzhang0107"
- "@jasonliu747"
creation-date: 2022-07-22
last-updated: 2022-07-22
status: provisional

---

# Multi-hierarchy-elastic-quota-management

<!-- TOC -->

- [Multi-hierarchy-elastic-quota-management](#multi-hierarchy-elastic-quota-management)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Compared with competitors](#Compared-with-competitors)
          - [Resource Quotas](#resource-quotas)
          - [Elastic Quota](#elastic-quota)
        - [Goals](#goals)
        - [Non-goals/Future work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [User Stories](#user-stories)
        - [API](#api)
        - [Implementation Details](#implementation-details)
        - [Compatibility](#compatibility)
    - [Unsolved Problems](#unsolved-problems)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)
    - [References](#references)

<!-- /TOC -->

## Summary
When several users or teams share a cluster, fairness of resource usage is very important. This proposal provides 
multi-hierarchy elastic quota management mechanism for the scheduler. 
- It supports configuring quota groups in a tree structure, which is closer to the real business scenario. 
- It supports the borrowing / returning of resources between different quota groups. The busy quota groups can automatically 
temporarily borrow the resources of the idle quota groups, which can improve the utilization of the cluster. At the same time, 
when the idle quota group turn into the busy quota group, it can also automatically recover the borrowed resources. 
- It considers the resource fairness between different quota groups. When the busy quota groups borrows the 
resources of the idle quota groups, the borrowed resources can be allocated to the busy quota groups with some fair rules.

## Motivation

### Compared with competitors

#### Resource Quotas
[Resource Quotas](https://kubernetes.io/docs/concepts/policy/resource-quotas/) provides the ability to restrain the upper 
limit of resource usage in one quota group. The quota group resource usage aggregated based on the pod resource configurations.
Suppose there are still free resources in the cluster, but the resource usage of this quota group is close to the limit. 
The quota group cannot flexibly borrow the idle resources of the cluster. The only possible way is to manually adjust the 
limit of the quota group, but it is difficult to determine the timing and value of the adjustment.

#### Elastic Quota
[Elastic Quota](https://github.com/kubernetes-sigs/scheduler-plugins/blob/master/kep/9-capacity-scheduling/README.md#goals)
proposed concepts of "max" and "min". "Max" is the upper bound of the resource consumption of the consumers. "Min" is the minimum 
resources that are guaranteed to ensure the functionality/performance of the consumers. This mechanism allows the workloads 
from one quota group to "borrow" unused reserved "min" resources from others quota group. A quota group's unused "min" 
resource can be used by other users, under the condition that there is a mechanism to guarantee the "victim" user can 
consume its "min" resource whenever it needs. 

If multiple quota groups need borrow unused reserved "min" resources from other quota groups at the same time, 
the implementation strategy is FIFO, which means that one quota group may occupy all borrowed resources, 
while other quota groups cannot borrow any resources at all.

Moreover, this mechanism allows quota group borrowing of unused reserved "min" resources of cross-namespaces quota groups, 
but does not allow preempting back borrowed resources between cross-namespaces. In this way, the "min" of the quota group 
cannot be guaranteed really.

Neither of the above support multi hierarchy quota management.

### Goals

### Non-goals/Future work

## Proposal

### User Stories

### API

### Implementation Details

### Compatibility

## Unsolved Problems

## Alternatives

## Implementation History

## References