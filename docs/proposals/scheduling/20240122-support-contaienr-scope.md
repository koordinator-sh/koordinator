---
title: Container Scope NUMA-Aware Scheduling
authors:
  - "@kunwuluan"
reviewers:
creation-date: 2024-01-22
last-updated: 2024-01-31
---

# Container Scope NUMA-Aware Scheduling

## Table of Contents

<!-- TOC -->
- [Container Scope NUMA-Aware Scheduling](#container-scope-numa-aware-scheduling)
  - [Table of Contents](#table-of-contents)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
  - [Proposal](#proposal)
    - [User Stories](#user-stories)
    - [API](#api)
      - [Topology Manager Scope](#topology-manager-scope)
    - [Koordlet](#koordlet)
    - [Koord-Scheduler](#koord-scheduler)
      - [Node NUMA Resource](#node-numa-resource)
      - [Device Share](#device-share)
    - [Notes](#notes)
    - [Constraints](#constraints)
<!-- TOC -->

## Summary

Introduce a new api for users to specify the NUMA-scope for a pod. The scope defines the granularity at which you would like resource alignment to be performed (e.g. at the pod or container level). It allows Topology Manager to deal with the alignment of resources in a couple of distinct scopes:

- container
- pod

## Motivation

Sometimes we just need to align the resources of a container to a 
specific NUMA node instead of the whole pod. Aligning the resources
of whole pod may make the pod pending for the resource reason.

Also, currently we can use container and pod scope in kubelet's option,
and the default scope is container for kubelet. When switch to koordinator,
the default scope is pod, and there is no way to support container
scope for now.

So we want to support container scope NUMA-aware scheduling in koordinator.

### Goals

- Support container scope NUMA-aware scheduling.

### Non-Goals/Future Work

- Support pod-level NUMA-aware scheduling settings. This should be
  discussed in a separate issue.

- Support aligning part resources of a pod to a specific NUMA   node.
  For example, if there is a container injected by webhook whose
limits 
  are not same with requests in the pod.

## Proposal

### User Stories

- There are only single-NUMA-node policy in my cluster. 


### API

#### Topology Manager Scope

### Koordlet

### Koord-Scheduler

#### Node NUMA Resource

#### Device Share

### Notes

### Constraints