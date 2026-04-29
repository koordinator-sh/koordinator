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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	clientfeatures "k8s.io/client-go/features"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	basemetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/metrics/testutil"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type mutableClientFeatureGates interface {
	clientfeatures.Gates
	Set(key clientfeatures.Feature, value bool) error
}

func init() {
	// Disable WatchListClient to avoid fake client compatibility issues in tests.
	// In k8s v1.35, WatchListClient defaults to true (Beta), but fake clients
	// don't support bookmark events required by WatchList, causing WaitForCacheSync to hang.
	if fg, ok := clientfeatures.FeatureGates().(mutableClientFeatureGates); ok {
		_ = fg.Set(clientfeatures.WatchListClient, false)
	}
}

// noopResizeLock is a no-op implementation of ReservationResizeLock for testing.
type noopResizeLock struct{}

func (n *noopResizeLock) LockReservationForResize(_ types.UID, _ string)   {}
func (n *noopResizeLock) UnlockReservationForResize(_ types.UID, _ string) {}
func (n *noopResizeLock) CleanStaleLocks() int                             { return 0 }

// trackingResizeLock records lock/unlock calls for test assertions.
type trackingResizeLock struct {
	mu        sync.Mutex
	locked    map[types.UID]string // uid -> nodeName
	lockLog   []types.UID
	unlockLog []types.UID
}

func newTrackingResizeLock() *trackingResizeLock {
	return &trackingResizeLock{locked: map[types.UID]string{}}
}

func (t *trackingResizeLock) LockReservationForResize(uid types.UID, nodeName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.locked[uid] = nodeName
	t.lockLog = append(t.lockLog, uid)
}

func (t *trackingResizeLock) UnlockReservationForResize(uid types.UID, _ string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.locked, uid)
	t.unlockLog = append(t.unlockLog, uid)
}

func (t *trackingResizeLock) CleanStaleLocks() int { return 0 }

func (t *trackingResizeLock) isLocked(uid types.UID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.locked[uid]
	return ok
}
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

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{}, &noopResizeLock{}, nil)

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

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{}, &noopResizeLock{}, nil)

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
	metrics.Register()
	metrics.ReservationStatusPhase.Reset()
	metricsResourceRegistry := basemetrics.NewKubeRegistry()
	metrics.ReservationResource.GetGaugeVec().Reset()
	metricsResourceRegistry.Registerer().MustRegister(metrics.ReservationResource.GetGaugeVec())

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
			AllocateOnce: ptr.To[bool](true),
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10000m"),
									corev1.ResourceMemory: resource.MustParse("10Gi"),
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

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{ControllerWorkers: 2, GCDurationSeconds: 3600, GCIntervalSeconds: 60}, &noopResizeLock{}, nil)
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
	// phase metrics are only written by resyncReservations(), not by syncStatus();
	// no phase metric assertion here because the legacyregistry is shared across tests.

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

	if err := testutil.GatherAndCompare(metricsResourceRegistry, strings.NewReader(expectedUtilizationMetrics),
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

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{}, &noopResizeLock{}, nil)

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

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{}, &noopResizeLock{}, nil)

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

func TestPodEventHandlerUnassignedPod(t *testing.T) {
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  "test-reservation-uid",
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node-1",
		},
	}
	_, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
	assert.NoError(t, err)

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{}, &noopResizeLock{}, nil)

	sharedInformerFactory.Start(nil)
	koordSharedInformerFactory.Start(nil)
	sharedInformerFactory.WaitForCacheSync(nil)
	koordSharedInformerFactory.WaitForCacheSync(nil)

	// test pod becoming unassigned (multi-scheduler scenario)
	oldPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "test-pod-uid",
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "test-reservation-uid"}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "test-pod-uid",
			Name:      "test-pod",
			Namespace: "test-ns",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name": "test-reservation", "uid": "test-reservation-uid"}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "", // became unassigned
		},
	}

	// add pod to controller cache
	controller.onPodAdd(oldPod)
	assert.Equal(t, 1, len(controller.getPodsOnReservation("test-reservation-uid")))

	// update pod to unassigned state
	controller.onPodUpdate(oldPod, newPod)
	assert.Equal(t, 0, len(controller.getPodsOnReservation("test-reservation-uid")))
	assert.Equal(t, 0, len(controller.getPodsOnNode("test-node-1")))
}

func TestRecordReservationPhases(t *testing.T) {
	tests := []struct {
		name            string
		reservation     *schedulingv1alpha1.Reservation
		expectedMetrics string
	}{
		{
			name: "Available reservation",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "r-available"},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationAvailable,
				},
			},
			expectedMetrics: `
# HELP scheduler_reservation_status_phase [ALPHA] The current number of reservations in each status phase (e.g. Pending, Available, Succeeded, Failed)
# TYPE scheduler_reservation_status_phase gauge
scheduler_reservation_status_phase{name="r-available",phase="Available"} 1
scheduler_reservation_status_phase{name="r-available",phase="Failed"} 0
scheduler_reservation_status_phase{name="r-available",phase="Pending"} 0
scheduler_reservation_status_phase{name="r-available",phase="Succeeded"} 0
`,
		},
		{
			name: "Pending reservation",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "r-pending"},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationPending,
				},
			},
			expectedMetrics: `
# HELP scheduler_reservation_status_phase [ALPHA] The current number of reservations in each status phase (e.g. Pending, Available, Succeeded, Failed)
# TYPE scheduler_reservation_status_phase gauge
scheduler_reservation_status_phase{name="r-pending",phase="Available"} 0
scheduler_reservation_status_phase{name="r-pending",phase="Failed"} 0
scheduler_reservation_status_phase{name="r-pending",phase="Pending"} 1
scheduler_reservation_status_phase{name="r-pending",phase="Succeeded"} 0
`,
		},
		{
			name: "Succeeded reservation",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "r-succeeded"},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationSucceeded,
				},
			},
			expectedMetrics: `
# HELP scheduler_reservation_status_phase [ALPHA] The current number of reservations in each status phase (e.g. Pending, Available, Succeeded, Failed)
# TYPE scheduler_reservation_status_phase gauge
scheduler_reservation_status_phase{name="r-succeeded",phase="Available"} 0
scheduler_reservation_status_phase{name="r-succeeded",phase="Failed"} 0
scheduler_reservation_status_phase{name="r-succeeded",phase="Pending"} 0
scheduler_reservation_status_phase{name="r-succeeded",phase="Succeeded"} 1
`,
		},
		{
			name: "Failed reservation",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "r-failed"},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase: schedulingv1alpha1.ReservationFailed,
				},
			},
			expectedMetrics: `
# HELP scheduler_reservation_status_phase [ALPHA] The current number of reservations in each status phase (e.g. Pending, Available, Succeeded, Failed)
# TYPE scheduler_reservation_status_phase gauge
scheduler_reservation_status_phase{name="r-failed",phase="Available"} 0
scheduler_reservation_status_phase{name="r-failed",phase="Failed"} 1
scheduler_reservation_status_phase{name="r-failed",phase="Pending"} 0
scheduler_reservation_status_phase{name="r-failed",phase="Succeeded"} 0
`,
		},
		{
			name: "reservation with empty phase defaults to Pending",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{Name: "r-empty-phase"},
				Status:     schedulingv1alpha1.ReservationStatus{},
			},
			expectedMetrics: `
# HELP scheduler_reservation_status_phase [ALPHA] The current number of reservations in each status phase (e.g. Pending, Available, Succeeded, Failed)
# TYPE scheduler_reservation_status_phase gauge
scheduler_reservation_status_phase{name="r-empty-phase",phase="Available"} 0
scheduler_reservation_status_phase{name="r-empty-phase",phase="Failed"} 0
scheduler_reservation_status_phase{name="r-empty-phase",phase="Pending"} 1
scheduler_reservation_status_phase{name="r-empty-phase",phase="Succeeded"} 0
`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			metrics.Register()
			metrics.ReservationStatusPhase.Reset()

			RecordReservationPhases(tt.reservation)

			if err := testutil.GatherAndCompare(legacyregistry.DefaultGatherer, strings.NewReader(tt.expectedMetrics), "scheduler_reservation_status_phase"); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestResyncReservations(t *testing.T) {
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	// Register metrics into the global legacyregistry and reset to ensure isolation.
	metrics.Register()
	metrics.ReservationStatusPhase.Reset()

	reservations := []*schedulingv1alpha1.Reservation{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "r-available"},
			Status: schedulingv1alpha1.ReservationStatus{
				Phase: schedulingv1alpha1.ReservationAvailable,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "r-pending"},
			Status: schedulingv1alpha1.ReservationStatus{
				Phase: schedulingv1alpha1.ReservationPending,
			},
		},
	}
	for _, r := range reservations {
		_, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), r, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{}, &noopResizeLock{}, nil)

	sharedInformerFactory.Start(nil)
	koordSharedInformerFactory.Start(nil)
	sharedInformerFactory.WaitForCacheSync(nil)
	koordSharedInformerFactory.WaitForCacheSync(nil)

	// First resync: both reservations are written.
	controller.resyncReservations()

	expectedAfterFirstResync := `
# HELP scheduler_reservation_status_phase [ALPHA] The current number of reservations in each status phase (e.g. Pending, Available, Succeeded, Failed)
# TYPE scheduler_reservation_status_phase gauge
scheduler_reservation_status_phase{name="r-available",phase="Available"} 1
scheduler_reservation_status_phase{name="r-available",phase="Failed"} 0
scheduler_reservation_status_phase{name="r-available",phase="Pending"} 0
scheduler_reservation_status_phase{name="r-available",phase="Succeeded"} 0
scheduler_reservation_status_phase{name="r-pending",phase="Available"} 0
scheduler_reservation_status_phase{name="r-pending",phase="Failed"} 0
scheduler_reservation_status_phase{name="r-pending",phase="Pending"} 1
scheduler_reservation_status_phase{name="r-pending",phase="Succeeded"} 0
`
	if err := testutil.GatherAndCompare(legacyregistry.DefaultGatherer, strings.NewReader(expectedAfterFirstResync), "scheduler_reservation_status_phase"); err != nil {
		t.Errorf("after first resync: %v", err)
	}

	// Delete r-available so that the second resync should clean up its metrics via Reset.
	err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Delete(context.TODO(), "r-available", metav1.DeleteOptions{})
	assert.NoError(t, err)

	// Wait until the lister no longer sees r-available (informer cache has processed the delete event).
	err = wait.PollUntilContextTimeout(context.TODO(), 5*time.Millisecond, 3*time.Second, true, func(ctx context.Context) (bool, error) {
		list, err := controller.reservationLister.List(labels.Everything())
		if err != nil {
			return false, err
		}
		for _, r := range list {
			if r.Name == "r-available" {
				return false, nil
			}
		}
		return true, nil
	})
	assert.NoError(t, err, "timed out waiting for lister to reflect deletion of r-available")

	// Second resync: Reset clears stale r-available metrics, only r-pending remains.
	controller.resyncReservations()

	expectedAfterSecondResync := `
# HELP scheduler_reservation_status_phase [ALPHA] The current number of reservations in each status phase (e.g. Pending, Available, Succeeded, Failed)
# TYPE scheduler_reservation_status_phase gauge
scheduler_reservation_status_phase{name="r-pending",phase="Available"} 0
scheduler_reservation_status_phase{name="r-pending",phase="Failed"} 0
scheduler_reservation_status_phase{name="r-pending",phase="Pending"} 1
scheduler_reservation_status_phase{name="r-pending",phase="Succeeded"} 0
`
	if err := testutil.GatherAndCompare(legacyregistry.DefaultGatherer, strings.NewReader(expectedAfterSecondResync), "scheduler_reservation_status_phase"); err != nil {
		t.Errorf("after second resync (deleted r-available): %v", err)
	}
}

