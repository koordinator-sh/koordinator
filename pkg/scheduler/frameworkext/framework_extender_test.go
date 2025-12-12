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
	"errors"
	"fmt"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkfake "k8s.io/kubernetes/pkg/scheduler/framework/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var (
	_ framework.PreFilterPlugin = &TestTransformer{}
	_ framework.FilterPlugin    = &TestTransformer{}
	_ framework.ScorePlugin     = &TestTransformer{}

	_ FindOneNodePluginProvider = &TestTransformer{}

	_ PreFilterTransformer  = &TestTransformer{}
	_ FilterTransformer     = &TestTransformer{}
	_ ScoreTransformer      = &TestTransformer{}
	_ PostFilterTransformer = &TestTransformer{}
)

type TestTransformer struct {
	name  string
	index int
}

func (h *TestTransformer) Name() string {
	if h.name != "" {
		return h.name
	}
	return "TestTransformer"
}

func (h *TestTransformer) BeforePreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool, *framework.Status) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("BeforePreFilter-%d", h.index)] = fmt.Sprintf("%d", h.index)
	return pod, true, nil
}

func (h *TestTransformer) PreFilter(ctx context.Context, state *framework.CycleState, p *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	return nil, nil
}

func (h *TestTransformer) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func (h *TestTransformer) AfterPreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, preFilterResult *framework.PreFilterResult) *framework.Status {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("AfterPreFilter-%d", h.index)] = fmt.Sprintf("%d", h.index)
	return nil
}

func (h *TestTransformer) BeforeFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) (*corev1.Pod, *framework.NodeInfo, bool, *framework.Status) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("BeforeFilter-%d", h.index)] = fmt.Sprintf("%d", h.index)
	return pod, nodeInfo, true, nil
}

func (h *TestTransformer) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	return nil
}

func (h *TestTransformer) FindOneNodePlugin() FindOneNodePlugin {
	return h
}

func (h *TestTransformer) FindOneNode(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, result *framework.PreFilterResult) (string, *framework.Status) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations["FindOneNode"] = "FindOneNode"
	return "", framework.NewStatus(framework.Skip)

}

func (h *TestTransformer) BeforeScore(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodes []*corev1.Node) (*corev1.Pod, []*corev1.Node, bool, *framework.Status) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("BeforeScore-%d", h.index)] = fmt.Sprintf("%d", h.index)
	return pod, nodes, true, nil
}

func (h *TestTransformer) Score(ctx context.Context, state *framework.CycleState, p *corev1.Pod, nodeName string) (int64, *framework.Status) {
	return 0, nil
}

func (h *TestTransformer) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func (h *TestTransformer) AfterPostFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, filteredNodeStatusMap framework.NodeToStatusMap) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("AfterPostFilter-%d", h.index)] = fmt.Sprintf("%d", h.index)
}

// PreBind implements the standard PreBind plugin interface
func (h *TestTransformer) PreBind(ctx context.Context, state *framework.CycleState, p *corev1.Pod, nodeName string) *framework.Status {
	return nil
}

type testPreBindReservationState struct {
	reservation *schedulingv1alpha1.Reservation
}

func (t *testPreBindReservationState) Clone() framework.StateData {
	return t
}

func (h *TestTransformer) PreBindReservation(ctx context.Context, cycleState *framework.CycleState, reservation *schedulingv1alpha1.Reservation, nodeName string) *framework.Status {
	if reservation.Annotations == nil {
		reservation.Annotations = map[string]string{}
	}
	reservation.Annotations[fmt.Sprintf("PreBindReservation-%d", h.index)] = fmt.Sprintf("%d", h.index)
	cycleState.Write("test-preBind-reservation", &testPreBindReservationState{reservation: reservation})
	return nil
}

type fakePreBindPlugin struct {
	name string
	err  error

	skipApplyPatch    bool
	appendAnnotations map[string]string
	modifiedObj       metav1.Object
}

func (f *fakePreBindPlugin) Name() string {
	if f.name != "" {
		return f.name
	}
	return "fakePreBindPlugin"
}

func (f *fakePreBindPlugin) PreBind(ctx context.Context, state *framework.CycleState, p *corev1.Pod, nodeName string) *framework.Status {
	if f.err != nil {
		return framework.AsStatus(f.err)
	}
	return nil
}

func (f *fakePreBindPlugin) ApplyPatch(ctx context.Context, cycleState *framework.CycleState, originalObj, modifiedObj metav1.Object) *framework.Status {
	if f.skipApplyPatch {
		return framework.NewStatus(framework.Skip)
	}
	if f.err != nil {
		return framework.AsStatus(f.err)
	}
	if f.appendAnnotations != nil {
		annotations := modifiedObj.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		for k, v := range f.appendAnnotations {
			annotations[k] = v
		}
		modifiedObj.SetAnnotations(annotations)
	}
	f.modifiedObj = modifiedObj
	return nil
}

