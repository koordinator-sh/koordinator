---
title: Dynamic Reservation Provisioning
authors:
  - "@eahydra"
reviewers:
  - "@hormes"
  - "@FillZpp"
creation-date: 2023-12-05
last-updated: 2023-12-05
status: provisional
---

<!-- TOC -->

- [Dynamic Reservation Provisioning](#dynamic-reservation-provisioning)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
  - [User stories](#user-stories)
    - [Use Case 1: Just-in-Time Resource Allocation for High-Priority Jobs](#use-case-1-just-in-time-resource-allocation-for-high-priority-jobs)
    - [Use Case 2: Efficient Resource Utilization for Fluctuating Workloads](#use-case-2-efficient-resource-utilization-for-fluctuating-workloads)
    - [Use Case 3: Simplified Operations for DevOps Teams](#use-case-3-simplified-operations-for-devops-teams)
    - [Use Case 4: On-Demand Scaling for Microservices](#use-case-4-on-demand-scaling-for-microservices)
  - [Proposal](#proposal)
    - [Overview](#overview)
    - [Fault tolerance](#fault-tolerance)
    - [API](#api)
      - [Reservation Template Annotation](#reservation-template-annotation)
      - [New Reservation Requirement Annotaton](#new-reservation-requirement-annotaton)
    - [Observability](#observability)
      - [Events](#events)
      - [Metrics](#metrics)
  - [Implementation History](#implementation-history)

<!-- /TOC -->

# Dynamic Reservation Provisioning

## Summary

The Koordinator's Reservation CRD provides a mechanism for reserving resources on nodes. However, it currently lacks the capability for dynamic provisioning based on immediate workload demands. This proposal introduces Dynamic Reservation Provisioning (DRP), a new mechanism that enables the automatic creation of Reservation CRD instances through interaction with an external provisioner. DRP allows for the on-the-fly creation of Reservation CRD instances to meet the resource requirements of incoming Pods that carry a specific annotation.

## Motivation

The current Reservation mechanism in Koordinator requires users to create Reservation objects prior to the scheduling of workloads. While this approach addresses many use cases effectively, there exist scenarios where no suitable Reservation objects are available at the time of scheduling, yet there is a need for resources to be reserved upon successful scheduling of a Pod.

In such cases, users may expect a Pod to be scheduled with a guarantee that a Reservation will be created post-scheduling to secure the required resources. This expectation highlights a gap in the current Reservation mechanism, which does not support the dynamic creation of Reservation objects based on real-time scheduling events.

The introduction of Dynamic Reservation Provisioning (DRP) aims to bridge this gap by allowing the Koordinator scheduler to handle situations where an upfront Reservation is not available. DRP will enable the scheduler to signal an external controller to create a Reservation object as soon as a Pod requiring such resources is successfully scheduled. This capability ensures that all workloads can be accommodated even when their resource needs cannot be anticipated in advance, thereby improving the flexibility and robustness of resource management within the Koordinator ecosystem.

The motivation for this proposal is to provide a seamless and automated solution to dynamically provision Reservation objects when needed, overcoming the limitations of the current static reservation process, and to support a broader range of workload scenarios within Koordinator.

### Goals

1. Introduce a means of defining dynamic reservation templates through Pod annotations.
2. Define a protocol and process for the scheduler to interact with an external provisioner, which is responsible for creating actual Reservation CRD instances.

### Non-Goals/Future Work

1. None

## User stories

### Use Case 1: Just-in-Time Resource Allocation for High-Priority Jobs

Scenario: A high-priority job needs to be scheduled immediately, but there are no pre-created Reservation CRDs that can guarantee the resources it needs.

Outcome with DRP: As the scheduler attempts to place the Pod for this job, DRP triggers the creation of a Reservation CRD tailored to the job's resource requirements. The job is scheduled without delay, ensuring that critical workloads receive the resources they need without the need for pre-planning.

### Use Case 2: Efficient Resource Utilization for Fluctuating Workloads

Scenario: Workloads in a cluster exhibit significant variance in resource demands, making it difficult to predict and pre-create suitable Reservation CRDs.

Outcome with DRP: DRP allows the system to adapt to fluctuating workloads by creating Reservations dynamically. This results in better resource utilization and avoids the overhead of managing a multitude of static reservations that may not align with actual usage.

### Use Case 3: Simplified Operations for DevOps Teams

Scenario: DevOps teams need to deploy applications quickly without being bottlenecked by reservation management.

Outcome with DRP: Teams can deploy Pods with annotations indicating their resource requirements, and DRP ensures the necessary Reservation CRDs are created in lockstep with Pod scheduling. This reduces the operational complexity for DevOps teams and accelerates the deployment pipeline.

### Use Case 4: On-Demand Scaling for Microservices

Scenario: A microservice architecture requires certain services to scale up rapidly in response to spikes in demand, which may not have corresponding Reservation CRDs in place.

Outcome with DRP: DRP facilitates on-demand scaling by provisioning reservations as microservices scale out. This ensures that each service instance has the resources it needs to perform optimally without pre-allocating resources.


## Proposal

### Overview

The DRP mechanism will work as follows:

1. Pod Annotation for Dynamic Reservation: Users can annotate Pods with `scheduling.koordinator.sh/reservation-template`, which includes the specification for a potential Reservation CRD.
2. Scheduler Responsiveness: If an incoming Pod has this annotation and there is no available Reservation CRD instance that fits its requirements, the Koordinator scheduler will construct a virtual reservation object in memory. This object will emulate the behavior of a Reservation CRD instance to temporarily reserve resources for the Pod.
3. Triggering Reservation Creation: In the PreBind phase of scheduling, the scheduler appends an annotation `scheduling.koordinator.sh/new-reservation-requirement` to the Pod, signaling an external provisioner to create an actual Reservation CRD instance based on the provided template.
4. Reservation CRD Instance Provisioning: The external provisioner watches the Pod, once one Pod has annotation `scheduling.koordinator.sh/new-reservation-requirement` and `scheduling.koordinator.sh/reservation-template`, the provisioner will create a Reservation CRD instance with the annotation `scheduling.koordinator.sh/reservation-id` that defined in `new-reservation-requirement` to associate the virtual reservation object in scheduler. When the scheduler detects a newly created Reservation object with an associated virtual reservation object, it takes on the responsibility to update the status of the Reservation. It binds the Reservation to the node where the virtual reservation object was originally intended and marks the Reservation as available. 
5. Completion of the Binding Process: After the scheduler receives notification of a new Reservation CRD instance, it replaces the virtual object with the actual reservation, adjusts the reservation allocable resources by an amount equal to the sum of the requests of the associated Pods, and continues to bind the Pod to the appropriate node.

### Fault tolerance

Once provisioner exception prevents reservation from being created, fault tolerance needs to be implemented:

- The scheduler should have a default wait timeout, similar to the mechanism in Dynamic Volume Provisioning. If a Reservation is not created after waiting for 10 minutes, the process should be rolled back, and the Pod should be re-queued for another scheduling attempt.
- Consider retaining the virtual reservation object; it could be reused in the next Pod scheduling attempt. It is recommended to keep it at first. Once the provisioner recovers from any exceptions, it can attempt to create a new Reservation object that can replace the virtual reservation object in memory.
- In terms of the resource delivery success rate, if the creation of a Reservation fails, the Pod's scheduling could still be completed, and the virtual reservation object should be deleted.

### API

#### Reservation Template Annotation

Annotation key: `scheduling.koordinator.sh/reservation-template`, defined as follows：

```go
type ReservationTemplate struct {
	Owners          []schedulingv1alpha1.ReservationOwner  `json:"owners"`
	RecommendedSize corev1.ResourceList                    `json:"recommendedSize"`
	RoundUp         corev1.ResourceList                    `json:"roundup"`
}
```

The `recommendedSize` represents the desired maximum specification for a Reservation, but it is not mandatory. When a Pod is scheduled and has the `scheduling.koordinator.sh/reservation-template` annotation, the scheduler calculates the maximum between the Pod's requests and recommendedSize, using this maximum value for scheduling. If the Pod's requests are less than recommendedSize and the remaining resources in cluster nodes are insufficient to meet recommendedSize, the scheduler will attempt to schedule the Pod using the Pod's requests, aiming to schedule the Pod as efficiently as possible.

For example:

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    scheduling.koordinator.sh/reservation-template: |
      {
        "owners": [
          {
            "labelSelector": {
              "matchLabels": {
                "tenancy-id": "1612285282502324"
              }
            }
          }
        ],
        "recommendedSize": {
          "cpu": "4",
          "memory": "8Gi"
        },
        "roundup": {
          "cpu": "2",
          "memory": "2Gi"
        }
      }

```

#### New Reservation Requirement Annotaton

Annotation key: `scheduling.koordinator.sh/new-reservation-requirement`, defined as follows：

```go

const (
  LabelReservationID = `scheduling.koordinator.sh/reservation-id`
)

type NewReservationRequirement struct {
  Labels    map[string]string   `json:"labels"`
  Resources corev1.ResourceList `json:"resources"`
}
```

The `Resources` field represents the minimum reserved resource specification for a Reservation.

For example:

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    scheduling.koordinator.sh/new-reservation-requirement: |
      {
        "labels": {
          "scheduling.koordinator.sh/reservation-id": "aaa-bbb-ccc-dddd"
        },
        "resources": {
          "cpu": "4",
          "memory": "4Gi"
        }
      }
```

### Observability

From an observability perspective, incorporating appropriate Events and Metrics is crucial for monitoring the status of the system, tracking the process of resource allocation, and for the timely detection and response to potential issues in Dynamic Reservation Provisioning (DRP).

#### Events

1. **Reservation Creation Attempt Event**: Triggered when the scheduler initiates the creation of a new Reservation CRD instance.
1. **Reservation Creation Success Event**: Triggered when a new Reservation CRD instance is successfully created.
1. **Reservation Creation Failure Event**: Triggered when there is a failure in attempting to create a Reservation CRD instance, including the reason for the failure.
1. **Reservation Timeout Event**: Triggered when the creation of a Reservation CRD instance times out.

#### Metrics

1. **Reservation Requests Count**: Total number of Pods requesting new Reservations.
1. **Reservation Created Count**: Count of successfully created Reservation instances.
1. **Reservation Creation Failures Count**: Number of times the creation of Reservation instances has failed.
1. **Reservation Creation Latency**: The latency from requesting to the successful creation of a Reservation instance.
1. **Reservation Pending Duration**: The time a Reservation has been waiting in the queue.

## Implementation History

- 2023-12-05: Initial proposal sent for review
- 2023-12-06: Add chapters fault-toleration and observability