// ==================== Reservation Resize Tests ====================

// newTestReservation creates an Available reservation on a node with the given allocatable.
func newTestReservation(name string, uid types.UID, nodeName string, specCPU, specMem, statusCPU, statusMem string) *schedulingv1alpha1.Reservation {
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:               uid,
			Name:              name,
			CreationTimestamp: metav1.Now(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(specCPU),
									corev1.ResourceMemory: resource.MustParse(specMem),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: nodeName,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(statusCPU),
				corev1.ResourceMemory: resource.MustParse(statusMem),
			},
		},
	}
	return r
}

func newTestNode(name string, cpu, mem string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
			},
		},
	}
}

func newTestPod(name, ns, nodeName string, cpu, mem string, reservationName string, reservationUID types.UID) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(name + "-uid"),
			Name:      name,
			Namespace: ns,
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(cpu),
							corev1.ResourceMemory: resource.MustParse(mem),
						},
					},
				},
			},
		},
	}
	if reservationName != "" {
		r := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: reservationName, UID: reservationUID},
		}
		apiext.SetReservationAllocated(pod, r)
	}
	return pod
}

func setupResizeTestController(
	t *testing.T,
	nodes []*corev1.Node,
	reservations []*schedulingv1alpha1.Reservation,
	pods []*corev1.Pod,
) (*Controller, *kubefake.Clientset, *koordfake.Clientset) {
	return setupResizeTestControllerWithLock(t, nodes, reservations, pods, &noopResizeLock{})
}

func setupResizeTestControllerWithLock(
	t *testing.T,
	nodes []*corev1.Node,
	reservations []*schedulingv1alpha1.Reservation,
	pods []*corev1.Pod,
	resizeLock ReservationResizeLock,
) (*Controller, *kubefake.Clientset, *koordfake.Clientset) {
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	for _, node := range nodes {
		_, err := fakeClientSet.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
		assert.NoError(t, err)
	}
	for _, r := range reservations {
		_, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), r, metav1.CreateOptions{})
		assert.NoError(t, err)
	}
	for _, pod := range pods {
		_, err := fakeClientSet.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{}, resizeLock, nil)

	sharedInformerFactory.Start(nil)
	koordSharedInformerFactory.Start(nil)
	sharedInformerFactory.WaitForCacheSync(nil)
	koordSharedInformerFactory.WaitForCacheSync(nil)

	// Populate controller internal cache for pods
	for _, pod := range pods {
		controller.onPodAdd(pod)
	}

	return controller, fakeClientSet, fakeKoordClientSet
}

func TestComputeNewAllocatable(t *testing.T) {
	tests := []struct {
		name        string
		reservation *schedulingv1alpha1.Reservation
		want        corev1.ResourceList
	}{
		{
			name: "nil template returns empty ResourceList",
			reservation: &schedulingv1alpha1.Reservation{
				Spec: schedulingv1alpha1.ReservationSpec{Template: nil},
				Status: schedulingv1alpha1.ReservationStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("10"),
					},
				},
			},
			want: corev1.ResourceList{},
		},
		{
			name:        "template with resources returns computed requests",
			reservation: newTestReservation("r1", "uid1", "node1", "8", "8Gi", "10", "10Gi"),
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Controller{}
			got := c.computeNewAllocatable(tt.reservation)
			if len(tt.want) == 0 {
				assert.Empty(t, got, "expected empty ResourceList")
			} else {
				assert.True(t, got.Cpu().Equal(*tt.want.Cpu()), "cpu: got %v, want %v", got.Cpu(), tt.want.Cpu())
				assert.True(t, got.Memory().Equal(*tt.want.Memory()), "mem: got %v, want %v", got.Memory(), tt.want.Memory())
			}
		})
	}
}

func TestIsShrink(t *testing.T) {
	tests := []struct {
		name           string
		oldAllocatable corev1.ResourceList
		newAllocatable corev1.ResourceList
		want           bool
	}{
		{
			name: "pure shrink: all resources decrease",
			oldAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
			newAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			want: true,
		},
		{
			name: "shrink with partial same",
			oldAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
			newAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
			want: true,
		},
		{
			name: "no change is a shrink",
			oldAllocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("10"),
			},
			newAllocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("10"),
			},
			want: true,
		},
		{
			name: "enlarge: CPU increases",
			oldAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
			newAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("12"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
			want: false,
		},
		{
			name: "mixed: CPU shrinks but memory enlarges",
			oldAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
			newAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("24Gi"),
			},
			want: false,
		},
		{
			name: "new resource type added",
			oldAllocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("10"),
			},
			newAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
			want: false,
		},
		{
			name: "resource removed is still shrink",
			oldAllocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("20Gi"),
			},
			newAllocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("10"),
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Controller{}
			got := c.isShrink(tt.oldAllocatable, tt.newAllocatable)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUnbindOwnerPods(t *testing.T) {
	rUID := types.UID("r-uid")
	rName := "test-reservation"

	reservation := newTestReservation(rName, rUID, "node1", "10", "10Gi", "10", "10Gi")
	ownerPod1 := newTestPod("pod1", "default", "node1", "2", "2Gi", rName, rUID)
	ownerPod2 := newTestPod("pod2", "default", "node1", "3", "3Gi", rName, rUID)
	otherPod := newTestPod("other-pod", "default", "node1", "1", "1Gi", "other-r", "other-uid")
	node := newTestNode("node1", "32", "64Gi")

	controller, fakeClientSet, _ := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		[]*corev1.Pod{ownerPod1, ownerPod2, otherPod},
	)

	err := controller.unbindOwnerPods(reservation)
	assert.NoError(t, err)

	// Owner pods should have reservation-allocated annotation removed
	gotPod1, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod1", metav1.GetOptions{})
	assert.Empty(t, gotPod1.Annotations[apiext.AnnotationReservationAllocated], "pod1 should be unbound")

	gotPod2, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod2", metav1.GetOptions{})
	assert.Empty(t, gotPod2.Annotations[apiext.AnnotationReservationAllocated], "pod2 should be unbound")

	// Other pod should be untouched
	gotOther, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "other-pod", metav1.GetOptions{})
	assert.NotEmpty(t, gotOther.Annotations[apiext.AnnotationReservationAllocated], "other-pod should be untouched")
}