type fakeNodeInfoLister struct {
	frameworkfake.NodeInfoLister
}

func (c fakeNodeInfoLister) NodeInfos() framework.NodeInfoLister {
	return c
}

func (c fakeNodeInfoLister) StorageInfos() framework.StorageInfoLister {
	return c
}

func (c fakeNodeInfoLister) IsPVCUsedByPods(key string) bool {
	return false
}

func Test_frameworkExtenderImpl_RunPreFilterPlugins(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want *framework.Status
	}{
		{
			name: "normal RunPreFilterPlugins",
			pod:  &corev1.Pod{},
			want: nil,
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
				schedulertesting.RegisterPreFilterPlugin("T1", PluginFactoryProxy(extenderFactory, func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &TestTransformer{name: "T1", index: 1}, nil
				})),
				schedulertesting.RegisterPreFilterPlugin("T2", PluginFactoryProxy(extenderFactory, func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &TestTransformer{name: "T2", index: 2}, nil
				})),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{NodeInfoLister: frameworkfake.NodeInfoLister{}}),
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
			frameworkExtender.SetConfiguredPlugins(fh.ListPlugins())
			_, status := frameworkExtender.RunPreFilterPlugins(context.TODO(), framework.NewCycleState(), tt.pod)
			assert.Equal(t, tt.want, status)
			expectedAnnotations := map[string]string{
				"BeforePreFilter-1": "1",
				"AfterPreFilter-1":  "1",
				"BeforePreFilter-2": "2",
				"AfterPreFilter-2":  "2",
				"FindOneNode":       "FindOneNode",
			}
			assert.Equal(t, expectedAnnotations, tt.pod.Annotations)
		})
	}
}

func Test_frameworkExtenderImpl_RunFilterPluginsWithNominatedPods(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		nodeInfo *framework.NodeInfo
		want     *framework.Status
	}{
		{
			name:     "normal RunFilterPluginsWithNominatedPods",
			pod:      &corev1.Pod{},
			nodeInfo: framework.NewNodeInfo(),
			want:     nil,
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
				schedulertesting.RegisterFilterPlugin("T1", PluginFactoryProxy(extenderFactory, func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &TestTransformer{name: "T1", index: 1}, nil
				})),
				schedulertesting.RegisterFilterPlugin("T2", PluginFactoryProxy(extenderFactory, func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &TestTransformer{name: "T2", index: 2}, nil
				})),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{NodeInfoLister: frameworkfake.NodeInfoLister{}}),
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
				frameworkruntime.WithPodNominator(NewFakePodNominator()),
			)
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
			frameworkExtender.SetConfiguredPlugins(fh.ListPlugins())
			tt.nodeInfo.SetNode(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
			})
			assert.Equal(t, tt.want, frameworkExtender.RunFilterPluginsWithNominatedPods(context.TODO(), framework.NewCycleState(), tt.pod, tt.nodeInfo))
			assert.Len(t, tt.pod.Annotations, 2)
			expectedAnnotations := map[string]string{
				"BeforeFilter-1": "1",
				"BeforeFilter-2": "2",
			}
			assert.Equal(t, expectedAnnotations, tt.pod.Annotations)
		})
	}
}

func Test_frameworkExtenderImpl_RunScorePlugins(t *testing.T) {
	tests := []struct {
		name       string
		pod        *corev1.Pod
		nodes      []*corev1.Node
		wantScore  []framework.NodePluginScores
		wantStatus *framework.Status
	}{
		{
			name:  "normal RunScorePlugins",
			pod:   &corev1.Pod{},
			nodes: []*corev1.Node{{}},
			wantScore: []framework.NodePluginScores{
				{
					Name: "",
					Scores: []framework.PluginScore{
						{
							Name:  "T1",
							Score: 0,
						},
						{
							Name:  "T2",
							Score: 0,
						},
					},
				},
			},
			wantStatus: nil,
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
				schedulertesting.RegisterScorePlugin("T1", PluginFactoryProxy(extenderFactory, func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &TestTransformer{name: "T1", index: 1}, nil
				}), 1),
				schedulertesting.RegisterScorePlugin("T2", PluginFactoryProxy(extenderFactory, func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &TestTransformer{name: "T2", index: 2}, nil
				}), 1),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
			frameworkExtender.SetConfiguredPlugins(fh.ListPlugins())
			score, status := frameworkExtender.RunScorePlugins(context.TODO(), framework.NewCycleState(), tt.pod, tt.nodes)
			assert.Equal(t, tt.wantScore, score)
			assert.Equal(t, tt.wantStatus, status)
			expectedAnnotations := map[string]string{
				"BeforeScore-1": "1",
				"BeforeScore-2": "2",
			}
			assert.Equal(t, expectedAnnotations, tt.pod.Annotations)
		})
	}
}

