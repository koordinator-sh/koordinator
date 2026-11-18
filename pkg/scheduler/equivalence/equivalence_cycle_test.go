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
	"slices"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/events"
	"k8s.io/component-base/metrics/testutil"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
	fwktype "k8s.io/kube-scheduler/framework"
	kubefeatures "k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	koordmetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
	featuregatetesting "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

type cycleHooks struct {
	preFilterCalls atomic.Int32
	preScoreCalls  atomic.Int32
	filterCalls    atomic.Int32
	scoreCalls     atomic.Int32
	preFilterError bool
	scoreError     bool
	resizeError    bool
	restricted     sets.Set[string]
	rejectNodes    map[string]fwktype.Code
	failFilterAt   int32
	checkClone     bool
}

type filterVisits struct{ count int }

func (s *filterVisits) Clone() fwktype.StateData {
	ns := *s
	return &ns
}

const filterVisitsKey fwktype.StateKey = "test-filter-visits"

func (p *cycleHooks) Name() string { return "CycleHooks" }
func (p *cycleHooks) PreFilter(_ context.Context, state fwktype.CycleState, _ *corev1.Pod, _ []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	p.preFilterCalls.Add(1)
	state.Write(filterVisitsKey, &filterVisits{})
	if p.preFilterError {
		return nil, fwktype.NewStatus(fwktype.Error, "injected prefilter failure")
	}
	if p.restricted != nil {
		return &fwktype.PreFilterResult{NodeNames: p.restricted}, nil
	}
	return nil, nil
}
func (p *cycleHooks) PreFilterExtensions() fwktype.PreFilterExtensions { return nil }
func (p *cycleHooks) Filter(_ context.Context, state fwktype.CycleState, _ *corev1.Pod, node fwktype.NodeInfo) *fwktype.Status {
	calls := p.filterCalls.Add(1)
	if p.checkClone {
		data, err := state.Read(filterVisitsKey)
		if err != nil {
			return fwktype.AsStatus(err)
		}
		visits := data.(*filterVisits)
		visits.count++
		if visits.count > 1 {
			return fwktype.NewStatus(fwktype.Error, "candidate precheck leaked into the cycle state")
		}
	}
	if calls == p.failFilterAt {
		return fwktype.NewStatus(fwktype.Error, "injected filter failure")
	}
	if code := p.rejectNodes[node.Node().Name]; code != fwktype.Success {
		return fwktype.NewStatus(code, "injected filter rejection")
	}
	return nil
}
func (p *cycleHooks) PreScore(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) *fwktype.Status {
	p.preScoreCalls.Add(1)
	return nil
}
func (p *cycleHooks) Score(_ context.Context, _ fwktype.CycleState, _ *corev1.Pod, n fwktype.NodeInfo) (int64, *fwktype.Status) {
	p.scoreCalls.Add(1)
	if p.scoreError {
		return 0, fwktype.NewStatus(fwktype.Error, "injected score failure")
	}
	return map[string]int64{"node-0": 30, "node-1": 20, "node-2": 10}[n.Node().Name], nil
}
func (p *cycleHooks) ScoreExtensions() fwktype.ScoreExtensions { return nil }
func (p *cycleHooks) SignPod(context.Context, *corev1.Pod) ([]fwktype.SignFragment, *fwktype.Status) {
	return nil, nil
}
func (p *cycleHooks) ResizePod(context.Context, fwktype.CycleState, *corev1.Pod, string) *fwktype.Status {
	if p.resizeError {
		return fwktype.NewStatus(fwktype.Error, "injected resize failure")
	}
	return nil
}

type cycleNominator struct {
	frameworkext.ReservationNominator
	fail bool
}

func (p *cycleNominator) ReservationNominate(context.Context, fwktype.CycleState, *corev1.Pod, string) *fwktype.Status {
	if p.fail {
		return fwktype.NewStatus(fwktype.Error, "injected nomination failure")
	}
	return nil
}

