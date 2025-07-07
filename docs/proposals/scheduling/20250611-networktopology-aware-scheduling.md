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

There are three parallel strategies in the above figure

- Tensor Parallel: The core idea is to decompose large matrices or tensors into multiple smaller parts and allocate these parts to different GPU devices for parallel computing.
This can significantly improve computational efficiency and shorten model training time, especially when facing large models with billions of parameters.

- Pipeline Parallel: The core idea is to divide the calculation process of the model into multiple stages, each stage being processed by one or more computing units (such as GPU or TPU). 
The data flows sequentially between these stages, with each stage processing different batches of data simultaneously. 
This process is similar to pipeline processing in traditional computers.

- Data Parallel: evenly distribute training data X (such as a batch) to different computing GPUs.


communication features
- Tensor Parallel: Generally used for communication between multiple GPU cards on a single machine, with nvlink for communication.
- Pipeline Parallel: It involves operations such as FWD and BWD, with a data exchange rate of over GB/s, and communication efficiency directly determines the training speed of the model. The demand for network performance is extremely high.
- Data Parallel: Typically, data parallel requires the collection, accumulation, and updating of gradients between multiple Pipeline Parallels. The data volume is approximately MB in size, and the requirements for network performance are average.


requirements:
- The communication between the 8 cards in one Pod is conducted through nvlink. No need for scheduler attention.
- Firstly, consider: The communication between pods in one Pipeline Parallel. such as node-1,node-2,node-3,node-4 requires RDMA high-speed network for communication, and the scheduler needs to schedule those pods into a set of high-performance communication domains as far as possible.
- Secondly, consider: The communication between pods in different Data Parallel. the scheduler needs to schedule those pods into a set of high-performance communication domains

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

Therefore, we need a new scheduling capability. For `PodGroups` with network topology requirements or preferences, the scheduler needs to select M (M=PodGroupMinNumber) nodes in the optimal performance domain from N idle nodes according to the network topology algorithm, and schedule the Pods in the PodGroup in a certain order.

- When N>M, that is, when resources are sufficient, according to the scheduling algorithm, the optimal scheduling strategy can be matched for PodGroup.
- When N<M, that is, when resources are insufficient, if preemption is possible, M-N nodes need to be preempted for scheduling according to network topology.

## Proposal

### User stories

#### Cluster configuration

