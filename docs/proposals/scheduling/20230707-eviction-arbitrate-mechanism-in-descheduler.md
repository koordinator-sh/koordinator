---
title: Eviction Arbitration Mechanism in Descheduler
authors:
  - "@baowj-678"
reviewers:
  - "@hormes"
  - "@eahydra"
  - "@FillZpp"
  - "@ZiMengSheng"
  - "@zwzhang0107"
  - "@saintube"
  - "@jasonliu747"
creation-date: 2023-07-07
last-updated: 2023-08-22
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
            - [Arbitration Mechanism](#arbitration-mechanism)
                - [About Arbitration Queue](#about-arbitration-queue)
                - [Arbitration Process](#arbitration-process)
                    - [Sort PodMigrationJob](#sort-podmigrationjob)
                    - [Filter PodMigrationJob](#filter-podmigrationjob)
                - [About Work Queue](#about-work-queue)
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

1. Provide an Arbitrate Mechanism to sort and filter the PodMigrationJobs from arbitration queue periodically.
2. Provide an event handler to intercepts event in creation status and place them in the arbitration queue.
3. Defines an Arbitrate Mechanism Configuration, through which user can configure the arbitration mechanism.

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

#### Arbitration Mechanism

The **PodMigrationJob Controller** will evaluate all PodMigrationJobs and select a batch of PodMigrationJobs from arbitration queue and add them to the reconcile work queue for executing. This selection process is called the **arbitration mechanism**. The arbitration mechanism includes two stages: `Sort` and `Filter`. And the reconcile work queue will help us to achieve rate limit.


This diagram can help us have a macro understanding of the system process.

![image](/docs/images/arbitration-mechanism-design.svg)

The controller process can be roughly divided into two parts, namely the **Arbitration Process**, **Controller Reconcile Process** (the EventHandler in the diagram is just an interceptor):

- **Arbitration Process**: This process periodically arbitrates the PodMigrationJobs in the arbitration collection and places the PodMigrationJobs selected by the arbitration into the work queue.
- **Controller Reconcile Process**: This is the original reconcile process of PodMigrationJob.

The following is a time series diagram that can help us understand the relationship between arbitration queue, arbitration mechanisms, and work queue.

![image](/docs/images/arbitration-mechanism-time-diagram.svg)

Below, I will provide a detailed introduction about **arbitration queue**, **arbitration process** and **work queue**.

##### About Arbitration Queue

1. From: Event handler intercepts all events in creation status and place them in the arbitration queue.
2. To: The arbitration queue is consumed by arbitration process.

##### Arbitration Process

The Arbitration Process works after the descheduler and before the PodMigrationJob Reconcile and mainly manages the PodMigrationJobs generated by the descheduler. It decides which PodMigrationJob to reconcile, to provide a guarantee for the stability of the descheduler and migration, and improve the stability of the system and applications.

It arbitrates the PodMigrationJobs in the arbitration queue and places the PodMigrationJobs already passed the arbitration into the work queue.

The brief process is as follows:
1. **Sort** the elements in the arbitration collection to generate a slice.
2. Call `NonRetryableFilter` and `RetryableFilter` functions to handle each PodMigrationJob in slice in loop.

Now, I will provide a detailed introduction about **Sort** and **Filter** process.

###### Sort PodMigrationJob

![image](/docs/images/arbitration-mechanism-sort-design.svg)

- Sort PodMigrationJobs in the following order:
    - The time interval between the start of migration and the current, the smaller the interval, the higher the ranking.
    - The Pod priority of PodMigrationJob, the lower the priority, the higher the ranking.
    - BE > LS > LSR = LSE.
    - Disperse Jobs by workload.
    - Make PodMigrationJobs close in the same job.
    - If some Pods in the job containing PodMigrationJob's Pod is being migrated, the PodMigrationJob's ranking is higher.
- Use a map to record the sorted ranking (key is PodMigrationJob, value is position).

The definition of the type `SortFn` is as follows.

~~~ go
// SortFn stably sorts PodMigrationJobs slice based on a certain strategy. Users 
// can implement different SortFn according to their needs.
type SortFn func(jobs []*v1alpha1.PodMigrationJob) []*v1alpha1.PodMigrationJob
~~~

###### Filter PodMigrationJob

![image](/docs/images/arbitration-mechanism-filter-design.svg)

Filter will check each PodMigrationJob in the slice in order, and call the `NonRetryableFilter` and `RetryableFilter` functions to filter this PMJ.

The specific process is shown in the figure above, and the following will briefly introduce the process.

- Read PMJ from list in loop.
- Call `NonRetryableFilter` to filter the PMJ.
  - If failed, update PMJ phase to `Failed` and delete if from the arbitration queue.
- Call `RetryableFilter` to filter the PMJ.
  - If failed, continue the next PMJ.
- If passed, update PMJ annotation, remove it from the arbitration queue and add it to the work queue.

If PMJ does not pass `NonRetryableFilter`, it will be directly marked as Failed. And if PMJ does not pass the `RetryableFilter`, it will be filtered again in the next arbitration.

`RetryableFilter` will filter PMJ based on workload, node, etc., as follows:

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

##### About Work Queue

1. From: 
   1. Original kubernetes events (update/delete/genetic).
   2. Events that passed arbitration mechanism.
2. To: The work queue is consumed by the Controller Reconcile Process.

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
    
    // Interval defines the running interval (ms) of the Arbitration Mechanism.
    // Default is 500ms
    Interval int
}
```

## Alternatives

## Implementation History

- 2023-07-07: Initial proposal.
- 2023-07-21: Update proposal based on review comments.
- 2023-08-10: Update proposal based on review comments.
- 2023-08-15: Update proposal based on review comments.
- 2023-08-21: Update proposal based on review comments.
- 2023-08-22: Update proposal based on review comments.