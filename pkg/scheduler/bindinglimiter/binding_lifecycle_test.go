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

package bindinglimiter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
	internalqueue "k8s.io/kubernetes/pkg/scheduler/backend/queue"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	plfeature "k8s.io/kubernetes/pkg/scheduler/framework/plugins/feature"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// testBindingPlugin records the binding lifecycle points a slot must be held and released across.
type testBindingPlugin struct {
	preBind    func(context.Context, *corev1.Pod) *fwktype.Status
	patchError *fwktype.Status
	unreserved chan string
	postBound  chan string
}

func (p *testBindingPlugin) Name() string { return "BindingLifecycle" }

func (p *testBindingPlugin) Reserve(context.Context, fwktype.CycleState, *corev1.Pod, string) *fwktype.Status {
	return nil
}

func (p *testBindingPlugin) Unreserve(_ context.Context, _ fwktype.CycleState, pod *corev1.Pod, _ string) {
	p.unreserved <- pod.Name
}

func (p *testBindingPlugin) PreBindPreFlight(context.Context, fwktype.CycleState, *corev1.Pod, string) *fwktype.Status {
	return nil
}

func (p *testBindingPlugin) PreBind(ctx context.Context, _ fwktype.CycleState, pod *corev1.Pod, _ string) *fwktype.Status {
	return p.preBind(ctx, pod)
}

func (p *testBindingPlugin) ApplyPatch(context.Context, fwktype.CycleState, metav1.Object, metav1.Object) *fwktype.Status {
	return p.patchError
}

func (p *testBindingPlugin) PostBind(_ context.Context, _ fwktype.CycleState, pod *corev1.Pod, _ string) {
	p.postBound <- pod.Name
}

func (p *testBindingPlugin) register(factory *frameworkext.FrameworkExtenderFactory) []schedulertesting.RegisterPluginFunc {
	proxy := frameworkext.PluginFactoryProxy(factory, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
		return p, nil
	})
	return []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterPluginAsExtensions(p.Name(), proxy, "Reserve", "PreBind", "PostBind"),
	}
}

// bindingTestSuit drives the real factory, queue and upstream ScheduleOne across two profiles with
// one shared limiter. The upstream node-selection function is substituted so the tests never depend
// on real node selection.
type bindingTestSuit struct {
	limiter   *BindingLimiter
	sched     *scheduler.Scheduler
	client    *kubefake.Clientset
	informers informers.SharedInformerFactory
	failures  chan *fwktype.Status
}

