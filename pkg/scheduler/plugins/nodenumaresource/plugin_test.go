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

package nodenumaresource

import (
	"context"
	"reflect"
	"testing"

	nrtfake "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1beta2"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"

	_ "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/scheme"
)

var _ framework.SharedLister = &testSharedLister{}

type testSharedLister struct {
	nodes       []*corev1.Node
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

	return &testSharedLister{
		nodes:       nodes,
		nodeInfos:   nodeInfos,
		nodeInfoMap: nodeInfoMap,
	}
}

func (f *testSharedLister) NodeInfos() framework.NodeInfoLister {
	return f
}

func (f *testSharedLister) List() ([]*framework.NodeInfo, error) {
	return f.nodeInfos, nil
}

func (f *testSharedLister) HavePodsWithAffinityList() ([]*framework.NodeInfo, error) {
	return nil, nil
}

func (f *testSharedLister) HavePodsWithRequiredAntiAffinityList() ([]*framework.NodeInfo, error) {
	return nil, nil
}

func (f *testSharedLister) Get(nodeName string) (*framework.NodeInfo, error) {
	return f.nodeInfoMap[nodeName], nil
}

type frameworkHandleExtender struct {
	frameworkext.FrameworkExtender
	*nrtfake.Clientset
}

type pluginTestSuit struct {
	framework.Handle
	ExtenderFactory      *frameworkext.FrameworkExtenderFactory
	Extender             frameworkext.FrameworkExtender
	NRTClientset         *nrtfake.Clientset
	KoordClientSet       *koordfake.Clientset
	proxyNew             runtime.PluginFactory
	nodeNUMAResourceArgs *schedulingconfig.NodeNUMAResourceArgs
}

func newPluginTestSuit(t *testing.T, nodes []*corev1.Node) *pluginTestSuit {
	var v1beta2args v1beta2.NodeNUMAResourceArgs
	v1beta2.SetDefaults_NodeNUMAResourceArgs(&v1beta2args)
	var nodeNUMAResourceArgs schedulingconfig.NodeNUMAResourceArgs
	err := v1beta2.Convert_v1beta2_NodeNUMAResourceArgs_To_config_NodeNUMAResourceArgs(&v1beta2args, &nodeNUMAResourceArgs, nil)
	assert.NoError(t, err)

	nrtClientSet := nrtfake.NewSimpleClientset()
	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	assert.NoError(t, err)

	proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, func(configuration apiruntime.Object, f framework.Handle) (framework.Plugin, error) {
		return New(configuration, &frameworkHandleExtender{
			FrameworkExtender: f.(frameworkext.FrameworkExtender),
			Clientset:         nrtClientSet,
		})
	})

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}

	cs := kubefake.NewSimpleClientset()
	for _, v := range nodes {
		_, err := cs.CoreV1().Nodes().Create(context.TODO(), v, metav1.CreateOptions{})
		assert.NoError(t, err)
	}
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newTestSharedLister(nil, nodes)
	fh, err := schedulertesting.NewFramework(
		registeredPlugins,
		"koord-scheduler",
		runtime.WithClientSet(cs),
		runtime.WithInformerFactory(informerFactory),
		runtime.WithSnapshotSharedLister(snapshot),
	)
	assert.Nil(t, err)

	extender := extenderFactory.NewFrameworkExtender(fh)

	return &pluginTestSuit{
		Handle:               fh,
		ExtenderFactory:      extenderFactory,
		Extender:             extender,
		NRTClientset:         nrtClientSet,
		KoordClientSet:       koordClientSet,
		proxyNew:             proxyNew,
		nodeNUMAResourceArgs: &nodeNUMAResourceArgs,
	}
}

func (p *pluginTestSuit) start() {
	ctx := context.TODO()
	p.Handle.SharedInformerFactory().Start(ctx.Done())
	p.Handle.SharedInformerFactory().WaitForCacheSync(ctx.Done())
}

func TestNew(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)
	assert.Equal(t, Name, p.Name())
}

