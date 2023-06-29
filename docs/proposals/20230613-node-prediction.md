---
title: Node Prediction
authors:
  - "@saintube"
reviewers:
  - "@zwzhang0107"
  - "@hormes"
  - "@eahydra"
  - "@FillZpp"
  - "@jasonliu747"
  - "@ZiMengSheng"
creation-date: 2023-06-13
last-updated: 2023-06-27
---
# Node Prediction

## Table of Contents

<!--ts-->
* [Node Prediction](#node-prediction)
   * [Table of Contents](#table-of-contents)
   * [Summary](#summary)
   * [Motivation](#motivation)
      * [Goals](#goals)
      * [Non-Goals/Future Work](#non-goalsfuture-work)
   * [User Stories](#user-stories)
      * [Story 1](#story-1)
      * [Story 2](#story-2)
   * [Design](#design)
      * [Design Principles](#design-principles)
      * [Architecture](#architecture)
         * [Workflow](#workflow)
         * [Scheduling Optimization](#scheduling-optimization)
      * [API](#api)
         * [Node Prediction](#node-prediction-1)
            * [Predict Policy](#predict-policy)
            * [Predicted Result](#predicted-result)
         * [Mid Overcommitment](#mid-overcommitment)
            * [Colocation Strategy](#colocation-strategy)
            * [Extended Resources](#extended-resources)
      * [Theoretical Model](#theoretical-model)
         * [Node Peak Prediction](#node-peak-prediction)
         * [N-sigma Prediction](#n-sigma-prediction)
         * [Mid-tier Overcommitment](#mid-tier-overcommitment)
   * [Example](#example)
   * [Alternatives](#alternatives)
      * [Peak Prediction Models](#peak-prediction-models)
   * [References](#references)
   * [Implementation History](#implementation-history)
<!--te-->

## Summary

The *node prediction* is proposed to both improve the node utilization and avoid overloading. By profiling the
tendency of the node metrics, we can estimate the peak usage and implement more efficient over-commitment policy.

## Motivation

Scheduling pods with setting appropriate resource requirements is truly hard to follow. Underestimating requests can
bring performance issues. However, overvaluing requests is likely to cause resource waste and low efficiency. One
common approach is using Vertical Pod Autoscaler (VPA) to autopilot the resource requirements for the pods of the same
workload. The VPA optimizes the resource requirements of the pod according to the pod metrics of the same workload. It
estimates the pod usage and specifies proper resource requirements. It works well when we want to optimize the resource
requirements of workloads. However, most VPA approaches try to abandon the time series attribute from the pod metrics
and generate a relatively static requests/limits that should guarantee to make no bad ignoring the timing. It leaves
the usage-to-limit gap, i.e. the gap between the recommended pod request with the real-time pod usage, and the
well-known pooling effect, i.e. the gap between the sum of the pod usages with the node usage. Inspired by
[Google's work](#references) in the EuroSys'21, we propose the node prediction in Koordinator to conquer these two
gaps.

### Goals

- Define the node prediction API.
- Propose an online history-based-optimized (HBO) prediction model.
- Clarify how the Mid-tier resources are calculated with the prediction.

### Non-Goals/Future Work

- Propose a time-series-forecasting-based or offline prediction model.

## User Stories

### Story 1

As a cluster administrator, there are many web service pods allocating almost node resources. Whereas, the node
utilization is low since most allocated resources are not actually used. To improve node utilization, I want to reclaim
the unused resources to submit some low-priority online-service pods and Flink jobs. However, I am concerned with the
risks of over-utilization bringing machine overload which may cause the performance degradation and hurt the pod QoS.

### Story 2

As a Kubernetes developer, I want to support the long-term load balancing in the scheduler. Thus, I need the information
that which nodes should be idle for a long time.

## Design

### Design Principles

- The node prediction is low-cost and can be implemented in the Koordlet.
- The node prediction is pluggable. Users can replace the default model to customize the prediction.

### Architecture

The node prediction is implemented mainly in the Koordlet and Koord-Manager. The architecture is as below:

![image](/docs/images/node-prediction.svg)

- Koordlet: The agent runs on the node. It implements the metrics collection, metrics storage, and predict server.
    - Metrics Advisor: It collects the cpu/memory usage of the node and running pods. It stores the collected metrics in the Metric Cache.
    - Metric Cache: It stores the node and pod metrics in a TSDB, which allows other modules to query the metrics later.
    - Predict Server: With the node and pod metrics retrieved from the Metric Cache, it calculates and checkpoints the predicted result based on the prediction model.
    - States Informer: It maintains the metadata of the node and the pods. It also reports the latest prediction periodically to the kube-apiserver.
- Koord-Manager: The controller runs on a master node.
    - Configuration delivery: It maintains the prediction and colocation strategies and distributes the node strategy onto the NodeMetric.
    - Resource Calculator: It fetches the node prediction result, and calculates the resource allocatable of the reclaimed resources (i.e. Mid-tier resource).
- Koord-Scheduler: It schedules the pod with different priority bands (e.g. Prod, Mid, Batch). It can enable load-aware scheduling to balance the over-committed nodes' utilization.

#### Workflow

In the koordlet, stages to update the node prediction are as follows:

1. Histogram initialization: The predict server initializes a set of histograms for CPU and memory. For implementing `N-Sigma_v1`, it initializes decayed histograms only for the node and priority classes. While implementing `N-Sigma_v2`, it initializes histograms both for the node and every running pod.
2. Metrics collection: The metrics advisor collects the usage statistics of node and pods and stores them as metric points into the metric cache every CollectInterval (e.g. 1s).
3. Histogram updating: The predict server fetches the node metrics and pod metrics of latest HistogramUpdateInterval (e.g. 30s). Then it uses the aggregated result to update the decayed histograms.
4. Periodical reporting: The states informer fetches node metrics and the last histograms for the node and priority classes every ReportingInterval (e.g. 60s). Then it reports the complete NodeMetric status with last node prediction info to the kube-apiserver.
5. Fast reporting: The states informer fetches the last histograms every CheckPredictionInterval (e.g. 20s). It checks if the predicted result is too small or too larger than the last updated prediction exceeding the ResourceDiffThreshold (e.g. 5%), or the updated duration is longer than ForceUpdateInterval (e.g. 600s). If the check result is true, It updates the latest node prediction to the kube-apiserver.

In the koord-manager, stages to update the Mid-tier resources allocatable are as follows:

1. NodeMetric lifecycle management: The koord-manager list-watches the Node and the ConfigMap slo-controller-config, and maintains the lifecycle of the NodeMetric CR. Once the colocation strategy in the slo-controller-config updated, the koord-manager parses the config data and updates the node prediction policy and mid colocation policy into the NodeMetric.Spec.
2. Mid resource updating: The koord-manager list-watches the NodeMetric. Once the NodeMetric status is updated, the koord-manager gets the latest node metrics and node prediction, and calculates the Mid allocatable resources based on the Mid over-commitment formula. Finally, it updates the Mid allocatable resources into the Node status as the extended resources (`kubernetes.io/mid-cpu`, `kubernetes.io/mid-memory`).

#### Scheduling Optimization

The results of the node prediction on the NodeMetric, the Mid extended resources on the Node and the scheduling Pod
in the scheduler are updated in different time. It is inevitable to find that the scheduler schedules a pod with an
older version of the node prediction, which may cause the schedule result "lagged".

To relief the lagged prediction, the koordlet and koord-manager try both updating earlier when the
prediction/NodeMetric differs from the previous result than a threshold and set a resource buffer which should
tolerant most of the result changes between synchronizations.

For the worst case in which the prediction could be lagged too much (e.g. 1 hour), we can maintain a lower bound of
the real Mid allocatable resources inside the scheduler. This part is not planned in the first version of the Mid-tier
over-commitment.

### API

#### Node Prediction

##### Predict Policy

```go
// ColocationStrategy defines the colocation strategy in slo-controller-config ConfigMap.
type ColocationStrategy struct {
	// ...
	NodePredictPolicy *slov1alpha1.PredictPolicy `json:"nodePredictPolicy,omitempty"`
}

type NodeMetricSpec struct {
	// ...
	PredictPolicy *PredictPolicy `json:"predictPolicy,omitempty"`
}

// PredictPolicy defines the policy for the node prediction.
type PredictPolicy struct {
	ResourceDiffThresholdPercent *int64 `json:"resourceDiffThresholdPercent,omitempty"`
	ColdStartPeriodSeconds       *int64 `json:"coldStartPeriodSeconds,omitempty"`
}
```

##### Predicted Result

```go
type NodeMetricStatus struct {
	// ...
    // ProdReclaimableMetric is the estimated reclaimable resources for the Prod-type pods.
    ProdReclaimableMetric *ReclaimableMetric `json:"prodReclaimableMetric,omitempty"`
}

type ReclaimableMetric struct {
    // Resource is the resource usage of the prediction.
    Resource ResourceMap `json:"resource,omitempty"`
}
```

#### Mid Overcommitment

##### Colocation Strategy

```go
type ColocationStrategy struct {
	// ...
    // MidCPUThresholdPercent defines the maximum percentage of the Mid-tier cpu resource dividing the node allocatable.
    // MidCPUAllocatable <= NodeCPUAllocatable * MidCPUThresholdPercent / 100.
    MidCPUThresholdPercent *int64 `json:"midCPUThresholdPercent,omitempty" validate:"omitempty,min=0,max=100"`
    // MidMemoryThresholdPercent defines the maximum percentage of the Mid-tier memory resource dividing the node allocatable.
    // MidMemoryAllocatable <= NodeMemoryAllocatable * MidMemoryThresholdPercent / 100.
    MidMemoryThresholdPercent *int64 `json:"midMemoryThresholdPercent,omitempty" validate:"omitempty,min=0,max=100"`
}
```

##### Extended Resources

```yaml
apiVersion: v1
kind: Node
metadata:
  name: test-node
status:
  allocatable:
    cpu: '32'
    memory: 129636240Ki
    pods: '213'
    kubernetes.io/mid-cpu: '16000' # allocatable cpu milli-cores for Mid-tier pods
    kubernetes.io/mid-memory: 64818120Ki # allocatable memory bytes for Mid-tier pods
  capacity:
    cpu: '32'
    memory: 129636240Ki
    pods: '213'
    kubernetes.io/mid-cpu: '16000'
    kubernetes.io/mid-memory: 64818120Ki
```

### Theoretical Model

#### Node Peak Prediction

Before elaborating the peak prediction algorithm, let's formalize the node peak prediction problem.

Let's denote the usage of a Pod `p` at the time `t` is `U(p, t)`.

Then the usage of a Node `M` which schedules a set of Pods is `MU(Pods, t) = sum[p in Pods](U(p, t))`.

> Note that the non-Pod usage of the node can be regarded as the usage of a special pod `S`.

When we want to predict the node peak at the time `T`, we are calculating
`Peak(Pods, T) = max[t >= T](sum[p in Pods](U(p, t)))`.

The predicted peak `Peak(Pods, T)` is our node prediction result at `T`.

#### N-sigma Prediction

There are several [statistical peak prediction models](#alternatives) which are practical to implement in the online
scheduler. [*N-sigma*](#references) is the picked peak prediction model in the current implementation. It assumes the
timing node metrics follow the Gaussian distribution, which allows us to estimate the node peak with the mean and
standard deviation (stdev):

`Peak_N-Sigma_v1(Pods, T) = mean[T0 <= t <= T](MU(Pods, t)) + N * stdev[T0 <= t <= T](MU(Pods, t))`

The `Peak_N-Sigma_v1` is the predicted node peak. It is implemented as the first version of node prediction, which is
calculated based on node-level metrics.

Moreover, we can calculate with the pods' metrics:

`Peak_Pods-N-Sigma'(Pods, T) = sum[p in Pods](mean[T0 <= t <= T](U(p, t)) + N * stdev[T0 <= t <= T](U(p, t)))`

A more conservative is derived from their maximal. The `Peak_N-sigma_v2` is the second version of node prediction,
which also considers the pod-level metrics.

`Peak_N-Sigma_v2(Pods, T) = max(Peak_N-Sigma_v1(Pods, T), Peak_Pods-N-Sigma(Pods, T))`.

#### Mid-tier Overcommitment

In the first version, the Mid-tier resource contains the reclaimable resources which are probably unused in the
long-term by the high-priority (i.e. Prod) pods.
The resource calculation for the Mid-tier resources can be described as follows:

```
Allocatable[Mid] := min(Reclaimable[Mid], NodeAllocatable * thresholdRatio)
```

- `Reclaimable[Mid] := max(0, reclaimRatio * Allocated[Prod] - Peak[Prod])`. The peak prediction model is used for estimating the future usage of the running Prod pods. The Mid pods can allocate a proportion of reclaimed resources from running Prod pods.
- `NodeAllocatable * thresholdRatio` is the maximal co-located Mid-tier resource setting from a ratio of the node allocatable.

In next versions, the Mid-tier resource is planned to mix with the default node allocatable (i.e. the Prod allocatable),
which means a Mid pod can allocate the unallocated node allocatable resource, and an idle node is able to schedule Mid
pods. The Prod pods can preempt the Mid pods when the mixed allocatable is exhausted by the Mid pods, so that the
Prod-tier resource is still more stable and guaranteed than the Mid-tier.
Then the resource calculation for the mixed Mid-tier resources can be described as follows:

```
Allocatable[Mid]' := min(Reclaimable[Mid], NodeAllocatable * thresholdRatio) + Unallocated[Mid]
Unallocated[Mid] = max(NodeAllocatable - Allocated[Prod], 0)
```

## Example

1. Edit the `koordinator-system/slo-controller-config` to enable the node prediction and the Mid-tier overcommitment.

```bash
$ kubectl edit configmap -n koordinator-system slo-controller-config
```

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
    name: slo-controller-config
    namespace: koordinator-system
data:
    # ...
    # change the `predictPolicy` and `midColocationPolicy`
    colocation-config: |
        {
            "enable": true,
            "metricReportIntervalSeconds": 60,
            "predictPolicy": {
                "resourceDiffThresholdPercent": 5,
                "coldStartPeriodSeconds": 300
            },
            "midCPUThresholdPercent": 60,
            "midMemoryThresholdPercent": 70
        }
```

2. Check the Mid-tier resource allocatable of the node.

```bash
$ kubectl get node test-node -o yaml
apiVersion: v1
kind: Node
metadata:
  name: test-node
status:
  # ...
  allocatable:
    cpu: '32'
    memory: 129636240Ki
    pods: '213'
    kubernetes.io/mid-cpu: '16000'
    kubernetes.io/mid-memory: 64818120Ki
  capacity:
    cpu: '32'
    memory: 129636240Ki
    pods: '213'
    kubernetes.io/mid-cpu: '16000'
    kubernetes.io/mid-memory: 64818120Ki
```

3. Submit a Mid-tier Pod and check the pod status.

```bash
$ cat test-mid-pod.yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-mid-pod
  labels:
    koordinator.sh/qosClass: BE
  ...
spec:
  # ...
  priority: 7000
  priorityClassName: koord-mid
  schedulerName: koord-scheduler
  containers:
  - name: app
    image: nginx:1.15.1
    resources:
        limits:
          kubernetes.io/mid-cpu: "2000"
          kubernetes.io/mid-memory: 4096Mi
        requests:
          kubernetes.io/mid-cpu: "2000"
          kubernetes.io/mid-memory: 4096Mi
$ kubectl create -f test-mid-pod.yaml
pod/test-mid-pod created
$ kubectl get pod test-mid-pod
NAME                                  READY   STATUS             RESTARTS           AGE
test-mid-pod                          1/1     Running            0                  29s
```

## Alternatives

### Peak Prediction Models

There are several different peak prediction and time series forecasting models which can estimate the future peak
based on the historical node metrics, including statistical methods and machine learning methods. In this proposal,
statistical peak prediction models are preferred since they are practical to implement in the online scheduling system,
have less overhead of metrics collection than the ML approaches, and more simple to analyze and debug.

Here are some common statistical peak prediction models:

1. [Borg-default](#references)

Borg-default simply over-commits the machine resources in a fixed rate `a`, which means the peak usage is regarded as
the result of the requests dividing `a`.

Let's denote the resource request of the Pod `p` at the time `t` is `R(p, t)`, where `R(p, t) = 0` when `p` is not
running. Then we have,

`Peak_Borg-default(Pods, T) = 1/a * sum[p in Pods](R(p, T))`, `a = 1.1` by default.

2. [Resource Central](#references)

Resource Central considers the peak of the machine as the sum of the peak of individual pods (or VMs). And a simple
peak prediction of a pod is the percentile of the historical usages, e.g. `percentile[t in [T-C, T]](U(p, t))`.

`Peak_ResourceCentral(Pods, T) = sum[p in Pods](percentile[t in [T-C, T]](U(p, t)))`

3. [Max](#references)

The Max prediction model does not use the historical metrics directly, but takes the maximal of any known peak results.
It gets the more conservative result than the input models. For example, we have a `Max_Borg-default_ResourceCentral`
model calculated from the Borg-default and Resource Central models:

`Peak_Max_Borg-default_ResourceCentral(Pods, T) = max(Peak_Borg-default(Pods, T), Peak_ResourceCentral(Pods, T))`

## References

1. Vertical Pod Autoscaler: https://github.com/kubernetes/autoscaler/tree/master/vertical-pod-autoscaler
2. Bashir, Noman, et al. "Take it to the limit: peak prediction-driven resource overcommitment in datacenters." Proceedings of the Sixteenth European Conference on Computer Systems. 2021.
3. Cortez, Eli, et al. "Resource central: Understanding and predicting workloads for improved resource management in large cloud platforms." Proceedings of the 26th Symposium on Operating Systems Principles. 2017.

## Implementation History

- [ ] 06/13/2023: Open PR for initial draft
