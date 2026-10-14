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

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/events"
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
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/bindinglimiter"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/equivalence"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func TestIsSandboxPod(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "nil pod",
			pod:  nil,
			want: false,
		},
		{
			name: "pod with sandbox label true",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelSandbox: "true"}}},
			want: true,
		},
		{
			name: "pod with sandbox label false",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelSandbox: "false"}}},
			want: false,
		},
		{
			name: "pod without sandbox label",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"other-label": "true"}}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsSandboxPod(tt.pod))
		})
	}
}

func TestGetSandboxTemplateHash(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want string
	}{
		{
			name: "nil pod",
			pod:  nil,
			want: "",
		},
		{
			name: "pod with a template hash",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelSandboxTemplateHash: "hash-a"}}},
			want: "hash-a",
		},
		{
			name: "pod without a template hash",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelSandbox: "true"}}},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetSandboxTemplateHash(tt.pod))
		})
	}
}

func TestIsSandboxActive(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "nil pod",
			pod:  nil,
			want: false,
		},
		{
			name: "sandbox pod with a template hash is active",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelSandbox: "true", LabelSandboxTemplateHash: "hash-a"}}},
			want: true,
		},
		{
			name: "sandbox pod without a template hash is not active",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelSandbox: "true", LabelSandboxTemplateHash: ""}}},
			want: false,
		},
		{
			name: "template hash without the sandbox label is not active",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelSandboxTemplateHash: "hash-a"}}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsSandboxActive(tt.pod))
		})
	}
}

func TestSandboxClass(t *testing.T) {
	var class frameworkext.EquivalenceClass = sandboxClass{}

	active := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelSandbox: "true", LabelSandboxTemplateHash: "hash-a"}}}
	assert.True(t, class.Handles(active))
	assert.Equal(t, "hash-a", class.Key(active))

	assert.False(t, class.Handles(nil))
	assert.Equal(t, "", class.Key(nil))

	unhashed := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{LabelSandbox: "true"}}}
	assert.False(t, class.Handles(unhashed), "the class must not group pods it has no identity for")
	assert.Equal(t, "", class.Key(unhashed))
}

func TestAddFlags(t *testing.T) {
	originalMax, originalCache := MaxConcurrentBindings, EquivalenceCacheSize
	t.Cleanup(func() {
		MaxConcurrentBindings = originalMax
		EquivalenceCacheSize = originalCache
	})
	MaxConcurrentBindings = bindinglimiter.DefaultMaxConcurrentBindings
	EquivalenceCacheSize = equivalence.DefaultEquivalenceClassCacheSize

	fs := pflag.NewFlagSet("sandbox", pflag.ContinueOnError)
	AddFlags(fs)
	require.NoError(t, fs.Set("sandbox-max-concurrent-bindings", "256"))
	require.NoError(t, fs.Set("sandbox-equivalence-cache-size", "32"))

	assert.Equal(t, 256, MaxConcurrentBindings)
	assert.Equal(t, 32, EquivalenceCacheSize)
}

// setupTestSuit drives a real scheduler with two profiles, the minimum Setup needs to wire its
// modules onto each profile's FrameworkExtender.
type setupTestSuit struct {
	sched     *scheduler.Scheduler
	informers informers.SharedInformerFactory
}

func newSetupTestSuit(t *testing.T, ctx context.Context) *setupTestSuit {
	t.Helper()
	metrics.Register()
	client := kubefake.NewSimpleClientset()
	koordClient := koordfake.NewSimpleClientset()
	factory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClient),
		frameworkext.WithKoordinatorSharedInformerFactory(koordinatorinformers.NewSharedInformerFactory(koordClient, 0)),
	)
	require.NoError(t, err)
	informerFactory := informers.NewSharedInformerFactory(client, 0)

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
	}

	plugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterPluginAsExtensions(noderesources.Name,
			frameworkruntime.FactoryAdapter(plfeature.Features{}, noderesources.NewFit), "PreFilter", "Filter", "Score"),
	}
	for _, name := range []string{"koord-scheduler", "other-scheduler"} {
		fwk, err := schedulertesting.NewFramework(ctx, plugins, name,
			frameworkruntime.WithClientSet(client),
			frameworkruntime.WithInformerFactory(informerFactory),
			frameworkruntime.WithSnapshotSharedLister(cache.NewEmptySnapshot()),
			frameworkruntime.WithEventRecorder(events.NewFakeRecorder(100)),
			frameworkruntime.WithPodNominator(q),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, fwk.Close()) })
		ext := factory.NewFrameworkExtender(fwk)
		ext.SetConfiguredPlugins(fwk.ListPlugins())
		sched.Profiles[name] = ext
	}
	factory.InitScheduler(&frameworkext.SchedulerAdapter{Scheduler: sched})

	return &setupTestSuit{sched: sched, informers: informerFactory}
}