type schedulingSuit struct {
	ctx       context.Context
	logger    klog.Logger
	sched     *scheduler.Scheduler
	fwk       framework.Framework
	engine    *EquivalenceScheduling
	hooks     *cycleHooks
	nominator *cycleNominator
}

func newSchedulingSuit(t testing.TB, nodes ...*corev1.Node) *schedulingSuit {
	t.Helper()
	koordmetrics.Register()
	logger, ctx := ktesting.NewTestContext(t)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	kc := koordfake.NewSimpleClientset()
	nominator := &cycleNominator{}
	factory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(kc),
		frameworkext.WithKoordinatorSharedInformerFactory(koordinatorinformers.NewSharedInformerFactory(kc, 0)),
		frameworkext.WithReservationNominator(nominator),
	)
	require.NoError(t, err)
	hooks := &cycleHooks{}
	reg := frameworkruntime.Registry{hooks.Name(): frameworkext.PluginFactoryProxy(factory,
		func(context.Context, runtime.Object, fwktype.Handle) (fwktype.Plugin, error) { return hooks, nil })}
	fitArgs := &config.NodeResourcesFitArgs{ScoringStrategy: &config.ScoringStrategy{
		Type: config.LeastAllocated, Resources: []config.ResourceSpec{{Name: "cpu", Weight: 1}, {Name: "memory", Weight: 1}},
	}}
	profile := config.KubeSchedulerProfile{SchedulerName: t.Name(), Plugins: &config.Plugins{
		QueueSort: config.PluginSet{Enabled: []config.Plugin{{Name: "PrioritySort"}}},
		Bind:      config.PluginSet{Enabled: []config.Plugin{{Name: "DefaultBinder"}}},
		PreFilter: config.PluginSet{Enabled: []config.Plugin{{Name: "NodeResourcesFit"}, {Name: "NodePorts"}, {Name: hooks.Name()}}},
		Filter:    config.PluginSet{Enabled: []config.Plugin{{Name: "NodeResourcesFit"}, {Name: "NodePorts"}, {Name: hooks.Name()}}},
		PreScore:  config.PluginSet{Enabled: []config.Plugin{{Name: "NodeResourcesFit"}, {Name: hooks.Name()}}},
		Score:     config.PluginSet{Enabled: []config.Plugin{{Name: "NodeResourcesFit", Weight: 1}, {Name: hooks.Name(), Weight: 100}}},
	}, PluginConfig: []config.PluginConfig{{Name: "NodeResourcesFit", Args: fitArgs}}}
	client := kubefake.NewSimpleClientset()
	inf := informers.NewSharedInformerFactory(client, 0)
	sched, err := scheduler.New(ctx, client, inf, nil,
		func(string) events.EventRecorder { return events.NewFakeRecorder(100) },
		scheduler.WithProfiles(profile), scheduler.WithFrameworkOutOfTreeRegistry(reg), scheduler.WithParallelism(1))
	require.NoError(t, err)
	t.Cleanup(sched.SchedulingQueue.Close)
	base := sched.Profiles[profile.SchedulerName]
	t.Cleanup(func() { require.NoError(t, base.Close()) })
	ext := factory.GetExtender(profile.SchedulerName)
	require.NotNil(t, ext)
	ext.SetConfiguredPlugins(base.ListPlugins())
	sched.Profiles[profile.SchedulerName] = ext
	factory.InitScheduler(&frameworkext.SchedulerAdapter{Scheduler: sched})
	engine := NewEquivalenceScheduling(sched, DefaultEquivalenceClassCacheSize, PlacementSpread, fakeClass)
	ext.(frameworkext.EquivalenceAwareHandle).SetReuseProvider(engine)
	ext.(frameworkext.EquivalenceAwareHandle).SetReuseRecorder(engine)
	if len(nodes) == 0 {
		nodes = []*corev1.Node{makeNode("node-0", "4", "8Gi"), makeNode("node-1", "4", "8Gi"), makeNode("node-2", "4", "8Gi")}
	}
	for _, node := range nodes {
		sched.Cache.AddNode(logger, node)
	}
	return &schedulingSuit{ctx: ctx, logger: logger, sched: sched, fwk: ext, engine: engine, hooks: hooks, nominator: nominator}
}
func (h *schedulingSuit) pod(name, key, cpu string) *corev1.Pod {
	p := makeClassPod(name, key)
	p.Spec.SchedulerName = h.fwk.ProfileName()
	p.Spec.Containers = makeQuotaPod(cpu, "1Gi").Spec.Containers
	return p
}
func (h *schedulingSuit) schedule(t testing.TB, pod *corev1.Pod) (scheduler.ScheduleResult, error) {
	t.Helper()
	h.sched.SchedulingQueue.Add(h.logger, pod)
	info, err := h.sched.NextPod(h.logger)
	require.NoError(t, err)
	defer h.sched.SchedulingQueue.Done(info.Pod.UID)
	return h.sched.SchedulePod(h.ctx, h.fwk, framework.NewCycleState(), info.Pod)
}