type fakeMetricsRecorder struct{}

func (f *fakeMetricsRecorder) ObservePluginDurationAsync(extensionPoint, plugin, status string, duration float64) {
}

func TestPreBind(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fake-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					SchedulerName: "koord-scheduler",
				},
			},
		},
	}
	tests := []struct {
		name            string
		pod             *corev1.Pod
		wantAnnotations map[string]string
		wantStatus      bool
	}{
		{
			name: "preBind reservation",
			pod:  reservationutil.NewReservePod(reservation),
			wantAnnotations: map[string]string{
				"PreBindReservation-1": "1",
				"PreBindReservation-2": "2",
			},
			wantStatus: true,
		},
		{
			name:            "preBind normal pod",
			pod:             &corev1.Pod{},
			wantAnnotations: nil,
			wantStatus:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := NewFrameworkExtenderFactory(
				WithKoordinatorClientSet(koordClientSet),
				WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)

			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
				schedulertesting.RegisterPreBindPlugin("fakePreBindPlugin", func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &fakePreBindPlugin{err: errors.New("failed")}, nil
				}),
				schedulertesting.RegisterPreBindPlugin("TestTransformer-1", PluginFactoryProxy(extenderFactory, func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &TestTransformer{name: "TestTransformer-1", index: 1}, nil
				})),
				schedulertesting.RegisterPreBindPlugin("TestTransformer-2", PluginFactoryProxy(extenderFactory, func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &TestTransformer{name: "TestTransformer-2", index: 2}, nil
				})),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)

			_, err = koordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation.DeepCopy(), metav1.CreateOptions{})
			assert.NoError(t, err)
			_ = koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Lister()
			koordSharedInformerFactory.Start(nil)
			koordSharedInformerFactory.WaitForCacheSync(nil)

			extender := extenderFactory.NewFrameworkExtender(fh)
			extender.SetConfiguredPlugins(fh.ListPlugins())
			impl := extender.(*frameworkExtenderImpl)

			impl.SharedInformerFactory().Start(nil)
			impl.KoordinatorSharedInformerFactory().Start(nil)
			impl.SharedInformerFactory().WaitForCacheSync(nil)
			impl.KoordinatorSharedInformerFactory().WaitForCacheSync(nil)

			cycleState := framework.NewCycleState()
			status := extender.RunPreBindPlugins(context.TODO(), cycleState, tt.pod, "test-node-1")
			assert.Equal(t, tt.wantStatus, status.IsSuccess(), status.Message())
			if status.IsSuccess() {
				s, err := cycleState.Read("test-preBind-reservation")
				assert.NoError(t, err)
				assert.Equal(t, tt.wantAnnotations, s.(*testPreBindReservationState).reservation.Annotations)
			}
		})
	}
}

func TestPreBindExtensionOrder(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       "xxx",
		},
		Spec: corev1.PodSpec{
			SchedulerName: "koord-scheduler",
		},
	}
	preBindA := &fakePreBindPlugin{name: "fakePreBindPluginA", skipApplyPatch: true}
	preBindB := &fakePreBindPlugin{name: "fakePreBindPluginB", appendAnnotations: map[string]string{"test": "2"}}
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterPreBindPlugin(preBindA.Name(), func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
			return preBindA, nil
		}),
		schedulertesting.RegisterPreBindPlugin(preBindB.Name(), func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
			return preBindB, nil
		}),
	}
	fakeClient := kubefake.NewSimpleClientset(pod)
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		frameworkruntime.WithClientSet(fakeClient),
		frameworkruntime.WithInformerFactory(sharedInformerFactory),
	)
	assert.NoError(t, err)

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, _ := NewFrameworkExtenderFactory(
		WithKoordinatorClientSet(koordClientSet),
		WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)

	extender := NewFrameworkExtender(extenderFactory, fh)
	extender.SetConfiguredPlugins(fh.ListPlugins())
	impl := extender.(*frameworkExtenderImpl)
	impl.updatePlugins(preBindA)
	impl.updatePlugins(preBindB)

	impl.SharedInformerFactory().Start(nil)
	impl.KoordinatorSharedInformerFactory().Start(nil)
	impl.SharedInformerFactory().WaitForCacheSync(nil)
	impl.KoordinatorSharedInformerFactory().WaitForCacheSync(nil)

	got, err := extender.ClientSet().CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, got)
	got, err = impl.podLister.Pods(pod.Namespace).Get(pod.Name)
	assert.NoError(t, err)
	assert.NotNil(t, got)

	cycleState := framework.NewCycleState()
	status := extender.RunPreBindPlugins(context.TODO(), cycleState, pod, "test-node-1")
	assert.True(t, status.IsSuccess(), status.Message())
	assert.Nil(t, preBindA.modifiedObj)
	assert.Equal(t, map[string]string{"test": "2"}, preBindB.modifiedObj.GetAnnotations())
}

