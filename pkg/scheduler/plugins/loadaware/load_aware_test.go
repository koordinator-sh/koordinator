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

package loadaware

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	scheduledconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config/v1beta2"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
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

func TestNew(t *testing.T) {
	var v1beta2args v1beta2.LoadAwareSchedulingArgs
	v1beta2.SetDefaults_LoadAwareSchedulingArgs(&v1beta2args)
	var loadAwareSchedulingArgs config.LoadAwareSchedulingArgs
	err := v1beta2.Convert_v1beta2_LoadAwareSchedulingArgs_To_config_LoadAwareSchedulingArgs(&v1beta2args, &loadAwareSchedulingArgs, nil)
	assert.NoError(t, err)

	loadAwareSchedulingPluginConfig := scheduledconfig.PluginConfig{
		Name: Name,
		Args: &loadAwareSchedulingArgs,
	}

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extendHandle := frameworkext.NewExtendedHandle(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	proxyNew := frameworkext.PluginFactoryProxy(extendHandle, New)

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		func(reg *runtime.Registry, profile *scheduledconfig.KubeSchedulerProfile) {
			profile.PluginConfig = []scheduledconfig.PluginConfig{
				loadAwareSchedulingPluginConfig,
			}
		},
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterFilterPlugin(Name, proxyNew),
		schedulertesting.RegisterScorePlugin(Name, proxyNew, 1),
		schedulertesting.RegisterReservePlugin(Name, proxyNew),
	}

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newTestSharedLister(nil, nil)
	fh, err := schedulertesting.NewFramework(registeredPlugins, "koord-scheduler",
		runtime.WithClientSet(cs),
		runtime.WithInformerFactory(informerFactory),
		runtime.WithSnapshotSharedLister(snapshot),
	)
	assert.Nil(t, err)

	p, err := proxyNew(&loadAwareSchedulingArgs, fh)
	assert.NotNil(t, p)
	assert.Nil(t, err)
}

func TestFilterExpiredNodeMetric(t *testing.T) {
	tests := []struct {
		name       string
		nodeMetric *slov1alpha1.NodeMetric
		wantStatus *framework.Status
	}{
		{
			name: "filter healthy nodeMetrics",
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter unhealthy nodeMetric with nil updateTime",
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, ErrReasonNodeMetricExpired),
		},
		{
			name: "filter unhealthy nodeMetric with expired updateTime",
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now().Add(-180 * time.Second),
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, ErrReasonNodeMetricExpired),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v1beta2args v1beta2.LoadAwareSchedulingArgs
			v1beta2.SetDefaults_LoadAwareSchedulingArgs(&v1beta2args)
			var loadAwareSchedulingArgs config.LoadAwareSchedulingArgs
			err := v1beta2.Convert_v1beta2_LoadAwareSchedulingArgs_To_config_LoadAwareSchedulingArgs(&v1beta2args, &loadAwareSchedulingArgs, nil)
			assert.NoError(t, err)

			loadAwareSchedulingPluginConfig := scheduledconfig.PluginConfig{
				Name: Name,
				Args: &loadAwareSchedulingArgs,
			}

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extendHandle := frameworkext.NewExtendedHandle(
				frameworkext.WithKoordinatorClientSet(koordClientSet),
				frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)
			proxyNew := frameworkext.PluginFactoryProxy(extendHandle, New)

			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				func(reg *runtime.Registry, profile *scheduledconfig.KubeSchedulerProfile) {
					profile.PluginConfig = []scheduledconfig.PluginConfig{
						loadAwareSchedulingPluginConfig,
					}
				},
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
				schedulertesting.RegisterFilterPlugin(Name, proxyNew),
				schedulertesting.RegisterScorePlugin(Name, proxyNew, 1),
				schedulertesting.RegisterReservePlugin(Name, proxyNew),
			}

			cs := kubefake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(cs, 0)

			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.nodeMetric.Name,
					},
				},
			}

			snapshot := newTestSharedLister(nil, nodes)
			fh, err := schedulertesting.NewFramework(registeredPlugins, "koord-scheduler",
				runtime.WithClientSet(cs),
				runtime.WithInformerFactory(informerFactory),
				runtime.WithSnapshotSharedLister(snapshot),
			)
			assert.Nil(t, err)

			p, err := proxyNew(&loadAwareSchedulingArgs, fh)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			_, err = koordClientSet.SloV1alpha1().NodeMetrics().Create(context.TODO(), tt.nodeMetric, metav1.CreateOptions{})
			assert.NoError(t, err)

			koordSharedInformerFactory.Start(context.TODO().Done())
			koordSharedInformerFactory.WaitForCacheSync(context.TODO().Done())

			cycleState := framework.NewCycleState()

			nodeInfo, err := snapshot.Get(tt.nodeMetric.Name)
			assert.NoError(t, err)
			assert.NotNil(t, nodeInfo)

			status := p.(*Plugin).Filter(context.TODO(), cycleState, &corev1.Pod{}, nodeInfo)
			assert.True(t, tt.wantStatus.Equal(status), "want status: %s, but got %s", tt.wantStatus.Message(), status.Message())
		})
	}
}