func TestPlugin_PreFilter(t *testing.T) {
	tests := []struct {
		name              string
		pod               *corev1.Pod
		defaultBindPolicy schedulingconfig.CPUBindPolicy
		want              *framework.Status
		wantState         *preFilterState
	}{
		{
			name: "cpu set with LSR Prod Pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"preferredCPUBindPolicy": "FullPCPUs"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "container-1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				requestCPUBind: true,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				resourceSpec:           &extension.ResourceSpec{PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
		},
		{
			name: "cpu set with LSE Prod Pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSE),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"preferredCPUBindPolicy": "FullPCPUs"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "container-1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				requestCPUBind: true,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				resourceSpec:           &extension.ResourceSpec{PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
		},
		{
			name: "cpu set with LSR Prod Pod and required CPUBindPolicy",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"requiredCPUBindPolicy": "FullPCPUs"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "container-1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				requestCPUBind: true,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				resourceSpec:           &extension.ResourceSpec{RequiredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs, PreferredCPUBindPolicy: extension.CPUBindPolicyDefault},
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicyFullPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
		},
		{
			name: "cpu set with LSR Prod Pod and required CPUBindPolicy by default",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"requiredCPUBindPolicy": "Default"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "container-1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			defaultBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
			wantState: &preFilterState{
				requestCPUBind: true,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				resourceSpec:           &extension.ResourceSpec{RequiredCPUBindPolicy: extension.CPUBindPolicyDefault, PreferredCPUBindPolicy: extension.CPUBindPolicyDefault},
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicySpreadByPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          4,
			},
		},
		{
			name: "cpu set with LSR Prod Pod but not specified CPUBindPolicy",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "container-1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				requestCPUBind: true,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				resourceSpec:           &extension.ResourceSpec{PreferredCPUBindPolicy: extension.CPUBindPolicyDefault},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
		},
		{
			name: "skip cpu share pod",
			pod:  &corev1.Pod{},
			wantState: &preFilterState{
				skip: true,
			},
		},
		{
			name: "skip BE Pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"preferredCPUBindPolicy": "FullPCPUs"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityBatchValueMin),
					Containers: []corev1.Container{
						{
							Name: "container-1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				requestCPUBind: false,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
		},
		{
			name: "skip Pod without cpu",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSE),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"preferredCPUBindPolicy": "FullPCPUs"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
				},
			},
			wantState: &preFilterState{
				skip: true,
			},
		},
		{
			name: "error with non-integer pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"preferredCPUBindPolicy": "FullPCPUs"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "container-1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4.5"),
								},
							},
						},
					},
				},
			},
			want: framework.NewStatus(framework.Error, "the requested CPUs must be integer"),
		},
		{
			name: "skip Pod with unsupported bind policy",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSE),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"preferredCPUBindPolicy": "test"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Name: "container-1",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4.5"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				requestCPUBind: false,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4.5"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			if tt.defaultBindPolicy != "" {
				suit.nodeNUMAResourceArgs.DefaultCPUBindPolicy = tt.defaultBindPolicy
			}
			p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			suit.start()

			cycleState := framework.NewCycleState()
			if _, got := p.(framework.PreFilterPlugin).PreFilter(context.TODO(), cycleState, tt.pod); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PreFilter() = %v, want %v", got, tt.want)
			}
			if !tt.want.IsSuccess() {
				return
			}
			state, status := getPreFilterState(cycleState)
			assert.True(t, status.IsSuccess())
			assert.Equal(t, tt.wantState, state)
		})
	}
}

