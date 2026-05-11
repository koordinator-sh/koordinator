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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
)

type FakeFitPlugin struct {
}

func (f *FakeFitPlugin) Filter(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodeInfo fwktype.NodeInfo) *fwktype.Status {
	if insufficientResources := noderesources.Fits(pod, nodeInfo, nil, noderesources.ResourceRequestsOptions{}); len(insufficientResources) != 0 {
		var reasons []string
		for _, insufficientResource := range insufficientResources {
			reasons = append(reasons, insufficientResource.Reason)
		}
		return fwktype.NewStatus(fwktype.Unschedulable, reasons...)
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
		want                  *fwktype.Status
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
			want: fwktype.NewStatus(fwktype.Unschedulable, "Insufficient cpu").WithPlugin("FakeFitPlugin"),
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
			want: fwktype.NewStatus(fwktype.Unschedulable, "Insufficient cpu").WithPlugin("FakeFitPlugin"),
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
				schedulertesting.RegisterFilterPlugin("FakeFitPlugin", func(ctx context.Context, configuration runtime.Object, f fwktype.Handle) (fwktype.Plugin, error) {
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
				frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{nodeInfoLister: nodeInfoLister{nodeInfo}}),
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
				frameworkruntime.WithPodNominator(NewFakePodNominator()),
			)
			ctx := context.TODO()
			logger := klog.FromContext(ctx)
			for i := range tt.nominatedPods {
				nominatedPodInfo, _ := framework.NewPodInfo(tt.nominatedPods[i])
				fh.AddNominatedPod(logger, nominatedPodInfo, &fwktype.NominatingInfo{
					NominatedNodeName: tt.nominatedPods[i].Status.NominatedNodeName,
					NominatingMode:    fwktype.ModeOverride,
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

// makeSimplePod creates a pod with the given name, uid, priority and nominated node name.
func makeSimplePod(name string, uid types.UID, priority int32, nominatedNode string) *corev1.Pod {
	p := priority
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       uid,
		},
		Spec: corev1.PodSpec{
			Priority: &p,
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			NominatedNodeName: nominatedNode,
		},
	}
}

// makeSimpleNodeInfo creates a NodeInfo with a node of the given name.
func makeSimpleNodeInfo(nodeName string) fwktype.NodeInfo {
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
	})
	return nodeInfo
}

// buildSimpleFrameworkHandleWithNominated builds a minimal framework.Handle that has the given
// nominated pods registered via the fake PodNominator.
func buildSimpleFrameworkHandleWithNominated(t *testing.T, nodeInfo fwktype.NodeInfo, nominatedPods []*corev1.Pod) fwktype.Handle {
	t.Helper()

	fakeClient := kubefake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)

	nominator := NewFakePodNominator()
	logger := klog.Background()
	for _, pod := range nominatedPods {
		pi, err := framework.NewPodInfo(pod)
		assert.NoError(t, err)
		nominator.AddNominatedPod(logger, pi, &fwktype.NominatingInfo{
			NominatingMode:    fwktype.ModeOverride,
			NominatedNodeName: pod.Status.NominatedNodeName,
		})
	}

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{nodeInfoLister: nodeInfoLister{nodeInfo}}),
		frameworkruntime.WithClientSet(fakeClient),
		frameworkruntime.WithInformerFactory(sharedInformerFactory),
		frameworkruntime.WithPodNominator(nominator),
	)
	assert.NoError(t, err)
	return fh
}

