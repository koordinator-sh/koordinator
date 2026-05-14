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
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	topologyv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	faketopologyclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"
	topologylister "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/listers/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	fakekoordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var _ topologylister.NodeResourceTopologyLister = &fakeNodeResourceTopologyLister{}

type fakeNodeResourceTopologyLister struct {
	nodeResourceTopologys *topologyv1alpha1.NodeResourceTopology
	getErr                error
}

func (f fakeNodeResourceTopologyLister) List(selector labels.Selector) (ret []*topologyv1alpha1.NodeResourceTopology, err error) {
	return []*topologyv1alpha1.NodeResourceTopology{f.nodeResourceTopologys}, nil
}

func (f fakeNodeResourceTopologyLister) Get(name string) (*topologyv1alpha1.NodeResourceTopology, error) {
	return f.nodeResourceTopologys, f.getErr
}

func Test_syncNodeResourceTopology(t *testing.T) {
	client := faketopologyclientset.NewSimpleClientset()
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	r := &nodeTopoInformer{
		topologyClient: client,
		nodeResourceTopologyLister: &fakeNodeResourceTopologyLister{
			nodeResourceTopologys: &topologyv1alpha1.NodeResourceTopology{
				ObjectMeta: metav1.ObjectMeta{},
			},
			getErr: errors.NewNotFound(schema.GroupResource{}, "test"),
		},
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

func Test_nodeResourceTopology_NewAndSetup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	type args struct {
		ctx   *PluginOption
		state *PluginState
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "new and setup node topo",
			args: args{
				ctx: &PluginOption{
					config:      NewDefaultConfig(),
					KubeClient:  fakeclientset.NewSimpleClientset(),
					KoordClient: fakekoordclientset.NewSimpleClientset(),
					TopoClient:  faketopologyclientset.NewSimpleClientset(),
					NodeName:    "test-node",
				},
				state: &PluginState{
					metricCache: mock_metriccache.NewMockMetricCache(ctrl),
					informerPlugins: map[PluginName]informerPlugin{
						podsInformerName: NewPodsInformer(),
						nodeInformerName: NewNodeInformer(),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewNodeTopoInformer()
			r.Setup(tt.args.ctx, tt.args.state)
		})
	}
}

func Test_calGuaranteedCpu(t *testing.T) {
	testCases := []struct {
		name              string
		podMap            map[string]*statesinformer.PodMeta
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
			podMap: map[string]*statesinformer.PodMeta{
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

func Test_calKubeletAllocatedCPUs(t *testing.T) {
	testSharePoolCPUs := map[int32]*extension.CPUInfo{
		0: {
			ID:     0,
			Core:   0,
			Socket: 0,
			Node:   0,
		},
		1: {
			ID:     1,
			Core:   1,
			Socket: 0,
			Node:   0,
		},
		2: {
			ID:     2,
			Core:   2,
			Socket: 0,
			Node:   0,
		},
		3: {
			ID:     3,
			Core:   3,
			Socket: 0,
			Node:   0,
		},
		4: {
			ID:     4,
			Core:   0,
			Socket: 0,
			Node:   0,
		},
		5: {
			ID:     5,
			Core:   1,
			Socket: 0,
			Node:   0,
		},
		6: {
			ID:     6,
			Core:   2,
			Socket: 0,
			Node:   0,
		},
		7: {
			ID:     7,
			Core:   3,
			Socket: 0,
			Node:   0,
		},
	}
	type fields struct {
		prepareFn func(helper *system.FileTestUtil)
		podMap    map[string]*statesinformer.PodMeta
	}
	tests := []struct {
		name    string
		fields  fields
		arg     map[int32]*extension.CPUInfo
		wantErr bool
		want    []extension.PodCPUAlloc
	}{
		{
			name: "cpu_manager_state not exist",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					var oldVarKubeletLibRoot string
					helper.SetConf(func(conf *system.Config) {
						oldVarKubeletLibRoot = conf.VarLibKubeletRootDir
						conf.VarLibKubeletRootDir = helper.TempDir
					}, func(conf *system.Config) {
						conf.VarLibKubeletRootDir = oldVarKubeletLibRoot
					})
				},
			},
			arg:     testSharePoolCPUs,
			wantErr: false,
			want:    nil,
		},
		{
			name: "cpu manager policy is none",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					var oldVarKubeletLibRoot string
					helper.SetConf(func(conf *system.Config) {
						oldVarKubeletLibRoot = conf.VarLibKubeletRootDir
						conf.VarLibKubeletRootDir = helper.TempDir
					}, func(conf *system.Config) {
						conf.VarLibKubeletRootDir = oldVarKubeletLibRoot
					})
					helper.WriteFileContents("cpu_manager_state", `{"policyName":"none","defaultCpuSet":"","checksum":1000000000}`)
				},
			},
			arg:     testSharePoolCPUs,
			wantErr: false,
			want:    nil,
		},
		{
			name: "cpu manager static is static",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					var oldVarKubeletLibRoot string
					helper.SetConf(func(conf *system.Config) {
						oldVarKubeletLibRoot = conf.VarLibKubeletRootDir
						conf.VarLibKubeletRootDir = helper.TempDir
					}, func(conf *system.Config) {
						conf.VarLibKubeletRootDir = oldVarKubeletLibRoot
					})
					helper.WriteFileContents("cpu_manager_state", `{"policyName":"static","defaultCpuSet":"0,2-7","entries":{"static-pod-xxx":{"demo":"1"}},"checksum":1000000000}`)
				},
				podMap: map[string]*statesinformer.PodMeta{
					"static-pod": {
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "static-pod",
								UID:       types.UID("static-pod-xxx"),
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
			},
			arg:     testSharePoolCPUs,
			wantErr: false,
			want: []extension.PodCPUAlloc{
				{
					Name:             "static-pod",
					Namespace:        "default",
					UID:              "static-pod-xxx",
					CPUSet:           "1",
					ManagedByKubelet: true,
				},
			},
		},
		{
			name: "failed to parse cpu manager checkpoint",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					var oldVarKubeletLibRoot string
					helper.SetConf(func(conf *system.Config) {
						oldVarKubeletLibRoot = conf.VarLibKubeletRootDir
						conf.VarLibKubeletRootDir = helper.TempDir
					}, func(conf *system.Config) {
						conf.VarLibKubeletRootDir = oldVarKubeletLibRoot
					})
					helper.WriteFileContents("cpu_manager_state", `invalidContent`)
				},
				podMap: map[string]*statesinformer.PodMeta{
					"static-pod": {
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "static-pod",
								UID:       types.UID("static-pod-xxx"),
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
			},
			arg:     testSharePoolCPUs,
			wantErr: true,
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}
			s := &nodeTopoInformer{
				podsInformer: &podsInformer{
					podMap: tt.fields.podMap,
				},
			}
			got, gotErr := s.calKubeletAllocatedCPUs(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_reportNodeTopology(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	client := faketopologyclientset.NewSimpleClientset()
	testNodeTemp := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{},
		},
	}

	mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
	mockNodeCPUInfo := metriccache.NodeCPUInfo{
		BasicInfo: extension.CPUBasicInfo{
			CPUModel:           "XXX",
			HyperThreadEnabled: true,
			TurboEnabled:       true,
		},
		ProcessorInfos: []koordletutil.ProcessorInfo{
			{CPUID: 0, CoreID: 0, NodeID: 0, SocketID: 0},
			{CPUID: 1, CoreID: 0, NodeID: 0, SocketID: 0},
			{CPUID: 2, CoreID: 1, NodeID: 0, SocketID: 0},
			{CPUID: 3, CoreID: 1, NodeID: 0, SocketID: 0},
			{CPUID: 4, CoreID: 2, NodeID: 1, SocketID: 1},
			{CPUID: 5, CoreID: 2, NodeID: 1, SocketID: 1},
			{CPUID: 6, CoreID: 3, NodeID: 1, SocketID: 1},
			{CPUID: 7, CoreID: 3, NodeID: 1, SocketID: 1},
		},
		TotalInfo: koordletutil.CPUTotalInfo{
			NumberCPUs: 8,
			CoreToCPU: map[int32][]koordletutil.ProcessorInfo{
				0: {
					{CPUID: 0, CoreID: 0, NodeID: 0, SocketID: 0},
					{CPUID: 1, CoreID: 0, NodeID: 0, SocketID: 0},
				},
				1: {
					{CPUID: 2, CoreID: 1, NodeID: 0, SocketID: 0},
					{CPUID: 3, CoreID: 1, NodeID: 0, SocketID: 0},
				},
				2: {
					{CPUID: 4, CoreID: 2, NodeID: 1, SocketID: 1},
					{CPUID: 5, CoreID: 2, NodeID: 1, SocketID: 1},
				},
				3: {
					{CPUID: 6, CoreID: 3, NodeID: 1, SocketID: 1},
					{CPUID: 7, CoreID: 3, NodeID: 1, SocketID: 1},
				},
			},
			NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
				0: {
					{CPUID: 0, CoreID: 0, NodeID: 0, SocketID: 0},
					{CPUID: 1, CoreID: 0, NodeID: 0, SocketID: 0},
					{CPUID: 2, CoreID: 1, NodeID: 0, SocketID: 0},
					{CPUID: 3, CoreID: 1, NodeID: 0, SocketID: 0},
				},
				1: {
					{CPUID: 4, CoreID: 2, NodeID: 1, SocketID: 1},
					{CPUID: 5, CoreID: 2, NodeID: 1, SocketID: 1},
					{CPUID: 6, CoreID: 3, NodeID: 1, SocketID: 1},
					{CPUID: 7, CoreID: 3, NodeID: 1, SocketID: 1},
				},
			},
			SocketToCPU: map[int32][]koordletutil.ProcessorInfo{
				0: {
					{CPUID: 0, CoreID: 0, NodeID: 0, SocketID: 0},
					{CPUID: 1, CoreID: 0, NodeID: 0, SocketID: 0},
					{CPUID: 2, CoreID: 1, NodeID: 0, SocketID: 0},
					{CPUID: 3, CoreID: 1, NodeID: 0, SocketID: 0},
				},
				1: {
					{CPUID: 4, CoreID: 2, NodeID: 1, SocketID: 1},
					{CPUID: 5, CoreID: 2, NodeID: 1, SocketID: 1},
					{CPUID: 6, CoreID: 3, NodeID: 1, SocketID: 1},
					{CPUID: 7, CoreID: 3, NodeID: 1, SocketID: 1},
				},
			},
		},
	}
	testMemInfo0 := &koordletutil.MemInfo{
		MemTotal: 263432804, MemFree: 254391744, MemAvailable: 256703236,
		Buffers: 958096, Cached: 0, SwapCached: 0,
		Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
		InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
		Unevictable: 0, Mlocked: 0, SwapTotal: 0,
		SwapFree: 0, Dirty: 624, Writeback: 0,
		AnonPages: 281748, Mapped: 495936, Shmem: 2340,
		Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
		KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
		Bounce: 0, WritebackTmp: 0, AnonHugePages: 38912,
		HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
		HugePages_Surp: 0,
	}
	testMemInfo1 := &koordletutil.MemInfo{
		MemTotal: 263432000, MemFree: 254391744, MemAvailable: 256703236,
		Buffers: 958096, Cached: 0, SwapCached: 0,
		Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
		InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
		Unevictable: 0, Mlocked: 0, SwapTotal: 0,
		SwapFree: 0, Dirty: 624, Writeback: 0,
		AnonPages: 281748, Mapped: 495936, Shmem: 2340,
		Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
		KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
		Bounce: 0, WritebackTmp: 0, AnonHugePages: 38912,
		HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
		HugePages_Surp: 0,
	}
	mockNodeNUMAInfo := &koordletutil.NodeNUMAInfo{
		NUMAInfos: []koordletutil.NUMAInfo{
			{
				NUMANodeID: 0,
				MemInfo:    testMemInfo0,
			},
			{
				NUMANodeID: 1,
				MemInfo:    testMemInfo1,
			},
		},
		MemInfoMap: map[int32]*koordletutil.MemInfo{
			0: testMemInfo0,
			1: testMemInfo1,
		},
	}

	mockPodMeta := map[string]*statesinformer.PodMeta{
		"pod1": {
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod1",
					Namespace: "ns1",
					UID:       "xxx-y1",
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceStatus: `{"cpuset": "4" }`,
					},
				},
			},
		},
		"pod2": {
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod2",
					Namespace: "ns2",
					UID:       "xxx-y2",
					Annotations: map[string]string{
						extension.LabelPodQoS:              string(extension.QoSLSR),
						extension.AnnotationResourceStatus: `{"cpuset": "3" }`,
					},
				},
			},
		},
		"pod3-lse": {
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod3",
					Namespace: "ns3",
					UID:       "xxx-y3",
					Annotations: map[string]string{
						extension.LabelPodQoS:              string(extension.QoSLSE),
						extension.AnnotationResourceStatus: `{"cpuset": "5" }`,
					},
				},
			},
		},
		"pod4-static": {
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod4",
					Namespace: "ns4",
					UID:       "xxx-y4",
				},
			},
		},
	}
	mockMetricCache.EXPECT().Get(metriccache.NodeCPUInfoKey).Return(&mockNodeCPUInfo, true).AnyTimes()
	mockMetricCache.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(mockNodeNUMAInfo, true).AnyTimes()

	expectedCPUSharedPool := `[{"socket":0,"node":0,"cpuset":"0-2"},{"socket":1,"node":1,"cpuset":"6-7"}]`
	expectedCPUSharedPool1 := `[{"socket":0,"node":0,"cpuset":"0,2"},{"socket":1,"node":1,"cpuset":"6-7"}]`
	expectedBECPUSharedPool := `[{"socket":0,"node":0,"cpuset":"0-2,3-4"},{"socket":1,"node":1,"cpuset":"6-7"}]`
	expectedCPUTopology := `{"detail":[{"id":0,"core":0,"socket":0,"node":0},{"id":1,"core":0,"socket":0,"node":0},{"id":2,"core":1,"socket":0,"node":0},{"id":3,"core":1,"socket":0,"node":0},{"id":4,"core":2,"socket":1,"node":1},{"id":5,"core":2,"socket":1,"node":1},{"id":6,"core":3,"socket":1,"node":1},{"id":7,"core":3,"socket":1,"node":1}]}`
	expectedCPUBasicInfoBytes, err := json.Marshal(mockNodeCPUInfo.BasicInfo)
	assert.NoError(t, err)

	expectedTopologyPolices := []string{string(topologyv1alpha1.None)}
	expectedZones := topologyv1alpha1.ZoneList{
		{
			Name: "node-0",
			Type: util.NodeZoneType,
			Resources: topologyv1alpha1.ResourceInfoList{
				{
					Name:        "cpu",
					Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
					Available:   *resource.NewQuantity(4, resource.DecimalSI),
				},
				{
					Name:        "hugepages-1Gi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "hugepages-2Mi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "memory",
					Capacity:    *resource.NewQuantity(269755191296, resource.BinarySI),
					Allocatable: *resource.NewQuantity(269755191296, resource.BinarySI),
					Available:   *resource.NewQuantity(269755191296, resource.BinarySI),
				},
			},
		},
		{
			Name: "node-1",
			Type: util.NodeZoneType,
			Resources: topologyv1alpha1.ResourceInfoList{
				{
					Name:        "cpu",
					Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
					Available:   *resource.NewQuantity(4, resource.DecimalSI),
				},
				{
					Name:        "hugepages-1Gi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "hugepages-2Mi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "memory",
					Capacity:    *resource.NewQuantity(269754368000, resource.BinarySI),
					Allocatable: *resource.NewQuantity(269754368000, resource.BinarySI),
					Available:   *resource.NewQuantity(269754368000, resource.BinarySI),
				},
			},
		},
	}
	oldZones := topologyv1alpha1.ZoneList{
		{
			Name: "node-0",
			Type: util.NodeZoneType,
			Resources: topologyv1alpha1.ResourceInfoList{
				{
					Name:        "cpu",
					Capacity:    *resource.NewQuantity(2, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(2, resource.DecimalSI),
					Available:   *resource.NewQuantity(2, resource.DecimalSI),
				},
				{
					Name:        "gpu",
					Capacity:    *resource.NewQuantity(1, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(1, resource.DecimalSI),
					Available:   *resource.NewQuantity(1, resource.DecimalSI),
				},
				{
					Name:        "memory",
					Capacity:    *resource.NewQuantity(269755191296, resource.BinarySI),
					Allocatable: *resource.NewQuantity(269755191296, resource.BinarySI),
					Available:   *resource.NewQuantity(269755191296, resource.BinarySI),
				},
			},
		},
		{
			Name: "node-1",
			Type: util.NodeZoneType,
			Resources: topologyv1alpha1.ResourceInfoList{
				{
					Name:        "cpu",
					Capacity:    *resource.NewQuantity(2, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(2, resource.DecimalSI),
					Available:   *resource.NewQuantity(2, resource.DecimalSI),
				},
				{
					Name:        "gpu",
					Capacity:    *resource.NewQuantity(1, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(1, resource.DecimalSI),
					Available:   *resource.NewQuantity(1, resource.DecimalSI),
				},
				{
					Name:        "memory",
					Capacity:    *resource.NewQuantity(269754368000, resource.BinarySI),
					Allocatable: *resource.NewQuantity(269754368000, resource.BinarySI),
					Available:   *resource.NewQuantity(269754368000, resource.BinarySI),
				},
			},
		},
	}
	mergedZones := topologyv1alpha1.ZoneList{
		{
			Name: "node-0",
			Type: util.NodeZoneType,
			Resources: topologyv1alpha1.ResourceInfoList{
				{
					Name:        "cpu",
					Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
					Available:   *resource.NewQuantity(4, resource.DecimalSI),
				},
				{
					Name:        "gpu",
					Capacity:    *resource.NewQuantity(1, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(1, resource.DecimalSI),
					Available:   *resource.NewQuantity(1, resource.DecimalSI),
				},
				{
					Name:        "hugepages-1Gi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "hugepages-2Mi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "memory",
					Capacity:    *resource.NewQuantity(269755191296, resource.BinarySI),
					Allocatable: *resource.NewQuantity(269755191296, resource.BinarySI),
					Available:   *resource.NewQuantity(269755191296, resource.BinarySI),
				},
			},
		},
		{
			Name: "node-1",
			Type: util.NodeZoneType,
			Resources: topologyv1alpha1.ResourceInfoList{
				{
					Name:        "cpu",
					Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
					Available:   *resource.NewQuantity(4, resource.DecimalSI),
				},
				{
					Name:        "gpu",
					Capacity:    *resource.NewQuantity(1, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(1, resource.DecimalSI),
					Available:   *resource.NewQuantity(1, resource.DecimalSI),
				},
				{
					Name:        "hugepages-1Gi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "hugepages-2Mi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "memory",
					Capacity:    *resource.NewQuantity(269754368000, resource.BinarySI),
					Allocatable: *resource.NewQuantity(269754368000, resource.BinarySI),
					Available:   *resource.NewQuantity(269754368000, resource.BinarySI),
				},
			},
		},
	}
	oldZones1 := topologyv1alpha1.ZoneList{
		{
			Name: "fake-name",
			Type: util.NodeZoneType,
		},
		{
			Name: "node-0",
			Type: util.NodeZoneType,
			Resources: topologyv1alpha1.ResourceInfoList{
				{
					Name:        "cpu",
					Capacity:    *resource.NewQuantity(2, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(2, resource.DecimalSI),
					Available:   *resource.NewQuantity(2, resource.DecimalSI),
				},
				{
					Name:        "gpu",
					Capacity:    *resource.NewQuantity(1, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(1, resource.DecimalSI),
					Available:   *resource.NewQuantity(1, resource.DecimalSI),
				},
				{
					Name:        "hugepages-1Gi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "hugepages-2Mi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "memory",
					Capacity:    *resource.NewQuantity(269755191296, resource.BinarySI),
					Allocatable: *resource.NewQuantity(269755191296, resource.BinarySI),
					Available:   *resource.NewQuantity(269755191296, resource.BinarySI),
				},
			},
		},
		{
			Name: "node-1",
			Type: util.NodeZoneType,
			Resources: topologyv1alpha1.ResourceInfoList{
				{
					Name:        "cpu",
					Capacity:    *resource.NewQuantity(2, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(2, resource.DecimalSI),
					Available:   *resource.NewQuantity(2, resource.DecimalSI),
				},
				{
					Name:        "gpu",
					Capacity:    *resource.NewQuantity(1, resource.DecimalSI),
					Allocatable: *resource.NewQuantity(1, resource.DecimalSI),
					Available:   *resource.NewQuantity(1, resource.DecimalSI),
				},
				{
					Name:        "hugepages-1Gi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "hugepages-2Mi",
					Capacity:    *resource.NewQuantity(0, resource.BinarySI),
					Allocatable: *resource.NewQuantity(0, resource.BinarySI),
					Available:   *resource.NewQuantity(0, resource.BinarySI),
				},
				{
					Name:        "memory",
					Capacity:    *resource.NewQuantity(269754368000, resource.BinarySI),
					Allocatable: *resource.NewQuantity(269754368000, resource.BinarySI),
					Available:   *resource.NewQuantity(269754368000, resource.BinarySI),
				},
			},
		},
	}

	tests := []struct {
		name                            string
		prepareFn                       func(helper *system.FileTestUtil)
		config                          *Config
		kubeletStub                     KubeletStub
		disableCreateTopologyCRD        bool
		oldZoneList                     *topologyv1alpha1.ZoneList
		nodeLabels                      map[string]string
		nodeStatus                      *corev1.NodeStatus
		nodeReserved                    *extension.NodeReservation
		systemQOSRes                    *extension.SystemQOSResource
		expectedKubeletCPUManagerPolicy extension.KubeletCPUManagerPolicy
		expectedCPUBasicInfo            string
		expectedCPUSharedPool           string
		expectedBECPUSharedPool         string
		expectedCPUTopology             string
		expectedNodeCPUAllocs           string
		expectedNodeReservation         string
		expectedSystemQOS               string
		expectedTopologyPolicies        []string
		expectedZones                   topologyv1alpha1.ZoneList
		expectedLabels                  map[string]string
	}{
		{
			name:   "report topology",
			config: NewDefaultConfig(),
			kubeletStub: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "static",
					KubeReserved: map[string]string{
						"cpu": "2000m",
					},
				},
			},
			expectedKubeletCPUManagerPolicy: extension.KubeletCPUManagerPolicy{
				Policy:       "static",
				ReservedCPUs: "0-1",
			},
			expectedCPUBasicInfo:     string(expectedCPUBasicInfoBytes),
			expectedCPUSharedPool:    expectedCPUSharedPool,
			expectedBECPUSharedPool:  expectedBECPUSharedPool,
			expectedCPUTopology:      expectedCPUTopology,
			expectedNodeCPUAllocs:    "null",
			expectedNodeReservation:  "{}",
			expectedSystemQOS:        "{}",
			expectedTopologyPolicies: expectedTopologyPolices,
			expectedZones:            expectedZones,
		},
		{
			name:   "report node topo with reserved and system qos specified",
			config: NewDefaultConfig(),
			kubeletStub: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "static",
					KubeReserved: map[string]string{
						"cpu": "2000m",
					},
				},
			},
			disableCreateTopologyCRD: false,
			nodeReserved: &extension.NodeReservation{
				ReservedCPUs: "1-2",
			},
			systemQOSRes: &extension.SystemQOSResource{
				CPUSet: "7",
			},
			expectedKubeletCPUManagerPolicy: extension.KubeletCPUManagerPolicy{
				Policy:       "static",
				ReservedCPUs: "0-1",
			},
			expectedCPUBasicInfo:     string(expectedCPUBasicInfoBytes),
			expectedCPUSharedPool:    `[{"socket":0,"node":0,"cpuset":"0"},{"socket":1,"node":1,"cpuset":"6"}]`,
			expectedCPUTopology:      expectedCPUTopology,
			expectedNodeCPUAllocs:    "null",
			expectedNodeReservation:  `{"reservedCPUs":"1-2"}`,
			expectedSystemQOS:        `{"cpuset":"7"}`,
			expectedTopologyPolicies: expectedTopologyPolices,
			expectedZones:            expectedZones,
		},
		{
			name: "disable query topology",
			config: &Config{
				DisableQueryKubeletConfig: true,
			},
			kubeletStub: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "static",
					KubeReserved: map[string]string{
						"cpu": "2000m",
					},
				},
			},
			expectedKubeletCPUManagerPolicy: extension.KubeletCPUManagerPolicy{
				Policy:       "",
				ReservedCPUs: "",
			},
			expectedCPUBasicInfo:     string(expectedCPUBasicInfoBytes),
			expectedCPUSharedPool:    expectedCPUSharedPool,
			expectedCPUTopology:      expectedCPUTopology,
			expectedNodeCPUAllocs:    "null",
			expectedNodeReservation:  "{}",
			expectedSystemQOS:        "{}",
			expectedTopologyPolicies: expectedTopologyPolices,
			expectedZones:            expectedZones,
		},
		{
			name:                     "disable report topology",
			disableCreateTopologyCRD: true,
			config:                   NewDefaultConfig(),
			kubeletStub: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "static",
					KubeReserved: map[string]string{
						"cpu": "2000m",
					},
				},
			},
			expectedKubeletCPUManagerPolicy: extension.KubeletCPUManagerPolicy{
				Policy:       "static",
				ReservedCPUs: "0-1",
			},
			expectedCPUBasicInfo:     string(expectedCPUBasicInfoBytes),
			expectedCPUSharedPool:    expectedCPUSharedPool,
			expectedCPUTopology:      expectedCPUTopology,
			expectedNodeCPUAllocs:    "null",
			expectedNodeReservation:  "{}",
			expectedSystemQOS:        "{}",
			expectedTopologyPolicies: expectedTopologyPolices,
			expectedZones:            expectedZones,
		},
		{
			name:   "report topology and merge old zone list",
			config: NewDefaultConfig(),
			kubeletStub: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "static",
					KubeReserved: map[string]string{
						"cpu": "2000m",
					},
				},
			},
			oldZoneList: &oldZones,
			expectedKubeletCPUManagerPolicy: extension.KubeletCPUManagerPolicy{
				Policy:       "static",
				ReservedCPUs: "0-1",
			},
			expectedCPUBasicInfo:     string(expectedCPUBasicInfoBytes),
			expectedCPUSharedPool:    expectedCPUSharedPool,
			expectedBECPUSharedPool:  expectedBECPUSharedPool,
			expectedCPUTopology:      expectedCPUTopology,
			expectedNodeCPUAllocs:    "null",
			expectedNodeReservation:  "{}",
			expectedSystemQOS:        "{}",
			expectedTopologyPolicies: expectedTopologyPolices,
			expectedZones:            mergedZones,
		},
		{
			name:   "report topology and trim expired zone",
			config: NewDefaultConfig(),
			kubeletStub: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "static",
					KubeReserved: map[string]string{
						"cpu": "2000m",
					},
				},
			},
			oldZoneList: &oldZones1,
			expectedKubeletCPUManagerPolicy: extension.KubeletCPUManagerPolicy{
				Policy:       "static",
				ReservedCPUs: "0-1",
			},
			expectedCPUBasicInfo:     string(expectedCPUBasicInfoBytes),
			expectedCPUSharedPool:    expectedCPUSharedPool,
			expectedBECPUSharedPool:  expectedBECPUSharedPool,
			expectedCPUTopology:      expectedCPUTopology,
			expectedNodeCPUAllocs:    "null",
			expectedNodeReservation:  "{}",
			expectedSystemQOS:        "{}",
			expectedTopologyPolicies: expectedTopologyPolices,
			expectedZones:            mergedZones,
		},
		{
			name: "report topology with kubelet allocated cpus",
			prepareFn: func(helper *system.FileTestUtil) {
				var oldVarKubeletLibRoot string
				helper.SetConf(func(conf *system.Config) {
					oldVarKubeletLibRoot = conf.VarLibKubeletRootDir
					conf.VarLibKubeletRootDir = helper.TempDir
				}, func(conf *system.Config) {
					conf.VarLibKubeletRootDir = oldVarKubeletLibRoot
				})
				helper.WriteFileContents("cpu_manager_state", `{"policyName":"static","defaultCpuSet":"2-7","entries":{"xxx-y4":{"demo":"1"}},"checksum":1000000000}`)
			},
			config: NewDefaultConfig(),
			kubeletStub: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "static",
					KubeReserved: map[string]string{
						"cpu": "2000m",
					},
				},
			},
			expectedKubeletCPUManagerPolicy: extension.KubeletCPUManagerPolicy{
				Policy:       "static",
				ReservedCPUs: "0-1",
			},
			expectedCPUBasicInfo:     string(expectedCPUBasicInfoBytes),
			expectedCPUSharedPool:    expectedCPUSharedPool1,
			expectedBECPUSharedPool:  expectedBECPUSharedPool,
			expectedCPUTopology:      expectedCPUTopology,
			expectedNodeCPUAllocs:    `[{"namespace":"ns4","name":"pod4","uid":"xxx-y4","cpuset":"1","managedByKubelet":true}]`,
			expectedNodeReservation:  "{}",
			expectedSystemQOS:        "{}",
			expectedTopologyPolicies: expectedTopologyPolices,
			expectedZones:            expectedZones,
		},
		{
			name: "report topology with NUMA reservation enabled - verify labels and capacity > allocatable",
			nodeLabels: map[string]string{
				extension.LabelNodeEnableNUMAReservation: "true",
			},
			nodeStatus: &corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(8, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(269755191296, resource.BinarySI),
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(7, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(268706611200, resource.BinarySI),
				},
			},
			config: NewDefaultConfig(),
			kubeletStub: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "none",
				},
			},
			expectedKubeletCPUManagerPolicy: extension.KubeletCPUManagerPolicy{
				Policy:       "none",
				ReservedCPUs: "",
			},
			expectedCPUBasicInfo:     string(expectedCPUBasicInfoBytes),
			expectedCPUSharedPool:    expectedCPUSharedPool,
			expectedBECPUSharedPool:  expectedBECPUSharedPool,
			expectedCPUTopology:      expectedCPUTopology,
			expectedNodeCPUAllocs:    "null",
			expectedNodeReservation:  "{}",
			expectedSystemQOS:        "{}",
			expectedTopologyPolicies: []string{string(topologyv1alpha1.None)},
			// Note: We don't verify exact zone values here due to complexity of NUMA memory calculation.
			// The unit tests Test_applyNUMAReservationToZoneResources cover the reservation logic.
			expectedZones: expectedZones,
			expectedLabels: map[string]string{
				extension.LabelNodeEnableNUMAReservation: "true",
			},
		},
		{
			name: "report topology with NUMA reservation disabled",
			nodeLabels: map[string]string{
				extension.LabelNodeEnableNUMAReservation: "false",
			},
			nodeStatus: &corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(8, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(269755191296, resource.BinarySI),
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    *resource.NewQuantity(7, resource.DecimalSI),
					corev1.ResourceMemory: *resource.NewQuantity(268706611200, resource.BinarySI),
				},
			},
			config: NewDefaultConfig(),
			kubeletStub: &testKubeletStub{
				config: &kubeletconfiginternal.KubeletConfiguration{
					CPUManagerPolicy: "none",
				},
			},
			expectedKubeletCPUManagerPolicy: extension.KubeletCPUManagerPolicy{
				Policy:       "none",
				ReservedCPUs: "",
			},
			expectedCPUBasicInfo:     string(expectedCPUBasicInfoBytes),
			expectedCPUSharedPool:    expectedCPUSharedPool,
			expectedBECPUSharedPool:  expectedBECPUSharedPool,
			expectedCPUTopology:      expectedCPUTopology,
			expectedNodeCPUAllocs:    "null",
			expectedNodeReservation:  "{}",
			expectedSystemQOS:        "{}",
			expectedTopologyPolicies: []string{string(topologyv1alpha1.None)},
			expectedZones:            expectedZones,
			expectedLabels: map[string]string{
				extension.LabelNodeEnableNUMAReservation: "false",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.prepareFn != nil {
				tt.prepareFn(helper)
			}
			// prepare feature map
			enabled := features.DefaultKoordletFeatureGate.Enabled(features.NodeTopologyReport)
			testFeatureGates := map[string]bool{string(features.NodeTopologyReport): !tt.disableCreateTopologyCRD}
			err = features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
			assert.NoError(t, err)
			defer func() {
				testFeatureGates[string(features.NodeTopologyReport)] = enabled
				err = features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
				assert.NoError(t, err)
			}()

			testNode := testNodeTemp.DeepCopy()
			if tt.nodeLabels != nil {
				testNode.Labels = tt.nodeLabels
			}
			if tt.nodeStatus != nil {
				testNode.Status = *tt.nodeStatus
			}
			if testNode.Labels == nil {
				testNode.Labels = map[string]string{}
			}
			if tt.nodeReserved != nil {
				testNode.Annotations[extension.AnnotationNodeReservation] = util.DumpJSON(tt.nodeReserved)
			}
			if tt.systemQOSRes != nil {
				testNode.Annotations[extension.AnnotationNodeSystemQOSResource] = util.DumpJSON(tt.systemQOSRes)
			}

			r := &nodeTopoInformer{
				config:         tt.config,
				kubelet:        tt.kubeletStub,
				topologyClient: client,
				metricCache:    mockMetricCache,
				nodeResourceTopologyLister: &fakeNodeResourceTopologyLister{
					nodeResourceTopologys: &topologyv1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test",
						},
					},
				},
				podsInformer: &podsInformer{
					podMap: mockPodMeta,
				},
				nodeInformer: &nodeInformer{
					node: testNode,
				},
				callbackRunner: NewCallbackRunner(),
			}

			topologyName := testNode.Name
			_ = client.TopologyV1alpha1().NodeResourceTopologies().Delete(context.TODO(), topologyName, metav1.DeleteOptions{})
			if !tt.disableCreateTopologyCRD {
				topologyTest := newNodeTopo(testNode)
				if tt.oldZoneList != nil {
					topologyTest.Zones = *tt.oldZoneList
				}
				_, err = client.TopologyV1alpha1().NodeResourceTopologies().Create(context.TODO(), topologyTest, metav1.CreateOptions{})
				r.nodeResourceTopologyLister = &fakeNodeResourceTopologyLister{
					nodeResourceTopologys: topologyTest,
				}
			}
			r.reportNodeTopology()

			var topo *topologyv1alpha1.NodeResourceTopology
			if tt.disableCreateTopologyCRD {
				topo = r.GetNodeTopo()
				_, err = client.TopologyV1alpha1().NodeResourceTopologies().Get(context.TODO(), topologyName, metav1.GetOptions{})
				assert.True(t, errors.IsNotFound(err))
			} else {
				topo, err = client.TopologyV1alpha1().NodeResourceTopologies().Get(context.TODO(), topologyName, metav1.GetOptions{})
				assert.NoError(t, err)
			}

			var kubeletCPUManagerPolicy extension.KubeletCPUManagerPolicy
			err = json.Unmarshal([]byte(topo.Annotations[extension.AnnotationKubeletCPUManagerPolicy]), &kubeletCPUManagerPolicy)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedKubeletCPUManagerPolicy, kubeletCPUManagerPolicy)
			assert.Equal(t, tt.expectedCPUBasicInfo, topo.Annotations[extension.AnnotationCPUBasicInfo])
			assert.Equal(t, tt.expectedCPUSharedPool, topo.Annotations[extension.AnnotationNodeCPUSharedPools])
			assert.Equal(t, tt.expectedCPUTopology, topo.Annotations[extension.AnnotationNodeCPUTopology])
			assert.Equal(t, tt.expectedNodeCPUAllocs, topo.Annotations[extension.AnnotationNodeCPUAllocs])
			assert.Equal(t, tt.expectedNodeReservation, topo.Annotations[extension.AnnotationNodeReservation])
			assert.Equal(t, tt.expectedSystemQOS, topo.Annotations[extension.AnnotationNodeSystemQOSResource])
			assert.Equal(t, tt.expectedTopologyPolicies, topo.TopologyPolicies)
			// Skip zone comparison for NUMA reservation enabled test as values are complex
			if tt.name != "report topology with NUMA reservation enabled - verify labels and capacity > allocatable" {
				assert.Equal(t, tt.expectedZones, topo.Zones)
			} else {
				// For NUMA reservation test, just verify zones are not empty and have 2 NUMA nodes
				assert.Len(t, topo.Zones, 2)
				assert.Equal(t, "node-0", topo.Zones[0].Name)
				assert.Equal(t, "node-1", topo.Zones[1].Name)
				// Verify that capacity >= allocatable for CPU and memory
				for _, zone := range topo.Zones {
					for _, res := range zone.Resources {
						if res.Name == "cpu" || res.Name == "memory" {
							assert.True(t, res.Capacity.Cmp(res.Allocatable) >= 0,
								"zone %s resource %s: capacity should be >= allocatable, got capacity=%v, allocatable=%v",
								zone.Name, res.Name, res.Capacity, res.Allocatable)
							assert.True(t, res.Allocatable.Cmp(res.Available) == 0,
								"zone %s resource %s: allocatable should equal available",
								zone.Name, res.Name)
						}
					}
				}
			}
			if tt.expectedLabels != nil {
				for key, expectedValue := range tt.expectedLabels {
					actualValue, exists := topo.Labels[key]
					assert.True(t, exists, "label %s should exist", key)
					assert.Equal(t, expectedValue, actualValue, "label %s should match", key)
				}
			}
		})
	}
}

