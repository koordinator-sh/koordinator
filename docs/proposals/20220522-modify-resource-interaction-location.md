---
title: Modify resource interaction location
authors:
- "@Mr-Linus"
  reviewers:
- "@jasonliu747"
  creation-date: 2022-05-22
  last-updated: 2022-05-22
  status: implementable
---

# Modify resource interaction location

## Summary

According to PR kubernetes/kubernetes#48922, extend resources could not be over-committed.
A good transition scenario might be move the batch resource to annotations, and it is 
easy to extend the custom resources type.

## Motivation

Since ApiServer will check whether the Request and Limit resources of extended resources 
are equal, flexible and elastic control resources cannot be achieved under the current 
version architecture. Once the Pod usage exceeds the usage of the custom resource, 
the Pod will be destroyed.

### Goals

1. Provide a resource structure used internally by the system, stored in the annotation.
2. ResManager implements the corresponding suppression strategy according to the structure/protocol

## Proposal

### Implementation Details/Notes/Constraints

- Webhook converts the resource requested by the pod into the resource structure inside the system, and appends it to the annotation in the function of mutation.
- Resmanager reads the Pod elastic resource Quota and transfers it from request to annotation.
- (To be determined)Remove the logic of NodeMetric to synchronize elastic/BE resource Quota to Node resource object.

### Risks and Mitigations

- The workload of changes is large and involves multiple components: webhook, resmanager & metricadvisor.

- Upgrading the existing Pods to a new version requires appending a new resource structure to the annotation.

- The batch-xxx related resource types on the node cannot be deleted because once deleted, the pods will be deleted together.

## Alternatives

Redefine a brand new custom resource object to handle related suppression logic, but we also need to maintain the resource object independently.

## Upgrade Strategy

- The batch-xxx related resource types on the node will be reserved.
- The existing Pods should be appended a new resource structure to the annotation.

## Implementation History

- [ ] 05/22/2022: Proposed idea in an issue