func TestSchedulingCycleReuse(t *testing.T) {
	h := newSchedulingSuit(t)
	result, err := h.schedule(t, h.pod("first", "class", "1"))
	require.NoError(t, err)
	require.Equal(t, "node-0", result.SuggestedHost)
	key := equivalenceCacheKey(h.fwk.ProfileName(), "class")
	require.Len(t, h.engine.equivalence.entries[key].nodes, 3)
	result, err = h.schedule(t, h.pod("second", "class", "1"))
	require.NoError(t, err)
	require.Equal(t, "node-1", result.SuggestedHost)
	require.Equal(t, int32(2), h.hooks.preFilterCalls.Load())
	require.Equal(t, int32(3), h.hooks.scoreCalls.Load())
	require.Equal(t, int32(1), h.hooks.preScoreCalls.Load())
}

func TestFailedNominationDoesNotConsume(t *testing.T) {
	h := newSchedulingSuit(t)
	_, err := h.schedule(t, h.pod("first", "class", "1"))
	require.NoError(t, err)
	key := equivalenceCacheKey(h.fwk.ProfileName(), "class")
	entry := h.engine.equivalence.entries[key]
	consumed := entry.consumed
	hits := hitCount(t, h.fwk.ProfileName())
	h.nominator.fail = true
	_, err = h.schedule(t, h.pod("second", "class", "1"))
	require.ErrorContains(t, err, "injected nomination failure")
	require.Equal(t, consumed, entry.consumed, "a cycle rejected before Assume must not consume the class quota")
	require.Equal(t, hits, hitCount(t, h.fwk.ProfileName()))
}

func TestNativeHintChargesFinalHost(t *testing.T) {
	require.True(t, utilfeature.DefaultFeatureGate.Enabled(kubefeatures.OpportunisticBatching))
	h := newSchedulingSuit(t)
	ordinary := h.pod("ordinary", "", "4")
	result, err := h.schedule(t, ordinary)
	require.NoError(t, err)
	require.Equal(t, "node-0", result.SuggestedHost)
	ordinary.Spec.NodeName = result.SuggestedHost
	require.NoError(t, h.sched.Cache.AssumePod(h.logger, ordinary))
	key := equivalenceCacheKey(h.fwk.ProfileName(), "class")
	// A surviving class entry need not match the last ordinary pod's native batch score table.
	h.engine.equivalence.store(key, quotaNodes(10, "node-2", "node-1"), h.sched.CurrentCycle(), nil, PlacementPack)
	hits := hitCount(t, h.fwk.ProfileName())
	result, err = h.schedule(t, h.pod("member", "class", "4"))
	require.NoError(t, err)
	entry := h.engine.equivalence.entries[key]
	charged := entry.nodes[entry.last].name
	t.Logf("actual suggested host=%s, reuse charged node=%s, quotas=%v", result.SuggestedHost, charged, entry.nodes)
	require.Equal(t, charged, result.SuggestedHost, "reuse accounting must follow the final decision, not a PreFilter proposal")
	require.Equal(t, "node-1", result.SuggestedHost, "ordinary pods must retain native opportunistic batching")
	require.Equal(t, []equivalenceClassNode{{name: "node-2", quota: 10}, {name: "node-1", quota: 9}}, entry.nodes)
	require.Equal(t, hits, hitCount(t, h.fwk.ProfileName()), "a native hint overriding the proposal is not a reuse hit")
}

