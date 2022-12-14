---
title: Fine-grained CPU orchestration
authors:
- "@eahydra"
reviewers:
- "@hormes"
- "@allwmh"
- "@honpey"
- "@jasonliu747"
- "@saintube"
- "@stormgbs"
- "@zwzhang0107"
creation-date: 2022-05-30
last-updated: 2022-12-13
status: provisional

---

<!-- TOC -->

- [Fine-grained CPU orchestration](#fine-grained-cpu-orchestration)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [Design Overview](#design-overview)
        - [User stories](#user-stories)
            - [Story 1](#story-1)
            - [Story 2](#story-2)
            - [Story 3](#story-3)
            - [Story 4](#story-4)
            - [Story 5](#story-5)
            - [Story 6](#story-6)
    - [Design Details](#design-details)
        - [Basic CPU orchestration principles](#basic-cpu-orchestration-principles)
        - [Koordinator QoS CPU orchestration principles](#koordinator-qos-cpu-orchestration-principles)
        - [Compatible kubelet CPU management policies](#compatible-kubelet-cpu-management-policies)
        - [Take over kubelet CPU management policies](#take-over-kubelet-cpu-management-policies)
        - [CPU orchestration API](#cpu-orchestration-api)
            - [Application CPU CPU orchestration API](#application-cpu-cpu-orchestration-api)
                - [Resource spec](#resource-spec)
                - [Resource status](#resource-status)
                - [Example](#example)
            - [Node CPU orchestration API](#node-cpu-orchestration-api)
                - [CPU bind policy](#cpu-bind-policy)
                - [NUMA allocate strategy](#numa-allocate-strategy)
                - [NUMA topology alignment policy](#numa-topology-alignment-policy)
                - [Example](#example)
            - [NodeResourceTopology CRD](#noderesourcetopology-crd)
                - [CRD Scheme definition](#crd-scheme-definition)
                - [Compatible](#compatible)
                - [Extension](#extension)
                - [Create/Update NodeResourceTopology](#createupdate-noderesourcetopology)
                - [Example](#example)
            - [RebindResource CRD](#rebindresource-crd)
        - [Fine-grained CPU orchestration plugin](#fine-grained-cpu-orchestration-plugin)
            - [Filter phase](#filter-phase)
            - [Score phase](#score-phase)
            - [Reserve phase](#reserve-phase)
            - [PreBind phase](#prebind-phase)
            - [CPU Allocation Algorithm](#cpu-allocation-algorithm)
            - [Plugin Configuration](#plugin-configuration)
    - [Alternatives](#alternatives)
        - [Defined Koordinator NodeResourceTopology CRD](#defined-koordinator-noderesourcetopology-crd)
    - [Unsolved Problems](#unsolved-problems)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

# Fine-grained CPU orchestration

## Summary

This proposal defines the fine-grained CPU orchestration for Koordinator QoS in detail, and how to be compatible with the existing design principles and implementations of K8s. This proposal describes the functionality that koordlet, koord-runtime-proxy and koord-scheduler need to enhance.

## Motivation

An increasing number of systems leverage a combination of CPUs and hardware accelerators to support latency-critical execution and high-throughput parallel computation. These include workloads in fields such as telecommunications, scientific computing, machine learning, financial services and data analytics. Such hybrid systems comprise a high performance environment.

In order to extract the best performance, optimizations related to CPU isolation, NUMA-locality are required.

### Goals

1. Improve the CPU orchestration semantics of Koordinator QoS.
1. Determine compatible kubelet policies.
1. Clarify how koordlet should enhance CPU scheduling mechanism.
1. Provide a set of API such as CPU bind policies, CPU exclusive policies, NUMA topology alignment policies, NUMA topology information, etc. for applications and cluster administrator to support complex CPU orchestration scenarios.
1. Provide the CPU orchestration optimization API.

### Non-Goals/Future Work

1. Describe specific design details of koordlet/koord-runtime-proxy.
1. Describe specific design details of CPU descheduling mechanism.

## Proposal

### Design Overview

![image](/docs/images/cpu-orchestration-seq-uml.svg)

When koordlet starts, koordlet gather the NUMA topology information from kubelet include NUMA Topology, CPU Topology, kubelet cpu management policy, kubelet allocated CPUs for Guaranteed Pods etc., and update to the NodeResourceTopology CRD. The latency-sensitive applications are scaling, the new Pod can set Koordinator QoS with LSE/LSR, CPU Bind policy and CPU exclusive policy to require koord-scheduler to allocate best-fit CPUs to get the best performance. When koord-scheduler scheduling the Pod, koord-scheduler will filter Nodes that satisfied NUMA Topology Alignment policy, and select the best Node by scoring, allocating the CPUs in Reserve phase, and records the result to Pod annotation when PreBinding. koordlet hooks the kubelet CRI request to replace the CPUs configuration parameters with the koord-scheduler scheduled result to the runtime such as configure the cgroup.

### User stories

#### Story 1

Compatible with kubelet's existing CPU management policies. The CPU manager policy `static` allows pods with certain resource characteristics to be granted increased CPU affinity and exclusivity in the node. If enabled the `static` policy, the cluster administrator must configure the kubelet reserve some CPUs. There are some options for `static` policy. If the `full-pcpus-only(beta, visible by default)` policy option is specified, the `static` policy will always allocate full physical cores. If the `distribute-cpus-across-numa(alpha, hidden by default)` option is specified, the `static` policy will evenly distribute CPUs across NUMA nodes in cases where more than one NUMA node is required to satisfy the allocation.

#### Story 2

Similarly, the semantics of the existing K8s Guaranteed Pods in the community should be compatible. The cpu cores allocated to K8s Guaranteed Pods with `static` policy will not share to the default best effort Pods, so it is equivalent to LSE. But when the load in the node is relatively low, the CPUs allocated by LSR Pods should be shared with best effort workloads to obtain economic benefits. 

#### Story 3

The Topology Manager is a kubelet component that aims to coordinate the set of components that are responsible for these optimizations. After Topology Manager was introduced the problem of launching pod in the cluster where worker nodes have different NUMA topology and different amount of resources in that topology became actual. The Pod could be scheduled in the node where the total amount of resources is enough, but resource distribution could not satisfy the appropriate Topology policy. 

#### Story 4

The scheduler can coordinate the arrangement between latency-sensitive applications. For example, the same latency-sensitive applications can be mutually exclusive in the CPU dimension, and latency-sensitive applications and general applications can be deployed in the CPU dimension affinity. Costs can be reduced and runtime quality can be guaranteed.

#### Story 5

When allocating CPUs based on NUMA topology, users want to have different allocation strategies. For example, bin-packing takes precedence, or assigns the most idle NUMA Node.

#### Story 6

As the application scaling or rolling deployment, the best-fit allocatable space will gradually become fragmented, which will lead to the bad allocation effect of some strategies and affect the runtime effect of the application. 

## Design Details

### Basic CPU orchestration principles

1. Only supports the CPU allocation mechanism of the Pod dimension.
1. Koordinator divides the CPU on the machine into `CPU Shared Pool`, `statically exclusive CPUs` and `BE CPU Shared Pool`. 
    1. The `CPU Shared Pool` is the set of CPUs on which any containers in `K8s Burstable` and `Koordinator LS` Pods run. Containers in `K8s Guaranteed` pods with `fractional CPU requests` also run on CPUs in the shared pool. The shared pool contains all unallocated CPUs in the node but excluding CPUs allocated by K8s Guaranteed, LSE and LSR Pods. If kubelet reserved CPUs, the shared pool includes the reserved CPUs. 
    1. The `statically exclusive CPUs` are the set of CPUs on which any containers in `K8s Guaranteed`, `Koordinator LSE` and `LSR` Pods that have integer CPU run. When K8s Guaranteed, LSE and LSR Pods request CPU, koord-scheduler will be allocated from the `CPU Shared Pool`.
    1. The `BE CPU Shared pool` is the set of CPUs on which any containers in `K8s BestEffort` and `Koordinator BE` Pods run. The `BE CPU Shared Pool` contains all CPUs in the node but excluding CPUs allocated by `K8s Guaranteed` and `Koordinator LSE` Pods.

### Koordinator QoS CPU orchestration principles

1. The Request and Limit of LSE/LSR Pods **MUST** be equal and the CPU value **MUST** be an integer multiple of 1000.
1. The CPUs allocated by the LSE Pod are completely **exclusive** and **MUST NOT** be shared. If the node is hyper-threading architecture, only the logical core dimension is guaranteed to be isolated, but better isolation can be obtained through the `CPUBindPolicyFullPCPUs` policy.
1. The CPUs allocated by the LSR Pod only can be shared with BE Pods.
1. LS Pods bind the CPU shared pool, **excluding** CPUs allocated by LSE/LSR Pods.
1. BE Pods bind all CPUs in the node, **excluding** CPUs allocated by LSE Pods.
1. The K8s Guaranteed Pods already running is equivalent to Koordinator LSR if kubelet enables the CPU manager `static` policy.
1. The K8s Guaranteed Pods already running is equivalent to Koordinator LS if kubelet enables the CPU manager `none` policy.
1. Newly created K8s Guaranteed Pod without Koordinator QoS specified will be treated as LS.

![image](/docs/images/qos-cpu-orchestration.png)

### Compatible kubelet CPU management policies

1. If kubelet set the CPU manager policy options `full-pcpus-only=true` / `distribute-cpus-across-numa=true`, and there is no new CPU bind policy defined by Koordinator in the node, follow the semantics of these parameters defined by the kubelet.
1. If kubelet set the Topology manager policy, and there is no new NUMA Topology Alignment policy defined by Koordinator in the node, follow the semantics of these parameters defined by the kubelet. 

### Take over kubelet CPU management policies

Because the CPU reserved by kubelet mainly serves K8s BestEffort and Burstable Pods. But Koordinator will not follow the policy. The K8s Burstable Pods should use the CPU Shared Pool, and the K8s BestEffort Pods should use the `BE CPU Shared Pool`.

1. For K8s Burstable and Koordinator LS Pods:
    1. When the koordlet starts, calculates the `CPU Shared Pool` and applies the shared pool to all Burstable and LS Pods in the node, that is, updating their cgroups to set cpuset. The same logic is executed when LSE/LSR Pods are creating or destroying. 
    1. koordlet ignore the CPUs reserved by kubelet, and replace them with CPU Shared Pool defined by Koordinator. 
1. For K8s BestEffort and Koordinator BE Pods:
    1. If kubelet reserved CPUs, the best effort Pods use the reserved CPUs first.
    1. koordlet can use all CPUs in the node but exclude the CPUs allocated by K8s Guaranteed and Koordinator LSE Pods that have integer CPU. It means that if koordlet enables the CPU Suppress feature should follow the constraint to guarantee not affecting LSE Pods. Similarly, if kubelet enables the CPU manager policy with `static`, the K8s Guaranteed Pods should also be excluded. 
1. For K8s Guaranteed Pods:
    1. If there is `scheduling.koordinator.sh/resource-status` updated by koord-scheduler in the Pod annotation, then replace the CPUSet in the kubelet CRI request, including Sandbox/Container creating stage.
    1. kubelet sometimes call `Update` method defined in CRI to update container cgroup to set new CPUs, so koordlet and koord-runtime-proxy need to hook the method.
1. Automatically resize CPU Shared Pool
    1. koordlet automatically resize `CPU Shared Pool` based on the changes such as Pod creating/destroying. If `CPU Shared Pool` changed, koordlet should update cgroups of all LS/K8s Burstable Pods with the CPUs of shared pool. 
    1. If the corresponding `CPU Shared Pool` is specified in the annotation `scheduling.koordinator.sh/resource-status` of the Pod, koordlet need to bind only the CPUs of the corresponding pool when configuring the cgroup.

The takeover logic will require koord-runtime-proxy to add new extension points, and require koordlet to implement a new runtime hook plugin. When koord-runtime-proxy is not installed, these takeover logic will also be able to be implemented.

### CPU orchestration API

#### Application CPU CPU orchestration API

##### Resource spec

The annotation `scheduling.koordinator.sh/resource-spec` is a resource allocation API defined by Koordinator. The user specifies the desired CPU orchestration policy by setting the annotation. In the future, we can also extend and add resource types that need to be supported as needed. The scheme corresponding to the annotation value is defined as follows:

```go
// ResourceSpec describes extra attributes of the compute resource requirements.
type ResourceSpec struct {
  PreferredCPUBindPolicy       CPUBindPolicy      `json:"preferredCPUBindPolicy,omitempty"`
  PreferredCPUExclusivePolicy  CPUExclusivePolicy `json:"preferredCPUExclusivePolicy,omitempty"`
}

type CPUBindPolicy string

const (
  // CPUBindPolicyDefault performs the default bind policy that specified in koord-scheduler configuration
  CPUBindPolicyDefault CPUBindPolicy = "Default"
  // CPUBindPolicyFullPCPUs favor cpuset allocation that pack in few physical cores
  CPUBindPolicyFullPCPUs CPUBindPolicy = "FullPCPUs"
  // CPUBindPolicySpreadByPCPUs favor cpuset allocation that evenly allocate logical cpus across physical cores
  CPUBindPolicySpreadByPCPUs CPUBindPolicy = "SpreadByPCPUs"
  // CPUBindPolicyConstrainedBurst constrains the CPU Shared Pool range of the Burstable Pod
  CPUBindPolicyConstrainedBurst CPUBindPolicy = "ConstrainedBurst"
)

type CPUExclusivePolicy string

const (
  // CPUExclusivePolicyDefault performs the default exclusive policy that specified in koord-scheduler configuration
  CPUExclusivePolicyDefault CPUExclusivePolicy = "Default"
  // CPUExclusivePolicyPCPULevel represents mutual exclusion in the physical core dimension 
  CPUExclusivePolicyPCPULevel CPUExclusivePolicy = "PCPULevel"
  // CPUExclusivePolicyNUMANodeLevel indicates mutual exclusion in the NUMA topology dimension
  CPUExclusivePolicyNUMANodeLevel CPUExclusivePolicy = "NUMANodeLevel"
)
```

- The `CPUBindPolicy` defines the CPU binding policy. The specific values are defined as follows:
   - `CPUBindPolicyDefault` or empty value performs the default bind policy that specified in koord-scheduler configuration.
   - `CPUBindPolicyFullPCPUs` is a bin-packing policy, similar to the `full-pcpus-only=true` option defined by the kubelet, that allocate full physical cores. However, if the number of remaining logical CPUs in the node is sufficient but the number of full physical cores is insufficient, the allocation will continue. This policy can effectively avoid the noisy neighbor problem.
   - `CPUBindPolicySpreadByPCPUs` is a spread policy. If the node enabled Hyper-Threading, when this policy is adopted, the scheduler will evenly allocate logical CPUs across physical cores. For example, the current node has 8 physical cores and 16 logical CPUs. When a Pod requires 8 logical CPUs and the `CPUBindPolicySpreadByPCPUs` policy is adopted, the scheduler will allocate an logical CPU from each physical core. This policy is mainly used by some latency-sensitive applications with multiple different peak-to-valley characteristics. It can not only allow the application to fully use the CPU at certain times, but will not be disturbed by the application on the same physical core. So the noisy neighbor problem may arise when using this policy.
   - `CPUBindPolicyConstrainedBurst` a special policy that mainly helps K8s Burstable/Koordinator LS Pod get better performance. When using the policy, koord-scheduler is filtering out Nodes that have NUMA Nodes with suitable CPU Shared Pool by Pod Limit. After the scheduling is successful, the scheduler will update `scheduling.koordinator.sh/resource-status` in the Pod, declaring the `CPU Shared Pool` to be bound. The koordlet binds the CPU Shared Pool of the corresponding NUMA Node according to the `CPU Shared Pool`
   - If `kubelet.koordinator.sh/cpu-manager-policy` in `NodeResourceTopology` has option `full-pcpus-only=true`, or `node.koordinator.sh/cpu-bind-policy` in the Node with the value `PCPUOnly`, the koord-scheduler will check whether the number of CPU requests of the Pod meets the `SMT-alignment` requirements, so as to avoid being rejected by the kubelet after scheduling. koord-scheduler will avoid such nodes if the Pod uses the `CPUBindPolicySpreadByPCPUs` policy or the number of logical CPUs mapped to the number of physical cores is not an integer.
- The `CPUExclusivePolicy` defines the CPU exclusive policy, it can help users to avoid noisy neighbor problems. The specific values are defined as follows:
   - `CPUExclusivePolicyDefault` or empty value performs the default exclusive policy that specified in koord-scheduler configuration.
   - `CPUExclusivePolicyPCPULevel`. When allocating logical CPUs, try to avoid physical cores that have already been applied for by the same exclusive policy. It is a supplement to the `CPUBindPolicySpreadByPCPUs` policy. 
   - `CPUExclusivePolicyNUMANodeLevel`. When allocating logical CPUs, try to avoid NUMA Nodes that has already been applied for by the same exclusive policy. If there is no NUMA Node that satisfies the policy, downgrade to `PCPU` policy.

For the ARM architecture, `CPUBindPolicy` only support `CPUBindPolicyFullPCPUs`, and `CPUExclusivePolicy` only support `CPUExclusivePolicyNUMANodeLevel`.

##### Resource status

The annotation `scheduling.koordinator.sh/resource-status` represents resource allocation result. koord-scheduler patch Pod with the annotation before binding to node. koordlet uses the result to configure cgroup.

The scheme corresponding to the annotation value is defined as follows:

```go
type ResourceStatus struct {
  CPUSet         string          `json:"cpuset,omitempty"`
  CPUSharedPools []CPUSharedPool `json:"cpuSharedPools,omitempty"`
}
```

- `CPUSet` represents the allocated CPUs. When LSE/LSR Pod requested, koord-scheduler will update the field. It is Linux CPU list formatted string. For more details, please refer to [doc](http://man7.org/linux/man-pages/man7/cpuset.7.html#FORMATS).
- `CPUSharedPools` represents the desired CPU Shared Pools used by LS Pods. If the Node has the label `node.koordinator.sh/numa-topology-alignment-policy` with `Restricted/SingleNUMANode`, koord-scheduler will find the best-fit NUMA Node for the LS Pod, and update the field that requires koordlet uses the specified CPU Shared Pool. It should be noted that the scheduler does not update the `CPUSet` field in the `CPUSharedPool`, koordlet binds the CPU Shared Pool of the corresponding NUMA Node according to the `SocketID` and `NodeID` fields in the `CPUSharedPool`.

##### Example

The following specific example:

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    scheduling.koordinator.sh/resource-spec: |-
      {
        "preferredCPUBindPolicy": "SpreadByPCPUs",
        "preferredCPUExclusivePolicy": "PCPULevel"
      }
    scheduling.koordinator.sh/resource-status: |-
      {
        "cpuset": "0-3"
      }
  name: test-pod
  namespace: default
spec:
  ...
```

#### Node CPU orchestration API

From the perspective of cluster administrators, it is necessary to support some APIs to control the CPU orchestration behavior of nodes.

##### CPU bind policy

The label `node.koordinator.sh/cpu-bind-policy` constrains how to bind CPU logical CPUs when scheduling. 

The following is the specific value definition:
- `None` or empty value does not perform any policy.
- `FullPCPUsOnly` requires that the scheduler must allocate full physical cores. Equivalent to kubelet CPU manager policy option `full-pcpus-only=true`. 
- `SpreadByPCPUs` requires that the schedler must evenly allocate logical CPUs across physical cores. 

If there is no `node.koordinator.sh/cpu-bind-policy` in the node's label, it will be executed according to the policy configured by the Pod or koord-scheduler.

##### NUMA allocate strategy

The label `node.koordinator.sh/numa-allocate-strategy` indicates how to choose satisfied NUMA Nodes when scheduling. The following is the specific value definition:
- `MostAllocated` indicates that allocates from the NUMA Node with the least amount of available resource.
- `LeastAllocated` indicates that allocates from the NUMA Node with the most amount of available resource.
- `DistributeEvenly` indicates that evenly distribute CPUs across NUMA Nodes.

If the cluster administrator does not set label `node.koordinator.sh/numa-allocate-strategy` on Node, but `kubelet.koordinator.sh/cpu-manager-policy` in `NodeResourceTopology` has option `distribute-cpus-across-numa=true`, then follow the semantic allocation of `distribute-cpus-across-numa`. 

If there is no `node.koordinator.sh/numa-allocate-strategy` in the node's label and no `kubelet.koordinator.sh/cpu-manager-policy` with `distribute-cpus-across-numa` option in `NodeResourceTopology`, it will be executed according to the policy configured by the koord-scheduler.

If both `node.koordinator.sh/numa-allocate-strategy` and `kubelet.koordinator.sh/cpu-manager-policy` are defined, `node.koordinator.sh/numa-allocate-strategy` is used first.

##### NUMA topology alignment policy

The label `node.koordinator.sh/numa-topology-alignment-policy` represents that how to aligning resource allocation according to the NUMA topology. The policy semantic follow the K8s community. Equivalent to the field `TopologyPolicies` in `NodeResourceTopology`, and the topology policies `SingleNUMANodePodLevel` and `SingleNUMANodeContainerLevel` are mapping to `SingleNUMANode` policy. 

- `None` is the default policy and does not perform any topology alignment.
- `BestEffort` indicates that preferred select NUMA Node that is topology alignment, and if not, continue to allocate resources to Pods.
- `Restricted` indicates that each resource requested by a Pod on the NUMA Node that is topology alignment, and if not, koord-scheduler will skip the node when scheduling.
- `SingleNUMANode` indicates that all resources requested by a Pod must be on the same NUMA Node, and if not, koord-scheduler will skip the node when scheduling.

If there is no `node.koordinator.sh/numa-topology-alignment-policy` in the node's label and `TopologyPolicies=None` in `NodeResourceTopology`, it will be executed according to the policy configured by the koord-scheduler.

If both `node.koordinator.sh/numa-topology-alignment-policy` in Node and `TopologyPolicies=None` in `NodeResourceTopology` are defined, `node.koordinator.sh/numa-topology-alignment-policy` is used first.

##### Example

The following specific example:

```yaml
apiVersion: v1
kind: Node
metadata:
  labels:
    node.koordinator.sh/cpu-bind-policy: "FullPCPUsOnly"
    node.koordinator.sh/numa-topology-alignment-policy: "BestEffort"
    node.koordinator.sh/numa-allocate-strategy: "MostAllocated"
  name: node-0
spec:
  ...
```

#### NodeResourceTopology CRD

The node resource information to be reported mainly includes the following categories:

- NUMA Topology, including resources information, CPU information such as logical CPU ID, physical Core ID, NUMA Socket ID and NUMA Node ID and etc. 
- The topology manager scopes and policies configured by kubelet.
- The CPU manager policies and options configured by kubelet.
- Pod bound CPUs allocated by kubelet or koord-scheduler, including K8s Guaranteed Pods, Koordinator LSE/LSR Pods but except the LS/BE.
- CPU Shared Pool defined by koordlet

The above information can guide koord-scheduler to better be compatible with the kubelet's CPU management logic, make more appropriate scheduling decisions and help users quickly troubleshoot.

##### CRD Scheme definition

We use [NodeResourceTopology](https://github.com/k8stopologyawareschedwg/noderesourcetopology-api/blob/master/pkg/apis/topology/v1alpha1/types.go) CRD to describe the NUMA Topology. The community-defined NodeResourceTopology CRD is mainly used for the following considerations:

- NodeResourceTopology already contains basic NUMA topology information and kubelet TopologyManager's Scope and Policies information. We can reuse the existing codes.
- Keep up with the evolution of the community and influence the community to make more changes.

##### Compatible

The koordlet creates or updates NodeResourceTopology periodically. The name of NodeResourceTopology is same as the name of Node. and add the label `app.kubernetes.io/managed-by=Koordinator` describes the node is managed by Koordinator.

##### Extension

At present, the NodeResourceTopology lacks some information, and it is temporarily written in the NodeResourceTopology in the form of annotations or labels:

- The annotation `kubelet.koordinator.sh/cpu-manager-policy` describes the kubelet CPU manager policy and options. The scheme is defined as follows:

```go
const (
  FullPCPUsOnlyOption            string = "full-pcpus-only"
  DistributeCPUsAcrossNUMAOption string = "distribute-cpus-across-numa"
)

type KubeletCPUManagerPolicy struct {
  Policy  string            `json:"policy,omitempty"`
  Options map[string]string `json:"options,omitempty"`
  ReservedCPUs string       `json:"reservedCPUs,omitempty"`
}

```

- The annotation `node.koordinator.sh/cpu-topology` describes the detailed CPU topology. Fine-grained management mechanism needs more detailed CPU topology information. The scheme is defined as follows:

```go
type CPUTopology struct {
  Detail []CPUInfo `json:"detail,omitempty"`
}

type CPUInfo struct {
  ID     int32 `json:"id"`
  Core   int32 `json:"core"`
  Socket int32 `json:"socket"`
  Node   int32 `json:"node"`
}
```

- annotation `node.koordinator.sh/pod-cpu-allocs` describes the CPUs allocated by Koordinator LSE/LSR and K8s Guaranteed Pods. The scheme corresponding to the annotation value is defined as follows:

```go
type PodCPUAlloc struct {
  Namespace        string    `json:"namespace,omitempty"`
  Name             string    `json:"name,omitempty"`
  UID              types.UID `json:"uid,omitempty"`
  CPUSet           string    `json:"cpuset,omitempty"`
  ManagedByKubelet bool      `json:"managedByKubelet,omitempty"`
}

type PodCPUAllocs []PodCPUAlloc
```

- The annotation `node.koordinator.sh/cpu-shared-pools` describes the CPU Shared Pool defined by Koordinator. The shared pool is mainly used by Koordinator LS Pods or K8s Burstable Pods. The scheme is defined as follows:

```go
type NUMACPUSharedPools []CPUSharedPool

type CPUSharedPool struct {
  Socket int32  `json:"socket"`
  Node   int32  `json:"node"`
  CPUSet string `json:"cpuset,omitempty"`
}
```
The `CPUSet` field is Linux CPU list formatted string. For more details, please refer to [doc](http://man7.org/linux/man-pages/man7/cpuset.7.html#FORMATS).


##### Create/Update NodeResourceTopology

- koordlet is responsible for creating/updating NodeResourceTopology
- It is recommended that koordlet obtain the CPU allocation information of the existing K8s Guaranteed Pod by parsing the CPU state checkpoint file. Or obtain this information through the CRI interface and gRPC provided by kubelet.
- When the CPU of the Pod is allocated by koord-scheduler, replace the CPUs in the kubelet state checkpoint file.
- It is recommended that koordlet obtain the CPU manager policy and options from [kubeletConfiguration](https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/).

##### Example

A complete NodeResourceTopology example:

```yaml
apiVersion: topology.node.k8s.io/v1alpha1
kind: NodeResourceTopology
metadata:
  annotations:
    kubelet.koordinator.sh/cpu-manager-policy: |-
      {
        "policy": "static",
        "options": {
          "full-pcpus-only": "true",
          "distribute-cpus-across-numa": "true"
        }
      }
    node.koordinator.sh/cpu-topology: |-
          {
            "detail": [
              {
                "id": 0,
                "core": 0,
                "socket": 0,
                "node": 0
              },
              {
                "id": 1,
                "core": 1,
                "socket": 1,
                "node": 1
              }
            ]
          }
    node.koordinator.sh/cpu-shared-pools: |-
      [
        {
          "socket": 0,
          "node": 0, 
          "cpuset": "0-3"
        }
      ]
    node.koordinator.sh/pod-cpu-allocs: |-
      [
        {
          "namespace": "default",
          "name": "static-guaranteed-pod",
          "uid": "32b14702-2efe-4be9-a9da-f3b779175846",
          "cpu": "4-8",
          "managedByKubelet": "true"
        }
      ]
  labels:
    app.kubernetes.io/managed-by: Koordinator
  name: node1
topologyPolicies: ["SingleNUMANodePodLevel"]
zones:
  - name: node-0
    type: Node
    resources:
      - name: cpu
        capacity: 20
        allocatable: 15
        available: 10
      - name: vendor/nic1
        capacity: 3
        allocatable: 3
        available: 3
  - name: node-1
    type: Node
    resources:
      - name: cpu
        capacity: 30
        allocatable: 25
        available: 15
      - name: vendor/nic2
        capacity: 6
        allocatable: 6
        available: 6
  - name: node-2
    type: Node
    resources:
      - name: cpu
        capacity: 30
        allocatable: 25
        available: 15
      - name: vendor/nic1
        capacity: 3
        allocatable: 3
        available: 3
  - name: node-3
    type: Node
    resources:
      - name: cpu
        capacity: 30
        allocatable: 25
        available: 15
      - name: vendor/nic1
        capacity: 3
        allocatable: 3
        available: 3
```

#### RebindResource CRD

Koordinator defines `RebindResource` CRD to resolve user story [#6](#story-6).

The `RebindResource` CRD trigger koord-scheudler rebinds the Pods on the specified Node with action such as `RebindCPU` to get better orchestration. When the koord-scheduler reconciling the CRD instance, koord-scheduler locks the node, sort the Pods in the node, and reallocate the CPUs in the new order. The reallocated CPUs will be updated to the Pod's annotation `scheduling.koordinator.sh/resource-status`. koordlet perceives changes in Pod annotation and updates cgroups.

Through the re-adjust mechanism, it is possible to obtain several benefits:
1. Pods have a chance to get better orchestration.
2. Free up more allocatable CPU orchestration space.

There are also some risks:
1. Pods that do not originally cross NUMA Nodes will cross NUMA Nodes.
2. May cause some sensitive applications to shake during adjustment.

Overall, re-adjust the CPU brings more benefits. The scheme is defined as follows:

```go

type RebindAction string

const (
  RebindCPU RebindAction = "RebindCPU"
)

type RebindPhase string

const (
  RebindPending RebindPhase = "Pending"
  RebindSucceed RebindPhase = "Succeed"
  RebindFailed  RebindPhase = "Failed"
  RebindUnknown RebindPhase = "Unknown"

  DefaultDeadlineInSeconds int64 = 600
)

// RebindResourceSpec defines the desired state of RebindResource
type RebindResourceSpec struct {
  // NodeName which need rebind resource
  NodeName string `json:"nodeName"`

  // RebindAction means the rebind action
  RebindAction RebindAction `json:"rebindAction"`

  // DeadlineInSeconds means the action duration for the rebind action
  DeadlineInSeconds *int64 `json:"deadlineInSeconds,omitempty"`
}

// RebindResourceStatus defines the observed state of RebindResource
type RebindResourceStatus struct {
  UpdateTimestamp *metav1.Time  `json:"updateTimestamp,omitempty"`
  Phase           RebindPhase   `json:"phase,omitempty"`
  Reason          string        `json:"reason,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced

// RebindResource is the Schema for the rebindresources API
// +k8s:openapi-gen=true
// +kubebuilder:resource:scope=Cluster
type RebindResource struct {
  metav1.TypeMeta   `json:",inline"`
  metav1.ObjectMeta `json:"metadata,omitempty"`

  Spec   RebindResourceSpec   `json:"spec,omitempty"`
  Status RebindResourceStatus `json:"status,omitempty"`
}
```

### Fine-grained CPU orchestration plugin

The API defined in this proposal requires koord-scheduler support to fully implement the corresponding capabilities. 

The plugin extends the Filter/Score/Reserve/PreBind extension points. Filter the nodes that satisfied strategies defined by the Pod and Node, scoring each Node by the NUMA Node Topology, Pod and Node CPU strategies, allocate logical CPUs in reserve stage, as the result be updated to annotation of Pod before binding.

#### Filter phase

- Skip all nodes that have not `NodeResourceTopology`.
- If the node has the label `node.koordinator.sh/cpu-bind-policy` with value `PCPUOnly` or the `NodeResourceTopology` has the label `kubelet.koordinator.sh/cpu-manager-policy` contains the option `full-pcpus-only=true`, the plugin will check if the number of CPUs requested by the Pod meets the requirements, if not, return Unschedulable.
   - Pod's K8s QoS **MUST BE** Guaranteed or Pod's Koordinator QoS **MUST BE** LSE/LSR
   - Pod's Request and Limit **MUST BE** equal 
   - Pod's CPU quantity **MUST BE** multiple of 1000
- If the node has the label `node.koordinator.sh/numa-topology-alignment-policy` with value `Restricted/SingleNUMANode` or the field `TopologyPolicies` in `NodeResourceTopology` has the value `Restricted/SingleNUMANodePodLevel/SingleNUMANodeContainerLevel`, the plugin will find the statisfied NUMA Nodes by the policy, and if not, return Unschedulable. The algorithm borrows from [the proposal](https://github.com/kubernetes-sigs/scheduler-plugins/tree/master/kep/119-node-resource-topology-aware-scheduling).

#### Score phase

1. Score 0 if there is no `NodeResourceTopology` for the node.
2. Calculate the NUMA Node Topology score as **_A_** when the node has the label `node.koordinator.sh/numa-topology-alignment-policy` with value `BestEffort/Restricted/SingleNUMANode`. use the `ScoringStrategy` in plugin configuration to score each satisfied NUMA Node, and get the minimum score.
3. Calculate the CPU allocation score as **_B_** for the LSE/LSR/K8s Guaranteed Pods with CPUBindPolicy and CPUExclusivePolicy:
   1. find the best NUMA Node by the `node.koordinator.sh/numa-topology-alignment-policy`, and if the value is `None`, get the policy from plugin configuration.
   1. filter satisfied logical CPUs with `CPUBindPolicy` and `CPUExclusivePolicy` in above filtered NUMA Nodes.
   1. calculate the number of NUMA Nodes involved as `requested`
   1. use the `ScoringStrategy` in plugin configuration to score, the _capacity_ of `LeastAllocated` and `MostAllocated` is _total(NUMA Node)_ , e.g. the Node has 8 NUMA Node, current Pod request 4 logical CPUs that allocated in 2 NUMA Node in the node, so the `LeastAllocated` formulas is *score = (8-2) * 100 / 8.*
4. LS Pods do not need special calculations, just follow the scores calculated by NUMA Node Topology.
5. final score:  _score = A + B_
6. Normalize the scores.

#### Reserve phase

According to the requirements of `scheduling.koordinator.sh/resource-spec` in the Pod and the requirements of the node CPU orchestration strategies, use the CPU allocation algorithm to allocate the CPU and record the result in `CycleState`.

If it is LS Pod, find the best-fit NUMA Node by NUMA topology alignment policy that be as the CPU Shared Pool.

In order to prevent nodes from being repeatedly allocated during the scheduling process, it is necessary to record the resource allocation information through the Reserve extension point. This scenario generally occurs in the Pod has been successfully scheduled, but some information such as the logical core allocation information of the CPU, the availability information of NUMA Node, etc. has not been recorded in the NodeResourceTopology.

#### PreBind phase

Update the annotation `scheduling.koordinator.sh/resource-status` of the Pod in the PreBind extension point to record the allocated CPU information that from the `CycleState`.

#### CPU Allocation Algorithm

The algorithm MUST BE stable, that means when reallocating with same NUMA Topology, same allocated CPUs and same requirements, get same result.

The following is an approximate brief algorithm logic:
1. If the node has the label `node.koordinator.sh/numa-topology-alignment-policy` with value `BestEffort/Restricted/SingleNUMANode`, filter the best-fit NUMA Nodes.
1. allocated from the best-fit NUMA Nodes
   1. if expect bind CPU with `CPUBindPolicyFullPCPUs` or current machine architecture is ARM, filter completely unallocated physical cores from NUMA Node by the NUMA allocate policy.
   1. failed to allocate if the topology alignment policy is `Restricted/SingleNUMANode` and has CPUs that are unallocated
   1. allocated remained CPUs with `CPUBindPolicySpreadByPCPUs` policy
   1. if expect bind CPU with `CPUBindPolicySpreadByPCPUs` policy
      1. filter unallocated logical CPUs with isolate policy `PCPU` or `NUMANode` from NUMA Node. 
      1. allocate remained CPUs from NUMA Node by NUMA allocate strategy.
1. Choose a different NUMA Node iteration order based on the configuration of `node.koordinator.sh/numa-allocate-policy`.

#### Plugin Configuration

```go
type CPUOrchestrationPluginArgs struct {
  metav1.TypeMeta

  DefaultCPUBindPolicy        CPUBindPolicy               `json:"defaultCPUBindPolicy,omitempty"`
  NUMATopologyAlignmentPolicy NUMATopologyAlignmentPolicy `json:"numaTopologyAlignmentPolicy,omitempty"`

  ScoringStrategy ScoringStrategy `json:"scoringStrategy,omitempty"`
}

// ScoringStrategyType is a "string" type.
type ScoringStrategyType string

const (
  // MostAllocated strategy favors node with the least amount of available resource
  MostAllocated ScoringStrategyType = "MostAllocated"
  // BalancedAllocation strategy favors nodes with balanced resource usage rate
  BalancedAllocation ScoringStrategyType = "BalancedAllocation"
  // LeastAllocated strategy favors node with the most amount of available resource
  LeastAllocated ScoringStrategyType = "LeastAllocated"
)

// ScoringStrategy define ScoringStrategyType for the plugin
type ScoringStrategy struct {
  // Type selects which strategy to run.
  Type ScoringStrategyType

  // Resources a list of pairs <resource, weight> to be considered while scoring
  // allowed weights start from 1.
  Resources []schedconfig.ResourceSpec
}
```

- `DefaultCPUBindPolicy` represents the default bind policy. If not set, use `FullPCPUs` as default value.
- `NUMATopologyAlignmentPolicy` represents the default NUMA topology alignment policy, If not set, use `BestEffort` as default value.
- `ScoringStrategy` represents the node resource scoring strategy. If not set, use `MostAllocated` as default value.

## Alternatives

### Defined Koordinator NodeResourceTopology CRD

- The current community-defined NodeResourceTopology only describes the NUMA Node dimension, and some specific hardware information, such as CPU topology, is not defined in detail. We can consider defining Koordinator's own CRD to describe more NUMA information. In addition, we can also consider promoting the community and improving NodeResourceTopology.

## Unsolved Problems

- Provides more flexible CPU exclusive policies. We can consider the idea of Pod Affinity/AntiAffinity to support the realization of affinity and anti-affinity between different applications in the CPU or NUMA Node dimension.

## Implementation History

- 2022-06-05: Initial proposal sent for review
- 2022-06-08: Refactor the document layout and add more complete clarification compatibility logic.
- 2022-06-16: Add more details
  - Change some APIs's name
  - Add details about how to process newly created K8s Guaranteed Pod
  - Support Burstable Pod staticly bind CPU
- 2022-06-24: Fix typo
- 2022-07-11: Adjust CPUBindPolicyNone to CPUBindPolicyDefault
- 2022-08-02: Update PodCPUAllocs definition
- 2022-09-08: Add ReservedCPUs in KubeletCPUManagerPolicy
- 2022-12-02: Clarify the mistakes in the original text and add QoS CPU orchestration picture
- 2022-12-12: NodeCPUBindPolicy support SpreadByPCPUs