// TestRescheduleReservationOnSameNode removed: rescheduleReservationOnSameNode was replaced by
// the fake resize pod pattern (makeResizePod + addToSchedulerQueueFn). See TestMakeResizePod.

func TestHandleReservationResize(t *testing.T) {
	t.Run("shrink: in-place resize", func(t *testing.T) {
		rUID := types.UID("resize-r-uid")
		rName := "resize-reservation"
		nodeName := "node1"

		reservation := newTestReservation(rName, rUID, nodeName, "8", "10Gi", "10", "10Gi")
		node := newTestNode(nodeName, "32", "64Gi")

		controller, _, fakeKoordClientSet := setupResizeTestController(t,
			[]*corev1.Node{node},
			[]*schedulingv1alpha1.Reservation{reservation},
			nil,
		)

		err := controller.handleReservationResize(reservation)
		assert.NoError(t, err)

		gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(
			context.TODO(), rName, metav1.GetOptions{})
		assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
		wantCPU := resource.MustParse("8")
		gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
		assert.True(t, gotCPU.Equal(wantCPU), "allocatable CPU: got %v, want 8", gotCPU.String())
	})

	t.Run("enlarge: fake resize pod queued, reservation stays Available", func(t *testing.T) {
		rUID := types.UID("resize-r-uid")
		rName := "resize-reservation"
		nodeName := "node1"

		reservation := newTestReservation(rName, rUID, nodeName, "14", "10Gi", "10", "10Gi")
		node := newTestNode(nodeName, "32", "64Gi")

		controller, _, fakeKoordClientSet := setupResizeTestController(t,
			[]*corev1.Node{node},
			[]*schedulingv1alpha1.Reservation{reservation},
			nil,
		)

		// Capture queued pods
		var queuedPods []*corev1.Pod
		controller.addToSchedulerQueueFn = func(pod *corev1.Pod) {
			queuedPods = append(queuedPods, pod)
		}

		err := controller.handleReservationResize(reservation)
		assert.NoError(t, err)

		// Reservation stays Available — no phase change, no API call for enlarge
		gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(
			context.TODO(), rName, metav1.GetOptions{})
		assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
		assert.Equal(t, nodeName, gotR.Status.NodeName, "node should be unchanged")

		// Allocatable unchanged (Bind will update it on success)
		wantCPU := resource.MustParse("10")
		gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
		assert.True(t, gotCPU.Equal(wantCPU), "allocatable should be unchanged: got %v", gotCPU.String())

		// Fake resize pod should have been queued
		assert.Len(t, queuedPods, 1, "should queue exactly 1 fake resize pod")
		resizePod := queuedPods[0]
		assert.Equal(t, "true", resizePod.Annotations[reservationutil.AnnotationReservationResizePod])
		assert.Equal(t, string(rUID), resizePod.Annotations[reservationutil.AnnotationResizeTargetReservationUID])
		assert.Equal(t, nodeName, resizePod.Annotations[reservationutil.AnnotationReservationNode])
		// Fake pod should request the NEW (enlarged) resources
		resizePodCPU := resizePod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
		wantEnlargedCPU := resource.MustParse("14")
		assert.True(t, resizePodCPU.Equal(wantEnlargedCPU),
			"resize pod CPU should be 14: got %v", resizePodCPU.String())
	})
}

func TestMakeResizePod(t *testing.T) {
	rUID := types.UID("test-r-uid")
	rName := "test-reservation"
	nodeName := "node1"

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: rName,
			UID:  rUID,
			Labels: map[string]string{
				"app": "test",
			},
			Annotations: map[string]string{
				"custom-anno": "value",
			},
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "main",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10"),
								corev1.ResourceMemory: resource.MustParse("10Gi"),
							},
						},
					}},
					SchedulerName: "koord-scheduler",
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: nodeName,
			Phase:    schedulingv1alpha1.ReservationAvailable,
		},
	}

	newAllocatable := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("14"),
		corev1.ResourceMemory: resource.MustParse("10Gi"),
	}

	c := &Controller{}
	pod := c.makeResizePod(reservation, newAllocatable)

	// UID must be different from reservation UID
	assert.NotEqual(t, rUID, pod.UID, "resize pod should have a different UID")
	assert.NotEmpty(t, pod.UID)

	// Name should contain reservation UID and "-resize-"
	assert.Contains(t, pod.Name, string(rUID))
	assert.Contains(t, pod.Name, "-resize-")

	// Namespace defaults to "default"
	assert.Equal(t, corev1.NamespaceDefault, pod.Namespace)

	// Annotations
	assert.Equal(t, "true", pod.Annotations[reservationutil.AnnotationReservePod])
	assert.Equal(t, rName, pod.Annotations[reservationutil.AnnotationReservationName])
	assert.Equal(t, "true", pod.Annotations[reservationutil.AnnotationReservationResizePod])
	assert.Equal(t, string(rUID), pod.Annotations[reservationutil.AnnotationResizeTargetReservationUID])
	assert.Equal(t, nodeName, pod.Annotations[reservationutil.AnnotationReservationNode])

	// Custom annotations preserved
	assert.Equal(t, "value", pod.Annotations["custom-anno"])

	// Labels preserved
	assert.Equal(t, "test", pod.Labels["app"])

	// Resources should be the NEW allocatable
	podCPU := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
	wantCPU := resource.MustParse("14")
	assert.True(t, podCPU.Equal(wantCPU), "resize pod CPU should be 14: got %v", podCPU.String())

	// NodeName should be empty (scheduled through normal cycle)
	assert.Empty(t, pod.Spec.NodeName, "resize pod should not be pre-assigned to a node")

	// Priority should be set
	assert.NotNil(t, pod.Spec.Priority)

	// Volumes should be cleared
	assert.Nil(t, pod.Spec.Volumes)
}

func TestSyncReservationResize_ResizeTriggered(t *testing.T) {
	t.Run("shrink triggers in-place resize", func(t *testing.T) {
		rUID := types.UID("resize-uid")
		rName := "resize-r"
		nodeName := "node1"

		// spec 8C < status 10C → triggers shrink (in-place)
		reservation := newTestReservation(rName, rUID, nodeName, "8", "10Gi", "10", "10Gi")
		node := newTestNode(nodeName, "32", "64Gi")

		controller, _, fakeKoordClientSet := setupResizeTestController(t,
			[]*corev1.Node{node},
			[]*schedulingv1alpha1.Reservation{reservation},
			nil,
		)

		err := controller.syncReservationResize(reservation)
		assert.NoError(t, err)

		gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(
			context.TODO(), rName, metav1.GetOptions{})

		// Should have been resized in-place to 8C
		wantCPU := resource.MustParse("8")
		gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
		assert.True(t, gotCPU.Equal(wantCPU),
			"allocatable CPU should be updated: got %v, want %v", gotCPU.String(), wantCPU.String())

		// Phase stays Available (in-place, no reschedule)
		assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	})

	t.Run("enlarge triggers fake resize pod", func(t *testing.T) {
		rUID := types.UID("resize-uid")
		rName := "resize-r"
		nodeName := "node1"

		// spec 12C > status 10C → triggers enlarge
		reservation := newTestReservation(rName, rUID, nodeName, "12", "10Gi", "10", "10Gi")
		node := newTestNode(nodeName, "32", "64Gi")

		controller, _, fakeKoordClientSet := setupResizeTestController(t,
			[]*corev1.Node{node},
			[]*schedulingv1alpha1.Reservation{reservation},
			nil,
		)

		var queuedPods []*corev1.Pod
		controller.addToSchedulerQueueFn = func(pod *corev1.Pod) {
			queuedPods = append(queuedPods, pod)
		}

		err := controller.syncReservationResize(reservation)
		assert.NoError(t, err)

		gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(
			context.TODO(), rName, metav1.GetOptions{})

		// Reservation stays Available (no phase transition for enlarge)
		assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
		assert.Equal(t, nodeName, gotR.Status.NodeName)

		// Allocatable unchanged (Bind will update on success)
		wantCPU := resource.MustParse("10")
		gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
		assert.True(t, gotCPU.Equal(wantCPU), "allocatable should be unchanged")

		// Fake resize pod should have been queued
		assert.Len(t, queuedPods, 1, "should queue 1 fake resize pod")
	})
}

