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
	"context"
	"testing"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func Test_podEligibleToPreemptOthers_Async(t *testing.T) {
	tests := []struct {
		name                  string
		enableAsyncPreemption bool
		nominatedNodeName     string
		terminatingPod        bool
		want                  bool
	}{
		{
			name:                  "disabled async preemption - no terminating pod",
			enableAsyncPreemption: false,
			nominatedNodeName:     "node-1",
			terminatingPod:        false,
			want:                  true,
		},
		{
			name:                  "disabled async preemption - terminating pod",
			enableAsyncPreemption: false,
			nominatedNodeName:     "node-1",
			terminatingPod:        true,
			want:                  false,
		},
		{
			name:                  "enabled async preemption - terminating pod",
			enableAsyncPreemption: true,
			nominatedNodeName:     "node-1",
			terminatingPod:        true,
			want:                  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := &preemptionEvaluatorImpl{
				enableAsyncPreemption: tt.enableAsyncPreemption,
				IsEligiblePod: func(nodeInfo fwktype.NodeInfo, victim fwktype.PodInfo, preemptor *corev1.Pod) bool {
					return true
				},
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "preemptor",
					Namespace: "default",
				},
				Status: corev1.PodStatus{
					NominatedNodeName: tt.nominatedNodeName,
				},
			}

			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
				},
			}

			var existingPods []*corev1.Pod
			if tt.terminatingPod {
				now := metav1.Now()
				existingPods = append(existingPods, &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "victim",
						Namespace:         "default",
						DeletionTimestamp: &now,
					},
					Spec: corev1.PodSpec{
						NodeName: "node-1",
						Priority: func() *int32 { p := int32(-1); return &p }(),
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.DisruptionTarget,
								Status: corev1.ConditionTrue,
								Reason: corev1.PodReasonPreemptionByScheduler,
							},
						},
					},
				})
			}

			fh := NewFakeExtendedFramework(t, nodes, existingPods, nil, nil, nil)
			ev.handle = fh.(frameworkext.ExtendedHandle)

			state := &JobPreemptionState{
				TerminatingPodOnNominatedNode: map[string]string{},
			}
			ctx := contextWithJobPreemptionState(context.Background(), state)

			got, _ := ev.podEligibleToPreemptOthers(ctx, pod, framework.NewNodeToStatus(nil, nil))
			assert.Equal(t, tt.want, got)
		})
	}
}

// Test_jobEligibleToPreemptOthers_Async verifies the gang-level async preemption
// eligibility check at preemption.go jobEligibleToPreemptOthers, which aggregates
// podEligibleToPreemptOthers results across all pending pods in a gang.
// When enableAsyncPreemption=true, the gang remains eligible to preempt even if
// some of its pods have terminating victims on their nominated nodes.
func Test_jobEligibleToPreemptOthers_Async(t *testing.T) {
	tests := []struct {
		name                  string
		enableAsyncPreemption bool
		// pod1 has a terminating victim on its nominated node; pod2 does not
		pod1HasTerminatingVictim bool
		wantEligible             bool
		wantReason               string
	}{
		{
			name:                     "async disabled - no terminating victims - eligible",
			enableAsyncPreemption:    false,
			pod1HasTerminatingVictim: false,
			wantEligible:             true,
		},
		{
			name:                     "async disabled - terminating victim on nominated node - not eligible",
			enableAsyncPreemption:    false,
			pod1HasTerminatingVictim: true,
			wantEligible:             false,
			wantReason:               ReasonTerminatingVictimOnNominatedNode,
		},
		{
			name:                     "async enabled - terminating victim on nominated node - still eligible",
			enableAsyncPreemption:    true,
			pod1HasTerminatingVictim: true,
			wantEligible:             true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := []*corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
			}

			var existingPods []*corev1.Pod
			if tt.pod1HasTerminatingVictim {
				now := metav1.Now()
				existingPods = append(existingPods, &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "victim",
						Namespace:         "default",
						DeletionTimestamp: &now,
					},
					Spec: corev1.PodSpec{
						NodeName: "node-1",
						Priority: func() *int32 { p := int32(-1); return &p }(),
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.DisruptionTarget,
								Status: corev1.ConditionTrue,
								Reason: corev1.PodReasonPreemptionByScheduler,
							},
						},
					},
				})
			}

			fh := NewFakeExtendedFramework(t, nodes, existingPods, nil, nil, nil)
			ev := &preemptionEvaluatorImpl{
				enableAsyncPreemption: tt.enableAsyncPreemption,
				handle:                fh.(frameworkext.ExtendedHandle),
				IsEligiblePod: func(nodeInfo fwktype.NodeInfo, victim fwktype.PodInfo, preemptor *corev1.Pod) bool {
					return true
				},
			}

			// Gang trigger pod nominated to node-1 (which may have a terminating victim)
			triggerPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "gang-pod-0", Namespace: "default"},
				Status:     corev1.PodStatus{NominatedNodeName: "node-1"},
			}
			// Second gang member nominated to node-2 (no terminating victims)
			pod2 := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "gang-pod-1", Namespace: "default"},
				Status:     corev1.PodStatus{NominatedNodeName: "node-2"},
			}
			allPendingPods := []*corev1.Pod{triggerPod, pod2}

			preemptionState := &JobPreemptionState{
				TriggerPodKey:                 framework.GetNamespacedName(triggerPod.Namespace, triggerPod.Name),
				TerminatingPodOnNominatedNode: map[string]string{},
			}
			ctx := contextWithJobPreemptionState(context.Background(), preemptionState)

			eligible, reason := ev.jobEligibleToPreemptOthers(ctx, triggerPod, allPendingPods, framework.NewNodeToStatus(nil, nil))
			assert.Equal(t, tt.wantEligible, eligible)
			if tt.wantReason != "" {
				assert.Equal(t, tt.wantReason, reason)
			}
		})
	}
}
