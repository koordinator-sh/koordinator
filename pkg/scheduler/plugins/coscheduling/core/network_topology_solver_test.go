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

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
)

func TestTopologyNodeLessFunc(t *testing.T) {
	// Build topology tree:
	// Cluster (root)
	// ├── Spine: s1 (OfferSlot=3)
	// │   ├── Block: b1 (OfferSlot=2)
	// │   └── Block: b2 (OfferSlot=2)
	// └── Spine: s2 (OfferSlot=4)
	//     ├── Block: b3 (OfferSlot=2)
	//     └── Block: b4 (OfferSlot=3)

	root := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "cluster"},
		OfferSlot:    7,
	}
	s1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "s1"},
		Parent:       root,
		OfferSlot:    3,
	}
	s2 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "s2"},
		Parent:       root,
		OfferSlot:    4,
	}
	b1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b1"},
		Parent:       s1,
		OfferSlot:    2,
		Score:        100,
	}
	b2 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b2"},
		Parent:       s1,
		OfferSlot:    2,
		Score:        100,
	}
	b3 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b3"},
		Parent:       s2,
		OfferSlot:    2,
		Score:        100,
	}
	b4 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b4"},
		Parent:       s2,
		OfferSlot:    3,
		Score:        100,
	}

	tests := []struct {
		name           string
		a              *networktopology.TreeNode
		b              *networktopology.TreeNode
		lowerOfferSlot bool
		want           bool
	}{
		{
			name:           "different OfferSlot, lowerFirst=true, a < b",
			a:              b1,
			b:              b4,
			lowerOfferSlot: true,
			want:           true,
		},
		{
			name:           "different OfferSlot, lowerFirst=true, a > b",
			a:              b4,
			b:              b1,
			lowerOfferSlot: true,
			want:           false,
		},
		{
			name:           "different OfferSlot, lowerFirst=false, a < b",
			a:              b1,
			b:              b4,
			lowerOfferSlot: false,
			want:           false,
		},
		{
			name:           "different OfferSlot, lowerFirst=false, a > b",
			a:              b4,
			b:              b1,
			lowerOfferSlot: false,
			want:           true,
		},
		{
			name:           "same OfferSlot, different parent OfferSlot, lowerFirst=true",
			a:              b1,
			b:              b3,
			lowerOfferSlot: true,
			want:           true,
		},
		{
			name:           "same OfferSlot, different parent OfferSlot, lowerFirst=false",
			a:              b1,
			b:              b3,
			lowerOfferSlot: false,
			want:           false,
		},
		{
			name:           "same OfferSlot, same parent OfferSlot, fallback to name",
			a:              b1,
			b:              b2,
			lowerOfferSlot: true,
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := topologyNodeLessFunc(tt.a, tt.b, tt.lowerOfferSlot)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTopologyNodeLessFunc_ScoreFallback(t *testing.T) {
	root := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "cluster"},
		OfferSlot:    4,
	}
	s1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "s1"},
		Parent:       root,
		OfferSlot:    2,
	}
	b1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b1"},
		Parent:       s1,
		OfferSlot:    1,
		Score:        200,
	}
	b2 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b2"},
		Parent:       s1,
		OfferSlot:    1,
		Score:        100,
	}

	tests := []struct {
		name           string
		a              *networktopology.TreeNode
		b              *networktopology.TreeNode
		lowerOfferSlot bool
		want           bool
	}{
		{
			name:           "same OfferSlot chain, different Score, higher Score wins",
			a:              b1,
			b:              b2,
			lowerOfferSlot: true,
			want:           true,
		},
		{
			name:           "same OfferSlot chain, different Score, higher Score wins (reversed)",
			a:              b2,
			b:              b1,
			lowerOfferSlot: true,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := topologyNodeLessFunc(tt.a, tt.b, tt.lowerOfferSlot)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTopologyNodeLessFunc_NilParent(t *testing.T) {
	a := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "a"},
		OfferSlot:    2,
		Score:        100,
	}
	b := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{Name: "b"},
		OfferSlot:    2,
		Score:        100,
	}

	got := topologyNodeLessFunc(a, b, true)
	assert.True(t, got)
}

