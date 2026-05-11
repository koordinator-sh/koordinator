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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func newTestController(t *testing.T) *Controller {
	t.Helper()
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)
	return New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{})
}

func podWithReservation(uid, name, ns, nodeName, rName, rUID string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(uid),
			Name:      name,
			Namespace: ns,
			Annotations: map[string]string{
				apiext.AnnotationReservationAllocated: `{"name":"` + rName + `","uid":"` + rUID + `"}`,
			},
		},
		Spec: corev1.PodSpec{NodeName: nodeName},
	}
}

func podNoReservation(uid, name, ns, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(uid),
			Name:      name,
			Namespace: ns,
		},
		Spec: corev1.PodSpec{NodeName: nodeName},
	}
}

// TestOnPodAdd_NilInput verifies that a nil pod does not panic or mutate state.
func TestOnPodAdd_NilInput(t *testing.T) {
	c := newTestController(t)
	c.onPodAdd(nil)
	assert.Empty(t, c.pods)
}

// TestOnPodAdd_NilCast verifies that a non-pod object does not panic.
func TestOnPodAdd_NilCast(t *testing.T) {
	c := newTestController(t)
	c.onPodAdd("not-a-pod")
	assert.Empty(t, c.pods)
}

// TestOnPodUpdate_NilNewObj verifies that a nil newObj does not panic.
func TestOnPodUpdate_NilNewObj(t *testing.T) {
	c := newTestController(t)
	old := podWithReservation("uid1", "pod1", "ns", "node1", "r1", "ruid1")
	c.onPodAdd(old)
	c.onPodUpdate(old, nil)
	// state must be unchanged
	assert.Equal(t, 1, len(c.getPodsOnNode("node1")))
}

// TestOnPodUpdate_NilOldObj verifies that a nil oldObj does not panic.
func TestOnPodUpdate_NilOldObj(t *testing.T) {
	c := newTestController(t)
	newPod := podWithReservation("uid1", "pod1", "ns", "node1", "r1", "ruid1")
	c.onPodUpdate(nil, newPod)
	assert.Empty(t, c.pods)
}

// TestOnPodDelete_Tombstone verifies that cache.DeletedFinalStateUnknown is handled correctly.
func TestOnPodDelete_Tombstone(t *testing.T) {
	c := newTestController(t)
	pod := podWithReservation("uid-ts", "pod-ts", "ns", "node1", "r-ts", "ruid-ts")
	c.onPodAdd(pod)
	assert.Equal(t, 1, len(c.getPodsOnNode("node1")))
	assert.Equal(t, 1, len(c.getPodsOnReservation("ruid-ts")))

	tombstone := cache.DeletedFinalStateUnknown{Key: "ns/pod-ts", Obj: pod}
	c.onPodDelete(tombstone)

	assert.Equal(t, 0, len(c.getPodsOnNode("node1")))
	assert.Equal(t, 0, len(c.getPodsOnReservation("ruid-ts")))
}

// TestOnPodDelete_TombstoneNilInner verifies that a tombstone wrapping nil does not panic.
func TestOnPodDelete_TombstoneNilInner(t *testing.T) {
	c := newTestController(t)
	tombstone := cache.DeletedFinalStateUnknown{Key: "ns/pod", Obj: "not-a-pod"}
	c.onPodDelete(tombstone)
	assert.Empty(t, c.pods)
}

// TestOnPodDelete_NilInput verifies that a nil direct delete does not panic.
func TestOnPodDelete_NilInput(t *testing.T) {
	c := newTestController(t)
	c.onPodDelete(nil)
	assert.Empty(t, c.pods)
}

// TestEnqueueIfPodBoundReservation_NoNode verifies no enqueue when pod has no node.
func TestEnqueueIfPodBoundReservation_NoNode(t *testing.T) {
	c := newTestController(t)
	pod := podWithReservation("uid1", "pod1", "ns", "", "r1", "ruid1")
	result := c.enqueueIfPodBoundReservation(pod)
	assert.Nil(t, result)
	assert.Equal(t, 0, c.queue.Len())
}

// TestEnqueueIfPodBoundReservation_NoAnnotation verifies no enqueue when annotation is missing.
func TestEnqueueIfPodBoundReservation_NoAnnotation(t *testing.T) {
	c := newTestController(t)
	pod := podNoReservation("uid1", "pod1", "ns", "node1")
	result := c.enqueueIfPodBoundReservation(pod)
	assert.Nil(t, result)
	assert.Equal(t, 0, c.queue.Len())
}

// TestEnqueueIfPodBoundReservation_Valid verifies queue receives the correct key.
func TestEnqueueIfPodBoundReservation_Valid(t *testing.T) {
	c := newTestController(t)
	pod := podWithReservation("uid1", "pod1", "ns", "node1", "my-reservation", "res-uid-123")
	result := c.enqueueIfPodBoundReservation(pod)
	assert.NotNil(t, result)
	assert.Equal(t, 1, c.queue.Len())

	item, _ := c.queue.Get()
	assert.Equal(t, "my-reservation/res-uid-123", item)
}

