---
title: Add compatibility interface for priority class
authors:
- "@hormes"
reviews:
- "@eahydra"
- "@saintube"
- "@zwzhang0107"
- "@jasonliu747"
---

# Add compatibility interface for priority class

## Table of Contents

- [Add compatibility interface for priority class](#add-compatibility-interface-for-priority-class)
  - [Table of Contents](#table-of-contents)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
  - [Proposal](#proposal)
    - [User Stories](#user-stories)
    - [Implementation](#implementation)
  - [Alternatives](#alternatives)
  - [Unsolved Problems](#unsolved-problems)
  - [Implementation History](#implementation-history)

## Summary

Provide a solution using Koordinator PriorityClass for already running a large number of Pod clusters by adding a label `koordinator.sh/priority-class`.

## Motivation

Koordinator defines a set of specifications on top of kubernetes priority class, and extends a dimension of to support fine-grained colocation(see [Koordinator Priority Class](https://koordinator.sh/docs/architecture/priority)). 
If your cluster follows Koordinator's best practice to build the entire scheduling system from initialization, then everything will be very natural. 

Then, the actual situation is that before install Koordinator, a large number of clusters have been running for a period of time, and have its own Priority Class definition and supporting operating system.
In order to be better compatible with usage habits and at the same time obtain the scheduling capability of Koordinator bound to PriorityClass, we need a compatible solution to support this situation.

### Goals

1. Provides a solution using Koordinator PriorityClass for already running a large number of Pod clusters.

### Non-Goals/Future Work

1. Provide an alternative API for PriorityClass.

## Proposal

### User Stories

The user has already run a large number of Pods, and the PriorityClass conflicts with the Koordinator declaration.
For example, the PriorityClass is declared as follows:

```yaml
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: user-defined-priority-class
value: 5000
description: "This priority class should be used for prod service pods only."
```

Then create many Pods like this:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: pod-name
  labels:
    env: test
spec:
  containers:
  - name: nginx
    image: nginx
    imagePullPolicy: IfNotPresent
  priorityClassName: user-defined-priority-class
```

When users install Koordinator, they cannot adjust all these Pods at once (affecting business stability), and hope that
there is a way to regulate these Pods into the best practice category of Koordinator.

### Implementation

1. Defines a new label `koordinator.sh/priority-class` to compatible with Pods that are already running.
2. When a user install Koordinator, find a way to label the existing Pods with this label. There is a default value inference mechanism for unlabeled Koordinator, but please do not rely on it.
3. When Koordinator calculates the priorityclass, priority-class is obtained according to the label firstly. If the Pod declares the label, the label will be used(even if the value is not recognized).

## Alternatives

Provide a feature-gate to express whether to enable the compatibility mode. The compatibility mode is defined as follows:
- All priority classes are based on the obtained label and are no longer calculated based on Priority

The reason why this method is not used is that PriorityClass is actually used to describe Priority. If it is completely described in a label loosely coupled manner, the Priority between different PriorityClasses will be confused, which is not conducive to long-term business operations. Long-term use on k8s has compatibility and stability risks.


## Unsolved Problems

1. When there are two situations of PriorityClass declared by label and inferred by Priority at the same time, the Prod Pod Priority may be lower than the Batch Pod Priority. Users need to pay attention to the preemptive declaration relationship in the corresponding PriorityClass configuration and the possible Priority flipped. (Koord-scheduler supports the process of disabling high-priority to low-priority preemption to support user grayscale switching).

## Implementation History

- 2023-07-07: Initialize proposal for review
