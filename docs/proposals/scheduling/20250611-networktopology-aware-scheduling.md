# Network Topology Aware Scheduling

## Summary

In the training scenario of large language models, model parallelism requires extremely high network throughput for exchanging data, which makes the network a key bottleneck.

The business requires workloads to be scheduled to the optimal network topology domain with the highest throughput and lowest latency, in order to accelerate the exchange of network data for training. Taking Spine leaf architecture as an example, it is necessary to schedule the pods of PodGroup under the same leaf to meet the requirement of low latency data exchange between pods.

- The scheduler needs to be aware of the node network topology in the k8s cluster.
- The scheduler needs to schedule the PodGroup on a set of nodes to meet the optimal performance domain.

This proposal proposes a solution:
- The function of network topology affinity scheduling based on Spine leaf architecture.
- Support network topology affinity scheduling in preemption scenarios.


## Background

### Network Architecture

In a typical Kubernetes heterogeneous cluster, there are four types of networks for communication between machines and GPUs:

1. `Frontend Networking` is a regular Ethernet network used to connect machines to the Internet. This network is used for connections between Pods and Kubernetes, data loading, model checkpoints, etc.
2. `Backend Networking` is used to scale GPU-to-GPU communications across hundreds or thousands of racks. It typically uses high-performance network technologies such as NVIDIA's Infiniband or RoCE networks.
3. `Accelerator Interconnect` is an ultra-high-speed network used to connect multiple GPUs within a computing system, enabling them to communicate at extremely high bandwidth. For example, Nvidia's NVLink, AMD's Infinity Fabric/UALink, Google TPU's ICI, and Amazon Trainium 2's NeuronLink.
4. `Out of Band Management Networking` is a dedicated network used for system management that is separate from the main data transmission network. This network enables administrators to perform operating system re-imaging, monitor node health (such as fan speed, temperature, power consumption, etc.), and control IT equipment.

