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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestReservationInfo(t *testing.T) {
	testRestrictedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-r",
			UID:  "123456",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"foo": "bar",
						},
					},
				},
			},
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			AllocateOnce:   ptr.To[bool](false),
		},
	}
	testOwnerMatcher, err := reservationutil.ParseReservationOwnerMatchers(testRestrictedReservation.Spec.Owners)
	assert.NoError(t, err)
	testInvalidReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-r",
			UID:  "123456",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "foo",
								Operator: metav1.LabelSelectorOpIn,
								Values:   nil,
							},
						},
					},
				},
			},
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			AllocateOnce:   ptr.To[bool](false),
		},
	}
	_, testParseInvalidOwnerErr := reservationutil.ParseReservationOwnerMatchers(testInvalidReservation.Spec.Owners)
	testPreAllocationReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-r",
			UID:  "123456",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"foo": "bar",
						},
					},
				},
			},
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			AllocateOnce:   ptr.To[bool](false),
			PreAllocation:  true,
		},
	}
	tests := []struct {
		name   string
		r      *schedulingv1alpha1.Reservation
		want   *ReservationInfo
		wantFn func(*testing.T, *ReservationInfo)
	}{
		{
			name: "normal restricted reservation",
			r:    testRestrictedReservation,
			want: &ReservationInfo{
				Reservation: testRestrictedReservation,
				Pod:         reservationutil.NewReservePod(testRestrictedReservation),
				ResourceNames: []corev1.ResourceName{
					corev1.ResourceCPU,
					corev1.ResourceMemory,
				},
				Allocatable:      reservationutil.ReservationRequests(testRestrictedReservation),
				AllocatablePorts: util.RequestedHostPorts(reservationutil.NewReservePod(testRestrictedReservation)),
				AssignedPods:     map[types.UID]*PodRequirement{},
				OwnerMatchers:    testOwnerMatcher,
			},
		},
		{
			name: "parse error for invalid reservation",
			r:    testInvalidReservation,
			want: &ReservationInfo{
				Reservation: testInvalidReservation,
				Pod:         reservationutil.NewReservePod(testInvalidReservation),
				ResourceNames: []corev1.ResourceName{
					corev1.ResourceCPU,
					corev1.ResourceMemory,
				},
				Allocatable:      reservationutil.ReservationRequests(testInvalidReservation),
				AllocatablePorts: util.RequestedHostPorts(reservationutil.NewReservePod(testInvalidReservation)),
				AssignedPods:     map[types.UID]*PodRequirement{},
				OwnerMatchers:    nil,
				ParseError:       utilerrors.NewAggregate([]error{testParseInvalidOwnerErr}),
			},
		},
		{
			name: "pre-allocation reservation",
			r:    testPreAllocationReservation,
			want: &ReservationInfo{
				Reservation: testPreAllocationReservation,
				Pod:         reservationutil.NewReservePod(testPreAllocationReservation),
				ResourceNames: []corev1.ResourceName{
					corev1.ResourceCPU,
					corev1.ResourceMemory,
				},
				Allocatable:      reservationutil.ReservationRequests(testPreAllocationReservation),
				AllocatablePorts: util.RequestedHostPorts(reservationutil.NewReservePod(testPreAllocationReservation)),
				AssignedPods:     map[types.UID]*PodRequirement{},
				OwnerMatchers:    testOwnerMatcher,
			},
			wantFn: func(t *testing.T, rInfo *ReservationInfo) {
				assert.True(t, rInfo.IsPreAllocation())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewReservationInfo(tt.r)
			assert.Equal(t, tt.want, got)
			got1 := got.Clone()
			assert.Equal(t, tt.want, got1)
			if tt.wantFn != nil {
				tt.wantFn(t, got1)
			}
		})
	}
}

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
				ResourceNames:     []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:       allocatable.DeepCopy(),
				Available:         framework.NewResource(allocatable),
				AllocatedResource: &framework.Resource{},
				AssignedPods:      map[types.UID]*PodRequirement{},
				OwnerMatchers:     ownerMatchers,
				ParseError:        parseError,
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
				ResourceNames:     []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:       allocatable.DeepCopy(),
				Available:         framework.NewResource(allocatable),
				AllocatedResource: &framework.Resource{},
				AssignedPods:      map[types.UID]*PodRequirement{},
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
				ResourceNames:     []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:       allocatable.DeepCopy(),
				Available:         framework.NewResource(allocatable),
				AllocatedResource: &framework.Resource{},
				AssignedPods:      map[types.UID]*PodRequirement{},
				OwnerMatchers:     nil,
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
				Available: &framework.Resource{
					MilliCPU: 8000,
					Memory:   16 * 1024 * 1024 * 1024,
				},
				AllocatedResource: &framework.Resource{},
				AssignedPods:      map[types.UID]*PodRequirement{},
				OwnerMatchers:     ownerMatchers,
				ParseError:        nil,
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
				Available: &framework.Resource{
					MilliCPU: 3000,
					Memory:   8 * 1024 * 1024 * 1024,
				},
				AllocatedResource: &framework.Resource{},
				AssignedPods:      map[types.UID]*PodRequirement{},
				OwnerMatchers:     ownerMatchers,
				ParseError:        nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reservationInfo := NewReservationInfo(tt.reservation)
			reservationInfo.UpdateReservation(tt.newReservation)
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
				Pod:                   pod,
				ResourceNames:         []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:           allocatable.DeepCopy(),
				Allocated:             corev1.ResourceList{},
				Available:             framework.NewResource(allocatable),
				AllocatedResource:     &framework.Resource{},
				Non0AllocatedMilliCPU: schedutil.DefaultMilliCPURequest,
				Non0AllocatedMem:      schedutil.DefaultMemoryRequest,
				AssignedPods:          map[types.UID]*PodRequirement{},
				OwnerMatchers:         ownerMatchers,
				ParseError:            parseError,
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
				ResourceNames:         []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:           allocatable.DeepCopy(),
				Allocated:             corev1.ResourceList{},
				Available:             framework.NewResource(allocatable),
				AllocatedResource:     &framework.Resource{},
				Non0AllocatedMilliCPU: schedutil.DefaultMilliCPURequest,
				Non0AllocatedMem:      schedutil.DefaultMemoryRequest,
				AssignedPods:          map[types.UID]*PodRequirement{},
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
				ResourceNames:         []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				Allocatable:           allocatable.DeepCopy(),
				Allocated:             corev1.ResourceList{},
				Available:             framework.NewResource(allocatable),
				AllocatedResource:     &framework.Resource{},
				Non0AllocatedMilliCPU: schedutil.DefaultMilliCPURequest,
				Non0AllocatedMem:      schedutil.DefaultMemoryRequest,
				AssignedPods:          map[types.UID]*PodRequirement{},
				OwnerMatchers:         nil,
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
			tt.want.Pod = tt.newPod
			assert.Equal(t, tt.want, reservationInfo)
		})
	}
}

