/*
Copyright 2022 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package extension

import corev1 "k8s.io/api/core/v1"

const (
	DomainPrefix = "koordinator.sh/"
	// ResourceDomainPrefix is a prefix "kubernetes.io/" used by particular extend resources (e.g. batch resources)
	ResourceDomainPrefix = corev1.ResourceDefaultNamespacePrefix
	// SchedulingDomainPrefix represents the scheduling domain prefix
	SchedulingDomainPrefix = "scheduling.koordinator.sh"
	// InternalSchedulingDomainPrefix represents the internal scheduling domain prefix
	InternalSchedulingDomainPrefix = "internal.scheduling.koordinator.sh"
	// NodeDomainPrefix represents the node domain prefix
	NodeDomainPrefix = "node.koordinator.sh"
	PodDomainPrefix  = "pod.koordinator.sh"

	LabelPodQoS      = DomainPrefix + "qosClass"
	LabelPodPriority = DomainPrefix + "priority"
	// LabelPodPriorityClass is used to revise those Pods that are already running and have Priority set, so that
	// Koordinator can be smoothly deployed to the running cluster. If you don't have a running Pod with
	// PriorityClass set, don't set this field specifically.
	LabelPodPriorityClass = DomainPrefix + "priority-class"

	LabelManagedBy = "app.kubernetes.io/managed-by"

	// LabelPodMutatingUpdate is a label key that pods with `pod.koordinator.sh/mutating-update=true` will
	// be mutated by Koordinator webhook when updating.
	LabelPodMutatingUpdate = PodDomainPrefix + "/mutating-update"

	// AnnotationNetworkQOS are used to set bandwidth for Pod. The unit is bps.
	// For example, 10M means 10 megabits per second.
	AnnotationNetworkQOS = DomainPrefix + "networkQOS"

	// LabelPodEvictEnabled is a label key that pods with `koordinator.sh/eviction-disabled` will
	// be able to evict.
	LabelPodEvictEnabled = DomainPrefix + "eviction-enabled"

	// AnnotationPodEvictPolicy are used to set restricted eviction policies for Pod.
	// When this annotation is missing, there are no policy restrictions
	AnnotationPodEvictPolicy = DomainPrefix + "eviction-policy"

	// AnnotationPodEvictionPriority is the pod-level eviction priority (int32 string, negative allowed).
	// In the koordlet resource eviction (e.g. MemoryEvict, CPUEvict), pods with lower values are evicted
	// before pods with higher values, taking precedence over spec.priority and the koordinator.sh/priority
	// label. Pods without the annotation take the implicit priority 0.
	AnnotationPodEvictionPriority = DomainPrefix + "eviction-priority"

	// LabelPodSkipEnhancedValidation is the pod label key used to opt out a pod from enhanced validation.
	LabelPodSkipEnhancedValidation = PodDomainPrefix + "/skip-enhanced-validation"

	// LabelPodPreAllocatable is the label key used to identify pre-allocatable pods in cluster mode.
	// When set to "true", the pod can be selected as a pre-allocatable candidate.
	LabelPodPreAllocatable = PodDomainPrefix + "/is-pre-allocatable"

	// AnnotationPodPreAllocatablePriority is the annotation key used to prioritize pre-allocatable pods in cluster mode.
	// The value should be a numeric string. Higher values indicate higher priority for pre-allocation.
	AnnotationPodPreAllocatablePriority = PodDomainPrefix + "/pre-allocatable-priority"

	// AnnotationOOMScoreAdj is the pod-level default oom_score_adj value (int64 string, range [-1000, 1000]).
	// The koordlet periodically reconciles the value to /proc/<pid>/oom_score_adj of all processes of the
	// pod's containers (including init/sidecar containers), so it supports hot-updating without restarting
	// the containers, with a latency up to the reconcile interval. Note that:
	// 1. The sandbox (pause) container is not affected since its oom_score_adj is managed by the runtime.
	// 2. Removing the annotation does NOT revert the processes to the runtime default values.
	AnnotationOOMScoreAdj = DomainPrefix + "oom-score-adj"

	// AnnotationOOMScoreAdjSpec is the container-level oom_score_adj spec in JSON format.
	// Example: {"container-a": -500, "container-b": 200}
	// Containers not listed here fall back to AnnotationOOMScoreAdj; if neither is set, no intervention is made.
	AnnotationOOMScoreAdjSpec = DomainPrefix + "oom-score-adj-spec"
)

type AggregationType string

const (
	// max is not welcomed since it may import outliers
	AVG AggregationType = "avg"
	P99 AggregationType = "p99"
	P95 AggregationType = "p95"
	P90 AggregationType = "p90"
	P50 AggregationType = "p50"
)
