---
title: Inference Orchestration Enhancement with Grove Integration
authors:
  - "kangclzjc"
  - "daisy-ycguo"
reviewers:
  - TBD
creation-date: 2025-12-01
last-updated: 2025-12-01
status: provisional
see-also:
  - "/docs/proposals/scheduling/20250611-networktopology-aware-scheduling.md"
---

# Inference Orchestration Enhancement with Grove Integration

## Table of Contents

A table of contents is helpful for quickly jumping to sections of a proposal and for highlighting
any additional information provided beyond the standard proposal template.
[Tools for generating](https://github.com/ekalinin/github-markdown-toc) a table of contents from markdown are available.

- [Title](#title)
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
    - [Requirements (Optional)](#requirements-optional)
      - [Functional Requirements](#functional-requirements)
        - [FR1](#fr1)
        - [FR2](#fr2)
      - [Non-Functional Requirements](#non-functional-requirements)
        - [NFR1](#nfr1)
        - [NFR2](#nfr2)
    - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
    - [Risks and Mitigations](#risks-and-mitigations)
  - [Alternatives](#alternatives)
  - [Upgrade Strategy](#upgrade-strategy)
  - [Additional Details](#additional-details)
    - [Test Plan [optional]](#test-plan-optional)
  - [Implementation History](#implementation-history)

## Glossary

- **Grove**: A Kubernetes API providing a declarative interface for orchestrating AI inference workloads with support for multi-node deployments, hierarchical gang scheduling, and topology-aware placement.
- **PodClique**: A group of pods representing a specific role (e.g., leader, worker, prefill, decode) with independent configuration and scaling logic.
- **PodCliqueScalingGroup**: A set of PodCliques that scale and are scheduled together as a gang unit.
- **PodCliqueSet**: The top-level Grove object defining a group of components managed and colocated together, supporting autoscaling with topology-aware spread.
- **Disaggregated Inference**: An inference architecture where different stages (e.g., prefill and decode) run on separate pods/nodes but work together to serve requests.
- **Gang Scheduling**: Scheduling multiple pods as an all-or-nothing unit to ensure all required components are available before any start running.

## Summary

This proposal aims to enhance Koordinator's inference orchestration capabilities by integrating with Grove, an open-source Kubernetes enhancement for network topology-aware gang scheduling and autoscaling specifically designed for AI inference workloads. 

Grove addresses critical gaps in orchestrating modern AI inference systems, including multi-node model deployments (e.g., DeepSeek-R1, Llama-4-Maverick), disaggregated inference architectures (separate prefill/decode stages), and agentic AI pipelines. By integrating Grove's capabilities with Koordinator's existing scheduling features, we can provide a comprehensive solution for managing complex inference workloads at scale, from single GPUs to data centers with tens of thousands of GPUs.

The integration will leverage Koordinator's network topology awareness and resource scheduling capabilities while adopting Grove's orchestration abstractions (PodClique, PodCliqueScalingGroup, PodCliqueSet) to enable hierarchical gang scheduling, startup ordering (via `startsAfter` dependencies), and multi-level autoscaling for inference workloads.

## Motivation

Modern AI inference workloads, especially large language models (LLMs) and multi-modal models, present orchestration challenges that exceed the capabilities of standard Kubernetes primitives:

1. **Multi-Node Model Instances**: Large models (e.g., DeepSeek-R1, Llama-4-Maverick) are often sharded across multiple nodes. A single model instance spans multiple pods, making the fundamental scaling unit a group of pods rather than individual pods.

2. **Disaggregated Inference Architectures**: To optimize resource utilization and performance, inference is increasingly split into separate stages (prefill for prompt processing, decode for token generation) running on different pods/nodes. These components must be scheduled together to form functional pipelines.

3. **Hierarchical Gang Scheduling**: Multi-node deployments require gang scheduling at multiple levels - pods within a model instance must be scheduled together, and in disaggregated architectures, at least one prefill instance and one decode instance must be scheduled to form a working system.

4. **Startup Ordering**: Even when components are gang-scheduled, they often need to start in specific order (e.g., MPI workloads require workers to be ready before the leader starts). Grove addresses this through `startsAfter` dependencies and init container injection.

5. **Topology-Aware Placement**: Inference components communicate heavily. Placement within NVLink domains or network-optimized locations is crucial for minimizing latency and maximizing throughput.

Grove (https://github.com/ai-dynamo/grove) provides a proven solution to these challenges with a clean Kubernetes-native API. Integrating Grove with Koordinator will combine Grove's inference-specific orchestration patterns with Koordinator's advanced scheduling capabilities and resource management, enabling native support for Dynamo (https://github.com/ai-dynamo/dynamo).

### Goals

- Enable orchestration of multi-node, disaggregated inference workloads in Koordinator through Grove integration
- Support hierarchical gang scheduling for inference workloads with multiple component types (prefill, decode, leader, worker, etc.)
- Implement startup ordering to ensure correct initialization sequences for distributed inference systems (Grove's `startsAfter` mechanism with init container injection)
- Leverage Koordinator's existing network topology awareness to optimize placement of Grove-managed workloads
- Provide multi-level autoscaling capabilities for inference workloads (scaling both individual components and entire inference systems)
- Maintain compatibility with Koordinator's existing scheduling features (reservation, preemption, QoS, etc.)

### Non-Goals

- Re-implementing Grove's core controllers from scratch (we will integrate with Grove's existing implementation)
- Modifications to Grove's upstream API definitions (we will adopt Grove's APIs as-is and extend through Koordinator-specific annotations if needed)
- Automatic model sharding or inference optimization (this proposal focuses on orchestration, not model serving logic)


### Future Work
- Modify grove to leverage community k8s gang schedule capability(Workload)
- Supporting non-inference workloads with Grove APIs (Grove is specifically designed for inference; other workloads should use existing Koordinator features)

## Proposal

This proposal introduces Grove integration into Koordinator to enable advanced orchestration of AI inference workloads. The integration strategy involves:

1. **Adopt Grove CRDs**: Deploy Grove's Custom Resource Definitions (PodClique, PodCliqueScalingGroup, PodCliqueSet, PodGang) alongside Koordinator
2. **Extend Koordinator Scheduler**: Enhance Koordinator's scheduler to recognize and process PodGang scheduling requirements
3. **Leverage Network Topology Plugin**: Integrate Grove's topology-aware placement needs with Koordinator's existing NetworkTopology plugin
4. **Provide Migration Path**: Enable gradual adoption for users with existing inference workloads

### User Stories

#### Story 1: Multi-Node Disaggregated Inference for Large Models

As an ML platform engineer, I want to deploy a large language model (e.g., DeepSeek-R1 with 671B parameters) using a disaggregated architecture where:
- Prefill stage runs on 4 nodes (8 GPUs total) with high memory bandwidth
- Decode stage runs on 8 nodes (16 GPUs total) optimized for throughput
- All prefill pods must be scheduled together (gang scheduling)
- All decode pods must be scheduled together (gang scheduling)  
- At least one prefill replica and one decode replica must be scheduled for the system to be functional (hierarchical gang)
- Prefill and decode stages should be placed in network-optimized locations to minimize communication latency
- The system should autoscale based on request load, adding/removing complete prefill+decode replica sets

With Grove integration in Koordinator, I can define a single `PodCliqueSet` CR that describes the entire system. Koordinator will ensure proper gang scheduling, topology-aware placement using NetworkTopology awareness, and coordinated autoscaling.

**Complete YAML Example**:

```yaml
apiVersion: grove.io/v1alpha1
kind: PodCliqueSet
metadata:
  name: deepseek-r1-disaggregated
  namespace: inference
  annotations:
    # Koordinator-specific annotations for the entire PodCliqueSet
    scheduling.koordinator.sh/network-topology-zone: "nvlink-domain-1"
    scheduling.koordinator.sh/gang-timeout: "600s"
spec:
  replicas: 2  # Two complete prefill+decode replica sets
  template:
    terminationDelay: 1m
    cliqueStartupType: CliqueStartupTypeExplicit  # Ensure startup order is honored
    cliques:
      # Prefill stage - processes prompts with high memory bandwidth
      - name: prefill
        spec:
          roleName: prefill-role
          replicas: 2  # 2 pods, each with 4 GPUs (8 GPUs total)
          # No startsAfter - prefill starts first (no dependencies)
          podSpec:
            metadata:
              labels:
                app: deepseek-r1
                component: prefill
              annotations:
                # Koordinator network topology annotation for fine-grained placement
                gang.scheduling.koordinator.sh/network-topology-index: "0"
                # Request specific QoS level
                scheduling.koordinator.sh/qos-class: "LS"  # Latency-Sensitive
            spec:
              schedulerName: koord-scheduler
              containers:
              - name: prefill-server
                image: deepseek/r1-prefill:v1.0
                resources:
                  requests:
                    cpu: "32"
                    memory: "256Gi"
                    nvidia.com/gpu: "4"
                  limits:
                    cpu: "32"
                    memory: "256Gi"
                    nvidia.com/gpu: "4"
                env:
                - name: MODEL_STAGE
                  value: "prefill"
                - name: DECODE_ENDPOINT
                  value: "deepseek-r1-decode-service:8000"
                ports:
                - containerPort: 8000
                  name: grpc
                volumeMounts:
                - name: model-cache
                  mountPath: /models
              volumes:
              - name: model-cache
                emptyDir:
                  sizeLimit: 100Gi

      # Decode stage - generates tokens optimized for throughput
      - name: decode
        spec:
          roleName: decode-role
          replicas: 4  # 4 pods, each with 4 GPUs (16 GPUs total)
          startsAfter:  # Start decode pods only after prefill is ready
            - prefill
          podSpec:
            metadata:
              labels:
                app: deepseek-r1
                component: decode
              annotations:
                # Schedule decode pods after prefill in topology placement
                gang.scheduling.koordinator.sh/network-topology-index: "1"
                scheduling.koordinator.sh/qos-class: "LS"
            spec:
              schedulerName: koord-scheduler
              # Grove will inject init container to enforce startsAfter dependency:
              # initContainers:
              # - name: wait-for-prefill
              #   image: grove/startup-coordinator:v1.0
              #   command: ["/wait-for-podclique"]
              #   args: ["--podclique=prefill"]
              containers:
              - name: decode-server
                image: deepseek/r1-decode:v1.0
                resources:
                  requests:
                    cpu: "24"
                    memory: "192Gi"
                    nvidia.com/gpu: "4"
                  limits:
                    cpu: "24"
                    memory: "192Gi"
                    nvidia.com/gpu: "4"
                env:
                - name: MODEL_STAGE
                  value: "decode"
                - name: PREFILL_ENDPOINT
                  value: "deepseek-r1-prefill-service:8000"
                ports:
                - containerPort: 8000
                  name: grpc
                volumeMounts:
                - name: model-cache
                  mountPath: /models
              volumes:
              - name: model-cache
                emptyDir:
                  sizeLimit: 50Gi
    
    podCliqueScalingGroups:
    - name: disaggregated-inference
      minAvailable: 1  # At least one complete prefill+decode pair must be scheduled
      cliqueNames:
        - prefill
        - decode
---
# Example: How Grove Implements Startup Ordering with startsAfter
# 
# For the decode PodClique with `startsAfter: [prefill]`, Grove will inject
# an init container into each decode pod:
#
# apiVersion: v1
# kind: Pod
# metadata:
#   name: deepseek-r1-disaggregated-0-decode-0
#   annotations:
#     grove.io/starts-after: "prefill"
# spec:
#   initContainers:
#   - name: wait-for-prefill
#     image: grove/startup-coordinator:v1.0
#     command: ["/wait-for-podclique"]
#     args:
#     - "--namespace=inference"
#     - "--podclique=prefill"
#     - "--podcliqueset=deepseek-r1-disaggregated"
#     - "--replica-index=0"
#     # This init container queries the API server and waits until all pods
#     # in the "prefill" PodClique reach Ready state before exiting
#   containers:
#   - name: decode-server
#     # ... main container starts only after init container completes
---
# Grove operator will create PodGang CR for Koordinator scheduler
apiVersion: scheduling.grove.io/v1alpha1
kind: PodGang
metadata:
  name: deepseek-r1-disaggregated-0
  namespace: inference
  ownerReferences:
  - apiVersion: grove.io/v1alpha1
    kind: PodCliqueSet
    name: deepseek-r1-disaggregated
    uid: <generated-uid>
spec:
  schedulerName: koord-scheduler
  minAvailable: 1  # At least 1 complete set of prefill+decode
  subGroups:
  - minMember: 2
    name: prefill
  - minMember: 4
    name: decode
```

#### Story 2: Agentic AI Pipeline with Multiple Models

As an AI application developer, I want to run an agentic pipeline where:
- A router model (single GPU) directs requests to specialized models
- A vision model (2 GPUs) handles image understanding  
- A reasoning model (4 GPUs, tensor-parallelized) handles complex logic
- A code generation model (2 GPUs) produces code outputs
- All components must be scheduled together - the pipeline is non-functional if any component is missing
- Vision and reasoning models should start before the router (startup ordering)
- The entire pipeline should scale as a unit based on end-to-end latency

With Grove + Koordinator, I can define PodCliques for each model, group them in a PodCliqueScalingGroup for gang scheduling, and use `startsAfter` to specify startup dependencies. Koordinator's scheduler ensures all components are placed together, and autoscaling adjusts the entire pipeline as one unit.

**Example with Multiple Startup Dependencies**:

```yaml
apiVersion: grove.io/v1alpha1
kind: PodCliqueSet
metadata:
  name: agentic-pipeline
spec:
  replicas: 1
  schedulerName: koord-scheduler
  template:
    cliqueStartupType: CliqueStartupTypeExplicit
    cliques:
    # Vision, reasoning, and code-gen models have no dependencies - start first
    - name: vision
      spec:
        roleName: vision-role
        replicas: 1
        # No startsAfter - starts immediately after scheduling
        podSpec:
          spec:
            schedulerName: koord-scheduler
            containers:
            - name: vision-model
              image: vision-model:latest
              resources:
                requests:
                  nvidia.com/gpu: 2
    
    - name: reasoning
      spec:
        roleName: reasoning-role
        replicas: 1
        # No startsAfter - starts immediately after scheduling
        podSpec:
          spec:
            schedulerName: koord-scheduler
            containers:
            - name: reasoning-model
              image: reasoning-model:latest
              resources:
                requests:
                  nvidia.com/gpu: 4
    
    - name: code-gen
      spec:
        roleName: code-gen-role
        replicas: 1
        # No startsAfter - starts immediately after scheduling
        podSpec:
          spec:
            schedulerName: koord-scheduler
            containers:
            - name: code-gen-model
              image: code-gen:latest
              resources:
                requests:
                  nvidia.com/gpu: 2
    
    # Router waits for all specialized models to be ready
    - name: router
      spec:
        roleName: router-role
        replicas: 1
        startsAfter:  # Router starts only after these are all Ready
          - vision
          - reasoning
          - code-gen
        podSpec:
          spec:
            schedulerName: koord-scheduler
            containers:
            - name: router-model
              image: router:latest
              resources:
                requests:
                  nvidia.com/gpu: 1
    
    podCliqueScalingGroups:
    - name: ai-pipeline
      minAvailable: 1
      cliqueNames:
        - vision
        - reasoning
        - code-gen
        - router
```

In this example, Grove will ensure that the router pod doesn't start until all three specialized model pods (vision, reasoning, code-gen) are in Ready state. This prevents the router from receiving requests before the backend models are available to serve them.

#### Story 3: MPI-Based Multi-Node Training/Inference

As an ML researcher, I want to run a multi-node MPI workload for inference where:
- One leader pod coordinates the MPI job
- N worker pods perform distributed computation
- All pods must be gang-scheduled (if only some workers are available, the job cannot run)
- Worker pods must be fully ready before the leader pod starts the MPI application
- Pods should be placed on nodes with high-speed interconnects (e.g., InfiniBand, NVLink)

Using Grove's PodClique with `startsAfter` dependencies and Koordinator's topology awareness, I can ensure workers start first, leader waits for workers to be ready, and all pods are placed optimally for MPI communication patterns.

### Requirements (Optional)

#### Functional Requirements

##### FR1: Hierarchical Gang Scheduling Support

**MUST**: Koordinator scheduler must recognize and enforce PodGang scheduling constraints with support for hierarchical gang requirements (gang of gangs). If minimum replica requirements cannot be satisfied at any level, no pods in the gang should be scheduled.

##### FR2: Startup Ordering

**MUST**: Support explicit startup ordering within PodCliqueScalingGroups using Grove's `startsAfter` field. Dependent pods must not start until prerequisite pods reach Ready state. This is enforced by Grove operator through init containers, not by the scheduler.

##### FR3: Topology-Aware Placement for Grove Workloads  

**MUST**: Integrate Grove's placement requirements with Koordinator's NetworkTopology plugin to prefer scheduling pods within the same NVLink domain, network zone, or other topology levels as specified.

##### FR4: Multi-Level Autoscaling

**SHOULD**: Support autoscaling at both PodClique level (scaling individual components) and PodCliqueSet level (scaling entire inference systems as units).

##### FR5: Reservation Compatibility

**MUST**: Grove-managed pods must be compatible with Koordinator's Reservation mechanism for resource pre-allocation and guaranteed scheduling.

##### FR6: API Compatibility

**MUST**: Adopt Grove's upstream API definitions without breaking changes. Koordinator-specific extensions should use annotations or separate CRDs.

#### Non-Functional Requirements

##### NFR1: Backward Compatibility  

**MUST**: Existing Koordinator features (QoS, ElasticQuota, Device Scheduling) must continue to work for non-Grove workloads. Grove integration should be opt-in.

### Implementation Details/Notes/Constraints

#### Architecture Overview

The integration follows a layered approach:

```
┌─────────────────────────────────────────────────────────────┐
│                     User Applications                        │
│         (PodCliqueSet, PodClique, PodGang CRs)              │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                   Grove Operator                             │
│  • Manages PodCliqueSet lifecycle                           │
│  • Creates PodCliques and PodGangs                          │
│  • Handles autoscaling logic                                │
│  • Enforces startup ordering                                │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│              Koordinator Scheduler                           │
│  • Gang scheduling plugin (PodGang-aware)                   │
│  • NetworkTopology plugin (topology placement)              │
│  • Reservation plugin (resource guarantee)                  │
│  • Coscheduling plugin (hierarchical gang)                  │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                 Kubernetes API Server                        │
│                    (Pods, Nodes)                            │
└─────────────────────────────────────────────────────────────┘
```

#### Component Changes

##### 1. CRD Integration

- **Deploy Grove CRDs**: Include Grove's CRD definitions in Koordinator installation
  - `PodClique`: Defines a group of homogeneous pods with a specific role
  - `PodCliqueScalingGroup`: Groups multiple PodCliques for gang scheduling
  - `PodCliqueSet`: Top-level resource for managing inference systems
  - `PodGang`: Scheduling API consumed by Koordinator scheduler

- **Annotation Extensions**: Use annotations for Koordinator-specific behaviors
  - `scheduling.koordinator.sh/network-topology-zone`: Specify preferred topology zones
  - `scheduling.koordinator.sh/reservation-ref`: Link to Reservation resources
  - `scheduling.koordinator.sh/gang-timeout`: Override default gang scheduling timeout

##### 2. Scheduler Enhancements

**Gang Scheduling Plugin Extension**:
- Extend existing Coscheduling plugin to understand PodGang CRs
- Implement hierarchical gang validation:
  ```
  PodGang (top level)
    ├─ PodGroup (prefill clique)
    │   └─ min replicas: 4
    └─ PodGroup (decode clique)  
        └─ min replicas: 8
  ```
- If any sub-group cannot meet minimum replicas, reject the entire PodGang
- Support gang-level scheduling timeout with graceful cleanup

**Topology-Aware Placement**:
- Integrate with existing NetworkTopology plugin

**Startup Ordering**:
- Grove uses the `startsAfter` field in PodClique spec to define startup dependencies
- Example: `startsAfter: [prefill]` means this PodClique waits for all pods in "prefill" PodClique to be Ready
- Grove operator injects init containers into dependent pods to enforce the startup order
- Init containers wait for prerequisite pods to reach Ready state before allowing the main container to start
- Koordinator scheduler is not involved in startup ordering enforcement - it only ensures all pods are scheduled
- Startup ordering happens at pod lifecycle level, not at scheduling level

##### 3. Controller Coordination

**Grove Operator**:
- Grove creates PodGang CRs consumed by Koordinator scheduler
- Grove manages autoscaling based on metrics (CPU, GPU, custom metrics)

##### 4. Autoscaling Integration

**HPA Integration**:
- Grove supports standard Kubernetes HPA for PodCliques
- Koordinator's existing VPA integration continues to work

**Custom Autoscaling**:
- Grove implements custom autoscaling logic for inference-specific metrics
  - Request queue length
  - GPU utilization per clique
  - End-to-end latency
- Scaling decisions respect gang constraints (scale entire groups together)

#### API Examples

**Example 1: Disaggregated Inference**

```yaml
apiVersion: grove.io/v1alpha1
kind: PodCliqueSet
metadata:
  name: llama-inference
  annotations:
    scheduling.koordinator.sh/network-topology-zone: nvlink-domain-1
spec:
  replicas: 2  # Two complete prefill+decode systems
  template:
    cliqueStartupType: CliqueStartupTypeExplicit
    cliques:
    - name: prefill
      spec:
        roleName: prefill-role
        replicas: 4
        # No startsAfter - prefill starts first
        podSpec:
          spec:
            schedulerName: koord-scheduler
            containers:
            - name: prefill
              image: llama-prefill:latest
              resources:
                requests:
                  nvidia.com/gpu: 2
    - name: decode
      spec:
        roleName: decode-role
        replicas: 8
        startsAfter:  # Start decode after prefill is ready
          - prefill
        podSpec:
          spec:
            schedulerName: koord-scheduler
            containers:
            - name: decode
              image: llama-decode:latest
              resources:
                requests:
                  nvidia.com/gpu: 2
    podCliqueScalingGroups:
    - name: inference-pipeline
      minAvailable: 1  # At least one complete pipeline
      cliqueNames:
        - prefill
        - decode
```

**Example 2: MPI Workload with Leader-Worker**

```yaml
apiVersion: grove.io/v1alpha1
kind: PodCliqueSet
metadata:
  name: mpi-inference
spec:
  replicas: 1
  schedulerName: koord-scheduler
  template:
    cliqueStartupType: CliqueStartupTypeExplicit
    cliques:
    - name: worker
      spec:
        roleName: worker-role
        replicas: 8
        # No startsAfter - workers start first
        podSpec:
          spec:
            schedulerName: koord-scheduler
            containers:
            - name: mpi-worker
              image: mpi-worker:latest
    - name: leader
      spec:
        roleName: leader-role
        replicas: 1
        startsAfter:  # Leader waits for all workers to be ready
          - worker
        podSpec:
          spec:
            schedulerName: koord-scheduler
            containers:
            - name: mpi-leader
              image: mpi-leader:latest
    podCliqueScalingGroups:
    - name: mpi-job
      minAvailable: 1
      cliqueNames:
        - worker
        - leader
```

#### Constraints and Limitations

1. **Scheduling Complexity**: Hierarchical gang scheduling with topology awareness is computationally expensive. Large gangs (>100 pods) may experience increased scheduling latency.

2. **Startup Ordering Delays**: Strict startup ordering may increase total deployment time. Users should set appropriate timeouts.

### Risks and Mitigations

#### Risk 1: Upstream Grove API Changes

**Risk**: Grove is in alpha stage. API breaking changes could require significant rework in Koordinator integration.

**Mitigation**: 
- Engage with Grove maintainers early and participate in API design discussions
- Contribute to Grove's API stabilization efforts

#### Risk 2: Scheduling Performance Degradation

**Risk**: Complex gang scheduling with topology awareness could significantly increase scheduling latency, especially in large clusters.

**Mitigation**:
- Provide configuration options to tune gang scheduling timeout and batch sizes
- Conduct performance benchmarks with 1000+ node clusters before GA release
- Implement scheduler metrics to detect performance regressions

#### Risk 3: Complexity for Users

**Risk**: Adding Grove concepts (PodClique, PodCliqueSet, etc.) increases learning curve for Koordinator users.

**Mitigation**:
- Provide comprehensive documentation with real-world examples
- Create migration guides from existing Koordinator workloads to Grove-based workloads
- Keep Grove integration optional - users not running inference workloads don't need to learn it

#### Risk 4: Incompatibility with Existing Features

**Risk**: Grove's gang scheduling might conflict with Koordinator's Reservation, Preemption, or ElasticQuota features.

**Mitigation**:
- Conduct thorough integration testing with all existing Koordinator features
- Document known limitations and incompatibilities clearly
- Implement feature gates to allow gradual rollout and easy rollback
- Add validation webhooks to reject invalid configurations early


## Alternatives

### Alternative 1: Implement Inference Orchestration Natively in Koordinator

Instead of integrating Grove, Koordinator could implement all inference orchestration features natively.

**Pros**:
- Full control over API design and implementation
- Tighter integration with existing Koordinator features
- No external dependencies

**Cons**:
- Significant development effort (Grove already provides mature implementation)
- Risk of diverging from community standards if Grove becomes widely adopted
- Need to maintain inference-specific logic long-term
- Reinventing the wheel when proven solution exists


### Alternative 2: Wait for Kubernetes Native Support

Wait for Kubernetes to add native support for advanced scheduling features.

**Pros**:
- Eventually standardized across Kubernetes ecosystem
- No need for custom implementations

**Cons**:
- Timeline uncertain (could be years)
- May not address inference-specific requirements
- Users need solutions now for production workloads


### Alternative 3: RBG (RoleBasedGroup) Solution

Adopt RBG (https://github.com/sgl-project/rbg), a Kubernetes API specifically designed for orchestrating distributed, stateful AI inference workloads with multi-role collaboration and built-in service discovery. RBG treats an inference service as a "role-based group" rather than a collection of isolated workloads, modeling the service as a topological, stateful, coordinated multi-role organism.

**Pros**:
- Built from the ground up for LLM inference patterns, especially disaggregated architectures (prefill/decode separation)
- Treats "Role" as the atomic unit for scheduling orchestration while establishing configurable relationships between roles, providing a more natural model for multi-component inference systems
- Native support for coordinated operations across different roles (gateway/router/prefill/decode) as a single logical unit

**Cons**:
- RBG is specifically designed for inference workloads; not suitable as a general-purpose orchestration solution for other workload types (training, batch jobs, etc.)
- Requires LeaderWorkerSet (>=v0.7.0) as an additional dependency, adding complexity to the deployment stack
- Lack hierarchical gang scheduling for a complicated workload with several components


## Upgrade Strategy

### Upgrade from Previous Koordinator Versions

**For clusters NOT using Grove features**:
- No changes required - Grove integration is opt-in
- Existing workloads continue to function normally
- Grove CRDs are installed but unused
- No performance impact on non-Grove workloads

**For clusters adopting Grove features**:
- Install Grove operator (can be done before or after Koordinator upgrade)
- Enable Grove feature gate in Koordinator scheduler: `--feature-gates=GroveIntegration=true`
- Update scheduler configuration to include Grove-aware plugins
- No changes to existing Grove CRDs if already using Grove standalone

### Migration from Standalone Grove to Koordinator+Grove

Users already running Grove without Koordinator can migrate gradually:

1. Install Koordinator alongside existing Grove installation
2. Enable Koordinator scheduler as secondary scheduler initially
3. Test with new workloads using Koordinator+Grove
4. Migrate existing PodCliqueSet resources to use Koordinator scheduler via annotation:
   ```yaml
   metadata:
     annotations:
       schedulerName: koord-scheduler
   ```
5. Monitor and validate functionality
6. Gradually migrate all workloads
7. Deprecate standalone scheduler if desired


**Feature Gate (temporary, during alpha/beta)**:
```bash
--feature-gates=GroveEnable=true
```

### Rollback Strategy

If issues arise during upgrade:

1. Disable Grove feature gate: `--feature-gates=GroveEnable=false`
2. Scheduler will ignore PodGang resources and fall back to standard scheduling
3. Existing Grove-managed pods continue running (no disruption)
4. New PodCliqueSet creation will fail until issue is resolved
5. Can roll back Koordinator to previous version without data loss (Grove CRDs remain)

## Additional Details

### Test Plan

#### Unit Tests

- **Scheduler Plugin Tests**
  - Gang scheduling logic for PodGang resources
  - Hierarchical gang validation (gang of gangs)
  - Note: Startup ordering (`startsAfter`) is enforced by Grove operator via init containers, not by scheduler

- **Controller Tests**  
  - Reservation integration with PodCliques
  - Grove operator's init container injection for `startsAfter` dependencies

#### Integration Tests

- **End-to-End Scenarios**
  - Deploy multi-node disaggregated inference system
  - Verify all pods in gang are scheduled together or none are scheduled
  - Validate startup ordering via `startsAfter` (workers ready before leader starts)
  - Test autoscaling of PodCliqueSet (scale up/down)
  - Verify topology-aware placement

- **Feature Interaction Tests**
  - Grove + Reservation: Pre-allocate resources for PodCliqueSets
  - Grove + Preemption: Ensure gangs are preempted atomically
  - Grove + ElasticQuota: Respect quota limits for gang workloads
  - Grove + Device Scheduling: GPU allocation within gangs

- **Failure Scenarios**
  - Gang scheduling timeout (cleanup partial pods)
  - Node failure during gang deployment
  - Insufficient resources for minimum gang size
  - Network partition affecting topology placement

#### Manual Tests

- **User Experience**
  - Deploy real inference workloads (LLaMA, DeepSeek models)
  - Validate end-to-end latency improvements from topology placement
  - Test kubectl plugin usability
  - Verify documentation accuracy

- **Upgrade/Downgrade**
  - Upgrade from Koordinator vX to vX+1 with Grove integration
  - Downgrade and verify existing workloads continue running
  - Migrate from standalone Grove to Koordinator+Grove

#### Acceptance Criteria

- All unit tests pass with >80% code coverage for new components
- Integration tests cover all user stories in this proposal
- Documentation includes at least 3 complete end-to-end examples

## Implementation History

- [x] 14/01/2026: Proposed idea and initial draft proposal
- [ ] XX/XX/2026: First round of feedback from Koordinator community
- [ ] XX/XX/2026: Open proposal PR for formal review
- [ ] XX/XX/2026: Address review comments and finalize proposal
- [ ] XX/XX/2026: Begin implementation (alpha phase)
  - Grove CRD integration
  - Basic gang scheduling for PodGang
  - Feature gate implementation