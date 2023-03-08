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

package framework

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	testNodeAnnoKey = "test-anno-key"
	testNodeAnnoVal = "test-anno-val"
)

type SetNodeAnnotation struct{}

func (s *SetNodeAnnotation) Execute(strategy *extension.ColocationStrategy, node *corev1.Node, nr *NodeResource) error {
	node.Annotations[testNodeAnnoKey] = testNodeAnnoVal
	return nil
}

func Test_NodePrepareExtender(t *testing.T) {
	t.Run("prepare extender", func(t *testing.T) {
		pluginName := "test-plugin-name"
		extender := &SetNodeAnnotation{}
		if err := RegisterNodePrepareExtender(pluginName, extender); err != nil {
			t.Errorf("RegisterNodePrepareExtender() error = %v", err)
		}
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{},
			},
		}
		RunNodePrepareExtenders(nil, node, NewNodeResource())
		annoVal := node.Annotations["test-anno-key"]
		if annoVal != "test-anno-val" {
			t.Errorf("runNodePrepareExtenders got node anno %v, want %v", annoVal, testNodeAnnoVal)
		}
		UnregisterNodePrepareExtender(pluginName)
	})
}

func Test_RegisterAlreadyExistNodePrepareExtender(t *testing.T) {
	t.Run("prepare extender", func(t *testing.T) {
		pluginName := "test-plugin-name"
		extender := &SetNodeAnnotation{}
		err := RegisterNodePrepareExtender(pluginName, extender)
		assert.NoError(t, err, "register first time")
		err1 := RegisterNodePrepareExtender(pluginName, extender)
		assert.Error(t, err1, "register duplicate")
		UnregisterNodePrepareExtender(pluginName)
	})
}

var _ NodePreparePlugin = (*testNodeResourcePlugin)(nil)
var _ NodeSyncPlugin = (*testNodeResourcePlugin)(nil)
var _ ResourceCalculatePlugin = (*testNodeResourcePlugin)(nil)

type testNodeResourcePlugin struct{}

func (p *testNodeResourcePlugin) Execute(strategy *extension.ColocationStrategy, node *corev1.Node, nr *NodeResource) error {
	return nil
}

func (p *testNodeResourcePlugin) NeedSync(strategy *extension.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	return true, "always sync"
}

func (p *testNodeResourcePlugin) Reset(node *corev1.Node, msg string) []ResourceItem {
	return []ResourceItem{
		{
			Name:    "unknown",
			Reset:   true,
			Message: msg,
		},
	}
}

func (p *testNodeResourcePlugin) Calculate(strategy *extension.ColocationStrategy, node *corev1.Node, podList *corev1.PodList, metrics *ResourceMetrics) ([]ResourceItem, error) {
	return []ResourceItem{
		{
			Name:     "unknown",
			Quantity: resource.NewQuantity(0, resource.DecimalSI),
		},
	}, nil
}

func TestNodeSyncPlugin(t *testing.T) {
	t.Run("node sync extender", func(t *testing.T) {
		name := "test-plugin-name"
		plugin := &testNodeResourcePlugin{}
		err := RegisterNodeSyncExtender(name, plugin)
		assert.NoError(t, err)

		err = RegisterNodeSyncExtender(name, plugin)
		assert.Error(t, err, "register duplicated")

		assert.NotPanics(t, func() {
			UnregisterNodeSyncExtender(name)
		}, "unregistered")
	})
}

func TestResourceCalculatePlugin(t *testing.T) {
	t.Run("resource calculate extender", func(t *testing.T) {
		name := "test-plugin-name"
		plugin := &testNodeResourcePlugin{}
		err := RegisterResourceCalculateExtender(name, plugin)
		assert.NoError(t, err)

		err = RegisterResourceCalculateExtender(name, plugin)
		assert.Error(t, err, "register duplicated")

		assert.NotPanics(t, func() {
			UnregisterResourceCalculateExtender(name)
		}, "unregistered")
	})
}
