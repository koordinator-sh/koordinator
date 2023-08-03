---
title: DeviceShare Supports Scoring
authors:
- "@eahydra"
reviewers:
- "@hormes"
- "@jasonliu747"
- "@FillZpp"
- "@zwzhang0107"
creation-date: 2023-08-02
last-updated: 2023-08-02
status: provisional

---

<!-- TOC -->

- [DeviceShare Supports Scoring](#deviceshare-supports-scoring)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [User stories](#user-stories)
        - [Story 1](#story-1)
        - [Story 2](#story-2)
        - [Story 3](#story-3)
    - [Proposal](#proposal)
        - [Score Strategy](#score-strategy)
        - [Scoring](#scoring)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

# DeviceShare Supports Scoring

## Summary

This proposal adds a scoring mechanism to DeviceShare plugins, which supports different strategies for spreading or bin-packing device resources.

## Motivation

The DeviceShare plugin in koord-scheduler supports multiple resource APIs, such as `nvidia.com/gpu`, `koordinator.sh/gpu-memory`, `koordinator.sh/gpu-memory-ratio`, `koordinator.sh/gpu-core`, etc. Users can use the above resource API to create some Pods on the same node, and the DeviceShare plugin will normalize resource requests to `koordinator.sh/gpu-memory`, etc., for example, convert `nvidia.com/gpu=1` to `koordinator.sh/gpu-core=100` and `koordinator.sh/gpu-memory-ratio=100`, this resource combination method is more flexible to use, but it breaks the node resource scoring algorithm of the NodeResourceFit plugin. Therefore, the same resource normalization method needs to be used in the DeviceShare plugin to score.

### Goals

1. Describe the scoring details of DeviceShare
2. Define the scoring strategy and configuration methods supported by DeviceShare

### Non-Goals/Future Work

1. None


## User stories

### Story 1

Users have higher requirements for high availability and need to use more idle nodes as much as possible.

### Story 2

The resources held by users are relatively tight, and they hope to use nodes with less remaining resources as much as possible.

### Story 3

Users also expect to consider high availability at the device instance dimension or give priority to using device instances with fewer remaining resources as much as possible.

## Proposal

### Score Strategy

In order to solve these problems, a new field `scoreStrategy` is added in the plugin configuration `DeviceShareArgs`, which supports users to customize different scoring strategies. The two most common strategies are currently supported: `MostAllocated` and `LeastAllocated`. By default `LeastAllocated` is used. 

And the resource dimension is scored according to `koordinator.sh/gpu-memory-ratio` by default, with a weight of 1. Users can customize the weight of device resources defined by koordinator as needed, such as `koordinator.sh/gpu-core`, `koordinator.sh/gpu-memory` or `koordinator.sh/rdma`. Resources with no weight set will be ignored when scoring.

```diff
diff --git a/pkg/scheduler/apis/config/v1beta2/types.go b/pkg/scheduler/apis/config/v1beta2/types.go
index 0f68b7d5c..473364228 100644
--- a/pkg/scheduler/apis/config/v1beta2/types.go
+++ b/pkg/scheduler/apis/config/v1beta2/types.go
@@ -203,4 +203,6 @@ type DeviceShareArgs struct {
 
        // Allocator indicates the expected allocator to use
        Allocator string `json:"allocator,omitempty"`
+       // ScoringStrategy selects the device resource scoring strategy.
+       ScoringStrategy *ScoringStrategy `json:"scoringStrategy,omitempty"`
 }
+
+type ScoringStrategyType string

+const (
+	// MostAllocated strategy favors node with the least amount of available resource
+	MostAllocated ScoringStrategyType = "MostAllocated"
+	// LeastAllocated strategy favors node with the most amount of available resource
+	LeastAllocated ScoringStrategyType = "LeastAllocated"
+)
+
+type ScoringStrategy struct {
+	// Type selects which strategy to run.
+	Type ScoringStrategyType `json:"type,omitempty"`
+
+	// Resources a list of pairs <resource, weight> to be considered while scoring
+	// allowed weights start from 1.
+	Resources []schedconfig.ResourceSpec `json:"resources,omitempty"`
+}
```

### Scoring

The special feature of the scoring of the DeviceShare plugin is that it needs to consider the resource usage of the device instance and the overall resource usage of the node. The former is only calculated in the final resource allocation stage (Reserve stage), which affects the ordering of device instances; the latter is considered in the Score stage and affects the ordering of nodes. But both use the same strategy declared by the plugin parameter.

The overall scoring implementation needs to be described here. It is necessary to consider the total capacity and usage according to the normalized resource statistics. Usage includes already allocated and currently requested. When scoring a node, it is necessary to traverse each device instance, accumulate the total capacity and usage of each device, and use the accumulated value to call MostAllocated or LeastAllocated to calculate the score.

The scoring algorithm follows the existing implementation of LeastAllocated/MostAllocated and will not be repeated in this proposal.


```go
// resourceAllocationScorer contains information to calculate resource allocation score.
type resourceAllocationScorer struct {
	Name                string
	scorer              func(requested, allocatable resourceToValueMap) int64
	resourceToWeightMap resourceToWeightMap
}

// resourceToValueMap is keyed with resource name and valued with quantity.
type resourceToValueMap map[corev1.ResourceName]int64

// scoreDevice will use `scorer` function to calculate the scoreDevice.
func (r *resourceAllocationScorer) scoreDevice(podRequest corev1.ResourceList, total, free corev1.ResourceList) int64 {
	if r.resourceToWeightMap == nil {
		return 0
	}

	requested := make(resourceToValueMap)
	allocatable := make(resourceToValueMap)
	for resourceName := range r.resourceToWeightMap {
		totalQuantity := total[resourceName]
		if !totalQuantity.IsZero() {
			used := totalQuantity.DeepCopy()
			used.Sub(free[resourceName])
			req := podRequest[resourceName]
			req.Add(used)
			allocatable[resourceName], requested[resourceName] = totalQuantity.Value(), req.Value()
		}
	}

	score := r.scorer(requested, allocatable)
	return score
}

func (r *resourceAllocationScorer) scoreNode(podRequest corev1.ResourceList, totalDeviceResources, freeDeviceResources deviceResources) int64 {
	if r.resourceToWeightMap == nil {
		return 0
	}

	requested := make(resourceToValueMap)
	allocatable := make(resourceToValueMap)
	for resourceName := range r.resourceToWeightMap {
		var total resource.Quantity
		for _, deviceRes := range totalDeviceResources {
			total.Add(deviceRes[resourceName])
		}
		if total.IsZero() {
			continue
		}
		var free resource.Quantity
		for _, deviceRes := range freeDeviceResources {
			free.Add(deviceRes[resourceName])
		}

		if total.Cmp(free) >= 0 {
			req := total.DeepCopy()
			req.Sub(free)
			req.Add(podRequest[resourceName])
			allocatable[resourceName], requested[resourceName] = total.Value(), req.Value()
		}
	}

	score := r.scorer(requested, allocatable)
	return score
}
```

## Alternatives

Users may expect that the scoring strategy for the node dimension is different from the scoring strategy for device allocation. For example, MostAllocated is used for node scoring, but LeastAllocated is used for device scoring. In this way, the node dimension should be bin-packed as much as possible, and the device dimension should be scattered to achieve high availability. This is certainly possible, but this kind of explanation is relatively poor, so support is not considered for the time being.

## Implementation History

- 2023-08-02: Initial proposal sent for review