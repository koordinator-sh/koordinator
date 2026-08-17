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

package deviceshare

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

// gpuAllocations builds a single-GPU DeviceAllocations on the given minor.
func gpuAllocations(minor int32, core int64) apiext.DeviceAllocations {
	return apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
			{
				Minor: minor,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        *resource.NewQuantity(core, resource.DecimalSI),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(core, resource.DecimalSI),
				},
			},
		},
	}
}

func assumeTestPod(uid, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(uid),
			Namespace: "default",
			Name:      "pod-" + uid,
		},
		Spec: corev1.PodSpec{NodeName: node},
	}
}

// eventPodFrom clones base as an informer event object: sets the bound node and writes the
// device-allocation annotation (updatePod reads allocations from annotations, not allocateSet).
func eventPodFrom(base *corev1.Pod, node string, alloc apiext.DeviceAllocations) *corev1.Pod {
	p := base.DeepCopy()
	p.Spec.NodeName = node
	if alloc != nil {
		_ = apiext.SetDeviceAllocations(p, alloc)
	}
	return p
}

// gpuMinors returns the GPU minors currently recorded for pod ns/name on nd.
func gpuMinors(nd *nodeDevice, ns, name string) []int {
	nd.lock.RLock()
	defer nd.lock.RUnlock()
	res := nd.getUsed(ns, name)[schedulingv1alpha1.GPU]
	minors := make([]int, 0, len(res))
	for m := range res {
		minors = append(minors, m)
	}
	return minors
}

// reserveInto simulates Plugin.Reserve writing an allocation to the per-node cache.
func reserveInto(cache *nodeDeviceCache, node string, pod *corev1.Pod, alloc apiext.DeviceAllocations) {
	nd := cache.getNodeDevice(node, true)
	nd.lock.Lock()
	nd.updateCacheUsed(alloc, pod, true)
	nd.lock.Unlock()
}

// unboundPod builds a pod that is assumed/reserved but not yet bound (Spec.NodeName == ""),
// matching the pod object as it exists through Reserve and the PreBind annotation patch.
func unboundPod(uid string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(uid),
			Namespace: "default",
			Name:      "pod-" + uid,
		},
	}
}

// The assumed marker is a pure rollback record: positive (assigned) events — the PreBind
// annotation patch and the bound event — never consume it or re-add the pod. It is cleared
// only by a terminal negative event (here, delete).
func Test_nodeDeviceCache_PositiveEvents_KeepMarkerAndDoNotReAdd(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := unboundPod("1")
	alloc := gpuAllocations(0, 100)
	reserveInto(cache, "node-1", pod, alloc)
	assert.NoError(t, cache.AssumePod(pod, "node-1"))
	nd := cache.getNodeDevice("node-1", false)

	// PreBind patches the allocation annotation while the pod is still unbound (NodeName=="").
	prebindPod := pod.DeepCopy()
	assert.NoError(t, apiext.SetDeviceAllocations(prebindPod, alloc))
	cache.OnPodUpdate(pod, prebindPod)
	_, ok := assumedEntry(cache, pod.UID)
	assert.True(t, ok, "PreBind (unbound) event must not consume the assumed marker")
	assert.ElementsMatch(t, []int{0}, gpuMinors(nd, pod.Namespace, pod.Name), "node stays credited while assumed")

	// The bound event is positive: it must NOT consume the marker and must NOT re-add the pod
	// (spec.nodeName is not a trustworthy bind signal — arbitration fakes it).
	boundPod := prebindPod.DeepCopy()
	boundPod.Spec.NodeName = "node-1"
	cache.OnPodUpdate(prebindPod, boundPod)
	_, ok = assumedEntry(cache, pod.UID)
	assert.True(t, ok, "bound (positive) event must leave the marker for a terminal event")
	assert.ElementsMatch(t, []int{0}, gpuMinors(nd, pod.Namespace, pod.Name), "no re-add, no double count")

	// A terminal delete finally clears the marker and rolls the allocation back exactly once.
	cache.OnPodDelete(boundPod)
	_, ok = assumedEntry(cache, pod.UID)
	assert.False(t, ok, "delete must clear the marker")
	assert.Empty(t, gpuMinors(nd, pod.Namespace, pod.Name), "delete rolls back the assumed write")
}

