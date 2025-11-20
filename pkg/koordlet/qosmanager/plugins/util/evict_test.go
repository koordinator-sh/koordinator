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

package util

import (
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
)

const defaultEvictPriority = 5999

func Test_DefaultEvictionExecutor(t *testing.T) {
	// test IsPodEvicted
	evictor := NewEvictor(nil, nil, "")
	d := &DefaultEvictionExecutor{
		Evictor: evictor,
	}
	res := d.IsPodEvicted(nil)
	assert.Equal(t, false, res)

	// test Evict
	fakeRecorder := &testutil.FakeRecorder{}
	evictor = NewEvictor(nil, fakeRecorder, "")
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{},
		},
	}
	tests := []struct {
		name           string
		onlyEvictByAPI bool
		expect         bool
	}{
		{
			name:           "evict by api: omit evict failed",
			onlyEvictByAPI: true,
			expect:         false,
		},
		{
			name:           "evict by kill containers: omit evict failed",
			onlyEvictByAPI: false,
			expect:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DefaultEvictionExecutor{
				OnlyEvictByAPI: tt.onlyEvictByAPI,
				Evictor:        evictor,
			}
			res := d.Evict(pod, nil, "", "")
			assert.Equal(t, tt.expect, res)
		})
	}
}

func Test_DefaultEvictionExecutor_IsPodEvicted(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{},
		},
	}
	tests := []struct {
		name        string
		evictedPods []*corev1.Pod
		pod         *corev1.Pod
		expect      bool
	}{
		{
			name:        "pod nil",
			evictedPods: nil,
			pod:         nil,
			expect:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evictor := NewEvictor(nil, nil, "")
			d := &DefaultEvictionExecutor{
				Evictor: evictor,
			}
			res := d.IsPodEvicted(pod)
			assert.Equal(t, tt.expect, res)
		})
	}
}

func Test_InitializeEvictionExecutor(t *testing.T) {
	customExecutorInitializer = nil
	assert.Equal(t, InitializeEvictionExecutor(nil, false), &DefaultEvictionExecutor{
		OnlyEvictByAPI: false,
		Evictor:        nil,
	})
	customExecutorInitializer = func(*Evictor, bool) EvictionExecutor {
		return &DefaultEvictionExecutor{
			OnlyEvictByAPI: true,
			Evictor:        nil,
		}
	}
	evictor := NewEvictor(nil, nil, "")
	assert.Equal(t, InitializeEvictionExecutor(evictor, false), &DefaultEvictionExecutor{
		OnlyEvictByAPI: true,
		Evictor:        nil,
	})
}

