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

package reservation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestRestoreReservation(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}
	// normal pods allocated 12C24Gi and ports 8080/9090
	pods := []*corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod-1",
				UID:       uuid.NewUUID(),
			},
			Spec: corev1.PodSpec{
				NodeName: node.Name,
				Affinity: &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"app": "test-app-1",
									},
								},
								TopologyKey: corev1.LabelHostname,
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("4"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
						},
						Ports: []corev1.ContainerPort{
							{
								HostIP:   "0.0.0.0",
								Protocol: corev1.ProtocolTCP,
								HostPort: 8080,
							},
						},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod-2",
				UID:       uuid.NewUUID(),
			},
			Spec: corev1.PodSpec{
				NodeName: node.Name,
				Affinity: &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"app": "test-app-2",
									},
								},
								TopologyKey: corev1.LabelHostname,
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("8"),
								corev1.ResourceMemory: resource.MustParse("16Gi"),
							},
						},
						Ports: []corev1.ContainerPort{
							{
								HostIP:   "0.0.0.0",
								Protocol: corev1.ProtocolTCP,
								HostPort: 9090,
							},
						},
					},
				},
			},
		},
	}

	// unmatched reservation allocated 12C24Gi, but assigned Pod with 4C8Gi, remaining 8C16Gi
	unmatchedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation12C24G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocateOnce: pointer.Bool(false),
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "test-app-2",
										},
									},
									TopologyKey: corev1.LabelHostname,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(12, resource.DecimalSI),
									corev1.ResourceMemory: resource.MustParse("24Gi"),
								},
							},
							Ports: []corev1.ContainerPort{
								{
									HostIP:   "0.0.0.0",
									Protocol: corev1.ProtocolTCP,
									HostPort: 8080,
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(12, resource.DecimalSI),
				corev1.ResourceMemory: resource.MustParse("24Gi"),
			},
		},
	}
	pods = append(pods, reservationutil.NewReservePod(unmatchedReservation))

	// matchedReservation allocated 8C16Gi
	matchedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation8C16G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation": "true",
						},
					},
				},
			},
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "test-app-3",
										},
									},
									TopologyKey: corev1.LabelHostname,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("8"),
									corev1.ResourceMemory: resource.MustParse("16Gi"),
								},
							},
							Ports: []corev1.ContainerPort{
								{
									HostIP:   "0.0.0.0",
									Protocol: corev1.ProtocolTCP,
									HostPort: 7070,
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
	pods = append(pods, reservationutil.NewReservePod(matchedReservation))

	podAllocatedWithUnmatchedReservation := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "unmatched-allocated-pod-1",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
		},
	}
	pods = append(pods, podAllocatedWithUnmatchedReservation)

	suit := newPluginTestSuitWith(t, pods, []*corev1.Node{node})
	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	pl := p.(*Plugin)

	nodeInfo, err := suit.fw.SnapshotSharedLister().NodeInfos().Get(node.Name)
	assert.NoError(t, err)
	expectedRequestedResources := &framework.Resource{
		MilliCPU: 36000,
		Memory:   72 * 1024 * 1024 * 1024,
	}
	assert.Equal(t, expectedRequestedResources, nodeInfo.Requested)

	pl.reservationCache.updateReservation(unmatchedReservation)
	pl.reservationCache.updateReservation(matchedReservation)

	pl.reservationCache.addPod(unmatchedReservation.UID, podAllocatedWithUnmatchedReservation)

	matchRInfo := pl.reservationCache.getReservationInfoByUID(matchedReservation.UID)
	unmatchedRInfo := pl.reservationCache.getReservationInfoByUID(unmatchedReservation.UID)

	cycleState := framework.NewCycleState()
	_, restored, status := pl.BeforePreFilter(context.TODO(), cycleState, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"test-reservation": "true",
			},
		},
	})
	assert.True(t, restored)
	assert.True(t, status.IsSuccess())

	status = pl.AfterPreFilter(context.TODO(), cycleState, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"test-reservation": "true",
			},
		},
	}, &framework.PreFilterResult{})
	assert.True(t, status.IsSuccess())

	expectedStat := &stateData{
		schedulingStateData: schedulingStateData{
			podRequests:          corev1.ResourceList{},
			podRequestsResources: framework.NewResource(nil),
			preemptible:          map[string]corev1.ResourceList{},
			preemptibleInRRs:     map[string]map[types.UID]corev1.ResourceList{},
			nodeReservationStates: map[string]*nodeReservationState{
				node.Name: {
					nodeName:         node.Name,
					matchedOrIgnored: []*frameworkext.ReservationInfo{matchRInfo},
					unmatched:        []*frameworkext.ReservationInfo{unmatchedRInfo},
					podRequested: &framework.Resource{
						MilliCPU: 32000,
						Memory:   68719476736,
					},
					rAllocated:    framework.NewResource(nil),
					preRestored:   true,
					finalRestored: true,
				},
			},
			nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
				node.Name: {
					nodeName:                 node.Name,
					ownerMatched:             1,
					affinityUnmatched:        0,
					isUnschedulableUnmatched: 0,
					taintsUnmatchedReasons:   map[string]int{},
				},
			},
		},
	}
	assert.Equal(t, expectedStat, getStateData(cycleState))

	unmatchedReservePod := pods[2].DeepCopy()
	unmatchedReservePod.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("12"),
		corev1.ResourceMemory: resource.MustParse("24Gi"),
	}
	expectNodeInfo := framework.NewNodeInfo(pods[0], pods[1], unmatchedReservePod, podAllocatedWithUnmatchedReservation)
	expectNodeInfo.SetNode(node)
	expectNodeInfo.Requested.MilliCPU -= 4000
	expectNodeInfo.Requested.Memory -= 8 * 1024 * 1024 * 1024
	expectNodeInfo.NonZeroRequested.MilliCPU -= 4000
	expectNodeInfo.NonZeroRequested.Memory -= 8 * 1024 * 1024 * 1024
	assert.Equal(t, expectNodeInfo.Requested, nodeInfo.Requested)
	assert.Equal(t, expectNodeInfo.UsedPorts, nodeInfo.UsedPorts)
	nodeInfo.Generation = 0
	expectNodeInfo.Generation = 0
	assert.True(t, equality.Semantic.DeepEqual(expectNodeInfo, nodeInfo))
}

