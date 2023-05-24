---
title: Enhanced Scheduler Extension
authors:
  - "@eahydra"
reviewers:
  - "@hormes"
  - "@saintube"
  - "@buptcozy"
creation-date: 2022-05-13
last-updated: 2023-05-11
status: provisional
---

<!-- TOC -->

- [Enhanced Scheduler Extension](#enhanced-scheduler-extension)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [User stories](#user-stories)
            - [Story 1](#story-1)
            - [Story 2](#story-2)
            - [Story 3](#story-3)
        - [Design Details](#design-details)
            - [Enhancement Kubernetes Scheduling Framework principles](#enhancement-kubernetes-scheduling-framework-principles)
            - [Custom Extension Overview](#custom-extension-overview)
            - [FrameworkExtender And ExtendedHandle](#frameworkextender-and-extendedhandle)
            - [Intercept plugin initialization process](#intercept-plugin-initialization-process)
            - [Custom Transformer extension points to support Reservation Scheduling](#custom-transformer-extension-points-to-support-reservation-scheduling)
            - [Expose the internal state of plugins](#expose-the-internal-state-of-plugins)
            - [Support for plugins to create controllers](#support-for-plugins-to-create-controllers)
            - [Debug Filter Result](#debug-filter-result)
            - [Debug Score Result](#debug-score-result)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

# Enhanced Scheduler Extension

## Summary

This proposal describes how to extend the kubernetes scheduling framework without modify upstream codes to support the scheduling features that  Koordinator needs to develop.

## Motivation

Although Kubernetes Scheduler provides the scheduling framework to help developer to extend scheduling features. However, it cannot support the features that Koordinator needs to develop, such as Reservation, problem diagnosis and analysis, etc. 

### Goals

1. Provides scheduling extension point hook mechanism
1. Provides scheduling plugins expose state mechanism to help diagnose analysis problems

### Non-Goals/Future Work

## Proposal

### User stories

#### Story 1

Koordiantor supports users to use `Reservation` CRD to reserve resources. We expect Reservation CRD objects to be scheduled like Pods. In this way, the native scheduling capabilities of Kubernetes and other extended scheduling capabilities can be reused. This requires a mechanism to disguise the Reservation CRD object as a Pod, and extend some scheduling framework extension points in order to better support Reservation.

#### Story 2

Koordinator provides some scheduling plugins, such as Fine-grained CPU Scheduling, Device Share Scheduling, Coscheduling, ElasticQuota, etc. These plugins are brand new, and the supported scenarios are relatively rich, and the internal logic and state of the plugins are also a bit complicated. When we may encounter some problems in the production environment and need to be diagnosed and analyzed, we need to confirm the cause of the problem based on the internal status of the plugin. But currently the kubernetes scheduling framework does not provide a mechanism to export the internal state of the plugin.

#### Story 3

The scheduler provides many plugins, and most plugins implement Scoring Extension Point. How to configure the weights of these plugins needs to be decided in combination with specific problems. When the optimal node is selected according to the scoring results, the results may not meet expectations. At this point we need to be able to trace or debug these scoring results in some way. But there is currently no good way.

### Design Details

#### Enhancement Kubernetes Scheduling Framework principles

At present, the kube-scheduler provided by Kubernetes can be divided into several parts. The outermost layer is `k8s.io/kubernetes/cmd/kube-scheduler`, which is the entrance of kube-scheduler; `k8s.io/kubernetes/pkg/scheduler` is responsible for integrating the framework And execute scheduling workflow, including initializing framework and plugins, scheduling Pod, etc. The core module is `k8s.io/kubernetes/pkg/scheduler/framwork`, which is the **Kubernetes Scheduling Framework**.

Each layer provides some interfaces or methods to support developers to extend some capabilities, and the evolution speed of each layer is also different. Generally, the evolution speed of the core module should be relatively slow, and the evolution of core modules tends to extend rather than modify the existing interface or extension mechanism, Otherwise, it will cause very large cost and reliability problems for external dependencies. But the meaning of design is to determine the boundaries of a module or system and clarify its own responsibilities, which means that its capabilities are limited. Some scenarios are indeed not concerned or covered by it, but good design lies in modules or systems are extensible or can easily be isolated again through abstractions without exposing internal details.From this point of view, the scenarios or functions that Koordinator currently needs to support can be solved by doing some extensions or workarounds to the kube scheduler framework. However, some principles need to be followed to reduce future conflicts with the evolution of the upstream Kubernetes community.

1. **DO NOT** modify the Kubernetes Scheduling Framework. The scheduling framework is the core module of kube-scheduler and is still evolving. In order to avoid conflict with the upstream community between Koordinator's enhanced capabilities.
1. **DO NOT** modify the `k8s.io/kubernetes/pkg/scheduler` but can implements supported interfaces or high-order functions, such as `ScheduleAlgorithm`, `NextPod`, `Error` and `Profiles`.  The `Profiles` contains an instance of the Framework interface corresponding to each KubeSchedulerProfile. We can implement the Framework and replace the instances in Profiles to get the opportunity to participate in the scheduling process to do something.
1. Extend `k8s.io/kubernetes/cmd/kube-scheduler` as simply as possible. 

#### Custom Extension Overview

![image](/docs/images/koord-scheduler-framework-ext.png)

#### FrameworkExtender And ExtendedHandle

- `FrameworkExtender` extends the K8s Scheduling Framework interface to provide more extension methods to support Koordinator. `FrameworkExtender` corresponds to each K8s Scheduling Profile and replaces the original `framework.Framework` object in scheduler.Scheduler.Profiles, and some transformers inside each `FrameworkExtender` are initialized through the type assert mechanism when the plugin is built.
- `FrameworkExtender` includes an `ExtendedHandle` interface. `ExtendedHandle` extends the K8s Scheduling Framework `Handle` interface to facilitate plugins to access Koordinator's resources and states. 

```go
type FrameworkExtender interface {
    framework.Framework
    ExtendedHandle

    RunReservationExtensionPreRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status
    RunReservationExtensionRestoreReservation(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, matched []*ReservationInfo, unmatched []*ReservationInfo, nodeInfo *framework.NodeInfo) (PluginToReservationRestoreStates, *framework.Status)
    RunReservationExtensionFinalRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, states PluginToNodeReservationRestoreStates) *framework.Status

}

type ExtendedHandle interface {
    framework.Handle
    KoordinatorClientSet() koordinatorclientset.Interface
    KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory
}
```

#### Intercept plugin initialization process

In order to pass the `ExtendedHandle` object to each custom plugins, we should intercept the plugin initialization process. 

And we expect that any customized plugins can be directly and seamlessly integrated into the koordinator scheduler, so the `PluginFactory` of the plugin will not be changed. Therefore, we can modify the prototype of `k8s.io/kubernetes/cmd/kube-scheduler/app.Option` and the implementation of `k8s.io/kubernetes/cmd/kube-scheduler/app.WithPlugin` as the follows to get the opportunity to intercept the plugin initialization process.

When the custom plugin is registered to the out-of registry using `WithPlugin`, it will use `frameworkext.PluginFactoryProxy` to wrap the plugin's original `PluginFactory`. We finally complete the interception of the plugin initialization process in `frameworkext.PluginFactoryProxy`. When constructing the K8s Scheduling Profile, it will first call the takeover function of `PluginFactoryProxy`. At this time, we can construct a `FrameworkExtender`, and then pass in the original `PluginFactory` function of the plugin .

Of course, we will not modify `k8s.io/kubernetes/cmd/kube-scheduler` directly. Considering that the logic of `k8s.io/kubernetes/cmd/kube-scheduler` itself is not complicated, it will basically not bring us additional maintenance costs, so we will copy the relevant code to Koordinator for separate maintenance.

```go

// cmd/koord-scheduler/app/server.go

// Option configures a framework.Registry.
type Option func(*frameworkext.FrameworkExtenderFactory, runtime.Registry) error

// WithPlugin creates an Option based on plugin name and factory. Please don't remove this function: it is used to register out-of-tree plugins,
// hence there are no references to it from the kubernetes scheduler code base.
func WithPlugin(name string, factory runtime.PluginFactory) Option {
    return func(extenderFactory *frameworkext.FrameworkExtenderFactory, registry runtime.Registry) error {
        return registry.Register(name, frameworkext.PluginFactoryProxy(extenderFactory, factory))
    }
}

// pkg/scheduler/frameworkext/framework_extender_factory.go

// PluginFactoryProxy is used to proxy the call to the PluginFactory function and pass in the ExtendedHandle for the custom plugin
func PluginFactoryProxy(extenderFactory *FrameworkExtenderFactory, factoryFn frameworkruntime.PluginFactory) frameworkruntime.PluginFactory {
    return func(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
        fw := handle.(framework.Framework)
        frameworkExtender := extenderFactory.NewFrameworkExtender(fw)
        plugin, err := factoryFn(args, frameworkExtender)
        if err != nil {
            return nil, err
        }
        extenderFactory.updatePlugins(plugin)
        frameworkExtender.(*frameworkExtenderImpl).updatePlugins(plugin)
        return plugin, nil
    }
}
```

#### Custom Transformer extension points to support Reservation Scheduling

When a Reservation is scheduled in the form of a Pod, it can use the Pod's original scheduling protocol, such as Pod Affinity/Pod Anti Affinity, PodTopologySpread, and resource specification statements and ports. This is very convenient. We don't need to customize a scheduling process for the Reservation, but it also introduces some new problems, that is, how to return the resources held by the Reservation to the Pod. For example, Reservation uses Pod Anti Affinity. When a Pod is scheduled, the relationship between Pods seen by the Pod should not be aware of the Reservation, otherwise the scheduling will fail. Similarly, how can the Pod get the CPU/Memory held by the Reservation and the fine-grained resources allocated by the fine-grained CPU Orchestration and DeviceShare. These are practical issues that need to be addressed.

In order to solve these problems, we need to make some new extensions in the Schedule process of the original K8s Scheduler.

We additionally take a snapshot of the data in the original framework.SharedLister before PreFilter. The purpose of this is to support the modification of NodeInfo in the subsequent resource return process of Reservation. Therefore, we define the function `SharedListerAdapter`, which supports the adaptation of the original framework.SharedLister, and can supplement or modify data in advance. And with the additional Snapshot mechanism, it ensures the consistency of subsequent data views.

Some additional logic can be performed before and after the PreFilter. For example, before PreFilter, we need to modify PVCRefCounts in NodeInfo to remove the count occupied by the PVC held by Reservation, otherwise the VolumeRestrictions plugin will fail to verify in PreFilter.

To solve these problems, we define the `Transformer` interface. The plugin can be implemented on demand, and the Pod or NodeInfo can be modified when the before or after PreFilter/Filter. Similar to the custom implementation method `RunScorePlugins` mentioned above, we can customize the implementation methods `RunPreFilterPlugins` and `RunFilterPluginsWithNominatedPods`. Before executing the real extension point logic, first execute the `Tranformer` interface and modify the Pod and NodeInfo. If necessary, you can modify the Pod or Node before executing the Score Extension Point by implementing ScoreTransformer.

Considering that there may be multiple different Transformers to modify the Pod or NodeInfo requirements, when the Hook is called, the Hook will be called cyclically, and the modification result of the previous Transformer and the input of the next Transformer will continue to be executed.

Here are some additional explanations for the scenarios in which these new extension points should be used. If you can complete the scheduling function through the extension points such as Filter/Score provided by the K8s Scheduling Framework without modifying the incoming NodeInfo/Pod and other objects, you do not need to use these new extension points.

Plugins like NodeNUMAResource/DeviceShare will allocate fine-grained resources to Pods, such as CPU Cores, some resources of GPU devices, etc. If the Reservation preempts such resources, it means that when the Pod it belongs to needs such resources, it should be allocated from the Reservation first. To express the semantics behind this scenario, we define the `ReservationRestorePlugin` interface. Koordinator Scheduler in BeforePreFilter phase calls the method `ReservationRestorePlugin.ResstoreReservation` to calc the state if restore the matched and unmatched reservations, and scheduler pass the results that merged by node to the plugin via the method `ReservationRestorePlugin.FinalRestoreReservation`.

When the Pod is successfully scheduled and a node is selected, it will enter the Reserve stage. If the node has a Reservation that matches the Pod, a most suitable Reservation instance needs to be selected. The most suitable Reservation instance must be perceived by plugins such as NodeNUMAResources/DeviceShare to help such plugins prioritize allocation from the Reservation when allocating resources. Therefore, we define the `Reservation Nominator` interface, which is responsible for nominating the most appropriate Reservation instance after the node scores and before Reserve. When the `ReservationNominator` gives a Reservation instance, the corresponding Reservation will be put into the CycleState, and other plugins can read the Reservation from the CycleState.

`ReservationNominator` should call the frameworkext.FrameworkExtender method `RunReservationFilterPlugins` to filter out those invalid Reservations that cannot meet the Pod resource requirements such as Pod needs CPU Core and GPU from Reservation but the Reservation instance has already allocated all resources for other Pods. The method `RunReservationFilterPlugins` calls the plugins that implement `ReservationFilterPlugin` such as NodeNUMAResources and DeviceShare.

After filter out invalid reservations, we may get empty reservations or a portion of valid Reservations that can be allocated.
So `ReservationNominator` should call the frameworkext.FrameworkExtender method `RunReservationScorePlugins` to call the plugins that need to be aware of Reservations, score these Reservations and select an optimal Reservation. The plugins such as NodeNUMAResource and DeviceShare should implement the interfce `ReservationScorePlugin`.

ReservationPreBindPlugin performs special binding logic specifically for Reservation in the PreBind phase. Similar to the built-in VolumeBinding plugin of kube-scheduler, it does not support Reservation, and how Reservation itself uses PVC reserved resources also needs special handling. In addition, implementing this interface can clearly indicate that the plugin supports Reservation.

```go

// SharedListerAdapter intercepts the incoming ShareLister and
// modifies the corresponding data to support some advanced scheduling scenarios.
type SharedListerAdapter func(lister framework.SharedLister) framework.SharedLister

// SchedulingTransformer is the parent type for all the custom transformer plugins.
type SchedulingTransformer interface {
    Name() string
}

// PreFilterTransformer is executed before and after PreFilter.
type PreFilterTransformer interface {
    SchedulingTransformer
    // BeforePreFilter If there is a change to the incoming Pod, it needs to be modified after DeepCopy and returned.
    BeforePreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool, *framework.Status)
    // AfterPreFilter is executed after PreFilter.
    // There is a chance to trigger the correction of the State data of each plugin after the PreFilter.
    AfterPreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status
}

// FilterTransformer is executed before Filter.
type FilterTransformer interface {
    SchedulingTransformer
    BeforeFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) (*corev1.Pod, *framework.NodeInfo, bool, *framework.Status)
}

// ScoreTransformer is executed before Score.
type ScoreTransformer interface {
    SchedulingTransformer
    BeforeScore(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodes []*corev1.Node) (*corev1.Pod, []*corev1.Node, bool, *framework.Status)
}

// PluginToReservationRestoreStates declares a map from plugin name to its ReservationRestoreState.
type PluginToReservationRestoreStates map[string]interface{}

// PluginToNodeReservationRestoreStates declares a map from plugin name to its NodeReservationRestoreStates.
type PluginToNodeReservationRestoreStates map[string]NodeReservationRestoreStates

// NodeReservationRestoreStates declares a map from plugin name to its ReservationRestoreState.
type NodeReservationRestoreStates map[string]interface{}

// ReservationRestorePlugin is used to support the return of fine-grained resources
// held by Reservation, such as CPU Cores, GPU Devices, etc. During Pod scheduling, resources
// held by these reservations need to be allocated first, otherwise resources will be wasted.
type ReservationRestorePlugin interface {
    framework.Plugin
    PreRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status
    RestoreReservation(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, matched []*ReservationInfo, unmatched []*ReservationInfo, nodeInfo *framework.NodeInfo) (interface{}, *framework.Status)
    FinalRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, states NodeReservationRestoreStates) *framework.Status
}

// ReservationFilterPlugin is an interface for Filter Reservation plugins.
// These plugins will be called during the Reserve phase to determine whether the Reservation can participate in the Reserve
type ReservationFilterPlugin interface {
    framework.Plugin
    FilterReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservation *schedulingv1alpha1.Reservation, nodeName string) *framework.Status
}


const (
    // MaxReservationScore is the maximum score a ReservationScorePlugin plugin is expected to return.
    MaxReservationScore int64 = 100

    // MinReservationScore is the minimum score a ReservationScorePlugin plugin is expected to return.
    MinReservationScore int64 = 0
)

// ReservationScoreList declares a list of reservations and their scores.
type ReservationScoreList []ReservationScore

// ReservationScore is a struct with reservation name and score.
type ReservationScore struct {
    Name  string
    Score int64
}

// PluginToReservationScores declares a map from plugin name to its ReservationScoreList.
type PluginToReservationScores map[string]ReservationScoreList

// ReservationScorePlugin is an interface that must be implemented by "ScoreReservation" plugins to rank
// reservations that passed the reserve phase.
type ReservationScorePlugin interface {
    framework.Plugin
    ScoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservation *schedulingv1alpha1.Reservation, nodeName string) (int64, *framework.Status)
}

// ReservationNominator nominates a more suitable Reservation in the Reserve stage and Pod will bind this Reservation.
// The Reservation will be recorded in CycleState through SetNominatedReservation.
// When executing Reserve, each plugin will obtain the currently used Reservation through GetNominatedReservation,
// and locate the previously returned reusable resources for Pod allocation.
type ReservationNominator interface {
    framework.Plugin
    NominateReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (*schedulingv1alpha1.Reservation, *framework.Status)
}

// ReservationPreBindPlugin performs special binding logic specifically for Reservation in the PreBind phase.
// Similar to the built-in VolumeBinding plugin of kube-scheduler, it does not support Reservation,
// and how Reservation itself uses PVC reserved resources also needs special handling.
// In addition, implementing this interface can clearly indicate that the plugin supports Reservation.
type ReservationPreBindPlugin interface {
    framework.Plugin
    PreBindReservation(ctx context.Context, state *framework.CycleState, reservation *schedulingv1alpha1.Reservation, nodeName string) *framework.Status
}
```


#### Expose the internal state of plugins

We define a new extension interface to help the plugin expose the internal state through the Restful API, and provide some built-in Restful APIs to query which APIs are exposed by the current scheduler and some commonly used internal data, such as NodeInfo, etc.

The new extension interface named `APIServiceProvider`. The plugins can implement this interface to register the API to be exposed as needed. When the plugin is initialized, `frameworkext.PluginFactoryProxy` will check whether the newly constructed plugin implements `APIServiceProvider`, and if so, it will call the `RegisterEndpoints` method of the interface to register the API. The Restful APIs exposed by these plugins will be bound to the URL path `/apis/v1/plugins/` and will be prefixed with the name of each plugin. For example, the API `/availableCPUs/:nodeName` exposed by the plugin `NodeNUMAResource` will be converted to `/apis/v1/plugins/NodeNUMAResource/availableCPUs/:nodeName`.


```go
type APIServiceProvider interface {
    RegisterEndpoints(group *gin.RouterGroup)
}

type ErrorMessage struct {
    Message string `json:"message,omitempty"`
}

func ResponseErrorMessage(c *gin.Context, statusCode int, format string, args ...interface{}) {
    var e ErrorMessage
    e.Message = fmt.Sprintf(format, args...)
    c.JSON(statusCode, e)
}
```

Users can use the built-in API `/apis/v1/__services__` to query how many Restful APIs are provided by the current scheduler.  The response as the follows:

```json
{
    "GET": [
        "/apis/v1/__services__",
        "/apis/v1/nodes/:nodeName",
        "/apis/v1/plugins/Coscheduling/gang/:namespace/:name",
        "/apis/v1/plugins/Coscheduling/gangs",
        "/apis/v1/plugins/NodeNUMAResource/availableCPUs/:nodeName",
        "/apis/v1/plugins/NodeNUMAResource/cpuTopologyOptions/:nodeName"
    ]
}
```

Koordinator scheduler also provides `/apis/v1/nodes/:nodeNa` to expose internal `NodeInfo` to developers. 


#### Support for plugins to create controllers

Similar to Coscheduling/ElasticQuota Scheduling, these scheduling plugins have a matching Controller to synchronize the status of the related CRD. The most common way is to deploy these controllers independently of the scheduler. This method will not only bring additional maintenance costs and resource costs, but also if there are more states in the scheduling plugin that need to be synchronized to the CRD Status, the logic in the Controller and the logic in the plugin need to be more closely coordinated. The best way is that the Controller and the scheduling plugin are in the same process.

We define a new interface called `ControllerProvider`. When the plugin is initialized, `frameworkext.PluginFactoryProxy` will check whether the newly constructed plugin implements `ControllerProvider`, and if so, it will call the `NewControllers` method of the interface to get the instances of Controllers, and save these instances in the `ExtendedHandle`. When the scheduler gets the leader role, it can trigger the `ExtendedHandle` to start these controllers.

```go
type ControllerProvider interface {
    NewControllers() ([]Controller, error)
}

type Controller interface {
    Start()
    Name() string
}
```

#### Debug Filter Result

The K8s Scheduler Framework only supports summarizing and updating the results of Filter failures to Pod Status and outputting them in the form of Events. However, these aggregated information are sometimes difficult to help locate the cause of Filter failure. Often we need to understand why a Pod fails to be scheduled on a certain node. Therefore, the corresponding implementation of ExtendedHandle intercepts the framework.Handle.RunFilterPluginsWithNominatedPods method, and outputs the Filter results in the form of logs (BTW: some enhancements can also be considered in the future). Users can configure the startup parameter `--debug-filters` or enable it dynamically with the following command:

```bash
# enables the debug-filters flags to output the Filter results
$ curl -X POST schedulerIP:10251/debug/flags/f --data 'true' 
successfully set debugFilterFailure to true
```

#### Debug Score Result

If we want to support debug scoring results, the easiest way is to directly modify `Framework.RunScorePlugins` and print the results after scoring. But this goes against the extend principles we laid out earlier. But we can think differently. When `scheduler.Scheduler` executes `scheduleOne`, it obtains an instance of the `framework.Framework` interface from `Profiles` and calls the method `RunScorePlugins`. At the same time, considering that we have maintained the initialization code of scheduler separately, then we can customize the implementation of the `framework.Framework` interface, implement the method `RunScorePlugins` and take over the `Profiles` in `scheduler.Scheduler`. In this way, we can first call the `RunScorePlugins` method of the original `framework.Framework` interface instance in the custom implemented `RunScorePlugins`, and then print the result. 

For the processing of the results, we can simply print it to the log in markdown format. When needed, enable Scoring Result debugging capability through the HTTP interface `/debug/flags/s` like `/debug/flags/v`. The developers also enable the capability via flags `--debug-scores`. 

```bash
# print top 100 score results.
$ curl -X POST schedulerIP:10251/debug/flags/s --data '100' 
successfully set debugTopNScores to 100
```

The following are the specific scoring results:

```
| # | Pod | Node | Score | ImageLocality | InterPodAffinity | LoadAwareScheduling | NodeAffinity | NodeNUMAResource | NodeResourcesBalancedAllocation | NodeResourcesFit | PodTopologySpread | Reservation | TaintToleration |
| --- | --- | --- | ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:|
| 0 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.51 | 577 | 0 | 0 | 87 | 0 | 0 | 96 | 94 | 200 | 0 | 100 |
| 1 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.50 | 574 | 0 | 0 | 85 | 0 | 0 | 96 | 93 | 200 | 0 | 100 |
| 2 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.19 | 541 | 0 | 0 | 55 | 0 | 0 | 95 | 91 | 200 | 0 | 100 |
| 3 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.18 | 487 | 0 | 0 | 15 | 0 | 0 | 90 | 82 | 200 | 0 | 100 |
```

| # | Pod | Node | Score | ImageLocality | InterPodAffinity | LoadAwareScheduling | NodeAffinity | NodeNUMAResource | NodeResourcesBalancedAllocation | NodeResourcesFit | PodTopologySpread | Reservation | TaintToleration |
| --- | --- | --- | ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:| ---:|
| 0 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.51 | 577 | 0 | 0 | 87 | 0 | 0 | 96 | 94 | 200 | 0 | 100 |
| 1 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.50 | 574 | 0 | 0 | 85 | 0 | 0 | 96 | 93 | 200 | 0 | 100 |
| 2 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.19 | 541 | 0 | 0 | 55 | 0 | 0 | 95 | 91 | 200 | 0 | 100 |
| 3 | default/curlimage-545745d8f8-rngp7 | cn-hangzhou.10.0.4.18 | 487 | 0 | 0 | 15 | 0 | 0 | 90 | 82 | 200 | 0 | 100 |


## Alternatives

## Implementation History

- 2022-05-13: Initial proposal
- 2022-10-25: Complete proposal
- 2022-11-10: Add Overview and ControllerProvider
- 2022-11-11: Update overview image and add comment to Hook
- 2023-03-18: Add new extension points and clarify existing framework extenders
- 2023-05-10: Update transformer interface signature
- 2023-05-11: Update ReservationRestorePlugin