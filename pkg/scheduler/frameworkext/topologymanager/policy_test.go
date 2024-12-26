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

	"github.com/koordinator-sh/koordinator/apis/extension"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
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
	name           string
	hp             []NUMATopologyHintProvider
	expected       NUMATopologyHint
	expectedReason []string
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
			name: "Two providers, 2 hints each, same mask (some with different bits), same preferred",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(0, 2),
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
							{
								NUMANodeAffinity: NewTestBitMask(0, 2),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0, 1),
				Preferred:        true,
			},
		},
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
			expectedReason: []string{ErrUnsatisfiedNUMAResource},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource prefer [] among [<nil>]"},
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
			expectedReason: []string{"Unsatisfied NUMA resource"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01] among [01] & resource2 prefer [10] among [10]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01] among [01] & resource2 prefer [] among [01]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [10] among [10] & resource2 prefer [] among [10]"},
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
				NUMANodeAffinity: NewTestBitMask(0, 1, 2, 3),
				Preferred:        false,
			},
			expectedReason: []string{"[Unaligned NUMA Hint cause resource1 prefer [01] among [01] & resource2 prefer [11] among [11]"},
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
				NUMANodeAffinity: NewTestBitMask(0, 1, 2, 3),
				Preferred:        false,
			},
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01 10] among [01 10] & resource2 prefer [] among [11]"},
		},
		{
			name: "Two providers, 1 hint each, 1 wider mask, both preferred 2/2",
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
				NUMANodeAffinity: NewTestBitMask(0, 1, 2, 3),
				Preferred:        false,
			},
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [10] among [10] & resource2 prefer [11] among [11]"},
		},
	}
}

func (p *restrictedPolicy) mergeTestCases(numaNodes []int) []policyMergeTestCase {
	return []policyMergeTestCase{
		{
			name: "Two providers, 2 hints each, same mask (some with different bits), same preferred",
			hp: []NUMATopologyHintProvider{
				&mockNUMATopologyHintProvider{
					map[string][]NUMATopologyHint{
						"resource1": {
							{
								NUMANodeAffinity: NewTestBitMask(0, 1),
								Preferred:        true,
							},
							{
								NUMANodeAffinity: NewTestBitMask(0, 2),
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
							{
								NUMANodeAffinity: NewTestBitMask(0, 2),
								Preferred:        true,
							},
						},
					},
				},
			},
			expected: NUMATopologyHint{
				NUMANodeAffinity: NewTestBitMask(0, 1),
				Preferred:        true,
			},
		},
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
			expectedReason: []string{"Unsatisfied NUMA resource"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource prefer [] among [<nil>]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01] among [01] & resource2 prefer [10] among [10]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01] among [01] & resource2 prefer [] among [01]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [10] among [10] & resource2 prefer [] among [10]"},
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
				Unsatisfied:      true,
				Preferred:        false,
			},
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01] among [01] & resource2 prefer [11] among [11]"},
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
				Unsatisfied:      true,
				Preferred:        false,
			},
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01 10] among [01 10] & resource2 prefer [] among [11]"},
		},
		{
			name: "Two providers, 1 hint each, 1 wider mask, both preferred 2/2",
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
				Unsatisfied:      true,
				Preferred:        false,
			},
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [10] among [10] & resource2 prefer [11] among [11]"},
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
			expectedReason: []string{"Unsatisfied NUMA resource"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource prefer [] among [<nil>]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01] among [01],resource2 prefer [10] among [10]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01] among [01],resource2 prefer [] among [01]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [10] among [10],resource2 prefer [] among [10]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [01 10] among [01 10],resource2 prefer [] among [11]"},
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
			expectedReason: []string{"Unaligned NUMA Hint cause resource1 prefer [11] among [11],resource2 prefer [01 10] among [01 10 11]"},
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

		actual, _, reasons := policy.Merge(providersHints, extension.NumaTopologyExclusivePreferred, []extension.NumaNodeStatus{})
		if !reflect.DeepEqual(actual, tc.expected) {
			t.Errorf("%v: Expected Topology Hint to be %v, got %v:", tc.name, tc.expected, actual)
		}
		if policy.Name() == PolicySingleNumaNode || policy.Name() == PolicyRestricted {
			if !reflect.DeepEqual(reasons, tc.expectedReason) {
				t.Errorf("%v: Expected reasons to be %v, got %v:", tc.name, tc.expectedReason, reasons)
			}
		}
	}
}

