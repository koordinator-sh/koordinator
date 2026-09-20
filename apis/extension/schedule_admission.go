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
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	// LabelScheduleAdmission is the fixed label key used by controllers to gate pod scheduling.
	// Gating is decided solely by the presence of the key; the label value is not inspected
	// (any value, including an empty string, gates the pod). A pod is only schedulable when
	// this label is absent (fast-path, exact-match mode).
	// Example: scheduling.koordinator.sh/schedule-admission: ""
	LabelScheduleAdmission = SchedulingDomainPrefix + "/schedule-admission"

	// LabelScheduleAdmissionPrefix is the label key prefix used when prefix-match mode is enabled.
	// Each controller adds a label with this prefix and a unique suffix (gate name). As with the
	// fixed key, only the presence of a matching key gates the pod; the value is not inspected.
	// Example: scheduling.koordinator.sh/schedule-admission-quota-check: ""
	LabelScheduleAdmissionPrefix = LabelScheduleAdmission + "-"
)

// HasScheduleAdmissionLabels returns true if the pod is gated by schedule-admission labels
// and should not be scheduled.
//
// When prefixMatch is false (default), only the fixed LabelScheduleAdmission key is checked (O(1)).
// When prefixMatch is true, the fixed key plus any label with the LabelScheduleAdmissionPrefix
// prefix gate the pod (the prefix mode is a superset of the exact-match mode).
func HasScheduleAdmissionLabels(pod *corev1.Pod, prefixMatch bool) bool {
	if pod == nil {
		return false
	}
	if _, ok := pod.Labels[LabelScheduleAdmission]; ok {
		return true
	}
	if !prefixMatch {
		return false
	}
	for key := range pod.Labels {
		if strings.HasPrefix(key, LabelScheduleAdmissionPrefix) {
			return true
		}
	}
	return false
}

// GetScheduleAdmissionGates returns the sorted list of schedule-admission gate labels on the pod.
// The fixed LabelScheduleAdmission key is reported by its full key; prefixed labels are reported
// by their gate name (the suffix after the prefix). Prefixed labels are only considered when
// prefixMatch is true. The result is sorted so that failure messages and logs are deterministic.
func GetScheduleAdmissionGates(pod *corev1.Pod, prefixMatch bool) []string {
	if pod == nil {
		return nil
	}
	var gates []string
	if _, ok := pod.Labels[LabelScheduleAdmission]; ok {
		gates = append(gates, LabelScheduleAdmission)
	}
	if prefixMatch {
		for key := range pod.Labels {
			if gate, ok := strings.CutPrefix(key, LabelScheduleAdmissionPrefix); ok {
				gates = append(gates, gate)
			}
		}
	}
	sort.Strings(gates)
	return gates
}
