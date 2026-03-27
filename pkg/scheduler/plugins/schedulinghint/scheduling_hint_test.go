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

package schedulinghint

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	configv1 "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
)

func TestPlugin_Name(t *testing.T) {
	p := &Plugin{maxHintNodes: 100}
	assert.Equal(t, Name, p.Name())
}

func TestPlugin_PreFilter(t *testing.T) {
	p := &Plugin{maxHintNodes: 100}
	state := framework.NewCycleState()
	pod := &corev1.Pod{}
	res, status := p.PreFilter(context.TODO(), state, pod)
	assert.Nil(t, res)
	assert.Nil(t, status)

	hintPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				extension.AnnotationSchedulingHint: "{\"nodeNames\":[\"node-1\",\"node-2\"]}",
			},
		},
	}
	state = framework.NewCycleState()
	_, _, _ = p.BeforePreFilter(context.TODO(), state, hintPod)
	res, status = p.PreFilter(context.TODO(), state, hintPod)
	assert.Nil(t, status)
	assert.Equal(t, &framework.PreFilterResult{NodeNames: sets.New("node-1", "node-2")}, res)
	_ = p.AfterPreFilter(context.TODO(), state, hintPod, res)
}

func TestPlugin_PreFilterExtensions(t *testing.T) {
	p := &Plugin{maxHintNodes: 100}
	assert.Nil(t, p.PreFilterExtensions())
}

func TestPlugin_AfterPreFilter(t *testing.T) {
	p := &Plugin{maxHintNodes: 100}
	state := framework.NewCycleState()
	pod := &corev1.Pod{}
	status := p.AfterPreFilter(context.TODO(), state, pod, nil)
	assert.Nil(t, status)
}

func TestPlugin_BeforePreFilter(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		wantModified   bool
		wantStatusNil  bool
		wantStatusCode framework.Code
		wantHintState  *hinter.SchedulingHintStateData
	}{
		{
			name:           "nil pod",
			pod:            nil,
			wantModified:   false,
			wantStatusNil:  true,
			wantHintState:  nil,
			wantStatusCode: framework.Success,
		},
		{
			name: "no scheduling hint annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"other-annotation": "value",
					},
				},
			},
			wantModified:   false,
			wantStatusNil:  true,
			wantHintState:  nil,
			wantStatusCode: framework.Success,
		},
		{
			name: "valid scheduling hint with node names and extensions",
			pod: func() *corev1.Pod {
				h := &extension.SchedulingHint{
					NodeNames: []string{"node-1", "node-2"},
					Extensions: map[string]interface{}{
						"key": "value",
					},
				}
				b, _ := json.Marshal(h)
				return &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationSchedulingHint: string(b),
						},
					},
				}
			}(),
			wantModified:  false,
			wantStatusNil: true,
			wantHintState: &hinter.SchedulingHintStateData{
				PreFilterNodes: []string{"node-1", "node-2"},
				Extensions:     map[string]interface{}{"key": "value"},
			},
			wantStatusCode: framework.Success,
		},
		{
			name: "valid scheduling hint with preferred nodes",
			pod: func() *corev1.Pod {
				h := &extension.SchedulingHint{
					NodeNames:          []string{"node-1", "node-2", "node-3"},
					PreferredNodeNames: []string{"node-1", "node-2"},
					Extensions: map[string]interface{}{
						"key": "value",
					},
				}
				b, _ := json.Marshal(h)
				return &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationSchedulingHint: string(b),
						},
					},
				}
			}(),
			wantModified:  false,
			wantStatusNil: true,
			wantHintState: &hinter.SchedulingHintStateData{
				PreFilterNodes: []string{"node-1", "node-2", "node-3"},
				PreferredNodes: []string{"node-1", "node-2"},
				Extensions:     map[string]interface{}{"key": "value"},
			},
			wantStatusCode: framework.Success,
		},
		{
			name: "invalid scheduling hint json",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationSchedulingHint: "invalid-json",
					},
				},
			},
			wantModified:   false,
			wantStatusNil:  false,
			wantHintState:  nil,
			wantStatusCode: framework.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{maxHintNodes: 100}
			state := framework.NewCycleState()

			retPod, modified, status := p.BeforePreFilter(context.TODO(), state, tt.pod)

			assert.Nil(t, retPod)
			assert.Equal(t, tt.wantModified, modified)

			if tt.wantStatusNil {
				assert.Nil(t, status)
			} else {
				assert.NotNil(t, status)
				assert.Equal(t, tt.wantStatusCode, status.Code())
			}

			hintState := hinter.GetSchedulingHintState(state)
			if tt.wantHintState == nil {
				assert.Nil(t, hintState)
			} else {
				assert.NotNil(t, hintState)
				assert.Equal(t, tt.wantHintState.PreFilterNodes, hintState.PreFilterNodes)
				assert.Equal(t, tt.wantHintState.PreferredNodes, hintState.PreferredNodes)
				assert.Equal(t, tt.wantHintState.Extensions, hintState.Extensions)
			}
		})
	}
}