When we talk about network topology, most of the time we are talking about Backend Networking and Accelerator Interconnection that connect GPUs. NVIDIA provides a [topograph](https://github.com/NVIDIA/topograph/blob/main/docs/k8s.md) tool to label the nodes in the cluster with their positions in the cluster network topology:

- `accelerator`: Network interconnect for direct accelerator communication (e.g., Multi-node NVLink interconnect between NVIDIA GPUs)
- `block`: Rack-level switches connecting hosts in one or more racks as a block.
- `spine`: Spine-level switches connecting multiple blocks inside a datacenter.
- `datacenter`: Zonal switches connecting multiple datacenters inside an availability zone.

For example, if a node belongs to NVLink domain `nvl1` and connects to switch `s1`, which connects to switch `s2`, and then to switch `s3`, Topograph will apply the following labels to the node:

```yaml
network.topology.nvidia.com/accelerator: nvl1
network.topology.nvidia.com/block: s1
network.topology.nvidia.com/spine: s2
network.topology.nvidia.com/datacenter: s3
```

Network topology domains can be organized in a tree structure. For example, the following figure shows a typical spine leaf network architecture. 

![image](/docs/images/networktopo-1.png)

When GPUs on different nodes communicate with each other,

- The fewer hops there are during communication, the lower the communication delay.
- The more hops there are during communication, the higher the communication delay and the greater the likelihood of congestion on the core switch.

### Parallel Strategies

![image](/docs/images/networktopo-2-dp-and-pp.png)

The above figure shows a PytorchJob training task:

- Create a total of 12 Pods (PP * DP=4 * 3).
- Each Pod requests all 8 GPU cards on the node.

requirements:
- The communication between the 8 cards in one Pod is conducted through nvlink. No need for scheduler attention.
- The communication between pods requires RDMA high-speed network for communication, and the scheduler needs to schedule DP * PP pods into a set of high-performance communication domains.

PodGroup schedules according to the following strategy:
- If all allocatable nodes satisfy strategy 1, bound podgroup's pod to the nodes.
- If strategy 1 is not satisfy, try strategy 2, strategy 3, etc. and so on.

|            | Strategy detail                                                                                | Demo                                                                                                          |
|------------|------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| strategy-1 | All nodes of a task are under the same unit.                                                   | case1： all node of pod under unit0                                                                            |
| strategy-2 | Nodes within the same DP group are in the same unit, while different DP groups cross units     | case2：pod-2-0, pod-2-1 under unit0。   pod-2-2, pod-2-3 under unit1                                            |
| strategy-3 | All nodes of a task are under the same leaf                                                    | case3：all node of pod under leaf0                                                                             |
| strategy-4 | Nodes within the same DP group are in the same unit, while different DP groups cross leaves    | case4：  <br/>pod-4-0,pod-4-1,pod-4-2 under leaf0's unit1。  <br/> pod-4-3,pod-4-4,pod-4-5 under leaf1's unit3。 |
| strategy-5 | Nodes within the same DP group are under the same leaf, while different DP groups cross leaves | case5：  <br/>pod-5-0,pod-5-1,pod-5-2 under leaf0 。   <br/>pod-5-3,pod-5-4,pod-5-5 under leaf1                 |
| strategy-6 | All nodes of a task are under the same spine                                                   | case6: all node of pod under spine0                                                                           |

Demo：
![image](/docs/images/networktopo-3-strategy-demo.png)

## Motivation

From the above background information, we can see that the topological location of the node where the member Pods of the training task are located and the relative order between the member Pods will greatly affect the communication performance between the Pods. 

In Kubernetes, the scheduler is responsible for selecting nodes for the Pods. Thus we first check whether the existing scheduling capabilities of the Kubernetes scheduler or Koord-Scheduler can meet the topology requirements. 

 In the native Kubernetes scheduler, only `PodAffinity` can handle the scheduling semantics of affinity inter Pods according to topology. This capability, combined with the topology-related Labels provided by [topograph](https://github.com/NVIDIA/topograph/blob/main/docs/k8s.md) on the nodes, can enhance Pod distribution to a certain extent. However, since the Kubernetes scheduler can only schedule one Pod at a time, the effectiveness of `PodAffinity` depends largely on how the first Pod is selected.

Koord-Scheduler provides the scheduling semantics of `PodGroup`, so that a group of Pods in the same `PodGroup` can be dequeued and enter the scheduling cycle continuously, and only when all Pods requested resources are satisfied will they enter the Binding Cycle. Combining `PodGroup` with `PodAffiniy` can satisfy some requreid topology requirements to a certain extent after possible multiple rounds of `PodGroup` scheduling, but it is powerless for preferred topology requirements.

### Goals

Therefore, we need a new scheduling capability. The scheduler needs to select M (M=PodGroupMinNumber) nodes in the optimal performance domain from N idle nodes according to the network topology algorithm, and schedule the Pods in the PodGroup in a certain order.

- When N>M, that is, when resources are sufficient, according to the scheduling algorithm, the optimal scheduling strategy can be matched for PodGroup.
- When N<M, that is, when resources are insufficient, if preemption is possible, M-N nodes need to be preempted for scheduling.


## Proposal

### User stories
The network topology architecture of all nodes in the cluster is shown in the following figure, and the nodes are in an idle state.

![image](/docs/images/networktopo-4-user-story.png)
|         | Available nodes                        | Create Job                                                           | Scheduling results                                                                                                                                                                                                                                 |
|---------|----------------------------------------|----------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Story 1 | Node0~Node11， total 12 available nodes | podGroup.minNumber=4 (DP=2, PP=2)  <br/>priority_class = best-effort | pod-1-0:node0,  <br/>pod-1-1:node1,  <br/>pod-1-2:node2,  <br/>pod-1-3:node3                                                                                                                                                                       |
| Story 2 | Node4~Node11， total 8 available nodes  | podGroup.minNumber=4( DP=2, PP=2)  <br/>priority_class = best-effort | pod-2-0:node4,  <br/>pod-2-1:node5,  <br/>pod-2-2:node6,  <br/>pod-2-3:node7                                                                                                                                                                       |
| Story 3 | Node8~Node11， total 4 available nodes  | podGroup.minNumber=8( Dp=2, PP=4)  <br/>priority_class = Guarantee   | pod-2-0, pod-2-1, pod-2-2, pod-2-3 was eviction。  <br/><br/>Scheduling results：<br/>pod-3-0:node4,<br/>pod-3-1:node5,<br/>pod-3-2:node6,  <br/>pod-3-3:node7,  <br/>pod-3-4:node8,  <br/>pod-3-5:node9,  <br/>pod-3-6:node10,  <br/>pod-3-7:node11 |


### Implementation Details

#### Design Strategy
We need to implement a network topology aware plugin to achieve two functions
- Building the optimal topology: finding the best topology node based on available nodes
- Assign Node: Assign the optimal Node to the Pod in the PodGroup.


##### Scheme-1：PreScore+Score
**Scheme**
- PreScore stage: When the first Pod of a PodGroup is scheduled, calculate the optimal topology structure of the entire PodGroup.
- Score stage: Select the best Node for each Pod in the PodGroup and score it with BestScore.
  ![image](/docs/images/networktopo-5-framework.png)

**Problem**  
In a preemptive scenario, this scheme will fail (N=idle node, M=PodGroupMinNumber)
- Before preemption:
  - N idle nodes must be allocated to N Pods first, and the remaining M-N Pods will only experience preemption when they perceive insufficient resources during scheduling.
  - Topology algorithms can only calculate the optimal allocation method for the first N nodes, and the remaining M-N nodes cannot participate in the algorithm's calculations. Unable to achieve optimal network topology.
- After preemption,
  - The scheduler will directly schedule the pods to the corresponding nodes based on the Pod.spec.nominated field, and cannot participate in network topology calculations.

Implementing network topology preemption plugin cannot solve the problem：
- preemption are not atomic operations: When multiple pods simultaneously preempt the same node, it is unknown which pod ultimately obtains the node.
- After the preemption is completed, all pods are waiting in the Permit stage, and the network topology algorithm can no longer affect the scheduling results. Unless all pods in the entire PodGroup are rejected and rescheduled. When rescheduling, resources may be occupied by other pods.

**Core issues**
In the scenario of preemption, satisfy the optimal network topology.
- Implement group preemption: When resources are found to be insufficient for the PodGroup, all required resources are preempted once time.
- Resource reservation: After the preemption is completed, reserve the node resources for all pods in the PodGroup.


##### Scheme-2：Based on AfterPreFilter scheme
That is, the plan presented in this article.


#### Architecture Design
Network topology aware plugin implementation extension points:
- AfterPreFilter
- Filter
- PostFilter


##### AfterPreFilter
The AfterPreFilter extension point is responsible for building the network topology.
```
PodGroupAvailableNode = idleNodes + podGroup's.nominated
isPodGroupResourceSatisfies =  len(PodGroupAvailableNode) > podGroup.spec.minNumber
```
- When resources are sufficient for PodGroup:
  - Build network topology for PodGroup, find the best node for each Pod, and set pod.nominated=BestNode.
- When resources are insufficient, notify the Filter stage and return FitError. PostFilter executes preemption logic.
  - After the preemption is completed, it means that there are enough resources to execute the network topology construction logic.
  - Set pod.nominated=bestNode。


![image](/docs/images/networktopo-6-afterprefilter.png)


##### Filter
Returning insufficient resources, triggering preemption.


##### PostFilter
> The core logic of preemption is: how to reserve resources after preemption is completed. Otherwise, it may be occupied by other PodGroups.

Determine if preemption is possible: There are pods with lower priority, and after preemption, the resources can meet the needs of the Pod Group.
- Non preemptive: clear all pod.nominated fields of podGroup.
- Can preemptive:
  - Preemption: preempt all the resources that podGroup needed at once time. (PodGroupAvailableNode=6, podGroup.spec.minNumber=8, that's mean need preempt 2 nodes once time)
  - Assign bestNode to all pod.nominated fields of podGroup
  - Recalculate network topology: idle nodes+preempted nodes,
  - Assign bestNode to all pod.nominated fields of podGroup

![image](/docs/images/networktopo-7-postfilter.png)


#### Detailed Design

##### 1. Management of Network Topology
Describe the network topology through node labels and generate a configmap of the network topology within the cluster.

##### 2. Definition of the Core Structure of Network Topology
- HyperNode: is a performance domain consisting of a set of nodes or sub performance domains. Within a supernode, the network bandwidth and latency are the same. This custom resource (CRD) is used to describe the network topology in a Kubernetes cluster.
- Tier: It is a way to distinguish between different energy domains. Within the same level, bandwidth and latency are the same. The smaller the value of the hierarchy, the higher the bandwidth. For example, computing networks and storage networks can be at different levels, or there are several levels (spine, leaf) of switches in the computing network, each level can be identified as a level.
For example, in network architecture 1 (spine leaf), assuming 8 nodes are connected, as shown in the following figure (both single plane and multi plane can be supported):

![image](/docs/images/networktopo-8-spine-leaf.png)

The format of the Config Map is as follows:
```
[
 {
    "layer": 2,
    "name": "s2",
    "children": [
      "s0",
      "s1"
    ]
  },
  {
    "layer": 1,
    "name": "s0",
    "parents": [
      "s2"
    ],
    "children": [
      "node0",
      "node1"
    ]
  },
  {
    "layer": 1,
    "name": "s1",
    "parents": [
      "s2"
    ],
    "children": [
      "node2",
      "node3"
    ]
  }
]

```


##### 3. Creation and updating of network topology
Discovery and detection tools for network topology：
![image](/docs/images/networktopo-9-topo-gen.png)


##### 4. Network topology algorithm
```

// FindBestNode find N best node . （N = minNumberWorker）
// 1.all node in the same tor
// 2.all pipeline parallel node in the same tor, but all node in the same leaf
// 3.all node in the same leaf
// 4.all pipeline parallel node in the same tor, but all node in the same spine
// 4.all pipeline parallel node in the same leaf, but all node in the same spine
// 5.all node in the same spine
func (nt *NetWorkTopology) FindBestNode(minNumberWorker int, pipelineParallel int, hyperNodeTree map[string][][]string) ([]string, int) {
    nt.printHyperNodeTree(hyperNodeTree)
    // 1.all node in the same tor
    tieIndex := 0
    for _, hyperNodes := range hyperNodeTree[tieIndex] {
       if len(hyperNodes) >= minNumberWorker {
          return hyperNodes[:minNumberWorker], TorTierIndex
       }
    }
    // 2.all pipeline parallel node in the same tor, but all node in the same leaf
    resNode := []string{}
    dataParallel := minNumberWorker / pipelineParallel
    dpRemainCnt := dataParallel
    hasFoundAllNode := false
    for _, hyperNodes := range hyperNodeTree[indexKey] {
       eachTorCnt := pipelineParallel
       for beginIndex := 0; eachTorCnt <= len(hyperNodes); beginIndex += pipelineParallel {
          resNode = append(resNode, hyperNodes[beginIndex:beginIndex+pipelineParallel]...)
          eachTorCnt += pipelineParallel
          dpRemainCnt -= 1
          if dpRemainCnt == 0 {
             klog.V(3).Infof("get all pipeline parallel node in the same tor: %v", resNode)
             hasFoundAllNode = true
             break
          }
       }
       if hasFoundAllNode {
          break
       }
    }
    if hasFoundAllNode {
       leafIndexKey := TierKeyWord + strconv.Itoa(LeafTierIndex)
       for _, hyperNodes := range hyperNodeTree[leafIndexKey] {
          if resNodeInSameLeaf := nt.isSubHyperNode(hyperNodes, resNode); resNodeInSameLeaf {
             klog.V(3).Infof("get all pipeline parallel node in the same tor and all node in same leaf: %v", resNode)
             return resNode, TorTierIndex
          }
       }
    }

    // 3.all node in the same leaf
    indexKey = TierKeyWord + strconv.Itoa(LeafTierIndex)
    for _, hyperNodes := range hyperNodeTree[indexKey] {
       if len(hyperNodes) >= minNumberWorker {
          klog.V(3).Infof("get node in the same tor: %v", hyperNodes[:minNumberWorker])
          return hyperNodes[:minNumberWorker], TorTierIndex
       }
    }
    // 4.all pipeline parallel node in the same tor, but all node in the same spine
    if hasFoundAllNode {
       klog.V(3).Infof("get all pipeline parallel node in the same tor and all node in same spine: %v", resNode)
       return resNode, LeafTierIndex
    }
    // 5.all pipeline parallel node in the same leaf, but all node in the same spine
    resNode = []string{}
    dpRemainCnt = dataParallel
    for _, hyperNodes := range hyperNodeTree[indexKey] {
       eachTorCnt := pipelineParallel
       for beginIndex := 0; eachTorCnt <= len(hyperNodes); beginIndex += pipelineParallel {
          resNode = append(resNode, hyperNodes[beginIndex:beginIndex+pipelineParallel]...)
          eachTorCnt += pipelineParallel
          dpRemainCnt -= 1
          if dpRemainCnt == 0 {
             klog.V(3).Infof("get all pipeline parallel node in the same tor: %v", resNode)
             return resNode, TorTierIndex
          }
       }
    }
    // 6.all node in the same spine
    indexKey = TierKeyWord + strconv.Itoa(SpineTierIndex)
    for _, hyperNodes := range hyperNodeTree[indexKey] {
       if len(hyperNodes) >= minNumberWorker {
          klog.V(3).Infof("get node in the same tor: %v", hyperNodes[:minNumberWorker])
          return hyperNodes[:minNumberWorker], TorTierIndex
       }
    }
    return nil, -1
}

```

### Compatibility

## Unsolved Problems
In the actual testing process, it was found that all pods of PodGroup may add to unschedulable queue at the same time and wait for 5 minutes before trying again. 
During this period, resources may be occupied by other PodGroups.

## Alternatives