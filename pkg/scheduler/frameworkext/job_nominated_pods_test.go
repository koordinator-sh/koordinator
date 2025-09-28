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

package frameworkext

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkfake "k8s.io/kubernetes/pkg/scheduler/framework/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
)

type FakeFitPlugin struct {
}

func (f *FakeFitPlugin) Filter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	if insufficientResources := noderesources.Fits(pod, nodeInfo); len(insufficientResources) != 0 {
		var reasons []string
		for _, insufficientResource := range insufficientResources {
			reasons = append(reasons, insufficientResource.Reason)
		}
		return framework.NewStatus(framework.Unschedulable, reasons...)
	}
	return nil
}
func (f *FakeFitPlugin) Name() string {
	return "FakeFitPlugin"
}

func Test_frameworkExtenderImpl_RunFilterPluginsWithNominatedIgnoreSameJob(t *testing.T) {
	tests := []struct {
		name                  string
		pod                   *corev1.Pod
		nominatedPods         []*corev1.Pod
		node                  *corev1.Node
		nominatedPodsToRemove sets.Set[string]
		want                  *framework.Status
	}{
		{
			name: "insufficient resource cause nominated pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-1",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("16"),
								},
							},
						},
					},
				},
			},
			nominatedPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nominated-pod-1",
						Namespace: "default",
						UID:       "nominated-pod-1",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
					},
					Status: corev1.PodStatus{
						NominatedNodeName: "test-node",
					},
				},
			},
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:  resource.MustParse("16"),
						corev1.ResourcePods: resource.MustParse("110"),
					},
				},
			},
			want: framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
		},
		{
			name: "ignore nominated pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-1",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("16"),
								},
							},
						},
					},
				},
			},
			nominatedPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nominated-pod-1",
						Namespace: "default",
						UID:       "nominated-pod-1",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
					},
					Status: corev1.PodStatus{
						NominatedNodeName: "test-node",
					},
				},
			},
			nominatedPodsToRemove: sets.New[string]("nominated-pod-1"),
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:  resource.MustParse("16"),
						corev1.ResourcePods: resource.MustParse("110"),
					},
				},
			},
			want: nil,
		},
		{
			name: "insufficient resource though ignore nominated pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-1",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("16"),
								},
							},
						},
					},
				},
			},
			nominatedPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nominated-pod-1",
						Namespace: "default",
						UID:       "nominated-pod-1",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
					},
					Status: corev1.PodStatus{
						NominatedNodeName: "test-node",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nominated-pod-2",
						Namespace: "default",
						UID:       "nominated-pod-2",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
					},
					Status: corev1.PodStatus{
						NominatedNodeName: "test-node",
					},
				},
			},
			nominatedPodsToRemove: sets.New[string]("nominated-pod-1"),
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:  resource.MustParse("16"),
						corev1.ResourcePods: resource.MustParse("110"),
					},
				},
			},
			want: framework.NewStatus(framework.Unschedulable, "Insufficient cpu").WithFailedPlugin("FakeFitPlugin"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, err := NewFrameworkExtenderFactory(
				WithServicesEngine(services.NewEngine(gin.New())),
				WithKoordinatorClientSet(koordClientSet),
				WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)
			assert.NoError(t, err)
			assert.NotNil(t, extenderFactory)
			assert.Equal(t, koordClientSet, extenderFactory.KoordinatorClientSet())
			assert.Equal(t, koordSharedInformerFactory, extenderFactory.KoordinatorSharedInformerFactory())
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
				schedulertesting.RegisterFilterPlugin("FakeFitPlugin", func(configuration runtime.Object, f framework.Handle) (framework.Plugin, error) {
					return &FakeFitPlugin{}, nil
				}),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)

			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(tt.node)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{NodeInfoLister: frameworkfake.NodeInfoLister{nodeInfo}}),
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
				frameworkruntime.WithPodNominator(NewFakePodNominator()),
			)
			ctx := context.TODO()
			logger := klog.FromContext(ctx)
			for i := range tt.nominatedPods {
				nominatedPodInfo, _ := framework.NewPodInfo(tt.nominatedPods[i])
				fh.AddNominatedPod(logger, nominatedPodInfo, &framework.NominatingInfo{
					NominatedNodeName: tt.nominatedPods[i].Status.NominatedNodeName,
					NominatingMode:    framework.ModeOverride,
				})
			}
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
			frameworkExtender.SetConfiguredPlugins(fh.ListPlugins())
			cycleState := framework.NewCycleState()
			if tt.nominatedPodsToRemove.Len() != 0 {
				MakeNominatedPodsOfTheSameJob(cycleState, tt.nominatedPodsToRemove.UnsortedList())
			}
			assert.Equal(t, tt.want, frameworkExtender.RunFilterPluginsWithNominatedPods(context.TODO(), cycleState, tt.pod, nodeInfo))
		})
	}
}
