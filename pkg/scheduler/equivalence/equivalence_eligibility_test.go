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

// eligibilityTestSuit is a real framework wrapped by the FrameworkExtender, which is what makes
// EquivalenceEligibilityPlugins visible to the equivalence path: the path discovers eligibility
// plugins by asserting its framework is an EquivalenceAwareHandle (see checkEligibility), so a bare
// framework silently runs without any of them.
type eligibilityTestSuit struct {
	scheduling *EquivalenceScheduling
	sched      *scheduler.Scheduler
	base       framework.Framework
	fwk        framework.Framework
	snapshot   *cache.Snapshot
}

func newEligibilityTestSuit(t *testing.T, ctx context.Context, plugin fwktype.Plugin, nodes ...*corev1.Node) *eligibilityTestSuit {
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

	snapshot := cache.NewEmptySnapshot()
	sched := &scheduler.Scheduler{
		Cache:           schedulerCache,
		SchedulingQueue: q,
		NextPod:         q.Pop,
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
	base, err := schedulertesting.NewFramework(ctx, registeredPlugins, "koord-scheduler",
		frameworkruntime.WithClientSet(client),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithSnapshotSharedLister(snapshot),
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
		frameworkruntime.WithPodNominator(q),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, base.Close()) })

	// Wrapping is what exposes EquivalenceEligibilityPlugins to the equivalence path.
	ext := extenderFactory.NewFrameworkExtender(base)
	ext.SetConfiguredPlugins(base.ListPlugins())

	scheduling := NewEquivalenceScheduling(sched, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
	scheduling.equivalence = newEquivalenceClassCache(time.Hour, DefaultEquivalenceClassCacheSize)
	ext.(frameworkext.EquivalenceAwareHandle).SetReuseProvider(scheduling)
	ext.(frameworkext.EquivalenceAwareHandle).SetReuseRecorder(scheduling)

	return &eligibilityTestSuit{scheduling: scheduling, sched: sched, base: base, fwk: ext, snapshot: snapshot}
}

// refresh makes the framework's snapshot reflect the scheduler cache, as the upstream cycle does at
// the start of every scheduling attempt.
func (h *eligibilityTestSuit) refresh(t *testing.T, ctx context.Context) {
	t.Helper()
	require.NoError(t, h.sched.Cache.UpdateSnapshot(klog.FromContext(ctx), h.snapshot))
}

// reuse runs PreFilter, which ends by consulting the reuse provider, and returns the single node it
// narrowed the cycle to, or "" when the cycle still covers every node.
func (h *eligibilityTestSuit) reuse(ctx context.Context, pod *corev1.Pod) string {
	state := framework.NewCycleState()
	result, status, _ := h.fwk.RunPreFilterPlugins(ctx, state, pod)
	if !status.IsSuccess() || result.AllNodes() {
		return ""
	}
	for node := range result.NodeNames {
		h.scheduling.RecordResult(ctx, state, h.fwk, pod, node, nil, 0)
		return node
	}
	return ""
}

// backfill runs the three hooks a successful full cycle runs, so the class ends with a decision the
// next pod can reuse: PreFilter (which declines to narrow), the score capture, and the backfill.
func (h *eligibilityTestSuit) backfill(t *testing.T, ctx context.Context, pod *corev1.Pod, host string) {
	t.Helper()
	h.refresh(t, ctx)
	state := framework.NewCycleState()
	result, status, _ := h.fwk.RunPreFilterPlugins(ctx, state, pod)
	require.True(t, status.IsSuccess(), "PreFilter: %v", status.Message())
	require.True(t, result.AllNodes(), "the first pod of a class has nothing to reuse")
	h.scheduling.RecordScoreTable(state, pod, []fwktype.NodePluginScores{
		{Name: "node-1", TotalScore: 10},
		{Name: "node-2", TotalScore: 5},
	})
	h.scheduling.RecordResult(ctx, state, h.fwk, pod, host, nil, 0)
}

func (h *eligibilityTestSuit) missCount(t *testing.T, reason equivalenceCacheMissReason) float64 {
	t.Helper()
	value, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassMisses.WithLabelValues(h.fwk.ProfileName(), reason.String()))
	require.NoError(t, err)
	return value
}

