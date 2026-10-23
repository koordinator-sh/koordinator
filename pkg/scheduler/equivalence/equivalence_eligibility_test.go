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

package equivalence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/events"
	"k8s.io/component-base/metrics/testutil"
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

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

// eligibilityPlugin is a PreFilter plugin whose equivalence eligibility the test changes between
// pods, so the current pod's eligibility can differ from the state the cached decision was built in.
type eligibilityPlugin struct {
	status *fwktype.Status
}

func (p *eligibilityPlugin) Name() string { return "TestEquivalenceEligibility" }

func (p *eligibilityPlugin) PreFilter(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	return nil, nil
}

func (p *eligibilityPlugin) PreFilterExtensions() fwktype.PreFilterExtensions { return nil }

func (p *eligibilityPlugin) EquivalenceEligible(context.Context, fwktype.CycleState, *corev1.Pod) *fwktype.Status {
	return p.status
}

// eligibilityTestSuit is newTestScheduling plus the FrameworkExtender wrapping, which is what makes
// EquivalenceEligibilityPlugins visible to the equivalence path: the path discovers eligibility
// plugins by asserting its framework is an EquivalenceAwareHandle (see checkEligibility), so a bare
// framework silently runs without any of them.
type eligibilityTestSuit struct {
	scheduling *EquivalenceScheduling
	sched      *scheduler.Scheduler
}

func newEligibilityTestSuit(t *testing.T, ctx context.Context, plugin fwktype.Plugin, nodes ...*corev1.Node) *eligibilityTestSuit {
	t.Helper()
	return newEligibilityTestSuitWithLister(t, ctx, plugin, cache.NewEmptySnapshot(), nodes...)
}

// newEligibilityTestSuitWithLister builds the suit with the given snapshot lister on its profile.
func newEligibilityTestSuitWithLister(t *testing.T, ctx context.Context, plugin fwktype.Plugin, lister fwktype.SharedLister, nodes ...*corev1.Node) *eligibilityTestSuit {
	t.Helper()
	logger, _ := ktesting.NewTestContext(t)
	metrics.Register()
	koordmetrics.Register()

	schedulerCache := cache.New(ctx, 30*time.Second, nil)
	for _, node := range nodes {
		schedulerCache.AddNode(logger, node)
	}

	koordClient := koordfake.NewSimpleClientset()
	extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClient),
		frameworkext.WithKoordinatorSharedInformerFactory(koordinatorinformers.NewSharedInformerFactory(koordClient, 0)),
	)
	require.NoError(t, err)

	client := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	q := internalqueue.NewTestQueue(ctx, (&queuesort.PrioritySort{}).Less)
	t.Cleanup(q.Close)

	sched := &scheduler.Scheduler{
		Cache:           schedulerCache,
		SchedulingQueue: q,
		NextPod:         q.Pop,
		Profiles:        profile.Map{},
		SchedulePod: func(context.Context, framework.Framework, fwktype.CycleState, *corev1.Pod) (scheduler.ScheduleResult, error) {
			return scheduler.ScheduleResult{SuggestedHost: "node-1", EvaluatedNodes: 1, FeasibleNodes: 1}, nil
		},
	}

	proxy := frameworkext.PluginFactoryProxy(extenderFactory, func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) {
		return plugin, nil
	})
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterPluginAsExtensions(noderesources.Name,
			frameworkruntime.FactoryAdapter(plfeature.Features{}, noderesources.NewFit), "PreFilter", "Filter", "Score"),
		schedulertesting.RegisterPluginAsExtensions(plugin.Name(), proxy, "PreFilter"),
	}
	fwk, err := schedulertesting.NewFramework(ctx, registeredPlugins, "koord-scheduler",
		frameworkruntime.WithClientSet(client),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithSnapshotSharedLister(lister),
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
		frameworkruntime.WithPodNominator(q),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fwk.Close()) })

	// Wrapping is what exposes EquivalenceEligibilityPlugins to the equivalence path.
	ext := extenderFactory.NewFrameworkExtender(fwk)
	ext.SetConfiguredPlugins(fwk.ListPlugins())
	sched.Profiles["koord-scheduler"] = ext

	scheduling := NewEquivalenceScheduling(sched, nil, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
	scheduling.equivalence = newEquivalenceClassCache(time.Hour, DefaultEquivalenceClassCacheSize)
	ext.(frameworkext.EquivalenceAwareHandle).RegisterSchedulingDecisionProvider(scheduling)
	extenderFactory.InitScheduler(&frameworkext.SchedulerAdapter{Scheduler: sched})

	return &eligibilityTestSuit{scheduling: scheduling, sched: sched}
}

