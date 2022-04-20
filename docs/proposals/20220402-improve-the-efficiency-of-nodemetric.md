---
title: Improve the efficiency of NodeMetrics
authors:
  - "@hormes"
reviewers:
  - "@zwzhang0107"
creation-date: 2022-04-02
last-updated: 2022-04-02
status: provisional
---

# Improve the efficiency of NodeMetrics


## Table of Contents

- [Title](#title)
  - [Table of Contents](#table-of-contents)
  - [Glossary](#glossary)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
  - [Proposal](#proposal)
    - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
    - [Risks and Mitigations](#risks-and-mitigations)
  - [Alternatives](#alternatives)
  - [Upgrade Strategy](#upgrade-strategy)
  - [Implementation History](#implementation-history)

## Glossary

Aggregated APIServer: https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/apiserver-aggregation/

## Summary

We hope to replace NodeMetric CRD with the implementation of Aggregated APIServer to improve the efficiency of node status reporting, so as to increase the frequency of status reporting and reduce the pressure on etcd.


## Motivation

In Koordinator, we need to perform resource overcommitment according to the actual resource usage of the Pod. The efficiency of such a resource view is a key part of the accuracy of resource overcommitment scheduling. The faster the frequency from the node to the center, the better. In the container service products provided by cloud providers, control-plane, especially etcd, is a key cost of the entire link. Excessive pressure may require increasing the specification and configuration of the control plane, thereby increasing the cost. Therefore, we hope to have a more efficient and lower cost status reporting link.


### Goals

- Improve the timeliness of the link for reporting the status of resources, and improve the accuracy of the link for overcommitment resources in colocation scenarios.
- Reduce the pressure of status reporting to the control plane.

### Non-Goals/Future Work

- Remove the CRD dependencies of NodeResource and NodeSLO. NodeMetric is volatile status data with particularity.

## Proposal

### Implementation Details/Notes/Constraints

Different from webhook, the Aggregated APIServer in Koord-manager adopts the active-standby working mode, and only the leader node accepts requests:

- Create independent service and healthcheck to support active/standby mode detection, only the leader node is in a healthy state.
- NodeMetric records data in memory, which is lost when restarting, and waiting for the node to report again.
- NodeResource supports data timeliness awareness. Nodes that have not been updated in the last period should be marked as unhealthy and start the exit process. Koord-manager restart time and data reporting recovery time should be kept within the node health judgment time.
- Koordlet finds the master node by self-service discovery, and directly reports it to Koord-manager without forwarding by APIServer, so as to avoid too high frequency to enhance the network traffic of the load balancer in front of apiserver.

### Risks and Mitigations

Using Aggregated APIServer and not persisting NodeMetrics will face the problem of data loss when restarting or switching between active and standby. Specifically:

- During the period of data loss, the resource view for overcommitment cannot be updated, and it is necessary to wait for the node reporting period. From the principle of periodic update, the increased delay is the time period for the process to restart, and in extreme cases, it is within 10s.
- After the data is lost, the informer cannot continue to watch the corresponding changes normally because the resource version can no longer keep monotonically increasing. Because it is an internal API, Koord-manager needs to do some special processing by itself.

## Alternatives

- Use metrics server instead of nodemetrics, in addition to cpu and mem usage, colocation scheduling decisions also require many other decision-making bases, such as Pod's memory allocation pressure, IO pressure, scheduling delay, etc., which are not provided in metrics-server. For the cpu and mem usage information, we also need to do some preprocessing on the node side to meet the peak prediction SLO requirements. The data timeliness and computing power overhead of relying on the metrics server for computing in the center are too large.

## Upgrade Strategy

This modification belongs to the internal reconstruction of the system and does not affect user functions. Just uninstall the CRD in the cluster and start the new version.

## Implementation History

- [ ] 04/02/2022: Proposed idea in an issue
- [ ] 04/19/2022: add an alternative option

