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

package elasticquota

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPlugin_OnNodeAdd(t *testing.T) {
	tests := []struct {
		name     string
		nodes    []*corev1.Node
		totalRes corev1.ResourceList
	}{
		{
			name:     "add invalid node",
			nodes:    []*corev1.Node{},
			totalRes: corev1.ResourceList{},
		},
		{
			name:     "add invalid node, enable profile",
			nodes:    []*corev1.Node{},
			totalRes: corev1.ResourceList{},
		},
		{
			name: "add invalid node 2",
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
					Status: corev1.NodeStatus{
						Allocatable: createResourceList(100, 1000),
					},
				},
			},
			totalRes: corev1.ResourceList{},
		},
		{
			name: "add normal node",
			nodes: []*corev1.Node{
				defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
				defaultCreateNodeWithLabels("node2", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-b"}),
			},
			totalRes: createResourceList(200, 2000),
		},
		{
			name: "add same node twice",
			nodes: []*corev1.Node{
				defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
				defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
			},
			totalRes: createResourceList(100, 1000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)
			plugin := p.(*Plugin)

			time.Sleep(100 * time.Millisecond)
			for _, node := range tt.nodes {
				plugin.OnNodeAdd(node)
			}
			gqm := plugin.groupQuotaManager
			assert.NotNil(t, gqm)
			assert.Equal(t, tt.totalRes, gqm.GetClusterTotalResource())
		})
	}
}

func TestPlugin_OnNodeUpdate(t *testing.T) {
	nodes := []*corev1.Node{
		defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
		defaultCreateNodeWithLabels("node2", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
		defaultCreateNodeWithLabels("node3", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-b"}),
	}
	tests := []struct {
		name     string
		nodes    []*corev1.Node
		totalRes corev1.ResourceList
	}{
		{
			name:     "update invalid node",
			nodes:    []*corev1.Node{},
			totalRes: createResourceList(300, 3000),
		},
		{
			name: "increase node resource",
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node1",
						Labels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
					Status: corev1.NodeStatus{
						Allocatable: createResourceList(200, 2000),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node2",
						Labels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
					Status: corev1.NodeStatus{
						Allocatable: createResourceList(200, 2000),
					},
				},
			},
			totalRes: createResourceList(500, 5000),
		},
		{
			name: "increase node resource, enable profile",
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node1",
						Labels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
					Status: corev1.NodeStatus{
						Allocatable: createResourceList(200, 2000),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node2",
						Labels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
					Status: corev1.NodeStatus{
						Allocatable: createResourceList(200, 2000),
					},
				},
			},
			totalRes: createResourceList(500, 5000),
		},
		{
			name: "decrease node resource",
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node1",
						Labels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
					Status: corev1.NodeStatus{
						Allocatable: createResourceList(50, 500),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node2",
						Labels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
					Status: corev1.NodeStatus{
						Allocatable: createResourceList(50, 500),
					},
				},
			},
			totalRes: createResourceList(200, 2000),
		},
		{
			name: "node not exist. we should add node",
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node4",
						Labels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"},
					},
					Status: corev1.NodeStatus{
						Allocatable: createResourceList(50, 500),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node5",
						Labels: map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-b"},
					},
					Status: corev1.NodeStatus{
						Allocatable: createResourceList(50, 500),
					},
				},
			},
			totalRes: createResourceList(400, 4000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
			assert.Nil(t, err)
			plugin := p.(*Plugin)

			for _, node := range nodes {
				plugin.OnNodeAdd(node)
			}

			for i, node := range tt.nodes {
				plugin.OnNodeUpdate(nodes[i], node)
			}
			assert.Equal(t, tt.totalRes, plugin.groupQuotaManager.GetClusterTotalResource())
		})
	}
}

func TestPlugin_OnNodeDelete(t *testing.T) {
	nodes := []*corev1.Node{
		defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
		defaultCreateNodeWithLabels("node2", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
		defaultCreateNodeWithLabels("node3", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-b"}),
	}
	tests := []struct {
		name     string
		toDelete []*corev1.Node
		totalRes corev1.ResourceList
	}{
		{
			name: "delete node1",
			toDelete: []*corev1.Node{
				defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
			},
			totalRes: createResourceList(200, 2000),
		},
		{
			name: "delete node1/node2",
			toDelete: []*corev1.Node{
				defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
				defaultCreateNodeWithLabels("node2", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
			},
			totalRes: createResourceList(100, 1000),
		},
		{
			name: "delete node1/node2/node3",
			toDelete: []*corev1.Node{
				defaultCreateNodeWithLabels("node1", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
				defaultCreateNodeWithLabels("node2", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-a"}),
				defaultCreateNodeWithLabels("node3", map[string]string{"topology.kubernetes.io/zone": "cn-hangzhou-b"}),
			},
			totalRes: createResourceList(0, 0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
			assert.Nil(t, err)
			plugin := p.(*Plugin)

			for _, node := range nodes {
				plugin.OnNodeAdd(node)
			}

			for _, node := range tt.toDelete {
				plugin.OnNodeDelete(node)
			}

			assert.Equal(t, tt.totalRes, plugin.groupQuotaManager.GetClusterTotalResource())
		})
	}
}