func TestRestoreReservationWithLazyReservationRestore(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}
	// normal pods allocated 12C24Gi and ports 8080/9090
	pods := []*corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod-1",
				UID:       uuid.NewUUID(),
			},
			Spec: corev1.PodSpec{
				NodeName: node.Name,
				Affinity: &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"app": "test-app-1",
									},
								},
								TopologyKey: corev1.LabelHostname,
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("4"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
						},
						Ports: []corev1.ContainerPort{
							{
								HostIP:   "0.0.0.0",
								Protocol: corev1.ProtocolTCP,
								HostPort: 8080,
							},
						},
					},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod-2",
				UID:       uuid.NewUUID(),
			},
			Spec: corev1.PodSpec{
				NodeName: node.Name,
				Affinity: &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"app": "test-app-2",
									},
								},
								TopologyKey: corev1.LabelHostname,
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("8"),
								corev1.ResourceMemory: resource.MustParse("16Gi"),
							},
						},
						Ports: []corev1.ContainerPort{
							{
								HostIP:   "0.0.0.0",
								Protocol: corev1.ProtocolTCP,
								HostPort: 9090,
							},
						},
					},
				},
			},
		},
	}

	// unmatched reservation allocated 12C24Gi, but assigned Pod with 4C8Gi, remaining 8C16Gi
	unmatchedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation12C24G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocateOnce: pointer.Bool(false),
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "test-app-2",
										},
									},
									TopologyKey: corev1.LabelHostname,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(12, resource.DecimalSI),
									corev1.ResourceMemory: resource.MustParse("24Gi"),
								},
							},
							Ports: []corev1.ContainerPort{
								{
									HostIP:   "0.0.0.0",
									Protocol: corev1.ProtocolTCP,
									HostPort: 8080,
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(12, resource.DecimalSI),
				corev1.ResourceMemory: resource.MustParse("24Gi"),
			},
		},
	}
	pods = append(pods, reservationutil.NewReservePod(unmatchedReservation))

	// matchedReservation allocated 8C16Gi
	matchedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation8C16G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation": "true",
						},
					},
				},
			},
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "test-app-3",
										},
									},
									TopologyKey: corev1.LabelHostname,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("8"),
									corev1.ResourceMemory: resource.MustParse("16Gi"),
								},
							},
							Ports: []corev1.ContainerPort{
								{
									HostIP:   "0.0.0.0",
									Protocol: corev1.ProtocolTCP,
									HostPort: 7070,
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
	pods = append(pods, reservationutil.NewReservePod(matchedReservation))

	podAllocatedWithUnmatchedReservation := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "unmatched-allocated-pod-1",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
		},
	}
	pods = append(pods, podAllocatedWithUnmatchedReservation)

	suit := newPluginTestSuitWith(t, pods, []*corev1.Node{node})
	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	pl := p.(*Plugin)
	pl.enableLazyReservationRestore = true

	nodeInfo, err := suit.fw.SnapshotSharedLister().NodeInfos().Get(node.Name)
	assert.NoError(t, err)
	expectedRequestedResources := &framework.Resource{
		MilliCPU: 36000,
		Memory:   72 * 1024 * 1024 * 1024,
	}
	assert.Equal(t, expectedRequestedResources, nodeInfo.Requested)

	pl.reservationCache.updateReservation(unmatchedReservation)
	pl.reservationCache.updateReservation(matchedReservation)

	pl.reservationCache.addPod(unmatchedReservation.UID, podAllocatedWithUnmatchedReservation)

	matchRInfo := pl.reservationCache.getReservationInfoByUID(matchedReservation.UID)
	unmatchedRInfo := pl.reservationCache.getReservationInfoByUID(unmatchedReservation.UID)

	cycleState := framework.NewCycleState()
	_, restored, status := pl.BeforePreFilter(context.TODO(), cycleState, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"test-reservation": "true",
			},
		},
	})
	assert.True(t, restored)
	assert.True(t, status.IsSuccess())

	status = pl.AfterPreFilter(context.TODO(), cycleState, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"test-reservation": "true",
			},
		},
	}, &framework.PreFilterResult{})
	assert.True(t, status.IsSuccess())

	expectedStat := &stateData{
		schedulingStateData: schedulingStateData{
			podRequests:          corev1.ResourceList{},
			podRequestsResources: framework.NewResource(nil),
			preemptible:          map[string]corev1.ResourceList{},
			preemptibleInRRs:     map[string]map[types.UID]corev1.ResourceList{},
			nodeReservationStates: map[string]*nodeReservationState{
				node.Name: {
					nodeName:         node.Name,
					matchedOrIgnored: []*frameworkext.ReservationInfo{matchRInfo},
					unmatched:        []*frameworkext.ReservationInfo{unmatchedRInfo},
					podRequested: &framework.Resource{
						MilliCPU: 32000,
						Memory:   68719476736,
					},
					rAllocated:    framework.NewResource(nil),
					preRestored:   true,
					finalRestored: true,
				},
			},
			nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
				node.Name: {
					nodeName:                 node.Name,
					ownerMatched:             1,
					affinityUnmatched:        0,
					isUnschedulableUnmatched: 0,
					taintsUnmatchedReasons:   map[string]int{},
				},
			},
		},
	}
	assert.Equal(t, expectedStat, getStateData(cycleState))

	unmatchedReservePod := pods[2].DeepCopy()
	unmatchedReservePod.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("12"),
		corev1.ResourceMemory: resource.MustParse("24Gi"),
	}
	expectNodeInfo := framework.NewNodeInfo(pods[0], pods[1], unmatchedReservePod, podAllocatedWithUnmatchedReservation)
	expectNodeInfo.SetNode(node)
	expectNodeInfo.Requested.MilliCPU -= 4000
	expectNodeInfo.Requested.Memory -= 8 * 1024 * 1024 * 1024
	expectNodeInfo.NonZeroRequested.MilliCPU -= 4000
	expectNodeInfo.NonZeroRequested.Memory -= 8 * 1024 * 1024 * 1024
	assert.Equal(t, expectNodeInfo.Requested, nodeInfo.Requested)
	assert.Equal(t, expectNodeInfo.UsedPorts, nodeInfo.UsedPorts)
	nodeInfo.Generation = 0
	expectNodeInfo.Generation = 0
	assert.True(t, equality.Semantic.DeepEqual(expectNodeInfo, nodeInfo))
}