func TestPreFilterErrorDuration(t *testing.T) {
	h := newSchedulingSuit(t)
	h.hooks.preFilterError = true
	before, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "error"))
	require.NoError(t, err)
	_, err = h.schedule(t, h.pod("member", "class", "1"))
	require.ErrorContains(t, err, "injected prefilter failure")
	after, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "error"))
	require.NoError(t, err)
	require.Equal(t, before+1, after, "internal scheduling failures must not disappear from duration metrics")
}

func TestScoreErrorDuration(t *testing.T) {
	h := newSchedulingSuit(t)
	h.hooks.scoreError = true
	before, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "error"))
	require.NoError(t, err)
	unschedBefore, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "unschedulable"))
	require.NoError(t, err)
	_, err = h.schedule(t, h.pod("member", "class", "1"))
	require.ErrorContains(t, err, "injected score failure")
	after, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "error"))
	require.NoError(t, err)
	unschedAfter, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "unschedulable"))
	require.NoError(t, err)
	t.Logf("score error histogram delta: error=%d, unschedulable=%d", after-before, unschedAfter-unschedBefore)
	require.Equal(t, before+1, after)
	require.Equal(t, unschedBefore, unschedAfter)
}

func TestSecondFilterFailureDoesNotConsume(t *testing.T) {
	h := newSchedulingSuit(t)
	hits := hitCount(t, h.fwk.ProfileName())
	before, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "fast", "error"))
	require.NoError(t, err)
	key := equivalenceCacheKey(h.fwk.ProfileName(), "class")
	h.engine.equivalence.store(key, quotaNodes(10, "node-1", "node-2"), h.sched.CurrentCycle(), nil, PlacementSpread)
	h.hooks.failFilterAt = 2
	_, err = h.schedule(t, h.pod("member", "class", "1"))
	require.ErrorContains(t, err, "injected filter failure")
	require.Equal(t, int32(2), h.hooks.filterCalls.Load(), "upstream must recheck the proposed node")
	require.Zero(t, h.engine.equivalence.entries[key].consumed)
	require.Equal(t, hits, hitCount(t, h.fwk.ProfileName()))
	count, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "fast", "error"))
	require.NoError(t, err)
	require.Equal(t, before+1, count)
}

func TestCandidatePrecheckClonesCycleState(t *testing.T) {
	h := newSchedulingSuit(t)
	key := equivalenceCacheKey(h.fwk.ProfileName(), "class")
	h.engine.equivalence.store(key, quotaNodes(10, "node-1", "node-2"), h.sched.CurrentCycle(), nil, PlacementSpread)
	h.hooks.checkClone = true
	h.hooks.rejectNodes = map[string]fwktype.Code{"node-1": fwktype.Unschedulable}
	result, err := h.schedule(t, h.pod("member", "class", "1"))
	require.NoError(t, err)
	require.Equal(t, "node-2", result.SuggestedHost)
	require.Equal(t, int32(3), h.hooks.filterCalls.Load(), "two isolated prechecks and one upstream Filter")
	require.Equal(t, 1, h.engine.equivalence.entries[key].consumed)
}

func TestRejectedCandidatesFallBackToSingleFeasibleNode(t *testing.T) {
	h := newSchedulingSuit(t)
	key := equivalenceCacheKey(h.fwk.ProfileName(), "class")
	h.engine.equivalence.store(key, quotaNodes(10, "node-1", "node-2"), h.sched.CurrentCycle(), nil, PlacementSpread)
	h.hooks.rejectNodes = map[string]fwktype.Code{"node-1": fwktype.Unschedulable, "node-2": fwktype.Unschedulable}
	result, err := h.schedule(t, h.pod("member", "class", "1"))
	require.NoError(t, err)
	require.Equal(t, "node-0", result.SuggestedHost)
	require.Zero(t, h.hooks.scoreCalls.Load())
	entry := h.engine.equivalence.entries[key]
	require.Len(t, entry.nodes, 1)
	require.Equal(t, "node-0", entry.nodes[0].name)
	require.Equal(t, 1, entry.consumed)
}

