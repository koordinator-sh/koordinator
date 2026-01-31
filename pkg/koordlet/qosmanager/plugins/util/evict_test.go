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
	"k8s.io/utils/ptr"

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

func Test_isZeroResourceList(t *testing.T) {
	tests := []struct {
		name     string
		a        corev1.ResourceList
		expected bool
	}{
		{
			name:     "nil resource list",
			a:        nil,
			expected: true,
		},
		{
			name:     "empty resource list",
			a:        corev1.ResourceList{},
			expected: true,
		},
		{
			name: "all zero resources",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(0, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(0, resource.BinarySI),
			},
			expected: true,
		},
		{
			name: "cpu zero, memory non-zero",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(0, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			expected: false,
		},
		{
			name: "cpu non-zero, memory zero",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(0, resource.BinarySI),
			},
			expected: false,
		},
		{
			name: "all non-zero resources",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			expected: false,
		},
		{
			name: "negative cpu",
			a: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(-100, resource.DecimalSI),
			},
			expected: false,
		},
		{
			name: "single resource zero",
			a: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(0, resource.DecimalSI),
			},
			expected: true,
		},
		{
			name: "extended resource zero",
			a: corev1.ResourceList{
				apiext.BatchCPU:    *resource.NewQuantity(0, resource.DecimalSI),
				apiext.BatchMemory: *resource.NewQuantity(0, resource.BinarySI),
			},
			expected: true,
		},
		{
			name: "extended resource non-zero",
			a: corev1.ResourceList{
				apiext.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
				apiext.BatchMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := isZeroResourceList(tt.a)
			if res != tt.expected {
				t.Errorf("got %v, want %v", res, tt.expected)
			}
		})
	}
}

