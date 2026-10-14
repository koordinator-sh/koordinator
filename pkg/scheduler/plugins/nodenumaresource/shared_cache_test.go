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

package nodenumaresource

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func runningLSRPodOnNode(uid, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Namespace: "default",
			Name:      uid,
			Labels:    map[string]string{extension.LabelPodQoS: string(extension.QoSLSR)},
			Annotations: map[string]string{
				extension.AnnotationResourceSpec:   `{"preferredCPUBindPolicy": "FullPCPUs"}`,
				extension.AnnotationResourceStatus: `{"cpuset": "0-3"}`,
			},
		},
		Spec:   corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// Test_resourceManager_SharedPluginCache_PodAndNodeEvents pins the SharedPluginCache method set on
// *resourceManager: OnPodAdd/OnPodUpdate credit the pod's allocation, OnPodDelete reverts it, node
// add/update are no-ops, and OnNodeDelete drops the node's allocation (replacing the removed inline
// node-delete informer handler).
func Test_resourceManager_SharedPluginCache_PodAndNodeEvents(t *testing.T) {
	tom := NewTopologyOptionsManager()
	tom.UpdateTopologyOptions("test-node-1", func(options *TopologyOptions) {
		options.CPUTopology = buildCPUTopologyForTest(2, 2, 4, 2)
	})
	rm := &resourceManager{
		topologyOptionsManager: tom,
		nodeAllocations:        map[string]*NodeAllocation{},
	}

	pod := runningLSRPodOnNode("pod-1", "test-node-1")

	// OnPodAdd credits the allocation.
	rm.OnPodAdd(pod)
	cpus, ok := rm.GetAllocatedCPUSet("test-node-1", pod.UID)
	assert.True(t, ok, "OnPodAdd must credit the pod")
	assert.Equal(t, cpuset.MustParse("0-3"), cpus)

	// OnPodUpdate for the same UID is idempotent (release-then-add by UID), not additive.
	rm.OnPodUpdate(pod, pod)
	cpus, ok = rm.GetAllocatedCPUSet("test-node-1", pod.UID)
	assert.True(t, ok)
	assert.Equal(t, cpuset.MustParse("0-3"), cpus)

	// Node add/update are no-ops.
	rm.OnNodeAdd(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node-1"}})
	rm.OnNodeUpdate(nil, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node-1"}})
	_, ok = rm.GetAllocatedCPUSet("test-node-1", pod.UID)
	assert.True(t, ok, "node add/update must not disturb allocations")

	// OnNodeDelete drops the whole node allocation.
	rm.OnNodeDelete(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node-1"}})
	rm.lock.Lock()
	_, present := rm.nodeAllocations["test-node-1"]
	rm.lock.Unlock()
	assert.False(t, present, "OnNodeDelete must drop the node's allocation")

	// OnPodDelete on a fresh add reverts the allocation.
	pod2 := runningLSRPodOnNode("pod-2", "test-node-1")
	rm.OnPodAdd(pod2)
	rm.OnPodDelete(pod2)
	_, ok = rm.GetAllocatedCPUSet("test-node-1", pod2.UID)
	assert.False(t, ok, "OnPodDelete must revert the allocation")
}

// Test_New_SharedResourceManagerAcrossProfiles pins that the resourceManager (and the
// topologyOptionsManager it owns) is created exactly once and shared across profiles built from the
// same FrameworkExtenderFactory, rather than duplicated per profile.
func Test_New_SharedResourceManagerAcrossProfiles(t *testing.T) {
	suit := newPluginTestSuit(t, nil, nil)

	p1, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	p2, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)

	pl1 := p1.(*Plugin)
	pl2 := p2.(*Plugin)
	assert.True(t, pl1.resourceManager == pl2.resourceManager,
		"both profiles must share a single resourceManager instance")
	assert.True(t, pl1.topologyOptionsManager == pl2.topologyOptionsManager,
		"both profiles must share a single topologyOptionsManager instance")
}

// Test_SharedResourceManager_DispatcherPopulatesFromPodInformer proves the end-to-end wiring: after
// StartSharedCaches registers the unified dispatcher, a pod informer event reaches the shared
// resourceManager's OnPodAdd and credits the allocation.
func Test_SharedResourceManager_DispatcherPopulatesFromPodInformer(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node-1"}}
	suit := newPluginTestSuit(t, nil, []*corev1.Node{node})

	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	// Seed the shared topology so resourceManager.Update accepts the allocation.
	pl.topologyOptionsManager.UpdateTopologyOptions("test-node-1", func(options *TopologyOptions) {
		options.CPUTopology = buildCPUTopologyForTest(2, 2, 4, 2)
	})

	suit.start(t)

	pod := runningLSRPodOnNode("pod-1", "test-node-1")
	_, err = suit.Handle.ClientSet().CoreV1().Pods("default").Create(context.TODO(), pod, metav1.CreateOptions{})
	assert.NoError(t, err)

	assert.Eventually(t, func() bool {
		_, ok := pl.resourceManager.GetAllocatedCPUSet("test-node-1", pod.UID)
		return ok
	}, 10*time.Second, 20*time.Millisecond, "the dispatcher must deliver the pod add to the shared resourceManager")

	cpus, ok := pl.resourceManager.GetAllocatedCPUSet("test-node-1", pod.UID)
	assert.True(t, ok)
	assert.Equal(t, cpuset.MustParse("0-3"), cpus)
}

// Test_SharedResourceManager_PerProfileNUMAAllocateStrategy pins @ZiMengSheng's review: although the
// resourceManager is shared across profiles, each profile must still allocate CPUs with ITS OWN
// NUMAScoringStrategy. The strategy is per-profile config and must not be captured by the shared
// cache (it rides through ResourceOptions, resolved per profile in getResourceOptions). Two profiles
// with opposite strategies, sharing one cache, must diverge on the same node+pre-allocation: given
// NUMA node 0 already fuller, MostAllocated packs into it while LeastAllocated spreads to node 1.
func Test_SharedResourceManager_PerProfileNUMAAllocateStrategy(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node-1", Labels: map[string]string{}},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("16"),
			corev1.ResourceMemory: resource.MustParse("512Gi"),
		}},
	}
	suit := newPluginTestSuit(t, nil, []*corev1.Node{node})

	mkArgs := func(st schedulingconfig.ScoringStrategyType) *schedulingconfig.NodeNUMAResourceArgs {
		a := suit.nodeNUMAResourceArgs.DeepCopy()
		a.NUMAScoringStrategy = &schedulingconfig.ScoringStrategy{Type: st}
		return a
	}
	pMost, err := suit.proxyNew(context.TODO(), mkArgs(schedulingconfig.MostAllocated), suit.Handle)
	assert.NoError(t, err)
	pLeast, err := suit.proxyNew(context.TODO(), mkArgs(schedulingconfig.LeastAllocated), suit.Handle)
	assert.NoError(t, err)
	plMost, plLeast := pMost.(*Plugin), pLeast.(*Plugin)

	// Both profiles share the one resourceManager instance (the point of the migration)...
	assert.True(t, plMost.resourceManager == plLeast.resourceManager, "profiles must share a single resourceManager")

	rm := plMost.resourceManager.(*resourceManager)
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2) // NUMA node 0 = CPUs 0-7, node 1 = CPUs 8-15
	plMost.topologyOptionsManager.UpdateTopologyOptions("test-node-1", func(o *TopologyOptions) {
		o.CPUTopology = cpuTopology
		for i := 0; i < cpuTopology.NumNodes; i++ {
			o.NUMANodeResources = append(o.NUMANodeResources, NUMANodeResource{
				Node:      i,
				Resources: corev1.ResourceList{corev1.ResourceCPU: *resource.NewQuantity(int64(cpuTopology.CPUsPerNode()), resource.DecimalSI)},
			})
		}
	})
	suit.start(t)

	// Pre-allocate CPUs 0-3 (NUMA node 0's cores 0-1) so node 0 is fuller than node 1.
	resetCache := func() {
		na := NewNodeAllocation("test-node-1")
		na.addCPUs(cpuTopology, uuid.NewUUID(), cpuset.NewCPUSet(0, 1, 2, 3), schedulingconfig.CPUExclusivePolicyNone)
		rm.lock.Lock()
		rm.nodeAllocations["test-node-1"] = na
		rm.lock.Unlock()
	}

	reserve := func(pl *Plugin, uid string) cpuset.CPUSet {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID(uid), Namespace: "default", Name: uid}}
		state := &preFilterState{requestCPUBind: true, numCPUsNeeded: 4, preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs}
		cs := framework.NewCycleState()
		cs.Write(stateKey, state)
		assert.True(t, pl.Reserve(context.TODO(), cs, pod, "test-node-1").IsSuccess())
		assert.NotNil(t, state.allocation)
		return state.allocation.CPUSet
	}

	// ...yet each profile must allocate per its own strategy. Reserve mutates the shared cache, so
	// reset it to the same starting state before each profile's allocation.
	resetCache()
	gotMost := reserve(plMost, "pod-most")
	resetCache()
	gotLeast := reserve(plLeast, "pod-least")

	node0 := cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7)
	assert.NotEqual(t, gotMost, gotLeast,
		"each profile must allocate per its own NUMAScoringStrategy despite sharing the cache (most=%v least=%v)", gotMost, gotLeast)
	assert.True(t, cpuset.NewCPUSet(4, 5, 6, 7).Equals(gotMost),
		"MostAllocated must pack into the already-fuller NUMA node 0, got %v", gotMost)
	assert.Equal(t, 0, gotLeast.Intersection(node0).Size(),
		"LeastAllocated must spread to the emptier NUMA node 1, got %v", gotLeast)
}