var _ framework.SharedLister = &testSharedLister{}

type testSharedLister struct {
	nodeInfos   []*framework.NodeInfo
	nodeInfoMap map[string]*framework.NodeInfo
}

func newTestSharedLister(pods []*corev1.Pod, nodes []*corev1.Node) *testSharedLister {
	nodeInfoMap := make(map[string]*framework.NodeInfo)
	nodeInfos := make([]*framework.NodeInfo, 0)
	for _, pod := range pods {
		nodeName := pod.Spec.NodeName
		if _, ok := nodeInfoMap[nodeName]; !ok {
			nodeInfoMap[nodeName] = framework.NewNodeInfo()
		}
		nodeInfoMap[nodeName].AddPod(pod)
	}
	for _, node := range nodes {
		if _, ok := nodeInfoMap[node.Name]; !ok {
			nodeInfoMap[node.Name] = framework.NewNodeInfo()
		}
		nodeInfoMap[node.Name].SetNode(node)
	}
	for _, v := range nodeInfoMap {
		nodeInfos = append(nodeInfos, v)
	}
	return &testSharedLister{nodeInfos: nodeInfos, nodeInfoMap: nodeInfoMap}
}

func (f *testSharedLister) StorageInfos() framework.StorageInfoLister { return f }
func (f *testSharedLister) IsPVCUsedByPods(key string) bool           { return false }
func (f *testSharedLister) NodeInfos() framework.NodeInfoLister       { return f }
func (f *testSharedLister) List() ([]*framework.NodeInfo, error)      { return f.nodeInfos, nil }
func (f *testSharedLister) HavePodsWithAffinityList() ([]*framework.NodeInfo, error) {
	return nil, nil
}
func (f *testSharedLister) HavePodsWithRequiredAntiAffinityList() ([]*framework.NodeInfo, error) {
	return nil, nil
}
func (f *testSharedLister) Get(nodeName string) (*framework.NodeInfo, error) {
	return f.nodeInfoMap[nodeName], nil
}

