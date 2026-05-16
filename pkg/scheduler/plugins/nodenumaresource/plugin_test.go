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
	"fmt"
	"reflect"
	"testing"
	"time"

	nrtfake "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	clientfeatures "k8s.io/client-go/features"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	apiresource "k8s.io/component-helpers/resource"
	fwktype "k8s.io/kube-scheduler/framework"
	k8sschedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"
	st "k8s.io/kubernetes/pkg/scheduler/testing"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	_ "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/scheme"
	v1 "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

type mutableClientFeatureGates interface {
	clientfeatures.Gates
	Set(key clientfeatures.Feature, value bool) error
}

func init() {
	schedulermetrics.Register()
	// Disable WatchListClient to avoid fake client compatibility issues in tests.
	if fg, ok := clientfeatures.FeatureGates().(mutableClientFeatureGates); ok {
		_ = fg.Set(clientfeatures.WatchListClient, false)
	}
}

var _ fwktype.SharedLister = &testSharedLister{}

type testSharedLister struct {
	nodes       []*corev1.Node
	nodeInfos   []fwktype.NodeInfo
	nodeInfoMap map[string]*framework.NodeInfo
}

func newTestSharedLister(pods []*corev1.Pod, nodes []*corev1.Node) *testSharedLister {
	nodeInfoMap := make(map[string]*framework.NodeInfo)
	nodeInfos := make([]fwktype.NodeInfo, 0)
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

func (f *testSharedLister) StorageInfos() fwktype.StorageInfoLister {
	return f
}

func (f *testSharedLister) IsPVCUsedByPods(key string) bool {
	return false
}

func (f *testSharedLister) NodeInfos() fwktype.NodeInfoLister {
	return f
}

func (f *testSharedLister) List() ([]fwktype.NodeInfo, error) {
	return f.nodeInfos, nil
}

func (f *testSharedLister) HavePodsWithAffinityList() ([]fwktype.NodeInfo, error) {
	return nil, nil
}

func (f *testSharedLister) HavePodsWithRequiredAntiAffinityList() ([]fwktype.NodeInfo, error) {
	return nil, nil
}

func (f *testSharedLister) Get(nodeName string) (fwktype.NodeInfo, error) {
	return f.nodeInfoMap[nodeName], nil
}

func makeNode(name string, capacity map[corev1.ResourceName]string, cpuAmpRatio extension.Ratio) *corev1.Node {
	node := st.MakeNode().Name(name).Capacity(capacity).Obj()
	extension.SetNodeRawAllocatable(node, node.Status.Allocatable)
	extension.AmplifyResourceList(node.Status.Allocatable, map[corev1.ResourceName]extension.Ratio{corev1.ResourceCPU: cpuAmpRatio}, corev1.ResourceCPU)
	_, _ = extension.SetNodeResourceAmplificationRatio(node, corev1.ResourceCPU, cpuAmpRatio)
	return node
}

func makePodOnNode(request map[corev1.ResourceName]string, node string, isCPUSet bool) *corev1.Pod {
	pod := st.MakePod().Req(request).Node(node).Priority(extension.PriorityProdValueMax).Obj()
	if isCPUSet {
		pod.Labels = map[string]string{
			extension.LabelPodQoS: string(extension.QoSLSR),
		}
		if node != "" {
			reqs := apiresource.PodRequests(pod, apiresource.PodResourcesOptions{})
			val := reqs.Cpu().MilliValue() / 1000
			_ = extension.SetResourceStatus(pod, &extension.ResourceStatus{CPUSet: fmt.Sprintf("0-%d", val-1)})
		}
	}
	return pod
}

func makePod(request map[corev1.ResourceName]string, isCPUSet bool) *corev1.Pod {
	return makePodOnNode(request, "", isCPUSet)
}

type frameworkHandleExtender struct {
	frameworkext.FrameworkExtender
	*nrtfake.Clientset
}

type pluginTestSuit struct {
	fwktype.Handle
	ExtenderFactory      *frameworkext.FrameworkExtenderFactory
	Extender             frameworkext.FrameworkExtender
	NRTClientset         *nrtfake.Clientset
	KoordClientSet       *koordfake.Clientset
	proxyNew             runtime.PluginFactory
	nodeNUMAResourceArgs *schedulingconfig.NodeNUMAResourceArgs
	plugin               fwktype.Plugin
}

func newPluginTestSuit(t testing.TB, pods []*corev1.Pod, nodes []*corev1.Node) *pluginTestSuit {
	// Reset registrations to avoid cross-test interference via package-level state.
	frameworkexthelper.ResetRegistrations()
	var v1args v1.NodeNUMAResourceArgs
	v1.SetDefaults_NodeNUMAResourceArgs(&v1args)
	var nodeNUMAResourceArgs schedulingconfig.NodeNUMAResourceArgs
	err := v1.Convert_v1_NodeNUMAResourceArgs_To_config_NodeNUMAResourceArgs(&v1args, &nodeNUMAResourceArgs, nil)
	assert.NoError(t, err)

	nrtClientSet := nrtfake.NewSimpleClientset()
	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
		frameworkext.WithReservationNominator(frameworkext.NewFakeReservationNominator()),
	)
	assert.NoError(t, err)

	proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, func(_ context.Context, configuration apiruntime.Object, f fwktype.Handle) (fwktype.Plugin, error) {
		return New(context.Background(), configuration, &frameworkHandleExtender{
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
	snapshot := newTestSharedLister(pods, nodes)
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		runtime.WithClientSet(cs),
		runtime.WithInformerFactory(informerFactory),
		runtime.WithSnapshotSharedLister(snapshot),
	)
	assert.Nil(t, err)

	extender := extenderFactory.NewFrameworkExtender(fh)

	suit := &pluginTestSuit{
		Handle:               fh,
		ExtenderFactory:      extenderFactory,
		Extender:             extender,
		NRTClientset:         nrtClientSet,
		KoordClientSet:       koordClientSet,
		proxyNew:             proxyNew,
		nodeNUMAResourceArgs: &nodeNUMAResourceArgs,
	}
	// Wrap proxyNew so that the created plugin is saved for start() to access.
	originalProxyNew := suit.proxyNew
	suit.proxyNew = func(ctx context.Context, configuration apiruntime.Object, f fwktype.Handle) (fwktype.Plugin, error) {
		pl, err := originalProxyNew(ctx, configuration, f)
		if err == nil {
			suit.plugin = pl
		}
		return pl, err
	}
	return suit
}

func (p *pluginTestSuit) start(t testing.TB) {
	// Use a test-lifetime stop channel so informers keep delivering events for
	// objects created/updated by the test AFTER start() returns. A stop channel
	// bound to a short-lived context would be closed the moment start() returns
	// and silently stop the informer watchers.
	stopCh := make(chan struct{})
	t.Cleanup(func() { close(stopCh) })
	p.Handle.SharedInformerFactory().Start(stopCh)
	p.ExtenderFactory.KoordinatorSharedInformerFactory().Start(stopCh)
	p.Handle.SharedInformerFactory().WaitForCacheSync(stopCh)
	p.ExtenderFactory.KoordinatorSharedInformerFactory().WaitForCacheSync(stopCh)
	// Start the NRT informer factory if the plugin has been created.
	if np, ok := p.plugin.(*Plugin); ok && np.nrtInformerFactory != nil {
		np.nrtInformerFactory.Start(stopCh)
		np.nrtInformerFactory.WaitForCacheSync(stopCh)
	}
	// Use a separate bounded context only for the handler-sync wait so a hang
	// doesn't block the whole test.
	syncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := frameworkexthelper.WaitForHandlersSync(syncCtx); err != nil {
		t.Fatalf("timed out waiting for handler registrations to sync: %v", err)
	}
}

func TestNew(t *testing.T) {
	suit := newPluginTestSuit(t, nil, nil)
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)
	assert.Equal(t, Name, p.Name())
}

func TestPlugin_PreFilter(t *testing.T) {
	tests := []struct {
		name              string
		pod               *corev1.Pod
		defaultBindPolicy schedulingconfig.CPUBindPolicy
		want              *fwktype.Status
		wantState         *preFilterState
	}{
		{
			name: "cpu set with LSR Prod Pod, designated allocation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec:   `{"preferredCPUBindPolicy": "FullPCPUs"}`,
						extension.AnnotationResourceStatus: `{"cpuset": "35-36,67-68", "numaNodeResources": [{"node": 1, "resources": {"cpu": 4}}]}`,
						extension.AnnotationSchedulingHint: `{"nodeNames": ["node-1"], "extensions": {"NodeNUMAResource": {}}}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
				designatedAllocation: &allocation{
					cpuset: cpuset.NewCPUSet(35, 36, 67, 68),
					numaNodeResource: map[int]corev1.ResourceList{
						1: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
		},
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
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
			want: fwktype.NewStatus(fwktype.Skip),
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
					Priority: ptr.To[int32](extension.PriorityBatchValueMin),
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
				numCPUsNeeded: 4,
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
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
				},
			},
			wantState: &preFilterState{
				skip: true,
			},
			want: fwktype.NewStatus(fwktype.Skip),
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
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
			want: fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, "the requested CPUs must be integer"),
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
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
				numCPUsNeeded: 4,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, nil)
			if tt.defaultBindPolicy != "" {
				suit.nodeNUMAResourceArgs.DefaultCPUBindPolicy = tt.defaultBindPolicy
			}
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			suit.start(t)
			cycleState := framework.NewCycleState()

			if tt.pod.Annotations != nil && tt.pod.Annotations[extension.AnnotationSchedulingHint] != "" {
				hint, err := extension.GetSchedulingHint(tt.pod)
				assert.NoError(t, err)
				assert.NotNil(t, hint)
				hintStateData := &hinter.SchedulingHintStateData{
					PreFilterNodes: hint.NodeNames,
					Extensions:     hint.Extensions,
				}
				hinter.SetSchedulingHintState(cycleState, hintStateData)
			}
			if _, got := p.(fwktype.PreFilterPlugin).PreFilter(context.TODO(), cycleState, tt.pod, nil); !reflect.DeepEqual(got, tt.want) {
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
		name                 string
		nodeLabels           map[string]string
		nodeAnnotations      map[string]string
		kubeletPolicy        *extension.KubeletCPUManagerPolicy
		cpuTopology          *CPUTopology
		state                *preFilterState
		allocationState      *NodeAllocation
		want                 *fwktype.Status
		wantAllocationResult *PodAllocation
	}{
		{
			name: "error with missing preFilterState",
			want: fwktype.AsStatus(fwktype.ErrNotFound),
		},
		{
			name: "error with invalid cpu topology",
			state: &preFilterState{
				requestCPUBind: true,
			},
			cpuTopology:     &CPUTopology{},
			allocationState: NewNodeAllocation("test-node-1"),
			want:            fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, ErrInvalidCPUTopology),
		},
		{
			name: "succeed with valid cpu topology",
			state: &preFilterState{
				requestCPUBind: true,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            nil,
		},
		{
			name: "succeed with skip",
			state: &preFilterState{
				requestCPUBind: false,
			},
			want: nil,
		},
		{
			name: "failed to verify Node FullPCPUsOnly with SMTAlignmentError",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          5,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, ErrSMTAlignmentError),
		},
		{
			name: "LS Pod failed to verify Node FullPCPUsOnly with SMTAlignmentError",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind: false,
				numCPUsNeeded:  5,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("5"),
				},
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, ErrSMTAlignmentError),
		},
		{
			name: "LS Pod failed to verify Node FullPCPUsOnly with non-integer request",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind: false,
				numCPUsNeeded:  5,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("5200m"),
				},
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, ErrInvalidRequestedCPUs),
		},
		{
			name: "verify Node FullPCPUsOnly",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            nil,
		},
		{
			name: "failed to verify required FullPCPUs SMTAlignmentError",
			state: &preFilterState{
				requestCPUBind:        true,
				requiredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:         5,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, ErrSMTAlignmentError),
		},
		{
			name: "verify required FullPCPUs",
			state: &preFilterState{
				requestCPUBind:        true,
				requiredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:         4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            nil,
		},
		{
			name: "verify FullPCPUsOnly with preferred SpreadByPCPUs",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind:         true,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            nil,
		},
		{
			name: "failed to verify FullPCPUsOnly with required SpreadByPCPUs",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicySpreadByPCPUs),
			},
			state: &preFilterState{
				requestCPUBind:        true,
				requiredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:         4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, ErrCPUBindPolicyConflict),
		},
		{
			name: "verify FullPCPUsOnly with required FullPCPUs",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicyFullPCPUsOnly),
			},
			state: &preFilterState{
				requestCPUBind:        true,
				requiredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:         4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            nil,
		},
		{
			name: "verify Kubelet FullPCPUsOnly with SMTAlignmentError",
			state: &preFilterState{
				requestCPUBind:         true,
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
			want: fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, ErrSMTAlignmentError),
		},
		{
			name: "verify Kubelet FullPCPUsOnly with required SpreadByPCPUs",
			state: &preFilterState{
				requestCPUBind:        true,
				requiredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
				numCPUsNeeded:         4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			kubeletPolicy: &extension.KubeletCPUManagerPolicy{
				Policy: extension.KubeletCPUManagerPolicyStatic,
				Options: map[string]string{
					extension.KubeletCPUManagerPolicyFullPCPUsOnlyOption: "true",
				},
			},
			want: fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable, ErrCPUBindPolicyConflict),
		},
		{
			name: "verify Kubelet FullPCPUsOnly with required FullPCPUs",
			state: &preFilterState{
				requestCPUBind:        true,
				requiredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:         4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			kubeletPolicy: &extension.KubeletCPUManagerPolicy{
				Policy: extension.KubeletCPUManagerPolicyStatic,
				Options: map[string]string{
					extension.KubeletCPUManagerPolicyFullPCPUsOnlyOption: "true",
				},
			},
			want: nil,
		},
		{
			name: "verify required FullPCPUs with none NUMA topology policy",
			state: &preFilterState{
				requestCPUBind:         true,
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicyFullPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
		},
		{
			name: "verify FullPCPUs with NUMA Topology Policy",
			nodeLabels: map[string]string{
				extension.LabelNUMATopologyPolicy: string(extension.NUMATopologyPolicySingleNUMANode),
			},
			state: &preFilterState{
				requestCPUBind:         true,
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicyFullPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
		},
		{
			name: "verify FullPCPUs with NUMA Topology Policy and amplification ratio",
			nodeLabels: map[string]string{
				extension.LabelNUMATopologyPolicy: string(extension.NUMATopologyPolicySingleNUMANode),
			},
			nodeAnnotations: map[string]string{
				extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 1.5}`,
			},
			state: &preFilterState{
				requestCPUBind:         true,
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicyFullPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
		},
		{
			name: "verify FullPCPUs with None NUMA Topology Policy and amplification ratio",
			nodeLabels: map[string]string{
				extension.LabelNUMATopologyPolicy: string(extension.NUMATopologyPolicyNone),
			},
			nodeAnnotations: map[string]string{
				extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 1.5}`,
			},
			state: &preFilterState{
				requestCPUBind:         true,
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicyFullPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
		},
		{
			name: "designatedAllocation, success",
			nodeLabels: map[string]string{
				extension.LabelNUMATopologyPolicy: string(extension.NUMATopologyPolicySingleNUMANode),
			},
			nodeAnnotations: map[string]string{
				extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 1.5}`,
			},
			state: &preFilterState{
				requestCPUBind:         true,
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicyFullPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
				designatedAllocation: &allocation{
					cpuset: cpuset.MustParse("0-3"),
					numaNodeResource: map[int]corev1.ResourceList{
						0: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			wantAllocationResult: &PodAllocation{
				CPUSet: cpuset.MustParse("0-3"),
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
		{
			name: "designatedAllocation, without numa, success",
			nodeLabels: map[string]string{
				extension.LabelNUMATopologyPolicy: string(extension.NUMATopologyPolicySingleNUMANode),
			},
			nodeAnnotations: map[string]string{
				extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 1.5}`,
			},
			state: &preFilterState{
				requestCPUBind:         true,
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicyFullPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
				designatedAllocation: &allocation{
					cpuset: cpuset.MustParse("0-3"),
				},
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			wantAllocationResult: &PodAllocation{
				CPUSet: cpuset.MustParse("0-3"),
			},
		},
		{
			name: "designatedAllocation, without cpuset, success",
			nodeLabels: map[string]string{
				extension.LabelNUMATopologyPolicy: string(extension.NUMATopologyPolicySingleNUMANode),
			},
			nodeAnnotations: map[string]string{
				extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 1.5}`,
			},
			state: &preFilterState{
				requestCPUBind: false,
				numCPUsNeeded:  4,
				designatedAllocation: &allocation{
					numaNodeResource: map[int]corev1.ResourceList{
						0: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			wantAllocationResult: &PodAllocation{
				NUMANodeResources: []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
		{
			name: "designatedAllocation, failure",
			nodeLabels: map[string]string{
				extension.LabelNUMATopologyPolicy: string(extension.NUMATopologyPolicySingleNUMANode),
			},
			nodeAnnotations: map[string]string{
				extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 1.5}`,
			},
			state: &preFilterState{
				requestCPUBind:         true,
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicyFullPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				numCPUsNeeded:          4,
				designatedAllocation: &allocation{
					cpuset: cpuset.MustParse("20-23"),
					numaNodeResource: map[int]corev1.ResourceList{
						0: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
			cpuTopology:     buildCPUTopologyForTest(2, 1, 4, 2),
			allocationState: NewNodeAllocation("test-node-1"),
			want:            fwktype.NewStatus(fwktype.Unschedulable, "not enough cpus available to satisfy request"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-node-1",
						Labels:      map[string]string{},
						Annotations: map[string]string{},
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
			for k, v := range tt.nodeAnnotations {
				nodes[0].Annotations[k] = v
			}

			suit := newPluginTestSuit(t, nil, nodes)
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			plg := p.(*Plugin)
			if tt.allocationState != nil {
				topologyOptions := TopologyOptions{
					CPUTopology: tt.cpuTopology,
					Policy:      tt.kubeletPolicy,
				}
				for i := 0; i < topologyOptions.CPUTopology.NumNodes; i++ {
					topologyOptions.NUMANodeResources = append(topologyOptions.NUMANodeResources, NUMANodeResource{
						Node: i,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewQuantity(int64(topologyOptions.CPUTopology.CPUsPerNode()), resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(32*1024*1024*1024, resource.BinarySI),
						}})
				}
				plg.topologyOptionsManager.UpdateTopologyOptions(tt.allocationState.nodeName, func(options *TopologyOptions) {
					*options = topologyOptions
				})

				manager := plg.resourceManager.(*resourceManager)
				manager.nodeAllocations[tt.allocationState.nodeName] = tt.allocationState
			}

			suit.start(t)

			cycleState := framework.NewCycleState()
			if tt.state != nil {
				if tt.state.numCPUsNeeded > 0 && tt.state.requests == nil {
					tt.state.requests = corev1.ResourceList{corev1.ResourceCPU: *resource.NewQuantity(int64(tt.state.numCPUsNeeded), resource.DecimalSI)}
				}
				cycleState.Write(stateKey, tt.state)
			}
			topologymanager.InitStore(cycleState)

			nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get("test-node-1")
			assert.NoError(t, err)
			assert.NotNil(t, nodeInfo)

			pod := &corev1.Pod{}
			if got := plg.Filter(context.TODO(), cycleState, pod, nodeInfo); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Filter() = %v, want %v", got, tt.want)
			}
			if tt.state != nil {
				assert.Equal(t, tt.wantAllocationResult, tt.state.allocation)
			}
		})
	}
}

func TestFilterWithAmplifiedCPUs(t *testing.T) {
	tests := []struct {
		name                      string
		pod                       *corev1.Pod
		existingPods              []*corev1.Pod
		cpuTopology               *CPUTopology
		nodeHasNRT                bool
		nodeCPUAmplificationRatio extension.Ratio
		wantStatus                *fwktype.Status
	}{
		{
			name:                      "no resources requested always fits",
			pod:                       &corev1.Pod{},
			existingPods:              []*corev1.Pod{makePodOnNode(map[corev1.ResourceName]string{"cpu": "4"}, "node-1", false)},
			cpuTopology:               buildCPUTopologyForTest(2, 1, 8, 2),
			nodeCPUAmplificationRatio: 2.0,
		},
		{
			name:                      "no filtering without node cpu amplification",
			pod:                       makePod(map[corev1.ResourceName]string{"cpu": "32"}, false),
			existingPods:              []*corev1.Pod{makePodOnNode(map[corev1.ResourceName]string{"cpu": "32"}, "node-1", false)},
			cpuTopology:               buildCPUTopologyForTest(2, 1, 8, 2),
			nodeCPUAmplificationRatio: 1.0,
		},
		{
			name:                      "cpu fits on no NRT node",
			pod:                       makePod(map[corev1.ResourceName]string{"cpu": "32"}, false),
			existingPods:              []*corev1.Pod{makePodOnNode(map[corev1.ResourceName]string{"cpu": "32"}, "node-1", false)},
			cpuTopology:               buildCPUTopologyForTest(2, 1, 8, 2),
			nodeCPUAmplificationRatio: 2.0,
		},
		{
			name:                      "insufficient cpu",
			pod:                       makePod(map[corev1.ResourceName]string{"cpu": "32"}, false),
			existingPods:              []*corev1.Pod{makePodOnNode(map[corev1.ResourceName]string{"cpu": "64"}, "node-1", false)},
			cpuTopology:               buildCPUTopologyForTest(2, 1, 8, 2),
			nodeCPUAmplificationRatio: 2.0,
			wantStatus:                fwktype.NewStatus(fwktype.Unschedulable, ErrInsufficientAmplifiedCPU),
		},
		{
			name:                      "insufficient cpu with cpuset pod on node",
			pod:                       makePod(map[corev1.ResourceName]string{"cpu": "32"}, false),
			existingPods:              []*corev1.Pod{makePodOnNode(map[corev1.ResourceName]string{"cpu": "32"}, "node-1", true)},
			cpuTopology:               buildCPUTopologyForTest(2, 1, 8, 2),
			nodeHasNRT:                true,
			nodeCPUAmplificationRatio: 2.0,
			wantStatus:                fwktype.NewStatus(fwktype.Unschedulable, ErrInsufficientAmplifiedCPU),
		},
		{
			name:                      "insufficient cpu when scheduling cpuset pod",
			pod:                       makePod(map[corev1.ResourceName]string{"cpu": "32"}, true),
			existingPods:              []*corev1.Pod{makePodOnNode(map[corev1.ResourceName]string{"cpu": "32"}, "node-1", false)},
			cpuTopology:               buildCPUTopologyForTest(2, 1, 8, 2),
			nodeHasNRT:                true,
			nodeCPUAmplificationRatio: 2.0,
			wantStatus:                fwktype.NewStatus(fwktype.Unschedulable, ErrInsufficientAmplifiedCPU),
		},
		{
			name:                      "insufficient cpu when scheduling cpuset pod with cpuset pod on node",
			pod:                       makePod(map[corev1.ResourceName]string{"cpu": "32"}, true),
			existingPods:              []*corev1.Pod{makePodOnNode(map[corev1.ResourceName]string{"cpu": "32"}, "node-1", true)},
			cpuTopology:               buildCPUTopologyForTest(2, 1, 8, 2),
			nodeHasNRT:                true,
			nodeCPUAmplificationRatio: 2.0,
			wantStatus:                fwktype.NewStatus(fwktype.Unschedulable, ErrInsufficientAmplifiedCPU),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			numCPUs := tt.cpuTopology.NumCPUs
			cpu := fmt.Sprintf("%d", numCPUs)
			node := makeNode("node-1", map[corev1.ResourceName]string{"cpu": cpu, "memory": "40Gi"}, tt.nodeCPUAmplificationRatio)
			suit := newPluginTestSuit(t, tt.existingPods, []*corev1.Node{node})

			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NoError(t, err)
			suit.start(t)
			pl := p.(*Plugin)

			if tt.nodeHasNRT {
				topologyOptions := TopologyOptions{
					CPUTopology: tt.cpuTopology,
				}
				for i := 0; i < tt.cpuTopology.NumNodes; i++ {
					topologyOptions.NUMANodeResources = append(topologyOptions.NUMANodeResources, NUMANodeResource{
						Node: i,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", extension.Amplify(int64(tt.cpuTopology.CPUsPerNode()), tt.nodeCPUAmplificationRatio))),
							corev1.ResourceMemory: resource.MustParse("20Gi"),
						}})
				}
				pl.topologyOptionsManager.UpdateTopologyOptions(node.Name, func(options *TopologyOptions) {
					*options = topologyOptions
				})
			}
			handler := &podEventHandler{resourceManager: pl.resourceManager}
			for _, v := range tt.existingPods {
				handler.OnAdd(v, true)
			}

			cycleState := framework.NewCycleState()
			_, preFilterStatus := pl.PreFilter(context.TODO(), cycleState, tt.pod, nil)
			assert.True(t, preFilterStatus.IsSuccess() || preFilterStatus.IsSkip())

			nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get("node-1")
			assert.NoError(t, err)
			gotStatus := pl.Filter(context.TODO(), cycleState, tt.pod, nodeInfo)
			assert.Equal(t, tt.wantStatus, gotStatus)
		})
	}
}

func TestPlugin_FilterNominateReservation(t *testing.T) {
	skipState := framework.NewCycleState()
	skipState.Write(stateKey, &preFilterState{
		skip: true,
	})
	testState := framework.NewCycleState()
	testState.Write(stateKey, &preFilterState{
		skip: false,
	})
	node0mask, _ := bitmask.NewBitMask(0)

	type fields struct {
		nodes []*corev1.Node
	}
	type args struct {
		cycleState      fwktype.CycleState
		pod             *corev1.Pod
		reservationInfo *frameworkext.ReservationInfo
		nodeName        string
	}
	tests := []struct {
		name string
		// fields/args are used by the simple early-return cases that bypass full construction.
		fields fields
		args   args
		// The following fields drive full NUMA-aware construction (used when state != nil).
		state                     *preFilterState
		rAlloc                    *reusableAlloc
		reservationAllocatePolicy schedulingv1alpha1.ReservationAllocatePolicy
		cpuTopology               *CPUTopology
		numaAffinity              bitmask.BitMask
		want                      *fwktype.Status
	}{
		// ---- early-return cases (no node / state issues) ----
		{
			name: "missing preFilterState",
			args: args{
				cycleState: framework.NewCycleState(),
			},
			want: fwktype.AsStatus(fwktype.ErrNotFound),
		},
		{
			name: "skip",
			args: args{
				cycleState: skipState,
			},
			want: nil,
		},
		{
			name: "failed to get node",
			args: args{
				cycleState: testState,
				nodeName:   "test-node",
			},
			want: fwktype.NewStatus(fwktype.Error, `getting nil node "test-node" from Snapshot`),
		},
		// ---- NUMA compatibility cases ----
		{
			// BestHint=NUMA0, R's remained is entirely on NUMA0.
			// The second tryAllocateFromReusable Allocate call uses requiredResources={NUMA0:...}
			// which is compatible with hint=NUMA0 → allocation succeeds → result != nil → pass.
			name: "NUMA-compatible R passes filter (no reservation-affinity)",
			state: &preFilterState{
				podNUMATopologyPolicy: extension.NUMATopologyPolicyRestricted,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
			rAlloc: &reusableAlloc{
				allocatable: map[int]corev1.ResourceList{
					0: {corev1.ResourceCPU: resource.MustParse("8")},
				},
				remained: map[int]corev1.ResourceList{
					0: {corev1.ResourceCPU: resource.MustParse("8")},
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			numaAffinity:              node0mask,
			want:                      nil,
		},
		{
			// Regression: before the fix, FilterNominateReservation returned nil (pass) when the
			// reservation's NUMA scope was incompatible with the pod's BestHint but
			// requiredFromReservation was false (no reservation-affinity). This caused the nominator
			// to include the mismatched reservation as a candidate; when selected by scoring, Reserve
			// then failed at hint-vs-nominated-NUMA validation.
			//
			// Root cause: tryAllocateFromReusable returned (nil, nil) on the fallback path
			// (requiredFromReservation=false), which FilterNominateReservation previously treated as
			// "pass". After the fix, result==nil is treated as Unschedulable.
			name: "NUMA-incompatible R blocked from nominator without reservation-affinity (regression fix)",
			state: &preFilterState{
				podNUMATopologyPolicy: extension.NUMATopologyPolicyRestricted,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				// hasReservationAffinity=false (zero value) → requiredFromReservation=false
			},
			rAlloc: &reusableAlloc{
				allocatable: map[int]corev1.ResourceList{
					1: {corev1.ResourceCPU: resource.MustParse("8")},
				},
				remained: map[int]corev1.ResourceList{
					1: {corev1.ResourceCPU: resource.MustParse("8")},
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			numaAffinity:              node0mask,
			want:                      fwktype.NewStatus(fwktype.Unschedulable, "reservation NUMA scope is incompatible with the best topology hint"),
		},
		{
			// Baseline: with reservation-affinity, requiredFromReservation=true, so
			// tryAllocateFromReusable already returned Unschedulable before the fix. Verify this
			// path still works correctly after the fix.
			name: "NUMA-incompatible R blocked from nominator with reservation-affinity (baseline)",
			state: &preFilterState{
				podNUMATopologyPolicy: extension.NUMATopologyPolicyRestricted,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				hasReservationAffinity: true, // requiredFromReservation=true
			},
			rAlloc: &reusableAlloc{
				allocatable: map[int]corev1.ResourceList{
					1: {corev1.ResourceCPU: resource.MustParse("8")},
				},
				remained: map[int]corev1.ResourceList{
					1: {corev1.ResourceCPU: resource.MustParse("8")},
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			numaAffinity:              node0mask,
			want:                      fwktype.NewStatus(fwktype.Unschedulable, "Reservation(s) Insufficient NUMA cpu"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const numaNodeName = "test-node-1"
			nodeName := numaNodeName

			var nodes []*corev1.Node
			var cycleState fwktype.CycleState
			var rInfo *frameworkext.ReservationInfo
			var pod *corev1.Pod

			if tt.state != nil {
				// NUMA-aware cases: build a real node so the snapshot lookup succeeds.
				nodes = []*corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   numaNodeName,
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
			} else {
				// Early-return cases: use fields.nodes and override nodeName if set.
				nodes = tt.fields.nodes
				if tt.args.nodeName != "" {
					nodeName = tt.args.nodeName
				}
			}

			suit := newPluginTestSuit(t, nil, nodes)
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)
			pl := p.(*Plugin)

			if tt.cpuTopology != nil {
				pl.topologyOptionsManager.UpdateTopologyOptions(numaNodeName, func(options *TopologyOptions) {
					options.CPUTopology = tt.cpuTopology
					for i := 0; i < tt.cpuTopology.NumNodes; i++ {
						options.NUMANodeResources = append(options.NUMANodeResources, NUMANodeResource{
							Node: i,
							Resources: corev1.ResourceList{
								corev1.ResourceCPU: *resource.NewQuantity(int64(tt.cpuTopology.CPUsPerNode()), resource.DecimalSI),
							},
						})
					}
				})
			}

			suit.start(t)

			if tt.state != nil {
				cycleState = framework.NewCycleState()
				cycleState.Write(stateKey, tt.state)

				// Construct the reservation and register it as the single matched alloc.
				reservationUID := uuid.NewUUID()
				reservation := &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						UID:  reservationUID,
						Name: "test-reservation",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						AllocatePolicy: tt.reservationAllocatePolicy,
					},
				}
				alloc := *tt.rAlloc
				alloc.rInfo = frameworkext.NewReservationInfo(reservation)
				rInfo = alloc.rInfo

				cycleState.Write(reservationRestoreStateKey, &reservationRestoreStateData{
					nodeToState: frameworkext.NodeReservationRestoreStates{
						numaNodeName: &nodeReservationRestoreStateData{
							matched: map[types.UID]reusableAlloc{reservationUID: alloc},
						},
					},
				})

				if tt.numaAffinity != nil {
					topologymanager.InitStore(cycleState)
					store := topologymanager.GetStore(cycleState)
					store.SetAffinity(numaNodeName, topologymanager.NUMATopologyHint{NUMANodeAffinity: tt.numaAffinity})
				}

				pod = &corev1.Pod{}
			} else {
				cycleState = tt.args.cycleState
				rInfo = tt.args.reservationInfo
				pod = tt.args.pod
				if pod == nil {
					pod = &corev1.Pod{}
				}
			}

			got := pl.FilterNominateReservation(context.TODO(), cycleState, pod, rInfo, nodeName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPlugin_Reserve(t *testing.T) {
	node0, _ := bitmask.NewBitMask(0)
	node1, _ := bitmask.NewBitMask(1)
	tests := []struct {
		name                      string
		nodeLabels                map[string]string
		state                     *preFilterState
		matched                   map[types.UID]reusableAlloc
		reservationAllocatePolicy schedulingv1alpha1.ReservationAllocatePolicy
		pod                       *corev1.Pod
		numaAffinity              bitmask.BitMask
		cpuTopology               *CPUTopology
		allocatedCPUs             []int
		// skipNominate skips AddNominatedReservation so the pod falls back to tryAllocateFromNode.
		skipNominate bool
		// extraNUMAAllocations is injected into nodeAllocation to simulate matched R owners' NUMA
		// usage double-counted on the node NUMA account (alongside the R fake pod allocation).
		extraNUMAAllocations []NUMANodeResource
		// mergedMatchedAllocatable/mergedMatchedAllocated are injected into restoreState; the former
		// is not consumed by tryAllocateFromNode directly but matches production restore semantics,
		// while the latter is what the fix must pass to reusableResources to offset double-count.
		mergedMatchedAllocatable map[int]corev1.ResourceList
		mergedMatchedAllocated   map[int]corev1.ResourceList
		want                     *fwktype.Status
		wantCPUSet               cpuset.CPUSet
		wantNUMAResource         []NUMANodeResource
	}{
		{
			name: "error with missing preFilterState",
			pod:  &corev1.Pod{},
			want: fwktype.AsStatus(fwktype.ErrNotFound),
		},
		{
			name: "error with invalid cpu topology",
			state: &preFilterState{
				requestCPUBind: true,
			},
			cpuTopology: &CPUTopology{},
			pod:         &corev1.Pod{},
			want:        fwktype.NewStatus(fwktype.Error, ErrInvalidCPUTopology),
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
				requestCPUBind:         true,
				numCPUsNeeded:          4,
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
				requestCPUBind: false,
				numCPUsNeeded:  4,
				requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
			wantCPUSet:  cpuset.NewCPUSet(0, 2, 4, 6),
		},
		{
			name: "BE Pod reserves with node cpu bind policy",
			nodeLabels: map[string]string{
				extension.LabelNodeCPUBindPolicy: string(extension.NodeCPUBindPolicySpreadByPCPUs),
			},
			state: &preFilterState{
				requestCPUBind: false,
				numCPUsNeeded:  4,
				requests: corev1.ResourceList{
					extension.BatchCPU: resource.MustParse("4000"),
				},
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        nil,
		},
		{
			name: "error with big request cpu",
			state: &preFilterState{
				requestCPUBind: true,
				numCPUsNeeded:  24,
			},
			cpuTopology: buildCPUTopologyForTest(2, 1, 4, 2),
			pod:         &corev1.Pod{},
			want:        fwktype.NewStatus(fwktype.Unschedulable, "not enough cpus available to satisfy request"),
		},
		{
			name: "succeed with valid cpu topology and node numa least allocate strategy",
			nodeLabels: map[string]string{
				extension.LabelNodeNUMAAllocateStrategy: string(extension.NodeNUMAAllocateStrategyLeastAllocated),
			},
			state: &preFilterState{
				requestCPUBind:         true,
				numCPUsNeeded:          4,
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
				requestCPUBind:         true,
				numCPUsNeeded:          4,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			cpuTopology:   buildCPUTopologyForTest(2, 1, 8, 2),
			allocatedCPUs: []int{0, 1, 2, 3},
			pod:           &corev1.Pod{},
			want:          nil,
			wantCPUSet:    cpuset.NewCPUSet(4, 5, 6, 7),
		},
		{
			name: "succeed allocate from reservation reserved cpus;default allocate policy",
			state: &preFilterState{
				requestCPUBind:         true,
				numCPUsNeeded:          4,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					remainedCPUs: cpuset.NewCPUSet(4, 5, 6, 7, 8, 9, 10),
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			want:                      nil,
			wantCPUSet:                cpuset.NewCPUSet(4, 5, 6, 7),
		},
		{
			name: "succeed allocate from reservation reserved cpus;restricted allocate policy",
			state: &preFilterState{
				requestCPUBind:         true,
				numCPUsNeeded:          4,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					remainedCPUs: cpuset.NewCPUSet(4, 5, 6, 7, 8, 9, 10),
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			want:                      nil,
			wantCPUSet:                cpuset.NewCPUSet(4, 5, 6, 7),
		},
		{
			name: "failed to allocate from reservation reserved cpus;restricted allocate policy",
			state: &preFilterState{
				requestCPUBind:         true,
				numCPUsNeeded:          4,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				hasReservationAffinity: true,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					remainedCPUs: cpuset.NewCPUSet(4, 5),
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			want:                      fwktype.NewStatus(fwktype.Unschedulable, "Reservation(s) not enough cpus available to satisfy request"),
			wantCPUSet:                cpuset.NewCPUSet(),
		},
		{
			name: "succeed allocate from reservation reserved numa resource and cpuset;restricted allocate policy",
			state: &preFilterState{
				requestCPUBind:         true,
				numCPUsNeeded:          4,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				hasReservationAffinity: true,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					remainedCPUs: cpuset.NewCPUSet(4, 5, 6, 7, 8, 9, 10),
					allocatable: map[int]corev1.ResourceList{
						0: {
							corev1.ResourceCPU: resource.MustParse("7"),
						},
					},
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 8, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      nil,
			wantCPUSet:                cpuset.NewCPUSet(4, 5, 6, 7),
			wantNUMAResource: []NUMANodeResource{
				{
					Node:      0,
					Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				},
			},
		},
		{
			name: "failed to allocate from reservation reserved numa resource and cpuset;restricted allocate policy",
			state: &preFilterState{
				requestCPUBind:         true,
				numCPUsNeeded:          4,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				hasReservationAffinity: true,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					remainedCPUs: cpuset.NewCPUSet(4, 5),
					allocatable: map[int]corev1.ResourceList{
						0: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					remained: map[int]corev1.ResourceList{
						0: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      fwktype.NewStatus(fwktype.Unschedulable, "Reservation(s) not enough cpus available to satisfy request"),
			wantCPUSet:                cpuset.NewCPUSet(),
		},
		{
			name: "succeed allocate from reservation reserved numa resource;restricted allocate policy",
			state: &preFilterState{
				numCPUsNeeded:          4,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				hasReservationAffinity: true,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					allocatable: map[int]corev1.ResourceList{
						1: {
							corev1.ResourceCPU: resource.MustParse("7"),
						},
					},
					remained: map[int]corev1.ResourceList{
						1: {
							corev1.ResourceCPU: resource.MustParse("7"),
						},
					},
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node1,
			want:                      nil,
			wantCPUSet:                cpuset.CPUSet{},
			wantNUMAResource: []NUMANodeResource{
				{
					Node:      1,
					Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				},
			},
		},
		{
			name: "failed to allocate from reservation reserved numa resource;restricted allocate policy",
			state: &preFilterState{
				numCPUsNeeded:          4,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				hasReservationAffinity: true,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					allocatable: map[int]corev1.ResourceList{
						0: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					remained: map[int]corev1.ResourceList{
						0: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      fwktype.NewStatus(fwktype.Unschedulable, "Reservation(s) Insufficient NUMA cpu"),
			wantCPUSet:                cpuset.NewCPUSet(),
		},
		{
			// Regression for the tryAllocateFromNode double-accounting bug:
			// node NUMA0 total=8 cpu; matched R.Spec.Resources (allocatable) = 7 cpu on NUMA0 and
			// R owner has already consumed 4 cpu on NUMA0; in production they are both recorded
			// on the node NUMA account, making the account 7+4=11 cpu on NUMA0 (exceeding total 8).
			// We simulate this by injecting a single extraNUMAAllocations of 11 cpu on NUMA0
			// (the test helper's addCPUs/addPodAllocation path cannot accumulate R's NUMA cpu when
			// R.remainedCPUs is empty because addCPUs pre-registers the UID in allocatedPods).
			// The pod has no reservation-affinity and is not nominated, so Reserve falls back to
			// tryAllocateFromNode which must return mergedMatchedAllocated (=4) so that
			// available = max(0, 8 - max(0, 11-4)) = max(0, 8-7) = 1 cpu, allowing a 1-cpu pod.
			// Without the fix, reusableResources omits mergedMatchedAllocated so available=0 and
			// the pod would be rejected with Insufficient NUMA cpu.
			name: "fallback tryAllocateFromNode: return mergedMatchedAllocated to offset matched R owner double-count",
			state: &preFilterState{
				numCPUsNeeded:          1,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					allocatable: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("7")},
					},
					remained: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("3")},
					},
				},
			},
			skipNominate: true,
			extraNUMAAllocations: []NUMANodeResource{
				{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("11")}},
			},
			mergedMatchedAllocatable: map[int]corev1.ResourceList{
				0: {corev1.ResourceCPU: resource.MustParse("7")},
			},
			mergedMatchedAllocated: map[int]corev1.ResourceList{
				0: {corev1.ResourceCPU: resource.MustParse("4")},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      nil,
			wantCPUSet:                cpuset.CPUSet{},
			wantNUMAResource: []NUMANodeResource{
				{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}},
			},
		},
		{
			// Boundary: same topology/accounts as the regression case above; pod requests 2 cpu.
			// Even after the fix returns mergedMatchedAllocated=4, available is clamped to
			// max(0, 8 - max(0, 11-4)) = 1 cpu, i.e. only the node free part outside R.allocatable.
			// The pod requesting 2 cpu must be rejected; this proves the fix does NOT let a pod
			// without reservation-affinity steal matched R capacity (R.allocatable stays reserved).
			name: "fallback tryAllocateFromNode: do not steal matched reservation capacity beyond node free",
			state: &preFilterState{
				numCPUsNeeded:          2,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					allocatable: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("7")},
					},
					remained: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("3")},
					},
				},
			},
			skipNominate: true,
			extraNUMAAllocations: []NUMANodeResource{
				{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("11")}},
			},
			mergedMatchedAllocatable: map[int]corev1.ResourceList{
				0: {corev1.ResourceCPU: resource.MustParse("7")},
			},
			mergedMatchedAllocated: map[int]corev1.ResourceList{
				0: {corev1.ResourceCPU: resource.MustParse("4")},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      fwktype.NewStatus(fwktype.Unschedulable, "Insufficient NUMA cpu"),
			wantCPUSet:                cpuset.NewCPUSet(),
		},
		{
			// Regression for the tryAllocateFromNode double-accounting bug:
			// node NUMA0 total=8 cpu; matched R.Spec.Resources (allocatable) = 7 cpu on NUMA0 and
			// R owner has already consumed 4 cpu on NUMA0; in production they are both recorded
			// on the node NUMA account, making the account 7+4=11 cpu on NUMA0 (exceeding total 8).
			// We simulate this by injecting a single extraNUMAAllocations of 11 cpu on NUMA0
			// (the test helper's addCPUs/addPodAllocation path cannot accumulate R's NUMA cpu when
			// R.remainedCPUs is empty because addCPUs pre-registers the UID in allocatedPods).
			// The pod has no reservation-affinity and is not nominated, so Reserve falls back to
			// tryAllocateFromNode which must return mergedMatchedAllocated (=4) so that
			// available = max(0, 8 - max(0, 11-4)) = max(0, 8-7) = 1 cpu, allowing a 1-cpu pod.
			// Without the fix, reusableResources omits mergedMatchedAllocated so available=0 and
			// the pod would be rejected with Insufficient NUMA cpu.
			name: "fallback tryAllocateFromNode: return mergedMatchedAllocated to offset matched R owner double-count",
			state: &preFilterState{
				numCPUsNeeded:          1,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					allocatable: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("7")},
					},
					remained: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("3")},
					},
				},
			},
			skipNominate: true,
			extraNUMAAllocations: []NUMANodeResource{
				{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("11")}},
			},
			mergedMatchedAllocatable: map[int]corev1.ResourceList{
				0: {corev1.ResourceCPU: resource.MustParse("7")},
			},
			mergedMatchedAllocated: map[int]corev1.ResourceList{
				0: {corev1.ResourceCPU: resource.MustParse("4")},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      nil,
			wantCPUSet:                cpuset.CPUSet{},
			wantNUMAResource: []NUMANodeResource{
				{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}},
			},
		},
		{
			// Boundary: same topology/accounts as the regression case above; pod requests 2 cpu.
			// Even after the fix returns mergedMatchedAllocated=4, available is clamped to
			// max(0, 8 - max(0, 11-4)) = 1 cpu, i.e. only the node free part outside R.allocatable.
			// The pod requesting 2 cpu must be rejected; this proves the fix does NOT let a pod
			// without reservation-affinity steal matched R capacity (R.allocatable stays reserved).
			name: "fallback tryAllocateFromNode: do not steal matched reservation capacity beyond node free",
			state: &preFilterState{
				numCPUsNeeded:          2,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					allocatable: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("7")},
					},
					remained: map[int]corev1.ResourceList{
						0: {corev1.ResourceCPU: resource.MustParse("3")},
					},
				},
			},
			skipNominate: true,
			extraNUMAAllocations: []NUMANodeResource{
				{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("11")}},
			},
			mergedMatchedAllocatable: map[int]corev1.ResourceList{
				0: {corev1.ResourceCPU: resource.MustParse("7")},
			},
			mergedMatchedAllocated: map[int]corev1.ResourceList{
				0: {corev1.ResourceCPU: resource.MustParse("4")},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      fwktype.NewStatus(fwktype.Unschedulable, "Insufficient NUMA cpu"),
			wantCPUSet:                cpuset.NewCPUSet(),
		},
		{
			// allocateWithNominated defense-in-depth: pod has a nominated R on NUMA1 but BestHint=NUMA0,
			// and no reservation-affinity (requiredFromReservation=false). tryAllocateFromReusable returns
			// (nil, nil) because Restricted second-Allocate fails (R resources not in hint), but the
			// fallback is refused since the pod has a concrete nominated reservation. Without this
			// check the pod would silently bypass the reservation via tryAllocateFromNode.
			name: "nominated R NUMA-incompatible without affinity: allocateWithNominated refuses fallback",
			state: &preFilterState{
				numCPUsNeeded:          4,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				// hasReservationAffinity=false (zero value) → requiredFromReservation=false
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					allocatable: map[int]corev1.ResourceList{
						1: {corev1.ResourceCPU: resource.MustParse("8")},
					},
					remained: map[int]corev1.ResourceList{
						1: {corev1.ResourceCPU: resource.MustParse("8")},
					},
					remainedCPUs: cpuset.NewCPUSet(8, 9, 10, 11, 12, 13, 14, 15),
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod:                       &corev1.Pod{},
			numaAffinity:              node0,
			want:                      fwktype.NewStatus(fwktype.Unschedulable, "pod has a nominated reservation but cannot be allocated within its NUMA scope"),
			wantCPUSet:                cpuset.NewCPUSet(),
		},
		{
			name: "succeed allocate for a reservation-ignored pod",
			state: &preFilterState{
				requestCPUBind:         true,
				numCPUsNeeded:          4,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			},
			matched: map[types.UID]reusableAlloc{
				uuid.NewUUID(): {
					remainedCPUs: cpuset.NewCPUSet(4, 5, 6, 7, 8, 9, 10),
				},
			},
			reservationAllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
			cpuTopology:               buildCPUTopologyForTest(2, 1, 4, 2),
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						extension.LabelReservationIgnored: "true",
					},
				},
				Spec: corev1.PodSpec{
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
			},
			want:       nil,
			wantCPUSet: cpuset.NewCPUSet(4, 5, 6, 7),
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

			suit := newPluginTestSuit(t, nil, nodes)
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			plg := p.(*Plugin)
			nodeAllocation := NewNodeAllocation("test-node-1")
			if tt.cpuTopology != nil {
				plg.topologyOptionsManager.UpdateTopologyOptions(nodeAllocation.nodeName, func(options *TopologyOptions) {
					options.CPUTopology = tt.cpuTopology
					for i := 0; i < tt.cpuTopology.NumSockets; i++ {
						options.NUMANodeResources = append(options.NUMANodeResources, NUMANodeResource{
							Node:      i,
							Resources: corev1.ResourceList{corev1.ResourceCPU: *resource.NewQuantity(int64(tt.cpuTopology.CPUsPerNode()), resource.DecimalSI)},
						})
					}
				})
				if len(tt.allocatedCPUs) > 0 {
					nodeAllocation.addCPUs(tt.cpuTopology, uuid.NewUUID(), cpuset.NewCPUSet(tt.allocatedCPUs...), schedulingconfig.CPUExclusivePolicyNone)
				}
				if len(tt.matched) > 0 {
					for reservationUID, alloc := range tt.matched {
						nodeAllocation.addCPUs(tt.cpuTopology, reservationUID, alloc.remainedCPUs, schedulingconfig.CPUExclusivePolicyNone)
						var numaResources []NUMANodeResource
						for i, allocatable := range alloc.allocatable {
							numaResources = append(numaResources, NUMANodeResource{
								Node:      i,
								Resources: allocatable,
							})
						}
						nodeAllocation.addPodAllocation(&PodAllocation{
							UID:                reservationUID,
							CPUSet:             alloc.remainedCPUs,
							CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
							NUMANodeResources:  numaResources,
						}, tt.cpuTopology)
					}
				}
				// Simulate owner pod NUMA usage already recorded on node NUMA account, which
				// together with the R fake pod allocation above produces the double-count.
				if len(tt.extraNUMAAllocations) > 0 {
					nodeAllocation.addPodAllocation(&PodAllocation{
						UID:               uuid.NewUUID(),
						NUMANodeResources: tt.extraNUMAAllocations,
					}, tt.cpuTopology)
				}
			}

			cpuManager := plg.resourceManager.(*resourceManager)
			cpuManager.nodeAllocations[nodeAllocation.nodeName] = nodeAllocation

			suit.start(t)

			cycleState := framework.NewCycleState()
			if tt.state != nil {
				cycleState.Write(stateKey, tt.state)
				if len(tt.matched) > 0 {
					for reservationUID, alloc := range tt.matched {
						reservation := &schedulingv1alpha1.Reservation{
							ObjectMeta: metav1.ObjectMeta{
								UID:  reservationUID,
								Name: "test-reservation",
							},
							Spec: schedulingv1alpha1.ReservationSpec{
								AllocatePolicy: tt.reservationAllocatePolicy,
							},
						}
						alloc.rInfo = frameworkext.NewReservationInfo(reservation)
						tt.matched[reservationUID] = alloc
						if !tt.skipNominate {
							rInfo := frameworkext.NewReservationInfo(reservation)
							plg.handle.GetReservationNominator().AddNominatedReservation(tt.pod, "test-node-1", rInfo)
						}
					}
					cycleState.Write(reservationRestoreStateKey, &reservationRestoreStateData{
						nodeToState: frameworkext.NodeReservationRestoreStates{
							"test-node-1": &nodeReservationRestoreStateData{
								matched:                  tt.matched,
								mergedMatchedAllocatable: tt.mergedMatchedAllocatable,
								mergedMatchedAllocated:   tt.mergedMatchedAllocated,
							},
						},
					})
				}
			}

			if tt.numaAffinity != nil {
				topologymanager.InitStore(cycleState)
				store := topologymanager.GetStore(cycleState)
				store.SetAffinity("test-node-1", topologymanager.NUMATopologyHint{NUMANodeAffinity: tt.numaAffinity})
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
			var gotNUMAResource []NUMANodeResource
			if tt.state.allocation != nil {
				gotCPUs = tt.state.allocation.CPUSet
				gotNUMAResource = tt.state.allocation.NUMANodeResources
			}
			assert.Equal(t, tt.wantCPUSet, gotCPUs)
			for i, nodeResource := range gotNUMAResource {
				assert.Equal(t, tt.wantNUMAResource[i].Node, nodeResource.Node)
				assert.True(t, quotav1.Equals(tt.wantNUMAResource[i].Resources, nodeResource.Resources))
			}
		})
	}
}

func TestPlugin_Unreserve(t *testing.T) {
	state := &preFilterState{
		requestCPUBind: true,
		numCPUsNeeded:  24,
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
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	}
	suit := newPluginTestSuit(t, nil, []*corev1.Node{node})
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
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

	suit.start(t)

	plg := p.(*Plugin)

	state := &preFilterState{
		requestCPUBind: true,
		numCPUsNeeded:  4,
		allocation: &PodAllocation{
			CPUSet: cpuset.NewCPUSet(0, 1, 2, 3),
		},
	}
	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)

	s := plg.PreBind(context.TODO(), cycleState, pod, node.Name)
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
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	}
	suit := newPluginTestSuit(t, nil, []*corev1.Node{node})
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
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

	suit.start(t)

	plg := p.(*Plugin)

	state := &preFilterState{
		requestCPUBind:         true,
		numCPUsNeeded:          4,
		preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
		allocation: &PodAllocation{
			CPUSet: cpuset.NewCPUSet(0, 1, 2, 3),
		},
	}
	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)

	s := plg.PreBind(context.TODO(), cycleState, pod, node.Name)
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
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	}
	suit := newPluginTestSuit(t, nil, []*corev1.Node{node})
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
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

	suit.start(t)

	plg := p.(*Plugin)

	state := &preFilterState{
		requestCPUBind: true,
		numCPUsNeeded:  4,
		allocation: &PodAllocation{
			CPUSet: cpuset.NewCPUSet(0, 1, 2, 3),
		},
	}
	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)

	s := plg.PreBindReservation(context.TODO(), cycleState, reservation, node.Name)
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
	suit := newPluginTestSuit(t, nil, nil)
	p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	pl := p.(*Plugin)
	cycleState := framework.NewCycleState()
	state := &preFilterState{
		requestCPUBind: true,
		numCPUsNeeded:  4,
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
			Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
	assert.Equal(t, cpuset.NewCPUSet(8, 9), nodeReservationState.(*nodeReservationRestoreStateData).matched[reservation.UID].remainedCPUs)

	rInfo.AddAssignedPod(podB)
	nodeReservationState, status = pl.RestoreReservation(context.TODO(), cycleState, testPod, []*frameworkext.ReservationInfo{rInfo}, nil, nodeInfo)
	assert.True(t, status.IsSuccess())
	assert.NotNil(t, nodeReservationState)
	assert.Equal(t, cpuset.NewCPUSet(), nodeReservationState.(*nodeReservationRestoreStateData).matched[reservation.UID].remainedCPUs)
	assert.Equal(t, cpuset.NewCPUSet(6, 7, 8, 9), nodeReservationState.(*nodeReservationRestoreStateData).matched[reservation.UID].allocatedCPUs)
}

func Test_appendResourceSpecIfMissed(t *testing.T) {
	tests := []struct {
		name         string
		resourceSpec *extension.ResourceSpec
		nodePolicy   extension.NodeCPUBindPolicy
		state        *preFilterState
		wantErr      bool
		wantSpec     *extension.ResourceSpec
	}{
		{
			name: "declared preferred cpu bind policy",
			resourceSpec: &extension.ResourceSpec{
				PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
			},
			state: &preFilterState{
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
			},
			wantSpec: &extension.ResourceSpec{
				PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
			},
		},
		{
			name: "declared default preferred cpu bind policy",
			resourceSpec: &extension.ResourceSpec{
				PreferredCPUBindPolicy: extension.CPUBindPolicyDefault,
			},
			state: &preFilterState{
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
			},
			wantSpec: &extension.ResourceSpec{
				PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
			},
		},
		{
			name: "declared required cpu bind policy",
			resourceSpec: &extension.ResourceSpec{
				RequiredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
			},
			state: &preFilterState{
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicySpreadByPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
			},
			wantSpec: &extension.ResourceSpec{
				RequiredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
			},
		},
		{
			name: "declared required cpu bind policy and default preferred cpu bind policy",
			resourceSpec: &extension.ResourceSpec{
				RequiredCPUBindPolicy:  extension.CPUBindPolicySpreadByPCPUs,
				PreferredCPUBindPolicy: extension.CPUBindPolicyDefault,
			},
			state: &preFilterState{
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicySpreadByPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
			},
			wantSpec: &extension.ResourceSpec{
				RequiredCPUBindPolicy:  extension.CPUBindPolicySpreadByPCPUs,
				PreferredCPUBindPolicy: extension.CPUBindPolicySpreadByPCPUs,
			},
		},
		{
			name: "declared required cpu bind policy and default preferred cpu bind policy and exclusive policy",
			resourceSpec: &extension.ResourceSpec{
				RequiredCPUBindPolicy:       extension.CPUBindPolicySpreadByPCPUs,
				PreferredCPUBindPolicy:      extension.CPUBindPolicyDefault,
				PreferredCPUExclusivePolicy: extension.CPUExclusivePolicyPCPULevel,
			},
			state: &preFilterState{
				requiredCPUBindPolicy:  schedulingconfig.CPUBindPolicySpreadByPCPUs,
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicySpreadByPCPUs,
			},
			wantSpec: &extension.ResourceSpec{
				RequiredCPUBindPolicy:       extension.CPUBindPolicySpreadByPCPUs,
				PreferredCPUBindPolicy:      extension.CPUBindPolicySpreadByPCPUs,
				PreferredCPUExclusivePolicy: extension.CPUExclusivePolicyPCPULevel,
			},
		},
		{
			name:       "LS Pod assigned on node with FullPCPUsOnly",
			state:      &preFilterState{},
			nodePolicy: extension.NodeCPUBindPolicyFullPCPUsOnly,
			wantSpec: &extension.ResourceSpec{
				RequiredCPUBindPolicy: extension.CPUBindPolicyFullPCPUs,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{}
			if tt.resourceSpec != nil {
				assert.NoError(t, extension.SetResourceSpec(pod, tt.resourceSpec))
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			}
			if tt.nodePolicy != "" {
				node.Labels[extension.LabelNodeCPUBindPolicy] = string(tt.nodePolicy)
			}
			err := appendResourceSpecIfMissed(pod, tt.state, node, &TopologyOptions{})
			if tt.wantErr != (err != nil) {
				t.Errorf("appendResourceSpecIfMissed(%v, %v)", pod, tt.state)
			}
			resourceSpec, err := extension.GetResourceSpec(pod.Annotations)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantSpec, resourceSpec)
		})
	}
}

func TestFilterWithNUMANodeScoring(t *testing.T) {
	mostAllocatedStrategy := &schedulingconfig.ScoringStrategy{
		Type: schedulingconfig.MostAllocated,
		Resources: []k8sschedconfig.ResourceSpec{
			{
				Name:   string(corev1.ResourceCPU),
				Weight: 1,
			},
			{
				Name:   string(corev1.ResourceMemory),
				Weight: 1,
			},
		},
	}
	leastAllocatedStrategy := &schedulingconfig.ScoringStrategy{
		Type: schedulingconfig.LeastAllocated,
		Resources: []k8sschedconfig.ResourceSpec{
			{
				Name:   string(corev1.ResourceCPU),
				Weight: 1,
			},
			{
				Name:   string(corev1.ResourceMemory),
				Weight: 1,
			},
		},
	}
	tests := []struct {
		name                string
		node                *corev1.Node
		numaNodeCount       int
		requestedPod        *corev1.Pod
		existingPods        map[int][]*corev1.Pod
		numaScoringStrategy *schedulingconfig.ScoringStrategy
		wantAffinity        bitmask.BitMask
	}{
		{
			name: "single numa nodes and select most allocated",
			node: st.MakeNode().Name("test-node-1").
				Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
				Label(extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode)).
				Obj(),
			numaNodeCount: 2,
			requestedPod:  st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "40Gi"}).Obj(),
			existingPods: map[int][]*corev1.Pod{
				0: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "8Gi"}).Obj(),
				},
				1: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "40", "memory": "8Gi"}).Obj(),
				},
			},
			numaScoringStrategy: mostAllocatedStrategy,
			wantAffinity: func() bitmask.BitMask {
				mask, _ := bitmask.NewBitMask(1)
				return mask
			}(),
		},
		{
			name: "single numa nodes and select least allocated",
			node: st.MakeNode().Name("test-node-1").
				Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
				Label(extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode)).
				Obj(),
			numaNodeCount: 2,
			requestedPod:  st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "40Gi"}).Obj(),
			existingPods: map[int][]*corev1.Pod{
				0: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "8Gi"}).Obj(),
				},
				1: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "40", "memory": "8Gi"}).Obj(),
				},
			},
			numaScoringStrategy: leastAllocatedStrategy,
			wantAffinity: func() bitmask.BitMask {
				mask, _ := bitmask.NewBitMask(0)
				return mask
			}(),
		},
		{
			name: "single numa nodes and only one node can be used",
			node: st.MakeNode().Name("test-node-1").
				Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
				Label(extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicySingleNUMANode)).
				Obj(),
			numaNodeCount: 2,
			requestedPod:  st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "40Gi"}).Obj(),
			existingPods: map[int][]*corev1.Pod{
				0: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "8Gi"}).Obj(),
				},
				1: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "52", "memory": "8Gi"}).Obj(),
				},
			},
			numaScoringStrategy: leastAllocatedStrategy,
			wantAffinity: func() bitmask.BitMask {
				mask, _ := bitmask.NewBitMask(0)
				return mask
			}(),
		},
		{
			name: "restricted numa nodes and select most allocated and preferred",
			node: st.MakeNode().Name("test-node-1").
				Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
				Label(extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicyRestricted)).
				Obj(),
			numaNodeCount: 4,
			requestedPod:  st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "40Gi"}).Obj(),
			existingPods: map[int][]*corev1.Pod{
				0: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "24", "memory": "8Gi"}).Obj(),
				},
				1: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "23", "memory": "8Gi"}).Obj(),
				},
				2: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "8Gi"}).Obj(),
				},
				3: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "8", "memory": "8Gi"}).Obj(),
				},
			},
			numaScoringStrategy: mostAllocatedStrategy,
			wantAffinity: func() bitmask.BitMask {
				mask, _ := bitmask.NewBitMask(3)
				return mask
			}(),
		},
		{
			name: "restricted numa nodes and select least allocated and preferred",
			node: st.MakeNode().Name("test-node-1").
				Capacity(map[corev1.ResourceName]string{"cpu": "104", "memory": "256Gi"}).
				Label(extension.LabelNUMATopologyPolicy, string(extension.NUMATopologyPolicyRestricted)).
				Obj(),
			numaNodeCount: 4,
			requestedPod:  st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "40Gi"}).Obj(),
			existingPods: map[int][]*corev1.Pod{
				0: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "24", "memory": "8Gi"}).Obj(),
				},
				1: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "23", "memory": "8Gi"}).Obj(),
				},
				2: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "4", "memory": "8Gi"}).Obj(),
				},
				3: {
					st.MakePod().Req(map[corev1.ResourceName]string{"cpu": "8", "memory": "8Gi"}).Obj(),
				},
			},
			numaScoringStrategy: leastAllocatedStrategy,
			wantAffinity: func() bitmask.BitMask {
				mask, _ := bitmask.NewBitMask(2)
				return mask
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, []*corev1.Node{tt.node})
			if tt.numaScoringStrategy != nil {
				suit.nodeNUMAResourceArgs.NUMAScoringStrategy = tt.numaScoringStrategy
			}
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NoError(t, err)
			pl := p.(*Plugin)

			if tt.numaNodeCount > 0 {
				numaNodeResource := corev1.ResourceList{}
				for resourceName, quantity := range tt.node.Status.Allocatable {
					if resourceName == corev1.ResourceCPU {
						numaNodeResource[resourceName] = *resource.NewMilliQuantity(quantity.MilliValue()/int64(tt.numaNodeCount), resource.DecimalSI)
					} else if resourceName == corev1.ResourceMemory {
						numaNodeResource[resourceName] = *resource.NewQuantity(quantity.Value()/int64(tt.numaNodeCount), resource.BinarySI)
					} else {
						numaNodeResource[resourceName] = *resource.NewQuantity(quantity.Value()/int64(tt.numaNodeCount), resource.DecimalSI)
					}
				}
				pl.topologyOptionsManager.UpdateTopologyOptions(tt.node.Name, func(options *TopologyOptions) {
					cores := tt.node.Status.Allocatable.Cpu().MilliValue() / 1000 / 2 / int64(tt.numaNodeCount)
					options.CPUTopology = buildCPUTopologyForTest(tt.numaNodeCount, 1, int(cores), 2)
					for i := 0; i < tt.numaNodeCount; i++ {
						options.NUMANodeResources = append(options.NUMANodeResources, NUMANodeResource{
							Node:      i,
							Resources: numaNodeResource.DeepCopy(),
						})
					}
				})
			}
			for numaNode, pods := range tt.existingPods {
				for _, v := range pods {
					id := uuid.NewUUID()
					pl.resourceManager.Update(tt.node.Name, &PodAllocation{
						UID:       id,
						Namespace: "default",
						Name:      string(id),
						NUMANodeResources: []NUMANodeResource{
							{
								Node:      numaNode,
								Resources: v.Spec.Containers[0].Resources.Requests,
							},
						},
					})
				}
			}

			cycleState := framework.NewCycleState()
			_, status := pl.PreFilter(context.TODO(), cycleState, tt.requestedPod, nil)
			assert.True(t, status.IsSuccess())

			nodeInfo, err := suit.Handle.SnapshotSharedLister().NodeInfos().Get(tt.node.Name)
			assert.NoError(t, err)
			status = pl.Filter(context.TODO(), cycleState, tt.requestedPod, nodeInfo)
			assert.True(t, status.IsSuccess())
			hint, _ := topologymanager.GetStore(cycleState).GetAffinity(tt.node.Name)
			assert.Equal(t, tt.wantAffinity.GetBits(), hint.NUMANodeAffinity.GetBits())
		})
	}
}

func Test_preFilterState_Clone(t *testing.T) {
	tests := []struct {
		name  string
		field *preFilterState
		want  *preFilterState
	}{
		{
			name: "normal state",
			field: &preFilterState{
				skip:                   false,
				numCPUsNeeded:          4,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				hasReservationAffinity: false,
			},
			want: &preFilterState{
				skip:                   false,
				numCPUsNeeded:          4,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				hasReservationAffinity: false,
			},
		},
		{
			name: "state with preemption",
			field: &preFilterState{
				schedulingStateData: schedulingStateData{
					preemptibleState: map[string]*preemptibleNodeState{
						"node1": {
							nodeAlloc: newPreemptibleAlloc(),
							reservationsAlloc: map[types.UID]*preemptibleAlloc{
								"reservation1": newPreemptibleAlloc(),
							},
						},
						"node2": {
							nodeAlloc: newPreemptibleAlloc(),
						},
					},
				},
				skip:                   false,
				numCPUsNeeded:          4,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				hasReservationAffinity: false,
			},
			want: &preFilterState{
				schedulingStateData: schedulingStateData{
					preemptibleState: map[string]*preemptibleNodeState{
						"node1": {
							nodeAlloc: newPreemptibleAlloc(),
							reservationsAlloc: map[types.UID]*preemptibleAlloc{
								"reservation1": newPreemptibleAlloc(),
							},
						},
						"node2": {
							nodeAlloc: newPreemptibleAlloc(),
						},
					},
				},
				skip:                   false,
				numCPUsNeeded:          4,
				requests:               corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
				preferredCPUBindPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
				podNUMATopologyPolicy:  extension.NUMATopologyPolicyRestricted,
				hasReservationAffinity: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.field.Clone()
			assert.Equal(t, tt.want, got)
		})
	}
}
