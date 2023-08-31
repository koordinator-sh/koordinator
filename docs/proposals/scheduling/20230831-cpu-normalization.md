---
title: CPU Normalization
authors:
  - "@zqzten"
reviewers:
  - "@saintube"
  - "@eahydra"
  - "@zwzhang0107"
  - "@hormes"
creation-date: 2023-08-31
last-updated: 2023-10-10
---

# CPU Normalization

## Table of Contents

<!-- TOC -->
* [CPU Normalization](#cpu-normalization)
  * [Table of Contents](#table-of-contents)
  * [Summary](#summary)
  * [Motivation](#motivation)
    * [Goals](#goals)
    * [Non-Goals/Future Work](#non-goalsfuture-work)
  * [Proposal](#proposal)
    * [User Stories](#user-stories)
      * [Story 1](#story-1)
    * [Architecture](#architecture)
    * [Workflow](#workflow)
    * [API](#api)
      * [Node CPU Normalization Enablement & Ratio](#node-cpu-normalization-enablement--ratio)
      * [CPU Basic Info](#cpu-basic-info)
      * [CPU Normalization Config](#cpu-normalization-config)
    * [Notes](#notes)
      * [Container CPU Utilization Monitoring](#container-cpu-utilization-monitoring)
      * [CPU Performance Evaluation](#cpu-performance-evaluation)
    * [Constraints](#constraints)
<!-- TOC -->

## Summary

Introduce an end-to-end mechanism to normalize the allocatable CPU of Kubernetes Nodes according to their performance, and finally achieve the effect that every CPU unit in Kubernetes resource declaration provides consistent computing power among heterogeneous Nodes. In other words, it makes workloads with the same CPU request get the same CPU computing power on different Nodes with different CPUs.

## Motivation

It's quite common for users to have a Kubernetes cluster with heterogeneous nodes nowadays. As CPUs with different architecture and generations may have large performance gap, different pods of workload with the same CPU request may get very different CPU computing power and thus leads to resource waste (for those running on powerful CPUs) or unexpected application performance degradation (for those running on less powerful CPUs).

### Goals

- Introduce an end-to-end mechanism to normalize the allocatable CPU of Kubernetes Nodes based on Node resource amplification.
- Normalize CPU for existing and new CPUShare Pods without affecting any CPUSet Pod on Nodes with CPU normalization enabled.

### Non-Goals/Future Work

- Normalize CPU for CPUSet Pods.
- Provide CPU performance evaluation.

## Proposal

### User Stories

#### Story 1

User has a Kubernetes cluster with heterogeneous Nodes equipped with different types of CPUs and wants to normalize their computing power among the cluster. Firstly, he evaluates the performance of each type of CPU in the cluster and calculates their corresponding normalization ratio. He then writes the normalization ratio mapping information to the `slo-controller-config` ConfigMap of koordinator and labels Nodes that he would like to enable CPU normalization. After that, he can apply CPUShare workloads to these Nodes and expect the ones with the same CPU request get the same CPU computing power among all the Nodes with CPU normalization enabled.

### Architecture

![image](/docs/images/cpu-normalization.svg)

Note: The Node Webhook and Scheduler are in dotted box because they are responsible for general Node resource amplification which is covered in the Node Resource Amplification proposal, and is out of the scope of this proposal.

### Workflow

On k8s side, user (end user or other system) writes CPU normalization model which contains CPU basic info to normalization ratio mapping information to the `slo-controller-config` ConfigMap and labels Nodes to enable CPU normalization for them.

On koord-manager side, the Node resource controller runs a reconcile loop to do following things for Nodes with CPU normalization enabled:
1. Get CPU normalization ratio of the Node from CPU normalization model according to the Node's CPU basic info from its `NodeResourceTopology`.
2. Calculate the final CPU amplification ratio (this is covered in the Node Resource Amplification proposal) by following formula: `CPU_Amplification_Ratio = CPU_Amplification_Ratio_Original * CPU_Normalization_Ratio`. The `CPU_Amplification_Ratio_Original` defaults to 1 if not specified beforehand.
3. Annotate the CPU normalization ratio and new CPU amplification ratio to the Node.

On koordlet side:

- A runtime hook intercepts the CPU resource allocating request from kubelet and allocates actual underlying CPU resource for CPUShare Pods according to the CPU normalization ratio. It also keeps reconciling CPU resource for all CPUShare Pods on the Node to make sure the normalization is applied to any existing CPUShare Pod. For details, it would do following mutation to CPU resource allocating for CPUShare Pods:
    * The `CFSQuota` of the Pod and every container of the Pod would be mutated to `CFSQuota_Original / CPU_Normalization_Ratio`.
- The node info collector of metrics advisor collects CPU basic info of the Node including CPU model and the enablement of hyper-threading and turbo-boost.
- The NRT states informer:
  * Reports CPU basic info of the Node to an annotation of its `NodeResourceTopology`.
  * Amplifies CPU resource in `zones` of `NodeResourceTopology` of the Node. This can make sure the NUMA topology scheduling of CPUShare Pods working properly with amplified allocatable CPU.

### API

#### Node CPU Normalization Enablement & Ratio

- Introduce a new Node label `node.koordinator.sh/cpu-normalization-enabled` whose value is a bool string. This is used by user to specify the enablement of CPU normalization of the Node.
- Introduce a new Node annotation `node.koordinator.sh/cpu-normalization-ratio` whose value is a float64 number string. The float value must >= 1. This is used by koord-manager to specify the CPU normalization ratio of the Node, and later read by koordlet to do normalization works.

#### CPU Basic Info

Extend the original `CPUBasicInfo` in `pkg/koordlet/util/cpuinfo.go` to below struct:

```go
type CPUBasicInfo struct {
	CPUModel           string `json:"cpuModel,omitempty"`
	HyperThreadEnabled bool   `json:"hyperThreadEnabled,omitempty"`
	TurboEnabled       bool   `json:"turboEnabled,omitempty"`
	CatL3CbmMask       string `json:"catL3CbmMask,omitempty"`
	VendorID           string `json:"vendorID,omitempty"`
}
```

The `CPUModel`, `HyperThreadEnabled` and `TurboEnabled` would be used by koord-manager to find the CPU normalization ratio for the Node.

#### CPU Normalization Config

Introduce below struct as `cpu-normalization-config` in the `slo-controller-config` ConfigMap:

```go
// CPUNormalizationCfg is the cluster-level configuration of the CPU normalization strategy.
type CPUNormalizationCfg struct {
	CPUNormalizationStrategy `json:",inline"`
	NodeConfigs              []NodeCPUNormalizationCfg `json:"nodeConfigs,omitempty"`
}

// NodeCPUNormalizationCfg is the node-level configuration of the CPU normalization strategy.
type NodeCPUNormalizationCfg struct {
	NodeCfgProfile `json:",inline"`
	CPUNormalizationStrategy
}

// CPUNormalizationStrategy is the CPU normalization strategy.
type CPUNormalizationStrategy struct {
	// Enable defines whether the cpu normalization is enabled.
	// If set to false, the node cpu normalization ratio will be removed.
	Enable *bool `json:"enable,omitempty"`
	// RatioModel defines the cpu normalization ratio of each CPU model.
	// It maps the CPUModel of BasicInfo into the ratios.
	RatioModel map[string]ModelRatioCfg `json:"ratioModel,omitempty"`
}

// ModelRatioCfg defines the cpu normalization ratio of a CPU model.
type ModelRatioCfg struct {
	// BaseRatio defines the ratio of which the CPU neither enables Hyper Thread, nor the Turbo.
	BaseRatio *float64 `json:"baseRatio,omitempty"`
	// HyperThreadEnabledRatio defines the ratio of which the CPU enables the Hyper Thread.
	HyperThreadEnabledRatio *float64 `json:"hyperThreadEnabledRatio,omitempty"`
	// TurboEnabledRatio defines the ratio of which the CPU enables the Turbo.
	TurboEnabledRatio *float64 `json:"turboEnabledRatio,omitempty"`
	// HyperThreadTurboEnabledRatio defines the ratio of which the CPU enables the Hyper Thread and Turbo.
	HyperThreadTurboEnabledRatio *float64 `json:"hyperThreadTurboEnabledRatio,omitempty"`
}
```

This should be manually configured by user according to the performance evaluation of CPUs in cluster and would be used by koord-manager to find the CPU normalization ratio for Nodes with different CPU types and settings. Also, this can be used to set the enablement of CPU normalization of Nodes but with lower priority than the label way.

### Notes

#### Container CPU Utilization Monitoring

Enabling CPU normalization would bring a subtle semantic issue on container CPU utilization monitoring:

Container CPU utilization in monitoring system is calculated by following formula: `CPU_Utilization = CPU_Time / CPU_Request`, as both the CPU time and request would not change on enabling CPU normalization, user would not notice this change in monitoring.

However, the CPU utilization under this case does not reflect the _real utilization_ because the CPU request of the container does not match the actual physical CPU resource allocated to it. For example, for a container with CPU request 2 on a Node with normalization ratio 2, the actual physical CPU resource the Pod can use is only 1, so that its maximum CPU utilization would be 50%, which can be confusing. So if user would like to see _real utilization_ in monitoring system, the calculation formula for container CPU utilization should be modified to `CPU_Utilization = CPU_Time / (CPU_Request / CPU_Normalization_Ratio)`.

#### CPU Performance Evaluation

Currently, we would not provide any standard CPU performance evaluation method since it is complicated and may vary in different use cases. In general cases, users can utilize standard CPU benchmarks such as [SPEC](https://www.spec.org/cpu/) and normalize the benchmark result as CPU normalization ratio. But note that the baseline (whose normalization ratio is 1) MUST BE the one with the worst performance since the CPU normalization ratio must not be smaller than 1, which is covered in [constraints](#constraints).

### Constraints

- The CPU normalization ratio must not be smaller than 1, otherwise there's a chance that CPU requests of CPUSet Pods on the Node would exceed the Node's allocatable CPU which causes kubelet to reject/evict Pods.
- If there are CPUSet Pods on Node, the Node's allocatable CPU would never _seem to be_ used up because CPUSet Pods' CPU request is directly mapped to physical CPU rather than the normalized CPU.