func Test_nodeDeviceCache_UnreserveAfterPreBind_NoDoubleCount(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := unboundPod("1")
	alloc := gpuAllocations(0, 100)
	reserveInto(cache, "node-1", pod, alloc)
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	// PreBind patch (unbound): marker preserved, pod still in the ledger.
	prebindPod := pod.DeepCopy()
	assert.NoError(t, apiext.SetDeviceAllocations(prebindPod, alloc))
	cache.OnPodUpdate(pod, prebindPod)

	// Bind fails → Unreserve: updateCacheUsed(..., false) then ForgetPod (what Plugin.Unreserve does).
	nd := cache.getNodeDevice("node-1", false)
	nd.lock.Lock()
	nd.updateCacheUsed(alloc, pod, false)
	nd.lock.Unlock()
	assert.NoError(t, cache.ForgetPod(pod))

	// Exactly one subtract: node freed, marker cleared, no negative/leaked accounting.
	assert.Empty(t, gpuMinors(nd, pod.Namespace, pod.Name))
	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok)
}

func Test_nodeDeviceCache_TerminatedAtBind_NotCredited(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := unboundPod("1")
	alloc := gpuAllocations(0, 100)
	reserveInto(cache, "node-1", pod, alloc)
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	// The bound event arrives for a pod that has already terminated (fast-failing pod).
	// Termination is a negative event: roll Reserve's write back via the marker and do NOT
	// credit the pod — a terminated pod holds no allocations.
	boundTerminated := pod.DeepCopy()
	boundTerminated.Spec.NodeName = "node-1"
	assert.NoError(t, apiext.SetDeviceAllocations(boundTerminated, alloc))
	boundTerminated.Status.Phase = corev1.PodFailed
	cache.OnPodUpdate(pod, boundTerminated)

	nd := cache.getNodeDevice("node-1", false)
	assert.Empty(t, gpuMinors(nd, pod.Namespace, pod.Name), "terminated pod must not be credited")
	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "marker must be consumed on the terminated event")
}

// A watch reconnect can relist an already-terminated Reserved pod as an Add (not an Update),
// before any annotation was written. OnPodAdd must treat termination as a negative event and
// roll back via the marker — not fall through to the annotation-based deletePod, which would
// leak Reserve's write and leave the marker dangling.
func Test_nodeDeviceCache_OnPodAdd_TerminatedWithMarker_RollsBack(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1") // bound, but carries no device-allocation annotation
	reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	terminated := pod.DeepCopy()
	terminated.Status.Phase = corev1.PodFailed
	cache.OnPodAdd(terminated)

	nd := cache.getNodeDevice("node-1", false)
	assert.Empty(t, gpuMinors(nd, pod.Namespace, pod.Name), "terminated relist Add must roll back the assumed write")
	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "terminated relist Add must clear the marker")
}

func assumedEntry(cache *nodeDeviceCache, uid types.UID) (*assumedAllocation, bool) {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	a, ok := cache.assumedPods[uid]
	return a, ok
}

func Test_nodeDeviceCache_AssumePod(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	alloc := gpuAllocations(0, 100)
	reserveInto(cache, "node-1", pod, alloc)

	err := cache.AssumePod(pod, "node-1")
	assert.NoError(t, err)

	assumed, ok := assumedEntry(cache, pod.UID)
	assert.True(t, ok, "pod must be recorded in assumedPods after AssumePod")
	assert.Equal(t, "node-1", assumed.nodeName)
	if assert.Len(t, assumed.allocations[schedulingv1alpha1.GPU], 1) {
		assert.Equal(t, int32(0), assumed.allocations[schedulingv1alpha1.GPU][0].Minor)
	}
}

func Test_nodeDeviceCache_AssumePod_MissingNode(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")

	err := cache.AssumePod(pod, "node-1")
	assert.Error(t, err, "AssumePod must error when the nodeDevice is missing")

	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "no assumed entry must be recorded on error")
}

// AssumePod snapshots the pod's cache contribution and publishes the marker under a single
// n.lock hold. If a concurrent event already removed the pod's allocation (empty snapshot),
// there is nothing to roll back later, so no marker is published — avoiding an orphaned entry.
func Test_nodeDeviceCache_AssumePod_SkipsEmptyMarker(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	// The node exists but the pod has no allocation recorded (simulating a concurrent removal
	// between Reserve's write and AssumePod).
	cache.getNodeDevice("node-1", true)

	assert.NoError(t, cache.AssumePod(pod, "node-1"))
	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "no marker must be published when the pod's cache contribution is empty")
}

