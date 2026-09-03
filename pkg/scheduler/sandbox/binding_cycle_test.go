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

package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"
)

const waitingPermitPluginName = "SandboxWaitingPermit"

type waitingPermitPlugin struct{}

func (p *waitingPermitPlugin) Name() string {
	return waitingPermitPluginName
}

func (p *waitingPermitPlugin) Permit(context.Context, fwktype.CycleState, *corev1.Pod, string) (*fwktype.Status, time.Duration) {
	return fwktype.NewStatus(fwktype.Wait), time.Minute
}

func TestBindingCyclePermitRejectRecordsPlugin(t *testing.T) {
	metrics.Register()

	tests := []struct {
		name       string
		pluginName string
	}{
		{
			name:       "named plugin",
			pluginName: waitingPermitPluginName,
		},
		{
			name: "empty plugin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fwk, err := schedulertesting.NewFramework(
				ctx,
				[]schedulertesting.RegisterPluginFunc{
					schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
					schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
					schedulertesting.RegisterPermitPlugin(waitingPermitPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
						return &waitingPermitPlugin{}, nil
					}),
				},
				"koord-scheduler",
				frameworkruntime.WithWaitingPods(frameworkruntime.NewWaitingPodsMap()),
			)
			require.NoError(t, err)

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "waiting",
					UID:       types.UID("waiting-" + tt.name),
				},
				Spec: corev1.PodSpec{SchedulerName: "koord-scheduler"},
			}
			state := framework.NewCycleState()
			require.True(t, fwk.RunPermitPlugins(ctx, state, pod, "node-1").IsWait())
			fwk.IterateOverWaitingPods(func(waitingPod fwktype.WaitingPod) {
				waitingPod.Reject(tt.pluginName, "rejected")
			})

			w := &Workflow{sched: &scheduler.Scheduler{}}
			status := w.bindingCycle(
				ctx,
				state,
				fwk,
				&scheduleResult{ScheduleResult: scheduler.ScheduleResult{SuggestedHost: "node-1"}},
				&framework.QueuedPodInfo{PodInfo: &framework.PodInfo{Pod: pod}},
				time.Now(),
				framework.NewPodsToActivate(),
				nil,
			)

			var fitErr *framework.FitError
			require.ErrorAs(t, status.AsError(), &fitErr)
			assert.Equal(t, tt.pluginName != "", fitErr.Diagnosis.UnschedulablePlugins.Has(tt.pluginName))
			assert.False(t, fitErr.Diagnosis.UnschedulablePlugins.Has(""))
		})
	}
}
