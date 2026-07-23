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

package scaledownbinpack

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	frameworkapi "github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	frameworkruntime "github.com/koordinator-sh/koordinator/pkg/descheduler/framework/runtime"
	frameworktesting "github.com/koordinator-sh/koordinator/pkg/descheduler/framework/testing"
)

// ---- test helpers ----

// recordingEvictor records evicted pods and allows all evictions by default.
type recordingEvictor struct {
	evicted    []*corev1.Pod
	filterFunc func(*corev1.Pod) bool
}

func (r *recordingEvictor) Name() string { return "RecordingEvictor" }
func (r *recordingEvictor) Filter(pod *corev1.Pod) bool {
	if r.filterFunc != nil {
		return r.filterFunc(pod)
	}
	return true
}
func (r *recordingEvictor) PreEvictionFilter(_ *corev1.Pod) bool { return true }
func (r *recordingEvictor) Evict(_ context.Context, pod *corev1.Pod, _ frameworkapi.EvictOptions) bool {
	r.evicted = append(r.evicted, pod)
	return true
}

func registerRecordingEvictor(rec **recordingEvictor) frameworktesting.RegisterPluginFunc {
	return func(reg *frameworkruntime.Registry, profile *deschedulerconfig.DeschedulerProfile) {
		var inst recordingEvictor
		*rec = &inst
		reg.Register((*rec).Name(), func(_ context.Context, _ runtime.Object, _ frameworkapi.Handle) (frameworkapi.Plugin, error) {
			return *rec, nil
		})
		profile.Plugins.Evict.Enabled = append(profile.Plugins.Evict.Enabled, deschedulerconfig.Plugin{Name: (*rec).Name()})
		profile.Plugins.Filter.Enabled = append(profile.Plugins.Filter.Enabled, deschedulerconfig.Plugin{Name: (*rec).Name()})
	}
}

// podsLister is a fake cached pod index. Tests verify that Balance() uses this
// function instead of calling the kube apiserver.
type podsLister struct {
	data        map[string][]*corev1.Pod
	called      int
	calledNodes []string
}

func (p *podsLister) fn() frameworkapi.GetPodsAssignedToNodeFunc {
	return func(nodeName string, filter frameworkapi.FilterFunc) ([]*corev1.Pod, error) {
		p.called++
		p.calledNodes = append(p.calledNodes, nodeName)
		pods := append([]*corev1.Pod(nil), p.data[nodeName]...)
		if filter != nil {
			filtered := make([]*corev1.Pod, 0, len(pods))
			for _, pod := range pods {
				if filter(pod) {
					filtered = append(filtered, pod)
				}
			}
			pods = filtered
		}
		return pods, nil
	}
}

func makeTestHandle(t *testing.T, lister *podsLister, opts ...frameworkruntime.Option) (frameworkapi.Handle, *recordingEvictor) {
	t.Helper()
	var rec *recordingEvictor
	fns := []frameworktesting.RegisterPluginFunc{registerRecordingEvictor(&rec)}

	var objs []runtime.Object
	if lister != nil {
		for _, pods := range lister.data {
			for _, pod := range pods {
				objs = append(objs, pod.DeepCopy())
			}
		}
		opts = append(opts, frameworkruntime.WithGetPodsAssignedToNodeFunc(lister.fn()))
	}
	client := kubefake.NewSimpleClientset(objs...)
	opts = append(opts, frameworkruntime.WithClientSet(client))

	h, err := frameworktesting.NewFramework(fns, "test", opts...)
	if err != nil {
		t.Fatalf("failed creating framework: %v", err)
	}
	return h, rec
}

func newTestNode(name string, labels map[string]string, cpuStr, memStr string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuStr),
				corev1.ResourceMemory: resource.MustParse(memStr),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuStr),
				corev1.ResourceMemory: resource.MustParse(memStr),
			},
		},
	}
}

