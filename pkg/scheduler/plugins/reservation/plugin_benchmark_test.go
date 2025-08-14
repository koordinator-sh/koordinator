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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

func Benchmark_filterWithReservations(b *testing.B) {
	testRInfo := frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-r",
			UID:  "123456",
			Annotations: map[string]string{
				apiext.AnnotationNodeReservation: `{"resources": {"cpu": "1"}}`,
			},
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:  resource.MustParse("7"),
									corev1.ResourcePods: resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:  resource.MustParse("7"),
				corev1.ResourcePods: resource.MustParse("2"),
			},
		},
	})
	testRInfo.AddAssignedPod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-a",
			Namespace: "test-ns",
			UID:       "xxxxxxxxx",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"),
						},
					},
				},
			},
		},
	})
	testRInfo.AddAssignedPod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-b",
			Namespace: "test-ns",
			UID:       "yyyyyy",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"),
						},
					},
				},
			},
		},
	})
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
				corev1.ResourcePods:   resource.MustParse("100"),
				apiext.BatchCPU:       resource.MustParse("7500"),
				apiext.BatchMemory:    resource.MustParse("10Gi"),
			},
		},
	}
	tests := []struct {
		name                          string
		stateData                     *stateData
		enableSkipReservationFitsNode bool
		wantStatus                    *framework.Status
	}{
		{
			name: "filter aligned reservation with nodeInfo",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter aligned reservation with nodeInfo",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 32 * 1000, // no remaining resources
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Insufficient cpu by node"),
		},
		{
			name: "filter restricted reservation with nodeInfo",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter restricted reservation with affinity",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter restricted reservation with nodeInfo and matched requests are zero",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("6000"),
						apiext.BatchMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
								ScalarResources: map[corev1.ResourceName]int64{
									apiext.BatchCPU:    1500,
									apiext.BatchMemory: 2 * 1024 * 1024 * 1024,
								},
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter restricted reservation with nodeInfo",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu"),
		},
		{
			name: "failed to filter restricted reservation since exceeding max pods",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("3"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								testRInfo.Clone(),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Too many pods"),
		},
		{
			name: "failed to filter restricted reservation since unmatched resources are insufficient",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("8000"),
						apiext.BatchMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
								ScalarResources: map[corev1.ResourceName]int64{
									apiext.BatchCPU:    1500,
									apiext.BatchMemory: 2 * 1024 * 1024 * 1024,
								},
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable,
				"Insufficient kubernetes.io/batch-cpu by node"),
		},
		{
			name: "filter restricted reservation and ignore matched requests are zero without affinity",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: false,
					podRequests: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("8000"),
						apiext.BatchMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
								ScalarResources: map[corev1.ResourceName]int64{
									apiext.BatchCPU:    1500,
									apiext.BatchMemory: 2 * 1024 * 1024 * 1024,
								},
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter restricted reservation due to reserved",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										Annotations: map[string]string{
											apiext.AnnotationNodeReservation: `{"resources": {"cpu": "2"}}`,
										},
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu"),
		},
		{
			name: "filter default reservations with preemption",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("4"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 36 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										UID:  "123456",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
									Status: schedulingv1alpha1.ReservationStatus{
										Allocatable: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
										Allocated: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter default reservations with preempt from reservation and node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("2"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										UID:  "123456",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
									Status: schedulingv1alpha1.ReservationStatus{
										Allocatable: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
										Allocated: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter default reservations with preempt from reservation",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("2"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Insufficient cpu by node"),
		},
		{
			name: "failed to filter default reservations with preempt from node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Insufficient cpu by node"),
		},
		{
			name: "filter restricted reservations with preempt from reservation",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("4"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										UID:  "123456",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
									Status: schedulingv1alpha1.ReservationStatus{
										Allocatable: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
										Allocated: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter restricted reservations with preempt from node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
					preemptibleInRRs: nil,
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu"),
		},
		{
			name: "failed to filter multiple restricted reservations with preempt from node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
					preemptibleInRRs: nil,
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 10000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r-1",
											UID:  "7891011",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("4"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu", "Reservation(s) Insufficient cpu"),
		},
		{
			name: "failed to filter restricted reservations with preempt from reservation and node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("2"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									ResourceNames: []corev1.ResourceName{
										corev1.ResourceCPU,
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu"),
		},
		{
			name: "filter restricted reservations with reservation name and preempt from reservation",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:     true,
					reservationName: "test-r",
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("4"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										UID:  "123456",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
									Status: schedulingv1alpha1.ReservationStatus{
										Allocatable: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
										Allocated: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter restricted reservations with preempt from reservation and node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
						Memory:   4 * 1024 * 1024 * 1024,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceMemory: resource.MustParse("32Gi"),
						},
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("4"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									ResourceNames: []corev1.ResourceName{
										corev1.ResourceCPU,
									},
								},
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter restricted reservation with reservation name, max pods and preemptible",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:     true,
					reservationName: "test-r",
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("3"),
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU:  resource.MustParse("1"),
								corev1.ResourcePods: resource.MustParse("1"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								testRInfo.Clone(),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter restricted reservation with reservation name since exceeding max pods",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:     true,
					reservationName: "test-r",
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("3"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								testRInfo.Clone(),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Too many pods, "+
				"requested: 1, used: 2, capacity: 2"),
		},
		{
			name: "failed to filter restricted reservation with name and reserved since insufficient resource",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:     true,
					reservationName: "test-r",
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("6"),
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU:  resource.MustParse("1"),
								corev1.ResourcePods: resource.MustParse("1"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								testRInfo.Clone(),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu, "+
				"requested: 6000, used: 1000, capacity: 6000"),
		},
		{
			name: "failed to filter restricted reservations with enableSkipReservationFitsNode",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
					preemptibleInRRs: nil,
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
							},
						},
					},
				},
			},
			enableSkipReservationFitsNode: true,
			wantStatus:                    framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu"),
		},
		{
			name: "skipped to filter aligned reservations with preempt from node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
							},
						},
					},
				},
			},
			enableSkipReservationFitsNode: true,
			wantStatus:                    nil,
		},
		{
			name: "still failed to filter multiple restricted reservations with enableSkipReservationFitsNode",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
					preemptibleInRRs: nil,
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 10000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r-1",
											UID:  "7891011",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("4"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
			enableSkipReservationFitsNode: true,
			wantStatus:                    framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu", "Reservation(s) Insufficient cpu"),
		},
		{
			name: "filter nothing",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, ErrReasonNoReservationsMeetRequirements),
		},
		{
			name: "filter nothing with enableSkipReservationFitsNode",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{},
						},
					},
				},
			},
			enableSkipReservationFitsNode: true,
			wantStatus:                    framework.NewStatus(framework.Unschedulable, ErrReasonNoReservationsMeetRequirements),
		},
	}
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			suit := newPluginTestSuit(b)
			p, err := suit.pluginFactory()
			assert.NoError(b, err)
			pl := p.(*Plugin)
			pl.enableSkipReservationFitsNode = tt.enableSkipReservationFitsNode
			suit.start()
			cycleState := framework.NewCycleState()
			if tt.stateData.podRequestsResources == nil {
				tt.stateData.podRequestsResources = framework.NewResource(tt.stateData.podRequests)
				tt.stateData.podResourceNames = quotav1.ResourceNames(tt.stateData.podRequests)
			}
			cycleState.Write(stateKey, tt.stateData)
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(node)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got := pl.filterWithReservations(context.TODO(), cycleState, &corev1.Pod{}, nodeInfo, tt.stateData.nodeReservationStates[node.Name].matchedOrIgnored, tt.stateData.hasAffinity)
				assert.Equal(b, tt.wantStatus, got)
			}
		})
	}
}