func (h *eligibilityTestSuit) hitCount(t *testing.T) float64 {
	t.Helper()
	value, err := testutil.GetCounterMetricValue(koordmetrics.EquivalenceClassHits.WithLabelValues(h.fwk.ProfileName()))
	require.NoError(t, err)
	return value
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

			for _, key := range []string{"key-a", "key-b"} {
				h.backfill(t, ctx, makeClassPod("first-"+key, key), "node-1")
			}
			require.Len(t, h.scheduling.equivalence.entries, 2)

			plugin.status = tt.status
			hitsBefore := h.hitCount(t)
			var missesBefore float64
			if tt.missReason != "" {
				missesBefore = h.missCount(t, tt.missReason)
			}

			node := h.reuse(ctx, makeClassPod("second", "key-a"))

			if tt.missReason == "" {
				// The class was backfilled by a pod placed on node-1, which makes node-1 the placement
				// anchor; spread walks on to the next cached node.
				assert.Equal(t, "node-2", node, "an eligible pod reuses the cached decision")
				assert.Equal(t, hitsBefore+1, h.hitCount(t))
			} else {
				assert.Empty(t, node, "an ineligible pod leaves the candidate set untouched")
				assert.Equal(t, hitsBefore, h.hitCount(t))
				assert.Equal(t, missesBefore+1, h.missCount(t, tt.missReason))
			}

			assert.Contains(t, h.scheduling.equivalence.entries, equivalenceCacheKey(h.fwk.ProfileName(), "key-b"),
				"a pod's eligibility never touches other classes")
			keyA := equivalenceCacheKey(h.fwk.ProfileName(), "key-a")
			if tt.keepsClass {
				assert.Contains(t, h.scheduling.equivalence.entries, keyA)
			} else {
				assert.NotContains(t, h.scheduling.equivalence.entries, keyA)
			}
		})
	}
}

func TestIneligiblePodDoesNotBackfill(t *testing.T) {
	_, ctx := ktesting.NewTestContext(t)
	plugin := &eligibilityPlugin{status: fwktype.NewStatus(fwktype.Unschedulable, "veto")}
	h := newEligibilityTestSuit(t, ctx, plugin, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "4", "8Gi"))
	key := equivalenceCacheKey(h.fwk.ProfileName(), "key-a")

	// A vetoed pod runs the full cycle, but the class must not be built from its decision.
	h.refresh(t, ctx)
	state := framework.NewCycleState()
	require.Empty(t, h.scheduling.Reuse(ctx, state, h.fwk, makeClassPod("vetoed", "key-a")))
	h.scheduling.RecordScoreTable(state, makeClassPod("vetoed", "key-a"), []fwktype.NodePluginScores{{Name: "node-1", TotalScore: 10}})
	h.scheduling.RecordResult(ctx, state, h.fwk, makeClassPod("vetoed", "key-a"), "node-1", nil, 0)
	assert.Empty(t, h.scheduling.equivalence.entries, "a vetoed pod must not build a decision")

	// Once the pod is eligible again, the class is backfilled and then reused.
	plugin.status = nil
	h.backfill(t, ctx, makeClassPod("backfill", "key-a"), "node-1")
	require.Contains(t, h.scheduling.equivalence.entries, key)

	hits := h.hitCount(t)
	// The backfilling pod was placed on node-1, which makes node-1 the placement anchor; spread
	// walks on to the next cached node.
	assert.Equal(t, "node-2", h.reuse(ctx, makeClassPod("reuse", "key-a")))
	assert.Equal(t, hits+1, h.hitCount(t))
}

// replacedLister stands for a snapshot lister a profile swapped in for the scheduler's own one. The
// narrowing reads whatever lister the profile exposes, so a replaced lister that does not hold the
// cached node must be reported, not silently ignored.
type replacedLister struct {
	fwktype.SharedLister
}

// listerFramework overrides the snapshot lister of the wrapped framework.
type listerFramework struct {
	framework.Framework
	lister fwktype.SharedLister
}

func (f *listerFramework) SnapshotSharedLister() fwktype.SharedLister {
	return f.lister
}

func TestReplacedSnapshotListerIsUsed(t *testing.T) {
	_, ctx := ktesting.NewTestContext(t)
	h := newEligibilityTestSuit(t, ctx, &eligibilityPlugin{}, makeNode("node-1", "4", "8Gi"))
	h.refresh(t, ctx)
	h.scheduling.equivalence.store(equivalenceCacheKey(h.fwk.ProfileName(), "key-a"), quotaNodes(1, "node-1"), h.sched.CurrentCycle(), nil, PlacementSpread)

	// The narrowing reads the lister of the framework it is handed, not the scheduler's own. Here that
	// lister does not hold the cached node, so the candidate cannot be resolved and the pod must be
	// reported as a snapshot miss rather than narrowed to a node nothing validated.
	empty := &listerFramework{Framework: h.base, lister: &replacedLister{SharedLister: cache.NewEmptySnapshot()}}
	hits := h.hitCount(t)
	missesBefore := h.missCount(t, equivalenceCacheMissSnapshotError)

	assert.Empty(t, h.scheduling.Reuse(ctx, framework.NewCycleState(), empty, makeClassPod("pod", "key-a")))
	assert.Equal(t, hits, h.hitCount(t))
	assert.Equal(t, missesBefore+1, h.missCount(t, equivalenceCacheMissSnapshotError))
}