func Test_nodeTopology_isChanged(t *testing.T) {
	type args struct {
		oldTopo       *topologyv1alpha1.NodeResourceTopology
		newTopoStatus *nodeTopologyStatus
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantMsg string
	}{
		{
			name: "old is nil",
			args: args{
				oldTopo: &topologyv1alpha1.NodeResourceTopology{},
				newTopoStatus: &nodeTopologyStatus{
					Annotations: map[string]string{
						"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
						"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
						"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
						"node.koordinator.sh/pod-cpu-allocs":        "{\"Namespace\":\"default1\",\"Name\":\"test-pod\",\"UID\":\"pod\",\"CPUSet\":\"1-3\",\"ManagedByKubelet\": \"true\"}",
					},
				},
			},
			want:    true,
			wantMsg: "metadata changed",
		},
		{
			name: "be cpu share pool changed",
			args: args{
				oldTopo: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"node.koordinator.sh/be-cpu-shared-pools": "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
						},
					},
					TopologyPolicies: []string{""},
					Zones:            nil,
				},
				newTopoStatus: &nodeTopologyStatus{
					Annotations: map[string]string{
						"node.koordinator.sh/be-cpu-shared-pools": "[{\"socket\":0,\"node\":0,\"cpuset\":\"1-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
					},
					TopologyPolicy: "",
					Zones:          nil,
				},
			},
			want:    true,
			wantMsg: "annotations changed, key node.koordinator.sh/be-cpu-shared-pools",
		},
		{
			name: "annotation is new",
			args: args{
				oldTopo: newNodeTopo(&corev1.Node{}),
				newTopoStatus: &nodeTopologyStatus{
					Annotations: map[string]string{
						"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
						"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
						"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
						"node.koordinator.sh/pod-cpu-allocs":        "{\"Namespace\":\"default1\",\"Name\":\"test-pod\",\"UID\":\"pod\",\"CPUSet\":\"1-3\",\"ManagedByKubelet\": \"true\"}",
					},
				},
			},
			want:    true,
			wantMsg: "metadata changed",
		},
		{
			name: "same json with different map order in cpu share pool",
			args: args{
				oldTopo: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelManagedBy: "Koordinator",
						},
						Annotations: map[string]string{
							"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
							"node.koordinator.sh/cpu-shared-pools":      "[{\"cpuset\":\"0-25,52-77\",\"socket\":0,\"node\":0},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
							"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
						},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "v1",
								Kind:       "Node",
								Name:       "test-node",
								UID:        "xxx",
							},
						},
					},
					// fields are required
					TopologyPolicies: []string{string(topologyv1alpha1.None)},
					Zones:            topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
				newTopoStatus: &nodeTopologyStatus{
					Annotations: map[string]string{
						"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
						"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
						"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
					},
					TopologyPolicy: topologyv1alpha1.None,
					Zones:          topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
			},
			want: false,
		},
		{
			name: "diff json on pod-cpu-allocs",
			args: args{
				oldTopo: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelManagedBy: "Koordinator",
						},
						Annotations: map[string]string{
							"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
							"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
							"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
							"node.koordinator.sh/pod-cpu-allocs":        "{\"Namespace\":\"default\",\"Name\":\"test-pod\",\"UID\":\"pod\",\"CPUSet\":\"1-3\",\"ManagedByKubelet\": \"true\"}",
						},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "v1",
								Kind:       "Node",
								Name:       "test-node",
								UID:        "xxx",
							},
						},
					},
					// fields are required
					TopologyPolicies: []string{string(topologyv1alpha1.None)},
					Zones:            topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
				newTopoStatus: &nodeTopologyStatus{
					Annotations: map[string]string{
						"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
						"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
						"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
						"node.koordinator.sh/pod-cpu-allocs":        "{\"Namespace\":\"default1\",\"Name\":\"test-pod\",\"UID\":\"pod\",\"CPUSet\":\"1-3\",\"ManagedByKubelet\": \"true\"}",
					},
					TopologyPolicy: topologyv1alpha1.None,
					Zones:          topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
			},
			want:    true,
			wantMsg: "annotations changed, key node.koordinator.sh/pod-cpu-allocs",
		},
		{
			name: "some are both not exist in old and new",
			args: args{
				oldTopo: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelManagedBy: "Koordinator",
						},
						Annotations: map[string]string{
							"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
							"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
							"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
						},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "v1",
								Kind:       "Node",
								Name:       "test-node",
								UID:        "xxx",
							},
						},
					},
					// fields are required
					TopologyPolicies: []string{string(topologyv1alpha1.None)},
					Zones:            topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
				newTopoStatus: &nodeTopologyStatus{
					Annotations: map[string]string{
						"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
						"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
						"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
					},
					TopologyPolicy: topologyv1alpha1.None,
					Zones:          topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
			},
			want: false,
		},
		{
			name: "part are not exist in old",
			args: args{
				oldTopo: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelManagedBy: "Koordinator",
						},
						Annotations: map[string]string{
							"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
							"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
							"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
						},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "v1",
								Kind:       "Node",
								Name:       "test-node",
								UID:        "xxx",
							},
						},
					},
					// fields are required
					TopologyPolicies: []string{string(topologyv1alpha1.None)},
					Zones:            topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
				newTopoStatus: &nodeTopologyStatus{
					Annotations: map[string]string{
						"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
						"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
						"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
						"node.koordinator.sh/pod-cpu-allocs":        "{\"Namespace\":\"default\",\"Name\":\"test-pod\",\"UID\":\"pod\",\"CPUSet\":\"1-3\",\"ManagedByKubelet\": \"true\"}",
					},
					TopologyPolicy: topologyv1alpha1.None,
					Zones:          topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
			},
			want:    true,
			wantMsg: "annotations changed, key node.koordinator.sh/pod-cpu-allocs",
		},
		{
			name: "part are not exist in new",
			args: args{
				oldTopo: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelManagedBy: "Koordinator",
						},
						Annotations: map[string]string{
							"kubelet.koordinator.sh/cpu-manager-policy": "{\"policy\":\"none\"}",
							"node.koordinator.sh/cpu-shared-pools":      "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
							"node.koordinator.sh/cpu-topology":          "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
							"node.koordinator.sh/pod-cpu-allocs":        "{\"Namespace\":\"default\",\"Name\":\"test-pod\",\"UID\":\"pod\",\"CPUSet\":\"1-3\",\"ManagedByKubelet\": \"true\"}",
						},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "v1",
								Kind:       "Node",
								Name:       "test-node",
								UID:        "xxx",
							},
						},
					},
					// fields are required
					TopologyPolicies: []string{string(topologyv1alpha1.None)},
					Zones:            topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
				newTopoStatus: &nodeTopologyStatus{
					Annotations: map[string]string{
						"node.koordinator.sh/cpu-topology":     "{\"detail\":[{\"id\":0,\"core\":0,\"socket\":0,\"node\":0},{\"id\":52,\"core\":0,\"socket\":0,\"node\":0},{\"id\":1,\"core\":1,\"socket\":0,\"node\":0}]}",
						"node.koordinator.sh/pod-cpu-allocs":   "{\"Namespace\":\"default\",\"Name\":\"test-pod\",\"UID\":\"pod\",\"CPUSet\":\"1-3\",\"ManagedByKubelet\": \"true\"}",
						"node.koordinator.sh/cpu-shared-pools": "[{\"socket\":0,\"node\":0,\"cpuset\":\"0-25,52-77\"},{\"socket\":1,\"node\":1,\"cpuset\":\"26-51,78-103\"}]",
					},
					TopologyPolicy: topologyv1alpha1.None,
					Zones:          topologyv1alpha1.ZoneList{topologyv1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
				},
			},
			want:    true,
			wantMsg: "annotations changed, key kubelet.koordinator.sh/cpu-manager-policy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, msg := tt.args.newTopoStatus.isChanged(tt.args.oldTopo)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantMsg, msg)
		})
	}
}