func (s *setupTestSuit) requireWired(t *testing.T) {
	t.Helper()
	for name, fwk := range s.sched.Profiles {
		ext, ok := fwk.(frameworkext.FrameworkExtender)
		require.True(t, ok, name)
		require.Len(t, ext.GetSchedulingDecisionProviders(), 1, "profile %s must get the equivalence path", name)
		require.NotNil(t, ext, name)
	}
}

func TestSetupWiresEveryProfile(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.EnableSandboxEquivalenceScheduling, true)()

	originalMax, originalCache := MaxConcurrentBindings, EquivalenceCacheSize
	t.Cleanup(func() {
		MaxConcurrentBindings = originalMax
		EquivalenceCacheSize = originalCache
	})

	_, ctx := ktesting.NewTestContext(t)
	s := newSetupTestSuit(t, ctx)
	percentage := int32(5)
	require.NoError(t, Setup(s.sched, s.informers, &percentage))
	s.requireWired(t)
}

func TestSetupDoesNothingWhenDisabled(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.EnableSandboxEquivalenceScheduling, false)()

	// Even nonsensical flags must be ignored: nothing is validated or wired when the gate is off.
	originalMax, originalCache := MaxConcurrentBindings, EquivalenceCacheSize
	t.Cleanup(func() {
		MaxConcurrentBindings = originalMax
		EquivalenceCacheSize = originalCache
	})
	MaxConcurrentBindings = 0
	EquivalenceCacheSize = 0

	_, ctx := ktesting.NewTestContext(t)
	s := newSetupTestSuit(t, ctx)
	require.NoError(t, Setup(s.sched, s.informers, nil))
	for name, fwk := range s.sched.Profiles {
		ext := fwk.(frameworkext.FrameworkExtender)
		assert.Empty(t, ext.GetSchedulingDecisionProviders(), "profile %s must stay on the upstream path", name)
	}
}

func TestSetupRejectsInvalidConfig(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.EnableSandboxEquivalenceScheduling, true)()

	originalMax, originalCache := MaxConcurrentBindings, EquivalenceCacheSize
	t.Cleanup(func() {
		MaxConcurrentBindings = originalMax
		EquivalenceCacheSize = originalCache
	})

	tests := []struct {
		name    string
		setup   func()
		wantErr string
	}{
		{
			name:    "zero max concurrent bindings",
			setup:   func() { MaxConcurrentBindings = 0 },
			wantErr: "sandbox max concurrent bindings must be greater than 0",
		},
		{
			name:    "zero equivalence cache size",
			setup:   func() { EquivalenceCacheSize = 0 },
			wantErr: "sandbox equivalence cache size must be greater than 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			MaxConcurrentBindings = originalMax
			EquivalenceCacheSize = originalCache
			tt.setup()

			_, ctx := ktesting.NewTestContext(t)
			s := newSetupTestSuit(t, ctx)
			err := Setup(s.sched, s.informers, nil)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestSetupRejectsInlineBatchSchedule(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.EnableSandboxEquivalenceScheduling, true)()
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.EnableInlineBatchSchedule, true)()

	_, ctx := ktesting.NewTestContext(t)
	s := newSetupTestSuit(t, ctx)
	err := Setup(s.sched, s.informers, nil)
	require.Error(t, err, "the sandbox path and inline batch schedule must be mutually exclusive")
	assert.Contains(t, err.Error(), string(koordfeatures.EnableSandboxEquivalenceScheduling))
	assert.Contains(t, err.Error(), string(koordfeatures.EnableInlineBatchSchedule))
}
