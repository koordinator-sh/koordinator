/*
Copyright 2025 The Koordinator Authors.

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

package custompriority

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	frameworkapi "github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	frameworkruntime "github.com/koordinator-sh/koordinator/pkg/descheduler/framework/runtime"
	frameworktesting "github.com/koordinator-sh/koordinator/pkg/descheduler/framework/testing"
)

// ---- helpers ----

type recordingEvictor struct {
	evicted []*corev1.Pod
}

func (r *recordingEvictor) Name() string                         { return "RecordingEvictor" }
func (r *recordingEvictor) Filter(_ *corev1.Pod) bool            { return true }
func (r *recordingEvictor) PreEvictionFilter(_ *corev1.Pod) bool { return true }
func (r *recordingEvictor) Evict(_ context.Context, pod *corev1.Pod, _ frameworkapi.EvictOptions) bool {
	r.evicted = append(r.evicted, pod)
	return true
}

func registerRecordingEvictor(rec **recordingEvictor) frameworktesting.RegisterPluginFunc {
	return func(reg *frameworkruntime.Registry, profile *deschedulerconfig.DeschedulerProfile) {
		// instantiate one instance captured in tests via closure
		var inst recordingEvictor
		*rec = &inst
		reg.Register((*rec).Name(), func(_ runtime.Object, _ frameworkapi.Handle) (frameworkapi.Plugin, error) { return *rec, nil })
		profile.Plugins.Evict.Enabled = append(profile.Plugins.Evict.Enabled, deschedulerconfig.Plugin{Name: (*rec).Name()})
		profile.Plugins.Filter.Enabled = append(profile.Plugins.Filter.Enabled, deschedulerconfig.Plugin{Name: (*rec).Name()})
	}
}

type podsLister struct {
	// node -> pods
	data map[string][]*corev1.Pod
	// returnErr makes the lister return an error for any call
	returnErr error
}

func (p *podsLister) fn() frameworkapi.GetPodsAssignedToNodeFunc {
	return func(nodeName string, filter frameworkapi.FilterFunc) ([]*corev1.Pod, error) {
		if p.returnErr != nil {
			return nil, p.returnErr
		}
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

func newNode(name string, labels map[string]string, cpuMilli, memBytes, podsCap int64) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(memBytes, resource.BinarySI),
				corev1.ResourcePods:   *resource.NewQuantity(podsCap, resource.DecimalSI),
			},
		},
	}
}

func newPod(ns, name, node string, cpuMilli, memBytes int64, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels, UID: types.UID(ns + "/" + name)},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{
				{
					Name: name,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(memBytes, resource.BinarySI),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func makeHandle(t *testing.T, opts ...frameworkruntime.Option) (frameworkapi.Handle, *recordingEvictor) {
	t.Helper()
	var rec *recordingEvictor
	fns := []frameworktesting.RegisterPluginFunc{registerRecordingEvictor(&rec)}
	h, err := frameworktesting.NewFramework(fns, "test", opts...)
	if err != nil {
		t.Fatalf("failed creating framework: %v", err)
	}
	return h, rec
}

// ---- validateCustomPriorityArgs ----

func Test_validateCustomPriorityArgs(t *testing.T) {
	ls := func(k, v string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{k: v}}
	}
	cases := []struct {
		name    string
		args    deschedulerconfig.CustomPriorityArgs
		wantErr string
	}{
		{name: "too few priorities", args: deschedulerconfig.CustomPriorityArgs{EvictionOrder: nil}, wantErr: "at least 2"},
		{name: "duplicate names", args: deschedulerconfig.CustomPriorityArgs{EvictionOrder: []deschedulerconfig.ResourcePriority{{Name: "p1", NodeSelector: ls("a", "1")}, {Name: "p1", NodeSelector: ls("b", "2")}}}, wantErr: "duplicate"},
		{name: "missing selector", args: deschedulerconfig.CustomPriorityArgs{EvictionOrder: []deschedulerconfig.ResourcePriority{{Name: "p1", NodeSelector: ls("a", "1")}, {Name: "p2"}}}, wantErr: "nodeSelector"},
		{name: "valid", args: deschedulerconfig.CustomPriorityArgs{EvictionOrder: []deschedulerconfig.ResourcePriority{{Name: "p1", NodeSelector: ls("a", "1")}, {Name: "p2", NodeSelector: ls("b", "2")}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCustomPriorityArgs(&tc.args)
			if len(tc.wantErr) > 0 {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ---- filterPods ----

func Test_filterPods(t *testing.T) {
	mkSel := func(lbl string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{"app": lbl}}
	}
	invalidSel := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: "Invalid"}}}

	cases := []struct {
		name      string
		selectors []deschedulerconfig.CustomPriorityPodSelector
		pod       *corev1.Pod
		wantErr   bool
		want      bool
	}{
		{name: "no selectors => allow all", selectors: nil, pod: newPod("default", "p", "n", 10, 10, map[string]string{"x": "y"}), want: true},
		{name: "match one selector", selectors: []deschedulerconfig.CustomPriorityPodSelector{{Name: "s1", Selector: mkSel("a")}}, pod: newPod("default", "p", "n", 10, 10, map[string]string{"app": "a"}), want: true},
		{name: "no match", selectors: []deschedulerconfig.CustomPriorityPodSelector{{Name: "s1", Selector: mkSel("a")}}, pod: newPod("default", "p", "n", 10, 10, map[string]string{"app": "b"}), want: false},
		{name: "invalid selector returns error", selectors: []deschedulerconfig.CustomPriorityPodSelector{{Name: "bad", Selector: invalidSel}}, pod: newPod("default", "p", "n", 10, 10, map[string]string{"app": "a"}), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ff, err := filterPods(tc.selectors)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			got := ff(tc.pod)
			assert.Equal(t, tc.want, got)
		})
	}
}

// ---- NewCustomPriority & Name ----

func Test_NewCustomPriority_and_Name(t *testing.T) {
	mkSel := func(k, v string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{k: v}}
	}
	lister := &podsLister{data: map[string][]*corev1.Pod{}}
	fh, _ := makeHandle(t, frameworkruntime.WithGetPodsAssignedToNodeFunc(lister.fn()))

	cases := []struct {
		name    string
		args    runtime.Object
		wantErr bool
	}{
		{name: "wrong type", args: &runtime.Unknown{}, wantErr: true},
		{name: "invalid args", args: &deschedulerconfig.CustomPriorityArgs{EvictionOrder: []deschedulerconfig.ResourcePriority{{Name: "p1", NodeSelector: mkSel("a", "1")}}}, wantErr: true},
		{name: "valid args", args: &deschedulerconfig.CustomPriorityArgs{EvictionOrder: []deschedulerconfig.ResourcePriority{{Name: "p1", NodeSelector: mkSel("a", "1")}, {Name: "p2", NodeSelector: mkSel("b", "2")}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plg, err := NewCustomPriority(tc.args, fh)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			cp := plg.(*CustomPriority)
			assert.Equal(t, PluginCustomPriorityName, cp.Name())
		})
	}
}

// ---- filterNodes ----

func Test_filterNodes(t *testing.T) {
	mkSel := func(k, v string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{k: v}}
	}
	cp := &CustomPriority{args: &deschedulerconfig.CustomPriorityArgs{}}
	nodes := []*corev1.Node{
		newNode("n1", map[string]string{"tier": "gold"}, 1000, 1<<30, 100),
		newNode("n2", map[string]string{"tier": "silver"}, 1000, 1<<30, 100),
	}
	cases := []struct {
		name     string
		selector *metav1.LabelSelector
		expect   int
	}{
		{name: "nil selector => all", selector: nil, expect: 2},
		{name: "match gold", selector: mkSel("tier", "gold"), expect: 1},
		{name: "match none", selector: mkSel("tier", "bronze"), expect: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp.args.NodeSelector = tc.selector
			got, err := cp.filterNodes(nodes)
			assert.NoError(t, err)
			assert.Equal(t, tc.expect, len(got))
		})
	}
}

// ---- classifyNodesByPriority ----

func Test_classifyNodesByPriority(t *testing.T) {
	mkSel := func(k, v string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{k: v}}
	}
	invalidSel := &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "tier", Operator: "Invalid"}}}
	cp := &CustomPriority{args: &deschedulerconfig.CustomPriorityArgs{
		EvictionOrder: []deschedulerconfig.ResourcePriority{
			{Name: "gold", NodeSelector: mkSel("tier", "gold")},
			{Name: "silver", NodeSelector: mkSel("tier", "silver")},
			{Name: "bad", NodeSelector: invalidSel},
		},
	}}
	nodes := []*corev1.Node{
		newNode("n1", map[string]string{"tier": "gold"}, 1000, 1<<30, 100),
		newNode("n2", map[string]string{"tier": "silver"}, 1000, 1<<30, 100),
		newNode("n3", map[string]string{"tier": "bronze"}, 1000, 1<<30, 100),
	}
	m := cp.classifyNodesByPriority(nodes)
	assert.Len(t, m["gold"], 1)
	assert.Len(t, m["silver"], 1)
	_, hasBad := m["bad"]
	assert.False(t, hasBad)
}

// ---- getPodRequests ----

func Test_getPodRequests(t *testing.T) {
	cp := &CustomPriority{}
	pod := newPod("default", "p", "n", 250, 128<<20, nil)
	// add another container
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
		Name:      "c2",
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: *resource.NewMilliQuantity(250, resource.DecimalSI), corev1.ResourceMemory: *resource.NewQuantity(256<<20, resource.BinarySI)}},
	})
	req := cp.getPodRequests(pod)
	assert.Equal(t, int64(500), req[corev1.ResourceCPU].MilliValue())
	assert.Equal(t, int64(384<<20), req[corev1.ResourceMemory].Value())
}

// ---- sortPodsByRequestsAscending ----

func Test_sortPodsByRequestsAscending(t *testing.T) {
	cp := &CustomPriority{}
	p1 := newPod("ns", "a", "n", 200, 100, nil)
	p2 := newPod("ns", "b", "n", 100, 500, nil)
	p3 := newPod("ns", "c", "n", 100, 100, nil)
	pods := []*corev1.Pod{p1, p2, p3}
	cp.sortPodsByRequestsAscending(pods)
	// expected order by CPU asc, then memory asc, then namespace/name asc
	expected := []*corev1.Pod{p3, p2, p1}
	assert.Equal(t, []string{expected[0].Name, expected[1].Name, expected[2].Name}, []string{pods[0].Name, pods[1].Name, pods[2].Name})
}

// ---- requestsFit & subRequests ----

func Test_requestsFit_and_subRequests(t *testing.T) {
	cp := &CustomPriority{}
	rem := map[corev1.ResourceName]*resource.Quantity{
		corev1.ResourceCPU:    resource.NewMilliQuantity(500, resource.DecimalSI),
		corev1.ResourceMemory: resource.NewQuantity(1024, resource.DecimalSI),
	}
	req := map[corev1.ResourceName]*resource.Quantity{
		corev1.ResourceCPU:    resource.NewMilliQuantity(200, resource.DecimalSI),
		corev1.ResourceMemory: resource.NewQuantity(512, resource.DecimalSI),
	}
	assert.True(t, cp.requestsFit(req, rem))
	cp.subRequests(rem, req)
	assert.Equal(t, int64(300), rem[corev1.ResourceCPU].MilliValue())
	assert.Equal(t, int64(512), rem[corev1.ResourceMemory].Value())
	// over ask
	req2 := map[corev1.ResourceName]*resource.Quantity{corev1.ResourceCPU: resource.NewMilliQuantity(400, resource.DecimalSI)}
	assert.False(t, cp.requestsFit(req2, rem))
}

// ---- findEvictablePods ----

func Test_findEvictablePods(t *testing.T) {
	// target nodes
	nodes := []*corev1.Node{
		newNode("t1", nil, 1000, 1<<30, 100),
	}
	// pod filter allows all
	cp := &CustomPriority{podFilter: func(p *corev1.Pod) bool { return true }, args: &deschedulerconfig.CustomPriorityArgs{NodeFit: true}}

	// create lister returning no existing pods so NodeFit reduces to allocatable check only
	lister := &podsLister{data: map[string][]*corev1.Pod{"t1": {}}}
	pods := []*corev1.Pod{
		newPod("ns", "small", "s", 100, 100, nil),
		newPod("ns", "big", "s", 2000, 100, nil), // won't fit
		newPod("ns", "mid", "s", 400, 100, nil),
		newPod("ns", "big2", "s", 3000, 100, nil),
		newPod("ns", "big3", "s", 3000, 100, nil),
		newPod("ns", "big4", "s", 3000, 100, nil),
	}
	got := cp.findEvictablePods(lister.fn(), pods, nodes)
	// NodeFit true -> only those that fit are included; also early stop kicks in after consecutive misses
	// only "small" and maybe "mid" fit CPU (1000m available)
	names := func(ps []*corev1.Pod) []string {
		r := make([]string, 0, len(ps))
		for _, p := range ps {
			r = append(r, p.Name)
		}
		sort.Strings(r)
		return r
	}
	assert.Subset(t, names(got), []string{"small", "mid"})

	// NodeFit false => all pass filter
	cp.args.NodeFit = false
	got2 := cp.findEvictablePods(lister.fn(), pods, nodes)
	assert.Equal(t, len(pods), len(got2))
}

// ---- nodeRemainingRequests ----

func Test_nodeRemainingRequests(t *testing.T) {
	n := newNode("n1", nil, 2000, 2<<30, 110)
	p1 := newPod("ns", "p1", "n1", 500, 256<<20, nil)
	p2 := newPod("ns", "p2", "n1", 700, 512<<20, nil)
	lister := &podsLister{data: map[string][]*corev1.Pod{"n1": {p1, p2}}}
	fh, _ := makeHandle(t, frameworkruntime.WithGetPodsAssignedToNodeFunc(lister.fn()))
	cp := &CustomPriority{handle: fh}
	rem, err := cp.nodeRemainingRequests(n)
	assert.NoError(t, err)
	assert.Equal(t, int64(800), rem[corev1.ResourceCPU].MilliValue())
	assert.True(t, rem[corev1.ResourceMemory].Value() > 0)

	// error path
	lister.returnErr = errors.New("boom")
	_, err = cp.nodeRemainingRequests(n)
	assert.Error(t, err)
}

// ---- cordonNode ----

func Test_cordonNode(t *testing.T) {
	n := newNode("n1", nil, 1000, 1<<30, 100)
	client := kubefake.NewSimpleClientset(n.DeepCopy())
	fh, _ := makeHandle(t, frameworkruntime.WithClientSet(client))
	cp := &CustomPriority{handle: fh}
	err := cp.cordonNode(context.TODO(), n)
	assert.NoError(t, err)
	latest, _ := client.CoreV1().Nodes().Get(context.TODO(), n.Name, metav1.GetOptions{})
	assert.True(t, latest.Spec.Unschedulable)

	// node not found
	err = cp.cordonNode(context.TODO(), newNode("missing", nil, 1, 1, 1))
	assert.Error(t, err)
}

// ---- evictFromPriorityToTargets ----

func Test_evictFromPriorityToTargets(t *testing.T) {
	// setup framework with lister and recording evictor
	lister := &podsLister{data: map[string][]*corev1.Pod{"s1": {}, "t1": {}}}
	fh, rec := makeHandle(t, frameworkruntime.WithGetPodsAssignedToNodeFunc(lister.fn()))
	cp := &CustomPriority{handle: fh, args: &deschedulerconfig.CustomPriorityArgs{DryRun: false, NodeFit: false}, podFilter: func(p *corev1.Pod) bool { return true }}
	src := newNode("s1", map[string]string{"tier": "gold"}, 1000, 1<<30, 100)
	tgt := newNode("t1", map[string]string{"tier": "silver"}, 1000, 1<<30, 100)
	pods := []*corev1.Pod{newPod("ns", "p1", "s1", 10, 10, nil), newPod("ns", "p2", "s1", 20, 20, nil)}
	// make lister return pods on source
	lister.data["s1"] = pods
	err := cp.evictFromPriorityToTargets(context.TODO(), "gold", []*corev1.Node{src}, []*corev1.Node{tgt})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(rec.evicted))

	// dry run path -> no eviction
	rec.evicted = nil
	cp.args.DryRun = true
	_ = cp.evictFromPriorityToTargets(context.TODO(), "gold", []*corev1.Node{src}, []*corev1.Node{tgt})
	assert.Len(t, rec.evicted, 0)
}

// ---- evictByDrainingNodes ----

func Test_evictByDrainingNodes_and_processEvictions(t *testing.T) {
	// two target nodes with capacities; one source node with small pods
	psmall := newPod("ns", "p1", "s1", 100, 100, nil)
	psmall2 := newPod("ns", "p2", "s1", 100, 100, nil)
	src := newNode("s1", map[string]string{"tier": "gold"}, 1000, 1<<30, 100)
	tgt1 := newNode("t1", map[string]string{"tier": "silver"}, 500, 1<<30, 100)
	tgt2 := newNode("t2", map[string]string{"tier": "bronze"}, 500, 1<<30, 100)
	lister := &podsLister{data: map[string][]*corev1.Pod{"s1": {psmall, psmall2}, "t1": {}, "t2": {}}}
	client := kubefake.NewSimpleClientset(src.DeepCopy(), tgt1.DeepCopy(), tgt2.DeepCopy())
	fh, rec := makeHandle(t,
		frameworkruntime.WithGetPodsAssignedToNodeFunc(lister.fn()),
		frameworkruntime.WithClientSet(client),
	)
	cp := &CustomPriority{handle: fh, args: &deschedulerconfig.CustomPriorityArgs{AutoCordon: true, Mode: deschedulerconfig.CustomPriorityEvictModeDrainNode}, podFilter: func(p *corev1.Pod) bool { return true }}

	// success path: all pods can be placed -> both are evicted, and cordon succeeds
	err := cp.evictByDrainingNodes(context.TODO(), "gold", []*corev1.Node{src}, []*corev1.Node{tgt1, tgt2})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(rec.evicted))

	// even if remaining calculation fails internally, processEvictions should not return error
	lister.returnErr = errors.New("boom")
	prioNodes := map[string][]*corev1.Node{"gold": {src}, "silver": {tgt1, tgt2}}
	st := cp.processEvictions(context.TODO(), prioNodes)
	assert.True(t, st == nil || st.Err == nil)
}

// ---- Balance ----

func Test_Balance(t *testing.T) {
	mkSel := func(k, v string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{k: v}}
	}
	nodes := []*corev1.Node{
		newNode("s1", map[string]string{"tier": "gold"}, 1000, 1<<30, 100),
		newNode("t1", map[string]string{"tier": "silver"}, 1000, 1<<30, 100),
	}
	lister := &podsLister{data: map[string][]*corev1.Pod{"s1": {newPod("ns", "p", "s1", 10, 10, nil)}, "t1": {}}}
	fh, _ := makeHandle(t, frameworkruntime.WithGetPodsAssignedToNodeFunc(lister.fn()))

	// paused
	cp := &CustomPriority{handle: fh, args: &deschedulerconfig.CustomPriorityArgs{Paused: true, EvictionOrder: []deschedulerconfig.ResourcePriority{{Name: "gold", NodeSelector: mkSel("tier", "gold")}, {Name: "silver", NodeSelector: mkSel("tier", "silver")}}}}
	cp.podFilter = func(p *corev1.Pod) bool { return true }
	st := cp.Balance(context.TODO(), nodes)
	assert.Nil(t, st)

	// not enough eviction order
	cp.args = &deschedulerconfig.CustomPriorityArgs{Paused: false, EvictionOrder: []deschedulerconfig.ResourcePriority{{Name: "gold", NodeSelector: mkSel("tier", "gold")}}}
	st = cp.Balance(context.TODO(), nodes)
	assert.Nil(t, st)

	// with node selector filtering no nodes
	cp.args = &deschedulerconfig.CustomPriorityArgs{NodeSelector: mkSel("none", "x"), EvictionOrder: []deschedulerconfig.ResourcePriority{{Name: "gold", NodeSelector: mkSel("tier", "gold")}, {Name: "silver", NodeSelector: mkSel("tier", "silver")}}}
	st = cp.Balance(context.TODO(), nodes)
	assert.Nil(t, st)

	// normal run BestEffort
	cp.args = &deschedulerconfig.CustomPriorityArgs{EvictionOrder: []deschedulerconfig.ResourcePriority{{Name: "gold", NodeSelector: mkSel("tier", "gold")}, {Name: "silver", NodeSelector: mkSel("tier", "silver")}}, Mode: deschedulerconfig.CustomPriorityEvictModeBestEffort}
	st = cp.Balance(context.TODO(), nodes)
	assert.True(t, st == nil || st.Err == nil)
}
