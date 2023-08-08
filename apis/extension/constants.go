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
	// NodeDomainPrefix represents the node domain prefix
	NodeDomainPrefix = "node.koordinator.sh"

	LabelPodQoS      = DomainPrefix + "qosClass"
	LabelPodPriority = DomainPrefix + "priority"
	// LabelPodPriorityClass is used to revise those Pods that are already running and have Priority set, so that
	// Koordinator can be smoothly deployed to the running cluster. If you don't have a running Pod with
	// PriorityClass set, don't set this field specifically.
	LabelPodPriorityClass = DomainPrefix + "priority-class"

	LabelManagedBy = "app.kubernetes.io/managed-by"
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
