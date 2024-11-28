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

package deviceshare

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	v1beta3schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1beta3"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexttesting "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/testing"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
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

func (f *testSharedLister) StorageInfos() framework.StorageInfoLister {
	return f
}

func (f *testSharedLister) IsPVCUsedByPods(key string) bool {
	return false
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

type pluginTestSuit struct {
	framework.Framework
	koordClientSet                   koordclientset.Interface
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory
	proxyNew                         runtime.PluginFactory
}

func newPluginTestSuit(t *testing.T, nodes []*corev1.Node) *pluginTestSuit {
	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, _ := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
		frameworkext.WithReservationNominator(frameworkexttesting.NewFakeReservationNominator()),
	)
	proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, New)

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}

	var objects []apiruntime.Object
	for _, node := range nodes {
		objects = append(objects, node)
	}
	cs := kubefake.NewSimpleClientset(objects...)
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newTestSharedLister(nil, nodes)

	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		runtime.WithClientSet(cs),
		runtime.WithInformerFactory(informerFactory),
		runtime.WithSnapshotSharedLister(snapshot),
	)
	assert.Nil(t, err)
	return &pluginTestSuit{
		Framework:                        fh,
		koordClientSet:                   koordClientSet,
		koordinatorSharedInformerFactory: koordSharedInformerFactory,
		proxyNew:                         proxyNew,
	}
}

func getDefaultArgs() *schedulerconfig.DeviceShareArgs {
	v1beta3Args := &v1beta3schedulerconfig.DeviceShareArgs{}
	v1beta3schedulerconfig.SetDefaults_DeviceShareArgs(v1beta3Args)
	args := &schedulerconfig.DeviceShareArgs{}
	_ = v1beta3schedulerconfig.Convert_v1beta3_DeviceShareArgs_To_config_DeviceShareArgs(v1beta3Args, args, nil)
	return args
}

func Test_New(t *testing.T) {
	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, _ := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, New)

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newTestSharedLister(nil, nil)
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		runtime.WithClientSet(cs),
		runtime.WithInformerFactory(informerFactory),
		runtime.WithSnapshotSharedLister(snapshot),
	)
	assert.Nil(t, err)
	p, err := proxyNew(getDefaultArgs(), fh)
	assert.NotNil(t, p)
	assert.Nil(t, err)
	assert.Equal(t, Name, p.Name())
}

type fakeReservationCache struct {
	rInfo *frameworkext.ReservationInfo
}

func (f *fakeReservationCache) DeleteReservation(r *schedulingv1alpha1.Reservation) *frameworkext.ReservationInfo {
	return frameworkext.NewReservationInfo(r)
}

func (f *fakeReservationCache) GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	return f.rInfo
}

func Test_Plugin_PreFilterExtensions(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	}
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	reservation.SetReservationCache(&fakeReservationCache{})

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	cycleState := framework.NewCycleState()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: resource.MustParse("100"),
						},
					},
				},
			},
		},
	}
	_, status := pl.PreFilter(context.TODO(), cycleState, pod)
	assert.True(t, status.IsSuccess())

	pl.nodeDeviceCache.updateNodeDevice("test-node-1", &schedulingv1alpha1.Device{
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  pointer.Int32(1),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  pointer.Int32(2),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
		},
	})
	nd := pl.nodeDeviceCache.getNodeDevice("test-node-1", false)
	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
		},
	}
	allocatedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "allocated-pod-1",
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
		},
	}
	nd.updateCacheUsed(allocations, allocatedPod, true)

	podInfo, _ := framework.NewPodInfo(allocatedPod)
	status = pl.PreFilterExtensions().RemovePod(context.TODO(), cycleState, pod, podInfo, nodeInfo)
	assert.True(t, status.IsSuccess())

	expectPreemptible := map[string]map[schedulingv1alpha1.DeviceType]deviceResources{
		"test-node-1": {
			schedulingv1alpha1.GPU: {
				1: {
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
		},
	}
	state, status := getPreFilterState(cycleState)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, expectPreemptible, state.preemptibleDevices)

	podInfo, _ = framework.NewPodInfo(allocatedPod)
	status = pl.PreFilterExtensions().AddPod(context.TODO(), cycleState, pod, podInfo, nodeInfo)
	assert.True(t, status.IsSuccess())
	expectPreemptible = map[string]map[schedulingv1alpha1.DeviceType]deviceResources{
		node.Name: {},
	}
	assert.Equal(t, expectPreemptible, state.preemptibleDevices)
}

