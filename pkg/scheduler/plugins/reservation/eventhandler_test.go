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
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	clientcache "k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestEventHandlerOnAdd(t *testing.T) {
	activeReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4000m"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4000m"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	pendingReservation := activeReservation.DeepCopy()
	pendingReservation.Status.Phase = schedulingv1alpha1.ReservationPending
	pendingReservation.Status.NodeName = ""

	failedReservation := activeReservation.DeepCopy()
	failedReservation.Status.Phase = schedulingv1alpha1.ReservationFailed

	succeededReservation := activeReservation.DeepCopy()
	succeededReservation.Status.Phase = schedulingv1alpha1.ReservationSucceeded

	tests := []struct {
		name            string
		reservation     *schedulingv1alpha1.Reservation
		wantReservation *schedulingv1alpha1.Reservation
	}{
		{
			name:            "active reservation",
			reservation:     activeReservation,
			wantReservation: activeReservation,
		},
		{
			name:            "pending reservation",
			reservation:     pendingReservation,
			wantReservation: nil,
		},
		{
			name:            "failed reservation",
			reservation:     failedReservation,
			wantReservation: nil,
		},
		{
			name:            "succeeded reservation",
			reservation:     succeededReservation,
			wantReservation: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newReservationCache(nil)
			eh := &reservationEventHandler{cache: cache}
			eh.OnAdd(tt.reservation, true)
			if tt.wantReservation == nil {
				rInfo := cache.getReservationInfoByUID(tt.reservation.UID)
				assert.Nil(t, rInfo)
			} else {
				rInfo := cache.getReservationInfoByUID(tt.wantReservation.UID)
				assert.Equal(t, tt.wantReservation, rInfo.Reservation)
			}
		})
	}
}

func TestEventHandlerUpdate(t *testing.T) {
	activeReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4000m"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4000m"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	pendingReservation := activeReservation.DeepCopy()
	pendingReservation.Status.Phase = schedulingv1alpha1.ReservationPending
	pendingReservation.Status.NodeName = ""

	failedReservation := activeReservation.DeepCopy()
	failedReservation.Status.Phase = schedulingv1alpha1.ReservationFailed

	succeededReservation := activeReservation.DeepCopy()
	succeededReservation.Status.Phase = schedulingv1alpha1.ReservationSucceeded

	tests := []struct {
		name            string
		oldReservation  *schedulingv1alpha1.Reservation
		newReservation  *schedulingv1alpha1.Reservation
		wantReservation *schedulingv1alpha1.Reservation
	}{
		{
			name:            "pending to active",
			oldReservation:  pendingReservation,
			newReservation:  activeReservation,
			wantReservation: activeReservation,
		},
		{
			name:            "active to failed",
			oldReservation:  activeReservation,
			newReservation:  failedReservation,
			wantReservation: failedReservation,
		},
		{
			name:            "active to succeeded",
			oldReservation:  activeReservation,
			newReservation:  succeededReservation,
			wantReservation: succeededReservation,
		},
		{
			name:            "pending to failed",
			oldReservation:  pendingReservation,
			newReservation:  failedReservation,
			wantReservation: nil,
		},
		{
			name:            "pending to succeeded",
			oldReservation:  pendingReservation,
			newReservation:  succeededReservation,
			wantReservation: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newReservationCache(nil)
			eh := &reservationEventHandler{cache: cache, rrNominator: newNominator(nil, nil)}
			eh.OnAdd(tt.oldReservation, false)

			eh.OnUpdate(tt.oldReservation, tt.newReservation)
			if tt.wantReservation == nil {
				rInfo := cache.getReservationInfoByUID(tt.newReservation.UID)
				assert.Nil(t, rInfo)
			} else {
				rInfo := cache.getReservationInfoByUID(tt.wantReservation.UID)
				assert.NotNil(t, rInfo)
				assert.Equal(t, tt.wantReservation, rInfo.Reservation)
			}
		})
	}
}

func TestEventHandlerDelete(t *testing.T) {
	activeReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4000m"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node-1",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4000m"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}
	cache := newReservationCache(nil)
	eh := &reservationEventHandler{cache: cache, rrNominator: newNominator(nil, nil)}
	eh.OnAdd(activeReservation, true)
	rInfo := cache.getReservationInfoByUID(activeReservation.UID)
	assert.NotNil(t, rInfo)
	reservePodInfo, _ := framework.NewPodInfo(reservation.NewReservePod(activeReservation))
	eh.rrNominator.AddNominatedReservePod(reservePodInfo, "test-node")
	reservePodInfo, _ = framework.NewPodInfo(reservation.NewReservePod(activeReservation))
	assert.Equal(t, []*framework.PodInfo{reservePodInfo}, eh.rrNominator.NominatedReservePodForNode("test-node"))
	eh.OnDelete(activeReservation)
	rInfo = cache.getReservationInfoByUID(activeReservation.UID)
	assert.NotNil(t, rInfo)
	assert.False(t, rInfo.IsAvailable())
	assert.Equal(t, []*framework.PodInfo{}, eh.rrNominator.NominatedReservePodForNode("test-node"))
}

