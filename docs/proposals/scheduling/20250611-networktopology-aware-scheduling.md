# Network Topology Aware Scheduling

## Summary

In the training scenario of large language models, model parallelism requires extremely high network throughput for exchanging data, which makes the network a key bottleneck.

Therefore, workloads are required to be scheduled to the optimal network topology domain with the highest throughput and lowest latency, in order to accelerate the exchange of network data for training. Taking spine-block architecture as an example, it is necessary to schedule the pods of `GangGroup` under the same block to meet the requirement of low latency data exchange between pods.

In Kubernetes, while the native scheduler uses `PodAffinity` to address topology-based inter pod affinity scheduling, its effectiveness is limited due to single Pod scheduling at a time. Koord-Scheduler improves this with `GangGroup` semantics, allowing for collective scheduling of Pods once all resource demands are met. Despite enhancing topology handling, these schedulers fall short with preferred topology needs. Thus, a new capability is required that employs a network topology algorithm to optimally select and schedule multiple nodes for `GangGroup` with specific network topology requirements.

This proposal provides the above capabilities in Koordinator, so that

- When cluster resources are sufficient, pods with network topology scheduling requirements will try to schedule to a topology domain with better performance.
- When cluster resources are insufficient, scheduler will seize resources for GangGroup based on the network topology and record them in nominator.


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

Network topology domains can be organized in a tree structure. For example, the following figure shows a typical spine block network architecture. 

![image](/docs/images/networktopo-1.png)

When GPUs on different nodes communicate with each other,

- The fewer hops there are during communication, the lower the communication delay.
- The more hops there are during communication, the higher the communication delay and the greater the likelihood of congestion on the core switch.

### Parallel Strategies

With the advent of large language models, the parameters of the model and the amount of data required for training have become extremely large. The previous training mode of running training on one GPU is no longer suitable for large language models. Therefore, in order to accelerate the training process of AI tasks, model developers split the amount of computation required to train the model into different parts and put them on different GPUs of the same machine or different machines to run in parallel. Typically, there are three parallel strategies for the training of large language models:

- Tensor Parallel: The core idea is to decompose large matrices or tensors into multiple smaller parts and allocate these parts to different GPU devices for parallel computing.
  This can significantly improve computational efficiency and shorten model training time, especially when facing large models with billions of parameters.
- Pipeline Parallel: The core idea is to divide the calculation process of the model into multiple stages, each stage being processed by one or more computing units (such as GPU or TPU). 
  The data flows sequentially between these stages, with each stage processing different batches of data simultaneously. 
  This process is similar to pipeline processing in traditional computers. 
- Data Parallel: evenly distribute training data X (such as a batch) to different computing GPUs.

In practice, these three parallel strategies are often mixed. For example, we can turn on three parallel strategy for a 12-machine training task at the same time, set the PP parallelism to 4 and the DP parallelism to 3, and the overall parallel model is shown in the following figure:

![image](/docs/images/networktopo-2-dp-and-pp.png)

TP happens inside the machine, and communication between GPUs is done through NVLink. DP and PP occur between different machines and involve the requirements for the network topology. PP involves operations such as FWD and BWD, with a data exchange rate of over GB/s.  DP requires the collection, accumulation, and updating of gradients between multiple pipelines and the data volume is approximately MB/s in size. Therefore, when the entire topology of the job is under the same switch, we prefer to let the machines in the same PP group be in the network topology with better performance. 

![image](/docs/images/pp_over_dp.jpg)

For example, in the network topology and parallel training job described in the diagram above, we prefer candidate0 than candidate 1 since candidate0 can make PP perform better.


## Motivation

From the above background information, it can be seen that the network topology location of the training task member Pods and the relative position between the member Pods after parallel grouping will greatly affect the communication performance between the Pods. 