func Test_addNominatedPods(t *testing.T) {
	const (
		highPriority = int32(200)
		midPriority  = int32(100)
		lowPriority  = int32(50)
	)
	const nodeName = "test-node"

	tests := []struct {
		name           string
		pod            *corev1.Pod
		nominatedPods  []*corev1.Pod
		podsOfSameJob  []string
		wantPodsAdded  bool
		wantAddedCount int // expected number of pods added to nodeInfo
		// wantCloned indicates whether stateOut/nodeInfoOut are expected to be clones.
		// True whenever pods are actually added to nodeInfo (i.e., podsAdded == true).
		wantCloned bool
	}{
		{
			name:           "no nominated pods returns false",
			pod:            makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nominatedPods:  nil,
			wantPodsAdded:  false,
			wantCloned:     false, // no nominated pods at all, early-return with originals
			wantAddedCount: 0,
		},
		{
			name:           "high priority nominated pod is added (priority >= current)",
			pod:            makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nominatedPods:  []*corev1.Pod{makeSimplePod("nominated-high", "uid-high", highPriority, nodeName)},
			wantPodsAdded:  true,
			wantCloned:     true,
			wantAddedCount: 1,
		},
		{
			name:           "equal priority nominated pod is added (priority >= current)",
			pod:            makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nominatedPods:  []*corev1.Pod{makeSimplePod("nominated-equal", "uid-equal", midPriority, nodeName)},
			wantPodsAdded:  true,
			wantCloned:     true,
			wantAddedCount: 1,
		},
		{
			// When no pods are added, stateOut/nodeInfoOut are the original objects (not clones).
			name:           "low priority nominated pod is not added (priority < current)",
			pod:            makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nominatedPods:  []*corev1.Pod{makeSimplePod("nominated-low", "uid-low", lowPriority, nodeName)},
			wantPodsAdded:  false,
			wantCloned:     false,
			wantAddedCount: 0,
		},
		{
			// When no pods are added, stateOut/nodeInfoOut are the original objects (not clones).
			name:           "same UID nominated pod is excluded",
			pod:            makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nominatedPods:  []*corev1.Pod{makeSimplePod("same-uid-pod", "uid-scheduling", highPriority, nodeName)},
			wantPodsAdded:  false,
			wantCloned:     false,
			wantAddedCount: 0,
		},
		{
			// When no pods are added, stateOut/nodeInfoOut are the original objects (not clones).
			name:           "sameJob nominated pod is excluded",
			pod:            makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nominatedPods:  []*corev1.Pod{makeSimplePod("nominated-job-pod", "uid-job-pod", highPriority, nodeName)},
			podsOfSameJob:  []string{"uid-job-pod"},
			wantPodsAdded:  false,
			wantCloned:     false,
			wantAddedCount: 0,
		},
		{
			name: "mixed nominated pods: only eligible ones are added",
			pod:  makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nominatedPods: []*corev1.Pod{
				makeSimplePod("nominated-high", "uid-high", highPriority, nodeName),    // should be added
				makeSimplePod("nominated-low", "uid-low", lowPriority, nodeName),       // should be excluded
				makeSimplePod("nominated-same-job", "uid-job", highPriority, nodeName), // should be excluded via sameJob
			},
			podsOfSameJob:  []string{"uid-job"},
			wantPodsAdded:  true,
			wantCloned:     true,
			wantAddedCount: 1, // only nominated-high
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeInfo := makeSimpleNodeInfo(nodeName)
			fh := buildSimpleFrameworkHandleWithNominated(t, nodeInfo, tt.nominatedPods)

			state := framework.NewCycleState()
			podsOfSameJob := sets.New[string](tt.podsOfSameJob...)

			podsAdded, stateOut, nodeInfoOut, err, addedPods := addNominatedPods(
				context.TODO(), fh, tt.pod, state, nodeInfo, podsOfSameJob,
			)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantPodsAdded, podsAdded, "podsAdded mismatch")
			_ = addedPods // addedPods is only populated when klog V(5) is enabled or NominatedNodeName matches

			if tt.wantCloned {
				// Clone is created whenever pods are actually added (podsAdded == true).
				assert.NotSame(t, state, stateOut, "stateOut should be a clone")
				assert.NotSame(t, nodeInfo, nodeInfoOut, "nodeInfoOut should be a clone")
			} else {
				// No eligible pods added: returns original state and nodeInfo.
				assert.Same(t, state, stateOut, "stateOut should be the original state")
				assert.Same(t, nodeInfo, nodeInfoOut, "nodeInfoOut should be the original nodeInfo")
			}
			// Verify the number of pods in the (possibly cloned) nodeInfo.
			assert.Equal(t, tt.wantAddedCount, len(nodeInfoOut.GetPods()), "number of pods in nodeInfoOut")
		})
	}
}