func TestEventHandlerUpdatePendingDeletesNomination(t *testing.T) {
	pendingReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:        uuid.NewUUID(),
			Name:       "test-pending-reservation",
			Generation: 1,
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("8000m"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase: schedulingv1alpha1.ReservationPending,
		},
	}
	eh := &reservationEventHandler{cache: newReservationCache(nil), rrNominator: newNominator(nil, nil)}
	reservePodInfo, _ := framework.NewPodInfo(reservation.NewReservePod(pendingReservation))
	eh.rrNominator.AddNominatedReservePod(reservePodInfo, "test-node")

	// A status-only write keeps the nomination untouched.
	statusTouched := pendingReservation.DeepCopy()
	eh.OnUpdate(pendingReservation, statusTouched)
	assert.Equal(t, 1, len(eh.rrNominator.NominatedReservePodForNode("test-node")))

	// A scheduling-relevant update (spec resize, placement constraints, or a
	// schedulerName handover - the framework handler never cleans this
	// nominator on a handover) must delete the nomination instead of
	// refreshing it in place; the reserve pod re-nominates on its next cycle
	// if the reservation still belongs to this scheduler.
	updatedReservation := pendingReservation.DeepCopy()
	updatedReservation.Generation = 2
	updatedReservation.Spec.Template.Spec.SchedulerName = "other-scheduler"
	eh.OnUpdate(pendingReservation, updatedReservation)
	assert.Equal(t, 0, len(eh.rrNominator.NominatedReservePodForNode("test-node")),
		"scheduling-relevant update must remove the nomination, or a schedulerName handover leaks a ghost nomination")
}

// newPendingReservationForNomination builds a still-unscheduled reservation
// with an explicit resourceVersion, matching what an informer store holds.
func newPendingReservationForNomination(name string, uid types.UID, rv string, cpu string) *schedulingv1alpha1.Reservation {
	return &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid, ResourceVersion: rv, Generation: 1},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)},
						},
					}},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationPending},
	}
}

func newNominatorWithReservations(t *testing.T, rs ...*schedulingv1alpha1.Reservation) (*nominator, clientcache.Indexer) {
	indexer := clientcache.NewIndexer(clientcache.MetaNamespaceKeyFunc, clientcache.Indexers{})
	for _, r := range rs {
		assert.NoError(t, indexer.Add(r))
	}
	return newNominator(nil, listerschedulingv1alpha1.NewReservationLister(indexer)), indexer
}

// TestNominatedReservePodForNodeRevalidates verifies the ordering invariant the
// QueueingHints rely on: the informer store (lister) is updated before any
// event handler runs, so a read must already reflect what that store says even
// when the listener maintaining this nominator has not processed the update.
func TestNominatedReservePodForNodeRevalidates(t *testing.T) {
	t.Run("a spec change drops the nomination", func(t *testing.T) {
		stored := newPendingReservationForNomination("r-spec", "uid-spec", "1", "8000m")
		nm, indexer := newNominatorWithReservations(t, stored)
		pi, err := framework.NewPodInfo(reservation.NewReservePod(stored))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")
		assert.Equal(t, 1, len(nm.NominatedReservePodForNode("test-node")))

		// The CRD has the status subresource, so a spec edit bumps the
		// generation. The reserve pod's placement constraints, requests,
		// priority or even schedulerName may have moved, so the nominated node
		// need not still be a valid choice for it - which is also why
		// reservationEventHandler.OnUpdate deletes the nomination for this.
		resized := newPendingReservationForNomination("r-spec", "uid-spec", "2", "2000m")
		resized.Generation = 2
		assert.NoError(t, indexer.Update(resized))
		assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
			"a reserve pod whose shape changed must not stay accounted for on the old node")
	})

	t.Run("a status-only write keeps the nomination", func(t *testing.T) {
		stored := newPendingReservationForNomination("r-status", "uid-status", "1", "8000m")
		nm, indexer := newNominatorWithReservations(t, stored)
		pi, err := framework.NewPodInfo(reservation.NewReservePod(stored))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")

		// This scheduler records an unschedulable condition on the reservation
		// after every failed attempt, so status-only bumps are the common case
		// and must not cost the reserve pod its nominated node.
		touched := stored.DeepCopy()
		touched.ResourceVersion = "2"
		touched.Status.Conditions = []schedulingv1alpha1.ReservationCondition{{
			Type:   schedulingv1alpha1.ReservationConditionScheduled,
			Status: schedulingv1alpha1.ConditionStatusFalse,
			Reason: schedulingv1alpha1.ReasonReservationUnschedulable,
		}}
		assert.NoError(t, indexer.Update(touched))

		nominated := nm.NominatedReservePodForNode("test-node")
		assert.Equal(t, 1, len(nominated))
		assert.Equal(t, pi.Pod.UID, nominated[0].Pod.UID)
	})
}

