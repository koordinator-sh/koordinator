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

package frameworkext

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func TestDumpDiagnosis(t *testing.T) {
	nowFunc = func() metav1.Time {
		return metav1.NewTime(time.Time{})
	}
	tests := []struct {
		name             string
		pod              *corev1.Pod
		setDiagnosisFunc func(state *framework.CycleState)
		wantDumpMessage  string
	}{
		{
			name: "normal flow",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Labels: map[string]string{
						extension.LabelQuestionedObjectKey: "default/test-pod",
					},
				},
				Status: corev1.PodStatus{
					NominatedNodeName: "nominatedNode",
				},
			},
			setDiagnosisFunc: func(state *framework.CycleState) {
				diagnosis := GetDiagnosis(state)
				diagnosis.PreFilterMessage = "preFilterMessage"
				diagnosis.TopologyKeyToExplain = "topologyKeyToExplain"
				diagnosis.ScheduleDiagnosis.NodeOfferSlot = map[string]int{
					"node1": 1,
					"node2": 2,
				}
				diagnosis.ScheduleDiagnosis.NodeToStatusMap = framework.NodeToStatusMap{
					"node1": framework.NewStatus(framework.Success),
					"node2": framework.NewStatus(framework.Unschedulable, "node2-reason"),
				}
				diagnosis.ScheduleDiagnosis.AlreadyWaitForBound = 1
				diagnosis.ScheduleDiagnosis.SchedulingMode = PodSchedulingMode
			},
			wantDumpMessage: `{"timestamp":null,"questionedKey":"default/test-pod","nominatedNode":"nominatedNode","preFilterMessage":"preFilterMessage","topologyKeyToExplain":"topologyKeyToExplain","scheduleDiagnosis":{"schedulingMode":"Pod","alreadyWaitForBound":1,"nodeOfferSlot":{"node1":1,"node2":2},"nodeFailedDetails":[{"nodeName":"node1","preemptMightHelp":true},{"nodeName":"node2","reason":"node2-reason","preemptMightHelp":true}]},"preemptionDiagnosis":{}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dumpDiagnosis = true
			dumpDiagnosisBlocking = true
			cycleState := framework.NewCycleState()
			initDiagnosis(cycleState, tt.pod)
			tt.setDiagnosisFunc(cycleState)
			gotDumpMessage := DumpDiagnosis(cycleState)
			assert.Equal(t, tt.wantDumpMessage, gotDumpMessage)
		})
	}
}