func TestFilterUsage(t *testing.T) {
	tests := []struct {
		name                  string
		usageThresholds       map[corev1.ResourceName]int64
		customUsageThresholds map[corev1.ResourceName]int64
		nodeMetric            *slov1alpha1.NodeMetric
		wantStatus            *framework.Status
	}{
		{
			name: "filter normal usage",
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("60"),
								corev1.ResourceMemory: resource.MustParse("256Gi"),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter exceed cpu usage",
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("70"),
								corev1.ResourceMemory: resource.MustParse("256Gi"),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, fmt.Sprintf(ErrReasonUsageExceedThreshold, corev1.ResourceCPU)),
		},
		{
			name: "filter exceed memory usage",
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("30"),
								corev1.ResourceMemory: resource.MustParse("500Gi"),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, fmt.Sprintf(ErrReasonUsageExceedThreshold, corev1.ResourceMemory)),
		},
		{
			name: "filter exceed memory usage by custom usage thresholds",
			customUsageThresholds: map[corev1.ResourceName]int64{
				corev1.ResourceMemory: 60,
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("30"),
								corev1.ResourceMemory: resource.MustParse("316Gi"),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, fmt.Sprintf(ErrReasonUsageExceedThreshold, corev1.ResourceMemory)),
		},
		{
			name: "disable filter exceed memory usage",
			usageThresholds: map[corev1.ResourceName]int64{
				corev1.ResourceMemory: 0,
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("30"),
								corev1.ResourceMemory: resource.MustParse("500Gi"),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v1beta2args v1beta2.LoadAwareSchedulingArgs
			v1beta2args.FilterExpiredNodeMetrics = pointer.Bool(false)
			if len(tt.usageThresholds) > 0 {
				v1beta2args.UsageThresholds = tt.usageThresholds
			}
			v1beta2.SetDefaults_LoadAwareSchedulingArgs(&v1beta2args)
			var loadAwareSchedulingArgs config.LoadAwareSchedulingArgs
			err := v1beta2.Convert_v1beta2_LoadAwareSchedulingArgs_To_config_LoadAwareSchedulingArgs(&v1beta2args, &loadAwareSchedulingArgs, nil)
			assert.NoError(t, err)

			loadAwareSchedulingPluginConfig := scheduledconfig.PluginConfig{
				Name: Name,
				Args: &loadAwareSchedulingArgs,
			}

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extendHandle := frameworkext.NewExtendedHandle(
				frameworkext.WithKoordinatorClientSet(koordClientSet),
				frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)
			proxyNew := frameworkext.PluginFactoryProxy(extendHandle, New)

			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				func(reg *runtime.Registry, profile *scheduledconfig.KubeSchedulerProfile) {
					profile.PluginConfig = []scheduledconfig.PluginConfig{
						loadAwareSchedulingPluginConfig,
					}
				},
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
				schedulertesting.RegisterFilterPlugin(Name, proxyNew),
				schedulertesting.RegisterScorePlugin(Name, proxyNew, 1),
				schedulertesting.RegisterReservePlugin(Name, proxyNew),
			}

			cs := kubefake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(cs, 0)

			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.nodeMetric.Name,
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("96"),
							corev1.ResourceMemory: resource.MustParse("512Gi"),
						},
					},
				},
			}

			if len(tt.customUsageThresholds) > 0 {
				data, err := json.Marshal(&extension.CustomUsageThresholds{UsageThresholds: tt.customUsageThresholds})
				if err != nil {
					t.Errorf("failed to marshal, err: %v", err)
				}
				node := nodes[0]
				if len(node.Annotations) == 0 {
					node.Annotations = map[string]string{}
				}
				node.Annotations[extension.AnnotationCustomUsageThresholds] = string(data)
			}

			snapshot := newTestSharedLister(nil, nodes)
			fh, err := schedulertesting.NewFramework(registeredPlugins, "koord-scheduler",
				runtime.WithClientSet(cs),
				runtime.WithInformerFactory(informerFactory),
				runtime.WithSnapshotSharedLister(snapshot),
			)
			assert.Nil(t, err)

			p, err := proxyNew(&loadAwareSchedulingArgs, fh)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			_, err = koordClientSet.SloV1alpha1().NodeMetrics().Create(context.TODO(), tt.nodeMetric, metav1.CreateOptions{})
			assert.NoError(t, err)

			koordSharedInformerFactory.Start(context.TODO().Done())
			koordSharedInformerFactory.WaitForCacheSync(context.TODO().Done())

			cycleState := framework.NewCycleState()

			nodeInfo, err := snapshot.Get(tt.nodeMetric.Name)
			assert.NoError(t, err)
			assert.NotNil(t, nodeInfo)

			status := p.(*Plugin).Filter(context.TODO(), cycleState, &corev1.Pod{}, nodeInfo)
			assert.True(t, tt.wantStatus.Equal(status), "want status: %s, but got %s", tt.wantStatus.Message(), status.Message())
		})
	}
}

