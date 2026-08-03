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

package fragmentationaware

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/test"
)

type fakeEvictor struct {
	evicted           []*corev1.Pod
	reasons           []string
	preEvictionFilter func(pod *corev1.Pod) bool
}

func (e *fakeEvictor) Filter(pod *corev1.Pod) bool {
	return true
}

func (e *fakeEvictor) PreEvictionFilter(pod *corev1.Pod) bool {
	if e.preEvictionFilter != nil {
		return e.preEvictionFilter(pod)
	}
	return true
}

func (e *fakeEvictor) Evict(ctx context.Context, pod *corev1.Pod, opts framework.EvictOptions) bool {
	e.evicted = append(e.evicted, pod)
	e.reasons = append(e.reasons, opts.Reason)
	return true
}

type fakeHandle struct {
	evictor      *fakeEvictor
	pods         []*corev1.Pod
	getPodsErr   error
	failOnFilter bool
}

func (h *fakeHandle) Evictor() framework.Evictor {
	return h.evictor
}

func (h *fakeHandle) GetPodsAssignedToNodeFunc() framework.GetPodsAssignedToNodeFunc {
	return func(nodeName string, filter framework.FilterFunc) ([]*corev1.Pod, error) {
		if h.getPodsErr != nil {
			if !h.failOnFilter || filter != nil {
				return nil, h.getPodsErr
			}
		}
		var res []*corev1.Pod
		for _, pod := range h.pods {
			if pod.Spec.NodeName == nodeName {
				if filter == nil || filter(pod) {
					res = append(res, pod)
				}
			}
		}
		return res, nil
	}
}

func (h *fakeHandle) ClientSet() clientset.Interface                         { return nil }
func (h *fakeHandle) KubeConfig() *restclient.Config                         { return nil }
func (h *fakeHandle) EventRecorder() events.EventRecorder                    { return nil }
func (h *fakeHandle) IsDryRun() bool                                         { return false }
func (h *fakeHandle) SharedInformerFactory() informers.SharedInformerFactory { return nil }
func (h *fakeHandle) NodeSelector() *metav1.LabelSelector                    { return nil }
func (h *fakeHandle) RunDeschedulePlugins(ctx context.Context, nodes []*corev1.Node) *framework.Status {
	return nil
}
func (h *fakeHandle) RunBalancePlugins(ctx context.Context, nodes []*corev1.Node) *framework.Status {
	return nil
}