func Test_UpdateReleasedByPod(t *testing.T) {
	tests := []struct {
		name                 string
		podInfo              *PodEvictInfo
		releaseTargetTypes   []ReleaseTargetType
		releaseResourceTypes map[corev1.ResourceName]bool
		initialReleased      ReleaseList
		expected             ReleaseList
	}{
		{
			name: "nil initialReleased",
			podInfo: &PodEvictInfo{
				MilliCPURequest: 500,
				MemoryRequest:   1024 * 1024 * 1024, // 1Gi
				MilliCPUUsed:    300,
				MemoryUsed:      512 * 1024 * 1024, // 512Mi
			},
			releaseTargetTypes: []ReleaseTargetType{ReleaseTargetTypeBatchResourceRequest},
			releaseResourceTypes: map[corev1.ResourceName]bool{
				corev1.ResourceCPU:    true,
				corev1.ResourceMemory: true,
			},
			initialReleased: nil,
			expected:        nil,
		},
		{
			name: "normal ReleaseTargetTypeBatchResourceRequest",
			podInfo: &PodEvictInfo{
				MilliCPURequest: 500,
				MemoryRequest:   1024 * 1024 * 1024, // 1Gi
				MilliCPUUsed:    300,
				MemoryUsed:      512 * 1024 * 1024, // 512Mi
			},
			releaseTargetTypes: []ReleaseTargetType{ReleaseTargetTypeBatchResourceRequest},
			releaseResourceTypes: map[corev1.ResourceName]bool{
				corev1.ResourceCPU:    true,
				corev1.ResourceMemory: true,
			},
			initialReleased: ReleaseList{},
			expected: ReleaseList{
				ReleaseTargetTypeBatchResourceRequest: {
					corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
		},
		{
			name: "ReleaseTargetTypeResourceUsed + cpu only",
			podInfo: &PodEvictInfo{
				MilliCPURequest: 500,
				MemoryRequest:   1073741824,
				MilliCPUUsed:    300,
				MemoryUsed:      536870912,
			},
			releaseTargetTypes: []ReleaseTargetType{ReleaseTargetTypeResourceUsed},
			releaseResourceTypes: map[corev1.ResourceName]bool{
				corev1.ResourceCPU: true,
			},
			initialReleased: ReleaseList{},
			expected: ReleaseList{
				ReleaseTargetTypeResourceUsed: {
					corev1.ResourceCPU: *resource.NewMilliQuantity(300, resource.DecimalSI),
				},
			},
		},
		{
			name: "initialReleased not nil",
			podInfo: &PodEvictInfo{
				MilliCPURequest: 500,
				MemoryRequest:   1073741824,
				MilliCPUUsed:    300,
				MemoryUsed:      536870912,
			},
			releaseTargetTypes: []ReleaseTargetType{ReleaseTargetTypeResourceUsed},
			releaseResourceTypes: map[corev1.ResourceName]bool{
				corev1.ResourceCPU: true,
			},
			initialReleased: ReleaseList{
				ReleaseTargetTypeResourceUsed: {
					corev1.ResourceCPU: *resource.NewMilliQuantity(200, resource.DecimalSI),
				},
			},
			expected: ReleaseList{
				ReleaseTargetTypeResourceUsed: {
					corev1.ResourceCPU: *resource.NewMilliQuantity(500, resource.DecimalSI),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateReleasedByPod(tt.podInfo, tt.releaseTargetTypes, tt.releaseResourceTypes, tt.initialReleased)
			if !reflect.DeepEqual(tt.initialReleased, tt.expected) {
				t.Errorf("got %v, want %v", tt.initialReleased, tt.expected)
			}
		})
	}
}

func Test_SubReleaseListNoNegative(t *testing.T) {
	tests := []struct {
		name     string
		a        corev1.ResourceList
		b        corev1.ResourceList
		expected corev1.ResourceList
	}{
		{
			name: "nil a",
			a:    nil,
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
			expected: make(corev1.ResourceList),
		},
		{
			name: "nil b",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
			b: nil,
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "cpu + memory/cpu + memory",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "cpu + memory/cpu",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(400, resource.DecimalSI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "cpu + memory/cpu, a<b",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(500, resource.DecimalSI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "cpu + memory/cpu, a<b",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(-400, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{},
		},
		{
			name: "cpu/cpu + memory",
			a: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := subReleaseListNoNegative(tt.a, tt.b)
			if !reflect.DeepEqual(res, tt.expected) {
				t.Errorf("got %v, want %v", res, tt.expected)
			}
		})
	}
}

func Test_AddReleased(t *testing.T) {
	tests := []struct {
		name     string
		a        ReleaseList
		b        ReleaseList
		expected ReleaseList
	}{
		{
			name: "nil a",
			a:    nil,
			b: ReleaseList{
				ReleaseTargetTypeResourceUsed: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
				},
			},
			expected: nil,
		},
		{
			name: "ReleaseTargetTypeResourceUsed: cpu + memory/cpu + memory",
			a: ReleaseList{
				ReleaseTargetTypeResourceUsed: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
			b: ReleaseList{
				ReleaseTargetTypeResourceUsed: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
				},
			},
			expected: ReleaseList{
				ReleaseTargetTypeResourceUsed: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(900, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1536*1024*1024, resource.BinarySI),
				},
			},
		},
		{
			name: "ReleaseTargetTypeBatchResourceRequest/ReleaseTargetTypeResourceUsed: cpu + memory/cpu + memory",
			a: ReleaseList{
				ReleaseTargetTypeBatchResourceRequest: {
					corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
			b: ReleaseList{
				ReleaseTargetTypeResourceUsed: {
					corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
				},
			},
			expected: ReleaseList{
				ReleaseTargetTypeBatchResourceRequest: {
					corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
				},
				ReleaseTargetTypeResourceUsed: {
					corev1.ResourceCPU:    *resource.NewMilliQuantity(400, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addReleased(tt.a, tt.b)
			if !reflect.DeepEqual(tt.a, tt.expected) {
				t.Errorf("got %v, want %v", tt.a, tt.expected)
			}
		})
	}
}

func Test_GetPodPriorityLabel(t *testing.T) {
	tests := []struct {
		name            string
		pod             *corev1.Pod
		defaultPriority int64
		expected        int64
	}{
		{
			name: "nil label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: nil,
				},
			},
			defaultPriority: defaultEvictPriority,
			expected:        defaultEvictPriority,
		},
		{
			name: "no that label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"xxx": "xxx",
					},
				},
			},
			defaultPriority: defaultEvictPriority,
			expected:        defaultEvictPriority,
		},
		{
			name: "invalid value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodPriority: "xxx",
					},
				},
			},
			defaultPriority: defaultEvictPriority,
			expected:        defaultEvictPriority,
		},
		{
			name: "invalid value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodPriority: "1200",
					},
				},
			},
			defaultPriority: defaultEvictPriority,
			expected:        1200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := GetPodPriorityLabel(tt.pod, defaultEvictPriority)
			assert.Equal(t, res, tt.expected)
		})
	}
}

