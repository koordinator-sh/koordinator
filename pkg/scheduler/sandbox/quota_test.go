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

package sandbox

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

type testEquivalenceCapacityPlugin struct {
	quota    int64
	reusable bool
	handled  bool
}

func (p *testEquivalenceCapacityPlugin) Name() string {
	return "TestEquivalenceCapacity"
}

func (p *testEquivalenceCapacityPlugin) EquivalenceCapacity(context.Context, fwktype.CycleState, *corev1.Pod, fwktype.NodeInfo) (int64, bool, bool) {
	return p.quota, p.reusable, p.handled
}

type fakeNodeInfoLister struct {
	infos map[string]fwktype.NodeInfo
}

func (f fakeNodeInfoLister) List() ([]fwktype.NodeInfo, error) {
	out := make([]fwktype.NodeInfo, 0, len(f.infos))
	for _, info := range f.infos {
		out = append(out, info)
	}
	return out, nil
}

func (f fakeNodeInfoLister) HavePodsWithAffinityList() ([]fwktype.NodeInfo, error) {
	return nil, nil
}

func (f fakeNodeInfoLister) HavePodsWithRequiredAntiAffinityList() ([]fwktype.NodeInfo, error) {
	return nil, nil
}

func (f fakeNodeInfoLister) Get(name string) (fwktype.NodeInfo, error) {
	info, ok := f.infos[name]
	if !ok {
		return nil, fmt.Errorf("nodeinfo not found for node %q", name)
	}
	return info, nil
}

type fakeSharedLister struct {
	lister fakeNodeInfoLister
}

func (f fakeSharedLister) NodeInfos() fwktype.NodeInfoLister { return f.lister }
func (f fakeSharedLister) StorageInfos() fwktype.StorageInfoLister {
	return nil
}

func makeQuotaPod(cpu, memory string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpu),
						corev1.ResourceMemory: resource.MustParse(memory),
					},
				},
			}},
		},
	}
}

func makeQuotaNodeInfo(name string, cpu, memory string, pods int64, occupants ...*corev1.Pod) fwktype.NodeInfo {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
				corev1.ResourcePods:   *resource.NewQuantity(pods, resource.DecimalSI),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
				corev1.ResourcePods:   *resource.NewQuantity(pods, resource.DecimalSI),
			},
		},
	}
	info := framework.NewNodeInfo(occupants...)
	info.SetNode(node)
	return info
}

func TestNodeQuotaForPod(t *testing.T) {
	pod := makeQuotaPod("100m", "100Mi")

	t.Run("min across dimensions", func(t *testing.T) {
		// cpu: 4000/100 = 40, memory: 8Gi/100Mi = 81, pods: 10 -> quota 10.
		info := makeQuotaNodeInfo("n1", "4", "8Gi", 10)
		assert.Equal(t, int64(10), nodeQuotaForPod(mustPodRequests(t, pod), info))
	})

	t.Run("existing occupants count against the quota", func(t *testing.T) {
		// Two occupants take 2 pod slots and 200m/200Mi; pods dimension binds first: 10-2=8.
		occupants := []*corev1.Pod{makeQuotaPod("100m", "100Mi"), makeQuotaPod("100m", "100Mi")}
		info := makeQuotaNodeInfo("n1", "4", "8Gi", 10, occupants...)
		assert.Equal(t, int64(8), nodeQuotaForPod(mustPodRequests(t, pod), info))
	})

	t.Run("cpu dimension can bind", func(t *testing.T) {
		// One occupant eats 3900m; remaining 100m cpu fits exactly one more pod.
		occupant := makeQuotaPod("3900m", "100Mi")
		info := makeQuotaNodeInfo("n1", "4", "8Gi", 110, occupant)
		assert.Equal(t, int64(1), nodeQuotaForPod(mustPodRequests(t, pod), info))
	})

	t.Run("full node gets zero quota", func(t *testing.T) {
		occupant := makeQuotaPod("4", "8Gi")
		info := makeQuotaNodeInfo("n1", "4", "8Gi", 110, occupant)
		assert.Equal(t, int64(0), nodeQuotaForPod(mustPodRequests(t, pod), info))
	})

	t.Run("extended resource dimension", func(t *testing.T) {
		podWithBatch := makeQuotaPod("100m", "100Mi")
		podWithBatch.Spec.Containers[0].Resources.Requests[apiext.BatchCPU] = resource.MustParse("2")
		// Build a node info whose allocatable carries the batch-cpu scalar resource.
		n := makeQuotaNodeInfo("n1", "4", "8Gi", 110)
		internal, ok := n.(*framework.NodeInfo)
		assert.True(t, ok)
		if internal.Allocatable.ScalarResources == nil {
			internal.Allocatable.ScalarResources = map[corev1.ResourceName]int64{}
		}
		internal.Allocatable.ScalarResources[apiext.BatchCPU] = 10
		// batch-cpu: 10/2 = 5 binds below cpu(40)/pods(110).
		assert.Equal(t, int64(5), nodeQuotaForPod(mustPodRequests(t, podWithBatch), n))
	})
}

