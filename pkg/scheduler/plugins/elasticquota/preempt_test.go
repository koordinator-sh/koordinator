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

package elasticquota

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	kubefake "k8s.io/client-go/kubernetes/fake"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/preemption"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

// ---------------------------------------------------------------------------
// Local helpers
// ---------------------------------------------------------------------------

// testCandidate is a minimal preemption.Candidate implementation for testing.
type testCandidate struct {
	nodeName string
	victims  *extenderv1.Victims
}

func (c *testCandidate) Name() string                 { return c.nodeName }
func (c *testCandidate) Victims() *extenderv1.Victims { return c.victims }

// ---------------------------------------------------------------------------
// Trivial interface-contract tests
// ---------------------------------------------------------------------------

func TestPlugin_GetOffsetAndNumCandidates(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	gp := suit.createPlugin(t).(*Plugin)

	offset, num := gp.GetOffsetAndNumCandidates(42)
	assert.Equal(t, int32(0), offset)
	assert.Equal(t, int32(42), num)
}

func TestPlugin_OrderedScoreFuncs(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	gp := suit.createPlugin(t).(*Plugin)

	result := gp.OrderedScoreFuncs(context.TODO(), map[string]*extenderv1.Victims{})
	assert.Nil(t, result)
}

func TestPlugin_CandidatesToVictimsMap(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	gp := suit.createPlugin(t).(*Plugin)

	pod1 := defaultCreatePod("p1", 10, 1, 1)
	pod2 := defaultCreatePod("p2", 5, 1, 1)

	c1 := &testCandidate{
		nodeName: "node-a",
		victims: &extenderv1.Victims{
			Pods:             []*corev1.Pod{pod1},
			NumPDBViolations: 0,
		},
	}
	c2 := &testCandidate{
		nodeName: "node-b",
		victims: &extenderv1.Victims{
			Pods:             []*corev1.Pod{pod2},
			NumPDBViolations: 1,
		},
	}

	candidates := []preemption.Candidate{c1, c2}
	m := gp.CandidatesToVictimsMap(candidates)

	assert.Len(t, m, 2)
	assert.Equal(t, c1.victims, m["node-a"])
	assert.Equal(t, c2.victims, m["node-b"])

	// Empty input
	assert.Empty(t, gp.CandidatesToVictimsMap(nil))
}

// ---------------------------------------------------------------------------
// TestPlugin_canPreempt
// ---------------------------------------------------------------------------

func TestPlugin_canPreempt(t *testing.T) {
	// helper: register a named quota and label both pods with it
	registerQuota := func(name string) func(gp *Plugin) {
		return func(gp *Plugin) {
			gp.quotaToTreeMapLock.Lock()
			gp.quotaToTreeMap[name] = ""
			gp.quotaToTreeMapLock.Unlock()
		}
	}
	withQuota := func(pod *corev1.Pod, name string) *corev1.Pod {
		pod.Labels[extension.LabelQuotaName] = name
		return pod
	}

	tests := []struct {
		name       string
		pod        *corev1.Pod // preemptor
		victim     *corev1.Pod
		extraSetup func(gp *Plugin)
		want       bool
	}{
		{
			name: "victim is non-preemptible",
			pod:  withQuota(defaultCreatePod("preemptor", 10, 1, 1), "q1"),
			victim: func() *corev1.Pod {
				v := withQuota(defaultCreatePod("victim", 5, 1, 1), "q1")
				v.Labels[extension.LabelPreemptible] = "false"
				return v
			}(),
			extraSetup: registerQuota("q1"),
			want:       false,
		},
		{
			// DisableDefaultQuotaPreemption defaults to true, so even without extra setup
			// preempting pods that fall back to DefaultQuota is blocked.
			name:   "DisableDefaultQuotaPreemption (default=true) blocks default-quota victims",
			pod:    defaultCreatePod("preemptor", 10, 1, 1), // no label -> DefaultQuota
			victim: defaultCreatePod("victim", 5, 1, 1),     // no label -> DefaultQuota
			want:   false,
		},
		{
			name:       "same priority – cannot preempt",
			pod:        withQuota(defaultCreatePod("preemptor", 10, 1, 1), "q1"),
			victim:     withQuota(defaultCreatePod("victim", 10, 1, 1), "q1"),
			extraSetup: registerQuota("q1"),
			want:       false,
		},
		{
			name:       "victim priority higher – cannot preempt",
			pod:        withQuota(defaultCreatePod("preemptor", 5, 1, 1), "q1"),
			victim:     withQuota(defaultCreatePod("victim", 15, 1, 1), "q1"),
			extraSetup: registerQuota("q1"),
			want:       false,
		},
		{
			name:   "different quota names – cannot preempt",
			pod:    withQuota(defaultCreatePod("preemptor", 10, 1, 1), "q1"),
			victim: withQuota(defaultCreatePod("victim", 5, 1, 1), "q2"),
			extraSetup: func(gp *Plugin) {
				gp.quotaToTreeMapLock.Lock()
				gp.quotaToTreeMap["q1"] = ""
				gp.quotaToTreeMap["q2"] = ""
				gp.quotaToTreeMapLock.Unlock()
			},
			want: false,
		},
		{
			// Use an explicit quota (not DefaultQuotaName) so DisableDefaultQuotaPreemption
			// (which defaults to true) does not block the preemption.
			name:       "all conditions met – preempts",
			pod:        withQuota(defaultCreatePod("preemptor", 10, 1, 1), "q1"),
			victim:     withQuota(defaultCreatePod("victim", 5, 1, 1), "q1"),
			extraSetup: registerQuota("q1"),
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			gp := suit.createPlugin(t).(*Plugin)
			if tt.extraSetup != nil {
				tt.extraSetup(gp)
			}
			assert.Equal(t, tt.want, gp.canPreempt(tt.pod, tt.victim))
		})
	}
}