In Kubernetes, the scheduler is responsible for selecting nodes for the Pods. Thus we first check whether the existing scheduling capabilities of the Kubernetes scheduler or Koord-Scheduler can meet the topology requirements. 

 In the native Kubernetes scheduler, only `PodAffinity` can handle the scheduling semantics of affinity inter Pods according to topology. This capability, combined with the topology-related Labels provided by [topograph](https://github.com/NVIDIA/topograph/blob/main/docs/k8s.md) on the nodes, can enhance Pod distribution to a certain extent. However, since the Kubernetes scheduler can only schedule one Pod at a time, the effectiveness of `PodAffinity` depends largely on how the first Pod is selected.

Koord-Scheduler provides the scheduling semantics of `GangGroup`, so that a group of Pods in the same `GangGroup` can be dequeued and enter the scheduling cycle continuously, and only when all Pods requested resources are satisfied will they enter the Binding Cycle. Combining `GangGroup` with `PodAffiniy` can satisfy some required topology requirements to a certain extent after possible multiple rounds of `GangGroup` scheduling, but it is powerless for preferred topology requirements.

### Goals

Therefore, we need a new scheduling capability. For `GangGroups` with network topology requirements or preferences, the scheduler needs to select nodes with the optimal performance domain according to the network topology algorithm, and schedule the Pods in the `GangGroup` in a certain order.

- When resources are sufficient, according to the scheduling algorithm, the optimal scheduling strategy can be matched for `GangGroup`.
- When resources are insufficient, if preemption is possible, some existing pods need to be preempted for scheduling according to network topology at the `GangGroup` level.

### Non-Goals/Future Work

#### Network topology requirements of dual-template inference service

In this proposal, we assume that the member Pods of `GangGroup` are homogeneous, which is applicable in training scenarios. However, in the inference service that adopts the P-D separation architecture, the resources requested by prefilling and decoding are often different. However, at this time, whether the communication bandwidth between prefilling and decoding will become a bottleneck, and whether we need to consider the network topology between prefilling and decoding, remains to be investigated.

If we really need to support dual-mode applications in the future, we need to do something about the network topology aggregation algorithm on this proposal, so the current proposal needs to support the configurable network topology algorithm.

#### Network topology support in elastic scaling

In the current proposal, we only support the scenario where all member Pods of the network topology are scheduled in an all-or-nothing manner. However, in the inference scenario, the number of service instances may scale dynamically as the request tree changes. If we want to support this scenario in the future, we need to provide a scoring plug-in that tries to make the Pod to be scheduled as close to the topological location of the existing Pods in the `GangGroup` as possible.

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

#### Submit topology-aware jobs 

Koordinator has provided the concept of `GangGroup` to abstract the all-or-nothing scheduling semantics of Jobs or Tasks. Users can declare a `GangGroup` and its `PodGroup` as follows, and mark which Pods belong to it through Pod Label.

```yaml
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: PodGroup
metadata:
  name: gang-master
  namespace: default
  annotations:
    gang.scheduling.koordinator.sh/groups: ["default/gang-master", "default/gang-worker"]
spec:
  scheduleTimeoutSeconds: 100
  minMember: 1
---
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: PodGroup
metadata:
  name: gang-worker
  namespace: default
  annotations:
    gang.scheduling.koordinator.sh/groups: ["default/gang-master", "default/gang-worker"]
spec:
  scheduleTimeoutSeconds: 100
  minMember: 2
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-master-0
  namespace: default
  labels:
    pod-group.scheduling.sigs.k8s.io: gang-master
spec:
  schedulerName: koord-scheduler
  ...
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-worker-0
  namespace: default
  labels:
    pod-group.scheduling.sigs.k8s.io: gang-worker
spec:
  schedulerName: koord-scheduler
  ...
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-worker-1
  namespace: default
  labels:
    pod-group.scheduling.sigs.k8s.io: gang-worker
spec:
  schedulerName: koord-scheduler
  ...
```

When users want to configure the network topology gather strategy for `GangGroup`, its `PodGroup` can be annotated as follows:

```yaml
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: PodGroup
metadata:
  name: gang-master
  namespace: default
  annotations:
    gang.scheduling.koordinator.sh/groups: ["default/gang-master", "default/gang-worker"]
    gang.scheduling.koordinator.sh/networkTopologyAware: |
      {
        "gatherStrategy": [
          {
            "layer": "spineLayer",
            "strategy": "PreferGather"
          },
          {
            "layer": "blockLayer",
            "strategy": "PreferGather"
          },
          {
            "layer": "acceleratorLayer",
            "strategy": "PreferGather"
          }
        ]
      }
spec:
  scheduleTimeoutSeconds: 100
  minMember: 1
---
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: PodGroup
metadata:
  name: gang-worker
  namespace: default
  annotations:
    gang.scheduling.koordinator.sh/groups: ["default/gang-master", "default/gang-worker"]
    gang.scheduling.koordinator.sh/networkTopologyAware: |
      {
        "gatherStrategy": [
          {
            "layer": "spineLayer",
            "strategy": "PreferGather"
          },
          {
            "layer": "blockLayer",
            "strategy": "PreferGather"
          },
          {
            "layer": "acceleratorLayer",
            "strategy": "PreferGather"
          }
        ]
      }
spec:
  scheduleTimeoutSeconds: 100
  minMember: 2
```

The above `GangGroup` indicates that the Pods belonging to it firstly try to be in an accelerator interconnection domain, and then try to be in a Block, and then try to be in a Spine network.

Sometimes, due to the strict demand for communication bandwidth, users may want to place all member Pods of a `GangGroup` under the same Spine. In this case, you can modify the  `PodGroup`as follows:

```yaml
```yaml

apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: PodGroup
metadata:
  name: gang-master
  namespace: default
  annotations:
    gang.scheduling.koordinator.sh/groups: ["default/gang-master", "default/gang-worker"]
    gang.scheduling.koordinator.sh/networkTopologyAware: |
      {
        "gatherStrategy": [
          {
            "layer": "spineLayer",
            "strategy": "MustGather"
          }
        ]
      }
spec:
  scheduleTimeoutSeconds: 100
  minMember: 1
---
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: PodGroup
metadata:
  name: gang-worker
  namespace: default
  annotations:
    gang.scheduling.koordinator.sh/groups: ["default/gang-master", "default/gang-worker"]
    gang.scheduling.koordinator.sh/networkTopologyAware: |
      {
        "gatherStrategy": [
          {
            "layer": "spineLayer",
            "strategy": "MustGather"
          }
        ]
      }
spec:
  scheduleTimeoutSeconds: 100
  minMember: 2
```

#### Submit parallel-aware jobs

In addition, in distributed training, users may need to set a index number for each member Pod of the Job. Generally speaking, member Pods with adjacent index numbers are grouped into a PP group. When PP is 1, communication primitives such as DP's AllReduce form a ring according to the index number of the member Pod. 

For example, if the user wants to create a `GangGroup` with four member Pods, and the DP parameter is equal to 2 and the PP parameter is also equal to 2, the Pod and PodGroup can be written as follows:

```yaml
apiVersion: scheduling.sigs.k8s.io/v1alpha1
kind: PodGroup
metadata:
  name: gang-example
  namespace: default
  annotations:
    gang.scheduling.koordinator.sh/groups: ["default/gang-example"]
    gang.scheduling.koordinator.sh/networkTopologyAware: |
      {
        "gatherStrategy": [
          {
            "layer": "spineLayer",
            "strategy": "PreferGather"
          },
          {
            "layer": "blockLayer",
            "strategy": "PreferGather"
          },
          {
            "layer": "acceleratorLayer",
            "strategy": "PreferGather"
          }
        ],
        "parallelModel": [
          {
            "parallelStrategy": "data-parallel",
            "parallelism": 2
          },
          {
            "parallelStrategy": "pipeline-parallel",
            "parallelism": 2
          }
        ]
      }
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
    pod-group.scheduling.koordinator.sh/index: 1
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
    pod-group.scheduling.koordinator.sh/index: 2
spec:
  schedulerName: koord-scheduler
  ...
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-example3
  namespace: default
  labels:
    pod-group.scheduling.sigs.k8s.io: gang-example
    pod-group.scheduling.koordinator.sh/index: 3
spec:
  schedulerName: koord-scheduler
  ...
---
apiVersion: v1
kind: Pod
metadata:
  name: pod-example4
  namespace: default
  labels:
    pod-group.scheduling.sigs.k8s.io: gang-example
    pod-group.scheduling.koordinator.sh/index: 4
spec:
  schedulerName: koord-scheduler
  ...
```

The above YAML indicates that pod-example1 and pod-example2 are in a PP group, pod-example3 and pod-example4 are in a PP group, and all four pods are in a DP group.

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
spec:
  preemptionPolicy: PreemptLowerPriority
  priorityClassName: high-priority
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
spec:
  preemptionPolicy: Never
  priorityClassName: low-priority
...
```

The above YAML indicates that when pod-example-1 fails to be scheduled due to insufficient resources, it can preempt lower-priority Pods on the machines it can see and it cannot be preempted by other Pods. However, pod-example-2 cannot initiate preemption when insufficient resources are scheduled, but it can be preempted by others.

### Workflow

Since this proposal involves concepts such as `ClusterNetworkTopology`, `PodGroup`, `GangGroup` and `ElasticQuota`, and the scheduling process involves multiple stages such as Pod dequeueing, scheduling, and preemption, we will sort out a workflow as a whole to illustrate how the logic of each block in the scheduler is connected in series.

Koord-Scheduler and Kube-Scheduler maintain the same framework, so the overall scheduling process is still Pod by Pod scheduling. Under the premise of unchanged framework, Koord-Scheduler supports `GangGroup` scheduling through the following highlights

1. Through the `Permit` mechanism, a member Pod of `GangGroup` will wait for the total number of all successfully scheduled member Pods to exceed `MinMember` before entering binding cycle together after being successfully scheduled.
2. The first Pod of `GangGroup` is sorted by `LastScheduleTime` and `Priority` in ActiveQ, and subsequent Pods follow the first Pod to be continuously dequeued through the `NextPod` mechanism of the `Coscheduling` plugin.

Therefore, to explain how to implement `network topology-aware scheduling` and `job-level preemption` based on `GangGroup` scheduling, we must explain Job scheduling in two steps. The first step is the scheduling of `FirstPod`, and the second step is the scheduling of `NextPod`.  The following figure describes the scheduling process of `FirstPod`:

![FirstPod](/docs/images/networktopology_firstpod.svg)

The scheduling process of `NextPod` is as follows:

![NextPod](/docs/images/networktopology_nextpod.svg)

## Details

### Job level preemption algorithm

The job-level preemption algorithm can generally reuse the previous [Proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20240115-support-job-level-preemption.md), except that
1. PostFilter needs to be implemented in the Coscheduling plug-in to facilitate obtaining the GangGroup to which the Pod belongs.
2. To determine whether the Pod can be successfully scheduled after the Victim is deleted, we need to execute PlanNodesForGangGroup to determine whether the node can meet both the network topology and resource requirements, rather than executing RunFilterWithNominatedNodes to determine whether the node can meet the resource requirements.
### Network topology gather algorithm 

The network topology gather algorithm is to find the best nodes for the M Pods, given the M member Pods belonging to a parallel-aware `GangGroup`, all the Nodes that can place the Pods, the network topology location of each node. The overall computation process can be described step by step as follows:

1. The member Pods of the `GangGroup` of the training task are generally homogeneous. We randomly select one from the member Pods as the representative Pod.

2. From the bottom to the top of the network topology hierarchy, recursively calculate the number of member Pods that each topology node can provide as `offerslots`. The `offerslots` that a Node can accommodate can be achieved by iteratively calling `NodeInfo.AddPod`, `fwk.RunPreFilterExtensionsAddPod`, and `fwk.RunFilterWithNominatedNode`.

3. Among all the topological nodes that can accommodate all the member Pods of the `GangGroup`, select those with the lowest level as our `candidate topological nodes`. 

   ![topology_offerslot_candidatenode](/docs/images/topology_offerslot_candidatenode.jpg)

4. From the bottom to the top of the network topology hierarchy, recursively calculate the `best pp group placement solution` for each topological node, and select the one with the lowest pp group level among the candidate topological nodes picked in 3 as the new candidate topological node. As shown in the figure below, we select spine-0 and spine-1 as the new candidate topological nodes.

   ![ppgroup_placement](/docs/images/ppgroup_placement.jpg)

5. Among the candidate topological nodes selected in 4, according to the `binpack` principle, the candidate topological nodes whose offerslot is closest to the offerslot required by the job are selected as our final topological node solution. As shown in the figure below, we select Node5-Node8 as the final scheduling result of the job.

   ![topology_final_placement](/docs/images/topology_final_placement.jpg)