func TestSyncAssignedReservation_NoResize(t *testing.T) {
	rUID := types.UID("normal-uid")
	rName := "normal-r"
	nodeName := "node1"

	// spec == status (10C == 10C), no annotation → normal sync
	reservation := newTestReservation(rName, rUID, nodeName, "10", "10Gi", "10", "10Gi")
	reservation.Spec.TTL = &metav1.Duration{Duration: 1 * time.Minute}
	node := newTestNode(nodeName, "32", "64Gi")

	controller, _, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		nil,
	)

	err := controller.syncAssignedReservation(reservation)
	assert.NoError(t, err)

	gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(
		context.TODO(), rName, metav1.GetOptions{})

	// Should remain unchanged — no resize triggered
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	wantCPU := resource.MustParse("10")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(wantCPU))
}

// ==================== Condition Helper Tests ====================

func TestHasResizeFailedCondition(t *testing.T) {
	tests := []struct {
		name       string
		conditions []schedulingv1alpha1.ReservationCondition
		want       bool
	}{
		{
			name:       "no conditions",
			conditions: nil,
			want:       false,
		},
		{
			name: "has ResizeFailed=True",
			conditions: []schedulingv1alpha1.ReservationCondition{
				{
					Type:   schedulingv1alpha1.ReservationConditionResizeFailed,
					Status: schedulingv1alpha1.ConditionStatusTrue,
				},
			},
			want: true,
		},
		{
			name: "has ResizeFailed=False (not active)",
			conditions: []schedulingv1alpha1.ReservationCondition{
				{
					Type:   schedulingv1alpha1.ReservationConditionResizeFailed,
					Status: schedulingv1alpha1.ConditionStatusFalse,
				},
			},
			want: false,
		},
		{
			name: "has other conditions only",
			conditions: []schedulingv1alpha1.ReservationCondition{
				{
					Type:   schedulingv1alpha1.ReservationConditionScheduled,
					Status: schedulingv1alpha1.ConditionStatusTrue,
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &schedulingv1alpha1.Reservation{
				Status: schedulingv1alpha1.ReservationStatus{
					Conditions: tt.conditions,
				},
			}
			assert.Equal(t, tt.want, hasResizeFailedCondition(r))
		})
	}
}

func TestClearResizeFailedCondition(t *testing.T) {
	t.Run("removes ResizeFailed, keeps others", func(t *testing.T) {
		r := &schedulingv1alpha1.Reservation{
			Status: schedulingv1alpha1.ReservationStatus{
				Conditions: []schedulingv1alpha1.ReservationCondition{
					{Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusTrue},
					{Type: schedulingv1alpha1.ReservationConditionResizeFailed, Status: schedulingv1alpha1.ConditionStatusTrue},
					{Type: schedulingv1alpha1.ReservationConditionReady, Status: schedulingv1alpha1.ConditionStatusTrue},
				},
			},
		}
		clearResizeFailedCondition(r)
		assert.Len(t, r.Status.Conditions, 2)
		for _, c := range r.Status.Conditions {
			assert.NotEqual(t, schedulingv1alpha1.ReservationConditionResizeFailed, c.Type)
		}
	})

	t.Run("no-op when no ResizeFailed", func(t *testing.T) {
		r := &schedulingv1alpha1.Reservation{
			Status: schedulingv1alpha1.ReservationStatus{
				Conditions: []schedulingv1alpha1.ReservationCondition{
					{Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusTrue},
				},
			},
		}
		clearResizeFailedCondition(r)
		assert.Len(t, r.Status.Conditions, 1)
	})
}

// TestClearResizingCondition removed: clearResizingCondition and the Resizing condition
// were eliminated in the fake resize pod refactoring. Reservations stay Available throughout resize.

// ==================== finishReservationResize Tests ====================

// TestFinishReservationResize removed: finishReservationResize and the Resizing condition
// were eliminated. Bind directly updates allocatable; no Resizing condition to clean up.

// ==================== resizeReservationInPlace Tests ====================

func TestResizeReservationInPlace(t *testing.T) {
	t.Run("updates allocatable and clears ResizeFailed", func(t *testing.T) {
		rUID := types.UID("inplace-uid")
		rName := "inplace-r"
		nodeName := "node1"

		// Had a previous ResizeFailed condition
		reservation := newTestReservation(rName, rUID, nodeName, "8", "10Gi", "10", "10Gi")
		reservation.Status.Conditions = []schedulingv1alpha1.ReservationCondition{
			{Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusTrue},
			{Type: schedulingv1alpha1.ReservationConditionResizeFailed, Status: schedulingv1alpha1.ConditionStatusTrue},
		}
		node := newTestNode(nodeName, "32", "64Gi")

		controller, _, fakeKoordClientSet := setupResizeTestController(t,
			[]*corev1.Node{node},
			[]*schedulingv1alpha1.Reservation{reservation},
			nil,
		)

		newAlloc := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("8"),
			corev1.ResourceMemory: resource.MustParse("10Gi"),
		}
		err := controller.resizeReservationInPlace(reservation, newAlloc)
		assert.NoError(t, err)

		gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})
		// Allocatable updated
		wantCPU := resource.MustParse("8")
		gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
		assert.True(t, gotCPU.Equal(wantCPU))
		// ResizeFailed condition cleared
		assert.False(t, hasResizeFailedCondition(gotR), "ResizeFailed should be cleared")
		// Phase unchanged
		assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	})
}

// ==================== syncReservationResize Tests ====================

// TestSyncAssignedReservation_ResizingCompleted removed: finishReservationResize and the
// Resizing condition were eliminated. syncAssignedReservation no longer has a "resizing completed"
// branch; resize completion is handled entirely by the Bind plugin hook.

func TestSyncAssignedReservation_ResizeFailedSkip(t *testing.T) {
	// Reservation has ResizeFailed + spec changed → should skip resize
	rUID := types.UID("failed-uid")
	rName := "failed-r"
	nodeName := "node1"

	// spec 14C > status 10C → resources changed, BUT ResizeFailed present with same target → skip
	reservation := newTestReservation(rName, rUID, nodeName, "14", "10Gi", "10", "10Gi")
	failedTarget := allocatableFingerprint(corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("14"),
		corev1.ResourceMemory: resource.MustParse("10Gi"),
	})
	reservation.Status.Conditions = []schedulingv1alpha1.ReservationCondition{
		{Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusTrue},
		{
			Type:    schedulingv1alpha1.ReservationConditionResizeFailed,
			Status:  schedulingv1alpha1.ConditionStatusTrue,
			Reason:  schedulingv1alpha1.ReasonReservationResizeFailed,
			Message: "previous failure;" + failedTarget,
		},
	}
	node := newTestNode(nodeName, "32", "64Gi")

	controller, _, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		nil,
	)

	err := controller.syncReservationResize(reservation)
	assert.NoError(t, err)

	gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})
	// Should remain unchanged — resize skipped due to ResizeFailed
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	wantCPU := resource.MustParse("10")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(wantCPU), "allocatable should be unchanged")
	assert.True(t, hasResizeFailedCondition(gotR), "ResizeFailed should still be present")
}

func TestSyncAssignedReservation_UnassignedSkip(t *testing.T) {
	// Reservation with empty NodeName → syncAssignedReservation should return nil immediately
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{UID: "uid", Name: "r"},
		Status:     schedulingv1alpha1.ReservationStatus{NodeName: ""},
	}
	controller := &Controller{}
	err := controller.syncAssignedReservation(r)
	assert.NoError(t, err)
}

