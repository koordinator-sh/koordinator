---
title: node resource reservation
authors:
  - "@lucming"
reviewers:
  - "@eahydra"
  - "@hormes"
  - "@yihuifeng"
  - "@honpey"
  - "@zwzhang0107"
  - "@jasonliu747"
creation-date: 2022-12-27
last-updated: 2023-04-13
---

# Resource Reservation

## Table of Contents

<!-- TOC -->

- [Resource Reservation](#resource-reservation)
    - [Table of Contents](#table-of-contents)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [User Stories](#user-stories)
            - [Story 1](#story-1)
            - [Story 2](#story-2)
            - [Story 3](#story-3)
        - [Implementation Details](#implementation-details)
            - [API](#api)
            - [koordlet](#koordlet)
            - [koord-manager](#koord-manager)
            - [koord-scheduler](#koord-scheduler)
            - [Descheduler](#descheduler)
            - [Eviction](#eviction)
            - [Example](#example)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

## Summary
The proposal provides a scheduling mechanism to reserve resources by node.annotation, such as reserving 0-6 cores of CPU on the node. 
and then these resources are not available for K8S, this is the same as `reserved-cpus` option in kubelet, another extension to the kubelet resource reservation.

<font color=Chocolate>*please attention:*</font> 
The reservation here is different from the scenario in which `reservation` scheduler plugin works, 
as the resources reserved here are outside of K8S and they are used by normal processes (not pods).  
The `reservation` plugin actually reserved resources in the K8S cluster to ensure that some pods could be scheduled first.

## Motivation


### Goals

- support for reserving a few specific cores on the node.
- support for reserving a number of cpus on the node.
- support for reserving a number of memory on the node.
- some other custom resources, such as batch-memory, bath-cpu

### Non-Goals/Future Work

## Proposal

### User Stories

#### Story 1
As a K8S cluster administrator, there are not only containers running on the nodes of the K8S cluster, 
but there may be other processes that are not controlled by K8S, just normal processes, 
in order to ensure the isolation of the containers and the original processes. 
At the CPU level, we usually want normal processes and containers to run on different CPU cores to reduce/avoid the impact of their resource usage.   
eg：   
total CPU on node: 24;   
expected performance maybe like this:   
normal process outside K8S use: 0-11;    
container use: 12-23   
so for K8S, the 0-11 core is reserved and it can not be used.

#### Story 2
As a K8S cluster administrator, set the total amount of resources to be reserved directly via `node.annotation`, just like: `1C2Gi`.

#### Story 3

The cluster administrator just wants to reserve some cpu cores on the nods, and also wants to use reservation mechanism of kubelet to trim the allocatable resources.  For example, suppose the CPU capacity of node A is 32000m, the administrator reserve `0-3` CPU cores to the processes outside the Kubernetes, and kubelet reserve 8000m CPU to keep the `Node.Status.Allocatable` is 24000m.

### Implementation Details

#### API

By adding annotation to the node, we specify the resources to be reserved by the koordinator, such as memory, CPU, etc. 
For CPU, we can specify the total amount of CPU to be reserved, or we can specify which cores to reserve explicitly. 

If resources are reserved and ApplyPolicy is empty or Default, it will affect `Node.Status.Allocatable`. If `ReservedCPUsOnly` is used, only CPU is reserved, but it will not affect `Node.Status.Allocatable`.

```golang
// resources that koordinator reserved, and you can reserve other resource if needed.
type NodeReservation struct {
  // resources need to be reserved. like, {"cpu":"1C", "memory":"2Gi"}
  Resources corev1.ResourceList `json:"resources,omitempty"`
  // reserved cpus need to be reserved, such as 1-6, or 2,4,6,8
  ReservedCPUs string `json:"reservedCPUs,omitempty"`
  // ApplyPolicy indicates how the reserved resources take effect.
  ApplyPolicy NodeReservationApplyPolicy `json:"applyPolicy,omitempty"`
}

type NodeReservationApplyPolicy string

const (
  // NodeReservationApplyPolicyDefault will affect the total amount of schedulable resources of the node and reserve CPU Cores.
  // For example, NodeInfo.Allocatable will be modified in the scheduler to deduct the amount of reserved resources
  NodeReservationApplyPolicyDefault NodeReservationApplyPolicy = "Default"
  // NodeReservationApplyPolicyReservedCPUsOnly means that only CPU Cores are reserved, but it will
  // not affect the total amount of schedulable resources of the node.
  // The total amount of schedulable resources is taken into effect by the kubelet's reservation mechanism.
  // But koordinator need to exclude reserved CPUs when allocating CPU Cores
  NodeReservationApplyPolicyReservedCPUsOnly NodeReservationApplyPolicy = "ReservedCPUsOnly"
)
```

#### koordlet   
1. When koordlet reports `NodeResourceTopology`, it updates the explicitly reserved cores in node.annntation to 
  `NodeResourceTopology.annotation["node.koordinator.sh/reservation"]`, so that the `runtimehook` can ignore these cores 
   when fetching the shared-pool from nodetopo.
   Here, the following three scenarios for reserving CPU need to be noted.
   1. it only reserve some CPUs by quantity, for example 5c, and allocates a specific CPU for 5c according to cpumanagerpolicy=static, if it gets 0, 1, 2, 4, 5, then these cpus are not available for other pods
      ```yaml
      apiVersion: v1
      kind: Node
      metadata:
        name: fake-node
        annotations: # specific 5 cores will be calculated, e.g. 0, 1, 2, 3, 4, and then those core will be reserved.
          node.koordinator.sh/reservation: '{"resources":{"cpu":"5"}}'
      ```
   2. just reserve a few explicit cpus, for example 0-3, then the other pods will not use the four cpus 0, 1, 2, 3.
      ```yaml
      apiVersion: v1
      kind: Node
      metadata:
        name: fake-node
        annotations: # the cores 0, 1, 2, 3 will be reserved.
          node.koordinator.sh/reservation: '{"reservedCPUs":"0-3"}'
      ```
   3. both of the previous ways are declared, handled as reserve CPU by `ReservedCPUs`
      ```yaml
      apiVersion: v1
      kind: Node
      metadata:
        name: fake-node
        annotations: # This case will be handled according to the configuration of the reservedCPUs field, the cores 0, 1, 2 and 3 will be reserved and the resources field will be ignored.
          node.koordinator.sh/reservation: '{"resources":{"cpu":"2"},"reservedCPUs":"0-3"}'
      ```
    
    So either by quantity or by specifying the CPU directly, the exact core that is reserved will be calculated, and then written to nodetopo.annotation["node.koordinator.sh/reservation"]

2. CPU supress:  
   we should ignore the CPUs reserved by node when supress. Here we calculate the resources that BE pod can use, based on the following formula, and then modify the cgroup.
   ```
   suppress(BE) := node.Total * SLOPercent - pod(LS).Used - max(system.Used, node.anno.reserved)
   ``` 
   - by quota  
      parse node.annotation["node.koordinator.sh/reservation"] to get the reserved `resourcelist`, then subtract this resource from the original formula  
   - by cpuset  
      read the specific CPU to be reserved on the nodetopo directly, not counting the cpus reserved from the annotation when suppressing  
      

3. runtimehooks:  
it will ignored the cpus reserved by `nodetopo.annotations` when create LS pod. 
   ```
   cpus(allocatable) = All - pod(LSE).Used - pod(LSR).Used - cpus(nodeAnnoReserved)
   ```  
  
<font color=Chocolate>*description:*</font> 
LS pod use cpus from the shared pool, so here we can ensure that LS pods do not use CPU that is already reserved on node.annotation.


#### koord-manager  

we should update batch-resource on node here, just like `batch-memory`,`batch-cpu`. 
the implementation of this part depends on the `nodeResource`  controller in koord-manager, 
so for batch-resource, the `batchResourceFit` plugin can take into account the resources reserved by the node.
The detailed algorithm is as follows:
```
reserveRatio = (100-thresholdPercent) / 100.0
node.reserved = node.alloc * reserveRatio
system.used = max(node.used - pod.used, node.anno.reserved)
Node(BE).Alloc = Node.Alloc - Node.Reserved - System.Used - Pod(LS).Used
```

#### koord-scheduler

1. Register the corresponding function by `RegisterNodeInfoTransformer`, subtract the resources reserved from `node.annotation` in `Nodeinfo.Allocatable` if the reservation apply policy is empty or `Default`.
This part of the logic executed before the Filter of the scheduling plugin. So the resources reserved from node are also taken into account in other scheduler plugins.

2. When allocating cpus to the LSE/LSR pod in the reserve phase of the `NodeNUMAResource` plugin, the reserved cores in `nodetopo.annotation` need to be removed.
  ```
  cpus(alloc) = cpus(total) - cpus(allocated) - cpus(kubeletReserved) - cpus(nodeAnnoReserved)
  ```
3. `ElasticQuota` plugin shuould also remove those reserved resource if the apply policy is empty or `Default` when calculate the total amount that can be allocated by Quota  

  <font color=Chocolate>*description*</font>: 
  In summary, the resources reserved in node.annotation are taken into account during the `Filter` phase of scheduling; 
  When allocating cores to pod in the `Reserve` phase, do not allocate the cores already reserved in node.annotation.  
...


#### Descheduler

- cpu: evict (LSE/LSR)pods that are using reserved cpus in node.annotation.  
for example, 0-1 core is already used by LSE/LSR pods, but at at the same time we have reserved 0-3 core CPU through `node.annotation`, as declared below.   
`node.annotation["node.koordinator.sh/reservation"]={"reservedCPUs":"0-3"}`  
We should evict those pods that occupy 0-3 cores  
- ...

Other eviction policies also need to take into account the resources reserved in node.annotation,   
e.g. `Node.Status.Allocatable` in the `LowNodeLoad` descheduler plugin should be minus the reserved resources if the policy is empty or `Default`.

#### Eviction

We can do something in the following stages：
- On the agent side, 
  it is easier to get the resource usage of individual containers,  we can evict pods with lower priority or higher CPU usage. 
  just ignore the higher-level controller of a pod and evict only by pod.
- On the controller side. 
  For example, we specific some cores to be reserved via node.annotation, if we find that a pod is using these cores 
  at this time, we should evict a group of pods according to the job/crd dimension. The simplest algorithm looks like this, 
  sorted by priority / number of pods in a group / total resource usage in a group of pods.

#### Example
There is a demo to reserve CPU by quantity, like this:
```yaml
apiVersion: v1
kind: Node
metadata:
  name: test-node1
  annotations:
    node.koordinator.sh/reservation: '{"resources":{"cpu":"2"}}'
```
and then we can see that `NodeResourceTopology.annotations["node.koordinator.sh/reservation"]` is set to "0,6", 
it means that CPU id in [0,6] haven been reserved, and no one can use those CPU cores, details as follows:
```yaml
apiVersion: topology.node.k8s.io/v1alpha1
kind: NodeResourceTopology
metadata:
  annotations:
    node.koordinator.sh/reservation: '{"reservedCPUs":"0,6"}'
    kubelet.koordinator.sh/cpu-manager-policy: '{"policy":"none"}'
    node.koordinator.sh/cpu-shared-pools: >-
      [{"socket":0,"node":0,"cpuset":"1-5,12-17"},{"socket":1,"node":1,"cpuset":"7-11,18-23"}]
    node.koordinator.sh/cpu-topology: >-
      {"detail":[{"id":0,"core":0,"socket":0,"node":0},...]}
  labels:
    app.kubernetes.io/managed-by: Koordinator
  name: test-node1
  ownerReferences:
    - apiVersion: v1
      blockOwnerDeletion: true
      controller: true
      kind: Node
      name: test-node1
topologyPolicies:
  - None
zones:
  - name: fake-name
    type: fake-type

```

## Implementation History

- [ ] 12/27/2022: Open PR for initial draft.
- [ ] 13/04/2023: Add ApplyPolicy.

