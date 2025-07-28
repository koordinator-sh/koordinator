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

package loadaware

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware/estimator"
)

var fakeTimeNowFn = func() time.Time {
	t := time.Time{}
	_ = t.Add(100 * time.Second)
	return t
}

func TestPodAssignCache_OnAdd(t *testing.T) {
	tests := []struct {
		name      string
		pod       *corev1.Pod
		wantCache map[string]map[types.UID]*podAssignInfo
	}{
		{
			name:      "update pending pod",
			pod:       &corev1.Pod{},
			wantCache: map[string]map[types.UID]*podAssignInfo{},
		},
		{
			name: "update terminated pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodFailed,
				},
			},
			wantCache: map[string]map[types.UID]*podAssignInfo{},
		},
		{
			name: "update scheduled running pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			wantCache: map[string]map[types.UID]*podAssignInfo{
				"test-node": {
					"123456789": &podAssignInfo{
						pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								UID:       "123456789",
								Namespace: "default",
								Name:      "test",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						timestamp: fakeTimeNowFn(),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preTimeNowFn := timeNowFn
			defer func() {
				timeNowFn = preTimeNowFn
			}()
			timeNowFn = fakeTimeNowFn
			e, _ := estimator.NewDefaultEstimator(&config.LoadAwareSchedulingArgs{}, nil)
			assignCache := newPodAssignCache(e, &config.LoadAwareSchedulingArgs{})
			assignCache.OnAdd(tt.pod, true)
			assert.Equal(t, tt.wantCache, assignCache.podInfoItems)
		})
	}
}

func TestPodAssignCache_OnUpdate(t *testing.T) {
	tests := []struct {
		name        string
		pod         *corev1.Pod
		assignCache *podAssignCache
		wantCache   map[string]map[types.UID]*podAssignInfo
	}{
		{
			name:      "update pending pod",
			pod:       &corev1.Pod{},
			wantCache: map[string]map[types.UID]*podAssignInfo{},
		},
		{
			name: "update terminated pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodFailed,
				},
			},
			assignCache: &podAssignCache{
				podInfoItems: map[string]map[types.UID]*podAssignInfo{
					"test-node": {
						"123456789": &podAssignInfo{
							pod: &corev1.Pod{
								ObjectMeta: metav1.ObjectMeta{
									UID:       "123456789",
									Namespace: "default",
									Name:      "test",
								},
								Spec: corev1.PodSpec{
									NodeName: "test-node",
								},
								Status: corev1.PodStatus{
									Phase: corev1.PodRunning,
								},
							},
							timestamp: fakeTimeNowFn(),
						},
					},
				},
			},
			wantCache: map[string]map[types.UID]*podAssignInfo{},
		},
		{
			name: "update scheduled running pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			wantCache: map[string]map[types.UID]*podAssignInfo{
				"test-node": {
					"123456789": &podAssignInfo{
						pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								UID:       "123456789",
								Namespace: "default",
								Name:      "test",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						timestamp: fakeTimeNowFn(),
					},
				},
			},
		},
		{
			name: "update scheduled running pod, timestamp won't be updated",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			assignCache: &podAssignCache{
				podInfoItems: map[string]map[types.UID]*podAssignInfo{
					"test-node": {
						"123456789": &podAssignInfo{
							pod: &corev1.Pod{
								ObjectMeta: metav1.ObjectMeta{
									UID:       "123456789",
									Namespace: "default",
									Name:      "test",
								},
								Spec: corev1.PodSpec{
									NodeName: "test-node",
								},
								Status: corev1.PodStatus{
									Phase: corev1.PodRunning,
								},
							},
							timestamp: fakeTimeNowFn().Add(1000),
						},
					},
				},
			},
			wantCache: map[string]map[types.UID]*podAssignInfo{
				"test-node": {
					"123456789": &podAssignInfo{
						pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								UID:       "123456789",
								Namespace: "default",
								Name:      "test",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						timestamp: fakeTimeNowFn().Add(1000),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preTimeNowFn := timeNowFn
			defer func() {
				timeNowFn = preTimeNowFn
			}()
			timeNowFn = fakeTimeNowFn
			assignCache := tt.assignCache
			if assignCache == nil {
				e, _ := estimator.NewDefaultEstimator(&config.LoadAwareSchedulingArgs{}, nil)
				assignCache = newPodAssignCache(e, &config.LoadAwareSchedulingArgs{})
			}
			assignCache.OnUpdate(nil, tt.pod)
			assert.Equal(t, tt.wantCache, assignCache.podInfoItems)
		})
	}
}