func Test_Plugin_PreFilterExtensionsWithReservation(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	}
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)
	testReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation",
			UID:  "123456",
		},
	}
	assert.NoError(t, reservationutil.SetReservationAvailable(testReservation, node.Name))
	reservationCache := &fakeReservationCache{
		rInfo: frameworkext.NewReservationInfo(testReservation),
	}
	reservation.SetReservationCache(reservationCache)

	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	cycleState := framework.NewCycleState()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: resource.MustParse("100"),
						},
					},
				},
			},
		},
	}
	_, status := pl.PreFilter(context.TODO(), cycleState, pod)
	assert.True(t, status.IsSuccess())

	pl.nodeDeviceCache.updateNodeDevice("test-node-1", &schedulingv1alpha1.Device{
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  pointer.Int32(1),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  pointer.Int32(2),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
		},
	})
	nd := pl.nodeDeviceCache.getNodeDevice("test-node-1", false)
	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
		},
	}
	allocatedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "allocated-pod-1",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}
	nd.updateCacheUsed(allocations, allocatedPod, true)
	reservationCache.rInfo.AddAssignedPod(allocatedPod)

	podInfo, _ := framework.NewPodInfo(allocatedPod)
	status = pl.PreFilterExtensions().RemovePod(context.TODO(), cycleState, pod, podInfo, nodeInfo)
	assert.True(t, status.IsSuccess())

	expectPreemptible := map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{
		"test-node-1": {
			"123456": {
				schedulingv1alpha1.GPU: {
					1: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
		},
	}
	state, status := getPreFilterState(cycleState)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, expectPreemptible, state.preemptibleInRRs)

	podInfo, _ = framework.NewPodInfo(allocatedPod)
	status = pl.PreFilterExtensions().AddPod(context.TODO(), cycleState, pod, podInfo, nodeInfo)
	assert.True(t, status.IsSuccess())
	expectPreemptible = map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{
		"test-node-1": {
			"123456": {},
		},
	}
	assert.Equal(t, expectPreemptible, state.preemptibleInRRs)

	state.preemptibleInRRs = map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{}
	podInfo, _ = framework.NewPodInfo(allocatedPod)
	status = pl.PreFilterExtensions().AddPod(context.TODO(), cycleState, pod, podInfo, nodeInfo)
	assert.True(t, status.IsSuccess())
	expectPreemptible = map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{
		"test-node-1": {
			"123456": {
				schedulingv1alpha1.GPU: {
					1: {
						apiext.ResourceGPUCore:        *resource.NewQuantity(-100, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(-8*1024*1024*1024, resource.BinarySI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(-100, resource.DecimalSI),
					},
				},
			},
		},
	}
	assert.Equal(t, expectPreemptible, state.preemptibleInRRs)
}

func Test_Plugin_PreFilter(t *testing.T) {
	tests := []struct {
		name       string
		pod        *corev1.Pod
		wantStatus *framework.Status
		wantState  *preFilterState
	}{
		{
			name: "skip non device pod",
			pod:  &corev1.Pod{},
			wantState: &preFilterState{
				skip:               true,
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
				preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
			wantStatus: framework.NewStatus(framework.Skip),
		},
		{
			name: "pod has invalid fpga request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("101"),
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("invalid resource unit %v: 101", apiext.ResourceFPGA)),
		},
		{
			name: "pod has invalid gpu request 1",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPU: resource.MustParse("101"),
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("invalid resource unit %v: 101", apiext.ResourceGPU)),
		},
		{
			name: "pod has invalid gpu request 2",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPUCore: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("invalid resource device requests: [%s]", apiext.ResourceGPUCore)),
		},
		{
			name: "pod has invalid gpu request 3",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPUMemoryRatio: resource.MustParse("101"),
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("invalid resource device requests: [%s]", apiext.ResourceGPUMemoryRatio)),
		},
		{
			name: "pod has valid gpu request 1",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPU: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				gpuRequirements: &GPURequirements{
					numberOfGPUs: 1,
					requestsPerGPU: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
				preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
		},
		{
			name: "pod has valid gpu request 2",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				gpuRequirements: &GPURequirements{
					numberOfGPUs: 1,
					requestsPerGPU: corev1.ResourceList{
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
				preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
		},
		{
			name: "pod has valid gpu request 3",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
				},
				gpuRequirements: &GPURequirements{
					numberOfGPUs: 1,
					requestsPerGPU: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					gpuShared: true,
				},
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
				preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
		},
		{
			name: "pod has valid gpu request 4",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPUCore:   resource.MustParse("100"),
									apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:   resource.MustParse("100"),
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
				},
				gpuRequirements: &GPURequirements{
					numberOfGPUs: 1,
					requestsPerGPU: corev1.ResourceList{
						apiext.ResourceGPUCore:   resource.MustParse("100"),
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					gpuShared: true,
				},
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
				preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
		},
		{
			name: "pod has valid gpu request 5",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				gpuRequirements: &GPURequirements{
					numberOfGPUs: 1,
					requestsPerGPU: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
				preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
		},
		{
			name: "pod has valid fpga request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.FPGA: {
						apiext.ResourceFPGA: resource.MustParse("100"),
					},
				},
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
				preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
		},
		{
			name: "pod has valid gpu & rdma request",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPU:  resource.MustParse("100"),
									apiext.ResourceRDMA: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
					schedulingv1alpha1.RDMA: {
						apiext.ResourceRDMA: resource.MustParse("100"),
					},
				},
				gpuRequirements: &GPURequirements{
					numberOfGPUs: 1,
					requestsPerGPU: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
				preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
		},
		{
			name: "skip zero requests",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPU: resource.MustParse("0"),
								},
							},
						},
					},
				},
			},
			wantState: &preFilterState{
				skip:               true,
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{},
				preemptibleInRRs:   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
			wantStatus: framework.NewStatus(framework.Skip),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			cycleState := framework.NewCycleState()
			_, status := p.PreFilter(context.TODO(), cycleState, tt.pod)
			assert.Equal(t, tt.wantStatus, status)
			state, _ := getPreFilterState(cycleState)
			if tt.wantState != nil && tt.wantState.podRequests != nil {
				for deviceType := range tt.wantState.podRequests {
					tt.wantState.podRequests[deviceType] = removeFormat(tt.wantState.podRequests[deviceType])
					state.podRequests[deviceType] = removeFormat(state.podRequests[deviceType])
				}
			}
			if tt.wantState != nil && tt.wantState.gpuRequirements != nil {
				tt.wantState.gpuRequirements.requestsPerGPU = removeFormat(tt.wantState.gpuRequirements.requestsPerGPU)
				state.gpuRequirements.requestsPerGPU = removeFormat(state.gpuRequirements.requestsPerGPU)
			}
			assert.Equal(t, tt.wantState, state)
		})
	}
}

