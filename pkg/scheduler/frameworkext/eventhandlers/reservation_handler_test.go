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

package eventhandlers

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/apis/core/validation"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/pointer"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type fakeReservationCache struct{}

func (f *fakeReservationCache) DeleteReservation(r *schedulingv1alpha1.Reservation) *frameworkext.ReservationInfo {
	return frameworkext.NewReservationInfo(r)
}

func (f *fakeReservationCache) GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	return nil
}

func TestAddReservationErrorHandler(t *testing.T) {
	testNodeName := "test-node-0"
	testR := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-1",
			UID:  "1234",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-1",
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					Object: &corev1.ObjectReference{
						Name: "test-pod-1",
					},
				},
			},
			TTL: &metav1.Duration{Duration: 30 * time.Minute},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationPending,
			NodeName: testNodeName,
		},
	}
	testPod := reservationutil.NewReservePod(testR)

	t.Run("test not panic", func(t *testing.T) {
		registeredPlugins := []schedulertesting.RegisterPluginFunc{
			schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
			schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		}

		fakeRecorder := record.NewFakeRecorder(1024)
		eventRecorder := record.NewEventRecorderAdapter(fakeRecorder)

		fh, err := schedulertesting.NewFramework(context.TODO(), registeredPlugins, "koord-scheduler",
			frameworkruntime.WithEventRecorder(eventRecorder),
			frameworkruntime.WithClientSet(kubefake.NewSimpleClientset()),
			frameworkruntime.WithInformerFactory(informers.NewSharedInformerFactory(kubefake.NewSimpleClientset(), 0)),
		)
		assert.Nil(t, err)
		sched := &scheduler.Scheduler{
			Profiles: profile.Map{
				"default-scheduler": fh,
			},
		}
		internalHandler := &frameworkext.FakeScheduler{}
		koordClientSet := koordfake.NewSimpleClientset(testR)
		koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)

		handler := MakeReservationErrorHandler(sched, internalHandler, koordClientSet, koordSharedInformerFactory)

		koordSharedInformerFactory.Start(nil)
		koordSharedInformerFactory.WaitForCacheSync(nil)

		podInfo, _ := framework.NewPodInfo(testPod)
		queuedPodInfo := &framework.QueuedPodInfo{
			PodInfo: podInfo,
		}

		expectedErr := errors.New(strings.Repeat("test error", validation.NoteLengthLimit))
		handler(context.TODO(), fh, queuedPodInfo, framework.AsStatus(expectedErr), nil, time.Now())

		r, err := koordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), testR.Name, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, r)
		var message string
		for _, v := range r.Status.Conditions {
			if v.Type == schedulingv1alpha1.ReservationConditionScheduled && v.Reason == schedulingv1alpha1.ReasonReservationUnschedulable {
				message = v.Message
			}
		}
		assert.Equal(t, expectedErr.Error(), message)

	})
}

func TestAddScheduleEventHandler(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		sched := &scheduler.Scheduler{}
		internalHandler := &frameworkext.FakeScheduler{}
		koordClientSet := koordfake.NewSimpleClientset()
		koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
		AddScheduleEventHandler(sched, internalHandler, koordSharedInformerFactory)
	})
}

func Test_addReservationToCache(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name           string
		obj            *schedulingv1alpha1.Reservation
		wantPodFromObj bool
		wantPod        *corev1.Pod
	}{
		{
			name: "failed to validate reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: nil,
				},
			},
			wantPod: nil,
		},
		{
			name: "add pending reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
			},
			wantPod: nil,
		},
		{
			name: "add available reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationAvailable,
					NodeName: "test-node",
				},
			},
			wantPodFromObj: true,
		},
		{
			name: "add failed reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationFailed,
					NodeName: "test-node",
				},
			},
			wantPod: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched := frameworkext.NewFakeScheduler()
			addReservationToSchedulerCache(sched, tt.obj)
			pod, err := sched.GetPod(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: tt.obj.GetUID(),
				},
			})
			assert.NoError(t, err)
			wantPod := tt.wantPod
			if tt.wantPodFromObj {
				wantPod = reservationutil.NewReservePod(tt.obj)
				if wantPod.Spec.NodeName != "" {
					wantPod.Spec.Priority = pointer.Int32(math.MaxInt32)
				}
			}
			assert.Equal(t, wantPod, pod)
		})
	}
}