// buildExtenderWithCrossNominator creates a frameworkExtenderImpl with an optional CrossSchedulerPodNominator
// and the given native nominated pods pre-registered.
func buildExtenderWithCrossNominator(t *testing.T, nodeInfo fwktype.NodeInfo, nativeNominated []*corev1.Pod, crossNominator *CrossSchedulerPodNominator) *frameworkExtenderImpl {
	t.Helper()

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)

	opts := []Option{
		WithKoordinatorClientSet(koordClientSet),
		WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	}
	if crossNominator != nil {
		opts = append(opts, WithCrossSchedulerPodNominator(crossNominator))
	}

	extenderFactory, err := NewFrameworkExtenderFactory(opts...)
	assert.NoError(t, err)

	fakeClient := kubefake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{nodeInfoLister: nodeInfoLister{nodeInfo}}),
		frameworkruntime.WithClientSet(fakeClient),
		frameworkruntime.WithInformerFactory(sharedInformerFactory),
		frameworkruntime.WithPodNominator(NewFakePodNominator()),
	)
	assert.NoError(t, err)

	extender := extenderFactory.NewFrameworkExtender(fh)
	extender.SetConfiguredPlugins(fh.ListPlugins())
	impl := extender.(*frameworkExtenderImpl)

	// Register native nominated pods.
	logger := klog.Background()
	for _, pod := range nativeNominated {
		pi, perr := framework.NewPodInfo(pod)
		assert.NoError(t, perr)
		impl.AddNominatedPod(logger, pi, &fwktype.NominatingInfo{
			NominatingMode:    fwktype.ModeOverride,
			NominatedNodeName: pod.Status.NominatedNodeName,
		})
	}

	return impl
}