func removeFormat(list corev1.ResourceList) corev1.ResourceList {
	newList := corev1.ResourceList{}
	for name, quantity := range list {
		newList[name] = *resource.NewQuantity(quantity.Value(), quantity.Format)
	}
	return newList
}

func Test_Plugin_Filter(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	testNodeInfo := &framework.NodeInfo{}
	testNodeInfo.SetNode(testNode)
	tests := []struct {
		name            string
		state           *preFilterState
		reserved        apiext.DeviceAllocations
		nodeDeviceCache *nodeDeviceCache
		nodeInfo        *framework.NodeInfo
		want            *framework.Status
	}{
		{
			name: "error missing preFilterState",
			want: framework.AsStatus(framework.ErrNotFound),
		},
		{
			name:  "skip == true",
			state: &preFilterState{skip: true},
			want:  nil,
		},
		{
			name:            "error missing nodecache",
			state:           &preFilterState{skip: false},
			nodeDeviceCache: newNodeDeviceCache(),
			nodeInfo:        testNodeInfo,
			want:            nil,
		},
		{
			name: "insufficient device resource 1",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": newNodeDevice(),
				},
			},
			nodeInfo: testNodeInfo,
			want:     framework.NewStatus(framework.UnschedulableAndUnresolvable, "Insufficient gpu devices"),
		},
		{
			name: "insufficient device resource 2",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("75"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
									apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
							},
						},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-1",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     framework.NewStatus(framework.Unschedulable, "Insufficient gpu devices"),
		},
		{
			name: "insufficient device resource 3",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
					schedulingv1alpha1.FPGA: {
						apiext.ResourceFPGA: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("75"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
									apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
								},
							},
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
							},
						},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-1",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							schedulingv1alpha1.FPGA: {
								{
									Type:   schedulingv1alpha1.FPGA,
									Health: true,
									UUID:   "123456-fpga-1",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     framework.NewStatus(framework.Unschedulable, "Insufficient gpu devices"),
		},
		{
			name: "insufficient device resource 4",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
					schedulingv1alpha1.FPGA: {
						apiext.ResourceFPGA: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("75"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
									apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
								},
							},
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("50"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
							},
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("50"),
								},
							},
						},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-1",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							schedulingv1alpha1.FPGA: {
								{
									Type:   schedulingv1alpha1.FPGA,
									Health: true,
									UUID:   "123456-fpga-1",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     framework.NewStatus(framework.Unschedulable, "Insufficient"),
		},
		{
			name: "sufficient device resource 1",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.FPGA: {
						apiext.ResourceFPGA: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
						},
						deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.FPGA: {
								{
									Type:   schedulingv1alpha1.FPGA,
									Health: true,
									UUID:   "123456-fpga-1",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "sufficient device resource 2",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.FPGA: {
						apiext.ResourceFPGA: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("75"),
								},
								1: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
								1: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("25"),
								},
							},
						},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.FPGA: {
								{
									Type:   schedulingv1alpha1.FPGA,
									Health: true,
									UUID:   "123456-fpga-0",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
								{
									Type:   schedulingv1alpha1.FPGA,
									Health: true,
									UUID:   "123456-fgpa-1",
									Minor:  pointer.Int32(1),
									Resources: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "sufficient device resource 3",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-1",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							schedulingv1alpha1.FPGA: {
								{
									Type:   schedulingv1alpha1.FPGA,
									Health: true,
									UUID:   "123456-fpga-1",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "sufficient device resource 4",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("75"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
									apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
								},
							},
						},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-0",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-1",
									Minor:  pointer.Int32(1),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "sufficient device resource 5",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("75"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
									apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
								},
							},
						},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-0",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-1",
									Minor:  pointer.Int32(1),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "sufficient device resource 6",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUMemory: resource.MustParse("16Gi"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("75"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
									apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
								},
							},
						},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(0)},
								{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(1)},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "allocate from preemptible",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{
					"test-node": {
						schedulingv1alpha1.GPU: {
							0: corev1.ResourceList{
								apiext.ResourceGPUCore:        resource.MustParse("100"),
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("0"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
									apiext.ResourceGPUMemory:      resource.MustParse("0Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-0",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "allocate from reserved",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
							apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
						},
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						allocateSet: map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("0"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
									apiext.ResourceGPUMemory:      resource.MustParse("0Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-0",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "allocate still successfully from node even if remaining of reserved are zero",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("0"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
							apiext.ResourceGPUMemory:      resource.MustParse("0Gi"),
						},
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						allocateSet: map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("0"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
									apiext.ResourceGPUMemory:      resource.MustParse("0Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-0",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-1",
									Minor:  pointer.Int32(1),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "pod stuck when use multi gpu",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUShared: resource.MustParse("4"),
						apiext.ResourceGPUMemory: resource.MustParse("160G"),
					},
				},
			},
			// reserved: apiext.DeviceAllocations{},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						allocateSet: map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								2: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								3: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								4: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								5: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								6: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								7: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								8: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								2: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								3: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								4: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								5: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								6: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								7: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
								8: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
								},
							},
						},
						deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
						vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
						numaTopology:  &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-0",
									Minor:  pointer.Int32(0),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-1",
									Minor:  pointer.Int32(1),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-2",
									Minor:  pointer.Int32(2),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-3",
									Minor:  pointer.Int32(3),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-4",
									Minor:  pointer.Int32(4),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-5",
									Minor:  pointer.Int32(5),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-6",
									Minor:  pointer.Int32(6),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
									},
								},
								{
									Type:   schedulingv1alpha1.GPU,
									Health: true,
									UUID:   "123456-7",
									Minor:  pointer.Int32(7),
									Resources: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("80Gi"),
									},
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{}
			p := &Plugin{nodeDeviceCache: tt.nodeDeviceCache}
			cycleState := framework.NewCycleState()
			if tt.state != nil {
				requests := corev1.ResourceList{}
				for _, req := range tt.state.podRequests {
					util.AddResourceList(requests, req)
				}
				pod.Spec.Containers = []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: requests,
						},
					},
				}
				state, status := preparePod(pod)
				assert.True(t, status.IsSuccess())
				state.preemptibleInRRs = tt.state.preemptibleInRRs
				state.preemptibleDevices = tt.state.preemptibleDevices
				cycleState.Write(stateKey, state)
			}
			if tt.reserved != nil {
				reservation := &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						UID:  uuid.NewUUID(),
						Name: "reservation-1",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Template: &corev1.PodTemplateSpec{},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						NodeName: "test-node",
					},
				}
				err := apiext.SetDeviceAllocations(reservation, tt.reserved)
				assert.NoError(t, err)

				tt.nodeDeviceCache.updatePod(nil, reservationutil.NewReservePod(reservation))

				namespacedName := reservationutil.GetReservePodNamespacedName(reservation)
				allocatable := tt.nodeDeviceCache.getNodeDevice("test-node", false).getUsed(namespacedName.Namespace, namespacedName.Name)

				restoreState := &reservationRestoreStateData{
					skip: false,
					nodeToState: frameworkext.NodeReservationRestoreStates{
						"test-node": &nodeReservationRestoreStateData{
							mergedMatchedAllocatable: allocatable,
							matched: []reservationAlloc{
								{
									rInfo:       frameworkext.NewReservationInfo(reservation),
									allocatable: allocatable,
									remained:    allocatable,
								},
							},
						},
					},
				}
				cycleState.Write(reservationRestoreStateKey, restoreState)
			}
			status := p.Filter(context.TODO(), cycleState, pod, tt.nodeInfo)
			assert.Equal(t, tt.want.Code(), status.Code())
			assert.True(t, strings.Contains(status.Message(), tt.want.Message()))
		})
	}
}