func newTestPod(ns, name, nodeName, cpu, mem string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         ns,
			Name:              name,
			Labels:            labels,
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{
				{
					Name: name,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(cpu),
							corev1.ResourceMemory: resource.MustParse(mem),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func defaultArgs() *deschedulerconfig.ScaleDownBinPackArgs {
	return &deschedulerconfig.ScaleDownBinPackArgs{
		Strategy:  deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly,
		Resources: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
		ResourceWeights: map[corev1.ResourceName]float64{
			corev1.ResourceCPU:    1.0,
			corev1.ResourceMemory: 1.0,
		},
	}
}

// ---- NewScaleDownBinPack ----

func TestNewScaleDownBinPack_TypeCheck(t *testing.T) {
	lister := &podsLister{data: map[string][]*corev1.Pod{}}
	fh, _ := makeTestHandle(t, lister)

	// Wrong argument type must fail.
	_, err := NewScaleDownBinPack(context.Background(), &runtime.Unknown{}, fh)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "want args to be of type ScaleDownBinPackArgs")
}

func TestNewScaleDownBinPack_InvalidArgs(t *testing.T) {
	lister := &podsLister{data: map[string][]*corev1.Pod{}}
	fh, _ := makeTestHandle(t, lister)

	// Invalid strategy must be rejected by validation.
	args := &deschedulerconfig.ScaleDownBinPackArgs{
		Strategy: "InvalidStrategy",
	}
	_, err := NewScaleDownBinPack(context.Background(), args, fh)
	assert.Error(t, err)
}

func TestNewScaleDownBinPack_ValidArgs(t *testing.T) {
	lister := &podsLister{data: map[string][]*corev1.Pod{}}
	fh, _ := makeTestHandle(t, lister)

	plg, err := NewScaleDownBinPack(context.Background(), defaultArgs(), fh)
	assert.NoError(t, err)
	assert.Equal(t, ScaleDownBinPackName, plg.Name())
}

// ---- Paused ----

func TestBalance_Paused(t *testing.T) {
	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {newTestPod("default", "p1", "node1", "100m", "128Mi", nil)},
	}}
	fh, _ := makeTestHandle(t, lister)

	args := defaultArgs()
	args.Paused = true
	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      args,
		podFilter: func(_ *corev1.Pod) bool { return true },
	}

	nodes := []*corev1.Node{newTestNode("node1", nil, "4", "8Gi")}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	// The cached pod function must not be called when paused.
	assert.Equal(t, 0, lister.called, "cached pod function should not be called when paused")
	assert.Empty(t, pl.GetRankedPods(), "no ranking when paused")
}

// ---- Cached Pod Function is Called (No API Server Calls) ----

func TestBalance_UsesCachedPodFunction(t *testing.T) {
	targetPod := newTestPod("default", "target1", "node1", "500m", "256Mi", map[string]string{"app": "web"})
	nonTargetPod := newTestPod("default", "system1", "node1", "100m", "64Mi", map[string]string{"app": "system"})

	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {targetPod, nonTargetPod},
	}}
	fh, _ := makeTestHandle(t, lister)

	args := defaultArgs()
	// podFilter passes all pods (both target and non-target appear as eligible targets
	// since no PodSelectors are configured).
	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      args,
		podFilter: func(_ *corev1.Pod) bool { return true },
	}

	nodes := []*corev1.Node{newTestNode("node1", nil, "4", "8Gi")}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	// The fake cached function must have been called exactly once for node1.
	assert.Equal(t, 1, lister.called, "cached pod function must be called once per node")
	assert.Equal(t, []string{"node1"}, lister.calledNodes)
	// Results should be ranked.
	assert.NotEmpty(t, pl.GetRankedPods())
}