// ---------------------------------------------------------------------------
// TestPlugin_PodEligibleToPreemptOthers
// ---------------------------------------------------------------------------

func TestPlugin_PodEligibleToPreemptOthers(t *testing.T) {
	const nodeName = "node-1"

	// helper: a pod that nominates nodeName
	nominatingPod := func(priority int32) *corev1.Pod {
		p := defaultCreatePod("preemptor", priority, 1, 1)
		p.Status.NominatedNodeName = nodeName
		return p
	}

	// helper: a terminating pod on nodeName
	terminatingPod := func(name string, priority int32, quotaLabel string) *corev1.Pod {
		now := metav1.Now()
		p := defaultCreatePod(name, priority, 1, 1)
		p.Spec.NodeName = nodeName
		p.DeletionTimestamp = &now
		if quotaLabel != "" {
			p.Labels[extension.LabelQuotaName] = quotaLabel
		}
		return p
	}

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}

	tests := []struct {
		name                string
		pod                 *corev1.Pod
		nominatedNodeStatus *fwktype.Status
		snapshotPods        []*corev1.Pod
		snapshotNodes       []*corev1.Node
		extraSetup          func(gp *Plugin)
		wantEligible        bool
		wantMsg             string
	}{
		{
			name: "preemptionPolicy=Never – not eligible",
			pod: func() *corev1.Pod {
				p := defaultCreatePod("preemptor", 10, 1, 1)
				p.Spec.PreemptionPolicy = ptr.To(corev1.PreemptNever)
				return p
			}(),
			nominatedNodeStatus: fwktype.NewStatus(fwktype.Unschedulable),
			wantEligible:        false,
			wantMsg:             "not eligible due to preemptionPolicy=Never.",
		},
		{
			name:                "no nominated node – always eligible",
			pod:                 defaultCreatePod("preemptor", 10, 1, 1),
			nominatedNodeStatus: fwktype.NewStatus(fwktype.Unschedulable),
			wantEligible:        true,
			wantMsg:             "",
		},
		{
			name:                "nominated node UnschedulableAndUnresolvable – eligible to try again",
			pod:                 nominatingPod(10),
			nominatedNodeStatus: fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable),
			snapshotNodes:       []*corev1.Node{node},
			wantEligible:        true,
			wantMsg:             "",
		},
		{
			// Node is in the snapshot but has no pods -> loop is empty -> eligible.
			// (The "node not in snapshot" path is exercised differently depending on the
			// NodeInfoLister implementation; with testSharedLister the typed-nil issue
			// would bypass the nil check, so we test the equivalent empty-node path here.)
			name:                "node in snapshot but no pods – eligible",
			pod:                 nominatingPod(10),
			nominatedNodeStatus: fwktype.NewStatus(fwktype.Unschedulable),
			snapshotNodes:       []*corev1.Node{node},
			snapshotPods:        nil, // no pods on node
			wantEligible:        true,
			wantMsg:             "",
		},
		{
			name:                "terminating pod on node, same quota, lower priority – not eligible",
			pod:                 nominatingPod(10),
			nominatedNodeStatus: fwktype.NewStatus(fwktype.Unschedulable),
			snapshotNodes:       []*corev1.Node{node},
			snapshotPods:        []*corev1.Pod{terminatingPod("term-pod", 5, "")},
			wantEligible:        false,
			wantMsg:             "not eligible due to a terminating pod on the nominated node.",
		},
		{
			name:                "terminating pod on node, different quota – eligible",
			pod:                 nominatingPod(10),
			nominatedNodeStatus: fwktype.NewStatus(fwktype.Unschedulable),
			snapshotNodes:       []*corev1.Node{node},
			snapshotPods:        []*corev1.Pod{terminatingPod("term-pod", 5, "other-quota")},
			extraSetup: func(gp *Plugin) {
				gp.quotaToTreeMapLock.Lock()
				gp.quotaToTreeMap["other-quota"] = ""
				gp.quotaToTreeMapLock.Unlock()
			},
			wantEligible: true,
			wantMsg:      "",
		},
		{
			name:                "terminating pod on node, same quota, higher priority – eligible",
			pod:                 nominatingPod(10),
			nominatedNodeStatus: fwktype.NewStatus(fwktype.Unschedulable),
			snapshotNodes:       []*corev1.Node{node},
			snapshotPods:        []*corev1.Pod{terminatingPod("term-pod", 15, "")},
			wantEligible:        true,
			wantMsg:             "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWithPod(t, tt.snapshotNodes, tt.snapshotPods)
			gp := suit.createPlugin(t).(*Plugin)
			if tt.extraSetup != nil {
				tt.extraSetup(gp)
			}

			eligible, msg := gp.PodEligibleToPreemptOthers(context.TODO(), tt.pod, tt.nominatedNodeStatus)
			assert.Equal(t, tt.wantEligible, eligible)
			assert.Equal(t, tt.wantMsg, msg)
		})
	}
}

