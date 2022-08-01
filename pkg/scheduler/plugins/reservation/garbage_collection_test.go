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
	"testing"
	"time"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
)

func Test_gcReservations(t *testing.T) {
	now := time.Now()
	testActiveReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "r-active",
			UID:  "0",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Expires: &metav1.Time{Time: now.Add(10 * time.Hour)},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "node-0",
		},
	}
	testPendingReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "r-pending",
			UID:  "1",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Expires: &metav1.Time{Time: now.Add(20 * time.Minute)},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationPending,
		},
	}
	testToExpireReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "r-to-expire",
			UID:  "2",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Expires: &metav1.Time{Time: now.Add(-30 * time.Second)},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationPending,
		},
	}
	testToExpireReservation1 := testToExpireReservation.DeepCopy()
	testToExpireReservation1.Status.Phase = schedulingv1alpha1.ReservationFailed
	testExpiredReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "r-expired",
			UID:  "3",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Expires: &metav1.Time{Time: now.Add(-40 * time.Minute)},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationFailed,
			Conditions: []schedulingv1alpha1.ReservationCondition{
				{
					Type:               schedulingv1alpha1.ReservationConditionReady,
					Status:             schedulingv1alpha1.ConditionStatusFalse,
					Reason:             schedulingv1alpha1.ReasonReservationExpired,
					LastTransitionTime: metav1.Time{Time: now.Add(-40 * time.Minute)},
					LastProbeTime:      metav1.Time{Time: now.Add(-40 * time.Minute)},
				},
			},
		},
	}
	testToCleanReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "r-to-clean",
			UID:  "4",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Expires: &metav1.Time{Time: now.Add(-50 * time.Hour)},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationFailed,
			Conditions: []schedulingv1alpha1.ReservationCondition{
				{
					Type:               schedulingv1alpha1.ReservationConditionReady,
					Status:             schedulingv1alpha1.ConditionStatusFalse,
					Reason:             schedulingv1alpha1.ReasonReservationExpired,
					LastTransitionTime: metav1.Time{Time: now.Add(-50 * time.Hour)},
					LastProbeTime:      metav1.Time{Time: now.Add(-50 * time.Hour)},
				},
			},
		},
	}
	type fields struct {
		reservationCache *reservationCache
		lister           *fakeReservationLister
		client           *fakeReservationClient
		listErr          bool
	}
	type wantFields struct {
		exist   map[string]*schedulingv1alpha1.Reservation // UID -> R
		expired map[string]*schedulingv1alpha1.Reservation // UID -> R
	}
	tests := []struct {
		name       string
		fields     fields
		wantFields wantFields
	}{
		{
			name: "no reservation exist",
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{},
				},
				client: &fakeReservationClient{},
			},
			wantFields: wantFields{
				exist:   map[string]*schedulingv1alpha1.Reservation{},
				expired: map[string]*schedulingv1alpha1.Reservation{},
			},
		},
		{
			name: "successfully gc reservations",
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testActiveReservation.Name:   testActiveReservation,
						testPendingReservation.Name:  testPendingReservation,
						testToExpireReservation.Name: testToExpireReservation,
						testExpiredReservation.Name:  testExpiredReservation,
						testToCleanReservation.Name:  testToCleanReservation,
					},
				},
				client: &fakeReservationClient{},
			},
			wantFields: wantFields{
				exist: map[string]*schedulingv1alpha1.Reservation{
					string(testActiveReservation.UID):    testActiveReservation,
					string(testPendingReservation.UID):   testPendingReservation,
					string(testToExpireReservation1.UID): testToExpireReservation1,
					string(testExpiredReservation.UID):   testExpiredReservation,
				},
				expired: map[string]*schedulingv1alpha1.Reservation{
					string(testToExpireReservation1.UID): testToExpireReservation1,
					string(testExpiredReservation.UID):   testExpiredReservation,
				},
			},
		},
		{
			name: "list error",
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testActiveReservation.Name:  testActiveReservation,
						testPendingReservation.Name: testPendingReservation,
					},
					listErr: true,
				},
				client:  &fakeReservationClient{},
				listErr: true,
			},
			wantFields: wantFields{
				exist:   map[string]*schedulingv1alpha1.Reservation{},
				expired: map[string]*schedulingv1alpha1.Reservation{},
			},
		},
		{
			name: "get error",
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testActiveReservation.Name:   testActiveReservation,
						testToExpireReservation.Name: testToExpireReservation,
					},
					getErr: map[string]bool{
						testToExpireReservation.Name: true,
					},
				},
				client: &fakeReservationClient{},
			},
			wantFields: wantFields{
				exist: map[string]*schedulingv1alpha1.Reservation{
					string(testActiveReservation.UID):   testActiveReservation,
					string(testToExpireReservation.UID): testToExpireReservation,
				},
				expired: map[string]*schedulingv1alpha1.Reservation{},
			},
		},
		{
			name: "update status error",
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testToExpireReservation.Name: testToExpireReservation,
						testExpiredReservation.Name:  testExpiredReservation,
						testToCleanReservation.Name:  testToCleanReservation,
					},
				},
				client: &fakeReservationClient{
					updateStatusErr: map[string]bool{
						testToExpireReservation.Name: true,
					},
				},
			},
			wantFields: wantFields{
				exist: map[string]*schedulingv1alpha1.Reservation{
					string(testToExpireReservation.UID): testToExpireReservation,
					string(testExpiredReservation.UID):  testExpiredReservation,
				},
				expired: map[string]*schedulingv1alpha1.Reservation{
					string(testExpiredReservation.UID): testExpiredReservation,
				},
			},
		},
		{
			name: "delete error",
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testToCleanReservation.Name: testToCleanReservation,
					},
				},
				client: &fakeReservationClient{
					deleteErr: map[string]bool{
						testToCleanReservation.Name: true,
					},
				},
			},
			wantFields: wantFields{
				exist: map[string]*schedulingv1alpha1.Reservation{
					string(testToCleanReservation.UID): testToCleanReservation,
				},
				expired: map[string]*schedulingv1alpha1.Reservation{
					string(testToCleanReservation.UID): testToCleanReservation,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				reservationCache: tt.fields.reservationCache,
				lister:           tt.fields.lister,
				client:           tt.fields.client,
			}
			tt.fields.client.lister = tt.fields.lister

			p.gcReservations()

			gotExist, gotExpired, gotErr := testListExistAndExpired(p.lister)
			if tt.fields.listErr {
				assert.Equal(t, true, gotErr != nil)
				return
			}
			assert.NoError(t, gotErr)
			assert.Equal(t, tt.wantFields.exist, gotExist)
			assert.Equal(t, tt.wantFields.expired, gotExpired)
		})
	}
}

