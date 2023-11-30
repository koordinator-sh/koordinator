---
title: Device Allocate Hint APIs
authors:
  - "@eahydra"
reviewers:
  - "@hormes"
  - "@FillZpp"
  - "@jasonliu747"
  - "@ZiMengSheng"
creation-date: 2023-08-03
last-updated: 2023-11-30
status: provisional
---

<!-- TOC -->

- [Device Allocate Hint API](#device-allocate-hint-api)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
  - [User stories](#user-stories)
    - [Story 1](#story-1)
    - [Story 2](#story-2)
  - [Proposal](#proposal)
    - [API Spec](#api-spec)
      - [Joint Allocation API](#joint-allocation-api)
      - [Device Allocation Hint API](#device-allocation-hint-api)
    - [Examples](#examples)
      - [Allocated GPU and NIC forced be on the same PCIe](#allocated-gpu-and-nic-forced-be-on-the-same-pcie)
      - [Allocated GPU and NIC forced be on the same PCIe and exclusive](#allocated-gpu-and-nic-forced-be-on-the-same-pcie-and-exclusive)
      - [Apply for all NICs on the machine](#apply-for-all-nics-on-the-machine)
      - [Allocate one VF from each of the two NICs](#allocate-one-vf-from-each-of-the-two-nics)
  - [Implementation History](#implementation-history)

<!-- /TOC -->

# Device Allocate Hint API

## Summary

This proposal introduces new APIs to define anticipated device allocation strategies. One API set supports joint allocation scenarios for devices like GPUs and NICs according to the hardware topology. The other set offers device allocation hints, detailing specific allocation rules to guide scheduler decisions.

## Motivation

Current mechanisms for RDMA device allocation are independent and may require differentiation between Physical Functions (PFs) and Virtual Functions (VFs) when SR-IOV is enabled. Users often need to allocate RDMA devices based on their proximity to GPUs to optimize communication in AI model training scenarios. This requirement presents challenges in Kubernetes environments that do not support Dynamic Resource Allocation (DRA).

To address these complex resource allocation scenarios, we propose the definition of new APIs that can guide the scheduler in making more informed allocation decisions.

### Goals

1. Implement new APIs to support joint allocation of GPU and RDMA devices, considering hardware system topology.
2. Introduce device allocation hints to provide detailed allocation rules, assisting the scheduler in optimizing resource usage based on specific workload requirements and hardware characteristics.

### Non-Goals/Future Work

1. None

## User stories

### Story 1

As a user, I want to allocate GPU and RDMA devices closely based on the hardware system topology.


### Story 2

As a user with SR-IOV enabled NIC devices, I want to request specific VFs.

## Proposal

### API Spec

#### Joint Allocation API

The joint-allocation logic is that, based on the configured device types, it prefers to ensure they are on the same PCIe; otherwise, it will try to allocate from the NUMA node dimension. If `requiredScope=SamePCIe` is not configured, and the resources under the NUMA node also cannot meet the demand, it will allocate from the entire machine dimension to ensure that the Pod definitely gets resources. If `requiredScope=SamePCIe` is configured and the configured device types are not on the same PCIe, the scheduling will fail.

Assume that there are 8 PCIe switches on a machine, and each PCIe has a GPU and NIC. After the scheduler recognizes the `scheduling.koordinator.sh/device-joint-allocate` annotation of the following Pod, it will allocate 4 GPUs and 4 NICs (although it is declared request rdma=1), and 4 GPUs and 4 NICs are distributed on the same PCIe as a group. 

For some heterogeneous scenarios, such as two NUMA Nodes on a machine, each NUMA Node has 4 PCIe, and then there is a GPU on each PCIe, but only one of the PCIe has a NIC. At this time, the scheduler will still allocate devices to the following Pods, but the difference is that only 4 GPUs and one NIC will be allocated. 

```yaml
kind: Pod
metadata:
  name: test-pod
  namespace: default
  annotations:
    scheduling.koordinator.sh/device-joint-allocate: |-
      {
        "deviceTypes": ["gpu","rdma"]
      }
spec:
  containers:
    - name: main
      resources:
        requests:
          nvidia.com/gpu: 4
          koordinator.sh/rdma: 1
```

The API will be annotated on the Pod as `scheduling.koordinator.sh/device-joint-allocate`. The corresponding structure is as follows:

```go
type DeviceJointAllocate struct {
	// DeviceTypes indicates that the specified types of devices are grouped and allocated according to topology.
	DeviceTypes []schedulingv1alpha1.DeviceType `json:"deviceTypes,omitempty"`
	// RequiredScope specifies the allocation scope required for the joint allocation of devices.
	// It defines the granularity at which devices should be joint-allocated, e.g. in the same PCIe.
	RequiredScope DeviceJointAllocateScope `json:"requiredScope,omitempty"`
}

type DeviceJointAllocateScope string

const (
	SamePCIeDeviceJointAllocateScope DeviceJointAllocateScope = "SamePCIe"
)
```

Field Descriptions:

- `DeviceJointAllocate`:
  - `deviceTypes`: An array of DeviceType indicating which devices should be allocated together based on the hardware topology.
  - `requiredScope`: Specifies the level at which devices should be grouped for allocation, such as on the same PCIe switch.

- `DeviceJointAllocateScope`: An enumerated type defining possible scopes for joint device allocation. Currently includes PCIe for PCIe-level allocation.

#### Device Allocation Hint API

`DeviceAllocateHint` guides the scheduler in selecting and allocating specialized hardware resources, such as network interface cards (NICs), that support Single Root Input/Output Virtualization (SR-IOV) mechanisms. With SR-IOV, a single NIC can present multiple virtual functions (VFs) that can be individually allocated to different workloads, often with specific usage constraints.

Different workloads have varying demands and may require VFs with particular characteristics. `DeviceAllocateHint` enables these workloads to express their needs, ensuring that the scheduler allocates the appropriate type of VF that aligns with the desired usage. It acts as a set of directives that help the scheduler understand the nuances of each requested device and make allocation decisions that satisfy the precise requirements of the Pod.

In scenarios where NICs do not use SR-IOV, or certain NICs are designated for special purposes, there may be constraints on which NICs can be allocated to specific workloads. `DeviceAllocateHint` becomes essential in such cases, enabling the description of allocation demands that comply with these special requirements. It ensures that specialized or constrained devices are reserved for and allocated to the workloads that need them, maintaining the integrity of the system's hardware resource usage.

The API will be annotated on the Pod as `scheduling.koordinator.sh/device-allocate-hint`. The corresponding structure is as follows:

```go
type DeviceAllocateHint map[schedulingv1alpha1.DeviceType]*DeviceHint

type DeviceHint struct {
	// Selector selects devices by label selector.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
	// VFSelector selects VFs by label selector.
	// If specified the VFSelector, scheduler will allocate VFs from PFs which satisfy VFSelector.
	VFSelector *metav1.LabelSelector `json:"vfSelector,omitempty"`
	// AllocateStrategy controls the allocation strategy
	AllocateStrategy DeviceAllocateStrategy `json:"allocateStrategy,omitempty"`
	// ExclusivePolicy indicates the exclusive policy.
	ExclusivePolicy DeviceExclusivePolicy `json:"exclusivePolicy,omitempty"`
}

type DeviceAllocateStrategy string

const (
	ApplyForAllDeviceAllocateStrategy DeviceAllocateStrategy = "ApplyForAll"
	RequestsAsCountAllocateStrategy   DeviceAllocateStrategy = "RequestsAsCount"
)

type DeviceExclusivePolicy string

const (
	// DeviceLevelDeviceExclusivePolicy represents mutual exclusion in the device instance dimension
	DeviceLevelDeviceExclusivePolicy DeviceExclusivePolicy = "DeviceLevel"
	// PCIExpressLevelDeviceExclusivePolicy represents mutual exclusion in the PCIe dimension
	PCIExpressLevelDeviceExclusivePolicy DeviceExclusivePolicy = "PCIeLevel"
)
```

Field Descriptions:

- `DeviceAllocateHint`: This map allows for specifying allocation hints for different types of devices.

- `DeviceHint`:
  - `selector`: A label selector to identify specific devices for allocation.
  - `vfSelector`: A label selector for VFs. When set, this instructs the scheduler to allocate VFs from PFs matching the selector.
  - `allocateStrategy`: Defines the approach for allocating devices.
  - `exclusivePolicy`: Details the exclusivity policy for device allocation, ensuring devices are not shared contrary to specified policies.

- `DeviceAllocateStrategy`: Enumerated type detailing different allocation strategies. Includes `ApplyForAll` which requests all matching devices, `RequestsAsCount` indicates that the corresponding resource request amount is used as the expected number of device instances.

- `DeviceExclusivePolicy`: Enumerated type indicating exclusivity levels for device allocation. Options include `DeviceLevel` for exclusivity at the device instsance level, `PCIeLevel` for exclusivity at the PCIe level.

### Examples

#### Allocated GPU and NIC forced be on the same PCIe

```yaml
kind: Pod
metadata:
  name: test-pod
  namespace: default
  annotations:
    scheduling.koordinator.sh/device-joint-allocate: |-
      {
        "deviceTypes": ["gpu","rdma"],
        "requiredScope": "SamePCIe"
      }
spec:
  containers:
    - name: main
      resources:
        requests:
          nvidia.com/gpu: 4
          koordinator.sh/rdma: 1
```

#### Allocated GPU and NIC forced be on the same PCIe and exclusive

```yaml
kind: Pod
metadata:
  name: test-pod
  namespace: default
  annotations:
    scheduling.koordinator.sh/device-joint-allocate: |-
      {
        "deviceTypes": ["gpu","rdma"],
        "requiredScope": "SamePCIe"
      }
    scheduling.koordinator.sh/device-allocate-hint: |-
      {
        "rdma": {
          "vfSelector": {},
          "exclusivePolicy": "PCIeLevel"
        }
      }
spec:
  containers:
    - name: main
      resources:
        requests:
          nvidia.com/gpu: 4
          koordinator.sh/rdma: 1
```

#### Apply for all NICs on the machine

```yaml
kind: Pod
metadata:
  name: test-pod
  namespace: default
  annotations:
    scheduling.koordinator.sh/device-allocate-hint: |-
      {
        "rdma": {
          "allocateStrategy": "ApplyForAll"
        }
      }
spec:
  containers:
    - name: main
      resources:
        requests:
          koordinator.sh/rdma: 100 # The request amount corresponding to each card is 100
```

#### Allocate one VF from each of the two NICs

For example, it is expected to apply for two VFs, and each VF belongs to two PFs respectively.

```yaml
kind: Pod
metadata:
  name: test-pod
  namespace: default
  annotations:
    scheduling.koordinator.sh/device-allocate-hint: |-
      {
        "rdma": {
          "vfSelector": {},
          "allocateStrategy": "RequestsAsCount"
        }
      }
spec:
  containers:
    - name: main
      resources:
        requests:
          koordinator.sh/rdma: 2 # requests as count, and allocates one VF from each of the two NICs
```

## Implementation History

- 2023-08-02: Initial proposal sent for review
- 2023-11-23: Updates the details of new APIs
- 2023-11-30: Clarify protocol semantics and add more examples