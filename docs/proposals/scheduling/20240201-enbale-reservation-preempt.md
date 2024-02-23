---
title: Enable-Reservation-Preempt
authors:
- "@xulinfei1996"
reviewers:
- "@buptcozy"
- "@eahydra"
- "@hormes"
creation-date: 2024-02-01
last-updated: 2024-02-01
status: provisional

---

# Enable Reservation Preempt

<!-- TOC -->

- [Enable Reservation Preempt](#enable-reservation-preempt)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-goals/Future work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [Key Concept/User Stories](#key-conceptuser-stories)
        - [Implementation Details](#implementation-details)
            - [Priority](#priority)
            - [Preemption](#preemption)
                - [Response To Preempted](#response-to-preempted)
                - [Extension Point](#extension-point)
                - [Over All](#over-all)
                - [PreFilter](#prefilter)
                - [PostFilter](#postfilter)
        - [API](#api)
            - [Reservation](#reservation)
        - [Compatibility](#compatibility)
    - [Unsolved Problems](#unsolved-problems)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)
    - [References](#references)

<!-- /TOC -->

## Summary
This proposal provides reservation preempt mechanism for the scheduler.

## Motivation
In business scenarios, there may be excessive creation of reservations. In such cases, reservations may not be scheduled
due to insufficient resources. What's more, reservations can set different priority to declare different level SLA resources.
If scheduler can support reservation preemption, it can ensure that important reservations with higher SLA can obtain the 
necessary resources, thereby improving the overall efficiency of resource utilization. But reservation preemption is not 
supported now, so users may hope to support reservation preemption. 

### Goals

1. Define API to announce reservation can trigger preemption and can be preempted.

2. Define API to set reservation priority.

### Non-goals/Future work

## Proposal

### Key Concept\User Stories
#### Story 1 
Reservation failed to schedule due to insufficient cluster resource, so need to preempt other reservations. Hence, users 
need to extend the implementation to set reservation priority. For now, we suppose the pods bound to the preempted 
reservations still need to be evicted. 

#### Story2
Only allow reservations can preempt reservations when failed to schedule. Pod is not allowed to preempt reservations.

### Implementation Details
If reservation failed to schedule after PreFilter and Filter, and the reservation can trigger preemption, scheduler
can trigger preemption in PostFilter. During the preemption, the reservation can preempt the lower priority reservations.

But by default, the koord-scheduler sets the priority of reserved pod (constructed by Reservation) to Int32Max to disable 
preempting reservation. So we decide to use the label`koordinator.sh/priority` to set priority according to the needs. 
If koord-scheduler notices that Reservation.Labels has labels, use that value as priority, otherwise keep the default 
behavior.`koordinator.sh/priority` should be defined as Metadata.Labels of Reservation. The higher the value, the higher 
the priority. 

Typically, users use preemptionPolicy to declare whether reservation can trigger preemption or not. If the preemptionPolicy 
is PreemptLowerPriority, the reservation can trigger preemption. However, in de-scheduler users may already set 
Reservation.Spec.Template.Spec.PreemptionPolicy with `PreemptLowerPriority`, so we need to compatible with the existing 
reservation pods. We suppose to introduce label `scheduling.koordinator.sh/can-preempt` to judge whether reservation can 
preempt or not.

#### Preemption Rule
The reservation preemption still follows the existing Filter/PostFilter procedure and can be combined with job-level
preemption mechanism. 

The rules of implementing the preemption strategy of reservation are as followed:
1. `scheduling.koordinator.sh/can-preempt` default is set as to false. Only `scheduling.koordinator.sh/can-preempt=true` 
   reservation can trigger preemption, and only lower priority reservations can be preempted. 

2. Reservations can only preempt reservations.

3. If only part of Reservation can be assigned successfully in preemption dry-run process, the preemption will not
   really happen.

4. If reservation is preempted, the bound pods should also be evicted.

5. Users are expected to implement the soft-eviction mechanism. If the soft-eviction mechanism is not implemented, 
   scheduler should delete the evicted reservation and pods once the reservation is preempted.

#### Response To Preempted
Once a reservation is chosen to be evicted, it will follow the scheduler implemented eviction mechanism, support soft-eviction
or delete according to the implementation. The only difference is that we need to check the evicted is pod or reservation.

If the reservation is evicted, the related pods will not be evicted by scheduler. However, we expect the soft evictor to 
handle the eviction of the related Pods before the reservation is deleted.

#### Extension Point

##### Over All
Generally we will extend the elastic quota plugin, and modify other plugins to support reservation preemption.
The new\delta parts are:
1. Enable Reserve pod to preempt.
2. Enable Reserve pod to be preempted.
3. patch evict label/annotation to Reservation.
4. Register Reservation eventHandler for job-oversold plugin.

##### PreFilter
If pod is Reserve pod, as reservation is not associated to quota yet, so it is no need to do quota check for reservation.

##### PostFilter
We will maintain the existing implementation process, but with the following differences.

1. If pod is Reserve pod, as reservation is not associated to quota yet, so it is no need to do quota check for reservation.
2. Reserve pods only preempt reserve pods.

### API
#### Reservation
We introduce some labels to describe reservation behaviour.
- `scheduling.koordinator.sh/soft-eviction` is filled by scheduler, indicate the reservation is preempted.
- `scheduling.koordinator.sh/can-preempt` is filled by user, describe whether a reservation can trigger preemption.
- `koordinator.sh/priority` is filled by user, describe reservation 's priority, only higher priority reservations can 
   preempt lower priority reservations.

For example, there are two Reservations as followed. If Reservation1 failed to schedule due to resource not enough, then
Reservation1 can preempt Reservation2. Because Reservation1 label `scheduling.koordinator.sh/can-preempt: true` and its
priority is higher than Reservation2's priority.

Reservation1

```yaml
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: Reservation
metadata:
  labels:
    scheduling.koordinator.sh/can-preempt: "true"
    koordinator.sh/priority: "9999"
```

Reservation2

```yaml
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: Reservation
metadata:
  labels:
    scheduling.koordinator.sh/can-preempt: "true"
    koordinator.sh/priority: "9990"
```


### Compatibility

## Alternatives

## Unsolved Problems
In CoScheduling, user can declare minimumNumber and totalNumber. For now, we only support minimumNumber=totalNumber scenario.

For now, we only support reservation not associated to quota yet.

For now, we only support reservations preempt reservations. And, we only support pods preempt pods.

## Implementation History

## References