// TestUpdatePod_OldReservationCleaned verifies that changing a pod's reservation
// removes the old rToPod entry and writes a new one atomically.
func TestUpdatePod_OldReservationCleaned(t *testing.T) {
	c := newTestController(t)

	pod := podWithReservation("pod-uid", "pod1", "ns", "node1", "r1", "ruid1")
	c.onPodAdd(pod)
	assert.Equal(t, 1, len(c.getPodsOnReservation("ruid1")))
	assert.Equal(t, 0, len(c.getPodsOnReservation("ruid2")))

	// same pod now bound to a different reservation
	updatedPod := podWithReservation("pod-uid", "pod1", "ns", "node1", "r2", "ruid2")
	c.onPodUpdate(pod, updatedPod)

	assert.Equal(t, 0, len(c.getPodsOnReservation("ruid1")), "old reservation must be cleaned")
	assert.Equal(t, 1, len(c.getPodsOnReservation("ruid2")), "new reservation must be populated")
}

// TestGetPodsOnNode_ReturnsDeepCopy verifies that mutating the returned map does not
// corrupt the controller's internal state.
func TestGetPodsOnNode_ReturnsDeepCopy(t *testing.T) {
	c := newTestController(t)
	pod := podNoReservation("uid1", "pod1", "ns", "node1")
	c.onPodAdd(pod)

	snapshot := c.getPodsOnNode("node1")
	assert.Equal(t, 1, len(snapshot))

	// mutate the snapshot
	delete(snapshot, "uid1")

	// internal state must be unchanged
	assert.Equal(t, 1, len(c.getPodsOnNode("node1")))
}

// TestGetPodsOnNode_Empty verifies nil is returned for a node with no pods.
func TestGetPodsOnNode_Empty(t *testing.T) {
	c := newTestController(t)
	assert.Nil(t, c.getPodsOnNode("unknown-node"))
}

// TestGetPodsOnReservation_ReturnsDeepCopy verifies that mutating the returned map does
// not corrupt the controller's internal state.
func TestGetPodsOnReservation_ReturnsDeepCopy(t *testing.T) {
	c := newTestController(t)
	pod := podWithReservation("uid1", "pod1", "ns", "node1", "r1", "ruid1")
	c.onPodAdd(pod)

	snapshot := c.getPodsOnReservation("ruid1")
	assert.Equal(t, 1, len(snapshot))

	// mutate the snapshot
	delete(snapshot, "uid1")

	// internal state must be unchanged
	assert.Equal(t, 1, len(c.getPodsOnReservation("ruid1")))
}

// TestGetPodsOnReservation_Empty verifies nil is returned for an unknown reservation.
func TestGetPodsOnReservation_Empty(t *testing.T) {
	c := newTestController(t)
	assert.Nil(t, c.getPodsOnReservation("unknown-ruid"))
}

// TestDeletePod_NoNode verifies deletePod is a no-op when pod has no node.
func TestDeletePod_NoNode(t *testing.T) {
	c := newTestController(t)
	pod := podNoReservation("uid1", "pod1", "ns", "")
	c.deletePod(pod)
	assert.Empty(t, c.pods)
}

// TestDeletePod_CleansAllMaps verifies that deleting a pod removes it from pods,
// podToR, and rToPod consistently.
func TestDeletePod_CleansAllMaps(t *testing.T) {
	c := newTestController(t)
	pod := podWithReservation("uid1", "pod1", "ns", "node1", "r1", "ruid1")
	c.onPodAdd(pod)

	c.deletePod(pod)

	assert.Equal(t, 0, len(c.getPodsOnNode("node1")))
	assert.Equal(t, 0, len(c.getPodsOnReservation("ruid1")))
	c.lock.RLock()
	_, hasPodToR := c.podToR["uid1"]
	c.lock.RUnlock()
	assert.False(t, hasPodToR, "podToR entry must be removed")
}

// TestMultiplePodsOnNode verifies that multiple pods on the same node are tracked correctly
// and that removing one does not affect the others.
func TestMultiplePodsOnNode(t *testing.T) {
	c := newTestController(t)
	pod1 := podWithReservation("uid1", "pod1", "ns", "node1", "r1", "ruid1")
	pod2 := podWithReservation("uid2", "pod2", "ns", "node1", "r2", "ruid2")

	c.onPodAdd(pod1)
	c.onPodAdd(pod2)
	assert.Equal(t, 2, len(c.getPodsOnNode("node1")))

	c.onPodDelete(pod1)
	assert.Equal(t, 1, len(c.getPodsOnNode("node1")))
	assert.Equal(t, 0, len(c.getPodsOnReservation("ruid1")))
	assert.Equal(t, 1, len(c.getPodsOnReservation("ruid2")))
}
