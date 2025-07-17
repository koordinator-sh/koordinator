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

package noderesourcesfitplus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8sConfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"

	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1beta3"
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

type PredicateClientSetAndHandle struct {
	frameworkext.ExtendedHandle
	koordinatorClientSet koordinatorclientset.Interface
	koordInformerFactory koordinatorinformers.SharedInformerFactory
}

func NodeResourcesPluginFactoryProxy(factoryFn frameworkruntime.PluginFactory, plugin *framework.Plugin) frameworkruntime.PluginFactory {
	return func(args apiruntime.Object, handle framework.Handle) (framework.Plugin, error) {
		koordClient := koordfake.NewSimpleClientset()
		koordInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClient, 0)
		extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
			frameworkext.WithKoordinatorClientSet(koordClient),
			frameworkext.WithKoordinatorSharedInformerFactory(koordInformerFactory))
		if err != nil {
			return nil, err
		}
		extender := extenderFactory.NewFrameworkExtender(handle.(framework.Framework))
		*plugin, err = factoryFn(args, &PredicateClientSetAndHandle{
			ExtendedHandle:       extender,
			koordinatorClientSet: koordClient,
			koordInformerFactory: koordInformerFactory,
		})
		return *plugin, err
	}
}

func TestPlugin_Score(t *testing.T) {

	var v1beta2args v1beta3.NodeResourcesFitPlusArgs
	v1beta2args.Resources = map[corev1.ResourceName]v1beta3.ResourcesType{
		"nvidia.com/gpu": {Type: k8sConfig.MostAllocated, Weight: 2},
		"cpu":            {Type: k8sConfig.LeastAllocated, Weight: 1},
		"memory":         {Type: k8sConfig.LeastAllocated, Weight: 1},
	}

	var nodeResourcesFitPlusArgs config.NodeResourcesFitPlusArgs
	err := v1beta3.Convert_v1beta3_NodeResourcesFitPlusArgs_To_config_NodeResourcesFitPlusArgs(&v1beta2args, &nodeResourcesFitPlusArgs, nil)
	assert.NoError(t, err)

	var ptplugin framework.Plugin
	proxyNew := NodeResourcesPluginFactoryProxy(New, &ptplugin)

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)

	nodes := []*corev1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testNode1",
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("96"),
					corev1.ResourceMemory:           resource.MustParse("512Gi"),
					"nvidia.com/gpu":                resource.MustParse("8"),
					corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
				},
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("96"),
					corev1.ResourceMemory:           resource.MustParse("512Gi"),
					"nvidia.com/gpu":                resource.MustParse("8"),
					corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testNode2",
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("96"),
					corev1.ResourceMemory:           resource.MustParse("512Gi"),
					"nvidia.com/gpu":                resource.MustParse("8"),
					"xx.xx/xx":                      resource.MustParse("8"),
					corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
				},
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("96"),
					corev1.ResourceMemory:           resource.MustParse("512Gi"),
					"nvidia.com/gpu":                resource.MustParse("8"),
					"xx.xx/xx":                      resource.MustParse("8"),
					corev1.ResourceEphemeralStorage: resource.MustParse("100Gi"),
				},
			},
		},
	}

	pods := []*corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "test-pod-0",
			},
			Spec: corev1.PodSpec{
				NodeName: "testNode1",
				Containers: []corev1.Container{
					{
						Name: "test-container",
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("16"),
								corev1.ResourceMemory: resource.MustParse("32Gi"),
								"nvidia.com/gpu":      resource.MustParse("4"),
							},
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("16"),
								corev1.ResourceMemory: resource.MustParse("32Gi"),
								"nvidia.com/gpu":      resource.MustParse("4"),
							},
						},
					},
				},
			},
		},
	}
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}

	snapshot := newTestSharedLister(pods, nodes)
	fh, err := schedulertesting.NewFramework(context.TODO(), registeredPlugins, "koord-scheduler",
		frameworkruntime.WithClientSet(cs),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithSnapshotSharedLister(snapshot),
	)
	assert.Nil(t, err)

	p, err := proxyNew(&nodeResourcesFitPlusArgs, fh)
	p.Name()
	assert.NotNil(t, p)
	assert.Nil(t, err)
	plug := p.(*Plugin)
	h := plug.handle.(*PredicateClientSetAndHandle)

	informerFactory.Start(context.TODO().Done())
	informerFactory.WaitForCacheSync(context.TODO().Done())

	h.koordInformerFactory.Start(context.TODO().Done())
	h.koordInformerFactory.WaitForCacheSync(context.TODO().Done())

	cycleState := framework.NewCycleState()

	nodeInfo, err := snapshot.Get("testNode1")
	assert.NoError(t, err)
	assert.NotNil(t, nodeInfo)
	nodeInfo, err = snapshot.Get("testNode2")
	assert.NoError(t, err)
	assert.NotNil(t, nodeInfo)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:              resource.MustParse("16"),
							corev1.ResourceMemory:           resource.MustParse("32Gi"),
							"nvidia.com/gpu":                resource.MustParse("2"),
							corev1.ResourceEphemeralStorage: resource.MustParse("10Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:              resource.MustParse("16"),
							corev1.ResourceMemory:           resource.MustParse("32Gi"),
							"nvidia.com/gpu":                resource.MustParse("2"),
							corev1.ResourceEphemeralStorage: resource.MustParse("10Gi"),
						},
					},
				},
			},
		},
	}

	status := p.(framework.PreScorePlugin).PreScore(context.TODO(), cycleState, pod, nodes)
	if status != nil {
		t.Fatal("PreScore run err")
	}

	scoreNode1, _ := p.(*Plugin).Score(context.TODO(), cycleState, pod, "testNode1")
	scoreNode2, _ := p.(*Plugin).Score(context.TODO(), cycleState, pod, "testNode2")
	if scoreNode1 <= scoreNode2 {
		t.Fatal("scoreNode1 must more than scoreNode2")
	}
}

func (f *testSharedLister) StorageInfos() framework.StorageInfoLister {
	return f
}

func (f *testSharedLister) IsPVCUsedByPods(key string) bool {
	return false
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
