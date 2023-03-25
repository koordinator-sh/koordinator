---
title: Resource Reservation
authors:
  - "@saintube"
reviewers:
  - "@eahydra"
  - "@hormes"
  - "@jasonliu747"
  - "@zwzhang0107"
creation-date: 2022-06-09
last-updated: 2023-04-01
---
# Resource Reservation

## Table of Contents

<!-- TOC -->

- [Resource Reservation](#resource-reservation)
    - [Table of Contents](#table-of-contents)
    - [Glossary](#glossary)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [User Stories](#user-stories)
            - [Story 1](#story-1)
            - [Story 2](#story-2)
            - [Story 3](#story-3)
            - [Story 4](#story-4)
        - [API](#api)
        - [Implementation Details](#implementation-details)
            - [Schedule Reservations](#schedule-reservations)
            - [Allocate Reserved Resources](#allocate-reserved-resources)
            - [Sync Reservation Status](#sync-reservation-status)
            - [Expiration and Cleanup](#expiration-and-cleanup)
            - [Use Cases](#use-cases)
                - [Usage in Preemption](#usage-in-preemption)
                - [Usage in Descheduling](#usage-in-descheduling)
                - [Usage in Pre-allocation](#usage-in-pre-allocation)
        - [Risks and Mitigations](#risks-and-mitigations)
    - [Tests](#tests)
        - [E2E Tests](#e2e-tests)
    - [Unsolved Problems](#unsolved-problems)
    - [Alternatives](#alternatives)
        - [Use a pause pod with a low priority to reserve resources](#use-a-pause-pod-with-a-low-priority-to-reserve-resources)
    - [Implementation History](#implementation-history)
    - [References](#references)

<!-- /TOC -->

## Glossary

Scheduling Framework: https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/

## Summary

A scheduling mechanism and its API is provided to reserve node resources for pods may not be created yet.

## Motivation

Pods are fundamental units for allocating node resources in Kubernetes, which bind resource requirements with business logic. The scheduler is not able to reserve node resources for specific pods or workloads. We may try using a [fake pod](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-can-i-configure-overprovisioning-with-cluster-autoscaler) to prepare resources by the preemption mechanism. However, fake pods can be preempted by any scheduled pods with higher priorities, which make resources get scrambled unexpectedly.

In Koordinator, a resource reservation mechanism is proposed to enhance scheduling and especially benefits scenarios below:

1. Preemption: Existing preemption does not guarantee that only preempting pods can allocate preempted resources. With a reservation, the scheduler should be able to "lock" resources preventing from allocation of other pods with the same or higher priority.
2. Descheduling: For the descheduler, it is better to ensure sufficient resources with the reservation before pods get rescheduled. Otherwise, descheduled pods may not be runnable anymore and make the belonging application disrupted.
3. Horizontal scaling: Using reservation to achieve more deterministic horizontal scaling. e.g. Submit a reservation and make sure it is available before scaling up replicas.
4. Resource Pre-allocation: Sometimes we want to pre-allocate node resources for future resource demands even if the resources are not currently allocatable. Reservation can help with this and it should make no physical cost.

### Goals

- Define the basic API of resource reservation for *Motivations<1,2,3>*, extensible for supporting *Motivation<4>* in the future.
- Provide a scheduler plugin that implements above reservation mechanism.

### Non-Goals/Future Work

- Detailed design of reservative preemption/descheduler/horizontal scaler/pre-allocation.
- Modify kubelet admission control for reservation objects.

## Proposal

### User Stories

#### Story 1

As a Kubernetes developer, I want to enhance the current **preemption** mechanism since preempted resources may be allocated by pods other than the preemptor. The scheduler can create a reservation for the preempting pods, so the ownership of preempted resources can be guaranteed, making the preemption more reliable.

#### Story 2

As a cluster administrator, I want to use **descheduler** to migrate pods that are placed abnormally to somewhere they could "live better" and fulfill orchestration requirements of the app. e.g. Move pods on a over-utilized node to idler nodes and bind CPUs of same NUMA node. Reservations can be created before rescheduling pods, helping ensure there are sufficient resources and well placement.

#### Story 3

As an application administrator, I want to make the **horizontal scaling** of my app more deterministic by submitting reservations before a scale-up. Besides, I can also reserve resources after a scale-down for future demands. It is useful especially when we want a guaranteed scale-up of applications for the coming business peak.

#### Story 4

As a cluster administrator, I want to **pre-allocate** node resources for future usage no matter whether they are available now or not. I want to allocate the future free resources but do not disrupt the running of scheduled pods. Reservation can be made to pre-allocate resources since it makes no physical cost to the node. It may be in a `Waiting` state. When there is enough space for the reservation, it will become `Available` for the owner pods' scheduling.

### API

In this section, a Custom Resource Definition (CRD) named `Reservation` is proposed to allow the scheduler to reserve node resources for specific pods.

![image](/docs/images/resource-reservation.svg)

```go
type Reservation struct {
    metav1.TypeMeta `json:",inline"`
    // A Reservation object is non-namespaced.
    // It can reserve resources for pods of any namespace. Any affinity/anti-affinity of reservation scheduling can be
    // specified in the pod template.
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec              ReservationSpec   `json:"spec,omitempty"`
    Status            ReservationStatus `json:"status,omitempty"`
}

type ReservationSpec struct {
    // Template defines the scheduling requirements (resources, affinities, images, ...) processed by the scheduler just
    // like a normal pod.
    // If the `template.spec.nodeName` is specified, the scheduler will not choose another node but reserve resources on
    // the specified node.
    // +kubebuilder:pruning:PreserveUnknownFields
    // +kubebuilder:validation:Schemaless
    // +kubebuilder:validation:Required
    Template *corev1.PodTemplateSpec `json:"template"`
    // Specify the owners who can allocate the reserved resources.
    // Multiple owner selectors and ORed.
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinItems=1
    Owners []ReservationOwner `json:"owners"`
    // Time-to-Live period for the reservation.
    // `expires` and `ttl` are mutually exclusive. Defaults to 24h. Set 0 to disable expiration.
    // +kubebuilder:default="24h"
    // +optional
    TTL *metav1.Duration `json:"ttl,omitempty"`
    // Expired timestamp when the reservation is expected to expire.
    // If both `expires` and `ttl` are set, `expires` is checked first.
    // `expires` and `ttl` are mutually exclusive. Defaults to being set dynamically at runtime based on the `ttl`.
    // +optional
    Expires *metav1.Time `json:"expires,omitempty"`
    // By default, the resources requirements of reservation (specified in `template.spec`) is filtered by whether the
    // node has sufficient free resources (i.e. Reservation Request <  Node Free).
    // When `preAllocation` is set, the scheduler will skip this validation and allow overcommitment. The scheduled
    // reservation would be waiting to be available until free resources are sufficient.
    // +optional
    PreAllocation bool `json:"preAllocation,omitempty"`
    // When `AllocateOnce` is set, the reserved resources are only available for the first owner who allocates successfully
    // and are not allocatable to other owners anymore. Defaults to true.
    // +kubebuilder:default=true
    // +optional
    AllocateOnce bool `json:"allocateOnce,omitempty"`
}

// ReservationTemplateSpec describes the data a Reservation should have when created from a template
type ReservationTemplateSpec struct {
    // Standard object's metadata.
    // +kubebuilder:pruning:PreserveUnknownFields
    // +optional
    metav1.ObjectMeta `json:"metadata,omitempty"`

    // Specification of the desired behavior of the Reservation.
    // +optional
    Spec ReservationSpec `json:"spec,omitempty"`
}

type ReservationStatus struct {
    // The `phase` indicates whether is reservation is waiting for process, available to allocate or failed/expired to
    // get cleanup.
    // +optional
    Phase ReservationPhase `json:"phase,omitempty"`
    // The `conditions` indicate the messages of reason why the reservation is still pending.
    // +optional
    Conditions []ReservationCondition `json:"conditions,omitempty"`
    // Current resource owners which allocated the reservation resources.
    // +optional
    CurrentOwners []corev1.ObjectReference `json:"currentOwners,omitempty"`
    // Name of node the reservation is scheduled on.
    // +optional
    NodeName string `json:"nodeName,omitempty"`
    // Resource reserved and allocatable for owners.
    // +optional
    Allocatable corev1.ResourceList `json:"allocatable,omitempty"`
    // Resource allocated by current owners.
    // +optional
    Allocated corev1.ResourceList `json:"allocated,omitempty"`
}

// ReservationOwner indicates the owner specification which can allocate reserved resources.
// +kubebuilder:validation:MinProperties=1
type ReservationOwner struct {
    // Multiple field selectors are ANDed.
    // +optional
    Object *corev1.ObjectReference `json:"object,omitempty"`
    // +optional
    Controller *ReservationControllerReference `json:"controller,omitempty"`
    // +optional
    LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
}

type ReservationControllerReference struct {
    // Extend with a `namespace` field for reference different namespaces.
    metav1.OwnerReference `json:",inline"`
    Namespace             string `json:"namespace,omitempty"`
}

type ReservationPhase string

const (
    // ReservationPending indicates the Reservation has not been processed by the scheduler or is unschedulable for
    // some reasons (e.g. the resource requirements cannot get satisfied).
    ReservationPending ReservationPhase = "Pending"
    // ReservationAvailable indicates the Reservation is both scheduled and available for allocation.
    ReservationAvailable ReservationPhase = "Available"
    // ReservationSucceeded indicates the Reservation is scheduled and allocated for a owner, but not allocatable anymore.
    ReservationSucceeded ReservationPhase = "Succeeded"
    // ReservationWaiting indicates the Reservation is scheduled, but the resources to reserve are not ready for
    // allocation (e.g. in pre-allocation for running pods).
    ReservationWaiting ReservationPhase = "Waiting"
    // ReservationFailed indicates the Reservation is failed to reserve resources, due to expiration or marked as
    // unavailable, which the object is not available to allocate and will get cleaned in the future.
    ReservationFailed ReservationPhase = "Failed"
)

type ReservationConditionType string

const (
    ReservationConditionScheduled ReservationConditionType = "Scheduled"
    ReservationConditionReady     ReservationConditionType = "Ready"
)

type ConditionStatus string

const (
    ConditionStatusTrue    ConditionStatus = "True"
    ConditionStatusFalse   ConditionStatus = "False"
    ConditionStatusUnknown ConditionStatus = "Unknown"
)

const (
    ReasonReservationScheduled     = "Scheduled"
    ReasonReservationUnschedulable = "Unschedulable"

    ReasonReservationAvailable = "Available"
    ReasonReservationSucceeded = "Succeeded"
    ReasonReservationExpired   = "Expired"
)

type ReservationCondition struct {
    Type               ReservationConditionType `json:"type,omitempty"`
    Status             ConditionStatus          `json:"status,omitempty"`
    Reason             string                   `json:"reason,omitempty"`
    Message            string                   `json:"message,omitempty"`
    LastProbeTime      metav1.Time              `json:"lastProbeTime,omitempty"`
    LastTransitionTime metav1.Time              `json:"lastTransitionTime,omitempty"`
}
```

### Implementation Details

#### Schedule Reservations

A `Reservation` object has its scheduling requirements like a pod. Ideally, A `Reservation` object should get processed directly by the scheduler like a pod. However, it can require a series of modifications on [scheduling framework](#glossary), losing the compatibility with standard kube-scheduler, kubelet, autoscaler, etc. In the koord-scheduler, we fake one *reservation pod* for one `Reservation` inside the scheduler to fulfill general scheduling plugins (NodeResources, NodeAffinity, InterPodAffinity, TaintTolerations, ...). The scheduling framework can handle `Reservation` objects by processing fake pods in both [scheduling cycle and binding cycle](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/#scheduling-cycle-binding-cycle).

A fake pod inside the scheduler can construct the same affinity/anti-affinity constraints as owner pods, which may change the reservation result. To handle this problem, koord-scheduler extends the framework to skip check of pod affinity/anti-affinity and pod topology spread for existing reservations.

A reservation specified `PreAllocation` intends to pre-allocate resources on nodes. The scheduler will skip its filtering of node resources in the scheduling cycle. However, the scheduled reservation will be `Waiting` to be `Available` until there are enough resources to fulfill its requests.

If all nodes are unscheduled for the reservation, the scheduler keeps its status as `Pending` and sets `Conditions` with the failure message.

Once the scheduling decision has been made, the corresponding `Reservation` object is updated with a new status indicating whether the reservation available or not. The fake pod does not expose to other components, and the kubelet without modification does not perceive a `Reservation` assigned. Fortunately, a `Reservation` does not need to be executable on the node, so existing containers can keep running as usual without additional admissions.

If a reservation has set the `nodeName` (inside the `template` field), the scheduler is responsible for checking if the node can fulfill the reservation since kubelet does not do admissions for the reservation.

If a reservation disable `AllocateOnce`, just only one same reusable reservation can be scheduled on a same node.

#### Allocate Reserved Resources

Let's call the reservation is *matched* for a pod if:

1. The reservation is available.
2. The pod matches the reservation owner spec.

When the reservation plugin is enabled, the scheduler checks for every scheduling pod if there are matched reservations on a node. 
If the resources reserved by a Reservation matching a Pod or the remaining resources of the Reservation may be smaller than the requested resources of the Pod, the Pod will first allocate the remaining available resources from the Reservation, and then allocate for remaining requested resources from the node. This can ensure that Pods can be scheduled as successfully as possible, and resources will not be unable to be allocated due to resource fragmentation. In this way, it is convenient and flexible to support users to reserve only a certain type of resource. For example, the Reservation reserves 4000m CPU, but the Pod actually needs 4000m CPU and 4Gi Memory. In this case, the Pod needs to allocate resources from both the Reservation and the Node.

Reservation also supports special resources such as reserved ports. After a Reservation successfully reserves ports, all reserved but unallocated ports will be released when the matching Pod is scheduled. In this way, the Pod can apply for the reserved ports from this Reservation, and can also apply for other required ports from other matching Reservations and Nodes.

The reservation plugin implements custom tranformers defined in package `frameworkext`. For more details, please to refer to documents [Enhanced Scheduler Extension#custom transformer extension points to support reservation scheduling](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20220513-enhanced-scheduler-extension.md#custom-transformer-extension-points-to-support-reservation-scheduling)

When a pod is scheduled on a node with allocatable reservations, it allocates resources belonging to one of reservations. With a `Score` plugin implemented, the scheduler prefers pods to schedule on nodes which have more allocatable reserved resources. To pick one of reservations, we choose the one which can get most reserved resources allocated (i.e. MostAllocated). And the scheduler also annotates the pod with the reservation info. 

If the reservation sets `AllocateOnce`, the reserved resources can get allocated only once. The reservation's phase becomes `Succeeded` when an owner uses the reservation successfully. Then the reservation is considered unavailable, and other owners cannot allocate from it anymore.
And if there are multiple Reservations that can be used by Pods on the node (these Reservations all disabled `AllocateOnce`, and they are reusable), then only the Reservation with the earliest creation time will be used. A Reservation that can be reused is expected to be used by multiple Pods on one node, but it does not make sense to create multiple such Reservation instances on the same node. Another reason is that if there are multiple reusable Reservations on the same node, a Pod may be allocated from multiple Reservations due to the resource fragmentation of the Reservation itself, which is not what we want.

#### Sync Reservation Status

When a pod assigned with one specified Reservation, the reservaton controller reconciles the reservation to update the `status.currentOwners` and `status.allocated`. 
If the Reservation enables `spec.allocateOnce`, the controller will mark the reservation to `Succeeded` that the reservation cannot be allocated anymore, even if the assigned Pod has been deleted.

#### Expiration and Cleanup

When a reservation has been created for a long time exceeding the `TTL` or `Expires`, the scheduler updates its status as `Expired`. For expired reservations, the scheduler will cleanup them with a custom garbage collection period.

When a node is deleted, the available and waiting reservations on the node should be marked as `Expired` since they are not allocatable any more.

#### Use Cases

To generally reserve node resources, submit a `Reservation` and set the pod template in the field `spec.template`. Then the koord-scheduler will update this `Reservation` with the scheduling result and the resources will get reserved.

To be more specific,

- `spec.template` specifies the fundamental resource requirements of a reservation. The scheduler will schedule the fake pod based on the template.
- `spec.owners` specifies which kinds of pods can use the reservation.
- `spec.ttl` and `expires` specifies the expiration for the reservation.
- `spec.preAllocation` indicates whether the scheduler should filter with its resource requirements. Otherwise, the pre-allocation of node resources is allowed, and the reservation will become available until there are sufficient resources.
- `spec.allocateOnce` indicates that the reserved resources can get allocated only once.
- `status.phase` is marked as `Pending` when the Reservation is created. And it is marked as `Available` when the Reservation is successfully scheduled.
- `status.conditions` shows why the reservation is unscheduled.
- When a Reservation is `Available` on the node, only specified pods can allocate the reserved resources.

##### Usage in Preemption

The [Priority Preemption](https://kubernetes.io/docs/concepts/scheduling-eviction/pod-priority-preemption/#preemption) happens in the PostFilter phase trying to make preemptive pods schedulable by evicting low-priority pods. When a pod succeeds the preemption, the pod `status` will be patched with a *nominated node* where the scheduler do the eviction. However, the preemptor's nominated node is not always the same as the scheduled node, since the scheduler does not reserve resources for the preemptor.
To ensure the preemptive resources are for the preemptor, firstly the scheduler can create a reservation that both sets `owners` with the preemptor pod and relevant affinity rules for reserving resources of the preempts. Then the scheduler evict pods, and the reservation will become `Available` once the resources are released. Finally, the preemptor pods can get scheduled on the nodes with preemptive resource reserved.

##### Usage in Descheduling

Before a pod is rescheduled, the descheduler can create a reservation that sets `template` and `owners` for the candidate and enables `allocateOnce` in most cases. When the reservation becomes `Available`, the descheduler can assign the pod to allocate the reserved resources. Once the pod allocate successfully, the reservation becomes `Succeeded` and no longer hold the resources. This solves the problem in which the rescheduled pod has stopped at the old node but cannot run on the new node. Moreover, the descheduler can migrate resources between pods by setting the `preAllocation` field.

##### Usage in Pre-allocation

Reservations with `preAllocation` specified allow users to pre-allocate the node resources from running pods. The `status.phase` of the reservation is set as `Waiting` until the resources are released, indicating that its availability is conditional. Once the referenced pods have terminated, the `phase` is `Available` for owners, and the pre-allocation succeeds.

### Risks and Mitigations

Kubelet without any modification possibly ignore `Reservation` objects in predicate admission, which increases the chance of unexpected overcommitment at nodes. `Reservation` does not require any physical resources to be executable, so the overcommitment is mainly a problem only when pods get scheduled with `Reservation` and start to run, which is somewhat easier to mitigate since Kubelet do admit these pods. To further descrease the possibility of unexpected overcommitment or pods admit failures, we could use resource estimation for in-flight pods, balance pods to the nodes with less reserved resources, etc.

## Tests

### E2E Tests

E2E test cases for InterPodAffinity

- Reservation reserved with Pod Affinity, owner Pod has same Pod Affinity
- Reservation reserved with Pod Affinity, multiple owner Pods have same Pod Affinity
- Reservation reserved with Pod AntiAffinity, owner Pod has same Pod AntiAffinity
- Reservation reserved with Pod Anti Affinity, multiple owner Pods have the same Pod Anti Affinity
- Reservation reserved with Pod Affinity, non-owner Pod has same Pod Affinity
- Reservation reserved with Pod AntiAffinity, non-owner Pod has same Pod AntiAffinity

E2E test cases for PodTopologySpread

- Reservation reserved with DoNotSchedule topology spread policy, multiple owner Pods equal to the number of nodes use the same Spread strategy

E2E test cases for NodeResources

The following cases are all running on the 16000m CPU and 32G Memory machine

- Reservation reserved 4000m CPU and 4Gi Memory, owner Pod has same resource requirments
- Reservation reserved 4000m CPU and 4Gi Memory, owner Pod requests 4000m CPU
- Reservation reserved 4000m CPU and 4Gi Memory, owner Pod requests 4000m CPU and 1Gi Memory
- Reservation reserved 4000m CPU, owner Pod requests 4000m CPU and 1Gi Memory
- Reservation reserved 4000m CPU, owner Pod requests 6000m CPU
- Reservation reserved 8000m CPU and disable AllocateOnce, two owner Pods each requests 4000m CPU
- Reservation reserved 6000m CPU and disable AllocateOnce, two owner Pods each requests 4000m CPU
- Reservation reserved 4000m CPU, non-owner Pod requests 4000m CPU
- Reservation reserved 4000m CPU, owner Pod requests 2000m CPU, and non-owner Pod requests 2000m CPU

E2E test cases for NodePorts

- Reservation reserved three ports: 7777/8888/9999, owner Pod A requests port 7777 and owner Pod B requests port 8888
- Reservation A reserved port 7777, Reservation B reserved port 8888, owner Pod A requests port 7777 and owner Pod B requests port 8888
- Reservation reserved three ports: 7777/8888/9999, non-owner Pod requests port 7777

E2E test cases for VolumeRestrictions

- Reservation reserved with PVC, AccessMode is ReadWriteOncePod, owner Pod has the same PVC

E2E test cases for NodeVolumeLimits

- Reservation reserved with Dynamic Provisining Volume, owner Pod has same PVC

E2E test cases for NUMANodeResources

- Reservation reserved 4 cores, owner Pod has same requests
- Reservation reserved 4 cores, owner Pod requests 6 cores
- Reservation reserved 4 cores, disable AllocateOnce, two owner Pods each request 2 cores
- Reservation reserved 4 cores, disable AllocateOnce, two owner Pods each request 3 cores

E2E test cases for DeviceShare

- Reservation reserved one GPU instance, owner Pod has same requests
- Reservation reserved one GPU instance, two owner Pods each request 50% GPU resources
- Reservation reserved one GPU instance, one owner Pod requests 60% GPU resources, another one owner Pod requests 50% GPU resources

## Unsolved Problems

As stated above, `Reservation` can generate the same pod affinity/anti-affinity rules as the owner pods. The problem gets resolved in the koord-scheduler by extending scheduling framework, but it still limits the standard kube-scheduler.

And, it is not supported that Reservation triggers the creation of PV during the reservation phase, but it can reserve the allocatable counts of CSINode.

## Alternatives

### Use a `pause` pod with a low priority to reserve resources

Reserving resources with [`pause` pods with very low assigned priority](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#how-can-i-configure-overprovisioning-with-cluster-autoscaler) does work when the preemption can be precisely enabled for specific pods. In the example of cluster autoscaler, `pause` pods are helpful when we need to overprovision resources to prevent idle nodes from scaling down by CA. However, a `pause` pod has no reservation guarantee except `priority`. As declared above, many scenarios require reservations to rely on other pod characteristics (e.g. names, namespaces, labels, priorityClass), where `pause` pods cannot meet the demands.

## Implementation History

- [X]  06/09/2022: Open PR for initial draft
- [X]  06/14/2022: Sent proposal for review
- [X]  07/20/2022: Update design details
- [X]  08/08/2022: Update allocateOnce API
- [X]  25/03/2023: Update the API and E2E Tests

## References

1. [Kueue Pod Resource Reservation](https://docs.google.com/document/d/1sbFUA_9qWtorJkcukNULr12FKX6lMvISiINxAURHNFo)