func TestBuildQuotaNodes(t *testing.T) {
	pod := makeQuotaPod("100m", "100Mi")
	lister := fakeSharedLister{lister: fakeNodeInfoLister{infos: map[string]fwktype.NodeInfo{
		"fits":   makeQuotaNodeInfo("fits", "4", "8Gi", 10),
		"full":   makeQuotaNodeInfo("full", "4", "8Gi", 10, makeQuotaPod("4", "8Gi")),
		"zero":   makeQuotaNodeInfo("zero", "4", "8Gi", 1, makeQuotaPod("100m", "100Mi")),
		"goneOK": makeQuotaNodeInfo("goneOK", "4", "8Gi", 10),
	}}}

	nodes := buildQuotaNodes(pod, []string{"fits", "full", "zero", "missing"}, lister)

	assert.Equal(t, []equivalenceClassNode{{name: "fits", quota: 10}}, nodes,
		"full/zero-quota/missing nodes must not enter the cache")
}

func TestBuildQuotaNodesWithCapacityPlugin(t *testing.T) {
	pod := makeQuotaPod("100m", "100Mi")
	lister := fakeSharedLister{lister: fakeNodeInfoLister{infos: map[string]fwktype.NodeInfo{
		"n1": makeQuotaNodeInfo("n1", "4", "8Gi", 10),
	}}}

	plugin := &testEquivalenceCapacityPlugin{quota: 2, reusable: true, handled: true}
	nodes := buildQuotaNodesWithPlugins(
		context.Background(),
		nil,
		pod,
		[]string{"n1"},
		lister,
		[]frameworkext.EquivalenceCapacityPlugin{plugin},
	)
	assert.Equal(t, []equivalenceClassNode{{name: "n1", quota: 2}}, nodes)
}

func TestBuildQuotaNodesWithCapacityPluginCanDisableReuse(t *testing.T) {
	pod := makeQuotaPod("100m", "100Mi")
	lister := fakeSharedLister{lister: fakeNodeInfoLister{infos: map[string]fwktype.NodeInfo{
		"n1": makeQuotaNodeInfo("n1", "4", "8Gi", 10),
	}}}

	plugin := &testEquivalenceCapacityPlugin{reusable: false, handled: true}
	nodes := buildQuotaNodesWithPlugins(
		context.Background(),
		nil,
		pod,
		[]string{"n1"},
		lister,
		[]frameworkext.EquivalenceCapacityPlugin{plugin},
	)
	assert.Empty(t, nodes)
}

func mustPodRequests(t *testing.T, pod *corev1.Pod) corev1.ResourceList {
	t.Helper()
	reqs := podRequestsForQuota(pod)
	assert.NotEmpty(t, reqs)
	return reqs
}