func Test_checkExclusivePolicy(t *testing.T) {
	type args struct {
		affinity          NUMATopologyHint
		exclusivePolicy   apiext.NumaTopologyExclusive
		allNUMANodeStatus []apiext.NumaNodeStatus
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		// TODO: Add test cases.
		{
			name: "preferred policy 1",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusivePreferred,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusShared, apiext.NumaNodeStatusShared},
			},
			want: true,
		},
		{
			name: "preferred policy 2",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0, 1), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusivePreferred,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusShared, apiext.NumaNodeStatusShared},
			},
			want: true,
		},
		{
			name: "preferred policy 3",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusivePreferred,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusIdle, apiext.NumaNodeStatusSingle},
			},
			want: true,
		},
		{
			name: "preferred policy 4",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0, 1), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusivePreferred,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusIdle, apiext.NumaNodeStatusSingle},
			},
			want: true,
		},
		{
			name: "required policy 1",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusiveRequired,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusIdle, apiext.NumaNodeStatusSingle},
			},
			want: true,
		},
		{
			name: "required policy 2",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusiveRequired,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusShared, apiext.NumaNodeStatusSingle},
			},
			want: false,
		},
		{
			name: "required policy 3",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusiveRequired,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusShared, apiext.NumaNodeStatusShared},
			},
			want: false,
		},
		{
			name: "required policy 4",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusiveRequired,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusSingle, apiext.NumaNodeStatusShared},
			},
			want: true,
		},
		{
			name: "required policy 5",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0, 1), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusiveRequired,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusShared, apiext.NumaNodeStatusSingle},
			},
			want: false,
		},
		{
			name: "required policy 6",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0, 1), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusiveRequired,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusShared, apiext.NumaNodeStatusIdle},
			},
			want: true,
		},
		{
			name: "required policy 7",
			args: args{
				affinity:          NUMATopologyHint{NUMANodeAffinity: NewTestBitMask(0, 1), Preferred: true, Score: 0},
				exclusivePolicy:   apiext.NumaTopologyExclusiveRequired,
				allNUMANodeStatus: []apiext.NumaNodeStatus{apiext.NumaNodeStatusShared, apiext.NumaNodeStatusShared},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkExclusivePolicy(tt.args.affinity, tt.args.exclusivePolicy, tt.args.allNUMANodeStatus); got != tt.want {
				t.Errorf("checkExclusivePolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_filterProvidersHints(t *testing.T) {
	tests := []struct {
		name           string
		providersHints []map[string][]NUMATopologyHint
		want           [][]NUMATopologyHint
		wantReasons    []string
		wantSummary    []string
	}{
		{
			name: "Two providers, 1 hint each, same mask, both preferred 1/2",
			providersHints: []map[string][]NUMATopologyHint{
				{
					"resource1": {
						{
							NUMANodeAffinity: NewTestBitMask(0),
							Preferred:        true,
						},
					},
				},
				{
					"resource2": {
						{
							NUMANodeAffinity: NewTestBitMask(0),
							Preferred:        true,
						},
					},
				},
			},
			want: [][]NUMATopologyHint{
				{
					{
						NUMANodeAffinity: NewTestBitMask(0),
						Preferred:        true,
					},
				},
				{
					{
						NUMANodeAffinity: NewTestBitMask(0),
						Preferred:        true,
					},
				},
			},
			wantSummary: []string{"resource1 prefer [01] among [01]", "resource2 prefer [01] among [01]"},
		},
		{
			name: "Two providers, 1 hint each, no common mask",
			providersHints: []map[string][]NUMATopologyHint{
				{
					"resource1": {
						{
							NUMANodeAffinity: NewTestBitMask(0),
							Preferred:        true,
						},
					},
				},
				{
					"resource2": {
						{
							NUMANodeAffinity: NewTestBitMask(1),
							Preferred:        true,
						},
					},
				},
			},
			want: [][]NUMATopologyHint{
				{
					{
						NUMANodeAffinity: NewTestBitMask(0),
						Preferred:        true,
					},
				},
				{
					{
						NUMANodeAffinity: NewTestBitMask(1),
						Preferred:        true,
					},
				},
			},
			wantSummary: []string{"resource1 prefer [01] among [01]", "resource2 prefer [10] among [10]"},
		},
		{
			name: "NUMATopologyHintProvider returns empty non-nil map[string][]NUMATopologyHint from provider",
			providersHints: []map[string][]NUMATopologyHint{
				{
					"resource": {},
				},
			},
			want: [][]NUMATopologyHint{
				{
					{
						Unsatisfied: true,
						Preferred:   false,
					},
				},
			},
			wantReasons: []string{"Unsatisfied NUMA resource"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotReasons, gotSummary := filterProvidersHints(tt.providersHints)
			assert.Equalf(t, tt.want, got, "filterProvidersHints(%v)", tt.providersHints)
			assert.Equalf(t, tt.wantReasons, gotReasons, "filterProvidersHints(%v)", tt.providersHints)
			assert.Equalf(t, tt.wantSummary, gotSummary, "filterProvidersHints(%v)", tt.providersHints)
		})
	}
}