func TestPodAssignCache_OnDelete(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "123456789",
			Namespace: "default",
			Name:      "test",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}
	assignCache := &podAssignCache{
		podInfoItems: map[string]map[types.UID]*podAssignInfo{
			"test-node": {
				"123456789": &podAssignInfo{
					pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							UID:       "123456789",
							Namespace: "default",
							Name:      "test",
						},
						Spec: corev1.PodSpec{
							NodeName: "test-node",
						},
						Status: corev1.PodStatus{
							Phase: corev1.PodRunning,
						},
					},
					timestamp: fakeTimeNowFn(),
				},
			},
		},
	}
	assignCache.OnDelete(pod)
	wantCache := map[string]map[types.UID]*podAssignInfo{}
	assert.Equal(t, wantCache, assignCache.podInfoItems)
}

func TestShouldEstimatePodDeadline(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name                              string
		estimatedSecondsAfterPodScheduled *int64
		estimatedSecondsAfterInitialized  *int64
		allowCustomizeEstimation          bool
		pod                               *corev1.Pod
		expected                          time.Time
	}{
		{
			name: "disabled",
		},
		{
			name:                              "enabled for pod scheduled",
			estimatedSecondsAfterPodScheduled: ptr.To((int64(180))),
			pod: schedulertesting.MakePod().Namespace("default").Name("pod").Conditions([]corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-time.Minute))},
			}).Obj(),
			expected: now.Add(2 * time.Minute),
		},
		{
			name:                              "disabled pod scheduled when pod initialized",
			estimatedSecondsAfterPodScheduled: ptr.To((int64(180))),
			estimatedSecondsAfterInitialized:  ptr.To((int64(10))),
			pod: schedulertesting.MakePod().Namespace("default").Name("pod").Conditions([]corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-time.Minute))},
				{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-30 * time.Second))},
			}).Obj(),
			expected: now.Add(-20 * time.Second),
		},
		{
			name:                             "enabled for pod initialized",
			estimatedSecondsAfterInitialized: ptr.To((int64(180))),
			pod: schedulertesting.MakePod().Namespace("default").Name("pod").Conditions([]corev1.PodCondition{
				{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-time.Minute))},
			}).Obj(),
			expected: now.Add(2 * time.Minute),
		},
		{
			name:                             "disabled for pod initialized when condition is not satisfied",
			estimatedSecondsAfterInitialized: ptr.To((int64(180))),
			pod: schedulertesting.MakePod().Namespace("default").Name("pod").Conditions([]corev1.PodCondition{
				{Type: corev1.PodInitialized, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(now.Add(-time.Minute))},
			}).Obj(),
		},
		{
			name:                     "after pod scheduled from metadata",
			allowCustomizeEstimation: true,
			pod: schedulertesting.MakePod().Namespace("default").
				Annotation(extension.AnnotationCustomEstimatedSecondsAfterPodScheduled, "180").
				Name("pod").Conditions([]corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-time.Minute))},
			}).Obj(),
			expected: now.Add(2 * time.Minute),
		},
		{
			name:                     "after initialized from metadata",
			allowCustomizeEstimation: true,
			pod: schedulertesting.MakePod().Namespace("default").
				Annotation(extension.AnnotationCustomEstimatedSecondsAfterInitialized, "180").
				Name("pod").Conditions([]corev1.PodCondition{
				{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-time.Minute))},
			}).Obj(),
			expected: now.Add(2 * time.Minute),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preTimeNowFn := timeNowFn
			defer func() {
				timeNowFn = preTimeNowFn
			}()
			timeNowFn = fakeTimeNowFn
			args := &config.LoadAwareSchedulingArgs{
				EstimatedSecondsAfterPodScheduled: tt.estimatedSecondsAfterPodScheduled,
				EstimatedSecondsAfterInitialized:  tt.estimatedSecondsAfterInitialized,
				AllowCustomizeEstimation:          tt.allowCustomizeEstimation,
			}
			e, _ := estimator.NewDefaultEstimator(args, nil)
			assignCache := newPodAssignCache(e, args)
			actual := assignCache.shouldEstimatePodDeadline(tt.pod, now.Add(-time.Minute))
			assert.Equal(t, tt.expected, actual)
		})
	}
}
