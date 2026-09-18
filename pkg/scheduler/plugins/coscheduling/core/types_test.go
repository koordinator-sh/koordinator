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

	min, err := info.GetMinMember()
	assert.NoError(t, err)
	assert.Equal(t, int32(3), min)

	total, err := info.GetTotalMember()
	assert.NoError(t, err)
	assert.Equal(t, int32(5), total)

	mode, err := info.GetMode()
	assert.NoError(t, err)
	assert.Equal(t, extension.GangModeNonStrict, mode)

	wait, err := info.GetWaitTime()
	assert.NoError(t, err)
	assert.Equal(t, 45*time.Second, wait)

	policy, err := info.GetMatchPolicy()
	assert.NoError(t, err)
	assert.Equal(t, extension.GangMatchPolicyOnceSatisfied, policy)

	groups, err := info.GetGangGroups()
	assert.NoError(t, err)
	assert.Equal(t, []string{"test-ns/gang-a", "test-ns/gang-b"}, groups)

	assert.Equal(t, GangFromPodAnnotation, info.GetGangFrom())
	assert.Equal(t, pod.CreationTimestamp, info.GetCreationTimestamp())

	spec, err := info.GetNetworkTopologySpec()
	assert.NoError(t, err)
	assert.Nil(t, spec)
}

func TestAnnotationGangInfo_MatchPolicyValidation(t *testing.T) {
	args := getTestDefaultCoschedulingArgs(t)

	validPolicies := []string{
		extension.GangMatchPolicyOnlyWaiting,
		extension.GangMatchPolicyWaitingAndRunning,
		extension.GangMatchPolicyOnceSatisfied,
	}
	for _, p := range validPolicies {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					extension.AnnotationGangMatchPolicy: p,
				},
			},
		}
		info := NewAnnotationGangInfo(pod, args)
		policy, err := info.GetMatchPolicy()
		assert.NoError(t, err)
		assert.Equal(t, p, policy)
	}

	// Empty annotation falls back to args.DefaultMatchPolicy without error
	podEmpty := &corev1.Pod{}
	infoEmpty := NewAnnotationGangInfo(podEmpty, args)
	policyEmpty, err := infoEmpty.GetMatchPolicy()
	assert.NoError(t, err)
	assert.Equal(t, args.DefaultMatchPolicy, policyEmpty)

	// Illegal policy string returns error and falls back to args.DefaultMatchPolicy
	podInvalid := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				extension.AnnotationGangMatchPolicy: "once-satisfied-TYPO",
			},
		},
	}
	infoInvalid := NewAnnotationGangInfo(podInvalid, args)
	policyInvalid, err := infoInvalid.GetMatchPolicy()
	assert.Error(t, err)
	assert.Equal(t, args.DefaultMatchPolicy, policyInvalid)
}

func TestAnnotationGangInfo_ErrorHandling(t *testing.T) {
	args := getTestDefaultCoschedulingArgs(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				extension.AnnotationGangMinNum:   "not-an-int",
				extension.AnnotationGangTotalNum: "not-an-int",
				extension.AnnotationGangMode:     "invalid-mode",
				extension.AnnotationGangWaitTime: "invalid-duration",
				extension.AnnotationGangGroups:   "invalid-json",
			},
		},
	}

	info := NewAnnotationGangInfo(pod, args)

	_, err := info.GetMinMember()
	assert.Error(t, err)

	_, err = info.GetTotalMember()
	assert.Error(t, err)

	mode, err := info.GetMode()
	assert.Error(t, err)
	assert.Equal(t, extension.GangModeStrict, mode)

	wait, err := info.GetWaitTime()
	assert.Error(t, err)
	assert.Equal(t, args.DefaultTimeout.Duration, wait)

	_, err = info.GetGangGroups()
	assert.Error(t, err)
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

	min, err := info.GetMinMember()
	assert.NoError(t, err)
	assert.Equal(t, int32(4), min)

	total, err := info.GetTotalMember()
	assert.NoError(t, err)
	assert.Equal(t, int32(0), total)

	mode, err := info.GetMode()
	assert.NoError(t, err)
	assert.Equal(t, extension.GangModeNonStrict, mode)

	wait, err := info.GetWaitTime()
	assert.NoError(t, err)
	assert.Equal(t, args.DefaultTimeout.Duration, wait)

	policy, err := info.GetMatchPolicy()
	assert.NoError(t, err)
	assert.Equal(t, args.DefaultMatchPolicy, policy)

	groups, err := info.GetGangGroups()
	assert.NoError(t, err)
	assert.Equal(t, []string{"test-ns/pg-a", "test-ns/pg-b"}, groups)

	assert.Equal(t, GangFromPodGroupCrd, info.GetGangFrom())
	assert.Equal(t, pg.CreationTimestamp, info.GetCreationTimestamp())

	spec, err := info.GetNetworkTopologySpec()
	assert.NoError(t, err)
	assert.Nil(t, spec)
}

func TestPodGroupGangInfo_MatchPolicyValidation(t *testing.T) {
	args := getTestDefaultCoschedulingArgs(t)

	// Valid policy
	pgValid := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				extension.AnnotationGangMatchPolicy: extension.GangMatchPolicyWaitingAndRunning,
			},
		},
	}
	infoValid := NewPodGroupGangInfo(pgValid, args)
	policyValid, err := infoValid.GetMatchPolicy()
	assert.NoError(t, err)
	assert.Equal(t, extension.GangMatchPolicyWaitingAndRunning, policyValid)

	// Invalid policy string returns error and falls back to args.DefaultMatchPolicy
	pgInvalid := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				extension.AnnotationGangMatchPolicy: "invalid-policy-name",
			},
		},
	}
	infoInvalid := NewPodGroupGangInfo(pgInvalid, args)
	policyInvalid, err := infoInvalid.GetMatchPolicy()
	assert.Error(t, err)
	assert.Equal(t, args.DefaultMatchPolicy, policyInvalid)
}
