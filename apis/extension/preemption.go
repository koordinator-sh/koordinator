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
	// LabelPodPreemptionPolicy is used to set the PreemptionPolicy of a Pod when the feature
	// PreemptionPolicyTransformer is enabled.
	// When PreemptionPolicyTransformer is enabled but the pod does not set the label, DefaultPreemptionPolicy is used.
	LabelPodPreemptionPolicy = SchedulingDomainPrefix + "/preemption-policy"

	// LabelDisablePreemptible determines whether the pod disables being a victim during the reservation preemption.
	LabelDisablePreemptible = SchedulingDomainPrefix + "/disable-preemptible"
)

func GetPodKoordPreemptionPolicy(pod *corev1.Pod) *corev1.PreemptionPolicy {
	if pod == nil || pod.Labels == nil {
		return nil
	}
	switch s := corev1.PreemptionPolicy(pod.Labels[LabelPodPreemptionPolicy]); s {
	case corev1.PreemptNever, corev1.PreemptLowerPriority:
		return &s
	default:
		return nil
	}
}

func GetPreemptionPolicyPtr(policy corev1.PreemptionPolicy) *corev1.PreemptionPolicy {
	return &policy
}

func IsPodPreemptible(pod *corev1.Pod) bool {
	if pod == nil || pod.Labels == nil {
		return true
	}
	v, ok := pod.Labels[LabelDisablePreemptible]
	if !ok {
		return true
	}
	return v != "true"
}
