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

	"github.com/golang/mock/gomock"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
)

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
	ctl := gomock.NewController(t)
	mockstatesinformer := mock_statesinformer.NewMockStatesInformer(ctl)
	mockstatesinformer.EXPECT().GetAllPods().Return(podMetaList).AnyTimes()
	mockstatesinformer.EXPECT().GetNode().Return(node).AnyTimes()
	mockstatesinformer.EXPECT().HasSynced().Return(true).AnyTimes()

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
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{UID: "node1"}}

	podUID := generator.Pod(pod)
	if podUID != "pod1" {
		t.Errorf("Expected pod UID to be 'pod1', got '%s'", podUID)
	}

	nodeUID := generator.Node(node)
	if nodeUID != "node1" {
		t.Errorf("Expected node UID to be 'node1', got '%s'", nodeUID)
	}
}
