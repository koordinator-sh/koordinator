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

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
)

const (
	// LabelSchedulerName is used to specify the internal scheduler name for a pod, overriding the spec.schedulerName.
	LabelSchedulerName = SchedulingDomainPrefix + "/scheduler-name"

	// LabelSandbox marks a pod as a sandbox workload. Sandbox pods are routed into the
	// dedicated sandbox scheduling path when the sandbox custom workflow is enabled.
	LabelSandbox = SchedulingDomainPrefix + "/sandbox"

	// LabelSandboxTemplateHash identifies the sandbox template a pod is created from. Pods
	// carrying the same hash are treated as scheduling-equivalent by the sandbox workflow:
	// the scheduling decision computed for one pod of the class can be reused by the others.
	// It is written by the sandbox controller/adapter, which MUST guarantee that pods sharing
	// a hash are equivalent in every scheduling-relevant field. The value must be a valid
	// label value (at most 63 characters).
	LabelSandboxTemplateHash = SchedulingDomainPrefix + "/sandbox-template-hash"

	// AnnotationOriginalSchedulerName stores the original pod.Spec.SchedulerName before
	// TransformSchedulerName overwrites it, allowing later restoration.
	AnnotationOriginalSchedulerName = InternalSchedulingDomainPrefix + "/original-scheduler-name"
)

func GetSchedulerName(pod *corev1.Pod) string {
	if schedulerName, ok := pod.Labels[LabelSchedulerName]; ok {
		return schedulerName
	}
	return pod.Spec.SchedulerName
}

// IsSandboxPod returns true if the pod is marked as a sandbox workload.
func IsSandboxPod(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	return pod.Labels[LabelSandbox] == "true"
}

// GetSandboxTemplateHash returns the sandbox template hash of the pod, or "" if the pod does
// not carry one. Only sandbox pods with a non-empty hash are eligible for equivalence-class
// decision reuse.
func GetSandboxTemplateHash(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	return pod.Labels[LabelSandboxTemplateHash]
}

type SchedulingHint struct {
	// NodeNames is a list of node names that the pod is required to be scheduled on.
	NodeNames []string `json:"nodeNames,omitempty"`
	// PreferredNodeNames is an ordered list of preferred node names that the pod should try to schedule first.
	// It is recommended to use as few nodes as possible to reduce the overhead.
	PreferredNodeNames []string `json:"preferredNodeNames,omitempty"`
	// Extensions is a map of hint extensions for plugins.
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

const (
	// DEPRECATED: This api is marked as internal and will be removed next version.
	// Please use the domain `internal.scheduling.koordinator.sh/` instead.
	// DeprecatedAnnotationSchedulingHint is used to specify a scheduling hint for the pod.
	// Each plugin can decide whether to use this hint or not.
	DeprecatedAnnotationSchedulingHint = SchedulingDomainPrefix + "/scheduling-hint"
	// AnnotationSchedulingHint is used to specify a scheduling hint for the pod.
	// Each plugin can decide whether to use this hint or not.
	AnnotationSchedulingHint = InternalSchedulingDomainPrefix + "/scheduling-hint"
)

func GetSchedulingHint(pod *corev1.Pod) (*SchedulingHint, error) {
	if pod == nil {
		return nil, nil
	}
	hintStr, ok := pod.Annotations[AnnotationSchedulingHint]
	if ok && len(hintStr) > 0 { // ignore empty hint
		hint := &SchedulingHint{}
		if err := json.Unmarshal([]byte(hintStr), hint); err != nil {
			return nil, err
		}
		return hint, nil
	}
	hintStr, ok = pod.Annotations[DeprecatedAnnotationSchedulingHint]
	if !ok || len(hintStr) == 0 { // ignore empty hint
		return nil, nil
	}
	hint := &SchedulingHint{}
	if err := json.Unmarshal([]byte(hintStr), hint); err != nil {
		return nil, err
	}
	return hint, nil
}