func TestReservationInfoRefreshAvailable(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		allocatable := corev1.ResourceList{
			corev1.ResourceCPU:       resource.MustParse("4"),
			corev1.ResourceMemory:    resource.MustParse("8Gi"),
			apiext.ResourceNvidiaGPU: resource.MustParse("1"),
		}
		allocated := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		}
		testPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
				UID:  "xxx",
				Labels: map[string]string{
					"test-label": "123",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "test-node",
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:              resource.MustParse("1"),
								corev1.ResourceMemory:           resource.MustParse("4Gi"),
								corev1.ResourceEphemeralStorage: resource.MustParse("32Gi"),
							},
						},
					},
				},
			},
		}
		testReservation := &schedulingv1alpha1.Reservation{
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
				NodeName:    "test-node",
				Phase:       schedulingv1alpha1.ReasonReservationAvailable,
				Allocatable: allocatable,
				Allocated:   allocated,
			},
		}
		ownerMatchers, parseError := reservationutil.ParseReservationOwnerMatchers(testReservation.Spec.Owners)
		assert.NoError(t, parseError)
		expectedRInfo := &ReservationInfo{
			Reservation:   testReservation,
			Pod:           reservationutil.NewReservePod(testReservation),
			ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, apiext.ResourceNvidiaGPU},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:       resource.MustParse("4"),
				corev1.ResourceMemory:    resource.MustParse("8Gi"),
				apiext.ResourceNvidiaGPU: resource.MustParse("1"),
			},
			Allocated: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Available: &framework.Resource{
				MilliCPU: 3000,
				Memory:   4 * 1024 * 1024 * 1024,
				ScalarResources: map[corev1.ResourceName]int64{
					apiext.ResourceNvidiaGPU: 1,
				},
			},
			AllocatedResource: &framework.Resource{
				MilliCPU: 1000,
				Memory:   4 * 1024 * 1024 * 1024,
			},
			Non0AllocatedMilliCPU: 1000,
			Non0AllocatedMem:      4 * 1024 * 1024 * 1024,
			AssignedPods: map[types.UID]*PodRequirement{
				testPod.UID: {
					Namespace: testPod.Namespace,
					Name:      testPod.Name,
					UID:       testPod.UID,
					Requests:  testPod.Spec.Containers[0].Resources.Requests,
				},
			},
			OwnerMatchers: ownerMatchers,
			ParseError:    nil,
		}
		expectedRInfo1 := &ReservationInfo{
			Reservation:   testReservation,
			Pod:           reservationutil.NewReservePod(testReservation),
			ResourceNames: []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory, apiext.ResourceNvidiaGPU},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:       resource.MustParse("4"),
				corev1.ResourceMemory:    resource.MustParse("8Gi"),
				apiext.ResourceNvidiaGPU: resource.MustParse("1"),
			},
			Allocated: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("0"),
				corev1.ResourceMemory: resource.MustParse("0"),
			},
			Available: &framework.Resource{
				MilliCPU: 4000,
				Memory:   8 * 1024 * 1024 * 1024,
				ScalarResources: map[corev1.ResourceName]int64{
					apiext.ResourceNvidiaGPU: 1,
				},
			},
			AllocatedResource:     &framework.Resource{},
			Non0AllocatedMilliCPU: 0,
			Non0AllocatedMem:      0,
			AssignedPods:          map[types.UID]*PodRequirement{},
			OwnerMatchers:         ownerMatchers,
			ParseError:            nil,
		}

		rInfo := NewReservationInfo(testReservation)
		rInfo.AddAssignedPod(testPod)
		rInfo.RefreshPreCalculated()
		assert.Equal(t, expectedRInfo, rInfo)
		gotAvailable := rInfo.GetAvailable()
		assert.Equal(t, expectedRInfo.Available, gotAvailable)
		gotAllocatedResource, gotNon0MilliCPU, gotNon0Mem := rInfo.GetAllocatedResource()
		assert.Equal(t, expectedRInfo.AllocatedResource, gotAllocatedResource)
		assert.Equal(t, expectedRInfo.Non0AllocatedMilliCPU, gotNon0MilliCPU)
		assert.Equal(t, expectedRInfo.Non0AllocatedMem, gotNon0Mem)
		rInfo.RemoveAssignedPod(testPod)
		assert.Equal(t, expectedRInfo1, rInfo)
		gotAvailable = rInfo.GetAvailable()
		assert.Equal(t, expectedRInfo1.Available, gotAvailable)
		gotAllocatedResource, gotNon0MilliCPU, gotNon0Mem = rInfo.GetAllocatedResource()
		assert.Equal(t, expectedRInfo1.AllocatedResource, gotAllocatedResource)
		assert.Equal(t, expectedRInfo1.Non0AllocatedMilliCPU, gotNon0MilliCPU)
		assert.Equal(t, expectedRInfo1.Non0AllocatedMem, gotNon0Mem)
	})
}

func TestReservationInfoMatchReservationAffinity(t *testing.T) {
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
			rInfo := NewReservationInfo(tt.reservation)
			got := rInfo.MatchReservationAffinity(reservationAffinity, &corev1.Node{})
			assert.Equal(t, tt.want, got)
		})
	}
}