// Pins the safety property the Reserve-error inline rollback relies on: rolling back Reserve's
// cache write, then a subsequent Unreserve subtract for the same pod, nets exactly one subtract.
// The isValid guard makes the second subtract a no-op, so the inline rollback cannot double-count
// even when the framework also runs Unreserve. (Corrects the round-3 "would double-subtract".)
func Test_nodeDeviceCache_ReserveErrorRollback_NoDoubleSubtract(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	alloc := gpuAllocations(0, 100)
	reserveInto(cache, "node-1", pod, alloc) // Reserve's updateCacheUsed(+A)
	nd := cache.getNodeDevice("node-1", false)
	assert.ElementsMatch(t, []int{0}, gpuMinors(nd, pod.Namespace, pod.Name))

	// Inline rollback on AssumePod error: updateCacheUsed(-A).
	nd.lock.Lock()
	nd.updateCacheUsed(alloc, pod, false)
	nd.lock.Unlock()
	assert.Empty(t, gpuMinors(nd, pod.Namespace, pod.Name), "inline rollback removes the write")

	// Framework Unreserve also runs updateCacheUsed(-A): must be a no-op, not go negative.
	nd.lock.Lock()
	nd.updateCacheUsed(alloc, pod, false)
	nd.lock.Unlock()
	assert.Empty(t, gpuMinors(nd, pod.Namespace, pod.Name), "second subtract is a no-op via isValid")
}

// A reservation's reserve pod is Reserved (marker stored); its synthetic delete event flows
// through the onPodDelete adapter, which now routes to OnPodDelete → releaseAssumed. The marker
// must be cleared and the assumed allocation rolled back — the legacy annotation path left the
// ledger entry to leak forever (ZiMengSheng review item 2).
func Test_nodeDeviceCache_onPodDelete_ReleasesAssumedMarker(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("resv-1", "node-1")
	alloc := gpuAllocations(0, 100)
	reserveInto(cache, "node-1", pod, alloc)
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	cache.onPodDelete(pod) // synthetic reservation delete (ResourceEventHandler interface{} arg)

	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "reservation delete must clear the assumed marker")
	nd := cache.getNodeDevice("node-1", false)
	assert.Empty(t, gpuMinors(nd, pod.Namespace, pod.Name), "reservation delete must roll back the assumed allocation")
}

// An expired/failed reservation surfaces as a synthetic reserve pod with Phase=PodFailed; its
// update event through the onPodUpdate adapter must hit the terminate→releaseAssumed branch and
// clear the marker (the legacy onPodUpdate→deletePod path did not).
func Test_nodeDeviceCache_onPodUpdate_TerminatedReservation_ReleasesMarker(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("resv-1", "node-1")
	alloc := gpuAllocations(0, 100)
	reserveInto(cache, "node-1", pod, alloc)
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	oldPod := pod.DeepCopy()
	newPod := pod.DeepCopy()
	newPod.Status.Phase = corev1.PodFailed
	cache.onPodUpdate(oldPod, newPod)

	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "terminated reservation must clear the assumed marker")
	nd := cache.getNodeDevice("node-1", false)
	assert.Empty(t, gpuMinors(nd, pod.Namespace, pod.Name))
}

func Test_nodeDeviceCache_ForgetPod_Idempotent(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	assert.NoError(t, cache.ForgetPod(pod))
	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "ForgetPod must clear the assumed marker")

	// Idempotent: a second ForgetPod, and a ForgetPod for a never-assumed pod, are safe no-ops.
	assert.NoError(t, cache.ForgetPod(pod))
	assert.NoError(t, cache.ForgetPod(assumeTestPod("never", "node-1")))
}

// A positive event on a node other than the one Reserve wrote (a faked arbitration nodeName)
// must not re-add the pod there, and must leave the reserved node's marker intact so a later
// ForgetPod can still roll it back.
func Test_nodeDeviceCache_PositiveEvent_DivergentNode_NotCredited(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	// Arbitration transform fakes spec.nodeName as node-2 (not the reserved node).
	cache.OnPodAdd(eventPodFrom(pod, "node-2", gpuAllocations(0, 100)))

	node1 := cache.getNodeDevice("node-1", false)
	assert.ElementsMatch(t, []int{0}, gpuMinors(node1, pod.Namespace, pod.Name), "reserved node unchanged")
	if node2 := cache.getNodeDevice("node-2", false); node2 != nil {
		assert.Empty(t, gpuMinors(node2, pod.Namespace, pod.Name), "faked node must not be credited")
	}
	_, ok := assumedEntry(cache, pod.UID)
	assert.True(t, ok, "marker must survive a faked-nodeName event for ForgetPod to roll back")
}

// A bound->unassigned transition (arbitration clears spec.nodeName back to "") is a negative
// event: it rolls Reserve's write back via the marker and clears the marker.
func Test_nodeDeviceCache_UnassignRelease_RollsBackAndClearsMarker(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	oldBound := eventPodFrom(pod, "node-1", gpuAllocations(0, 100))
	newUnassigned := unboundPod("1")
	cache.OnPodUpdate(oldBound, newUnassigned)

	node1 := cache.getNodeDevice("node-1", false)
	assert.Empty(t, gpuMinors(node1, pod.Namespace, pod.Name), "unassign must roll back the reserved node")
	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "unassign must clear the marker")
}

