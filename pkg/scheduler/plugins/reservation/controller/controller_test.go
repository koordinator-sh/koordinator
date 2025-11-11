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

package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	basemetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/testutil"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestFailedOrSucceededReservation(t *testing.T) {
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	failedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "failedReservation",
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationFailed,
		},
	}
	succededReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "succededReservation",
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationSucceeded,
		},
	}
	_, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), failedReservation, metav1.CreateOptions{})
	assert.NoError(t, err)
	_, err = fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), succededReservation, metav1.CreateOptions{})
	assert.NoError(t, err)

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{})

	sharedInformerFactory.Start(nil)
	koordSharedInformerFactory.Start(nil)
	sharedInformerFactory.WaitForCacheSync(nil)
	koordSharedInformerFactory.WaitForCacheSync(nil)

	_, err = controller.sync(getReservationKey(failedReservation))
	assert.NoError(t, err)
	_, err = controller.sync(getReservationKey(succededReservation))
	assert.NoError(t, err)

	got, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), failedReservation.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, failedReservation, got)

	got, err = fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), succededReservation.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, succededReservation, got)
}

func TestExpireActiveReservation(t *testing.T) {
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	shouldExpireReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "shouldExpireReservation",
			CreationTimestamp: metav1.Time{
				Time: time.Now().Add(-5 * time.Minute),
			},
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			TTL: &metav1.Duration{
				Duration: 1 * time.Minute,
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
		},
	}
	pendingReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "pendingReservation",
			CreationTimestamp: metav1.Time{
				Time: time.Now().Add(-5 * time.Minute),
			},
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			TTL: &metav1.Duration{
				Duration: 1 * time.Minute,
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationPending,
			NodeName: "test-node",
		},
	}
	normalReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:               uuid.NewUUID(),
			Name:              "normalReservation",
			CreationTimestamp: metav1.Now(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			TTL: &metav1.Duration{
				Duration: 1 * time.Minute,
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
		},
	}
	missingNodeReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "missingNodeReservation",
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "missing-node",
		},
	}

	reservations := []*schedulingv1alpha1.Reservation{
		shouldExpireReservation,
		pendingReservation,
		normalReservation,
		missingNodeReservation,
	}
	for _, v := range reservations {
		_, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), v, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	_, err := fakeClientSet.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
	assert.NoError(t, err)

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{})

	sharedInformerFactory.Start(nil)
	koordSharedInformerFactory.Start(nil)
	sharedInformerFactory.WaitForCacheSync(nil)
	koordSharedInformerFactory.WaitForCacheSync(nil)

	for _, v := range reservations {
		_, err := controller.sync(getReservationKey(v))
		assert.NoError(t, err)
	}

	got, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), shouldExpireReservation.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.True(t, reservationutil.IsReservationExpired(got))

	got, err = fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), pendingReservation.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.True(t, reservationutil.IsReservationExpired(got))

	r, err := controller.sync(getReservationKey(normalReservation))
	assert.NoError(t, err)
	assert.Equal(t, maxRetryAfterTime, r.requeueAfter)

	got, err = fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), normalReservation.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, normalReservation, got)

	got, err = fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), missingNodeReservation.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.True(t, reservationutil.IsReservationExpired(got))
}