const fakeReservationRestoreStateKey = "fakeReservationRestoreState"

type fakeReservationRestoreStateData struct {
	m NodeReservationRestoreStates
}

func (s *fakeReservationRestoreStateData) Clone() framework.StateData {
	return s
}

type fakeNodeReservationRestoreStateData struct {
	matched   []*ReservationInfo
	unmatched []*ReservationInfo
}

type fakeReservationRestorePlugin struct {
	restoreReservationErr      error
	finalRestoreReservationErr error
}

func (f *fakeReservationRestorePlugin) Name() string { return "fakeReservationRestorePlugin" }

func (f *fakeReservationRestorePlugin) PreRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	cycleState.Write(fakeReservationRestoreStateKey, &fakeReservationRestoreStateData{})
	return nil
}

func (f *fakeReservationRestorePlugin) RestoreReservation(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, matched []*ReservationInfo, unmatched []*ReservationInfo, nodeInfo *framework.NodeInfo) (interface{}, *framework.Status) {
	if f.restoreReservationErr != nil {
		return nil, framework.AsStatus(f.restoreReservationErr)
	}
	return &fakeNodeReservationRestoreStateData{
		matched:   matched,
		unmatched: unmatched,
	}, nil
}

func (f *fakeReservationRestorePlugin) FinalRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, states NodeReservationRestoreStates) *framework.Status {
	if f.finalRestoreReservationErr != nil {
		return framework.AsStatus(f.finalRestoreReservationErr)
	}
	val, err := cycleState.Read(fakeReservationRestoreStateKey)
	if err != nil {
		return framework.AsStatus(err)
	}
	val.(*fakeReservationRestoreStateData).m = states
	return nil
}

func TestReservationRestorePlugin(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fake-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{},
		},
	}
	tests := []struct {
		name                   string
		reservation            *schedulingv1alpha1.Reservation
		removeReservationErr   error
		addPodInReservationErr error
		wantReservation        *schedulingv1alpha1.Reservation
		wantStatus1            bool
		wantStatus2            bool
	}{
		{
			name:            "store reservation and pods",
			reservation:     reservation,
			wantReservation: reservation,
			wantStatus1:     true,
			wantStatus2:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := NewFrameworkExtenderFactory(
				WithKoordinatorClientSet(koordClientSet),
				WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)

			extender := NewFrameworkExtender(extenderFactory, fh)
			pl := &fakeReservationRestorePlugin{
				restoreReservationErr:      tt.removeReservationErr,
				finalRestoreReservationErr: tt.addPodInReservationErr,
			}
			impl := extender.(*frameworkExtenderImpl)
			impl.updatePlugins(pl)

			cycleState := framework.NewCycleState()

			rInfo := NewReservationInfo(tt.reservation)

			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
			})

			status := extender.RunReservationExtensionPreRestoreReservation(context.TODO(), cycleState, &corev1.Pod{})
			assert.True(t, status.IsSuccess())

			pluginToRestoreState, status := extender.RunReservationExtensionRestoreReservation(context.TODO(), cycleState, &corev1.Pod{}, []*ReservationInfo{rInfo}, nil, nodeInfo)
			assert.Equal(t, tt.wantStatus1, status.IsSuccess())
			assert.NotNil(t, pluginToRestoreState)

			pluginToNodeReservationRestoreState := PluginToNodeReservationRestoreStates{}
			for pluginName, pluginState := range pluginToRestoreState {
				if pluginState == nil {
					continue
				}
				nodeRestoreStates := pluginToNodeReservationRestoreState[pluginName]
				if nodeRestoreStates == nil {
					nodeRestoreStates = NodeReservationRestoreStates{}
					pluginToNodeReservationRestoreState[pluginName] = nodeRestoreStates
				}
				nodeRestoreStates["test-node-1"] = pluginState
			}

			// TODO: remove deprecated methods
			status = extender.RunReservationExtensionFinalRestoreReservation(context.TODO(), cycleState, &corev1.Pod{}, pluginToNodeReservationRestoreState)
			assert.Equal(t, tt.wantStatus2, status.IsSuccess())
			val, err := cycleState.Read(fakeReservationRestoreStateKey)
			assert.NoError(t, err)
			stateData := val.(*fakeReservationRestoreStateData)
			assert.Equal(t, tt.wantReservation, stateData.m["test-node-1"].(*fakeNodeReservationRestoreStateData).matched[0].Reservation)
		})
	}
}

type fakeNodeReservationPreAllocationRestoreStateData struct {
	preAllocatable []*corev1.Pod
}

