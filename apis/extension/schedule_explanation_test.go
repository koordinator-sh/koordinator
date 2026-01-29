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
	"reflect"
	"sort"
	"testing"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

// TestSortNodeFailedDetails tests the SortNodeFailedDetails function
func TestSortNodeFailedDetails(t *testing.T) {
	tests := []struct {
		name     string
		input    v1alpha1.NodeFailedDetails
		expected v1alpha1.NodeFailedDetails
	}{
		{
			name:     "empty slice",
			input:    v1alpha1.NodeFailedDetails{},
			expected: v1alpha1.NodeFailedDetails{},
		},
		{
			name: "single element",
			input: v1alpha1.NodeFailedDetails{
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reason1",
						PreemptMightHelp: true,
					},
					FailedNodes: []string{"node1", "node3", "node2"},
				},
			},
			expected: v1alpha1.NodeFailedDetails{
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reason1",
						PreemptMightHelp: true,
					},
					FailedNodes: []string{"node1", "node2", "node3"}, // FailedNodes should be sorted
				},
			},
		},
		{
			name: "multiple elements with different plugins",
			input: v1alpha1.NodeFailedDetails{
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginC",
						Reason:           "reason2",
						PreemptMightHelp: false,
					},
					FailedNodes: []string{"node3", "node1"},
				},
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reason1",
						PreemptMightHelp: true,
					},
					FailedNodes: []string{"node2", "node1"},
				},
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginB",
						Reason:           "reason3",
						PreemptMightHelp: false,
					},
					FailedNodes: []string{"node1", "node2"},
				},
			},
			expected: v1alpha1.NodeFailedDetails{
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reason1",
						PreemptMightHelp: true,
					},
					FailedNodes: []string{"node1", "node2"}, // Sorted
				},
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginB",
						Reason:           "reason3",
						PreemptMightHelp: false,
					},
					FailedNodes: []string{"node1", "node2"}, // Already sorted
				},
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginC",
						Reason:           "reason2",
						PreemptMightHelp: false,
					},
					FailedNodes: []string{"node1", "node3"}, // Sorted
				},
			},
		},
		{
			name: "same plugin, different reasons",
			input: v1alpha1.NodeFailedDetails{
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reasonZ",
						PreemptMightHelp: true,
					},
					FailedNodes: []string{"node2", "node1"},
				},
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reasonA",
						PreemptMightHelp: false,
					},
					FailedNodes: []string{"node1", "node2"},
				},
			},
			expected: v1alpha1.NodeFailedDetails{
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reasonA",
						PreemptMightHelp: false,
					},
					FailedNodes: []string{"node1", "node2"}, // Already sorted
				},
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reasonZ",
						PreemptMightHelp: true,
					},
					FailedNodes: []string{"node1", "node2"}, // Sorted
				},
			},
		},
		{
			name: "same plugin and reason, different preempt help",
			input: v1alpha1.NodeFailedDetails{
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reason1",
						PreemptMightHelp: false,
					},
					FailedNodes: []string{"node1", "node2"},
				},
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reason1",
						PreemptMightHelp: true,
					},
					FailedNodes: []string{"node3", "node1", "node2"},
				},
			},
			expected: v1alpha1.NodeFailedDetails{

				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{

						FailedPlugin:     "pluginA",
						Reason:           "reason1",
						PreemptMightHelp: true,
					},
					FailedNodes: []string{"node1", "node2", "node3"}, // Sorted
				},
				{
					NodeFailedStatus: v1alpha1.NodeFailedStatus{
						FailedPlugin:     "pluginA",
						Reason:           "reason1",
						PreemptMightHelp: false,
					},
					FailedNodes: []string{"node1", "node2"}, // Already sorted
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy of input to avoid modifying original
			inputCopy := make(v1alpha1.NodeFailedDetails, len(tt.input))
			for i := range tt.input {
				inputCopy[i] = tt.input[i].DeepCopy()
			}

			SortNodeFailedDetails(inputCopy)

			if !reflect.DeepEqual(inputCopy, tt.expected) {
				t.Errorf("SortNodeFailedDetails() = %v, want %v", inputCopy, tt.expected)
			}

			// Additional validation: check if FailedNodes are sorted within each element
			for i, detail := range inputCopy {
				if !sort.StringsAreSorted(detail.FailedNodes) {
					t.Errorf("FailedNodes in index %d are not sorted: %v", i, detail.FailedNodes)
				}
			}
		})
	}
}

// TestSortNodeFailedDetailsStability tests that sorting is stable for identical elements
func TestSortNodeFailedDetailsStability(t *testing.T) {
	original := v1alpha1.NodeFailedDetails{
		{
			NodeFailedStatus: v1alpha1.NodeFailedStatus{
				FailedPlugin:     "pluginA",
				Reason:           "reason1",
				PreemptMightHelp: false,
			},
			FailedNodes: []string{"node3", "node1", "node2"},
		},
		{
			NodeFailedStatus: v1alpha1.NodeFailedStatus{
				FailedPlugin:     "pluginA",
				Reason:           "reason1",
				PreemptMightHelp: false,
			},
			FailedNodes: []string{"node5", "node4"},
		},
	}

	// Make two copies and sort them independently
	copy1 := make(v1alpha1.NodeFailedDetails, len(original))
	copy2 := make(v1alpha1.NodeFailedDetails, len(original))

	for i := range original {
		copy1[i] = original[i].DeepCopy()
		copy2[i] = original[i].DeepCopy()
	}

	SortNodeFailedDetails(copy1)
	SortNodeFailedDetails(copy2)

	// Both should be identical after sorting
	if !reflect.DeepEqual(copy1, copy2) {
		t.Errorf("Sorting is not deterministic: first result = %v, second result = %v", copy1, copy2)
	}
}
