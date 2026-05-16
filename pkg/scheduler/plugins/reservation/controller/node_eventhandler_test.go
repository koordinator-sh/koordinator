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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/indexer"
)

// newNodeEventHandlerController creates a controller with the reservation indexer registered
// and informers synced, with the given reservations pre-populated.
func newNodeEventHandlerController(t *testing.T, reservations ...*schedulingv1alpha1.Reservation) *Controller {
	t.Helper()
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()

	for _, r := range reservations {
		_, err := fakeKoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), r, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	// register the nodeName indexer so ByIndex works in onNodeDelete
	err := indexer.AddIndexers(koordSharedInformerFactory)
	assert.NoError(t, err)

	c := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{})

	stopCh := make(chan struct{})
	t.Cleanup(func() { close(stopCh) })

	sharedInformerFactory.Start(stopCh)
	koordSharedInformerFactory.Start(stopCh)
	sharedInformerFactory.WaitForCacheSync(stopCh)
	koordSharedInformerFactory.WaitForCacheSync(stopCh)

	return c
}

func makeNode(name string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func makeReservationOnNode(name string, uid types.UID, nodeName string) *schedulingv1alpha1.Reservation {
	return &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid},
		Status:     schedulingv1alpha1.ReservationStatus{NodeName: nodeName},
	}
}

// TestOnNodeDelete_Nil verifies that a nil input does not panic and nothing is enqueued.
func TestOnNodeDelete_Nil(t *testing.T) {
	c := newNodeEventHandlerController(t)
	c.onNodeDelete(nil)
	assert.Equal(t, 0, c.queue.Len())
}

// TestOnNodeDelete_NonNodeObject verifies that a non-node object does not panic.
func TestOnNodeDelete_NonNodeObject(t *testing.T) {
	c := newNodeEventHandlerController(t)
	c.onNodeDelete("not-a-node")
	assert.Equal(t, 0, c.queue.Len())
}

// TestOnNodeDelete_Direct verifies that deleting a node enqueues all reservations on that node.
func TestOnNodeDelete_Direct(t *testing.T) {
	r1 := makeReservationOnNode("r1", "uid-1", "node1")
	r2 := makeReservationOnNode("r2", "uid-2", "node1")
	r3 := makeReservationOnNode("r3", "uid-3", "node2") // different node, must not be enqueued

	c := newNodeEventHandlerController(t, r1, r2, r3)

	c.onNodeDelete(makeNode("node1"))

	assert.Equal(t, 2, c.queue.Len())

	enqueued := map[string]bool{}
	for i := 0; i < 2; i++ {
		item, _ := c.queue.Get()
		enqueued[item.(string)] = true
		c.queue.Done(item)
	}
	assert.True(t, enqueued["r1/uid-1"])
	assert.True(t, enqueued["r2/uid-2"])
}

// TestOnNodeDelete_Tombstone verifies that cache.DeletedFinalStateUnknown is unwrapped correctly.
func TestOnNodeDelete_Tombstone(t *testing.T) {
	r1 := makeReservationOnNode("r1", "uid-1", "node1")
	c := newNodeEventHandlerController(t, r1)

	tombstone := cache.DeletedFinalStateUnknown{Key: "node1", Obj: makeNode("node1")}
	c.onNodeDelete(tombstone)

	assert.Equal(t, 1, c.queue.Len())
	item, _ := c.queue.Get()
	assert.Equal(t, "r1/uid-1", item)
}

// TestOnNodeDelete_TombstoneNilInner verifies that a tombstone wrapping a non-node does not panic.
func TestOnNodeDelete_TombstoneNilInner(t *testing.T) {
	c := newNodeEventHandlerController(t)
	tombstone := cache.DeletedFinalStateUnknown{Key: "node1", Obj: "not-a-node"}
	c.onNodeDelete(tombstone)
	assert.Equal(t, 0, c.queue.Len())
}

// TestOnNodeDelete_NoReservationsOnNode verifies that nothing is enqueued when the node
// has no associated reservations.
func TestOnNodeDelete_NoReservationsOnNode(t *testing.T) {
	r1 := makeReservationOnNode("r1", "uid-1", "node2")
	c := newNodeEventHandlerController(t, r1)

	c.onNodeDelete(makeNode("node1"))
	assert.Equal(t, 0, c.queue.Len())
}

// TestOnNodeDelete_IndexerNotRegistered verifies that when the indexer is not registered,
// the function logs the error and returns without enqueuing anything.
func TestOnNodeDelete_IndexerNotRegistered(t *testing.T) {
	fakeClientSet := kubefake.NewSimpleClientset()
	fakeKoordClientSet := koordfake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(fakeKoordClientSet, 0)

	// intentionally skip indexer.AddIndexers so ByIndex returns an error
	c := New(sharedInformerFactory, koordSharedInformerFactory, fakeClientSet, fakeKoordClientSet, &config.ReservationArgs{})

	stopCh := make(chan struct{})
	t.Cleanup(func() { close(stopCh) })
	sharedInformerFactory.Start(stopCh)
	koordSharedInformerFactory.Start(stopCh)
	sharedInformerFactory.WaitForCacheSync(stopCh)
	koordSharedInformerFactory.WaitForCacheSync(stopCh)

	c.onNodeDelete(makeNode("node1"))
	assert.Equal(t, 0, c.queue.Len())
}

// TestOnNodeDelete_MultipleReservationsOnSameNode verifies all reservations on the deleted
// node are enqueued and reservations on other nodes are not affected.
func TestOnNodeDelete_MultipleReservationsOnSameNode(t *testing.T) {
	reservations := []*schedulingv1alpha1.Reservation{
		makeReservationOnNode("r1", "uid-1", "node1"),
		makeReservationOnNode("r2", "uid-2", "node1"),
		makeReservationOnNode("r3", "uid-3", "node1"),
		makeReservationOnNode("r4", "uid-4", "node2"),
	}
	c := newNodeEventHandlerController(t, reservations...)

	c.onNodeDelete(makeNode("node1"))

	assert.Equal(t, 3, c.queue.Len())

	enqueued := map[string]bool{}
	for i := 0; i < 3; i++ {
		item, _ := c.queue.Get()
		enqueued[item.(string)] = true
		c.queue.Done(item)
	}
	assert.True(t, enqueued["r1/uid-1"])
	assert.True(t, enqueued["r2/uid-2"])
	assert.True(t, enqueued["r3/uid-3"])
	assert.False(t, enqueued["r4/uid-4"])
}
