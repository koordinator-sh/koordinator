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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	clientschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/scheduling/v1alpha1"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
)

var _ listerschedulingv1alpha1.ReservationLister = &fakeReservationLister{}
var _ listerschedulingv1alpha1.ReservationNamespaceLister = &fakeReservationLister{}

type fakeReservationLister struct {
	reservations map[string]*schedulingv1alpha1.Reservation
	listErr      bool
	getErr       map[string]bool
}

func (f *fakeReservationLister) List(selector labels.Selector) (ret []*schedulingv1alpha1.Reservation, err error) {
	if f.listErr {
		return nil, fmt.Errorf("list error")
	}
	var rList []*schedulingv1alpha1.Reservation
	for _, r := range f.reservations {
		rList = append(rList, r)
	}
	return rList, nil
}

func (f *fakeReservationLister) Reservations(namespace string) listerschedulingv1alpha1.ReservationNamespaceLister {
	return f
}

func (f *fakeReservationLister) Get(name string) (*schedulingv1alpha1.Reservation, error) {
	if f.getErr[name] {
		return nil, fmt.Errorf("get error")
	}
	return f.reservations[name], nil
}

type fakeReservationClient struct {
	clientschedulingv1alpha1.SchedulingV1alpha1Interface
	clientschedulingv1alpha1.ReservationInterface
	lister          *fakeReservationLister
	updateStatusErr map[string]bool
	deleteErr       map[string]bool
}

func (f *fakeReservationClient) Reservations(namespace string) clientschedulingv1alpha1.ReservationInterface {
	return f
}

func (f *fakeReservationClient) UpdateStatus(ctx context.Context, reservation *schedulingv1alpha1.Reservation, opts metav1.UpdateOptions) (*schedulingv1alpha1.Reservation, error) {
	if f.updateStatusErr[reservation.Name] {
		return nil, fmt.Errorf("updateStatus error")
	}
	r := f.lister.reservations[reservation.Name].DeepCopy()
	r.Status = reservation.Status
	f.lister.reservations[reservation.Name] = r
	return r, nil
}

func (f *fakeReservationClient) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	if f.deleteErr[name] {
		return fmt.Errorf("delete error")
	}
	delete(f.lister.reservations, name)
	return nil
}

func Test_gcReservations(t *testing.T) {
	type fields struct {
		reservationCache *reservationCache
		lister           *fakeReservationLister
		client           *fakeReservationClient
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
			rList, err := p.lister.List(labels.Everything())
			assert.NoError(t, err)
			gotExist := map[string]*schedulingv1alpha1.Reservation{}
			gotExpired := map[string]*schedulingv1alpha1.Reservation{}
			for _, r := range rList {
				gotExist[string(r.UID)] = r
				if r.Status.Phase == schedulingv1alpha1.ReservationExpired {
					gotExpired[string(r.UID)] = r
				}
			}
			assert.Equal(t, tt.wantFields.exist, gotExist)
			assert.Equal(t, tt.wantFields.expired, gotExpired)
		})
	}
}
