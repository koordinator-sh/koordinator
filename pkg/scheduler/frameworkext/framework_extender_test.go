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

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkfake "k8s.io/kubernetes/pkg/scheduler/framework/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var (
	_ PreFilterTransformer = &TestTransformer{}
	_ FilterTransformer    = &TestTransformer{}
	_ ScoreTransformer     = &TestTransformer{}
)

type TestTransformer struct {
	index int
}

func (h *TestTransformer) Name() string { return "TestTransformer" }

func (h *TestTransformer) BeforePreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool, *framework.Status) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("BeforePreFilter-%d", h.index)] = fmt.Sprintf("%d", h.index)
	return pod, true, nil
}

func (h *TestTransformer) AfterPreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
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

func (h *TestTransformer) BeforeScore(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodes []*corev1.Node) (*corev1.Pod, []*corev1.Node, bool, *framework.Status) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[fmt.Sprintf("BeforeScore-%d", h.index)] = fmt.Sprintf("%d", h.index)
	return pod, nodes, true, nil
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

var _ ReservationNominator = &fakeReservationNominator{}

type fakeReservationNominator struct {
	reservation *ReservationInfo
	err         error
}

func (f fakeReservationNominator) Name() string { return "fakeReservationNominator" }

func (f fakeReservationNominator) NominateReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (*ReservationInfo, *framework.Status) {
	if f.err != nil {
		return nil, framework.AsStatus(f.err)
	}
	return f.reservation, nil
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
			testTransformers := []SchedulingTransformer{
				&TestTransformer{index: 1},
				&TestTransformer{index: 2},
			}
			extenderFactory, _ := NewFrameworkExtenderFactory(WithDefaultTransformers(testTransformers...))
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
				frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{NodeInfoLister: frameworkfake.NodeInfoLister{}}),
			)
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
			_, status := frameworkExtender.RunPreFilterPlugins(context.TODO(), framework.NewCycleState(), tt.pod)
			assert.Equal(t, tt.want, status)
			expectedAnnotations := map[string]string{
				"BeforePreFilter-1": "1",
				"AfterPreFilter-1":  "1",
				"BeforePreFilter-2": "2",
				"AfterPreFilter-2":  "2",
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
			testTransformers := []SchedulingTransformer{
				&TestTransformer{index: 1},
				&TestTransformer{index: 2},
			}
			extenderFactory, _ := NewFrameworkExtenderFactory(WithDefaultTransformers(testTransformers...))
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
			)
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
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
		wantScore  framework.PluginToNodeScores
		wantStatus *framework.Status
	}{
		{
			name:       "normal RunScorePlugins",
			pod:        &corev1.Pod{},
			nodes:      []*corev1.Node{{}},
			wantScore:  framework.PluginToNodeScores{},
			wantStatus: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testTransformers := []SchedulingTransformer{
				&TestTransformer{index: 1},
				&TestTransformer{index: 2},
			}
			extenderFactory, _ := NewFrameworkExtenderFactory(WithDefaultTransformers(testTransformers...))
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
			)
			assert.NoError(t, err)
			frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
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

func TestRunReservePluginsReserve(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fake-reservation",
		},
	}
	reservationInfo := NewReservationInfo(reservation)
	tests := []struct {
		name            string
		nominators      []ReservationNominator
		wantReservation *ReservationInfo
		wantStatus      bool
	}{
		{
			name: "nominate reservation",
			nominators: []ReservationNominator{
				fakeReservationNominator{
					reservation: reservationInfo,
				},
			},
			wantReservation: reservationInfo,
			wantStatus:      true,
		},
		{
			name:            "no nominator",
			wantReservation: nil,
			wantStatus:      true,
		},
		{
			name: "multi nominators",
			nominators: []ReservationNominator{
				fakeReservationNominator{},
				fakeReservationNominator{
					reservation: reservationInfo,
				},
			},
			wantReservation: reservationInfo,
			wantStatus:      true,
		},
		{
			name: "error nominator",
			nominators: []ReservationNominator{
				fakeReservationNominator{
					err: fmt.Errorf("fail"),
				},
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
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
			)
			assert.NoError(t, err)

			extenderFactory, _ := NewFrameworkExtenderFactory()
			extender := NewFrameworkExtender(extenderFactory, fh)
			impl := extender.(*frameworkExtenderImpl)
			for _, v := range tt.nominators {
				impl.updatePlugins(v)
			}
			cycleState := framework.NewCycleState()
			status := extender.RunReservePluginsReserve(context.TODO(), cycleState, &corev1.Pod{}, "test-node-1")
			assert.Equal(t, tt.wantStatus, status.IsSuccess())
			reservation := GetNominatedReservation(cycleState)
			assert.Equal(t, tt.wantReservation, reservation)
		})
	}
}

