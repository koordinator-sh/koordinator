---
title: Node Resource Amplification
authors:
  - "@zqzten"
reviewers:
  - "@saintube"
  - "@eahydra"
  - "@hormes"
creation-date: 2023-08-11
last-updated: 2023-10-08
---

# Node Resource Amplification

## Table of Contents

<!-- TOC -->
* [Node Resource Amplification](#node-resource-amplification)
  * [Table of Contents](#table-of-contents)
  * [Summary](#summary)
  * [Motivation](#motivation)
    * [Goals](#goals)
    * [Non-Goals/Future Work](#non-goalsfuture-work)
  * [Proposal](#proposal)
    * [User Stories](#user-stories)
    * [Workflow](#workflow)
    * [API](#api)
    * [Constraints](#constraints)
    * [Risks and Mitigations](#risks-and-mitigations)
      * [Amplification Ratio Change](#amplification-ratio-change)
      * [Unexpected Resource Overcommitment](#unexpected-resource-overcommitment)
<!-- TOC -->

## Summary

Introduce a mechanism to amplify the resources of Kubernetes Nodes **at scheduling level**, which enables the node to meet higher pod CPU requests. This feature can then be utilized by other higher-level ones such as CPU normalization.

## Motivation

There are cases where users would like to amplify the resources of a node, for example:

- The node is equipped with a new-gen CPU, which is way faster than those on other nodes. In this case, users would like to amplify the allocatable CPU of the node in order to make most of the fast CPU of the node.
- The node's allocatable CPU is nearly full but the actual CPU usage remains at an extremely low level. In this case, users would like to amplify the allocatable CPU of the node in order to improve the CPU utilization of the node as well as to increase the amount of schedulable pods of the cluster.

### Goals

- Introduce a mechanism to amplify resources (capacity & allocatable) of Kubernetes Nodes at scheduling level.
- Make sure CPUSet Pods get raw CPUs it requested on scheduling.

### Non-Goals/Future Work

- Arrange underlying resources on Node with those amplified.

## Proposal

### User Stories

This has been covered in [motivation](#motivation).

### Workflow

On k8s side, user (end user or other system) annotates the Node to specify its resource amplification ratio.

On koord-manager side, the Node mutating webhook intercepts the Node status updating request from kubelet and modifies its `capacity` and `allocatable` according to the amplification ratio. It also records their raw values in annotations of the Node.

On koord-scheduler side, the `NodeNUMAResource` scheduling plugin filters and scores Nodes with similar logic to the `NodeResourcesFit` plugin for those without NUMA topology policy, but with one difference: It amplifies the request of CPUSet Pods with the Node's CPU normalization ratio, as CPUSet Pods would get real physical CPUs requested so that their requests would occupy more CPU resources than what they specify on Nodes with allocatable CPU amplified. For example, a CPUSet Pod with CPU request 1 would occupy 1 physical CPU on a Node, if the Node's CPU amplification ratio is 2, the Pod would actually occupy 2 amplified CPUs (which are mapped to 1 physical CPU) on this Node. Similarly, for those with NUMA topology policy, the plugin filters and scores Nodes with the same special logic with those NUMA zone CPU resources amplified.

### API

- Introduce a new Node annotation `node.koordinator.sh/resource-amplification-ratio` whose value is a JSON object string of type `map[corev1.ResourceName]float64`. The float value must > 1. This is used by user to specify the amplification ratio for each resource of the Node.
- Introduce two new Node annotations `node.koordinator.sh/raw-capacity` and `node.koordinator.sh/raw-allocatable` whose value is a JSON object string of type `corev1.ResourceList`. These are used by koord-manager to record the Node's original resources (the `status.capacity` and `status.allocatable`), just informational for now.

### Constraints

To impose clear semantics, the Node resource amplification ratio must not be smaller than 1.

### Risks and Mitigations

#### Amplification Ratio Change

If user disables the resource amplification or reduces its ratio for a Node, there's a chance that resource requests of Pods on the Node would exceed the Node's allocatable which causes kubelet to reject/evict Pods. This can lead to unexpected behavior.

To mitigate this risk, a Node validating webhook can be introduced to validate the setting of resource amplification. It will check if the current resource requests of Pods on the Node would exceed the Node's allocatable after resource amplification setting changed, and only allow settings that would not lead to such a case.

#### Unexpected Resource Overcommitment

Since the resource amplification only takes effect at scheduling level, unexpected resource overcommitment may happen if this feature is not used carefully, for example:

A Node with 4 allocatable physical CPUs and CPU amplification 2 would claim 8 allocatable CPUs, if there's two CPUShare Pods with the same CPU request and limit 4 on this Node, user would suppose that there would be no CPU competition (because their total limits equal to the Node's allocatable CPU), however, each Pod would be given a CFS quota to use all the 4 physical CPUs of the Node actually, so if both Pods use CPU more than 2, there would be a CPU competition.

To mitigate this risk, users should do amplification very carefully, and it is recommended to do amplification only on Nodes that are regularly idle. Another way is to utilize end-to-end resource-specific features such as CPU normalization which is covered in another individual proposal.