type fakeReservationPreAllocationRestorePlugin struct {
	restoreErr    error
	preRestoreErr error
}

var _ ReservationPreAllocationRestorePlugin = &fakeReservationPreAllocationRestorePlugin{}

func (f *fakeReservationPreAllocationRestorePlugin) Name() string {
	return "fakeReservationPreAllocationRestorePlugin"
}

func (f *fakeReservationPreAllocationRestorePlugin) PreRestoreReservationPreAllocation(ctx context.Context, cycleState *framework.CycleState, r *ReservationInfo) *framework.Status {
	if f.preRestoreErr != nil {
		return framework.AsStatus(f.preRestoreErr)
	}
	cycleState.Write(fakeReservationRestoreStateKey, &fakeReservationRestoreStateData{})
	return nil
}

func (f *fakeReservationPreAllocationRestorePlugin) RestoreReservationPreAllocation(ctx context.Context, cycleState *framework.CycleState, r *ReservationInfo, preAllocatable []*corev1.Pod, nodeInfo *framework.NodeInfo) (interface{}, *framework.Status) {
	if f.restoreErr != nil {
		return nil, framework.AsStatus(f.restoreErr)
	}
	return &fakeNodeReservationPreAllocationRestoreStateData{
		preAllocatable: preAllocatable,
	}, nil
}

func TestRunReservationExtensionPreRestoreReservationPreAllocation(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fake-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{},
		},
	}
	rInfo := NewReservationInfo(reservation)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fake-pod",
		},
	}
	tests := []struct {
		name               string
		rInfo              *ReservationInfo
		preAllocatable     []*corev1.Pod
		preRestoreErr      error
		restoreErr         error
		wantPreAllocatable []*corev1.Pod
		wantStatus1        bool
		wantStatus2        bool
	}{
		{
			name:               "store successfully",
			rInfo:              rInfo,
			preAllocatable:     []*corev1.Pod{pod},
			wantPreAllocatable: []*corev1.Pod{pod},
			wantStatus1:        true,
			wantStatus2:        true,
		},
		{
			name:               "pre restore err",
			rInfo:              rInfo,
			preAllocatable:     []*corev1.Pod{pod},
			preRestoreErr:      fmt.Errorf("pre restore err"),
			wantPreAllocatable: nil,
			wantStatus1:        false,
			wantStatus2:        false,
		},
		{
			name:               "restore err",
			rInfo:              rInfo,
			preAllocatable:     []*corev1.Pod{pod},
			restoreErr:         fmt.Errorf("restore err"),
			wantPreAllocatable: nil,
			wantStatus1:        true,
			wantStatus2:        false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := NewFrameworkExtenderFactory(
				WithKoordinatorClientSet(koordClientSet),
				WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)

			extender := NewFrameworkExtender(extenderFactory, fh)
			pl := &fakeReservationPreAllocationRestorePlugin{
				preRestoreErr: tt.preRestoreErr,
				restoreErr:    tt.restoreErr,
			}
			impl := extender.(*frameworkExtenderImpl)
			impl.updatePlugins(pl)

			cycleState := framework.NewCycleState()

			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
			})

			status := extender.RunReservationExtensionPreRestoreReservationPreAllocation(context.TODO(), cycleState, tt.rInfo)
			assert.Equal(t, tt.wantStatus1, status.IsSuccess())
			if !tt.wantStatus1 {
				return
			}
			pluginToRestoreState, status := extender.RunReservationExtensionRestoreReservationPreAllocation(context.TODO(), cycleState, tt.rInfo, tt.preAllocatable, nodeInfo)
			assert.Equal(t, tt.wantStatus2, status.IsSuccess())
			if !tt.wantStatus2 {
				return
			}
			assert.NotNil(t, pluginToRestoreState)
			if tt.wantPreAllocatable != nil {
				assert.NotNil(t, pluginToRestoreState[pl.Name()])
				assert.Equal(t, tt.wantPreAllocatable, pluginToRestoreState[pl.Name()].(*fakeNodeReservationPreAllocationRestoreStateData).preAllocatable)
			}
		})
	}
}

type fakeReservationFilterPlugin struct {
	index int
	err   error
}

func (f *fakeReservationFilterPlugin) Name() string { return "fakeReservationFilterPlugin" }

func (f *fakeReservationFilterPlugin) FilterReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	if reservationInfo.Reservation.Annotations == nil {
		reservationInfo.Reservation.Annotations = map[string]string{}
	}
	reservationInfo.Reservation.Annotations[fmt.Sprintf("reservationFilterWithPlugin-%d", f.index)] = fmt.Sprintf("%d", f.index)
	if f.err != nil {
		return framework.AsStatus(f.err)
	}
	return nil
}

