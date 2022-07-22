---
title: batchpod schedule
authors:
- "@buptcozy"
reviewers:
- "@hormes"
- "@allwmh"
- "@honpey"
- "@jasonliu747"
- "@saintube"
- "@stormgbs"
- "@zwzhang0107"
- "@eahydra"
creation-date: 2022-07-04
last-updated: 2022-07-04
status: provisional

---

# batchpod schedule

## Table of Contents

<!--ts-->

* [batchpod schedule](#batchpod-schedule)
  * [Table of Contents](#table-of-contents)
  * [Summary](#summary)
  * [Motivation](#motivation)
  * [Goals](#goals)
  * [User Stories](#user-stories)
    * [Story AI](#story-AI)
  * [Implementation Details](#implementation-details)
    * [pod annotation to describe batchpod](#pod-annotation-to-describe-batchpod)
    * [batchpod crd](#batchpod crd)
    * [system-design](#system-design)

<!--te-->

## Summary
batchpod schedule

## Motivation
1. in gpu scenario, when one cluster has multi-type-gpu-model nodes like P100 and V100, we want one job's worker run in 
same gpu-model nodes to avoid large speed gap between different gpu-model. This may lead not only efficiency problem, 
but also correction problem.

2. in gpu nvlink scenario, we want to pack one job's pods in a best-nvlink-topo result in one card or between multi-card

so batchpod means some pods have some same properties or strong relationships when scheduling, which we should provide 
a mechanism for these pods to communicate with each other.

specially, gang is another "batchpod". due to gang concept is so win support among the people，we make it independent 
from batchpod concept.

### Goals
1. propose some pod's annotations to announce batchpod properties

2. design a batchpod-crd to display batchpod-status

3. scheduler should provide batchpod data-structure and schedule-plugin 

### User Stories

#### Story AI
1. in gpu scenario, when one cluster has multi-type-gpu-model nodes like P100 and V100, we want one job's worker run in 
same gpu-model nodes to avoid large speed gap between different gpu-model. This may lead not only efficiency problem, 
but also correction problem.

2. in gpu nvlink scenario, we want to pack one job's pods in a best-nvlink-topo result in one card or between multi-card
     
## Implementation Details
### pod annotation to describe batchpod
we choose to create batchpod-related by pod's annotations instead of batchpod crd. This is because we don't want high level
operator to maintain batchpod-crd's life circle. meanwhile, it's inconvenient to maintain receive-order-issue's between 
batchpod-crd and pod in scheduler side.

when we receive first pod which as batchpod-information in annotations, we believe it as batchpod-config and assume all 
pods in one batchpod have same config.

the gang basic data-structure is below:

```go
type BatchPod struct {
    Name                 string
    BatchInfo            BatchInfo
    PodMap               sets.string
}

type BundleInfo struct {
    Name                     string
    Children                 map[string]*PodInfo  
    BoundChildren            map[string]*PodInfo
    WaitingForBindChildren   map[string]*PodInfo
    MinRequiredNumber        int
    TotalChildrenNum         int
}
```

let's assume a job has two roles: ps and worker, each role has several pods. podA belongs to ps, podB belongs to worker.
if we want bind-condition is ps and worker both reach min-required-number condition, then we can:
```go
podA.Annotation[GangName] = "job1"
podA.Annotation[GangBundleName] = "ps"
podA.Annotation[GangWaitTime] = "3600s"
podA.Annotation[GangBundleMinRequiredNumber] = 5
podA.Annotation[TotalChildrenNum] = 5

podB.Annotation[GangName] = "job1"
podB.Annotation[GangBundleName] = "worker"
podB.Annotation[GangWaitTime] = "3600s"
podB.Annotation[GangBundleMinRequiredNumber] = 5
podB.Annotation[TotalChildrenNum] = 5
```

if we want bind-condition is ps and worker reach min-required-number condition independently, then we can:
```go
podA.Annotation[GangName] = "job1-ps"
podA.Annotation[GangBundleName] = "ps"
podA.Annotation[GangWaitTime] = "3600s"
podA.Annotation[GangBundleMinRequiredNumber] = 5
podA.Annotation[TotalChildrenNum] = 5

podB.Annotation[GangName] = "job1-worker"
podB.Annotation[GangBundleName] = "worker"
podB.Annotation[GangWaitTime] = "3600s"
podB.Annotation[GangBundleMinRequiredNumber] = 5
podB.Annotation[TotalChildrenNum] = 5
```

### batchpod crd
the reason we need gang crd is:
1. easily to query gang status
2. once gang has been satisfied, if there happens new allocations(.like be preempted), we should bind new allcocation
   as soon as possible, so we should keep this status in gang crd in case scheduler failover.

```go
type Gang struct {
    metav1.TypeMeta

    metav1.ObjectMeta

    Status GangStatus
}

type GangStatus struct {
    Name         string
    WaitTime     time.Duration
    Bundles      map[string]*BundleInfo
    HasBound     bool
}

type BundleInfo struct {
    Name                     string
    Children                 map[string]struct{}  
    BoundChildren            map[string]struct{}  
    WaitingForBindChildren   map[string]struct{}  
    MinRequiredNumber        int
    TotalChildrenNum         int
}
```

### system-design
1. we should create a new gang-plugin to implement PreFilter\Filter\Allocate\Permit stage
2. we should implement queue-sort-func to make gang-pods as closer as possible in scheduler-queue.
3. we should implement gang-crd update logic in gang-plugin
4. we should implement gang-crd recover logic in gang-plugin