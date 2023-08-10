---
title: Eviction Arbitration Mechanism in Descheduler
authors:
  - "@baowj-678"
reviewers:
  - @hormes
  - @eahydra
  - @FillZpp
  - @ZiMengSheng
  - @zwzhang0107
  - @saintube
  - @jasonliu747
creation-date: 2023-07-07
last-updated: 2023-08-10
status: provisional
---

# Eviction Arbitration Mechanism in Descheduler

## Table of Contents
<!-- TOC -->

- [Eviction-Arbitration-Mechanism-in-Descheduler](#eviction-arbitration-mechanism-in-descheduler)
    - [Table of Contents](#table-of-contents)
    - [Glossary](#glossary)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [User Stories](#user-stories)
            - [Story 1](#story-1)
            - [Story 2](#story-2)
            - [Story 3](#story-3)
        - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
            - [PodMigrationJob Controller](#podmigrationjob-controller)
                - [Controller Process](#controller-process)
                    - [EventHandle Process in Watch](#eventhandle-process-in-watch)
                    - [Arbitration Process](#arbitration-process)
                    - [Controller Reconcile Process](#controller-reconcile-process)
                - [Arbitration Mechanism](#arbitration-mechanism)
                    - [Sort PodMigrationJob](#sort-podmigrationjob)
                    - [GroupFilter PodMigrationJob](#groupfilter-podmigrationjob)
                    - [Select PodMigrationJob](#select-podmigrationjob)
            - [Controller Configuration](#controller-configuration)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)

<!-- /TOC -->
## Glossary

## Summary

This proposal designed a mechanism in descheduler to arbitrate `PodMigrationJob`, through which the system's stability could be improved when a mount of Pods or some important Pods is being evicted. 

## Motivation

Arbitrate Mechanism is an important capability that Pod Migration relies on, and Pod Migration is relied on by many components (such as deschedulers). But Pod Migration is a complex process,  involving steps such as auditing, resource allocation, and application startup, and is mixed with application upgrading, scaling scenarios, and resource operation and maintenance operations by cluster administrators. 

So when a large number of Pods are concurrently migrated, this may have some impact on the stability of the system. In addition, if many Pods of the same workload are migrated concurrently, it will also have an impact on the stability of the application. Moreover, If multiple jobs' Pods are migrated concurrently, it will cause a thundering herd effect. So we hope to handle the Pods in each job in sequence.

Therefore, it is necessary to design an arbitration mechanism. This arbitration mechanism will select suitable `PodMigrationJob` to execute and control the execution speed of `PodMigrationJob` (to avoid a large number of jobs executing concurrently), thereby ensuring the stability of the system and application.

### Goals

1. Defines an Arbitrate Mechanism Configuration, through which user can configure the arbitration mechanism.
2. Describe a simple central flow control mechanism to limit the number of Pod migrations over a period of time.
3. Describe in detail the design details of the arbitrate mechanism.

### Non-Goals/Future Work

1. A new CRD or Controller.
2. The generation process of PodMigrationJob.
3. The specific execution process of PodMigrationJob.

## Proposal

### User Stories

#### Story 1

The descheduler evicts Pods with different priorities, and the importance of high priority Pods is also high. Therefore, The cost of migrating high priority Pods is greater than that of low priority Pods. So users expect low-priority Pods to be executed first to minimize migration costs.

#### Story 2

Multiple Pods of different workloads may be concurrently evicted by the descheduler. We hope that the migrated Pods can be dispersed across different workloads (like deployments) as much as possible, in order to avoid a decrease in service availability caused by the migration process.

#### Story 3

Multiple Pods in different jobs are evicted. We should migrate all Pods from one job at a time and process different jobs in sequence to avoid a thundering herd effect.

### Implementation Details/Notes/Constraints

#### PodMigrationJob Controller

The **PodMigrationJobController** will evaluate all PodMigrationJobs and select a batch of PodMigrationJob and add them to the reconcile queue for executing. This selection process is called the **arbitration mechanism**. The arbitration mechanism includes three stages: `Sort`, `GroupFilter` and `Select`.

![image](/docs/images/arbitration-mechanism-design.svg)

##### Controller Process

Arbitration Mechanism will put arbitrated MigrationJob into the reconcile queue To achieve rate limit.

The controller process can be roughly divided into three parts, namely the Controller Reconcile Process, Arbitration Process, and Migration Process:

- **Controller Reconcile Process**: This is the `Reconcile` funtion of PodMigrationJob Controller, which aims to put PodMigrationJobs whose phase if `pending` or empty to the arbitration collection.
- **Arbitration Process**: This process periodically arbitrates the PodMigrationJobs in the arbitration collection and places the PodMigrationJobs selected by the arbitration into the rate limit queue.
- **Migration Process**: This process will read `PodMigrationJob` from the rate limit queue and call `doMigrat`e to handle the `PodMigrationJob`.

###### EventHandle Process in Watch

1. Intercept all Create events and place them in the arbitration collection.

###### Arbitration Process

1. Sort the elements in the arbitration collection to generate a slice.
2. Use a map to record the sorted positions of each `PodMigrationJob` in the slice, with key being the `PodMigrationJob` and value being the position.
3. Call different `GroupFilter` functions in sequence for group and filter operations to update the slice.
4. Resort the elements in the slice using the previous map.
5. Place all the jobs to the rate limit queue and remove these elements from the arbitration collection.

###### Controller Reconcile Process

1. Do reconcile process as before.

##### Arbitration Mechanism

The Arbitration Mechanism works after the descheduler and mainly manages the PodMigrationJobs generated by the descheduler. It decides which PodMigrationJob to execute, to provide a guarantee for the stability of the descheduler and migration, and improve the stability of the system and applications.

It arbitrates the PodMigrationJobs in the arbitration collection and places the PodMigrationJobs already passed the arbitration into the rate limit queue.

###### Sort PodMigrationJob

![image](/docs/images/arbitration-mechanism-sort-design.svg)

- Using the **stable sorting** method, sort PodMigrationJobs in the following order:
  - The time interval between the start of migration and the current, the smaller the interval, the higher the ranking.
  - The Pod priority of PodMigrationJob, the lower the priority, the higher the ranking.
  - BE > LS > LSR = LSE
  - Disperse Jobs by Workload
  - Make PodMigrationJobs close in the same job
  - If some Pods in the job containing PodMigrationJob's Pod is being migrated, the PodMigrationJob's ranking is higher.
- Use a map to record the sorted ranking (key is PodMigrationJob, value is position)

The definition of the type `SortFn` is as follows.

~~~ go
// SortFn stably sorts PodMigrationJobs slice based on a certain strategy. Users 
// can implement different SortFn according to their needs.
type SortFn func(jobs []*v1alpha1.PodMigrationJob, client client.Client) []*v1alpha1.PodMigrationJob
~~~

###### GroupFilter PodMigrationJob

![image](/docs/images/arbitration-mechanism-groupfilter-design.svg)

Aggregate PodMigrationJob according to different workloads and filter them based on different strategies.

- According to Workload
  - Group: Aggregate PodMigrationJob by workload.
  - Filter: 
    - Check how many PodMigrationJob of each workload are in the Running state, and record them as ***migratingReplicas***. If the **migratingReplicas** reach a certain threshold, excess parts will be excluded. 
    - Check the number of **unavailableReplicas** of each workload, and determine whether the **unavailableReplicas** exceeds the **MaxUnavailablePerWorkload**, exclude excess parts.
- According to Node
  - Group: Aggregate PodMigrationJob by Node.
  - Filter:
    - Check the number of Pods being migrated on the node where each target Pod is located. If it exceeds the maximum migration amount for a single node, exclude excess parts.
- According to Namespace
  - Group: Aggregate PodMigrationJob by Namespace.
  - Filter: 
    - Check the number of Pods being migrated in the Namespace where each target Pod is located. If it exceeds the maximum migration amount for a single Namespace, exclude excess parts.
- According to Job
  - Group: Aggregate PodMigrationJob by Job.
  - Filter:
    - Check the number of Jobs that PodMigrationJobs' Pod belongs to. If it exceeds 1, exclude excess parts.

The definition of the type `GroupFilterFn` is as follows.

~~~ go
type GroupFilterFn func(jobs []*v1alpha1.PodMigrationJob, client client.Client) []*v1alpha1.PodMigrationJob
~~~

###### Select PodMigrationJob

![image](/docs/images/arbitration-mechanism-select-design.svg)

- Sort PodMigrationJob slices based on the map.
- Place all remaining jobs to the rate limit queue.
- Remove these elements from the arbitration collection.

#### Controller Configuration

User can configure the `MigrationControllerArgs.ArbitrationArgs` through Koordinator Descheduler ConfigMap. 

```go
// MigrationControllerArgs holds arguments used to configure the MigrationController
type MigrationControllerArgs struct {
	...
	
    // Arbitration define the control parameters of the arbitration mechanism.
    // +optional
    Arbitration *ArbitrationArgs
}

// ArbitrationArgs holds arguments used to configure the Arbitration Mechanism.
type ArbitrationArgs struct {
    // Enabled defines if Arbitration Mechanism should be enabled.
    // Default is true
    Enabled bool
    
    // Interval defines the running interval (second) of the Arbitration Mechanism.
    // Default is 60s
    Interval int
}
```

## Alternatives

## Implementation History

- 2023-07-07: Initial proposal
- 2023-07-21: Update proposal based on review comments 
- 2023-08-10: Update proposal based on review comments