func newBindingTestSuit(t *testing.T, ctx context.Context, register func(*frameworkext.FrameworkExtenderFactory) []schedulertesting.RegisterPluginFunc) *bindingTestSuit {
	t.Helper()
	metrics.Register()
	koordmetrics.Register()
	h := &bindingTestSuit{
		limiter:  NewBindingLimiter(1, fakeClass),
		client:   kubefake.NewSimpleClientset(),
		failures: make(chan *fwktype.Status, 10),
	}
	h.informers = informers.NewSharedInformerFactory(h.client, 0)
	h.client.PrependReactor("create", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return action.GetSubresource() == "binding", nil, nil
	})
	koordClient := koordfake.NewSimpleClientset()
	factory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClient),
		frameworkext.WithKoordinatorSharedInformerFactory(koordinatorinformers.NewSharedInformerFactory(koordClient, 0)),
	)
	require.NoError(t, err)
	q := internalqueue.NewTestQueue(ctx, (&queuesort.PrioritySort{}).Less)
	t.Cleanup(q.Close)
	sched := &scheduler.Scheduler{
		Cache:           cache.New(ctx, time.Minute, nil),
		SchedulingQueue: q,
		NextPod:         q.Pop,
		Profiles:        profile.Map{},
		SchedulePod: func(context.Context, framework.Framework, fwktype.CycleState, *corev1.Pod) (scheduler.ScheduleResult, error) {
			return scheduler.ScheduleResult{SuggestedHost: "node-1", EvaluatedNodes: 1, FeasibleNodes: 1}, nil
		},
		FailureHandler: func(_ context.Context, _ framework.Framework, _ *framework.QueuedPodInfo, status *fwktype.Status, _ *fwktype.NominatingInfo, _ time.Time) {
			h.failures <- status
		},
	}
	h.sched = sched
	sched.Cache.AddNode(klog.FromContext(ctx), makeNode("node-1", "4", "8Gi"))
	sched.Cache.AddNode(klog.FromContext(ctx), makeNode("node-2", "4", "8Gi"))
	plugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterPluginAsExtensions(noderesources.Name,
			frameworkruntime.FactoryAdapter(plfeature.Features{}, noderesources.NewFit), "PreFilter", "Filter", "Score"),
	}
	if register != nil {
		plugins = append(plugins, register(factory)...)
	}
	for _, name := range []string{"koord-scheduler", "other-scheduler"} {
		fwk, err := schedulertesting.NewFramework(ctx, plugins, name,
			frameworkruntime.WithClientSet(h.client),
			frameworkruntime.WithInformerFactory(h.informers),
			frameworkruntime.WithSnapshotSharedLister(cache.NewEmptySnapshot()),
			frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
			frameworkruntime.WithPodNominator(q),
			frameworkruntime.WithWaitingPods(frameworkruntime.NewWaitingPodsMap()),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, fwk.Close()) })
		ext := factory.NewFrameworkExtender(fwk)
		ext.SetConfiguredPlugins(fwk.ListPlugins())
		// One limiter instance is shared across profiles so its semaphore bounds the total number of
		// concurrent binding cycles, not the number per profile.
		ext.SetBindingLimiter(h.limiter)
		sched.Profiles[name] = ext
	}
	factory.InitScheduler(&frameworkext.SchedulerAdapter{Scheduler: sched})
	factory.InterceptSchedulerError(sched)
	return h
}

// makeNode builds a schedulable node with the given cpu and memory capacity.
func makeNode(name string, cpu, memory string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
		},
	}
}

