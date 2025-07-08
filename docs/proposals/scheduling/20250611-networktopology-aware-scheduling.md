# Network Topology Aware Scheduling

## Summary

In the training scenario of large language models, model parallelism requires extremely high network throughput for exchanging data, which makes the network a key bottleneck.

Therefore, workloads are required to be scheduled to the optimal network topology domain with the highest throughput and lowest latency, in order to accelerate the exchange of network data for training. Taking Spine leaf architecture as an example, it is necessary to schedule the pods of PodGroup under the same leaf to meet the requirement of low latency data exchange between pods.

In Kubernetes, while the native scheduler uses `PodAffinity` to address topology-based inter pod affinity scheduling, its effectiveness is limited due to single Pod scheduling at a time. Koord-Scheduler improves this with `PodGroup` semantics, allowing for collective scheduling of Pods once all resource demands are met. Despite enhancing topology handling, these schedulers fall short with preferred topology needs. Thus, a new capability is required that employs a network topology algorithm to optimally select and schedule multiple nodes for `PodGroups` with specific network topology requirements.

This proposal provides the above capabilities in Koordinator, so that

- When cluster resources are sufficient, pods with network topology scheduling requirements will try to schedule to a topology domain with better performance.
- When cluster resources are insufficient, scheduler will seize resources for PodGroup based on the network topology and record them in nominator.


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

There are three parallel strategies in the above figure:

- Tensor Parallel: The core idea is to decompose large matrices or tensors into multiple smaller parts and allocate these parts to different GPU devices for parallel computing.
This can significantly improve computational efficiency and shorten model training time, especially when facing large models with billions of parameters.

- Pipeline Parallel: The core idea is to divide the calculation process of the model into multiple stages, each stage being processed by one or more computing units (such as GPU or TPU). 
The data flows sequentially between these stages, with each stage processing different batches of data simultaneously. 
This process is similar to pipeline processing in traditional computers.

- Data Parallel: evenly distribute training data X (such as a batch) to different computing GPUs.


Different parallel communications have different characteristics:
- Tensor Parallel: Generally used for communication between multiple GPU cards on a single machine, with nvlink for communication.
- Pipeline Parallel: It involves operations such as FWD and BWD, with a data exchange rate of over GB/s, and communication efficiency directly determines the training speed of the model. The demand for network performance is extremely high.
- Data Parallel: Typically, data parallel requires the collection, accumulation, and updating of gradients between multiple Pipeline Parallels. The data volume is approximately MB in size, and the requirements for network performance are average.

Therefore, different communication methods have different communication bandwidth requirements. The communication between the gpu cards in one Pod is conducted through nvlink and there is no need for scheduler attention. When performing cross-machine communication, the communication bandwidth requirement is the network topology requirement. 

Now, we have the following two scheduling strategy for `PodGroup` with network topology requirements:

1. Try to schedule member Pods of the same `PodGroup` to a network topology domain with better performance
2. Pipeline parallism has a larger communication volume than data parallism. Therefore, try to schedule member Pods of the same PP group in the `PodGroup` to a network topology domain with better performance

Of the two strategies above, strategy 1 is preferred. Among the network topology domains with the best performance selected by strategy 1, the one that best satisfies strategy 2 is selected.

![image](/docs/images/networktopo-3-strategy-demo.png)

The above figure shows some typical cases. Let's describe how we select nodes when the PP and DP parameters are the same.

|            | Strategy detail                                                                                | Demo                                                                                                          |
|------------|------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| strategy-1 | All nodes of a task are under the same unit.                                                   | case1： all node of pod under unit0                                                                            |
| strategy-2 | Nodes within the same DP group are in the same unit, while different DP groups cross units     | case2：pod-2-0, pod-2-1 under unit0。   pod-2-2, pod-2-3 under unit1                                            |
| strategy-3 | All nodes of a task are under the same leaf                                                    | case3：all node of pod under leaf0                                                                             |
| strategy-4 | Nodes within the same DP group are in the same unit, while different DP groups cross leaves    | case4：  <br/>pod-4-0,pod-4-1,pod-4-2 under leaf0's unit1。  <br/> pod-4-3,pod-4-4,pod-4-5 under leaf1's unit3。 |
| strategy-5 | Nodes within the same DP group are under the same leaf, while different DP groups cross leaves | case5：  <br/>pod-5-0,pod-5-1,pod-5-2 under leaf0 。   <br/>pod-5-3,pod-5-4,pod-5-5 under leaf1                 |
| strategy-6 | All nodes of a task are under the same spine                                                   | case6: all node of pod under spine0                                                                           |




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

## Details

### Job level preemption algorithm

The job-level preemption algorithm can generally reuse the previous [Proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20240115-support-job-level-preemption.md), except that
1. PostFilter needs to be implemented in the Coscheduling plug-in to facilitate obtaining the Pod to which the Job belongs.
2. To determine whether the Pod can be successfully scheduled after the Victim is deleted, we need to execute PlanNodesForPodGroup to determine whether the node can meet both the network topology and resource requirements, rather than executing RunFilterWithNominatedNodes to determine whether the node can meet the resource requirements.
### Network topology gather algorithm 

The network topology gather algorithm is to find the best nodes for the M Pods, given the M member Pods belonging to a PodGroup, all the Nodes that can place the Pods, the network topology location of each node. Due to its complexity, we will use an example to describe the input, output, and overall process of the algorithm.

#### Network topology hierarchy

First of all, we have a cluster with 12 nodes, named Node0-Node11. The overall network topology of the cluster and the positions of these 12 nodes in the topology are as follows:

![image](/docs/images/networktopo-4-user-story.png)

#### Examples with three jobs 