func (f *fakeReservationFilterPlugin) FilterNominateReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeName string) *framework.Status {
	if reservationInfo.Reservation.Annotations == nil {
		reservationInfo.Reservation.Annotations = map[string]string{}
	}
	reservationInfo.Reservation.Annotations[fmt.Sprintf("reservationFilterPlugin-%d", f.index)] = fmt.Sprintf("%d", f.index)
	if f.err != nil {
		return framework.AsStatus(f.err)
	}
	return nil
}

func TestRunReservationFilterPlugins(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	}
	testNodeInfo := framework.NewNodeInfo()
	testNodeInfo.SetNode(testNode)
	tests := []struct {
		name            string
		reservation     *schedulingv1alpha1.Reservation
		plugins         []*fakeReservationFilterPlugin
		wantAnnotations map[string]string
		wantStatus      bool
	}{
		{
			name: "filter reservation succeeded",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
				},
			},
			plugins: []*fakeReservationFilterPlugin{
				{index: 1},
				{index: 2},
			},
			wantAnnotations: map[string]string{
				"reservationFilterWithPlugin-1": "1",
				"reservationFilterWithPlugin-2": "2",
			},
			wantStatus: true,
		},
		{
			name: "first plugin failed",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
				},
			},
			plugins: []*fakeReservationFilterPlugin{
				{index: 1, err: errors.New("failed")},
				{index: 2},
			},
			wantAnnotations: map[string]string{
				"reservationFilterWithPlugin-1": "1",
			},
			wantStatus: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := NewFrameworkExtenderFactory(
				WithKoordinatorClientSet(koordClientSet),
				WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)

			extender := NewFrameworkExtender(extenderFactory, fh)
			impl := extender.(*frameworkExtenderImpl)
			for _, pl := range tt.plugins {
				impl.updatePlugins(pl)
			}

			cycleState := framework.NewCycleState()

			reservationInfo := NewReservationInfo(tt.reservation)
			status := extender.RunReservationFilterPlugins(context.TODO(), cycleState, &corev1.Pod{}, reservationInfo, testNodeInfo)
			assert.Equal(t, tt.wantStatus, status.IsSuccess())
			assert.Equal(t, tt.wantAnnotations, reservationInfo.Reservation.Annotations)
		})
	}
}

func TestRunNominateReservationFilterPlugins(t *testing.T) {
	tests := []struct {
		name            string
		reservation     *schedulingv1alpha1.Reservation
		plugins         []*fakeReservationFilterPlugin
		wantAnnotations map[string]string
		wantStatus      bool
	}{
		{
			name: "filter reservation succeeded",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
				},
			},
			plugins: []*fakeReservationFilterPlugin{
				{index: 1},
				{index: 2},
			},
			wantAnnotations: map[string]string{
				"reservationFilterPlugin-1": "1",
				"reservationFilterPlugin-2": "2",
			},
			wantStatus: true,
		},
		{
			name: "first plugin failed",
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
				},
			},
			plugins: []*fakeReservationFilterPlugin{
				{index: 1, err: errors.New("failed")},
				{index: 2},
			},
			wantAnnotations: map[string]string{
				"reservationFilterPlugin-1": "1",
			},
			wantStatus: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := NewFrameworkExtenderFactory(
				WithKoordinatorClientSet(koordClientSet),
				WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)

			extender := NewFrameworkExtender(extenderFactory, fh)
			impl := extender.(*frameworkExtenderImpl)
			for _, pl := range tt.plugins {
				impl.updatePlugins(pl)
			}

			cycleState := framework.NewCycleState()

			reservationInfo := NewReservationInfo(tt.reservation)
			status := extender.RunNominateReservationFilterPlugins(context.TODO(), cycleState, &corev1.Pod{}, reservationInfo, "test-node-1")
			assert.Equal(t, tt.wantStatus, status.IsSuccess())
			assert.Equal(t, tt.wantAnnotations, reservationInfo.Reservation.Annotations)
		})
	}
}

type fakeReservationScorePlugin struct {
	name   string
	scores map[string]int64
	err    error

	enableNormalization bool
	isPreAllocation     bool
}

func (f *fakeReservationScorePlugin) Name() string { return f.name }

func (f *fakeReservationScorePlugin) ScoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeName string) (int64, *framework.Status) {
	var status *framework.Status
	if f.err != nil {
		status = framework.AsStatus(f.err)
	}
	if f.isPreAllocation {
		return f.scores[pod.GetName()], status
	}
	return f.scores[reservationInfo.GetName()], status
}

func (f *fakeReservationScorePlugin) ReservationScoreExtensions() ReservationScoreExtensions {
	return f
}

func (f *fakeReservationScorePlugin) NormalizeReservationScore(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, scores ReservationScoreList) *framework.Status {
	if f.enableNormalization {
		return DefaultReservationNormalizeScore(MaxReservationScore, false, scores)
	}
	return nil
}

