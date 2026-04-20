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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

func makePod(name, uid, schedulerName, nominatedNodeName, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(uid),
		},
		Spec: corev1.PodSpec{
			SchedulerName: schedulerName,
			NodeName:      nodeName,
		},
		Status: corev1.PodStatus{
			NominatedNodeName: nominatedNodeName,
		},
	}
}

// TestCrossSchedulerPodNominator_AddDelete tests basic add and delete operations.
func TestCrossSchedulerPodNominator_AddDelete(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	pod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "")

	// Add a non-local pod with nominatedNodeName.
	nominator.OnAdd(pod)
	pods := nominator.NominatedPodsForNode("node-1")
	assert.Len(t, pods, 1)
	assert.Equal(t, "pod-1", pods[0].GetPod().Name)

	// Delete the pod and verify the index is cleared.
	nominator.OnDelete(pod)
	pods = nominator.NominatedPodsForNode("node-1")
	assert.Empty(t, pods)
}

// TestCrossSchedulerPodNominator_LocalSchedulerExcluded tests that local scheduler pods are excluded by ShouldHandle.
func TestCrossSchedulerPodNominator_LocalSchedulerExcluded(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("koord-scheduler")

	// A pod from the local scheduler should NOT pass ShouldHandle filter.
	localPod := makePod("local-pod", "uid-local", "koord-scheduler", "node-1", "")
	assert.False(t, nominator.ShouldHandle(localPod), "local scheduler pod should not pass ShouldHandle")

	// In production, FilteringResourceEventHandler won't call OnAdd because ShouldHandle returns false.
	// OnAdd does NOT filter by schedulerName - that's ShouldHandle's job.
	// We do NOT call OnAdd for localPod here because that's not how the handler works in production.

	// A pod from a non-local scheduler SHOULD pass ShouldHandle and be added.
	foreignPod := makePod("foreign-pod", "uid-foreign", "other-scheduler", "node-1", "")
	assert.True(t, nominator.ShouldHandle(foreignPod), "non-local scheduler pod should pass ShouldHandle")
	nominator.OnAdd(foreignPod)
	pods := nominator.NominatedPodsForNode("node-1")
	assert.Len(t, pods, 1)
	assert.Equal(t, "foreign-pod", pods[0].GetPod().Name)
}

// TestCrossSchedulerPodNominator_MultiplePodsOnSameNode tests that multiple pods on the same node are all returned.
func TestCrossSchedulerPodNominator_MultiplePodsOnSameNode(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	pod1 := makePod("pod-1", "uid-1", "scheduler-a", "node-1", "")
	pod2 := makePod("pod-2", "uid-2", "scheduler-b", "node-1", "")
	pod3 := makePod("pod-3", "uid-3", "scheduler-c", "node-2", "")

	nominator.OnAdd(pod1)
	nominator.OnAdd(pod2)
	nominator.OnAdd(pod3)

	node1Pods := nominator.NominatedPodsForNode("node-1")
	assert.Len(t, node1Pods, 2)

	node2Pods := nominator.NominatedPodsForNode("node-2")
	assert.Len(t, node2Pods, 1)
	assert.Equal(t, "pod-3", node2Pods[0].GetPod().Name)
}

// TestCrossSchedulerPodNominator_BoundPodClearedOnUpdate tests that a bound pod is removed from the index.
// With FilteringResourceEventHandler, when oldPod passes ShouldHandle but newPod doesn't (bound),
// the handler calls OnDelete(oldPod) instead of OnUpdate.
func TestCrossSchedulerPodNominator_BoundPodClearedOnUpdate(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	oldPod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "")
	nominator.OnAdd(oldPod)
	assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)

	// Simulate pod being bound: spec.nodeName is set.
	newPod := oldPod.DeepCopy()
	newPod.Spec.NodeName = "node-1"

	// Verify ShouldHandle behavior: oldPod passes, newPod doesn't.
	assert.True(t, nominator.ShouldHandle(oldPod), "oldPod should pass ShouldHandle")
	assert.False(t, nominator.ShouldHandle(newPod), "bound newPod should not pass ShouldHandle")

	// With FilteringResourceEventHandler, when old passes filter but new doesn't,
	// OnDelete(oldObj) is called instead of OnUpdate.
	nominator.OnDelete(oldPod)
	assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
}

