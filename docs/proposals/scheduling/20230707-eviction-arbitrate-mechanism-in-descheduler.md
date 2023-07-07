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
last-updated: 2023-07-07
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
        - [Pod Migration Job CRD Field](#pod-migration-job-crd-field)
            - [Migration Job Spec](#migration-job-spec)
        - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
            - [PodMigrationJob Controller](#podmigrationjob-controller)
                - [Controller Process](#controller-process)
                    - [Controller Reconcile Process](#controller-reconcile-process)
                    - [Arbitration Process](#arbitration-process)
                    - [Migration Process](#migration-process)
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

This proposal designed a mechanism in descheduler to arbitrate `PodMigrationJob`, through which the system's stability could be improved when a mount of pods or some important pods is being evicted. 

## Motivation

Arbitrate Mechanism is an important capability that Pod Migration relies on, and Pod Migration is relied on by many components (such as deschedulers). But pod Migration is a complex process,  involving steps such as auditing, resource allocation, and application startup, and is mixed with application upgrading, scaling scenarios, and resource operation and maintenance operations by cluster administrators. 

So when a large number of PODs are simultaneously migrated, this may have some impact on the stability of the system. In addition, if many pods of the same workload are migrated simultaneously, it will also have an impact on the stability of the application. Moreover, if a job's pod migration takes too long, it can affect the job's completion time.

Therefore, it is necessary to design an arbitration mechanism. This arbitration mechanism will select suitable `PodMigrationJob` to execute and control the execution speed of `PodMigrationJob` (to avoid a large number of jobs executing simultaneously), thereby ensuring the stability of the system and application.

### Goals

1. Defines an Arbitrate Mechanism Configuration, through which user can configure the arbitration mechanism.
2. Describe a simple central flow control mechanism to limit the number of pod migrations over a period of time.
3. Describe in detail the design details of the arbitrate mechanism.

### Non-Goals/Future Work

1. A new CRD or Controller.
2. The generation process of PodMigrationJob.
3. The specific execution process of PodMigrationJob.

## Proposal

### User Stories

#### Story 1

The descheduler evicts the Pods through the Eviction API, and lots of pods may be migrated to the same node by the Migration Controller at the same time. This may cause an increase in the CPU and network pressure of the node, thereby affecting the stability of other Pods on the node.

#### Story 2

Multiple pods of the same workload may be simultaneously evicted by the descheduler. If these PodMigrationJobs are executed simultaneously, it may cause the number of workload replicas to fall below the watermark, thereby affecting the stability of the application.

#### Story 3

Multiple pods in the same job are evicted. If the interval between these pods' migration start time is too long, it will result in an increase in the completion time of the entire job.

### Pod Migration Job CRD Field

In order to support the above user stories, we add some fileds in Custom Resource Definition(CRD) `PodMigrationJob`.

#### Migration Job Spec

```go
type PodMigrationJobSpec struct {
    // PriorityClassName defines the priority of the PodMigrationJob which should be 
    // the name of some PriorityClass object.
    // +optional
    PriorityClassName string `json:"priorityClassName,omitempty"`
    
    // Priority define the priority of the PodMigrationJob.
    // If this field is set, PriorityClassName will be discarded.
    // +optional
    Priority          *int32 `json:"priority,omitempty"`
}
```

- `PriorityClassName` indicates the priority of the PodMigrationJob which should be the name of some PriorityClass object, and the Priority value will be set as the value of the PriorityClass.
- `Priority` indicate the priority of the PodMigrationJob. The higher the priority, the greater the possibility of arbitration. If this field is set, PriorityClassName will be discarded.

### Implementation Details/Notes/Constraints

#### PodMigrationJob Controller

The **PodMigrationJobController** will evaluate all PodMigrationJobs and select a batch of PodMigrationJob and execute them. This selection process is called the **arbitration mechanism**. The reason why the arbitration mechanism is introduced is mainly to control the stability risk and control the cost of migrating Pods. The arbitration mechanism includes three stages: `Sort`, `GroupFilter` and `Select`.

##### Controller Process

Arbitration Mechanism Supports a simple central flow control mechanism to limit the number of migrations over a period of time. To achieve this goal, we used a rate limited queue to control the job processing speed during the migration process.

The controller process can be roughly divided into three parts, namely the Controller Reconcile Process, Arbitration Process, and Migration Process:

- **Controller Reconcile Process**: This is the `Reconcile` funtion of PodMigrationJob Controller, which aims to put PodMigrationJobs whose phase if `pending` or empty to the arbitration collection.
- **Arbitration Process**: This process periodically arbitrates the PodMigrationJobs in the arbitration collection and places the PodMigrationJobs selected by the arbitration into the rate limit queue.
- **Migration Process**: This process will read `PodMigrationJob` from the rate limit queue and call `doMigrat`e to handle the `PodMigrationJob`.

###### Controller Reconcile Process

1. Get the `PodMigrationJob`.
2. If PodMigrationJob's phase is empty or `pending`, put it to the arbitration collection.
3. Else do other things.

###### Arbitration Process

1. Sort the elements in the arbitration collection to generate a slice.
2. Use a map to record the sorted positions of each `PodMigrationJob` in the slice, with key being the `PodMigrationJob` and value being the position.
3. Call different `GroupFilter` functions in sequence for group and filter operations to update the slice.
4. Resort the elements in the slice using the previous map.
5. Place the first n elements of slice to the rate limit queue and remove these elements from the arbitration collection.

###### Migration Process

1. For each element in the rate limit queue, call `doMigrate` to process the PodMigrationJob.

##### Arbitration Mechanism

The Arbitration Mechanism works after the descheduler and mainly manages the PodMigrationJobs generated by the descheduler. It decides which PodMigrationJob to execute, to provide a guarantee for the stability of the descheduler and migration, and improve the stability of the system and applications.

It arbitrates the PodMigrationJobs in the arbitration collection and places the PodMigrationJobs already passed the arbitration into the rate limit queue.

###### Sort PodMigrationJob

- Using the **stable sorting** method, sort PodMigrationJobs in the following order:
  - The time interval between the start of migration and the current, the smaller the interval, the higher the ranking.
  - The Pod priority of PodMigrationJob, the lower the priority, the higher the ranking.
  - If some pods in the job containing PodMigrationJob's pod is being migrated, the PodMigrationJob's ranking is higher.
  - The higher the migration priority, the higher the ranking.
  - BE > LS > LSR = LSE
- Use a map to record the sorted ranking (key is PodMigrationJob, value is position)
- Set all `PodMigrationJob` ranking under the same job in the map to the highest one of them (Try to migrate pods from the same job simultaneously as much as possible).

The definition of the type `SortFn` is as follows.

~~~ go
// SortFn stably sorts PodMigrationJobs slice based on a certain strategy. Users 
// can implement different SortFn according to their needs.
type SortFn func(jobs []*v1alpha1.PodMigrationJob, client *client.Client) []*v1alpha1.PodMigrationJob
~~~

###### GroupFilter PodMigrationJob

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
    - Check the number of Pods being migrated in the Namespace where each target Pod is located. If it exceeds the maximum migration amount for a single Namespace, exclude excess parts

The definition of the type `GroupFilterFn` is as follows.

~~~ go
type GroupFilterFn func(jobs []*v1alpha1.PodMigrationJob, client *client.Client) []*v1alpha1.PodMigrationJob
~~~

###### Select PodMigrationJob

- Sort PodMigrationJob slices based on the map.
- Place the first **n** elements of slice to the rate limit queue.
- Remove these elements from the arbitration collection.

#### Controller Configuration

User can configure the `MigrationControllerArgs.ArbitrationArgs` through Koordinator Descheduler ConfigMap. 

```go
// MigrationControllerArgs holds arguments used to configure the MigrationController
type MigrationControllerArgs struct {
	...
	
    // ArbitrationArgs define the control parameters of the arbitration mechanism.
    // +optional
    ArbitrationArgs *PodMigrationArbitrationArgs
}

// PodMigrationArbitrationArgs holds arguments used to configure the Arbitration Mechanism.
type PodMigrationArbitrationArgs struct {
    // ArbitrationEnabled defines if Arbitration Mechanism should be enabled.
    // Default is true
    ArbitrationEnabled bool
    
    // ArbitrationInterval defines the running interval of the Arbitration Mechanism.
    // Default is 1 minute
    ArbitrationInterval time.Duration
    
    // ArbitrationProcessQueueQPS defines the speed of process queues (per second)
    // Default to 3
    ArbitrationProcessQueueQPS int
    
    // ArbitrationSelectJobRate defines the proportion of the number of PodMigrationJobs 
    // that the Arbitration Mechanism passes each time.
    // Default to 1.2
    ArbitrationSelectJobRate float32
}
```

## Alternatives

## Implementation History

- 2023-07-07: Initial proposal