func TestBalance_CachedFunctionCalledPerNode(t *testing.T) {
	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {newTestPod("default", "p1", "node1", "100m", "64Mi", nil)},
		"node2": {newTestPod("default", "p2", "node2", "200m", "128Mi", nil)},
		"node3": {newTestPod("default", "p3", "node3", "300m", "256Mi", nil)},
	}}
	fh, _ := makeTestHandle(t, lister)

	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      defaultArgs(),
		podFilter: func(_ *corev1.Pod) bool { return true },
	}

	nodes := []*corev1.Node{
		newTestNode("node1", nil, "4", "8Gi"),
		newTestNode("node2", nil, "4", "8Gi"),
		newTestNode("node3", nil, "4", "8Gi"),
	}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	assert.Equal(t, 3, lister.called, "cached pod function must be called once per node")
}

// ---- Node Target Counts Match Cached Pods ----

func TestBalance_NodeTargetCountsMatchCachedPods(t *testing.T) {
	// node1: 2 eligible target pods (pass filter), 1 non-target pod (fails filter).
	// node2: 1 eligible target pod.
	// node3: 0 eligible target pods (only non-target) → skipped entirely.
	targetLabel := map[string]string{"app": "target"}
	nonTargetLabel := map[string]string{"app": "other"}

	t1a := newTestPod("default", "t1a", "node1", "500m", "256Mi", targetLabel)
	t1b := newTestPod("default", "t1b", "node1", "300m", "128Mi", targetLabel)
	nt1 := newTestPod("default", "nt1", "node1", "100m", "64Mi", nonTargetLabel)
	t2a := newTestPod("default", "t2a", "node2", "200m", "128Mi", targetLabel)
	nt3 := newTestPod("default", "nt3", "node3", "400m", "256Mi", nonTargetLabel)

	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {t1a, t1b, nt1},
		"node2": {t2a},
		"node3": {nt3},
	}}
	fh, _ := makeTestHandle(t, lister)

	// podFilter only allows pods with app=target label.
	targetFilter := func(pod *corev1.Pod) bool {
		return pod.Labels["app"] == "target"
	}

	args := defaultArgs()
	args.PodSelectors = []deschedulerconfig.ScaleDownBinPackPodSelector{
		{Name: "targets", Selector: &metav1.LabelSelector{MatchLabels: targetLabel}},
	}

	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      args,
		podFilter: targetFilter,
	}

	nodes := []*corev1.Node{
		newTestNode("node1", nil, "4", "8Gi"),
		newTestNode("node2", nil, "4", "8Gi"),
		newTestNode("node3", nil, "4", "8Gi"),
	}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	ranked := pl.GetRankedPods()
	// Only 3 eligible target pods: t1a, t1b, t2a.
	assert.Equal(t, 3, len(ranked), "only eligible target pods should be ranked")

	// Verify all ranked pods are targets.
	rankedNames := make(map[string]bool)
	for _, rp := range ranked {
		rankedNames[rp.Pod.Name] = true
		assert.Equal(t, "target", rp.Pod.Labels["app"], "ranked pod must be a target")
	}
	assert.True(t, rankedNames["t1a"])
	assert.True(t, rankedNames["t1b"])
	assert.True(t, rankedNames["t2a"])

	// Non-target pods and node3 must not appear.
	assert.False(t, rankedNames["nt1"])
	assert.False(t, rankedNames["nt3"])
	for _, rp := range ranked {
		assert.NotEqual(t, "node3", rp.NodeName, "node with zero eligible targets must be skipped")
	}
}

// ---- Node Selector Filtering ----

func TestBalance_NodeSelectorFiltering(t *testing.T) {
	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {newTestPod("default", "p1", "node1", "100m", "64Mi", nil)},
		"node2": {newTestPod("default", "p2", "node2", "100m", "64Mi", nil)},
	}}
	fh, _ := makeTestHandle(t, lister)

	args := defaultArgs()
	args.NodeSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"zone": "us-east"}}

	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      args,
		podFilter: func(_ *corev1.Pod) bool { return true },
	}

	nodes := []*corev1.Node{
		newTestNode("node1", map[string]string{"zone": "us-east"}, "4", "8Gi"),
		newTestNode("node2", map[string]string{"zone": "eu-west"}, "4", "8Gi"),
	}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	// Only node1 matches the selector; node2 must be skipped entirely (no call).
	assert.Equal(t, 1, lister.called, "only matching nodes should be queried")
	assert.Equal(t, []string{"node1"}, lister.calledNodes)

	ranked := pl.GetRankedPods()
	assert.Equal(t, 1, len(ranked))
	assert.Equal(t, "node1", ranked[0].NodeName)
}