func TestFragmentationAware(t *testing.T) {
	node1 := test.BuildTestNode("node1", 4000, 4000, 10, func(n *corev1.Node) {
		n.Labels = map[string]string{"pool": "test"}
	})
	node2 := test.BuildTestNode("node2", 4000, 4000, 10, nil)

	tests := []struct {
		name              string
		args              *deschedulerconfig.FragmentationAwareArgs
		nodes             []*corev1.Node
		pods              []*corev1.Pod
		preEvictionFilter func(pod *corev1.Pod) bool
		expectedEvicted   []string
	}{
		{
			name: "paused plugin does nothing",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Paused:             true,
				Resources:          []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold: 0.1,
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				test.BuildTestPod("pod1", 3000, 1000, "node1", nil),
			},
			expectedEvicted: nil,
		},
		{
			name: "nodeSelector filters nodes",
			args: &deschedulerconfig.FragmentationAwareArgs{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"pool": "test"},
				},
				Resources:          []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold: 0.1,
			},
			nodes: []*corev1.Node{node1, node2},
			pods: []*corev1.Pod{
				test.BuildTestPod("pod1", 3000, 1000, "node1", nil),
				test.BuildTestPod("pod2", 3000, 1000, "node2", nil),
			},
			expectedEvicted: []string{"pod1"},
		},
		{
			name: "PodSelectors filter pods",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.1,
				MinImprovementThreshold: 0.01,
				PodSelectors: []deschedulerconfig.FragmentationAwarePodSelector{
					{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"foo": "bar"},
						},
					},
					{
						Selector: nil,
					},
				},
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				test.BuildTestPod("pod1", 2000, 500, "node1", func(pod *corev1.Pod) { pod.UID = "uid1"; pod.Labels = map[string]string{"foo": "bar"} }),
				test.BuildTestPod("pod2", 1000, 500, "node1", func(pod *corev1.Pod) { pod.UID = "uid2" }),
			},
			expectedEvicted: []string{"pod1"},
		},
		{
			name: "node below imbalanceThreshold evicts nothing",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:          []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold: 0.5,
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				test.BuildTestPod("pod1", 2000, 2000, "node1", nil),
			},
			expectedEvicted: nil,
		},
		{
			name: "node above threshold chooses best-gain pod",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.1,
				MinImprovementThreshold: 0.01,
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				// node1 allocation: CPU 3000/4000 (0.75), Mem 1000/4000 (0.25). stdDev = 0.25
				// pod1: CPU 2500, Mem 500. Without it, node1 has CPU 500/4000 (0.125), Mem 500/4000 (0.125). stdDev = 0.
				// pod2: CPU 500, Mem 500. Without it, node1 has CPU 2500/4000 (0.625), Mem 500/4000 (0.125). stdDev = 0.25
				test.BuildTestPod("pod1", 2500, 500, "node1", func(pod *corev1.Pod) { pod.UID = "uid1" }),
				test.BuildTestPod("pod2", 500, 500, "node1", func(pod *corev1.Pod) { pod.UID = "uid2" }),
			},
			expectedEvicted: []string{"pod1"},
		},
		{
			name: "non-evictable pods are not candidates",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.1,
				MinImprovementThreshold: 0.01,
				EvictableNamespaces: &deschedulerconfig.Namespaces{
					Exclude: []string{"kube-system"},
				},
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				// pod1 gives best gain but is in excluded namespace
				test.BuildTestPod("pod1", 2500, 500, "node1", func(pod *corev1.Pod) { pod.Namespace = "kube-system"; pod.UID = "uid1" }),
				test.BuildTestPod("pod2", 2000, 200, "node1", func(pod *corev1.Pod) { pod.UID = "uid2" }),
			},
			expectedEvicted: []string{"pod2"},
		},
		{
			name: "non-evictable pods still count in stdBefore",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.1,
				MinImprovementThreshold: 0.01,
				EvictableNamespaces: &deschedulerconfig.Namespaces{
					Exclude: []string{"kube-system"},
				},
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				test.BuildTestPod("pod1", 2000, 100, "node1", func(pod *corev1.Pod) { pod.Namespace = "kube-system"; pod.UID = "uid1" }),
				test.BuildTestPod("pod2", 1000, 100, "node1", func(pod *corev1.Pod) { pod.UID = "uid2" }),
			},
			expectedEvicted: []string{"pod2"},
		},
		{
			name: "minImprovementThreshold blocks weak candidate",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.1,
				MinImprovementThreshold: 0.5, // requires large gain
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				test.BuildTestPod("pod1", 2500, 500, "node1", func(pod *corev1.Pod) { pod.UID = "uid1" }),
			},
			expectedEvicted: nil,
		},
		{
			name: "NodeFit skips pods that cannot fit elsewhere",
			args: &deschedulerconfig.FragmentationAwareArgs{
				NodeFit:                 true,
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.1,
				MinImprovementThreshold: 0.01,
			},
			nodes: []*corev1.Node{node1, node2},
			pods: []*corev1.Pod{
				// node2 has 4000 cpu, pod needs 5000 cpu, won't fit on node2
				test.BuildTestPod("pod1", 5000, 500, "node1", func(pod *corev1.Pod) { pod.UID = "uid1" }),
			},
			expectedEvicted: nil,
		},
		{
			name: "PreEvictionFilter false prevents eviction",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.1,
				MinImprovementThreshold: 0.01,
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				test.BuildTestPod("pod1", 2500, 500, "node1", func(pod *corev1.Pod) { pod.UID = "uid1" }),
			},
			preEvictionFilter: func(pod *corev1.Pod) bool {
				return false
			},
			expectedEvicted: nil,
		},
		{
			name: "evictBestCandidate passes useful reason",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.1,
				MinImprovementThreshold: 0.01,
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				test.BuildTestPod("pod1", 2500, 500, "node1", func(pod *corev1.Pod) { pod.UID = "uid1" }),
			},
			expectedEvicted: []string{"pod1"},
		},
		{
			name: "DryRun prevents actual eviction",
			args: &deschedulerconfig.FragmentationAwareArgs{
				DryRun:                  true,
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.1,
				MinImprovementThreshold: 0.01,
			},
			nodes: []*corev1.Node{node1},
			pods: []*corev1.Pod{
				test.BuildTestPod("pod1", 2500, 500, "node1", func(pod *corev1.Pod) { pod.UID = "uid1" }),
			},
			expectedEvicted: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evictor := &fakeEvictor{
				preEvictionFilter: tt.preEvictionFilter,
			}
			handle := &fakeHandle{
				evictor: evictor,
				pods:    tt.pods,
			}

			plugin, err := NewFragmentationAware(context.TODO(), tt.args, handle)
			assert.NoError(t, err)

			balancePlugin := plugin.(framework.BalancePlugin)
			status := balancePlugin.Balance(context.TODO(), tt.nodes)
			assert.Nil(t, status)

			var evictedNames []string
			for _, p := range evictor.evicted {
				evictedNames = append(evictedNames, p.Name)
			}
			assert.ElementsMatch(t, tt.expectedEvicted, evictedNames)

			if tt.name == "evictBestCandidate passes useful reason" && len(evictor.reasons) > 0 {
				assert.Contains(t, evictor.reasons[0], "fragmentation reduction")
			}
		})
	}
}

