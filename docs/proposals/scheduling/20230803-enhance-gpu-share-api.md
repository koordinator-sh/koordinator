---
title: Enhance the GPU Share API
authors:
  - "@eahydra"
reviewers:
  - "@hormes"
  - "@jasonliu747"
  - "@FillZpp"
  - "@ZiMengSheng"
creation-date: 2023-08-03
last-updated: 2023-08-07
status: provisional
---

<!-- TOC -->

- [Enhance the GPU Share API](#enhance-the-gpu-share-api)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [User stories](#user-stories)
        - [Story 1](#story-1)
        - [Story 2](#story-2)
    - [Proposal](#proposal)
        - [GPU Resource APIs](#gpu-resource-apis)
            - [GPU Vendor APIs](#gpu-vendor-apis)
            - [Koordinator GPU Share APIs](#koordinator-gpu-share-apis)
        - [Implementation](#implementation)
            - [NodeResource controller](#noderesource-controller)
            - [Mutating Webhook](#mutating-webhook)
            - [Validating Webhook](#validating-webhook)
            - [koord-scheduler](#koord-scheduler)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

# Enhance the GPU Share API

## Summary

This proposal clarifies the usage and constraints of the GPU Share resource API, and supplements a new resource API for GPU Share scenarios to clarify the semantics of GPU Share resources.

## Motivation

The existing proposal [Fine-grained Device Scheduling](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20220629-fine-grained-device-scheduling.md) defines a set of resource APIs that support the use of GPU device resources in two ways: exclusive and shared. The APIs provided are so flexible that users are confused when using it. For example, users can use two resource names `koordinator.sh/gpu-memory` and `koordinator.sh/gpu-memory-ratio` to represent GPU memory resources, especially the latter, new users may not understand the corresponding scenarios. In addition, these APIs are not very friendly to Device Plugin and NRI/CDI. For example, we have no way to declare koordinator.sh/gpu-memory through the Device Plugin mechanism(Because koordidnator defines the unit of GPU Memory as bytes, if the extended resource is reported through the device plugin, it needs to be converted to a Device object perceived by kubelet, which means that each byte is an object, and the memory overhead is huge).

### Goals

1. Clarify and restrict the usage of Device Resource APIs.
2. Add a new API to clearly express the semantics of GPU Share.

### Non-Goals/Future Work

1. None

## User stories

### Story 1

Users use vendor APIs such as nvidia.com/gpu to apply for GPU devices.

### Story 2

Users create a batch of Pods to share GPU devices.

- Users apply for exclusive use of a part of GPU memory, but share the use of GPU core.
- Users apply for exclusive use of a part of GPU core, but share the use of GPU memory.
- Users apply for exclusive use of a part of GPU memory and core.
- Users do not explicitly declare the GPU resources to be used, but expects to share and use all GPU devices.

## Proposal

### GPU Resource APIs

The existing GPU Resource APIs are clearly divided into two types: vendor APIs and Koordinator GPU Share APIs.

#### GPU Vendor APIs

Koordinator supports vendor agreements in strict accordance with the vendor's technical specifications.

| Vendor | Resources   | Resource Name  | Comment                                       |
| ------ | ----------- | -------------- | --------------------------------------------- |
| Nvidia | GPU Devices | nvidia.com/gpu | The unit is the number of GPU device objects. |
| Hygon  | GPU Devices | dcu.com/gpu    | The unit is the number of GPU device objects. |

#### Koordinator GPU Share APIs

| Resources           | Resource Name                   | **Required** | Comment                                                                                                                                                                                                                                    |
| ------------------- | ------------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| GPU Memory          | koordinator.sh/gpu-memory       | **Yes**      | The unit is the number of bytes.                     Recommended Use.                                                                                                                                                                                      |
| Ratio of GPU Memory | koordinator.sh/gpu-memory-ratio | **Yes**      | It represents the percentage of the GPU's memory. Currently still maintained and supported, but not recommended to use at first.                                                                                                           |
| GPU Core            | koordinator.sh/gpu-core         | No           | It represents the computing capacity of the GPU. Similar to K8s MilliCPU, we abstract the total computing power of GPU into one hundred, and users can apply for the corresponding amount of GPU computing power according to their needs. |
| GPU Shared          | koordinator.sh/gpu.shared       | **Yes**      | It indicates that several GPU device objects are expected to be shared. At least 1 must be set. |

The following principles should be followed when using:

- **MUST** declare the GPU Memory resource names(koordinator.sh/gpu-memory or koordinator.sh/gpu-memory-ratio), and choose one of them according to the scenario since GPU Memory resources are very precious and are closely related to reliability.
- **MUST** declare the `koordinator.sh/gpu.shared` to indicate that it is currently running in GPU Share mode. At least 1 must be set.
- **NOT ALLOWED** to use `koordinator.sh/gpu-memory` and `koordinator.sh/gpu-memory-ratio` **at the same time**.
- **NOT ALLOWED** to declare only `koordinator.sh/gpu-core`. **MUST** be used with GPU Memory Resources.

It is no longer recommended to use **koordinator.sh/gpu**, but it is still maintained and supported. Later, the resource name will be converted to GPU Memory Ratio and GPU Core through webhook.

About the difference between `koordinator.sh/gpu-memory` and `koordinator.sh/gpu-memory-ratio`:

- Use `koordinator.sh/gpu-memory` if you need to specify the memory size. **Recommended** Use.
- If a Pod applies for GPU Memory through `koordinator.sh/gpu-memory-ratio`, the scheduler will calculate the actual number of bytes of GPU memory according to the total amount of GPU memory on the node and the declared ratio during scheduling. For example, if the memory size of a GPU device object on a node is 8Gi, and the koordinator.sh/gpu-memory-ratio is 50, then the number of GPU Memory bytes to apply is 4Gi = 8Gi \* (50 /100).


About of `koordinator.sh/gpu.shared`:

- `koordinator.sh/gpu.shared` is a secondary-purpose resource.
- It defines the number of Pods that a GPU device object can support in shared mode.
- It indicates that several GPU device objects are expected to be shared. At least 1 must be set.
- The resources allocated to each device object are the total resource requests of the Pod divided by the number of devices. For example, requests koordinator.sh/gpu-memory=4Gi, koordinator.sh/gpu-core=100, koordinator.sh/gpu.shared=2, then it means to use two GPU devices, and each device object allocates koordinator.sh/gpu-memory= 2Gi, koordinator.sh/gpu-core=50.
- The total amount of koordinator.sh/gpu.shared is not equal to the number of GPU devices, but a virtual value. Based on the upper limit of the number of shared Pods that a GPU device object can support, by default, each GPU device object can support 100 shared Pods (although it will not run so many). Suppose a node has 8 GPU devices, then koordinator.sh/gpu.shared=800 recorded in `node.status.allocatable` and `node.status.capacity`.

### Implementation


#### NodeResource controller

The NodeResource controller in koord-manager currently updates the device resource information to Node.Status.Allocatable/Capacity based on the Device CRD object, so this implementation can be extended to report koordinator.sh/gpu.shared to Node.Status.Allocatable/ Capacity

#### Mutating Webhook 

Added a Pod Webhook plugin to mutate Pods using the GPU Share API. In the Pod Create phase, if the Pod uses the GPU Share API but does not declare `koordinator.sh/gpu.shared`, the webhook will inject `koordinator.sh/gpu.shared`. The webhook will calculate the value of `koordinator.sh/gpu.shared` according to the following rules:
1. If `koordinator.sh/gpu-memory-ratio` is an integer multiple of 100, then the value of `koordinator.sh/gpu.shared` is equal to `koordinator.sh/gpu-memory-ratio` divided by 100.
2. If `koordinator.sh/gpu-memory-ratio` is not an integer multiple of 100, then the value of `koordinator.sh/gpu.shared` is equal to 1, that is, only one GPU device object is applied.

If only use vendor API, such as `nvidia.com/gpu`, the webhook will not append `koordinator.sh/gpu.shared`, but the scheduler will convert to `koordinator.sh/gpu.shared` through the normalization mechanism when scheduling.

In addition, webhook will process `koordinator.sh/gpu` in advance, and convert `koordinator.sh/gpu` to `koordinator.sh/gpu-core` and `koordinator.sh/gpu-memory-ratio`.

#### Validating Webhook

Added a new webhook plugin for validating the GPU Resource API. The verification rules are as follows:

1. Reject Pods that declare the vendor API and the GPU Share API at the same time.
2. Reject Pods that declare both `koordinator.sh/gpu-memory` and `koordinator.sh/gpu-memory-ratio`.
3. Reject Pods that declare `koordinator.sh/gpu-core` but not `koordinator.sh/gpu-memory` or `koordinator.sh/gpu-memory-ratio`.
4. Reject Pods that declare `koordinator.sh/gpu-memory` or `koordinator.sh/gpu-memory-ratio` but not `koordinator.sh/gpu.shared`.

#### koord-scheduler

The DeviceShare plugin in the scheduler needs to divide the resource requests equally according to the number of devices specified by `koordinator.sh/gpu.shared`, and allocate corresponding GPU devices.

## Implementation History

- 2023-08-02: Initial proposal sent for review
- 2023-08-07: Added implementation section