func TestNew(t *testing.T) {
	// Build the default args via the v1 conversion chain.
	var v1Args configv1.SchedulingHintArgs
	configv1.SetDefaults_SchedulingHintArgs(&v1Args)
	var defaultArgs config.SchedulingHintArgs
	err := configv1.Convert_v1_SchedulingHintArgs_To_config_SchedulingHintArgs(&v1Args, &defaultArgs, nil)
	assert.NoError(t, err)
	assert.Equal(t, int32(100), defaultArgs.MaxHintNodes)

	tests := []struct {
		name    string
		args    runtime.Object
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid args with default MaxHintNodes",
			args:    &config.SchedulingHintArgs{MaxHintNodes: 100},
			wantErr: false,
		},
		{
			name:    "valid args with custom MaxHintNodes",
			args:    &config.SchedulingHintArgs{MaxHintNodes: 50},
			wantErr: false,
		},
		{
			name:    "wrong args type",
			args:    &config.LoadAwareSchedulingArgs{},
			wantErr: true,
			errMsg:  "want args to be of type SchedulingHintArgs",
		},
		{
			name:    "invalid args - MaxHintNodes is zero",
			args:    &config.SchedulingHintArgs{MaxHintNodes: 0},
			wantErr: true,
			errMsg:  "maxHintNodes",
		},
		{
			name:    "invalid args - MaxHintNodes is negative",
			args:    &config.SchedulingHintArgs{MaxHintNodes: -1},
			wantErr: true,
			errMsg:  "maxHintNodes",
		},
	}

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, _ := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, New)

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		frameworkruntime.WithClientSet(cs),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithSnapshotSharedLister(newTestSharedLister(nil, nil)),
	)
	assert.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := proxyNew(tt.args, fh)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				assert.Nil(t, p)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, p)
				assert.Equal(t, Name, p.Name())
			}
		})
	}

	// Test handle not being an ExtendedHandle.
	t.Run("handle is not ExtendedHandle", func(t *testing.T) {
		var handle framework.Handle
		args := &config.SchedulingHintArgs{MaxHintNodes: 100}
		p, err := New(args, handle)
		assert.Nil(t, p)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "frameworkext.ExtendedHandle")
	})
}

func TestPlugin_PreferNodesPlugin(t *testing.T) {
	p := &Plugin{maxHintNodes: 100}
	assert.Equal(t, p, p.PreferNodesPlugin())
}