func TestSyncStatus(t *testing.T) {
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)
	// register metrics
	metricsRegistry := basemetrics.NewKubeRegistry()
	metrics.ReservationStatusPhase.GetGaugeVec().Reset()
	metrics.ReservationResource.GetGaugeVec().Reset()
	metricsRegistry.Registerer().MustRegister(
		metrics.ReservationStatusPhase.GetGaugeVec(),
		metrics.ReservationResource.GetGaugeVec())

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:               uuid.NewUUID(),
			Name:              "normalReservation",
			CreationTimestamp: metav1.Now(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			TTL: &metav1.Duration{
				Duration: 1 * time.Minute,
			},
			AllocateOnce: pointer.Bool(true),
			Template:     &corev1.PodTemplateSpec{},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10000m"),
				corev1.ResourceMemory: resource.MustParse("10Gi"),
			},
		},
	}

	_, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
	assert.NoError(t, err)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	_, err = fakeClientSet.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
	assert.NoError(t, err)

	var owners []corev1.ObjectReference
	for i := 0; i < 4; i++ {
		owners = append(owners, corev1.ObjectReference{
			UID:       types.UID(fmt.Sprintf("%d", i)),
			Namespace: "default",
			Name:      fmt.Sprintf("pod-%d", i),
		})
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID:       types.UID(fmt.Sprintf("%d", i)),
				Namespace: "default",
				Name:      fmt.Sprintf("pod-%d", i),
			},
			Spec: corev1.PodSpec{
				NodeName: node.Name,
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:              resource.MustParse("2000m"),
								corev1.ResourceMemory:           resource.MustParse("2Gi"),
								corev1.ResourceEphemeralStorage: resource.MustParse("10Gi"),
							},
						},
					},
				},
			},
		}
		apiext.SetReservationAllocated(pod, reservation)
		_, err := fakeClientSet.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{ControllerWorkers: 2, GCDurationSeconds: 3600, GCIntervalSeconds: 60})
	controller.Start()

	time.Sleep(100 * time.Millisecond)

	r, err := controller.sync(getReservationKey(reservation))
	assert.NoError(t, err)
	assert.Equal(t, time.Duration(0), r.requeueAfter)

	got, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
	assert.NoError(t, err)

	expectReservation := reservation.DeepCopy()
	expectReservation.Status.Phase = schedulingv1alpha1.ReasonReservationSucceeded
	reservationutil.SetReservationSucceeded(expectReservation)
	expectReservation.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("8000m"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	sort.Slice(owners, func(i, j int) bool {
		return owners[i].UID < owners[j].UID
	})
	expectReservation.Status.CurrentOwners = owners
	for i := range expectReservation.Status.Conditions {
		cond := &expectReservation.Status.Conditions[i]
		cond.LastProbeTime = metav1.Time{}
		cond.LastTransitionTime = metav1.Time{}
	}
	for i := range got.Status.Conditions {
		cond := &got.Status.Conditions[i]
		cond.LastProbeTime = metav1.Time{}
		cond.LastTransitionTime = metav1.Time{}
	}
	assert.Equal(t, expectReservation, got)
	// check metrics
	expectAvailableMetricCount := 0
	expectFailedMetricCount := 0
	expectPendingMetricCount := 0
	expectSucceededMetricCount := 1
	expectedSuccessedMetrics := fmt.Sprintf(`
				# HELP scheduler_reservation_status_phase The current number of reservations in each status phase (e.g. Pending, Available, Succeeded, Failed)
				# TYPE scheduler_reservation_status_phase gauge
				scheduler_reservation_status_phase{name="%s",phase="Available"} %v
				scheduler_reservation_status_phase{name="%s",phase="Failed"} %v
				scheduler_reservation_status_phase{name="%s",phase="Pending"} %v
				scheduler_reservation_status_phase{name="%s",phase="Succeeded"} %v
				`,
		"normalReservation", expectAvailableMetricCount,
		"normalReservation", expectFailedMetricCount,
		"normalReservation", expectPendingMetricCount,
		"normalReservation", expectSucceededMetricCount,
	)
	if err := testutil.GatherAndCompare(metricsRegistry, strings.NewReader(expectedSuccessedMetrics), "scheduler_reservation_status_phase"); err != nil {
		t.Error(err)
	}

	expectedUtilizationMetrics := fmt.Sprintf(`
# HELP scheduler_reservation_resource Resource metrics for a reservation, including allocatable, allocated, and utilization with unit.
# TYPE scheduler_reservation_resource gauge
scheduler_reservation_resource{name="%s",resource="cpu",type="allocatable",unit="core"} 10
scheduler_reservation_resource{name="%s",resource="cpu",type="allocated",unit="core"} 8
scheduler_reservation_resource{name="%s",resource="cpu",type="utilization",unit="ratio"} 0.8
scheduler_reservation_resource{name="%s",resource="memory",type="allocatable",unit="Gi"} 10
scheduler_reservation_resource{name="%s",resource="memory",type="allocated",unit="Gi"} 8
scheduler_reservation_resource{name="%s",resource="memory",type="utilization",unit="ratio"} 0.8
`,
		"normalReservation", "normalReservation",
		"normalReservation", "normalReservation",
		"normalReservation", "normalReservation",
	)

	if err := testutil.GatherAndCompare(metricsRegistry, strings.NewReader(expectedUtilizationMetrics),
		"scheduler_reservation_resource"); err != nil {
		t.Error(err)
	}
}