// TestNominatedReservePodForNodeDropsInvalidated covers the nominations that
// must disappear as soon as the store says so. Keeping them would let a
// phantom reserve pod occupy the node during exactly the window in which a
// requeued waiter re-evaluates, and no further event would wake that waiter.
func TestNominatedReservePodForNodeDropsInvalidated(t *testing.T) {
	t.Run("reservation deleted from the store", func(t *testing.T) {
		r := newPendingReservationForNomination("r-gone", "uid-gone", "1", "8000m")
		nm, indexer := newNominatorWithReservations(t, r)
		pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")

		assert.NoError(t, indexer.Delete(r))
		assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
			"a deleted reservation must not keep occupying its nominated node")
	})

	t.Run("reservation replaced by a same-named object", func(t *testing.T) {
		old := newPendingReservationForNomination("r-replaced", "uid-old", "1", "8000m")
		nm, indexer := newNominatorWithReservations(t, old)
		pi, err := framework.NewPodInfo(reservation.NewReservePod(old))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")

		replaced := newPendingReservationForNomination("r-replaced", "uid-new", "2", "8000m")
		assert.NoError(t, indexer.Update(replaced))
		assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
			"the replaced reservation's nomination must not survive as a phantom")
	})

	t.Run("reservation became active", func(t *testing.T) {
		r := newPendingReservationForNomination("r-active", "uid-active", "1", "8000m")
		nm, indexer := newNominatorWithReservations(t, r)
		pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")

		active := r.DeepCopy()
		active.ResourceVersion = "2"
		assert.NoError(t, reservation.SetReservationAvailable(active, "test-node"))
		assert.NoError(t, indexer.Update(active))
		assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
			"an assumed reserve pod is already accounted for by the scheduler cache, so the nomination must not double count it")
	})

	t.Run("reservation terminated", func(t *testing.T) {
		r := newPendingReservationForNomination("r-failed", "uid-failed", "1", "8000m")
		nm, indexer := newNominatorWithReservations(t, r)
		pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")

		failed := r.DeepCopy()
		failed.ResourceVersion = "2"
		failed.Status.Phase = schedulingv1alpha1.ReservationFailed
		assert.NoError(t, indexer.Update(failed))
		assert.Empty(t, nm.NominatedReservePodForNode("test-node"))
	})

	t.Run("nil lister keeps the stored snapshot", func(t *testing.T) {
		r := newPendingReservationForNomination("r-nolister", "uid-nolister", "1", "8000m")
		nm := newNominator(nil, nil)
		pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")
		assert.Equal(t, 1, len(nm.NominatedReservePodForNode("test-node")))
	})
}

// TestAddNominatedReservePodRejectsReplaced covers the other half of a
// same-name replacement: a scheduling cycle still holding the previous reserve
// pod must not be able to resurrect its nomination after cleanup.
func TestAddNominatedReservePodRejectsReplaced(t *testing.T) {
	old := newPendingReservationForNomination("r-race", "uid-old", "1", "8000m")
	replaced := newPendingReservationForNomination("r-race", "uid-new", "2", "8000m")
	nm, _ := newNominatorWithReservations(t, replaced)

	stalePodInfo, err := framework.NewPodInfo(reservation.NewReservePod(old))
	assert.NoError(t, err)
	nm.AddNominatedReservePod(stalePodInfo, "test-node")
	assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
		"a stale reserve pod must not be nominated once its reservation was replaced")
}

