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
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
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
			AllocateOnce: ptr.To[bool](false),
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
			podResourceNames:     []corev1.ResourceName{},
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
			AllocateOnce: ptr.To[bool](false),
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
		Spec: corev1.PodSpec{
			TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
				{
					MaxSkew:           1,
					TopologyKey:       corev1.LabelHostname,
					WhenUnsatisfiable: corev1.ScheduleAnyway,
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation": "true",
						},
					},
				},
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
		Spec: corev1.PodSpec{
			TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
				{
					MaxSkew:           1,
					TopologyKey:       corev1.LabelTopologyZone,
					WhenUnsatisfiable: corev1.ScheduleAnyway,
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-reservation": "true",
						},
					},
				},
			},
		},
	}, &framework.PreFilterResult{})
	assert.True(t, status.IsSuccess())

	expectedStat := &stateData{
		schedulingStateData: schedulingStateData{
			podRequests:          corev1.ResourceList{},
			podRequestsResources: framework.NewResource(nil),
			podResourceNames:     []corev1.ResourceName{},
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
		name           string
		rAffinity      *apiext.ReservationAffinity
		exactMatchSpec *apiext.ExactMatchReservationSpec
		wantRestored   bool
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
			name: "pod has reservation affinity but failed to exact match",
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
			exactMatchSpec: &apiext.ExactMatchReservationSpec{
				ResourceNames: []corev1.ResourceName{
					corev1.ResourceCPU,
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
		{
			name: "pod specifies a reservation name but failed to exact match",
			rAffinity: &apiext.ReservationAffinity{
				Name: "reservation8C16G",
			},
			exactMatchSpec: &apiext.ExactMatchReservationSpec{
				ResourceNames: []corev1.ResourceName{
					corev1.ResourceCPU,
				},
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
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						schedulertesting.MakeContainer().Resources(map[corev1.ResourceName]string{
							corev1.ResourceCPU:    "4",
							corev1.ResourceMemory: "8Gi",
						}).Obj(),
					},
				},
			}
			if tt.rAffinity != nil {
				assert.NoError(t, apiext.SetReservationAffinity(testPod, tt.rAffinity))
			}
			if tt.exactMatchSpec != nil {
				assert.NoError(t, apiext.SetExactMatchReservationSpec(testPod, tt.exactMatchSpec))
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
			tt.pod.Spec.Affinity = &corev1.Affinity{
				NodeAffinity: tt.nodeAffinity,
			}

			cycleState := framework.NewCycleState()
			_, restored, status := pl.BeforePreFilter(context.TODO(), cycleState, tt.pod)
			assert.Equal(t, tt.wantRestored, restored)
			assert.True(t, status.IsSuccess())
		})
	}
}

func TestBeforePreFilterForReservePod(t *testing.T) {
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
	testReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation",
			Labels: map[string]string{
				"test-reservation": "true",
			},
			UID: "xxxxxx",
		},
	}
	testPod := reservationutil.NewReservePod(testReservation)
	availableReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation8C16G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-available-reservation": "true",
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
			AllocateOnce: ptr.To[bool](false),
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
	testAssignedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-assigned-pod",
			Labels: map[string]string{
				"test-available-reservation": "true",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
		},
	}
	apiext.SetReservationAllocated(testAssignedPod, availableReservation)
	pods := []*corev1.Pod{testAssignedPod, reservationutil.NewReservePod(availableReservation)}

	tests := []struct {
		name         string
		pod          *corev1.Pod
		reservation  *schedulingv1alpha1.Reservation
		nodeAffinity *corev1.NodeAffinity
		wantRestored bool
	}{
		{
			name:         "pod has no affinity",
			pod:          testPod.DeepCopy(),
			reservation:  testReservation.DeepCopy(),
			nodeAffinity: nil,
			wantRestored: true,
		},
		{
			name:        "pod has affinity with matchField",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
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
			name:        "pod has affinity with matchField but failed to match",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
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
			name:        "pod has affinity with matchExpressions",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
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
			name:        "pod has affinity with matchExpressions but failed to match",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, pods, []*corev1.Node{node})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			_, err = pl.client.Reservations().Create(context.TODO(), tt.reservation, metav1.CreateOptions{})
			assert.NoError(t, err)
			_, err = pl.client.Reservations().Create(context.TODO(), availableReservation, metav1.CreateOptions{})
			assert.NoError(t, err)

			tt.pod.Spec.Affinity = &corev1.Affinity{
				NodeAffinity: tt.nodeAffinity,
			}
			pl.reservationCache.updateReservation(availableReservation)
			err = pl.reservationCache.assumePod(availableReservation.UID, testAssignedPod)
			assert.NoError(t, err)
			suit.start()

			cycleState := framework.NewCycleState()
			_, restored, status := pl.BeforePreFilter(context.TODO(), cycleState, tt.pod)
			assert.True(t, status.IsSuccess())
			assert.Equal(t, tt.wantRestored, restored)
		})
	}
}