func Test_IsEvictionPolicyAllowed(t *testing.T) {
	tests := []struct {
		name     string
		policy   string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name:   "pod with nil annotations",
			policy: "evictByMemoryUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: nil,
				},
			},
			expected: true,
		},
		{
			name:   "pod without eviction policy annotation",
			policy: "evictByMemoryUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"fake-annotation": "value",
					},
				},
			},
			expected: true,
		},
		{
			name:   "pod with empty eviction policy annotation",
			policy: "evictByMemoryUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodEvictPolicy: "",
					},
				},
			},
			expected: false,
		},
		{
			name:   "policy allowed - single policy",
			policy: "evictByMemoryUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodEvictPolicy: `["evictByMemoryUsage"]`,
					},
				},
			},
			expected: true,
		},
		{
			name:   "policy allowed - multiple policies",
			policy: "evictByCPUUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodEvictPolicy: `["evictByMemoryUsage","evictByCPUUsage","evictByNodeMemoryUsage"]`,
					},
				},
			},
			expected: true,
		},
		{
			name:   "policy not allowed - single policy",
			policy: "evictByCPUUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodEvictPolicy: `["evictByMemoryUsage"]`,
					},
				},
			},
			expected: false,
		},
		{
			name:   "policy not allowed - multiple policies",
			policy: "evictByDiskUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodEvictPolicy: `["evictByMemoryUsage","evictByCPUUsage"]`,
					},
				},
			},
			expected: false,
		},
		{
			name:   "invalid json format",
			policy: "evictByMemoryUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodEvictPolicy: `invalid-json`,
					},
				},
			},
			expected: false,
		},
		{
			name:   "invalid json format - not an array",
			policy: "evictByMemoryUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodEvictPolicy: `"evictByMemoryUsage"`,
					},
				},
			},
			expected: false,
		},
		{
			name:   "empty policy list",
			policy: "evictByMemoryUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodEvictPolicy: `[]`,
					},
				},
			},
			expected: false,
		},
		{
			name:   "policy check with exact match",
			policy: "evictByMemoryUsage",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationPodEvictPolicy: `["evictByMemory","evictByMemoryUsageHigh"]`,
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := IsEvictionPolicyAllowed(tt.policy, tt.pod)
			if res != tt.expected {
				t.Errorf("IsEvictionPolicyAllowed() = %v, want %v", res, tt.expected)
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

func Test_GetRequestFromPod(t *testing.T) {
	tests := []struct {
		name         string
		pod          *corev1.Pod
		resourceName corev1.ResourceName
		expected     corev1.ResourceList
	}{
		{
			name: "batch pod - cpu request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					PriorityClassName: string(apiext.PriorityBatch),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU: *resource.NewQuantity(2000, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceCPU,
			expected: corev1.ResourceList{
				apiext.BatchCPU: *resource.NewQuantity(2000, resource.DecimalSI),
			},
		},
		{
			name: "batch pod - memory request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					PriorityClassName: string(apiext.PriorityBatch),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceMemory,
			expected: corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "batch pod - multiple containers cpu",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					PriorityClassName: string(apiext.PriorityBatch),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU: *resource.NewQuantity(1000, resource.DecimalSI),
								},
							},
						},
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU: *resource.NewQuantity(500, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceCPU,
			expected: corev1.ResourceList{
				apiext.BatchCPU: *resource.NewQuantity(1500, resource.DecimalSI),
			},
		},
		{
			name: "mid pod - cpu request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority:          ptr.To[int32](7500),
					PriorityClassName: string(apiext.PriorityMid),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.MidCPU: *resource.NewQuantity(3000, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceCPU,
			expected: corev1.ResourceList{
				apiext.MidCPU: *resource.NewQuantity(3000, resource.DecimalSI),
			},
		},
		{
			name: "mid pod - memory request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority:          ptr.To[int32](7500),
					PriorityClassName: string(apiext.PriorityMid),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.MidMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceMemory,
			expected: corev1.ResourceList{
				apiext.MidMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "prod pod - cpu request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					PriorityClassName: string(apiext.PriorityProd),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: *resource.NewQuantity(4, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceCPU,
			expected: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(4000, resource.DecimalSI),
			},
		},
		{
			name: "prod pod - memory request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					PriorityClassName: string(apiext.PriorityProd),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceMemory,
			expected: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "batch pod - zero cpu request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					PriorityClassName: string(apiext.PriorityBatch),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceCPU,
			expected: corev1.ResourceList{
				apiext.BatchCPU: *resource.NewQuantity(0, resource.DecimalSI),
			},
		},
		{
			name: "batch pod - negative cpu treated as zero",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					PriorityClassName: string(apiext.PriorityBatch),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU: *resource.NewQuantity(-100, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceCPU,
			expected: corev1.ResourceList{
				apiext.BatchCPU: *resource.NewQuantity(0, resource.DecimalSI),
			},
		},
		{
			name: "pod without priority class - cpu",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceCPU,
			expected: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
			},
		},
		{
			name: "pod without priority class - memory",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI),
								},
							},
						},
					},
				},
			},
			resourceName: corev1.ResourceMemory,
			expected: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetRequestFromPod(tt.pod, tt.resourceName)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("GetRequestFromPod() = %v, want %v", result, tt.expected)
			}
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
			Name: "test-node-2",
		},
	}

	batchPod1 := &PodEvictInfo{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "batch-pod-1",
				Namespace: "default",
				UID:       "batch-pod-1-uid",
				Labels: map[string]string{
					apiext.LabelPodQoS: string(apiext.QoSBE),
				},
			},
			Spec: corev1.PodSpec{
				PriorityClassName: string(apiext.PriorityBatch),
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								apiext.BatchCPU:    *resource.NewQuantity(1000, resource.DecimalSI),
								apiext.BatchMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
							},
						},
					},
				},
			},
		},
		MilliCPUUsed:    800,
		MemoryUsed:      1536 * 1024 * 1024,
		MilliCPURequest: 1000,
		MemoryRequest:   2 * 1024 * 1024 * 1024,
	}

	batchPod2 := &PodEvictInfo{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "batch-pod-2",
				Namespace: "default",
				UID:       "batch-pod-2-uid",
				Labels: map[string]string{
					apiext.LabelPodQoS: string(apiext.QoSBE),
				},
			},
			Spec: corev1.PodSpec{
				PriorityClassName: string(apiext.PriorityBatch),
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								apiext.BatchCPU:    *resource.NewQuantity(500, resource.DecimalSI),
								apiext.BatchMemory: *resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
							},
						},
					},
				},
			},
		},
		MilliCPUUsed:    400,
		MemoryUsed:      768 * 1024 * 1024,
		MilliCPURequest: 500,
		MemoryRequest:   1 * 1024 * 1024 * 1024,
	}

	midPod := &PodEvictInfo{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mid-pod-1",
				Namespace: "default",
				UID:       "mid-pod-1-uid",
				Labels: map[string]string{
					apiext.LabelPodQoS: string(apiext.QoSLS),
				},
			},
			Spec: corev1.PodSpec{
				Priority:          ptr.To[int32](7500),
				PriorityClassName: string(apiext.PriorityMid),
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								apiext.MidCPU:    *resource.NewQuantity(2000, resource.DecimalSI),
								apiext.MidMemory: *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI),
							},
						},
					},
				},
			},
		},
		MilliCPUUsed:    1800,
		MemoryUsed:      3 * 1024 * 1024 * 1024,
		MilliCPURequest: 2000,
		MemoryRequest:   4 * 1024 * 1024 * 1024,
	}
	prioritiesMp := map[apiext.PriorityClass]bool{
		apiext.PriorityMid:   true,
		apiext.PriorityBatch: true,
	}
	tasks := []*EvictTaskInfo{
		{
			Reason:        "evict-by-memory-usage",
			ReleaseTarget: ReleaseTargetTypeResourceUsed,
			ToReleaseResource: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			SortedEvictPods: []*PodEvictInfo{
				batchPod1,
				batchPod2,
			},
			GetPodResourceFunc: func(podInfo *PodEvictInfo) corev1.ResourceList {
				return corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(podInfo.MemoryUsed, resource.BinarySI),
				}
			},
		},
		{
			Reason:        "evict-by-mid/batch-request cpu",
			ReleaseTarget: ReleaseTargetTypeResourceRequest,
			ToReleaseResource: corev1.ResourceList{
				apiext.BatchCPU: *resource.NewMilliQuantity(1200, resource.DecimalSI),
				apiext.MidCPU:   *resource.NewMilliQuantity(1500, resource.DecimalSI),
			},
			SortedEvictPods: []*PodEvictInfo{
				batchPod1,
				batchPod2,
				midPod,
			},
			GetPodResourceFunc: func(podInfo *PodEvictInfo) corev1.ResourceList {
				if _, ok := prioritiesMp[apiext.GetPodPriorityClassWithDefault(podInfo.Pod)]; !ok {
					return nil
				}
				return GetRequestFromPod(podInfo.Pod, corev1.ResourceCPU)
			},
		},
	}

	res, success := KillAndEvictPods(executor, node, tasks)
	assert.Equal(t, true, success)

	assert.NotNil(t, res[ReleaseTargetTypeResourceUsed])
	if res[ReleaseTargetTypeResourceUsed] != nil {
		// evicted batch-pod-1 and batch-pod-2, added midPod(2000), release 1536MB + 768MB = 2304MB
		memoryReleased := res[ReleaseTargetTypeResourceUsed][corev1.ResourceMemory]
		assert.True(t, memoryReleased.Value() >= 2*1024*1024*1024, "Memory should be released at least 2Gi")
		assert.True(t, memoryReleased.Value() == 2304*1024*1024+3*1024*1024*1024)
	}

	assert.NotNil(t, res[ReleaseTargetTypeResourceRequest])
	assert.NotNil(t, res[ReleaseTargetTypeResourceRequest])
	if res[ReleaseTargetTypeResourceRequest] != nil {
		// evicted midPod(2000), added batch-pod-1 (1000), batch-pod-2 (500), sum
		cpuReleased := res[ReleaseTargetTypeResourceRequest][apiext.BatchCPU]
		assert.True(t, cpuReleased.Value() >= 1200, "BatchCPU should be released at least 1200m")
		assert.True(t, cpuReleased.Value() == 1500)
		cpuReleased = res[ReleaseTargetTypeResourceRequest][apiext.MidCPU]
		assert.True(t, cpuReleased.Value() >= 1500, "BatchCPU should be released at least 1200m")
		assert.True(t, cpuReleased.Value() == 2000)
	}
}