func TestPlugin_Filter(t *testing.T) {
	tests := []struct {
		name            string
		nodeLabels      map[string]string
		kubeletPolicy   *extension.KubeletCPUManagerPolicy
		cpuTopology     *CPUTopology
		state           *preFilterState
		pod             *corev1.Pod
		allocationState *NodeAllocation
		want            *framework.Status
	}{
		{
			name: "error with missing preFilterState",
			pod:  &corev1.Pod{},
			want: framework.AsStatus(framework.ErrNotFound),
		},
		{
			name: "error with missing CPUTopology",
			state: &preFilterState{
				requestCPUBind: true,
			},
			pod:  &corev1.Pod{},
			want: framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrNotFoundCPUTopology),
		},
		{
			name: "error with invalid cpu topology",
			state: &preFilterState{
				requestCPUBind: true,
			},
			cpuTopology:     &CPUTopology{},
			allocationState: NewNodeAllocation("test-node-1"),
			pod:             &corev1.Pod{},
			want:            framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrInvalidCPUTopology),
		},
		{
			name: "succeed with valid cpu topology",
			state: &preFilterState{
				requestCPUBind: true,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			pod:             &corev1.Pod{},
			want:            nil,
		},
		{
			name: "succeed with skip",
			state: &preFilterState{
				requestCPUBind: false,
			},
			pod:  &corev1.Pod{},
			want: nil,
		},
		{
			name: "verify FullPCPUsOnly with SMTAlignmentError",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind:         true,
				resourceSpec:           &extension.ResourceSpec{},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          5,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			pod:             &corev1.Pod{},
			want:            framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrSMTAlignmentError),
		},
		{
			name: "verify FullPCPUsOnly with RequiredFullPCPUsPolicy",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind:         true,
				resourceSpec:           &extension.ResourceSpec{},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			pod:             &corev1.Pod{},
			want:            framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrRequiredFullPCPUsPolicy),
		},
		{
			name: "verify Kubelet FullPCPUsOnly with SMTAlignmentError",
			state: &preFilterState{
				requestCPUBind:         true,
				resourceSpec:           &extension.ResourceSpec{},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          5,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			kubeletPolicy: &extension.KubeletCPUManagerPolicy{
				Policy: extension.KubeletCPUManagerPolicyStatic,
				Options: map[string]string{
					extension.KubeletCPUManagerPolicyFullPCPUsOnlyOption: "true",
				},
			},
			pod:  &corev1.Pod{},
			want: framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrSMTAlignmentError),
		},
		{
			name: "verify Kubelet FullPCPUsOnly with RequiredFullPCPUsPolicy",
			state: &preFilterState{
				requestCPUBind:         true,
				resourceSpec:           &extension.ResourceSpec{},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			kubeletPolicy: &extension.KubeletCPUManagerPolicy{
				Policy: extension.KubeletCPUManagerPolicyStatic,
				Options: map[string]string{
					extension.KubeletCPUManagerPolicyFullPCPUsOnlyOption: "true",
				},
			},
			pod:  &corev1.Pod{},
			want: framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrRequiredFullPCPUsPolicy),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node-1",
						Labels: map[string]string{},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("96"),
							corev1.ResourceMemory: resource.MustParse("512Gi"),
						},
					},
				},
			}
			for k, v := range tt.nodeLabels {
				nodes[0].Labels[k] = v
			}

			suit := newPluginTestSuit(t, nodes)
			p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			plg := p.(*Plugin)
			if tt.allocationState != nil {
				topologyOptions := TopologyOptions{
					CPUTopology: tt.cpuTopology,
					Policy:      tt.kubeletPolicy,
				}
				plg.topologyOptionsManager.UpdateTopologyOptions(tt.allocationState.nodeName, func(options *TopologyOptions) {
					*options = topologyOptions
				})

				cpuManager := plg.resourceManager.(*resourceManager)
				cpuManager.nodeAllocations[tt.allocationState.nodeName] = tt.allocationState
			}

			suit.start()

			cycleState := framework.NewCycleState()
			if tt.state != nil {
				cycleState.Write(stateKey, tt.state)
			}

			nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get("test-node-1")
			assert.NoError(t, err)
			assert.NotNil(t, nodeInfo)

			if got := plg.Filter(context.TODO(), cycleState, tt.pod, nodeInfo); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Filter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlugin_Score(t *testing.T) {
	tests := []struct {
		name        string
		nodeLabels  map[string]string
		state       *preFilterState
		pod         *corev1.Pod
		cpuTopology *CPUTopology
		want        *framework.Status
		wantScore   int64
	}{
		{
			name: "error with missing preFilterState",
			pod:  &corev1.Pod{},
			want: framework.AsStatus(framework.ErrNotFound),
		},
		{
			name: "error with missing allocationState",
			state: &preFilterState{
				requestCPUBind: true,
			},
			pod:       &corev1.Pod{},
			want:      nil,
			wantScore: 0,
		},
		{
			name: "error with invalid cpu topology",
			state: &preFilterState{
				requestCPUBind: true,
			},
			cpuTopology: &CPUTopology{},
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   0,
		},
		{
			name: "succeed with skip",
			state: &preFilterState{
				requestCPUBind: false,
			},
			pod:       &corev1.Pod{},
			want:      nil,
			wantScore: 0,
		},
		{
			name: "score with full empty node FullPCPUs",
			state: &preFilterState{
				requestCPUBind: true,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   25,
		},
		{
			name: "score with satisfied node FullPCPUs",
			state: &preFilterState{
				requestCPUBind: true,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          8,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   50,
		},
		{
			name: "score with full empty node SpreadByPCPUs",
			state: &preFilterState{
				requestCPUBind: true,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   25,
		},
		{
			name: "score with exceed socket FullPCPUs",
			state: &preFilterState{
				requestCPUBind: true,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          16,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   100,
		},
		{
			name: "score with satisfied socket FullPCPUs",
			state: &preFilterState{
				requestCPUBind: true,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          16,
			},
			cpuTopology: buildCPUTopologyForTest(2, 2, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   50,
		},
		{
			name: "score with full empty socket SpreadByPCPUs",
			state: &preFilterState{
				requestCPUBind: true,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   25,
		},
		{
			name: "score with Node NUMA Allocate Strategy",
			nodeLabels: map[string]string{
				extension.LabelNodeNUMAAllocateStrategy: string(extension.NodeNUMAAllocateStrategyLeastAllocated),
			},
			state: &preFilterState{
				requestCPUBind: true,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          2,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   12,
		},
		{
			name: "score with Node CPU Bind Policy",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind: true,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          8,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantScore:   50,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			totalCPUs := 96
			if tt.cpuTopology != nil {
				totalCPUs = tt.cpuTopology.CPUDetails.CPUs().Size()
			}
			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node-1",
						Labels: map[string]string{},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(totalCPUs*1000), resource.DecimalSI),
							corev1.ResourceMemory: resource.MustParse("512Gi"),
						},
					},
				},
			}
			for k, v := range tt.nodeLabels {
				nodes[0].Labels[k] = v
			}

			suit := newPluginTestSuit(t, nodes)
			suit.nodeNUMAResourceArgs.ScoringStrategy.Type = schedulingconfig.MostAllocated
			p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			plg := p.(*Plugin)
			allocateState := NewNodeAllocation("test-node-1")
			if tt.cpuTopology != nil {
				plg.topologyOptionsManager.UpdateTopologyOptions(allocateState.nodeName, func(options *TopologyOptions) {
					options.CPUTopology = tt.cpuTopology
				})
			}

			cpuManager := plg.resourceManager.(*resourceManager)
			cpuManager.nodeAllocations[allocateState.nodeName] = allocateState

			suit.start()

			cycleState := framework.NewCycleState()
			if tt.state != nil {
				cycleState.Write(stateKey, tt.state)
			}

			nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get("test-node-1")
			assert.NoError(t, err)
			assert.NotNil(t, nodeInfo)

			gotScore, gotStatus := plg.Score(context.TODO(), cycleState, tt.pod, "test-node-1")
			if !reflect.DeepEqual(gotStatus, tt.want) {
				t.Errorf("Score() = %v, want %v", gotStatus, tt.want)
			}
			if !tt.want.IsSuccess() {
				return
			}
			assert.Equal(t, tt.wantScore, gotScore)
		})
	}
}

func TestPlugin_Reserve(t *testing.T) {
	tests := []struct {
		name          string
		nodeLabels    map[string]string
		state         *preFilterState
		reservedCPUs  map[types.UID]cpuset.CPUSet
		pod           *corev1.Pod
		cpuTopology   *CPUTopology
		allocatedCPUs []int
		want          *framework.Status
		wantCPUSet    cpuset.CPUSet
		wantState     *preFilterState
	}{
		{
			name: "error with missing preFilterState",
			pod:  &corev1.Pod{},
			want: framework.AsStatus(framework.ErrNotFound),
		},
		{
			name: "error with missing allocationState",
			state: &preFilterState{
				requestCPUBind: true,
			},
			pod:  &corev1.Pod{},
			want: framework.NewStatus(framework.Error, ErrNotFoundCPUTopology),
		},
		{
			name: "error with invalid cpu topology",
			state: &preFilterState{
				requestCPUBind: true,
			},
			cpuTopology: &CPUTopology{},
			pod:         &corev1.Pod{},
			want:        framework.NewStatus(framework.Error, ErrInvalidCPUTopology),
		},
		{
			name: "succeed with skip",
			state: &preFilterState{
				requestCPUBind: false,
			},
			pod:  &corev1.Pod{},
			want: nil,
		},
		{
			name: "succeed with valid cpu topology",
			state: &preFilterState{
				requestCPUBind: true,
				numCPUsNeeded:  4,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantCPUSet:  cpuset.NewCPUSet(0, 1, 2, 3),
		},
		{
			name: "allocated by node cpu bind policy",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicySpreadByPCPUs),
			},
			state: &preFilterState{
				requestCPUBind: true,
				numCPUsNeeded:  4,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantCPUSet:  cpuset.NewCPUSet(0, 2, 4, 6),
			wantState: &preFilterState{
				requestCPUBind: true,
				numCPUsNeeded:  4,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
			},
		},
		{
			name: "error with big request cpu",
			state: &preFilterState{
				requestCPUBind: true,
				numCPUsNeeded:  24,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        framework.NewStatus(framework.Error, "not enough cpus available to satisfy request"),
		},
		{
			name: "succeed with valid cpu topology and node numa least allocate strategy",
			nodeLabels: map[string]string{
				extension.LabelNodeNUMAAllocateStrategy: string(extension.NodeNUMAAllocateStrategyLeastAllocated),
			},
			state: &preFilterState{
				requestCPUBind: true,
				numCPUsNeeded:  4,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			cpuTopology:   buildCPUTopologyForTest(2, 1, 8, 2),
			allocatedCPUs: []int{0, 1, 2, 3},
			pod:           &corev1.Pod{},
			want:          nil,
			wantCPUSet:    cpuset.NewCPUSet(16, 17, 18, 19),
		},
		{
			name: "succeed with valid cpu topology and node numa most allocate strategy",
			nodeLabels: map[string]string{
				extension.LabelNodeNUMAAllocateStrategy: string(extension.NodeNUMAAllocateStrategyMostAllocated),
			},
			state: &preFilterState{
				requestCPUBind: true,
				numCPUsNeeded:  4,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			cpuTopology:   buildCPUTopologyForTest(2, 1, 8, 2),
			allocatedCPUs: []int{0, 1, 2, 3},
			pod:           &corev1.Pod{},
			want:          nil,
			wantCPUSet:    cpuset.NewCPUSet(4, 5, 6, 7),
		},
		{
			name: "succeed allocate from reservation reserved cpus",
			state: &preFilterState{
				requestCPUBind: true,
				numCPUsNeeded:  4,
				resourceSpec: &extension.ResourceSpec{
					PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			reservedCPUs: map[types.UID]cpuset.CPUSet{
				uuid.NewUUID(): cpuset.NewCPUSet(4, 5, 6, 7, 8, 9, 10),
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantCPUSet:  cpuset.NewCPUSet(4, 5, 6, 7),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node-1",
						Labels: map[string]string{},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("96"),
							corev1.ResourceMemory: resource.MustParse("512Gi"),
						},
					},
				},
			}
			for k, v := range tt.nodeLabels {
				nodes[0].Labels[k] = v
			}

			suit := newPluginTestSuit(t, nodes)
			p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			plg := p.(*Plugin)
			nodeAllocation := NewNodeAllocation("test-node-1")
			if tt.cpuTopology != nil {
				plg.topologyOptionsManager.UpdateTopologyOptions(nodeAllocation.nodeName, func(options *TopologyOptions) {
					options.CPUTopology = tt.cpuTopology
				})
				if len(tt.allocatedCPUs) > 0 {
					nodeAllocation.addCPUs(tt.cpuTopology, uuid.NewUUID(), cpuset.NewCPUSet(tt.allocatedCPUs...), schedulingconfig.CPUExclusivePolicyNone)
				}
				if len(tt.reservedCPUs) > 0 {
					for reservationUID, cpus := range tt.reservedCPUs {
						nodeAllocation.addCPUs(tt.cpuTopology, reservationUID, cpus, schedulingconfig.CPUExclusivePolicyNone)
					}
				}
			}

			cpuManager := plg.resourceManager.(*resourceManager)
			cpuManager.nodeAllocations[nodeAllocation.nodeName] = nodeAllocation

			suit.start()

			cycleState := framework.NewCycleState()
			if tt.state != nil {
				cycleState.Write(stateKey, tt.state)
				if len(tt.reservedCPUs) > 0 {
					for reservationUID := range tt.reservedCPUs {
						reservation := &schedulingv1alpha1.Reservation{
							ObjectMeta: metav1.ObjectMeta{
								UID:  reservationUID,
								Name: "test-reservation",
							},
						}
						rInfo := frameworkext.NewReservationInfo(reservation)
						frameworkext.SetNominatedReservation(cycleState, map[string]*frameworkext.ReservationInfo{"test-node-1": rInfo})
					}
					cycleState.Write(reservationRestoreStateKey, &reservationRestoreStateData{
						skip: false,
						nodeToState: frameworkext.NodeReservationRestoreStates{
							"test-node-1": &nodeReservationRestoreStateData{reservedCPUs: tt.reservedCPUs},
						},
					})
				}
			}

			nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get("test-node-1")
			assert.NoError(t, err)
			assert.NotNil(t, nodeInfo)

			if got := plg.Reserve(context.TODO(), cycleState, tt.pod, "test-node-1"); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Reserve() = %v, want %v", got, tt.want)
			}
			if !tt.want.IsSuccess() {
				return
			}
			var gotCPUs cpuset.CPUSet
			if tt.state.allocation != nil {
				gotCPUs = tt.state.allocation.CPUSet
			}
			assert.True(t, tt.wantCPUSet.Equals(gotCPUs))
		})
	}
}

func TestPlugin_Unreserve(t *testing.T) {
	state := &preFilterState{
		requestCPUBind: true,
		numCPUsNeeded:  24,
		resourceSpec: &extension.ResourceSpec{
			PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
		},
		allocation: &PodAllocation{
			CPUSet: cpuset.NewCPUSet(0, 1, 2, 3),
		},
	}
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: uuid.NewUUID(),
		},
	}

	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)
	topologyOptionsManager := NewTopologyOptionsManager()
	topologyOptionsManager.UpdateTopologyOptions("test-node-1", func(options *TopologyOptions) {
		options.CPUTopology = cpuTopology
	})
	plg := &Plugin{
		resourceManager: &resourceManager{
			topologyOptionsManager: topologyOptionsManager,
			nodeAllocations:        map[string]*NodeAllocation{},
		},
	}
	plg.resourceManager.Update("test-node-1", &PodAllocation{
		UID:                pod.UID,
		CPUSet:             state.allocation.CPUSet,
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
	})
	plg.Unreserve(context.TODO(), cycleState, pod, "test-node-1")

	availableCPUs, allocated, err := plg.resourceManager.GetAvailableCPUs("test-node-1", cpuset.CPUSet{})
	assert.NoError(t, err)
	assert.Empty(t, allocated)
	assert.Equal(t, cpuTopology.CPUDetails.CPUs().ToSlice(), availableCPUs.ToSlice())
}

