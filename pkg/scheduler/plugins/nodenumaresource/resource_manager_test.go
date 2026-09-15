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
	quotav1 "k8s.io/apiserver/pkg/quota/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func TestResourceManagerAllocate(t *testing.T) {
	tests := []struct {
		name                string
		pod                 *corev1.Pod
		options             *ResourceOptions
		amplificationRatios map[corev1.ResourceName]apiext.Ratio
		allocated           *PodAllocation
		want                *PodAllocation
		wantErr             bool
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
		{
			name: "allocate with required CPUBindPolicyFullPCPUs",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0-3"),
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
			name: "allocate with required CPUBindPolicyFullPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("4-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0-3"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(4, resource.DecimalSI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "failed to allocate with required CPUBindPolicyFullPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("1,3,5,7-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,4,6"),
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
			name: "allocate with required CPUBindPolicySpreadByPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("1,3,5,7-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,4,6"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(4, resource.DecimalSI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "failed to allocate with required CPUBindPolicySpreadByPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("4-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs and amplified requests",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("6"),
				},
				originalRequests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			amplificationRatios: map[corev1.ResourceName]apiext.Ratio{
				corev1.ResourceCPU: 1.5,
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,4,6"),
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
			name: "allocate with required CPUBindPolicySpreadByPCPUs and allocated and amplified requests",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("6"),
				},
				originalRequests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			amplificationRatios: map[corev1.ResourceName]apiext.Ratio{
				corev1.ResourceCPU: 1.5,
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("1,3,5,7-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,4,6"),
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
			name: "failed to allocate with CPU Share and allocated and amplified ratios",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        false,
				requiredCPUBindPolicy: false,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				originalRequests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			amplificationRatios: map[corev1.ResourceName]apiext.Ratio{
				corev1.ResourceCPU: 1.5,
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("0-49,52-101"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50"),
						},
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "allocate by numa hint on mixed cpuset/share node",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         8,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("8"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("0-43,53-96"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("44-47,98-101"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(4, resource.DecimalSI),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(4, resource.DecimalSI),
						},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, nil)
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
			tt.options.topologyOptions = tom.GetTopologyOptions("test-node")

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
			apiext.SetNodeResourceAmplificationRatios(node, tt.amplificationRatios)
			resourceManager := NewResourceManager(suit.Handle, schedulingconfig.NUMALeastAllocated, tom)
			if tt.allocated != nil {
				resourceManager.Update(node.Name, tt.allocated)
			}
			if tt.options.originalRequests == nil {
				tt.options.originalRequests = tt.options.requests.DeepCopy()
			}
			assert.NoError(t, amplifyNUMANodeResources(node, &tt.options.topologyOptions))

			got, err := resourceManager.Allocate(node, tt.pod, tt.options)
			if tt.wantErr != (err != nil) {
				t.Errorf("wantErr %v but got %v", tt.wantErr, err != nil)
			}
			if tt.want != nil {
				for i := range tt.want.NUMANodeResources {
					assert.Equal(t, tt.want.NUMANodeResources[i].Node, got.NUMANodeResources[i].Node)
					assert.True(t, quotav1.Equals(tt.want.NUMANodeResources[i].Resources, got.NUMANodeResources[i].Resources))
				}
				tt.want.NUMANodeResources = nil
				got.NUMANodeResources = nil
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAllocateDistributeEvenly(t *testing.T) {
	tests := []struct {
		name                string
		pod                 *corev1.Pod
		options             *ResourceOptions
		amplificationRatios map[corev1.ResourceName]apiext.Ratio
		allocated           *PodAllocation
		want                *PodAllocation
		wantErr             bool
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
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			want: &PodAllocation{
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
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
					corev1.ResourceCPU: resource.MustParse("108"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "allocate with required CPUBindPolicyFullPCPUs",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0-1,52-53"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate with required CPUBindPolicyFullPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("4-101"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50"),
						},
					},
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0-1,102-103"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "failed to allocate with required CPUBindPolicyFullPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("1,3,5,7-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,52,54"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs and allocated, plural numCPUsNeeded, not balanced",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("1,3,5,7-101,103"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50"),
						},
					},
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,4,102"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(3, resource.DecimalSI),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs and allocated, plural numCPUsNeeded, balanced",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("4-100"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("49"),
						},
					},
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,101-102"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs and allocated, singular num cpus needed",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         5,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("5"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("1,3,5,7-97,103"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("47"),
						},
					},
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,4,98,100"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(3, resource.DecimalSI),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "failed to allocate with required CPUBindPolicySpreadByPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("4-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs and amplified requests",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("6"),
				},
				originalRequests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			amplificationRatios: map[corev1.ResourceName]apiext.Ratio{
				corev1.ResourceCPU: 1.5,
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,52,54"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs and allocated and amplified requests",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("6"),
				},
				originalRequests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			amplificationRatios: map[corev1.ResourceName]apiext.Ratio{
				corev1.ResourceCPU: 1.5,
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("1,3,5,7-101"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50"),
						},
					},
				},
			},
			want: &PodAllocation{
				CPUSet: cpuset.MustParse("0,2,4,102"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("3"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate with CPU Share and allocated and amplified ratios",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         0,
				requestCPUBind:        false,
				requiredCPUBindPolicy: false,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("3.5"),
				},
				originalRequests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("3.5"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			amplificationRatios: map[corev1.ResourceName]apiext.Ratio{
				corev1.ResourceCPU: 1.5,
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("0-49,52-101"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50"),
						},
					},
				},
			},
			want: &PodAllocation{
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1.75"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1.75"),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate memory evenly across all NUMA nodes with NUMADistributeEvenly",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:            0,
				requestCPUBind:           false,
				preferWidestNUMAAffinity: true,
				requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("200Gi"),
				},
				hint: topologymanager.NUMATopologyHint{
					NUMANodeAffinity: func() bitmask.BitMask {
						mask, _ := bitmask.NewBitMask(0, 1)
						return mask
					}(),
				},
			},
			want: &PodAllocation{
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("100Gi"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("100Gi"),
						},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, nil)
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
			tt.options.topologyOptions = tom.GetTopologyOptions("test-node")

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
			apiext.SetNodeResourceAmplificationRatios(node, tt.amplificationRatios)
			resourceManager := NewResourceManager(suit.Handle, schedulingconfig.NUMALeastAllocated, tom)
			if tt.allocated != nil {
				resourceManager.Update(node.Name, tt.allocated)
			}
			if tt.options.originalRequests == nil {
				tt.options.originalRequests = tt.options.requests.DeepCopy()
			}
			assert.NoError(t, amplifyNUMANodeResources(node, &tt.options.topologyOptions))

			got, err := resourceManager.Allocate(node, tt.pod, tt.options)
			if tt.wantErr != (err != nil) {
				t.Errorf("wantErr %v but got %v", tt.wantErr, err != nil)
			}
			if tt.want != nil {
				for i := range tt.want.NUMANodeResources {
					assert.Equal(t, tt.want.NUMANodeResources[i].Node, got.NUMANodeResources[i].Node)
					assert.True(t, quotav1.Equals(tt.want.NUMANodeResources[i].Resources, got.NUMANodeResources[i].Resources))
				}
				tt.want.NUMANodeResources = nil
				got.NUMANodeResources = nil
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResourceManagerGetTopologyHint(t *testing.T) {
	tests := []struct {
		name                string
		pod                 *corev1.Pod
		options             *ResourceOptions
		amplificationRatios map[corev1.ResourceName]apiext.Ratio
		allocated           *PodAllocation
		want                map[string][]topologymanager.NUMATopologyHint
		wantErr             bool
	}{
		{
			name: "allocate with required CPUBindPolicyFullPCPUs",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU): {
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0)
							return mask
						}(),
						Preferred: true,
					},
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(1)
							return mask
						}(),
						Preferred: true,
					},
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0, 1)
							return mask
						}(),
						Preferred: false,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate with required CPUBindPolicyFullPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("4-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU): {
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0)
							return mask
						}(),
						Preferred: true,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "failed to allocate with required CPUBindPolicyFullPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicyFullPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("1,3,5,7-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU): {},
			},
			wantErr: false,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU): {
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0)
							return mask
						}(),
						Preferred: true,
					},
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(1)
							return mask
						}(),
						Preferred: true,
					},
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0, 1)
							return mask
						}(),
						Preferred: false,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "allocate with required CPUBindPolicySpreadByPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("1,3,5,7-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU): {
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0)
							return mask
						}(),
						Preferred: true,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "failed to allocate with required CPUBindPolicySpreadByPCPUs and allocated",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        true,
				requiredCPUBindPolicy: true,
				cpuBindPolicy:         schedulingconfig.CPUBindPolicySpreadByPCPUs,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("4-104"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("48"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("52"),
						},
					},
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU): {},
			},
			wantErr: false,
		},
		{
			name: "failed to allocate with CPU Share and allocated and amplified ratios",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:         4,
				requestCPUBind:        false,
				requiredCPUBindPolicy: false,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				originalRequests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
			amplificationRatios: map[corev1.ResourceName]apiext.Ratio{
				corev1.ResourceCPU: 1.5,
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				CPUSet:    cpuset.MustParse("0-49,52-101"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("50"),
						},
					},
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU): {
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0, 1)
							return mask
						}(),
						Preferred: true,
					},
				},
			},
		},
		{
			name: "failed to generate hints with insufficient memory and hugepages",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded: 4,
				requests: corev1.ResourceList{
					corev1.ResourceCPU:                     resource.MustParse("4"),
					corev1.ResourceMemory:                  resource.MustParse("4Gi"),
					corev1.ResourceHugePagesPrefix + "1Gi": resource.MustParse("4Gi"),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:                     resource.MustParse("4"),
							corev1.ResourceMemory:                  resource.MustParse("120Gi"),
							corev1.ResourceHugePagesPrefix + "1Gi": resource.MustParse("4Gi"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:                     resource.MustParse("4"),
							corev1.ResourceMemory:                  resource.MustParse("120Gi"),
							corev1.ResourceHugePagesPrefix + "1Gi": resource.MustParse("4Gi"),
						},
					},
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU):             {},
				string(corev1.ResourceMemory):          {},
				corev1.ResourceHugePagesPrefix + "1Gi": {},
			},
			wantErr: false,
		},
		{
			name: "distribute evenly prefers spanning all NUMA nodes",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:            4,
				requestCPUBind:           false,
				preferWidestNUMAAffinity: true,
				requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU): {
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0)
							return mask
						}(),
						Preferred: false,
					},
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(1)
							return mask
						}(),
						Preferred: false,
					},
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0, 1)
							return mask
						}(),
						Preferred: true,
					},
				},
				string(corev1.ResourceMemory): {
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0)
							return mask
						}(),
						Preferred: false,
					},
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(1)
							return mask
						}(),
						Preferred: false,
					},
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0, 1)
							return mask
						}(),
						Preferred: true,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "distribute evenly falls back to narrower affinity when wider is infeasible",
			pod:  &corev1.Pod{},
			options: &ResourceOptions{
				numCPUsNeeded:            4,
				requestCPUBind:           false,
				preferWidestNUMAAffinity: true,
				requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("128Gi"),
				},
			},
			allocated: &PodAllocation{
				UID:       "123456",
				Name:      "test-xxx",
				Namespace: "default",
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("128Gi"),
						},
					},
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(corev1.ResourceCPU): {
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(1)
							return mask
						}(),
						Preferred: false,
					},
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(0, 1)
							return mask
						}(),
						Preferred: true,
					},
				},
				string(corev1.ResourceMemory): {
					{
						NUMANodeAffinity: func() bitmask.BitMask {
							mask, _ := bitmask.NewBitMask(1)
							return mask
						}(),
						Preferred: true,
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, nil)
			tom := NewTopologyOptionsManager()
			tom.UpdateTopologyOptions("test-node", func(options *TopologyOptions) {
				options.CPUTopology = buildCPUTopologyForTest(2, 1, 26, 2)
				options.NUMANodeResources = []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:                     resource.MustParse("52"),
							corev1.ResourceMemory:                  resource.MustParse("128Gi"),
							corev1.ResourceHugePagesPrefix + "1Gi": resource.MustParse("4Gi"),
						},
					},
					{
						Node: 1,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:                     resource.MustParse("52"),
							corev1.ResourceMemory:                  resource.MustParse("128Gi"),
							corev1.ResourceHugePagesPrefix + "1Gi": resource.MustParse("4Gi"),
						},
					},
				}
			})
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:                     resource.MustParse("104"),
						corev1.ResourceMemory:                  resource.MustParse("256Gi"),
						corev1.ResourceHugePagesPrefix + "1Gi": resource.MustParse("8Gi"),
					},
				},
			}
			apiext.SetNodeResourceAmplificationRatios(node, tt.amplificationRatios)

			resourceManager := NewResourceManager(suit.Handle, schedulingconfig.NUMALeastAllocated, tom)
			if tt.allocated != nil {
				resourceManager.Update(node.Name, tt.allocated)
			}
			tt.options.topologyOptions = tom.GetTopologyOptions(node.Name)

			if tt.options.originalRequests == nil {
				tt.options.originalRequests = tt.options.requests.DeepCopy()
			}
			assert.NoError(t, amplifyNUMANodeResources(node, &tt.options.topologyOptions))

			got, err := resourceManager.GetTopologyHints(node, tt.pod, tt.options, apiext.NUMATopologyPolicyBestEffort, &nodeReservationRestoreStateData{})
			if tt.wantErr != (err != nil) {
				t.Errorf("wantErr %v but got %v", tt.wantErr, err != nil)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDistributeEvenlyRestrictedAdmission guards Restricted admission. Under the Restricted policy the even-spread
// hint flip must be exempted. Otherwise, because maxAffinitySize is tracked per resource, a pod whose resources
// have different widest feasible masks (here cpu can span {0,1} but memory only fits {1}) would get per-resource
// Preferred flags on non-overlapping masks; mergePermutation then marks every merged permutation non-preferred,
// and Restricted (canAdmitPodResult == hint.Preferred) rejects an otherwise-schedulable pod.
func TestDistributeEvenlyRestrictedAdmission(t *testing.T) {
	suit := newPluginTestSuit(t, nil, nil)
	tom := NewTopologyOptionsManager()
	tom.UpdateTopologyOptions("test-node", func(options *TopologyOptions) {
		options.CPUTopology = buildCPUTopologyForTest(2, 1, 26, 2)
		options.NUMANodeResources = []NUMANodeResource{
			{Node: 0, Resources: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("52"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			}},
			{Node: 1, Resources: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("52"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			}},
		}
	})
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("104"),
				corev1.ResourceMemory: resource.MustParse("256Gi"),
			},
		},
	}
	resourceManager := NewResourceManager(suit.Handle, schedulingconfig.NUMALeastAllocated, tom)
	// Consume node 0's memory so the widest feasible mask differs across resources (cpu spans {0,1}, memory
	// only fits {1}) — the exact shape that made Restricted reject the pod before the exemption.
	resourceManager.Update("test-node", &PodAllocation{
		UID:       "123456",
		Name:      "test-xxx",
		Namespace: "default",
		NUMANodeResources: []NUMANodeResource{
			{Node: 0, Resources: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Gi")}},
		},
	})

	pod := &corev1.Pod{}
	newOptions := func(preferWidest bool) *ResourceOptions {
		options := &ResourceOptions{
			numCPUsNeeded:            4,
			requestCPUBind:           false,
			preferWidestNUMAAffinity: preferWidest,
			requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
			topologyOptions: tom.GetTopologyOptions("test-node"),
		}
		options.originalRequests = options.requests.DeepCopy()
		assert.NoError(t, amplifyNUMANodeResources(node, &options.topologyOptions))
		return options
	}

	// Under Restricted the flip is exempted: the hints are identical whether or not even spread is requested.
	restrictedWidest, err := resourceManager.GetTopologyHints(node, pod, newOptions(true), apiext.NUMATopologyPolicyRestricted, &nodeReservationRestoreStateData{})
	assert.NoError(t, err)
	restrictedNarrow, err := resourceManager.GetTopologyHints(node, pod, newOptions(false), apiext.NUMATopologyPolicyRestricted, &nodeReservationRestoreStateData{})
	assert.NoError(t, err)
	assert.Equal(t, restrictedNarrow, restrictedWidest, "Restricted must not apply the even-spread flip")

	// And the pod is admitted under Restricted; before the fix the merged hint was non-preferred and rejected.
	numaNodes := []int{0, 1}
	numaNodeStatus := resourceManager.GetNodeAllocation("test-node").GetAllNUMANodeStatus(len(numaNodes))
	_, admit, reasons := topologymanager.NewRestrictedPolicy(numaNodes).Merge(
		[]map[string][]topologymanager.NUMATopologyHint{restrictedWidest}, "", numaNodeStatus)
	assert.True(t, admit, "Restricted must admit the DistributeEvenly pod; reasons: %v", reasons)

	// Contrast: under BestEffort the flip is still active, so even spread changes the hint preference. This
	// proves the exemption is policy-scoped rather than a global no-op of the DistributeEvenly feature.
	bestEffortWidest, err := resourceManager.GetTopologyHints(node, pod, newOptions(true), apiext.NUMATopologyPolicyBestEffort, &nodeReservationRestoreStateData{})
	assert.NoError(t, err)
	bestEffortNarrow, err := resourceManager.GetTopologyHints(node, pod, newOptions(false), apiext.NUMATopologyPolicyBestEffort, &nodeReservationRestoreStateData{})
	assert.NoError(t, err)
	assert.NotEqual(t, bestEffortNarrow, bestEffortWidest, "BestEffort must still apply the even-spread flip")
}