// TestEventHandlerUpdateReplacedUIDDeletesOldNomination covers the informer
// coalescing a delete and a same-named create into a single update: the
// nominator keys reserve pods by reservation UID, so cleaning up only the new
// UID would leave the replaced reservation occupying its old node forever.
func TestEventHandlerUpdateReplacedUIDDeletesOldNomination(t *testing.T) {
	old := newPendingReservationForNomination("r-coalesced", "uid-old", "1", "8000m")
	eh := &reservationEventHandler{cache: newReservationCache(nil), rrNominator: newNominator(nil, nil)}
	pi, err := framework.NewPodInfo(reservation.NewReservePod(old))
	assert.NoError(t, err)
	eh.rrNominator.AddNominatedReservePod(pi, "test-node")
	assert.Equal(t, 1, len(eh.rrNominator.NominatedReservePodForNode("test-node")))

	replaced := newPendingReservationForNomination("r-coalesced", "uid-new", "2", "8000m")
	eh.OnUpdate(old, replaced)
	assert.Empty(t, eh.rrNominator.NominatedReservePodForNode("test-node"),
		"the replaced reservation's nomination must be removed, not just the new UID's")
}

// BenchmarkNominatedReservePodForNode measures the per-pod-per-node read path
// that BeforeFilter runs: one lister lookup and one PodInfo copy per
// nomination. The status-bumped variant exists to show that a resourceVersion
// change alone does not make the read more expensive, since revalidation
// compares the reservation's scheduling identity rather than its revision.
func BenchmarkNominatedReservePodForNode(b *testing.B) {
	run := func(b *testing.B, count int, bumpVersion bool) {
		indexer := clientcache.NewIndexer(clientcache.MetaNamespaceKeyFunc, clientcache.Indexers{})
		nm := newNominator(nil, listerschedulingv1alpha1.NewReservationLister(indexer))
		for i := 0; i < count; i++ {
			r := &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "r-bench-" + strconv.Itoa(i), UID: types.UID("uid-bench-" + strconv.Itoa(i)),
					ResourceVersion: "1", Generation: 1,
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
								},
							}},
							Affinity: &corev1.Affinity{
								PodAntiAffinity: &corev1.PodAntiAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
										LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "bench"}},
										TopologyKey:   "kubernetes.io/hostname",
									}},
								},
							},
						},
					},
				},
				Status: schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationPending},
			}
			if err := indexer.Add(r); err != nil {
				b.Fatal(err)
			}
			pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
			if err != nil {
				b.Fatal(err)
			}
			nm.AddNominatedReservePod(pi, "bench-node")
			if bumpVersion {
				bumped := r.DeepCopy()
				bumped.ResourceVersion = "2"
				if err := indexer.Update(bumped); err != nil {
					b.Fatal(err)
				}
			}
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = nm.NominatedReservePodForNode("bench-node")
		}
	}

	b.Run("1-nomination", func(b *testing.B) { run(b, 1, false) })
	b.Run("10-nominations", func(b *testing.B) { run(b, 10, false) })
	b.Run("10-nominations-status-bumped", func(b *testing.B) { run(b, 10, true) })
}

// badAffinityReservation carries a pod affinity term whose selector cannot be
// parsed, which is what makes framework.NewPodInfo fail.
func badAffinityReservation(name string, uid types.UID, rv string) *schedulingv1alpha1.Reservation {
	r := newPendingReservationForNomination(name, uid, rv, "2")
	r.Spec.Template.Spec.Affinity = &corev1.Affinity{
		PodAffinity: &corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: "NotAnOperator"}},
				},
				TopologyKey: "kubernetes.io/hostname",
			}},
		},
	}
	return r
}

// TestAddNominatedReservePodSkipsUnbuildableCurrent covers the case where the
// current object cannot be turned into a reserve pod at all. Storing a
// partially parsed PodInfo would understate what the nomination occupies for
// every pod that later reads it, so nothing is nominated.
func TestAddNominatedReservePodSkipsUnbuildableCurrent(t *testing.T) {
	unparsable := badAffinityReservation("r-badaffinity", "uid-bad", "1")
	nm, _ := newNominatorWithReservations(t, unparsable)

	// The caller carries the same unparsable shape, so the call gets past the
	// shape comparison and fails where the reserve pod is actually built. A
	// caller with a different shape would be turned away earlier and would
	// leave this path untested.
	pi, err := framework.NewPodInfo(reservation.NewReservePod(unparsable))
	assert.Error(t, err, "the fixture is only useful if NewPodInfo does reject it")
	assert.NotNil(t, pi)
	nm.AddNominatedReservePod(pi, "test-node")

	assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
		"a reservation whose current shape cannot be parsed must not be nominated")
}

// TestPluginAddNominatedReservePodSkipsUnparsable ensures a reserve pod whose
// PodInfo cannot be built is not nominated at all: a partially parsed PodInfo
// would understate its constraints for every pod that later reads it.
func TestPluginAddNominatedReservePodSkipsUnparsable(t *testing.T) {
	pl := &Plugin{nominator: newNominator(nil, nil)}
	bad := reservation.NewReservePod(badAffinityReservation("r-skip", "uid-skip", "1"))
	pl.AddNominatedReservePod(bad, "test-node")
	assert.Empty(t, pl.nominator.NominatedReservePodForNode("test-node"))

	good := reservation.NewReservePod(newPendingReservationForNomination("r-ok", "uid-ok", "1", "2"))
	pl.AddNominatedReservePod(good, "test-node")
	assert.Equal(t, 1, len(pl.nominator.NominatedReservePodForNode("test-node")))
}

