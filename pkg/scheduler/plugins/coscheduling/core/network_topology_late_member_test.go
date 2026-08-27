package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
)

func newLateMemberTestNodes() []*corev1.Node {
	newNode := func(name, spine, block string) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					networktopology.FakeSpineLabel: spine,
					networktopology.FakeBlockLabel: block,
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:  resource.MustParse("16"),
					corev1.ResourcePods: resource.MustParse("110"),
				},
			},
		}
	}
	return []*corev1.Node{
		newNode("node-1", "s1", "b1"),
		newNode("node-2", "s1", "b1"),
		newNode("node-3", "s1", "b2"),
		newNode("node-5", "s2", "b3"),
	}
}

func newMustGatherBlockTopologySpec() *extension.NetworkTopologySpec {
	return &extension.NetworkTopologySpec{
		GatherStrategy: []extension.NetworkTopologyGatherRule{
			{
				Layer:    "BlockLayer",
				Strategy: extension.NetworkTopologyGatherStrategyMustGather,
			},
		},
	}
}

func newLateMemberPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      name,
			UID:       types.UID(name),
			Labels: map[string]string{
				v1alpha1.PodGroupLabel: "gangA",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("16"),
						},
					},
				},
			},
		},
	}
}

func TestPodGroupManager_FindOneNode_LateMembersOfSatisfiedGang(t *testing.T) {
	gangID := "default/gangA"
	boundPodOnNode := func(name, nodeName string) *corev1.Pod {
		pod := newLateMemberPod(name)
		pod.Spec.NodeName = nodeName
		return pod
	}
	tests := []struct {
		name                   string
		triggerPod             *corev1.Pod
		pendingPods            []*corev1.Pod
		withClusterTopology    bool
		networkTopologySpec    *extension.NetworkTopologySpec
		boundPods              []*corev1.Pod
		manuallySetSatisfied   bool
		wantStatusCode         fwktype.Code
		wantStatusMsgSubstring string
		wantPlannedNodes       sets.Set[string]
	}{
		{
			name:                "not a gang pod",
			triggerPod:          &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "late-pod"}},
			withClusterTopology: true,
			networkTopologySpec: newMustGatherBlockTopologySpec(),
			wantStatusCode:      fwktype.Skip,
		},
		{
			name:                "gang without network topology spec",
			triggerPod:          newLateMemberPod("late-pod"),
			withClusterTopology: true,
			networkTopologySpec: nil,
			wantStatusCode:      fwktype.Skip,
		},
		{
			name:                "gang not satisfied yet",
			triggerPod:          newLateMemberPod("late-pod"),
			withClusterTopology: true,
			networkTopologySpec: newMustGatherBlockTopologySpec(),
			wantStatusCode:      fwktype.Skip,
		},
		{
			name:                   "no cluster network topology",
			triggerPod:             newLateMemberPod("late-pod"),
			withClusterTopology:    false,
			networkTopologySpec:    newMustGatherBlockTopologySpec(),
			boundPods:              []*corev1.Pod{boundPodOnNode("bound-pod-1", "node-1")},
			wantStatusCode:         fwktype.UnschedulableAndUnresolvable,
			wantStatusMsgSubstring: ErrNoClusterNetworkTopology,
		},
		{
			name:                "no must-gather requirement",
			triggerPod:          newLateMemberPod("late-pod"),
			withClusterTopology: true,
			networkTopologySpec: &extension.NetworkTopologySpec{
				GatherStrategy: []extension.NetworkTopologyGatherRule{
					{
						Layer:    "BlockLayer",
						Strategy: extension.NetworkTopologyGatherStrategyPreferGather,
					},
				},
			},
			boundPods:      []*corev1.Pod{boundPodOnNode("bound-pod-1", "node-1")},
			wantStatusCode: fwktype.Skip,
		},
		{
			name:                   "satisfied but no bound member",
			triggerPod:             newLateMemberPod("late-pod"),
			withClusterTopology:    true,
			networkTopologySpec:    newMustGatherBlockTopologySpec(),
			manuallySetSatisfied:   true,
			wantStatusCode:         fwktype.UnschedulableAndUnresolvable,
			wantStatusMsgSubstring: "no bound member is found",
		},
		{
			name:                   "bound member node not in cluster network topology",
			triggerPod:             newLateMemberPod("late-pod"),
			withClusterTopology:    true,
			networkTopologySpec:    newMustGatherBlockTopologySpec(),
			boundPods:              []*corev1.Pod{boundPodOnNode("bound-pod-1", "node-unknown")},
			wantStatusCode:         fwktype.UnschedulableAndUnresolvable,
			wantStatusMsgSubstring: `bound member "default/bound-pod-1" is on node "node-unknown" which is not in the cluster network topology`,
		},
		{
			name:                "bound members span multiple blocks",
			triggerPod:          newLateMemberPod("late-pod"),
			withClusterTopology: true,
			networkTopologySpec: newMustGatherBlockTopologySpec(),
			boundPods: []*corev1.Pod{
				boundPodOnNode("bound-pod-1", "node-1"),
				boundPodOnNode("bound-pod-2", "node-3"),
			},
			wantStatusCode:         fwktype.UnschedulableAndUnresolvable,
			wantStatusMsgSubstring: "the bound members span multiple must-gather topology domains",
		},
		{
			name:                "batch scheduled within the bound members' block",
			triggerPod:          newLateMemberPod("late-pod"),
			pendingPods:         []*corev1.Pod{newLateMemberPod("late-pod-2")},
			withClusterTopology: true,
			networkTopologySpec: newMustGatherBlockTopologySpec(),
			boundPods: []*corev1.Pod{
				boundPodOnNode("bound-pod-1", "node-1"),
				boundPodOnNode("bound-pod-2", "node-2"),
			},
			wantStatusCode:   fwktype.Success,
			wantPlannedNodes: sets.New("node-1", "node-2"),
		},
		{
			name:       "insufficient offer slot in the bound members' block",
			triggerPod: newLateMemberPod("late-pod"),
			pendingPods: []*corev1.Pod{
				newLateMemberPod("late-pod-2"),
				newLateMemberPod("late-pod-3"),
			},
			withClusterTopology: true,
			networkTopologySpec: newMustGatherBlockTopologySpec(),
			boundPods: []*corev1.Pod{
				boundPodOnNode("bound-pod-1", "node-1"),
				boundPodOnNode("bound-pod-2", "node-2"),
			},
			wantStatusCode: fwktype.Unschedulable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterNetworkTopology := networktopology.FakeClusterNetworkTopology
			if !tt.withClusterTopology {
				clusterNetworkTopology = nil
			}
			extendedFramework := NewFakeExtendedFramework(t, newLateMemberTestNodes(), nil, nil, nil, clusterNetworkTopology)
			pgMgr := &PodGroupManager{handle: extendedFramework, networkTopologySolver: NewNetworkTopologySolver(extendedFramework)}
			pgMgr.cache = NewGangCache(nil, nil, nil, nil, nil)

			gang := NewGang(gangID)
			gang.HasGangInit = true
			gang.GangGroup = []string{gangID}
			gang.NetworkTopologySpec = tt.networkTopologySpec
			for _, boundPod := range tt.boundPods {
				gang.addBoundPod(boundPod)
			}
			if tt.manuallySetSatisfied {
				gang.setResourceSatisfied()
			}
			gang.setChild(tt.triggerPod)
			for _, pendingPod := range tt.pendingPods {
				gang.setChild(pendingPod)
			}
			pgMgr.cache.gangItems[gangID] = gang

			cycleState := framework.NewCycleState()
			frameworkext.InitDiagnosis(cycleState, tt.triggerPod)
			plan, status := pgMgr.FindOneNode(context.TODO(), cycleState, tt.triggerPod, nil)
			if tt.wantStatusCode == fwktype.Success {
				assert.True(t, status.IsSuccess())
				assert.NotNil(t, plan)
				assert.Len(t, plan.Pods, len(tt.pendingPods)+1)
				plannedNodeNames := sets.New[string]()
				for podKey, nodeName := range plan.PodToNodeName {
					assert.Contains(t, tt.wantPlannedNodes, nodeName, "pod %s planned to unexpected node", podKey)
					plannedNodeNames.Insert(nodeName)
				}
				assert.True(t, tt.wantPlannedNodes.Equal(plannedNodeNames))
			} else {
				assert.Nil(t, plan)
				assert.Equal(t, tt.wantStatusCode, status.Code())
				if tt.wantStatusMsgSubstring != "" {
					assert.Contains(t, status.Message(), tt.wantStatusMsgSubstring)
				}
			}
		})
	}
}