func Test_calTopologyZoneList(t *testing.T) {
	type fields struct {
		metricCache func(ctrl *gomock.Controller) metriccache.MetricCache
	}
	type args struct {
		nodeCPUInfo *metriccache.NodeCPUInfo
	}
	tests := []struct {
		name           string
		fields         fields
		args           args
		hugepageEnable bool
		want           topologyv1alpha1.ZoneList
		wantErr        bool
	}{
		{
			name: "err when numa info not exist",
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mc := mock_metriccache.NewMockMetricCache(ctrl)
					mc.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(nil, false).Times(1)
					return mc
				},
			},
			args:    args{},
			want:    nil,
			wantErr: true,
		},
		{
			name: "err when cpu info and numa info are not aligned",
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mc := mock_metriccache.NewMockMetricCache(ctrl)
					mc.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(&koordletutil.NodeNUMAInfo{
						NUMAInfos: []koordletutil.NUMAInfo{
							{
								NUMANodeID: 0,
								MemInfo:    &koordletutil.MemInfo{},
							},
						},
					}, true).Times(1)
					return mc
				},
			},
			args: args{
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					TotalInfo: koordletutil.CPUTotalInfo{
						NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
							0: {
								{
									CPUID:    0,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
							},
							1: {
								{
									CPUID:    1,
									CoreID:   1,
									SocketID: 1,
									NodeID:   1,
								},
							},
						},
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "calculate single numa node",
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mc := mock_metriccache.NewMockMetricCache(ctrl)
					mc.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(&koordletutil.NodeNUMAInfo{
						NUMAInfos: []koordletutil.NUMAInfo{
							{
								NUMANodeID: 0,
								MemInfo: &koordletutil.MemInfo{
									MemTotal: 157286400, // 150G
								},
							},
						},
						MemInfoMap: map[int32]*koordletutil.MemInfo{
							0: {
								MemTotal: 157286400, // 150G
							},
						},
					}, true).Times(1)
					return mc
				},
			},
			args: args{
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					TotalInfo: koordletutil.CPUTotalInfo{
						NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
							0: {
								{
									CPUID:    0,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    1,
									CoreID:   1,
									SocketID: 0,
									NodeID:   0,
								},
							},
						},
					},
				},
			},
			want: topologyv1alpha1.ZoneList{
				{
					Name: "node-0",
					Type: util.NodeZoneType,
					Resources: topologyv1alpha1.ResourceInfoList{
						{
							Name:        "cpu",
							Capacity:    *resource.NewQuantity(2, resource.DecimalSI),
							Allocatable: *resource.NewQuantity(2, resource.DecimalSI),
							Available:   *resource.NewQuantity(2, resource.DecimalSI),
						},
						{
							Name:        "hugepages-1Gi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "hugepages-2Mi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "memory",
							Capacity:    *resource.NewQuantity(161061273600, resource.BinarySI),
							Allocatable: *resource.NewQuantity(161061273600, resource.BinarySI),
							Available:   *resource.NewQuantity(161061273600, resource.BinarySI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate single numa node with hugepage, but featuregate hugepage not enable",
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mc := mock_metriccache.NewMockMetricCache(ctrl)
					mc.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(&koordletutil.NodeNUMAInfo{
						NUMAInfos: []koordletutil.NUMAInfo{
							{
								NUMANodeID: 0,
								MemInfo: &koordletutil.MemInfo{
									MemTotal: 157286400, // 150G
								},
								HugePages: map[uint64]*koordletutil.HugePagesInfo{
									koordletutil.Hugepage2Mkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage2Mkbyte,
									}, // 60M
									koordletutil.Hugepage1Gkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage1Gkbyte,
									}, // 30G
								},
							},
						},
						MemInfoMap: map[int32]*koordletutil.MemInfo{
							0: {
								MemTotal: 157286400, // 150G
							},
						},
						HugePagesMap: map[int32]map[uint64]*koordletutil.HugePagesInfo{
							0: {
								koordletutil.Hugepage2Mkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage2Mkbyte,
								}, // 60M
								koordletutil.Hugepage1Gkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage1Gkbyte,
								}, // 30G
							},
						},
					}, true).Times(1)
					return mc
				},
			},
			args: args{
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					TotalInfo: koordletutil.CPUTotalInfo{
						NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
							0: {
								{
									CPUID:    0,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    1,
									CoreID:   1,
									SocketID: 0,
									NodeID:   0,
								},
							},
						},
					},
				},
			},
			want: topologyv1alpha1.ZoneList{
				{
					Name: "node-0",
					Type: util.NodeZoneType,
					Resources: topologyv1alpha1.ResourceInfoList{
						{
							Name:        "cpu",
							Capacity:    *resource.NewQuantity(2, resource.DecimalSI),
							Allocatable: *resource.NewQuantity(2, resource.DecimalSI),
							Available:   *resource.NewQuantity(2, resource.DecimalSI),
						},
						{
							Name:        "hugepages-1Gi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "hugepages-2Mi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "memory",
							Capacity:    *resource.NewQuantity(161061273600, resource.BinarySI),
							Allocatable: *resource.NewQuantity(161061273600, resource.BinarySI),
							Available:   *resource.NewQuantity(161061273600, resource.BinarySI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name:           "calculate single numa node with hugepage, featuregate hugepage enable",
			hugepageEnable: true,
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mc := mock_metriccache.NewMockMetricCache(ctrl)
					mc.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(&koordletutil.NodeNUMAInfo{
						NUMAInfos: []koordletutil.NUMAInfo{
							{
								NUMANodeID: 0,
								MemInfo: &koordletutil.MemInfo{
									MemTotal: 157286400, // 150G
								},
								HugePages: map[uint64]*koordletutil.HugePagesInfo{
									koordletutil.Hugepage2Mkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage2Mkbyte,
									}, // 60M
									koordletutil.Hugepage1Gkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage1Gkbyte,
									}, // 30G
								},
							},
						},
						MemInfoMap: map[int32]*koordletutil.MemInfo{
							0: {
								MemTotal: 157286400, // 150G
							},
						},
						HugePagesMap: map[int32]map[uint64]*koordletutil.HugePagesInfo{
							0: {
								koordletutil.Hugepage2Mkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage2Mkbyte,
								}, // 60M
								koordletutil.Hugepage1Gkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage1Gkbyte,
								}, // 30G
							},
						},
					}, true).Times(1)
					return mc
				},
			},
			args: args{
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					TotalInfo: koordletutil.CPUTotalInfo{
						NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
							0: {
								{
									CPUID:    0,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    1,
									CoreID:   1,
									SocketID: 0,
									NodeID:   0,
								},
							},
						},
					},
				},
			},
			want: topologyv1alpha1.ZoneList{
				{
					Name: "node-0",
					Type: util.NodeZoneType,
					Resources: topologyv1alpha1.ResourceInfoList{
						{
							Name:        "cpu",
							Capacity:    *resource.NewQuantity(2, resource.DecimalSI),
							Allocatable: *resource.NewQuantity(2, resource.DecimalSI),
							Available:   *resource.NewQuantity(2, resource.DecimalSI),
						},
						{
							Name:        "hugepages-1Gi",
							Capacity:    *resource.NewQuantity(32212254720, resource.BinarySI),
							Allocatable: *resource.NewQuantity(32212254720, resource.BinarySI),
							Available:   *resource.NewQuantity(32212254720, resource.BinarySI),
						},
						{
							Name:        "hugepages-2Mi",
							Capacity:    *resource.NewQuantity(62914560, resource.BinarySI),
							Allocatable: *resource.NewQuantity(62914560, resource.BinarySI),
							Available:   *resource.NewQuantity(62914560, resource.BinarySI),
						},
						{
							Name:        "memory",
							Capacity:    *resource.NewQuantity(128786104320, resource.BinarySI),
							Allocatable: *resource.NewQuantity(128786104320, resource.BinarySI),
							Available:   *resource.NewQuantity(128786104320, resource.BinarySI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate multiple numa nodes",
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mc := mock_metriccache.NewMockMetricCache(ctrl)
					mc.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(&koordletutil.NodeNUMAInfo{
						NUMAInfos: []koordletutil.NUMAInfo{
							{
								NUMANodeID: 0,
								MemInfo: &koordletutil.MemInfo{
									MemTotal: 157286400, // 150G
								},
							},
							{
								NUMANodeID: 1,
								MemInfo: &koordletutil.MemInfo{
									MemTotal: 157286400, // 150G
								},
							},
						},
						MemInfoMap: map[int32]*koordletutil.MemInfo{
							0: {
								MemTotal: 157286400,
							},
							1: {
								MemTotal: 157286400,
							},
						},
					}, true).Times(1)
					return mc
				},
			},
			args: args{
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					TotalInfo: koordletutil.CPUTotalInfo{
						NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
							0: {
								{
									CPUID:    0,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    1,
									CoreID:   1,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    4,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    5,
									CoreID:   1,
									SocketID: 0,
									NodeID:   0,
								},
							},
							1: {
								{
									CPUID:    2,
									CoreID:   2,
									SocketID: 1,
									NodeID:   1,
								},
								{
									CPUID:    3,
									CoreID:   3,
									SocketID: 1,
									NodeID:   1,
								},
								{
									CPUID:    6,
									CoreID:   2,
									SocketID: 1,
									NodeID:   1,
								},
								{
									CPUID:    7,
									CoreID:   3,
									SocketID: 1,
									NodeID:   1,
								},
							},
						},
					},
				},
			},
			want: topologyv1alpha1.ZoneList{
				{
					Name: "node-0",
					Type: util.NodeZoneType,
					Resources: topologyv1alpha1.ResourceInfoList{
						{
							Name:        "cpu",
							Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
							Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
							Available:   *resource.NewQuantity(4, resource.DecimalSI),
						},
						{
							Name:        "hugepages-1Gi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "hugepages-2Mi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "memory",
							Capacity:    *resource.NewQuantity(161061273600, resource.BinarySI),
							Allocatable: *resource.NewQuantity(161061273600, resource.BinarySI),
							Available:   *resource.NewQuantity(161061273600, resource.BinarySI),
						},
					},
				},
				{
					Name: "node-1",
					Type: util.NodeZoneType,
					Resources: topologyv1alpha1.ResourceInfoList{
						{
							Name:        "cpu",
							Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
							Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
							Available:   *resource.NewQuantity(4, resource.DecimalSI),
						},
						{
							Name:        "hugepages-1Gi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "hugepages-2Mi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "memory",
							Capacity:    *resource.NewQuantity(161061273600, resource.BinarySI),
							Allocatable: *resource.NewQuantity(161061273600, resource.BinarySI),
							Available:   *resource.NewQuantity(161061273600, resource.BinarySI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate multiple numa nodes with hugepage, but featuregate hugepage not enable",
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mc := mock_metriccache.NewMockMetricCache(ctrl)
					mc.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(&koordletutil.NodeNUMAInfo{
						NUMAInfos: []koordletutil.NUMAInfo{
							{
								NUMANodeID: 0,
								MemInfo: &koordletutil.MemInfo{
									MemTotal: 157286400, // 150G
								},
								HugePages: map[uint64]*koordletutil.HugePagesInfo{
									koordletutil.Hugepage2Mkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage2Mkbyte,
									}, // 60M
									koordletutil.Hugepage1Gkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage1Gkbyte,
									}, // 30G
								},
							},
							{
								NUMANodeID: 1,
								MemInfo: &koordletutil.MemInfo{
									MemTotal: 157286400, // 150G
								},
								HugePages: map[uint64]*koordletutil.HugePagesInfo{
									koordletutil.Hugepage2Mkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage2Mkbyte,
									}, // 60M
									koordletutil.Hugepage1Gkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage1Gkbyte,
									}, // 30G
								},
							},
						},
						MemInfoMap: map[int32]*koordletutil.MemInfo{
							0: {
								MemTotal: 157286400,
							},
							1: {
								MemTotal: 157286400,
							},
						},
						HugePagesMap: map[int32]map[uint64]*koordletutil.HugePagesInfo{
							0: {
								koordletutil.Hugepage2Mkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage2Mkbyte,
								},
								koordletutil.Hugepage1Gkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage1Gkbyte,
								},
							},
							1: {
								koordletutil.Hugepage2Mkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage2Mkbyte,
								},
								koordletutil.Hugepage1Gkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage1Gkbyte,
								},
							},
						},
					}, true).Times(1)
					return mc
				},
			},
			args: args{
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					TotalInfo: koordletutil.CPUTotalInfo{
						NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
							0: {
								{
									CPUID:    0,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    1,
									CoreID:   1,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    4,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    5,
									CoreID:   1,
									SocketID: 0,
									NodeID:   0,
								},
							},
							1: {
								{
									CPUID:    2,
									CoreID:   2,
									SocketID: 1,
									NodeID:   1,
								},
								{
									CPUID:    3,
									CoreID:   3,
									SocketID: 1,
									NodeID:   1,
								},
								{
									CPUID:    6,
									CoreID:   2,
									SocketID: 1,
									NodeID:   1,
								},
								{
									CPUID:    7,
									CoreID:   3,
									SocketID: 1,
									NodeID:   1,
								},
							},
						},
					},
				},
			},
			want: topologyv1alpha1.ZoneList{
				{
					Name: "node-0",
					Type: util.NodeZoneType,
					Resources: topologyv1alpha1.ResourceInfoList{
						{
							Name:        "cpu",
							Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
							Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
							Available:   *resource.NewQuantity(4, resource.DecimalSI),
						},
						{
							Name:        "hugepages-1Gi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "hugepages-2Mi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "memory",
							Capacity:    *resource.NewQuantity(161061273600, resource.BinarySI),
							Allocatable: *resource.NewQuantity(161061273600, resource.BinarySI),
							Available:   *resource.NewQuantity(161061273600, resource.BinarySI),
						},
					},
				},
				{
					Name: "node-1",
					Type: util.NodeZoneType,
					Resources: topologyv1alpha1.ResourceInfoList{
						{
							Name:        "cpu",
							Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
							Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
							Available:   *resource.NewQuantity(4, resource.DecimalSI),
						},
						{
							Name:        "hugepages-1Gi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "hugepages-2Mi",
							Capacity:    *resource.NewQuantity(0, resource.BinarySI),
							Allocatable: *resource.NewQuantity(0, resource.BinarySI),
							Available:   *resource.NewQuantity(0, resource.BinarySI),
						},
						{
							Name:        "memory",
							Capacity:    *resource.NewQuantity(161061273600, resource.BinarySI),
							Allocatable: *resource.NewQuantity(161061273600, resource.BinarySI),
							Available:   *resource.NewQuantity(161061273600, resource.BinarySI),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name:           "calculate multiple numa nodes with hugepage, featuregate hugepage enable",
			hugepageEnable: true,
			fields: fields{
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mc := mock_metriccache.NewMockMetricCache(ctrl)
					mc.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(&koordletutil.NodeNUMAInfo{
						NUMAInfos: []koordletutil.NUMAInfo{
							{
								NUMANodeID: 0,
								MemInfo: &koordletutil.MemInfo{
									MemTotal: 157286400, // 150G
								},
								HugePages: map[uint64]*koordletutil.HugePagesInfo{
									koordletutil.Hugepage2Mkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage2Mkbyte,
									}, // 60M
									koordletutil.Hugepage1Gkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage1Gkbyte,
									}, // 30G
								},
							},
							{
								NUMANodeID: 1,
								MemInfo: &koordletutil.MemInfo{
									MemTotal: 157286400, // 150G
								},
								HugePages: map[uint64]*koordletutil.HugePagesInfo{
									koordletutil.Hugepage2Mkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage2Mkbyte,
									}, // 60M
									koordletutil.Hugepage1Gkbyte: {
										NumPages: 30,
										PageSize: koordletutil.Hugepage1Gkbyte,
									}, // 30G
								},
							},
						},
						MemInfoMap: map[int32]*koordletutil.MemInfo{
							0: {
								MemTotal: 157286400,
							},
							1: {
								MemTotal: 157286400,
							},
						},
						HugePagesMap: map[int32]map[uint64]*koordletutil.HugePagesInfo{
							0: {
								koordletutil.Hugepage2Mkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage2Mkbyte,
								},
								koordletutil.Hugepage1Gkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage1Gkbyte,
								},
							},
							1: {
								koordletutil.Hugepage2Mkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage2Mkbyte,
								},
								koordletutil.Hugepage1Gkbyte: {
									NumPages: 30,
									PageSize: koordletutil.Hugepage1Gkbyte,
								},
							},
						},
					}, true).Times(1)
					return mc
				},
			},
			args: args{
				nodeCPUInfo: &metriccache.NodeCPUInfo{
					TotalInfo: koordletutil.CPUTotalInfo{
						NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
							0: {
								{
									CPUID:    0,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    1,
									CoreID:   1,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    4,
									CoreID:   0,
									SocketID: 0,
									NodeID:   0,
								},
								{
									CPUID:    5,
									CoreID:   1,
									SocketID: 0,
									NodeID:   0,
								},
							},
							1: {
								{
									CPUID:    2,
									CoreID:   2,
									SocketID: 1,
									NodeID:   1,
								},
								{
									CPUID:    3,
									CoreID:   3,
									SocketID: 1,
									NodeID:   1,
								},
								{
									CPUID:    6,
									CoreID:   2,
									SocketID: 1,
									NodeID:   1,
								},
								{
									CPUID:    7,
									CoreID:   3,
									SocketID: 1,
									NodeID:   1,
								},
							},
						},
					},
				},
			},
			want: topologyv1alpha1.ZoneList{
				{
					Name: "node-0",
					Type: util.NodeZoneType,
					Resources: topologyv1alpha1.ResourceInfoList{
						{
							Name:        "cpu",
							Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
							Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
							Available:   *resource.NewQuantity(4, resource.DecimalSI),
						},
						{
							Name:        "hugepages-1Gi",
							Capacity:    *resource.NewQuantity(32212254720, resource.BinarySI),
							Allocatable: *resource.NewQuantity(32212254720, resource.BinarySI),
							Available:   *resource.NewQuantity(32212254720, resource.BinarySI),
						},
						{
							Name:        "hugepages-2Mi",
							Capacity:    *resource.NewQuantity(62914560, resource.BinarySI),
							Allocatable: *resource.NewQuantity(62914560, resource.BinarySI),
							Available:   *resource.NewQuantity(62914560, resource.BinarySI),
						},
						{
							Name:        "memory",
							Capacity:    *resource.NewQuantity(128786104320, resource.BinarySI),
							Allocatable: *resource.NewQuantity(128786104320, resource.BinarySI),
							Available:   *resource.NewQuantity(128786104320, resource.BinarySI),
						},
					},
				},
				{
					Name: "node-1",
					Type: util.NodeZoneType,
					Resources: topologyv1alpha1.ResourceInfoList{
						{
							Name:        "cpu",
							Capacity:    *resource.NewQuantity(4, resource.DecimalSI),
							Allocatable: *resource.NewQuantity(4, resource.DecimalSI),
							Available:   *resource.NewQuantity(4, resource.DecimalSI),
						},
						{
							Name:        "hugepages-1Gi",
							Capacity:    *resource.NewQuantity(32212254720, resource.BinarySI),
							Allocatable: *resource.NewQuantity(32212254720, resource.BinarySI),
							Available:   *resource.NewQuantity(32212254720, resource.BinarySI),
						},
						{
							Name:        "hugepages-2Mi",
							Capacity:    *resource.NewQuantity(62914560, resource.BinarySI),
							Allocatable: *resource.NewQuantity(62914560, resource.BinarySI),
							Available:   *resource.NewQuantity(62914560, resource.BinarySI),
						},
						{
							Name:        "memory",
							Capacity:    *resource.NewQuantity(128786104320, resource.BinarySI),
							Allocatable: *resource.NewQuantity(128786104320, resource.BinarySI),
							Available:   *resource.NewQuantity(128786104320, resource.BinarySI),
						},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			enabled := features.DefaultKoordletFeatureGate.Enabled(features.HugePageReport)
			testFeatureGates := map[string]bool{string(features.HugePageReport): tt.hugepageEnable}
			err := features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
			assert.NoError(t, err)
			defer func() {
				testFeatureGates[string(features.HugePageReport)] = enabled
				err = features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
				assert.NoError(t, err)
			}()

			s := &nodeTopoInformer{
				metricCache: tt.fields.metricCache(ctrl),
			}
			got, gotErr := s.calTopologyZoneList(tt.args.nodeCPUInfo)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func Test_getNodeReserved(t *testing.T) {
	proportionalQuantity := resource.MustParse("2500m")
	fakeTopo := topology.CPUTopology{
		NumCPUs:    12,
		NumSockets: 2,
		NumCores:   6,
		CPUDetails: map[int]topology.CPUInfo{
			0:  {CoreID: 0, SocketID: 0, NUMANodeID: 0},
			1:  {CoreID: 1, SocketID: 1, NUMANodeID: 1},
			2:  {CoreID: 2, SocketID: 0, NUMANodeID: 0},
			3:  {CoreID: 3, SocketID: 1, NUMANodeID: 1},
			4:  {CoreID: 4, SocketID: 0, NUMANodeID: 0},
			5:  {CoreID: 5, SocketID: 1, NUMANodeID: 1},
			6:  {CoreID: 0, SocketID: 0, NUMANodeID: 0},
			7:  {CoreID: 1, SocketID: 1, NUMANodeID: 1},
			8:  {CoreID: 2, SocketID: 0, NUMANodeID: 0},
			9:  {CoreID: 3, SocketID: 1, NUMANodeID: 1},
			10: {CoreID: 4, SocketID: 0, NUMANodeID: 0},
			11: {CoreID: 5, SocketID: 1, NUMANodeID: 1},
		},
	}
	type args struct {
		anno map[string]string
	}
	tests := []struct {
		name string
		args args
		want *extension.NodeReservation
	}{
		{
			name: "node.annotation is nil",
			args: args{},
			want: &extension.NodeReservation{},
		},
		{
			name: "node.annotation not nil but nothing reserved",
			args: args{
				map[string]string{
					"k": "v",
				},
			},
			want: &extension.NodeReservation{},
		},
		{
			name: "node.annotation not nil but without cpu reserved",
			args: args{
				map[string]string{
					extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{}),
				},
			},
			want: &extension.NodeReservation{},
		},
		{
			name: "reserve cpu only by quantity",
			args: args{
				map[string]string{
					extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
						Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
					}),
				},
			},
			want: &extension.NodeReservation{ReservedCPUs: "0,6", Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}},
		},
		{
			name: "reserve cpu only by quantity but value not integer",
			args: args{
				map[string]string{
					extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
						Resources: corev1.ResourceList{corev1.ResourceCPU: proportionalQuantity},
					}),
				},
			},
			want: &extension.NodeReservation{ReservedCPUs: "0,2,6", Resources: corev1.ResourceList{corev1.ResourceCPU: proportionalQuantity}},
		},
		{
			name: "reserve cpu only by quantity but value is negative",
			args: args{
				map[string]string{
					extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
						Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("-2")},
					}),
				},
			},
			want: &extension.NodeReservation{Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("-2")}},
		},
		{
			name: "reserve cpu only by specific cpus",
			args: args{
				map[string]string{
					extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
						ReservedCPUs: "0-1",
					}),
				},
			},
			want: &extension.NodeReservation{ReservedCPUs: "0-1"},
		},
		{
			name: "reserve cpu only by specific cpus but core id is unavailable",
			args: args{
				map[string]string{
					extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
						ReservedCPUs: "-1",
					}),
				},
			},
			want: &extension.NodeReservation{},
		},
		{
			name: "reserve cpu by specific cpus and quantity",
			args: args{
				map[string]string{
					extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
						Resources:    corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10")},
						ReservedCPUs: "0-1",
					}),
				},
			},
			want: &extension.NodeReservation{ReservedCPUs: "0-1", Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10")}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getNodeReserved(&fakeTopo, tt.args.anno)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_removeSystemQOSCPUs(t *testing.T) {
	originCPUSharePool := []extension.CPUSharedPool{
		{
			Socket: 0,
			Node:   0,
			CPUSet: "0-7",
		},
		{
			Socket: 1,
			Node:   0,
			CPUSet: "8-15",
		},
	}
	type args struct {
		cpuSharePools []extension.CPUSharedPool
		sysQOSRes     *extension.SystemQOSResource
	}
	tests := []struct {
		name string
		args args
		want []extension.CPUSharedPool
	}{
		{
			name: "system qos res is nil",
			args: args{
				cpuSharePools: originCPUSharePool,
				sysQOSRes:     nil,
			},
			want: originCPUSharePool,
		},
		{
			name: "system qos res is empty cpuset",
			args: args{
				cpuSharePools: originCPUSharePool,
				sysQOSRes: &extension.SystemQOSResource{
					CPUSet: "",
				},
			},
			want: originCPUSharePool,
		},
		{
			name: "system qos res is not exclusive",
			args: args{
				cpuSharePools: originCPUSharePool,
				sysQOSRes: &extension.SystemQOSResource{
					CPUSet:          "0-3",
					CPUSetExclusive: ptr.To[bool](false),
				},
			},
			want: originCPUSharePool,
		},
		{
			name: "system qos with bad cpuset fmt",
			args: args{
				cpuSharePools: originCPUSharePool,
				sysQOSRes: &extension.SystemQOSResource{
					CPUSet: "0b",
				},
			},
			want: originCPUSharePool,
		},
		{
			name: "exclude cpuset from share pool",
			args: args{
				cpuSharePools: originCPUSharePool,
				sysQOSRes: &extension.SystemQOSResource{
					CPUSet: "0-3",
				},
			},
			want: []extension.CPUSharedPool{
				{
					Socket: 0,
					Node:   0,
					CPUSet: "4-7",
				},
				{
					Socket: 1,
					Node:   0,
					CPUSet: "8-15",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := removeSystemQOSCPUs(tt.args.cpuSharePools, tt.args.sysQOSRes); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("removeSystemQOSCPUs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getTopologyPolicy(t *testing.T) {
	type args struct {
		topologyManagerPolicy string
		topologyManagerScope  string
	}
	tests := []struct {
		name string
		args args
		want topologyv1alpha1.TopologyManagerPolicy
	}{
		{
			name: "get None policy by default",
			want: topologyv1alpha1.None,
		},
		{
			name: "get None policy by default 1",
			args: args{
				topologyManagerScope: kubeletconfiginternal.ContainerTopologyManagerScope,
			},
			want: topologyv1alpha1.None,
		},
		{
			name: "get None policy by default 2",
			args: args{
				topologyManagerPolicy: kubeletconfiginternal.BestEffortTopologyManagerPolicy,
			},
			want: topologyv1alpha1.None,
		},
		{
			name: "get container single numa policy",
			args: args{
				topologyManagerPolicy: kubeletconfiginternal.SingleNumaNodeTopologyManagerPolicy,
				topologyManagerScope:  kubeletconfiginternal.ContainerTopologyManagerScope,
			},
			want: topologyv1alpha1.SingleNUMANodeContainerLevel,
		},
		{
			name: "get container restricted policy",
			args: args{
				topologyManagerPolicy: kubeletconfiginternal.RestrictedTopologyManagerPolicy,
				topologyManagerScope:  kubeletconfiginternal.ContainerTopologyManagerScope,
			},
			want: topologyv1alpha1.RestrictedContainerLevel,
		},
		{
			name: "get container besteffort policy",
			args: args{
				topologyManagerPolicy: kubeletconfiginternal.BestEffortTopologyManagerPolicy,
				topologyManagerScope:  kubeletconfiginternal.ContainerTopologyManagerScope,
			},
			want: topologyv1alpha1.BestEffortContainerLevel,
		},
		{
			name: "get container none policy",
			args: args{
				topologyManagerPolicy: kubeletconfiginternal.NoneTopologyManagerPolicy,
				topologyManagerScope:  kubeletconfiginternal.ContainerTopologyManagerScope,
			},
			want: topologyv1alpha1.None,
		},
		{
			name: "get pod single numa policy",
			args: args{
				topologyManagerPolicy: kubeletconfiginternal.SingleNumaNodeTopologyManagerPolicy,
				topologyManagerScope:  kubeletconfiginternal.PodTopologyManagerScope,
			},
			want: topologyv1alpha1.SingleNUMANodePodLevel,
		},
		{
			name: "get pod restricted policy",
			args: args{
				topologyManagerPolicy: kubeletconfiginternal.RestrictedTopologyManagerPolicy,
				topologyManagerScope:  kubeletconfiginternal.PodTopologyManagerScope,
			},
			want: topologyv1alpha1.RestrictedPodLevel,
		},
		{
			name: "get pod besteffort policy",
			args: args{
				topologyManagerPolicy: kubeletconfiginternal.BestEffortTopologyManagerPolicy,
				topologyManagerScope:  kubeletconfiginternal.PodTopologyManagerScope,
			},
			want: topologyv1alpha1.BestEffortPodLevel,
		},
		{
			name: "get pod none policy",
			args: args{
				topologyManagerPolicy: kubeletconfiginternal.NoneTopologyManagerPolicy,
				topologyManagerScope:  kubeletconfiginternal.PodTopologyManagerScope,
			},
			want: topologyv1alpha1.None,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getTopologyPolicy(tt.args.topologyManagerPolicy, tt.args.topologyManagerScope)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_divideResourceByCount(t *testing.T) {
	tests := []struct {
		name     string
		quantity resource.Quantity
		count    int
		want     resource.Quantity
	}{
		{
			name:     "divide cpu 4000m by 2",
			quantity: resource.MustParse("4000m"),
			count:    2,
			want:     *resource.NewMilliQuantity(2000, resource.DecimalSI),
		},
		{
			name:     "divide cpu 4000m by 3 with ceiling",
			quantity: resource.MustParse("4000m"),
			count:    3,
			want:     *resource.NewMilliQuantity(1334, resource.DecimalSI),
		},
		{
			name:     "divide cpu 4 by 2",
			quantity: resource.MustParse("4"),
			count:    2,
			want:     *resource.NewMilliQuantity(2000, resource.DecimalSI),
		},
		{
			name:     "divide memory 10Gi by 4",
			quantity: resource.MustParse("10Gi"),
			count:    4,
			want:     *resource.NewMilliQuantity(2684354560000, resource.BinarySI),
		},
		{
			name:     "divide memory 10Gi by 3 with ceiling",
			quantity: resource.MustParse("10Gi"),
			count:    3,
			want:     *resource.NewMilliQuantity(3579139413334, resource.BinarySI),
		},
		{
			name:     "divide zero by 2",
			quantity: resource.MustParse("0"),
			count:    2,
			want:     *resource.NewMilliQuantity(0, resource.DecimalSI),
		},
		{
			name:     "divide by 1",
			quantity: resource.MustParse("5"),
			count:    1,
			want:     *resource.NewMilliQuantity(5000, resource.DecimalSI),
		},
		{
			name:     "divide by zero returns zero",
			quantity: resource.MustParse("4000m"),
			count:    0,
			want:     *resource.NewMilliQuantity(0, resource.DecimalSI),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := divideResourceByCount(tt.quantity, tt.count)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_applyNUMAReservationToZoneResources(t *testing.T) {
	tests := []struct {
		name          string
		zoneResources map[string]util.ZoneResources
		nodeReserved  corev1.ResourceList
		numaCount     int
		want          map[string]util.ZoneResources
	}{
		{
			name: "apply reservation to 2 numa nodes with cpu and memory",
			zoneResources: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
				},
				"node-1": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
				},
			},
			nodeReserved: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(4000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2147483648, resource.BinarySI),
			},
			numaCount: 2,
			want: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(46000, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewMilliQuantity(127775277056000, resource.BinarySI),
					},
				},
				"node-1": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(46000, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewMilliQuantity(127775277056000, resource.BinarySI),
					},
				},
			},
		},
		{
			name: "apply reservation to 3 numa nodes with ceiling division",
			zoneResources: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(85899345920, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(85899345920, resource.BinarySI),
					},
				},
				"node-1": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(85899345920, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(85899345920, resource.BinarySI),
					},
				},
				"node-2": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(85899345920, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(85899345920, resource.BinarySI),
					},
				},
			},
			nodeReserved: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(6000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(3221225472, resource.BinarySI),
			},
			numaCount: 3,
			want: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(85899345920, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(30000, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewMilliQuantity(84825604096000, resource.BinarySI),
					},
				},
				"node-1": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(85899345920, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(30000, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewMilliQuantity(84825604096000, resource.BinarySI),
					},
				},
				"node-2": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(32, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(85899345920, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewMilliQuantity(30000, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewMilliQuantity(84825604096000, resource.BinarySI),
					},
				},
			},
		},
		{
			name: "empty node reserved returns original",
			zoneResources: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
				},
			},
			nodeReserved: corev1.ResourceList{},
			numaCount:    1,
			want: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
				},
			},
		},
		{
			name: "zero numa count returns original",
			zoneResources: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
				},
			},
			nodeReserved: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(4000, resource.DecimalSI),
			},
			numaCount: 0,
			want: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(48, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(128849018880, resource.BinarySI),
					},
				},
			},
		},
		{
			name: "allocatable not less than zero",
			zoneResources: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(1, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(1073741824, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(1, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(1073741824, resource.BinarySI),
					},
				},
			},
			nodeReserved: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(8589934592, resource.BinarySI),
			},
			numaCount: 1,
			want: map[string]util.ZoneResources{
				"node-0": {
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(1, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(1073741824, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(0, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(0, resource.BinarySI),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyNUMAReservationToZoneResources(tt.zoneResources, tt.nodeReserved, tt.numaCount)
			for zoneName, wantZoneRes := range tt.want {
				gotZoneRes, exists := got[zoneName]
				assert.True(t, exists, "zone %s should exist", zoneName)

				// Check capacity
				for resourceName, wantQty := range wantZoneRes.Capacity {
					gotQty, exists := gotZoneRes.Capacity[resourceName]
					assert.True(t, exists, "capacity resource %s should exist in zone %s", resourceName, zoneName)
					assert.Equal(t, wantQty, gotQty, "capacity resource %s in zone %s should match", resourceName, zoneName)
				}

				// Check allocatable
				for resourceName, wantQty := range wantZoneRes.Allocatable {
					gotQty, exists := gotZoneRes.Allocatable[resourceName]
					assert.True(t, exists, "allocatable resource %s should exist in zone %s", resourceName, zoneName)
					assert.Equal(t, wantQty, gotQty, "allocatable resource %s in zone %s should match", resourceName, zoneName)
				}
			}
		})
	}
}

// Test_extendNRTStatusFn verifies that the extendNRTStatusFn hook is invoked during calcNodeTopo
// and can inject extra fields into nodeTopologyStatus.
func Test_extendNRTStatusFn(t *testing.T) {
	// Save and restore the original hook.
	origFn := extendNRTStatusFn
	defer func() { extendNRTStatusFn = origFn }()

	called := false
	var capturedNode *corev1.Node
	var capturedNumZones int
	const extraAnnotationKey = "test.koordinator.sh/extra"
	const extraAnnotationVal = "extra-value"

	extendNRTStatusFn = func(status *nodeTopologyStatus, node *corev1.Node, numZones int) error {
		called = true
		capturedNode = node
		capturedNumZones = numZones
		if status.Annotations == nil {
			status.Annotations = map[string]string{}
		}
		status.Annotations[extraAnnotationKey] = extraAnnotationVal
		return nil
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(8589934592, resource.BinarySI),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(8589934592, resource.BinarySI),
			},
		},
	}

	nodeCPUInfo := &metriccache.NodeCPUInfo{
		ProcessorInfos: []koordletutil.ProcessorInfo{
			{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 1, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 2, CoreID: 2, SocketID: 0, NodeID: 1},
			{CPUID: 3, CoreID: 3, SocketID: 0, NodeID: 1},
		},
		TotalInfo: koordletutil.CPUTotalInfo{
			NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
				0: {{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0}, {CPUID: 1, CoreID: 1, SocketID: 0, NodeID: 0}},
				1: {{CPUID: 2, CoreID: 2, SocketID: 0, NodeID: 1}, {CPUID: 3, CoreID: 3, SocketID: 0, NodeID: 1}},
			},
		},
	}

	nodeNUMAInfo := &koordletutil.NodeNUMAInfo{
		NUMAInfos: []koordletutil.NUMAInfo{
			{NUMANodeID: 0, MemInfo: &koordletutil.MemInfo{MemTotal: 4194304}},
			{NUMANodeID: 1, MemInfo: &koordletutil.MemInfo{MemTotal: 4194304}},
		},
		MemInfoMap: map[int32]*koordletutil.MemInfo{
			0: {MemTotal: 4194304},
			1: {MemTotal: 4194304},
		},
	}

	mockMetricCache := mock_metriccache.NewMockMetricCache(ctrl)
	mockMetricCache.EXPECT().Get(metriccache.NodeCPUInfoKey).Return(nodeCPUInfo, true).AnyTimes()
	mockMetricCache.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(nodeNUMAInfo, true).AnyTimes()

	informer := &nodeTopoInformer{
		config:       NewDefaultConfig(),
		metricCache:  mockMetricCache,
		nodeInformer: &nodeInformer{node: testNode},
		podsInformer: &podsInformer{},
		kubelet: &testKubeletStub{
			config: &kubeletconfiginternal.KubeletConfiguration{
				CPUManagerPolicy: "none",
			},
		},
	}

	status, err := informer.calcNodeTopo()
	assert.NoError(t, err)
	assert.True(t, called, "extendNRTStatusFn should be called")
	assert.Equal(t, testNode, capturedNode)
	assert.Equal(t, 2, capturedNumZones)
	assert.Equal(t, extraAnnotationVal, status.Annotations[extraAnnotationKey],
		"extra annotation injected by extendNRTStatusFn should be present")
}