// TestBeforeFilterEarlyReturns covers the two guards at the top of
// BeforeFilter: a NodeInfo without a Node must not be dereferenced, and a node
// holding no nominations must be returned untouched.
func TestBeforeFilterEarlyReturns(t *testing.T) {
	pl := &Plugin{nominator: newNominator(nil, nil)}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default", UID: "p"}}

	nilNodeInfo := framework.NewNodeInfo()
	gotPod, gotNodeInfo, transformed, status := pl.BeforeFilter(context.TODO(), framework.NewCycleState(), pod, nilNodeInfo)
	assert.True(t, status == nil || status.IsSuccess())
	assert.False(t, transformed)
	assert.Equal(t, pod, gotPod)
	assert.Equal(t, nilNodeInfo, gotNodeInfo)

	emptyNodeInfo := framework.NewNodeInfo()
	emptyNodeInfo.SetNode(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}})
	gotPod, gotNodeInfo, transformed, status = pl.BeforeFilter(context.TODO(), framework.NewCycleState(), pod, emptyNodeInfo)
	assert.True(t, status == nil || status.IsSuccess())
	assert.False(t, transformed, "a node without nominations must not be snapshotted")
	assert.Equal(t, pod, gotPod)
	assert.Equal(t, emptyNodeInfo, gotNodeInfo)
}

// TestNominatedReservePodForNodeDropsTerminating covers the state a
// finalizer creates: the object stays Pending with every field a nomination is
// keyed on unchanged, but its reserve pod will never be scheduled, so the node
// it was holding has to be released. ReservationInfo.IsUnschedulable already
// treats terminating reservations this way.
func TestNominatedReservePodForNodeDropsTerminating(t *testing.T) {
	r := newPendingReservationForNomination("r-terminating", "uid-terminating", "1", "2")
	r.Finalizers = []string{"example.com/cleanup"}
	nm, indexer := newNominatorWithReservations(t, r)
	pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
	assert.NoError(t, err)
	nm.AddNominatedReservePod(pi, "test-node")
	assert.Equal(t, 1, len(nm.NominatedReservePodForNode("test-node")))

	terminating := r.DeepCopy()
	terminating.ResourceVersion = "2"
	now := metav1.Now()
	terminating.DeletionTimestamp = &now
	assert.NoError(t, indexer.Update(terminating))

	assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
		"a reservation waiting on a finalizer must not keep holding its nominated node")
}

// TestAddNominatedReservePodRejectsStaleNodeDecision covers a late scheduling
// cycle: addNominatedReservation reuses the NominatedNodeName from the cycle
// that failed, so a cycle that ran against an older revision would otherwise
// pin the current reservation to a node chosen for a shape it no longer has.
// This also subsumes what a caller's stale PodInfo could do on its own: once
// the shapes have to match, the caller's copy and a rebuild from the current
// object are byte-identical, since NewReservePod derives the reserve pod from
// exactly the spec, labels and annotations being compared.
func TestAddNominatedReservePodRejectsStaleNodeDecision(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*schedulingv1alpha1.Reservation)
		rejects bool
	}{
		{
			name: "requests changed",
			mutate: func(r *schedulingv1alpha1.Reservation) {
				r.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("8000m")
			},
			rejects: true,
		},
		{
			name: "required node affinity now points elsewhere",
			mutate: func(r *schedulingv1alpha1.Reservation) {
				r.Spec.Template.Spec.Affinity = &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{{
								MatchFields: []corev1.NodeSelectorRequirement{{
									Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{"node-1"},
								}},
							}},
						},
					},
				}
			},
			rejects: true,
		},
		{
			name: "schedulerName handed to another scheduler",
			mutate: func(r *schedulingv1alpha1.Reservation) {
				r.Spec.Template.Spec.SchedulerName = "other-scheduler"
			},
			rejects: true,
		},
		{
			name: "labels changed",
			mutate: func(r *schedulingv1alpha1.Reservation) {
				r.Labels = map[string]string{"tier": "gold"}
			},
			rejects: true,
		},
		{
			// Same shape, older revision: the cycle still describes the current
			// reservation, so its node choice stands.
			name:    "nothing that affects scheduling changed",
			mutate:  func(r *schedulingv1alpha1.Reservation) {},
			rejects: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := newPendingReservationForNomination("r-stalenode", "uid-stalenode", "2", "2000m")
			current.Generation = 2
			nm, _ := newNominatorWithReservations(t, current)

			// A newer cycle already nominated the node the current shape belongs on.
			currentPodInfo, err := framework.NewPodInfo(reservation.NewReservePod(current))
			assert.NoError(t, err)
			nm.AddNominatedReservePod(currentPodInfo, "node-2")
			assert.Equal(t, 1, len(nm.NominatedReservePodForNode("node-2")))

			// The older cycle finishes afterwards, carrying its own node choice.
			stale := newPendingReservationForNomination("r-stalenode", "uid-stalenode", "1", "2000m")
			tt.mutate(stale)
			stalePodInfo, err := framework.NewPodInfo(reservation.NewReservePod(stale))
			assert.NoError(t, err)
			nm.AddNominatedReservePod(stalePodInfo, "node-1")

			if tt.rejects {
				assert.Empty(t, nm.NominatedReservePodForNode("node-1"),
					"a node chosen for an older revision must not be nominated")
				assert.Equal(t, 1, len(nm.NominatedReservePodForNode("node-2")),
					"the rejected call must leave the newer cycle's nomination alone")
				return
			}
			assert.Equal(t, 1, len(nm.NominatedReservePodForNode("node-1")),
				"an unchanged shape means the cycle still describes the current reservation")
		})
	}
}

