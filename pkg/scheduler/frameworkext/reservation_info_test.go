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

package frameworkext

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestIsAvailable(t *testing.T) {
	tests := []struct {
		name string
		obj  metav1.Object
		want bool
	}{
		{
			name: "normal reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-r",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			want: true,
		},
		{
			name: "failed reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-r",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationFailed,
					NodeName: "test-node",
				},
			},
			want: false,
		},
		{
			name: "ready and running pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-p",
					Namespace: "default",
					UID:       "123456",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			want: true,
		},
		{
			name: "not ready and running pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-p",
					Namespace: "default",
					UID:       "123456",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "not ready and not running pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-p",
					Namespace: "default",
					UID:       "123456",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rInfo *ReservationInfo
			switch obj := tt.obj.(type) {
			case *schedulingv1alpha1.Reservation:
				rInfo = NewReservationInfo(obj)
			case *corev1.Pod:
				rInfo = NewReservationInfoFromPod(obj)
			}
			assert.NotNil(t, rInfo)
			assert.Equal(t, tt.want, rInfo.IsAvailable())
		})
	}
}

func TestIsSchedulable(t *testing.T) {
	tests := []struct {
		name string
		obj  *schedulingv1alpha1.Reservation
		want bool
	}{
		{
			name: "normal reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-r",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			want: false,
		},
		{
			name: "deleting reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-r",
					UID:               "123456",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			want: true,
		},
		{
			name: "unschedulable reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-r",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template:      &corev1.PodTemplateSpec{},
					Unschedulable: true,
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rInfo := NewReservationInfo(tt.obj)
			assert.NotNil(t, rInfo)
			assert.Equal(t, tt.want, rInfo.IsUnschedulable())
		})
	}
}

func TestIsTerminating(t *testing.T) {
	tests := []struct {
		name string
		obj  metav1.Object
		want bool
	}{
		{
			name: "normal reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-r",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			want: false,
		},
		{
			name: "deleting reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-r",
					UID:               "123456",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			want: true,
		},
		{
			name: "ready and running pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-p",
					Namespace: "default",
					UID:       "123456",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "ready, running and deleting pod",
			obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-p",
					Namespace:         "default",
					UID:               "123456",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rInfo *ReservationInfo
			switch obj := tt.obj.(type) {
			case *schedulingv1alpha1.Reservation:
				rInfo = NewReservationInfo(obj)
			case *corev1.Pod:
				rInfo = NewReservationInfoFromPod(obj)
			}
			assert.NotNil(t, rInfo)
			assert.Equal(t, tt.want, rInfo.IsTerminating())
		})
	}
}

func TestReservationInfoUpdateReservation(t *testing.T) {
	allocatable := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("4"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: allocatable.DeepCopy(),
							},
						},
					},
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-label": "123",
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName:    "node-1",
			Phase:       schedulingv1alpha1.ReasonReservationAvailable,
			Allocatable: allocatable.DeepCopy(),
		},
	}
	ownerMatchers, parseError := reservationutil.ParseReservationOwnerMatchers(reservation.Spec.Owners)
	assert.NoError(t, parseError)

	tests := []struct {
		name           string
		reservation    *schedulingv1alpha1.Reservation
		newReservation *schedulingv1alpha1.Reservation
		want           *ReservationInfo
	}{
		{
			name:           "reservation has not changed",
			reservation:    reservation,
			newReservation: reservation,
			want: &ReservationInfo{
				ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:   allocatable.DeepCopy(),
				AssignedPods:  map[types.UID]*PodRequirement{},
				OwnerMatchers: ownerMatchers,
				ParseError:    parseError,
			},
		},
		{
			name:        "reservation's owners have changed",
			reservation: reservation,
			newReservation: func() *schedulingv1alpha1.Reservation {
				r := reservation.DeepCopy()
				r.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
					{
						Object: &corev1.ObjectReference{
							UID: "123456",
						},
					},
				}
				return r
			}(),
			want: &ReservationInfo{
				ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:   allocatable.DeepCopy(),
				AssignedPods:  map[types.UID]*PodRequirement{},
				OwnerMatchers: func() []reservationutil.ReservationOwnerMatcher {
					m, _ := reservationutil.ParseReservationOwnerMatchers([]schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								UID: "123456",
							},
						},
					})
					return m
				}(),
				ParseError: nil,
			},
		},
		{
			name:        "reservation's owners have changed with error",
			reservation: reservation,
			newReservation: func() *schedulingv1alpha1.Reservation {
				r := reservation.DeepCopy()
				r.Spec.Owners = []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: "xxx",
								},
							},
						},
					},
				}
				return r
			}(),
			want: &ReservationInfo{
				ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:   allocatable.DeepCopy(),
				AssignedPods:  map[types.UID]*PodRequirement{},
				OwnerMatchers: nil,
				ParseError: func() error {
					_, err := reservationutil.ParseReservationOwnerMatchers([]schedulingv1alpha1.ReservationOwner{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "xxx",
										Operator: "xxx",
									},
								},
							},
						},
					})
					return utilerrors.NewAggregate([]error{err})
				}(),
			},
		},
		{
			name:        "reservation's allocatable has changed",
			reservation: reservation,
			newReservation: func() *schedulingv1alpha1.Reservation {
				r := reservation.DeepCopy()
				r.Status.Allocatable = corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				}
				return r
			}(),
			want: &ReservationInfo{
				ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
				AssignedPods:  map[types.UID]*PodRequirement{},
				OwnerMatchers: ownerMatchers,
				ParseError:    nil,
			},
		},
		{
			name:        "reservation's reserved has changed",
			reservation: reservation,
			newReservation: func() *schedulingv1alpha1.Reservation {
				r := reservation.DeepCopy()
				if r.Annotations == nil {
					r.Annotations = map[string]string{}
				}
				r.Annotations[apiext.AnnotationNodeReservation] = `{"resources": {"cpu": "1"}}`
				return r
			}(),
			want: &ReservationInfo{
				ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:   allocatable.DeepCopy(),
				Reserved: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceCPU: resource.MustParse("1"),
				},
				AssignedPods:  map[types.UID]*PodRequirement{},
				OwnerMatchers: ownerMatchers,
				ParseError:    nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reservationInfo := NewReservationInfo(tt.reservation)
			reservationInfo.UpdateReservation(tt.newReservation)
			sort.Slice(reservationInfo.ResourceNames, func(i, j int) bool {
				return reservationInfo.ResourceNames[i] < reservationInfo.ResourceNames[j]
			})
			sort.Slice(tt.want.ResourceNames, func(i, j int) bool {
				return tt.want.ResourceNames[i] < tt.want.ResourceNames[j]
			})
			tt.want.Reservation = tt.newReservation
			tt.want.Pod = reservationutil.NewReservePod(tt.newReservation)
			assert.Equal(t, tt.want, reservationInfo)
		})
	}
}

