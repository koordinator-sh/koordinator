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

// capacityPreFilterPlugin reports a per-node reuse capacity computed per pod, so the current pod's
// capacity can differ from what the cached decision was built with.
type capacityPreFilterPlugin struct {
	testEquivalenceCapacityPlugin
}

func (p *capacityPreFilterPlugin) PreFilter(context.Context, fwktype.CycleState, *corev1.Pod, []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	return nil, nil
}

func (p *capacityPreFilterPlugin) PreFilterExtensions() fwktype.PreFilterExtensions { return nil }

// capacityTestSuit is newTestScheduling plus the FrameworkExtender wrapping, which is what makes
// EquivalenceCapacityPlugins visible to the equivalence path: the path discovers capacity plugins
// by asserting its framework is a FrameworkExtender (see equivalenceCapacityPlugins), so a bare
// framework silently runs without any of them.
type capacityTestSuit struct {
	scheduling *EquivalenceScheduling
	sched      *scheduler.Scheduler
}

func newCapacityTestSuit(t *testing.T, ctx context.Context, plugin fwktype.Plugin, nodes ...*corev1.Node) *capacityTestSuit {
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
		frameworkruntime.WithSnapshotSharedLister(cache.NewEmptySnapshot()),
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
		frameworkruntime.WithPodNominator(q),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fwk.Close()) })

	// Wrapping is what exposes EquivalenceCapacityPlugins to the equivalence path.
	ext := extenderFactory.NewFrameworkExtender(fwk)
	ext.SetConfiguredPlugins(fwk.ListPlugins())
	sched.Profiles["koord-scheduler"] = ext

	scheduling := NewEquivalenceScheduling(sched, nil, DefaultEquivalenceClassCacheSize, fakeClass)
	scheduling.equivalence = newEquivalenceClassCache(time.Hour, DefaultEquivalenceClassCacheSize)
	ext.RegisterSchedulingDecisionProvider(scheduling)
	extenderFactory.InitScheduler(&frameworkext.SchedulerAdapter{Scheduler: sched})

	return &capacityTestSuit{scheduling: scheduling, sched: sched}
}

func TestWarmCacheHonorsCurrentPodCapacity(t *testing.T) {
	for _, tt := range []struct {
		reason    equivalenceCacheMissReason
		reusable  bool
		evaluated int
	}{
		{reason: equivalenceCacheMissPluginVeto, evaluated: 3},
		{reason: equivalenceCacheMissQuotaExhausted, reusable: true, evaluated: 4},
	} {
		t.Run(tt.reason.String(), func(t *testing.T) {
			_, ctx := ktesting.NewTestContext(t)
			plugin := &capacityPreFilterPlugin{}
			h := newCapacityTestSuit(t, ctx, plugin, makeNode("node-1", "4", "8Gi"), makeNode("node-2", "4", "8Gi"))
			s := h.scheduling
			fwk := h.sched.Profiles["koord-scheduler"]

			_, err := h.sched.SchedulePod(ctx, fwk, framework.NewCycleState(), makeClassPod("first", "key-a"))
			require.NoError(t, err)
			require.Len(t, s.equivalence.entries, 1)

			// The current pod's plugin state can differ even when its class is already cached.
			plugin.handled = true
			plugin.reusable = tt.reusable
			counter := koordmetrics.EquivalenceClassMisses.WithLabelValues(fwk.ProfileName(), tt.reason.String())
			before, err := testutil.GetCounterMetricValue(counter)
			require.NoError(t, err)

			result, err := h.sched.SchedulePod(ctx, fwk, framework.NewCycleState(), makeClassPod("second", "key-a"))
			require.NoError(t, err)
			assert.Equal(t, 2, result.FeasibleNodes, "a current-pod capacity rejection must run full node selection")
			assert.Equal(t, tt.evaluated, result.EvaluatedNodes, "include failed fast attempts and full fallback")
			after, err := testutil.GetCounterMetricValue(counter)
			require.NoError(t, err)
			assert.Equal(t, before+1, after)
			assert.Empty(t, s.equivalence.entries, "a non-reusable backfill must not leave the old class cached")
		})
	}
}
