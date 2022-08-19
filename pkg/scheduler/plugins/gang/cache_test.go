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

package gang

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/apis/scheduling/config"
)

func TestGangCache_OnPodAdd(t *testing.T) {
	tests := []struct {
		name      string
		pods      []*corev1.Pod
		wantCache map[string]*Gang
	}{
		{
			name:      "update invalid pod",
			pods:      []*corev1.Pod{{}},
			wantCache: map[string]*Gang{},
		},
		{
			name: "update invalid pod2",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Labels:      map[string]string{"test": "gang"},
						Annotations: map[string]string{"test": "gang"},
					},
				},
			},
			wantCache: map[string]*Gang{},
		},
		{
			name: "update pod announcing Gang in CRD way before CRD created,gang should be created but not initialized",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "crdPod",
						Namespace: "default",
						Labels:    map[string]string{v1alpha1.PodGroupLabel: "test"},
					},
				},
			},
			wantCache: map[string]*Gang{
				"default/test": {
					Name:               "default/test",
					CreateTime:         fakeTimeNowFn(),
					WaitTime:           0,
					Mode:               extension.GangModeStrict,
					ScheduleCycleValid: true,
					ScheduleCycle:      1,
					GangFrom:           GangFromPodAnnotation,
					HasGangInit:        false,
					Children: map[string]*corev1.Pod{
						"default/crdPod": {
							ObjectMeta: metav1.ObjectMeta{
								Name:      "crdPod",
								Namespace: "default",
								Labels:    map[string]string{v1alpha1.PodGroupLabel: "test"},
							},
						},
					},
					WaitingForBindChildren: map[string]*corev1.Pod{},
					BoundChildren:          map[string]*corev1.Pod{},
					ChildrenScheduleRoundMap: map[string]int{
						"default/crdPod": 0,
					},
				},
			},
		},
		{
			name: "update pod announcing Gang in Annotation way",
			pods: []*corev1.Pod{
				// pod1 announce GangA
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod1",
						Annotations: map[string]string{
							extension.AnnotationGangName:     "gangA",
							extension.AnnotationGangMinNum:   "2",
							extension.AnnotationGangWaitTime: "300s",
							extension.AnnotationGangMode:     extension.GangModeNonStrict,
							extension.AnnotationGangGroups:   "[\"default/gangA\",\"default/gangB\"]",
						},
					},
				},
				// pod2 also announce GangA but with different annotations after pod1's announcing
				// so gangA in cache should only be created with pod1's Annotations
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod2",
						Annotations: map[string]string{
							extension.AnnotationGangName:     "gangA",
							extension.AnnotationGangMinNum:   "7",
							extension.AnnotationGangWaitTime: "3000s",
							extension.AnnotationGangGroups:   "[\"default/gangC\",\"default/gangD\"]",
						},
					},
				},
			},
			wantCache: map[string]*Gang{
				"default/gangA": {
					Name:              "default/gangA",
					WaitTime:          300 * time.Second,
					CreateTime:        fakeTimeNowFn(),
					Mode:              extension.GangModeNonStrict,
					MinRequiredNumber: 2,
					TotalChildrenNum:  2,
					GangGroup:         []string{"default/gangA", "default/gangB"},
					HasGangInit:       true,
					GangFrom:          GangFromPodAnnotation,
					Children: map[string]*corev1.Pod{
						"default/pod1": {
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "pod1",
								Annotations: map[string]string{
									extension.AnnotationGangName:     "gangA",
									extension.AnnotationGangMinNum:   "2",
									extension.AnnotationGangWaitTime: "300s",
									extension.AnnotationGangMode:     extension.GangModeNonStrict,
									extension.AnnotationGangGroups:   "[\"default/gangA\",\"default/gangB\"]",
								},
							},
						},
						"default/pod2": {
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "pod2",
								Annotations: map[string]string{
									extension.AnnotationGangName:     "gangA",
									extension.AnnotationGangMinNum:   "7",
									extension.AnnotationGangWaitTime: "3000s",
									extension.AnnotationGangGroups:   "[\"default/gangC\",\"default/gangD\"]",
								},
							},
						},
					},
					WaitingForBindChildren: map[string]*corev1.Pod{},
					BoundChildren:          map[string]*corev1.Pod{},
					ScheduleCycleValid:     true,
					ScheduleCycle:          1,
					ChildrenScheduleRoundMap: map[string]int{
						"default/pod1": 0,
						"default/pod2": 0,
					},
				},
			},
		},
		{
			name: "update pods announcing Gang in Annotation way,but with illegal args",
			pods: []*corev1.Pod{
				// pod1 announce GangA with illegal minNum,
				// so that gangA's info depends on the next pod's Annotations
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod3",
						Annotations: map[string]string{
							extension.AnnotationGangName:   "gangA",
							extension.AnnotationGangMinNum: "xxx",
						},
					},
				},
				// pod4 also announce GangA but with legal minNum,illegal remaining args
				// so gangA in cache should only be created with pod4's Annotations(illegal args set by default)
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod4",
						Annotations: map[string]string{
							extension.AnnotationGangName:     "gangA",
							extension.AnnotationGangMinNum:   "2",
							extension.AnnotationGangTotalNum: "1",
							extension.AnnotationGangMode:     "WenShiqi222",
							extension.AnnotationGangWaitTime: "WenShiqi222",
							extension.AnnotationGangGroups:   "gangA,gangX",
						},
					},
				},
			},
			wantCache: map[string]*Gang{
				"default/gangA": {
					Name:              "default/gangA",
					WaitTime:          0,
					CreateTime:        fakeTimeNowFn(),
					Mode:              extension.GangModeStrict,
					MinRequiredNumber: 2,
					TotalChildrenNum:  2,
					GangGroup:         []string{},
					HasGangInit:       true,
					GangFrom:          GangFromPodAnnotation,
					Children: map[string]*corev1.Pod{
						"default/pod3": {
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "pod3",
								Annotations: map[string]string{
									extension.AnnotationGangName:   "gangA",
									extension.AnnotationGangMinNum: "xxx",
								},
							},
						},
						"default/pod4": {
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "pod4",
								Annotations: map[string]string{
									extension.AnnotationGangName:     "gangA",
									extension.AnnotationGangMinNum:   "2",
									extension.AnnotationGangTotalNum: "1",
									extension.AnnotationGangMode:     "WenShiqi222",
									extension.AnnotationGangWaitTime: "WenShiqi222",
									extension.AnnotationGangGroups:   "gangA,gangX",
								},
							},
						},
					},
					WaitingForBindChildren: map[string]*corev1.Pod{},
					BoundChildren:          map[string]*corev1.Pod{},
					ScheduleCycleValid:     true,
					ScheduleCycle:          1,
					ChildrenScheduleRoundMap: map[string]int{
						"default/pod3": 0,
						"default/pod4": 0,
					},
				},
			},
		},
		{
			name: "update pods announcing Gang in Annotation way,but with illegal args",
			pods: []*corev1.Pod{
				// pod1 announce GangA with illegal AnnotationGangWaitTime,
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod1",
						Annotations: map[string]string{
							extension.AnnotationGangName:     "gangA",
							extension.AnnotationGangMinNum:   "0",
							extension.AnnotationGangWaitTime: "0",
							extension.AnnotationGangGroups:   "[a,b]",
						},
					},
				},
				// pod2 announce GangB with illegal AnnotationGangWaitTime,
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod2",
						Annotations: map[string]string{
							extension.AnnotationGangName:     "gangB",
							extension.AnnotationGangMinNum:   "0",
							extension.AnnotationGangWaitTime: "-20s",
							extension.AnnotationGangGroups:   "[a,b]",
						},
					},
				},
			},
			wantCache: map[string]*Gang{
				"default/gangA": {
					Name:              "default/gangA",
					WaitTime:          0,
					CreateTime:        fakeTimeNowFn(),
					Mode:              extension.GangModeStrict,
					MinRequiredNumber: 0,
					TotalChildrenNum:  0,
					GangGroup:         []string{},
					HasGangInit:       true,
					GangFrom:          GangFromPodAnnotation,
					Children: map[string]*corev1.Pod{
						"default/pod1": {
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "pod1",
								Annotations: map[string]string{
									extension.AnnotationGangName:     "gangA",
									extension.AnnotationGangMinNum:   "0",
									extension.AnnotationGangWaitTime: "0",
									extension.AnnotationGangGroups:   "[a,b]",
								},
							},
						},
					},
					WaitingForBindChildren: map[string]*corev1.Pod{},
					BoundChildren:          map[string]*corev1.Pod{},
					ScheduleCycleValid:     true,
					ScheduleCycle:          1,
					ChildrenScheduleRoundMap: map[string]int{
						"default/pod1": 0,
					},
				},
				"default/gangB": {
					Name:              "default/gangB",
					WaitTime:          0,
					CreateTime:        fakeTimeNowFn(),
					Mode:              extension.GangModeStrict,
					MinRequiredNumber: 0,
					TotalChildrenNum:  0,
					GangGroup:         []string{},
					HasGangInit:       true,
					GangFrom:          GangFromPodAnnotation,
					Children: map[string]*corev1.Pod{
						"default/pod2": {
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "pod2",
								Annotations: map[string]string{
									extension.AnnotationGangName:     "gangB",
									extension.AnnotationGangMinNum:   "0",
									extension.AnnotationGangWaitTime: "-20s",
									extension.AnnotationGangGroups:   "[a,b]",
								},
							},
						},
					},
					WaitingForBindChildren: map[string]*corev1.Pod{},
					BoundChildren:          map[string]*corev1.Pod{},
					ScheduleCycleValid:     true,
					ScheduleCycle:          1,
					ChildrenScheduleRoundMap: map[string]int{
						"default/pod2": 0,
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
			gangCache := NewGangCache(&schedulingconfig.GangArgs{}, nil, nil)
			for _, pod := range tt.pods {
				gangCache.onPodAdd(pod)
			}
			assert.Equal(t, tt.wantCache, gangCache.gangItems)
		})
	}
}

