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
	"context"
	"testing"
	"time"

	faketopologyclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	fakekoordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
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

type hookInformer struct {
	cache.SharedIndexInformer
	hook func(handlers cache.ResourceEventHandler)
}

func (h *hookInformer) AddEventHandler(handlers cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	h.hook(handlers)
	return nil, nil
}

func TestNodeInformerHasSyncedAndGetNode(t *testing.T) {
	fk := fakeclientset.NewSimpleClientset()
	old := newNodeInformer
	defer func() {
		newNodeInformer = old
	}()
	newNodeInformer = func(client clientset.Interface, nodeName string) cache.SharedIndexInformer {
		inf := old(client, nodeName)
		return &hookInformer{
			SharedIndexInformer: inf,
			hook: func(handlers cache.ResourceEventHandler) {
				wrapper := cache.ResourceEventHandlerFuncs{
					AddFunc: func(obj interface{}) {
						// delay adding event to confirm HasSynced works correctly
						time.Sleep(time.Second * 3)
						handlers.OnAdd(obj, false)
					},
					UpdateFunc: func(oldObj, newObj interface{}) {
						// delay updating event to confirm HasSynced works correctly
						time.Sleep(time.Second * 3)
						handlers.OnUpdate(oldObj, newObj)
					},
					DeleteFunc: handlers.OnDelete,
				}
				inf.AddEventHandler(wrapper)
			},
		}
	}
	s := NewNodeInformer()
	s.Setup(&PluginOption{
		config:      NewDefaultConfig(),
		KubeClient:  fk,
		KoordClient: fakekoordclientset.NewSimpleClientset(),
		TopoClient:  faketopologyclientset.NewSimpleClientset(),
		NodeName:    "test-node",
	}, &PluginState{
		callbackRunner: NewCallbackRunner(),
	})
	stopCh := make(chan struct{})
	defer close(stopCh)
	s.Start(stopCh)
	assert.False(t, s.HasSynced())
	_, err := fk.CoreV1().Nodes().Create(context.Background(), &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}, metav1.CreateOptions{})
	assert.NoError(t, err)
	notSyncedCount := 0
	for {
		if s.HasSynced() != s.nodeInformer.HasSynced() {
			notSyncedCount++
		}
		if !s.HasSynced() {
			time.Sleep(time.Second)
			continue
		}
		// must not be nil
		assert.NotNil(t, s.GetNode())
		break
	}
	assert.Equal(t, true, notSyncedCount > 0)
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

			m.syncNode(tt.arg)
		})
	}
}