func TestEligibilityStatusSemantics(t *testing.T) {
	for _, tt := range []struct {
		name       string
		status     *fwktype.Status
		missReason equivalenceCacheMissReason
		// keepsClass reports whether the pod's class keeps its cached decision.
		keepsClass bool
	}{
		{name: "success reuses the decision", status: nil, keepsClass: true},
		{name: "skip has no opinion", status: fwktype.NewStatus(fwktype.Skip), keepsClass: true},
		{name: "unschedulable invalidates the class", status: fwktype.NewStatus(fwktype.Unschedulable, "veto"),
			missReason: equivalenceCacheMissPluginVeto},
		{name: "unresolvable invalidates the class", status: fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, "veto"),
			missReason: equivalenceCacheMissPluginVeto},
		{name: "error keeps the decision but not the pod", status: fwktype.AsStatus(errors.New("broken")),
			missReason: equivalenceCacheMissEligibilityError, keepsClass: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, ctx := ktesting.NewTestContext(t)
			plugin := &eligibilityPlugin{}
			h := newEligibilityTestSuit(t, ctx, plugin, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "4", "8Gi"))
			s := h.scheduling
			fwk := h.sched.Profiles["koord-scheduler"]
			keyA := equivalenceCacheKey(fwk.ProfileName(), "key-a")
			keyB := equivalenceCacheKey(fwk.ProfileName(), "key-b")

			for _, key := range []string{"key-a", "key-b"} {
				_, err := h.sched.SchedulePod(ctx, fwk, framework.NewCycleState(), makeClassPod("first-"+key, key))
				require.NoError(t, err)
			}
			require.Len(t, s.equivalence.entries, 2)
			cachedA := s.equivalence.entries[keyA]

			plugin.status = tt.status
			hits := koordmetrics.EquivalenceClassHits.WithLabelValues(fwk.ProfileName())
			hitsBefore, err := testutil.GetCounterMetricValue(hits)
			require.NoError(t, err)
			var missesBefore float64
			if tt.missReason != "" {
				missesBefore, err = testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassMisses.WithLabelValues(fwk.ProfileName(), tt.missReason.String()))
				require.NoError(t, err)
			}

			result, err := h.sched.SchedulePod(ctx, fwk, framework.NewCycleState(), makeClassPod("second", "key-a"))
			require.NoError(t, err)

			hitsAfter, err := testutil.GetCounterMetricValue(hits)
			require.NoError(t, err)
			if tt.missReason == "" {
				assert.Equal(t, hitsBefore+1, hitsAfter, "an eligible pod reuses the cached decision")
			} else {
				assert.Equal(t, 2, result.FeasibleNodes, "an ineligible pod runs full node selection")
				assert.Equal(t, 2, result.EvaluatedNodes, "an ineligible pod makes no fast-path attempt")
				assert.Equal(t, hitsBefore, hitsAfter)
				missesAfter, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassMisses.WithLabelValues(fwk.ProfileName(), tt.missReason.String()))
				require.NoError(t, err)
				assert.Equal(t, missesBefore+1, missesAfter)
			}

			assert.Contains(t, s.equivalence.entries, keyB, "a pod's eligibility never touches other classes")
			if tt.keepsClass {
				require.Contains(t, s.equivalence.entries, keyA)
				if tt.missReason != "" {
					assert.Same(t, cachedA, s.equivalence.entries[keyA], "an ineligible pod must not backfill")
				}
			} else {
				assert.NotContains(t, s.equivalence.entries, keyA)
				_, ok, reason := s.equivalence.next(keyA, h.sched.CurrentCycle())
				assert.False(t, ok)
				// The veto drops the class decision; key-b is still cached, so the lookup reports
				// the class as unknown rather than the cache as empty.
				assert.Equal(t, equivalenceCacheMissUnknownClass, reason)
			}
		})
	}
}

