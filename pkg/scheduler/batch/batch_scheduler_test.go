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

package batch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwktype "k8s.io/kube-scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

func newPod(namespace, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
	}
}

func TestBuildJobRequest(t *testing.T) {
	triggerPod := newPod("ns", "p1")
	triggerPod.Spec.SchedulerName = "koord-scheduler"
	p2 := newPod("ns", "p2")
	p3 := newPod("ns", "p3") // no planned node -> skipped

	plan := &frameworkext.BatchScheduleResult{
		Pods: []*corev1.Pod{triggerPod, p2, p3},
		PodToNodeName: map[string]string{
			"ns/p1": "node-a",
			"ns/p2": "node-b",
		},
	}

	jr := buildJobRequest(triggerPod, plan)
	assert.Equal(t, "koord-scheduler", jr.SchedulerName)
	assert.Equal(t, "ns", jr.Namespace)
	assert.Equal(t, "p1", jr.JobName)
	assert.Equal(t, 3, jr.MinMember)
	assert.Len(t, jr.PodsByNode, 2)
	assert.Contains(t, jr.PodsByNode["node-a"], "ns/p1")
	assert.Contains(t, jr.PodsByNode["node-b"], "ns/p2")
	// p3 has no planned node and must be excluded from the grouping.
	for _, pods := range jr.PodsByNode {
		_, ok := pods["ns/p3"]
		assert.False(t, ok)
	}
}

func TestValidateAndGroupByRequest_BuiltFromPlan(t *testing.T) {
	triggerPod := newPod("ns", "p1")
	plan := &frameworkext.BatchScheduleResult{
		Pods:          []*corev1.Pod{triggerPod},
		PodToNodeName: map[string]string{"ns/p1": "node-a"},
	}
	jr := buildJobRequest(triggerPod, plan)
	requestsByNode, err := ValidateAndGroupByRequest(jr)
	assert.NoError(t, err)
	assert.Len(t, requestsByNode, 1)
	assert.Len(t, requestsByNode[0], 1)
	assert.Equal(t, "node-a", requestsByNode[0][0].NodeName)
}

func TestBuildSiblingFailedStatus(t *testing.T) {
	status := BuildSiblingFailedStatus([]string{"ns/p1", "ns/p2"})
	assert.Equal(t, fwktype.Unschedulable, status.Code())
	assert.Contains(t, status.Message(), "ns/p1")
	assert.Contains(t, status.Message(), "ns/p2")
}