func TestBalance_NilNodeSelectorAllNodes(t *testing.T) {
	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {newTestPod("default", "p1", "node1", "100m", "64Mi", nil)},
		"node2": {newTestPod("default", "p2", "node2", "100m", "64Mi", nil)},
	}}
	fh, _ := makeTestHandle(t, lister)

	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      defaultArgs(),
		podFilter: func(_ *corev1.Pod) bool { return true },
	}

	nodes := []*corev1.Node{
		newTestNode("node1", nil, "4", "8Gi"),
		newTestNode("node2", nil, "4", "8Gi"),
	}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	assert.Equal(t, 2, lister.called, "all nodes should be queried when NodeSelector is nil")
}

// ---- Completed Pods Skipped ----

func TestBalance_SkipsCompletedPods(t *testing.T) {
	succeededPod := newTestPod("default", "done", "node1", "100m", "64Mi", nil)
	succeededPod.Status.Phase = corev1.PodSucceeded
	failedPod := newTestPod("default", "fail", "node1", "100m", "64Mi", nil)
	failedPod.Status.Phase = corev1.PodFailed
	runningPod := newTestPod("default", "running", "node1", "100m", "64Mi", nil)

	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {succeededPod, failedPod, runningPod},
	}}
	fh, _ := makeTestHandle(t, lister)

	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      defaultArgs(),
		podFilter: func(_ *corev1.Pod) bool { return true },
	}

	nodes := []*corev1.Node{newTestNode("node1", nil, "4", "8Gi")}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	ranked := pl.GetRankedPods()
	assert.Equal(t, 1, len(ranked), "only running pod should be ranked")
	assert.Equal(t, "running", ranked[0].Pod.Name)
}

// ---- Evictor Filter Integration ----

func TestBalance_EvictorFilterBlocksPods(t *testing.T) {
	// The evictor blocks pod "blocked" but allows "allowed".
	allowed := newTestPod("default", "allowed", "node1", "200m", "128Mi", nil)
	blocked := newTestPod("default", "blocked", "node1", "200m", "128Mi", nil)

	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {allowed, blocked},
	}}
	fh, rec := makeTestHandle(t, lister)

	// Configure the evictor to block the "blocked" pod.
	rec.filterFunc = func(pod *corev1.Pod) bool {
		return pod.Name != "blocked"
	}

	// Rebuild the plugin with the evictor-wrapping podFilter as the real
	// constructor does.
	plg, err := NewScaleDownBinPack(context.Background(), defaultArgs(), fh)
	assert.NoError(t, err)

	pl := plg.(*ScaleDownBinPack)
	nodes := []*corev1.Node{newTestNode("node1", nil, "4", "8Gi")}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	ranked := pl.GetRankedPods()
	assert.Equal(t, 1, len(ranked), "only allowed pod should be ranked")
	assert.Equal(t, "allowed", ranked[0].Pod.Name)
}

// ---- No Eligible Pods ----

func TestBalance_NoEligiblePods(t *testing.T) {
	// All pods are blocked by the filter.
	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {newTestPod("default", "p1", "node1", "100m", "64Mi", nil)},
	}}
	fh, _ := makeTestHandle(t, lister)

	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      defaultArgs(),
		podFilter: func(_ *corev1.Pod) bool { return false },
	}

	nodes := []*corev1.Node{newTestNode("node1", nil, "4", "8Gi")}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	assert.Empty(t, pl.GetRankedPods(), "no ranked pods when none are eligible")
}

// ---- No Nodes ----

