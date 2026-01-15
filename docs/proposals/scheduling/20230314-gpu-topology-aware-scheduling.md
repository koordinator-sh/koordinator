---
title: GPU topology-aware scheduling
authors:
  - "@happy2048"
reviewers:
  - "@eahydra"
  - "@hormes"
  - "@yihuifeng"
  - "@honpey"
  - "@zwzhang0107"
  - "@jasonliu747"
creation-date: 2023-03-14

---


# GPU topology-aware scheduling

## Table of Contents
<!--ts-->

* [GPU topology-aware scheduling](#gpu-topology-aware-scheduling)
   * [Table of Contents](#table-of-contents)
   * [Summary](#summary)
   * [Motivation](#motivation)
      * [Goals](#goals)
      * [Non-Goals/Future Work](#non-goalsfuture-work)
   * [Proposal](#proposal)
      * [User stories](#user-stories)
         * [Story 1](#story-1)
         * [Story 2](#story-2)
      * [Implementation Details](#implementation-details)
         * [main steps](#main-steps)
         * [How to identify pods are in the same group](#how-to-identify-pods-are-in-the-same-group)
         * [GPU topology resource reporting](#gpu-topology-resource-reporting)
         * [Node Selection](#node-selection)
         * [Pick GPUs of Node](#pick-gpus-of-node)
         * [Record the allocation information to the pod annotation](#record-the-allocation-information-to-the-pod-annotation)
         * [Container environment variable assignment](#container-environment-variable-assignment)
         * [Works with the Gang plugin](#works-with-the-gang-plugin)
   * [Unsolved Problems](#unsolved-problems)

<!--te-->


## Summary
In Distributed Deep Learning Job, each worker for the training job may involve data exchange and other operations. The bandwidth between GPU cards will affect the training time of the training job. Although the k8s native scheduler can allocate GPU cards to the workers of the training job, bandwidth between GPU cards is not considered; this proposal will provide a scheduling plugin to consider the bandwidth between a group of GPU cards when allocating a group of GPU cards to pods.
## Motivation
NVIDIA Collective Communication Library (NCCL) is a Magnum IO library provided by NVIDIA, which can realize GPU-accelerated collective operations. NCCL is topology-aware (automatically perceives the connection type between GPU cards, no manual configuration is required) and is optimized to pass PCIe, NVLink, Ethernet, and InfiniBand interconnects enable high bandwidth and low latency. In the deep learning distributed training job, the distributed training framework (Pytorch, MPI) combined with the NCCL library can achieve the acceleration effect. The NCCL library can perceive the connection between the GPU cards. Different connection types have different bandwidths. The size of the bandwidth affects the training time of the training job.

The following is a matrix describing the bandwidth between 8 GPU cards on a node, and the unit of value is GB/s:
```
Bandwidth Matrix:
       gpu_0   gpu_1   gpu_2   gpu_3   gpu_4   gpu_5   gpu_6   gpu_7
gpu_0  750.48  48.39   48.33   96.41   15.77   15.52   96.40   15.74
gpu_1  48.39   753.38  96.46   48.38   4.64    16.93   16.98   96.39
gpu_2  48.38   96.25   751.92  96.48   48.39   17.57   17.59   16.72
gpu_3  96.25   48.39   96.43   750.48  15.45   48.39   15.88   14.62
gpu_4  5.00    16.81   48.38   15.98   755.56  96.39   48.38   96.44
gpu_5  15.80   16.93   17.50   48.39   96.25   751.92  96.23   48.38
gpu_6  96.42   16.75   17.47   15.89   48.35   96.28   754.10  48.33
gpu_7  15.65   96.20   16.77   15.71   96.25   48.38   48.33   754.83
```
If a distributed training job has 2 Pods, and each Pod requests 2 GPU cards, then the [gpu0, gpu1, gpu2, gpu3] combination should be selected first rather than the [gpu0, gpu1, gpu2, gpu5] combination, because the former The bottleneck bandwidth is 48.33 (bottleneck bandwidth refers to the minimum bandwidth of any two GPU connections in a group of GPU cards), while the bottleneck bandwidth of the latter is 4.64. If the latter is allocated to the training job, it will greatly affect the training time.
### Goals

1. A scheduling plugin is provided, which considers the bandwidth between GPU cards when allocating GPU cards for pods and preferentially selects the GPU cards combination with large bottleneck bandwidth.
2. The scheduling plugin supports allocating GPU cards to individual Pods and groups of Pods.
3. Topology-aware scheduling tries to select the optimal combination of GPU cards currently available on the node for the training job rather than a mandatory behavior; that is to say, the GPU group allocated for the training job may also be the worst combination.
4. If a node cannot place all the pods of the training job, it will try to place these pods with the fewest nodes to avoid node resource fragmentation.
### Non-Goals/Future Work

1. In this proposal, it is assumed that a training job can tolerate some pods running on the node first while the remaining pods are pending. If the training job cannot tolerate this situation, the GPU topology plugin needs to be used in conjunction with the gang plugin to implement All Or Nothing scheduling; that is, this solution does not implement the All Or Nothing scheduling logic.

## Proposal
### User stories
#### Story 1
**Single Pod requests GPU cards:** There is only one pod for the training job, and the number of GPU cards requested by the pod exceeds 1. At the same time, the training job uses the NCCL library for communication between GPU cards.  The communication bandwidth between GPU cards needs to be considered when allocating GPU cards to pods.
#### Story 2
**Multiple Pods request GPU cards:** The distributed training job has multiple workers (or multiple pods), the underlying communication framework of the workers uses the NCCL library, and there is data communication between GPU cards. If a node can run these workers, then these workers should be run on a node first to reduce the communication delay between GPUs. If one node cannot run these workers, consider multiple nodes to run these workers; when each node selects GPUs for the workers, which should be run on the node, communication bandwidth between GPU cards should be considered, and GPU combination with the largest bottleneck bandwidth is preferred.

In this scenario, the following situation may occur: some workers(or pods) of the training job are running, while the remaining pods are pending due to untimely scheduling for some reasons. If the training job can tolerate this situation, no special handling is required; If the training job cannot tolerate this situation, the running pods occupy resources and waste resources. To avoid the situation, it is necessary to ensure All Or Nothing resource scheduling. In this case, gang scheduling is required.

### Implementation Details
#### main steps
the main steps are described as:

- When pod1 starts to be scheduled, the GPU topology plugin uses two specific pod labels(will be introduced later) to find pods that have not been scheduled in the same group (including pod1 itself) in preFilter extension, for example, [pod1, pod2, pod3].
- At the preScore extension, get the list of nodes that are currently able to run pod1, for example, [node1, node2, node3]. Select a node group from [node1, node2, nod3], the node group can place [pod1, pod2, pod3], and each node is responsible for its pod combination (for example: [pod1, pod2, pod3] can be run on [node1, node2] and node1 is responsible for running [pod1, pod2], node2 is responsible for running pod3), the node needs to provide a group of GPUs which the bottleneck bandwidth of the GPU group is the largest among all combinations. Update the given pre-allocated scheme (including the pre-allocated GPUs for pod1, pod2, and pod3) to the cache of the GPUTopology plugin to pre-occupy node GPU resources. At this point, from other GPU Topology Groups, this group of pods has been allocated GPUs.
- At the score extension, if the current node to be scored is the same as the node pre-allocated by the preScore extension point for pod1, then give the current node 100 points, otherwise 0 points.
-  At the Reserve extension, update the GPU information allocated for pod1 to pod1's annotation and  koordlet mounts the GPU device for the pod1 by the GPU information.
- At the Bind extension, the bind operation is performed on pod1, and pod1 is scheduled.
- When pod2 or pod3 is scheduling, the GPUs pre-allocated for them are directly obtained from the cache of the GPUTopology plugin and the allocated GPU information is updated to their annotations, and the binding operation is performed.
- If pod2 or pod3 finds that the pre-allocation scheme is invalid during scheduling (for example: when pod2 is scheduling, the node list in preScore extension does not contain the node recommended by the pre-allocation scheme, and the pre-allocation scheme is considered invalid), then the GPUToplogy plugin needs to re-select a node for the current pod. At this time:
   - If other pods in the same group have already been scheduled, it is impossible to find an optimal GPU combination for the entire group. In this case, the GPUToplogy plugin only needs to find a suitable node for the current pod.
   - If all pods in the same group have not been scheduled yet, it is necessary to find an optimal GPU combination for the pods of the entire group again.

![image.png](/docs/images/gpu-topology-aware-scheduling.png)
#### How to identify pods are in the same group
If the scheduler needs to select a better GPU combination for a group of pods. How to confirm which pods belong to the same group? The solution is that Pods belong to the same group if they have the same label key and values as below:
```
    gputopology.scheduling.koordinator.sh/name: "xxxx"
    gputopology.scheduling.koordinator.sh/replica: "xxxx" 
```
The value of gputopology.scheduling.koordinator.sh/replica in the pod labels must be consistent with the number of copies of the job.
#### GPU topology resource reporting
Report the GPU topology resources of each node through the following CRD:
```
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: Device
```
In order to meet the requirements of reporting GPU topology resources, a field Topologies needs to be added to DeviceSpec:
```
type DeviceTopologyInfo struct {
	Name     string `json:"name"`
	Topology string `json:"topology"`
}

type DeviceSpec struct {
  // add a field to report gpu topology information
	Topologies map[DeviceType][]DeviceTopologyInfo `json:"topologies,omitempty"`
  // device information
	Devices    []DeviceInfo                        `json:"devices,omitempty"`
}
```
Each node will only report GPU bandwidth topology information, the following is an example:
```
Bandwidth Matrix:
       gpu_0   gpu_1   gpu_2   gpu_3   gpu_4   gpu_5   gpu_6   gpu_7
gpu_0  750.48  48.39   48.33   96.41   15.77   15.52   96.40   15.74
gpu_1  48.39   753.38  96.46   48.38   4.64    16.93   16.98   96.39
gpu_2  48.38   96.25   751.92  96.48   48.39   17.57   17.59   16.72
gpu_3  96.25   48.39   96.43   750.48  15.45   48.39   15.88   14.62
gpu_4  5.00    16.81   48.38   15.98   755.56  96.39   48.38   96.44
gpu_5  15.80   16.93   17.50   48.39   96.25   751.92  96.23   48.38
gpu_6  96.42   16.75   17.47   15.89   48.35   96.28   754.10  48.33
gpu_7  15.65   96.20   16.77   15.71   96.25   48.38   48.33   754.83
```
And the CR example is described as below:
```
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: Device
metadata:
  name: host04
spec:
  # report the gpu topology information
  topologies:
    gpu: 
    - name: bandwidth
      topology: '[[750.48,48.39,48.39,96.46,15.97,16.15,96.41,16.18],[48.39,752.65,96.46,48.38,16.96,16.84,16.67,96.23],[48.38,96.25,749.04,6.02,48.38,17.57,17.54,16.95],[96.44,48.39,96.48,752.65,17.27,48.38,17.33,16.8],[15.99,16.8,48.38,17.27,755.56,96.44,48.38,96.44],[16.14,16.74,17.73,48.38,96.23,755.56,96.25,48.38],[96.43,16.81,17.6,17.35,48.33,96.23,754.83,48.39],[16.28,96.22,17.18,16.88,96.23,48.33,48.33,755.56]]'
  devices:
  - health: true
    id: GPU-04cea5cd-966f-7116-1d58-1ac34421541b
    minor: 0
    resources:
      kubernetes.io/gpu-core: "100"
      kubernetes.io/gpu-memory: 16Gi
      kubernetes.io/gpu-memory-ratio: "100"
    type: gpu
  - health: true
    id: GPU-3680858f-1753-371e-3c1a-
    minor: 1
    resources:
      kubernetes.io/gpu-core: "100"
      kubernetes.io/gpu-memory: 16Gi
      kubernetes.io/gpu-memory-ratio: "100"
    type: gpu
  - health: true
    id: GPU-95fe7a8b-bf9b-73cc-2903-c6e65883f3a7
    minor: 2
    resources:
      kubernetes.io/gpu-core: "100"
      kubernetes.io/gpu-memory: 16Gi
      kubernetes.io/gpu-memory-ratio: "100"
    type: gpu
  - health: true
    id: GPU-cd8d5d8c-7334-4c68-587e-04202daa38a5
    minor: 3
    resources:
      kubernetes.io/gpu-core: "100"
      kubernetes.io/gpu-memory: 16Gi
      kubernetes.io/gpu-memory-ratio: "100"
    type: gpu
  - health: true
    id: GPU-511dd579-5044-b716-e08a-841f51796a59
    minor: 4
    resources:
      kubernetes.io/gpu-core: "100"
      kubernetes.io/gpu-memory: 16Gi
      kubernetes.io/gpu-memory-ratio: "100"
    type: gpu
  - health: true
    id: GPU-62460a09-6838-abc8-00f5-31d2c6c101ef
    minor: 5
    resources:
      kubernetes.io/gpu-core: "100"
      kubernetes.io/gpu-memory: 16Gi
      kubernetes.io/gpu-memory-ratio: "100"
    type: gpu
  - health: true
    id: GPU-2da27328-f395-a226-7486-c08e6e98570f
    minor: 6
    resources:
      kubernetes.io/gpu-core: "100"
      kubernetes.io/gpu-memory: 16Gi
      kubernetes.io/gpu-memory-ratio: "100"
    type: gpu
  - health: true
    id: GPU-8137b226-b69a-1f22-4367-da110c8ba6b5
    minor: 7
    resources:
      kubernetes.io/gpu-core: "100"
      kubernetes.io/gpu-memory: 16Gi
      kubernetes.io/gpu-memory-ratio: "100"
    type: gpu
```
#### Node Selection
Suppose a training job has a total of 3 pods waiting to be scheduled, namely pod1, pod2, and pod3. When pod1 is scheduled, the list of available nodes filtered by the filter extension point is [node1, node2, node3]. The logic for selecting available node groups for [pod1, pod2, pod3] is as follows:

- First try to use one node to place [pod1, pod2, pod3]. The conditions for a node to place these three pods are as follows:
   - The number of GPUs available on the candidate node is greater than or equal to the sum of the number of GPUs requested by [pod1, pod2, pod3].
   - Call the RunFilterPlugins function provided by the scheduler on the candidate node and each pod to run all filter extension points to determine whether the pod can run on the node. If all pods can run on the node, then the candidate node can place the set of pods:
```
// nodeInfo is the current node info
satisfied := true 
for _,pod := range []*v1.Pod{pod1,pod2,pod3} {
	status := RunFilterPlugins(context.TODO(), state, pod, nodeInfo)
	if status.Merge().IsSuccess() {
			nodeInfo.AddPod(p)
	}else {
    satisfied := false
  	break
  }
}
```

- If one node cannot place [pod1, pod2, pod2], then try to place these three pods with 2 nodes. After allocating GPUs to the pods, the combination with less remaining GPU resources on the node is preferred
- If two nodes cannot place these pods, then try to place these pods on 3 nodes until the number of nodes tried equals the number of pods.
#### Pick GPUs of Node
After selecting a set of nodes for the pods, the next step is to select a set of GPUs with the largest bottleneck bandwidth from the nodes and allocate them to the pods.
#### Record the allocation information to the pod annotation
After the GPUs are allocated to the pod, the allocation result needs to be recorded in the pod annotation, and the allocation result will be used by koordlet. The allocation result is described as follows:
```
type ContainerIndex string 

type GPUIndex string 

type Allocation struct {
   // allocatedGPUs represents the GPU index number that can be used by the current pod
   AllocatedGPUs map[ContainerIndex][]GPUIndex `json:"allocatedGPUs"`
   // visibleGPUs represents the GPUs provided by the node on which the current pod is running for the pods of entire group
   VisibleGPUs []GPUIndex                      `json:"visibleGPUs"`
}
```
and the example annotation is:
```
annotations:
  topology.scheduling.koordinator.sh/gpu: '{"allocatedGPUs":{"0":["4","5"]},"visibleGPUs":["4","5","6","7"]}'
```
#### Container environment variable assignment
The following logic needs to be implemented in koordlet：

- The GPU information allocated to the container is parsed from the pod annotation, and the value of visibleGPUs is assigned to the environment variable NVIDIA_VISIBLE_DEVICES, which represents the GPUs that the NCCL library can discover.
- Assign the GPUs allocated for this container in the allocatedGPUs field to the environment variable CUDA_VISIBLE_DEVICES, which represents the GPUs that can be used by the current container.
#### Works with the Gang plugin
This plugin can be combined with the gang plugin to achieve consistent scheduling for pods of the training job. If a training job needs gang scheduling, the pods of the training job need to add annotations:
```
    gang.scheduling.koordinator.sh/name: "xxxx"
    gang.scheduling.koordinator.sh/min-available: "xxx" 
```
User needs to make sure that:

- values of label gputopology.scheduling.koordinator.sh/name  and annotation gang.scheduling.koordinator.sh/name are consistent
- values of label gputopology.scheduling.koordinator.sh/replica and annotation  gang.scheduling.koordinator.sh/min-available are consistent
## Unsolved Problems