func Test_expireReservationOnNode(t *testing.T) {
	now := time.Now()
	testNoReserveNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "no-reservation",
		},
	}
	testReservedNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserved-0",
		},
	}
	testToExpireReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "r-to-expire",
			UID:  "2",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Expires: &metav1.Time{Time: now.Add(-30 * time.Second)},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationPending,
			NodeName: testReservedNode.Name,
		},
	}
	testToExpireReservation1 := testToExpireReservation.DeepCopy()
	testToExpireReservation1.Status.Phase = schedulingv1alpha1.ReservationFailed
	type fields struct {
		reservationCache *reservationCache
		lister           *fakeReservationLister
		client           *fakeReservationClient
		informer         *fakeIndexedInformer
		listErr          bool
	}
	type wantFields struct {
		exist   map[string]*schedulingv1alpha1.Reservation // UID -> R
		expired map[string]*schedulingv1alpha1.Reservation // UID -> R
	}
	tests := []struct {
		name       string
		arg        *corev1.Node
		fields     fields
		wantFields wantFields
	}{
		{
			name: "no reservation",
			arg:  testNoReserveNode,
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testToExpireReservation.Name: testToExpireReservation,
					},
				},
				client: &fakeReservationClient{},
				informer: &fakeIndexedInformer{
					rOnNode: map[string][]*schedulingv1alpha1.Reservation{
						testReservedNode.Name: {testToExpireReservation},
					},
				},
			},
			wantFields: wantFields{
				exist: map[string]*schedulingv1alpha1.Reservation{
					string(testToExpireReservation.UID): testToExpireReservation,
				},
				expired: map[string]*schedulingv1alpha1.Reservation{},
			},
		},
		{
			name: "byIndex error",
			arg:  testReservedNode,
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testToExpireReservation.Name: testToExpireReservation,
					},
				},
				client: &fakeReservationClient{},
				informer: &fakeIndexedInformer{
					rOnNode: map[string][]*schedulingv1alpha1.Reservation{
						testReservedNode.Name: {testToExpireReservation},
					},
					byIndexErr: map[string]bool{
						testReservedNode.Name: true,
					},
				},
			},
			wantFields: wantFields{
				exist: map[string]*schedulingv1alpha1.Reservation{
					string(testToExpireReservation.UID): testToExpireReservation,
				},
				expired: map[string]*schedulingv1alpha1.Reservation{},
			},
		},
		{
			name: "successfully gc on deleted node",
			arg:  testReservedNode,
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testToExpireReservation.Name: testToExpireReservation,
					},
				},
				client: &fakeReservationClient{},
				informer: &fakeIndexedInformer{
					rOnNode: map[string][]*schedulingv1alpha1.Reservation{
						testReservedNode.Name: {testToExpireReservation},
					},
				},
			},
			wantFields: wantFields{
				exist: map[string]*schedulingv1alpha1.Reservation{
					string(testToExpireReservation1.UID): testToExpireReservation1,
				},
				expired: map[string]*schedulingv1alpha1.Reservation{
					string(testToExpireReservation1.UID): testToExpireReservation1,
				},
			},
		},
		{
			name: "update status error",
			arg:  testReservedNode,
			fields: fields{
				reservationCache: newReservationCache(),
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testToExpireReservation.Name: testToExpireReservation,
					},
				},
				client: &fakeReservationClient{
					updateStatusErr: map[string]bool{
						testToExpireReservation.Name: true,
					},
				},
				informer: &fakeIndexedInformer{
					rOnNode: map[string][]*schedulingv1alpha1.Reservation{
						testReservedNode.Name: {testToExpireReservation},
					},
				},
			},
			wantFields: wantFields{
				exist: map[string]*schedulingv1alpha1.Reservation{
					string(testToExpireReservation.UID): testToExpireReservation,
				},
				expired: map[string]*schedulingv1alpha1.Reservation{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				reservationCache: tt.fields.reservationCache,
				lister:           tt.fields.lister,
				client:           tt.fields.client,
				informer:         tt.fields.informer,
			}
			tt.fields.client.lister = tt.fields.lister

			p.expireReservationOnNode(tt.arg)

			gotExist, gotExpired, gotErr := testListExistAndExpired(p.lister)
			if tt.fields.listErr {
				assert.Equal(t, true, gotErr != nil)
				return
			}
			assert.NoError(t, gotErr)
			assert.Equal(t, tt.wantFields.exist, gotExist)
			assert.Equal(t, tt.wantFields.expired, gotExpired)
		})
	}
}

