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

package reservation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var _ framework.PreFilterPlugin = &fakePlugin{}
var _ framework.FilterPlugin = &fakePlugin{}

type fakePlugin struct {
	pod      *corev1.Pod
	nodeInfo *framework.NodeInfo
}

func (f *fakePlugin) Name() string { return "fakePlugin" }

func (f *fakePlugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	f.pod = pod
	return nil
}

func (f *fakePlugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func (f *fakePlugin) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	f.pod = pod
	f.nodeInfo = nodeInfo
	return nil
}

func TestPreFilterTransformer(t *testing.T) {
	reservePod := testGetReservePod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-0",
		},
	})
	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-1",
		},
	}
	testNodeName := "test-node-0"
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNodeName,
		},
	}
	rScheduled := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "reserve-pod-1",
			CreationTimestamp: metav1.Now(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-1",
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					Object: &corev1.ObjectReference{
						Name: "test-pod-1",
					},
				},
			},
			TTL: &metav1.Duration{Duration: 30 * time.Minute},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: testNodeName,
		},
	}

	tests := []struct {
		name                       string
		sharedLister               *fakeSharedLister
		koordSharedInformerFactory *fakeKoordinatorSharedInformerFactory
		reservation                *schedulingv1alpha1.Reservation
		pod                        *corev1.Pod
		wantPod                    *corev1.Pod
		wantState                  *stateData
		want1                      bool
	}{
		{
			name:      "skip for reserve pod",
			pod:       reservePod,
			wantPod:   reservePod,
			wantState: nil,
			want1:     true,
		},
		{
			name:         "failed to list nodes",
			sharedLister: newFakeSharedLister(nil, nil, true),
			pod:          normalPod,
			wantPod:      normalPod,
			wantState:    nil,
			want1:        true,
		},
		{
			name:         "get skip state",
			sharedLister: newFakeSharedLister(nil, nil, false),
			pod:          normalPod,
			wantPod:      normalPod,
			wantState: &stateData{
				skip:               true,
				matchedCache:       newAvailableCache(),
				allocatedResources: map[string]corev1.ResourceList{},
			},
			want1: true,
		},
		{
			name:         "get matched state",
			sharedLister: newFakeSharedLister([]*corev1.Pod{reservationutil.NewReservePod(rScheduled)}, []*corev1.Node{testNode}, false),
			reservation:  rScheduled,
			pod:          normalPod,
			wantPod:      normalPod,
			wantState: &stateData{
				skip:               false,
				matchedCache:       newAvailableCache(rScheduled),
				allocatedResources: map[string]corev1.ResourceList{},
			},
			want1: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			koordClientSet := koordfake.NewSimpleClientset()
			if tt.reservation != nil {
				_, err := koordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), tt.reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := frameworkext.NewFrameworkExtenderFactory(
				frameworkext.WithKoordinatorClientSet(koordClientSet),
				frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)
			proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, New)

			fakePl := &fakePlugin{}
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				func(reg *frameworkruntime.Registry, profile *config.KubeSchedulerProfile) {
					profile.PluginConfig = append(profile.PluginConfig, config.PluginConfig{
						Name: Name,
						Args: &schedulerconfig.ReservationArgs{},
					})
				},
				schedulertesting.RegisterPreFilterPlugin(fakePl.Name(), func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return fakePl, nil
				}),
				schedulertesting.RegisterPreFilterPlugin(Name, proxyNew),
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}

			cs := kubefake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(cs, 0)
			sharedLister := tt.sharedLister
			if sharedLister == nil {
				sharedLister = newFakeSharedLister(nil, nil, false)
			}
			fh, err := schedulertesting.NewFramework(registeredPlugins, "koord-scheduler",
				frameworkruntime.WithClientSet(cs),
				frameworkruntime.WithInformerFactory(informerFactory),
				frameworkruntime.WithSnapshotSharedLister(sharedLister),
			)
			assert.NoError(t, err)
			assert.NotNil(t, fh)

			informerFactory.Start(nil)
			koordSharedInformerFactory.Start(nil)
			informerFactory.WaitForCacheSync(nil)
			koordSharedInformerFactory.WaitForCacheSync(nil)

			extender := extenderFactory.GetExtender("koord-scheduler")
			assert.NotNil(t, extender)

			cycleState := framework.NewCycleState()
			status := extender.RunPreFilterPlugins(context.TODO(), cycleState, tt.pod)
			assert.Equal(t, tt.want1, status.IsSuccess())
			assert.Equal(t, tt.wantPod, fakePl.pod)
			assert.Equal(t, tt.wantState, getPreFilterState(cycleState))
		})
	}
}