func Test_matchReservation(t *testing.T) {
	tests := []struct {
		name                string
		reservation         *schedulingv1alpha1.Reservation
		reservationAffinity *apiext.ReservationAffinity
		want                bool
	}{
		{
			name: "nothing to match",
			reservation: &schedulingv1alpha1.Reservation{
				Spec: schedulingv1alpha1.ReservationSpec{
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "test",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "match reservation affinity",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"reservation-type": "reservation-test",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "test",
								},
							},
						},
					},
				},
			},
			reservationAffinity: &apiext.ReservationAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &apiext.ReservationAffinitySelector{
					ReservationSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "reservation-type",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"reservation-test"},
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "not match reservation affinity",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"reservation-type": "reservation-test-not-match",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "test",
								},
							},
						},
					},
				},
			},
			reservationAffinity: &apiext.ReservationAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &apiext.ReservationAffinitySelector{
					ReservationSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "reservation-type",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"reservation-test"},
								},
							},
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			}
			if tt.reservationAffinity != nil {
				affinityData, err := json.Marshal(tt.reservationAffinity)
				assert.NoError(t, err)
				if pod.Annotations == nil {
					pod.Annotations = map[string]string{}
				}
				pod.Annotations[apiext.AnnotationReservationAffinity] = string(affinityData)
			}
			reservationAffinity, err := reservationutil.GetRequiredReservationAffinity(pod)
			assert.NoError(t, err)
			rInfo := frameworkext.NewReservationInfo(tt.reservation)
			got := matchReservationAffinity(&corev1.Node{}, rInfo, reservationAffinity)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBeforePreFilterWithReservationAffinity(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"test": "true",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}
	matchedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation8C16G",
			Labels: map[string]string{
				"reservation-a": "true",
			},
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation": "true",
						},
					},
				},
			},
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("8"),
									corev1.ResourceMemory: resource.MustParse("16Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: node.Name,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
	var pods []*corev1.Pod
	pods = append(pods, reservationutil.NewReservePod(matchedReservation))

	tests := []struct {
		name         string
		rAffinity    *apiext.ReservationAffinity
		wantRestored bool
	}{
		{
			name:         "pod has no reservation affinity",
			wantRestored: true,
		},
		{
			name: "pod has reservation affinity and matched",
			rAffinity: &apiext.ReservationAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &apiext.ReservationAffinitySelector{
					ReservationSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "reservation-a",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
					},
				},
			},
			wantRestored: true,
		},
		{
			name: "pod has reservation affinity but failed to match",
			rAffinity: &apiext.ReservationAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &apiext.ReservationAffinitySelector{
					ReservationSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "reservation-a",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"false"},
								},
							},
						},
					},
				},
			},
			wantRestored: false,
		},
		{
			name: "pod specifies a reservation name and matched",
			rAffinity: &apiext.ReservationAffinity{
				Name: "reservation8C16G",
			},
			wantRestored: true,
		},
		{
			name: "pod specifies a reservation name but failed to match",
			rAffinity: &apiext.ReservationAffinity{
				Name: "not-reservation8C16G",
			},
			wantRestored: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, pods, []*corev1.Node{node})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)

			pl.reservationCache.updateReservation(matchedReservation)

			testPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"test-reservation": "true",
					},
				},
			}
			if tt.rAffinity != nil {
				assert.NoError(t, apiext.SetReservationAffinity(testPod, tt.rAffinity))
			}
			cycleState := framework.NewCycleState()
			_, restored, status := pl.BeforePreFilter(context.TODO(), cycleState, testPod)
			assert.Equal(t, tt.wantRestored, restored)
			assert.True(t, status.IsSuccess())
		})
	}
}

