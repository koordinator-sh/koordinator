/*
Copyright 2022 The Koordinator Authors.
Copyright 2019 The Kubernetes Authors.

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

package topologymanager

import (
	"testing"
)

func TestPolicyBestEffortCanAdmitPodResult(t *testing.T) {
	tcases := []struct {
		name     string
		hint     NUMATopologyHint
		expected bool
	}{
		{
			name:     "Preferred is set to false in topology hints",
			hint:     NUMATopologyHint{nil, false, 0},
			expected: true,
		},
		{
			name:     "Preferred is set to true in topology hints",
			hint:     NUMATopologyHint{nil, true, 0},
			expected: true,
		},
	}

	for _, tc := range tcases {
		numaNodes := []int{0, 1}
		policy := NewBestEffortPolicy(numaNodes)
		result := policy.(*bestEffortPolicy).canAdmitPodResult(&tc.hint)

		if result != tc.expected {
			t.Errorf("Expected result to be %t, got %t", tc.expected, result)
		}
	}
}

func TestPolicyBestEffortMerge(t *testing.T) {
	numaNodes := []int{0, 1, 2, 3}
	policy := NewBestEffortPolicy(numaNodes)

	tcases := commonPolicyMergeTestCases(numaNodes)
	tcases = append(tcases, policy.(*bestEffortPolicy).mergeTestCases(numaNodes)...)

	testPolicyMerge(policy, tcases, t)
}
