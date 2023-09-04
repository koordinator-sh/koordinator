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

package nodenumaresource

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
)

func TestAllocate(t *testing.T) {
	tests := []struct {
		name    string
		pod     *corev1.Pod
		options *ResourceOptions
		want    *PodAllocation
		wantErr bool
	}{
		{
			name: "allocate with non-existing resources in NUMA",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:  4,
				requestCPUBind: false,
				requests: corev1.ResourceList{
					corev1.ResourceCPU:       resource.MustParse("4"),
					apiext.ResourceGPUMemory: resource.MustParse("10Gi"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			want: &PodAllocation{
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate with insufficient resources",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:  4,
				requestCPUBind: false,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("54"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			tom := NewTopologyOptionsManager()
			tom.UpdateTopologyOptions("test-node", func(options *TopologyOptions) {
				options.CPUTopology = buildCPUTopologyForTest(2, 1, 26, 2)
				options.NUMANodeResources = []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("52"),
							corev1.ResourceMemory: resource.MustParse("128Gi"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("52"),
							corev1.ResourceMemory: resource.MustParse("128Gi"),
						},
					},
				}
			})
			resourceManager := NewResourceManager(suit.Handle, schedulingconfig.NUMALeastAllocated, tom)
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("104"),
						corev1.ResourceMemory: resource.MustParse("256Gi"),
					},
				},
			}
			got, err := resourceManager.Allocate(node, tt.pod, tt.options)
			if tt.wantErr != (err != nil) {
				t.Errorf("wantErr %v but got %v", tt.wantErr, err != nil)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