// Test_extendNRTStatusFn_error verifies that if extendNRTStatusFn returns an error,
// calcNodeTopo propagates it.
func Test_extendNRTStatusFn_error(t *testing.T) {
	origFn := extendNRTStatusFn
	defer func() { extendNRTStatusFn = origFn }()

	extendNRTStatusFn = func(_ *nodeTopologyStatus, _ *corev1.Node, _ int) error {
		return fmt.Errorf("extend status error")
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI), corev1.ResourceMemory: *resource.NewQuantity(4294967296, resource.BinarySI)},
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: *resource.NewQuantity(2, resource.DecimalSI), corev1.ResourceMemory: *resource.NewQuantity(4294967296, resource.BinarySI)},
		},
	}
	nodeCPUInfo := &metriccache.NodeCPUInfo{
		ProcessorInfos: []koordletutil.ProcessorInfo{
			{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 1, CoreID: 1, SocketID: 0, NodeID: 0},
		},
		TotalInfo: koordletutil.CPUTotalInfo{
			NodeToCPU: map[int32][]koordletutil.ProcessorInfo{
				0: {{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0}, {CPUID: 1, CoreID: 1, SocketID: 0, NodeID: 0}},
			},
		},
	}
	nodeNUMAInfo := &koordletutil.NodeNUMAInfo{
		NUMAInfos:  []koordletutil.NUMAInfo{{NUMANodeID: 0, MemInfo: &koordletutil.MemInfo{MemTotal: 4194304}}},
		MemInfoMap: map[int32]*koordletutil.MemInfo{0: {MemTotal: 4194304}},
	}
	mockMetricCache := mock_metriccache.NewMockMetricCache(ctrl)
	mockMetricCache.EXPECT().Get(metriccache.NodeCPUInfoKey).Return(nodeCPUInfo, true).AnyTimes()
	mockMetricCache.EXPECT().Get(metriccache.NodeNUMAInfoKey).Return(nodeNUMAInfo, true).AnyTimes()

	informer := &nodeTopoInformer{
		config:       NewDefaultConfig(),
		metricCache:  mockMetricCache,
		nodeInformer: &nodeInformer{node: testNode},
		podsInformer: &podsInformer{},
		kubelet: &testKubeletStub{
			config: &kubeletconfiginternal.KubeletConfiguration{CPUManagerPolicy: "none"},
		},
	}

	_, err := informer.calcNodeTopo()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "extend status error")
}