type fakePodNominator struct{}

func (f *fakePodNominator) AddNominatedPod(pod *framework.PodInfo, nodeName string) {}

func (f *fakePodNominator) DeleteNominatedPodIfExists(pod *corev1.Pod)                           {}
func (f *fakePodNominator) UpdateNominatedPod(oldPod *corev1.Pod, newPodInfo *framework.PodInfo) {}
func (f *fakePodNominator) NominatedPodsForNode(nodeName string) []*framework.PodInfo {
	return nil
}

func TestFilterTransformer(t *testing.T) {
	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-1",
		},
	}
	testNodeName := "test-node-0"
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNodeName,
		},
	}
	testNodeInfo := framework.NewNodeInfo()
	testNodeInfo.SetNode(testNode)
	testNodeInfo1 := framework.NewNodeInfo()
	testNodeInfo1.SetNode(testNode)
	testNodeInfo1.Requested = framework.NewResource(corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"),
	})
	rScheduled := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "reserve-pod-1",
			CreationTimestamp: metav1.Now(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-1",
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					Object: &corev1.ObjectReference{
						Name: "test-pod-1",
					},
				},
			},
			TTL: &metav1.Duration{Duration: 30 * time.Minute},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: testNodeName,
			Allocated: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1"),
			},
		},
	}
	stateNoMatched := framework.NewCycleState()
	stateNoMatched.Write(preFilterStateKey, &stateData{
		skip:         true,
		matchedCache: newAvailableCache(),
	})
	stateNoMatchedButHasAllocated := framework.NewCycleState()
	stateNoMatchedButHasAllocated.Write(preFilterStateKey, &stateData{
		skip:         false,
		matchedCache: newAvailableCache(),
		allocatedResources: map[string]corev1.ResourceList{
			testNodeName: {
				corev1.ResourceCPU: resource.MustParse("1"),
			},
		},
	})
	stateMatched := framework.NewCycleState()
	stateMatched.Write(preFilterStateKey, &stateData{
		matchedCache: newAvailableCache(rScheduled),
	})
	type args struct {
		cycleState *framework.CycleState
		pod        *corev1.Pod
		nodeInfo   *framework.NodeInfo
	}
	tests := []struct {
		name      string
		args      args
		expectCPU int64
	}{
		{
			name: "no prefilter state, maybe no reservation matched",
			args: args{
				cycleState: framework.NewCycleState(),
				pod:        normalPod,
				nodeInfo:   testNodeInfo,
			},
			expectCPU: 0,
		},
		{
			name: "no reservation matched on the node",
			args: args{
				cycleState: stateNoMatched,
				pod:        normalPod,
				nodeInfo:   testNodeInfo,
			},
			expectCPU: 0,
		},
		{
			name: "no reservation matched on the node, but there are allocated reservation",
			args: args{
				cycleState: stateNoMatchedButHasAllocated,
				pod:        normalPod,
				nodeInfo:   testNodeInfo1,
			},
			expectCPU: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := frameworkext.NewFrameworkExtenderFactory(
				frameworkext.WithKoordinatorClientSet(koordClientSet),
				frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)
			proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, New)

			fakePl := &fakePlugin{}
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				func(reg *frameworkruntime.Registry, profile *config.KubeSchedulerProfile) {
					profile.PluginConfig = append(profile.PluginConfig, config.PluginConfig{
						Name: Name,
						Args: &schedulerconfig.ReservationArgs{},
					})
				},
				schedulertesting.RegisterFilterPlugin(fakePl.Name(), func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return fakePl, nil
				}),
				schedulertesting.RegisterPreFilterPlugin(Name, proxyNew),
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}

			cs := kubefake.NewSimpleClientset()
			informerFactory := informers.NewSharedInformerFactory(cs, 0)
			fh, err := schedulertesting.NewFramework(registeredPlugins, "koord-scheduler",
				frameworkruntime.WithClientSet(cs),
				frameworkruntime.WithInformerFactory(informerFactory),
				frameworkruntime.WithSnapshotSharedLister(newFakeSharedLister(nil, nil, false)),
				frameworkruntime.WithPodNominator(&fakePodNominator{}),
			)
			assert.NoError(t, err)
			assert.NotNil(t, fh)

			informerFactory.Start(nil)
			koordSharedInformerFactory.Start(nil)
			informerFactory.WaitForCacheSync(nil)
			koordSharedInformerFactory.WaitForCacheSync(nil)

			extender := extenderFactory.GetExtender("koord-scheduler")
			assert.NotNil(t, extender)

			cycleState := tt.args.cycleState
			status := extender.RunFilterPluginsWithNominatedPods(context.TODO(), cycleState, tt.args.pod, tt.args.nodeInfo)
			assert.True(t, status.IsSuccess())
			assert.Equal(t, tt.expectCPU*1000, fakePl.nodeInfo.Requested.MilliCPU)
		})
	}
}

