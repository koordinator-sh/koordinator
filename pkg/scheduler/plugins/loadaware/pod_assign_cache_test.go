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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware/estimator"
)

var fakeTimeNowFn = func() time.Time {
	t := time.Time{}
	_ = t.Add(100 * time.Second)
	return t
}

func TestPodAssignCache_OnAdd(t *testing.T) {
	vectorizer := NewResourceVectorizer(corev1.ResourceCPU, corev1.ResourceMemory)
	node := "test-node"
	m := &slov1alpha1.NodeMetric{ObjectMeta: metav1.ObjectMeta{Name: node}}
	tests := []struct {
		name string
		pod  *corev1.Pod
		want func(*testing.T, *nodeInfo)
	}{
		{
			name: "add pending pod",
			pod:  schedulertesting.MakePod().Obj(),
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, 0, len(n.podInfos))
			},
		},
		{
			name: "add terminated pod",
			pod:  schedulertesting.MakePod().Node(node).Phase(corev1.PodFailed).Obj(),
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, 0, len(n.podInfos))
			},
		},
		{
			name: "add scheduled running pod without resources",
			pod:  schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).Obj(),
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, map[types.UID]*podAssignInfo{
					"123456789": {
						pod:       schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).Obj(),
						timestamp: fakeTimeNowFn(),
						estimated: vectorizer.ToFactorVec(map[corev1.ResourceName]int64{
							corev1.ResourceCPU:    estimator.DefaultMilliCPURequest,
							corev1.ResourceMemory: estimator.DefaultMemoryRequest,
						}),
					},
				}, n.podInfos)
				v := vectorizer.ToFactorVec(map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    estimator.DefaultMilliCPURequest,
					corev1.ResourceMemory: estimator.DefaultMemoryRequest,
				})
				assert.Equal(t, v, n.nodeDelta)
				assert.Equal(t, v, n.nodeEstimated)
				s := sets.New(NamespacedName{Namespace: "default", Name: "test"})
				assert.Equal(t, s, n.nodeDeltaPods)
				assert.Equal(t, s, n.nodeEstimatedPods)
			},
		},
		{
			name: "add prod pod",
			pod: schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).
				Req(map[corev1.ResourceName]string{corev1.ResourceCPU: "1", corev1.ResourceMemory: "4Gi"}).Obj(),
			want: func(t *testing.T, n *nodeInfo) {
				v := vectorizer.ToVec(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				})
				assert.Equal(t, v, n.nodeDelta)
				assert.Equal(t, v, n.prodDelta)
				assert.Equal(t, v, n.nodeEstimated)
				s := sets.New(NamespacedName{Namespace: "default", Name: "test"})
				assert.Equal(t, s, n.nodeDeltaPods)
				assert.Equal(t, s, n.prodDeltaPods)
				assert.Equal(t, s, n.nodeEstimatedPods)
			},
		},
		{
			name: "add non prod pod",
			pod: schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Priority(extension.PriorityMidValueDefault).
				Req(map[corev1.ResourceName]string{extension.MidCPU: "1k", extension.MidMemory: "4Gi"}).Obj(),
			want: func(t *testing.T, n *nodeInfo) {
				v := vectorizer.ToVec(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				})
				assert.Equal(t, v, n.nodeDelta)
				assert.Equal(t, v, n.nodeEstimated)
				s := sets.New(NamespacedName{Namespace: "default", Name: "test"})
				assert.Equal(t, s, n.nodeDeltaPods)
				assert.Equal(t, s, n.nodeEstimatedPods)
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
			e, _ := estimator.NewDefaultEstimator(&config.LoadAwareSchedulingArgs{EstimatedScalingFactors: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    100,
				corev1.ResourceMemory: 100,
			}}, nil)
			assignCache := newPodAssignCache(e, vectorizer, &config.LoadAwareSchedulingArgs{})
			assignCache.AddOrUpdateNodeMetric(m)
			assignCache.OnAdd(tt.pod, true)
			actual, _ := assignCache.getNodeInfo(node)
			tt.want(t, actual)
		})
	}
}

