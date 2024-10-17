---
title: GPU Partitioning APIs
authors:
- "@ZiMengSheng"
reviewers:
- "@hormes"
- "@songtao98"
- "@saintube"
creation-date: 2024-10-08
last-updated: 2024-10-08
status: provisional
---

# GPU Partitioning APIs

## Summary
This proposal outlines an enhancement to the GPU scheduling capabilities of Koordinator, particularly focusing on NVIDIA GPUs operating under SharedNVSwitch mode. The primary objective is to introduce functionality that allows Pods to specifically request GPU partitions based on predefined configurations (Partitions).

## Motivation
In virtualized environments, when NVIDIA FabricManager operates in SharedNVSwitch mode, for security reasons, NVIDIA imposes certain requirements on the GPU configurations that can be allocated to a single VM, allowing only a few specific combinations of GPUs. NVIDIA refers to a combination of GPUs as a Partition and a table consisting of several such Partitions as a Partition Table.

The scheduler in Koordinator is currently responsible for selection of GPUs for Pods. This PR expands upon the existing GPU scheduling capabilities of Koordinator, enabling it to recognize specific machine configurations and user requirements regarding GPU partitioning.

### Goals
- Provide the API for a Pod to request a specific GPU Partition.
- Allow nodes to offer permitted Partitions.

### Non-Goals/Future Work
- Describe what the Partition Table looks like for a specific GPU model.

## User Story

Typically, the rules for GPU partitioning are determined by the specific GPU model or system configuration, and may also be influenced by the configuration of GPUs on each individual node. The scheduler does not have insight into the specifics of the hardware models or GPU types; instead, it relies on components at the node level to report these Partition Rules to the Device Custom Resource (CR) as follows:

```yaml
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: Device
metadata:
  annotations:
    scheduling.koordinator.sh/gpu-partitions: |
      {
        "1": [
            "NVLINK": {
                {
                  # Which GPUs are included
                  "minors": [
                      0
                  ],
                  # GPU Interconnect Type
                  "gpuLinkType": "NVLink",
                  # Here we take the bottleneck bandwidth between GPUs in the Ring algorithm. BusBandwidth can be referenced from https://github.com/NVIDIA/nccl-tests/blob/master/doc/PERFORMANCE.md
                  "ringBusBandwidth": 400Gi
                  # Indicate the overall allocation quality for the node after the partition has been assigned away.
                  "allocationScore": "1",
                },
                ...
            }
            ...
        ],
        "2": [
            ...
        ],
        "4": [
            ...
        ],
        "8": [
            ...
        ]
      }
  labels:
    node.koordinator.sh/gpu-partition-policy: "Honor"
  name: node-1
```



Users can specify the desired GPU partition requirements at the Pod level.

```yaml
kind: Pod
metadata:
  name: hello-gpu
  annotations:
    scheduling.koordinator.sh/gpu-partition-spec: |
      {
        "allocatePolicy": "Restricted",
        "ringBusBandwidth": "200Gi"
      }
spec:
  containers:
    - name: main
      resources:
        limits:
          nvidia.com/gpu: 1
```

## Proposal

### GPUPartitionTable

Before we proceed, let's define the term "Partition" step by step. A Partition refers to a combination of GPUs that can be allocated to a user, possessing the following attributes:

```yaml
{
    # Which GPUs are included
    "minors": [
        0
    ],
    # GPU Interconnect Type
    "gpuLinkType": "NVLink",
    # GPU Interconnect Bandwidth
    "ringBusBandwidth": 400Gi
},
```

Additionally, selecting a Partition inherently means forgoing potentially better alternatives, implying that there is a trade-off among Partitions. By examining the Partition table, we can actually quantify the quality of the current allocation by calculating the maximum number of cards and the greatest bandwidth available in the remaining Partitions after assigning one. When no Partitions have been allocated yet, the sequential order of these goodness evaluations, which reflects the priority or desirability of each Partition, can be pre-established and attributed as a characteristic of the Partitions themselves. We refer to this attribute as the AllocationScore.

```yaml
{
    # Which GPUs are included
    "minors": [
        0
    ],
    # GPU Interconnect Type
    "gpuLinkType": "NVLink",
    # Ring All Reduce Bandwidth
    "ringBusBandwidth": 400Gi,
    # Indicate the overall allocation quality for the node after the partition has been assigned away.
    "allocationScore": 1,
},
```

Combining all possible combinations of partitions yields a partition table. The key here in a partition table is an integer that reflects how many GPU cards are included in this partition group.

```yaml
{
    "1": [
        {
          # Which GPUs are included
          "minors": [
              0
          ],
          # GPU Interconnect Type
          "gpuLinkType": "NVLink",
          # GPU Interconnect Bandwidth
          "ringAllReduceBandwidth": 400Gi,
          # Indicate the overall allocation quality for the node after the partition has been assigned away.
          "allocationScore": 1,
        },
        ...
    ],
    "2": [
        ...
    ],
    "4": [
        ...
    ],
    "8": [
        ...
    ]
}
```

Finally, when the AllocationScores of Partitions are equal, it implies that a allocation with the least fragmentation needs to be generated based on the current allocation situation. This calculation is to be performed during the actual allocation process within the scheduler.

The GPU PartitionTable structure is defined as follows:

```go
const(
    // AnnotationGPUPartitions represents the GPU partitions supported on the node 
    AnnotationGPUPartitions = SchedulingDomainPrefix + "/gpu-partitions"
)

type GPULinkType string

const (
    GPUNVLink  GPULinkType = "NVLink"
)

type GPUPartition struct {
    Minors           []int              `json:"minors"`
    GPULinkType      GPULinkType        `json:"gpuLinkType,omitempty"`
    RingBusBandwidth *resource.Quantity `json:"ringBusBandwidth,omitempty"`
    AllocationScore  int                `json:"allocationScore,omitempty"`
}

// GPUPartitionTable will be annotated on Device
type GPUPartitionTable map[int][]GPUPartition
```

### GPU Partition Policy

GPU Partition Policy indicates whether the partitions annotated to the Device CR need to be honored.

```go
const(
    LabelGPUPartitionPolicy string = NodeDomainPrefix + "/gpu-partition-policy"
)

type GPUPartitionPolicy string

const (
    // GPUPartitionPolicyHonor indicates that the partitions annotated to the Device CR should be honored.
    GPUPartitionPolicyHonor GPUPartitionPolicy = "Honor"
    // GPUPartitionPolicyPrefer indicates that the partitions annotated to the Device CR are preferred.
    GPUPartitionPolicyPrefer GPUPartitionPolicy = "Prefer"
)
```

### GPUPartitionSpec API

The GPUPartitionSpec structure is defined as follows:

```go
const(
    // AnnotationGPUPartitionSpec represents the GPU partition spec that pod requests
    AnnotationGPUPartitionSpec = SchedulingDomainPrefix + "/gpu-partition-spec"
)

type GPUPartitionSpec struct {
    AllocatePolicy   GPUPartitionAllocatePolicy `json:"allocatePolicy,omitempty"`
    RingBusBandwidth *resource.Quantity         `json:"ringBusBandwidth,omitempty"`
}

type GPUPartitionAllocatePolicy string

const (
    // GPUPartitionAllocatePolicyRestricted indicates that only partitions with the most allocationScore will be considered.
    GPUPartitionAllocatePolicyRestricted GPUPartitionAllocatePolicy = "Restricted"
    // GPUPartitionAllocatePolicyBestEffort indicates that try best to pursue partition with more allocationScore.
    GPUPartitionAllocatePolicyBestEffort GPUPartitionAllocatePolicy = "BestEffort"
)
```