func TestScore(t *testing.T) {
	tests := []struct {
		name        string
		pod         *corev1.Pod
		assignedPod []*podAssignInfo
		nodeMetric  *slov1alpha1.NodeMetric
		wantScore   int64
		wantStatus  *framework.Status
	}{
		{
			name: "score node with expired nodeMetric",
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now().Add(-180 * time.Second),
					},
				},
			},
			wantScore:  0,
			wantStatus: nil,
		},
		{
			name: "score empty node",
			pod: &corev1.Pod{
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
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
				},
			},
			wantScore:  90,
			wantStatus: nil,
		},
		{
			name: "score cert load node",
			pod: &corev1.Pod{
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
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("32"),
								corev1.ResourceMemory: resource.MustParse("10Gi"),
							},
						},
					},
				},
			},
			wantScore:  72,
			wantStatus: nil,
		},
		{
			name: "score cert load node with just assigned pod",
			pod: &corev1.Pod{
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
			},
			assignedPod: []*podAssignInfo{
				{
					timestamp: time.Now(),
					pod: &corev1.Pod{
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
					},
				},
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("32"),
								corev1.ResourceMemory: resource.MustParse("10Gi"),
							},
						},
					},
				},
			},
			wantScore:  63,
			wantStatus: nil,
		},
		{
			name: "score cert load node with just assigned pod where after updateTime",
			pod: &corev1.Pod{
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
			},
			assignedPod: []*podAssignInfo{
				{
					timestamp: time.Now(),
					pod: &corev1.Pod{
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
					},
				},
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now().Add(-10 * time.Second),
					},
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("32"),
								corev1.ResourceMemory: resource.MustParse("10Gi"),
							},
						},
					},
				},
			},
			wantScore:  63,
			wantStatus: nil,
		},
		{
			name: "score cert load node with just assigned pod where before updateTime",
			pod: &corev1.Pod{
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
			},
			assignedPod: []*podAssignInfo{
				{
					timestamp: time.Now().Add(-10 * time.Second),
					pod: &corev1.Pod{
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
					},
				},
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("32"),
								corev1.ResourceMemory: resource.MustParse("10Gi"),
							},
						},
					},
				},
			},
			wantScore:  63,
			wantStatus: nil,
		},
		{
			name: "score batch Pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(extension.PriorityBatchValueMin),
					Containers: []corev1.Container{
						{
							Name: "test-container",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("16000"),
									extension.BatchMemory: resource.MustParse("32Gi"),
								},
								Requests: corev1.ResourceList{
									extension.BatchCPU:    resource.MustParse("16000"),
									extension.BatchMemory: resource.MustParse("32Gi"),
								},
							},
						},
					},
				},
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
				},
			},
			wantScore:  90,
			wantStatus: nil,
		},
		{
			name: "score request less than limit",
			pod: &corev1.Pod{
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
									corev1.ResourceCPU:    resource.MustParse("8"),
									corev1.ResourceMemory: resource.MustParse("16Gi"),
								},
							},
						},
					},
				},
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
				},
			},
			wantScore:  88,
			wantStatus: nil,
		},
		{
			name: "score empty pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container",
						},
					},
				},
			},
			nodeMetric: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
				Spec: slov1alpha1.NodeMetricSpec{
					CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
						ReportIntervalSeconds: pointer.Int64(60),
					},
				},
				Status: slov1alpha1.NodeMetricStatus{
					UpdateTime: &metav1.Time{
						Time: time.Now(),
					},
				},
			},
			wantScore:  99,
			wantStatus: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v1beta2args v1beta2.LoadAwareSchedulingArgs
			v1beta2.SetDefaults_LoadAwareSchedulingArgs(&v1beta2args)
			var loadAwareSchedulingArgs config.LoadAwareSchedulingArgs
			err := v1beta2.Convert_v1beta2_LoadAwareSchedulingArgs_To_config_LoadAwareSchedulingArgs(&v1beta2args, &loadAwareSchedulingArgs, nil)
			assert.NoError(t, err)

			loadAwareSchedulingPluginConfig := scheduledconfig.PluginConfig{
				Name: Name,
				Args: &loadAwareSchedulingArgs,
			}

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extendHandle := frameworkext.NewExtendedHandle(
				frameworkext.WithKoordinatorClientSet(koordClientSet),
				frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)
			proxyNew := frameworkext.PluginFactoryProxy(extendHandle, New)

			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				func(reg *runtime.Registry, profile *scheduledconfig.KubeSchedulerProfile) {
					profile.PluginConfig = []scheduledconfig.PluginConfig{
						loadAwareSchedulingPluginConfig,
					}
				},
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
				schedulertesting.RegisterFilterPlugin(Name, proxyNew),
				schedulertesting.RegisterScorePlugin(Name, proxyNew, 1),
				schedulertesting.RegisterReservePlugin(Name, proxyNew),
			}

			cs := kubefake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(cs, 0)

			nodes := []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: tt.nodeMetric.Name,
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("96"),
							corev1.ResourceMemory: resource.MustParse("512Gi"),
						},
					},
				},
			}

			snapshot := newTestSharedLister(nil, nodes)
			fh, err := schedulertesting.NewFramework(registeredPlugins, "koord-scheduler",
				runtime.WithClientSet(cs),
				runtime.WithInformerFactory(informerFactory),
				runtime.WithSnapshotSharedLister(snapshot),
			)
			assert.Nil(t, err)

			p, err := proxyNew(&loadAwareSchedulingArgs, fh)
			assert.NotNil(t, p)
			assert.Nil(t, err)

			_, err = koordClientSet.SloV1alpha1().NodeMetrics().Create(context.TODO(), tt.nodeMetric, metav1.CreateOptions{})
			assert.NoError(t, err)

			koordSharedInformerFactory.Start(context.TODO().Done())
			koordSharedInformerFactory.WaitForCacheSync(context.TODO().Done())

			cycleState := framework.NewCycleState()

			nodeInfo, err := snapshot.Get(tt.nodeMetric.Name)
			assert.NoError(t, err)
			assert.NotNil(t, nodeInfo)

			assignCache := p.(*Plugin).podAssignCache
			for _, v := range tt.assignedPod {
				m := assignCache.podInfoItems[tt.nodeMetric.Name]
				if m == nil {
					m = map[types.UID]*podAssignInfo{}
					assignCache.podInfoItems[tt.nodeMetric.Name] = m
				}
				v.pod.UID = uuid.NewUUID()
				m[v.pod.UID] = v
			}

			score, status := p.(*Plugin).Score(context.TODO(), cycleState, tt.pod, tt.nodeMetric.Name)
			assert.Equal(t, tt.wantScore, score)
			assert.True(t, tt.wantStatus.Equal(status), "want status: %s, but got %s", tt.wantStatus.Message(), status.Message())
		})
	}
}