func TestPlugin_PreBind(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}

	_, status := suit.Handle.ClientSet().CoreV1().Pods("default").Create(context.TODO(), pod, metav1.CreateOptions{})
	assert.Nil(t, status)

	suit.start()

	plg := p.(*Plugin)

	state := &preFilterState{
		requestCPUBind: true,
		numCPUsNeeded:  4,
		resourceSpec: &extension.ResourceSpec{
			PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
		},
		allocation: &PodAllocation{
			CPUSet: cpuset.NewCPUSet(0, 1, 2, 3),
		},
	}
	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)

	s := plg.PreBind(context.TODO(), cycleState, pod, "test-node-1")
	assert.True(t, s.IsSuccess())
	resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
	assert.NoError(t, err)
	assert.NotNil(t, resourceStatus)
	expectResourceStatus := &extension.ResourceStatus{
		CPUSet: "0-3",
	}
	assert.Equal(t, expectResourceStatus, resourceStatus)
}

func TestPlugin_PreBindWithCPUBindPolicyNone(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}

	_, status := suit.Handle.ClientSet().CoreV1().Pods("default").Create(context.TODO(), pod, metav1.CreateOptions{})
	assert.Nil(t, status)

	suit.start()

	plg := p.(*Plugin)

	state := &preFilterState{
		requestCPUBind: true,
		numCPUsNeeded:  4,
		resourceSpec: &extension.ResourceSpec{
			PreferredCPUBindPolicy: extension.CPUBindPolicyDefault,
		},
		preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
		allocation: &PodAllocation{
			CPUSet: cpuset.NewCPUSet(0, 1, 2, 3),
		},
	}
	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)

	s := plg.PreBind(context.TODO(), cycleState, pod, "test-node-1")
	assert.True(t, s.IsSuccess())
	resourceStatus, err := extension.GetResourceStatus(pod.Annotations)
	assert.NoError(t, err)
	assert.NotNil(t, resourceStatus)
	expectResourceStatus := &extension.ResourceStatus{
		CPUSet: "0-3",
	}
	assert.Equal(t, expectResourceStatus, resourceStatus)
	resourceSpec, err := extension.GetResourceSpec(pod.Annotations)
	assert.NoError(t, err)
	assert.NotNil(t, resourceSpec)
	expectedResourceSpec := &extension.ResourceSpec{
		PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
	}
	assert.Equal(t, expectedResourceSpec, resourceSpec)
}

