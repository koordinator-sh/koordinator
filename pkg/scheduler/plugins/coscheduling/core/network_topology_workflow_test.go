package core

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
)

func TestPodGroupManager_PreFilter(t *testing.T) {
	tests := []struct {
		name                  string
		pod                   *corev1.Pod
		gangSchedulingContext *GangSchedulingContext
		wantPreFilterResult   *framework.PreFilterResult
		wantStatus            *framework.Status
	}{
		{
			name: "not gang",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			},
			wantPreFilterResult: nil,
			wantStatus:          nil,
		},
		{
			name: "not network topology aware gang",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				networkTopologySpec: nil,
			},
			wantPreFilterResult: nil,
			wantStatus:          nil,
		},
		{
			name: "firstPod return nil",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				alreadyAttemptedPods: sets.New[string](),
				networkTopologySpec: &extension.NetworkTopologySpec{
					GatherStrategy: []extension.NetworkTopologyGatherRule{
						{
							Layer:    "SpineLayer",
							Strategy: extension.NetworkTopologyGatherStrategyMustGather,
						},
					},
				},
			},
			wantPreFilterResult: nil,
			wantStatus:          nil,
		},
		{
			name: "next pod return planned node",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod",
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				alreadyAttemptedPods: sets.New[string]("firstPod", "default/test-pod"),
				networkTopologySpec: &extension.NetworkTopologySpec{
					GatherStrategy: []extension.NetworkTopologyGatherRule{
						{
							Layer:    "SpineLayer",
							Strategy: extension.NetworkTopologyGatherStrategyMustGather,
						},
					},
				},
				networkTopologyPlannedNodes: map[string]string{
					"firstPod":         "node1",
					"default/test-pod": "node2",
				},
			},
			wantPreFilterResult: &framework.PreFilterResult{
				NodeNames: sets.New[string]("node2"),
			},
			wantStatus: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgMgr := &PodGroupManager{holder: GangSchedulingContextHolder{gangSchedulingContext: tt.gangSchedulingContext}}
			cycleState := framework.NewCycleState()
			gotPreFilterResult, gotStatus := pgMgr.PreFilter(context.TODO(), cycleState, tt.pod)
			assert.Equal(t, tt.wantPreFilterResult, gotPreFilterResult)
			assert.Equal(t, tt.wantStatus, gotStatus)

		})
	}
}

