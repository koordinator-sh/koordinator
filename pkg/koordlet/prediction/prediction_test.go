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

package prediction

import (
	"testing"

	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type mockStatesInformer struct {
	node *v1.Node
	pods []*statesinformer.PodMeta
}

func (m *mockStatesInformer) Run(stopCh <-chan struct{}) error {
	return nil
}

func (m *mockStatesInformer) HasSynced() bool {
	return true
}

func (m *mockStatesInformer) GetNode() *v1.Node {
	return m.node
}

func (m *mockStatesInformer) GetNodeSLO() *slov1alpha1.NodeSLO {
	return nil
}

func (m *mockStatesInformer) GetNodeMetricSpec() *slov1alpha1.NodeMetricSpec {
	return nil
}

func (m *mockStatesInformer) GetAllPods() []*statesinformer.PodMeta {
	return m.pods
}

func (m *mockStatesInformer) GetNodeTopo() *topov1alpha1.NodeResourceTopology {
	return nil

}

func (m *mockStatesInformer) GetVolumeName(pvcNamespace, pvcName string) string {
	return ""
}

func (m *mockStatesInformer) RegisterCallbacks(objType statesinformer.RegisterType, name, description string, callbackFn statesinformer.UpdateCbFn) {
}

func TestInformer(t *testing.T) {
	pod1 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "pod1"}}
	pod2 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "pod2"}}
	pod3 := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "pod3"}}
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{UID: "node1"}}
	podMetaList := []*statesinformer.PodMeta{
		{
			Pod: pod1,
		},
		{
			Pod: pod2,
		},
		{
			Pod: pod3,
		},
	}
	mockstatesinformer := &mockStatesInformer{
		node: node,
		pods: podMetaList,
	}

	informer := NewInformer(mockstatesinformer)

	if !informer.HasSynced() {
		t.Error("Expected HasSynced to return true")
	}

	pods := informer.ListPods()
	if len(pods) != 3 {
		t.Errorf("Expected 3 pods, got %d", len(pods))
	}

	if informer.GetNode() != node {
		t.Error("Expected GetNode to return the node")
	}
}

func TestUIDGenerator(t *testing.T) {
	generator := &generator{}
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "pod1"}}

	podUID := generator.Pod(pod)
	if podUID != "pod1" {
		t.Errorf("Expected pod UID to be 'pod1', got '%s'", podUID)
	}

	nodeUID := generator.Node()
	if nodeUID != DefaultNodeID {
		t.Errorf("Expected node UID to be 'node1', got '%s'", nodeUID)
	}
}
