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
			Name: "reservation4C8G",
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
									corev1.ResourceCPU:    resource.MustParse("12"),
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
		},
	}
	pods = append(pods, reservationutil.NewReservePod(unmatchedReservation))

	// matchedReservation allocated 8C16Gi
	matchedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation2C4G",
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
		},
	}
	pods = append(pods, reservationutil.NewReservePod(matchedReservation))

	suit := newPluginTestSuitWith(t, pods, []*corev1.Node{node})
	p, err := suit.pluginFactory()
	assert.NoError(t, err)
	pl := p.(*Plugin)

	nodeInfo, err := suit.fw.SnapshotSharedLister().NodeInfos().Get(node.Name)
	assert.NoError(t, err)
	expectedRequestedResources := &framework.Resource{
		MilliCPU: 32000,
		Memory:   64 * 1024 * 1024 * 1024,
	}
	assert.Equal(t, expectedRequestedResources, nodeInfo.Requested)

	pl.reservationCache.updateReservation(unmatchedReservation)
	pl.reservationCache.updateReservation(matchedReservation)

	pl.reservationCache.addPod(unmatchedReservation.UID, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "unmatched-allocated-pod-1",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
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
	})

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

	matchRInfo := pl.reservationCache.getReservationInfoByUID(matchedReservation.UID)
	expectedStat := &stateData{
		podRequests:          corev1.ResourceList{},
		podRequestsResources: framework.NewResource(nil),
		nodeReservationStates: map[string]nodeReservationState{
			node.Name: {
				nodeName: node.Name,
				matched:  []*frameworkext.ReservationInfo{matchRInfo},
				podRequested: &framework.Resource{
					MilliCPU: 28000,
					Memory:   60129542144,
				},
				rAllocated: framework.NewResource(nil),
			},
		},
	}
	assert.Equal(t, expectedStat, getStateData(cycleState))

	unmatchedReservePod := pods[2].DeepCopy()
	unmatchedReservePod.Spec.Containers[0].Resources.Requests = corev1.ResourceList{}
	unmatchedReservePod.Spec.Containers = append(unmatchedReservePod.Spec.Containers, corev1.Container{
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8000m"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	})
	expectNodeInfo := framework.NewNodeInfo(pods[0], pods[1], unmatchedReservePod)
	expectNodeInfo.SetNode(node)
	assert.Equal(t, expectNodeInfo.Requested, nodeInfo.Requested)
	assert.Equal(t, expectNodeInfo.UsedPorts, nodeInfo.UsedPorts)
	nodeInfo.Generation = 0
	expectNodeInfo.Generation = 0
	assert.True(t, equality.Semantic.DeepEqual(expectNodeInfo, nodeInfo))

	status = pl.AfterPreFilter(context.TODO(), cycleState, &corev1.Pod{})
	assert.True(t, status.IsSuccess())
}

func Test_matchReservation(t *testing.T) {
	tests := []struct {
		name                string
		pod                 *corev1.Pod
		reservation         *schedulingv1alpha1.Reservation
		reservationAffinity *apiext.ReservationAffinity
		want                bool
	}{
		{
			name: "only match reservation owners",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
			},
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
			name: "match reservation owners and match reservation affinity",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
			},
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.reservationAffinity != nil {
				affinityData, err := json.Marshal(tt.reservationAffinity)
				assert.NoError(t, err)
				if tt.pod.Annotations == nil {
					tt.pod.Annotations = map[string]string{}
				}
				tt.pod.Annotations[apiext.AnnotationReservationAffinity] = string(affinityData)
			}
			reservationAffinity, err := reservationutil.GetRequiredReservationAffinity(tt.pod)
			assert.NoError(t, err)
			got := matchReservation(tt.pod, &corev1.Node{}, tt.reservation, reservationAffinity)
			assert.Equal(t, tt.want, got)
		})
	}
}