func TestBeforePreFilterWithNodeAffinity(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"test": "true",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}
	matchedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation8C16G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation": "true",
						},
					},
				},
			},
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("8"),
									corev1.ResourceMemory: resource.MustParse("16Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: node.Name,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
	var pods []*corev1.Pod
	pods = append(pods, reservationutil.NewReservePod(matchedReservation))
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"test-reservation": "true",
			},
		},
	}

	tests := []struct {
		name         string
		pod          *corev1.Pod
		nodeAffinity *corev1.NodeAffinity
		wantRestored bool
	}{
		{
			name:         "pod has no affinity",
			pod:          testPod.DeepCopy(),
			nodeAffinity: nil,
			wantRestored: true,
		},
		{
			name: "pod has affinity with matchField",
			pod:  testPod.DeepCopy(),
			nodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchFields: []corev1.NodeSelectorRequirement{
								{
									Key:      metav1.ObjectNameField,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"node1", "node2"},
								},
							},
						},
					},
				},
			},
			wantRestored: true,
		},
		{
			name: "pod has affinity with matchField but failed to match",
			pod:  testPod.DeepCopy(),
			nodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchFields: []corev1.NodeSelectorRequirement{
								{
									Key:      metav1.ObjectNameField,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"node2"},
								},
							},
						},
					},
				},
			},
			wantRestored: false,
		},
		{
			name: "pod has affinity with matchExpressions",
			pod:  testPod.DeepCopy(),
			nodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "test",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
					},
				},
			},
			wantRestored: true,
		},
		{
			name: "pod has affinity with matchExpressions but failed to match",
			pod:  testPod.DeepCopy(),
			nodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "test",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"false"},
								},
							},
						},
					},
				},
			},
			wantRestored: false,
		},
		{
			name: "pod has affinity and set reservation ignored",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"test-reservation":             "true",
						apiext.LabelReservationIgnored: "true",
					},
				},
			},
			nodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "test",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
					},
				},
			},
			wantRestored: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, pods, []*corev1.Node{node})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)

			pl.reservationCache.updateReservation(matchedReservation)
			testPod.Spec.Affinity = &corev1.Affinity{
				NodeAffinity: tt.nodeAffinity,
			}

			cycleState := framework.NewCycleState()
			_, restored, status := pl.BeforePreFilter(context.TODO(), cycleState, testPod)
			assert.Equal(t, tt.wantRestored, restored)
			assert.True(t, status.IsSuccess())
		})
	}
}

