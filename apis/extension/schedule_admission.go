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
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	// LabelScheduleAdmissionPrefix is the label key prefix used by controllers to gate pod scheduling.
	// Each controller adds a label with this prefix and a unique suffix (gate name) with value "true".
	// A pod is only schedulable when no labels with this prefix exist.
	// Example: scheduling.koordinator.sh/schedule-admission-quota-check: "true"
	LabelScheduleAdmissionPrefix = SchedulingDomainPrefix + "/schedule-admission-"
)

// HasScheduleAdmissionLabels returns true if the pod has any label with the schedule-admission prefix,
// meaning the pod is gated and should not be scheduled.
func HasScheduleAdmissionLabels(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for key := range pod.Labels {
		if strings.HasPrefix(key, LabelScheduleAdmissionPrefix) {
			return true
		}
	}
	return false
}

// GetScheduleAdmissionGates returns the list of gate names (suffixes after the prefix)
// from all schedule-admission labels on the pod.
func GetScheduleAdmissionGates(pod *corev1.Pod) []string {
	if pod == nil {
		return nil
	}
	var gates []string
	for key := range pod.Labels {
		if gate, ok := strings.CutPrefix(key, LabelScheduleAdmissionPrefix); ok {
			gates = append(gates, gate)
		}
	}
	return gates
}
