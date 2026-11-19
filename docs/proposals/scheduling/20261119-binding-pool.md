---
title: Binding Pool
authors:
  - "@tan90github"
reviewers:
  - "@saintube"
  - "@ZiMengSheng"
  - "@KunWuLuan"
creation-date: 2026-11-19
last-updated: 2026-11-19
status: provisional
---

# Binding Pool

## Table of Contents

<!-- TOC -->

- [Binding Pool](#binding-pool)
    - [Table of Contents](#table-of-contents)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals/Future Work](#non-goalsfuture-work)
    - [Proposal](#proposal)
        - [User Stories](#user-stories)
            - [Story 1](#story-1)
            - [Story 2](#story-2)
        - [How It Works](#how-it-works)
        - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
        - [Configuration Reference](#configuration-reference)
    - [Alternatives](#alternatives)
    - [Upgrade Strategy](#upgrade-strategy)
    - [Implementation History](#implementation-history)

<!-- TOC -->

## Summary

This proposal adds a koord-scheduler plugin, `BindingPool`, that replaces `DefaultBinder` at the bind extension point. The plugin hands Binding requests to a fixed number of worker goroutines, so the number of binds in flight stays within a configured limit instead of growing with the number of pods. This relieves pressure on the apiserver.

## Motivation

The scheduling framework runs the binding cycle of each pod in its own goroutine, and that goroutine calls the bind extension point right after `PreBind`. When pods are created in a burst, the number of binding goroutines grows with the number of pods, and all of them send Bindings to the apiserver at the same time. Once the apiserver slows down, requests start to queue up and the apiserver returns 429 (Too Many Requests). client-go retries these requests according to the `Retry-After` header, which keeps the binding goroutines waiting even longer. When the retries are exhausted, the binding cycle fails, and the assumed pod is forgotten and requeued for another scheduling cycle. This amplifies the load and lowers the scheduling throughput.

### Goals

- Limit the number of bind requests that koord-scheduler has in flight, to smooth out load peaks on the apiserver.
- Return bind failures to the binding cycle synchronously, so that the existing forget/requeue path still applies.
- Make the number of workers configurable, with a reasonable default.

### Non-Goals/Future Work

- Asynchronous binding, i.e., reporting success as soon as a Binding is queued. Bind failures would then go unhandled.
- Rate limiting by QPS. Only the number of requests in flight is limited.
- A global worker pool shared across profiles. It may be added later if needed.

## Proposal

### User Stories

#### Story 1

As a cluster administrator, I do not want the bind requests from the scheduler to spike along with the number of pods when the cluster scales out or a large batch of jobs is submitted. When tens of thousands of pods finish scheduling within a short time, their Bindings hit the apiserver at once. The apiserver slows down and returns 429, and the pods whose retries are exhausted are rescheduled, which triggers another round of binds. I want to cap the number of binds in flight, so that the extra pods wait inside the scheduler instead of all the load being pushed onto the apiserver.

#### Story 2

As a cluster administrator, I have APF enabled on my apiserver. APF queues requests on the apiserver side: the Bindings have already been sent and are waiting inside the apiserver, and when the queue is full, the apiserver returns 429. Once client-go exhausts its retries, the bind fails and the pod is rescheduled. I want bind concurrency to be controlled on the scheduler side, so that the extra requests wait inside the scheduler, are not sent to the apiserver, and do not fail just because they cannot get a slot.

### How It Works

`BindingPool` implements the bind extension point and replaces `DefaultBinder`. On startup, the plugin starts a fixed number of worker goroutines, as set by `workers`, and all Bindings are sent by these workers.

```
                   10000 binding                     256 workers send
                   goroutines in total               Bindings in turn

┌─────────────────┐                                                 ┌─────────────┐
│                 │ ────────────▶ ┃ ╲                               │             │
│                 │ ────────────▶ ┃   ╲                             │             │
│                 │ ────────────▶ ┃     ┌───────────┐               │             │
│                 │ ────────────▶ ┃     │           │ ────────────▶ │             │
│ koord-scheduler │      .....    ┃     │ bind pool │ ────────────▶ │  apiserver  │
│                 │ ────────────▶ ┃     │           │ ────────────▶ │             │
│                 │ ────────────▶ ┃     └───────────┘               │             │
│                 │ ────────────▶ ┃   ╱                             │             │
│                 │ ────────────▶ ┃ ╱                               │             │
└─────────────────┘                                                 └─────────────┘
                          the rest wait here
```

Each bind goes through the following steps:

1. The binding cycle reaches the bind extension point. The plugin builds a Binding request from the pod and the target node and hands it to the workers.
2. If a worker is idle, it takes the Binding right away. If all workers are busy, the binding cycle waits at this step until a worker becomes free.
3. The worker sends the Binding to the apiserver, returns the response to the binding cycle that submitted it, and then moves on to the next request.
4. The binding cycle continues with the result as usual. On success, it runs `PostBind`. On failure, the framework forgets the assumed pod and requeues it, the same as when `DefaultBinder` fails.

As a result:

- Each worker sends one Binding at a time, so at most `workers` Bindings are in flight to the apiserver.
- The plugin limits the number of requests in flight, not the QPS. The throughput is roughly `workers / latency of one Bind call`. When the apiserver slows down, the throughput drops with it, so the scheduler does not add more load.
- Pods waiting at step 2 remain assumed. Waiting by itself never causes a bind to fail.
- The bind result is returned to the binding cycle synchronously, and failures go through the existing handling in the framework, so no bind result is lost.

### Implementation Details/Notes/Constraints

- The plugin is registered as `BindingPool`, and `DefaultBinder` must be disabled at the bind extension point. `DefaultBinder` is enabled through the default MultiPoint plugins, which the framework places before the plugins enabled directly at an extension point, and `DefaultBinder` never returns `Skip`. If `BindingPool` is enabled while `DefaultBinder` is still enabled, `DefaultBinder` binds the pod first and `BindingPool` never runs. Disabling only `DefaultBinder` is enough; the example uses `disabled: "*"` to match how the other extension points of koord-scheduler are configured.
- `BindingPool` works together with `Reservation`. The `Bind` method of `Reservation` returns `Skip` for pods that are not reserve pods, so regular pods fall through to `BindingPool`.
- Plugin instances are created per profile. Each profile that enables `BindingPool` has its own set of workers, so the scheduler-wide limit of binds in flight is the number of such profiles × `workers`. With multiple profiles, either split `workers` across them, or enable `BindingPool` only in the profile that needs it, for example the profile for offline workloads.
- The workers are tied to the root context of the scheduler and exit with the process.
- The plugin is incompatible with the `SchedulerAsyncAPICalls` feature gate. When the gate is enabled, API calls go through the `APIDispatcher`, which starts a new goroutine for every call without any limit and would bypass the workers. The plugin returns an error if the gate is enabled, so the scheduler fails to start.
- The client-go QPS/Burst of the scheduler needs to be raised (the scheduler defaults to a QPS of 50 and a burst of 100). If the QPS is lower than `workers / latency of one Bind call`, the throughput is bounded by the token bucket instead, and the workers have no effect.
- **We recommend also matching koord-scheduler with a dedicated FlowSchema that maps to the `exempt` priority level**, so that APF no longer limits binds and `workers` becomes the only control on binds in flight. Otherwise, binds queue twice: once for the workers and again in the APF queue. With the two limits stacked, raising `workers` does not increase throughput and only adds another round of 429 retries. Note that `exempt` applies to all API calls from that ServiceAccount, not just binds.

### Configuration Reference

`BindingPool` is disabled by default. To enable it, add it to the bind extension point and disable `DefaultBinder` there:

```yaml
apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration
profiles:
  - schedulerName: koord-scheduler
    plugins:
      bind:
        enabled:
          - name: Reservation
          - name: BindingPool
        disabled:
          - name: "*"
    pluginConfig:
      - name: BindingPool
        args:
          apiVersion: kubescheduler.config.k8s.io/v1
          kind: BindingPoolArgs
          workers: 256
```

The `pluginConfig` entry is optional. Without it, `workers` defaults to 256, so it is only needed to change `workers`.

`workers` must be a positive integer. To choose a value:

- Each worker handles one Binding at a time, so `workers ≈ target bind throughput × latency of one Bind call`. For the latency, use the mean of `apiserver_request_duration_seconds{verb="POST",resource="pods",subresource="binding"}` on the apiserver under normal load. For example, a target of 1000 pods/s with a latency of 250ms gives 250, which is close to the default of 256.
- If the scheduler is not in the `exempt` priority level, keep `workers` at or below the number of concurrency seats of its priority level, `apiserver_flowcontrol_nominal_limit_seats`. Binds beyond that number only wait in the APF queue and tie up workers for nothing. The `exempt` level has no such limit, so the previous rule alone applies.
- Once the plugin is enabled, `scheduler_framework_extension_point_duration_seconds{extension_point="Bind"}` on the scheduler includes the time spent waiting in the bind pool. If it stays high, either there are too few workers or the apiserver is slow.

## Alternatives

- **Asynchronous binding (the binding cycle returns immediately)**: This fully decouples the number of binding goroutines from scheduling, but nothing handles a failed bind, and the pod stays assumed. The plugin would have to reimplement forget/requeue, duplicating the failure path of the framework, which is too risky.
- **Tuning only the client-go QPS/Burst**: A token bucket limits the rate at which requests are sent, not their concurrency. When requests pile up on the apiserver, many goroutines are still waiting at the same time. The QPS limit is also shared by the whole client, so different profiles and other API calls interfere with each other.

## Upgrade Strategy

This proposal adds a new plugin and does not change any existing behavior. To use it, enable it explicitly as shown above and disable `DefaultBinder` at the bind extension point. No other action is needed on upgrade.

## Implementation History

- 11/19/2026: Design discussion; settled on limiting bind concurrency synchronously.
- 11/19/2026: Implemented the `BindingPool` plugin prototype and unit tests.
