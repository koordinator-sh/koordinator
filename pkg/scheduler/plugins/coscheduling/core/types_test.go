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
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	schedulingv1alpha1 "k8s.io/api/scheduling/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

func TestAnnotationGangInfo(t *testing.T) {
	args := getTestDefaultCoschedulingArgs(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				extension.AnnotationGangName:     "gang-a",
				extension.AnnotationGangMinNum:   "3",
				extension.AnnotationGangTotalNum: "5",
				extension.AnnotationGangMode:     extension.GangModeNonStrict,
				extension.AnnotationGangWaitTime: "45s",
				extension.AnnotationGangGroups:   `["test-ns/gang-a", "test-ns/gang-b"]`,
			},
			CreationTimestamp: metav1.Now(),
		},
	}

	info := NewAnnotationGangInfo(pod, args)

	assert.Equal(t, "gang-a", info.GetName())
	assert.Equal(t, "test-ns", info.GetNamespace())
	assert.Equal(t, int32(3), info.GetMinMember())
	assert.Equal(t, int32(5), info.GetTotalMember())
	assert.Equal(t, extension.GangModeNonStrict, info.GetMode())
	assert.Equal(t, 45*time.Second, info.GetWaitTime())
	assert.Equal(t, extension.GangMatchPolicyOnceSatisfied, info.GetMatchPolicy())
	assert.Equal(t, []string{"test-ns/gang-a", "test-ns/gang-b"}, info.GetGangGroups())
	assert.Equal(t, GangFromPodAnnotation, info.GetGangFrom())
	assert.Equal(t, pod.CreationTimestamp, info.GetCreationTimestamp())
	assert.Nil(t, info.GetNetworkTopologySpec())
}

func TestAnnotationGangInfoFallback(t *testing.T) {
	args := getTestDefaultCoschedulingArgs(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
		},
	}

	info := NewAnnotationGangInfo(pod, args)

	assert.Equal(t, int32(-1), info.GetMinMember())
	assert.Equal(t, int32(0), info.GetTotalMember())
	assert.Equal(t, extension.GangModeStrict, info.GetMode())
	assert.Equal(t, args.DefaultTimeout.Duration, info.GetWaitTime())
	assert.Equal(t, args.DefaultMatchPolicy, info.GetMatchPolicy())
	assert.Equal(t, []string{"test-ns/"}, info.GetGangGroups())
}

func TestPodGroupGangInfo(t *testing.T) {
	args := getTestDefaultCoschedulingArgs(t)
	pg := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-a",
			Namespace: "test-ns",
			Annotations: map[string]string{
				extension.AnnotationGangMode:   extension.GangModeNonStrict,
				extension.AnnotationGangGroups: `["test-ns/pg-a", "test-ns/pg-b"]`,
			},
			CreationTimestamp: metav1.Now(),
		},
		Spec: v1alpha1.PodGroupSpec{
			MinMember: 4,
		},
	}

	info := NewPodGroupGangInfo(pg, args)

	assert.Equal(t, "pg-a", info.GetName())
	assert.Equal(t, "test-ns", info.GetNamespace())
	assert.Equal(t, int32(4), info.GetMinMember())
	assert.Equal(t, int32(0), info.GetTotalMember())
	assert.Equal(t, extension.GangModeNonStrict, info.GetMode())
	assert.Equal(t, args.DefaultTimeout.Duration, info.GetWaitTime())
	assert.Equal(t, args.DefaultMatchPolicy, info.GetMatchPolicy())
	assert.Equal(t, []string{"test-ns/pg-a", "test-ns/pg-b"}, info.GetGangGroups())
	assert.Equal(t, GangFromPodGroupCrd, info.GetGangFrom())
	assert.Equal(t, pg.CreationTimestamp, info.GetCreationTimestamp())
	assert.Nil(t, info.GetNetworkTopologySpec())
}

func TestPodGroupGangInfoFallback(t *testing.T) {
	args := getTestDefaultCoschedulingArgs(t)
	pg := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-a",
			Namespace: "test-ns",
		},
	}

	info := NewPodGroupGangInfo(pg, args)
	assert.Equal(t, extension.GangModeStrict, info.GetMode())
	assert.Equal(t, []string{"test-ns/pg-a"}, info.GetGangGroups())
}

func TestWorkloadGangInfo(t *testing.T) {
	args := getTestDefaultCoschedulingArgs(t)
	wl := &schedulingv1alpha1.Workload{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "wl-a",
			Namespace:         "test-ns",
			CreationTimestamp: metav1.Now(),
		},
		Spec: schedulingv1alpha1.WorkloadSpec{
			PodGroups: []schedulingv1alpha1.PodGroup{
				{
					Name: "wl-pg-1",
					Policy: schedulingv1alpha1.PodGroupPolicy{
						Gang: &schedulingv1alpha1.GangSchedulingPolicy{
							MinCount: 3,
						},
					},
				},
				{
					Name: "wl-pg-2",
					Policy: schedulingv1alpha1.PodGroupPolicy{
						Gang: &schedulingv1alpha1.GangSchedulingPolicy{
							MinCount: 5,
						},
					},
				},
			},
		},
	}

	info := NewWorkloadGangInfo(wl, "wl-pg-1", args)

	assert.Equal(t, "wl-pg-1", info.GetName())
	assert.Equal(t, "test-ns", info.GetNamespace())
	assert.Equal(t, int32(3), info.GetMinMember())
	assert.Equal(t, int32(3), info.GetTotalMember())
	assert.Equal(t, extension.GangModeStrict, info.GetMode())
	assert.Equal(t, args.DefaultTimeout.Duration, info.GetWaitTime())
	assert.Equal(t, args.DefaultMatchPolicy, info.GetMatchPolicy())
	assert.Equal(t, []string{"test-ns/wl-pg-1", "test-ns/wl-pg-2"}, info.GetGangGroups())
	assert.Equal(t, GangFromNativeWorkload, info.GetGangFrom())
	assert.Equal(t, wl.CreationTimestamp, info.GetCreationTimestamp())
	assert.Nil(t, info.GetNetworkTopologySpec())
}

func TestWorkloadGangInfoFallback(t *testing.T) {
	args := getTestDefaultCoschedulingArgs(t)
	wl := &schedulingv1alpha1.Workload{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wl-a",
			Namespace: "test-ns",
		},
	}

	info := NewWorkloadGangInfo(wl, "non-existent-pg", args)
	assert.Equal(t, int32(0), info.GetMinMember())
}
