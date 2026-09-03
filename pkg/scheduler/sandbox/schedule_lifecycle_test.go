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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2/ktesting"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	internalqueue "k8s.io/kubernetes/pkg/scheduler/backend/queue"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/workloadauditor"
)

const lifecycleBindPluginName = "SandboxLifecycleBind"

type lifecycleBindPlugin struct {
	bound chan<- struct{}
}

type lifecycleAuditor struct {
	attempts atomic.Int32
}

func (a *lifecycleAuditor) Enabled() bool {
	return true
}

func (a *lifecycleAuditor) AddPod(*corev1.Pod) {}

func (a *lifecycleAuditor) DeletePod(*corev1.Pod) {}

func (a *lifecycleAuditor) RecordPod(*corev1.Pod, workloadauditor.RecordType, string) {}

func (a *lifecycleAuditor) RecordPodGating(*corev1.Pod, bool) {}

func (a *lifecycleAuditor) RecordAttemptPod(*corev1.Pod) {
	a.attempts.Add(1)
}

func (a *lifecycleAuditor) RecordPodScheduleResult(*corev1.Pod, workloadauditor.RecordType, string) {}

func (a *lifecycleAuditor) AddGangGroup(string, int) {}

func (a *lifecycleAuditor) DeleteGangGroup(string) {}

func (a *lifecycleAuditor) RecordGangGroup(string, *corev1.Pod, workloadauditor.RecordType, string) {}

func (a *lifecycleAuditor) RecordGangGating(string, *corev1.Pod, bool) {}

func (a *lifecycleAuditor) RecordGangScheduleResult(string, workloadauditor.RecordType, string) {}

func (a *lifecycleAuditor) RecordDiagnosis(*corev1.Pod, string, workloadauditor.RecordType, string) {}

func (p *lifecycleBindPlugin) Name() string {
	return lifecycleBindPluginName
}

func (p *lifecycleBindPlugin) Bind(context.Context, fwktype.CycleState, *corev1.Pod, string) *fwktype.Status {
	p.bound <- struct{}{}
	return nil
}

func TestScheduleOneRunsKoordinatorLifecycleOnceForOrdinaryPod(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logger, _ := ktesting.NewTestContext(t)
	metrics.Register()
	queue := internalqueue.NewTestQueue(ctx, (&queuesort.PrioritySort{}).Less)
	bound := make(chan struct{}, 1)
	kubeClient := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(kubeClient, 0)
	fwk, err := schedulertesting.NewFramework(
		ctx,
		[]schedulertesting.RegisterPluginFunc{
			schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			schedulertesting.RegisterBindPlugin(lifecycleBindPluginName, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
				return &lifecycleBindPlugin{bound: bound}, nil
			}),
		},
		"koord-scheduler",
		frameworkruntime.WithClientSet(kubeClient),
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(10)),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithPodNominator(queue),
		frameworkruntime.WithWaitingPods(frameworkruntime.NewWaitingPodsMap()),
	)
	require.NoError(t, err)

	koordClient := koordfake.NewSimpleClientset()
	koordInformerFactory := koordinformers.NewSharedInformerFactory(koordClient, 0)
	auditor := &lifecycleAuditor{}
	extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClient),
		frameworkext.WithKoordinatorSharedInformerFactory(koordInformerFactory),
		frameworkext.WithWorkloadAuditor(auditor),
	)
	require.NoError(t, err)
	extender := extenderFactory.NewFrameworkExtender(fwk)
	extender.SetConfiguredPlugins(fwk.ListPlugins())

	schedulerCache := cache.New(ctx, 30*time.Second, nil)
	schedulerCache.AddNode(logger, makeNode("node-1", "4", "8Gi"))
	sched := &scheduler.Scheduler{
		Cache:           schedulerCache,
		NextPod:         queue.Pop,
		SchedulingQueue: queue,
		Profiles: profile.Map{
			"koord-scheduler": extender,
		},
		SchedulePod: func(context.Context, framework.Framework, fwktype.CycleState, *corev1.Pod) (scheduler.ScheduleResult, error) {
			return scheduler.ScheduleResult{
				SuggestedHost:  "node-1",
				EvaluatedNodes: 1,
				FeasibleNodes:  1,
			}, nil
		},
	}
	extenderFactory.InitScheduler(&frameworkext.SchedulerAdapter{Scheduler: sched})
	failed := make(chan *fwktype.Status, 1)
	sched.FailureHandler = func(_ context.Context, _ framework.Framework, _ *framework.QueuedPodInfo, status *fwktype.Status, _ *fwktype.NominatingInfo, _ time.Time) {
		failed <- status
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "ordinary",
			UID:       types.UID("ordinary"),
		},
		Spec: corev1.PodSpec{SchedulerName: "koord-scheduler"},
	}
	require.NoError(t, informerFactory.Core().V1().Pods().Informer().GetStore().Add(pod))
	queue.Add(logger, pod)

	workflow := NewWorkflow(sched, kubeClient)
	workflow.nominatedNodeNameForExpectationEnabled = false
	customWorkflow := &SandboxCustomWorkflow{
		workflow:   workflow,
		scheduling: &equivalenceScheduling{sched: sched},
	}
	customWorkflow.ScheduleOne(ctx)

	select {
	case <-bound:
	case status := <-failed:
		t.Fatalf("ordinary pod scheduling failed: %v", status)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ordinary pod binding")
	}
	assert.Equal(t, int32(1), auditor.attempts.Load())
}

func TestDecisionForPodAssignsKoordinatorHookOwnership(t *testing.T) {
	workflow := &Workflow{sched: &scheduler.Scheduler{}}
	customWorkflow := &SandboxCustomWorkflow{
		workflow:   workflow,
		scheduling: &equivalenceScheduling{sched: workflow.sched},
	}

	assert.False(t, customWorkflow.decisionForPod(&corev1.Pod{}).workflowRunsKoordinatorHooks)
	assert.True(t, customWorkflow.decisionForPod(makeSandboxPod("sandbox", "hash-a")).workflowRunsKoordinatorHooks)
}