func TestNewFragmentationAwareErrors(t *testing.T) {
	handle := &fakeHandle{
		evictor: &fakeEvictor{},
	}

	// Invalid args type
	_, err := NewFragmentationAware(context.TODO(), &corev1.Pod{}, handle)
	assert.Error(t, err)

	// Invalid PodSelector
	_, err = NewFragmentationAware(context.TODO(), &deschedulerconfig.FragmentationAwareArgs{
		Resources: []corev1.ResourceName{corev1.ResourceCPU},
		PodSelectors: []deschedulerconfig.FragmentationAwarePodSelector{
			{
				Selector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Operator: "invalid-operator",
						},
					},
				},
			},
		},
	}, handle)
	assert.Error(t, err)

	// Invalid NodeSelector during initialization
	_, err = NewFragmentationAware(context.TODO(), &deschedulerconfig.FragmentationAwareArgs{
		Resources: []corev1.ResourceName{corev1.ResourceCPU},
		NodeSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Operator: "invalid-operator",
				},
			},
		},
	}, handle)
	assert.Error(t, err)

	// Error initializing pod filter function (intersecting namespaces)
	_, err = NewFragmentationAware(context.TODO(), &deschedulerconfig.FragmentationAwareArgs{
		Resources: []corev1.ResourceName{corev1.ResourceCPU},
		EvictableNamespaces: &deschedulerconfig.Namespaces{
			Include: []string{"default"},
			Exclude: []string{"default"},
		},
	}, handle)
	assert.Error(t, err)
}

func TestBalanceInvalidNodeSelector(t *testing.T) {
	node1 := test.BuildTestNode("node1", 4000, 4000, 10, nil)
	handle := &fakeHandle{
		evictor: &fakeEvictor{},
	}
	plugin, _ := NewFragmentationAware(context.TODO(), &deschedulerconfig.FragmentationAwareArgs{
		Resources: []corev1.ResourceName{corev1.ResourceCPU},
	}, handle)

	// artificially inject invalid NodeSelector after initialization
	plugin.(*FragmentationAware).args.NodeSelector = &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Operator: "invalid-operator"},
		},
	}

	status := plugin.(framework.BalancePlugin).Balance(context.TODO(), []*corev1.Node{node1})
	assert.NotNil(t, status)
	assert.NotNil(t, status.Err)
}

func TestBalanceListPodsError(t *testing.T) {
	node1 := test.BuildTestNode("node1", 4000, 4000, 10, nil)

	// Error on first call
	handle1 := &fakeHandle{
		evictor:    &fakeEvictor{},
		getPodsErr: fmt.Errorf("mock error"),
	}
	plugin1, _ := NewFragmentationAware(context.TODO(), &deschedulerconfig.FragmentationAwareArgs{
		Resources: []corev1.ResourceName{corev1.ResourceCPU},
	}, handle1)
	status1 := plugin1.(framework.BalancePlugin).Balance(context.TODO(), []*corev1.Node{node1})
	assert.Nil(t, status1)

	// Error on second call
	handle2 := &fakeHandle{
		evictor:      &fakeEvictor{},
		getPodsErr:   fmt.Errorf("mock error 2"),
		failOnFilter: true,
	}
	plugin2, _ := NewFragmentationAware(context.TODO(), &deschedulerconfig.FragmentationAwareArgs{
		Resources: []corev1.ResourceName{corev1.ResourceCPU},
	}, handle2)
	status2 := plugin2.(framework.BalancePlugin).Balance(context.TODO(), []*corev1.Node{node1})
	assert.Nil(t, status2)
}