func Test_updateReservationInCache(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name           string
		oldObj         *schedulingv1alpha1.Reservation
		newObj         *schedulingv1alpha1.Reservation
		wantPodFromObj bool
		wantPod        *corev1.Pod
	}{
		{
			name: "failed to validate reservation",
			oldObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: nil,
				},
			},
			newObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: nil,
				},
			},
			wantPod: nil,
		},
		{
			name: "update reservation from pending to available",
			oldObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
			},
			newObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test",
								},
							},
						},
					},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationAvailable,
				},
			},
			wantPodFromObj: true,
		},
		{
			name: "update available reservation",
			oldObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test",
								},
							},
						},
					},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationAvailable,
				},
			},
			newObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test",
								},
							},
						},
					},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationAvailable,
				},
			},
			wantPodFromObj: true,
		},
		{
			name: "update reservation from available to succeeded",
			oldObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test",
								},
							},
						},
					},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationAvailable,
				},
			},
			newObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test",
								},
							},
						},
					},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationSucceeded,
				},
			},
			wantPod: nil,
		},
		{
			name: "update reservation from available to failed",
			oldObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test",
								},
							},
						},
					},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationAvailable,
				},
			},
			newObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test",
								},
							},
						},
					},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationFailed,
				},
			},
			wantPod: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reservation.SetReservationCache(&fakeReservationCache{})
			sched := frameworkext.NewFakeScheduler()
			updateReservationInSchedulerCache(sched, tt.oldObj, tt.newObj)
			pod, err := sched.GetPod(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: tt.newObj.GetUID(),
				},
			})
			assert.NoError(t, err)
			wantPod := tt.wantPod
			if tt.wantPodFromObj {
				wantPod = reservationutil.NewReservePod(tt.newObj)
				if wantPod.Spec.NodeName != "" {
					wantPod.Spec.Priority = pointer.Int32(math.MaxInt32)
				}
			}
			assert.Equal(t, wantPod, pod)
		})
	}
}

func Test_deleteReservationFromCache(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		obj  *schedulingv1alpha1.Reservation
	}{
		{
			name: "failed to validate reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: nil,
				},
			},
		},
		{
			name: "delete available reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},

				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationAvailable,
				},
			},
		},
		{
			name: "delete succeeded reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},

				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationSucceeded,
				},
			},
		},
		{
			name: "delete succeeded reservation",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},

				Status: schedulingv1alpha1.ReservationStatus{
					NodeName: "test-node",
					Phase:    schedulingv1alpha1.ReservationFailed,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reservation.SetReservationCache(&fakeReservationCache{})
			sched := frameworkext.NewFakeScheduler()
			if reservationutil.ValidateReservation(tt.obj) == nil {
				sched.AddPod(klog.Background(), reservationutil.NewReservePod(tt.obj))
			}
			deleteReservationFromSchedulerCache(sched, tt.obj)
			pod, err := sched.GetPod(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: tt.obj.GetUID(),
				},
			})
			assert.NoError(t, err)
			assert.Nil(t, pod)
		})
	}
}

func Test_addReservationToSchedulingQueue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name           string
		obj            *schedulingv1alpha1.Reservation
		wantPodFromObj bool
	}{
		{
			name: "allow incomplete reservation, validate it in plugin",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: nil,
				},
			},
			wantPodFromObj: true,
		},
		{
			name: "add reservation successfully",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
					UID:  "123456",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
			},
			wantPodFromObj: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched := frameworkext.NewFakeScheduler()
			addReservationToSchedulingQueue(sched, tt.obj)
			pod := sched.Queue.Pods[string(tt.obj.UID)]
			var wantPod *corev1.Pod
			if tt.wantPodFromObj {
				wantPod = reservationutil.NewReservePod(tt.obj)
			}
			assert.Equal(t, wantPod, pod)
		})
	}
}

