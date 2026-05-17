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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func newReservationEventHandlerController(t *testing.T) *Controller {
	t.Helper()
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)
	return New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{})
}

func makeReservation(name string, uid types.UID, generation int64, phase schedulingv1alpha1.ReservationPhase, nodeName string) *schedulingv1alpha1.Reservation {
	return &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			UID:        uid,
			Generation: generation,
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    phase,
			NodeName: nodeName,
		},
	}
}

// --- onReservationAdd ---

func TestOnReservationAdd_Nil(t *testing.T) {
	c := newReservationEventHandlerController(t)
	c.onReservationAdd(nil)
	assert.Equal(t, 0, c.queue.Len())
}

func TestOnReservationAdd_NonReservationObject(t *testing.T) {
	c := newReservationEventHandlerController(t)
	c.onReservationAdd("not-a-reservation")
	assert.Equal(t, 0, c.queue.Len())
}

func TestOnReservationAdd_Valid(t *testing.T) {
	c := newReservationEventHandlerController(t)
	t.Cleanup(c.queue.ShutDown)
	r := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "node1")
	c.onReservationAdd(r)
	assert.Equal(t, 1, c.queue.Len())
	item, _ := c.queue.Get()
	defer c.queue.Done(item)
	assert.Equal(t, "r1/uid-1", item)
}

// --- onReservationUpdate ---

func TestOnReservationUpdate_NilOld(t *testing.T) {
	c := newReservationEventHandlerController(t)
	newR := makeReservation("r1", "uid-1", 2, schedulingv1alpha1.ReservationAvailable, "node1")
	c.onReservationUpdate(nil, newR)
	assert.Equal(t, 0, c.queue.Len())
}

func TestOnReservationUpdate_NilNew(t *testing.T) {
	c := newReservationEventHandlerController(t)
	oldR := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "node1")
	c.onReservationUpdate(oldR, nil)
	assert.Equal(t, 0, c.queue.Len())
}

func TestOnReservationUpdate_NilBoth(t *testing.T) {
	c := newReservationEventHandlerController(t)
	c.onReservationUpdate(nil, nil)
	assert.Equal(t, 0, c.queue.Len())
}

func TestOnReservationUpdate_NonReservationObjects(t *testing.T) {
	c := newReservationEventHandlerController(t)
	c.onReservationUpdate("old", "new")
	assert.Equal(t, 0, c.queue.Len())
}

func TestOnReservationUpdate_GenerationChanged(t *testing.T) {
	c := newReservationEventHandlerController(t)
	t.Cleanup(c.queue.ShutDown)
	oldR := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "node1")
	newR := makeReservation("r1", "uid-1", 2, schedulingv1alpha1.ReservationAvailable, "node1")
	c.onReservationUpdate(oldR, newR)
	assert.Equal(t, 1, c.queue.Len())
	item, _ := c.queue.Get()
	defer c.queue.Done(item)
	assert.Equal(t, "r1/uid-1", item)
}

func TestOnReservationUpdate_PhaseChanged(t *testing.T) {
	c := newReservationEventHandlerController(t)
	oldR := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationPending, "")
	newR := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "")
	c.onReservationUpdate(oldR, newR)
	assert.Equal(t, 1, c.queue.Len())
}

func TestOnReservationUpdate_NodeNameChanged(t *testing.T) {
	c := newReservationEventHandlerController(t)
	oldR := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "")
	newR := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "node1")
	c.onReservationUpdate(oldR, newR)
	assert.Equal(t, 1, c.queue.Len())
}

func TestOnReservationUpdate_NoRelevantChange(t *testing.T) {
	c := newReservationEventHandlerController(t)
	oldR := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "node1")
	newR := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "node1")
	// only labels changed — should NOT enqueue
	newR.Labels = map[string]string{"foo": "bar"}
	c.onReservationUpdate(oldR, newR)
	assert.Equal(t, 0, c.queue.Len())
}

// --- onReservationDelete ---

func TestOnReservationDelete_Direct(t *testing.T) {
	c := newReservationEventHandlerController(t)
	t.Cleanup(c.queue.ShutDown)
	r := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "node1")
	c.onReservationDelete(r)
	assert.Equal(t, 1, c.queue.Len())
	item, _ := c.queue.Get()
	defer c.queue.Done(item)
	assert.Equal(t, "r1/uid-1", item)
}

func TestOnReservationDelete_Tombstone(t *testing.T) {
	c := newReservationEventHandlerController(t)
	t.Cleanup(c.queue.ShutDown)
	r := makeReservation("r1", "uid-1", 1, schedulingv1alpha1.ReservationAvailable, "node1")
	tombstone := cache.DeletedFinalStateUnknown{Key: "r1", Obj: r}
	c.onReservationDelete(tombstone)
	assert.Equal(t, 1, c.queue.Len())
	item, _ := c.queue.Get()
	defer c.queue.Done(item)
	assert.Equal(t, "r1/uid-1", item)
}

func TestOnReservationDelete_TombstoneNilInner(t *testing.T) {
	c := newReservationEventHandlerController(t)
	tombstone := cache.DeletedFinalStateUnknown{Key: "r1", Obj: "not-a-reservation"}
	c.onReservationDelete(tombstone)
	assert.Equal(t, 0, c.queue.Len())
}

func TestOnReservationDelete_NilInput(t *testing.T) {
	c := newReservationEventHandlerController(t)
	c.onReservationDelete(nil)
	assert.Equal(t, 0, c.queue.Len())
}

// --- getReservationKey ---

func TestGetReservationKey(t *testing.T) {
	r := makeReservation("my-reservation", "abc-123", 1, schedulingv1alpha1.ReservationAvailable, "node1")
	assert.Equal(t, "my-reservation/abc-123", getReservationKey(r))
}

// --- getReservationKeyByAllocated ---

func TestGetReservationKeyByAllocated(t *testing.T) {
	allocated := &apiext.ReservationAllocated{
		Name: "my-reservation",
		UID:  "uid-xyz",
	}
	assert.Equal(t, "my-reservation/uid-xyz", getReservationKeyByAllocated(allocated))
}

// --- parseReservationKey ---

func TestParseReservationKey_Valid(t *testing.T) {
	name, uid, err := parseReservationKey("my-reservation/uid-123")
	assert.NoError(t, err)
	assert.Equal(t, "my-reservation", name)
	assert.Equal(t, types.UID("uid-123"), uid)
}

func TestParseReservationKey_NoSlash(t *testing.T) {
	_, _, err := parseReservationKey("invalid-key")
	assert.Error(t, err)
}

func TestParseReservationKey_TooManyParts(t *testing.T) {
	_, _, err := parseReservationKey("a/b/c")
	assert.Error(t, err)
}

func TestParseReservationKey_Empty(t *testing.T) {
	_, _, err := parseReservationKey("")
	assert.Error(t, err)
}