// Test_extendNRTEqualFn verifies that the extendNRTEqualFn hook is invoked during isChanged
// and can force a change to be reported.
func Test_extendNRTEqualFn(t *testing.T) {
	origFn := extendNRTEqualFn
	defer func() { extendNRTEqualFn = origFn }()

	tests := []struct {
		name       string
		hookResult bool
		hookMsg    string
		wantChange bool
		wantMsg    string
	}{
		{
			name:       "hook returns equal",
			hookResult: true,
			hookMsg:    "",
			wantChange: false,
			wantMsg:    "",
		},
		{
			name:       "hook returns not equal",
			hookResult: false,
			hookMsg:    "extra field changed",
			wantChange: true,
			wantMsg:    "extended status changed, item: extra field changed",
		},
	}

	// A fixed oldNRT and newTopoStatus that are otherwise equal.
	oldNRT := &topologyv1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				extension.AnnotationKubeletCPUManagerPolicy: `{"policy":"none"}`,
				extension.AnnotationNodeCPUSharedPools:      `[]`,
				extension.AnnotationNodeBECPUSharedPools:    `[]`,
				extension.AnnotationNodeCPUTopology:         `{"detail":[]}`,
				extension.AnnotationNodeCPUAllocs:           `null`,
				extension.AnnotationCPUBasicInfo:            `{}`,
			},
		},
		TopologyPolicies: []string{string(topologyv1alpha1.None)},
		Zones:            topologyv1alpha1.ZoneList{},
	}
	newStatus := &nodeTopologyStatus{
		Annotations: map[string]string{
			extension.AnnotationKubeletCPUManagerPolicy: `{"policy":"none"}`,
			extension.AnnotationNodeCPUSharedPools:      `[]`,
			extension.AnnotationNodeBECPUSharedPools:    `[]`,
			extension.AnnotationNodeCPUTopology:         `{"detail":[]}`,
			extension.AnnotationNodeCPUAllocs:           `null`,
			extension.AnnotationCPUBasicInfo:            `{}`,
		},
		TopologyPolicy: topologyv1alpha1.None,
		Zones:          topologyv1alpha1.ZoneList{},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hookIsEqual := tt.hookResult
			hookMsg := tt.hookMsg
			extendNRTEqualFn = func(_ *nodeTopologyStatus, _ *topologyv1alpha1.NodeResourceTopology) (bool, string) {
				return hookIsEqual, hookMsg
			}
			gotChanged, gotMsg := newStatus.isChanged(oldNRT)
			assert.Equal(t, tt.wantChange, gotChanged)
			assert.Equal(t, tt.wantMsg, gotMsg)
		})
	}
}

