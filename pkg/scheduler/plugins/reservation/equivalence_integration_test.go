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

package reservation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/events"
	"k8s.io/component-base/metrics/testutil"
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
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	st "k8s.io/kubernetes/pkg/scheduler/testing"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	v1 "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/equivalence"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// equivalenceSuit schedules pods through equivalence-class reuse with the real Reservation plugin
// and a real scheduler cache, so the reservation state written by BeforePreFilter drives
// eligibility the same way it does in koord-scheduler.
type equivalenceSuit struct {
	t      *testing.T
	ctx    context.Context
	logger klog.Logger
	cache  cache.Cache
	sched  *scheduler.Scheduler
	fwk    framework.Framework
	pl     *Plugin
}

func newEquivalenceSuit(t *testing.T) *equivalenceSuit {
	t.Helper()
	logger, ctx := ktesting.NewTestContext(t)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	schedulermetrics.Register()
	koordmetrics.Register()
	frameworkexthelper.ResetRegistrations()

	schedulerCache := cache.New(ctx, 30*time.Second, nil)
	for _, name := range []string{"node-1", "node-2"} {
		schedulerCache.AddNode(logger, st.MakeNode().Name(name).Capacity(map[corev1.ResourceName]string{
			corev1.ResourceCPU: "4", corev1.ResourceMemory: "8Gi",
		}).Obj())
	}

	koordClient := koordfake.NewSimpleClientset()
	extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClient),
		frameworkext.WithKoordinatorSharedInformerFactory(koordinatorinformers.NewSharedInformerFactory(koordClient, 0)),
	)
	require.NoError(t, err)

	var v1args v1.ReservationArgs
	v1.SetDefaults_ReservationArgs(&v1args)
	var args config.ReservationArgs
	require.NoError(t, v1.Convert_v1_ReservationArgs_To_config_ReservationArgs(&v1args, &args, nil))
	var pl *Plugin
	proxy := frameworkext.PluginFactoryProxy(extenderFactory, func(ctx context.Context, _ runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
		p, err := New(ctx, &args, handle)
		if err == nil {
			pl = p.(*Plugin)
		}
		return p, err
	})

	client := kubefake.NewSimpleClientset()
	q := internalqueue.NewTestQueue(ctx, (&queuesort.PrioritySort{}).Less)
	t.Cleanup(q.Close)
	// A per-test profile keeps the per-profile hit and miss counters independent across tests.
	profileName := t.Name()
	fw, err := schedulertesting.NewFramework(ctx, []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterPluginAsExtensions(noderesources.Name,
			frameworkruntime.FactoryAdapter(plfeature.Features{}, noderesources.NewFit), "PreFilter", "Filter", "Score"),
		schedulertesting.RegisterPluginAsExtensions(Name, proxy, "PreFilter", "Filter", "PreScore", "Score"),
	}, profileName,
		frameworkruntime.WithClientSet(client),
		frameworkruntime.WithInformerFactory(informers.NewSharedInformerFactory(client, 0)),
		frameworkruntime.WithSnapshotSharedLister(cache.NewEmptySnapshot()),
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
		frameworkruntime.WithPodNominator(q),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fw.Close()) })
	require.NotNil(t, pl)

	ext := extenderFactory.NewFrameworkExtender(fw)
	ext.SetConfiguredPlugins(fw.ListPlugins())
	sched := &scheduler.Scheduler{
		Cache:           schedulerCache,
		SchedulingQueue: q,
		NextPod:         q.Pop,
		Profiles:        profile.Map{profileName: ext},
		SchedulePod: func(context.Context, framework.Framework, fwktype.CycleState, *corev1.Pod) (scheduler.ScheduleResult, error) {
			return scheduler.ScheduleResult{}, fmt.Errorf("class pods must not reach the upstream scheduling path")
		},
	}
	scheduling := equivalence.NewEquivalenceScheduling(sched, nil, equivalence.DefaultEquivalenceClassCacheSize, equivalence.PlacementSpread, frameworkext.NewFakeEquivalenceClass())
	ext.(frameworkext.EquivalenceAwareHandle).RegisterSchedulingDecisionProvider(scheduling)
	extenderFactory.InitScheduler(&frameworkext.SchedulerAdapter{Scheduler: sched})

	return &equivalenceSuit{t: t, ctx: ctx, logger: logger, cache: schedulerCache, sched: sched, fwk: ext, pl: pl}
}

// schedule decides a node for a pod of the class labeled app, requesting one CPU.
func (s *equivalenceSuit) schedule(name, app string) scheduler.ScheduleResult {
	s.t.Helper()
	pod := frameworkext.NewFakeEquivalenceClass().MakePod(name, "class-"+app)
	pod.Labels["app"] = app
	pod.Spec.Containers = []corev1.Container{{Name: "main", Resources: corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
	}}}
	result, err := s.sched.SchedulePod(s.ctx, s.fwk, framework.NewCycleState(), pod)
	require.NoError(s.t, err)
	return result
}

// addReservation makes the reservation available to the plugin and places its reserve pod, plus
// the pods it has allocated, on the node in the scheduler cache.
func (s *equivalenceSuit) addReservation(r *schedulingv1alpha1.Reservation, allocated ...*corev1.Pod) {
	s.t.Helper()
	s.pl.reservationCache.updateReservation(r)
	reservePod := reservationutil.NewReservePod(r)
	reservePod.Spec.NodeName = r.Status.NodeName
	require.NoError(s.t, s.cache.AddPod(s.logger, reservePod))
	for _, pod := range allocated {
		require.NoError(s.t, s.pl.reservationCache.addPod(r.UID, pod))
		require.NoError(s.t, s.cache.AddPod(s.logger, pod))
	}
}