func TestReservationScorePlugin(t *testing.T) {
	tests := []struct {
		name         string
		reservations []*schedulingv1alpha1.Reservation
		plugins      []*fakeReservationScorePlugin
		wantScores   PluginToReservationScores
		wantStatus   bool
	}{
		{
			name: "normal score",
			reservations: []*schedulingv1alpha1.Reservation{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-reservation-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-reservation-2",
					},
				},
			},
			plugins: []*fakeReservationScorePlugin{
				{
					name: "pl-1", scores: map[string]int64{
						"test-reservation-1": 1,
						"test-reservation-2": 1,
					},
				},
				{
					name: "pl-2", scores: map[string]int64{
						"test-reservation-1": 2,
						"test-reservation-2": 2,
					},
				},
			},
			wantScores: PluginToReservationScores{
				"pl-1": {
					{Name: "test-reservation-1", Score: 1},
					{Name: "test-reservation-2", Score: 1},
				},
				"pl-2": {
					{Name: "test-reservation-1", Score: 2},
					{Name: "test-reservation-2", Score: 2},
				},
			},
			wantStatus: true,
		},
		{
			name: "normalize score",
			reservations: []*schedulingv1alpha1.Reservation{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-reservation-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-reservation-2",
					},
				},
			},
			plugins: []*fakeReservationScorePlugin{
				{
					name: "pl-1",
					scores: map[string]int64{
						"test-reservation-1": 100,
						"test-reservation-2": 200,
					},
					enableNormalization: true,
				},
				{
					name: "pl-2",
					scores: map[string]int64{
						"test-reservation-1": 100,
						"test-reservation-2": 50,
					},
					enableNormalization: true,
				},
			},
			wantScores: PluginToReservationScores{
				"pl-1": {
					{Name: "test-reservation-1", Score: 50},
					{Name: "test-reservation-2", Score: 100},
				},
				"pl-2": {
					{Name: "test-reservation-1", Score: 100},
					{Name: "test-reservation-2", Score: 50},
				},
			},
			wantStatus: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := NewFrameworkExtenderFactory(
				WithKoordinatorClientSet(koordClientSet),
				WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)

			extender := NewFrameworkExtender(extenderFactory, fh)
			impl := extender.(*frameworkExtenderImpl)
			for _, pl := range tt.plugins {
				impl.updatePlugins(pl)
			}

			cycleState := framework.NewCycleState()

			var rInfos []*ReservationInfo
			for _, v := range tt.reservations {
				rInfos = append(rInfos, NewReservationInfo(v))
			}

			pluginToReservationScores, status := extender.RunReservationScorePlugins(context.TODO(), cycleState, &corev1.Pod{}, rInfos, "test-node-1")
			assert.Equal(t, tt.wantStatus, status.IsSuccess())
			assert.Equal(t, tt.wantScores, pluginToReservationScores)
		})
	}
}

func TestRunReservationPreAllocationScorePlugins(t *testing.T) {
	testRInfo := NewReservationInfo(&schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation-1",
		},
	})
	tests := []struct {
		name       string
		pods       []*corev1.Pod
		plugins    []*fakeReservationScorePlugin
		wantScores PluginToReservationScores
		wantStatus bool
	}{
		{
			name: "normal score",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod-2",
					},
				},
			},
			plugins: []*fakeReservationScorePlugin{
				{
					name: "pl-1", scores: map[string]int64{
						"test-pod-1": 1,
						"test-pod-2": 1,
					},
					isPreAllocation: true,
				},
				{
					name: "pl-2", scores: map[string]int64{
						"test-pod-1": 2,
						"test-pod-2": 2,
					},
					isPreAllocation: true,
				},
			},
			wantScores: PluginToReservationScores{
				"pl-1": {
					{Name: "test-pod-1", Score: 1},
					{Name: "test-pod-2", Score: 1},
				},
				"pl-2": {
					{Name: "test-pod-1", Score: 2},
					{Name: "test-pod-2", Score: 2},
				},
			},
			wantStatus: true,
		},
		{
			name: "normalize score",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod-2",
					},
				},
			},
			plugins: []*fakeReservationScorePlugin{
				{
					name: "pl-1",
					scores: map[string]int64{
						"test-pod-1": 100,
						"test-pod-2": 200,
					},
					enableNormalization: true,
					isPreAllocation:     true,
				},
				{
					name: "pl-2",
					scores: map[string]int64{
						"test-pod-1": 100,
						"test-pod-2": 50,
					},
					enableNormalization: true,
					isPreAllocation:     true,
				},
			},
			wantScores: PluginToReservationScores{
				"pl-1": {
					{Name: "test-pod-1", Score: 50},
					{Name: "test-pod-2", Score: 100},
				},
				"pl-2": {
					{Name: "test-pod-1", Score: 100},
					{Name: "test-pod-2", Score: 50},
				},
			},
			wantStatus: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := NewFrameworkExtenderFactory(
				WithKoordinatorClientSet(koordClientSet),
				WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)

			extender := NewFrameworkExtender(extenderFactory, fh)
			impl := extender.(*frameworkExtenderImpl)
			for _, pl := range tt.plugins {
				impl.updatePlugins(pl)
			}

			cycleState := framework.NewCycleState()

			pluginToReservationScores, status := extender.RunReservationPreAllocationScorePlugins(context.TODO(), cycleState, testRInfo, tt.pods, "test-node-1")
			assert.Equal(t, tt.wantStatus, status.IsSuccess())
			assert.Equal(t, tt.wantScores, pluginToReservationScores)
		})
	}
}