func Test_preparePreFilterNodeInfo(t *testing.T) {
	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "claim-with-rwop",
						},
					},
				},
			},
		},
	}
	readWriteOncePodPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "claim-with-rwop",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOncePod},
		},
	}
	testNodeName := "test-node-0"
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNodeName,
		},
	}
	testNodeInfo := framework.NewNodeInfo()
	testNodeInfo.SetNode(testNode)
	testNodeInfo.PodsWithRequiredAntiAffinity = []*framework.PodInfo{
		framework.NewPodInfo(normalPod),
	}
	testNodeInfo.PVCRefCounts["default/claim-with-rwop"] = 1
	rScheduled := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-1",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-1",
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "claim-with-rwop",
								},
							},
						},
					},
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					Object: &corev1.ObjectReference{
						Name: "test-pod-1",
					},
				},
			},
			TTL: &metav1.Duration{Duration: 30 * time.Minute},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: testNodeName,
		},
	}
	matchedCache := newAvailableCache(rScheduled)

	fh := &fakeExtendedHandle{
		informerFactory: informers.NewSharedInformerFactory(
			kubefake.NewSimpleClientset(
				readWriteOncePodPVC),
			0),
	}
	fh.informerFactory.Core().V1().PersistentVolumeClaims().Informer().GetIndexer().Add(readWriteOncePodPVC)
	t.Run("test not panic", func(t *testing.T) {
		preparePreFilterNodeInfo(fh, testNodeInfo, normalPod, matchedCache)
		for _, podInfo := range testNodeInfo.PodsWithRequiredAntiAffinity {
			assert.Nil(t, podInfo.RequiredAntiAffinityTerms)
		}
		assert.Zero(t, testNodeInfo.PVCRefCounts["default/claim-with-rwop"])
	})
}

func Test_preparePreFilterPod(t *testing.T) {
	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-1",
		},
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{},
					},
				},
			},
		},
	}
	t.Run("test not panic", func(t *testing.T) {
		got := preparePreFilterPod(normalPod)
		assert.NotEqual(t, normalPod, got)
		assert.Nil(t, got.Spec.Affinity.PodAntiAffinity)
		assert.Nil(t, got.Spec.TopologySpreadConstraints)
	})
}
