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

package impl

import (
	"testing"

	faketopologyclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclientset "k8s.io/client-go/kubernetes/fake"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	fakekoordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
)

func TestNodeInformerSetup(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		s := NewNodeInformer()
		s.Setup(&PluginOption{
			config:      NewDefaultConfig(),
			KubeClient:  fakeclientset.NewSimpleClientset(),
			KoordClient: fakekoordclientset.NewSimpleClientset(),
			TopoClient:  faketopologyclientset.NewSimpleClientset(),
			NodeName:    "test-node",
		}, &PluginState{
			callbackRunner: NewCallbackRunner(),
		})
		assert.Nil(t, s.GetNode())
	})
}

func Test_statesInformer_syncNode(t *testing.T) {
	tests := []struct {
		name string
		arg  *corev1.Node
	}{
		{
			name: "node is nil",
			arg:  nil,
		},
		{
			name: "node is incomplete",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test",
					Labels: map[string]string{},
				},
			},
		},
		{
			name: "node is valid",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-node",
					Labels: map[string]string{},
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("200Gi"),
						apiext.BatchCPU:       resource.MustParse("50000"),
						apiext.BatchMemory:    resource.MustParse("80Gi"),
					},
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("200Gi"),
						apiext.BatchCPU:       resource.MustParse("50000"),
						apiext.BatchMemory:    resource.MustParse("80Gi"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &nodeInformer{
				callbackRunner: NewCallbackRunner(),
			}
			metrics.Register(tt.arg)
			defer metrics.Register(nil)

			m.syncNode(tt.arg)
		})
	}
}