func Test_Plugin_FilterReservation(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	}
	suit := newPluginTestSuit(t, []*corev1.Node{node})
	p, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	cycleState := framework.NewCycleState()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: resource.MustParse("100"),
						},
					},
				},
			},
		},
	}
	_, status := pl.PreFilter(context.TODO(), cycleState, pod)
	assert.True(t, status.IsSuccess())

	pl.nodeDeviceCache.updateNodeDevice("test-node-1", &schedulingv1alpha1.Device{
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  pointer.Int32(1),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  pointer.Int32(2),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
		},
	})
	nd := pl.nodeDeviceCache.getNodeDevice("test-node-1", false)
	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
		},
	}

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation-1",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
		},
	}
	nd.updateCacheUsed(allocations, reservationutil.NewReservePod(reservation), true)

	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	status = pl.PreRestoreReservation(context.TODO(), cycleState, pod)
	assert.True(t, status.IsSuccess())
	reservationInfo := frameworkext.NewReservationInfo(reservation)
	nodeToState, status := pl.RestoreReservation(context.TODO(), cycleState, pod, []*frameworkext.ReservationInfo{reservationInfo}, nil, nodeInfo)
	assert.True(t, status.IsSuccess())
	status = pl.FinalRestoreReservation(context.TODO(), cycleState, pod, frameworkext.NodeReservationRestoreStates{
		"test-node-1": nodeToState,
	})
	assert.True(t, status.IsSuccess())

	status = pl.FilterReservation(context.TODO(), cycleState, pod, reservationInfo, "test-node-1")
	assert.True(t, status.IsSuccess())

	allocatedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "allocated-pod-1",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}
	nd.updateCacheUsed(apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
			{
				Minor: 2,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
		},
	}, allocatedPod, true)
	reservationInfo.AddAssignedPod(allocatedPod)
	nodeToState, status = pl.RestoreReservation(context.TODO(), cycleState, pod, []*frameworkext.ReservationInfo{reservationInfo}, nil, nodeInfo)
	assert.True(t, status.IsSuccess())
	status = pl.FinalRestoreReservation(context.TODO(), cycleState, pod, frameworkext.NodeReservationRestoreStates{
		"test-node-1": nodeToState,
	})
	assert.True(t, status.IsSuccess())

	status = pl.FilterReservation(context.TODO(), cycleState, pod, reservationInfo, "test-node-1")
	assert.Equal(t, framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient gpu devices"), status)
}