After the cluster administrator configures `ClusterNetworkTopology` for the cluster, the scheduler will perceive it and build the above network topology in memory. Now we create three jobs in sequence according to the following table. Each member Pod of each job applies for the entire machine resources and prefer to be aggregated into a finer topology domain. 

| PodGroup  | MinMember | Parallelism | Preemptible Or Non-Preemptible |
| --------- | --------- | ----------- | ------------------------------ |
| PodGroup1 | 4         | DP=2,PP=2   | Preemptible                    |
| PodGroup2 | 4         | DP=2,PP=2   | Preemptible                    |
| PodGroup3 | 8         | DP=2,PP=4   | Non-Preemptible                |

Let's see how the scheduler arranges them. When PodGroup1 enter into scheduling,

1. The scheduler finds that Leaf0, Unit2, and Unit3 can all meet its requirements. In order to provide it with better performance, the scheduler tends to choose Unit2 or Unit3.
2. PodGroup1 is a 4-pod Job. It makes no difference whether it is placed in Unit2 or Unit3. The scheduler places it in Unit2.

When PodGroup2 enter into scheduling, the scheduler finds that both Unit3 and Leaf0 can meet its needs. In order to provide it with better performance, the scheduler tends to choose Unit3.

When PodGroup3 enter into scheduling, the scheduler finds that the cluster resources are insufficient and enters the preemption process. At this time, the scheduler finds that it has multiple choices:

1. Preempt PodGroup1 and schedule it to Node0-Node7

2. Preempt PodGroup2 and schedule it to Node0-Node3 and Node8-Node11

3. Preempt PodGroup1 and PodGroup2 and schedule them to Node4-Node11

Since 3 has higher performance, it will choose to preempt PodGroup1 and PodGroup2 and schedule them to Node4-Node11

#### Algorithm pseudocode

Now, let's formalize the above process. Firstly, let's give the scheduling requirements of PodGroup as follows

```go
type PodGroupRequirements struct {
	MinMember             int
	DataParallelism       int
	PipelineParallelism   int
	RequiredTopologyLayer TopologyLayer
}
```

The topological position represented by each parent node on the network topology tree is called a topology node. 

```go
// multi-layer network topology
// s3
//   |
//   |- s2-0
//        |- s1-0: {node-0, node-1}
//        |- s1-1: {node-2, node-3}
//   |- s2-1
//        |- s1-2: {node-4, node-5}
//        |- s1-3: {node-6, node-7, node-8, node-9}
```

Each topology node has a layer attribute and a part of nodes in the network topology tree. 

```go
// convert multi-layer network topology to layeredTopologyNodes
layeredTopologyNodes := map[string][][]string{
		"s3": {{"node-0", "node-1", "node-2", "node-3", "node-4", "node-5", "node-6", "node-7", "node-8", "node-9"}},
		"s2": {{"node-0", "node-1", "node-2", "node-3"}, {"node-4", "node-5", "node-6", "node-7", "node-8", "node-9"}},
		"s1": {{"node-0", "node-1}", "{node-2", "node-3}", "{node-4", "node-5}", "{node-6", "node-7", "node-8", "node-9"}},
}

// Sort the topological nodes of each layer according to the number of nodes they have
layeredTopologyNodes := map[string][][]string{
		"s3": {{"node-0", "node-1", "node-2", "node-3", "node-4", "node-5", "node-6", "node-7", "node-8", "node-9"}},       // len(s3)=10
		"s2": {{"node-0", "node-1", "node-2", "node-3"}, {"node-4", "node-5", "node-6", "node-7", "node-8", "node-9"}},     // len(s2-0)=4, len(s2-1)=6, 
		"s1": {{"node-0", "node-1"}, {"node-2", "node-3"}, {"node-4", "node-5"},{"node-6", "node-7", "node-8", "node-9"}}, // len(s1-2)=2, len(s1-1)=2,len(s1-0)=2,len(s1-3)=4, 
}
```

The essence of the network topology algorithm is to select the topology node with the lowest layer that can place all the member Pods of the Job.

```go
// Pseudocode
func (nt *NetWorkTopology) FindBestNode(requirements PodGroupRequirements, layeredTopologyNodes map[string][][]string) ([]string, int) {
	// 1. if all node in s1
	layerIndex := s1
	for _, nodesOfTopologyNode := range layeredTopologyNodes[s1] {
		if len(nodesOfTopologyNode) >= minNumberWorker {
			return nodesOfSomeTopologyNode[:minNumberWorker], layerIndex
		}
	}

	// 2.all pipeline parallel node in s1, but all node in s2
	hasFoundAllNode, resNode := findNode in topologyNodes in s1
	if hasFoundAllNode {
		if resNode isChildrenOf one s2 hyperNodeTree {
			return resNode, s2
		}else{
			// 3. get node in each s2 hyperNodeTree
			nodesOfTopologyNode := get node in each s2 topologyNode
			if len(nodesOfTopologyNode) > minNumberWorker {
				return resNode, LeafTierIndex
			}
		}

		// 4.all pipeline parallel node in the same tor, but all node in the same spine
		return resNode, SpineTierIndex
	}

	// 5.all pipeline parallel node in the same leaf, but all node in the same spine
	tieIndex = s2
	hasFoundAllNode, resNode, remainNodes := find node in s2 hyperNodeTree
	if hasFoundAllNode {
		return resNode, SpineTierIndex
	}else{
		// 6.all node in the same spine
		totalNode := append(resNode,remainNodes)
		if len(totalNode) > minNumberWorker {
			return totalNode, SpineTierIndex
		}
	}
}
```



```go
// detail
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
		hyperNodes := hyperNodeTree[indexKey][i]
		if len(hyperNodes) >= minNumberWorker {
			torHyperNodes := getHyperNodeTor(hyperNodes, hyperNodeTree[TorTierIndex])
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
	indexKey = s2
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

```go
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

