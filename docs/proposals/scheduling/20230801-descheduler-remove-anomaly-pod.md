---
title: Descheduler Remove Anomaly Pods
authors:
  - "@eahydra"
reviewers:
  - "@hormes"
  - "@zwzhang0107"
  - "@FillZpp"
  - "@ZiMengSheng"
  - "@jasonliu747"
creation-date: 2023-08-01
last-updated: 2023-08-01
status: provisional
---

<!-- TOC -->

- [Descheduler Remove Anomaly Pods](#descheduler-remove-anomaly-pods)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [User stories](#user-stories)
        - [Story 1](#story-1)
        - [Story 2](#story-2)
        - [Story 3](#story-3)
        - [Story 4](#story-4)
    - [Proposal](#proposal)
        - [Abnormal characteristics](#abnormal-characteristics)
        - [Abnormal determination](#abnormal-determination)
        - [Pod Eviction](#pod-eviction)
        - [Node downgrade](#node-downgrade)
        - [Implementation Details](#implementation-details)
            - [Plugin configuration](#plugin-configuration)
        - [Observability](#observability)
        - [Grayscale/Stability](#grayscalestability)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)

<!-- /TOC -->

# Descheduler Remove Anomaly Pods

## Summary

Define a descheduling plugin to handle pods that start abnormally after scheduling.

## Motivation

There are always a small number of nodes in the K8s cluster that are abnormal, causing the newly created Pod to fail to start due to errors on some nodes after scheduling, such as CNI configuration exceptions, PVC/PV exceptions, image pull failures, PreStartHook execution failures, and possibly other unrecognized anomalies that exist, etc. These problems will magnify the problem in the HPA scenario, causing the effect of HPA to not meet expectations. Therefore, there needs to be a mechanism to solve or optimize this problem.

### Goals

1. Design a descheduling plugin that recognizes classified anomalies and safely evicts those Pods.
2. Supports downgrading or isolating abnormal nodes, and tries to avoid subsequent scheduling of new Pods to abnormal nodes.

### Non-Goals/Future Work

1. Infrequent or unrecognized abnormalities.

## User stories

### Story 1

The container image service is unavailable or network congestion causes a timeout, and the image still cannot be pulled after many retries.

### Story 2

PVC/PV is often used to mount the remote disk provided by the cloud, but there may be an exception in the cloud API service or the mounting requires the node to meet certain conditions but cannot meet them, resulting in continuous failure to mount the PV.

### Story 3

There is an undiscovered new abnormality in the operating system kernel or hardware of the node itself, and there is no corresponding fault-tolerant recovery mechanism, but you can still try to judge it by analyzing the information output by kubelet.

### Story 4

The abnormal recovery of node components is not so reliable. It is possible that the container has not been started for a long time due to improper policy configuration or inactive retry.


## Proposal

### Abnormal characteristics

Analysis of common startup exceptions shows that a few exceptions can be perceived through Pod Annotation or Pod Status, but most of them are transmitted through Pod Events, and even a few scenarios are reflected in PVC/PV Events. This means we need to identify abnormalities from K8s Events.

Most of these exceptions are concentrated in several common scenarios, which can be subdivided by the technical field to which the error belongs:

1. Network related. For example, Pod Sandbox setup network failed or timed out.
2. Storage-related, such as insufficient free space on the system disk; or abnormal PV mounting, the reason of Event reported by kubelet is FailedAttachVolume
3. Image pull timeout
4. Occasionally encountered errors such as OutOfcpu/OutOfmemory/OutOfsigma/eni.

According to these exception scenarios, error information and code analysis (mainly the implementation of kubelet reporting exceptions), the errors reported by kubelet can basically be divided into several categories:

1. FailedCreatePodSandbox
2. FailedAttachVolume/FailedMount
3. FailedToPullImage
4. There are several other less common types, such as FailedToCreateContainer.

Further analysis can also reveal that these exceptions are closely related to many external services. For example, if a container image fails to be pulled, it may be that the container image service is unavailable, or it may be due to network congestion that causes a timeout; When triggering the creation of a PV during scheduling, the PV may not be created due to the unavailability of the storage service.

In addition, in these scenarios, when the Pod is abnormal, Pod.Status.Phase is basically Pending, and the status of the Container is also not created or created but abnormal.

Moreover, it can also be found that although these scenarios are related to external services, the reliability of the components on the node is not as high as imagined, or the fault-tolerant mechanism or fault-tolerant policies cannot quickly recover these abnormalities, which eventually leads to users think that their expectations have not been met, and it may seriously affect the SLA promised because these exceptions cannot be fixed.

Therefore, for these scenarios, it is necessary to expel the abnormal Pod and reduce the node weight or isolate the node, and trigger recovery from the outside.

### Abnormal determination

According to the above abnormal characteristic analysis conclusions, frequently encountered scenarios can be detected through the `Event Reason` and `Event Message` reported by kubelet.

A basic process for identifying abnormalies:

1. When the Pod is scheduled, process the Pod-related Events.
2. If the Event Reason is in the configured `eventReasons` and appears `N times consecutively`, it will be judged as abnormal.
3. If `reasons` are configured and identified from the following information sources, abnormalies can be determined:
   - Event Message
   - Pod.Status.Reason
   - Pod.Status.ContainerStatus.State.Waiting.Reason
   - Pod.Status.ContainerStatus.State.Terminated.Reason
   - Pod.Status.InitContainerStatus.State.Waiting.Reason
   - Pod.Status.InitContainerStatus.State.Terminated.Reason
   - and special annotation

### Pod Eviction

After identifying the abnormalies of Pod, call the `Evict` interface in koord-descheduler to initiate eviction, so that the existing filters and Reservation can be reused. By default, resources will be reserved before eviction, and resources will not be evicted if resource reservation fails.

### Node downgrade

When Pod abnormalies are identified, the plugin can decide whether to downgrade the node according to the configuration.

We can add a label to the node and use the `NodeLabel` plugin provided by k8s scheduler in the past to downgrade (of course, we can also consider introducing a new webhook to inject into the newly created Pod with Node Affinity, try to schedule nodes but anti-affnity these labels).

Or we can taint the node to refuse to schedule new Pods.

After a certain period of time, we can remove these newly added labels or taints, and restore the state of the node.

### Implementation Details

#### Plugin configuration

Learn from some parameters of RemoveFailedPods in k8s descheduler:

| Name                    | Type                      | Comment                                                                                     |
| ----------------------- | ------------------------- | ------------------------------------------------------------------------------------------- |
| minPodStartupSeconds    | uint                      | Minimum startup window time, default 5 minutes                                                               |
| excludeOwnerKinds       | list(string)              | Exclude some special workload types, such as StatefulSet                                            |
| eventReasons            | list(string)              | Event Reason with obvious characteristics, such as FailedCreatePodSandbox                                    |
| reasons                 | list(string)              | Some special message characteristics                                                                     |
| annotationHints         | list(string)              | A key with an annotation that can give some error messages |
| includingInitContainers | bool                      | Whether to include initContainer                          |
| namespaces              | (see [namespace filtering](https://github.com/kubernetes-sigs/descheduler#namespace-filtering)) | The namespace to be processed or not to be processed                                                          |
| labelSelector           | (see [label filtering](https://github.com/kubernetes-sigs/descheduler#label-filtering))     | Which Pods to process, if not set, it will default to all Pods                             |
| anomalyCondition        | object                    | Conditions for abnormal judgment                                                                                |
| nodeDowngradePolicy     | object                    | Node downgrade policies                                                                                |

- `anomalyCondition` is defined as follows:
By default, if the problem is identified 5 times in a row, it is considered abnormal

```go
type AnomalyCondition struct {
	// Timeout indicates the expiration time of the abnormal state, the default is 1 minute
	Timeout *metav1.Duration `json:"timeout,omitempty"`
	// ConsecutiveAbnormalities indicates the number of consecutive abnormalities
	ConsecutiveAbnormalities uint32 `json:"consecutiveAbnormalities,omitempty"`
	// ConsecutiveNormalities indicates the number of consecutive normalities
	ConsecutiveNormalities uint32 `json:"consecutiveNormalities,omitempty"`
}
```

- The `nodeDowngradePolicy` is defined as follows:
    - The default weight reduction method is label
    - Default ttl is 5 minutes

```go
type NodeDowngradeMethod string

const (
	NodeDowngradeMethodTaint NodeDowngradeMethod = "taint"
	NodeDowngradeMethodLabel NodeDowngradeMethod = "label"
)

type NodeDowngradePolicy struct {
	Method NodeDowngradeMethod `json:"method,omitempty"`
	TTL    *metav1.Duration    `json:"ttl,omitempty"`
}
```

### Observability

In addition to descheduler's existing indicators such as eviction rate and times, the following metrics need to be added for observation:

1. The number of recently identified abnormal Pods.
2. The number of Pods that are suspected to be abnormal recently but have not yet been determined to be abnormal.
3. Count the number of abnormalities according to reason.
4. The number of nodes that have been downgraded.

### Grayscale/Stability

The plugin supports configuration to control the grayscale range by namespace/labelSelector.


## Alternatives

There is a plugin called `RemoveFailedPods` in K8s descheduler that also solves similar problems, but it still cannot meet our needs, but we can learn some ideas from RemoveFailedPods. The main reason why it cannot meet the requirements is that the RemoveFailedPods plugin only handles Pods whose Pod.Status.Phase is equal to Failed. In this regard, it cannot meet the more common startup phase exception scenarios.

## Implementation History

- 2023-08-01: Initial proposal sent for review