func TestPodGroupManager_NetworkTopology(t *testing.T) {
	highPriority := int32(1000)
	lowPriority := int32(1)
	gangName := "gangA"
	tests := []struct {
		name                   string
		triggerPod             *corev1.Pod
		gangSchedulingContext  *GangSchedulingContext
		allPendingPods         []*corev1.Pod
		clusterNetworkTopology *schedulingv1alpha1.ClusterNetworkTopology
		nodes                  []*corev1.Node
		existingPods           []*corev1.Pod
		existingNominatedPods  []*corev1.Pod
		filterPlugin           schedulertesting.RegisterPluginFunc
		wantScheduleStatus     *framework.Status
		wantPlannedPodToNode   map[string]string
		wantDiagnosis          *frameworkext.Diagnosis
		wantPreemptionState    *JobPreemptionState
		wantPreemptMessage     string
		wantPreempt            *framework.PostFilterResult
		wantPreemptStatus      *framework.Status
		wantPossibleVictims    []schedulingv1alpha1.PossibleVictim
		wantVictims            []schedulingv1alpha1.PossibleVictim
		wantJobPods            []corev1.Pod
	}{
		{
			name: "scheduled, prefer gather",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-3",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-4",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:   sets.New[string]("default/gangA"),
				gangGroupID: "default/gangA",
				networkTopologySpec: &extension.NetworkTopologySpec{
					GatherStrategy: []extension.NetworkTopologyGatherRule{
						{
							Layer:    "BlockLayer",
							Strategy: extension.NetworkTopologyGatherStrategyPreferGather,
						},
						{
							Layer:    "SpineLayer",
							Strategy: extension.NetworkTopologyGatherStrategyPreferGather,
						},
					},
				},
			},
			/*
				Cluster Topology
				├── SpineLayer: s1
				│   ├── BlockLayer: b1
				│   │   ├── node-8
				│   │   └── node-1
				│   └── BlockLayer: b2
				│       ├── node-7
				│       └── node-2
				└── SpineLayer: s2
				    ├── BlockLayer: b3
				    │   ├── node-6
				    │   └── node-3
				    └── BlockLayer: b4
				        ├── node-5
				        └── node-4
			*/
			clusterNetworkTopology: networktopology.FakeClusterNetworkTopology,
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-8",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-7",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-6",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-3",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-5",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-4",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			wantScheduleStatus: nil,
			wantPlannedPodToNode: map[string]string{
				"default/pending-pod-1": "node-1",
				"default/pending-pod-2": "node-8",
				"default/pending-pod-3": "node-2",
				"default/pending-pod-4": "node-7",
			},
			wantDiagnosis: &frameworkext.Diagnosis{
				ScheduleDiagnosis: &frameworkext.ScheduleDiagnosis{
					SchedulingMode:      frameworkext.JobSchedulingMode,
					AlreadyWaitForBound: 0,
					NodeOfferSlot: map[string]int{
						"node-1": 1,
						"node-2": 1,
						"node-3": 1,
						"node-4": 1,
						"node-5": 1,
						"node-6": 1,
						"node-7": 1,
						"node-8": 1,
					},
					NodeToStatusMap: map[string]*framework.Status{
						"node-1": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-2": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-3": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-4": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-5": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-6": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-7": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-8": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					},
				},
			},
		},
		{
			name: "scheduled, must gather",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:   sets.New[string]("default/gangA"),
				gangGroupID: "default/gangA",
				networkTopologySpec: &extension.NetworkTopologySpec{
					GatherStrategy: []extension.NetworkTopologyGatherRule{
						{
							Layer:    "BlockLayer",
							Strategy: extension.NetworkTopologyGatherStrategyMustGather,
						},
						{
							Layer:    "SpineLayer",
							Strategy: extension.NetworkTopologyGatherStrategyMustGather,
						},
					},
				},
			},
			/*
				Cluster Topology
				├── SpineLayer: s1
				│   ├── BlockLayer: b1
				│   │   ├── node-8
				│   │   └── node-1
				│   └── BlockLayer: b2
				│       ├── node-7
				│       └── node-2
				└── SpineLayer: s2
				    ├── BlockLayer: b3
				    │   ├── node-6
				    │   └── node-3
				    └── BlockLayer: b4
				        ├── node-5
				        └── node-4
			*/
			clusterNetworkTopology: networktopology.FakeClusterNetworkTopology,
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-8",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-7",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-6",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-3",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-5",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-4",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			wantScheduleStatus: nil,
			wantPlannedPodToNode: map[string]string{
				"default/pending-pod-1": "node-1",
				"default/pending-pod-2": "node-8",
			},
			wantDiagnosis: &frameworkext.Diagnosis{
				TopologyKeyToExplain: "BlockLayer",
				ScheduleDiagnosis: &frameworkext.ScheduleDiagnosis{
					SchedulingMode:      frameworkext.JobSchedulingMode,
					AlreadyWaitForBound: 0,
					NodeOfferSlot: map[string]int{
						"node-1": 1,
						"node-2": 1,
						"node-3": 1,
						"node-4": 1,
						"node-5": 1,
						"node-6": 1,
						"node-7": 1,
						"node-8": 1,
					},
					NodeToStatusMap: map[string]*framework.Status{
						"node-1": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-2": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-3": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-4": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-5": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-6": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-7": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-8": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					},
				},
			},
		},
		{
			name: "scheduled, all pods to one node",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("8"),
									},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("8"),
									},
								},
							},
						},
					},
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:   sets.New[string]("default/gangA"),
				gangGroupID: "default/gangA",
				networkTopologySpec: &extension.NetworkTopologySpec{
					GatherStrategy: []extension.NetworkTopologyGatherRule{
						{
							Layer:    "BlockLayer",
							Strategy: extension.NetworkTopologyGatherStrategyPreferGather,
						},
						{
							Layer:    "SpineLayer",
							Strategy: extension.NetworkTopologyGatherStrategyPreferGather,
						},
					},
				},
			},
			/*
				Cluster Topology
				├── SpineLayer: s1
				│   ├── BlockLayer: b1
				│   │   ├── node-8
				│   │   └── node-1
				│   └── BlockLayer: b2
				│       ├── node-7
				│       └── node-2
				└── SpineLayer: s2
				    ├── BlockLayer: b3
				    │   ├── node-6
				    │   └── node-3
				    └── BlockLayer: b4
				        ├── node-5
				        └── node-4
			*/
			clusterNetworkTopology: networktopology.FakeClusterNetworkTopology,
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-8",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-7",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-6",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-3",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-5",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-4",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			wantScheduleStatus: nil,
			wantPlannedPodToNode: map[string]string{
				"default/pending-pod-1": "node-1",
				"default/pending-pod-2": "node-1",
			},
			wantDiagnosis: &frameworkext.Diagnosis{
				ScheduleDiagnosis: &frameworkext.ScheduleDiagnosis{
					SchedulingMode:      frameworkext.JobSchedulingMode,
					AlreadyWaitForBound: 0,
					NodeOfferSlot: map[string]int{
						"node-1": 2,
						"node-2": 2,
						"node-3": 2,
						"node-4": 2,
						"node-5": 2,
						"node-6": 2,
						"node-7": 2,
						"node-8": 2,
					},
					NodeToStatusMap: map[string]*framework.Status{},
				},
			},
		},
		{
			name: "schedule failure, preempt success",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-3",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-4",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:   sets.New[string]("default/gangA"),
				gangGroupID: "default/gangA",
				networkTopologySpec: &extension.NetworkTopologySpec{
					GatherStrategy: []extension.NetworkTopologyGatherRule{
						{
							Layer:    "SpineLayer",
							Strategy: extension.NetworkTopologyGatherStrategyMustGather,
						},
					},
				},
			},
			/*
				Cluster Topology
				├── SpineLayer: s1
				│   ├── BlockLayer: b1
				│   │   ├── node-8
				│   │   └── node-1 existing-pod-1 low priority
				│   └── BlockLayer: b2
				│       ├── node-7
				│       └── node-2
				└── SpineLayer: s2
				    ├── BlockLayer: b3
				    │   ├── node-6
				    │   └── node-3
				    └── BlockLayer: b4
				        ├── node-5
				        └── node-4 existing-pod-2 low priority
			*/
			clusterNetworkTopology: networktopology.FakeClusterNetworkTopology,
			existingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-1",
						Namespace: "default",
						UID:       "existing-pod-1",
					},
					Spec: corev1.PodSpec{
						NodeName: "node-1",
						Priority: pointer.Int32(lowPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-2",
						Namespace: "default",
						UID:       "existing-pod-2",
					},
					Spec: corev1.PodSpec{
						NodeName: "node-4",
						Priority: pointer.Int32(lowPriority),
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
				},
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-8",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-7",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-6",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-3",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-5",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-4",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			wantScheduleStatus: framework.NewStatus(framework.Unschedulable, "no candidate topology nodes can accommodate job, desiredOfferSlot: 4, topology topologyNode SpineLayer/s1: 3;topology topologyNode SpineLayer/s2: 3, 0/8 nodes are available: 8 Insufficient cpu."),
			wantDiagnosis: &frameworkext.Diagnosis{
				TopologyKeyToExplain: "SpineLayer",
				ScheduleDiagnosis: &frameworkext.ScheduleDiagnosis{
					SchedulingMode:      frameworkext.JobSchedulingMode,
					AlreadyWaitForBound: 0,
					NodeOfferSlot: map[string]int{
						"node-1": 0,
						"node-2": 1,
						"node-3": 1,
						"node-4": 0,
						"node-5": 1,
						"node-6": 1,
						"node-7": 1,
						"node-8": 1,
					},
					NodeToStatusMap: map[string]*framework.Status{
						"node-1": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-2": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-3": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-4": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-5": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-6": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-7": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-8": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					},
				},
			},
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:                 "default/pending-pod-1",
				PreemptorKey:                  "default/gangA",
				Reason:                        ReasonTriggerPodPreemptSuccess,
				Message:                       "preempt success, alreadyWaitingForBound: 0/4",
				ClearNominatedNodeFailedMsg:   map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{},
				PodToNominatedNode: map[string]string{
					"default/pending-pod-1": "node-1",
					"default/pending-pod-2": "node-8",
					"default/pending-pod-3": "node-2",
					"default/pending-pod-4": "node-7",
				},
				SchedulingMode: frameworkext.JobSchedulingMode,
			},
			wantPreemptMessage: "preemption already attempted by default/pending-pod-1 with message preempt success, alreadyWaitingForBound: 0/4",
			wantPreempt:        framework.NewPostFilterResultWithNominatedNode("node-1"),
			wantPreemptStatus:  framework.NewStatus(framework.Success),
			wantPossibleVictims: []schedulingv1alpha1.PossibleVictim{
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-1", Namespace: "default"}},
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-2", Namespace: "default"}},
			},
			wantVictims: []schedulingv1alpha1.PossibleVictim{
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-1", Namespace: "default"}},
			},
			wantJobPods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
					Status: corev1.PodStatus{
						NominatedNodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
					Status: corev1.PodStatus{
						NominatedNodeName: "node-8",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-3",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
					Status: corev1.PodStatus{
						NominatedNodeName: "node-2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-4",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
					Status: corev1.PodStatus{
						NominatedNodeName: "node-7",
					},
				},
			},
		},
		{
			name: "schedule failure, preempt failure",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-3",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-4",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:   sets.New[string]("default/gangA"),
				gangGroupID: "default/gangA",
				networkTopologySpec: &extension.NetworkTopologySpec{
					GatherStrategy: []extension.NetworkTopologyGatherRule{
						{
							Layer:    "SpineLayer",
							Strategy: extension.NetworkTopologyGatherStrategyMustGather,
						},
					},
				},
			},
			/*
				Cluster Topology
				├── SpineLayer: s1
				│   ├── BlockLayer: b1
				│   │   ├── node-8
				│   │   └── node-1 existing-pod-1 high priority
				│   └── BlockLayer: b2
				│       ├── node-7
				│       └── node-2 existing-pod-3 low priority
				└── SpineLayer: s2
				    ├── BlockLayer: b3
				    │   ├── node-6
				    │   └── node-3
				    └── BlockLayer: b4
				        ├── node-5
				        └── node-4 existing-pod-2 high priority
			*/
			clusterNetworkTopology: networktopology.FakeClusterNetworkTopology,
			existingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-1",
						Namespace: "default",
						UID:       "existing-pod-1",
					},
					Spec: corev1.PodSpec{
						NodeName: "node-1",
						Priority: pointer.Int32(highPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-3",
						Namespace: "default",
						UID:       "existing-pod-3",
					},
					Spec: corev1.PodSpec{
						NodeName: "node-2",
						Priority: pointer.Int32(lowPriority),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-2",
						Namespace: "default",
						UID:       "existing-pod-2",
					},
					Spec: corev1.PodSpec{
						NodeName: "node-4",
						Priority: pointer.Int32(highPriority),
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
				},
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-8",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b1",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-7",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s1",
							networktopology.FakeBlockLabel: "b2",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-6",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-3",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-5",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-4",
						Labels: map[string]string{
							networktopology.FakeSpineLabel: "s2",
							networktopology.FakeBlockLabel: "b4",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("16"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			wantScheduleStatus: framework.NewStatus(framework.Unschedulable, "no candidate topology nodes can accommodate job, desiredOfferSlot: 4, topology topologyNode SpineLayer/s1: 2;topology topologyNode SpineLayer/s2: 3, 0/8 nodes are available: 8 Insufficient cpu."),
			wantDiagnosis: &frameworkext.Diagnosis{
				TopologyKeyToExplain: "SpineLayer",
				ScheduleDiagnosis: &frameworkext.ScheduleDiagnosis{
					SchedulingMode:      frameworkext.JobSchedulingMode,
					AlreadyWaitForBound: 0,
					NodeOfferSlot: map[string]int{
						"node-1": 0,
						"node-2": 0,
						"node-3": 1,
						"node-4": 0,
						"node-5": 1,
						"node-6": 1,
						"node-7": 1,
						"node-8": 1,
					},
					NodeToStatusMap: map[string]*framework.Status{
						"node-1": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-2": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-3": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-4": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-5": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-6": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-7": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
						"node-8": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					},
				},
			},
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:                 "default/pending-pod-1",
				PreemptorKey:                  "default/gangA",
				Reason:                        ReasonPreemptionNotHelpful,
				Message:                       "no candidate topology nodes can accommodate job, desiredOfferSlot: 4, topology topologyNode SpineLayer/s1: 3;topology topologyNode SpineLayer/s2: 3, 0/8 nodes are available: 8 Insufficient cpu.",
				ClearNominatedNodeFailedMsg:   map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{},
				statusMap: map[string]*framework.Status{
					"node-1": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					"node-2": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					"node-3": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					"node-4": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					"node-5": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					"node-6": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					"node-7": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
					"node-8": framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
				},
				NodeToOfferSlot: map[string]int{
					"node-1": 0,
					"node-2": 1,
					"node-3": 1,
					"node-4": 0,
					"node-5": 1,
					"node-6": 1,
					"node-7": 1,
					"node-8": 1,
				},
				SchedulingMode: frameworkext.JobSchedulingMode,
			},
			wantPreemptMessage: "preemption already attempted by default/pending-pod-1 with message no candidate topology nodes can accommodate job, desiredOfferSlot: 4, topology topologyNode SpineLayer/s1: 3;topology topologyNode SpineLayer/s2: 3, 0/8 nodes are available: 8 Insufficient cpu.",
			wantPreempt:        framework.NewPostFilterResultWithNominatedNode(""),
			wantPreemptStatus:  framework.NewStatus(framework.Unschedulable, `no candidate topology nodes can accommodate job, desiredOfferSlot: 4, topology topologyNode SpineLayer/s1: 3;topology topologyNode SpineLayer/s2: 3, 0/8 nodes are available: 8 Insufficient cpu.`),
			wantPossibleVictims: []schedulingv1alpha1.PossibleVictim{
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-3", Namespace: "default"}},
			},
			wantJobPods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
					Status: corev1.PodStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
					Status: corev1.PodStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-3",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
					Status: corev1.PodStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-4",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
					Status: corev1.PodStatus{},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extendedFramework := NewFakeExtendedFramework(t, tt.nodes, tt.existingPods, tt.existingNominatedPods, tt.filterPlugin, tt.clusterNetworkTopology)
			if tt.gangSchedulingContext != nil && tt.gangSchedulingContext.networkTopologySpec != nil {
				tt.gangSchedulingContext.networkTopologySnapshot = extendedFramework.GetNetworkTopologyTreeManager().GetSnapshot()
			}
			podStore := extendedFramework.SharedInformerFactory().Core().V1().Pods().Informer().GetStore()
			logger := klog.FromContext(context.TODO())
			pgMgr := &PodGroupManager{handle: extendedFramework, networkTopologySolver: NewNetworkTopologySolver(extendedFramework)}
			pgMgr.holder = GangSchedulingContextHolder{gangSchedulingContext: tt.gangSchedulingContext}
			pgMgr.cache = NewGangCache(nil, nil, nil, nil, nil)
			for i := range tt.allPendingPods {
				pod := tt.allPendingPods[i]
				pgMgr.cache.onPodAdd(pod)
				err := podStore.Add(pod)
				assert.NoError(t, err)
				_, _ = extendedFramework.ClientSet().CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				if pod.Status.NominatedNodeName != "" {
					podInfo, _ := framework.NewPodInfo(pod)
					extendedFramework.AddNominatedPod(logger, podInfo, &framework.NominatingInfo{
						NominatingMode:    framework.ModeOverride,
						NominatedNodeName: pod.Status.NominatedNodeName,
					})
				}
			}

			ctx := context.Background()
			cycleState := framework.NewCycleState()
			m := framework.NodeToStatusMap{}
			frameworkext.InitDiagnosis(cycleState, tt.triggerPod)
			if tt.gangSchedulingContext != nil {
				tt.gangSchedulingContext.triggerPod = tt.triggerPod
			}

			_, status := pgMgr.FindOneNode(ctx, cycleState, tt.triggerPod, nil)
			assert.Equal(t, tt.wantScheduleStatus, status)
			diagnosis := frameworkext.GetDiagnosis(cycleState)
			diagnosis.TargetPod = nil
			diagnosis.Timestamp = metav1.Time{}
			assert.Equal(t, tt.wantDiagnosis, diagnosis)
			assert.Equal(t, tt.wantPlannedPodToNode, tt.gangSchedulingContext.networkTopologyPlannedNodes)
			if status.IsSuccess() {
				return
			}

			ev := NewPreemptionEvaluator(extendedFramework, pgMgr.cache, &pgMgr.holder, pgMgr.networkTopologySolver).(*preemptionEvaluatorImpl)
			preemptionState := &JobPreemptionState{
				TerminatingPodOnNominatedNode: map[string]string{},
				ClearNominatedNodeFailedMsg:   map[string]string{},
			}
			ctx = contextWithJobPreemptionState(context.Background(), preemptionState)
			gotResult, gotStatus := ev.preempt(ctx, cycleState, tt.triggerPod, m)
			assert.Equal(t, tt.wantPreempt, gotResult)
			assert.Equal(t, tt.wantPreemptStatus, gotStatus)
			sort.Slice(preemptionState.allPendingPods, func(i, j int) bool {
				return preemptionState.allPendingPods[i].Name < preemptionState.allPendingPods[j].Name
			})
			sort.Slice(preemptionState.allWaitingPods, func(i, j int) bool {
				return preemptionState.allWaitingPods[i].Name < preemptionState.allWaitingPods[j].Name
			})
			assert.Equal(t, tt.allPendingPods, preemptionState.allPendingPods)
			preemptionState.allPendingPods = nil
			preemptionState.allWaitingPods = nil
			preemptionState.allPods = nil
			preemptionState.DurationOfCycleStateClone = metav1.Duration{}
			preemptionState.DurationOfNodeInfoClone = metav1.Duration{}
			preemptionState.DurationOfRemovePossibleVictims = metav1.Duration{}
			preemptionState.DurationOfPlaceToSchedulePods = metav1.Duration{}
			preemptionState.DurationOfSelectVictimsOnNode = metav1.Duration{}
			preemptionState.DurationOfPrepareCandidates = metav1.Duration{}
			preemptionState.DurationOfMakeNomination = metav1.Duration{}
			preemptionState.DurationOfCancelNomination = metav1.Duration{}
			var gotPossibleVictims []schedulingv1alpha1.PossibleVictim
			for _, possibleVictims := range preemptionState.possibleVictims {
				for _, victim := range possibleVictims {
					gotPossibleVictims = append(gotPossibleVictims, schedulingv1alpha1.PossibleVictim{
						NamespacedName: schedulingv1alpha1.NamespacedName{
							Name:      victim.Pod.Name,
							Namespace: victim.Pod.Namespace,
						},
					})
				}

			}
			sort.Slice(gotPossibleVictims, func(i, j int) bool {
				return gotPossibleVictims[i].NamespacedName.Name < gotPossibleVictims[j].NamespacedName.Name
			})
			assert.Equal(t, tt.wantPossibleVictims, gotPossibleVictims)
			preemptionState.possibleVictims = nil
			var gotVictims []schedulingv1alpha1.PossibleVictim
			for _, victims := range preemptionState.victims {
				for _, victim := range victims {
					gotVictims = append(gotVictims, schedulingv1alpha1.PossibleVictim{
						NamespacedName: schedulingv1alpha1.NamespacedName{
							Name:      victim.Name,
							Namespace: victim.Namespace,
						},
					})
				}
			}
			sort.Slice(gotVictims, func(i, j int) bool {
				return gotVictims[i].NamespacedName.Name < gotVictims[j].NamespacedName.Name
			})
			assert.Equal(t, tt.wantVictims, gotVictims)
			for _, victim := range gotVictims {
				_, err := extendedFramework.ClientSet().CoreV1().Pods(victim.Namespace).Get(context.TODO(), victim.Name, metav1.GetOptions{})
				assert.True(t, errors.IsNotFound(err))
			}
			preemptionState.victims = nil
			preemptionState.gangSchedulingContext = nil
			assert.Equal(t, tt.wantPreemptionState, preemptionState)
			if tt.gangSchedulingContext != nil {
				assert.Equal(t, tt.wantPreemptMessage, tt.gangSchedulingContext.preemptionMessage)
			}
			if len(tt.wantJobPods) > 0 {
				gotJobPods, _ := extendedFramework.ClientSet().CoreV1().Pods(tt.wantJobPods[0].Namespace).List(context.TODO(), metav1.ListOptions{
					LabelSelector: fmt.Sprintf(v1alpha1.PodGroupLabel + "=" + gangName),
				})
				sort.Slice(gotJobPods.Items, func(i, j int) bool {
					return gotJobPods.Items[i].Name < gotJobPods.Items[j].Name
				})
				assert.Equal(t, tt.wantJobPods, gotJobPods.Items)
			}
		})
	}
}
