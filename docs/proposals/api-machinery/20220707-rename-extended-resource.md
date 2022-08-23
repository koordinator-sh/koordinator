---
title: Refactor extended resource for overcommitment
authors:
- "@zwzhang0107"
reviews:
- "@jasonliu747"
- "@stormgbs"
- "@hormes"
---

# Refactor extended resource for overcommitment

## Table of Contents

- [Refactor extended resource for overcommitment](#refactor-extended-resource-for-overcommitment)
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

Using `kubernetes.io` as the namespace for koordinator extended resources, so that `koord-batch` resource can be
over-committed and `koord-batch-cpu` cores can be requested as non-integer with better readability.

## Motivation

Koordinator defines extended resource for co-location such as `batch-cpu` and `batch-memory`. Due to the limitation of
api-server, resources outside the `*kubernetes.io` namespace must be integers and cannot be over-committed
(see https://github.com/kubernetes/kubernetes/pull/48922). This proposal suggests using `koordinator.kubernetes.io`
as the namespace of koordiantor extended resources.

### Goals

1. Refactor the extended resource of koordinator to use `kubernetes.io` as namespace.

### Non-Goals/Future Work

1. Remove the constraints of api-server for extended resource (https://github.com/kubernetes/kubernetes/issues/110536)

## Proposal

### User Stories

Co-located pods request resources as the following format to achieve resource overcommitment (request < limit).

```yaml
resources:
  requests:
    kubernetes.io/batch-cpu: 1500 # 1.5 core
    kubernetes.io/batch-memory: 1Gi
  limites:
    kubernetes.io/batch-cpu: 3000 # 3 core
    kubernetes.io/batch-memory: 2Gi    
```

### Implementation

1. Defines new formats of extended resources as `kubernetes.io/batch-cpu` and `kubernetes.io/batch-memory`
2. Koord-manager updates new extended resources to `Node.Status`.
3. Webhook of `ColocationProfile` injects new extended resources to `Pod`.
4. Considering pods with the old format may continuously exist for a period of time, koord-scheduler will provide a
   filter plugin of resource allocation, summarizing the two kinds of batch resource request as allocated resource of
   node.

## Alternatives

Here are the pros and cons for alternative plans.

1. Using annotations to express batch resource requests and limits
2. Using `koordinato.sh/` namespace for koordinator batch resources.
3. Using `koordinato.sh/` for resource requests and annotations for resource limits.

| Alternative                                                                                    | Pros                          | Cons                                    |
|------------------------------------------------------------------------------------------------|-------------------------------|-----------------------------------------|
| annotations["batch-cpu"]                                                                       | resource overcommitment;      | incompatible with k8s scheduler         |
| extended resource `koordinator.sh/batch-cpu`                                                   | compatible with k8s scheduler | resource overcommitment is not allowed; | 
| setting requests as extended resource `koordinator.sh/batch-cpu` and limits in pod annotations | barely works with ugly design | bad expression ; confusing using habits |

## Unsolved Problems

1. [Remove the constrains for extended resources in api-server.](https://github.com/kubernetes/kubernetes/pull/110536)
2. Extended resources with old formats (koordinator.sh) on node needs to be removed manually.
3. Old format of extended resource will be marked as `deprecated`, koordinator will not support in next few versions.
   So please update the pod with old format as soon as possible.
4. Although new pods will be injected with new formats of extended resource, there may be some unscheduled pods with
   old formats of extended resource. If all nodes with old format happen to run out, these pods needs to be deleted
   manually
   for resubmissions.

## Implementation History

- 2022-07-07: Initialize proposal for review
- 2022-08-10: Fix the explanation that batch-cpu still need to be filled as milli-core, since the `scheduler`
  and `kubelet` will round down by `Value()` for extended-resource