// TestAddNominatedReservePodRejectsTerminating covers a cycle that finishes
// after its reservation entered deletion: the reserve pod will never be
// scheduled, so its node choice must not be recorded.
func TestAddNominatedReservePodRejectsTerminating(t *testing.T) {
	r := newPendingReservationForNomination("r-term-add", "uid-term-add", "1", "2")
	r.Finalizers = []string{"example.com/cleanup"}
	terminatingAt := metav1.Now()
	r.DeletionTimestamp = &terminatingAt
	nm, _ := newNominatorWithReservations(t, r)

	pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
	assert.NoError(t, err)
	nm.AddNominatedReservePod(pi, "test-node")

	assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
		"a terminating reservation must not take a node, whatever a late cycle decided")
}

// TestAddNominatedReservePodRejectionPaths covers what happens to an existing
// nomination when a call is refused. Anything that says the reservation is no
// longer waiting on a node must clear it; a call that is merely unusable must
// leave it alone.
func TestAddNominatedReservePodRejectionPaths(t *testing.T) {
	newNominatedAt := func(t *testing.T, r *schedulingv1alpha1.Reservation) (*nominator, clientcache.Indexer) {
		nm, indexer := newNominatorWithReservations(t, r)
		pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")
		assert.Equal(t, 1, len(nm.NominatedReservePodForNode("test-node")))
		return nm, indexer
	}

	t.Run("an empty nominated node clears the nomination", func(t *testing.T) {
		r := newPendingReservationForNomination("r-nonode", "uid-nonode", "1", "2")
		nm, _ := newNominatedAt(t, r)

		pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "")
		assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
			"losing the nominated node must release the one being held")
	})

	t.Run("a reservation that left the store clears the nomination", func(t *testing.T) {
		r := newPendingReservationForNomination("r-gonemid", "uid-gonemid", "1", "2")
		nm, indexer := newNominatedAt(t, r)

		assert.NoError(t, indexer.Delete(r))
		pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")
		assert.Empty(t, nm.NominatedReservePodForNode("test-node"))
	})

	t.Run("a reservation that got scheduled clears the nomination", func(t *testing.T) {
		r := newPendingReservationForNomination("r-scheduled", "uid-scheduled", "1", "2")
		nm, indexer := newNominatedAt(t, r)

		active := r.DeepCopy()
		active.ResourceVersion = "2"
		assert.NoError(t, reservation.SetReservationAvailable(active, "test-node"))
		assert.NoError(t, indexer.Update(active))

		pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
		assert.NoError(t, err)
		nm.AddNominatedReservePod(pi, "test-node")
		assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
			"an assumed reserve pod is accounted for by the scheduler cache instead")
	})

	t.Run("a pod that is not a reserve pod is ignored", func(t *testing.T) {
		r := newPendingReservationForNomination("r-notreserve", "uid-notreserve", "1", "2")
		nm, _ := newNominatedAt(t, r)

		plain, err := framework.NewPodInfo(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "default", UID: "plain"},
		})
		assert.NoError(t, err)
		nm.AddNominatedReservePod(plain, "test-node")
		assert.Equal(t, 1, len(nm.NominatedReservePodForNode("test-node")),
			"an unrelated pod must not disturb an existing nomination")
	})
}

