---
title: Inference Orchestration Enhancement with Grove Integration
authors:
  - ""
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

The integration will leverage Koordinator's network topology awareness and resource scheduling capabilities while adopting Grove's orchestration abstractions (PodClique, PodCliqueScalingGroup, PodCliqueSet) to enable hierarchical gang scheduling, startup ordering, and multi-level autoscaling for inference workloads.

## Motivation

Modern AI inference workloads, especially large language models (LLMs) and multi-modal models, present orchestration challenges that exceed the capabilities of standard Kubernetes primitives:

1. **Multi-Node Model Instances**: Large models (e.g., DeepSeek-R1, Llama-4-Maverick) are often sharded across multiple nodes. A single model instance spans multiple pods, making the fundamental scaling unit a group of pods rather than individual pods.

2. **Disaggregated Inference Architectures**: To optimize resource utilization and performance, inference is increasingly split into separate stages (prefill for prompt processing, decode for token generation) running on different pods/nodes. These components must be scheduled together to form functional pipelines.

3. **Hierarchical Gang Scheduling**: Multi-node deployments require gang scheduling at multiple levels - pods within a model instance must be scheduled together, and in disaggregated architectures, at least one prefill instance and one decode instance must be scheduled to form a working system.

4. **Startup Ordering**: Even when components are gang-scheduled, they often need to start in specific order (e.g., MPI workloads require workers to be ready before the leader starts).

5. **Topology-Aware Placement**: Inference components communicate heavily. Placement within NVLink domains or network-optimized locations is crucial for minimizing latency and maximizing throughput.

