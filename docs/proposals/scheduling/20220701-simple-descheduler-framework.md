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
last-updated: 2022-07-15
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
            - [Abstract PodEvictor interface](#abstract-podevictor-interface)
            - [Plug-in descheduler strategy](#plug-in-descheduler-strategy)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

## Glossary

## Summary

This proposal is based on the K8s community's [descheduler](https://github.com/kubernetes-sigs/descheduler) to design and implement the descheduler framework required by the koordinator.

## Motivation

The existing [descheduler](https://github.com/kubernetes-sigs/descheduler) in the community can solve some problems, but we think that there are still many aspects of the descheduler that can be improved, for example, it only supports the mode of periodic execution, and does not support the event-triggered mode. It is not possible to extend and configure custom rescheduling strategies without invading the existing code of descheduler like kube-scheduler; it also does not support implementing custom evictor. 

We also noticed that the K8s descheduler community also found these problems and proposed corresponding solutions such as [#753 Descheduler framework Proposal](https://github.com/kubernetes-sigs/descheduler/issues/753) and [PoC #781](https://github.com/kubernetes-sigs/descheduler/pull/781). The K8s descheduler community tries to implement a descheduler framework similar to the k8s scheduling framework. This coincides with our thinking.  

On the whole, these solutions solved most of our problems, but we also noticed that the related implementations were not merged into the main branch. But we review these implementations and discussions, and we believe this is the right direction. Considering that Koordiantor has clear milestones for descheduler-related features, we will implement Koordinator's own descheduler independently of the upstream community. We try to use some of the designs in the [#753 PR](https://github.com/kubernetes-sigs/descheduler/issues/753) proposed by the community and we will follow the Koordinator's compatibility principle with K8s to maintain compatibility with the upstream community descheduler when implementing. Such as independent implementation can also drive the evolution of the upstream community's work on the descheduler framework. And when the upstream community has new changes or switches to the architecture that Koordinator deems appropriate, Koordinator will follow up promptly and actively.

### Goals

1. Implement Koordinator Descheduler following part of the design in [#753](https://github.com/kubernetes-sigs/descheduler/issues/753) proposed by the community

### Non-Goals/Future Work

1. Break any existing use cases of the Descheduler.

## Proposal

### Implementation Details/Notes/Constraints

#### Descheduler profile

The current descheduler configuration is too simple to support disabling or enabling plugins or supporting custom plugin configurations. The [PR #587](https://github.com/kubernetes-sigs/descheduler/pull/587) introducing descheduler profiles with v1alpha2 api version. We will use this proposal as Koordiantor Descheduler's configuration API.

- The descheduler profile API supports user specify which extension points are enabled/disabled, alongside specifying plugin configuration. Including ability to configure multiple descheduling profiles.
- The descheduling framework configuration can be converted into an internal representation.
- To reduce need to specify value for every possible configuration, also defaulting serves as a recommended/opinionated settings for the plugins.

#### Abstract PodEvictor interface

Currently, descheduler has split `Pod Evictor` and `Evictor Filter`. Users can inject `Evictor Filter` on demand, and the plug-in calls `Evictor Filter` when selecting abnormal Pods to select Pods that meet the requirements and calls `Pod Evictor` to initiate eviction. At present, `Pod Evictor` has not been abstracted as an interface. We adopt the solution in [PoC #781](https://github.com/kubernetes-sigs/descheduler/pull/781) to abstract an `Evictor interface`. And refer to [PR #885](https://github.com/kubernetes-sigs/descheduler/pull/885) to add an `EvictOptions` paramters.  We can implement custom Evictor based on [PodMigrationJob](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20220701-pod-migration-job.md). 

The `Evictor` interface defined as follows:

```go
type EvictOptons struct {
  Strategy string
  Reason   string
}

type Evictor interface {
  Evict(context.Context, *v1.Pod, options EvictOptions) bool
}
```

#### Plug-in descheduler strategy

The current descheduler has some strategies. In [PoC #781](https://github.com/kubernetes-sigs/descheduler/pull/781), it is converted into `Plugin` and executed periodically. In this `periodic execution mode`, it is appropriate to abstract the policy for Pod and Node dimensions as `DeschedulePlugin` or `BalancePlugin`. The load hotspot descheduling capability that we will implement later can also implement the BalancePlugin interface.

We also need to support the `event-triggered mode`, which means that descheduling is performed in the form of a Controller.
In some scenarios, CRD-oriented descheduling needs to be implemented. For example, different descheduling configurations are provided according to the workload. When some abnormality is detected in the workload, descheduling will be triggered. We can think of Controller as a special form of Plugin. When the descheduler is initialized, an instance is constructed through the plugin factory function like a normal Plugin, and then a similar Run method is called to start execution.

## Alternatives

## Implementation History

- 2022-07-01: Initial proposal
- 2022-07-15: Refactor proposal for review
