---
title: Gang scheduling
authors:
  - "@buptcozy"
reviewers:
  - "@eahydra"
  - "@hormes"
  - "@yihuifeng"
  - "@honpey"
  - "@zwzhang0107"
  - "@jasonliu747"
creation-date: 2022-07-01
last-updated: 2022-07-01

---

# Gang scheduling

## Table of Contents

<!--ts-->

* [Gang scheduling](#Gang-scheduling)
    * [Table of Contents](#table-of-contents)
    * [Summary](#summary)
    * [Motivation](#motivation)
      * [Compared with competitors](#Compared-with-competitors)
        * [Coscheduling](#Coscheduling)
      * [Goals](#goals)
      * [Non Goals and Future Work](#Non-Goals-and-Future-Work)
    * [Proposal](#Proposal)
      * [Key concept](#key-concept)
        * [Strict and NonStrict](#Strict-and-NonStrict)
        * [GangGroup](#ganggroup)
        * [After gang](#after-gang)
      * [API](#API)
        * [Definition](#definition)
          * [CRD way](#crd-way)
            * [Example](#example)  
          * [Annotation way](#annotation-way)
            * [Example](#example)         
      * [Implementation Details](#Implementation-Details)
        * [QueueSortPlugin](#QueueSortPlugin)
        * [Data-Structure](#data-structure)
        * [GangPlugin](#gang-plugin)
    * [Unsolved Problems](#Unsolved-Problems)
    * [Alternatives](#Alternatives)
    * [Implementation History](#Implementation-History)
    * [References](#References)
<!--te-->

## Summary
This proposal provides Gang mechanism for the scheduler to control pods binding opportunity. User can declare a resource-collection-minimum number, 
only when assigned-resources reach the given limitation can trigger the binding. We provide `Strict` and `NonStrict` to 
control the resource-accumulation-process by a configuration. We also provide a two-level Gang description for better matching 
the real scenario, which is different from community.

## Motivation
In AI scenarios, lots of jobs need Gang scheduling. The community have lots of related implements such as `Coscheduling` or `vocalno`.
We received lots of inspirations in the design process from them.

### Compared with competitors

#### Coscheduling
1. `Coscheduling` implement a new queue-sort interface and other methods to let one Gang's pods get out of the queue in order as much as possible.
If a pod failed to be scheduled, the requests that have been successfully scheduled in this round of Gang scheduling cycle will be rolled back,
and the remaining pods waiting for scheduling will be rejected in PreFilter check until this scheduling cycle passed. 
For example, there is a Gang requires 10 tasks to be scheduled, if first 5 tasks allocated, the 6th task failed to be scheduled,
`Coscheduling` will roll-back first 5 tasks and ignore the remaining 4 tasks in this Gang scheduling cycle. `Coscheduling` simply use a 
global time interval to control the Gang scheduling cycle. The first defect is that the uniform time interval will cause 
some problems. If the time configuration is too long, it will lead to useless waiting; If the time configuration is too short, 
it will lead to useless scheduling. Secondly, it is very difficult for a large job to meet all resource requests at one time. 
This mechanism will lead to a very low probability of full resources, and eventually make the job starve to death. We call this process as `Strict`.

2. Some jobs have complex Gang requirements. For example, a job has several roles. Each role will have several pods 
and its own Gang conditions. Jobs also need different roles to form different GangGroups. All pods in a GangGroup can 
trigger the bind process only after all roles in a GangGroup meet their Gang conditions. The `Coscheduling` can't meet
this requirement.

### Goals
1. Define API to announce Gang scheduling configuration.

2. Provides a scheduler plugin to achieve Gang scheduling ability.

### Non Goals and Future Work
1. Provide ability to solve Gang resource deadlock problems with `NonStrict`.

## Proposal

### Key concept

#### Strict and NonStrict

As mentioned above, in `Strict`, if a pod failed to be scheduled, the pods that have been successfully scheduled in 
this scheduling cycle will be rolled back, and the remaining pods waiting for scheduling will be rejected in 
PreFilter check util this scheduling cycle passed. We call this mode is `Strict`.

In `NonStrict`, if a pod failed to be scheduled, it has no impact on any other pod. We will continue to accumulate 
the allocated pod until the condition of Gang is met. This process is friendly to Gangs with large number of pods, but it 
will increase the risk of resource deadlock between Gangs. For example, the quota of the quota group is 10(quota will be proposed later), 
and the user submits three Gangs with 5 pods. Due to various plugin constraints, Gang1\2\3 may allocate resources of 3\3\4 respectively. 
Since the quota group's quota is full, there will be no new resource scheduling. We call this is resource deadlock of resource Gang.
In future proposal, we will try to fix this problem.

#### GangGroup
As mentioned above, Some jobs have complex Gang requirements. For example, a job has several roles. Each role will have several pods 
and its own Gang conditions. Jobs also need different roles to form different GangGroups. All pods in a GangGroup can 
trigger the bind process only after all roles in a GangGroup meet their Gang conditions. So we introduce `GangGroup` concept,
which allow user to bundle different Gangs together.

#### After Gang
It should be noted that, if the resource accumulation conditions of Gang are met, then some pods failed in the process of binding,
or some bound pods are preempted\rescheduled, should the constraints of Gang still be effective in the process of resource reallocation? 
Because the initial purpose of Gang is to require pods to be pulled up at the same time, if some pods have been pulled up, 
then the subsequent Gang behavior is meaningless. Therefore, when once Gang has been satisfied, all subsequent resource allocations 
are no longer constrained by Gang rules, and their performance is similar to ordinary pod.

As mentioned above, `WaitTime` is the max wait time since first pod comes to permit stage. If `WaitTime` is timeout, 
scheduler will roll back all assumed pods, update each pod's annotation with `gang.scheduling.koordinator.sh/timeout=true`, and
won't schedule these pods anymore. User should pay attention to this status and delete pods timely.

### API
#### Definition

Our original intention is to improve and enhance the ability of the community's original `PodGroup`, so we will be 
compatible with the way the community declares the `PodGroup`. We also provide a lighting way to just use annotations to 
use Gang feature.

#### CRD way
User can use `PodGroup` CRD in community to declare a gang:
```go
type PodGroup struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec PodGroupSpec `json:"spec,omitempty"`
    Status PodGroupStatus `json:"status,omitempty"`
}
type PodGroupSpec struct {
    MinMember int32 `json:"minMember,omitempty"`
    MinResources *v1.ResourceList `json:"minResources,omitempty"`
    
    ScheduleTimeoutSeconds *int32 `json:"scheduleTimeoutSeconds,omitempty"`
}
```
Pod should use `pod-group.scheduling.sigs.k8s.io` in label to associate with `PodGroup`.

Also, we introduce some optional definitions as below:
```yaml
gang.scheduling.koordinator.sh/total-number
gang.scheduling.koordinator.sh/mode        
gang.scheduling.koordinator.sh/groups
```
- `gang.scheduling.koordinator.sh/name` indicates the gang's name, it should be emphasized that the name should be in the form of RFC 1123 

- `gang.scheduling.koordinator.sh/total-number` helps to calculate Gang scheduling cycle in `strict mode`, you can 
find more detail in `Data-Structure` chapter. Default equals to `gang.scheduling.koordinator.sh/min-available`.

- `gang.scheduling.koordinator.sh/mode` determines `Strict` or `NonStrict`. Default is `Strict`.

- `gang.scheduling.koordinator.sh/groups` describes GangGroups. Default is empty, which means don't need to form a `GangGroup` with others,
and the gangs in one gangGroup can from different namespaces.

`gang.scheduling.koordinator.sh/total-number`, `gang.scheduling.koordinator.sh/mode`, `gang.scheduling.koordinator.sh/gang-groups` should be found in
`PodGroup`'s annotation if needed.

##### Example
When user apply a basic gang, the example is as follows:
```yaml
apiVersion: v1alpha1
kind: PodGroup
metadata:
  creationTimestamp: "2022-07-11T18:26:33Z"
  name: gang-a
  namespace: default
spec:
  minMember: 5
  minResources:
    cpu: "5"
    memory: "2048Mi"
  scheduleTimeoutSeconds: 600
```

Let's assume a job has two roles: A and B, each role has several pods. podA belongs to roleA, podB belongs to roleB.
roleA and roleB belongs to one GangGroup, the example is as follows:
```yaml
apiVersion: v1alpha1
kind: PodGroup
metadata:
  creationTimestamp: "2022-07-11T18:26:33Z"
  name: gang-a
  namespace: namespaceA
  annotations:
    gang.scheduling.koordinator.sh/total-number: 5
    gang.scheduling.koordinator.sh/mode: Strict
    gang.scheduling.koordinator.sh/groups: ["namespaceA/gang-a", "namespaceB/gang-b"]
spec:
  minMember: 5
  minResources:
    cpu: "5"
    memory: "2048Mi"
  scheduleTimeoutSeconds: 600
```

It should be noted that, if use Gang feature by `CRD way`, user should let high level operator maintain Gang CRD life circle 
like handling `update/create/delete` events. Also, from a Scheduler perspective, scheduler should handle receive-order-issue's 
between Gang CRD and pod. For example, if pods arrive to scheduler before Gang CRD, we have to build a fake Gang data structure 
temporarily to collect all related pods, and need to suspend the scheduling of pods until parse the configuration from real Gang CRD.

#### Annotation way
```yaml
gang.scheduling.koordinator.sh/name           
gang.scheduling.koordinator.sh/min-available
```

The upper definitions are indispensable. We are compatible with `pod-group.scheduling.sigs.k8s.io`, `pod-group.scheduling.sigs.k8s.io/name` 
and `pod-group.scheduling.sigs.k8s.io/min-available` in community. We also support new definitions to declare Gang's name and minimum number.

Also, we introduce some optional definitions as below, most are mentioned above:
```yaml
gang.scheduling.koordinator.sh/waiting-time
gang.scheduling.koordinator.sh/total-number
gang.scheduling.koordinator.sh/mode        
gang.scheduling.koordinator.sh/groups
```

- `gang.scheduling.koordinator.sh/waiting-time` represents max wait time since first pod comes to permit stage. Default is a global config.

- `gang.scheduling.koordinator.sh/total-number` helps to calculate Gang scheduling cycle in `strict mode`, you can 
find more detail in `Data-Structure` chapter. Default equals to `gang.scheduling.koordinator.sh/min-available`.

- `gang.scheduling.koordinator.sh/mode` determines `Strict` or `NonStrict`. Default is `Strict`.

- `gang.scheduling.koordinator.sh/groups` describes GangGroups. Default is empty, which means don't need to form a `GangGroup` with others.

It should be noted that, the annotation mode's parameter will overwrite CRD's mode if both exist.
And gangGroup should be announced with " gangNamespace" + "/" + "gangName "

##### Example
When user apply a basic gang, the example is as follows:
```yaml
metadata:
   annotations:
    gang.scheduling.koordinator.sh/name: gang-a
    gang.scheduling.koordinator.sh/min-available: 5
```

Let's assume a job has two roles: A and B, each role has several pods. PodA belongs to roleA, podB belongs to roleB.
roleA and roleB belongs to one GangGroup, the example is as follows:
```yaml
metadata:
   annotations:
     gang.scheduling.koordinator.sh/name: gang-a
     gang.scheduling.koordinator.sh/waiting-time: 3600s 
     gang.scheduling.koordinator.sh/min-available: 5
     gang.scheduling.koordinator.sh/total-number: 5
     gang.scheduling.koordinator.sh/mode: Strict
     gang.scheduling.koordinator.sh/groups: ["namespaceA/gang-a", "namespaceB/gang-b"]
metadata:
   annotations:
     gang.scheduling.koordinator.sh/name: gang-b
     gang.scheduling.koordinator.sh/waiting-time: 3600s 
     gang.scheduling.koordinator.sh/min-available: 5
     gang.scheduling.koordinator.sh/total-number: 5
     gang.scheduling.koordinator.sh/mode: Strict
     gang.scheduling.koordinator.sh/groups: ["namespaceA/gang-a", "namespaceB/gang-b"]
```

Assuming a job has two roles: A and B, each role has several pods. podA belongs to roleA, podB belongs to roleB.
roleA and roleB belongs to different GangGroup, the example as follows:
```yaml
metadata:
  annotations:
     gang.scheduling.koordinator.sh/name: gang-a
     gang.scheduling.koordinator.sh/waiting-time: 3600s 
     gang.scheduling.koordinator.sh/min-available: 5
     gang.scheduling.koordinator.sh/total-number: 5
     gang.scheduling.koordinator.sh/mode: Strict
     gang.scheduling.koordinator.sh/groups: ""
metadata:
   annotations:
     gang.scheduling.koordinator.sh/name: gang-b
     gang.scheduling.koordinator.sh/waiting-time: 3600s 
     gang.scheduling.koordinator.sh/min-available: 5
     gang.scheduling.koordinator.sh/total-number: 5
     gang.scheduling.koordinator.sh/mode: Strict
     gang.scheduling.koordinator.sh/groups: ""
```

### Implementation Details
#### QueueSortPlugin

We design an independent plugin to implement the `QueueSort` extension point separately, so that we can integrate 
queue sort logic of all plugins, and register them at one time.

In this proposal, we implement the Less function to gather pods belong to same Gang. The specific queuing rule is:

1. Firstly, compare the priorities of the two pods, the higher priority is at the front of the queue.

2. Secondly, compare creationTimestamp of two pods, if pod belongs to a Gang, then we compare creationTimestamp of the Gang, 
the one created first will be at the front of the queue.

3. Finally, compare pod's namespace, if pod belongs to a Gang, then we compare Gang name. 

```go
type QueueSortPlugin interface{
    QueueSort(*QueuedPodInfo, *QueuedPodInfo) bool
}
```

#### GangSchedulingPlugin
##### Data-Structure
###### Gang
```go
type Gang struct {
    Name                         string                
    WaitTime                     time.Duration                       
    Mode                         string                 //Strict or NonStrict
    GangGroup                    []string               
    MinRequiredNumber            int                    
    TotalChildrenNum             int
    Children                     map[string]*PodInfo  
    BoundChildren                map[string]*PodInfo
    WaitingForBindChildren       map[string]*PodInfo
    ResourceSatisfied            bool 
    ScheduleCycle                int
    ScheduleCycleValid           bool
    ChildrenScheduleRoundMap     map[string]int
}
```

We design the Gang to record Gang status in scheduler memory. We can get the children pods from "Children" field, and the 
`BoundChildren, WaitingForBindChildren` store the pods binding status, which is used to check if the pods can pass permit stage.

Once Permit stage passed, we will set `ResourceSatisfied=true`, as mentioned above in `After Gang` chapter, this variable is
used for judging whether gang has been satisfied. when handle failover case, if any pod in Gang has been bound, we set `ResourceSatisfied=true`.

We especially explain `scheduleCycle` and `childrenScheduleRoundMap` field. These fields control Gang's scheduling cycle. For example,
at the beginning, `scheduleCycle` is 1, and each pod's cycle in `childrenScheduleRoundMap` is 0. When each pod comes to PreFilter, 
we will check if the pod's value in `childrenScheduleRoundMap` is smaller than Gang's `scheduleCycle`, If result is positive, 
we set the pod's cycle in `childrenScheduleRoundMap` equal with `scheduleCycle` and pass the check. If result is negative, means
the pod has been scheduled in this cycle, so we should reject it. With `totalChildrenNum`'s help, when the last pod comes to make all 
`childrenScheduleRoundMap`'s values equal to `scheduleCycle`, Gang's `scheduleCycle` will be added by 1, which means a new schedule cycle.

We continue to explain `scheduleCycleValid` field, during the scheduling,  When a pod failed at Filter stage, we will set ScheduleCycleValid to 
false in PostFilter stage, which means any pod in this Gang shouldn't be scheduled until it is set to "true",
and the remaining pods should be rejected in PreFilter stage. Only When `scheduleCycle` added by 1, we will reset the `scheduleCycleValid` to true.

It should be emphasized that `scheduleCycle\scheduleCycleValid\childrenScheduleRoundMap` only work in `Strict`. 

##### GangPlugin

this is the framework of the Plugin,we cache the Gang info above in the gangCache.
```go
type GangPlugin struct {
    frameworkHandler            framework.Handle
    gangClient                  gangClient.Interface
    podLister                   listerv1.PodLister
    snapshotSharedLister        framework.SharedLister
    gangCache                   map[string]*Gang
}
```
during the whole kubernetes shceduling process,we only need to realize our logic into four extention points as below:
```go
var(
	_ framework.PreFilterPlugin = &GangScheduling{}
	_ framework.PostFilterPlugin = &GangScheduling{}
	_ framework.PermitPlugin = &GangScheduling{}
	_ framework.ReservePlugin = &Coscheduling{}
)
type GangScheduling interface{
    ActiveGang(pod *corev1.Pod, state *framework.CycleState)
    PreFilter(context.Context, *corev1.Pod) error
    PostFilter(ctx context.Context, state *CycleState, pod *v1.Pod, filteredNodeStatusMap NodeToStatusMap) (*PostFilterResult, *Status)
    Permit(context.Context, *corev1.Pod) Status
    Unreserve(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string)
}
```
###### **PreFilter**

if `NonStrict`, we only do step1 and step2:

- Check whether childes in Gang has met the requirements of minimum number under each Gang, and reject the pod if negative.

- Check whether the Gang has been timeout(check the pod's annotation,later introduced at Permit section), and reject the pod if positive.

- Check whether the Gang has met the `scheduleCycleValid` check, and reject the pod if negative.

- Try update `scheduleCycle`, `scheduleCycleValid`, `childrenScheduleRoundMap` as mentioned above.


###### **PostFilter**

At this point means the pod didn't pass the Filter Plugin, we should:

- If `Strict`, we will set `scheduleCycleValid` to false and release all assumed pods.

- If `NonStrict`, we will do nothing.

###### **Permit**

Any pod passes Filter stage will come to this stage. Scheduler will calculate all Gangs in GangGroup whether the current 
number of assumed-pods in each Gang meets the Gang's minimum requirement.

- If Gang don't meet the bind-condition, we will give the pod a "Wait" Status with a timeout duration, and the bind 
goroutine will keep waiting until the wait is timeout or passed. Then we will run the `ActiveGang` method, it can put all 
the pods belong to the Gang which in `schedulableQueue` or `backoffQueue` back to `activeQueue`, so that the pod of Gang 
can be continuously scheduled as much as possible. 

It should be noted that, in community, scheduler limit maximum timeout value under 15 min, we may need to hook RunPermitPlugins 
to enlarge the timeout when 15 minutes is not enough. Now we record as a known-issue.

- If Gang meet the bind-condition, we will give every waiting pod a "Success" status, which will let the bind goroutine of
each pod leave the waiting status and continue to run. Also, as mentioned above, we will set Gang's `ResourceSatisfied` to true.

###### **Un-reserve**

Both permit stage is timeout and binding failed will lead the pod to un-reserve stage, we can distinguish from Gang's "ResourceSatisfied" field,
if the field is true means binding failed, else means the Gang is timeout.

- When permit stage is timeout, we will give an annotation like `gang.scheduling.koordinator.sh/timeout=true` to all the pods 
belong to the Gang and will release the resource of all the assumed pods. The Gang will not be scheduled anymore, 
user should manually handle the timeout event.

- When binding failed, as mentioned above, the collection of Gang's resource is over, we will do nothing except roll back
the failed pod resource.

###### **Init**

We will register pod's event handler to watch pod event for updating Gang.

## Unsolved Problems

## Alternatives
User can choose use Gang by `Strict` and `NonStrict` case by case.

## Implementation History

## References