func (s *equivalenceSuit) hits() float64 {
	s.t.Helper()
	value, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassHits.WithLabelValues(s.fwk.ProfileName()))
	require.NoError(s.t, err)
	return value
}

func (s *equivalenceSuit) vetoes() float64 {
	s.t.Helper()
	value, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassMisses.WithLabelValues(s.fwk.ProfileName(), "plugin_not_reusable"))
	require.NoError(s.t, err)
	return value
}

// makeEquivalenceReservation returns an available reservation of cpu on node, owned by pods
// labeled app=ownerApp.
func makeEquivalenceReservation(name, node, ownerApp, cpu string) *schedulingv1alpha1.Reservation {
	requests := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)}
	return &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "main", Resources: corev1.ResourceRequirements{Requests: requests},
			}}}},
			Owners: []schedulingv1alpha1.ReservationOwner{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": ownerApp}},
			}},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:       schedulingv1alpha1.ReservationAvailable,
			NodeName:    node,
			Allocatable: requests,
		},
	}
}

func TestEquivalenceReuseWithUnmatchedAllocatedReservation(t *testing.T) {
	s := newEquivalenceSuit(t)
	allocated := st.MakePod().Namespace("default").Name("allocated").UID("allocated-uid").Node("node-1").
		Req(map[corev1.ResourceName]string{corev1.ResourceCPU: "1"}).Obj()
	// The reservation takes part in every class pod's resource restoration, which used to veto reuse.
	s.addReservation(makeEquivalenceReservation("other-owner", "node-1", "x", "2"), allocated)

	hits, vetoes := s.hits(), s.vetoes()
	s.schedule("a-1", "a")
	assert.Equal(t, hits, s.hits(), "the first pod backfills the class")
	s.schedule("a-2", "a")
	assert.Equal(t, hits+1, s.hits(), "the next pod reuses the decision")
	assert.Equal(t, vetoes, s.vetoes())
}

func TestEquivalenceVetoForMatchedReservation(t *testing.T) {
	s := newEquivalenceSuit(t)
	s.addReservation(makeEquivalenceReservation("owned", "node-1", "a", "2"))

	hits, vetoes := s.hits(), s.vetoes()
	for _, name := range []string{"a-1", "a-2"} {
		s.schedule(name, "a")
	}
	assert.Equal(t, hits, s.hits(), "a pod matching a reservation runs full node selection")
	assert.Equal(t, vetoes+2, s.vetoes())
}

func TestEquivalenceVetoInvalidatesOnlyItsClass(t *testing.T) {
	s := newEquivalenceSuit(t)
	s.schedule("a-1", "a")
	s.schedule("b-1", "b")

	// A reservation appears after both classes were cached; only class a can allocate it.
	s.addReservation(makeEquivalenceReservation("owned-by-a", "node-1", "a", "2"))
	hits, vetoes := s.hits(), s.vetoes()
	s.schedule("a-2", "a")
	assert.Equal(t, vetoes+1, s.vetoes())
	assert.Equal(t, hits, s.hits())
	s.schedule("b-2", "b")
	assert.Equal(t, hits+1, s.hits(), "class b keeps its decision")
}

func TestEquivalenceFollowsReservationOwnershipChanges(t *testing.T) {
	s := newEquivalenceSuit(t)
	r := makeEquivalenceReservation("changing-owner", "node-1", "x", "2")
	s.addReservation(r)
	s.schedule("a-1", "a")

	// The reservation becomes allocatable by class a after its decision was cached.
	owned := r.DeepCopy()
	owned.Spec.Owners[0].LabelSelector.MatchLabels["app"] = "a"
	s.pl.reservationCache.updateReservation(owned)
	hits, vetoes := s.hits(), s.vetoes()
	s.schedule("a-2", "a")
	assert.Equal(t, vetoes+1, s.vetoes())

	// Once the ownership moves away again, the class is backfilled and then reused.
	s.pl.reservationCache.updateReservation(r)
	s.schedule("a-3", "a")
	assert.Equal(t, hits, s.hits(), "the vetoed decision is not reused")
	s.schedule("a-4", "a")
	assert.Equal(t, hits+1, s.hits())
}

func TestEquivalenceStaleQuotaIsRejectedByFilter(t *testing.T) {
	s := newEquivalenceSuit(t)
	// Both nodes enter the cached decision with room for several class pods.
	s.schedule("a-1", "a")

	// An unrelated reservation then takes all of node-1. No event reaches the equivalence cache.
	s.addReservation(makeEquivalenceReservation("fills-node-1", "node-1", "x", "4"))
	hits := s.hits()
	evaluated := 0
	for _, name := range []string{"a-2", "a-3"} {
		result := s.schedule(name, "a")
		assert.Equal(t, "node-2", result.SuggestedHost, "the refreshed snapshot and per-pod Filter reject the stale node-1 quota")
		evaluated += result.EvaluatedNodes
	}
	assert.Equal(t, hits+2, s.hits(), "both pods are still served from the cached decision")
	assert.Equal(t, 3, evaluated, "node-1 is tried once, rejected, and then dropped from the decision")
}