func TestGangCache_OnPodDelete(t *testing.T) {
	tests := []struct {
		name      string
		podGroups []*v1alpha1.PodGroup
		pods      []*corev1.Pod
		wantCache map[string]*Gang
	}{
		{
			name: "delete invalid pod,has no gang",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod1",
					},
				},
			},
			wantCache: map[string]*Gang{},
		},
		{
			name: "update invalid pod2,gang has not find",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "pod2",
						Namespace:   "wenshiqi",
						Labels:      map[string]string{"test": "gang"},
						Annotations: map[string]string{"test": "gang"},
					},
				},
			},
			wantCache: map[string]*Gang{},
		},
		{
			name: "delete gangA's pods one by one,finally gangA should be deleted",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod3",
						Annotations: map[string]string{
							extension.AnnotationGangName: "gangA",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod4",
						Annotations: map[string]string{
							extension.AnnotationGangName: "gangA",
						},
					},
				},
			},
			wantCache: map[string]*Gang{},
		},
		{
			name: "delete gangB's pods one by one,but gangB is created by CRD",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod5",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: "GangB",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod6",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: "GangB",
						},
					},
				},
			},
			podGroups: []*v1alpha1.PodGroup{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "gangB",
					},
					Spec: v1alpha1.PodGroupSpec{
						MinMember:              4,
						ScheduleTimeoutSeconds: pointer.Int32(10),
					},
				},
			},
			wantCache: map[string]*Gang{
				"default/gangB": {
					Name:                     "default/gangB",
					WaitTime:                 10 * time.Second,
					CreateTime:               fakeTimeNowFn(),
					Mode:                     extension.GangModeStrict,
					MinRequiredNumber:        4,
					TotalChildrenNum:         4,
					GangGroup:                []string{},
					HasGangInit:              true,
					GangFrom:                 GangFromPodGroupCrd,
					Children:                 map[string]*corev1.Pod{},
					WaitingForBindChildren:   map[string]*corev1.Pod{},
					BoundChildren:            map[string]*corev1.Pod{},
					ScheduleCycleValid:       true,
					ScheduleCycle:            1,
					ChildrenScheduleRoundMap: map[string]int{},
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
			gangCache := NewGangCache(&schedulingconfig.GangArgs{}, nil, nil)
			for _, pod := range tt.pods {
				gangCache.onPodAdd(pod)
			}
			for _, pg := range tt.podGroups {
				gangCache.onPodGroupAdd(pg)
			}
			// start deleting pods
			for _, pod := range tt.pods {
				gangCache.onPodDelete(pod)
			}
			assert.Equal(t, tt.wantCache, gangCache.gangItems)
		})
	}
}

