---
title: PodMigrationJob Support Eviction Conditions
authors:
  - "@sjtufl"
reviewers:
  - "@saintube"
  - "@eahydra"
  - "@zwzhang0107"
  - "@hormes"
creation-date: 2023-11-29
last-updated: 2023-12-01
---

# PodMigrationJob Support Eviction Conditions

## Table of Contents

- [PodMigrationJob Support Eviction Conditions](#podmigrationjob-support-eviction-conditions)
	- [Table of Contents](#table-of-contents)
	- [Summary](#summary)
	- [Motivation](#motivation)
		- [Goals](#goals)
		- [Non-Goals/Future Work](#non-goalsfuture-work)
	- [Proposal](#proposal)
		- [User Stories](#user-stories)
			- [Story 1](#story-1)
			- [Story 2](#story-2)
		- [API Changes](#api-changes)
		- [Configuration API](#configuration-api)
		- [Workflow](#workflow)
			- [Basic workflow illustration](#basic-workflow-illustration)
			- [Notes](#notes)


## Summary

PodMigrationJob supports two types of pod migration modes currently, i.e. `EvictDirectly` and `ReservationFirst`. Pod eviction is performed directly, or after Reservation is successfully scheduled. However, as an operation at high risk, evicting pod must meet some external interception conditions in scenarios with high technical risk requirements (such as financial scenarios). Typical eviction conditions include cutting off North/South & East/West traffic, getting approval from change risk control system, etc. Therefore, the eviction conditions mechanism is needed to let the PodMigrationJob check whether the predefined interception conditions have been met before performing the actual eviction operation, reducing the risk of pod migration.

## Motivation

### Goals

- Introduce mechanism for PodMigrationJob to make sure that some eviction conditions are met before performing actual evictions on pods.

### Non-Goals/Future Work

- Embed validation logics of various eviction conditions inside PodMigrationJob controller.
- Illustrate details of eviction conditions.

## Proposal

### User Stories

#### Story 1

Some users have built their own operators to fully manage the North/South & East/West traffic during the whole lifecycle of their pods, so that errors and exceptions could be avoided when containers are created or destroyed. The mission of traffic operator can be simply summarized as automatically turning on traffic before the pod is brought online, and automatically turning off traffic before the pod goes offline. Through the PodMigrationJob eviction condition mechanism, the condition that traffic is turned off before evicting the pod can be ensured. This helps users to reduce technological risks during the pod migration process. 

#### Story 2

Some companies have built technical risk control platform, which hosts a variety of risk control rules based on static threshold detection and anomaly detection algorithms. Change actions to production environment such as workload scaling, upgrading, etc., can only be executed after the approval being granted by the platform. Pod migration is also a risky action and getting approval from the risk control platform could be an eviction condition of PodMigrationJob.

### API Changes

Add an array type field `EvictionConditions` to `PodMigrationJobSpec`, which contains a list of eviction conditions. The length of `EvictionConditions` can be zero, thus no external conditions need to be met before pod eviction.

```go
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

	// EvictionConditions contains the key list of the
	// eviction conditions.
	// +optional
	EvictionConditions []EvictionCondition `json:"evictionConditions,omitempty"`

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


type EvictionCondition struct {
	Name string `json:"name,omitempty"`
}
```

### Configuration API

`PodMigrationJobProfile` is introduced to better help inject eviction conditions to `PodMigrationJob` automatically. Specified eviction conditions will only be added for `PodMigrationJobs` that matches the pod namespace selector, label selector, or both.

```go
// PodMigrationJobProfileSpec is a description of a PodMigrationJobProfile.
type PodMigrationJobProfileSpec struct {
	// NamespaceSelector decides whether to mutate PMJs if the
	// namespace of corresponding pod matches the selector.
	// Default to the empty LabelSelector, which matches everything.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// Selector decides whether to mutate PMJs if the
	// corresponding pod matches the selector.
	// Default to the empty LabelSelector, which matches everything.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// EvictionConditions contains the list of eviction conditions
	// to be added on selected PodMigrationJob.
	// +optional
	EvictionConditions []EvictionCondition `json:"evictionConditions,omitempty"`
}
```

### Workflow

#### Basic workflow illustration

![image](/docs/images/pmj-eviction-conditions.svg)

#### Notes

- `ExternalJob` is actually a **general concept** of external operations, e.g., it could be turn-off-traffic job which will be handled by user's traffic operator.
- The conditions status should be updated by external-controller-coordinator once `ExternalJob` is completed.
- PodMigrationJob controller respects eviction conditions, but the implementation details of `ExternalJob` is out of the scope. It should be customized according to the needs of the adopted users. 