// ---------------------------------------------------------------------------
// TestFilterPodsWithPDBViolation
// ---------------------------------------------------------------------------

func TestFilterPodsWithPDBViolation(t *testing.T) {
	makePodInfo := func(name, ns string, labels map[string]string) fwktype.PodInfo {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Labels:    labels,
			},
		}
		pi, err := framework.NewPodInfo(pod)
		if err != nil {
			panic(err)
		}
		return pi
	}

	matchLabels := map[string]string{"app": "test"}

	tests := []struct {
		name             string
		podInfos         []fwktype.PodInfo
		pdbs             []*policy.PodDisruptionBudget
		wantViolating    int
		wantNonViolating int
	}{
		{
			name:     "pod with no labels – always non-violating",
			podInfos: []fwktype.PodInfo{makePodInfo("p1", "ns1", nil)},
			pdbs: []*policy.PodDisruptionBudget{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
					Spec:       policy.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: matchLabels}},
					Status:     policy.PodDisruptionBudgetStatus{DisruptionsAllowed: 0},
				},
			},
			wantViolating:    0,
			wantNonViolating: 1,
		},
		{
			name:     "PDB in different namespace – non-violating",
			podInfos: []fwktype.PodInfo{makePodInfo("p1", "ns-a", matchLabels)},
			pdbs: []*policy.PodDisruptionBudget{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns-b"},
					Spec:       policy.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: matchLabels}},
					Status:     policy.PodDisruptionBudgetStatus{DisruptionsAllowed: 0},
				},
			},
			wantViolating:    0,
			wantNonViolating: 1,
		},
		{
			name:     "pod listed in DisruptedPods – non-violating (no decrement)",
			podInfos: []fwktype.PodInfo{makePodInfo("p1", "ns1", matchLabels)},
			pdbs: []*policy.PodDisruptionBudget{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
					Spec:       policy.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: matchLabels}},
					Status: policy.PodDisruptionBudgetStatus{
						DisruptionsAllowed: 0,
						DisruptedPods:      map[string]metav1.Time{"p1": metav1.Now()},
					},
				},
			},
			wantViolating:    0,
			wantNonViolating: 1,
		},
		{
			name: "PDB allowance drops below zero – violating",
			podInfos: []fwktype.PodInfo{
				makePodInfo("p1", "ns1", matchLabels),
				makePodInfo("p2", "ns1", matchLabels),
			},
			pdbs: []*policy.PodDisruptionBudget{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
					Spec:       policy.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: matchLabels}},
					Status:     policy.PodDisruptionBudgetStatus{DisruptionsAllowed: 1},
				},
			},
			// p1 decrements to 0 (still ok), p2 decrements to -1 -> p2 violates
			wantViolating:    1,
			wantNonViolating: 1,
		},
		{
			name:     "invalid PDB selector – treated as no match",
			podInfos: []fwktype.PodInfo{makePodInfo("p1", "ns1", matchLabels)},
			pdbs: []*policy.PodDisruptionBudget{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
					Spec: policy.PodDisruptionBudgetSpec{
						// An invalid requirements string triggers LabelSelectorAsSelector error
						Selector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "app", Operator: "InvalidOp", Values: []string{"test"}},
							},
						},
					},
					Status: policy.PodDisruptionBudgetStatus{DisruptionsAllowed: 0},
				},
			},
			wantViolating:    0,
			wantNonViolating: 1,
		},
		{
			name: "multiple PDBs – order preserved and violating counted correctly",
			podInfos: []fwktype.PodInfo{
				makePodInfo("p1", "ns1", matchLabels),
				makePodInfo("p2", "ns1", matchLabels),
				makePodInfo("p3", "ns1", matchLabels),
			},
			pdbs: []*policy.PodDisruptionBudget{
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
					Spec:       policy.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: matchLabels}},
					Status:     policy.PodDisruptionBudgetStatus{DisruptionsAllowed: 1},
				},
			},
			// p1: allowed=1->0 (ok); p2: allowed=0->-1 (violating); p3: allowed=-1->-2 (violating)
			wantViolating:    2,
			wantNonViolating: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violating, nonViolating := filterPodsWithPDBViolation(tt.podInfos, tt.pdbs)
			assert.Len(t, violating, tt.wantViolating, "violating count")
			assert.Len(t, nonViolating, tt.wantNonViolating, "non-violating count")
			// verify the combined set equals the input (no pods lost)
			assert.Len(t, append(violating, nonViolating...), len(tt.podInfos), "total count preserved")
		})
	}
}