func Test_syncPodDeleted(t *testing.T) {
	now := time.Now()
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-0",
			UID:  "1234",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `
{
  "Name": "test-reserve-0"
}
`,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
	}
	testReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reserve-0",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Expires: &metav1.Time{Time: now.Add(10 * time.Hour)},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node-0",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			Allocated: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
			CurrentOwners: []corev1.ObjectReference{
				getPodOwner(testPod),
			},
		},
	}
	testReservationFailed := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reserve-0",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Expires: &metav1.Time{Time: now.Add(10 * time.Hour)},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationFailed,
			NodeName: "test-node-0",
		},
	}
	type fields struct {
		lister *fakeReservationLister
		client *fakeReservationClient
	}
	tests := []struct {
		name   string
		fields fields
		arg    *corev1.Pod
	}{
		{
			name: "not allocate reservation",
			arg:  &corev1.Pod{},
		},
		{
			name: "invalid allocate info",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod-0",
					Annotations: map[string]string{
						apiext.AnnotationReservationAllocated: "invalid info",
					},
				},
			},
		},
		{
			name: "failed to get reservation",
			arg:  testPod,
			fields: fields{
				lister: &fakeReservationLister{
					getErr: map[string]bool{
						testReservation.Name: true,
					},
				},
			},
		},
		{
			name: "failed to update status",
			arg:  testPod,
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testReservation.Name: testReservation,
					},
				},
				client: &fakeReservationClient{
					updateStatusErr: map[string]bool{
						testReservation.Name: true,
					},
				},
			},
		},
		{
			name: "skip for failed reservation",
			arg:  testPod,
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testReservationFailed.Name: testReservationFailed,
					},
				},
			},
		},
		{
			name: "sync successfully",
			arg:  testPod,
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						testReservation.Name: testReservation,
					},
				},
				client: &fakeReservationClient{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				lister: tt.fields.lister,
				client: tt.fields.client,
			}
			if tt.fields.lister != nil && tt.fields.client != nil {
				tt.fields.client.lister = tt.fields.lister
			}
			p.syncPodDeleted(tt.arg)
		})
	}
}

func testListExistAndExpired(lister listerschedulingv1alpha1.ReservationLister) (exist, expired map[string]*schedulingv1alpha1.Reservation, err error) {
	rList, err := lister.List(labels.Everything())
	if err != nil {
		return nil, nil, err
	}
	exist = map[string]*schedulingv1alpha1.Reservation{}
	expired = map[string]*schedulingv1alpha1.Reservation{}
	for _, r := range rList {
		exist[string(r.UID)] = r
		if r.Status.Phase == schedulingv1alpha1.ReservationFailed {
			expired[string(r.UID)] = r
		}
	}
	return exist, expired, nil
}