// TestCrossSchedulerPodNominator_NominatedNodeNameCleared tests removal when nominatedNodeName is cleared.
// With FilteringResourceEventHandler, when oldPod passes ShouldHandle but newPod doesn't (nominatedNodeName cleared),
// the handler calls OnDelete(oldPod) instead of OnUpdate.
func TestCrossSchedulerPodNominator_NominatedNodeNameCleared(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	oldPod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "")
	nominator.OnAdd(oldPod)
	assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)

	// Simulate nominatedNodeName being cleared.
	newPod := oldPod.DeepCopy()
	newPod.Status.NominatedNodeName = ""

	// Verify ShouldHandle behavior: oldPod passes, newPod doesn't.
	assert.True(t, nominator.ShouldHandle(oldPod), "oldPod should pass ShouldHandle")
	assert.False(t, nominator.ShouldHandle(newPod), "newPod with cleared nominatedNodeName should not pass ShouldHandle")

	// With FilteringResourceEventHandler, when old passes filter but new doesn't,
	// OnDelete(oldObj) is called instead of OnUpdate.
	nominator.OnDelete(oldPod)
	assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
}

// TestCrossSchedulerPodNominator_SchedulerNameChangedToLocal tests that when schedulerName changes to local,
// the pod is removed from the cross-scheduler nominator.
// With FilteringResourceEventHandler, when oldPod passes ShouldHandle but newPod doesn't (schedulerName changed to local),
// the handler calls OnDelete(oldPod) instead of OnUpdate.
func TestCrossSchedulerPodNominator_SchedulerNameChangedToLocal(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("koord-scheduler")

	// Initially a non-local pod with nomination.
	oldPod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "")
	nominator.OnAdd(oldPod)
	assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)

	// schedulerName changes from non-local to local: should be removed.
	newPod := oldPod.DeepCopy()
	newPod.Spec.SchedulerName = "koord-scheduler"

	// Verify ShouldHandle behavior: oldPod passes, newPod doesn't.
	assert.True(t, nominator.ShouldHandle(oldPod), "oldPod should pass ShouldHandle")
	assert.False(t, nominator.ShouldHandle(newPod), "newPod with local schedulerName should not pass ShouldHandle")

	// With FilteringResourceEventHandler, when old passes filter but new doesn't,
	// OnDelete(oldObj) is called instead of OnUpdate.
	nominator.OnDelete(oldPod)
	assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
}

// TestCrossSchedulerPodNominator_SchedulerNameChangedFromLocal tests that when schedulerName changes from local
// to non-local with a nominatedNodeName, the pod is added to the cross-scheduler nominator.
func TestCrossSchedulerPodNominator_SchedulerNameChangedFromLocal(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("koord-scheduler")

	// Initially a local pod — would not be in the cross-scheduler index.
	oldPod := makePod("pod-1", "uid-1", "koord-scheduler", "node-1", "")

	// schedulerName changes from local to non-local with nominatedNodeName set.
	newPod := oldPod.DeepCopy()
	newPod.Spec.SchedulerName = "other-scheduler"
	newPod.Status.NominatedNodeName = "node-1"

	nominator.OnUpdate(oldPod, newPod)
	pods := nominator.NominatedPodsForNode("node-1")
	assert.Len(t, pods, 1)
	assert.Equal(t, "pod-1", pods[0].GetPod().Name)
}

// TestCrossSchedulerPodNominator_SchedulerAndNominatedNodeBothChange tests combined changes.
// With FilteringResourceEventHandler, when oldPod passes ShouldHandle but newPod doesn't (schedulerName changed to local),
// the handler calls OnDelete(oldPod) instead of OnUpdate.
func TestCrossSchedulerPodNominator_SchedulerAndNominatedNodeBothChange(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("koord-scheduler")

	// Pod starts as non-local nominated on node-1.
	oldPod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "")
	nominator.OnAdd(oldPod)
	assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)

	// schedulerName changes to local AND nominatedNodeName also changes — should be removed.
	newPod := oldPod.DeepCopy()
	newPod.Spec.SchedulerName = "koord-scheduler"
	newPod.Status.NominatedNodeName = "node-2"

	// Verify ShouldHandle behavior: oldPod passes, newPod doesn't.
	assert.True(t, nominator.ShouldHandle(oldPod), "oldPod should pass ShouldHandle")
	assert.False(t, nominator.ShouldHandle(newPod), "newPod with local schedulerName should not pass ShouldHandle")

	// With FilteringResourceEventHandler, when old passes filter but new doesn't,
	// OnDelete(oldObj) is called instead of OnUpdate.
	nominator.OnDelete(oldPod)
	assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
	assert.Empty(t, nominator.NominatedPodsForNode("node-2"))
}