func TestDistributeOfferSlot_WithSlotMultiple(t *testing.T) {
	// Build topology tree:
	// Cluster (root)
	// └── Spine: s1
	//     ├── Block: b1
	//     │   ├── Node: n1 (OfferSlot=5)
	//     │   └── Node: n2 (OfferSlot=7)
	//     └── Block: b2
	//         ├── Node: n3 (OfferSlot=6)
	//         └── Node: n4 (OfferSlot=4)

	n1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: "NodeTopologyLayer",
			Name:  "n1",
		},
		OfferSlot: 5,
	}
	n2 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: "NodeTopologyLayer",
			Name:  "n2",
		},
		OfferSlot: 7,
	}
	n3 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: "NodeTopologyLayer",
			Name:  "n3",
		},
		OfferSlot: 6,
	}
	n4 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: "NodeTopologyLayer",
			Name:  "n4",
		},
		OfferSlot: 4,
	}

	b1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: "BlockLayer",
			Name:  "b1",
		},
		OfferSlot: 12,
		Children: map[string]*networktopology.TreeNode{
			"n1": n1,
			"n2": n2,
		},
	}
	b2 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: "BlockLayer",
			Name:  "b2",
		},
		OfferSlot: 10,
		Children: map[string]*networktopology.TreeNode{
			"n3": n3,
			"n4": n4,
		},
	}

	s1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: "SpineLayer",
			Name:  "s1",
		},
		OfferSlot: 22,
		Children: map[string]*networktopology.TreeNode{
			"b1": b1,
			"b2": b2,
		},
	}

	n1.Parent = b1
	n2.Parent = b1
	n3.Parent = b2
	n4.Parent = b2
	b1.Parent = s1
	b2.Parent = s1

	tests := []struct {
		name              string
		desiredOfferSlot  int
		layerSlotMultiple map[string]int
		wantDistribution  map[string]int
		wantTotalSlot     int
	}{
		{
			name:              "no slot multiple constraint",
			desiredOfferSlot:  15,
			layerSlotMultiple: nil,
			wantDistribution: map[string]int{
				"n1": 5,
				"n2": 7,
				"n3": 3,
				"n4": 0,
			},
			wantTotalSlot: 15,
		},
		{
			name:             "node layer slot multiple = 4",
			desiredOfferSlot: 20,
			layerSlotMultiple: map[string]int{
				"NodeTopologyLayer": 4,
			},
			// n1: 5 -> 4, n2: 7 -> 4, n3: 6 -> 4, n4: 4 -> 4
			wantDistribution: map[string]int{
				"n1": 4,
				"n2": 4,
				"n3": 4,
				"n4": 4,
			},
			wantTotalSlot: 16,
		},
		{
			name:             "block layer slot multiple = 8",
			desiredOfferSlot: 20,
			layerSlotMultiple: map[string]int{
				"BlockLayer": 8,
			},
			// b1: OfferSlot=12 -> maxOfferSlot=8, children get 8 total
			// b2: OfferSlot=10 -> maxOfferSlot=8, children get 8 total
			// But desiredOfferSlot=20, so b1 gets 8, b2 gets remaining 12->8
			wantDistribution: map[string]int{
				"n2": 7,
				"n1": 1,
				"n3": 6,
				"n4": 2,
			},
			wantTotalSlot: 16,
		},
		{
			name:             "both node and block layer slot multiple",
			desiredOfferSlot: 20,
			layerSlotMultiple: map[string]int{
				"NodeTopologyLayer": 2,
				"BlockLayer":        8,
			},
			// Block layer: b1 maxOfferSlot=8, b2 maxOfferSlot=8
			// Node layer within b1: n2: 7->6, n1: remaining 2->2
			// Node layer within b2: n3: 6->6, n4: remaining 2->2
			wantDistribution: map[string]int{
				"n2": 6,
				"n1": 2,
				"n3": 6,
				"n4": 2,
			},
			wantTotalSlot: 16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var layerSlotMultiple map[schedulingv1alpha1.TopologyLayer]int
			if tt.layerSlotMultiple != nil {
				layerSlotMultiple = make(map[schedulingv1alpha1.TopologyLayer]int)
				for k, v := range tt.layerSlotMultiple {
					layerSlotMultiple[schedulingv1alpha1.TopologyLayer(k)] = v
				}
			}

			n1.OfferSlot = 5
			n2.OfferSlot = 7
			n3.OfferSlot = 6
			n4.OfferSlot = 4
			b1.OfferSlot = 12
			b2.OfferSlot = 10
			s1.OfferSlot = 22

			distribution := make(map[string]int)
			_, totalSlot := distributeOfferSlot(tt.desiredOfferSlot, s1, distribution, layerSlotMultiple)

			assert.Equal(t, tt.wantTotalSlot, totalSlot, "total slot mismatch")
			assert.Equal(t, tt.wantDistribution, distribution, "distribution mismatch")
		})
	}
}

