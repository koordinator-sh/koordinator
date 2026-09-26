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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/koordinator-sh/koordinator/apis/extension"
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