func Test_EvictTaskCheck(t *testing.T) {
	tests := []struct {
		name        string
		task        *EvictTaskInfo
		releaseList ReleaseList
		expectRes   bool
		expectedRL  corev1.ResourceList
	}{
		{
			name:       "nil task",
			task:       nil,
			expectRes:  true,
			expectedRL: nil,
		},
		{
			name: "nil ToReleaseResource",
			task: &EvictTaskInfo{
				ToReleaseResource: nil,
			},
			expectRes:  true,
			expectedRL: nil,
		},
		{
			name: "len(ToReleaseResource)=0",
			task: &EvictTaskInfo{
				ToReleaseResource: corev1.ResourceList{},
			},
			expectRes:  true,
			expectedRL: nil,
		},
		{
			name: "nil released",
			task: &EvictTaskInfo{
				ToReleaseResource: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
				},
			},
			expectRes: false,
			expectedRL: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
			},
		},
		{
			name: "target not exist",
			task: &EvictTaskInfo{
				ReleaseTarget: ReleaseTargetTypeResourceUsed,
				ToReleaseResource: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
			releaseList: ReleaseList{
				ReleaseTargetTypeBatchResourceRequest: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
			expectRes: false,
			expectedRL: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "cpu + mem/cpu + mem: success",
			task: &EvictTaskInfo{
				ReleaseTarget: ReleaseTargetTypeResourceUsed,
				ToReleaseResource: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
			releaseList: ReleaseList{
				ReleaseTargetTypeResourceUsed: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
			expectRes:  true,
			expectedRL: nil,
		},
		{
			name: "cpu + mem/cpu + mem: failed",
			task: &EvictTaskInfo{
				ReleaseTarget: ReleaseTargetTypeResourceUsed,
				ToReleaseResource: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(800, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewMilliQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
			releaseList: ReleaseList{
				ReleaseTargetTypeResourceUsed: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewMilliQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
			expectRes: false,
			expectedRL: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(300, resource.DecimalSI),
			},
		},
		{
			name: "mem/cpu + mem: failed",
			task: &EvictTaskInfo{
				ReleaseTarget: ReleaseTargetTypeBatchResourceRequest,
				ToReleaseResource: corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewMilliQuantity(2048*1024*1024, resource.BinarySI),
				},
			},
			releaseList: ReleaseList{
				ReleaseTargetTypeBatchResourceRequest: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewMilliQuantity(1024*1024*1024, resource.BinarySI),
				},
			},
			expectRes: false,
			expectedRL: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewMilliQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, rl := EvictTaskCheck(tt.task, tt.releaseList)
			assert.Equal(t, tt.expectRes, res)
			assert.Equal(t, tt.expectedRL, rl)
		})
	}
}

func Test_KillAndEvictPods(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()
	executor := NewMockEvictionExecutor(ctl)
	executor.EXPECT().Evict(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(true)
	executor.EXPECT().IsPodEvicted(gomock.Any()).AnyTimes().Return(false)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	podInfo1 := &PodEvictInfo{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod1",
				Namespace: "default",
				UID:       "pod1",
			},
		},
		MilliCPUUsed:    100,
		MemoryUsed:      1024 * 1024 * 1,
		MilliCPURequest: 500,
		MemoryRequest:   1024 * 1024 * 3,
	}
	podInfo2 := &PodEvictInfo{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod2",
				Namespace: "default",
				UID:       "pod2",
			},
		},
		MilliCPUUsed:    200,
		MemoryUsed:      1024 * 1024 * 2,
		MilliCPURequest: 600,
		MemoryRequest:   1024 * 1024 * 4,
	}
	tasks := []*EvictTaskInfo{
		{
			Reason:            "test-reason0",
			ReleaseTarget:     ReleaseTargetTypeResourceUsed,
			ToReleaseResource: nil,
			SortedEvictPods:   nil,
		},
		{
			Reason:        "test-reason1",
			ReleaseTarget: ReleaseTargetTypeResourceUsed,
			ToReleaseResource: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
			},
			SortedEvictPods: []*PodEvictInfo{
				podInfo1,
				podInfo2,
			},
		},
		{
			Reason:        "test-reason2",
			ReleaseTarget: ReleaseTargetTypeBatchResourceRequest,
			ToReleaseResource: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*8, resource.BinarySI),
			},
			SortedEvictPods: []*PodEvictInfo{
				podInfo1,
				podInfo2,
			},
		},
	}
	res, success := KillAndEvictPods(executor, node, tasks)
	assert.Equal(t, true, success)
	assert.Equal(t, ReleaseList{
		ReleaseTargetTypeResourceUsed: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(300, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(1024*1024*3, resource.BinarySI),
		},
		ReleaseTargetTypeBatchResourceRequest: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(1100, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(1024*1024*7, resource.BinarySI),
		},
	}, res)
}