func TestBeforePreFilterForReservationPreAllocation(t *testing.T) {
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
	testReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
			Labels: map[string]string{
				"test-reservation": "true",
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
					NodeName: node.Name,
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
			PreAllocation:  true,
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
		},
	}
	testPod := reservationutil.NewReservePod(testReservation)
	testPreAllocatablePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "test-pre-allocatable-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				"test-reservation": "true",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
		},
	}
	testUnmatchedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "test-unmatched-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				"test-reservation": "true",
			},
			Annotations: map[string]string{
				apiext.AnnotationReservationAffinity: `{"name":"other-reservation-name"}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
		},
	}
	testUnmatchedPod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "test-unmatched-pod-1",
			Namespace: "test-ns",
			Labels: map[string]string{
				"test-reservation": "true",
			},
			Annotations: map[string]string{
				apiext.AnnotationReservationAffinity: `{"reservationSelector": {"reservation-type": "test"}}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
		},
	}
	testUnmatchedPod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "test-unmatched-pod-2",
			Namespace: "test-ns",
			Labels: map[string]string{
				"test-other-reservation": "true",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
		},
	}
	testUnmatchedPod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "test-unmatched-pod-3",
			Namespace: "test-ns",
			Labels: map[string]string{
				"test-reservation": "true",
			},
			Annotations: map[string]string{
				apiext.AnnotationExactMatchReservationSpec: `{"resourceNames": ["cpu"]}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("16Gi"),
						},
					},
				},
			},
		},
	}
	testErrPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "test-err-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				"test-reservation": "true",
			},
			Annotations: map[string]string{
				apiext.AnnotationReservationAffinity: `}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
		},
	}
	testErrPod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "test-err-pod-1",
			Namespace: "test-ns",
			Labels: map[string]string{
				"test-reservation": "true",
			},
			Annotations: map[string]string{
				apiext.AnnotationExactMatchReservationSpec: `[`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
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
	}
	availableReservation := &schedulingv1alpha1.Reservation{
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
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			AllocateOnce:   ptr.To[bool](false),
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
	testAssignedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "test-assigned-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				"test-reservation": "true",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
		},
	}
	apiext.SetReservationAllocated(testAssignedPod, availableReservation)

	tests := []struct {
		name               string
		pod                *corev1.Pod
		reservation        *schedulingv1alpha1.Reservation
		assignPods         []*corev1.Pod
		nodeAffinity       *corev1.NodeAffinity
		wantPreAllocatable int
		wantRestored       bool
	}{
		{
			name:               "pod has no affinity",
			pod:                testPod.DeepCopy(),
			reservation:        testReservation.DeepCopy(),
			assignPods:         []*corev1.Pod{testAssignedPod, testPreAllocatablePod, testUnmatchedPod, testUnmatchedPod1, reservationutil.NewReservePod(testReservation)},
			nodeAffinity:       nil,
			wantPreAllocatable: 1,
			wantRestored:       true,
		},
		{
			name:        "pod has affinity with matchField",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
			assignPods:  []*corev1.Pod{testAssignedPod, testPreAllocatablePod, testUnmatchedPod, testUnmatchedPod1, reservationutil.NewReservePod(testReservation)},
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
			wantPreAllocatable: 1,
			wantRestored:       true,
		},
		{
			name:        "pod has affinity with matchField but failed to match",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
			assignPods:  []*corev1.Pod{testAssignedPod, testPreAllocatablePod, testUnmatchedPod, testUnmatchedPod1, reservationutil.NewReservePod(testReservation)},
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
			wantPreAllocatable: 0,
			wantRestored:       false,
		},
		{
			name:        "pod has affinity with matchExpressions",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
			assignPods:  []*corev1.Pod{testAssignedPod, testPreAllocatablePod, testUnmatchedPod, testUnmatchedPod1, reservationutil.NewReservePod(testReservation)},
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
			wantPreAllocatable: 1,
			wantRestored:       true,
		},
		{
			name:        "pod has affinity with matchExpressions 1",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
			assignPods: []*corev1.Pod{
				reservationutil.NewReservePod(testReservation),
				testAssignedPod,
				testPreAllocatablePod,
				testUnmatchedPod,
				testUnmatchedPod1,
				testUnmatchedPod2,
				testUnmatchedPod3,
				testErrPod,
				testErrPod1,
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
			wantPreAllocatable: 1,
			wantRestored:       true,
		},
		{
			name:        "pod has affinity with matchExpressions but failed to match",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
			assignPods:  []*corev1.Pod{testAssignedPod, testPreAllocatablePod, testUnmatchedPod, testUnmatchedPod1, reservationutil.NewReservePod(testReservation)},
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
			wantPreAllocatable: 0,
			wantRestored:       false,
		},
		{
			name:        "pod has no pre-allocatable",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
			assignPods:  []*corev1.Pod{testAssignedPod, testUnmatchedPod, testUnmatchedPod1, reservationutil.NewReservePod(testReservation)},
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
			wantPreAllocatable: 0,
			wantRestored:       true,
		},
		{
			name:        "pod has nothing to restore",
			pod:         testPod.DeepCopy(),
			reservation: testReservation.DeepCopy(),
			assignPods:  []*corev1.Pod{testAssignedPod, testUnmatchedPod, reservationutil.NewReservePod(testReservation)},
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
			wantPreAllocatable: 0,
			wantRestored:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, tt.assignPods, []*corev1.Node{node})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			_, err = pl.client.Reservations().Create(context.TODO(), tt.reservation, metav1.CreateOptions{})
			assert.NoError(t, err)
			_, err = pl.client.Reservations().Create(context.TODO(), availableReservation, metav1.CreateOptions{})
			assert.NoError(t, err)
			for _, pod := range tt.assignPods {
				_, err = suit.fw.ClientSet().CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			tt.pod.Spec.Affinity = &corev1.Affinity{
				NodeAffinity: tt.nodeAffinity,
			}
			pl.reservationCache.updateReservation(availableReservation)
			err = pl.reservationCache.assumePod(availableReservation.UID, testAssignedPod)
			assert.NoError(t, err)
			suit.start()

			cycleState := framework.NewCycleState()
			_, restored, status := pl.BeforePreFilter(context.TODO(), cycleState, tt.pod)
			assert.True(t, status.IsSuccess())
			assert.Equal(t, tt.wantRestored, restored)
			if restored {
				state := getStateData(cycleState)
				assert.NotNil(t, state)
				assert.NotNil(t, state.nodeReservationStates[node.Name])
				assert.Equal(t, tt.wantPreAllocatable, len(state.nodeReservationStates[node.Name].preAllocatablePods), state.nodeReservationStates[node.Name].preAllocatablePods)
			}
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

func TestAfterPreFilter_WithSchedulingHint(t *testing.T) {
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-2",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
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
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: node1.Name,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	}

	tests := []struct {
		name                string
		hintNodes           []string
		skipRestoreNodeInfo bool
		preFilterResult     *framework.PreFilterResult
		expectedNodes       []string
	}{
		{
			name:                "hint takes priority over AllNodes PreFilterResult",
			hintNodes:           []string{node1.Name},
			skipRestoreNodeInfo: false,
			preFilterResult:     nil, // nil means AllNodes
			expectedNodes:       []string{node1.Name},
		},
		{
			name:                "hint takes priority over specific PreFilterResult",
			hintNodes:           []string{node1.Name},
			skipRestoreNodeInfo: false,
			preFilterResult:     &framework.PreFilterResult{NodeNames: sets.New(node2.Name)},
			expectedNodes:       []string{node1.Name}, // hint wins over PreFilterResult
		},
		{
			name:                "hint with SkipRestoreNodeInfo true",
			hintNodes:           []string{node1.Name},
			skipRestoreNodeInfo: true,
			preFilterResult:     nil,
			expectedNodes:       []string{node1.Name},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pods := []*corev1.Pod{reservationutil.NewReservePod(reservation)}
			suit := newPluginTestSuitWith(t, pods, []*corev1.Node{node1, node2})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			pl.reservationCache.updateReservation(reservation)

			cycleState := framework.NewCycleState()

			// Setup hint state
			hintState := &hinter.SchedulingHintStateData{
				PreFilterNodes: tt.hintNodes,
				Extensions: map[string]interface{}{
					Name: HintExtensions{SkipRestoreNodeInfo: tt.skipRestoreNodeInfo},
				},
			}
			cycleState.Write(hinter.SchedulingHintStateKey, hintState)

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"test-reservation": "true",
					},
				},
			}
			_, _, status := pl.BeforePreFilter(context.TODO(), cycleState, pod)
			assert.True(t, status.IsSuccess())

			status = pl.AfterPreFilter(context.TODO(), cycleState, pod, tt.preFilterResult)
			assert.True(t, status.IsSuccess())

			// Verify state - expected nodes should be processed
			state := getStateData(cycleState)
			assert.Equal(t, len(tt.expectedNodes), len(state.nodeReservationStates),
				"expected %d nodes but got %d", len(tt.expectedNodes), len(state.nodeReservationStates))
			for _, nodeName := range tt.expectedNodes {
				_, exists := state.nodeReservationStates[nodeName]
				assert.True(t, exists, "node %s should be processed", nodeName)
			}
		})
	}
}
