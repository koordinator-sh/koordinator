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
	"encoding/json"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	topologyclientsetfake "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func Test_syncNodeResourceTopology(t *testing.T) {
	client := topologyclientsetfake.NewSimpleClientset()
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	r := &nodeTopoInformer{
		topologyClient: client,
		nodeInformer: &nodeInformer{
			node: testNode,
		},
	}
	r.createNodeTopoIfNotExist()

	topologyName := testNode.Name

	topology, err := client.TopologyV1alpha1().NodeResourceTopologies().Get(context.TODO(), topologyName, metav1.GetOptions{})

	assert.Equal(t, nil, err)
	assert.Equal(t, topologyName, topology.Name)
	assert.Equal(t, "Koordinator", topology.Labels[extension.LabelManagedBy])
}

func Test_calGuaranteedCpu(t *testing.T) {
	testCases := []struct {
		name              string
		podMap            map[string]*PodMeta
		checkpointContent string
		expectedError     bool
		expectedPodAllocs []extension.PodCPUAlloc
	}{
		{
			name:              "Restore non-existing checkpoint",
			checkpointContent: "",
			expectedError:     true,
			expectedPodAllocs: nil,
		},
		{
			name: "Restore empty entry",
			checkpointContent: `{
				"policyName": "none",
				"defaultCPUSet": "4-6",
				"entries": {},
				"checksum": 354655845
			}`,
			expectedError:     false,
			expectedPodAllocs: nil,
		},
		{
			name:              "Restore checkpoint with invalid JSON",
			checkpointContent: `{`,
			expectedError:     true,
			expectedPodAllocs: nil,
		},
		{
			name: "Restore checkpoint with normal assignment entry",
			checkpointContent: `{
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
			expectedError: false,
			expectedPodAllocs: []extension.PodCPUAlloc{
				{
					UID:              "pod",
					CPUSet:           "1-3",
					ManagedByKubelet: true,
				},
			},
		},
		{
			name: "Filter Managed Pods",
			checkpointContent: `
				{
				    "policyName": "none",
				    "defaultCPUSet": "1-8",
				    "entries": {
				        "pod": {
				            "container1": "1-2",
				            "container2": "2-3"
				        },
				        "LSPod": {
				            "container1": "3-4"   
				        },
				        "BEPod": {
				            "container1": "4-5"   
				        },
				        "LSRPod": {
				            "container1": "5-6"   
				        },
				        "LSEPod": {
				            "container1": "6-7"   
				        }
				    },
				    "checksum": 962272150
				}`,
			podMap: map[string]*PodMeta{
				"pod": {
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "default",
							Name:      "test-pod",
							UID:       types.UID("pod"),
						},
					},
				},
				"LSPod": {
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "default",
							Name:      "test-ls-pod",
							UID:       types.UID("LSPod"),
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLS),
							},
							Annotations: map[string]string{
								extension.AnnotationResourceStatus: `{"cpuset": "3-4"}`,
							},
						},
					},
				},
				"BEPod": {
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "default",
							Name:      "test-be-pod",
							UID:       types.UID("BEPod"),
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSBE),
							},
						},
					},
				},
				"LSRPod": {
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "default",
							Name:      "test-lsr-pod",
							UID:       types.UID("LSRPod"),
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSR),
							},
							Annotations: map[string]string{
								extension.AnnotationResourceStatus: `{"cpuset": "4-5"}`,
							},
						},
					},
				},
				"LSEPod": {
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "default",
							Name:      "test-lse-pod",
							UID:       types.UID("LSEPod"),
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSE),
							},
							Annotations: map[string]string{
								extension.AnnotationResourceStatus: `{"cpuset": "5-6"}`,
							},
						},
					},
				},
			},
			expectedError: false,
			expectedPodAllocs: []extension.PodCPUAlloc{
				{
					Namespace:        "default",
					Name:             "test-pod",
					UID:              "pod",
					CPUSet:           "1-3",
					ManagedByKubelet: true,
				},
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			s := &nodeTopoInformer{
				podsInformer: &podsInformer{
					podMap: tt.podMap,
				},
			}
			podAllocs, err := s.calGuaranteedCpu(map[int32]*extension.CPUInfo{}, tt.checkpointContent)
			assert.Equal(t, tt.expectedError, err != nil)
			assert.Equal(t, tt.expectedPodAllocs, podAllocs)
		})
	}
}

func Test_reportNodeTopology(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		// prepare feature map
		enabled := features.DefaultKoordletFeatureGate.Enabled(features.NodeTopologyReport)
		testFeatureGates := map[string]bool{string(features.NodeTopologyReport): true}
		err := features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
		assert.NoError(t, err)
		defer func() {
			testFeatureGates[string(features.NodeTopologyReport)] = enabled
			err = features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
			assert.NoError(t, err)
		}()

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
		_, err = client.TopologyV1alpha1().NodeResourceTopologies().Create(context.TODO(), &mockTopology, metav1.CreateOptions{})
		assert.Equal(t, nil, err)

		ctl := gomock.NewController(t)
		defer ctl.Finish()
		mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
		mockNodeCPUInfo := metriccache.NodeCPUInfo{
			ProcessorInfos: []util.ProcessorInfo{
				{CPUID: 0, CoreID: 0, NodeID: 0, SocketID: 0},
				{CPUID: 1, CoreID: 0, NodeID: 0, SocketID: 0},
				{CPUID: 2, CoreID: 1, NodeID: 0, SocketID: 0},
				{CPUID: 3, CoreID: 1, NodeID: 0, SocketID: 0},
				{CPUID: 4, CoreID: 2, NodeID: 1, SocketID: 1},
				{CPUID: 5, CoreID: 2, NodeID: 1, SocketID: 1},
				{CPUID: 6, CoreID: 3, NodeID: 1, SocketID: 1},
				{CPUID: 7, CoreID: 3, NodeID: 1, SocketID: 1},
			},
		}

		mockPodMeta := map[string]*PodMeta{
			"pod1": {
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod1",
						Namespace: "ns1",
						Annotations: map[string]string{
							extension.AnnotationResourceStatus: `{"cpuset": "4-5" }`,
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
							extension.AnnotationResourceStatus: `{"cpuset": "3" }`,
						},
					},
				},
			},
		}
		mockMetricCache.EXPECT().GetNodeCPUInfo(gomock.Any()).Return(&mockNodeCPUInfo, nil).Times(1)
		r := &nodeTopoInformer{
			topologyClient: client,
			metricCache:    mockMetricCache,
			podsInformer: &podsInformer{
				podMap: mockPodMeta,
			},
			nodeInformer: &nodeInformer{
				node: testNode,
			},
			callbackRunner: NewCallbackRunner(),
			kubelet: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "static",
					KubeReserved: map[string]string{
						"cpu": "2000m",
					},
				},
			},
		}

		// reporting enabled
		r.reportNodeTopology()

		topology, err := client.TopologyV1alpha1().NodeResourceTopologies().Get(context.TODO(), topologyName, metav1.GetOptions{})
		assert.Equal(t, nil, err)

		expectKubeletCPUManagerPolicy := extension.KubeletCPUManagerPolicy{
			Policy:       "static",
			ReservedCPUs: "0-1",
		}
		var kubeletCPUManagerPolicy extension.KubeletCPUManagerPolicy
		err = json.Unmarshal([]byte(topology.Annotations[extension.AnnotationKubeletCPUManagerPolicy]), &kubeletCPUManagerPolicy)
		assert.NoError(t, err)
		assert.Equal(t, expectKubeletCPUManagerPolicy, kubeletCPUManagerPolicy)

		assert.Equal(t, `[{"socket":0,"node":0,"cpuset":"0-2"},{"socket":1,"node":1,"cpuset":"6-7"}]`, topology.Annotations[extension.AnnotationNodeCPUSharedPools])
		assert.Equal(t, `{"detail":[{"id":0,"core":0,"socket":0,"node":0},{"id":1,"core":0,"socket":0,"node":0},{"id":2,"core":1,"socket":0,"node":0},{"id":3,"core":1,"socket":0,"node":0},{"id":4,"core":2,"socket":1,"node":1},{"id":5,"core":2,"socket":1,"node":1},{"id":6,"core":3,"socket":1,"node":1},{"id":7,"core":3,"socket":1,"node":1}]}`, topology.Annotations[extension.AnnotationNodeCPUTopology])

		// reporting disabled
		testFeatureGates[string(features.NodeTopologyReport)] = false
		err = features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
		assert.NoError(t, err)
		// expect not CREATE/GET/UPDATE any more
		r.topologyClient = nil
		mockMetricCache.EXPECT().GetNodeCPUInfo(gomock.Any()).Return(&mockNodeCPUInfo, nil).Times(1)
		r.reportNodeTopology()
	})
}