// forgetPod is the framework ForgetPodHandler: it rolls back the assumed snapshot (Fix for the
// leak where the framework forget path left the marker dangling) and is idempotent.
func Test_nodeDeviceCache_ForgetPod_RollsBackAssumedAndIdempotent(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	cache.forgetPod(pod)
	node1 := cache.getNodeDevice("node-1", false)
	assert.Empty(t, gpuMinors(node1, pod.Namespace, pod.Name), "framework forget must roll back the assumed write")
	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "framework forget must clear the marker")

	// Idempotent: a second forget is a safe no-op, never a double subtract.
	cache.forgetPod(pod)
	assert.Empty(t, gpuMinors(node1, pod.Namespace, pod.Name))
}

// The framework ForgetPod (arbitration failure) and the informer unassign event fire
// asynchronously in either order; both must net exactly one subtract and never go negative.
func Test_nodeDeviceCache_ForgetAndUnassign_IdempotentEitherOrder(t *testing.T) {
	// forget first, then the unassign informer update.
	t.Run("forget then unassign", func(t *testing.T) {
		cache := newNodeDeviceCache(nil)
		pod := assumeTestPod("1", "node-1")
		reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
		assert.NoError(t, cache.AssumePod(pod, "node-1"))

		cache.forgetPod(pod)
		cache.OnPodUpdate(eventPodFrom(pod, "node-1", gpuAllocations(0, 100)), unboundPod("1"))

		node1 := cache.getNodeDevice("node-1", false)
		assert.Empty(t, gpuMinors(node1, pod.Namespace, pod.Name))
		_, ok := assumedEntry(cache, pod.UID)
		assert.False(t, ok)
	})

	// unassign informer update first, then forget.
	t.Run("unassign then forget", func(t *testing.T) {
		cache := newNodeDeviceCache(nil)
		pod := assumeTestPod("1", "node-1")
		reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
		assert.NoError(t, cache.AssumePod(pod, "node-1"))

		cache.OnPodUpdate(eventPodFrom(pod, "node-1", gpuAllocations(0, 100)), unboundPod("1"))
		cache.forgetPod(pod)

		node1 := cache.getNodeDevice("node-1", false)
		assert.Empty(t, gpuMinors(node1, pod.Namespace, pod.Name))
		_, ok := assumedEntry(cache, pod.UID)
		assert.False(t, ok)
	})
}

func Test_nodeDeviceCache_OnPodDelete_DeleteBeforeBind(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	// Delete arrives before any add/update landed; the delete object carries NO allocation
	// annotation, proving the rollback relies on the assumed snapshot, not deletePod reading
	// annotations.
	deletePod := pod.DeepCopy()
	cache.OnPodDelete(deletePod)

	node1 := cache.getNodeDevice("node-1", false)
	assert.Empty(t, gpuMinors(node1, pod.Namespace, pod.Name), "assumed write must be rolled back")
	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "assumed marker must be cleared")
}

func Test_nodeDeviceCache_OnPodAdd_NotAssumed_PassThrough(t *testing.T) {
	// A pod that was never assumed follows the ordinary updatePod path from its annotations.
	cache := newNodeDeviceCache(nil)
	pod := eventPodFrom(assumeTestPod("1", "node-1"), "node-1", gpuAllocations(0, 100))

	cache.OnPodAdd(pod)

	node1 := cache.getNodeDevice("node-1", false)
	assert.ElementsMatch(t, []int{0}, gpuMinors(node1, pod.Namespace, pod.Name))
}

// While a pod is assumed, a positive update is suppressed: the ledger stays at Reserve's write
// and the event's (untrusted) annotation is not applied. Reserve owns the accounting until a
// negative event rolls it back.
func Test_nodeDeviceCache_OnPodUpdate_PositiveWhileAssumed_Suppressed(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	// Reserve wrote minor 0; the assumed snapshot records that.
	reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	// A bound update whose annotation claims a different minor must NOT be applied while assumed.
	oldPod := eventPodFrom(pod, "node-1", gpuAllocations(0, 100))
	newPod := eventPodFrom(pod, "node-1", gpuAllocations(1, 50))
	cache.OnPodUpdate(oldPod, newPod)

	node1 := cache.getNodeDevice("node-1", false)
	assert.ElementsMatch(t, []int{0}, gpuMinors(node1, pod.Namespace, pod.Name),
		"positive update while assumed must not re-add or mutate the ledger")
	_, ok := assumedEntry(cache, pod.UID)
	assert.True(t, ok, "positive update must leave the marker in place")
}
