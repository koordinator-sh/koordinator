---
title: Data Analysis Framework for Prediction and Detection
authors:
  - "@zwzhang0107"
reviewers:
  - "@eahydra"
  - "@FillZpp"
  - "@hormes"
  - "@yihuifeng"
  - "@zwzhang0107"
  - "@saintube"
creation-date: 2023-06-09
last-updated: 2023-06-16
---

# Data Analysis Framework for Prediction and Detection

## Table of Contents

* [Summary](#summary)
* [Motivation](#motivation)
  * [Goals](#goals)
  * [Non-Goals](#non-goals)
* [Proposal](#proposal)
  * [User Stories](#user-stories)
     * [Detect Interference for Noisy Neighbor Problem](#detect-interference-for-noisy-neighbor-problem)
     * [Estimation of Resource Consumption](#estimation-of-resource-consumption)
     * [Time-series Data Prediction](#time-series-data-prediction)
* [Degign Details](#degign-details)
  * [Which metric API should we use](#which-metric-api-should-we-use)
  * [Where does the metric come from](#where-does-the-metric-come-from)
  * [How does the forecasting work](#how-does-the-forecasting-work)
     * [Group](#group)
     * [Collect, Feed and Model](#collect-feed-and-model)
     * [End to End (Prepare &amp; Observe)](#end-to-end-prepare--observe)
  * [How to assemble forecasting models to support multiple scenarios](#how-to-assemble-forecasting-models-to-support-multiple-scenarios)
* [Examples](#examples)
  * [Detect Outliers for Container CPI Metric](#detect-outliers-for-container-cpi-metric)
  * [Profile Resource Usage for FaaS Tasks](#profile-resource-usage-for-faas-tasks)
* [Alternatives](#alternatives)

## Summary
Koordinator is more than just colocation, but is a scheduling system for better QoS. Currently, on the node part, noisy 
neighbors may still occur during runtime; on the scheduling part. we need some history-based optimization during 
autoscaling and allocation for cost-efficiency.

The optimization above relies on the analysis of historical metric data for prediction and detection. So, we propose a general 
forecasting framework, retrieving metric data and building models such as resource usage and runtime quality. These models
can be used on Interference Detection for higher QoS, and Resource Prediction to improve resource efficiency. 

## Motivation

Several topics are still waiting to be proposed in Koordinator yet!

- Interference Detection: Koordlet has provided several QoS Strategies to mitigate the noisy neighbor problem: setting different resource limit
for pods for different QoS Class. These are pre-active and preventive measures, trying to prevent noisy neighbor happens.
We need a proactive approach by collecting metrics and analyzing periodically and continually, catching the bad guy once
someone got interference by noisy neighbors.

- Resource Prediction: Koordinator needs a better estimation of resource consumption during the resource overcommitment.
Since a radical strategy may cause resource competition, and a conservative leads to the waste of resource. This recommendation
also supports the new [MPA framework](https://github.com/kubernetes/autoscaler/blob/master/multidimensional-pod-autoscaler/AEP.md),
which allows integrating with extended Recommenders.

- Forecasting of self-defined metrics: Forecasting is more than just for Kubernetes objects like pods and containers.
Actually, metric APIs already support self-defined types of data, e.g. `custom.metrics.k8s.io`, `external.metrics.k8s.io`, and
the commonly used `Prometheus`. In a two-level scheduling architecture like FaaS, tasks running in containers vary over
time, so prediction of task metric is valuable when routing task from central.

These topics are all related to data analysis, so we need a general framework for prediction and detection to support 
different scenarios. 

### Goals
- Design a holistic framework with modules of profile control, forecasting and data scraping to support data prediction and detection.

- Clarify the data type and transfer links, and how to save the storage and collection overhead.

- Collaboration and division of builtin and self-extended metric exporters, and adaption of multiple using scenarios.

### Non-Goals
- Design of forecasting algorithm. The interference detection and resource prediction may share same prediction models,
however, this is over-detailed now. 

- Supports of MPA framework proposed in Kubernetes autoscaler. Although our work can run as a recommender for MPA after
some protocol conversion works, we can discuss in the following proposals.

- Long-term optimization, even though we have already mentioned some ideas about saving transfer cost and storage overhead.  

## Proposal

### User Stories

#### Detect Interference for Noisy Neighbor Problem
Numbers of applications running in K8s cluster, the cluster administrator needs to know whether some pods got
interference by others through general methods, instead of application-specified metrics. Advanced metrics like
CPI, PSI are provided for analysis and modeling, and interference will be detected once there is an outlier. 
  

#### Estimation of Resource Consumption
Collecting history metrics to estimate resource usage of containers or tasks, which can help the right-sizing and scheduling
optimization for load-balancing.

#### Time-series Data Prediction
HPA and VPA control the scaling actions of K8s workloads, which rely on the prediction of resource usage over time. We can
also customize the recommender with different algorithms on the [autoscaler](https://cloud.google.com/kubernetes-engine/docs/concepts/horizontalpodautoscaler?hl=zh-cn) 
as needed. 


## Degign Details

### Which metric API should we use
Kubernetes has defined three types of metric api: `metrics.k8s.io`, `custom.metrics.k8s.io` and `external.metrics.k8s.io`,
which can be accessed as Kubernetes resources.

`Prometheus` is another widely used metric service, which also provides good extendability. User can collect metrics in their
own components(e.g. daemon-set, operator), and expose as standard exporter format. Then these metrics can be accessed from
`Prometheus` after the service monitor defined.

Besides, Kubernetes also provides customized API as CRD and aggregate layer in `apiserver`, which can be used for metric
report. Furthermore, we can define dedicated protocols as an internal metric service.

`Prometheus` seems to be a good option before we take a deep look at the pros and cons.

Pros:
- Forecasting models can be verified rapidly since the Prometheus data source is ready-made.
- New metrics can be integrated into Prometehus easily as exporter.
- Koordinator metric-server is a brand-new component and still waiting for construction.

Cons:
- Prometheus shows bad performance in large-scale environments.
- The storage of Prometheus is wasted since some forecasting model does not need all history data, but
 all metrics in Prometheus will be persistent with a fixed duration.

**And here are the milestones we plan.**

Koordinator will use `Prometheus` API at first so that our forecasting model can be verified rapidly with current data source.
In the future we will use `metric api` and Koordinator metric-server for better performance, and use history data in 
`Prometheus` only for warm up since it is an optional component.

![metric-apis](/docs/images/forecasting/metric-apis.svg)

### Where does the metric come from
Metrics from different API varies on data source.

![metric-exporters](/docs/images/forecasting/metric-exporters.svg)

Here are the data source of Kubernetes built-in metric APIs.
 
- `metric.k8s.io`: providing the latest CPU and memory usage of pods, containers and nodes. The backend is implemented by 
`metric-server`, which scrapes metrics from `kubelet` periodically and saves as snapshots in memory.

- `custom.metrics.k8s.io`: for customized metric implemented by `adapter`, for example, `Prometheus` supports custom metric
adapter for using self-defined metrics on HPA. The origin data is stored in `TSDB` of `Prometheus`. 

- `external.metrics.k8s.io`: also for customization, which is usually implemented by cloud service providers, such as collecting
metrics from its log service, SLB.

Customized metrics can also be exported in daemon-sets or operators, then collected and stored in Prometheus. In the future,
we will optimize the efficiency and cost through koord metric server.

![metric-nod-node](/docs/images/forecasting/metric-on-node.svg)

Examples of the data source we used for different purposes.

| topic | metric type | API | collector | scenario |
| --- | --- | --- | --- | --- | 
| resource usage estimation (CPU and memory) | resource usage | metric server | kubelet | resource right-sizing; resource overcommitment; |
| time-series estimation | resource usage | prometheus | kubelet; addon exporters | autoscaling of HPA and VPA; workload prewarming; |
| interference detection | PMU; resource pressure | prometheus; Koordinator metric server | node exporter; addon exporter | outlier detection for noisy neighbor |
| self-defined metric profiling | task resource usage; task running duration | prometheus; Koordinator metric server  | addon exporter | FaaS task profiling |

### How does the forecasting work
The forecasting procedure works periodically, and in each round we divide the procedure into several stages: Group, 
Collect, Feed, Model and Observe. Let's start with the Group first. 

#### Group
Forecasting aims to a `target`. This `target` could be a workload (e.g. Deployment), a collection of pods or just a
series of metrics. Here are some examples.

- Resource usage estimation for Deployment: the `target` identifier is "ContainerName + DeploymentName".

- Prediction of some Prometheus metrics: the `target` identifier is a "series kv of metric labels"
(e.g. service name + CPU/Memory).

```go
type WorkloadTarget struct {
    *autoscaling.CrossVersionObjectReference `json:",inline"`
    ContainerName string `json:"container"`
}

type PodsCollectionTarget struct {
    Selector *metav1.LabelSelector `json:"selector,omitempty"`
    ContainerName string `json:"container"`
}

type PrometheusLabelsTarget struct {
    Labels map[string]string `json:"labels,omitempty"`
}
```

The `Group` stage classifies all replicas of same groups into one collection(e.g. pods belong to same deployment). 

![forecast-grouping](/docs/images/forecasting/forecast-grouping.svg)

#### Collect, Feed and Model
The `Collect` stage scrapes metrics for each replica in group from different sources, and then feed the data to corresponding
model.

The `Model` part contains the core algorithm, receives data from collectors and finishes the training works. Each implement
of model should also support checkpoints for its (intermediate) result and save in third-party storage like apiserver, which
can speed up the warm-up if the forecast controller restarts.

![forecast-collect](/docs/images/forecasting/forecasting-collect.svg)

#### End to End (Prepare & Observe)
The forecast framework supports specifying metric source and algorithm as needed. So we have a `Prepare` stage to do the
chores works at the first beginning, converting the interface into forecast framework, creating clients of metric and
`target` and loading checkpoints to warm up models.

After all stages are done in each round, the `Observe` stage shows the result according to the format defined by models.
For example, a `Statistical Distribution` type of model will show the result as:
```go
type DistributionValues struct {
   Mean              resource.Quantity                `json:"mean,omitempty"`
   Quantiles         map[string]resource.Quantity     `json:"quantiles,omitempty"`
   StdDev            resource.Quantity                `json:"stddev,omitempty"`
   TotalSamplesCount int                              `json:"totalSamplesCount,omitempty"`
}
```

### How to assemble forecasting models to support multiple scenarios
All forecasting stages are designed as tool packages, which can be extended with various implementations (e.g. Prometheus 
or metric-server type `Collector`). We assemble all stages into a `Closure` for each target according to the spec, 
then a module called `Forecast Runner` will run these `Closure` parallel.
    
We define a `Closure` for each target, including the metric and prediction model to use. `Closure` can be run independently
by `Forecast Runner`.

![forecast-closure](/docs/images/forecasting/forecast-closure.svg)

The top-level are controllers for different scenarios such as resource recommendation and interference detection. We define
CRD for each controller and then the CR will be converted to a `Closure` we defined in forecast framework. The 
`Forcast Runner` executes all `Closures` and produces the result through observer, which will be convert and written back
to CRD status field.

![forecasting-controller](/docs/images/forecasting/forecasting-controller.svg)

## Examples

### Detect Outliers for Container CPI Metric
Container [CPI metric](https://static.googleusercontent.com/media/research.google.com/zh-CN//pubs/archive/40737.pdf) shows
the runtime quality of applications, which keeps relatively stable over serving time. We build a prediction model based on
historical metric data to determine whether a container disrupted by others if metric has outliers at present. 

### Profile Resource Usage for FaaS Tasks
A FaaS service consists of functions, which receive requests and invoke the executor. Resource usage metrics are exposed
for `Prometheus` as exporter.
```
function_cpu_usage{service="service-word-count", function="f-map-name", slice="xxx"} 1.2
function_memory_usage{service="service-word-count", function="f-map-name", slice="xxx"} 1024000
``` 
We use the forecasting framework to aggregate the metric of each function, and predict the resource usage for right-sizing
and load-aware scheduling.

## Alternatives
Actually, we plan to implement each forecasting or detection model as an independent controller, and each controller
finishes all work by themselves (e.g. resource-recommendation, interference-detection). However, after taking a deep look 
at the prediction model they use, we find that the algorithm model are similar. For example, both resource-recommendation
and CPI outlier detection use histogram to calculate the distribution of metric for statistical indicators. So we decide
to separate the `Forecast Framework` and `Forcast Runner` out for better extension. 

## Milestones
- [ ] Use statistical distribution method for self-defined metric forecasting
  - [ ] Prometheus client tools to collect metric data
  - [ ] Distribution model using histogram-like tools for prediction and detection 
  - [ ] API for statistical distribution model
  - [ ] Metric outlier detection: define CR for deployment and generate forecast value
  - [ ] Resource prediction for FaaS-type task
  
- [ ] Performance and Easy of Use
  - [ ] Support multiple kinds of data sources, first is the metric server
  - [ ] Sync model checkpoints periodically to speed up the cold start 
  - [ ] Builtin metric server for saving the prometheus storage and scraping overhead
  
- [ ] Advanced Models and Algorithm: Using OneClassSVM for the container QoS detection
  - [ ] API for OneClassSVM analysis model, supporting multi-dimension metrics 
  - [ ] Send model result to node through private protocol
  - [ ] Koordlet compares the real-time metric with the model for outlier 