func (h *bindingTestSuit) schedule(t *testing.T, ctx context.Context, pod *corev1.Pod) {
	t.Helper()
	require.NoError(t, h.informers.Core().V1().Pods().Informer().GetStore().Add(pod))
	h.sched.SchedulingQueue.Add(klog.FromContext(ctx), pod)
	done := make(chan struct{})
	go func() {
		h.sched.ScheduleOne(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ScheduleOne blocked on an asynchronous binding cycle")
	}
}

func TestBindingLifecycleHoldsSlotExactlyOnce(t *testing.T) {
	for _, stage := range []string{"success", "prebind", "patch", "bind", "extender-bind"} {
		t.Run(stage, func(t *testing.T) {
			_, ctx := ktesting.NewTestContext(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			p := &testBindingPlugin{unreserved: make(chan string, 1), postBound: make(chan string, 1)}
			h := newBindingTestSuit(t, ctx, p.register)
			var slotsAtPreBind atomic.Int32
			p.preBind = func(context.Context, *corev1.Pod) *fwktype.Status {
				slotsAtPreBind.Store(int32(len(h.limiter.slots)))
				if stage == "prebind" {
					return fwktype.AsStatus(errors.New("prebind failed"))
				}
				return nil
			}
			switch stage {
			case "patch":
				p.patchError = fwktype.AsStatus(errors.New("patch failed"))
			case "bind":
				h.client.PrependReactor("create", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("bind failed")
				})
			case "extender-bind":
				h.sched.Extenders = []fwktype.Extender{&schedulertesting.FakeExtender{
					Binder: func() error { return errors.New("extender-bind failed") },
				}}
			}
			pod := makeClassPod("pod", "key-a")
			h.schedule(t, ctx, pod)
			if stage == "success" {
				select {
				case name := <-p.postBound:
					assert.Equal(t, pod.Name, name)
				case status := <-h.failures:
					t.Fatalf("unexpected failure: %v", status)
				case <-time.After(5 * time.Second):
					t.Fatal("PostBind was not called")
				}
				assert.Empty(t, p.unreserved)
			} else {
				select {
				case status := <-h.failures:
					assert.Contains(t, status.Message(), stage+" failed")
				case <-time.After(5 * time.Second):
					t.Fatal("binding failure was not handled")
				}
				require.Len(t, p.unreserved, 1)
				assert.Equal(t, pod.Name, <-p.unreserved)
				assert.Empty(t, p.postBound)
				_, err := h.sched.Cache.GetPod(pod)
				assert.Error(t, err, "failed binding must forget the assumed pod")
			}
			assert.Equal(t, int32(1), slotsAtPreBind.Load(), "PreBind must run holding exactly one slot")
			assert.Empty(t, h.limiter.slots, "the slot must be returned on every terminal path")
		})
	}
}

func TestBindingBackpressureAcrossProfiles(t *testing.T) {
	_, ctx := ktesting.NewTestContext(t)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	entered := make(chan string, 3)
	releaseFirst := make(chan struct{})
	p := &testBindingPlugin{
		unreserved: make(chan string, 3),
		postBound:  make(chan string, 3),
		preBind: func(ctx context.Context, pod *corev1.Pod) *fwktype.Status {
			entered <- pod.Name
			if pod.Name == "first" {
				select {
				case <-releaseFirst:
				case <-ctx.Done():
					return fwktype.AsStatus(ctx.Err())
				}
			}
			return nil
		},
	}
	h := newBindingTestSuit(t, ctx, p.register)
	h.schedule(t, ctx, makeClassPod("first", "key-a"))
	select {
	case name := <-entered:
		require.Equal(t, "first", name)
	case <-time.After(5 * time.Second):
		t.Fatal("first pod did not reach PreBind")
	}
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	second := makeClassPod("second", "key-a")
	second.Spec.SchedulerName = "other-scheduler"
	h.schedule(t, waitCtx, second)
	ordinary := makeClassPod("ordinary", "")
	ordinary.Labels = nil
	h.schedule(t, ctx, ordinary)
	select {
	case name := <-p.postBound:
		require.Equal(t, "ordinary", name, "the ordinary pod must bypass the full limiter")
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary binding blocked behind class pods")
	}
	require.NotEmpty(t, entered)
	require.Equal(t, "ordinary", <-entered)
	assert.Empty(t, entered, "both profiles must share one limiter")
	cancelWait()
	select {
	case status := <-h.failures:
		assert.ErrorIs(t, status.AsError(), context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled waiter did not leave the binding cycle")
	}
	require.Len(t, p.unreserved, 1)
	assert.Equal(t, "second", <-p.unreserved)
	assert.Len(t, h.limiter.slots, 1, "cancellation must not release another pod's slot")
	close(releaseFirst)
	select {
	case name := <-p.postBound:
		assert.Equal(t, "first", name)
	case <-time.After(5 * time.Second):
		t.Fatal("first pod did not finish binding")
	}
	assert.Empty(t, h.limiter.slots)
}

func TestReservePodBypassesBindingAdmission(t *testing.T) {
	_, ctx := ktesting.NewTestContext(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h := newBindingTestSuit(t, ctx, nil)
	holder := makeClassPod("holder", "key-a")
	require.NoError(t, h.limiter.Acquire(ctx, holder))
	defer h.limiter.Release(holder)
	ext := h.sched.Profiles["koord-scheduler"].(frameworkext.FrameworkExtender)
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "reservation", UID: "reservation-uid"},
		Spec: schedulingv1alpha1.ReservationSpec{Template: &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{SchedulerName: ext.ProfileName()},
		}},
	}
	reservations := ext.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Reservations().Informer()
	require.NoError(t, reservations.GetStore().Add(r))
	pod := reservationutil.NewReservePod(r)
	pod.Labels = makeClassPod("reserve", "key-a").Labels
	status := ext.RunPreBindPlugins(ctx, framework.NewCycleState(), pod, "node-1")
	require.True(t, status.IsSuccess(), "%v", status)
	ext.RunReservePluginsUnreserve(ctx, framework.NewCycleState(), pod, "node-1")
	assert.Len(t, h.limiter.slots, 1, "reserve pod must neither wait for nor release a class slot")
}