func TestIneligiblePodDoesNotBackfill(t *testing.T) {
	_, ctx := ktesting.NewTestContext(t)
	plugin := &eligibilityPlugin{status: fwktype.NewStatus(fwktype.Unschedulable, "veto")}
	h := newEligibilityTestSuit(t, ctx, plugin, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "4", "8Gi"))
	s := h.scheduling
	fwk := h.sched.Profiles["koord-scheduler"]

	result, err := h.sched.SchedulePod(ctx, fwk, framework.NewCycleState(), makeClassPod("vetoed", "key-a"))
	require.NoError(t, err)
	assert.Equal(t, 2, result.FeasibleNodes)
	assert.Empty(t, s.equivalence.entries, "a vetoed pod must not build a decision")

	// Once the pod is eligible again, the class is backfilled and then reused.
	plugin.status = nil
	result, err = h.sched.SchedulePod(ctx, fwk, framework.NewCycleState(), makeClassPod("backfill", "key-a"))
	require.NoError(t, err)
	assert.Equal(t, 2, result.FeasibleNodes)
	require.Len(t, s.equivalence.entries, 1)
	hits := hitCount(t, fwk.ProfileName())
	_, err = h.sched.SchedulePod(ctx, fwk, framework.NewCycleState(), makeClassPod("reuse", "key-a"))
	require.NoError(t, err)
	assert.Equal(t, hits+1, hitCount(t, fwk.ProfileName()))
}

// replacedLister stands for a snapshot lister a profile swapped in for the scheduler's own
// *cache.Snapshot, which the equivalence path cannot refresh.
type replacedLister struct {
	fwktype.SharedLister
}

func TestUnsupportedSnapshotListerFallsBackToUpstream(t *testing.T) {
	_, ctx := ktesting.NewTestContext(t)
	plugin := &eligibilityPlugin{status: fwktype.AsStatus(errors.New("eligibility must not be consulted"))}
	h := newEligibilityTestSuitWithLister(t, ctx, plugin, replacedLister{cache.NewEmptySnapshot()},
		makeNode("node-1", "4", "8Gi"), makeNode("node-2", "4", "8Gi"))
	fwk := h.sched.Profiles["koord-scheduler"]
	declined := koordmetrics.EquivalenceClassMisses.WithLabelValues(fwk.ProfileName(), equivalenceCacheMissSnapshotUnsupported.String())
	before, err := testutil.GetCounterMetricValue(declined)
	require.NoError(t, err)

	// h.sched.SchedulePod is FrameworkExtenderFactory.scheduleOne, which hands a declined pod to the
	// upstream schedulePod (the stub configured by the suit) instead of failing it.
	result, err := h.sched.SchedulePod(ctx, fwk, framework.NewCycleState(), makeClassPod("pod", "key-a"))
	require.NoError(t, err)
	assert.Equal(t, scheduler.ScheduleResult{SuggestedHost: "node-1", EvaluatedNodes: 1, FeasibleNodes: 1}, result)
	after, err := testutil.GetCounterMetricValue(declined)
	require.NoError(t, err)
	assert.Equal(t, before+1, after)
	assert.Empty(t, h.scheduling.equivalence.entries)
}