func TestPlugin_PreferNodes(t *testing.T) {
	tests := []struct {
		name              string
		setupState        func() *framework.CycleState
		pod               *corev1.Pod
		preFilterResult   *framework.PreFilterResult
		wantNodeNames     []string
		wantStatusCode    framework.Code
		wantStatusMessage string
	}{
		{
			name: "no scheduling hint state",
			setupState: func() *framework.CycleState {
				return framework.NewCycleState()
			},
			pod:            &corev1.Pod{},
			wantNodeNames:  nil,
			wantStatusCode: framework.Skip,
		},
		{
			name: "empty preferred nodes",
			setupState: func() *framework.CycleState {
				state := framework.NewCycleState()
				hinter.SetSchedulingHintState(state, &hinter.SchedulingHintStateData{
					PreferredNodes: []string{},
				})
				return state
			},
			pod:            &corev1.Pod{},
			wantNodeNames:  nil,
			wantStatusCode: framework.Skip,
		},
		{
			name: "preferred nodes with nil prefilter result",
			setupState: func() *framework.CycleState {
				state := framework.NewCycleState()
				hinter.SetSchedulingHintState(state, &hinter.SchedulingHintStateData{
					PreferredNodes: []string{"node-1", "node-2"},
				})
				return state
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			preFilterResult: nil,
			wantNodeNames:   []string{"node-1", "node-2"},
			wantStatusCode:  framework.Success,
		},
		{
			name: "preferred nodes with AllNodes prefilter result",
			setupState: func() *framework.CycleState {
				state := framework.NewCycleState()
				hinter.SetSchedulingHintState(state, &hinter.SchedulingHintStateData{
					PreferredNodes: []string{"node-1", "node-2"},
				})
				return state
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			preFilterResult: &framework.PreFilterResult{},
			wantNodeNames:   []string{"node-1", "node-2"},
			wantStatusCode:  framework.Success,
		},
		{
			name: "some preferred nodes in prefilter result node names - return filtered",
			setupState: func() *framework.CycleState {
				state := framework.NewCycleState()
				hinter.SetSchedulingHintState(state, &hinter.SchedulingHintStateData{
					PreferredNodes: []string{"node-1", "node-2", "node-3"},
				})
				return state
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			preFilterResult: &framework.PreFilterResult{
				NodeNames: sets.New("node-1", "node-4"),
			},
			wantNodeNames:  []string{"node-1"},
			wantStatusCode: framework.Success,
		},
		{
			name: "multiple preferred nodes match prefilter result - maintain order",
			setupState: func() *framework.CycleState {
				state := framework.NewCycleState()
				hinter.SetSchedulingHintState(state, &hinter.SchedulingHintStateData{
					PreferredNodes: []string{"node-1", "node-2", "node-3", "node-4"},
				})
				return state
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			preFilterResult: &framework.PreFilterResult{
				NodeNames: sets.New("node-2", "node-4", "node-5"),
			},
			wantNodeNames:  []string{"node-2", "node-4"},
			wantStatusCode: framework.Success,
		},
		{
			name: "no preferred nodes in prefilter result node names - skip",
			setupState: func() *framework.CycleState {
				state := framework.NewCycleState()
				hinter.SetSchedulingHintState(state, &hinter.SchedulingHintStateData{
					PreferredNodes: []string{"node-3", "node-4"},
				})
				return state
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			preFilterResult: &framework.PreFilterResult{
				NodeNames: sets.New("node-1", "node-2"),
			},
			wantNodeNames:  nil,
			wantStatusCode: framework.Skip,
		},
		{
			name: "preferred nodes exceed maxHintNodes - truncated to first maxHintNodes",
			setupState: func() *framework.CycleState {
				state := framework.NewCycleState()
				// build a list larger than maxHintNodes (100)
				nodes := make([]string, 110)
				for i := range nodes {
					nodes[i] = fmt.Sprintf("node-%d", i)
				}
				hinter.SetSchedulingHintState(state, &hinter.SchedulingHintStateData{
					PreferredNodes: nodes,
				})
				return state
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			preFilterResult: nil,
			wantNodeNames: func() []string {
				nodes := make([]string, 100)
				for i := range nodes {
					nodes[i] = fmt.Sprintf("node-%d", i)
				}
				return nodes
			}(),
			wantStatusCode: framework.Success,
		},
		{
			name: "preferred nodes exactly at maxHintNodes - no truncation",
			setupState: func() *framework.CycleState {
				state := framework.NewCycleState()
				nodes := make([]string, 100)
				for i := range nodes {
					nodes[i] = fmt.Sprintf("node-%d", i)
				}
				hinter.SetSchedulingHintState(state, &hinter.SchedulingHintStateData{
					PreferredNodes: nodes,
				})
				return state
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			preFilterResult: nil,
			wantNodeNames: func() []string {
				nodes := make([]string, 100)
				for i := range nodes {
					nodes[i] = fmt.Sprintf("node-%d", i)
				}
				return nodes
			}(),
			wantStatusCode: framework.Success,
		},
		{
			name: "preferred nodes exceed maxHintNodes and intersect with prefilter result - truncated first, then intersected",
			setupState: func() *framework.CycleState {
				state := framework.NewCycleState()
				nodes := make([]string, 110)
				for i := range nodes {
					nodes[i] = fmt.Sprintf("node-%d", i)
				}
				hinter.SetSchedulingHintState(state, &hinter.SchedulingHintStateData{
					PreferredNodes: nodes,
				})
				return state
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			// Only nodes past the truncation boundary (node-100..node-109) appear in preFilterResult;
			// after truncation to maxHintNodes the list ends at node-99, so all are filtered out.
			preFilterResult: &framework.PreFilterResult{
				NodeNames: sets.New("node-100", "node-105"),
			},
			wantNodeNames:  nil,
			wantStatusCode: framework.Skip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{maxHintNodes: 100}
			state := tt.setupState()

			nodeNames, status := p.PreferNodes(context.TODO(), state, tt.pod, tt.preFilterResult)

			assert.Equal(t, tt.wantNodeNames, nodeNames)
			assert.Equal(t, tt.wantStatusCode, status.Code())
			if tt.wantStatusMessage != "" {
				assert.Contains(t, status.Message(), tt.wantStatusMessage)
			}
		})
	}
}