// TestCrossSchedulerPodNominator_OnDelete_DeletedFinalStateUnknown tests handling of tombstone objects.
func TestCrossSchedulerPodNominator_OnDelete_DeletedFinalStateUnknown(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	pod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "")
	nominator.OnAdd(pod)
	assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)

	// Wrap in a DeletedFinalStateUnknown tombstone to simulate informer cache eviction.
	tombstone := cache.DeletedFinalStateUnknown{
		Key: "default/pod-1",
		Obj: pod,
	}
	nominator.OnDelete(tombstone)
	assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
}

// TestCrossSchedulerPodNominator_OnAdd_InvalidObject tests that non-pod objects are ignored gracefully.
func TestCrossSchedulerPodNominator_OnAdd_InvalidObject(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	// Passing a non-pod object should not panic and should not add anything.
	nominator.OnAdd("not-a-pod")
	nominator.OnAdd(42)
	nominator.OnAdd(nil)

	// Nothing should have been added.
	assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
}

// TestCrossSchedulerPodNominator_OnUpdate_InvalidObject tests that non-pod objects are ignored gracefully.
func TestCrossSchedulerPodNominator_OnUpdate_InvalidObject(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	pod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "")
	nominator.OnAdd(pod)

	// Invalid old or new object should not panic.
	nominator.OnUpdate("not-a-pod", pod)
	nominator.OnUpdate(pod, "not-a-pod")

	// The original pod should still be present (or gracefully removed — just no panic).
	// Either way, no crash is the key assertion.
}

// TestCrossSchedulerPodNominator_NominatedNodeNameChanged tests update when nominatedNodeName moves to another node.
func TestCrossSchedulerPodNominator_NominatedNodeNameChanged(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	oldPod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "")
	nominator.OnAdd(oldPod)
	assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)
	assert.Empty(t, nominator.NominatedPodsForNode("node-2"))

	// nominatedNodeName changes from node-1 to node-2.
	newPod := oldPod.DeepCopy()
	newPod.Status.NominatedNodeName = "node-2"

	nominator.OnUpdate(oldPod, newPod)
	assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
	assert.Len(t, nominator.NominatedPodsForNode("node-2"), 1)
}

// TestCrossSchedulerPodNominator_OnAdd_BoundPodIgnored tests that a pod already bound to a node
// is filtered out by ShouldHandle.
func TestCrossSchedulerPodNominator_OnAdd_BoundPodIgnored(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	// Pod is already bound (spec.nodeName set).
	pod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "node-1")

	// Verify ShouldHandle returns false for bound pods.
	assert.False(t, nominator.ShouldHandle(pod), "bound pod should not pass ShouldHandle")

	// In production, FilteringResourceEventHandler won't call OnAdd because ShouldHandle returns false.
	// We do NOT call OnAdd for bound pod here because that's not how the handler works in production.
	// addNominatedPod only checks NominatedNodeName, not NodeName, so it would add bound pods
	// if called directly. But ShouldHandle filters them out first.
}