func Test_mergeResourceListByMax(t *testing.T) {
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
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "nil b",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			b: nil,
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "empty a",
			a:    corev1.ResourceList{},
			b: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(300, resource.DecimalSI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(300, resource.DecimalSI),
			},
		},
		{
			name: "empty b",
			a: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(300, resource.DecimalSI),
			},
			b: corev1.ResourceList{},
			expected: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(300, resource.DecimalSI),
			},
		},
		{
			name: "a > b for all resources",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "b > a for all resources",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "mixed: a_cpu > b_cpu, b_memory > a_memory",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(800, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(3*1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(3*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "b has resource not in a",
			a: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "a has resource not in b",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(1500, resource.DecimalSI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "equal resources",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "extended resources - batch cpu and memory",
			a: corev1.ResourceList{
				apiext.BatchCPU:    *resource.NewQuantity(2000, resource.DecimalSI),
				apiext.BatchMemory: *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				apiext.BatchCPU:    *resource.NewQuantity(3000, resource.DecimalSI),
				apiext.BatchMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				apiext.BatchCPU:    *resource.NewQuantity(3000, resource.DecimalSI),
				apiext.BatchMemory: *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "mixed standard and extended resources",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
				apiext.BatchCPU:       *resource.NewQuantity(1500, resource.DecimalSI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
				apiext.MidMemory:      *resource.NewQuantity(3*1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(1500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
				apiext.BatchCPU:       *resource.NewQuantity(1500, resource.DecimalSI),
				apiext.MidMemory:      *resource.NewQuantity(3*1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "zero value in a, non-zero in b",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(0, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "negative value in a, positive in b",
			a: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(-100, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
			b: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			expected: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy of 'a' to verify the function modifies it in-place
			original := tt.a
			if original == nil {
				original = make(corev1.ResourceList)
			}
			res := mergeResourceListByMax(original, tt.b)

			// Verify all expected resources are present with correct values
			for resName, expectedQty := range tt.expected {
				actualQty, exists := res[resName]
				assert.True(t, exists, "resource %s should exist in result", resName)
				if exists {
					assert.Equal(t, expectedQty.Value(), actualQty.Value(), "resource %s value mismatch", resName)
				}
			}

			// Verify no unexpected resources are present
			assert.Equal(t, len(tt.expected), len(res), "result should have same number of resources as expected")
		})
	}
}
