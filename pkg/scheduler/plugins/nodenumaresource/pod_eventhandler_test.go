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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func TestPodEventHandler(t *testing.T) {
	tests := []struct {
		name    string
		pod     *corev1.Pod
		wantAdd bool
		want    cpuset.CPUSet
	}{
		{
			name: "pending pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:  uuid.NewUUID(),
					Name: "test",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
		},
		{
			name: "scheduled CPU Shared Pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:  uuid.NewUUID(),
					Name: "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node-1",
				},
			},
		},
		{
			name: "terminated Pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:  uuid.NewUUID(),
					Name: "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node-1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodSucceeded,
				},
			},
		},
		{
			name: "running LSR Pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:  uuid.NewUUID(),
					Name: "test",
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec:   `{"preferredCPUBindPolicy": "FullPCPUs"}`,
						extension.AnnotationResourceStatus: `{"cpuset": "0-3"}`,
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node-1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			wantAdd: true,
			want:    cpuset.MustParse("0-3"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpuTopology := buildCPUTopologyForTest(2, 2, 4, 2)
			topologyOptionsManager := NewTopologyOptionsManager()
			topologyOptionsManager.UpdateTopologyOptions("test-node-1", func(options *TopologyOptions) {
				options.CPUTopology = cpuTopology
			})
			resourceManager := &resourceManager{
				topologyOptionsManager: topologyOptionsManager,
				nodeAllocations:        map[string]*NodeAllocation{},
			}
			handler := &podEventHandler{
				resourceManager: resourceManager,
			}
			handler.OnAdd(tt.pod, true)
			handler.OnUpdate(tt.pod, tt.pod)

			nodeAllocation := resourceManager.getOrCreateNodeAllocation("test-node-1")
			_, ok := nodeAllocation.allocatedPods[tt.pod.UID]
			if tt.wantAdd && !ok {
				t.Errorf("expect add the Pod but not found")
			} else if !tt.wantAdd && ok {
				t.Errorf("expect not add the Pod but found")
			}

			cpusetBuilder := cpuset.NewCPUSetBuilder()
			for _, v := range nodeAllocation.allocatedCPUs {
				cpusetBuilder.Add(v.CPUID)
			}
			cpuset := cpusetBuilder.Result()
			if tt.want.IsEmpty() && !cpuset.IsEmpty() {
				t.Errorf("expect empty cpuset but got")
			} else if !tt.want.IsEmpty() && cpuset.IsEmpty() {
				t.Errorf("expect cpuset but got empty")
			} else if !tt.want.Equals(cpuset) {
				t.Errorf("expect cpuset equal, but failed, expect: %v, got: %v", tt.want, cpuset)
			}
			allocation := nodeAllocation.allocatedPods[tt.pod.UID]
			cpuset = allocation.CPUSet
			if tt.want.IsEmpty() && !cpuset.IsEmpty() {
				t.Errorf("expect empty cpuset but got")
			} else if !tt.want.IsEmpty() && cpuset.IsEmpty() {
				t.Errorf("expect cpuset but got empty")
			} else if !tt.want.Equals(cpuset) {
				t.Errorf("expect cpuset equal, but failed, expect: %v, got: %v", tt.want, cpuset)
			}

			handler.OnDelete(tt.pod)
			assert.Empty(t, nodeAllocation.allocatedPods)
			assert.Empty(t, nodeAllocation.allocatedCPUs)
		})
	}

}

func TestPodEventHandler_UpdatePod_unassigned(t *testing.T) {
	tests := []struct {
		name                       string
		oldPod                     *corev1.Pod
		newPod                     *corev1.Pod
		wantOldPodCleanedFromCache bool
	}{
		{
			name: "newPod nodeName becomes empty, should clean up oldPod",
			oldPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       uuid.NewUUID(),
					Namespace: "default",
					Name:      "test-pod",
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec:   `{"preferredCPUBindPolicy": "FullPCPUs"}`,
						extension.AnnotationResourceStatus: `{"cpuset": "0-3"}`,
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "node1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       uuid.NewUUID(),
					Namespace: "default",
					Name:      "test-pod",
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec:   `{"preferredCPUBindPolicy": "FullPCPUs"}`,
						extension.AnnotationResourceStatus: `{"cpuset": "0-3"}`,
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "", // nodeName becomes empty
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			wantOldPodCleanedFromCache: true,
		},
		{
			name: "oldPod without nodeName, should not clean up",
			oldPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       uuid.NewUUID(),
					Namespace: "default",
					Name:      "test-pod",
				},
				Spec: corev1.PodSpec{
					NodeName: "", // oldPod also has no nodeName
				},
			},
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       uuid.NewUUID(),
					Namespace: "default",
					Name:      "test-pod",
				},
				Spec: corev1.PodSpec{
					NodeName: "",
				},
			},
			wantOldPodCleanedFromCache: false,
		},
		{
			name:   "oldPod is nil, should not clean up",
			oldPod: nil,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       uuid.NewUUID(),
					Namespace: "default",
					Name:      "test-pod",
				},
				Spec: corev1.PodSpec{
					NodeName: "",
				},
			},
			wantOldPodCleanedFromCache: false,
		},
		{
			name: "normal update: newPod has nodeName, should process normally",
			oldPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       uuid.NewUUID(),
					Namespace: "default",
					Name:      "test-pod",
				},
				Spec: corev1.PodSpec{
					NodeName: "",
				},
			},
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       uuid.NewUUID(),
					Namespace: "default",
					Name:      "test-pod",
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec:   `{"preferredCPUBindPolicy": "FullPCPUs"}`,
						extension.AnnotationResourceStatus: `{"cpuset": "0-3"}`,
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "node1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			wantOldPodCleanedFromCache: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cpuTopology := buildCPUTopologyForTest(2, 2, 4, 2)
			topologyOptionsManager := NewTopologyOptionsManager()
			topologyOptionsManager.UpdateTopologyOptions("node1", func(options *TopologyOptions) {
				options.CPUTopology = cpuTopology
			})
			resourceManager := &resourceManager{
				topologyOptionsManager: topologyOptionsManager,
				nodeAllocations:        map[string]*NodeAllocation{},
			}
			handler := &podEventHandler{
				resourceManager: resourceManager,
			}

			// Pre-populate cache if oldPod has nodeName and allocations
			if tt.oldPod != nil && tt.oldPod.Spec.NodeName != "" {
				if _, ok := tt.oldPod.Annotations[extension.AnnotationResourceStatus]; ok {
					handler.updatePod(nil, tt.oldPod)
				}
			}

			// Call updatePod
			handler.updatePod(tt.oldPod, tt.newPod)

			// Verify cache state
			if tt.wantOldPodCleanedFromCache && tt.oldPod != nil && tt.oldPod.Spec.NodeName != "" {
				nodeAllocation := resourceManager.getOrCreateNodeAllocation(tt.oldPod.Spec.NodeName)
				_, exists := nodeAllocation.allocatedPods[tt.oldPod.UID]
				assert.False(t, exists, "oldPod allocation should be cleaned from cache")
			}
		})
	}
}
