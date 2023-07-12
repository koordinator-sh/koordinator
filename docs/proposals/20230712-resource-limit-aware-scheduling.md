---
title: Limit Aware Scheduling
authors:
- "@ZiMengSheng"
reviewers:
- "@hormes"
- "@eahydra"
creation-date: 2023-07-12
last-updated: 2023-07-12
---

# Limit Aware Scheduling

## Motivation

Kubernetes divides pod QoS into guaranteed, burstable, and best effort. Among them, the limits of burstable pods is greater than requests. However, the current scheduler actually only considers pod requests when scheduling pods. In this way, if a large number of burstable pods are scheduled to the node, the total resource usage of the pods on the node will probably exceed the allocatable capacity of the node, that is, the resources are over-subscriped.  Resource over-subscription may lead to resource contention, performance degradation and even pod failure (e.g., OOM).

This proposal uses the limit to allocatable ratio of the node to measure the degree of resource over-subscription, and proposes a scheduling plugin named LimitAware to control the limit/allocatable of the node.

### Goals

1. Introduce a filtering plugin, determine whether the Pod can be scheduled on the node according to limit to allocatable ratios of nodes.
2. Introduce a scoring plugin to mitigate resource over-subscription issue caused by burstable pods through "spreading" or "balancing" pod's resource limits across nodes.

### Non-Goals

Replace existing resource allocated plugin.

## Proposal

### User Story

#### Story 1: Use limit aware score plugin to mitigate resource over-subscription

Consider two nodes with the same resource allocatable (8 CPU cores) and each has two pods running.

- node1: pod1(request: 2, limit: 6), pod2(request: 2, limit: 4); total request/allocatable: 4/8, total limit/allocatable: 10/8
- node2: pod3(request: 3, limit: 3), pod4(request: 2, limit: 2); total request/allocatable: 5/8, total limit/allocatable: 5/8

To schedule a new pod pod5 (request: 1, limit: 4), although node1's total request/allocatable ratio is smaller than node2's, our `LimitAware` plugin considers node1's resource limit is already oversubscribed and hence place pod5 on node 2 instead.

#### Story 2: Use limit aware filter plugin to filter out nodes with high risk of resource over-subscription

Consider a node with resource allocatable of 8 CPU cores and having two pods running. The user expects limit/allocatable less than or equal 125%.

- node1: pod1(request: 2, limit: 6), pod2(request: 2, limit: 4); total request/allocatable: 4/8, total limit/allocatable: 10/8

To schedule a new pod pod5 (request: 1, limit: 4), our `LimitAware` plugin will think pod doesn't fit node1 as node1 total limits > allocatable * (expected limit/allocatable): 14 > 8 x 125%.

## Design Details

### Allocatable and Allocated Limit of a node

This proposal actually provides a limit aware ResourceFit filter, so it is necessary to define the allocatable and allocated limit of the node. The allocated limit of a node is equal to the sum of the limits of all pods on the node, and the allocatable limit of a node is determined by the user according to the desired degree of resource over-subscription. This proposal provides plugin configuration and node annotation for users to configure the limit to allocatable ratio in cluster level and node level respectively according to the desired degree of resource over-subscription.

The node level protocol is as follows:

```go

const ( 
	// AnnotationLimitToAllocatable The limit to allocatable ratio used by limit aware scheduler plugin, represent by percentage 
	AnnotationLimitToAllocatable = NodeDomainPrefix + "/limit-to-allocatable"
)

type LimitToAllocatableRatio map[corev1.ResourceName]intstr.IntOrString


func GetNodeLimitToAllocatableRatio(annotations map[string]string, defaultLimitToAllocatableRatio LimitToAllocatableRatio) (LimitToAllocatableRatio, error) {
	data, ok := annotations[AnnotationLimitToAllocatable]
	if !ok {
		return defaultLimitToAllocatableRatio, nil
	}
	limitToAllocatableRatio := LimitToAllocatableRatio{}
	err := json.Unmarshal([]byte(data), &limitToAllocatableRatio)
	if err != nil {
		return nil, err
	}
	return limitToAllocatableRatio, nil
}
```

The cluster level protocols is as follows:

```go
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LimitAwareArgs defines the parameters for LimitAware plugin.
type LimitAwareArgs struct {
	metav1.TypeMeta 
	
	// Resources a list of pairs <resource, weight> to be considered while scoring 
	//allowed weights start from 1. 
	Resources []schedconfig.ResourceSpec
	
	DefaultLimitToAllocatableRatio extension.LimitToAllocatableRatio
}
```

