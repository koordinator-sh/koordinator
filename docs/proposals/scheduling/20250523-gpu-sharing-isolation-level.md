---
title: GPU Sharing Isolation Level
authors:
  - "@zqzten"
reviewers:
  - "@ZiMengSheng"
  - "@hormes"
creation-date: 2025-05-23
last-updated: 2025-05-23
---

# GPU Sharing Isolation Level

## Table of Contents

<!-- TOC -->
* [GPU Sharing Isolation Level](#gpu-sharing-isolation-level)
  * [Table of Contents](#table-of-contents)
  * [Summary](#summary)
  * [Motivation](#motivation)
    * [Goals](#goals)
    * [Non-Goals](#non-goals)
  * [Proposal](#proposal)
    * [User Story](#user-story)
    * [API](#api)
    * [Workflow](#workflow)
    * [Feature Gate](#feature-gate)
<!-- TOC -->

## Summary

Introduce a mechanism to differentiate a GPU share Pod's GPU resource isolation level. This takes effect on both scheduling and device allocating.

## Motivation

It's common that user want to use different GPU isolation levels for different GPU sharing workloads in one cluster. Currently, there're two common levels:

- Isolated: Pod can only use (or see) the GPU resource it requested. e.g. The GPU has 16Gi memory and the Pod request 8Gi, it can only use up to 8Gi.
- Shared: Pod can use (or see) the GPU resource up to the full card which assigned to it. e.g. The GPU has 16Gi memory and the Pod request 8Gi, it can use up to 16Gi.

The Isolated is somewhat like K8s Guaranteed and the Shared is like Burstable.

### Goals

- Introduce a mechanism to differentiate a GPU share Pod's GPU resource isolation level.

### Non-Goals

- Introduce new GPU isolation providers.

## Proposal

### User Story

User has two different kind of GPU sharing workloads in one K8s cluster. The first ones' GPU usage is predictable and user wants to ensure their stability, the second ones' GPU usage is burstable and user wants to maximize their GPU utilization.

For the first ones, user labels the pods to set their GPU isolation level to **Isolated**, so that they would get guaranteed GPU share resources, different pods sharing the same GPU card would not disturb each other. Thus, the stability of the workload is ensured.

For the second ones, user labels the pods to set their GPU isolation level to **Shared**, so that they would get burstable GPU share resources, every pod sharing the same GPU card can take use of the full card. Thus, the GPU utilization is maximized. A typical use case is running two pods with different models on the same GPU card, a model router would route requests to one of the model, so that there would be only one active inferencing process running on the GPU at the same time. By sharing the same GPU card, both of the pods could utilize the computing power of full card.

### API

- Introduce a new Pod label `koordinator.sh/gpu-share-isolation-level` whose value is one of `Isolated` and `Shared`. In the future, we might introduce more isolation levels such as `CoreIsolated` and `MemoryIsolated` for more use cases.
- The existing Pod label `koordinator.sh/gpu-isolation-provider` should be set if `koordinator.sh/gpu-share-isolation-level` is set to `Isolated`.
- For convenience and compatibility, if user does not set the isolation level label, it would be defaulted to `Isolated` if the isolation provider label is set, otherwise to `Shared`. But note if the isolation provider supports multiple isolation levels, user must set the isolation level label himself.

### Workflow

On k8s side, user creates GPU share Pod with/without the isolation level label.

On koord-manager side, the Pod mutating webhook defaults the isolation level label if not set by user, and the Pod validating webhook validates if the isolation provider supports the isolation level.

On koord-scheduler side, the device share scheduler makes sure that only Pods with the same isolation level would be allocated on the same GPU card.

On koordlet side, the GPU hook enables GPU isolation provider only if the isolation level of the Pod is set to `Isolated`.

### Feature Gate

Since this feature may introduce additional complexity to user and system (especially scheduler), and may not be needed in some scenarios (e.g. only one isolation level in one cluster), a feature gate named `GPUIsolationLevel` would be introduced to completely enable/disable this feature.