func TestGroupPlacementByMustGatherDomain(t *testing.T) {
	extendedFramework := NewFakeExtendedFramework(t, newLateMemberTestNodes(), nil, nil, nil, networktopology.FakeClusterNetworkTopology)
	snapshot := extendedFramework.(frameworkext.ExtendedHandle).GetNetworkTopologyTreeManager().GetSnapshot()
	mustGatherSpec := newMustGatherBlockTopologySpec()
	preferGatherSpec := &extension.NetworkTopologySpec{
		GatherStrategy: []extension.NetworkTopologyGatherRule{
			{
				Layer:    "BlockLayer",
				Strategy: extension.NetworkTopologyGatherStrategyPreferGather,
			},
		},
	}
	blockB1 := networktopology.TreeNodeMeta{Layer: "BlockLayer", Name: "b1"}
	blockB2 := networktopology.TreeNodeMeta{Layer: "BlockLayer", Name: "b2"}
	blockUnknown := networktopology.TreeNodeMeta{Layer: "BlockLayer", Name: "unknown"}

	tests := []struct {
		name        string
		spec        *extension.NetworkTopologySpec
		snapshot    *networktopology.TreeSnapshot
		podToNode   map[string]string
		wantLayer   schedulingv1alpha1.TopologyLayer
		wantDomains map[networktopology.TreeNodeMeta][]string
	}{
		{
			name:      "no topology snapshot",
			spec:      mustGatherSpec,
			snapshot:  nil,
			podToNode: map[string]string{"default/pod-1": "node-1", "default/pod-2": "node-2"},
			wantLayer: "",
			wantDomains: map[networktopology.TreeNodeMeta][]string{
				{}: {"default/pod-1 -> node-1", "default/pod-2 -> node-2"},
			},
		},
		{
			name:      "no must-gather requirement",
			spec:      preferGatherSpec,
			snapshot:  snapshot,
			podToNode: map[string]string{"default/pod-1": "node-1"},
			wantLayer: "",
			wantDomains: map[networktopology.TreeNodeMeta][]string{
				{}: {"default/pod-1 -> node-1"},
			},
		},
		{
			name:      "gathered in one block",
			spec:      mustGatherSpec,
			snapshot:  snapshot,
			podToNode: map[string]string{"default/pod-1": "node-1", "default/pod-2": "node-2"},
			wantLayer: "BlockLayer",
			wantDomains: map[networktopology.TreeNodeMeta][]string{
				blockB1: {"default/pod-1 -> node-1", "default/pod-2 -> node-2"},
			},
		},
		{
			name:      "scattered across blocks",
			spec:      mustGatherSpec,
			snapshot:  snapshot,
			podToNode: map[string]string{"default/pod-1": "node-1", "default/pod-2": "node-3"},
			wantLayer: "BlockLayer",
			wantDomains: map[networktopology.TreeNodeMeta][]string{
				blockB1: {"default/pod-1 -> node-1"},
				blockB2: {"default/pod-2 -> node-3"},
			},
		},
		{
			name:      "placement on node missing in topology",
			spec:      mustGatherSpec,
			snapshot:  snapshot,
			podToNode: map[string]string{"default/pod-1": "node-unknown"},
			wantLayer: "BlockLayer",
			wantDomains: map[networktopology.TreeNodeMeta][]string{
				blockUnknown: {"default/pod-1 -> node-unknown"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLayer, gotDomains := groupPlacementByMustGatherDomain(tt.spec, tt.snapshot, tt.podToNode)
			assert.Equal(t, tt.wantLayer, gotLayer)
			assert.Equal(t, len(tt.wantDomains), len(gotDomains))
			for domainMeta, wantPlacements := range tt.wantDomains {
				gotPlacements := gotDomains[domainMeta]
				assert.ElementsMatch(t, wantPlacements, gotPlacements)
			}
		})
	}
}