func Test_updateReservationInSchedulingQueue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name           string
		oldObj         *schedulingv1alpha1.Reservation
		newObj         *schedulingv1alpha1.Reservation
		wantPodFromObj bool
	}{
		{
			name: "allow incomplete reservation, validate it in plugin",
			oldObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "r-0",
					UID:             "123",
					ResourceVersion: "1",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: nil,
				},
			},
			newObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "r-0",
					UID:             "123",
					ResourceVersion: "2",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: nil,
				},
			},
			wantPodFromObj: true,
		},
		{
			name: "update reservation successfully",
			oldObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "r-0",
					UID:             "456",
					ResourceVersion: "1",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
			},
			newObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "r-0",
					UID:             "456",
					ResourceVersion: "2",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
			},
			wantPodFromObj: true,
		},
		{
			name: "update new reservation successfully",
			oldObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "r-0",
					UID:             "456",
					ResourceVersion: "0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
			},
			newObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "r-0",
					UID:             "456",
					ResourceVersion: "1",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
			},
			wantPodFromObj: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched := frameworkext.NewFakeScheduler()
			updateReservationInSchedulingQueue(sched, tt.oldObj, tt.newObj)
			pod := sched.Queue.Pods[string(tt.newObj.UID)]
			var wantPod *corev1.Pod
			if tt.wantPodFromObj {
				wantPod = reservationutil.NewReservePod(tt.newObj)
			}
			assert.Equal(t, wantPod, pod)
		})
	}
}

var _ framework.PermitPlugin = &fakePermitPlugin{}

type fakePermitPlugin struct{}

func (f *fakePermitPlugin) Name() string { return "fakePermitPlugin" }

func (f *fakePermitPlugin) Permit(ctx context.Context, state *framework.CycleState, p *corev1.Pod, nodeName string) (*framework.Status, time.Duration) {
	return framework.NewStatus(framework.Wait), 30 * time.Second
}

func Test_deleteReservationFromSchedulingQueue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		obj     *schedulingv1alpha1.Reservation
		waiting bool
	}{
		{
			name: "allow incomplete reservation, validate it later",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: nil,
				},
			},
		},
		{
			name: "delete reservation successfully",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
			},
		},
		{
			name: "delete permitted reservation successfully",
			obj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{},
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "pod-0",
							},
						},
					},
					Expires: &metav1.Time{Time: now.Add(30 * time.Minute)},
				},
			},
			waiting: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
				schedulertesting.RegisterPermitPlugin("fakePermitPlugin", func(_ runtime.Object, f framework.Handle) (framework.Plugin, error) {
					return &fakePermitPlugin{}, nil
				}),
			}
			fh, err := schedulertesting.NewFramework(context.TODO(), registeredPlugins, corev1.DefaultSchedulerName)
			assert.NoError(t, err)

			sched := &scheduler.Scheduler{
				Profiles: profile.Map{
					corev1.DefaultSchedulerName: fh,
				},
			}

			reservePod := reservationutil.NewReservePod(tt.obj)
			cycleState := framework.NewCycleState()
			rejected := make(chan bool, 1)
			if tt.waiting {
				status := fh.RunPermitPlugins(context.TODO(), cycleState, reservePod, "test-node")
				assert.True(t, status.IsSuccess() || status.Code() == framework.Wait)
				hasWaitingPod := false
				fh.IterateOverWaitingPods(func(pod framework.WaitingPod) {
					if pod.GetPod() != nil && pod.GetPod().UID == reservePod.UID {
						hasWaitingPod = true
					}
				})
				assert.True(t, hasWaitingPod)
				go func() {
					fh.WaitOnPermit(context.TODO(), reservePod)
					rejected <- true
				}()
			}

			schedAdapter := frameworkext.NewFakeScheduler()
			addReservationToSchedulingQueue(schedAdapter, tt.obj)
			assert.NotNil(t, schedAdapter.Queue.Pods[string(tt.obj.UID)])
			deleteReservationFromSchedulingQueue(sched, schedAdapter, tt.obj)
			assert.Nil(t, schedAdapter.Queue.Pods[string(tt.obj.UID)])

			if tt.waiting {
				hasRejected := <-rejected
				assert.True(t, hasRejected)
			}
		})
	}
}

