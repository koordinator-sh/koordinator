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
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
)

var _ NUMATopologyHintProvider = &mockNUMATopologyHintProvider{}

type mockNUMATopologyHintProvider struct {
	th map[string][]NUMATopologyHint
	//TODO: Add this field and add some tests to make sure things error out
	//appropriately on allocation errors.
	//allocateError error
}

func (m *mockNUMATopologyHintProvider) GetPodTopologyHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (map[string][]NUMATopologyHint, *framework.Status) {
	return m.th, nil
}

func (m *mockNUMATopologyHintProvider) Allocate(ctx context.Context, cycleState *framework.CycleState, affinity NUMATopologyHint, pod *corev1.Pod, nodeName string) *framework.Status {
	return nil
}

func NewTestBitMask(sockets ...int) bitmask.BitMask {
	s, _ := bitmask.NewBitMask(sockets...)
	return s
}

type policyMergeTestCase struct {
	name     string
	hp       []NUMATopologyHintProvider
	expected NUMATopologyHint
}

func commonPolicyMergeTestCases(numaNodes []int) []policyMergeTestCase {
	return []policyMergeTestCase{
		{
			name: "Two providers, 1 hint each, same mask, both preferred 1/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0),
				Preferred:        true,
			},
		},
		{
			name: "Two providers, 1 hint each, same mask, both preferred 2/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(1),
				Preferred:        true,
			},
		},
		{
			name: "Two providers, 1 no hints, 1 single hint preferred 1/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0),
				Preferred:        true,
			},
		},
		{
			name: "Two providers, 1 no hints, 1 single hint preferred 2/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(1),
				Preferred:        true,
			},
		},
		{
			name: "Two providers, 1 with 2 hints, 1 with single hint matching 1/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0),
				Preferred:        true,
			},
		},
		{
			name: "Two providers, 1 with 2 hints, 1 with single hint matching 2/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(1),
				Preferred:        true,
			},
		},
		{
			name: "Two providers, both with 2 hints, matching narrower preferred hint from both",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0),
				Preferred:        true,
			},
		},
		{
			name: "Ensure less narrow preferred hints are chosen over narrower non-preferred hints",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        false,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(1),
				Preferred:        true,
			},
		},
		{
			name: "Multiple resources, same provider",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        false,
							},
						},
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(1),
				Preferred:        true,
			},
		},
	}
}

func (p *bestEffortPolicy) mergeTestCases(numaNodes []int) []policyMergeTestCase {
	return []policyMergeTestCase{
		{
			name: "NUMATopologyHint not set",
			hp:   []NUMATopologyHintProvider{},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(numaNodes...),
				Preferred:        true,
			},
		},
		{
			name: "NUMATopologyHintProvider returns empty non-nil map[string][]NUMATopologyHint",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(numaNodes...),
				Preferred:        true,
			},
		},
		{
			name: "NUMATopologyHintProvider returns -nil map[string][]NUMATopologyHint from provider",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": nil,
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(numaNodes...),
				Preferred:        true,
			},
		},
		{
			name: "NUMATopologyHintProvider returns empty non-nil map[string][]NUMATopologyHint from provider", hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": {},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(numaNodes...),
				Preferred:        false,
			},
		},
		{
			name: "Single NUMATopologyHint with Preferred as true and NUMANodeAffinity as nil",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": {
							{
								NUMANodeAffinity: nil,
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(numaNodes...),
				Preferred:        true,
			},
		},
		{
			name: "Single NUMATopologyHint with Preferred as false and NUMANodeAffinity as nil",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": {
							{
								NUMANodeAffinity: nil,
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(numaNodes...),
				Preferred:        false,
			},
		},
		{
			name: "Two providers, 1 hint each, no common mask",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(numaNodes...),
				Preferred:        false,
			},
		},
		{
			name: "Two providers, 1 hint each, same mask, 1 preferred, 1 not 1/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0),
				Preferred:        false,
			},
		},
		{
			name: "Two providers, 1 hint each, same mask, 1 preferred, 1 not 2/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(1),
				Preferred:        false,
			},
		},
		{
			name: "Two providers, 1 hint each, 1 wider mask, both preferred 1/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0),
				Preferred:        true,
			},
		},
		{
			name: "Two providers, 1 with 2 hints, 1 with single non-preferred hint matching",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0),
				Preferred:        false,
			},
		},
		{
			name: "Two providers, 1 hint each, 1 wider mask, both preferred 1/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(1),
				Preferred:        true,
			},
		},
	}
}

func (p *singleNumaNodePolicy) mergeTestCases(numaNodes []int) []policyMergeTestCase {
	return []policyMergeTestCase{
		{
			name: "NUMATopologyHint not set",
			hp:   []NUMATopologyHintProvider{},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        true,
			},
		},
		{
			name: "NUMATopologyHintProvider returns empty non-nil map[string][]NUMATopologyHint",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        true,
			},
		},
		{
			name: "NUMATopologyHintProvider returns -nil map[string][]NUMATopologyHint from provider",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": nil,
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        true,
			},
		},
		{
			name: "NUMATopologyHintProvider returns empty non-nil map[string][]NUMATopologyHint from provider", hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": {},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        false,
			},
		},
		{
			name: "Single NUMATopologyHint with Preferred as true and NUMANodeAffinity as nil",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": {
							{
								NUMANodeAffinity: nil,
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        true,
			},
		},
		{
			name: "Single NUMATopologyHint with Preferred as false and NUMANodeAffinity as nil",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource": {
							{
								NUMANodeAffinity: nil,
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        false,
			},
		},
		{
			name: "Two providers, 1 hint each, no common mask",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        false,
			},
		},
		{
			name: "Two providers, 1 hint each, same mask, 1 preferred, 1 not 1/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        false,
			},
		},
		{
			name: "Two providers, 1 hint each, same mask, 1 preferred, 1 not 2/2",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        false,
			},
		},
		{
			name: "Two providers, 1 with 2 hints, 1 with single non-preferred hint matching",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        false,
			},
		},
		{
			name: "Single NUMA hint generation",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        true,
							},
						},
						"resource2": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        false,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: nil,
				Preferred:        false,
			},
		},
		{
			name: "One no-preference provider",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(1),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        false,
							},
						},
					},
				},
				&mockNUMATopologyHintProvider{
					nil,
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0),
				Preferred:        true,
			},
		},
	}
}

func testPolicyMerge(policy Policy, tcases []policyMergeTestCase, t *testing.T) {
	for _, tc := range tcases {
		var providersHints []map[string][]NUMATopologyHint
		for _, provider := range tc.hp {
			hints, status := provider.GetPodTopologyHints(context.TODO(), nil, &corev1.Pod{}, "")
			assert.True(t, status.IsSuccess())
			providersHints = append(providersHints, hints)
		}

		actual, _ := policy.Merge(providersHints)
		if !reflect.DeepEqual(actual, tc.expected) {
			t.Errorf("%v: Expected Topology Hint to be %v, got %v:", tc.name, tc.expected, actual)
		}
	}
}