// TestNominatedReservePodForNodeDropsForeignNode covers the window where a
// reservation has been given a node but has not become active yet.
// isReservationWaitingForScheduling deliberately still accepts it, because
// during that window the nomination is the only thing accounting for its
// reserve pod - but only on the node it is actually going to.
func TestNominatedReservePodForNodeDropsForeignNode(t *testing.T) {
	r := newPendingReservationForNomination("r-foreign", "uid-foreign", "1", "2")
	nm, indexer := newNominatorWithReservations(t, r)
	pi, err := framework.NewPodInfo(reservation.NewReservePod(r))
	assert.NoError(t, err)
	nm.AddNominatedReservePod(pi, "test-node")
	assert.Equal(t, 1, len(nm.NominatedReservePodForNode("test-node")))

	// A status-only write, so the scheduling identity is untouched and only the
	// assigned node distinguishes this from the stored nomination.
	assigned := r.DeepCopy()
	assigned.ResourceVersion = "2"
	assigned.Status.NodeName = "another-node"
	assert.NoError(t, indexer.Update(assigned))

	assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
		"a reservation on its way to another node must not keep holding this one")
}

// TestPluginAddNominatedReservePodEmptyNodeDeletes covers the exported entry
// point's empty-node contract. An empty node means the reserve pod lost its
// nomination, and that has to be honored even for a pod the framework cannot
// parse - otherwise a reserve pod whose affinity just became invalid keeps
// holding the node it was nominated to beforehand.
func TestPluginAddNominatedReservePodEmptyNodeDeletes(t *testing.T) {
	pl := &Plugin{nominator: newNominator(nil, nil)}
	r := newPendingReservationForNomination("r-empty-node", "uid-empty-node", "1", "2")
	pl.AddNominatedReservePod(reservation.NewReservePod(r), "test-node")
	assert.Equal(t, 1, len(pl.nominator.NominatedReservePodForNode("test-node")))

	// The reservation's affinity is edited to something unparsable, and the
	// cycle that follows finds no node for it.
	unparsable := reservation.NewReservePod(badAffinityReservation("r-empty-node", "uid-empty-node", "2"))
	pl.AddNominatedReservePod(unparsable, "")

	assert.Empty(t, pl.nominator.NominatedReservePodForNode("test-node"),
		"losing the nominated node must release it even when the reserve pod no longer parses")
}

// TestEventHandlerUpdateKeepsNominationMadeAfterTheUpdate covers the ordering
// this handler cannot control: the informer store is already at the new
// revision, a scheduling cycle sees it and records a nomination for it, and only
// then does this handler get the same update. Deleting by UID would discard a
// nomination that describes the current object, and the read path never
// recreates one.
func TestEventHandlerUpdateKeepsNominationMadeAfterTheUpdate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*schedulingv1alpha1.Reservation)
	}{
		{
			name:   "spec change",
			mutate: func(r *schedulingv1alpha1.Reservation) { r.Generation = 2 },
		},
		{
			name:   "label change",
			mutate: func(r *schedulingv1alpha1.Reservation) { r.Labels = map[string]string{"tier": "gold"} },
		},
		{
			name: "annotation change",
			mutate: func(r *schedulingv1alpha1.Reservation) {
				r.Annotations = map[string]string{"example.com/key": "value"}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldR := newPendingReservationForNomination("r-late-handler", "uid-late-handler", "1", "2")
			newR := oldR.DeepCopy()
			newR.ResourceVersion = "2"
			tt.mutate(newR)

			// The store is already at newR, and a cycle that saw it nominated a node.
			nm, _ := newNominatorWithReservations(t, newR)
			h := &reservationEventHandler{cache: newReservationCache(nil), rrNominator: nm}
			pi, err := framework.NewPodInfo(reservation.NewReservePod(newR))
			assert.NoError(t, err)
			nm.AddNominatedReservePod(pi, "node-new")
			assert.Equal(t, 1, len(nm.NominatedReservePodForNode("node-new")))

			// The handler only now catches up with that same update.
			h.OnUpdate(oldR, newR)

			assert.Equal(t, 1, len(nm.NominatedReservePodForNode("node-new")),
				"a nomination recorded for the new revision must survive the late handler")
		})
	}
}