// Test_mergeNRTZoneFn verifies that the mergeNRTZoneFn hook controls how zones are merged
// when writing to the NRT object.
func Test_mergeNRTZoneFn(t *testing.T) {
	origFn := mergeNRTZoneFn
	defer func() { mergeNRTZoneFn = origFn }()

	customZone := topologyv1alpha1.ZoneList{
		{Name: "custom-zone", Type: "Node"},
	}
	mergeNRTZoneFn = func(_ *topologyv1alpha1.NodeResourceTopology, zoneList topologyv1alpha1.ZoneList) topologyv1alpha1.ZoneList {
		// Return a custom zone list regardless of inputs.
		return customZone
	}

	status := &nodeTopologyStatus{
		Zones: topologyv1alpha1.ZoneList{
			{Name: "node-0", Type: "Node"},
			{Name: "node-1", Type: "Node"},
		},
	}
	nrt := &topologyv1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
	}
	status.updateNRT(nrt)
	assert.Equal(t, customZone, nrt.Zones,
		"mergeNRTZoneFn override should replace the zone list")
}

// Test_managedNRTAnnotationKeys_extended verifies that dynamically appended annotation keys
// are tracked by isEqualNRTAnnotations.
func Test_managedNRTAnnotationKeys_extended(t *testing.T) {
	origKeys := managedNRTAnnotationKeys
	defer func() { managedNRTAnnotationKeys = origKeys }()

	const extraKey = "test.koordinator.sh/extra-annotation"
	managedNRTAnnotationKeys = append(managedNRTAnnotationKeys, extraKey)

	// Both have the extra key -> equal.
	isEqual, key := isEqualNRTAnnotations(
		map[string]string{extraKey: `"v1"`},
		map[string]string{extraKey: `"v1"`},
	)
	assert.True(t, isEqual)
	assert.Empty(t, key)

	// New has the extra key, old does not -> not equal.
	isEqual, key = isEqualNRTAnnotations(
		map[string]string{},
		map[string]string{extraKey: `"v2"`},
	)
	assert.False(t, isEqual)
	assert.Equal(t, extraKey, key)

	// Old has the extra key, new does not -> not equal.
	isEqual, key = isEqualNRTAnnotations(
		map[string]string{extraKey: `"v2"`},
		map[string]string{},
	)
	assert.False(t, isEqual)
	assert.Equal(t, extraKey, key)

	// Values differ -> not equal.
	isEqual, key = isEqualNRTAnnotations(
		map[string]string{extraKey: `"v1"`},
		map[string]string{extraKey: `"v2"`},
	)
	assert.False(t, isEqual)
	assert.Equal(t, extraKey, key)
}

