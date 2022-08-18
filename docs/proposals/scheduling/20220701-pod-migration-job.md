---
title: Pod Migration Job
authors:
  - "@eahydra"
reviewers:
  - "@hormes"
  - "@allwmh"
  - "@jasonliu747"
  - "@saintube"
  - "@stormgbs"
  - "@zwzhang0107"
creation-date: 2022-07-01
last-updated: 2022-07-13
status: provisional
---

# Pod Migration Job

## Table of Contents
<!-- TOC -->

- [Pod Migration Job](#pod-migration-job)
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
            - [Story 3](#story-3)
        - [Basic Migration API](#basic-migration-api)
        - [Pod Migration Job CRD](#pod-migration-job-crd)
            - [Migration Job Spec](#migration-job-spec)
            - [Migration Job Status](#migration-job-status)
        - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
            - [PodMigrationJob Controller](#podmigrationjob-controller)
                - [Group PodMigrationJob](#group-podmigrationjob)
                - [Filter PodMigrationJob](#filter-podmigrationjob)
                - [Sort PodMigrationJob](#sort-podmigrationjob)
                - [Execute PodMigrationJob](#execute-podmigrationjob)
                - [Migration Stability mechanism](#migration-stability-mechanism)
            - [Controller Configuration](#controller-configuration)
    - [Alternatives](#alternatives)
    - [Implementation History](#implementation-history)

<!-- /TOC -->
## Glossary

## Summary

This proposal defines a CRD-based Pod migration API, through which the descheduler or other automatic fault recovery components can evict or delete Pods more safely. At the same time, the proposal also describes the specific implementation details of the API.

## Motivation

Migrating Pods is an important capability that many components (such as deschedulers) rely on, and can be used to optimize scheduling or help resolve workload runtime quality issues. We believe that pod migration is a complex process, involving steps such as auditing, resource allocation, and application startup, and is mixed with application upgrading, scaling scenarios, and resource operation and maintenance operations by cluster administrators. Therefore, how to manage the stability risk of this process to ensure that the application does not fail due to the migration of Pods is a very critical issue that must be resolved.

Therefore, it is necessary to realize a final state-oriented migration capability based on CRD, track the status of each process in the migration, and perceive scenarios such as upgrading and scaling of the application.

### Goals

1. Defines a CRD-based Pod Migration Job API, through which the descheduler can evict or delete Pods more safely.
2. Describe in detail the design details behind the API.

### Non-Goals/Future Work

1. A new descheduler framework
2. Descheduling capability for different scenarios such as load-aware descheduling, defragemention, etc.
3. The details about Deterministic preemption that preempts other Pods for Reservation.

## Proposal

### User Stories

#### Story 1

The descheduler in the K8s community evicts pods to be rescheduled according to different strategies. However, it does not guarantee whether the evicted Pod has resources available after re-creation. If a large number of new Pods are in the Pending state when the resources in the cluster are tight, may lower the application availabilities.

#### Story 2

The descheduler evicts the Pod through the Eviction API, and the Eviction API decides whether to delete the Pod according to the PDB status. However, it is unable to perceive workload upgrades, scaling and other scenarios in which Pods are deleted, which will also bring security risks.

#### Story 3

The Pod migration capability itself can be provided to users as a service. Users can integrate this API in their own systems to achieve safe migration, and are no longer limited to deschedulers.


### Basic Migration API

These APIs provide cluster administrators with more fine-grained migration control capabilities, which can better reduce risks.

- `scheduling.koordinator.sh/eviction-cost` indicates the eviction cost. It can be used to set to an int32. The implicit eviction cost for pods that don't set the annotation is 0, negative values are permitted. If set the cost ith `math.MaxInt32`, it means the Pod will not be evicted. Pods with lower eviction cost are preferred to be evicted before pods with higher eviction cost. If a batch of Pods to be evicted have the same priority, they will be sorted by cost, and the Pod with the smallest cost will be evicted. Although the K8s community has [Pod Deletion Cost #2255](https://github.com/kubernetes/enhancements/issues/2255), it is not a general mechanism. To avoid conflicts with components that use `Pod Deletion Cost`, users can individually mark the eviction cost for Pods. 


### Pod Migration Job CRD

In order to support the above user stories, a Custom Resource Definition(CRD) named `PodMigrationJob` is proposed to ensure the migration process safely.  

#### Migration Job Spec

```go

// PodMigrationJob is the Schema for the PodMigrationJob API
// +k8s:openapi-gen=true
// +kubebuilder:resource:scope=Cluster
type PodMigrationJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PodMigrationJobSpec   `json:"spec,omitempty"`
	Status            PodMigrationJobStatus `json:"status,omitempty"`
}

type PodMigrationJobSpec struct {
	// Paused indicates whether the PodMigrationJob should to work or not.
	// Default is false
	// +optional
	Paused bool `json:"paused,omitempty"`

	// TTL controls the PodMigrationJob timeout duration.
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// Mode represents the operating mode of the Job
	// Default is PodMigrationJobModeReservationFirst
	// +optional
	Mode PodMigrationJobMode `json:"mode,omitempty"`

	// PodRef represents the Pod that be migrated
	// +required
	PodRef *corev1.ObjectReference `json:"podRef"`

	// ReservationOptions defines the Reservation options for migrated Pod
	// +optional
	ReservationOptions *PodMigrateReservationOptions `json:"reservationOptions,omitempty"`

	// DeleteOptions defines the deleting options for the migrated Pod and preempted Pods
	// +optional
	DeleteOptions *metav1.DeleteOptions `json:"deleteOptions,omitempty"`
}

type PodMigrationJobMode string

const (
	PodMigrationJobModeReservationFirst PodMigrationJobMode = "ReservationFirst"
	PodMigrationJobModeEvictionDirectly PodMigrationJobMode = "EvictDirectly"
)

type PodMigrateReservationOptions struct {
	// ReservationRef if specified, PodMigrationJob will check if the status of Reservation is available.
	// ReservationRef if not specified, PodMigrationJob controller will create Reservation by Template,
	// and update the ReservationRef to reference the Reservation
	// +optional
	ReservationRef *corev1.ObjectReference `json:"reservationRef,omitempty"`

	// Template is the object that describes the Reservation that will be created if not specified ReservationRef
	// +optional
	Template *ReservationTemplateSpec `json:"template,omitempty"`

	// PreemptionOption decides whether to preempt other Pods.
	// The preemption is safe and reserves resources for preempted Pods.
	// +optional
	PreemptionOptions *PodMigrationJobPreemptionOptions `json:"preemptionOptions,omitempty"`
}

type PodMigrationJobPreemptionOptions struct {
	// Reserved object.
}
```

- `Paused` indicates whether the PodMigrationJob should to work or not. In some scenarios, the user does not expect the PodMigrationJob Controller to process the PodMigrationJob immediately, but rather to decide whether to execute it after completing some operations similar to auditing.
- `TimeoutInSeconds` controls the PodMigrationJob timeout duration.
- The `PodMigrationJob` support two modes defined by the field `Mode`:
  - `PodMigrationJobModeReservationFirst` means that before migrating a Pod, try to reserve resources through the `Reservation` API, delete the Pod to be migrated after successfully reserved, and observe the status of the `Reservation` to ensure that the `Reservation` is consumed.
  - `PodMigrationJobModeEvictionDirectly` indicates that the user clearly knows the risk of evicting the Pod and decides to evict the Pod directly.
  - If `Mode` is not specified, `PodMigrationJobModeReservationFirst` is used by default
- `PodRef` represents the Pod that be migrated.  The field is required.
- `ReservationOptions` defines options for how to reserve resource through `Reservation` API:
  - `ReservationRef` if is specified, the referenced `Reservation` instance is used first. In some scenarios, such as defragmentation, in order to ensure the reliability of the upper-layer logic, resources may have been reserved on the target node. In this case, the specified `Reservation` can be used directly.
  - `Template` describes the spec of `Reservation`. It is often not necessary to set this field. When neither `ReservationRef` nor `Template` is specified, the `PodMigrationJob controller` will construct the `ReservationSpec` reserved resources according to the Spec of the migrated Pod. If `Template` is set, the `ReservationTemplateSpec` and the Spec of the migrated Pod will be merged to construct the `ReservationSpec` reserved resources.
  - `PreemptionOptions` decides whether to preempt other Pods if reserved resources failed. The specific details of preemption will be submitted in a separate proposal description in future work, and will not be expanded here for the time being.
- `DeleteOptions` defines the options of delete operation. Whether to delete a Pod through the `K8s Delete API` or evict a Pod through the `K8s Eviction API` depends on how the user configures the parameters of the `PodMigrationJob Controller`. Users only need to set `DeleteOptions` according to the workload in their own cluster.

#### Migration Job Status

```go
type PodMigrationJobStatus struct {
	// PodMigrationJobPhase represents the phase of a PodMigrationJob is a simple, high-level summary of where the PodMigrationJob is in its lifecycle.
	// e.g. Pending/Running/Failed
	Phase PodMigrationJobPhase `json:"phase,omitempty"`
	// Status represents the current status of PodMigrationJob
	// e.g. ReservationCreated
	Status string `json:"status,omitempty"`
	// Reason represents a brief CamelCase message indicating details about why the PodMigrationJob is in this state.
	Reason string `json:"reason,omitempty"`
	// Message represents a human-readable message indicating details about why the PodMigrationJob is in this state.
	Message string `json:"message,omitempty"`
	// Conditions records the stats of PodMigrationJob
	Conditions []PodMigrationJobCondition `json:"conditions,omitempty"`
	// NodeName represents the node's name of migrated Pod
	NodeName string `json:"nodeName,omitempty"`
	// PodRef represents the newly created Pod after being migrated
	PodRef *corev1.ObjectReference `json:"podRef,omitempty"`
	// PreemptedPodsRef represents the Pods that be preempted
	PreemptedPodsRef []corev1.ObjectReference `json:"preemptedPodsRef,omitempty"`
	// PreemptedPodsReservations records information about Reservations created due to preemption
	PreemptedPodsReservations []PodMigrationJobPreemptedReservation `json:"preemptedPodsReservation,omitempty"`
}

type PodMigrationJobPreemptedReservation struct {
	// Namespace represents the namespace of Reservation
	Namespace string `json:"namespace,omitempty"`
	// Name represents the name of Reservation
	Name string `json:"name,omitempty"`
	// NodeName represents the assigned node for Reservation by scheduler
	NodeName string `json:"nodeName,omitempty"`
	// Phase represents the Phase of Reservation
	Phase string `json:"phase,omitempty"`
	// PreemptedPodRef represents the Pod that be preempted
	PreemptedPodRef *corev1.ObjectReference `json:"preemptedPodRef,omitempty"`
	// PodsRef represents the newly created Pods after being preempted
	PodsRef []corev1.ObjectReference `json:"podsRef,omitempty"`
}

type PodMigrationJobCondition struct {
	// Type is the type of the condition.
	Type PodMigrationJobConditionType `json:"type"`
	// Status is the status of the condition.
	// Can be True, False, Unknown.
	Status PodMigrationJobConditionStatus `json:"status"`
	// Last time we probed the condition.
	// +nullable
	LastProbeTime metav1.Time `json:"lastProbeTime,omitempty"`
	// Last time the condition transitioned from one status to another.
	// +nullable
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// Unique, one-word, CamelCase reason for the condition's last transition.
	Reason string `json:"reason,omitempty"`
	// Human-readable message indicating details about last transition.
	Message string `json:"message,omitempty"`
}

type PodMigrationJobPhase string

const (
	// PodMigrationJobPending represents the initial status
	PodMigrationJobPending PodMigrationJobPhase = "Pending"
	// PodMigrationJobRunning represents the PodMigrationJob is being processed
	PodMigrationJobRunning PodMigrationJobPhase = "Running"
	// PodMigrationJobSucceed represents the PodMigrationJob processed successfully
	PodMigrationJobSucceed PodMigrationJobPhase = "Succeed"
	// PodMigrationJobFailed represents the PodMigrationJob process failed caused by Timeout, Reservation failed, etc.
	PodMigrationJobFailed PodMigrationJobPhase = "Failed"
	// PodMigrationJobAborted represents the user forcefully aborted the PodMigrationJob.
	PodMigrationJobAborted PodMigrationJobPhase = "Aborted"
)

// These are valid conditions of PodMigrationJob.
const (
	PodMigrationJobConditionReservationCreated             PodMigrationJobConditionType = "ReservationCreated"
	PodMigrationJobConditionReservationScheduled           PodMigrationJobConditionType = "ReservationScheduled"
	PodMigrationJobConditionPreemption                     PodMigrationJobConditionType = "Preemption"
	PodMigrationJobConditionEviction                       PodMigrationJobConditionType = "Eviction"
	PodMigrationJobConditionPodScheduled                   PodMigrationJobConditionType = "PodScheduled"
	PodMigrationJobConditionReservationPodBoundReservation PodMigrationJobConditionType = "PodBoundReservation"
	PodMigrationJobConditionReservationBound               PodMigrationJobConditionType = "ReservationBound"
)

// These are valid reasons of PodMigrationJob.
const (
	PodMigrationJobReasonTimeout                   = "Timeout"
	PodMigrationJobReasonFailedCreateReservation   = "FailedCreateReservation"
	PodMigrationJobReasonUnschedulable             = "Unschedulable"
	PodMigrationJobReasonMissingPod                = "MissingPod"
	PodMigrationJobReasonMissingReservation        = "MissingReservation"
	PodMigrationJobReasonPreempting                = "Preempting"
	PodMigrationJobReasonPreemptComplete           = "PreemptComplete"
	PodMigrationJobReasonEvicting                  = "Evicting"
	PodMigrationJobReasonFailedEvict               = "FailedEvict"
	PodMigrationJobReasonEvictComplete             = "EvictComplete"
	PodMigrationJobReasonWaitForPodBindReservation = "WaitForPodBindReservation"
)

type PodMigrationJobConditionStatus string

const (
	PodMigrationJobConditionStatusTrue    PodMigrationJobConditionStatus = "True"
	PodMigrationJobConditionStatusFalse   PodMigrationJobConditionStatus = "False"
	PodMigrationJobConditionStatusUnknown PodMigrationJobConditionStatus = "Unknown"
)
```

### Implementation Details/Notes/Constraints

#### PodMigrationJob Controller

The difference between `PodMigrationJobController` and general controller is that `PodMigrationJobController` will evaluate all pending PodMigrationJobs together (ie PodMigrationJob.Phase is Pending) and select a batch of PodMigrationJob and reconcile them. This selection process is called the arbitration mechanism. The reason why the arbitration mechanism is introduced is mainly to control the stability risk and control the cost of migrating Pods. The arbitration mechanism includes three stages: `Group`, `Filter` and `Sort`.

##### Group PodMigrationJob

Aggregate according to different workloads to facilitate the processing of subsequent processes

- Aggregate PodMigrationJob by workload
- Aggregate PodMigrationJob by Node
- Aggregate PodMigrationJob by Namespace

##### Filter PodMigrationJob

- Check how many PodMigrationJob of each workload are in the Running state, and record them as ***migratingReplicas***. If the ***migratingReplicas*** reach a certain threshold, they will be excluded. The detailed algorithm of this threshold is described later.
- Check the number of ***unavailableReplicas*** of each workload, and determine whether the ***unavailableReplicas + migratingReplicas*** conform to the corresponding [PDB(Pod Disruption Budget)](https://kubernetes.io/docs/tasks/run-application/configure-pdb/) or [PUB(Pod Unavailable Budget)](https://openkruise.io/docs/user-manuals/podunavailablebudget). If there is no PDB or PUB, use the algorithm to calculate dynamically. If not, exclude the corresponding PodMigrationJob.
- Check the number of Pods being migrated on the node where each target Pod is located. If it exceeds the maximum migration amount for a single node, exclude it.
- Check the number of Pods being migrated in the Namespace where each target Pod is located. If it exceeds the maximum migration amount for a single Namespace, exclude it

The detailed algorithm of Workload Max Migrating/Unavailable Replicas:

```go
func GetMaxMigrating(replicas int, intOrPercent *intstr.IntOrString) (int, error) {
	return GetMaxUnavailable(replicas, intOrPercent)
}

func GetMaxUnavailable(replicas int, intOrPercent *intstr.IntOrString) (int, error) {
	if intOrPercent == nil {
		if replicas > 10 {
			s := intstr.FromString("10%")
			intOrPercent = &s
		} else if replicas >= 4 && replicas <= 10 {
			s := intstr.FromInt(2)
			intOrPercent = &s
		} else {
			s := intstr.FromInt(1)
			intOrPercent = &s
		}
	}
	return intstr.GetValueFromIntOrPercent(intOrPercent, replicas, true)
}
``` 

##### Sort PodMigrationJob

- Pods with higher QoS requirements are given priority, LSE > LSR > LS > BE
- Pods with higher priority will be processed first
- The higher migration priority will be processed first
- If the Pod has already initiated a migration job in the past and it fails, sort by the number of times. The lower the number of times, the priority will be given to processing
- If the workload where the Pod is located has been descheduled for a certain number of times in the past, it is sorted according to the number of times. The lower the number of times, the priority will be processed.
- Sort by the number of replicas being migrated by the workload. The lower the number of replicas being migrated, the priority will be given to processing.

##### Execute PodMigrationJob

- Update PodMigrationJobStatus.Phase to Running to trigger the PodMigrationJob controller reconcile these jobs
- PodMigrationJob controller reconciles process:
  - If the mode of PodMigrationJob is `EvictionDirectly`, just delete the Pod through the delete method that configured in PodMigrationJob controller. And update the phase of PodMigrationJob to Success.
  - If not specified ReservationOptions.ReservationRef, create the Reservation instance by the reservation template or Pod  spec to reserve resources. And updates the created Reservation instance to the ReservationOptions.ReservationRef.
  - Check the status of Reservation to determine whether reserve resource successfully.
  - If failed to reserve, abort the PodMigrationJob and update the phase of PodMigrationJob to Fail
  - If successfully reserve, delete the Pod through the delete method that configured in PodMigrationJob controller.
  - Check the Reservation status to determine whether the Reservation consumed.
  - If Reservation consumed, tracks the status of Reservation and update the status to PodMigrationJob
  - Update phase of PodMigrationJob to Success.

##### Migration Stability mechanism

- Support for disabling this capability by configuration 
- Supports a simple central flow control mechanism to limit the number of migrations over a period of time. 

See the Configuration section for more details

#### Controller Configuration

User can configure the `MigrationControllerArgs` through Koordinator Descheduler ConfigMap. 

```go
// MigrationControllerArgs holds arguments used to configure the MigrationController
type MigrationControllerArgs struct {
	metav1.TypeMeta

	// DryRun means only execute the entire migration logic except create Reservation or Delete Pod
	// Default is false
	DryRun bool `json:"dryRun,omitempty"`

	// MaxConcurrentReconciles is the maximum number of concurrent Reconciles which can be run. Defaults to 1.
	MaxConcurrentReconciles *int32 `json:"maxConcurrentReconciles,omitempty"`

	// EvictFailedBarePods allows pods without ownerReferences and in failed phase to be evicted.
	EvictFailedBarePods bool `json:"evictFailedBarePods"`

	// EvictLocalStoragePods allows pods using local storage to be evicted.
	EvictLocalStoragePods bool `json:"evictLocalStoragePods"`

	// EvictSystemCriticalPods allows eviction of pods of any priority (including Kubernetes system pods)
	EvictSystemCriticalPods bool `json:"evictSystemCriticalPods"`

	// IgnorePVCPods prevents pods with PVCs from being evicted.
	IgnorePvcPods bool `json:"ignorePvcPods"`

	// LabelSelector sets whether to apply label filtering when evicting.
	// Any pod matching the label selector is considered evictable.
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	// Namespaces carries a list of included/excluded namespaces
	Namespaces *Namespaces `json:"namespaces,omitempty"`

	// MaxMigratingPerNode represents he maximum number of pods that can be migrating during migrate per node.
	MaxMigratingPerNode *int32 `json:"maxMigratingPerNode,omitempty"`

	// MaxMigratingPerNamespace represents he maximum number of pods that can be migrating during migrate per namespace.
	MaxMigratingPerNamespace *int32 `json:"maxMigratingPerNamespace,omitempty"`

	// MaxMigratingPerWorkload represents he maximum number of pods that can be migrating during migrate per workload.
	// Value can be an absolute number (ex: 5) or a percentage of desired pods (ex: 10%).
	MaxMigratingPerWorkload *intstr.IntOrString `json:"maxMigratingPerWorkload,omitempty"`

	// MaxUnavailablePerWorkload represents he maximum number of pods that can be unavailable during migrate per workload.
	// The unavailable state includes NotRunning/NotReady/Migrating/Evicting
	// Value can be an absolute number (ex: 5) or a percentage of desired pods (ex: 10%).
	MaxUnavailablePerWorkload *intstr.IntOrString `json:"maxUnavailablePerWorkload,omitempty"`

	// DefaultJobMode represents the default operating mode of the PodMigrationJob
	// Default is PodMigrationJobModeReservationFirst
	DefaultJobMode string `json:"defaultJobMode,omitempty"`

	// DefaultJobTTL represents the default TTL of the PodMigrationJob
	// Default is 5 minute
	DefaultJobTTL *metav1.Duration `json:"defaultJobTTL,omitempty"`

	// EvictQPS controls the number of evict per second
	EvictQPS string `json:"evictQPS,omitempty"`
	// EvictBurst is the maximum number of tokens
	EvictBurst int32 `json:"evictBurst,omitempty"`
	// EvictionPolicy represents how to delete Pod, support "Delete" and "Eviction", default value is "Eviction"
	EvictionPolicy string `json:"evictionPolicy,omitempty"`
	// DefaultDeleteOptions defines options when deleting migrated pods and preempted pods through the method specified by EvictionPolicy
	DefaultDeleteOptions *metav1.DeleteOptions `json:"defaultDeleteOptions,omitempty"`
}
```

## Alternatives

## Implementation History

- 2022-07-01: Initial proposal
- 2022-07-11: Refactor proposal for review
- 2022-07-13: Update proposal based on review comments
- 2022-07-22: Update Spec
- 2022-08-02: Update MigrationJob configuration
- 2022-08-25: Update MigrationJob configuration