// TestEventHandlerUpdateTerminatingDeletesUnconditionally is the counterpart to
// the late-handler case above. A shape change only removes the nomination that
// predates it, but a reservation entering deletion will never have its reserve
// pod scheduled at all, so no revision of it may keep holding a node - not even
// one recorded after the update was already visible.
func TestEventHandlerUpdateTerminatingDeletesUnconditionally(t *testing.T) {
	oldR := newPendingReservationForNomination("r-term-handler", "uid-term-handler", "1", "2")
	oldR.Finalizers = []string{"example.com/cleanup"}
	newR := oldR.DeepCopy()
	newR.ResourceVersion = "2"
	terminatingAt := metav1.Now()
	newR.DeletionTimestamp = &terminatingAt

	// The store is already terminating, so the recorded identity is the new
	// revision's - the case the shape-change branch deliberately keeps.
	nm, indexer := newNominatorWithReservations(t, oldR)
	h := &reservationEventHandler{cache: newReservationCache(nil), rrNominator: nm}
	pi, err := framework.NewPodInfo(reservation.NewReservePod(oldR))
	assert.NoError(t, err)
	nm.AddNominatedReservePod(pi, "test-node")
	assert.Equal(t, 1, len(nm.NominatedReservePodForNode("test-node")))
	assert.NoError(t, indexer.Update(newR))

	h.OnUpdate(oldR, newR)

	assert.Empty(t, nm.NominatedReservePodForNode("test-node"),
		"a terminating reservation must not keep a nomination whatever recorded it")
}

// TestEventHandlerUpdateKeepsNominationAfterMetadataRoundTrip covers labels and
// annotations returning to an earlier value. A CRD with the status subresource
// does not advance metadata.generation for metadata-only writes, so the two
// visits to A produce identical scheduling identities. A cleanup that compared
// the stored identity with the event's old object would see a match on the
// delayed A -> B event and delete the nomination a cycle had just recorded for
// the second A.
func TestEventHandlerUpdateKeepsNominationAfterMetadataRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		setA func(*schedulingv1alpha1.Reservation)
		setB func(*schedulingv1alpha1.Reservation)
	}{
		{
			name: "labels",
			setA: func(r *schedulingv1alpha1.Reservation) { r.Labels = map[string]string{"tier": "a"} },
			setB: func(r *schedulingv1alpha1.Reservation) { r.Labels = map[string]string{"tier": "b"} },
		},
		{
			name: "annotations",
			setA: func(r *schedulingv1alpha1.Reservation) { r.Annotations = map[string]string{"example.com/k": "a"} },
			setB: func(r *schedulingv1alpha1.Reservation) { r.Annotations = map[string]string{"example.com/k": "b"} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := newPendingReservationForNomination("r-aba", "uid-aba", "1", "2")
			tt.setA(first)
			second := first.DeepCopy()
			second.ResourceVersion = "2"
			tt.setB(second)
			third := first.DeepCopy() // back to A, generation never moved
			third.ResourceVersion = "3"

			// The store is already at the second A and a cycle nominated for it.
			nm, _ := newNominatorWithReservations(t, third)
			h := &reservationEventHandler{cache: newReservationCache(nil), rrNominator: nm}
			pi, err := framework.NewPodInfo(reservation.NewReservePod(third))
			assert.NoError(t, err)
			nm.AddNominatedReservePod(pi, "node-current")
			assert.Equal(t, 1, len(nm.NominatedReservePodForNode("node-current")))

			// The handler only now catches up with the earlier A -> B event.
			h.OnUpdate(first, second)

			assert.Equal(t, 1, len(nm.NominatedReservePodForNode("node-current")),
				"a nomination matching the current store must survive an older update")
		})
	}
}

// TestEventHandlerUpdateKeepsPreAllocationOfCurrentCycle covers the other state
// this cleanup could reach. Pre-allocation candidates are recorded without any
// identity, so a cleanup that treated "no identity" as "no provenance" would
// clear the candidates a cycle in progress had just recorded.
func TestEventHandlerUpdateKeepsPreAllocationOfCurrentCycle(t *testing.T) {
	oldR := newPendingReservationForNomination("r-prealloc", "uid-prealloc", "1", "2")
	newR := oldR.DeepCopy()
	newR.ResourceVersion = "2"
	newR.Generation = 2

	nm, _ := newNominatorWithReservations(t, newR)
	h := &reservationEventHandler{cache: newReservationCache(nil), rrNominator: nm}
	rInfo := frameworkext.NewReservationInfo(newR)
	candidate := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "candidate", Namespace: "default", UID: "candidate"},
		Spec:       corev1.PodSpec{NodeName: "test-node"},
	}
	nm.AddNominatedPreAllocation(rInfo, "test-node", candidate)
	assert.NotNil(t, nm.GetNominatedPreAllocation(rInfo, "test-node"))

	h.OnUpdate(oldR, newR)

	assert.NotNil(t, nm.GetNominatedPreAllocation(rInfo, "test-node"),
		"a reserve pod cleanup must not take the pre-allocation state of a cycle in progress")
}