// Test_managedNRTLabelKeys_extended verifies that dynamically appended label keys
// are tracked by isEqualNRTLabels.
func Test_managedNRTLabelKeys_extended(t *testing.T) {
	origKeys := managedNRTLabelKeys
	defer func() { managedNRTLabelKeys = origKeys }()

	const extraLabelKey = "test.koordinator.sh/extra-label"
	managedNRTLabelKeys = append(managedNRTLabelKeys, extraLabelKey)

	// Same value -> equal.
	isEqual, key := isEqualNRTLabels(
		map[string]string{extraLabelKey: "true"},
		map[string]string{extraLabelKey: "true"},
	)
	assert.True(t, isEqual)
	assert.Empty(t, key)

	// Different value -> not equal.
	isEqual, key = isEqualNRTLabels(
		map[string]string{extraLabelKey: "true"},
		map[string]string{extraLabelKey: "false"},
	)
	assert.False(t, isEqual)
	assert.Equal(t, extraLabelKey, key)

	// Old missing, new has it -> not equal.
	isEqual, key = isEqualNRTLabels(
		map[string]string{},
		map[string]string{extraLabelKey: "true"},
	)
	assert.False(t, isEqual)
	assert.Equal(t, extraLabelKey, key)
}

// Test_managedNRTResources_extended verifies that dynamically appended resource names
// are tracked by isEqualNRTZones.
func Test_managedNRTResources_extended(t *testing.T) {
	origResources := managedNRTResources
	defer func() { managedNRTResources = origResources }()

	const extraResource corev1.ResourceName = "example.com/fpga"
	managedNRTResources = append(managedNRTResources, extraResource)

	zoneWithExtra := topologyv1alpha1.ZoneList{
		{
			Name: "node-0",
			Type: "Node",
			Resources: topologyv1alpha1.ResourceInfoList{
				{Name: string(extraResource), Available: resource.MustParse("4"), Capacity: resource.MustParse("4"), Allocatable: resource.MustParse("4")},
			},
		},
	}
	zoneWithExtraDiff := topologyv1alpha1.ZoneList{
		{
			Name: "node-0",
			Type: "Node",
			Resources: topologyv1alpha1.ResourceInfoList{
				{Name: string(extraResource), Available: resource.MustParse("2"), Capacity: resource.MustParse("4"), Allocatable: resource.MustParse("2")},
			},
		},
	}

	// Same zones -> equal.
	isEqual, msg := isEqualNRTZones(zoneWithExtra, zoneWithExtra)
	assert.True(t, isEqual, msg)

	// Different extended resource quantity -> not equal.
	isEqual, msg = isEqualNRTZones(zoneWithExtra, zoneWithExtraDiff)
	assert.False(t, isEqual, msg)
}