func Test_Plugin_Reserve(t *testing.T) {
	type args struct {
		nodeDeviceCache *nodeDeviceCache
		state           *preFilterState
		reserved        apiext.DeviceAllocations
		pod             *corev1.Pod
		nodeName        string
	}
	type wants struct {
		allocationResult apiext.DeviceAllocations
		status           *framework.Status
	}
	tests := []struct {
		name  string
		args  args
		wants wants
	}{
		{
			name: "error missing preFilterState",
			args: args{
				pod: &corev1.Pod{},
			},
			wants: wants{
				status: framework.AsStatus(framework.ErrNotFound),
			},
		},
		{
			name: "skip == true",
			args: args{
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: true,
				},
			},
		},
		{
			name: "error missing node cache",
			args: args{
				nodeDeviceCache: newNodeDeviceCache(),
				pod:             &corev1.Pod{},
				state: &preFilterState{
					skip: false,
				},
				nodeName: "test-node",
			},
			wants: wants{
				status: nil,
			},
		},
		{
			name: "insufficient device resource 1",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("25"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
										apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("75"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
										apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
									},
								},
							},
							vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
							numaTopology:  &NUMATopology{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.GPU: {
									{
										Type:   schedulingv1alpha1.GPU,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				status: framework.NewStatus(framework.Unschedulable, "Insufficient gpu devices"),
			},
		},
		{
			name: "insufficient device resource 2",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("25"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
										apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("75"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
										apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
									},
								},
							},
							vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
							numaTopology:  &NUMATopology{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.GPU: {
									{
										Type:   schedulingv1alpha1.GPU,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUCore:        resource.MustParse("200"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				status: framework.NewStatus(framework.Unschedulable, "Insufficient gpu devices"),
			},
		},
		{
			name: "insufficient device resource 3",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
							vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
							numaTopology:  &NUMATopology{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.GPU: {
									{
										Type:   schedulingv1alpha1.GPU,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUCore:        resource.MustParse("200"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				status: framework.NewStatus(framework.Unschedulable, "Insufficient gpu devices"),
			},
		},
		{
			name: "insufficient device resource 4",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("50"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
								},
							},
							deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("50"),
									},
								},
							},
							vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
							numaTopology:  &NUMATopology{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.RDMA: {
									{
										Type:   schedulingv1alpha1.RDMA,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.RDMA: {
							apiext.ResourceRDMA: resource.MustParse("100"),
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				status: framework.NewStatus(framework.Unschedulable, "Insufficient"),
			},
		},
		{
			name: "insufficient device resource 5",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.FPGA: {
									0: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.FPGA: {
									0: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
							},
							deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
							vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
							numaTopology:  &NUMATopology{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.RDMA: {
									{
										Type:   schedulingv1alpha1.RDMA,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
								schedulingv1alpha1.FPGA: {
									{
										Type:   schedulingv1alpha1.FPGA,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.RDMA: {
							apiext.ResourceRDMA: resource.MustParse("200"),
						},
						schedulingv1alpha1.FPGA: {
							apiext.ResourceFPGA: resource.MustParse("200"),
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				status: framework.NewStatus(framework.Unschedulable, "Insufficient"),
			},
		},
		{
			name: "sufficient device resource 1",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.FPGA: {
									0: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.FPGA: {
									0: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
							allocateSet:   make(map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources),
							vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
							numaTopology:  &NUMATopology{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.GPU: {
									{
										Type:   schedulingv1alpha1.GPU,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
								schedulingv1alpha1.RDMA: {
									{
										Type:   schedulingv1alpha1.RDMA,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
								schedulingv1alpha1.FPGA: {
									{
										Type:   schedulingv1alpha1.FPGA,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
						},
						schedulingv1alpha1.RDMA: {
							apiext.ResourceRDMA: resource.MustParse("100"),
						},
						schedulingv1alpha1.FPGA: {
							apiext.ResourceFPGA: resource.MustParse("100"),
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:        resource.MustParse("100"),
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
					schedulingv1alpha1.FPGA: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceFPGA: resource.MustParse("100"),
							},
						},
					},
					schedulingv1alpha1.RDMA: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceRDMA: resource.MustParse("100"),
							},
						},
					},
				},
			},
		},
		{
			name: "sufficient device resource 2",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
									1: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.FPGA: {
									0: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
									1: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
									1: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
									1: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.FPGA: {
									0: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
									1: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
									1: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
							allocateSet:   make(map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources),
							vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
							numaTopology:  &NUMATopology{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.GPU: {
									{
										Type:   schedulingv1alpha1.GPU,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
									{
										Type:   schedulingv1alpha1.GPU,
										Health: true,
										UUID:   "123456-2",
										Minor:  pointer.Int32(1),
									},
								},
								schedulingv1alpha1.RDMA: {
									{
										Type:   schedulingv1alpha1.RDMA,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
									{
										Type:   schedulingv1alpha1.RDMA,
										Health: true,
										UUID:   "123456-2",
										Minor:  pointer.Int32(1),
									},
								},
								schedulingv1alpha1.FPGA: {
									{
										Type:   schedulingv1alpha1.FPGA,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
									{
										Type:   schedulingv1alpha1.FPGA,
										Health: true,
										UUID:   "123456-2",
										Minor:  pointer.Int32(1),
									},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUCore:        resource.MustParse("200"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
						},
						schedulingv1alpha1.RDMA: {
							apiext.ResourceRDMA: resource.MustParse("200"),
						},
						schedulingv1alpha1.FPGA: {
							apiext.ResourceFPGA: resource.MustParse("200"),
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
						{
							Minor: 1,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
					schedulingv1alpha1.FPGA: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceFPGA: *resource.NewQuantity(100, resource.DecimalSI),
							},
						},
						{
							Minor: 1,
							Resources: corev1.ResourceList{
								apiext.ResourceFPGA: *resource.NewQuantity(100, resource.DecimalSI),
							},
						},
					},
					schedulingv1alpha1.RDMA: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
							},
						},
						{
							Minor: 1,
							Resources: corev1.ResourceList{
								apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
							},
						},
					},
				},
			},
		},
		{
			name: "sufficient device resource 3",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
							allocateSet:   make(map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources),
							vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
							numaTopology:  &NUMATopology{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.GPU: {
									{
										Type:   schedulingv1alpha1.GPU,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
				},
			},
		},
		{
			name: "sufficient device resource 4",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
							allocateSet:   make(map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources),
							vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
							numaTopology:  &NUMATopology{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.GPU: {
									{
										Type:   schedulingv1alpha1.GPU,
										Health: true,
										UUID:   "123456-1",
										Minor:  pointer.Int32(0),
									},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUMemory: resource.MustParse("16Gi"),
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
				},
			},
		},
		{
			name: "reserve from preemptible",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("0"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
										apiext.ResourceGPUMemory:      resource.MustParse("0Gi"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							numaTopology: &NUMATopology{},
							allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.GPU: {
									{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(0)},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
						},
					},
					preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{
						"test-node": {
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				status: nil,
				allocationResult: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
					schedulingv1alpha1.GPU: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:        resource.MustParse("100"),
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
				},
			},
		},
		{
			name: "reserve from reservation",
			args: args{
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("0"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
										apiext.ResourceGPUMemory:      resource.MustParse("0Gi"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							numaTopology: &NUMATopology{},
							deviceUsed:   map[schedulingv1alpha1.DeviceType]deviceResources{},
							allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
							deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
								schedulingv1alpha1.GPU: {
									{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(0)},
								},
							},
						},
					},
				},
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
						},
					},
				},
				reserved: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:        resource.MustParse("100"),
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
				},
				nodeName: "test-node",
			},
			wants: wants{
				status: nil,
				allocationResult: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
					schedulingv1alpha1.GPU: {
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:        resource.MustParse("100"),
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
			}
			suit := newPluginTestSuit(t, []*corev1.Node{node})
			p, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)
			pl := p.(*Plugin)
			pl.nodeDeviceCache = tt.args.nodeDeviceCache
			cycleState := framework.NewCycleState()
			if tt.args.state != nil {
				tt.args.state.gpuRequirements, _ = parseGPURequirements(&corev1.Pod{}, tt.args.state.podRequests, nil)
				cycleState.Write(stateKey, tt.args.state)
			}

			if len(tt.args.reserved) > 0 {
				reservation := &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						UID:  uuid.NewUUID(),
						Name: "reservation-1",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Template: &corev1.PodTemplateSpec{},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						NodeName: "test-node",
					},
				}
				err := apiext.SetDeviceAllocations(reservation, tt.args.reserved)
				assert.NoError(t, err)

				tt.args.nodeDeviceCache.updatePod(nil, reservationutil.NewReservePod(reservation))

				namespacedName := reservationutil.GetReservePodNamespacedName(reservation)
				allocatable := tt.args.nodeDeviceCache.getNodeDevice("test-node", false).getUsed(namespacedName.Namespace, namespacedName.Name)

				restoreState := &reservationRestoreStateData{
					skip: false,
					nodeToState: frameworkext.NodeReservationRestoreStates{
						"test-node": &nodeReservationRestoreStateData{
							mergedMatchedAllocatable: allocatable,
							matched: []reservationAlloc{
								{
									rInfo:       frameworkext.NewReservationInfo(reservation),
									allocatable: allocatable,
									remained:    allocatable,
								},
							},
						},
					},
				}
				cycleState.Write(reservationRestoreStateKey, restoreState)
				rInfo := frameworkext.NewReservationInfo(reservation)
				pl.handle.GetReservationNominator().AddNominatedReservation(tt.args.pod, "test-node", rInfo)
			}

			status := pl.Reserve(context.TODO(), cycleState, tt.args.pod, tt.args.nodeName)
			assert.Equal(t, tt.wants.status.Code(), status.Code())
			assert.True(t, strings.Contains(status.Message(), tt.wants.status.Message()))
			if tt.wants.allocationResult != nil {
				sortDeviceAllocations(tt.wants.allocationResult)
				sortDeviceAllocations(tt.args.state.allocationResult)
				assert.True(t, equality.Semantic.DeepEqual(tt.wants.allocationResult, tt.args.state.allocationResult))
			}
		})
	}
}

func sortDeviceAllocations(deviceAllocations apiext.DeviceAllocations) {
	for k, v := range deviceAllocations {
		sort.Slice(v, func(i, j int) bool {
			return v[i].Minor < v[j].Minor
		})
		deviceAllocations[k] = v
	}
}

func Test_Plugin_Unreserve(t *testing.T) {
	namespacedName := types.NamespacedName{
		Namespace: "default",
		Name:      "test",
	}
	type args struct {
		state           *preFilterState
		pod             *corev1.Pod
		nodeDeviceCache *nodeDeviceCache
	}
	tests := []struct {
		name      string
		args      args
		changed   bool
		wantCache *nodeDeviceCache
	}{
		{
			name: "return missing preFilterState",
			args: args{
				pod: &corev1.Pod{},
			},
		},
		{
			name: "return when skip == true",
			args: args{
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: true,
				},
			},
		},
		{
			name: "return missing node cache",
			args: args{
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: false,
				},
				nodeDeviceCache: newNodeDeviceCache(),
			},
		},
		{
			name: "normal case",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						UID:       "123456789",
						Namespace: "default",
						Name:      "test",
					},
				},
				state: &preFilterState{
					skip: false,
					allocationResult: apiext.DeviceAllocations{
						schedulingv1alpha1.GPU: {
							{
								Minor: 0,
								Resources: corev1.ResourceList{
									apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
									apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
							{
								Minor: 1,
								Resources: corev1.ResourceList{
									apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
									apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						schedulingv1alpha1.FPGA: {
							{
								Minor: 0,
								Resources: corev1.ResourceList{
									apiext.ResourceFPGA: *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
							{
								Minor: 1,
								Resources: corev1.ResourceList{
									apiext.ResourceFPGA: *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
						},
						schedulingv1alpha1.RDMA: {
							{
								Minor: 0,
								Resources: corev1.ResourceList{
									apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
							{
								Minor: 1,
								Resources: corev1.ResourceList{
									apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
				},
				nodeDeviceCache: &nodeDeviceCache{
					nodeDeviceInfos: map[string]*nodeDevice{
						"test-node": {
							deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("0"),
									},
									1: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("0"),
									},
								},
								schedulingv1alpha1.FPGA: {
									0: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("0"),
									},
									1: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("0"),
									},
								},
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("0"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
										apiext.ResourceGPUMemory:      resource.MustParse("0"),
									},
									1: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("0"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
										apiext.ResourceGPUMemory:      resource.MustParse("0"),
									},
								},
							},
							deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
									1: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.FPGA: {
									0: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
									1: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
									1: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.RDMA: {
									0: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
									1: corev1.ResourceList{
										apiext.ResourceRDMA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.FPGA: {
									0: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
									1: corev1.ResourceList{
										apiext.ResourceFPGA: resource.MustParse("100"),
									},
								},
								schedulingv1alpha1.GPU: {
									0: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
									1: corev1.ResourceList{
										apiext.ResourceGPUCore:        resource.MustParse("100"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
										apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
									},
								},
							},
							allocateSet: map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{
								schedulingv1alpha1.GPU: {
									namespacedName: {
										0: corev1.ResourceList{
											apiext.ResourceGPUCore:        resource.MustParse("100"),
											apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
											apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
										},
										1: corev1.ResourceList{
											apiext.ResourceGPUCore:        resource.MustParse("100"),
											apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
											apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
										},
									},
								},
								schedulingv1alpha1.FPGA: {
									namespacedName: {
										0: corev1.ResourceList{
											apiext.ResourceFPGA: resource.MustParse("100"),
										},
										1: corev1.ResourceList{
											apiext.ResourceFPGA: resource.MustParse("100"),
										},
									},
								},
								schedulingv1alpha1.RDMA: {
									namespacedName: {
										0: corev1.ResourceList{
											apiext.ResourceRDMA: resource.MustParse("100"),
										},
										1: corev1.ResourceList{
											apiext.ResourceRDMA: resource.MustParse("100"),
										},
									},
								},
							},
						},
					},
				},
			},

			changed: true,
			wantCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.RDMA: {
								0: corev1.ResourceList{
									apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
								},
								1: corev1.ResourceList{
									apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: *resource.NewQuantity(100, resource.DecimalSI),
								},
								1: corev1.ResourceList{
									apiext.ResourceFPGA: *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
									apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
									apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.RDMA: {
								0: corev1.ResourceList{
									apiext.ResourceRDMA: resource.MustParse("100"),
								},
								1: corev1.ResourceList{
									apiext.ResourceRDMA: resource.MustParse("100"),
								},
							},
							schedulingv1alpha1.FPGA: {
								0: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
								1: corev1.ResourceList{
									apiext.ResourceFPGA: resource.MustParse("100"),
								},
							},
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
								1: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
						allocateSet: map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{
							schedulingv1alpha1.GPU:  {},
							schedulingv1alpha1.FPGA: {},
							schedulingv1alpha1.RDMA: {},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{nodeDeviceCache: tt.args.nodeDeviceCache}
			cycleState := framework.NewCycleState()
			if tt.args.state != nil {
				cycleState.Write(stateKey, tt.args.state)
			}
			p.Unreserve(context.TODO(), cycleState, tt.args.pod, "test-node")
			if tt.changed {
				assert.Empty(t, tt.args.state.allocationResult)
				stateCmpOpts := []cmp.Option{
					cmp.AllowUnexported(nodeDevice{}),
					cmp.AllowUnexported(nodeDeviceCache{}),
					cmpopts.IgnoreFields(nodeDevice{}, "lock"),
					cmpopts.IgnoreFields(nodeDeviceCache{}, "lock"),
				}
				if diff := cmp.Diff(tt.wantCache, tt.args.nodeDeviceCache, stateCmpOpts...); diff != "" {
					t.Errorf("nodeDeviceCache does not match (-want,+got):\n%s", diff)
				}
			}
		})
	}
}

func Test_Plugin_PreBind(t *testing.T) {
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "123456789",
			Namespace: "default",
			Name:      "test",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			Containers: []corev1.Container{
				{
					Name: "test-container-a",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU:  resource.MustParse("2"),
							apiext.ResourceRDMA: resource.MustParse("100"),
						},
					},
				},
			},
		},
	}
	type args struct {
		state *preFilterState
		pod   *corev1.Pod
	}
	tests := []struct {
		name       string
		args       args
		wantPod    *corev1.Pod
		deviceCR   *schedulingv1alpha1.Device
		wantStatus *framework.Status
	}{
		{
			name: "empty state",
			args: args{
				pod: &corev1.Pod{},
			},
			wantPod:    &corev1.Pod{},
			wantStatus: framework.AsStatus(framework.ErrNotFound),
		},
		{
			name: "state skip",
			args: args{
				pod: &corev1.Pod{},
				state: &preFilterState{
					skip: true,
				},
			},
			wantPod: &corev1.Pod{},
		},
		{
			name: "pre-bind successfully, deviceCR with topology",
			args: args{
				pod: testPod.DeepCopy(),
				state: &preFilterState{
					skip: false,
					allocationResult: apiext.DeviceAllocations{
						schedulingv1alpha1.GPU: {
							{
								Minor: 0,
								Resources: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
							{
								Minor: 1,
								Resources: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
								},
							},
						},
						schedulingv1alpha1.RDMA: {
							{
								Minor: 1,
								Resources: corev1.ResourceList{
									apiext.ResourceRDMA: resource.MustParse("100"),
								},
							},
						},
					},
					podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
						schedulingv1alpha1.GPU: {
							apiext.ResourceGPUCore:        resource.MustParse("200"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
							apiext.ResourceGPUMemory:      resource.MustParse("32Gi"),
						},
						schedulingv1alpha1.RDMA: {
							apiext.ResourceRDMA: resource.MustParse("100"),
						},
					},
				},
			},
			deviceCR: fakeDeviceCR,
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":0,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"16Gi","koordinator.sh/gpu-memory-ratio":"100"}},{"minor":1,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"16Gi","koordinator.sh/gpu-memory-ratio":"100"}}],"rdma":[{"minor":1,"resources":{"koordinator.sh/rdma":"100"},"id":"0000:1f:00.0"}]}`,
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPU:  resource.MustParse("2"),
									apiext.ResourceRDMA: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			_, err := suit.ClientSet().CoreV1().Pods(testPod.Namespace).Create(context.TODO(), testPod, metav1.CreateOptions{})
			assert.NoError(t, err)
			if tt.deviceCR != nil {
				_, err = suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), tt.deviceCR, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			pl, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)

			suit.Framework.SharedInformerFactory().Start(nil)
			suit.koordinatorSharedInformerFactory.Start(nil)
			suit.Framework.SharedInformerFactory().WaitForCacheSync(nil)
			suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)

			cycleState := framework.NewCycleState()
			if tt.args.state != nil {
				cycleState.Write(stateKey, tt.args.state)
			}
			status := pl.(*Plugin).PreBind(context.TODO(), cycleState, tt.args.pod, fakeDeviceCR.Name)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantPod, tt.args.pod)
		})
	}
}

func Test_Plugin_PreBindReservation(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.ResizePod, true)()
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  "123456789",
			Name: "test",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPU: resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node",
		},
	}

	state := &preFilterState{
		skip: false,
		allocationResult: apiext.DeviceAllocations{
			schedulingv1alpha1.GPU: {
				{
					Minor: 0,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
					},
				},
			},
		},
		podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
			schedulingv1alpha1.GPU: {
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
			},
		},
	}

	suit := newPluginTestSuit(t, nil)

	_, err := suit.koordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
	assert.NoError(t, err)

	_, err = suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), fakeDeviceCRWithoutTopology, metav1.CreateOptions{})
	assert.NoError(t, err)

	pl, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)

	suit.Framework.SharedInformerFactory().Start(nil)
	suit.koordinatorSharedInformerFactory.Start(nil)
	suit.Framework.SharedInformerFactory().WaitForCacheSync(nil)
	suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)

	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)
	status := pl.(*Plugin).PreBindReservation(context.TODO(), cycleState, reservation, fakeDeviceCRWithoutTopology.Name)
	assert.True(t, status.IsSuccess())

	allocations, err := apiext.GetDeviceAllocations(reservation.Annotations)
	assert.NoError(t, err)
	expectAllocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 0,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
				},
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocations))
	resizeAllocatable, err := reservationutil.GetReservationResizeAllocatable(reservation.Annotations)
	assert.NoError(t, err)
	assert.Equal(t, expectAllocations[schedulingv1alpha1.GPU][0].Resources, resizeAllocatable.Resources)
}