func TestGetLayerSlotMultiple(t *testing.T) {
	tests := []struct {
		name string
		spec *extension.NetworkTopologySpec
		want map[schedulingv1alpha1.TopologyLayer]int
	}{
		{
			name: "nil spec",
			spec: nil,
			want: nil,
		},
		{
			name: "empty gather strategy",
			spec: &extension.NetworkTopologySpec{
				GatherStrategy: []extension.NetworkTopologyGatherRule{},
			},
			want: nil,
		},
		{
			name: "no slot multiple specified",
			spec: &extension.NetworkTopologySpec{
				GatherStrategy: []extension.NetworkTopologyGatherRule{
					{
						Layer:    "SpineLayer",
						Strategy: extension.NetworkTopologyGatherStrategyMustGather,
					},
				},
			},
			want: nil,
		},
		{
			name: "slot multiple = 1 should be ignored",
			spec: &extension.NetworkTopologySpec{
				GatherStrategy: []extension.NetworkTopologyGatherRule{
					{
						Layer:        "SpineLayer",
						Strategy:     extension.NetworkTopologyGatherStrategyMustGather,
						SlotMultiple: 1,
					},
				},
			},
			want: nil,
		},
		{
			name: "slot multiple = 0 should be ignored",
			spec: &extension.NetworkTopologySpec{
				GatherStrategy: []extension.NetworkTopologyGatherRule{
					{
						Layer:        "SpineLayer",
						Strategy:     extension.NetworkTopologyGatherStrategyMustGather,
						SlotMultiple: 0,
					},
				},
			},
			want: nil,
		},
		{
			name: "single layer with slot multiple",
			spec: &extension.NetworkTopologySpec{
				GatherStrategy: []extension.NetworkTopologyGatherRule{
					{
						Layer:        "BlockLayer",
						Strategy:     extension.NetworkTopologyGatherStrategyPreferGather,
						SlotMultiple: 8,
					},
				},
			},
			want: map[schedulingv1alpha1.TopologyLayer]int{
				"BlockLayer": 8,
			},
		},
		{
			name: "multiple layers with slot multiple",
			spec: &extension.NetworkTopologySpec{
				GatherStrategy: []extension.NetworkTopologyGatherRule{
					{
						Layer:        "SpineLayer",
						Strategy:     extension.NetworkTopologyGatherStrategyMustGather,
						SlotMultiple: 16,
					},
					{
						Layer:        "BlockLayer",
						Strategy:     extension.NetworkTopologyGatherStrategyPreferGather,
						SlotMultiple: 8,
					},
					{
						Layer:        schedulingv1alpha1.NodeTopologyLayer,
						Strategy:     extension.NetworkTopologyGatherStrategyPreferGather,
						SlotMultiple: 4,
					},
				},
			},
			want: map[schedulingv1alpha1.TopologyLayer]int{
				"SpineLayer":                         16,
				"BlockLayer":                         8,
				schedulingv1alpha1.NodeTopologyLayer: 4,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetLayerSlotMultiple(tt.spec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDistributeOfferSlot_SlotMultipleInsufficientSlots(t *testing.T) {
	// Build topology tree:
	// Cluster (root)
	// └── Spine: s1
	//     └── Block: b1
	//         ├── Node: n1 (OfferSlot=5)
	//         └── Node: n2 (OfferSlot=5)
	n1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: schedulingv1alpha1.NodeTopologyLayer,
			Name:  "n1",
		},
		OfferSlot: 5,
	}
	n2 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: schedulingv1alpha1.NodeTopologyLayer,
			Name:  "n2",
		},
		OfferSlot: 5,
	}

	b1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: "BlockLayer",
			Name:  "b1",
		},
		OfferSlot: 10,
		Children: map[string]*networktopology.TreeNode{
			"n1": n1,
			"n2": n2,
		},
	}

	s1 := &networktopology.TreeNode{
		TreeNodeMeta: networktopology.TreeNodeMeta{
			Layer: "SpineLayer",
			Name:  "s1",
		},
		OfferSlot: 10,
		Children: map[string]*networktopology.TreeNode{
			"b1": b1,
		},
	}

	n1.Parent = b1
	n2.Parent = b1
	b1.Parent = s1

	layerSlotMultiple := map[schedulingv1alpha1.TopologyLayer]int{
		schedulingv1alpha1.NodeTopologyLayer: 4,
	}

	distribution := make(map[string]int)
	_, actualSlot := distributeOfferSlot(10, s1, distribution, layerSlotMultiple)

	assert.Equal(t, 8, actualSlot, "should only allocate 8 slots due to SlotMultiple constraint")
	assert.Equal(t, 4, distribution["n1"], "n1 should have 4 slots")
	assert.Equal(t, 4, distribution["n2"], "n2 should have 4 slots")
}