// TestSyncReservationResize_ClearStaleResizeFailed tests that when the user reverts
// the spec back to match allocatable, the stale ResizeFailed condition is automatically cleared.
// This is handled by the fork in sync() before syncAssignedReservation.
func TestSyncReservationResize_ClearStaleResizeFailed(t *testing.T) {
	rUID := types.UID("stale-failed-uid")
	rName := "stale-failed-r"
	nodeName := "node1"

	// spec=10C == allocatable=10C (user reverted), but ResizeFailed is still present
	reservation := newTestReservation(rName, rUID, nodeName, "10", "10Gi", "10", "10Gi")
	reservation.Status.Conditions = []schedulingv1alpha1.ReservationCondition{
		{Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusTrue},
		{Type: schedulingv1alpha1.ReservationConditionReady, Status: schedulingv1alpha1.ConditionStatusTrue},
		{
			Type:   schedulingv1alpha1.ReservationConditionResizeFailed,
			Status: schedulingv1alpha1.ConditionStatusTrue,
			Reason: schedulingv1alpha1.ReasonReservationResizeFailed,
		},
	}
	node := newTestNode(nodeName, "32", "64Gi")

	controller, _, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		nil,
	)

	_, err := controller.sync(getReservationKey(reservation))
	assert.NoError(t, err)

	gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})
	// ResizeFailed should be cleared
	assert.False(t, hasResizeFailedCondition(gotR), "ResizeFailed should be cleared after spec reverted")
	// Other conditions preserved
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	assert.Equal(t, nodeName, gotR.Status.NodeName)
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

			controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, tt.args, &noopResizeLock{}, nil)

			assert.Equal(t, tt.expectedNumWorker, controller.numWorker)
			assert.Equal(t, tt.expectedIsGCDisabled, controller.isGCDisabled)
			assert.Equal(t, tt.expectedGCDuration, controller.gcDuration)
			assert.Equal(t, tt.expectedGCInterval, controller.gcInterval)
		})
	}
}

// TestControllerStart verifies that controller.Start() registers a pod event handler
// via ForceSyncFromInformer and that the handler receives OnAdd events after informers start.
func TestControllerStart(t *testing.T) {
	// Reset registrations before the test to avoid cross-test pollution.
	frameworkexthelper.ResetRegistrations()
	defer frameworkexthelper.ResetRegistrations()

	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	// Pre-create a pod assigned to a reservation so the OnAdd handler will store it in
	// the controller's internal cache.
	testReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  "res-uid-start-test",
			Name: "test-reservation-start",
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationAvailable,
		},
	}
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "pod-uid-start-test",
			Name:      "test-pod-start",
			Namespace: "default",
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name":"test-reservation-start","uid":"res-uid-start-test"}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
	}
	_, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), testReservation, metav1.CreateOptions{})
	assert.NoError(t, err)
	_, err = fakeClientSet.CoreV1().Pods(testPod.Namespace).Create(context.TODO(), testPod, metav1.CreateOptions{})
	assert.NoError(t, err)

	// Create controller with GC and resync disabled to keep the test focused.
	controller := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet,
		&config.ReservationArgs{DisableGarbageCollection: true, ResyncIntervalSeconds: 0}, &noopResizeLock{}, nil)

	// Start() calls ForceSyncFromInformer for the pod informer, which should add a registration.
	controller.Start()

	// After Start(), at least one registration must have been collected (the pod handler).
	regs := frameworkexthelper.GetRegistrations()
	assert.NotEmpty(t, regs, "expected at least one handler registration after controller.Start()")

	// Wait for all registered handlers to finish their initial list sync so that
	// OnAdd events have been delivered before we inspect controller state.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = frameworkexthelper.WaitForHandlersSync(ctx)
	assert.NoError(t, err, "handlers did not sync within timeout")

	// The pod handler's OnAdd must have stored the pod in the controller's internal cache.
	podsOnNode := controller.getPodsOnNode("test-node")
	assert.Len(t, podsOnNode, 1, "expected test pod to be recorded in controller cache after OnAdd")

	podsOnReservation := controller.getPodsOnReservation("res-uid-start-test")
	assert.Len(t, podsOnReservation, 1, "expected test pod to be indexed by reservation UID")
}

// ==================== End-to-End Resize Scenario Tests ====================

// TestReservationResizeScenario_Shrink tests a complete shrink scenario:
// An Available reservation with 2 owner pods, user changes spec from 10C→8C.
// Expected: allocatable updated in-place, phase stays Available, owner pods NOT unbound.
func TestReservationResizeScenario_Shrink(t *testing.T) {
	rUID := types.UID("scenario-shrink-uid")
	rName := "scenario-shrink-r"
	nodeName := "node1"

	// Available reservation: spec=10C/10Gi, status.allocatable=10C/10Gi
	reservation := newTestReservation(rName, rUID, nodeName, "10", "10Gi", "10", "10Gi")
	reservation.Spec.TTL = &metav1.Duration{Duration: 10 * time.Minute}
	node := newTestNode(nodeName, "32", "64Gi")

	// 2 owner pods allocated on this reservation
	ownerPod1 := newTestPod("pod1", "default", nodeName, "2", "2Gi", rName, rUID)
	ownerPod2 := newTestPod("pod2", "default", nodeName, "3", "3Gi", rName, rUID)

	controller, fakeClientSet, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		[]*corev1.Pod{ownerPod1, ownerPod2},
	)

	// --- User changes spec from 10C → 8C (shrink) ---
	reservation = reservation.DeepCopy()
	reservation.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("8")

	err := controller.syncReservationResize(reservation)
	assert.NoError(t, err)

	// Verify API state
	gotR, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})
	assert.NoError(t, err)

	// Phase stays Available (no reschedule for shrink)
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	assert.Equal(t, nodeName, gotR.Status.NodeName, "node should be unchanged")

	// Allocatable updated to new spec value
	wantCPU := resource.MustParse("8")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(wantCPU), "allocatable CPU: got %v, want 8", gotCPU.String())
	wantMem := resource.MustParse("10Gi")
	gotMem := gotR.Status.Allocatable[corev1.ResourceMemory]
	assert.True(t, gotMem.Equal(wantMem), "allocatable Memory unchanged")

	// Owner pods are NOT unbound — their reservation-allocated annotation is untouched
	gotPod1, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod1", metav1.GetOptions{})
	assert.NotEmpty(t, gotPod1.Annotations[apiext.AnnotationReservationAllocated],
		"pod1 should still be bound to reservation")
	gotPod2, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod2", metav1.GetOptions{})
	assert.NotEmpty(t, gotPod2.Annotations[apiext.AnnotationReservationAllocated],
		"pod2 should still be bound to reservation")
}

// TestReservationResizeScenario_Enlarge tests a complete enlarge scenario:
// An Available reservation with 2 owner pods, user changes spec from 10C→14C.
// Expected: phase stays Available, fake resize pod queued, allocatable unchanged,
// owner pods NOT unbound.
func TestReservationResizeScenario_Enlarge(t *testing.T) {
	rUID := types.UID("scenario-enlarge-uid")
	rName := "scenario-enlarge-r"
	nodeName := "node1"

	// Available reservation: spec=10C/10Gi, status.allocatable=10C/10Gi
	reservation := newTestReservation(rName, rUID, nodeName, "10", "10Gi", "10", "10Gi")
	reservation.Spec.TTL = &metav1.Duration{Duration: 10 * time.Minute}
	node := newTestNode(nodeName, "32", "64Gi")

	// 2 owner pods allocated on this reservation
	ownerPod1 := newTestPod("pod1", "default", nodeName, "2", "2Gi", rName, rUID)
	ownerPod2 := newTestPod("pod2", "default", nodeName, "3", "3Gi", rName, rUID)

	controller, fakeClientSet, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		[]*corev1.Pod{ownerPod1, ownerPod2},
	)

	// Capture queued pods
	var queuedPods []*corev1.Pod
	controller.addToSchedulerQueueFn = func(pod *corev1.Pod) {
		queuedPods = append(queuedPods, pod)
	}

	// --- User changes spec from 10C → 14C (enlarge) ---
	reservation = reservation.DeepCopy()
	reservation.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("14")

	err := controller.syncReservationResize(reservation)
	assert.NoError(t, err)

	// Verify API state
	gotR, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})
	assert.NoError(t, err)

	// Phase stays Available (no phase change for enlarge)
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	assert.Equal(t, nodeName, gotR.Status.NodeName, "node should be unchanged")

	// Allocatable unchanged (Bind will update on success)
	oldCPU := resource.MustParse("10")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(oldCPU), "allocatable CPU should be unchanged: got %v, want 10", gotCPU.String())

	// Fake resize pod should have been queued
	assert.Len(t, queuedPods, 1, "should queue exactly 1 fake resize pod")
	resizePod := queuedPods[0]
	assert.Equal(t, "true", resizePod.Annotations[reservationutil.AnnotationReservationResizePod])
	assert.Equal(t, string(rUID), resizePod.Annotations[reservationutil.AnnotationResizeTargetReservationUID])
	assert.Equal(t, nodeName, resizePod.Annotations[reservationutil.AnnotationReservationNode])

	// Owner pods are NOT unbound — their reservation-allocated annotation is untouched
	gotPod1, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod1", metav1.GetOptions{})
	assert.NotEmpty(t, gotPod1.Annotations[apiext.AnnotationReservationAllocated],
		"pod1 should still be bound to reservation")
	gotPod2, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod2", metav1.GetOptions{})
	assert.NotEmpty(t, gotPod2.Annotations[apiext.AnnotationReservationAllocated],
		"pod2 should still be bound to reservation")
}

