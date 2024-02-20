---
title: Support-Job-Level-Preemption
authors:
- "@xulinfei1996"
reviewers:
- "@buptcozy"
- "@eahydra"
- "@hormes"
creation-date: 2024-01-30
last-updated: 2024-01-30
status: provisional

---

# Support Job Level Preemption

<!-- TOC -->

- [Support Job Level Preemption](#support-job-level-preemption)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-goals/Future work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [User Stories](#user-stories)
        - [Key Concept](#key-concept)
        - [Implementation Details](#implementation-details)
            - [Non-Preemptible and Preemptible](#non-preemptible-and-preemptible)
            - [Quota Assignment](#quota-assignment)
            - [Preemption](#preemption)
                - [Job Level Preemption](#job-level-preemption)
                - [Adaptive Eviction Approach](#adaptive-eviction-approach)
            - [Extension Point](#extension-point)
                - [Over All](#over-all)
                - [PreFilter](#prefilter)
                - [PostFilter](#postfilter)
        - [API](#api)
            - [Pod](#pod)
            - [Plugin Args](#plugin-args)
        - [Compatibility](#compatibility)
    - [Unsolved Problems](#unsolved-problems)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)
    - [References](#references)

<!-- /TOC -->

## Summary
This proposal provides job level resource assign and preemption mechanism for the scheduler.

## Motivation
In a large scale shared-cluster, some quotas may be very busy, some quotas may be idle. In ElasticQuota plugin, we already 
support borrowing resources from idle ElasticQuota. But when a Pod associated with a borrowed ElasticQuota needs to get 
resources back, Job granularity is not considered. For Pods belonging to the same Job, we need to perform preemption at Job 
granularity to ensure that we get enough resources to meet the requirements of the Job and improve resource delivery efficiency.

### Goals

1. Supplement necessary APIs, improve preemption-related APIs, and support job granular preemption mechanism.

2. Clearly define the job-granular preemption mechanism.

### Non-goals/Future work

## Proposal
In this proposal, we use ElasticQuota to implement the job level arrangement and preemption. Each ElasticQuota declares 
its "min" and "max". The semantics of "min" is the ElasticQuota's non-preemptible resources. If ElasticQuota's "request" 
is less than or equal to "min", the ElasticQuota can obtain equivalent resources to the "request". The semantics of "max" 
is the ElasticQuota's upper limit of resources. We require "min" should be less than or equal to "max".

"non-preemptible" means the pod can't be preempted during preemption, so we set the limit for non-preemptible pods used
as the "min". "preemptible" means the pod can be preempted during preemption, so we use the "max" minus the request
for non-preemptible pods as the limit for preemptible resource usage.

### User Stories
#### Story1
If job contains multiple pods, and only some of the pods can assign successfully, we suppose scheduler to 
trigger preemption for the left not assigned pods. 

#### Story2
If only some of the not assigned pods can be assigned during preemption, we suppose the preemption should 
not really happen, which also means there are no victim pods evicted during preemption.

### Key Concept
1. There are two kind of pods: non-preemptible and preemptible. non-preemptible pod will be limited by quota's min.
   preemptible pod will be limited by quota's max. If job is preemptible, please fill job's pods with label
   `quota.scheduling.koordinator.sh/preemptible=true`, if job is non-preemptible, please fill job's pods with label
   `quota.scheduling.koordinator.sh/preemptible=false`.

2. Typically, we use pod.spec.PreemptionPolicy to judge whether a pod can preempt preemptible pods. If pod's policy is
   PreemptLowerPriority, it can trigger preemption. Higher priority pods can preempt lower priority pod. The
   pod.spec.PreemptionPolicy is a commonly used parameter for determining whether a Pod can trigger preemption eviction.
   However, users may not have been aware of this, resulting in all existing pods potentially triggering preemption, which
   is not our intention. Therefore, we introduce a new label `quota.scheduling.koordinator.sh/can-preempt` to address.

3. In AI scenario, job has property of "AllOrNothing", which means the process of resource allocation and preemption must
   be executed by job level, not pod level.In koordinator, not only `pod-group.scheduling.sigs.k8s.io` is supported, but
   also other labels are supported to identify Pods that belong to the same Job. Considering compatibility, we continue
   to use label `pod-group.scheduling.sigs.k8s.io` to support it.

### Implementation Details

#### Non-Preemptible and Preemptible
When user submit pods, it should mark pods as non-preemptible or preemptible by label configuration. In koordinator, if pods
do not declare `quota.scheduling.koordinator.sh/preemptible`, the pod can be preempted. However, in some scenario, 
the expectation is opposite, the exist running pods don't have this label and can't be preempted. So here we will add a
new plugin args to control this behaviour, please see in api part.

#### Quota Assignment
When user submit job, it should fill a job-id into pod's label. When we try to assign pod, we should first list all
pods with the same job, and summarize all pods' request as totalRequest. For non-preemptible job, we should check whether
non-preemptibleTotalUsed + newNonPreemptibleJobTotalRequest <= minQuota and totalUsed + newNonPreemptibleJobTotalRequest <= maxQuota. 
For preemptible job, we should check whether math.Min(minQuota, non-preemptibleTotalRequest) + preemptibleTotalUsed + newPreemptibleJobTotalRequest <= maxQuota.

[Koordinator Quota Management](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20220722-multi-hierarchy-elastic-quota-management.md)
provide a mechanism to share idle resources among quotas, which called "runtime quota". Generally it will distribute idle
resources fairly to different quotas according to a certain proportion. However, in AI training scenario, due to job's
"AllOrNothing" property, the runtime quota mechanism may lead to preemptible job can't be assigned successfully because
each quota only obtains partial idle resources. So our strategy is that the idle resources will be assigned to each
quota by a greedy way, which means if one quota's preemptible job comes first, it can use all idle resources as much as it reaches its max.

#### Preemption
##### Job Level Preemption
The new job-granularity preemption mechanism follows the existing scheduling and orchestration constraint strategy, and 
will retry executing Filter during the preemption process to ensure that the candidate nodes meet the requirements for 
preempting Pods.

#### Adaptive Eviction Approach
The plugin will patch preempted pod a label `scheduling.koordinator.sh/soft-eviction` to reflect the pod is preempted.
We expect there will be a third party operator handle these labels and really do delete pod action, it will give
the user spaces to do some clean jobs before pod is really deleted. If multiple operators need to do clean jobs, the
user can also fill pod finalizer to achieve the target.  Finally, we will also update preemption event for the preempted
pod.

#### Extension Point

##### Over All
The new\delta parts are:
1. Non-preemptible and preemptible resource judgement.
2. Job level preemption logic.
3. Quota resource recycling logic.

##### PreFilter
we should first list all pods with the same job, and summarize all pod's request as totalRequest. For non-preemptible job,
we should check whether non-preemptibleTotalUsed + newNonPreemptibleJobTotalRequest <= minQuota and totalUsed + newNonPreemptibleJobTotalRequest <= maxQuota;
for preemptible job, we should check whether math.Min(minQuota, non-preemptibleTotalRequest) + preemptibleTotalUsed + newPreemptibleJobTotalRequest <= maxQuota.

##### PostFilter
We need job level preemption, which means we can't execute preemption pod by pod as usual. If one of pod preemption failed,
the previous pod successful preemption will be meaningless and harmful. So the process is as followed:

1. Judge pod can trigger preempt or not by plugin args and pod label. If job is non-preemptible, non-preemptibleTotalUsed + 
   non-preemptibleJobRequest <= minQuota, then job can trigger preempt. If job is preemptible, 
   math.Min(minQuota, non-preemptibleTotalRequest) + preemptibleTotalUsed + newPreemptibleJobTotalRequest <= maxQuota, then 
   job can trigger preempt.
2. List of all pods of the job, iterate over waiting pods of framework to get all not-assumed pods list.
   (some pods may assume successfully in previous assign process and in waiting permit status).
3. Iterate filteredNodeStatusMap of PostFilter's variable which code is not UnschedulableAndUnresolvable, then try to remove
   all preemptible pods for nodeInfo's copy. To avoid after preemption, the non-preemptibleUsed + preemptibleUsed > maxQuota, 
   we also list all running preemptible pods of this quota, and try to remove all preemptible pods for nodeInfo's copy too.
4. For each not-assumed pod of the job, try to execute RunFilterPlugins based on nodeInfo's copy, if success, add pod into nodeInfo.
5. If all not-assumed pod preemption process success, which means job preemption success. We do preempted-pod try-assign-back
   logic and finally pick up real preempted pods and node. If one not-assumed pod preemption fail, we think this round
   preemption is fail. During assign-back process, we should check the quota limit of the preemption quota.
6. For all preempted pods, we patch a preemption label for upper operator to handle some clean logic and delete pod.
7. For all preemptor pods, we first execute framework.AddNominatedPods() to hold the resources in memory, and we should
   store preemptor pod/preempted pods/preempted node to the local cache. Due to preempted pod are deleted by
   other operator, if deleting process is slow and resource are not returned, once the next round of scheduling process
   comes, it will trigger preemption again, which may generate new preempted pods, and this process may not stop
   util all preemptible are preempted. So at the beginning of PostFilter, it will check cache if last round preemption finishes 
   or not.


### API
#### Pod
We introduce some labels to describe pod behaviour.
- `pod-group.scheduling.sigs.k8s.io` is filled by user, describe pod's jobName.
- `quota.scheduling.koordinator.sh/preemptible`is filled by user, indicate whether is non-preemptible or preemptible.
- `scheduling.koordinator.sh/soft-eviction` is filled by scheduler, indicate the pod is preempted.
- `pod.spec.PreemptionPolicy` is filled by user, describe whether a pod can trigger preemption to preemptible pods.
- `quota.scheduling.koordinator.sh/can-preempt` is filled by user, also describe whether a pod can trigger preemption to preemptible pods.

If pod does not have `quota.scheduling.koordinator.sh/preemptible` label, we declare that the pod equals to non-preemptible.

if you want to declare pod belongs to a job, please use as follows:
```yaml
labels:
  pod-group.scheduling.sigs.k8s.io: "job1"
```

if you want to declare pod is non-preemptible or preemptible, please use as follows(one job's pods should be homogeneous):
```yaml
labels:
  quota.scheduling.koordinator.sh/preemptible: "false"
```
or
```yaml
labels:
  quota.scheduling.koordinator.sh/preemptible: "true"
```

if you want to declare pod can trigger preemption to preemptible pods, please use as follows:
```yaml
Spec:
  preemptionPolicy: PreemptLowerPriority
labels:
  quota.scheduling.koordinator.sh/can-preempt: "true"
```

if you want to check preemptible preempted message, please focus on label:
```yaml
labels:
  scheduling.koordinator.sh/soft-eviction: "{preemptJob:job2...}"
```

#### Plugin Args
we now introduce plugin args:
`disableDefaultPreemptiable` control pod can be preempted without label `quota.scheduling.koordinator.sh/preemptible`
`disableDefaultTriggerPreemption` control pod can trigger preemption by label `quota.scheduling.koordinator.sh/can-preempt`, or
by preemption policy PreemptLowerPriority.
`podShouldBeDeletedWhenPreempted` control a preempted pod should be deleted by scheduler.

```
args:
  disableDefaultPreemptiable: true\false
  disableDefaultTriggerPreemption: true\false
  podShouldBeDeletedWhenPreempted: true\false
```

### Compatibility
In some scenario, some users may expect the pod should be deleted directly by scheduler, some user want other operators to
do some clean jobs before really deleted. So here we introduce a new plugin args to control this behaviour.

In koordinator, if pods do not declare `quota.scheduling.koordinator.sh/preemptible`, the pod can be preempted. However,
in some scenarios, the expectation is opposite, the exist running pods don't have this label and can't be preempted.
So here we will add a new plugin args to control this behaviour.

In koordinator, if pods declare `preemptionPolicy: PreemptLowerPriority`, the pod can trigger preemption. However, in some 
scenarios, the expectation is opposite, the exist running pods' preemptionPolicy all equal to PreemptLowerPriority. So
here we will add introduce a new label and add a new plugin args to control this behaviour.

We use `pod-group.scheduling.sigs.k8s.io` to declare the job, and this label has been already used in coScheduling.
In coScheduling, user can declare minimumNumber and totalNumber. For now, we only support minimumNumber=totalNumber scenario
cause AI job training job's property.

## Alternatives

## Unsolved Problems

## Implementation History

## References