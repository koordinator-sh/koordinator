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

package statesinformer

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	topologyclientsetfake "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func Test_syncNodeResourceTopology(t *testing.T) {
	client := topologyclientsetfake.NewSimpleClientset()
	r := &statesInformer{
		topologyClient: client,
	}
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	r.syncNodeResourceTopology(testNode)

	topologyName := testNode.Name

	topology, err := client.TopologyV1alpha1().NodeResourceTopologies().Get(context.TODO(), topologyName, metav1.GetOptions{})

	assert.Equal(t, nil, err)
	assert.Equal(t, topologyName, topology.Name)
	assert.Equal(t, "Koordinator", topology.Labels[extension.LabelManagedBy])
}

func Test_calGuaranteedCpu(t *testing.T) {
	testCases := []struct {
		description       string
		checkpointContent string
		expectedError     bool
		expectedPodAllocs []extension.PodCPUAlloc
	}{
		{
			"Restore non-existing checkpoint",
			"",
			true,
			nil,
		},
		{
			"Restore empty entry",
			`{
				"policyName": "none",
				"defaultCPUSet": "4-6",
				"entries": {},
				"checksum": 354655845
			}`,
			false,
			extension.PodCPUAllocs{},
		},
		{
			"Restore checkpoint with invalid JSON",
			`{`,
			true,
			nil,
		},
		{
			"Restore checkpoint with normal assignment entry",
			`{
				"policyName": "none",
				"defaultCPUSet": "1-3",
				"entries": {
					"pod": {
						"container1": "1-2",
						"container2": "2-3"
					}
				},
				"checksum": 962272150
			}`,
			false,
			[]extension.PodCPUAlloc{
				{
					Name:   "pod",
					CPUSet: "1-3",
				},
			},
		},
	}
	s := &statesInformer{}
	for _, c := range testCases {
		podAllocs, err := s.calGuaranteedCpu(map[int32]*extension.CPUInfo{}, c.checkpointContent)
		assert.Equal(t, c.expectedError, err != nil)
		assert.Equal(t, c.expectedPodAllocs, podAllocs)
	}
}

func Test_reportNodeTopology(t *testing.T) {
	client := topologyclientsetfake.NewSimpleClientset()
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	topologyName := testNode.Name
	mockTopology := v1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Name: topologyName,
		},
		TopologyPolicies: []string{"None"},
		Zones:            v1alpha1.ZoneList{v1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
	}
	_, err := client.TopologyV1alpha1().NodeResourceTopologies().Create(context.TODO(), &mockTopology, metav1.CreateOptions{})
	assert.Equal(t, nil, err)

	ctl := gomock.NewController(t)
	mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
	mockNodeCPUInfo := metriccache.NodeCPUInfo{
		ProcessorInfos: []util.ProcessorInfo{
			{
				CPUID:    0,
				CoreID:   0,
				NodeID:   0,
				SocketID: 0,
			},
			{
				CPUID:    1,
				CoreID:   0,
				NodeID:   0,
				SocketID: 0,
			},
			{
				CPUID:    2,
				CoreID:   1,
				NodeID:   1,
				SocketID: 1,
			},
			{
				CPUID:    3,
				CoreID:   1,
				NodeID:   1,
				SocketID: 1,
			},
		},
	}

	mockPodMeta := map[string]*PodMeta{
		"pod1": {
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod1",
					Namespace: "ns1",
					Annotations: map[string]string{
						extension.AnnotationResourceStatus: "{\"cpuset\": \"0-1\" }",
					},
				},
			},
		},
		"pod2": {
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod2",
					Namespace: "ns2",
					Annotations: map[string]string{
						extension.AnnotationResourceStatus: "{\"cpuset\": \"3\" }",
					},
				},
			},
		},
	}
	mockMetricCache.EXPECT().GetNodeCPUInfo(gomock.Any()).Return(&mockNodeCPUInfo, nil).AnyTimes()
	r := &statesInformer{
		topologyClient: client,
		metricsCache:   mockMetricCache,
		podMap:         mockPodMeta,
		node:           testNode,
	}

	r.reportNodeTopology()

	topology, err := client.TopologyV1alpha1().NodeResourceTopologies().Get(context.TODO(), topologyName, metav1.GetOptions{})
	assert.Equal(t, nil, err)
	assert.Equal(t, "[{\"socket\":1,\"node\":1,\"cpuset\":\"2\"}]", topology.Annotations[extension.AnnotationNodeCPUSharedPools])
	assert.Equal(t, "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":0,\"socket\":0,\"node\":0},{\"id\":2,\"core\":1,\"socket\":1,\"node\":1},{\"id\":3,\"core\":1,\"socket\":1,\"node\":1}]}", topology.Annotations[extension.AnnotationNodeCPUTopology])
}