func TestPodAssignCache_OnUpdate(t *testing.T) {
	vectorizer := NewResourceVectorizer(corev1.ResourceCPU, corev1.ResourceMemory)
	node := "test-node"
	m := &slov1alpha1.NodeMetric{ObjectMeta: metav1.ObjectMeta{Name: node}}
	tests := []struct {
		name         string
		pod          *corev1.Pod
		existingPods []*corev1.Pod
		want         func(*testing.T, *nodeInfo)
	}{
		{
			name: "update pending pod",
			pod:  schedulertesting.MakePod().Obj(),
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, 0, len(n.podInfos))
			},
		},
		{
			name: "update terminated pod",
			pod:  schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodFailed).Obj(),
			existingPods: []*corev1.Pod{
				schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).Obj(),
			},
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, 0, len(n.podInfos))
			},
		},
		{
			name: "update scheduled running pod without resources",
			pod:  schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).Obj(),
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, map[types.UID]*podAssignInfo{
					"123456789": &podAssignInfo{
						pod:       schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).Obj(),
						timestamp: fakeTimeNowFn(),
						estimated: vectorizer.ToFactorVec(map[corev1.ResourceName]int64{
							corev1.ResourceCPU:    estimator.DefaultMilliCPURequest,
							corev1.ResourceMemory: estimator.DefaultMemoryRequest,
						}),
					},
				}, n.podInfos)
				v := vectorizer.ToFactorVec(map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    estimator.DefaultMilliCPURequest,
					corev1.ResourceMemory: estimator.DefaultMemoryRequest,
				})
				assert.Equal(t, v, n.nodeDelta)
				assert.Equal(t, v, n.nodeEstimated)
				s := sets.New(NamespacedName{Namespace: "default", Name: "test"})
				assert.Equal(t, s, n.nodeDeltaPods)
				assert.Equal(t, s, n.nodeEstimatedPods)
			},
		},
		{
			name: "update pod metadata only, cache won't be updated",
			pod: schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).
				Annotation("foo", "bar").Label("foo", "bar").Obj(),
			existingPods: []*corev1.Pod{
				schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).Obj(),
			},
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, map[types.UID]*podAssignInfo{
					"123456789": &podAssignInfo{
						pod:       schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).Obj(),
						timestamp: fakeTimeNowFn(),
						estimated: vectorizer.ToFactorVec(map[corev1.ResourceName]int64{
							corev1.ResourceCPU:    estimator.DefaultMilliCPURequest,
							corev1.ResourceMemory: estimator.DefaultMemoryRequest,
						}),
					},
				}, n.podInfos)
			},
		},
		{
			name: "update pod conditions, cache will be updated",
			pod: schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).
				Conditions([]corev1.PodCondition{
					{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(fakeTimeNowFn().Add(1000))},
					{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(fakeTimeNowFn().Add(3000))},
				}).Obj(),
			existingPods: []*corev1.Pod{
				schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).
					Conditions([]corev1.PodCondition{
						{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(fakeTimeNowFn().Add(1000))},
					}).Obj(),
			},
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, map[types.UID]*podAssignInfo{
					"123456789": &podAssignInfo{
						pod: schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).
							Conditions([]corev1.PodCondition{
								{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(fakeTimeNowFn().Add(1000))},
								{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(fakeTimeNowFn().Add(3000))},
							}).Obj(),
						timestamp: fakeTimeNowFn().Add(1000),
						estimated: vectorizer.ToFactorVec(map[corev1.ResourceName]int64{
							corev1.ResourceCPU:    estimator.DefaultMilliCPURequest,
							corev1.ResourceMemory: estimator.DefaultMemoryRequest,
						}),
					},
				}, n.podInfos)
				v := vectorizer.ToFactorVec(map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    estimator.DefaultMilliCPURequest,
					corev1.ResourceMemory: estimator.DefaultMemoryRequest,
				})
				assert.Equal(t, v, n.nodeDelta)
				assert.Equal(t, v, n.nodeEstimated)
				s := sets.New(NamespacedName{Namespace: "default", Name: "test"})
				assert.Equal(t, s, n.nodeDeltaPods)
				assert.Equal(t, s, n.nodeEstimatedPods)
			},
		},
		{
			name: "update pod resources, cache will be updated",
			pod: schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).
				Req(map[corev1.ResourceName]string{corev1.ResourceCPU: "1", corev1.ResourceMemory: "4Gi"}).Obj(),

			existingPods: []*corev1.Pod{
				schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).
					Conditions([]corev1.PodCondition{
						{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(fakeTimeNowFn().Add(1000))},
					}).Obj(),
			},
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, map[types.UID]*podAssignInfo{
					"123456789": &podAssignInfo{
						pod: schedulertesting.MakePod().UID("123456789").Namespace("default").Name("test").Node(node).Phase(corev1.PodRunning).
							Req(map[corev1.ResourceName]string{corev1.ResourceCPU: "1", corev1.ResourceMemory: "4Gi"}).Obj(),
						timestamp: fakeTimeNowFn(),
						estimated: vectorizer.ToVec(corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						}),
					},
				}, n.podInfos)
				v := vectorizer.ToVec(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				})
				assert.Equal(t, v, n.nodeDelta)
				assert.Equal(t, v, n.prodDelta)
				assert.Equal(t, v, n.nodeEstimated)
				s := sets.New(NamespacedName{Namespace: "default", Name: "test"})
				assert.Equal(t, s, n.nodeDeltaPods)
				assert.Equal(t, s, n.prodDeltaPods)
				assert.Equal(t, s, n.nodeEstimatedPods)
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
			e, _ := estimator.NewDefaultEstimator(&config.LoadAwareSchedulingArgs{EstimatedScalingFactors: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    100,
				corev1.ResourceMemory: 100,
			}}, nil)
			assignCache := newPodAssignCache(e, vectorizer, &config.LoadAwareSchedulingArgs{})
			assignCache.AddOrUpdateNodeMetric(m)
			for _, pod := range tt.existingPods {
				assignCache.OnAdd(pod, true)
			}
			assignCache.OnUpdate(nil, tt.pod)
			actual, _ := assignCache.getNodeInfo(node)
			tt.want(t, actual)
		})
	}
}

