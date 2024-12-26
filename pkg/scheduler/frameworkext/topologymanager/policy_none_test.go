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

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func TestPolicyNoneName(t *testing.T) {
	tcases := []struct {
		name     string
		expected string
	}{
		{
			name:     "New None Policy",
			expected: "none",
		},
	}
	for _, tc := range tcases {
		policy := NewNonePolicy()
		if policy.Name() != tc.expected {
			t.Errorf("Expected Policy Name to be %s, got %s", tc.expected, policy.Name())
		}
	}
}

func TestPolicyNoneCanAdmitPodResult(t *testing.T) {
	tcases := []struct {
		name     string
		hint     NUMATopologyHint
		expected bool
	}{
		{
			name:     "Preferred is set to false in topology hints",
			hint:     NUMATopologyHint{nil, false, false, 0},
			expected: true,
		},
		{
			name:     "Preferred is set to true in topology hints",
			hint:     NUMATopologyHint{nil, false, true, 0},
			expected: true,
		},
	}

	for _, tc := range tcases {
		policy := NewNonePolicy()
		result := policy.(*nonePolicy).canAdmitPodResult(&tc.hint)

		if result != tc.expected {
			t.Errorf("Expected result to be %t, got %t", tc.expected, result)
		}
	}
}

func TestPolicyNoneMerge(t *testing.T) {
	tcases := []struct {
		name           string
		providersHints []map[string][]NUMATopologyHint
		expectedHint   NUMATopologyHint
		expectedAdmit  bool
	}{
		{
			name:           "merged empty providers hints",
			providersHints: []map[string][]NUMATopologyHint{},
			expectedHint:   NUMATopologyHint{},
			expectedAdmit:  true,
		},
		{
			name: "merge with a single provider with a single preferred resource",
			providersHints: []map[string][]NUMATopologyHint{
				{
					"resource": {{NUMANodeAffinity: NewTestBitMask(0, 1), Preferred: true}},
				},
			},
			expectedHint:  NUMATopologyHint{},
			expectedAdmit: true,
		},
		{
			name: "merge with a single provider with a single non-preferred resource",
			providersHints: []map[string][]NUMATopologyHint{
				{
					"resource": {{NUMANodeAffinity: NewTestBitMask(0, 1), Preferred: false}},
				},
			},
			expectedHint:  NUMATopologyHint{},
			expectedAdmit: true,
		},
	}

	for _, tc := range tcases {
		policy := NewNonePolicy()
		result, admit, _ := policy.Merge(tc.providersHints, apiext.NumaTopologyExclusivePreferred, []apiext.NumaNodeStatus{})
		if !result.IsEqual(tc.expectedHint) || admit != tc.expectedAdmit {
			t.Errorf("Test Case: %s: Expected merge hint to be %v, got %v", tc.name, tc.expectedHint, result)
		}
	}
}
