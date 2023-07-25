/*
Copyright 2019 The Kubernetes Authors.

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

package limitaware

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	st "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1beta2"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
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

type pluginTestSuit struct {
	fw              framework.Framework
	pluginFactory   func() (framework.Plugin, error)
	extenderFactory *frameworkext.FrameworkExtenderFactory
}

func newPluginTestSuitWith(t *testing.T, pods []*corev1.Pod, nodes []*corev1.Node, ratio extension.LimitToAllocatableRatio) *pluginTestSuit {
	var v1beta2args v1beta2.LimitAwareArgs
	v1beta2.SetDefaults_LimitAwareArgs(&v1beta2args)
	var limitAwareArgs config.LimitAwareArgs
	err := v1beta2.Convert_v1beta2_LimitAwareArgs_To_config_LimitAwareArgs(&v1beta2args, &limitAwareArgs, nil)
	assert.NoError(t, err)
	limitAwareArgs.DefaultLimitToAllocatableRatio = ratio

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, _ := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	extenderFactory.InitScheduler(frameworkext.NewFakeScheduler())
	proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, New)

	registeredPlugins := []st.RegisterPluginFunc{
		st.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		st.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newTestSharedLister(pods, nodes)

	fakeRecorder := record.NewFakeRecorder(1024)
	eventRecorder := record.NewEventRecorderAdapter(fakeRecorder)

	fw, err := st.NewFramework(
		registeredPlugins,
		"koord-scheduler",
		runtime.WithClientSet(cs),
		runtime.WithInformerFactory(informerFactory),
		runtime.WithSnapshotSharedLister(snapshot),
		runtime.WithEventRecorder(eventRecorder),
	)
	assert.NoError(t, err)

	factory := func() (framework.Plugin, error) {
		return proxyNew(&limitAwareArgs, fw)
	}

	return &pluginTestSuit{
		fw:              fw,
		pluginFactory:   factory,
		extenderFactory: extenderFactory,
	}
}

func (s *pluginTestSuit) start() {
	s.fw.SharedInformerFactory().Start(nil)
	s.extenderFactory.KoordinatorSharedInformerFactory().Start(nil)
	s.fw.SharedInformerFactory().WaitForCacheSync(nil)
	s.extenderFactory.KoordinatorSharedInformerFactory().WaitForCacheSync(nil)
}

var (
	extendedResourceA     = corev1.ResourceName("example.com/aaa")
	extendedResourceB     = corev1.ResourceName("example.com/bbb")
	kubernetesIOResourceA = corev1.ResourceName("kubernetes.io/something")
	kubernetesIOResourceB = corev1.ResourceName("subdomain.kubernetes.io/something")
	hugePageResourceA     = corev1.ResourceName(corev1.ResourceHugePagesPrefix + "2Mi")
)

func makeResources(milliCPU, memory, pods, extendedA, storage, hugePageA int64) corev1.NodeResources {
	return corev1.NodeResources{
		Capacity: corev1.ResourceList{
			corev1.ResourceCPU:              *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
			corev1.ResourceMemory:           *resource.NewQuantity(memory, resource.BinarySI),
			corev1.ResourcePods:             *resource.NewQuantity(pods, resource.DecimalSI),
			extendedResourceA:               *resource.NewQuantity(extendedA, resource.DecimalSI),
			corev1.ResourceEphemeralStorage: *resource.NewQuantity(storage, resource.BinarySI),
			hugePageResourceA:               *resource.NewQuantity(hugePageA, resource.BinarySI),
		},
	}
}

func makeAllocatableResources(milliCPU, memory, pods, extendedA, storage, hugePageA int64) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:              *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
		corev1.ResourceMemory:           *resource.NewQuantity(memory, resource.BinarySI),
		corev1.ResourcePods:             *resource.NewQuantity(pods, resource.DecimalSI),
		extendedResourceA:               *resource.NewQuantity(extendedA, resource.DecimalSI),
		corev1.ResourceEphemeralStorage: *resource.NewQuantity(storage, resource.BinarySI),
		hugePageResourceA:               *resource.NewQuantity(hugePageA, resource.BinarySI),
	}
}

func newResourcePod(usage ...framework.Resource) *corev1.Pod {
	var containers []corev1.Container
	for _, req := range usage {
		rl := corev1.ResourceList{
			corev1.ResourceCPU:              *resource.NewMilliQuantity(req.MilliCPU, resource.DecimalSI),
			corev1.ResourceMemory:           *resource.NewQuantity(req.Memory, resource.BinarySI),
			corev1.ResourcePods:             *resource.NewQuantity(int64(req.AllowedPodNumber), resource.BinarySI),
			corev1.ResourceEphemeralStorage: *resource.NewQuantity(req.EphemeralStorage, resource.BinarySI),
		}
		for rName, rQuant := range req.ScalarResources {
			if rName == hugePageResourceA {
				rl[rName] = *resource.NewQuantity(rQuant, resource.BinarySI)
			} else {
				rl[rName] = *resource.NewQuantity(rQuant, resource.DecimalSI)
			}
		}
		containers = append(containers, corev1.Container{
			Resources: corev1.ResourceRequirements{Limits: rl},
		})
	}
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: containers,
		},
	}
}

func newResourcePodForNonZeroTest(zeroNotSet bool, usage ...framework.Resource) *corev1.Pod {
	var containers []corev1.Container
	for _, req := range usage {
		rl := corev1.ResourceList{
			corev1.ResourceCPU:              *resource.NewMilliQuantity(req.MilliCPU, resource.DecimalSI),
			corev1.ResourceMemory:           *resource.NewQuantity(req.Memory, resource.BinarySI),
			corev1.ResourcePods:             *resource.NewQuantity(int64(req.AllowedPodNumber), resource.BinarySI),
			corev1.ResourceEphemeralStorage: *resource.NewQuantity(req.EphemeralStorage, resource.BinarySI),
		}
		for rName, rQuant := range req.ScalarResources {
			if rName == hugePageResourceA {
				rl[rName] = *resource.NewQuantity(rQuant, resource.BinarySI)
			} else {
				rl[rName] = *resource.NewQuantity(rQuant, resource.DecimalSI)
			}
		}
		if zeroNotSet {
			if req.MilliCPU == 0 {
				delete(rl, corev1.ResourceCPU)
			}
			if req.Memory == 0 {
				delete(rl, corev1.ResourceMemory)
			}
		}
		containers = append(containers, corev1.Container{
			Resources: corev1.ResourceRequirements{Limits: rl},
		})
	}
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: containers,
		},
	}
}

func newResourceInitPod(pod *corev1.Pod, usage ...framework.Resource) *corev1.Pod {
	pod.Spec.InitContainers = newResourcePod(usage...).Spec.Containers
	return pod
}

func newResourceOverheadPod(pod *corev1.Pod, overhead corev1.ResourceList) *corev1.Pod {
	pod.Spec.Overhead = overhead
	return pod
}

func getErrReason(rn corev1.ResourceName) string {
	return fmt.Sprintf("Insufficient %v limit", rn)
}

var defaultLimitToAllocatableRatio = extension.LimitToAllocatableRatio{
	corev1.ResourceCPU:    intstr.FromInt(100),
	corev1.ResourceMemory: intstr.FromInt(100),
	extendedResourceA:     intstr.FromInt(100),
	extendedResourceB:     intstr.FromInt(100),
	kubernetesIOResourceA: intstr.FromInt(100),
	kubernetesIOResourceB: intstr.FromInt(100),
	hugePageResourceA:     intstr.FromInt(100),
}

func TestEnoughLimits(t *testing.T) {
	enoughPodsTests := []struct {
		name                      string
		pod                       *corev1.Pod
		ownerReference            *metav1.OwnerReference
		existingPods              []*corev1.Pod
		args                      config.LimitAwareArgs
		wantInsufficientResources []noderesources.InsufficientResource
		wantStatus                *framework.Status
	}{
		{
			pod:                       &corev1.Pod{},
			existingPods:              []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 10, Memory: 20})},
			name:                      "no resources requested always fits",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod:          newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 10, Memory: 20})},
			name:         "too many resources fails",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(corev1.ResourceCPU), getErrReason(corev1.ResourceMemory)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: corev1.ResourceCPU, Reason: getErrReason(corev1.ResourceCPU), Requested: 1, Used: 10, Capacity: 10},
				{ResourceName: corev1.ResourceMemory, Reason: getErrReason(corev1.ResourceMemory), Requested: 1, Used: 20, Capacity: 20},
			},
		},
		{
			pod: newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
			ownerReference: &metav1.OwnerReference{
				APIVersion:         "apps/v1",
				Kind:               "DaemonSet",
				Name:               "test-daemonset",
				UID:                "",
				Controller:         nil,
				BlockOwnerDeletion: nil,
			},
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 10, Memory: 20})},
			name:         "too many resources fails, but daemonset pod pass",
			wantStatus:   nil,
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: corev1.ResourceCPU, Reason: getErrReason(corev1.ResourceCPU), Requested: 1, Used: 10, Capacity: 10},
				{ResourceName: corev1.ResourceMemory, Reason: getErrReason(corev1.ResourceMemory), Requested: 1, Used: 20, Capacity: 20},
			},
		},

		{
			pod:          newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 3, Memory: 1}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 8, Memory: 19})},
			name:         "too many resources fails due to init container cpu",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(corev1.ResourceCPU)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: corev1.ResourceCPU, Reason: getErrReason(corev1.ResourceCPU), Requested: 3, Used: 8, Capacity: 10},
			},
		},
		{
			pod:          newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 3, Memory: 1}, framework.Resource{MilliCPU: 2, Memory: 1}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 8, Memory: 19})},
			name:         "too many resources fails due to highest init container cpu",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(corev1.ResourceCPU)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: corev1.ResourceCPU, Reason: getErrReason(corev1.ResourceCPU), Requested: 3, Used: 8, Capacity: 10},
			},
		},
		{
			pod:          newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 1, Memory: 3}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 9, Memory: 19})},
			name:         "too many resources fails due to init container memory",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(corev1.ResourceMemory)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: corev1.ResourceMemory, Reason: getErrReason(corev1.ResourceMemory), Requested: 3, Used: 19, Capacity: 20},
			},
		},
		{
			pod:          newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 1, Memory: 3}, framework.Resource{MilliCPU: 1, Memory: 2}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 9, Memory: 19})},
			name:         "too many resources fails due to highest init container memory",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(corev1.ResourceMemory)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: corev1.ResourceMemory, Reason: getErrReason(corev1.ResourceMemory), Requested: 3, Used: 19, Capacity: 20},
			},
		},
		{
			pod:                       newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 1, Memory: 1}),
			existingPods:              []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 9, Memory: 19})},
			name:                      "init container fits because it's the max, not sum, of containers and init containers",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod:                       newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}), framework.Resource{MilliCPU: 1, Memory: 1}, framework.Resource{MilliCPU: 1, Memory: 1}),
			existingPods:              []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 9, Memory: 19})},
			name:                      "multiple init containers fit because it's the max, not sum, of containers and init containers",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod:                       newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
			existingPods:              []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 5, Memory: 5})},
			name:                      "both resources fit",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod:          newResourcePod(framework.Resource{MilliCPU: 2, Memory: 1}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 9, Memory: 5})},
			name:         "one resource memory fits",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(corev1.ResourceCPU)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: corev1.ResourceCPU, Reason: getErrReason(corev1.ResourceCPU), Requested: 2, Used: 9, Capacity: 10},
			},
		},
		{
			pod:          newResourcePod(framework.Resource{MilliCPU: 1, Memory: 2}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 5, Memory: 19})},
			name:         "one resource cpu fits",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(corev1.ResourceMemory)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: corev1.ResourceMemory, Reason: getErrReason(corev1.ResourceMemory), Requested: 2, Used: 19, Capacity: 20},
			},
		},
		{
			pod:                       newResourcePod(framework.Resource{MilliCPU: 5, Memory: 1}),
			existingPods:              []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 5, Memory: 19})},
			name:                      "equal edge case",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod:                       newResourceInitPod(newResourcePod(framework.Resource{MilliCPU: 4, Memory: 1}), framework.Resource{MilliCPU: 5, Memory: 1}),
			existingPods:              []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 5, Memory: 19})},
			name:                      "equal edge case for init container",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod:                       newResourcePod(framework.Resource{ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 1}}),
			existingPods:              []*corev1.Pod{newResourcePod()},
			name:                      "extended resource fits",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod:                       newResourceInitPod(newResourcePod(framework.Resource{}), framework.Resource{ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 1}}),
			existingPods:              []*corev1.Pod{newResourcePod()},
			name:                      "extended resource fits for init container",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 10}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 0}})},
			name:         "extended resource capacity enforced",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: extendedResourceA, Reason: getErrReason(extendedResourceA), Requested: 10, Used: 0, Capacity: 5},
			},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 10}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 0}})},
			name:         "extended resource capacity enforced for init container",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: extendedResourceA, Reason: getErrReason(extendedResourceA), Requested: 10, Used: 0, Capacity: 5},
			},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 1}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 5}})},
			name:         "extended resource allocatable enforced",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: extendedResourceA, Reason: getErrReason(extendedResourceA), Requested: 1, Used: 5, Capacity: 5},
			},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 1}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 5}})},
			name:         "extended resource allocatable enforced for init container",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: extendedResourceA, Reason: getErrReason(extendedResourceA), Requested: 1, Used: 5, Capacity: 5},
			},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 3}},
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 3}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 2}})},
			name:         "extended resource allocatable enforced for multiple containers",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: extendedResourceA, Reason: getErrReason(extendedResourceA), Requested: 6, Used: 2, Capacity: 5},
			},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 3}},
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 3}}),
			existingPods:              []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 2}})},
			name:                      "extended resource allocatable admits multiple init containers",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 6}},
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 3}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{extendedResourceA: 2}})},
			name:         "extended resource allocatable enforced for multiple init containers",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: extendedResourceA, Reason: getErrReason(extendedResourceA), Requested: 6, Used: 2, Capacity: 5},
			},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceB: 1}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0})},
			name:         "extended resource allocatable enforced for unknown resource",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceB)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: extendedResourceB, Reason: getErrReason(extendedResourceB), Requested: 1, Used: 0, Capacity: 0},
			},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{extendedResourceB: 1}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0})},
			name:         "extended resource allocatable enforced for unknown resource for init container",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(extendedResourceB)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: extendedResourceB, Reason: getErrReason(extendedResourceB), Requested: 1, Used: 0, Capacity: 0},
			},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{kubernetesIOResourceA: 10}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0})},
			name:         "kubernetes.io resource capacity enforced",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(kubernetesIOResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: kubernetesIOResourceA, Reason: getErrReason(kubernetesIOResourceA), Requested: 10, Used: 0, Capacity: 0},
			},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{kubernetesIOResourceB: 10}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0})},
			name:         "kubernetes.io resource capacity enforced for init container",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(kubernetesIOResourceB)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: kubernetesIOResourceB, Reason: getErrReason(kubernetesIOResourceB), Requested: 10, Used: 0, Capacity: 0},
			},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{hugePageResourceA: 10}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{hugePageResourceA: 0}})},
			name:         "hugepages resource capacity enforced",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(hugePageResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: hugePageResourceA, Reason: getErrReason(hugePageResourceA), Requested: 10, Used: 0, Capacity: 5},
			},
		},
		{
			pod: newResourceInitPod(newResourcePod(framework.Resource{}),
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{hugePageResourceA: 10}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{hugePageResourceA: 0}})},
			name:         "hugepages resource capacity enforced for init container",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(hugePageResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: hugePageResourceA, Reason: getErrReason(hugePageResourceA), Requested: 10, Used: 0, Capacity: 5},
			},
		},
		{
			pod: newResourcePod(
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{hugePageResourceA: 3}},
				framework.Resource{MilliCPU: 1, Memory: 1, ScalarResources: map[corev1.ResourceName]int64{hugePageResourceA: 3}}),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 0, Memory: 0, ScalarResources: map[corev1.ResourceName]int64{hugePageResourceA: 2}})},
			name:         "hugepages resource allocatable enforced for multiple containers",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(hugePageResourceA)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: hugePageResourceA, Reason: getErrReason(hugePageResourceA), Requested: 6, Used: 2, Capacity: 5},
			},
		},
		{
			pod: newResourceOverheadPod(
				newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
				corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("3m"), corev1.ResourceMemory: resource.MustParse("13")},
			),
			existingPods:              []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 5, Memory: 5})},
			name:                      "resources + pod overhead fits",
			wantInsufficientResources: []noderesources.InsufficientResource{},
		},
		{
			pod: newResourceOverheadPod(
				newResourcePod(framework.Resource{MilliCPU: 1, Memory: 1}),
				corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1m"), corev1.ResourceMemory: resource.MustParse("15")},
			),
			existingPods: []*corev1.Pod{newResourcePod(framework.Resource{MilliCPU: 5, Memory: 5})},
			name:         "requests + overhead does not fit for memory",
			wantStatus:   framework.NewStatus(framework.Unschedulable, getErrReason(corev1.ResourceMemory)),
			wantInsufficientResources: []noderesources.InsufficientResource{
				{ResourceName: corev1.ResourceMemory, Reason: getErrReason(corev1.ResourceMemory), Requested: 16, Used: 5, Capacity: 20},
			},
		},
	}

	for _, test := range enoughPodsTests {
		t.Run(test.name, func(t *testing.T) {
			nodeInfo := framework.NewNodeInfo(test.existingPods...)
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}, Status: corev1.NodeStatus{Capacity: makeResources(10, 20, 32, 5, 20, 5).Capacity, Allocatable: makeAllocatableResources(10, 20, 32, 5, 20, 5)}}
			nodeInfo.SetNode(node)

			if test.ownerReference != nil {
				test.pod.OwnerReferences = append(test.pod.OwnerReferences, *test.ownerReference)
			}

			suit := newPluginTestSuitWith(t, test.existingPods, []*corev1.Node{node}, defaultLimitToAllocatableRatio)
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			cache := p.(*Plugin).nodeLimitsCache
			for _, existingPod := range test.existingPods {
				cache.AddPod(node.Name, existingPod)
			}

			cycleState := framework.NewCycleState()
			_, preFilterStatus := p.(framework.PreFilterPlugin).PreFilter(context.Background(), cycleState, test.pod)
			if !preFilterStatus.IsSuccess() {
				t.Errorf("prefilter failed with status: %v", preFilterStatus)
			}

			gotStatus := p.(framework.FilterPlugin).Filter(context.Background(), cycleState, test.pod, nodeInfo)
			if !reflect.DeepEqual(gotStatus, test.wantStatus) {
				t.Errorf("status does not match: %v, want: %v", gotStatus, test.wantStatus)
			}

			gotInsufficientResources := fitsLimit(defaultLimitToAllocatableRatio, getPodResourceLimit(test.pod, false), cache.GetNodeLimits(node.Name), nodeInfo.Allocatable)
			if !reflect.DeepEqual(gotInsufficientResources, test.wantInsufficientResources) {
				t.Errorf("insufficient resources do not match: %+v, want: %v", gotInsufficientResources, test.wantInsufficientResources)
			}
		})
	}
}