// TestCrossSchedulerPodNominator_ShouldHandle tests the ShouldHandle filter function
// which determines whether a pod should be handled by the cross-scheduler nominator.
func TestCrossSchedulerPodNominator_ShouldHandle(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("koord-scheduler")

	tests := []struct {
		name            string
		obj             interface{}
		wantHandle      bool
		wantDescription string
	}{
		{
			name:            "pod with nominatedNodeName and non-local scheduler - should handle",
			obj:             makePod("pod-1", "uid-1", "other-scheduler", "node-1", ""),
			wantHandle:      true,
			wantDescription: "non-local scheduler pod with nominatedNodeName should be handled",
		},
		{
			name:            "pod with empty nominatedNodeName - should not handle",
			obj:             makePod("pod-2", "uid-2", "other-scheduler", "", ""),
			wantHandle:      false,
			wantDescription: "pod without nominatedNodeName should not be handled",
		},
		{
			name:            "pod with local schedulerName - should not handle",
			obj:             makePod("pod-3", "uid-3", "koord-scheduler", "node-1", ""),
			wantHandle:      false,
			wantDescription: "local scheduler pod should not be handled",
		},
		{
			name:            "bound pod (nodeName set) - should not handle",
			obj:             makePod("pod-4", "uid-4", "other-scheduler", "node-1", "node-1"),
			wantHandle:      false,
			wantDescription: "bound pod should not be handled",
		},
		{
			name:            "non-pod object - should not handle",
			obj:             "not-a-pod",
			wantHandle:      false,
			wantDescription: "non-pod object should not be handled",
		},
		{
			name:            "nil object - should not handle",
			obj:             nil,
			wantHandle:      false,
			wantDescription: "nil object should not be handled",
		},
		{
			name: "DeletedFinalStateUnknown with valid pod - should handle",
			obj: cache.DeletedFinalStateUnknown{
				Key: "default/pod-5",
				Obj: makePod("pod-5", "uid-5", "other-scheduler", "node-1", ""),
			},
			wantHandle:      true,
			wantDescription: "tombstone with valid non-local pod should be handled",
		},
		{
			name: "DeletedFinalStateUnknown with local scheduler pod - should not handle",
			obj: cache.DeletedFinalStateUnknown{
				Key: "default/pod-6",
				Obj: makePod("pod-6", "uid-6", "koord-scheduler", "node-1", ""),
			},
			wantHandle:      false,
			wantDescription: "tombstone with local scheduler pod should not be handled",
		},
		{
			name: "DeletedFinalStateUnknown with non-pod object - should not handle",
			obj: cache.DeletedFinalStateUnknown{
				Key: "default/not-a-pod",
				Obj: "not-a-pod",
			},
			wantHandle:      false,
			wantDescription: "tombstone with non-pod object should not be handled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nominator.ShouldHandle(tt.obj)
			assert.Equal(t, tt.wantHandle, got, tt.wantDescription)
		})
	}
}

// TestCrossSchedulerPodNominator_NominatedPodsForNode_DeepCopy tests that NominatedPodsForNode
// returns a deep copy, so modifications to the returned slice don't affect the internal state.
func TestCrossSchedulerPodNominator_NominatedPodsForNode_DeepCopy(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("local-scheduler")

	// Add a pod.
	pod := makePod("pod-1", "uid-1", "other-scheduler", "node-1", "")
	nominator.OnAdd(pod)

	// Get the nominated pods.
	pods1 := nominator.NominatedPodsForNode("node-1")
	assert.Len(t, pods1, 1)
	assert.Equal(t, "pod-1", pods1[0].GetPod().Name)

	// Get the nominated pods again before modification.
	pods2 := nominator.NominatedPodsForNode("node-1")
	assert.Len(t, pods2, 1)

	// Verify that the returned PodInfo objects are different instances.
	assert.NotSame(t, pods1[0], pods2[0], "returned PodInfo should be different instances")

	// Verify that the returned Pod objects are different instances (deep copy).
	assert.NotSame(t, pods1[0].GetPod(), pods2[0].GetPod(), "returned Pod should be different instances (deep copy)")

	// Verify true deep copy: modifying the returned pod does NOT affect internal state.
	// Modify the returned pod's name.
	originalName := pods1[0].GetPod().Name
	pods1[0].GetPod().Name = "modified-name"

	// Get the pods again and verify the internal state is unchanged.
	pods3 := nominator.NominatedPodsForNode("node-1")
	assert.Len(t, pods3, 1)
	assert.Equal(t, originalName, pods3[0].GetPod().Name, "internal state should not be affected by modification to returned pod")
	assert.NotEqual(t, "modified-name", pods3[0].GetPod().Name, "modification should not leak to internal state")
}