// TestReservationResizeScenario_MixedResizeBelowAllocated tests that a mixed resize
// (some resources enlarge, others shrink below allocated) is rejected.
// Reservation has allocatable=10C/10Gi, allocated=8C/8Gi.
// User changes spec to 14C/4Gi: CPU enlarges (14>10) but mem shrinks below allocated (4<8).
// Expected: ResizeFailed set, allocatable unchanged, phase stays Available.
func TestReservationResizeScenario_MixedResizeBelowAllocated(t *testing.T) {
	rUID := types.UID("scenario-mixed-reject-uid")
	rName := "scenario-mixed-reject-r"
	nodeName := "node1"

	reservation := newTestReservation(rName, rUID, nodeName, "10", "10Gi", "10", "10Gi")
	reservation.Spec.TTL = &metav1.Duration{Duration: 10 * time.Minute}
	reservation.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("8"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	node := newTestNode(nodeName, "32", "64Gi")

	ownerPod1 := newTestPod("pod1", "default", nodeName, "4", "4Gi", rName, rUID)
	ownerPod2 := newTestPod("pod2", "default", nodeName, "4", "4Gi", rName, rUID)

	controller, fakeClientSet, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		[]*corev1.Pod{ownerPod1, ownerPod2},
	)

	// --- User changes spec from 10C/10Gi -> 14C/4Gi (CPU up, memory down below allocated 8Gi) ---
	reservation = reservation.DeepCopy()
	reservation.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("14")
	reservation.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("4Gi")

	err := controller.syncReservationResize(reservation)
	assert.NoError(t, err)

	gotR, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})
	assert.NoError(t, err)

	// Phase stays Available
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	assert.Equal(t, nodeName, gotR.Status.NodeName)

	// Allocatable NOT updated -- still 10C/10Gi (mixed resize rejected)
	wantCPU := resource.MustParse("10")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(wantCPU), "allocatable CPU should be unchanged: got %v, want 10", gotCPU.String())
	wantMem := resource.MustParse("10Gi")
	gotMem := gotR.Status.Allocatable[corev1.ResourceMemory]
	assert.True(t, gotMem.Equal(wantMem), "allocatable Memory should be unchanged: got %v, want 10Gi", gotMem.String())

	// ResizeFailed condition is set
	assert.True(t, hasResizeFailedCondition(gotR), "should have ResizeFailed condition")

	// Owner pods are NOT unbound
	gotPod1, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod1", metav1.GetOptions{})
	assert.NotEmpty(t, gotPod1.Annotations[apiext.AnnotationReservationAllocated],
		"pod1 should still be bound to reservation")
	gotPod2, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod2", metav1.GetOptions{})
	assert.NotEmpty(t, gotPod2.Annotations[apiext.AnnotationReservationAllocated],
		"pod2 should still be bound to reservation")
}

// TestReservationResizeScenario_ShrinkBelowAllocated tests that shrinking below
// the current allocated amount is rejected with a ResizeFailed condition.
// Reservation has allocatable=10C, allocated=8C (from owner pods), user shrinks spec to 6C.
// Expected: ResizeFailed set, allocatable unchanged (still 10C), phase stays Available.
func TestReservationResizeScenario_ShrinkBelowAllocated(t *testing.T) {
	rUID := types.UID("scenario-shrink-reject-uid")
	rName := "scenario-shrink-reject-r"
	nodeName := "node1"

	// Available reservation: spec=10C/10Gi, allocatable=10C/10Gi, allocated=8C/8Gi
	reservation := newTestReservation(rName, rUID, nodeName, "10", "10Gi", "10", "10Gi")
	reservation.Spec.TTL = &metav1.Duration{Duration: 10 * time.Minute}
	reservation.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("8"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	node := newTestNode(nodeName, "32", "64Gi")

	// 2 owner pods: 4C+4C = 8C allocated total
	ownerPod1 := newTestPod("pod1", "default", nodeName, "4", "4Gi", rName, rUID)
	ownerPod2 := newTestPod("pod2", "default", nodeName, "4", "4Gi", rName, rUID)

	controller, fakeClientSet, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		[]*corev1.Pod{ownerPod1, ownerPod2},
	)

	// --- User changes spec from 10C → 6C (shrink below allocated 8C) ---
	reservation = reservation.DeepCopy()
	reservation.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("6")

	err := controller.syncReservationResize(reservation)
	assert.NoError(t, err)

	gotR, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})
	assert.NoError(t, err)

	// Phase stays Available — not rescheduled, not terminated
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	assert.Equal(t, nodeName, gotR.Status.NodeName, "node should be unchanged")

	// Allocatable NOT updated — still 10C (shrink rejected)
	wantCPU := resource.MustParse("10")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(wantCPU), "allocatable CPU should be unchanged: got %v, want 10", gotCPU.String())

	// ResizeFailed condition is set
	assert.True(t, hasResizeFailedCondition(gotR), "should have ResizeFailed condition")

	// Owner pods are NOT unbound
	gotPod1, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod1", metav1.GetOptions{})
	assert.NotEmpty(t, gotPod1.Annotations[apiext.AnnotationReservationAllocated],
		"pod1 should still be bound to reservation")
	gotPod2, _ := fakeClientSet.CoreV1().Pods("default").Get(context.TODO(), "pod2", metav1.GetOptions{})
	assert.NotEmpty(t, gotPod2.Annotations[apiext.AnnotationReservationAllocated],
		"pod2 should still be bound to reservation")
}

// TestReservationResizeScenario_ShrinkBelowAllocatedThenRetryWithNewSpec tests that
// after a shrink is rejected (ResizeFailed set for target 6C), changing the spec to a
// different valid target (8C >= allocated 8C) automatically clears ResizeFailed and retries.
func TestReservationResizeScenario_ShrinkBelowAllocatedThenRetryWithNewSpec(t *testing.T) {
	rUID := types.UID("scenario-retry-uid")
	rName := "scenario-retry-r"
	nodeName := "node1"

	// State after a previous failed shrink to 6C:
	// spec=6C, allocatable=10C, allocated=8C, ResizeFailed present with target=6C
	reservation := newTestReservation(rName, rUID, nodeName, "6", "10Gi", "10", "10Gi")
	reservation.Spec.TTL = &metav1.Duration{Duration: 10 * time.Minute}
	reservation.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("8"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	// ResizeFailed with target fingerprint for 6C/10Gi
	failedTarget := allocatableFingerprint(corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("6"),
		corev1.ResourceMemory: resource.MustParse("10Gi"),
	})
	reservation.Status.Conditions = []schedulingv1alpha1.ReservationCondition{
		{Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusTrue},
		{Type: schedulingv1alpha1.ReservationConditionReady, Status: schedulingv1alpha1.ConditionStatusTrue},
		{
			Type:    schedulingv1alpha1.ReservationConditionResizeFailed,
			Status:  schedulingv1alpha1.ConditionStatusTrue,
			Reason:  schedulingv1alpha1.ReasonReservationResizeFailed,
			Message: "new allocatable is less than current allocated resources;" + failedTarget,
		},
	}
	node := newTestNode(nodeName, "32", "64Gi")

	ownerPod1 := newTestPod("pod1", "default", nodeName, "4", "4Gi", rName, rUID)
	ownerPod2 := newTestPod("pod2", "default", nodeName, "4", "4Gi", rName, rUID)

	controller, _, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		[]*corev1.Pod{ownerPod1, ownerPod2},
	)

	// --- User changes spec from 6C → 8C (valid: 8C >= allocated 8C) ---
	reservation = reservation.DeepCopy()
	reservation.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("8")

	err := controller.syncReservationResize(reservation)
	assert.NoError(t, err)

	gotR, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})
	assert.NoError(t, err)

	// Shrink succeeded — allocatable updated to 8C
	wantCPU := resource.MustParse("8")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(wantCPU), "allocatable CPU should be 8: got %v", gotCPU.String())

	// ResizeFailed cleared after successful retry
	assert.False(t, hasResizeFailedCondition(gotR), "ResizeFailed should be cleared after successful retry")

	// Phase stays Available (shrink is in-place)
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	assert.Equal(t, nodeName, gotR.Status.NodeName)
}