func TestReservationInfoUpdatePod(t *testing.T) {
	allocatable := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("4"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: allocatable.DeepCopy(),
					},
				},
			},
			NodeName: "node-1",
		},
	}

	owners := []schedulingv1alpha1.ReservationOwner{
		{
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test-label": "123",
				},
			},
		},
	}
	assert.NoError(t, apiext.SetReservationOwners(pod, owners))
	ownerMatchers, parseError := reservationutil.ParseReservationOwnerMatchers(owners)
	assert.NoError(t, parseError)

	tests := []struct {
		name   string
		pod    *corev1.Pod
		newPod *corev1.Pod
		want   *ReservationInfo
	}{
		{
			name:   "pod has not changed",
			pod:    pod,
			newPod: pod,
			want: &ReservationInfo{
				Pod:           pod,
				ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:   allocatable.DeepCopy(),
				Allocated:     corev1.ResourceList{},
				AssignedPods:  map[types.UID]*PodRequirement{},
				OwnerMatchers: ownerMatchers,
				ParseError:    parseError,
			},
		},
		{
			name: "pod's owners have changed",
			pod:  pod,
			newPod: func() *corev1.Pod {
				p := pod.DeepCopy()
				_ = apiext.SetReservationOwners(p, []schedulingv1alpha1.ReservationOwner{
					{
						Object: &corev1.ObjectReference{
							UID: "123456",
						},
					},
				})
				return p
			}(),
			want: &ReservationInfo{
				ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:   allocatable.DeepCopy(),
				Allocated:     corev1.ResourceList{},
				AssignedPods:  map[types.UID]*PodRequirement{},
				OwnerMatchers: func() []reservationutil.ReservationOwnerMatcher {
					m, _ := reservationutil.ParseReservationOwnerMatchers([]schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								UID: "123456",
							},
						},
					})
					return m
				}(),
				ParseError: nil,
			},
		},
		{
			name: "pod's owners have changed with error",
			pod:  pod,
			newPod: func() *corev1.Pod {
				p := pod.DeepCopy()
				_ = apiext.SetReservationOwners(p, []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: "xxx",
								},
							},
						},
					},
				})
				return p
			}(),
			want: &ReservationInfo{
				ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:   allocatable.DeepCopy(),
				Allocated:     corev1.ResourceList{},
				AssignedPods:  map[types.UID]*PodRequirement{},
				OwnerMatchers: nil,
				ParseError: func() error {
					_, err := reservationutil.ParseReservationOwnerMatchers([]schedulingv1alpha1.ReservationOwner{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "xxx",
										Operator: "xxx",
									},
								},
							},
						},
					})
					return utilerrors.NewAggregate([]error{err})
				}(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reservationInfo := NewReservationInfoFromPod(tt.pod)
			reservationInfo.UpdatePod(tt.newPod)
			sort.Slice(reservationInfo.ResourceNames, func(i, j int) bool {
				return reservationInfo.ResourceNames[i] < reservationInfo.ResourceNames[j]
			})
			sort.Slice(tt.want.ResourceNames, func(i, j int) bool {
				return tt.want.ResourceNames[i] < tt.want.ResourceNames[j]
			})
			tt.want.Pod = tt.newPod
			assert.Equal(t, tt.want, reservationInfo)
		})
	}
}
