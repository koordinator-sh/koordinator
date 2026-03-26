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

package scarceresourceavoidance

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	clientfeatures "k8s.io/client-go/features"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	configv1 "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
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

type PredicateClientSetAndHandle struct {
	frameworkext.ExtendedHandle
	koordinatorClientSet koordinatorclientset.Interface
	koordInformerFactory koordinatorinformers.SharedInformerFactory
}

func NodeResourcesPluginFactoryProxy(factoryFn frameworkruntime.PluginFactory, plugin *fwktype.Plugin) frameworkruntime.PluginFactory {
	return func(_ context.Context, args apiruntime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
		koordClient := koordfake.NewSimpleClientset()
		koordInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClient, 0)
		extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
			frameworkext.WithKoordinatorClientSet(koordClient),
			frameworkext.WithKoordinatorSharedInformerFactory(koordInformerFactory))
		if err != nil {
			return nil, err
		}
		extender := extenderFactory.NewFrameworkExtender(handle.(framework.Framework))
		*plugin, err = factoryFn(context.Background(), args, &PredicateClientSetAndHandle{
			ExtendedHandle:       extender,
			koordinatorClientSet: koordClient,
			koordInformerFactory: koordInformerFactory,
		})
		return *plugin, err
	}
}

func TestPlugin_Score(t *testing.T) {

	var v1args configv1.ScarceResourceAvoidanceArgs
	v1args.Resources = []v1.ResourceName{"nvidia.com/gpu"}

	var scarceResourceAvoidanceArgs config.ScarceResourceAvoidanceArgs
	err := configv1.Convert_v1_ScarceResourceAvoidanceArgs_To_config_ScarceResourceAvoidanceArgs(&v1args, &scarceResourceAvoidanceArgs, nil)
	assert.NoError(t, err)

	var ptplugin fwktype.Plugin
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
					corev1.ResourceCPU:    resource.MustParse("96"),
					corev1.ResourceMemory: resource.MustParse("512Gi"),
					"nvidia.com/gpu":      resource.MustParse("8"),
					"xx.xx/xx":            resource.MustParse("8"),
				},
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("96"),
					corev1.ResourceMemory: resource.MustParse("512Gi"),
					"nvidia.com/gpu":      resource.MustParse("8"),
					"xx.xx/xx":            resource.MustParse("8"),
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "testNode2",
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("96"),
					corev1.ResourceMemory: resource.MustParse("512Gi"),
				},
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("96"),
					corev1.ResourceMemory: resource.MustParse("512Gi"),
				},
			},
		},
	}

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}

	snapshot := newTestSharedLister(nil, nodes)
	fh, err := schedulertesting.NewFramework(context.TODO(), registeredPlugins, "koord-scheduler",
		frameworkruntime.WithClientSet(cs),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithSnapshotSharedLister(snapshot),
	)
	assert.Nil(t, err)

	p, err := proxyNew(context.TODO(), &scarceResourceAvoidanceArgs, fh)
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
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("32Gi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("16"),
							corev1.ResourceMemory: resource.MustParse("32Gi"),
						},
					},
				},
			},
		},
	}

	nodeInfos := make([]fwktype.NodeInfo, 0, len(nodes))
	for _, n := range nodes {
		ni, err := snapshot.Get(n.Name)
		assert.NoError(t, err)
		nodeInfos = append(nodeInfos, ni)
	}
	status := p.(fwktype.PreScorePlugin).PreScore(context.TODO(), cycleState, pod, nodeInfos)
	if status != nil {
		t.Fatal("PreScore run err")
	}

	var nodeInfo1, nodeInfo2 fwktype.NodeInfo
	nodeInfo1, err = snapshot.Get("testNode1")
	assert.NoError(t, err)
	nodeInfo2, err = snapshot.Get("testNode2")
	assert.NoError(t, err)
	scoreNode1, _ := p.(*Plugin).Score(context.TODO(), cycleState, pod, nodeInfo1)
	scoreNode2, _ := p.(*Plugin).Score(context.TODO(), cycleState, pod, nodeInfo2)
	if scoreNode1 >= scoreNode2 {
		t.Fatal("scoreNode1 must >= scoreNode2")
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