func TestBalance_NoNodes(t *testing.T) {
	lister := &podsLister{data: map[string][]*corev1.Pod{}}
	fh, _ := makeTestHandle(t, lister)

	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      defaultArgs(),
		podFilter: func(_ *corev1.Pod) bool { return true },
	}

	status := pl.Balance(context.Background(), []*corev1.Node{})

	assert.Nil(t, status)
	assert.Equal(t, 0, lister.called, "no nodes means no cached pod calls")
}

// ---- Ranks Are Stored Internally (No Execution) ----

func TestBalance_RanksStoredInternally_NoExecution(t *testing.T) {
	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {
			newTestPod("default", "p1", "node1", "500m", "256Mi", nil),
			newTestPod("default", "p2", "node1", "300m", "128Mi", nil),
		},
		"node2": {
			newTestPod("default", "p3", "node2", "200m", "64Mi", nil),
		},
	}}
	fh, rec := makeTestHandle(t, lister)

	pl := &ScaleDownBinPack{
		handle:    fh,
		args:      defaultArgs(),
		podFilter: func(_ *corev1.Pod) bool { return true },
	}

	nodes := []*corev1.Node{
		newTestNode("node1", nil, "4", "8Gi"),
		newTestNode("node2", nil, "4", "8Gi"),
	}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	ranked := pl.GetRankedPods()
	assert.Equal(t, 3, len(ranked))

	// Ranks must be sequential starting from 0.
	for i, rp := range ranked {
		assert.Equal(t, i, rp.Rank, "rank %d must match position", i)
	}

	// No pods should have been evicted — Balance only ranks.
	assert.Empty(t, rec.evicted, "Balance must not evict any pods")
}

// ---- isTargetPod ----

func TestIsTargetPod(t *testing.T) {
	targetLabel := map[string]string{"app": "web"}
	otherLabel := map[string]string{"app": "system"}

	cases := []struct {
		name         string
		podSelectors []deschedulerconfig.ScaleDownBinPackPodSelector
		podLabels    map[string]string
		want         bool
	}{
		{
			name:         "no selectors means all pods are targets",
			podSelectors: nil,
			podLabels:    otherLabel,
			want:         true,
		},
		{
			name: "nil selector in entry means all pods are targets",
			podSelectors: []deschedulerconfig.ScaleDownBinPackPodSelector{
				{Name: "all", Selector: nil},
			},
			podLabels: otherLabel,
			want:      true,
		},
		{
			name: "matching selector",
			podSelectors: []deschedulerconfig.ScaleDownBinPackPodSelector{
				{Name: "web", Selector: &metav1.LabelSelector{MatchLabels: targetLabel}},
			},
			podLabels: targetLabel,
			want:      true,
		},
		{
			name: "non-matching selector",
			podSelectors: []deschedulerconfig.ScaleDownBinPackPodSelector{
				{Name: "web", Selector: &metav1.LabelSelector{MatchLabels: targetLabel}},
			},
			podLabels: otherLabel,
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pl := &ScaleDownBinPack{
				args: &deschedulerconfig.ScaleDownBinPackArgs{
					PodSelectors: tc.podSelectors,
				},
			}
			pod := newTestPod("default", "test", "node1", "100m", "64Mi", tc.podLabels)
			assert.Equal(t, tc.want, pl.isTargetPod(pod))
		})
	}
}

// ---- filterPods (package-level) ----

func TestFilterPods(t *testing.T) {
	cases := []struct {
		name      string
		selectors []deschedulerconfig.ScaleDownBinPackPodSelector
		podLabels map[string]string
		wantErr   bool
		want      bool
	}{
		{
			name:      "no selectors allows all",
			selectors: nil,
			podLabels: map[string]string{"x": "y"},
			want:      true,
		},
		{
			name: "matching selector",
			selectors: []deschedulerconfig.ScaleDownBinPackPodSelector{
				{Name: "web", Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
			},
			podLabels: map[string]string{"app": "web"},
			want:      true,
		},
		{
			name: "non-matching selector",
			selectors: []deschedulerconfig.ScaleDownBinPackPodSelector{
				{Name: "web", Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
			},
			podLabels: map[string]string{"app": "db"},
			want:      false,
		},
		{
			name: "invalid selector returns error",
			selectors: []deschedulerconfig.ScaleDownBinPackPodSelector{
				{Name: "bad", Selector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "app", Operator: "Invalid"},
					},
				}},
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn, err := filterPods(tc.selectors)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			pod := newTestPod("default", "p", "node1", "100m", "64Mi", tc.podLabels)
			assert.Equal(t, tc.want, fn(pod))
		})
	}
}

