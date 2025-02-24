---
title: Node Resource Balance Rescheduling
authors:
  - "@JBinin"
reviewers:
  - "@YYY"
creation-date: 2025-02-15
last-updated: 2025-02-15
status: provisional
---

# Node Resource Balance Rescheduling

## Table of Contents
- [Node Resource Balance Rescheduling](#node-resource-balance-rescheduling)
  - [Table of Contents](#table-of-contents)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non\-Goals/Future Work](#non-goalsfuture-work)
  - [Proposal](#proposal)
    - [User Stories](#user-stories)
      -  [Story 1](#story-1)
    - [Implementation Details](#implementation-details)
      - [Node Selection](#node-selection)
      - [Pod Eviction](#pod-selection)
  - [Implementation History](#implementation-history)

## Summary
Imbalances in the allocation rates of different resources within a node can cause high-allocation resources to become bottlenecks, while other resource types on the same node may become fragmented and underutilized.
This proposal defines a descheduling plugin designed to rebalance resource allocation rates at the node level.

## Motivation
Over time, certain nodes in the cluster may experience imbalances in the allocation of different resources.
This situation can cause resources with lower allocation rates in those nodes to become fragmented and underutilized, resulting in inefficient resource utilization and ultimately causing resource wastage.

### Goals
- Provides a configurable descheduling plugin to help rebalancing resource allocation at the node level.
- Define the metrics used to represent the imbalance of different resources on a node, and define the threshold values for selecting nodes for eviction.
- Define the eviction priority of Pods on the node.

### Non-Goals/Future Work
- Considering real utilization with Koordinator's NodeMetrics.
- Trying to design a scheduling plug-ins to keep all nodes in resource balance as much as possible.
- Providing best practices for working together with the Scheduler, e.g., using scheduling plugins like **NodeResourcesBalancedAllocation**.

## Proposal

### User Stories

#### Story 1
When the resource allocation rates of certain nodes in the cluster become imbalanced, resources with lower allocation rates on those nodes will become fragmented and underutilized. 
For example, suppose node A has a CPU allocation rate of 90% and a memory allocation rate of 50%, while node B has a CPU allocation rate of 50% and a memory allocation rate of 90%. 
If a pod requests 15% CPU and 15% memory of the node, the pod may fail to be scheduled, even though the total resources on node A and node B are sufficient.
Such situations should be avoided.

### Implementation Details

#### Node Selection
The node's fragmentation rate is described using the standard deviation of allocation rates across different resource types on the node:
```node fragmentation rate = std(node CPU allocation rate, node memory allocation rate).```
In scenarios involving only two resource types, the standard deviation simplifies to `|node CPU allocation rate - node memory allocation rate| / 2`. 
This metric intuitively represents the balance degree of resource allocation rates of a node.
It consistency with the scheduler's **NodeResourcesBalancedAllocation** strategy and provides scalability for incorporating additional resource types in the future.

A node is considered to have excessive fragmentation when its fragmentation rate exceeds a threshold, which may adversely affect future pod scheduling on that node.
To simplify user configuration, the plugin autonomously computes the mean (μ) and standard deviation (σ) of fragmentation rates across the cluster, dynamically setting the threshold at μ + σ.

#### Pod Selection
The plugin receives externally filter to evaluate and classify Pods on a node into two distinct categories: removable Pods and non-removable Pods. 
Subsequently, a secondary filtering process is applied to the removable Pods, which eliminates those whose eviction would result in an increased fragmentation rate on the node.
The final candidate Pods are sorted based on the following priority criteria (in descending order of importance):
- KoordinatorPriorityClass
- Priority
- KubernetesQoSClass
- KoordinatorQoSClass
- PodDeletionCost
- EvictionCost
- NodeFragmentationRate (the node fragmentation rate is calculated under the hypothetical scenario of pod eviction)
- PodCreationTimestamp.

The plugin sequentially evicts Pods according to the aforementioned priority ordering until the node's fragmentation rate falls below the threshold.

## Implementation History
- [X] 02/10/2025: Proposed idea in an [issue](https://github.com/koordinator-sh/koordinator/issues/2332).
- [X] 02/17/2025: Initial proposal sent for review.