func TestPodAssignCache_OnDelete(t *testing.T) {
	vectorizer := NewResourceVectorizer(corev1.ResourceCPU, corev1.ResourceMemory)
	node := "test-node"
	m := &slov1alpha1.NodeMetric{
		ObjectMeta: metav1.ObjectMeta{Name: node},
		Status: slov1alpha1.NodeMetricStatus{
			NodeMetric: &slov1alpha1.NodeMetricInfo{
				NodeUsage: slov1alpha1.ResourceMap{
					ResourceList: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("70"),
						corev1.ResourceMemory: resource.MustParse("280Gi"),
					},
				},
			},
			PodsMetric: []*slov1alpha1.PodMetricInfo{
				{
					Namespace: "default", Name: "prod-0", Priority: extension.PriorityProd,
					PodUsage: slov1alpha1.ResourceMap{
						ResourceList: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("50"),
							corev1.ResourceMemory: resource.MustParse("200Gi"),
						},
					},
				},
				{
					Namespace: "default", Name: "prod-1", Priority: extension.PriorityProd,
					PodUsage: slov1alpha1.ResourceMap{
						ResourceList: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("6Gi"),
						},
					},
				},
				{
					Namespace: "default", Name: "prod-2", Priority: extension.PriorityProd,
					PodUsage: slov1alpha1.ResourceMap{
						ResourceList: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
				{
					Namespace: "default", Name: "prod-3", Priority: extension.PriorityMid,
					PodUsage: slov1alpha1.ResourceMap{
						ResourceList: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("3"),
							corev1.ResourceMemory: resource.MustParse("6Gi"),
						},
					},
				},
				{
					Namespace: "default", Name: "mid-1", Priority: extension.PriorityMid,
					PodUsage: slov1alpha1.ResourceMap{
						ResourceList: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("6Gi"),
						},
					},
				},
				{
					Namespace: "default", Name: "mid-2", Priority: extension.PriorityMid,
					PodUsage: slov1alpha1.ResourceMap{
						ResourceList: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
				{
					Namespace: "default", Name: "mid-3", Priority: extension.PriorityProd,
					PodUsage: slov1alpha1.ResourceMap{
						ResourceList: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("3"),
							corev1.ResourceMemory: resource.MustParse("6Gi"),
						},
					},
				},
			},
		},
	}
	e, _ := estimator.NewDefaultEstimator(&config.LoadAwareSchedulingArgs{EstimatedScalingFactors: map[corev1.ResourceName]int64{
		corev1.ResourceCPU:    50,
		corev1.ResourceMemory: 50,
	}}, nil)
	assignCache := newPodAssignCache(e, vectorizer, &config.LoadAwareSchedulingArgs{})
	assignCache.AddOrUpdateNodeMetric(m)
	assignCache.OnAdd(schedulertesting.MakePod().UID("1").Namespace("default").Name("prod-1").Node(node).Phase(corev1.PodRunning).
		Req(map[corev1.ResourceName]string{corev1.ResourceCPU: "4", corev1.ResourceMemory: "8Gi"}).Obj(), false)
	assignCache.OnAdd(schedulertesting.MakePod().UID("2").Namespace("default").Name("prod-2").Node(node).Phase(corev1.PodRunning).
		Req(map[corev1.ResourceName]string{corev1.ResourceCPU: "4", corev1.ResourceMemory: "8Gi"}).Obj(), false)
	assignCache.OnAdd(schedulertesting.MakePod().UID("3").Namespace("default").Name("prod-3").Node(node).Phase(corev1.PodRunning).
		Req(map[corev1.ResourceName]string{corev1.ResourceCPU: "4", corev1.ResourceMemory: "8Gi"}).Obj(), false)
	assignCache.OnAdd(schedulertesting.MakePod().UID("4").Namespace("default").Name("mid-1").Node(node).Phase(corev1.PodRunning).Priority(extension.PriorityMidValueDefault).
		Req(map[corev1.ResourceName]string{extension.MidCPU: "4k", extension.MidMemory: "8Gi"}).Obj(), false)
	assignCache.OnAdd(schedulertesting.MakePod().UID("5").Namespace("default").Name("mid-2").Node(node).Phase(corev1.PodRunning).Priority(extension.PriorityMidValueDefault).
		Req(map[corev1.ResourceName]string{extension.MidCPU: "4k", extension.MidMemory: "8Gi"}).Obj(), false)
	assignCache.OnAdd(schedulertesting.MakePod().UID("6").Namespace("default").Name("mid-3").Node(node).Phase(corev1.PodRunning).Priority(extension.PriorityMidValueDefault).
		Req(map[corev1.ResourceName]string{extension.MidCPU: "4k", extension.MidMemory: "8Gi"}).Obj(), false)
	actual, _ := assignCache.getNodeInfo(node)
	assert.Equal(t, vectorizer.ToVec(corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("5"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}), actual.prodUsage)
	assert.Equal(t, vectorizer.ToVec(corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("2"),
		corev1.ResourceMemory: resource.MustParse("4Gi"),
	}), actual.nodeDelta)
	assert.Equal(t, vectorizer.ToVec(corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("3"),
		corev1.ResourceMemory: resource.MustParse("6Gi"),
	}), actual.prodDelta)
	assert.Equal(t, vectorizer.ToVec(corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("12"),
		corev1.ResourceMemory: resource.MustParse("24Gi"),
	}), actual.nodeEstimated)
	assert.Equal(t, sets.New(
		NamespacedName{Namespace: "default", Name: "prod-1"}, NamespacedName{Namespace: "default", Name: "prod-2"},
		NamespacedName{Namespace: "default", Name: "mid-1"}, NamespacedName{Namespace: "default", Name: "mid-2"},
	), actual.nodeDeltaPods)
	assert.Equal(t, sets.New(
		NamespacedName{Namespace: "default", Name: "prod-1"}, NamespacedName{Namespace: "default", Name: "prod-2"},
		NamespacedName{Namespace: "default", Name: "prod-3"},
	), actual.prodDeltaPods)
	assert.Equal(t, sets.New(
		NamespacedName{Namespace: "default", Name: "prod-1"}, NamespacedName{Namespace: "default", Name: "prod-2"},
		NamespacedName{Namespace: "default", Name: "prod-3"}, NamespacedName{Namespace: "default", Name: "mid-1"},
		NamespacedName{Namespace: "default", Name: "mid-2"}, NamespacedName{Namespace: "default", Name: "mid-3"},
	), actual.nodeEstimatedPods)

	assignCache.OnDelete(schedulertesting.MakePod().UID("1").Namespace("default").Name("prod-1").Node(node).Phase(corev1.PodFailed).
		Req(map[corev1.ResourceName]string{corev1.ResourceCPU: "2", corev1.ResourceMemory: "8Gi"}).Obj())
	assignCache.OnDelete(schedulertesting.MakePod().UID("2").Namespace("default").Name("prod-2").Node(node).Obj())
	assignCache.OnDelete(schedulertesting.MakePod().UID("3").Namespace("default").Name("prod-3").Node(node).Obj())
	assignCache.OnDelete(schedulertesting.MakePod().UID("4").Namespace("default").Name("mid-1").Node(node).Obj())
	assignCache.OnDelete(schedulertesting.MakePod().UID("5").Namespace("default").Name("mid-2").Node(node).Obj())
	assignCache.OnDelete(schedulertesting.MakePod().UID("6").Namespace("default").Name("mid-3").Node(node).Obj())
	actual, _ = assignCache.getNodeInfo(node)
	assert.Equal(t, 0, len(actual.podInfos))
	assert.Equal(t, vectorizer.EmptyVec(), actual.nodeDelta)
	assert.Equal(t, vectorizer.EmptyVec(), actual.prodDelta)
	assert.Equal(t, vectorizer.EmptyVec(), actual.nodeEstimated)
	s := sets.New[NamespacedName]()
	assert.Equal(t, s, actual.nodeDeltaPods)
	assert.Equal(t, s, actual.prodDeltaPods)
	assert.Equal(t, s, actual.nodeEstimatedPods)
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
			assignCache := newPodAssignCache(e, NewResourceVectorizer(corev1.ResourceCPU, corev1.ResourceMemory), args)
			actual := assignCache.shouldEstimatePodDeadline(tt.pod, now.Add(-time.Minute))
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestNodeMetric(t *testing.T) {
	vectorizer := NewResourceVectorizer(corev1.ResourceCPU, corev1.ResourceMemory)
	node := "test-node"
	now := metav1.Now().Rfc3339Copy()
	tests := []struct {
		name         string
		args         *config.LoadAwareSchedulingArgs
		nodeMetric   *slov1alpha1.NodeMetric
		existingPods []*corev1.Pod
		want         func(*testing.T, *nodeInfo)
	}{
		{
			name: "disable estimator",
			args: &config.LoadAwareSchedulingArgs{},
			nodeMetric: &slov1alpha1.NodeMetric{
				Status: slov1alpha1.NodeMetricStatus{
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("72"),
								corev1.ResourceMemory: resource.MustParse("280Gi"),
							},
						},
					},
					PodsMetric: []*slov1alpha1.PodMetricInfo{
						nil,
						{Name: "invalid"},
						{
							Namespace: "default", Name: "prod-1", Priority: extension.PriorityProd,
							PodUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50"),
									corev1.ResourceMemory: resource.MustParse("200Gi"),
								},
							},
						},
						{
							Namespace: "default", Name: "mid-1", Priority: extension.PriorityMid,
							PodUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("20"),
									corev1.ResourceMemory: resource.MustParse("75Gi"),
								},
							},
						},
					},
				},
			},
			existingPods: []*corev1.Pod{
				schedulertesting.MakePod().Namespace("default").UID("1").Name("prod-1").Node(node).Phase(corev1.PodRunning).
					Req(map[corev1.ResourceName]string{corev1.ResourceCPU: "40", corev1.ResourceMemory: "160Gi"}).Obj(),
				schedulertesting.MakePod().Namespace("default").UID("2").Name("mid-1").Node(node).Phase(corev1.PodRunning).Priority(extension.PriorityMidValueDefault).
					Req(map[corev1.ResourceName]string{extension.MidCPU: "4k", extension.MidMemory: "8Gi"}).Obj(),
			},
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, vectorizer.ToVec(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50"),
					corev1.ResourceMemory: resource.MustParse("200Gi"),
				}), n.prodUsage)
			},
		},
		{
			name: "enable prod usage include sys",
			args: &config.LoadAwareSchedulingArgs{ProdUsageIncludeSys: true},
			nodeMetric: &slov1alpha1.NodeMetric{
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: ptr.To[int64](180),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &now,
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2"),
								corev1.ResourceMemory: resource.MustParse("4Gi"),
							},
						},
						SystemUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2"),
								corev1.ResourceMemory: resource.MustParse("4Gi"),
							},
						},
						AggregatedNodeUsages: []slov1alpha1.AggregatedUsage{
							{
								Duration: metav1.Duration{Duration: time.Minute},
								Usage: map[extension.AggregationType]slov1alpha1.ResourceMap{
									extension.AVG: {
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
									},
									extension.P90: {
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
									},
								},
							},
							{
								Duration: metav1.Duration{Duration: time.Hour},
								Usage: map[extension.AggregationType]slov1alpha1.ResourceMap{
									extension.AVG: {
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("500m"),
											corev1.ResourceMemory: resource.MustParse("1Gi"),
										},
									},
									extension.P90: {
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1500m"),
											corev1.ResourceMemory: resource.MustParse("3Gi"),
										},
									},
								},
							},
						},
					},
				},
			},
			want: func(t *testing.T, n *nodeInfo) {
				assert.Equal(t, now.Time, n.updateTime)
				assert.Equal(t, 180*time.Second, n.reportInterval)
				assert.Equal(t, vectorizer.ToVec(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}), n.nodeUsage)
				assert.Equal(t, vectorizer.ToVec(corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}), n.prodUsage)
				assert.Equal(t, map[aggUsageKey]ResourceVector{
					{Type: extension.AVG}: vectorizer.ToVec(corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					}),
					{Type: extension.P90}: vectorizer.ToVec(corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1500m"),
						corev1.ResourceMemory: resource.MustParse("3Gi"),
					}),
					{Type: extension.AVG, Duration: time.Minute}: vectorizer.ToVec(corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					}),
					{Type: extension.P90, Duration: time.Minute}: vectorizer.ToVec(corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					}),
					{Type: extension.AVG, Duration: time.Hour}: vectorizer.ToVec(corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					}),
					{Type: extension.P90, Duration: time.Hour}: vectorizer.ToVec(corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1500m"),
						corev1.ResourceMemory: resource.MustParse("3Gi"),
					}),
				}, n.aggUsages)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.nodeMetric.Name = node
			preTimeNowFn := timeNowFn
			defer func() {
				timeNowFn = preTimeNowFn
			}()
			timeNowFn = fakeTimeNowFn
			if tt.args == nil {
				tt.args = &config.LoadAwareSchedulingArgs{EstimatedScalingFactors: map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    100,
					corev1.ResourceMemory: 100,
				}}
			}
			e, _ := estimator.NewDefaultEstimator(tt.args, nil)
			assignCache := newPodAssignCache(e, vectorizer, tt.args)
			for _, pod := range tt.existingPods {
				assignCache.OnAdd(pod, true)
			}
			// test add node metrics
			assignCache.AddOrUpdateNodeMetric(tt.nodeMetric)
			actual, _ := assignCache.getNodeInfo(node)
			tt.want(t, actual)
			// test delete all pods
			for _, pod := range tt.existingPods {
				assignCache.OnDelete(pod)
			}
			// test delete node metrics
			assignCache.DeleteNodeMetric(node)
			_, ok := assignCache.getNodeInfo(node)
			assert.Equal(t, false, ok)
			_, _, _, err := assignCache.GetNodeMetricAndEstimatedOfExisting(node, false, metav1.Duration{}, "", false)
			assert.True(t, errors.IsNotFound(err))
		})
	}
}