func Test_unscheduledReservationEventHandler(t *testing.T) {
	sched := &scheduler.Scheduler{
		Profiles: map[string]framework.Framework{
			corev1.DefaultSchedulerName: nil,
		},
	}
	adapt := frameworkext.NewFakeScheduler()
	handler := unscheduledReservationEventHandler(sched, adapt)
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "r-0",
			UID:             "456",
			ResourceVersion: "1",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					Object: &corev1.ObjectReference{
						Kind: "Pod",
						Name: "pod-0",
					},
				},
			},
		},
	}
	handler.OnAdd(reservation, true)
	assert.NotNil(t, adapt.Queue.Pods[string(reservation.UID)])
	reservationCopy := reservation.DeepCopy()
	reservationCopy.ResourceVersion = "2"
	reservationCopy.Status.Phase = schedulingv1alpha1.ReservationFailed
	handler.OnUpdate(reservation, reservationCopy)
	assert.Nil(t, adapt.Queue.Pods[string(reservation.UID)])
}

var _ framework.Framework = &fakeFramework{}

type fakeFramework struct {
	framework.Framework
}

func Test_isResponsibleForReservation(t *testing.T) {
	type args struct {
		profiles profile.Map
		r        *schedulingv1alpha1.Reservation
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "not responsible when profile is empty",
			args: args{
				profiles: profile.Map{},
				r:        &schedulingv1alpha1.Reservation{},
			},
			want: false,
		},
		{
			name: "responsible when scheduler name matched the profile",
			args: args{
				profiles: profile.Map{
					"test-scheduler": &fakeFramework{},
				},
				r: &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-reserve-sample",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Template: &corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								SchedulerName: "test-scheduler",
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
			got := isResponsibleForReservation(tt.args.profiles, tt.args.r)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_setReservationUnschedulable(t *testing.T) {
	type args struct {
		r   *schedulingv1alpha1.Reservation
		msg string
	}
	tests := []struct {
		name string
		args args
		want *schedulingv1alpha1.Reservation
	}{
		{
			name: "add condition",
			args: args{
				r: &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "reserve-pod-0",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Kind: "Pod",
									Name: "test-pod-0",
								},
							},
						},
						TTL: &metav1.Duration{Duration: 30 * time.Minute},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						Phase: schedulingv1alpha1.ReservationPending,
					},
				},
				msg: "unschedule msg",
			},
			want: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "test-pod-0",
							},
						},
					},
					TTL: &metav1.Duration{Duration: 30 * time.Minute},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationPending,
					Conditions: []schedulingv1alpha1.ReservationCondition{
						{
							Type:               schedulingv1alpha1.ReservationConditionScheduled,
							Status:             schedulingv1alpha1.ConditionStatusFalse,
							Reason:             schedulingv1alpha1.ReasonReservationUnschedulable,
							Message:            "unschedule msg",
							LastProbeTime:      metav1.Now(),
							LastTransitionTime: metav1.Now(),
						},
					},
				},
			},
		},
		{
			name: "update condition",
			args: args{
				r: &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "reserve-pod-0",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Kind: "Pod",
									Name: "test-pod-0",
								},
							},
						},
						TTL: &metav1.Duration{Duration: 30 * time.Minute},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						Phase: schedulingv1alpha1.ReservationPending,
						Conditions: []schedulingv1alpha1.ReservationCondition{
							{
								Type:               schedulingv1alpha1.ReservationConditionScheduled,
								Status:             schedulingv1alpha1.ConditionStatusTrue,
								Reason:             schedulingv1alpha1.ReasonReservationScheduled,
								LastProbeTime:      metav1.Now(),
								LastTransitionTime: metav1.Now(),
							},
						},
					},
				},
				msg: "unschedule msg",
			},
			want: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "test-pod-0",
							},
						},
					},
					TTL: &metav1.Duration{Duration: 30 * time.Minute},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationPending,
					Conditions: []schedulingv1alpha1.ReservationCondition{
						{
							Type:               schedulingv1alpha1.ReservationConditionScheduled,
							Status:             schedulingv1alpha1.ConditionStatusTrue,
							Reason:             schedulingv1alpha1.ReasonReservationScheduled,
							LastProbeTime:      metav1.Now(),
							LastTransitionTime: metav1.Now(),
						},
					},
				},
			},
		},
		{
			name: "change condition",
			args: args{
				r: &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "reserve-pod-0",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Owners: []schedulingv1alpha1.ReservationOwner{
							{
								Object: &corev1.ObjectReference{
									Kind: "Pod",
									Name: "test-pod-0",
								},
							},
						},
						TTL: &metav1.Duration{Duration: 30 * time.Minute},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						Phase: schedulingv1alpha1.ReservationPending,
						Conditions: []schedulingv1alpha1.ReservationCondition{
							{
								Type:               schedulingv1alpha1.ReservationConditionScheduled,
								Status:             schedulingv1alpha1.ConditionStatusFalse,
								Reason:             schedulingv1alpha1.ReasonReservationUnschedulable,
								Message:            "old unschedule msg",
								LastProbeTime:      metav1.Now(),
								LastTransitionTime: metav1.Now(),
							},
						},
					},
				},
				msg: "unschedule msg",
			},
			want: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-0",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Owners: []schedulingv1alpha1.ReservationOwner{
						{
							Object: &corev1.ObjectReference{
								Kind: "Pod",
								Name: "test-pod-0",
							},
						},
					},
					TTL: &metav1.Duration{Duration: 30 * time.Minute},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationPending,
					Conditions: []schedulingv1alpha1.ReservationCondition{
						{
							Type:               schedulingv1alpha1.ReservationConditionScheduled,
							Status:             schedulingv1alpha1.ConditionStatusFalse,
							Reason:             schedulingv1alpha1.ReasonReservationUnschedulable,
							Message:            "unschedule msg",
							LastProbeTime:      metav1.Now(),
							LastTransitionTime: metav1.Now(),
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setReservationUnschedulable(tt.args.r, tt.args.msg)
			assertEqualReservationCondition(t, tt.want, tt.args.r)
		})
	}
}

