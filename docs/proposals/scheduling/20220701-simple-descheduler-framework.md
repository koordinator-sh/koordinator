---
title: Simple Descheduler Framework
authors:
  - "@eahydra"
reviewers:
  - "@hormes"
  - "@allwmh"
  - "@jasonliu747"
  - "@saintube"
  - "@zwzhang0107"
creation-date: 2022-07-01
last-updated: 2023-03-08
status: provisional
---

# Simple Descheduler Framework

## Table of Contents

<!-- TOC -->

- [Simple Descheduler Framework](#simple-descheduler-framework)
    - [Table of Contents](#table-of-contents)
    - [Glossary](#glossary)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
            - [Descheduler profile](#descheduler-profile)
            - [Abstract Evict and Filter interfaces](#abstract-evict-and-filter-interfaces)
            - [Plugin descheduler strategy](#plugin-descheduler-strategy)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

## Glossary

## Summary

This proposal is based on the K8s community's [descheduler](https://github.com/kubernetes-sigs/descheduler) to design and implement the descheduler framework required by the koordinator.

## Motivation

The existing [descheduler](https://github.com/kubernetes-sigs/descheduler) in the community can solve some problems, but we think that there are still many aspects of the descheduler that can be improved, for example, it only supports the mode of periodic execution, and does not support the event-triggered mode. It is not possible to extend and configure custom rescheduling strategies without invading the existing code of descheduler like kube-scheduler; it also does not support implementing custom evictor. 

We also noticed that the K8s descheduler community also found these problems and proposed corresponding solutions such as [#753 Descheduler framework Proposal](https://github.com/kubernetes-sigs/descheduler/issues/753) and [PoC #781](https://github.com/kubernetes-sigs/descheduler/pull/781). The K8s descheduler community tries to implement a descheduler framework similar to the k8s scheduling framework. This coincides with our thinking.  

On the whole, these solutions solved most of our problems, but we also noticed that the related implementations were not merged into the main branch. But we review these implementations and discussions, and we believe this is the right direction. Considering that Koordinator has clear milestones for descheduler-related features, we will implement Koordinator's own descheduler independently of the upstream community. We try to use some of the designs in the [#753 PR](https://github.com/kubernetes-sigs/descheduler/issues/753) proposed by the community and we will follow the Koordinator's compatibility principle with K8s to maintain compatibility with the upstream community descheduler when implementing. Such as independent implementation can also drive the evolution of the upstream community's work on the descheduler framework. And when the upstream community has new changes or switches to the architecture that Koordinator deems appropriate, Koordinator will follow up promptly and actively.

### Goals

1. Implement Koordinator Descheduler following part of the design in [#753](https://github.com/kubernetes-sigs/descheduler/issues/753) proposed by the community

### Non-Goals/Future Work

1. Break any existing use cases of the Descheduler.

## Proposal

### Implementation Details/Notes/Constraints

#### Descheduler profile

The current descheduler configuration is too simple to support disabling or enabling plugins or supporting custom plugin configurations. The [PR #587](https://github.com/kubernetes-sigs/descheduler/pull/587) introducing descheduler profiles with v1alpha2 api version. We will use this proposal as Koordinator Descheduler's configuration API.

- The descheduler profile API supports user specify which extension points are enabled/disabled, alongside specifying plugin configuration. Including ability to configure multiple descheduling profiles.
- The descheduling framework configuration can be converted into an internal representation.
- To reduce need to specify value for every possible configuration, also defaulting serves as a recommended/opinionated settings for the plugins.

The K8s Descheduler Profile does not yet support custom eviction mechanisms, but Koordinator Descheduler allows users to configure eviction plug-ins using the `Evict` extension point in the Profile, allowing users to customize the eviction mechanism; Koordinator Descheduler also supports users to use the `Filter` extension in the Profile Configure different eviction filter plug-ins, which is similar to the `Evict` extension point in the K8s Descheduler Profile.

#### Abstract Evict and Filter interfaces

The `Evict` interface encapsulates specific eviction methods. The `Filter` interface encapsulates the criteria for determining whether pod eviction is allowed. The specific interfaces are defined as follows:

```go
type EvictPlugin interface {
	Plugin
	// Evict evicts a pod (no pre-check performed)
	Evict(ctx context.Context, pod *corev1.Pod, evictOptions EvictOptions) bool
}

type FilterPlugin interface {
	Plugin
	// Filter checks if a pod can be evicted
	Filter(pod *corev1.Pod) bool
	// PreEvictionFilter checks if pod can be evicted right before eviction
	PreEvictionFilter(pod *corev1.Pod) bool
}

// EvictOptions provides a handle for passing additional info to EvictPod
type EvictOptions struct {
	// PluginName represents the initiator of the eviction operation
	PluginName string
	// Reason allows for passing details about the specific eviction for logging.
	Reason string
	// DeleteOptions holds the arguments used to delete
	DeleteOptions *metav1.DeleteOptions
}
```

Although multiple Evict plugins can be configured in a Descheduler Profile, but only one Evict plugin instance is used. Koordinator descheduler will use MigrationController based on [PodMigrationJob](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20220701-pod-migration-job.md) as the default Evict plugin. 

The Evict plugin can get the `Filter` instance through the descheduler `Framework.Evictor()` method. The `Evict`, `Deschedule` and `Balance` plugins can use `Filter` to determine whether eviction is allowed before initiating Pod eviction. 
Multiple Filter plugins can be configured in the Descheduler Profile. When a plugin instance filter fails, it is considered that the Pod should not be evicted. 

`Filter` is theoretically stateless, but if there is state, the plugin implementer needs to ensure concurrency safety, because it is possible for the framework layer to execute Deschedule/Balance plugins concurrently, and these plugins will call the Filter interface concurrently.

For example, Deschedule/Balance plugins are used to implement custom descheduling strategies. These plugins generally select some Pods as candidates based on their own internal logic, and then call `Filter` to exclude some Pods. One reason for this is that multiple Deschedule/Balance plugins can do some linkage through Filter. For example, some plugins may have evicted a batch of Pods, if the next plugin does not call `Filter`, there may be a risk of failure. Moreover, `Filter` also has global common properties, for example, the replicas of unavailable of the workload to which the Pod belongs has exceeded maxUnavailable.

#### Plugin descheduler strategy

The current descheduler has some strategies. In [PoC #781](https://github.com/kubernetes-sigs/descheduler/pull/781), it is converted into `Plugin` and executed periodically. In this `periodic execution mode`, it is appropriate to abstract the policy for Pod and Node dimensions as `DeschedulePlugin` or `BalancePlugin`. The load hotspot descheduling capability that we will implement later can also implement the BalancePlugin interface.

The goals of both `Deschedule` and `Balance` Plugin are to evict a batch of Pods to obtain better orchestration status or runtime quality. The reason why they have two names is that the semantics of the scenarios they represent are different. For example, to evict pods that fail to run on some nodes to achieve a single goal or local optimum, it is more appropriate to use `Deschedule`; but if it involves balancing the state of a batch of nodes to obtain a global optimum, such as resource allocation balance, or load balance, etc., it is more appropriate to use `Balance`.

We also need to support the `event-triggered mode`, which means that descheduling is performed in the form of a Controller.
In some scenarios, CRD-oriented descheduling needs to be implemented. For example, different descheduling configurations are provided according to the workload. When some abnormality is detected in the workload, descheduling will be triggered. We can think of Controller as a special form of Plugin. When the descheduler is initialized, an instance is constructed through the plugin factory function like a normal Plugin, and then a similar Run method is called to start execution.

## Alternatives

## Implementation History

- 2022-07-01: Initial proposal
- 2022-07-15: Refactor proposal for review
- 2023-03-28: Append description of extension point responsibilities and relationships