func TestPreBind(t *testing.T) {
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fake-reservation",
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
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
				schedulertesting.RegisterPreBindPlugin("fakePreBindPlugin", func(_ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
					return &fakePreBindPlugin{err: errors.New("failed")}, nil
				}),
			}
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
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
			_ = koordSharedInformerFactory.Scheduling().V1alpha1().Reservations().Lister()
			koordSharedInformerFactory.Start(nil)
			koordSharedInformerFactory.WaitForCacheSync(nil)

			extender := NewFrameworkExtender(extenderFactory, fh)
			impl := extender.(*frameworkExtenderImpl)
			impl.updatePlugins(&TestTransformer{index: 1})
			impl.updatePlugins(&TestTransformer{index: 2})

			cycleState := framework.NewCycleState()

			status := extender.RunPreBindPlugins(context.TODO(), cycleState, tt.pod, "test-node-1")
			assert.Equal(t, tt.wantStatus, status.IsSuccess())
			if status.IsSuccess() {
				s, err := cycleState.Read("test-preBind-reservation")
				assert.NoError(t, err)
				assert.Equal(t, tt.wantAnnotations, s.(*testPreBindReservationState).reservation.Annotations)
			}
		})
	}
}

func TestPreBindExtensionOrder(t *testing.T) {
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
	fh, err := schedulertesting.NewFramework(
		registeredPlugins,
		"koord-scheduler",
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
	impl.updatePlugins(preBindA)
	impl.updatePlugins(preBindB)

	cycleState := framework.NewCycleState()

	pod := &corev1.Pod{}

	status := extender.RunPreBindPlugins(context.TODO(), cycleState, pod, "test-node-1")
	assert.True(t, status.IsSuccess())
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
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
			)
			assert.NoError(t, err)

			extenderFactory, _ := NewFrameworkExtenderFactory()

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

			status = extender.RunReservationExtensionFinalRestoreReservation(context.TODO(), cycleState, &corev1.Pod{}, pluginToNodeReservationRestoreState)
			assert.Equal(t, tt.wantStatus2, status.IsSuccess())
			val, err := cycleState.Read(fakeReservationRestoreStateKey)
			assert.NoError(t, err)
			stateData := val.(*fakeReservationRestoreStateData)
			assert.Equal(t, tt.wantReservation, stateData.m["test-node-1"].(*fakeNodeReservationRestoreStateData).matched[0].Reservation)
		})
	}
}

type fakeReservationFilterPlugin struct {
	index int
	err   error
}

func (f *fakeReservationFilterPlugin) Name() string { return "fakeReservationFilterPlugin" }

func (f *fakeReservationFilterPlugin) FilterReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeName string) *framework.Status {
	if reservationInfo.Reservation.Annotations == nil {
		reservationInfo.Reservation.Annotations = map[string]string{}
	}
	reservationInfo.Reservation.Annotations[fmt.Sprintf("reservationFilterPlugin-%d", f.index)] = fmt.Sprintf("%d", f.index)
	if f.err != nil {
		return framework.AsStatus(f.err)
	}
	return nil
}

func TestReservationFilterPlugin(t *testing.T) {
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
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
			)
			assert.NoError(t, err)

			extenderFactory, _ := NewFrameworkExtenderFactory()

			extender := NewFrameworkExtender(extenderFactory, fh)
			impl := extender.(*frameworkExtenderImpl)
			for _, pl := range tt.plugins {
				impl.updatePlugins(pl)
			}

			cycleState := framework.NewCycleState()

			reservationInfo := NewReservationInfo(tt.reservation)
			status := extender.RunReservationFilterPlugins(context.TODO(), cycleState, &corev1.Pod{}, reservationInfo, "test-node-1")
			assert.Equal(t, tt.wantStatus, status.IsSuccess())
			assert.Equal(t, tt.wantAnnotations, reservationInfo.Reservation.Annotations)
		})
	}
}

type fakeReservationScorePlugin struct {
	name  string
	score int64
	err   error
}

func (f *fakeReservationScorePlugin) Name() string { return f.name }

func (f *fakeReservationScorePlugin) ScoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeName string) (int64, *framework.Status) {
	var status *framework.Status
	if f.err != nil {
		status = framework.AsStatus(f.err)
	}
	return f.score, status
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
				{name: "pl-1", score: 1},
				{name: "pl-2", score: 2},
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registeredPlugins := []schedulertesting.RegisterPluginFunc{
				schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
				schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
			}
			fh, err := schedulertesting.NewFramework(
				registeredPlugins,
				"koord-scheduler",
			)
			assert.NoError(t, err)

			extenderFactory, _ := NewFrameworkExtenderFactory()

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