func assertEqualReservationCondition(t *testing.T, expect, got *schedulingv1alpha1.Reservation) {
	if expect == nil && got == nil {
		return
	}
	if expect == nil || got == nil {
		if expect != got {
			t.Errorf("reservation condition not equal, expect %v, got %v", expect, got)
		}
		return
	}
	if len(expect.Status.Conditions) != len(got.Status.Conditions) {
		t.Errorf("reservation condition not equal, expect len %v, got len %v", len(expect.Status.Conditions), len(got.Status.Conditions))
		return
	}
	expectConditions := map[string]*schedulingv1alpha1.ReservationCondition{}
	for i, condition := range expect.Status.Conditions {
		expectConditions[string(condition.Type)] = &expect.Status.Conditions[i]
	}
	for _, condition := range got.Status.Conditions {
		e, ok := expectConditions[string(condition.Type)]
		if !ok {
			t.Errorf("reservation condition not equal, got unexpect condition type %v", condition.Type)
			continue
		}
		msg := "condition type " + string(condition.Type)
		assert.Equal(t, e.Status, condition.Status, msg)
		assert.Equal(t, e.Message, condition.Message, msg)
		assert.Equal(t, e.Reason, condition.Reason, msg)
	}
}

func Test_generatePodEventOnReservationLevel(t *testing.T) {
	tests := []struct {
		name          string
		errorMsg      string
		wantMsg       string
		wantIsReserve bool
	}{
		{
			name:          "simple reservation errors",
			errorMsg:      "0/3 nodes are available: 1 Reservation(s) Insufficient cpu. 1 Reservation(s) matched owner total.",
			wantMsg:       "0/1 reservations are available: 1 Reservation(s) Insufficient cpu.",
			wantIsReserve: true,
		},
		{
			name: "extract reservation errors ",
			errorMsg: "0/1 nodes are available: 3 Reservation(s) didn't match affinity rules, 1 Reservation(s) is unschedulable, " +
				"1 Reservation(s) is unavailable, 2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory, " +
				"1 Insufficient cpu, 1 Insufficient memory. 8 Reservation(s) matched owner total, " +
				"Gang \"default/demo-job-podgroup\" gets rejected due to pod is unschedulable.",
			wantMsg: "0/8 reservations are available: 3 Reservation(s) didn't match affinity rules, " +
				"1 Reservation(s) is unschedulable, 1 Reservation(s) is unavailable, " +
				"2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory.",
			wantIsReserve: true,
		},
		{
			name: "reservation gpu resource not enough",
			errorMsg: "0/21 nodes are available: 1 Insufficient nvidia.com/gpu by node, 4 Reservation(s) Insufficient nvidia.com/gpu. " +
				"1 Reservation(s) is unschedulable, 5 Reservation(s) matched owner total",
			wantMsg:       "0/5 reservations are available: 4 Reservation(s) Insufficient nvidia.com/gpu, 1 Reservation(s) is unschedulable.",
			wantIsReserve: true,
		},
		{
			name: "pod topology spread constraints missing required label errors",
			errorMsg: "0/5 nodes are available: 3 node(s) didn't match pod topology spread constraints (missing required label)," +
				"1 Insufficient cpu, 1 Insufficient memory, 2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory. " +
				"8 Reservation(s) matched owner total.",
			wantMsg: "0/8 reservations are available: at least 3 didn't match pod topology spread constraints (missing required label) Reservation(s), " +
				"2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory.",
			wantIsReserve: true,
		},
		{
			name: "pod topology spread constraints errors",
			errorMsg: "0/5 nodes are available: 3 node(s) didn't match pod topology spread constraints," +
				"1 Insufficient cpu, 1 Insufficient memory, 2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory. " +
				"8 Reservation(s) matched owner total.",
			wantMsg: "0/8 reservations are available: at least 3 didn't match pod topology spread constraints Reservation(s), " +
				"2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory.",
			wantIsReserve: true,
		},
		{
			name: "satisfy existing pods anti-affinity rules, errors",
			errorMsg: "0/5 nodes are available: 3 node(s) didn't satisfy existing pods anti-affinity rules," +
				"1 Insufficient cpu, 1 Insufficient memory, 2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory. " +
				"8 Reservation(s) matched owner total.",
			wantMsg: "0/8 reservations are available: at least 3 didn't satisfy existing pods anti-affinity rules Reservation(s), " +
				"2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory.",
			wantIsReserve: true,
		},
		{
			name: "match pod affinity rules errors",
			errorMsg: "0/5 nodes are available: 3 node(s) didn't match pod affinity rules," +
				"1 Insufficient cpu, 1 Insufficient memory, 2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory. " +
				"8 Reservation(s) matched owner total.",
			wantMsg: "0/8 reservations are available: at least 3 didn't match pod affinity rules Reservation(s), " +
				"2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory.",
			wantIsReserve: true,
		},
		{
			name: "match pod anti-affinity rules errors",
			errorMsg: "0/5 nodes are available: 3 node(s) didn't match pod anti-affinity rules," +
				"1 Insufficient cpu, 1 Insufficient memory, 2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory. " +
				"8 Reservation(s) matched owner total.",
			wantMsg: "0/8 reservations are available: at least 3 didn't match pod anti-affinity rules Reservation(s), " +
				"2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory.",
			wantIsReserve: true,
		},
		{
			name: "mix affinity errors of 'match pod topology spread constraints' and 'match pod affinity rules'",
			errorMsg: "0/5 nodes are available: 3 node(s) didn't match pod topology spread constraints, " +
				"1 node(s) didn't match pod affinity rules, " +
				"1 Insufficient memory, 2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory. " +
				"8 Reservation(s) matched owner total.",
			wantMsg: "0/8 reservations are available: at least 3 didn't match pod topology spread constraints Reservation(s), " +
				"at least 1 didn't match pod affinity rules Reservation(s), " +
				"2 Reservation(s) Insufficient cpu, 1 Reservation(s) Insufficient memory.",
			wantIsReserve: true,
		},
		{
			name: "only gang errors",
			errorMsg: "Gang \"default/demo-job-podgroup\" gets rejected due to member Pod \"demo-job-kfqfs\" is" +
				"unschedulable with reason \"0/3 nodes are available: 3 Insufficient cpu.\"",
			wantIsReserve: false,
		},
		{
			name:          "only node errors",
			errorMsg:      `0/5 nodes are available: 3 Insufficient cpu, 2 Insufficient memory.`,
			wantIsReserve: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMsg, hasReserveMsg := generatePodEventOnReservationLevel(tt.errorMsg)
			assert.Equal(t, tt.wantIsReserve, hasReserveMsg)
			if hasReserveMsg {
				assert.Equalf(t, tt.wantMsg, gotMsg, "generatePodEventOnReservationLevel(%v)", tt.errorMsg)
			}
		})
	}
}
