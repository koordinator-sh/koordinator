/*
Copyright 2022 The Koordinator Authors.
Copyright 2020 The Kubernetes Authors.

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

package reservation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func Test_newPreemptionMgr(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		suit := newPluginTestSuitWith(t,
			nil,
			nil,
			func(args *config.ReservationArgs) {
				args.EnablePreemption = true
			})
		p, err := suit.pluginFactory()
		assert.NoError(t, err)
		assert.NotNil(t, p)
		pl, ok := p.(*Plugin)
		assert.True(t, ok)
		assert.NotNil(t, pl.preemptionMgr)
		assert.Equal(t, Name, pl.preemptionMgr.Name())
	})
}

func TestPostFilterWithPreemption(t *testing.T) {
	preemptionPolicyNever := corev1.PreemptNever
	testFilterReservationStatus := framework.NewStatus(framework.Unschedulable,
		reservationutil.NewReservationReason("Insufficient nvidia.com/gpu"),
		reservationutil.NewReservationReason("Insufficient koordinator.sh/gpu-mem-ratio"))
	testFilterReservationStatus1 := framework.NewStatus(framework.Unschedulable,
		reservationutil.NewReservationReason("Insufficient cpu"),
		"Insufficient memory")
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-0",
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
		},
	}
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			Priority:         pointer.Int32(extension.PriorityProdValueMax),
			PreemptionPolicy: &preemptionPolicyNever,
		},
	}
	testReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation2C4G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Priority:         pointer.Int32(extension.PriorityProdValueMin),
					PreemptionPolicy: &preemptionPolicyNever,
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: testNode.Name,
		},
	}
	testReservePod := reservationutil.NewReservePod(testReservation)
	type fields struct {
		pods         []*corev1.Pod
		reservePods  []*corev1.Pod
		nodes        []*corev1.Node
		reservations []*schedulingv1alpha1.Reservation
	}
	type args struct {
		hasStateData             bool
		hasAffinity              bool
		nodeReservationDiagnosis map[string]*nodeDiagnosisState
		filteredNodeStatusMap    framework.NodeToStatusMap
		enablePreemption         bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *framework.PostFilterResult
		want1  *framework.Status
	}{
		{
			name: "no reservation filtering",
			args: args{
				hasStateData:          false,
				filteredNodeStatusMap: framework.NodeToStatusMap{},
			},
			want:  nil,
			want1: framework.NewStatus(framework.Unschedulable),
		},
		{
			name: "show reservation reasons without preemption",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 0,
						affinityUnmatched:        1,
					},
					"test-node-1": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 1,
						affinityUnmatched:        0,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": testFilterReservationStatus,
					"test-node-1": testFilterReservationStatus1,
				},
				enablePreemption: false,
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"1 Reservation(s) didn't match affinity rules",
				"1 Reservation(s) is unschedulable",
				"1 Reservation(s) for node reason that Insufficient memory",
				"6 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation reasons with preemption",
			fields: fields{
				pods: []*corev1.Pod{
					testPod,
				},
				reservePods: []*corev1.Pod{
					testReservePod,
				},
				nodes: []*corev1.Node{
					testNode,
				},
				reservations: []*schedulingv1alpha1.Reservation{
					testReservation,
				},
			},
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 0,
						affinityUnmatched:        1,
					},
					"test-node-1": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 1,
						affinityUnmatched:        0,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": testFilterReservationStatus,
					"test-node-1": testFilterReservationStatus1,
				},
				enablePreemption: true,
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"preemption: not eligible due to preemptionPolicy=Never.",
				"1 Reservation(s) didn't match affinity rules",
				"1 Reservation(s) is unschedulable",
				"1 Reservation(s) for node reason that Insufficient memory",
				"6 Reservation(s) matched owner total"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t,
				append(tt.fields.pods, tt.fields.reservePods...),
				tt.fields.nodes,
				func(args *config.ReservationArgs) {
					args.EnablePreemption = tt.args.enablePreemption
				})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			assert.NotNil(t, p)
			pl, ok := p.(*Plugin)
			assert.True(t, ok)
			for _, pod := range tt.fields.pods {
				_, err = pl.handle.ClientSet().CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			for _, node := range tt.fields.nodes {
				_, err = pl.handle.ClientSet().CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			for _, reservation := range tt.fields.reservations {
				_, err = pl.handle.KoordinatorClientSet().SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			suit.start()

			cycleState := framework.NewCycleState()
			if tt.args.hasStateData {
				cycleState.Write(stateKey, &stateData{
					schedulingStateData: schedulingStateData{
						hasAffinity:              tt.args.hasAffinity,
						nodeReservationDiagnosis: tt.args.nodeReservationDiagnosis,
					},
				})
			}
			got, got1 := pl.PostFilter(context.TODO(), cycleState, testPod, tt.args.filteredNodeStatusMap)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestPreemptionMgrSelectVictimsOnNode(t *testing.T) {
	preemptionPolicyLowerPriority := corev1.PreemptLowerPriority
	preemptionPolicyNever := corev1.PreemptNever
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-0",
			UID:  uuid.NewUUID(),
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
		},
	}
	testHPPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hp-pod",
			Namespace: "test-ns",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			Priority:         pointer.Int32(extension.PriorityProdValueMax),
			PreemptionPolicy: &preemptionPolicyNever,
			NodeName:         testNode.Name,
		},
	}
	testLPPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-lp-pod",
			Namespace: "test-ns",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			Priority:         pointer.Int32(extension.PriorityProdValueMin),
			PreemptionPolicy: &preemptionPolicyLowerPriority,
			NodeName:         testNode.Name,
		},
	}
	testReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation2C4G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
					Priority:         pointer.Int32(extension.PriorityProdValueMax),
					PreemptionPolicy: &preemptionPolicyLowerPriority,
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationPending,
		},
	}
	testReservePod := reservationutil.NewReservePod(testReservation)
	testNodeInfo := framework.NewNodeInfo()
	testNodeInfo.SetNode(testNode)
	testNodeInfo.AddPod(testLPPod)
	testNodeInfo.AddPod(testHPPod)
	testNodeInfo1 := framework.NewNodeInfo()
	testNodeInfo1.SetNode(testNode)
	testNodeInfo1.AddPod(testLPPod)
	type fields struct {
		pods         []*corev1.Pod
		reservePods  []*corev1.Pod
		nodes        []*corev1.Node
		reservations []*schedulingv1alpha1.Reservation
	}
	type args struct {
		state    *framework.CycleState
		pod      *corev1.Pod
		nodeInfo *framework.NodeInfo
		pdbs     []*policy.PodDisruptionBudget
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []*corev1.Pod
		want1  int
		want2  *framework.Status
	}{
		{
			name: "reserve pod preempt successfully",
			fields: fields{
				pods: []*corev1.Pod{
					testLPPod,
					testHPPod,
				},
				reservePods: []*corev1.Pod{
					testReservePod,
				},
				nodes: []*corev1.Node{
					testNode,
				},
				reservations: []*schedulingv1alpha1.Reservation{
					testReservation,
				},
			},
			args: args{
				state:    framework.NewCycleState(),
				pod:      testReservePod,
				nodeInfo: testNodeInfo.Clone(),
				pdbs:     nil,
			},
			want:  nil,
			want1: 0,
			want2: framework.NewStatus(framework.Success),
		},
		{
			name: "compatible to pod preemption",
			fields: fields{
				pods: []*corev1.Pod{
					testLPPod,
				},
				nodes: []*corev1.Node{
					testNode,
				},
			},
			args: args{
				state:    framework.NewCycleState(),
				pod:      testHPPod,
				nodeInfo: testNodeInfo1.Clone(),
				pdbs:     nil,
			},
			want:  nil,
			want1: 0,
			want2: framework.NewStatus(framework.Success),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t,
				append(tt.fields.pods, tt.fields.reservePods...),
				tt.fields.nodes,
				func(args *config.ReservationArgs) {
					args.EnablePreemption = true
				})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			assert.NotNil(t, p)
			pl, ok := p.(*Plugin)
			assert.True(t, ok)
			for _, pod := range tt.fields.pods {
				_, err = pl.handle.ClientSet().CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			for _, node := range tt.fields.nodes {
				_, err = pl.handle.ClientSet().CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			for _, reservation := range tt.fields.reservations {
				_, err = pl.handle.KoordinatorClientSet().SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			suit.start()

			got, got1, got2 := pl.preemptionMgr.SelectVictimsOnNode(context.TODO(), tt.args.state, tt.args.pod, tt.args.nodeInfo, tt.args.pdbs)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
			assert.Equal(t, tt.want2, got2)
		})
	}
}