// ---------------------------------------------------------------------------
// TestPlugin_SelectVictimsOnNode
// ---------------------------------------------------------------------------

func TestPlugin_SelectVictimsOnNode(t *testing.T) {
	const testNodeName = "test-select-node"
	// Use an explicit, non-default quota so DisableDefaultQuotaPreemption (default=true)
	// does not block canPreempt from returning true.
	const testQuotaName = "svon-quota"

	buildNodeInfo := func(victims ...*corev1.Pod) *framework.NodeInfo {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeName},
		}
		ni := framework.NewNodeInfo()
		ni.SetNode(node)
		for _, p := range victims {
			ni.AddPod(p)
		}
		return ni
	}

	withQuota := func(pod *corev1.Pod, uid string) *corev1.Pod {
		pod.Labels[extension.LabelQuotaName] = testQuotaName
		// nodeInfo.RemovePod requires a non-empty UID to build the pod key.
		pod.UID = types.UID(uid)
		return pod
	}

	// preemptor requests 2 CPU
	preemptor := withQuota(defaultCreatePod("preemptor", 10, 2, 0), "preemptor-uid")

	tests := []struct {
		name            string
		podsOnNode      []*corev1.Pod
		postFilterState *PostFilterState
		pdbs            []*policy.PodDisruptionBudget
		wantVictimCount int
		wantViolating   int
		wantStatusCode  fwktype.Code
	}{
		{
			name: "no potential victims – all pods have higher priority",
			podsOnNode: []*corev1.Pod{
				// priority 20 > preemptor priority 10 -> canPreempt returns false
				withQuota(defaultCreatePod("high-pri", 20, 1, 0), "high-pri-uid"),
			},
			postFilterState: &PostFilterState{
				used:      createResourceList(0, 0),
				usedLimit: createResourceList(100, 0),
				quotaInfo: &core.QuotaInfo{PodCache: map[string]*core.PodInfo{}},
			},
			wantVictimCount: 0,
			wantViolating:   0,
			wantStatusCode:  fwktype.UnschedulableAndUnresolvable,
		},
		{
			name: "victims found, quota fits – all reprieved, no actual victims",
			podsOnNode: []*corev1.Pod{
				// priority 5 < preemptor priority 10 -> canPreempt returns true
				withQuota(defaultCreatePod("low-pri", 5, 1, 0), "low-pri-uid"),
			},
			postFilterState: &PostFilterState{
				// used(0) + preemptor.req(2) = 2 <= usedLimit(100) -> fits
				used:      createResourceList(0, 0),
				usedLimit: createResourceList(100, 0),
				quotaInfo: &core.QuotaInfo{PodCache: map[string]*core.PodInfo{}},
			},
			wantVictimCount: 0,
			wantViolating:   0,
			wantStatusCode:  fwktype.Success,
		},
		{
			name: "victims found, quota exceeded – victim becomes actual victim",
			podsOnNode: []*corev1.Pod{
				withQuota(defaultCreatePod("low-pri", 5, 1, 0), "low-pri-uid"),
			},
			postFilterState: &PostFilterState{
				// used(0) + preemptor.req(2) = 2 > usedLimit(1) -> exceeds quota
				used:      createResourceList(0, 0),
				usedLimit: createResourceList(1, 0),
				quotaInfo: &core.QuotaInfo{PodCache: map[string]*core.PodInfo{}},
			},
			// numViolatingVictim only counts filter failures, not quota-limit violations
			wantVictimCount: 1,
			wantViolating:   0,
			wantStatusCode:  fwktype.Success,
		},
		{
			name: "non-preemptible pod on node – skipped as victim",
			podsOnNode: []*corev1.Pod{
				func() *corev1.Pod {
					p := withQuota(defaultCreatePod("non-preemptible", 5, 1, 0), "np-uid")
					p.Labels[extension.LabelPreemptible] = "false"
					return p
				}(),
			},
			postFilterState: &PostFilterState{
				used:      createResourceList(0, 0),
				usedLimit: createResourceList(1, 0),
				quotaInfo: &core.QuotaInfo{PodCache: map[string]*core.PodInfo{}},
			},
			// canPreempt returns false for non-preemptible -> no potential victims
			wantVictimCount: 0,
			wantViolating:   0,
			wantStatusCode:  fwktype.UnschedulableAndUnresolvable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use newPluginTestSuitWithPod because it includes WithPodNominator which is
			// required by RunFilterPluginsWithNominatedPods -> NominatedPodsForNode.
			suit := newPluginTestSuitWithPod(t, nil, nil)
			gp := suit.createPlugin(t).(*Plugin)

			// Register the test quota so getPodAssociateQuotaName resolves it correctly.
			gp.quotaToTreeMapLock.Lock()
			gp.quotaToTreeMap[testQuotaName] = ""
			gp.quotaToTreeMapLock.Unlock()

			// Write postFilterState into a fresh CycleState.
			state := framework.NewCycleState()
			state.Write(postFilterKey, tt.postFilterState)

			nodeInfo := buildNodeInfo(tt.podsOnNode...)
			victims, numViolating, status := gp.SelectVictimsOnNode(
				context.TODO(), state, preemptor, nodeInfo, tt.pdbs,
			)

			assert.Equal(t, tt.wantVictimCount, len(victims), "victim count")
			assert.Equal(t, tt.wantViolating, numViolating, "numViolatingVictim")
			assert.Equal(t, tt.wantStatusCode, status.Code(), "status code")
		})
	}
}