// ---- filterNodes ----

func TestFilterNodes(t *testing.T) {
	nodes := []*corev1.Node{
		newTestNode("node1", map[string]string{"zone": "a"}, "4", "8Gi"),
		newTestNode("node2", map[string]string{"zone": "b"}, "4", "8Gi"),
	}

	cases := []struct {
		name     string
		selector *metav1.LabelSelector
		wantLen  int
	}{
		{name: "nil selector returns all", selector: nil, wantLen: 2},
		{name: "match one", selector: &metav1.LabelSelector{MatchLabels: map[string]string{"zone": "a"}}, wantLen: 1},
		{name: "match none", selector: &metav1.LabelSelector{MatchLabels: map[string]string{"zone": "c"}}, wantLen: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pl := &ScaleDownBinPack{args: &deschedulerconfig.ScaleDownBinPackArgs{NodeSelector: tc.selector}}
			got, err := pl.filterNodes(nodes)
			assert.NoError(t, err)
			assert.Equal(t, tc.wantLen, len(got))
		})
	}
}

// ---- Multi-Node Ranking Integration ----

func TestBalance_MultiNodeRankingIntegration(t *testing.T) {
	// node1: only target pods (score 0, perfect node)
	// node2: target + non-target pods (score > 0, multi-tenant)
	// Expect node1 targets to be ranked first (lower score).
	t1 := newTestPod("default", "target1", "node1", "500m", "256Mi", nil)
	t2 := newTestPod("default", "target2", "node2", "500m", "256Mi", nil)
	nt2 := newTestPod("default", "system2", "node2", "300m", "128Mi", nil)

	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {t1},
		"node2": {t2, nt2},
	}}
	fh, _ := makeTestHandle(t, lister)

	pl := &ScaleDownBinPack{
		handle: fh,
		args:   defaultArgs(),
		// podFilter allows all pods. Since no PodSelectors are set, isTargetPod
		// considers all pods as targets. The non-target classification depends on
		// the podFilter rejecting the pod. Here we selectively allow only the
		// target pods so nt2 becomes non-target.
		podFilter: func(pod *corev1.Pod) bool {
			return pod.Name != "system2"
		},
	}

	nodes := []*corev1.Node{
		newTestNode("node1", nil, "4", "8Gi"),
		newTestNode("node2", nil, "4", "8Gi"),
	}
	status := pl.Balance(context.Background(), nodes)

	assert.Nil(t, status)
	ranked := pl.GetRankedPods()
	assert.Equal(t, 2, len(ranked))

	// node1 has score 0 (perfect), so target1 should be ranked first.
	assert.Equal(t, "target1", ranked[0].Pod.Name, "perfect node target should rank first")
	assert.Equal(t, "node1", ranked[0].NodeName)
	assert.Equal(t, 0, ranked[0].Rank)

	// node2 has score > 0 (multi-tenant), so target2 ranks second.
	assert.Equal(t, "target2", ranked[1].Pod.Name)
	assert.Equal(t, "node2", ranked[1].NodeName)
	assert.Equal(t, 1, ranked[1].Rank)

	// Verify evacuation scores.
	assert.Equal(t, 0.0, ranked[0].EvacuationScore, "perfect node should have 0 score")
	assert.Greater(t, ranked[1].EvacuationScore, 0.0, "multi-tenant node should have positive score")
}