// TestReservationResizeScenario_ShrinkExactlyToAllocated tests shrinking to exactly
// the current allocated amount — this should succeed (allocatable == allocated is valid).
func TestReservationResizeScenario_ShrinkExactlyToAllocated(t *testing.T) {
	rUID := types.UID("scenario-shrink-exact-uid")
	rName := "scenario-shrink-exact-r"
	nodeName := "node1"

	// Available reservation: spec=10C/10Gi, allocatable=10C/10Gi, allocated=8C/8Gi
	reservation := newTestReservation(rName, rUID, nodeName, "10", "10Gi", "10", "10Gi")
	reservation.Spec.TTL = &metav1.Duration{Duration: 10 * time.Minute}
	reservation.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("8"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	node := newTestNode(nodeName, "32", "64Gi")

	controller, _, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		nil,
	)

	// --- User changes spec from 10C → 8C (exactly matches allocated) ---
	reservation = reservation.DeepCopy()
	reservation.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("8")

	err := controller.syncReservationResize(reservation)
	assert.NoError(t, err)

	gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})

	// Should succeed — allocatable updated to 8C
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	wantCPU := resource.MustParse("8")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(wantCPU), "allocatable CPU: got %v, want 8", gotCPU.String())

	// No ResizeFailed — shrink to exactly allocated is allowed
	assert.False(t, hasResizeFailedCondition(gotR), "should NOT have ResizeFailed")
}

// TestReservationResizeScenario_EnlargeWithPreviousResizeFailed tests that
// a reservation with ResizeFailed condition skips resize even if spec changed.
func TestReservationResizeScenario_EnlargeWithPreviousResizeFailed(t *testing.T) {
	rUID := types.UID("scenario-failed-uid")
	rName := "scenario-failed-r"
	nodeName := "node1"

	// spec=14C (user wants 14C), allocatable=10C (still old), ResizeFailed present with same target
	reservation := newTestReservation(rName, rUID, nodeName, "14", "10Gi", "10", "10Gi")
	// Build the target fingerprint that matches computeNewAllocatable for spec=14C/10Gi
	failedTarget := allocatableFingerprint(corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("14"),
		corev1.ResourceMemory: resource.MustParse("10Gi"),
	})
	reservation.Status.Conditions = []schedulingv1alpha1.ReservationCondition{
		{Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusTrue},
		{Type: schedulingv1alpha1.ReservationConditionReady, Status: schedulingv1alpha1.ConditionStatusTrue},
		{
			Type:    schedulingv1alpha1.ReservationConditionResizeFailed,
			Status:  schedulingv1alpha1.ConditionStatusTrue,
			Reason:  schedulingv1alpha1.ReasonReservationResizeFailed,
			Message: "previous failure;" + failedTarget,
		},
	}
	node := newTestNode(nodeName, "32", "64Gi")

	controller, _, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		nil,
	)

	// Capture queued pods to verify resize is skipped
	var queuedPods []*corev1.Pod
	controller.addToSchedulerQueueFn = func(pod *corev1.Pod) {
		queuedPods = append(queuedPods, pod)
	}

	err := controller.syncReservationResize(reservation)
	assert.NoError(t, err)

	gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})
	// Should remain Available, not reset to Pending (skip resize)
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)
	assert.Equal(t, nodeName, gotR.Status.NodeName, "should stay on same node")
	// Allocatable unchanged
	wantCPU := resource.MustParse("10")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(wantCPU), "allocatable should be unchanged")
	// ResizeFailed still present
	assert.True(t, hasResizeFailedCondition(gotR), "ResizeFailed should still be present")
	// No fake resize pod should have been queued (resize skipped)
	assert.Empty(t, queuedPods, "should NOT queue any resize pod when ResizeFailed is present")
}

// ==================== ResizeFailed Fingerprint Utility Tests ====================

func TestAllocatableFingerprint(t *testing.T) {
	t.Run("single resource", func(t *testing.T) {
		rl := corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("4"),
		}
		fp := allocatableFingerprint(rl)
		assert.Equal(t, "cpu=4", fp)
	})

	t.Run("multiple resources sorted", func(t *testing.T) {
		rl := corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("8Gi"),
			corev1.ResourceCPU:    resource.MustParse("4"),
		}
		fp := allocatableFingerprint(rl)
		// Must be sorted alphabetically: cpu before memory
		assert.Equal(t, "cpu=4,memory=8Gi", fp)
	})

	t.Run("deterministic across calls", func(t *testing.T) {
		rl := corev1.ResourceList{
			corev1.ResourceMemory:           resource.MustParse("16Gi"),
			corev1.ResourceCPU:              resource.MustParse("8"),
			corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
		}
		fp1 := allocatableFingerprint(rl)
		fp2 := allocatableFingerprint(rl)
		assert.Equal(t, fp1, fp2, "fingerprint must be deterministic")
	})

	t.Run("empty resource list", func(t *testing.T) {
		fp := allocatableFingerprint(corev1.ResourceList{})
		assert.Equal(t, "", fp)
	})
}

func TestEncodeResizeFailedMessage(t *testing.T) {
	target := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("6"),
		corev1.ResourceMemory: resource.MustParse("10Gi"),
	}
	msg := EncodeResizeFailedMessage("shrink below allocated", target)
	assert.Contains(t, msg, "shrink below allocated;")
	assert.Contains(t, msg, "cpu=6")
	assert.Contains(t, msg, "memory=10Gi")

	// Verify format: "reason;fingerprint"
	parts := strings.SplitN(msg, ";", 2)
	assert.Len(t, parts, 2)
	assert.Equal(t, "shrink below allocated", parts[0])
	assert.Equal(t, allocatableFingerprint(target), parts[1])
}

func TestGetResizeFailedTarget(t *testing.T) {
	t.Run("extracts target from Message", func(t *testing.T) {
		r := &schedulingv1alpha1.Reservation{
			Status: schedulingv1alpha1.ReservationStatus{
				Conditions: []schedulingv1alpha1.ReservationCondition{
					{
						Type:    schedulingv1alpha1.ReservationConditionResizeFailed,
						Status:  schedulingv1alpha1.ConditionStatusTrue,
						Message: "some reason;cpu=6,memory=10Gi",
					},
				},
			},
		}
		got := getResizeFailedTarget(r)
		assert.Equal(t, "cpu=6,memory=10Gi", got)
	})

	t.Run("no ResizeFailed condition", func(t *testing.T) {
		r := &schedulingv1alpha1.Reservation{
			Status: schedulingv1alpha1.ReservationStatus{
				Conditions: []schedulingv1alpha1.ReservationCondition{
					{Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusTrue},
				},
			},
		}
		got := getResizeFailedTarget(r)
		assert.Equal(t, "", got)
	})

	t.Run("Message without semicolon (legacy format)", func(t *testing.T) {
		r := &schedulingv1alpha1.Reservation{
			Status: schedulingv1alpha1.ReservationStatus{
				Conditions: []schedulingv1alpha1.ReservationCondition{
					{
						Type:    schedulingv1alpha1.ReservationConditionResizeFailed,
						Status:  schedulingv1alpha1.ConditionStatusTrue,
						Message: "old message without target",
					},
				},
			},
		}
		got := getResizeFailedTarget(r)
		assert.Equal(t, "", got)
	})

	t.Run("roundtrip with encodeResizeFailedMessage", func(t *testing.T) {
		target := corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("8"),
			corev1.ResourceMemory: resource.MustParse("16Gi"),
		}
		r := &schedulingv1alpha1.Reservation{}
		setResizeFailedCondition(r, "test reason", target)

		got := getResizeFailedTarget(r)
		want := allocatableFingerprint(target)
		assert.Equal(t, want, got, "roundtrip: encode then extract should match fingerprint")
	})
}

