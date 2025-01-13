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

package deviceshare

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
)

func newBitMask(bits ...int) bitmask.BitMask {
	mask, _ := bitmask.NewBitMask(bits...)
	return mask
}

func TestPlugin_GetPodTopologyHints(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
	}

	gpuRequests := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("100"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
	}
	largeGPURequets := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("1700"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("1700"),
	}
	rdmaRequests := corev1.ResourceList{
		apiext.ResourceRDMA: resource.MustParse("2"),
	}
	tests := []struct {
		name            string
		podRequests     map[schedulingv1alpha1.DeviceType]corev1.ResourceList
		hints           apiext.DeviceAllocateHints
		jointAllocate   *apiext.DeviceJointAllocate
		assignedDevices apiext.DeviceAllocations
		want            map[string][]topologymanager.NUMATopologyHint
		wantErr         bool
	}{
		{
			name: "generate gpu&rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU:  gpuRequests,
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.GPU): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate gpu&rdma hints but large gpu requests",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU:  largeGPURequets,
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "generate gpu hints with assigned devices",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU: {
					apiext.ResourceGPUCore:        resource.MustParse("400"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("400"),
				},
			},
			assignedDevices: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
				schedulingv1alpha1.GPU: {
					{
						Minor:     0,
						Resources: gpuRequests,
					},
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.GPU): {
					{NUMANodeAffinity: newBitMask(1), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate fpga empty hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.FPGA: {
					apiext.ResourceFPGA: resource.MustParse("100"),
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "generate 2 rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate rdma 2 vf hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector:       &metav1.LabelSelector{},
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate rdma 4 vf hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: {
					apiext.ResourceRDMA: resource.MustParse("4"),
				},
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector:       &metav1.LabelSelector{},
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: true, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate joint-allocate gpu&rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
				schedulingv1alpha1.GPU:  gpuRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector: &metav1.LabelSelector{},
				},
			},
			jointAllocate: &apiext.DeviceJointAllocate{
				DeviceTypes: []schedulingv1alpha1.DeviceType{schedulingv1alpha1.GPU, schedulingv1alpha1.RDMA},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
				string(schedulingv1alpha1.GPU): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, []*corev1.Node{node})
			deviceCR := fakeDeviceCR.DeepCopy()
			deviceCR.Name = node.Name
			deviceCR.ResourceVersion = "1"
			_, err := suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), deviceCR, metav1.CreateOptions{})
			assert.NoError(t, err)

			p, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)

			suit.koordinatorSharedInformerFactory.Start(nil)
			suit.SharedInformerFactory().Start(nil)
			suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)
			suit.SharedInformerFactory().WaitForCacheSync(nil)

			pl := p.(*Plugin)
			if tt.assignedDevices != nil {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "assigned-pod",
						UID:       "123456",
					},
					Spec: corev1.PodSpec{
						NodeName: node.Name,
					},
				}
				assert.NoError(t, apiext.SetDeviceAllocations(pod, tt.assignedDevices))
				pl.nodeDeviceCache.updatePod(nil, pod)
			}

			hintSelectors, err := newHintSelectors(tt.hints)
			assert.NoError(t, err)

			pod := &corev1.Pod{}
			state := &preFilterState{
				skip:          false,
				podRequests:   tt.podRequests,
				hints:         tt.hints,
				hintSelectors: hintSelectors,
				jointAllocate: tt.jointAllocate,
			}
			state.gpuRequirements, _ = parseGPURequirements(pod, state.podRequests, state.hints[schedulingv1alpha1.GPU])
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, state)

			got, status := pl.GetPodTopologyHints(context.TODO(), cycleState, pod, node.Name)
			assert.Equal(t, tt.want, got)
			if !tt.wantErr != status.IsSuccess() {
				t.Errorf("expect tt.wantErr=%v, but got %v", tt.wantErr, status)
				return
			}
		})
	}
}

func TestPlugin_Allocate(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
	}

	gpuRequests := corev1.ResourceList{
		apiext.ResourceGPUCore:   resource.MustParse("100"),
		apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
	}
	rdmaRequests := corev1.ResourceList{
		apiext.ResourceRDMA: resource.MustParse("2"),
	}
	tests := []struct {
		name          string
		podRequests   map[schedulingv1alpha1.DeviceType]corev1.ResourceList
		hints         apiext.DeviceAllocateHints
		affinity      topologymanager.NUMATopologyHint
		jointAllocate *apiext.DeviceJointAllocate
		wantErr       bool
	}{
		{
			name: "allocate gpu&rdma by affinity",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU:  gpuRequests,
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
		},
		{
			name: "generate fpga empty hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.FPGA: {
					apiext.ResourceFPGA: resource.MustParse("100"),
				},
			},
			affinity: topologymanager.NUMATopologyHint{},
			wantErr:  true,
		},
		{
			name: "generate 2 rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
		},
		{
			name: "generate rdma 2 vf hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector:       &metav1.LabelSelector{},
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
		},
		{
			name: "generate rdma 4 vf hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: {
					apiext.ResourceRDMA: resource.MustParse("4"),
				},
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector:       &metav1.LabelSelector{},
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
			wantErr: true,
		},
		{
			name: "generate joint-allocate gpu&rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
				schedulingv1alpha1.GPU:  gpuRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector: &metav1.LabelSelector{},
				},
			},
			jointAllocate: &apiext.DeviceJointAllocate{
				DeviceTypes: []schedulingv1alpha1.DeviceType{schedulingv1alpha1.GPU, schedulingv1alpha1.RDMA},
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, []*corev1.Node{node})
			deviceCR := fakeDeviceCR.DeepCopy()
			deviceCR.Name = node.Name
			deviceCR.ResourceVersion = "1"
			_, err := suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), deviceCR, metav1.CreateOptions{})
			assert.NoError(t, err)
			p, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)

			suit.koordinatorSharedInformerFactory.Start(nil)
			suit.SharedInformerFactory().Start(nil)
			suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)
			suit.SharedInformerFactory().WaitForCacheSync(nil)

			hintSelectors, err := newHintSelectors(tt.hints)
			assert.NoError(t, err)

			pod := &corev1.Pod{}
			state := &preFilterState{
				skip:          false,
				podRequests:   tt.podRequests,
				hints:         tt.hints,
				hintSelectors: hintSelectors,
				jointAllocate: tt.jointAllocate,
			}
			state.gpuRequirements, _ = parseGPURequirements(pod, tt.podRequests, state.hints[schedulingv1alpha1.GPU])
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, state)
			pl := p.(*Plugin)
			status := pl.Allocate(context.TODO(), cycleState, tt.affinity, pod, node.Name)
			if !tt.wantErr != status.IsSuccess() {
				t.Errorf("expect tt.wantErr=%v, but got %v", tt.wantErr, status)
				return
			}
		})
	}
}