### Requested Limit of Pod

Similar to calculating the resource request of a pod, we use the following function to calculate the requested limit of a Pod.

```go
func getPodResourceLimit(pod *corev1.Pod, nonZero bool) (result *framework.Resource) {
	result = &framework.Resource{}
	non0CPU, non0Mem := int64(0), int64(0)
	for _, container := range pod.Spec.Containers {
		limit := quotav1.Max(container.Resources.Limits, container.Resources.Requests)
		result.Add(limit)
		non0CPUReq, non0MemReq := schedutil.GetNonzeroRequests(&limit)
		non0CPU += non0CPUReq
		non0Mem += non0MemReq
	}
	// take max_resource(sum_pod, any_init_container) 
	for _, container := range pod.Spec.InitContainers {
		limit := quotav1.Max(container.Resources.Limits, container.Resources.Requests)
		result.SetMaxResource(limit)
		non0CPUReq, non0MemReq := schedutil.GetNonzeroRequests(&limit)
		non0CPU = max(non0CPU, non0CPUReq)
		non0Mem = max(non0Mem, non0MemReq)
	}
	// If Overhead is being utilized, add to the total limits for the pod 
	if pod.Spec.Overhead != nil {
		result.Add(pod.Spec.Overhead)
		if _, found := pod.Spec.Overhead[corev1.ResourceCPU]; found {
			non0CPU += pod.Spec.Overhead.Cpu().MilliValue()
		}
		if _, found := pod.Spec.Overhead[corev1.ResourceMemory]; found {
			non0Mem += pod.Spec.Overhead.Memory().Value()
		}
	}
	if nonZero {
		result.MilliCPU = non0CPU
		result.Memory = non0Mem
	}
	return
}
```

When the container does not specify Limits, use requests as the limits of the container.
For cpu and memory, a pod that doesn't explicitly specify request and limit will be treated as having limited the amount indicated below, for the purpose of computing priority only. 
This ensures that when scheduling zero-limit pods, such pods will not all be scheduled to the machine with the smallest in-use limit, and that when scheduling regular pods, such pods will not see zero-limit pods as consuming no resources whatsoever.

### Filter out nodes with high risk of resource over-subscription

From before, we can already determine the allocatable limit and allocated limit of Node. When scheduling a new pod, all nodes which satisfy requested limit + allocated limit > allocatable limit will be filtered out.

It is possible for DaemonSet not to set Request, but to set Limit; and DaemonSet is quite special, generally it is a basic component, and it is possible to use DaemonSet to expand its functions. 
So we skip to check DaemonSet pods during the Filter stage.
### Score nodes to favor less risk of resource over-subscription

This proposal actually borrows [the idea of kubernetes-sigs to score nodes](https://github.com/kubernetes-sigs/scheduler-plugins/blob/master/kep/217-resource-limit-aware-scoring/README.md#design-details) but replace node's allocatable resource with our allocatable limit.

Since the main purpose of the limit aware score plugin is to reduce the risk of resource over-subscription through spreading or balancing, this proposal uses the least allocated limit strategy to score nodes.

1. Calculate each node’s raw score. A node's raw score will be negative for an node of which the allocated limit is greater than allocatable limit .
    - `Score = (allocatable limit - allocated limit) * MaxNodeScore / allocatable limit`
    - For multiple resources, the raw score is the weighted sum of all allocatable resources. The resources and their weights are configurable as the plugin arguments. Like the other `NodeResources` scoring plugins, the plugin supports standard resource types: CPU, Memory, Ephemeral-Storage plus any Scalar-Resources.
2. Normalize the raw score to `[MinNodeScore , MaxNodeScore]` (e.g., [0, 100]) after all nodes are scored.
    - `NormalizedScore = MinNodeScore+(RawScore-LowestRawScore)/(HighestRawScore-LowestRawScore) *(MaxNodScore - MinNodeScore)`
3. Choose the node with the highest score.

## Alternatives

The kubernetes-sigs of scheduler plugins also noticed this problem and proposed [resource limit aware scoring](https://github.com/kubernetes-sigs/scheduler-plugins/blob/master/kep/217-resource-limit-aware-scoring/README.md) plugins. However, they don't support configuring limit to allocatable ratio of nodes. Configurable allocatable limit is really useful when different nodes have different resource-contention degree according to prior knowledge. 