func TestReservationPreBindPluginOrder(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  "123456789",
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					SchedulerName: "koord-scheduler",
				},
			},
		},
	}

	tests := []struct {
		name                       string
		pluginRegistrationOrder    []string // PreBind plugin registration order
		wantExecutionOrder         []int    // Expected execution order by index
		wantReservationAnnotations map[string]string
	}{
		{
			name:                    "plugins execute in PreBind registration order",
			pluginRegistrationOrder: []string{"PluginB", "PluginA", "PluginC"},
			wantExecutionOrder:      []int{2, 1, 3}, // B(index=2), A(index=1), C(index=3)
			wantReservationAnnotations: map[string]string{
				"PreBindReservation-1": "1",
				"PreBindReservation-2": "2",
				"PreBindReservation-3": "3",
			},
		},
		{
			name:                    "different order should execute differently",
			pluginRegistrationOrder: []string{"PluginA", "PluginC", "PluginB"},
			wantExecutionOrder:      []int{1, 3, 2}, // A(index=1), C(index=3), B(index=2)
			wantReservationAnnotations: map[string]string{
				"PreBindReservation-1": "1",
				"PreBindReservation-2": "2",
				"PreBindReservation-3": "3",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create plugins with specific names and indices
			plugins := map[string]*TestTransformer{
				"PluginA": {name: "PluginA", index: 1},
				"PluginB": {name: "PluginB", index: 2},
				"PluginC": {name: "PluginC", index: 3},
			}

			// Register PreBind plugins in the specified order
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			for _, pluginName := range tt.pluginRegistrationOrder {
				pl := plugins[pluginName]
				registeredPlugins = append(registeredPlugins,
					schedulertesting.RegisterPreBindPlugin(pl.Name(), func(p *TestTransformer) func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
						return func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
							return p, nil
						}
					}(pl)),
				)
			}

			fakeClient := kubefake.NewSimpleClientset()
			sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
			fh, err := schedulertesting.NewFramework(
				context.TODO(),
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithClientSet(fakeClient),
				frameworkruntime.WithInformerFactory(sharedInformerFactory),
			)
			assert.NoError(t, err)

			koordClientSet := koordfake.NewSimpleClientset()
			koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
			extenderFactory, _ := NewFrameworkExtenderFactory(
				WithKoordinatorClientSet(koordClientSet),
				WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
			)

			_, err = koordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation.DeepCopy(), metav1.CreateOptions{})
			assert.NoError(t, err)
			koordSharedInformerFactory.Start(nil)
			koordSharedInformerFactory.WaitForCacheSync(nil)

			extender := NewFrameworkExtender(extenderFactory, fh)
			extender.SetConfiguredPlugins(fh.ListPlugins())
			impl := extender.(*frameworkExtenderImpl)

			// Register ReservationPreBindPlugins
			for _, pl := range plugins {
				impl.updatePlugins(pl)
			}

			// Verify plugins are stored in map
			assert.Equal(t, len(plugins), len(impl.reservationPreBindPlugins))
			for name, pl := range plugins {
				assert.Equal(t, pl, impl.reservationPreBindPlugins[name])
			}

			// Execute PreBind for reservation
			impl.SharedInformerFactory().Start(nil)
			impl.KoordinatorSharedInformerFactory().Start(nil)
			impl.SharedInformerFactory().WaitForCacheSync(nil)
			impl.KoordinatorSharedInformerFactory().WaitForCacheSync(nil)

			cycleState := framework.NewCycleState()
			reservePod := reservationutil.NewReservePod(reservation)
			status := extender.RunPreBindPlugins(context.TODO(), cycleState, reservePod, "test-node-1")
			assert.True(t, status.IsSuccess(), status.Message())

			// Verify annotations were set in correct order
			s, err := cycleState.Read("test-preBind-reservation")
			assert.NoError(t, err)
			assert.Equal(t, tt.wantReservationAnnotations, s.(*testPreBindReservationState).reservation.Annotations)
		})
	}
}