// ==================== ResizeFailed Condition Lifecycle Scenario Tests ====================

// TestReservationResizeScenario_ManualDeleteConditionRetriggers tests that manually
// deleting the ResizeFailed condition causes the controller to re-evaluate and re-reject
// (for the same invalid shrink) — proving the system is safe against condition deletion.
func TestReservationResizeScenario_ManualDeleteConditionRetriggers(t *testing.T) {
	rUID := types.UID("scenario-manual-delete-uid")
	rName := "scenario-manual-delete-r"
	nodeName := "node1"

	// spec=6C, allocatable=10C, allocated=8C — same invalid shrink, but NO ResizeFailed
	// (user manually deleted it)
	reservation := newTestReservation(rName, rUID, nodeName, "6", "10Gi", "10", "10Gi")
	reservation.Spec.TTL = &metav1.Duration{Duration: 10 * time.Minute}
	reservation.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("8"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	// No ResizeFailed condition — simulates manual deletion
	reservation.Status.Conditions = []schedulingv1alpha1.ReservationCondition{
		{Type: schedulingv1alpha1.ReservationConditionScheduled, Status: schedulingv1alpha1.ConditionStatusTrue},
		{Type: schedulingv1alpha1.ReservationConditionReady, Status: schedulingv1alpha1.ConditionStatusTrue},
	}
	node := newTestNode(nodeName, "32", "64Gi")

	ownerPod1 := newTestPod("pod1", "default", nodeName, "4", "4Gi", rName, rUID)
	ownerPod2 := newTestPod("pod2", "default", nodeName, "4", "4Gi", rName, rUID)

	controller, _, fakeKoordClientSet := setupResizeTestController(t,
		[]*corev1.Node{node},
		[]*schedulingv1alpha1.Reservation{reservation},
		[]*corev1.Pod{ownerPod1, ownerPod2},
	)

	err := controller.syncReservationResize(reservation)
	assert.NoError(t, err)

	gotR, _ := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), rName, metav1.GetOptions{})

	// Re-evaluated and re-rejected: ResizeFailed set again
	assert.True(t, hasResizeFailedCondition(gotR), "ResizeFailed should be re-set after manual deletion")

	// Allocatable unchanged
	wantCPU := resource.MustParse("10")
	gotCPU := gotR.Status.Allocatable[corev1.ResourceCPU]
	assert.True(t, gotCPU.Equal(wantCPU), "allocatable should remain 10C")

	// Phase stays Available
	assert.Equal(t, schedulingv1alpha1.ReservationAvailable, gotR.Status.Phase)

	// Verify the new ResizeFailed has the correct target fingerprint
	target := getResizeFailedTarget(gotR)
	expectedTarget := allocatableFingerprint(corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("6"),
		corev1.ResourceMemory: resource.MustParse("10Gi"),
	})
	assert.Equal(t, expectedTarget, target, "ResizeFailed should record the failed target fingerprint")
}

// TestHandleReservationResize_LockRollbackOnAPIFailure verifies that when the API call
// fails after locking, the lock is rolled back so the reservation remains matchable.
func TestHandleReservationResize_LockRollbackOnAPIFailure(t *testing.T) {
	tests := []struct {
		name      string
		specCPU   string // spec template CPU → determines shrink/enlarge vs status
		statusCPU string // current status.allocatable CPU
		allocCPU  string // current status.allocated CPU (for isBelowAllocated)
		nodeCPU   string
		failVerb  string // which verb to fail: "update" (for UpdateStatus) or "update" on reservations
	}{
		{
			name:      "shrink rejected (isBelowAllocated) - UpdateStatus fails, lock rolled back",
			specCPU:   "6",
			statusCPU: "10",
			allocCPU:  "8", // 6 < 8 → isBelowAllocated → setResizeFailedCondition → UpdateStatus
			nodeCPU:   "32",
			failVerb:  "update",
		},
		{
			name:      "shrink in-place - UpdateStatus fails, lock rolled back",
			specCPU:   "8",
			statusCPU: "10",
			allocCPU:  "5", // 8 > 5 → not below allocated → resizeReservationInPlace → UpdateStatus
			nodeCPU:   "32",
			failVerb:  "update",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rUID := types.UID("lock-test-uid")
			rName := "lock-test-reservation"
			nodeName := "node1"

			reservation := newTestReservation(rName, rUID, nodeName, tt.specCPU, "10Gi", tt.statusCPU, "10Gi")
			if tt.allocCPU != "" {
				reservation.Status.Allocated = corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(tt.allocCPU),
					corev1.ResourceMemory: resource.MustParse("5Gi"),
				}
			}
			node := newTestNode(nodeName, tt.nodeCPU, "64Gi")

			lock := newTrackingResizeLock()
			controller, _, fakeKoordClientSet := setupResizeTestControllerWithLock(t,
				[]*corev1.Node{node},
				[]*schedulingv1alpha1.Reservation{reservation},
				nil,
				lock,
			)

			// Inject API failure for UpdateStatus on reservations
			injectedErr := fmt.Errorf("injected API error")
			fakeKoordClientSet.PrependReactor(tt.failVerb, "reservations", func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, injectedErr
			})

			err := controller.handleReservationResize(reservation)
			assert.Error(t, err, "handleReservationResize should return error on API failure")

			// Lock must be rolled back (unlocked)
			assert.False(t, lock.isLocked(rUID),
				"lock should be rolled back after API failure")
			assert.Equal(t, 1, len(lock.lockLog), "should have locked once")
			assert.Equal(t, 1, len(lock.unlockLog), "should have unlocked once (rollback)")
		})
	}
}

// TestHandleReservationResize_LockCommittedOnSuccess verifies that when the API call
// succeeds, the lock is NOT rolled back (committed=true, defer is a no-op).
func TestHandleReservationResize_LockCommittedOnSuccess(t *testing.T) {
	tests := []struct {
		name      string
		specCPU   string
		statusCPU string
		allocCPU  string
		nodeCPU   string
	}{
		{
			name:      "shrink rejected (isBelowAllocated) - UpdateStatus succeeds, lock committed",
			specCPU:   "6",
			statusCPU: "10",
			allocCPU:  "8",
			nodeCPU:   "32",
		},
		{
			name:      "shrink in-place - UpdateStatus succeeds, lock committed",
			specCPU:   "8",
			statusCPU: "10",
			allocCPU:  "5",
			nodeCPU:   "32",
		},
		{
			name:      "enlarge - lock committed, fake pod queued",
			specCPU:   "14",
			statusCPU: "10",
			allocCPU:  "5",
			nodeCPU:   "32",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rUID := types.UID("commit-test-uid")
			rName := "commit-test-reservation"
			nodeName := "node1"

			reservation := newTestReservation(rName, rUID, nodeName, tt.specCPU, "10Gi", tt.statusCPU, "10Gi")
			if tt.allocCPU != "" {
				reservation.Status.Allocated = corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(tt.allocCPU),
					corev1.ResourceMemory: resource.MustParse("5Gi"),
				}
			}
			node := newTestNode(nodeName, tt.nodeCPU, "64Gi")

			lock := newTrackingResizeLock()
			controller, _, _ := setupResizeTestControllerWithLock(t,
				[]*corev1.Node{node},
				[]*schedulingv1alpha1.Reservation{reservation},
				nil,
				lock,
			)

			// Capture queued pods for enlarge verification
			var queuedPods []*corev1.Pod
			controller.addToSchedulerQueueFn = func(pod *corev1.Pod) {
				queuedPods = append(queuedPods, pod)
			}

			err := controller.handleReservationResize(reservation)
			assert.NoError(t, err, "handleReservationResize should succeed")

			// Lock should NOT be rolled back — committed=true
			assert.True(t, lock.isLocked(rUID),
				"lock should remain held after successful API call (event handler will unlock)")
			assert.Equal(t, 1, len(lock.lockLog), "should have locked once")
			assert.Equal(t, 0, len(lock.unlockLog), "should not have unlocked (committed)")

			// For enlarge, a fake resize pod should have been queued (no API UpdateStatus)
			if tt.specCPU == "14" {
				assert.Len(t, queuedPods, 1, "enlarge should queue a fake resize pod")
			} else {
				assert.Empty(t, queuedPods, "shrink should not queue any pod")
			}
		})
	}
}
