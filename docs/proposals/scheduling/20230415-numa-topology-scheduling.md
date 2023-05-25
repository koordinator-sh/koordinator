---
title: NUMA Topology Scheduling
authors:
- "@eahydra"
reviewers:
- "@hormes"
- "@zwzhang0107"
- "@FillZpp"
- "@jasonliu747"
- "@saintube"
- "@ZiMengSheng"
creation-date: 2023-04-16
last-updated: 2023-04-25
status: provisional

---

<!-- TOC -->

- [Motivation](#motivation)
- [Goal](#goal)
- [Non-Goals](#non-goals)
- [User Stories](#user-stories)
    - [Story 1](#story-1)
    - [Story 2](#story-2)
- [Proposal](#proposal)
    - [Design Detail](#design-detail)
        - [NUMA Topology Policy](#numa-topology-policy)
            - [Example](#example)
        - [NUMA memory management policy](#numa-memory-management-policy)
        - [Changes in the Scheduler component](#changes-in-the-scheduler-component)
            - [Add NUMATopology plugin](#add-numatopology-plugin)
                - [Computing Preferred NUMA Node Affinity](#computing-preferred-numa-node-affinity)
                - [Manage NUMATopologyHintProvider](#manage-numatopologyhintprovider)
            - [Changes in the NodeNUMAResource Plugin](#changes-in-the-nodenumaresource-plugin)
            - [Changes in the DeviceShare Plugin](#changes-in-the-deviceshare-plugin)
        - [Changes in Device CRD](#changes-in-device-crd)
        - [Changes in koordlet](#changes-in-koordlet)
        - [Compatibility](#compatibility)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

# Motivation

The scheduler and koordlet of Koordinator have the ability to allocate resources according to NUMA Topology and align them with NUMA Node as much as possible, but currently only support CPU resources. In scenarios such as model training in the field of machine learning, it is required to allocate GPU, RDMA, and CPU at the same time, and also requires them to be on the same NUMA Node or even the same PCI-E, so as to obtain optimal performance.

Although Kubernetes’ kubelet provides Topology Manager, it works on the node side and is not aware of the scheduler. This may cause the scheduled Pod to encounter Topology-related errors when it is scheduled to a certain node, resulting in the Pod being unable to start. And this kind of error is sometimes unacceptable. For example, some job-class workloads may cause the entire job to fail due to this reason. Another problem is that it is impossible to obtain cluster-level optimal NUMA Node resources.

Therefore, a mechanism needs to be implemented to solve this requirement, which can coordinate different resource allocation plugins to uniformly align according to NUMA Topology and obtain optimal performance.

# Goal

1. Provide multiple NUMA Topology management strategies to support flexible resource management requirements.
1. Provide a mechanism to ensure that when scheduling, CPU/Device and other resources are coordinated and the best/preferred NUMA Node at the Pod-Level is obtained.
1. The scheduler adds extension points to support the collaborative allocation of different resource allocation plugins.
1. Describe the capabilities that koordlet needs to guarantee for different NUMA Topology strategies.

# Non-Goals

1. There is no limit to the memory management mechanism under the NUMA architecture. Users can set different kernel parameters according to the actual situation to decide whether to allow memory to be accessed across NUMA Nodes.

# User Stories

## Story 1

The user applies for CPU, GPU and RDMA, and expects the allocated resources to be on the same NUMA node to obtain optimal performance.

## Story 2

The user applies for CPU, GPU and RDMA at the same time, and expects the allocated resources to be aligned according to the NUMA node. At this time, it may be necessary to use multiple NUMA nodes.

# Proposal

## Design Detail

### NUMA Topology Policy

The label `node.koordinator.sh/numa-topology-policy` represents that how to aligning resource allocation according to the NUMA topology. 
- `None` is the default policy and does not perform any topology alignment.
- `BestEffort` indicates that the node *does not strictly* allocate resources according to NUMA Topology alignment. As long as the remaining total amount of nodes meets the needs of Pods, the scheduler can always allocate such nodes to Pods.
- `Restricted` indicates that the node allocates resources *strictly* according to NUMA Topology alignment, that is, the scheduler is required to only select the same one or more NUMA Nodes when allocating multiple resources, otherwise the node should not be used. There can be multiple NUMA Nodes. For example, if a Pod requests 33C and each NUMA Node has 32C, then it can be allocated to use two NUMA Nodes. If this Pod also needs to request GPU/RDMA, then it needs to be on the same NUMA Node as the CPU. This strategy is more flexible.
- `SingleNUMANode` is similar to Restricted, that is, it is also strictly aligned according to NUMA Topology, but unlike Restricted, Restricted allows multiple NUMA Nodes to be used, while SingleNUMANode only allows the use of one NUMA Node.

If there is no `node.koordinator.sh/numa-topology-policy` in the node's label and TopologyPolicies=None in NodeResourceTopology, it will be executed according to the policy configured by the koord-scheduler.

If both `node.koordinator.sh/numa-topology-policy` in Node and `TopologyPolicies` in NodeResourceTopology are defined and `TopologyPolicies` is not equal None, The `TopologyPolicies` in NodeResourceTopology used first since the `TopologyPolicies` come from kubelet.  If we don't do this, it may conflict with the kubelet's policy, or it may cause the Pod to be rejected by the kubelet.
The label `node.koordinator.sh/numa-topology-policy` is equivalent to the field TopologyPolicies in NodeResourceTopology. The topology policies `SingleNUMANodePodLevel` and `SingleNUMANodeContainerLevel` are mapped to the `SingleNUMANode` policy.

#### Example

The following specific example:

```yaml
apiVersion: v1
kind: Node
metadata:
  labels:
    node.koordinator.sh/numa-topology-policy: "BestEffort"
  name: node-0
spec:
  ...
```

### NUMA memory management policy

Under the NUMA architecture, memory management is relatively complex. If a Pod is completely restricted to accessing physical memory under only one NUMA Node, it may reduce stability. For this type of memory usage, the kernel parameters are generally modified, or the runtime of all Pods is responsible for locking memory, or HugePages are used to plan memory usage in advance.

Regardless of these specific methods, Koordinator does not directly provide a mechanism to lock memory, but adds a protocol to perceive whether the memory of a node needs to be processed according to NUMA Node Topology. This policy is written on the Node in the form of a Label, with Label Key: node.koordinator.sh/numa-memory-policy and value not defined for now. When there is such a label on the node, the NodeNUMAResource plugin of the scheduler will record the memory allocation situation under each NUMA Node and avoid allocating memory on NUMA Nodes where memory allocation is complete. The scheduler does not perceive the actual usage of NUMA Nodes.

### Changes in the Scheduler component

#### Add NUMATopology plugin

Koodinator Scheduler needs to introduce a new plugin NUMATopology, which is responsible for coordinating the allocation of resources between different plugins and deciding on a suitable NUMA Node based on NUMA Topology Policy, and ensuring that the NUMA Node based on the decision can allocate resources needed by Pods. And by Score mechanism, select the most suitable node at the cluster level.

NUMATopology will require plugins that perceive NUMA Topology to implement a new interface called NUMATopologyHintProvider. NUMATopologyHintProvider is borrowed from kubelet’s Topology Manager. Existing plugins NodeNUMAResource/DeviceShare need to implement this new interface.

In the Filter phase, the NUMATopology plugin will call NUMATopologyHintProvider.GetPodTopologyHints of each HintProvider to obtain the NUMA Node Hint corresponding to each resource supported by this HintProvider, indicating which NUMA Node these resources can be allocated on. The NUMATopology plugin will integrate these Hints according to Policy and calculate a NUMA Node that can satisfy all resources. If there is no such NUMA Node, it means that this node cannot participate in subsequent scheduling allocation. If there is such a NUMA Node, it is called Preferred NUMA Node. Then continue to call NUMATopologyHintProvider.Allocate method to confirm whether resources can be allocated on Preferred NUMA Node. If not, it is considered that this node cannot participate in scheduling allocation either. If this node has a Preferred NUMA Node that meets the Pod’s requirements, it will be saved in CycleState for subsequent Score/Reserve phases.

By default, MostAllocated algorithm is used for scoring, which prioritizes nodes with fewer remaining NUMA Nodes. It also supports scoring based on strategies specified by the node.koordinator.sh/numa-allocate-strategy label on nodes, but only supports two strategies corresponding to this label: MostAllocated and LeastAllocated.

MostAllocated and LeastAllocated strategies are both calculated based on the relationship between the number of used NUMA Nodes and the total number of NUMA Nodes. Taking MostAllocated as an example, if a node has 4 NUMA Nodes but one has been allocated completely and one more is needed during this scheduling process, then the calculation formula is as follows: *Score = (2 * 100)/4 = 50*

In Reserve phase, pass Preferred NUMA Node obtained in Filter phase into each NUMATopologyHintProvider.Allocate interface and set assume parameter to true, indicating that the result of this allocation needs to be assumed.


##### Computing Preferred NUMA Node Affinity

NUMATopologyHintProvider provides hints that indicate whether a combination of different NUMA Nodes using a certain resource can be allocated. For example, if a Pod requires 8000m CPU and there are 4 NUMA Nodes on a node, each NUMA Node is 8000m and assuming that no Pod is using it, each NUMA Node can be allocated, including combinations of different NUMA Nodes. For example, if NUMA Node 0 and NUMA Node 1 are combined, it will be 16000m CPU and naturally satisfies the request. However, this combination is generally not used for allocation. Therefore, a concept called “narrowest” is introduced here to indicate which combination relationship is the simplest and meets the resource request of the Pod. In the example above, each NUMA Node is naturally narrowest.

With these hints, a preferred NUMA Node can be aggregated according to different policies. For example, in the SingleNUMANode policy, when there are many hints, ensure that only one NUMA Node is selected as Preferred in the end. For the Restricted policy, just ensure that the allocated resources are aligned with the NUMA Nodes. For example, if a node has only one NUMA Socket and two NUMA Nodes and each NUMA Node has 8000m CPU. When a Pod needs 16000m CPU, Restricted can be allocated while SingleNUMANode cannot.

For more information on how Policy calculates a Preferred NUMA Nodes, please refer to the implementation of kubernetes kubelet topology manager and see document [KEP-693: Node Topology Manager#Computing Preferred Affinity](https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/693-topology-manager#computing-preferred-affinity) for specific details.

##### Manage NUMATopologyHintProvider

NUMATopology is not directly coupled with plugins that implement the NUMATopologyHintProvider interface, but requires the frameworkext.Extender of Koordinator Scheduler to manage the instances of these NUMATopologyHintProvider interfaces. When the plugin is initialized, it will be taken over by the Extender and perceive and confirm whether it implements the NUMATopologyHintProvider interface. If it does, it will hold an instance of this interface and expose these instances to the NUMATopology plugin through the GetHintProvider method.

![image](/docs/images/numa-topology-hint-provider.svg)

```go
// NUMATopologyHint is a struct containing the NUMANodeAffinity for a Container
type NUMATopologyHint struct {
    NUMANodeAffinity bitmask.BitMask
    // Preferred is set to true when the NUMANodeAffinity encodes a preferred
    // allocation for the Pod. It is set to false otherwise.
    Preferred bool
}

type NUMATopologyHintProvider interface {
    // GetPodTopologyHints returns a map of resource names to a list of possible
    // concrete resource allocations per Pod in terms of NUMA locality hints.
    GetPodTopologyHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) map[corev1.ResourceName][]NUMATopologyHint
    // Allocate triggers resource allocation to occur on the HintProvider after
    // all hints have been gathered and the aggregated Hint
    Allocate(ctx context.Context, cycleState *framework.CycleState, affinity NUMATopologyHint, pod *corev1.Pod, nodeName string, assume bool) error
}
```

#### Changes in the NodeNUMAResource Plugin

The Kubernetes Topology Manager requires the CPU Manager to enable the static policy in order to restrict CPU allocation on a specific NUMA node. However, users do not necessarily want all Pods to be bound to a CPU core, especially in colocation scenarios where using CPU shares can provide more flexible space. Therefore, the NUMANodeResource plugin of the Koordinator Scheduler needs to support both the CPUShare and CPUSet scenarios according to the NUMA Topology policy for resource allocation.

The NUMANodeResource plugin implements the NUMATopologyHintProvider interface, which gives hints about which NUMA nodes can be allocated based on the pod requests. When calculating the hints, only the actual remaining amount of each NUMA node is considered, and it is not concerned with whether the bound CPU core is optimal. The NUMATopologyHintProvider.Allocate implementation needs to confirm whether CPU resources can be allocated in the specified NUMA node based on the Preferred NUMA Affinity passed in.

The original Filter logic of the NodeNUMAResource remains unchanged, and it currently only checks whether the CPU topology recorded in the NodeResourceTopology of the node is valid. The original Score implementation also remains unchanged and only supports the CPUSet scenario.

The Reserve implementation needs to be aware of the NUMA Topology policy of the node and return if the policy is not None. In the NUMATopologyHintProvider.Allocate implementation, it is responsible for reserving resources.

In addition, the NodeNUMAResource plugin needs to support CPU Share Pods. If the NUMA Topology policy of the node is not None, resources need to be reserved in the Allocate phase, and in the PreBind phase, ResourceStatus needs to be set to indicate that CPU Share can only be implemented on the specified NUMA node.

During failover, the NodeNUMAResource plugin needs to record which NUMA nodes the CPU Share Pods have used and the amount of resources used. The plugin also needs to support preemption. Restricted/SingleNUMANode policies require that each resource can only be allocated on the specified NUMA node, which means that preemptable resources must also follow these policies.

The NodeNUMAResource plugin does not change its original logic when processing Reservation CRD objects to reserve CPUs. Because the scheduling logic followed when reserving resources is the same, the obtained resources also satisfy the NUMA Topology policy constraints, which means that Owner Pods can also reuse these resources according to the policy.

#### Changes in the DeviceShare Plugin

The DeviceShare Plugin is responsible for allocating device resources such as GPUs and RDMA. It also needs to implement the NUMATopologyHintProvider and provide hints based on the Pod requests to calculate which NUMA node is more suitable for allocating these device resources.

The overall logic changes of the DeviceShare Plugin are consistent with those of the NodeNUMAResource plugin. The difference is that the Filter implementation of the DeviceShare Plugin needs to be aware of whether the node has set a NUMA Topology policy. If it is set and not None, it will not confirm whether there are idle devices available in the Filter phase, but rather implement it in the NUMATopologyHintProvider.GetPodTopologyHints and Allocate methods.

When implementing the GetPodTopologyHints method, the DeviceShare Plugin only needs to calculate the idle device resources for each NUMA node. When implementing the Allocate method, it confirms whether allocation is possible based on the given NUMA node.

For preemption, it also needs to be processed at the granularity of the NUMA node to ensure that the preempted resources also comply with the Topology policy.

The logic for processing Reservations does not need to change at this time.

### Changes in Device CRD

To support the DeviceShare plugin's awareness of NUMA Topology Policy, it is necessary to extend the Device CRD by appending NUMA Node information to each type of device information, describing its location in the NUMA Socket/NUMA Node or even PCI-E information.

```diff
diff --git a/apis/scheduling/v1alpha1/device_types.go b/apis/scheduling/v1alpha1/device_types.go
index 496f25fa..78fddf8a 100644
--- a/apis/scheduling/v1alpha1/device_types.go
+++ b/apis/scheduling/v1alpha1/device_types.go
@@ -44,6 +44,15 @@ type DeviceInfo struct {
type DeviceInfo struct {
	// UUID represents the UUID of device
	UUID string `json:"id,omitempty"`
	// Minor represents the Minor number of Device, starting from 0
	Minor *int32 `json:"minor,omitempty"`
	// Type represents the type of device
	Type DeviceType `json:"type,omitempty"`
	// Health indicates whether the device is normal
	Health bool `json:"health,omitempty"`
	// Resources is a set of (resource name, quantity) pairs
	Resources corev1.ResourceList `json:"resources,omitempty"`
+	// Topology represents the topology information about the device
+	Topology *DeviceTopology `json:"topology,omitempty"`
+}
+
+type DeviceTopology struct {
+	SocketID int32  `json:"socketID"`
+	NodeID   int32  `json:"nodeID"`
+	PCIEID   int32  `json:"pcieID"`
+	BusID    string `json:"busID,omitempty"`
 }
```

### Changes in koordlet

Because we need to divide the CPU shared pool into multiple instances according to the NUMA Topology, some of the elasticity capabilities of koordlet need to be adapted. For example, when dynamically adjusting more CPUs for the CPUShare Pod, it should only be processed in the specified NUMA Node, rather than on the entire machine. Of course, this is not strict for a single-machine context. We can consider whether the Pod has device resources. If it does, it is best not to cross NUMA Nodes, otherwise it may cause some performance issues.

If the current NUMA Topology Policy of the node is BestEffort, we can also consider handling it according to the original elastic mechanism, and only enhance the restrictions when it is Restricted/SingleNUMANode.

In the reporting logic of the Device CRD, we need to adapt to support new fields. When reporting the NodeResourceTopology, we need to report the NUMA Topology policy of kubelet and write it to the field NodeResourceTopology.TopologyPolicies according to the NodeResourceTopology specification.

### Compatibility

- Because Koordinator Scheduler adopts the same Preferred NUMA Node Affinity calculation method as kubelet Topology Manager, and Koordinator Scheduler can perceive the Topology Policy configured by kubelet, theoretically, the two can maintain an identical logic, so there will no longer be TopologyError-related errors even when Topology Manager is enabled on the node. However, in a production environment, it is still advisable to consider disabling kubelet's Topology Manager to avoid unknown risks as much as possible.
- Koordinator Scheduler only supports resource allocation at the Pod level, so if kubelet is configured at the Container level, conflicts may occur.
- Koordinator's NUMA Topology Scheduling can only be applied to newly imported nodes. When there are existing nodes and Pods running on them, it is best to clear the Pods on the node before configuring the NUMA Topology Policy.

## Implementation History

- 2023-04-16: Initial proposal sent for review