func Test_nodeTopoInformer_getNodeKubeletReservedResources(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want corev1.ResourceList
	}{
		{
			name: "calculate reserved from capacity and allocatable",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(96, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(257698037760, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(94, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(255550554112, resource.BinarySI),
					},
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(2147483648, resource.BinarySI),
			},
		},
		{
			name: "no reservation when capacity equals allocatable",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(96, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(257698037760, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(96, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(257698037760, resource.BinarySI),
					},
				},
			},
			want: corev1.ResourceList{},
		},
		{
			name: "nil node returns empty",
			node: nil,
			want: corev1.ResourceList{},
		},
		{
			name: "missing capacity fields",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    *resource.NewQuantity(96, resource.DecimalSI),
						corev1.ResourceMemory: *resource.NewQuantity(257698037760, resource.BinarySI),
					},
				},
			},
			want: corev1.ResourceList{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			informer := &nodeTopoInformer{
				nodeInformer: &nodeInformer{
					node: tt.node,
				},
			}
			got := informer.getNodeKubeletReservedResources()
			for resourceName, wantQty := range tt.want {
				gotQty, exists := got[resourceName]
				assert.True(t, exists, "resource %s should exist", resourceName)
				assert.Equal(t, wantQty, gotQty, "resource %s should match", resourceName)
			}
			for resourceName := range got {
				if _, exists := tt.want[resourceName]; !exists {
					t.Errorf("unexpected resource %s in result", resourceName)
				}
			}
		})
	}
}