Grove (https://github.com/ai-dynamo/grove) provides a proven solution to these challenges with a clean Kubernetes-native API. Integrating Grove with Koordinator will combine Grove's inference-specific orchestration patterns with Koordinator's advanced scheduling capabilities and resource management, enabling native support for Dynamo (https://github.com/ai-dynamo/dynamo).

### Goals

- Enable orchestration of multi-node, disaggregated inference workloads in Koordinator through Grove integration
- Support hierarchical gang scheduling for inference workloads with multiple component types (prefill, decode, leader, worker, etc.)
- Implement startup ordering to ensure correct initialization sequences for distributed inference systems
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
4. **Coordinate Controllers**: Ensure Grove's operator and Koordinator's controllers work harmoniously for reservation, preemption, and resource management
5. **Provide Migration Path**: Enable gradual adoption for users with existing inference workloads

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

#### Story 2: Agentic AI Pipeline with Multiple Models

As an AI application developer, I want to run an agentic pipeline where:
- A router model (single GPU) directs requests to specialized models
- A vision model (2 GPUs) handles image understanding  
- A reasoning model (4 GPUs, tensor-parallelized) handles complex logic
- A code generation model (2 GPUs) produces code outputs
- All components must be scheduled together - the pipeline is non-functional if any component is missing
- Vision and reasoning models should start before the router (startup ordering)
- The entire pipeline should scale as a unit based on end-to-end latency

With Grove + Koordinator, I can define PodCliques for each model, group them in a PodCliqueScalingGroup for gang scheduling, and specify startup ordering. Koordinator's scheduler ensures all components are placed together, and autoscaling adjusts the entire pipeline as one unit.

#### Story 3: MPI-Based Multi-Node Training/Inference

As an ML researcher, I want to run a multi-node MPI workload for inference where:
- One leader pod coordinates the MPI job
- N worker pods perform distributed computation
- All pods must be gang-scheduled (if only some workers are available, the job cannot run)
- Worker pods must be fully ready before the leader pod starts the MPI application
- Pods should be placed on nodes with high-speed interconnects (e.g., InfiniBand, NVLink)

Using Grove's PodClique with startup ordering and Koordinator's topology awareness, I can ensure workers start first, leader waits for workers to be ready, and all pods are placed optimally for MPI communication patterns.

### Requirements (Optional)

#### Functional Requirements

##### FR1: Hierarchical Gang Scheduling Support

**MUST**: Koordinator scheduler must recognize and enforce PodGang scheduling constraints with support for hierarchical gang requirements (gang of gangs). If minimum replica requirements cannot be satisfied at any level, no pods in the gang should be scheduled.

##### FR2: Startup Ordering

**MUST**: Support explicit startup ordering within PodCliqueScalingGroups. Dependent pods must not start until prerequisite pods reach Ready state.

##### FR3: Topology-Aware Placement for Grove Workloads  

**MUST**: Integrate Grove's placement requirements with Koordinator's NetworkTopology plugin to prefer scheduling pods within the same NVLink domain, network zone, or other topology levels as specified.

##### FR4: Multi-Level Autoscaling

**SHOULD**: Support autoscaling at both PodClique level (scaling individual components) and PodCliqueSet level (scaling entire inference systems as units).

##### FR5: Reservation Compatibility

**MUST**: Grove-managed pods must be compatible with Koordinator's Reservation mechanism for resource pre-allocation and guaranteed scheduling.

##### FR6: API Compatibility

**MUST**: Adopt Grove's upstream API definitions without breaking changes. Koordinator-specific extensions should use annotations or separate CRDs.

#### Non-Functional Requirements

##### NFR1: Scheduling Latency

**SHOULD**: Gang scheduling decisions for Grove workloads should complete within 10 seconds for workloads with up to 100 pods per gang under normal cluster load.

##### NFR2: Scalability

**MUST**: Support clusters with up to 10,000 nodes and 1,000 concurrent PodCliqueSet instances without significant performance degradation.

##### NFR3: Backward Compatibility  

**MUST**: Existing Koordinator features (QoS, ElasticQuota, Device Scheduling) must continue to work for non-Grove workloads. Grove integration should be opt-in.

##### NFR4: Observability

**SHOULD**: Provide clear metrics and events for gang scheduling decisions, topology placement, and autoscaling actions to facilitate debugging and monitoring.

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
- Prefer placing pods from the same PodClique in the same topology zone
- Score nodes based on:
  - Network distance between components (prefill ↔ decode)
  - Available GPU resources
  - NVLink/interconnect availability
- Add new scoring algorithm: `NetworkAffinityForPodClique`

**Startup Ordering**:
- Extend scheduler to support pod dependencies within PodCliqueScalingGroup
- Introduce new condition: `PodStartupBlocked` with reason field
- Scheduler marks dependent pods as `PodStartupBlocked` until prerequisites are Ready
- Use Kubernetes Pod readiness gates for coordination

##### 3. Controller Coordination

**Grove Operator**:
- Deploy Grove operator as-is (upstream compatibility)
- Grove creates PodGang CRs consumed by Koordinator scheduler
- Grove manages autoscaling based on metrics (CPU, GPU, custom metrics)

**Koordinator Controllers**(Need to check):
- Ensure Reservation controller recognizes Grove-managed pods
- Device scheduling continues to work for GPU allocation within gang
- QoS manager applies policies to all pods in a gang uniformly

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
apiVersion: grove.ai/v1alpha1
kind: PodCliqueSet
metadata:
  name: llama-inference
  annotations:
    scheduling.koordinator.sh/network-topology-zone: nvlink-domain-1
spec:
  replicas: 2  # Two complete prefill+decode systems
  template:
    podCliqueScalingGroups:
    - name: inference-pipeline
      minAvailable: 1  # At least one complete pipeline
      podCliques:
      - name: prefill
        replicas: 4
        startOrder: 0  # Start prefill first
        template:
          spec:
            containers:
            - name: prefill
              image: llama-prefill:latest
              resources:
                requests:
                  nvidia.com/gpu: 2
      - name: decode  
        replicas: 8
        startOrder: 1  # Start decode after prefill
        template:
          spec:
            containers:
            - name: decode
              image: llama-decode:latest
              resources:
                requests:
                  nvidia.com/gpu: 2
```

**Example 2: MPI Workload with Leader-Worker**

```yaml
apiVersion: grove.ai/v1alpha1
kind: PodCliqueSet
metadata:
  name: mpi-inference
spec:
  replicas: 1
  template:
    podCliqueScalingGroups:
    - name: mpi-job
      minAvailable: 1
      podCliques:
      - name: worker
        replicas: 8
        startOrder: 0  # Workers start first
        template:
          spec:
            containers:
            - name: mpi-worker
              image: mpi-worker:latest
      - name: leader
        replicas: 1  
        startOrder: 1  # Leader waits for workers
        template:
          spec:
            containers:
            - name: mpi-leader
              image: mpi-leader:latest
```

#### Constraints and Limitations

1. **Scheduling Complexity**: Hierarchical gang scheduling with topology awareness is computationally expensive. Large gangs (>100 pods) may experience increased scheduling latency.

2. **Startup Ordering Delays**: Strict startup ordering may increase total deployment time. Users should set appropriate timeouts.

3. **Cross-Namespace Limitations**: PodCliques within a PodCliqueSet must be in the same namespace. Cross-namespace gang scheduling is not supported in the initial implementation.

4. **Grove Version Compatibility**: This integration targets Grove v0.1.0-alpha.3+. Future Grove API changes may require updates to Koordinator.

### Risks and Mitigations

#### Risk 1: Upstream Grove API Changes

**Risk**: Grove is in alpha stage (v0.1.0-alpha.3). API breaking changes could require significant rework in Koordinator integration.

**Mitigation**: 
- Engage with Grove maintainers early and participate in API design discussions
- Use adapter pattern to isolate Grove API dependencies
- Pin to specific Grove versions with documented upgrade paths
- Contribute to Grove's API stabilization efforts

#### Risk 2: Scheduling Performance Degradation

**Risk**: Complex gang scheduling with topology awareness could significantly increase scheduling latency, especially in large clusters.

**Mitigation**:
- Implement incremental scheduling for large gangs (schedule subgroups progressively)
- Add caching layer for topology scoring calculations
- Provide configuration options to tune gang scheduling timeout and batch sizes
- Conduct performance benchmarks with 1000+ node clusters before GA release
- Implement scheduler metrics to detect performance regressions

#### Risk 3: Complexity for Users

**Risk**: Adding Grove concepts (PodClique, PodCliqueSet, etc.) increases learning curve for Koordinator users.

**Mitigation**:
- Provide comprehensive documentation with real-world examples
- Create migration guides from existing Koordinator workloads to Grove-based workloads
- Offer kubectl plugin for simplified Grove resource management
- Keep Grove integration optional - users not running inference workloads don't need to learn it
- Provide Helm chart templates for common inference patterns

#### Risk 4: Incompatibility with Existing Features

**Risk**: Grove's gang scheduling might conflict with Koordinator's Reservation, Preemption, or ElasticQuota features.

**Mitigation**:
- Conduct thorough integration testing with all existing Koordinator features
- Document known limitations and incompatibilities clearly
- Implement feature gates to allow gradual rollout and easy rollback
- Add validation webhooks to reject invalid configurations early

#### Risk 5: Security Concerns

**Risk**: Grove operator has elevated permissions to create/manage pods, which could be exploited if compromised.

**Mitigation**:
- Follow least-privilege principle for Grove operator RBAC
- Implement admission webhooks to validate PodCliqueSet specifications
- Conduct security review with Koordinator security team
- Enable audit logging for all Grove-related operations
- Support Pod Security Standards enforcement for Grove-created pods

#### Security Review

- Security review will be conducted by Koordinator security team
- Threat modeling for Grove operator and scheduler integration
- RBAC and admission control validation
- Penetration testing for privilege escalation scenarios
- Review by Kubernetes SIG-Auth if needed

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

**Decision**: Rejected. Grove provides battle-tested implementation with growing community adoption. Integration leverages existing work and allows Koordinator to focus on core scheduling capabilities.

### Alternative 2: Use Kubernetes Batch/Job APIs with Custom Controllers

Use native Kubernetes Job/CronJob with custom controllers for gang scheduling and topology awareness.

**Pros**:
- Leverages standard Kubernetes APIs
- Familiar to Kubernetes users
- No new CRDs to learn

**Cons**:
- Jobs are designed for batch workloads, not long-running inference services
- No built-in support for hierarchical gang scheduling
- Difficult to express startup ordering constraints
- Would require extensive custom controller development anyway

**Decision**: Rejected. Jobs API is insufficient for complex inference orchestration patterns.

### Alternative 3: Integrate with Volcano for Gang Scheduling

Use Volcano's gang scheduling capabilities instead of Grove.

**Pros**:
- Volcano is mature and widely used for batch/HPC workloads
- Already has gang scheduling implementation
- Active community

**Cons**:
- Volcano is primarily designed for batch jobs, not inference serving
- Lacks inference-specific abstractions (PodClique, disaggregated components)
- No built-in autoscaling for inference workloads
- Would still need custom controllers for startup ordering and multi-level scaling

**Decision**: Rejected. While Volcano provides gang scheduling, Grove offers higher-level abstractions specifically designed for inference workloads. Koordinator could potentially use Volcano internally for gang scheduling implementation, but Grove's API layer is more appropriate for inference use cases.

### Alternative 4: Wait for Kubernetes Native Support

Wait for Kubernetes to add native support for advanced scheduling features.

**Pros**:
- Eventually standardized across Kubernetes ecosystem
- No need for custom implementations

**Cons**:
- Timeline uncertain (could be years)
- May not address inference-specific requirements
- Users need solutions now for production workloads

**Decision**: Rejected. Current production needs require immediate solution. We can migrate to native Kubernetes features if/when they become available.


### Alternative 5: RGB solution
TODO:

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
       schedulerName: koordinator-scheduler
   ```
5. Monitor and validate functionality
6. Gradually migrate all workloads
7. Deprecate standalone scheduler if desired


**Feature Gate (temporary, during alpha/beta)**:
```bash
--feature-gates=GroveIntegration=true
```

### Rollback Strategy

If issues arise during upgrade:

1. Disable Grove feature gate: `--feature-gates=GroveIntegration=false`
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
  - Topology-aware scoring for PodCliques
  - Startup ordering constraint validation
  - Edge cases: partial gang satisfaction, timeout handling

- **Controller Tests**  
  - Grove CRD reconciliation with Koordinator features
  - Reservation integration with PodCliques
  - Device scheduling for gang-scheduled pods
  - QoS policy application to gangs

#### Integration Tests

- **End-to-End Scenarios**
  - Deploy multi-node disaggregated inference system
  - Verify all pods in gang are scheduled together or none are scheduled
  - Validate startup ordering (workers before leader)
  - Test autoscaling of PodCliqueSet (scale up/down)
  - Verify topology-aware placement (pods in same NVLink domain)

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

#### Performance Tests

- **Scalability Tests**
  - 1000-node cluster with 100 concurrent PodCliqueSet instances
  - Measure scheduling latency for gangs of varying sizes (10, 50, 100 pods)
  - Resource utilization of scheduler with Grove integration enabled
  - Comparison with baseline Koordinator (without Grove)

- **Stress Tests**  
  - Rapid create/delete of PodCliqueSet resources
  - Autoscaling storm (many PodCliqueSets scaling simultaneously)
  - Large gang sizes (500+ pods in single gang)

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
- Performance tests show <10% overhead vs. baseline Koordinator for non-Grove workloads
- Gang scheduling latency <10s for gangs with <100 pods in 1000-node cluster
- Zero data loss or workload disruption during upgrade/downgrade
- Security review completed with no critical findings
- Documentation includes at least 3 complete end-to-end examples

## Implementation History

- [x] 12/01/2025: Proposed idea and initial draft proposal
- [ ] 12/XX/2025: First round of feedback from Koordinator community
- [ ] 12/XX/2025: Present proposal at Koordinator community meeting
- [ ] 01/XX/2026: Open proposal PR for formal review
- [ ] 01/XX/2026: Address review comments and finalize proposal
- [ ] 02/XX/2026: Begin implementation (alpha phase)
  - Grove CRD integration
  - Basic gang scheduling for PodGang
  - Feature gate implementation