After deploying [topograph](https://github.com/NVIDIA/topograph/blob/main/docs/k8s.md) and other similar tools, you can see the network topology location of the node through the node Labels.

```yaml
apiVersion: v1
kind: node
metadata:
  name: node-0
  labels:
    network.topology.nvidia.com/accelerator: nvl1
    network.topology.nvidia.com/block: s1
    network.topology.nvidia.com/spine: s2
    network.topology.nvidia.com/datacenter: s3
```

Then, the cluster administrator needs to configure a CR to tell Koord-Scheduler how to build topological relationships based on `NodeLabel`. Taking the topological modeling of [topograph](https://github.com/NVIDIA/topograph/blob/main/docs/k8s.md) as an example, the `ClusterNetworkTopology` hierarchy can be configured as follows:

```yaml
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: ClusterNetworkTopology
metadata:
  name: default
spec:
  hierarchy:
  - layerName: nodeLayer
    parentTopologyLayer: acceleratorLayer
  - layerName: acceleratorLayer
    labelKey: network.topology.nvidia.com/accelerator
    parentTopologyLayer: blockLayer
  - layerName: blockLayer
    labelKey: network.topology.nvidia.com/block
    parentTopologyLayer: spineLayer
  - layerName: spineLayer
    labelKey: network.topology.nvidia.com/spine
    parentTopologyLayer: datacenterLayer
  - layerName: datacenterLayer
    labelKey: network.topology.nvidia.com/datacenter
```

#### Submit jobs

Koordinator has provided the concept of `PodGroup` to abstract the all-or-nothing scheduling semantics of Jobs or Tasks. Users can declare a `PodGroup` as follows and mark which Pods belong to it through Pod Label.

```yaml
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: PodGroup
metadata:
  name: gang-example
  namespace: default
spec:
  scheduleTimeoutSeconds: 100
  minMember: 2
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-example1
  namespace: default
  labels:
    pod-group.scheduling.sigs.k8s.io: gang-example
spec:
  schedulerName: koord-scheduler
  ...
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-example2
  namespace: default
  labels:
    pod-group.scheduling.sigs.k8s.io: gang-example
spec:
  schedulerName: koord-scheduler
  ...
```

When users want to configure the network topology aggregation strategy for `PodGroup`, `PodGroup` can be modified as follows:

```yaml
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: PodGroup
metadata:
  name: gang-example
  namespace: default
spec:
  minMember: 2
  networkTopologyAware:
    gatherStrategy:
    - layer: spineLayer
      strategy: MustGather
    - layer: blockLayer
      strategy: PreferGather
    - layer: acceleratorLayer
      strategy: PreferGather
    communicationMode: AllRingDuce
```

The above `PodGroup` indicates that the Pods belonging to it must be in a Spine network, and then try to be in an accelerator interconnection domain, and then try to be in a Block.

#### Preemptible or Non-Preemptible

GPU resources are very limited, but the number of tasks that want to apply for GPUs is often unlimited. Koordinator has provided `ElasticQuota` to allow cluster resource administrators to set GPU resource quotas for different organizations and users based on the GPU resources they have purchased, and to organize such quotas through a hierarchical structure so that different organizations and users can use GPUs as flexibly and fairly as possible.

Each `ElasticQuota` declares its "`min`" and "`max`". The semantics of "min" is the ElasticQuota's non-preemptible resources. If `ElasticQuota`'s "`request`" is less than or equal to "`min`", the ElasticQuota can obtain equivalent resources to the "`request`". The semantics of "`max`" is the ElasticQuota's upper limit of resources. We require "`min`" should be less than or equal to "`max`". An example of an ElasticQuota is as follows:

```yaml
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: ElasticQuota
metadata:
  name: quota-example
  namespace: default
  labels:
    quota.scheduling.koordinator.sh/parent: ""
    quota.scheduling.koordinator.sh/is-parent: "false"
spec:
  max:
    nvidia.com/gpu: 40
  min:
    nvidia.com/gpu: 16
```

There are two kind of pods: `non-preemptible` and `preemptible`. `Non-preemptible` pod will be limited by quota's min. `preemptible` pod will be limited by quota's max. If job is preemptible, please fill job's pods with label `quota.scheduling.koordinator.sh/preemptible=true`, if job is non-preemptible, please fill job's pods with label `quota.scheduling.koordinator.sh/preemptible=false`.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: pod-example-1
  namespace: default
  labels:
    quota.scheduling.koordinator.sh/name: "quota-example"
    quota.scheduling.koordinator.sh/preemptible: false
    quota.scheduling.koordinator.sh/can-preempible: true
spec:
...
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-example-2
  namespace: default
  labels:
    quota.scheduling.koordinator.sh/name: "quota-example"
    quota.scheduling.koordinator.sh/preemptible: true
    quota.scheduling.koordinator.sh/can-preempible: false
spec:
...
```

The above YAML indicates that when pod-example-1 fails to be scheduled due to insufficient resources, it can preempt other Pods on the machines it can see and it cannot be preempted by other Pods. However, pod-example-2 cannot initiate preemption when insufficient resources are scheduled, but it can be preempted by others.

### Workflow

Since this proposal involves concepts such as `ClusterNetworkTopology`, `PodGroup`, and `ElasticQuota`, and the scheduling process involves multiple stages such as Pod dequeueing, scheduling, and preemption, we will sort out a workflow as a whole to illustrate how the logic of each block in the scheduler is connected in series.

Koord-Scheduler and Kube-Scheduler maintain the same framework, so the overall scheduling process is still Pod by Pod scheduling. Under the premise of unchanged framework, Koord-Scheduler supports `PodGroup` scheduling through the following highlights

1. Through the `Permit` mechanism, a member Pod of `PodGroup` will wait for the total number of all successfully scheduled member Pods to exceed `MinMember` before entering binding cycle together after being successfully scheduled.
2. The first Pod of `PodGroup` is sorted by `LastScheduleTime` and `Priority` in ActiveQ, and subsequent Pods follow the first Pod to be continuously dequeued through the `NextPod` mechanism of the `Coscheduling` plugin.

Therefore, to explain how to implement `network topology-aware scheduling` and `job-level preemption` based on `PodGroup` scheduling, we must explain Job scheduling in two steps. The first step is the scheduling of `FirstPod`, and the second step is the scheduling of `NextPod`.  The following figure describes the scheduling process of `FirstPod`:

![FirstPod](/docs/images/networktopology_firstpod.svg)

The scheduling process of `NextPod` is as follows:

![NextPod](/docs/images/networktopology_nextpod.svg)

## Design Details

### Job level preemption algorithm

The job-level preemption algorithm can generally reuse the previous [Proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20240115-support-job-level-preemption.md), except that
1. PostFilter needs to be implemented in the Coscheduling plug-in to facilitate obtaining the Pod to which the Job belongs.
2. To determine whether the Pod can be successfully scheduled after the Victim is deleted, you need to execute PlanNodesForPodGroup to determine whether the node can meet both the network topology and resource requirements, rather than executing RunFilterWithNominatedNodes to determine whether the node can meet the resource requirements.
### Network topology gather algorithm 

The Network topology gather algorithm is to find the best nodes for the M Pods, given the M member Pods belonging to a PodGroup, all the Nodes that can place the Pods, the network topology location of each node, and the overall topology hierarchy. Due to its complexity, we will describe the output, output, and final calculation process of Step by Step.

#### 0. ClusterNetwork Topology Hierarchy

The network topology architecture of all nodes in the cluster is shown in the following figure, and the nodes are in an idle state.

![image](/docs/images/networktopo-4-user-story.png)

|         | Available nodes                         | Create Job                                                   | Scheduling results                                           |
| ------- | --------------------------------------- | ------------------------------------------------------------ | ------------------------------------------------------------ |
| Story 1 | Node0~Node11， total 12 available nodes | podGroup.minNumber=4 (DP=2, PP=2)  <br/>priority_class = best-effort | pod-1-0:node0,  <br/>pod-1-1:node1,  <br/>pod-1-2:node2,  <br/>pod-1-3:node3 |
| Story 2 | Node4~Node11， total 8 available nodes  | podGroup.minNumber=4( DP=2, PP=2)  <br/>priority_class = best-effort | pod-2-0:node4,  <br/>pod-2-1:node5,  <br/>pod-2-2:node6,  <br/>pod-2-3:node7 |
| Story 3 | Node8~Node11， total 4 available nodes  | podGroup.minNumber=8( Dp=2, PP=4)  <br/>priority_class = Guarantee | pod-2-0, pod-2-1, pod-2-2, pod-2-3 was eviction。  <br/><br/>Scheduling results：<br/>pod-3-0:node4,<br/>pod-3-1:node5,<br/>pod-3-2:node6,  <br/>pod-3-3:node7,  <br/>pod-3-4:node8,  <br/>pod-3-5:node9,  <br/>pod-3-6:node10,  <br/>pod-3-7:node11 |

#### 1. Management of Network Topology
Describe the network topology through node labels and generate a configmap of the network topology within the cluster.

#### 2. Definition of the Core Structure of Network Topology
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


#### 3. Creation and updating of network topology
Discovery and detection tools for network topology：
![image](/docs/images/networktopo-9-topo-gen.png)


#### 4. Network topology algorithm

4.1 convert multi-layer network topology to hyperNodeTree and sort the hyperNodeTree
```
// multi-layer network topology
// s3
//   |
//   |- s2-0
//        |- s1-0: {node-0, node-1}
//        |- s1-1: {node-2, node-3}
//   |- s2-1
//        |- s1-2: {node-4, node-5}
//        |- s1-3: {node-6, node-7, node-8, node-9}


// convert multi-layer network topology to hyperNodeTree
hyperNodeTree := map[string][][]string{
		"s3": {{"node-0", "node-1", "node-2", "node-3", "node-4", "node-5", "node-6", "node-7", "node-8", "node-9"}},
		"s2": {{"node-0", "node-1", "node-2", "node-3"}, {"node-4", "node-5", "node-6", "node-7", "node-8", "node-9"}},
		"s1": {{"node-0", "node-1}", "{node-2", "node-3}", "{node-4", "node-5}", "{node-6", "node-7", "node-8", "node-9"}},
}

// sort earch layer in hyperNodeTree. 
hyperNodeTree := map[string][][]string{
		"s3": {{"node-0", "node-1", "node-2", "node-3", "node-4", "node-5", "node-6", "node-7", "node-8", "node-9"}},       // len(s3)=10
		"s2": {{"node-0", "node-1", "node-2", "node-3"}, {"node-4", "node-5", "node-6", "node-7", "node-8", "node-9"}},     // len(s2-0)=4, len(s2-1)=6, 
		"s1": {{"node-0", "node-1"}, {"node-2", "node-3"}, {"node-4", "node-5"},{"node-6", "node-7", "node-8", "node-9"}}, // len(s1-2)=2, len(s1-1)=2,len(s1-0)=2,len(s1-3)=4, 
}
```


4.2 FindBestNode in sort hyperNodeTree
```
// defaultPipelineParallel := 1
func (nt *NetWorkTopology) FindBestNode(requirements PodGrouprequirements, hyperNodeTree map[string][][]string) ([]string, int) {
    minNumberWorker := requirements.MinMember
    pipelineParallel := requirements.PipelineParallel
    if minNumberWorker%pipelineParallel != 0 {
        pipelineParallel = defaultPipelineParallel
    }
    dataParallel := minNumberWorker / pipelineParallel

    // 1. if all node in the same tor
    tieIndex := s1
    for _, hyperNodes := range hyperNodeTree[tieIndex] {
       if len(hyperNodes) >= minNumberWorker {
          return hyperNodes[:minNumberWorker], tieIndex
       }
    }

	// 2.all pipeline parallel node in the same tor, but all node in the same leaf
    hasFoundAllNode, resNode := nt.ppSameHyperNode(hyperNodeTree[s1], tieIndex, minNumberWorker, pipelineParallel)
    if hasFoundAllNode {
		for _, hyperNodes := range hyperNodeTree[s2] {
			if ok := isSubHyperNode(hyperNodes, resNode); ok {
				return resNode, LeafTierIndex
			}else{
               
               return resNode, LeafTierIndex
            }
		}
        return resNode, SpineTierIndex
    }

    // 3.all node in the same leaf:  try to find a set of nodes in the same leaf that meets the minimum number of workers required
    indexKey = s2
	for i := range hyperNodeTree[indexKey] {
		hyperNodes := hyperNodeTree[indexKey][len(hyperNodeTree[indexKey])-i-1]
		if len(hyperNodes) >= minNumberWorker {
			torHyperNodes := getHyperNodeTor(hyperNodes, hyperNodeTree[TierKeyWord+strconv.Itoa(TorTierIndex)])
			_, resNode, remainNodes := nt.ppSameHyperNode(torHyperNodes, dataParallel, pipelineParallel)
			for _, nodes := range remainNodes {
				resNode = append(resNode, nodes...)
			}
			return resNode[:minNumberWorker], LeafTierIndex
		}
	}

	// 4.all pipeline parallel node in the same tor, but all node in the same spine
	if hasFoundAllNode {
		return resNode, SpineTierIndex
	}

	// 5.all pipeline parallel node in the same leaf, but all node in the same spine
	hasFoundAllNode, resNode, remainNodes := nt.ppSameHyperNode(hyperNodeTree[indexKey], dataParallel, pipelineParallel)
	if hasFoundAllNode {
		return resNode, SpineTierIndex
	}

	// 6.all node in the same spine
	indexKey = TierKeyWord + strconv.Itoa(SpineTierIndex)
	for _, hyperNodes := range hyperNodeTree[indexKey] {
		if len(hyperNodes) >= minNumberWorker {
			klog.V(3).Infof("get node in the same spine: %v", hyperNodes[:minNumberWorker])
			// only have one spine, all nodes in this spine, so resNode and remainNodes are included in spine
			for _, nodes := range remainNodes {
				resNode = append(resNode, nodes...)
			}
			return resNode[:minNumberWorker], SpineTierIndex
		}
	}
	return nil, -1
}
```

```
func (nt *NetWorkTopology) ppSameHyperNode(hyperNodeList [][]string, dpRemainCnt int, pipelineParallel int) (bool, []string, [][]string) {
	var resNode []string
	var remainNodes [][]string
	hasFoundAllNode := false
	for _, hyperNodes := range hyperNodeList {
		eachTorCnt := pipelineParallel
		beginIndex := 0
		for ; eachTorCnt <= len(hyperNodes); beginIndex += pipelineParallel {
			resNode = append(resNode, hyperNodes[beginIndex:beginIndex+pipelineParallel]...)
			eachTorCnt += pipelineParallel
			dpRemainCnt -= 1
			if dpRemainCnt == 0 {
				hasFoundAllNode = true
				break
			}
		}
		if beginIndex < len(hyperNodes) {
			remainNodes = append(remainNodes, hyperNodes[beginIndex:])
		}
		if hasFoundAllNode {
			break
		}
	}
	sort.Slice(remainNodes, func(i, j int) bool {
		return len(remainNodes[i]) > len(remainNodes[j])
	})
	return hasFoundAllNode, resNode, remainNodes
}
```