// ---------------------------------------------------------------------------
// TestGetPDBLister
// ---------------------------------------------------------------------------

func TestGetPDBLister(t *testing.T) {
	t.Run("feature gate disabled – returns nil", func(t *testing.T) {
		defer utilfeature.SetFeatureGateDuringTest(
			t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.PodDisruptionBudget, false,
		)()

		suit := newPluginTestSuit(t, nil)
		lister := getPDBLister(suit.Handle)
		assert.Nil(t, lister)
	})

	t.Run("feature gate enabled, discovery returns error – returns nil", func(t *testing.T) {
		defer utilfeature.SetFeatureGateDuringTest(
			t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.PodDisruptionBudget, true,
		)()

		// Default kubefake client has no resources registered, so ServerResourcesForGroupVersion
		// returns a not-found error -> getPDBLister returns nil.
		suit := newPluginTestSuit(t, nil)
		lister := getPDBLister(suit.Handle)
		assert.Nil(t, lister)
	})

	t.Run("feature gate enabled, policy/v1 resources present – returns lister", func(t *testing.T) {
		defer utilfeature.SetFeatureGateDuringTest(
			t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.PodDisruptionBudget, true,
		)()

		suit := newPluginTestSuit(t, nil)

		// Inject the policy/v1 resource list so the discovery call succeeds and returns
		// a non-zero Size() response.
		// The kubefake.Clientset embeds testing.Fake which has the Resources slice.
		// FakeDiscovery uses &cs.Fake so mutations here are visible to discovery calls.
		if fakeCS, ok := suit.Handle.ClientSet().(*kubefake.Clientset); ok {
			fakeCS.Fake.Resources = append(fakeCS.Fake.Resources, &metav1.APIResourceList{
				GroupVersion: policy.SchemeGroupVersion.String(),
				APIResources: []metav1.APIResource{
					{Name: "poddisruptionbudgets", Kind: "PodDisruptionBudget"},
				},
			})
		}

		lister := getPDBLister(suit.Handle)
		assert.NotNil(t, lister)
	})
}