func Test_syncPodsForTerminatedReservation(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.CleanExpiredReservationAllocated, true)()

	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	testReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  "xxxxxx",
			Name: "test-reservation",
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationAvailable,
		},
	}
	testOwnerPods := []*corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       uuid.NewUUID(),
				Name:      "test-pod-1",
				Namespace: "test-ns",
				Annotations: map[string]string{
					apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "xxxxxx"}`,
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "test-node",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       uuid.NewUUID(),
				Name:      "test-pod-2",
				Namespace: "test-ns-2",
				Annotations: map[string]string{
					apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "xxxxxx"}`,
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "test-node",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       uuid.NewUUID(),
				Name:      "test-pod-3",
				Namespace: "test-ns-3",
				Annotations: map[string]string{
					apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "xxxxxx"}`,
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "test-node-1",
			},
		},
	}
	testNotFoundPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "test-not-found-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "xxxxxx"}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}
	testOtherPods := []*corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       uuid.NewUUID(),
				Name:      "test-other-pod-1",
				Namespace: "test-ns-1",
				Annotations: map[string]string{
					apiext.AnnotationReservationAllocated: `{"name": "test-other-reservation", "uid": "zzz"}`,
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "test-node",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       uuid.NewUUID(),
				Name:      "test-other-pod-2",
				Namespace: "test-ns-2",
				Annotations: map[string]string{
					apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "aaaaaa"}`,
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "test-node-1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       uuid.NewUUID(),
				Name:      "test-other-pod-3",
				Namespace: "test-ns-3",
			},
			Spec: corev1.PodSpec{
				NodeName: "test-node-3",
			},
		},
	}
	testErrPods := []*corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       uuid.NewUUID(),
				Name:      "test-err-pod",
				Namespace: "test-ns",
				Annotations: map[string]string{
					apiext.AnnotationReservationAllocated: `{]`,
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "test-node-1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				UID:       uuid.NewUUID(),
				Name:      "test-err-pod-1",
				Namespace: "test-ns",
				Annotations: map[string]string{
					apiext.AnnotationReservationAllocated: `{]`,
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "test-node-1",
			},
		},
	}

	_, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), testReservation, metav1.CreateOptions{})
	assert.NoError(t, err)
	err = fakeKoordClientSet.SchedulingV1alpha1().Reservations().Delete(context.TODO(), testReservation.Name, metav1.DeleteOptions{})
	assert.NoError(t, err)
	for _, pod := range append(testOwnerPods, append(testOtherPods, testErrPods...)...) {
		_, err = fakeClientSet.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{})

	sharedInformerFactory.Start(nil)
	koordSharedInformerFactory.Start(nil)
	sharedInformerFactory.WaitForCacheSync(nil)
	koordSharedInformerFactory.WaitForCacheSync(nil)

	controller.onReservationAdd(testReservation)
	controller.onReservationDelete(testReservation)
	for _, pod := range append(testOwnerPods, testNotFoundPod) {
		controller.onPodAdd(pod)
	}
	for _, pod := range testOtherPods {
		// force add to mock unexpected cache
		controller.updatePod(pod, &apiext.ReservationAllocated{UID: testReservation.UID, Name: testReservation.Name})
	}

	_, err = controller.sync(getReservationKey(testReservation))
	assert.NoError(t, err)

	_, err = fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), testReservation.Name, metav1.GetOptions{})
	assert.Error(t, err)
	assert.True(t, errors.IsNotFound(err), err)
	for _, pod := range testOwnerPods {
		gotPod, err := fakeClientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, gotPod)
		assert.Equal(t, "", gotPod.Annotations[apiext.AnnotationReservationAllocated])
	}
	for _, pod := range testOtherPods {
		gotPod, err := fakeClientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, gotPod)
		assert.Equal(t, pod.Annotations, gotPod.Annotations)
	}

	for _, pod := range testErrPods {
		// force add to mock unexpected cache
		controller.updatePod(pod, &apiext.ReservationAllocated{UID: testReservation.UID, Name: testReservation.Name})
	}
	_, err = controller.sync(getReservationKey(testReservation))
	assert.Error(t, err)
	for _, pod := range testErrPods {
		gotPod, err := fakeClientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, gotPod)
		assert.Equal(t, pod.Annotations, gotPod.Annotations)
	}
}