func TestRestrictedAndNominatedCyclesDoNotBackfill(t *testing.T) {
	for _, nominated := range []bool{false, true} {
		t.Run(map[bool]string{false: "restricted", true: "nominated"}[nominated], func(t *testing.T) {
			h := newSchedulingSuit(t)
			before, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "success"))
			require.NoError(t, err)
			pod := h.pod("member", "class", "1")
			if nominated {
				pod.Status.NominatedNodeName = "node-1"
			} else {
				h.hooks.restricted = sets.New("node-1")
			}
			result, err := h.schedule(t, pod)
			require.NoError(t, err)
			require.Equal(t, "node-1", result.SuggestedHost)
			require.Empty(t, h.engine.equivalence.entries)
			count, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "success"))
			require.NoError(t, err)
			require.Equal(t, before+1, count)
		})
	}
}

func TestResizeFailureDoesNotConsume(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultMutableFeatureGate, features.ResizePod, true)()
	h := newSchedulingSuit(t)
	hits := hitCount(t, h.fwk.ProfileName())
	_, err := h.schedule(t, h.pod("first", "class", "1"))
	require.NoError(t, err)
	key := equivalenceCacheKey(h.fwk.ProfileName(), "class")
	entry := h.engine.equivalence.entries[key]
	consumed := entry.consumed
	h.hooks.resizeError = true
	_, err = h.schedule(t, h.pod("second", "class", "1"))
	require.ErrorContains(t, err, "injected resize failure")
	require.Equal(t, consumed, entry.consumed)
	require.Equal(t, hits, hitCount(t, h.fwk.ProfileName()))
}

func TestFitErrorDuration(t *testing.T) {
	h := newSchedulingSuit(t)
	before, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "unschedulable"))
	require.NoError(t, err)
	_, err = h.schedule(t, h.pod("oversized", "class", "5"))
	var fitError *framework.FitError
	require.ErrorAs(t, err, &fitError)
	after, err := testutil.GetHistogramMetricCount(koordmetrics.EquivalenceClassSchedulingDuration.WithLabelValues(h.fwk.ProfileName(), "full", "unschedulable"))
	require.NoError(t, err)
	require.Equal(t, before+1, after)
	require.Empty(t, h.engine.equivalence.entries)
}

func TestScoreTableDoesNotAliasUpstreamScores(t *testing.T) {
	h := newSchedulingSuit(t)
	snapshot, err := h.engine.updateSnapshot(h.logger, h.fwk)
	require.NoError(t, err)
	pod := h.pod("member", "class", "1")
	state := framework.NewCycleState()
	_, status, _ := h.fwk.RunPreFilterPlugins(h.ctx, state, pod)
	require.True(t, status.IsSuccess())
	nodes, err := snapshot.NodeInfos().List()
	require.NoError(t, err)
	require.True(t, h.fwk.RunPreScorePlugins(h.ctx, state, pod, nodes).IsSuccess())
	scores, status := h.fwk.RunScorePlugins(h.ctx, state, pod, nodes)
	require.True(t, status.IsSuccess())
	captured := slices.Clone(readReuseState(state).scores)
	scores[0].TotalScore = -1
	scores[0], scores[1] = scores[1], scores[0]
	require.Equal(t, captured, readReuseState(state).scores)
	upstream := slices.Clone(scores)
	h.engine.RecordResult(h.ctx, state, h.fwk, pod, "node-0", nil, 0)
	require.Equal(t, upstream, scores, "backfill sorting must not mutate upstream's table")
}
