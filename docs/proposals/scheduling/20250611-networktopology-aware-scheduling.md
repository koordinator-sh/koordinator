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
![image](/docs/images/networktopo-4-cluster.png)

After the cluster administrator configures `ClusterNetworkTopology` for the cluster, the scheduler will perceive it and build the above network topology in memory.
Now we create three jobs in sequence according to the following table. Each member Pod of each job applies for the entire machine resources and prefer to be aggregated into a finer topology domain. 

#### Examples with jobs 

Demo1, One Job. we create a job with 4 Pods (DP=2, PP=2). The following figure and table shows the details how the scheduler arranges them in different nodes are unavailable.

![image](/docs/images/networktopo-3-demo1.png)

|            | Strategy detail                                      | Scheduler Result                                                               |
|------------|------------------------------------------------------|--------------------------------------------------------------------------------|
| case-1     | All dp in one block.                                 | all node is available, node8~node11 is best node in one block                  |
| case-2 | Each dp in same block.     All dp in same spine.     | node0~node7 is available, node0,node1,node3,node4 is best node                 |
| case-3 | Each dp not in same block. All dp in same spine.     | node0~node3, node5~node7 is available,node0,node1,node2,node3 is best node     |
| case-4 | Each dp in same block .    All dp in same datacenter | node0,node3~node7 is available,node3,node4,node5,node6 is best node            |
| case-5 | Each dp in same spine.     All dp in same datacenter | node0,node3~node5 is available,node3,node4,node5,node8 is best node            |
| case-6 | All pod in same datacenter         | node0,node4,node5,node8 is available, only node0,node4,node5,node8 can be used |


Demo2, multiple Job with Preempt. 
we create 4 job following blow table shows the details how the scheduler arranges them.

| Job  | PodGroup  | MinMember | Parallelism | Preemptible Or Non-Preemptible |
|------|-----------|-----------|-------------| ------------------------------ |
| job1 | PodGroup1 | 2         | DP=1,PP=2   | Preemptible                    |
| job2 | PodGroup2 | 2         | DP=1,PP=2   | Preemptible                    |
| job3 | PodGroup3 | 8         | DP=4,PP=2   | Preemptible                |
| job4 | PodGroup4 | 4         | DP=2,PP=2   | Non-Preemptible                               |

Let's see how the scheduler arranges them. When PodGroup1 enter into scheduling,

1. PodGroup1 enter into scheduling: The scheduler finds all node meet its requirements. 
- In order to provide it with better performance, the scheduler tends to choose block0,block1,block2,block3
- In order to reduce network block fragments, the scheduler tends to choose a minimum satisfiability block, is block2.
2. PodGroup2 enter into scheduling: same as PodGroup1, the scheduler tends to choose block0.
3. PodGroup3 enter into scheduling: 
- pod0,pod1,pod2,pod3,pod4,pod5,pod6 will be scheduled to a block3 and block2 in spine-1.
- pod7 only can be scheduled to block0 in spine-0.
4. PodGroup4 enter into scheduling:
- This is no resource in cluster. But PodGroup4 is non-preemptible with high Priority, so it will be preempt PodGroup3 and schedule them to Node8-Node11.

![image](/docs/images/networktopo-preempt.png)