func Test_addMergedNominatedPods(t *testing.T) {
	const (
		highPriority = int32(200)
		midPriority  = int32(100)
		lowPriority  = int32(50)
	)
	const nodeName = "test-node"

	newCrossNominator := func(pods ...*corev1.Pod) *CrossSchedulerPodNominator {
		nominator := NewCrossSchedulerPodNominator()
		nominator.AddLocalProfileName("koord-scheduler")
		for _, pod := range pods {
			nominator.OnAdd(pod)
		}
		return nominator
	}

	tests := []struct {
		name            string
		pod             *corev1.Pod
		nativeNominated []*corev1.Pod
		crossNominator  *CrossSchedulerPodNominator
		podsOfSameJob   []string
		wantPodsAdded   bool
		wantAddedCount  int
		// wantCloned indicates whether stateOut/nodeInfoOut are expected to be clones.
		// True whenever pods are actually added to nodeInfo (i.e., podsAdded == true).
		wantCloned bool
	}{
		{
			name:           "no native and no cross-scheduler nominated pods returns false",
			pod:            makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			crossNominator: newCrossNominator(),
			wantPodsAdded:  false,
			wantCloned:     false, // both lists are empty: early-return with originals
			wantAddedCount: 0,
		},
		{
			name:            "native nominated pod (priority >= current) is added",
			pod:             makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nativeNominated: []*corev1.Pod{makeSimplePod("native-high", "uid-native-high", highPriority, nodeName)},
			crossNominator:  newCrossNominator(),
			wantPodsAdded:   true,
			wantCloned:      true,
			wantAddedCount:  1,
		},
		{
			name: "cross-scheduler nominated pod (priority > current) is added",
			pod:  makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			crossNominator: newCrossNominator(
				makePriorityPod("cross-high", "uid-cross-high", highPriority, "100m", "other-scheduler", nodeName, ""),
			),
			wantPodsAdded:  true,
			wantCloned:     true,
			wantAddedCount: 1,
		},
		{
			// When no pods are added (priority == current for cross-scheduler is not added),
			// stateOut/nodeInfoOut are the original objects (not clones).
			name: "cross-scheduler nominated pod (priority == current) is not added",
			pod:  makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			crossNominator: newCrossNominator(
				makePriorityPod("cross-equal", "uid-cross-equal", midPriority, "100m", "other-scheduler", nodeName, ""),
			),
			wantPodsAdded:  false,
			wantCloned:     false,
			wantAddedCount: 0,
		},
		{
			// When no pods are added (priority < current for cross-scheduler),
			// stateOut/nodeInfoOut are the original objects (not clones).
			name: "cross-scheduler nominated pod (priority < current) is not added",
			pod:  makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			crossNominator: newCrossNominator(
				makePriorityPod("cross-low", "uid-cross-low", lowPriority, "100m", "other-scheduler", nodeName, ""),
			),
			wantPodsAdded:  false,
			wantCloned:     false,
			wantAddedCount: 0,
		},
		{
			// When no pods are added (sameJob excludes all),
			// stateOut/nodeInfoOut are the original objects (not clones).
			name:            "sameJob excludes native nominated pod",
			pod:             makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nativeNominated: []*corev1.Pod{makeSimplePod("native-job-pod", "uid-job-pod", highPriority, nodeName)},
			crossNominator:  newCrossNominator(),
			podsOfSameJob:   []string{"uid-job-pod"},
			wantPodsAdded:   false,
			wantCloned:      false,
			wantAddedCount:  0,
		},
		{
			name: "mixed: native >= and cross >, each correctly filtered",
			pod:  makeSimplePod("scheduling-pod", "uid-scheduling", midPriority, ""),
			nativeNominated: []*corev1.Pod{
				makeSimplePod("native-high", "uid-native-high", highPriority, nodeName), // added (>=)
				makeSimplePod("native-low", "uid-native-low", lowPriority, nodeName),    // excluded (<)
				makeSimplePod("native-job", "uid-native-job", highPriority, nodeName),   // excluded (sameJob)
			},
			crossNominator: newCrossNominator(
				makePriorityPod("cross-high", "uid-cross-high", highPriority, "100m", "other-scheduler", nodeName, ""),  // added (>)
				makePriorityPod("cross-equal", "uid-cross-equal", midPriority, "100m", "other-scheduler", nodeName, ""), // excluded (==, not strictly >)
			),
			podsOfSameJob:  []string{"uid-native-job"},
			wantPodsAdded:  true,
			wantCloned:     true,
			wantAddedCount: 2, // native-high + cross-high
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeInfo := makeSimpleNodeInfo(nodeName)
			impl := buildExtenderWithCrossNominator(t, nodeInfo, tt.nativeNominated, tt.crossNominator)

			state := framework.NewCycleState()
			podsOfSameJob := sets.New[string](tt.podsOfSameJob...)

			podsAdded, stateOut, nodeInfoOut, err, addedPods := addMergedNominatedPods(
				context.TODO(), impl, tt.pod, state, nodeInfo, podsOfSameJob,
			)

			assert.NoError(t, err, "unexpected error")
			assert.Equal(t, tt.wantPodsAdded, podsAdded, "podsAdded mismatch")
			_ = addedPods // addedPods is only populated when klog V(5) is enabled or NominatedNodeName matches

			if tt.wantCloned {
				// Clone is returned whenever pods are actually added (podsAdded == true).
				assert.NotSame(t, state, stateOut, "stateOut should be a clone")
				assert.NotSame(t, nodeInfo, nodeInfoOut, "nodeInfoOut should be a clone")
			} else {
				// No eligible pods added: returns original state and nodeInfo.
				assert.Same(t, state, stateOut, "stateOut should be the original state")
				assert.Same(t, nodeInfo, nodeInfoOut, "nodeInfoOut should be the original nodeInfo")
			}
			assert.Equal(t, tt.wantAddedCount, len(nodeInfoOut.GetPods()), "number of pods in nodeInfoOut")
		})
	}
}

func TestMakeAndGetNominatedPodsOfTheSameJob(t *testing.T) {
	t.Run("set and get", func(t *testing.T) {
		state := framework.NewCycleState()
		uids := []string{"uid-1", "uid-2", "uid-3"}
		MakeNominatedPodsOfTheSameJob(state, uids)

		result := GetNominatedPodsOfTheSameJob(state)
		assert.NotNil(t, result, "should return non-nil set when data exists")
		assert.Equal(t, 3, result.Len())
		for _, uid := range uids {
			assert.True(t, result.Has(uid), "set should contain uid %s", uid)
		}
		assert.False(t, result.Has("uid-not-exist"))
	})

	t.Run("not set returns nil", func(t *testing.T) {
		state := framework.NewCycleState()
		result := GetNominatedPodsOfTheSameJob(state)
		assert.Nil(t, result, "should return nil when no data stored")
	})

	t.Run("empty list returns empty set", func(t *testing.T) {
		state := framework.NewCycleState()
		MakeNominatedPodsOfTheSameJob(state, []string{})

		result := GetNominatedPodsOfTheSameJob(state)
		assert.NotNil(t, result, "should return non-nil set even for empty list")
		assert.Equal(t, 0, result.Len())
	})
}