func TestPlugin_PreBindReservation(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation-1",
		},
	}

	_, status := suit.KoordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
	assert.Nil(t, status)

	suit.start()

	plg := p.(*Plugin)

	state := &preFilterState{
		requestCPUBind: true,
		numCPUsNeeded:  4,
		resourceSpec: &extension.ResourceSpec{
			PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
		},
		allocation: &PodAllocation{
			CPUSet: cpuset.NewCPUSet(0, 1, 2, 3),
		},
	}
	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)

	s := plg.PreBindReservation(context.TODO(), cycleState, reservation, "test-node-1")
	assert.True(t, s.IsSuccess())

	resourceStatus, err := extension.GetResourceStatus(reservation.Annotations)
	assert.NoError(t, err)
	assert.NotNil(t, resourceStatus)
	expectResourceStatus := &extension.ResourceStatus{
		CPUSet: "0-3",
	}
	assert.Equal(t, expectResourceStatus, resourceStatus)
}

func TestRestoreReservation(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	pl := p.(*Plugin)
	cycleState := framework.NewCycleState()
	state := &preFilterState{
		requestCPUBind: true,
		numCPUsNeeded:  4,
		resourceSpec: &extension.ResourceSpec{
			PreferredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
		},
	}
	cycleState.Write(stateKey, state)

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation",
			UID:  uuid.NewUUID(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node",
		},
	}
	pl.topologyOptionsManager.UpdateTopologyOptions("test-node", func(options *TopologyOptions) {
		options.CPUTopology = buildCPUTopologyForTest(1, 2, 8, 2)
		options.MaxRefCount = 1
	})
	pl.resourceManager.Update("test-node", &PodAllocation{
		UID:                reservation.UID,
		CPUSet:             cpuset.NewCPUSet(6, 7, 8, 9),
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
	})

	podA := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "pod-a",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
	}
	podB := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       uuid.NewUUID(),
			Name:      "pod-b",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
	}
	pl.resourceManager.Update("test-node", &PodAllocation{
		UID:                podA.UID,
		CPUSet:             cpuset.NewCPUSet(6, 7),
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
	})
	pl.resourceManager.Update("test-node", &PodAllocation{
		UID:                podB.UID,
		CPUSet:             cpuset.NewCPUSet(8, 9),
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
	})
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	rInfo := frameworkext.NewReservationInfo(reservation)
	rInfo.AddAssignedPod(podA)

	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLSR),
			},
		},
		Spec: corev1.PodSpec{
			Priority: pointer.Int32(extension.PriorityProdValueMax),
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
	}
	status := pl.PreRestoreReservation(context.TODO(), cycleState, testPod)
	assert.True(t, status.IsSuccess())

	nodeReservationState, status := pl.RestoreReservation(context.TODO(), cycleState, testPod, []*frameworkext.ReservationInfo{rInfo}, nil, nodeInfo)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, cpuset.NewCPUSet(8, 9), nodeReservationState.(*nodeReservationRestoreStateData).reservedCPUs[reservation.UID])

	rInfo.AddAssignedPod(podB)
	nodeReservationState, status = pl.RestoreReservation(context.TODO(), cycleState, testPod, []*frameworkext.ReservationInfo{rInfo}, nil, nodeInfo)
	assert.True(t, status.IsSuccess())
	assert.Nil(t, nodeReservationState)
}