func TestGangCache_OnPodGroupAdd(t *testing.T) {
	waitTime := int32(300)
	tests := []struct {
		name      string
		pgs       []*v1alpha1.PodGroup
		wantCache map[string]*Gang
	}{
		{
			name: "update podGroup with annotations",
			pgs: []*v1alpha1.PodGroup{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "gangA",
						Annotations: map[string]string{
							extension.AnnotationGangMode:   extension.GangModeNonStrict,
							extension.AnnotationGangGroups: "[\"default/gangA\",\"default/gangB\"]",
						},
					},
					Spec: v1alpha1.PodGroupSpec{
						MinMember:              2,
						ScheduleTimeoutSeconds: &waitTime,
					},
				},
			},
			wantCache: map[string]*Gang{
				"default/gangA": {
					Name:                     "default/gangA",
					WaitTime:                 300 * time.Second,
					CreateTime:               fakeTimeNowFn(),
					Mode:                     extension.GangModeNonStrict,
					MinRequiredNumber:        2,
					TotalChildrenNum:         2,
					GangGroup:                []string{"default/gangA", "default/gangB"},
					HasGangInit:              true,
					GangFrom:                 GangFromPodGroupCrd,
					Children:                 map[string]*corev1.Pod{},
					WaitingForBindChildren:   map[string]*corev1.Pod{},
					BoundChildren:            map[string]*corev1.Pod{},
					ScheduleCycleValid:       true,
					ScheduleCycle:            1,
					ChildrenScheduleRoundMap: map[string]int{},
				},
			},
		},
		{
			name: "update podGroup with illegal annotations",
			pgs: []*v1alpha1.PodGroup{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "gangA",
						Annotations: map[string]string{
							extension.AnnotationGangMode:     "WenShiqi222",
							extension.AnnotationGangGroups:   "a,b",
							extension.AnnotationGangTotalNum: "2",
						},
					},
					Spec: v1alpha1.PodGroupSpec{
						MinMember:              4,
						ScheduleTimeoutSeconds: &waitTime,
					},
				},
			},
			wantCache: map[string]*Gang{
				"default/gangA": {
					Name:                     "default/gangA",
					WaitTime:                 300 * time.Second,
					CreateTime:               fakeTimeNowFn(),
					Mode:                     extension.GangModeStrict,
					MinRequiredNumber:        4,
					TotalChildrenNum:         4,
					GangGroup:                []string{},
					HasGangInit:              true,
					GangFrom:                 GangFromPodGroupCrd,
					Children:                 map[string]*corev1.Pod{},
					WaitingForBindChildren:   map[string]*corev1.Pod{},
					BoundChildren:            map[string]*corev1.Pod{},
					ScheduleCycleValid:       true,
					ScheduleCycle:            1,
					ChildrenScheduleRoundMap: map[string]int{},
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
			gangCache := NewGangCache(&schedulingconfig.GangArgs{}, nil, nil)
			for _, pg := range tt.pgs {
				gangCache.onPodGroupAdd(pg)
			}
			assert.Equal(t, tt.wantCache, gangCache.gangItems)
		})
	}
}

func TestGangCache_OnGangDelete(t *testing.T) {
	cache := NewGangCache(&schedulingconfig.GangArgs{}, nil, nil)
	podGroup := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "gangA",
		},
	}
	gangId := getNamespaceSplicingName("default", "gangA")
	cache.createGangToCache(gangId)
	cache.onPodGroupDelete(podGroup)
	assert.Equal(t, 0, len(cache.gangItems))

}
