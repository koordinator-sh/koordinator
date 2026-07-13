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

package operator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
)

func TestPSIExportInitWithInjectedClient(t *testing.T) {
	exporter := &PSIExport{}
	exporter.SetClientset(clientsetfake.NewSimpleClientset())

	assert.NoError(t, exporter.Init())
}

func TestPressureConditionTransitions(t *testing.T) {
	exporter := &PSIExport{}
	pod := &corev1.Pod{}
	now := metav1.Now()
	threshold := StallThreshold{Avg10: 20, Avg60: 20, Avg300: 20}

	err := exporter.pressureCondition(pod, now, PodCpuInPressure, StallInformation{Avg10: 25}, threshold)
	assert.NoError(t, err)
	if assert.Len(t, pod.Status.Conditions, 1) {
		condition := pod.Status.Conditions[0]
		assert.Equal(t, PodCpuInPressure, condition.Type)
		assert.Equal(t, corev1.ConditionTrue, condition.Status)
		assert.Equal(t, string(PodCpuInPressure), condition.Reason)
	}

	err = exporter.pressureCondition(pod, now, PodCpuInPressure, StallInformation{Avg10: 10}, threshold)
	assert.NoError(t, err)
	if assert.Len(t, pod.Status.Conditions, 1) {
		condition := pod.Status.Conditions[0]
		assert.Equal(t, corev1.ConditionFalse, condition.Status)
		assert.Equal(t, "PodCpuNotInPressure", condition.Reason)
		assert.Empty(t, condition.Message)
	}
}

func TestPatchPodStatusWithInjectedClient(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{
				Type:   PodCpuInPressure,
				Status: corev1.ConditionFalse,
			}},
		},
	}
	client := clientsetfake.NewSimpleClientset(pod.DeepCopy())
	exporter := &PSIExport{}
	exporter.SetClientset(client)

	modified := pod.DeepCopy()
	modified.Status.Conditions[0].Status = corev1.ConditionTrue
	modified.Status.Conditions[0].Reason = string(PodCpuInPressure)

	patched, err := exporter.patchPodStatus(pod, modified)
	assert.NoError(t, err)
	assert.Equal(t, corev1.ConditionTrue, patched.Status.Conditions[0].Status)

	stored, err := client.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, types.UID(""), stored.UID)
	assert.Equal(t, corev1.ConditionTrue, stored.Status.Conditions[0].Status)
}