func Test_podEventHandlers(t *testing.T) {
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{})

	sharedInformerFactory.Start(nil)
	koordSharedInformerFactory.Start(nil)
	sharedInformerFactory.WaitForCacheSync(nil)
	koordSharedInformerFactory.WaitForCacheSync(nil)

	assert.Equal(t, 0, len(controller.getPodsOnNode("test-node-1")))
	controller.onPodAdd(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "aaaaaa",
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "aaaaaa"}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	})
	assert.Equal(t, 1, len(controller.getPodsOnNode("test-node-1")))
	assert.Equal(t, 0, len(controller.getPodsOnNode("test-node-2")))
	assert.Equal(t, 1, len(controller.getPodsOnReservation("aaaaaa")))
	assert.Equal(t, 0, len(controller.getPodsOnReservation("bbb")))

	controller.onPodUpdate(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "aaaaaa",
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "aaaaaa"}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "aaaaaa",
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "aaaaaa"}`,
			},
			Labels: map[string]string{
				"foor": "bar",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	})
	assert.Equal(t, 1, len(controller.getPodsOnReservation("aaaaaa")))
	controller.onPodUpdate(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "aaaaaa",
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "aaaaaa"}`,
			},
			Labels: map[string]string{
				"foor": "bar",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:         "aaaaaa",
			Name:        "test-pod",
			Namespace:   "test-ns",
			Annotations: map[string]string{},
			Labels: map[string]string{
				"foor": "bar",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	})
	assert.Equal(t, 0, len(controller.getPodsOnReservation("aaaaaa")))
	controller.onPodUpdate(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:         "aaaaaa",
			Name:        "test-pod",
			Namespace:   "test-ns",
			Annotations: map[string]string{},
			Labels: map[string]string{
				"foor": "bar",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "aaaaaa",
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "aaaaaa"}`,
			},
			Labels: map[string]string{
				"foor": "bar",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	})
	assert.Equal(t, 1, len(controller.getPodsOnReservation("aaaaaa")))

	controller.onPodDelete(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "aaaaaa",
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "aaaaaa"}`,
			},
			Labels: map[string]string{
				"foor": "bar",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	})
	assert.Equal(t, 0, len(controller.getPodsOnReservation("aaaaaa")))
}

func TestNew(t *testing.T) {
	tests := []struct {
		name                 string
		args                 *config.ReservationArgs
		expectedNumWorker    int
		expectedIsGCDisabled bool
		expectedGCDuration   time.Duration
		expectedGCInterval   time.Duration
	}{
		{
			name:                 "default values",
			args:                 nil,
			expectedNumWorker:    1,
			expectedIsGCDisabled: false,
			expectedGCDuration:   defaultGCDuration,
			expectedGCInterval:   defaultGCCheckInterval,
		},
		{
			name: "custom values",
			args: &config.ReservationArgs{
				ControllerWorkers:        3,
				GCDurationSeconds:        7200,
				GCIntervalSeconds:        120,
				DisableGarbageCollection: false,
			},
			expectedNumWorker:    3,
			expectedIsGCDisabled: false,
			expectedGCDuration:   2 * time.Hour,
			expectedGCInterval:   120 * time.Second,
		},
		{
			name: "disable garbage collection",
			args: &config.ReservationArgs{
				ControllerWorkers:        2,
				GCDurationSeconds:        3600,
				GCIntervalSeconds:        90,
				DisableGarbageCollection: true,
			},
			expectedNumWorker:    2,
			expectedIsGCDisabled: true,
			expectedGCDuration:   1 * time.Hour,
			expectedGCInterval:   90 * time.Second,
		},
		{
			name: "zero gc interval uses default",
			args: &config.ReservationArgs{
				ControllerWorkers:        1,
				GCDurationSeconds:        0,
				GCIntervalSeconds:        0,
				DisableGarbageCollection: false,
			},
			expectedNumWorker:    1,
			expectedIsGCDisabled: false,
			expectedGCDuration:   defaultGCDuration,
			expectedGCInterval:   defaultGCCheckInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClientSet := kubefake.NewSimpleClientset()
			fakeKoordClientSet := koordfake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
			koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

			controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, tt.args)

			assert.Equal(t, tt.expectedNumWorker, controller.numWorker)
			assert.Equal(t, tt.expectedIsGCDisabled, controller.isGCDisabled)
			assert.Equal(t, tt.expectedGCDuration, controller.gcDuration)
			assert.Equal(t, tt.expectedGCInterval, controller.gcInterval)
		})
	}
}