func Test_parseSpecificNodesFromAffinity(t *testing.T) {
	tests := []struct {
		name         string
		nodeAffinity *corev1.NodeAffinity
		want         sets.String
		wantError    bool
	}{
		{
			name: "no node affinity",
		},
		{
			name: "use MatchExpressions to affinity nodes",
			nodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelTopologyZone,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"az1"},
								},
							},
						},
					},
				},
			},
			want:      nil,
			wantError: false,
		},
		{
			name: "has MatchExpressions and MatchFields - 1",
			nodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelTopologyZone,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"az1"},
								},
							},
							MatchFields: []corev1.NodeSelectorRequirement{
								{
									Key:      metav1.ObjectNameField,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"node1", "node2"},
								},
							},
						},
					},
				},
			},
			want:      sets.NewString("node1", "node2"),
			wantError: false,
		},
		{
			name: "has MatchExpressions and MatchFields - 2",
			nodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelTopologyZone,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"az1"},
								},
							},
							MatchFields: []corev1.NodeSelectorRequirement{
								{
									Key:      metav1.ObjectNameField,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"node1", "node2"},
								},
							},
						},
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelTopologyZone,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"az2"},
								},
							},
						},
					},
				},
			},
			want:      nil,
			wantError: false,
		},
		{
			name: "invalid MatchFields",
			nodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchFields: []corev1.NodeSelectorRequirement{
								{
									Key:      metav1.ObjectNameField,
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{},
								},
							},
						},
					},
				},
			},
			want:      nil,
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSpecificNodesFromAffinity(&corev1.Pod{
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: tt.nodeAffinity,
					},
				},
			})
			assert.Equal(t, tt.wantError, err != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}
