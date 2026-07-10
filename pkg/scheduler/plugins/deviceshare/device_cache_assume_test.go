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

func Test_nodeDeviceCache_OnPodAdd_Reconcile(t *testing.T) {
	tests := []struct {
		name         string
		assumedNode  string
		assumedAlloc apiext.DeviceAllocations
		eventNode    string
		eventAlloc   apiext.DeviceAllocations // nil => event carries no allocation
		wantNode1    []int                    // GPU minors expected for the pod on node-1
		wantNode2    []int                    // GPU minors expected for the pod on node-2
	}{
		{
			name:         "event agrees with reserve: net-zero on the same node",
			assumedNode:  "node-1",
			assumedAlloc: gpuAllocations(0, 100),
			eventNode:    "node-1",
			eventAlloc:   gpuAllocations(0, 100),
			wantNode1:    []int{0},
			wantNode2:    nil,
		},
		{
			name:         "event on a different node: phantom cleared, event's node credited",
			assumedNode:  "node-1",
			assumedAlloc: gpuAllocations(0, 100),
			eventNode:    "node-2",
			eventAlloc:   gpuAllocations(0, 100),
			wantNode1:    nil,
			wantNode2:    []int{0},
		},
		{
			name:         "event with mutated allocation: cache converges on the event",
			assumedNode:  "node-1",
			assumedAlloc: gpuAllocations(0, 100),
			eventNode:    "node-1",
			eventAlloc:   gpuAllocations(1, 50),
			wantNode1:    []int{1},
			wantNode2:    nil,
		},
		{
			name:         "event with empty allocation: assumed rollback only",
			assumedNode:  "node-1",
			assumedAlloc: gpuAllocations(0, 100),
			eventNode:    "node-1",
			eventAlloc:   nil,
			wantNode1:    nil,
			wantNode2:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newNodeDeviceCache(nil)
			pod := assumeTestPod("1", tt.assumedNode)
			reserveInto(cache, tt.assumedNode, pod, tt.assumedAlloc)
			assert.NoError(t, cache.AssumePod(pod, tt.assumedNode))

			cache.OnPodAdd(eventPodFrom(pod, tt.eventNode, tt.eventAlloc))

			node1 := cache.getNodeDevice("node-1", false)
			assert.ElementsMatch(t, tt.wantNode1, gpuMinors(node1, pod.Namespace, pod.Name))
			if node2 := cache.getNodeDevice("node-2", false); node2 != nil {
				assert.ElementsMatch(t, tt.wantNode2, gpuMinors(node2, pod.Namespace, pod.Name))
			} else {
				assert.Empty(t, tt.wantNode2)
			}
			_, ok := assumedEntry(cache, pod.UID)
			assert.False(t, ok, "assumed marker must be cleared after the informer event")
		})
	}
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

func Test_nodeDeviceCache_OnPodUpdate_ReconcilesAgainstAssumedNotOldPod(t *testing.T) {
	cache := newNodeDeviceCache(nil)
	pod := assumeTestPod("1", "node-1")
	// Reserve wrote minor 0; the assumed snapshot records that.
	reserveInto(cache, "node-1", pod, gpuAllocations(0, 100))
	assert.NoError(t, cache.AssumePod(pod, "node-1"))

	// oldPod carries a stale, never-applied allocation (minor 5). The cache state is Reserve's
	// assumed write (minor 0), so reconcile must roll back the assumed snapshot and apply
	// newPod (minor 1) — oldPod's minor 5 must never be treated as truth.
	oldPod := eventPodFrom(pod, "node-1", gpuAllocations(5, 100))
	newPod := eventPodFrom(pod, "node-1", gpuAllocations(1, 50))
	cache.OnPodUpdate(oldPod, newPod)

	node1 := cache.getNodeDevice("node-1", false)
	assert.ElementsMatch(t, []int{1}, gpuMinors(node1, pod.Namespace, pod.Name),
		"cache must converge on newPod's allocation, ignoring oldPod")
	_, ok := assumedEntry(cache, pod.UID)
	assert.False(t, ok, "assumed marker must be cleared after the update event")
}