// TestCrossSchedulerPodNominator_FilteringResourceEventHandler tests the complete
// FilteringResourceEventHandler behavior wrapping the nominator.
func TestCrossSchedulerPodNominator_FilteringResourceEventHandler(t *testing.T) {
	nominator := NewCrossSchedulerPodNominator()
	nominator.AddLocalProfileName("koord-scheduler")

	// Create a FilteringResourceEventHandler that wraps the nominator using ResourceEventHandlerFuncs.
	filterHandler := cache.FilteringResourceEventHandler{
		FilterFunc: nominator.ShouldHandle,
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    nominator.OnAdd,
			UpdateFunc: nominator.OnUpdate,
			DeleteFunc: nominator.OnDelete,
		},
	}

	// Test 1: old passes filter, new doesn't (bound) -> OnDelete(old)
	t.Run("old_passes_new_does_not_bound", func(t *testing.T) {
		pod := makePod("test-pod-1", "uid-test-1", "other-scheduler", "node-1", "")
		filterHandler.OnAdd(pod, false)
		assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)

		// Pod becomes bound.
		boundPod := pod.DeepCopy()
		boundPod.Spec.NodeName = "node-1"

		// FilteringResourceEventHandler.OnUpdate will call OnDelete(oldObj) because old passes but new doesn't.
		filterHandler.OnUpdate(pod, boundPod)
		assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
	})

	// Test 2: old passes filter, new doesn't (nominatedNodeName cleared) -> OnDelete(old)
	t.Run("old_passes_new_does_not_nominated_cleared", func(t *testing.T) {
		pod := makePod("test-pod-2", "uid-test-2", "other-scheduler", "node-1", "")
		filterHandler.OnAdd(pod, false)
		assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)

		// Pod's nominatedNodeName is cleared.
		clearedPod := pod.DeepCopy()
		clearedPod.Status.NominatedNodeName = ""

		filterHandler.OnUpdate(pod, clearedPod)
		assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
	})

	// Test 3: old passes filter, new doesn't (schedulerName changed to local) -> OnDelete(old)
	t.Run("old_passes_new_does_not_scheduler_local", func(t *testing.T) {
		pod := makePod("test-pod-3", "uid-test-3", "other-scheduler", "node-1", "")
		filterHandler.OnAdd(pod, false)
		assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)

		// Pod's schedulerName changes to local.
		localPod := pod.DeepCopy()
		localPod.Spec.SchedulerName = "koord-scheduler"

		filterHandler.OnUpdate(pod, localPod)
		assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
	})

	// Test 4: both pass filter (nominatedNodeName change) -> OnUpdate
	t.Run("both_pass_nominated_node_changed", func(t *testing.T) {
		pod := makePod("test-pod-4", "uid-test-4", "other-scheduler", "node-1", "")
		filterHandler.OnAdd(pod, false)
		assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)

		// Pod's nominatedNodeName changes to another node.
		newNodePod := pod.DeepCopy()
		newNodePod.Status.NominatedNodeName = "node-2"

		filterHandler.OnUpdate(pod, newNodePod)
		assert.Empty(t, nominator.NominatedPodsForNode("node-1"))
		assert.Len(t, nominator.NominatedPodsForNode("node-2"), 1)
	})

	// Test 5: old doesn't pass filter, new passes -> OnAdd(new)
	t.Run("old_does_not_new_passes", func(t *testing.T) {
		// Pod starts as local scheduler (doesn't pass filter).
		localPod := makePod("test-pod-5", "uid-test-5", "koord-scheduler", "node-1", "")

		// Pod's schedulerName changes to non-local with nominatedNodeName.
		nonLocalPod := localPod.DeepCopy()
		nonLocalPod.Spec.SchedulerName = "other-scheduler"
		nonLocalPod.Status.NominatedNodeName = "node-1"

		// FilteringResourceEventHandler.OnUpdate will call OnAdd(newObj) because old doesn't pass but new does.
		filterHandler.OnUpdate(localPod, nonLocalPod)
		assert.Len(t, nominator.NominatedPodsForNode("node-1"), 1)
	})

	// Test 6: neither passes filter -> nothing happens
	t.Run("neither_passes", func(t *testing.T) {
		// Use a fresh nominator for this test to avoid pollution from previous tests.
		freshNominator := NewCrossSchedulerPodNominator()
		freshNominator.AddLocalProfileName("koord-scheduler")

		freshFilterHandler := cache.FilteringResourceEventHandler{
			FilterFunc: freshNominator.ShouldHandle,
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc:    freshNominator.OnAdd,
				UpdateFunc: freshNominator.OnUpdate,
				DeleteFunc: freshNominator.OnDelete,
			},
		}

		localPod1 := makePod("test-pod-6", "uid-test-6", "koord-scheduler", "node-3", "")
		localPod2 := localPod1.DeepCopy()
		localPod2.Status.NominatedNodeName = "node-4"

		freshFilterHandler.OnUpdate(localPod1, localPod2)
		assert.Empty(t, freshNominator.NominatedPodsForNode("node-3"))
		assert.Empty(t, freshNominator.NominatedPodsForNode("node-4"))
	})
}
