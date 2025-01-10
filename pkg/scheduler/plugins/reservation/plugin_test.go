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
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	apiresource "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodename"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1beta3"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var _ framework.SharedLister = &fakeSharedLister{}

type fakeSharedLister struct {
	nodes       []*corev1.Node
	nodeInfos   []*framework.NodeInfo
	nodeInfoMap map[string]*framework.NodeInfo
	listErr     bool
}

func newFakeSharedLister(pods []*corev1.Pod, nodes []*corev1.Node, listErr bool) *fakeSharedLister {
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

	return &fakeSharedLister{
		nodes:       nodes,
		nodeInfos:   nodeInfos,
		nodeInfoMap: nodeInfoMap,
		listErr:     listErr,
	}
}

func (f *fakeSharedLister) NodeInfos() framework.NodeInfoLister {
	return f
}

func (f *fakeSharedLister) StorageInfos() framework.StorageInfoLister {
	return f
}

func (f *fakeSharedLister) IsPVCUsedByPods(key string) bool {
	return false
}

func (f *fakeSharedLister) List() ([]*framework.NodeInfo, error) {
	if f.listErr {
		return nil, fmt.Errorf("list error")
	}
	return f.nodeInfos, nil
}

func (f *fakeSharedLister) HavePodsWithAffinityList() ([]*framework.NodeInfo, error) {
	return nil, nil
}

func (f *fakeSharedLister) HavePodsWithRequiredAntiAffinityList() ([]*framework.NodeInfo, error) {
	return nil, nil
}

func (f *fakeSharedLister) Get(nodeName string) (*framework.NodeInfo, error) {
	return f.nodeInfoMap[nodeName], nil
}

type pluginTestSuit struct {
	fw              framework.Framework
	pluginFactory   func() (framework.Plugin, error)
	extenderFactory *frameworkext.FrameworkExtenderFactory
}

func newPluginTestSuitWith(t testing.TB, pods []*corev1.Pod, nodes []*corev1.Node, setArgs ...func(*config.ReservationArgs)) *pluginTestSuit {
	var v1beta3args v1beta3.ReservationArgs
	v1beta3.SetDefaults_ReservationArgs(&v1beta3args)
	var reservationArgs config.ReservationArgs
	err := v1beta3.Convert_v1beta3_ReservationArgs_To_config_ReservationArgs(&v1beta3args, &reservationArgs, nil)
	assert.NoError(t, err)
	for _, fn := range setArgs {
		fn(&reservationArgs)
	}

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, _ := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	extenderFactory.InitScheduler(frameworkext.NewFakeScheduler())
	proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, New)

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterFilterPlugin(nodename.Name, nodename.New),
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newFakeSharedLister(pods, nodes, false)

	fakeRecorder := record.NewFakeRecorder(1024)
	eventRecorder := record.NewEventRecorderAdapter(fakeRecorder)

	fw, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		frameworkruntime.WithClientSet(cs),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithSnapshotSharedLister(snapshot),
		frameworkruntime.WithEventRecorder(eventRecorder),
	)
	assert.NoError(t, err)

	fwExt := extenderFactory.NewFrameworkExtender(fw)
	fwExt.SetConfiguredPlugins(fw.ListPlugins())
	fwExt.SetPodNominator(NewPodNominator())

	factory := func() (framework.Plugin, error) {
		return proxyNew(&reservationArgs, fw)
	}

	return &pluginTestSuit{
		fw:              fw,
		pluginFactory:   factory,
		extenderFactory: extenderFactory,
	}
}

func newPluginTestSuit(t *testing.T) *pluginTestSuit {
	return newPluginTestSuitWith(t, nil, nil)
}

func (s *pluginTestSuit) start() {
	s.fw.SharedInformerFactory().Start(nil)
	s.extenderFactory.KoordinatorSharedInformerFactory().Start(nil)
	s.fw.SharedInformerFactory().WaitForCacheSync(nil)
	s.extenderFactory.KoordinatorSharedInformerFactory().WaitForCacheSync(nil)
}

func TestNew(t *testing.T) {
	suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
		args.EnablePreemption = true
	})
	pl, err := suit.pluginFactory()
	assert.NoError(t, err)
	assert.NotNil(t, pl)
	assert.Equal(t, Name, pl.Name())
}

func TestPreFilter(t *testing.T) {
	reservePod := testGetReservePod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-0",
		},
	})
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-0",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-0",
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					Object: &corev1.ObjectReference{
						Kind: "Pod",
						Name: "test-pod-0",
					},
				},
			},
			TTL: &metav1.Duration{Duration: 30 * time.Minute},
		},
	}
	missTemplateReservation := r.DeepCopy()
	missTemplateReservation.Spec.Template = nil

	tests := []struct {
		name                  string
		pod                   *corev1.Pod
		reservation           *schedulingv1alpha1.Reservation
		nodeReservationStates map[string]*nodeReservationState
		wantStatus            *framework.Status
		wantPreRes            *framework.PreFilterResult
	}{
		{
			name: "skip for non-reserve pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "not-reserve",
				},
			},
			wantStatus: framework.NewStatus(framework.Skip),
			wantPreRes: nil,
		},
		{
			name: "get reservation error",
			pod:  reservePod,
			wantStatus: framework.NewStatus(framework.Error, fmt.Sprintf("cannot get reservation, err: %v",
				apierrors.NewNotFound(schedulingv1alpha1.Resource("reservation"), reservePod.Name))),
			wantPreRes: nil,
		},
		{
			name:        "failed to validate reservation",
			pod:         reservePod,
			reservation: missTemplateReservation,
			wantStatus:  framework.NewStatus(framework.Error, "the reservation misses the template spec"),
			wantPreRes:  nil,
		},
		{
			name:        "validate reservation successfully",
			pod:         reservePod,
			reservation: r,
			wantStatus:  nil,
			wantPreRes:  nil,
		},
		{
			name: "failed to reservation affinity",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationReservationAffinity: `{"reservationSelector": {"reservation-type": "test"}}`,
					},
				},
			},
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAffinity),
			wantPreRes: nil,
		},
		{
			name: "reservation affinity",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationReservationAffinity: `{"reservationSelector": {"reservation-type": "test"}}`,
					},
				},
			},
			nodeReservationStates: map[string]*nodeReservationState{
				"test-node-1": {},
			},
			wantStatus: nil,
			wantPreRes: &framework.PreFilterResult{
				NodeNames: sets.New("test-node-1"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t)
			if tt.reservation != nil {
				_, err := suit.extenderFactory.KoordinatorClientSet().SchedulingV1alpha1().Reservations().Create(context.TODO(), tt.reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			p, err := suit.pluginFactory()

			assert.NoError(t, err)
			pl := p.(*Plugin)

			reservationAffinity, err := reservationutil.GetRequiredReservationAffinity(tt.pod)
			assert.NoError(t, err)
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:           reservationAffinity != nil,
					nodeReservationStates: tt.nodeReservationStates,
				},
			})
			preRes, got := pl.PreFilter(context.TODO(), cycleState, tt.pod)
			assert.Equal(t, tt.wantStatus, got)
			assert.Equal(t, tt.wantPreRes, preRes)
		})
	}
}

func TestFilter(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}
	testNodeInfo := framework.NewNodeInfo()
	testNodeInfo.SetNode(testNode)

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation",
			UID:  uuid.NewUUID(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeName: testNode.Name,
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
		},
	}

	reservationNotSetNode := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reservationNotSetNode",
			UID:  uuid.NewUUID(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{},
			},
		},
	}

	reservationNotMatchedNode := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reservationNotMatchedNode",
			UID:  uuid.NewUUID(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeName: "other-node",
				},
			},
		},
	}

	alignedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "aligned-reservation-1",
			UID:  uuid.NewUUID(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: testNode.Name,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"),
			},
		},
	}

	restrictedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "restricted-reservation-1",
			UID:  uuid.NewUUID(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: testNode.Name,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"),
			},
		},
	}

	testReservationIgnoredPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
			Labels: map[string]string{
				apiext.LabelReservationIgnored: "true",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name         string
		pod          *corev1.Pod
		reservations []*schedulingv1alpha1.Reservation
		nodeInfo     *framework.NodeInfo
		stateData    *stateData
		want         *framework.Status
	}{
		{
			name: "skip for non-reserve pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "not-reserve",
				},
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "failed for node is nil",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "not-reserve",
				},
			},
			nodeInfo: nil,
			want:     framework.NewStatus(framework.Error, "node not found"),
		},
		{
			name:         "skip for pod not set node",
			pod:          reservationutil.NewReservePod(reservationNotSetNode),
			reservations: []*schedulingv1alpha1.Reservation{reservationNotSetNode},
			nodeInfo:     testNodeInfo,
			want:         nil,
		},
		{
			name:         "filter pod successfully",
			pod:          reservationutil.NewReservePod(reservation),
			reservations: []*schedulingv1alpha1.Reservation{reservation},
			nodeInfo:     testNodeInfo,
			want:         nil,
		},
		{
			name:         "failed for node does not matches the pod",
			pod:          reservationutil.NewReservePod(reservationNotMatchedNode),
			reservations: []*schedulingv1alpha1.Reservation{reservationNotMatchedNode},
			nodeInfo:     testNodeInfo,
			want:         framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonNodeNotMatchReservation),
		},
		{
			name: "ReservationAllocatePolicyDefault cannot coexist with Aligned policy",
			pod:  reservationutil.NewReservePod(reservation),
			reservations: []*schedulingv1alpha1.Reservation{
				reservation,
				alignedReservation,
			},
			nodeInfo: testNodeInfo,
			want:     framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAllocatePolicyConflict),
		},
		{
			name: "ReservationAllocatePolicyDefault cannot coexist with Restricted policy",
			pod:  reservationutil.NewReservePod(reservation),
			reservations: []*schedulingv1alpha1.Reservation{
				reservation,
				restrictedReservation,
			},
			nodeInfo: testNodeInfo,
			want:     framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAllocatePolicyConflict),
		},
		{
			name: "Aligned policy can coexist with Restricted policy",
			pod:  reservationutil.NewReservePod(alignedReservation),
			reservations: []*schedulingv1alpha1.Reservation{
				alignedReservation,
				restrictedReservation,
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name: "Restricted policy can coexist with Aligned policy",
			pod:  reservationutil.NewReservePod(restrictedReservation),
			reservations: []*schedulingv1alpha1.Reservation{
				alignedReservation,
				restrictedReservation,
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
		{
			name:     "normal pod has reservation affinity but no matched reservation",
			pod:      &corev1.Pod{},
			nodeInfo: testNodeInfo,
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
				},
			},
			want: framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAffinity),
		},
		{
			name: "normal pod has reservation affinity and filter successfully",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo.Clone(),
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					nodeReservationStates: map[string]*nodeReservationState{
						testNode.Name: {
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(reservation),
							},
						},
					},
				},
			},
			want: nil,
		},
		{
			name: "normal pod specifies a reservation name and filter successfully",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			nodeInfo: testNodeInfo,
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:     true,
					reservationName: restrictedReservation.Name,
					nodeReservationStates: map[string]*nodeReservationState{
						testNode.Name: {
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(restrictedReservation),
							},
						},
					},
				},
			},
			want: nil,
		},
		{
			name: "Aligned policy can coexist with Restricted policy",
			pod:  testReservationIgnoredPod,
			reservations: []*schedulingv1alpha1.Reservation{
				alignedReservation,
				restrictedReservation,
			},
			nodeInfo: testNodeInfo,
			want:     nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t)
			client := suit.extenderFactory.KoordinatorClientSet()
			for _, reservation := range tt.reservations {
				_, err := client.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			pl.enableLazyReservationRestore = true
			cycleState := framework.NewCycleState()
			if tt.stateData != nil {
				tt.stateData.podRequests = apiresource.PodRequests(tt.pod, apiresource.PodResourcesOptions{})
				tt.stateData.podRequestsResources = framework.NewResource(tt.stateData.podRequests)
				cycleState.Write(stateKey, tt.stateData)
			}
			suit.start()

			got := pl.Filter(context.TODO(), cycleState, tt.pod, tt.nodeInfo)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFilterWithPreemption(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
				corev1.ResourcePods:   resource.MustParse("100"),
			},
		},
	}
	tests := []struct {
		name       string
		stateData  *stateData
		wantStatus *framework.Status
	}{
		{
			name: "successfully filter non-reservations with preemption",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 32 * 1000,
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter non-reservations with preemption",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 32 * 1000,
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonPreemptionFailed),
		},
		{
			name: "filter non-reservations with preemption but no preemptible resources",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 32 * 1000,
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
			pl := &Plugin{}
			cycleState := framework.NewCycleState()
			if tt.stateData.podRequestsResources == nil {
				resources := framework.NewResource(tt.stateData.podRequests)
				tt.stateData.podRequestsResources = resources
			}
			cycleState.Write(stateKey, tt.stateData)
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(node)
			got := pl.Filter(context.TODO(), cycleState, &corev1.Pod{}, nodeInfo)
			assert.Equal(t, tt.wantStatus, got)
		})
	}
}

func Test_filterWithReservations(t *testing.T) {
	testRInfo := frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-r",
			UID:  "123456",
			Annotations: map[string]string{
				apiext.AnnotationNodeReservation: `{"resources": {"cpu": "1"}}`,
			},
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:  resource.MustParse("7"),
									corev1.ResourcePods: resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:  resource.MustParse("7"),
				corev1.ResourcePods: resource.MustParse("2"),
			},
		},
	})
	testRInfo.AddAssignedPod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-a",
			Namespace: "test-ns",
			UID:       "xxxxxxxxx",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"),
						},
					},
				},
			},
		},
	})
	testRInfo.AddAssignedPod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-b",
			Namespace: "test-ns",
			UID:       "yyyyyy",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"),
						},
					},
				},
			},
		},
	})
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
				corev1.ResourcePods:   resource.MustParse("100"),
				apiext.BatchCPU:       resource.MustParse("7500"),
				apiext.BatchMemory:    resource.MustParse("10Gi"),
			},
		},
	}
	tests := []struct {
		name       string
		stateData  *stateData
		wantStatus *framework.Status
	}{
		{
			name: "filter aligned reservation with nodeInfo",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter aligned reservation with nodeInfo",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 32 * 1000, // no remaining resources
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Insufficient cpu by node"),
		},
		{
			name: "filter restricted reservation with nodeInfo",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter restricted reservation with affinity",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter restricted reservation with nodeInfo and matched requests are zero",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("6000"),
						apiext.BatchMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
								ScalarResources: map[corev1.ResourceName]int64{
									apiext.BatchCPU:    1500,
									apiext.BatchMemory: 2 * 1024 * 1024 * 1024,
								},
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter restricted reservation with nodeInfo",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu"),
		},
		{
			name: "failed to filter restricted reservation since exceeding max pods",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("3"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								testRInfo.Clone(),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Too many pods"),
		},
		{
			name: "failed to filter restricted reservation since unmatched resources are insufficient",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("8000"),
						apiext.BatchMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
								ScalarResources: map[corev1.ResourceName]int64{
									apiext.BatchCPU:    1500,
									apiext.BatchMemory: 2 * 1024 * 1024 * 1024,
								},
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable,
				"Insufficient kubernetes.io/batch-cpu by node"),
		},
		{
			name: "filter restricted reservation and ignore matched requests are zero without affinity",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: false,
					podRequests: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("8000"),
						apiext.BatchMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
								ScalarResources: map[corev1.ResourceName]int64{
									apiext.BatchCPU:    1500,
									apiext.BatchMemory: 2 * 1024 * 1024 * 1024,
								},
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter restricted reservation due to reserved",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("6"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 0,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										Annotations: map[string]string{
											apiext.AnnotationNodeReservation: `{"resources": {"cpu": "2"}}`,
										},
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu"),
		},
		{
			name: "filter default reservations with preemption",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("4"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 36 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										UID:  "123456",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
									Status: schedulingv1alpha1.ReservationStatus{
										Allocatable: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
										Allocated: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter default reservations with preempt from reservation and node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("2"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										UID:  "123456",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
									Status: schedulingv1alpha1.ReservationStatus{
										Allocatable: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
										Allocated: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter default reservations with preempt from reservation",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("2"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Insufficient cpu by node"),
		},
		{
			name: "failed to filter default reservations with preempt from node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Insufficient cpu by node"),
		},
		{
			name: "filter restricted reservations with preempt from reservation",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("4"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										UID:  "123456",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
									Status: schedulingv1alpha1.ReservationStatus{
										Allocatable: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
										Allocated: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter restricted reservations with preempt from node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
					preemptibleInRRs: nil,
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu"),
		},
		{
			name: "failed to filter multiple restricted reservations with preempt from node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
					preemptibleInRRs: nil,
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 10000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
								},
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r-1",
											UID:  "7891011",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									ResourceNames: []corev1.ResourceName{corev1.ResourceCPU},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("4"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("1"),
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu", "Reservation(s) Insufficient cpu"),
		},
		{
			name: "failed to filter restricted reservations with preempt from reservation and node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("2"),
						},
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("2"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									ResourceNames: []corev1.ResourceName{
										corev1.ResourceCPU,
									},
								},
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu"),
		},
		{
			name: "filter restricted reservations with reservation name and preempt from reservation",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:     true,
					reservationName: "test-r",
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("4"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
									ObjectMeta: metav1.ObjectMeta{
										Name: "test-r",
										UID:  "123456",
									},
									Spec: schedulingv1alpha1.ReservationSpec{
										AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										Template: &corev1.PodTemplateSpec{
											Spec: corev1.PodSpec{
												Containers: []corev1.Container{
													{
														Resources: corev1.ResourceRequirements{
															Requests: corev1.ResourceList{
																corev1.ResourceCPU: resource.MustParse("6"),
															},
														},
													},
												},
											},
										},
									},
									Status: schedulingv1alpha1.ReservationStatus{
										Allocatable: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
										Allocated: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("6"),
										},
									},
								}),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter restricted reservations with preempt from reservation and node",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity: true,
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
					podRequestsResources: &framework.Resource{
						MilliCPU: 4 * 1000,
						Memory:   4 * 1024 * 1024 * 1024,
					},
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceMemory: resource.MustParse("32Gi"),
						},
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("4"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 38 * 1000,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 6000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								{
									Reservation: &schedulingv1alpha1.Reservation{
										ObjectMeta: metav1.ObjectMeta{
											Name: "test-r",
											UID:  "123456",
										},
										Spec: schedulingv1alpha1.ReservationSpec{
											AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
										},
									},
									Allocatable: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									Allocated: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("6"),
									},
									ResourceNames: []corev1.ResourceName{
										corev1.ResourceCPU,
									},
								},
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "filter restricted reservation with reservation name, max pods and preemptible",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:     true,
					reservationName: "test-r",
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("3"),
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU:  resource.MustParse("1"),
								corev1.ResourcePods: resource.MustParse("1"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								testRInfo.Clone(),
							},
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to filter restricted reservation with reservation name since exceeding max pods",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:     true,
					reservationName: "test-r",
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("3"),
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								testRInfo.Clone(),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Too many pods, "+
				"requested: 1, used: 2, capacity: 2"),
		},
		{
			name: "failed to filter restricted reservation with name and reserved since insufficient resource",
			stateData: &stateData{
				schedulingStateData: schedulingStateData{
					hasAffinity:     true,
					reservationName: "test-r",
					podRequests: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("6"),
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU:  resource.MustParse("1"),
								corev1.ResourcePods: resource.MustParse("1"),
							},
						},
					},
					nodeReservationStates: map[string]*nodeReservationState{
						node.Name: {
							podRequested: &framework.Resource{
								MilliCPU: 30 * 1000,
								Memory:   24 * 1024 * 1024 * 1024,
							},
							rAllocated: &framework.Resource{
								MilliCPU: 2000,
							},
							matchedOrIgnored: []*frameworkext.ReservationInfo{
								testRInfo.Clone(),
							},
						},
					},
				},
			},
			wantStatus: framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient cpu, "+
				"requested: 6000, used: 1000, capacity: 6000"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t)
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			suit.start()
			cycleState := framework.NewCycleState()
			if tt.stateData.podRequestsResources == nil {
				resources := framework.NewResource(tt.stateData.podRequests)
				tt.stateData.podRequestsResources = resources
			}
			cycleState.Write(stateKey, tt.stateData)
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(node)
			got := pl.filterWithReservations(context.TODO(), cycleState, &corev1.Pod{}, nodeInfo, tt.stateData.nodeReservationStates[node.Name].matchedOrIgnored, tt.stateData.hasAffinity)
			assert.Equal(t, tt.wantStatus, got)
		})
	}
}

func TestPreFilterExtensionAddPod(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	tests := []struct {
		name                 string
		pod                  *corev1.Pod
		withR                bool
		state                *stateData
		wantPreemptible      map[string]corev1.ResourceList
		wantPreemptibleInRRs map[string]map[types.UID]corev1.ResourceList
	}{
		{
			name: "with BestEffort Pod",
			pod:  &corev1.Pod{},
			state: &stateData{
				schedulingStateData: schedulingStateData{
					preemptible:      map[string]corev1.ResourceList{},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{},
				},
			},
			wantPreemptible: map[string]corev1.ResourceList{
				"test-node": {
					corev1.ResourcePods: *resource.NewQuantity(-1, resource.DecimalSI),
				},
			},
			wantPreemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{},
		},
		{
			name: "preempt normal pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					UID:  "123456",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			state: &stateData{
				schedulingStateData: schedulingStateData{
					preemptible: map[string]corev1.ResourceList{
						node.Name: {
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{},
				},
			},
			wantPreemptible: map[string]corev1.ResourceList{
				"test-node": {
					corev1.ResourceCPU:  *resource.NewQuantity(0, resource.DecimalSI),
					corev1.ResourcePods: *resource.NewQuantity(-1, resource.DecimalSI),
				},
			},
			wantPreemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{},
		},
		{
			name: "preempt pod allocated in reservation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					UID:  "123456",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			withR: true,
			state: &stateData{
				schedulingStateData: schedulingStateData{
					preemptible: map[string]corev1.ResourceList{},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
						node.Name: {
							"123456": {
								corev1.ResourceCPU: resource.MustParse("4"),
							},
						},
					},
				},
			},
			wantPreemptible: map[string]corev1.ResourceList{},
			wantPreemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
				"test-node": {
					"123456": {
						corev1.ResourceCPU:  *resource.NewQuantity(0, resource.DecimalSI),
						corev1.ResourcePods: *resource.NewQuantity(-1, resource.DecimalSI),
					},
				},
			},
		},
		{
			name: "add nominated pod allocated in reservation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					UID:  "123456",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			withR: true,
			state: &stateData{
				schedulingStateData: schedulingStateData{
					preemptible:      map[string]corev1.ResourceList{},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{},
				},
			},
			wantPreemptible: map[string]corev1.ResourceList{},
			wantPreemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
				"test-node": {
					"123456": {
						corev1.ResourceCPU:  *resource.NewQuantity(-4, resource.DecimalSI),
						corev1.ResourcePods: *resource.NewQuantity(-1, resource.DecimalSI),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t)
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			suit.start()
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, tt.state)
			if tt.withR {
				reservation := &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-r",
						UID:  "123456",
					},
					Spec: schedulingv1alpha1.ReservationSpec{},
				}
				assert.NoError(t, reservationutil.SetReservationAvailable(reservation, node.Name))
				pl.reservationCache.updateReservation(reservation)
				assert.NoError(t, pl.reservationCache.assumePod(reservation.UID, tt.pod))
			}
			podInfo, _ := framework.NewPodInfo(tt.pod)
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(node)
			status := pl.PreFilterExtensions().AddPod(context.TODO(), cycleState, nil, podInfo, nodeInfo)
			assert.True(t, status.IsSuccess())
			sd := getStateData(cycleState)
			assert.Equal(t, tt.wantPreemptible, sd.preemptible)
			assert.Equal(t, tt.wantPreemptibleInRRs, sd.preemptibleInRRs)
		})
	}
}

func TestPreFilterExtensionRemovePod(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	tests := []struct {
		name                 string
		pod                  *corev1.Pod
		withR                bool
		wantPreemptible      map[string]corev1.ResourceList
		wantPreemptibleInRRs map[string]map[types.UID]corev1.ResourceList
	}{
		{
			name: "with BestEffort Pod",
			pod:  &corev1.Pod{},
			wantPreemptible: map[string]corev1.ResourceList{
				"test-node": {
					corev1.ResourcePods: *resource.NewQuantity(1, resource.DecimalSI),
				},
			},
			wantPreemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{},
		},
		{
			name: "preempt normal pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					UID:  "123456",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			wantPreemptible: map[string]corev1.ResourceList{
				node.Name: {
					corev1.ResourceCPU:  resource.MustParse("4"),
					corev1.ResourcePods: *resource.NewQuantity(1, resource.DecimalSI),
				},
			},
			wantPreemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{},
		},
		{
			name: "preempt pod allocated in reservation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					UID:  "123456",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			withR:           true,
			wantPreemptible: map[string]corev1.ResourceList{},
			wantPreemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{
				node.Name: {
					"123456": {
						corev1.ResourceCPU:  resource.MustParse("4"),
						corev1.ResourcePods: *resource.NewQuantity(1, resource.DecimalSI),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t)
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, &stateData{
				schedulingStateData: schedulingStateData{
					preemptible:      map[string]corev1.ResourceList{},
					preemptibleInRRs: map[string]map[types.UID]corev1.ResourceList{},
				},
			})
			if tt.withR {
				reservation := &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-r",
						UID:  "123456",
					},
					Spec: schedulingv1alpha1.ReservationSpec{},
				}
				assert.NoError(t, reservationutil.SetReservationAvailable(reservation, node.Name))
				pl.reservationCache.updateReservation(reservation)
				assert.NoError(t, pl.reservationCache.assumePod(reservation.UID, tt.pod))
			}
			podInfo, _ := framework.NewPodInfo(tt.pod)
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(node)
			status := pl.PreFilterExtensions().RemovePod(context.TODO(), cycleState, nil, podInfo, nodeInfo)
			assert.True(t, status.IsSuccess())
			sd := getStateData(cycleState)
			assert.Equal(t, tt.wantPreemptible, sd.preemptible)
			assert.Equal(t, tt.wantPreemptibleInRRs, sd.preemptibleInRRs)
		})
	}
}

func TestFilterNominateReservation(t *testing.T) {
	reservation4C8G := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation4C8G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("4"),
									corev1.ResourceMemory: resource.MustParse("8Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	}
	reservation2C4G := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation2C4G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}
	allocateOnceAndAllocatedReservation := reservation2C4G.DeepCopy()
	allocateOnceAndAllocatedReservation.Name = "allocateOnceAndAllocatedReservation"
	allocateOnceAndAllocatedReservation.UID = uuid.NewUUID()
	allocateOnceAndAllocatedReservation.Spec.AllocateOnce = pointer.Bool(true)
	reservationutil.SetReservationAvailable(allocateOnceAndAllocatedReservation, "test-node")
	for i := range allocateOnceAndAllocatedReservation.Status.Conditions {
		allocateOnceAndAllocatedReservation.Status.Conditions[i].LastProbeTime = metav1.Time{}
		allocateOnceAndAllocatedReservation.Status.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	allocateOnceAndAllocatedReservation.Status.Allocated = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("2"),
		corev1.ResourceMemory: resource.MustParse("4Gi"),
	}

	tests := []struct {
		name              string
		podRequests       corev1.ResourceList
		reservations      []*schedulingv1alpha1.Reservation
		targetReservation *schedulingv1alpha1.Reservation
		wantStatus        *framework.Status
	}{
		{
			name: "satisfied reservation",
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			reservations: []*schedulingv1alpha1.Reservation{
				reservation2C4G,
				reservation4C8G,
			},
			targetReservation: reservation2C4G,
			wantStatus:        nil,
		},
		{
			name: "intersection resource names",
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			},
			reservations: []*schedulingv1alpha1.Reservation{
				reservation2C4G,
				reservation4C8G,
			},
			targetReservation: reservation2C4G,
			wantStatus:        nil,
		},
		{
			name: "no intersection resource names",
			podRequests: corev1.ResourceList{
				corev1.ResourceEphemeralStorage: resource.MustParse("2Gi"),
			},
			reservations: []*schedulingv1alpha1.Reservation{
				reservation2C4G,
				reservation4C8G,
			},
			targetReservation: reservation2C4G,
			wantStatus:        framework.NewStatus(framework.Unschedulable, ErrReasonNoReservationsMeetRequirements),
		},
		{
			name: "failed with allocateOnce and allocated reservation",
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			},
			reservations: []*schedulingv1alpha1.Reservation{
				reservation4C8G,
				allocateOnceAndAllocatedReservation,
			},
			targetReservation: allocateOnceAndAllocatedReservation,
			wantStatus:        framework.NewStatus(framework.Unschedulable, "reservation has allocateOnce enabled and has already been allocated"),
		},
		{
			name: "missing reservation info but impossible",
			podRequests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			},
			targetReservation: reservation4C8G,
			wantStatus:        framework.AsStatus(fmt.Errorf("impossible, there is no relevant Reservation information")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{},
			}

			suit := newPluginTestSuitWith(t, nil, []*corev1.Node{node})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			assert.NotNil(t, p)
			pl := p.(*Plugin)
			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: tt.podRequests,
							},
						},
					},
				},
			}

			state := &stateData{
				schedulingStateData: schedulingStateData{
					nodeReservationStates: map[string]*nodeReservationState{},
					podRequests:           tt.podRequests,
					podRequestsResources:  framework.NewResource(tt.podRequests),
				},
			}
			for _, v := range tt.reservations {
				pl.reservationCache.updateReservation(v)
				if apiext.IsReservationAllocateOnce(v) && len(v.Status.Allocated) > 0 {
					pl.reservationCache.addPod(v.UID, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "allocated-pod", UID: uuid.NewUUID()}})
				}
				rInfo := pl.reservationCache.getReservationInfoByUID(v.UID)
				nodeRState := state.nodeReservationStates[v.Status.NodeName]
				if nodeRState == nil {
					nodeRState = &nodeReservationState{}
				}
				nodeRState.nodeName = v.Status.NodeName
				nodeRState.matchedOrIgnored = append(nodeRState.matchedOrIgnored, rInfo)
				state.nodeReservationStates[v.Status.NodeName] = nodeRState
			}
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, state)

			rInfo := frameworkext.NewReservationInfo(tt.targetReservation)
			status := pl.FilterNominateReservation(context.TODO(), cycleState, pod, rInfo, node.Name)
			assert.Equal(t, tt.wantStatus, status)
		})
	}
}

func TestPostFilter(t *testing.T) {
	testFilterStatus := framework.NewStatus(framework.Unschedulable, "node(s) didn't match the requested node name")
	testFilterReservationStatus := framework.NewStatus(framework.Unschedulable,
		reservationutil.NewReservationReason("Insufficient nvidia.com/gpu"),
		reservationutil.NewReservationReason("Insufficient koordinator.sh/gpu-mem-ratio"))
	testFilterReservationStatus1 := framework.NewStatus(framework.Unschedulable,
		reservationutil.NewReservationReason("Insufficient cpu"),
		"Insufficient memory")
	type args struct {
		hasStateData             bool
		hasAffinity              bool
		nodeReservationDiagnosis map[string]*nodeDiagnosisState
		filteredNodeStatusMap    framework.NodeToStatusMap
	}
	tests := []struct {
		name  string
		args  args
		want  *framework.PostFilterResult
		want1 *framework.Status
	}{
		{
			name: "no reservation filtering",
			args: args{
				hasStateData:          false,
				filteredNodeStatusMap: framework.NodeToStatusMap{},
			},
			want:  nil,
			want1: framework.NewStatus(framework.Unschedulable),
		},
		{
			name: "show reservation owner matched when reservation affinity specified",
			args: args{
				hasStateData:             true,
				hasAffinity:              true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{},
				filteredNodeStatusMap:    framework.NodeToStatusMap{},
			},
			want:  nil,
			want1: framework.NewStatus(framework.Unschedulable, "0 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation owner matched, unschedulable unmatched",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 3,
						affinityUnmatched:        0,
					},
					"test-node-1": {
						ownerMatched:             1,
						isUnschedulableUnmatched: 1,
						affinityUnmatched:        0,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": {},
					"test-node-1": {},
				},
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"4 Reservation(s) is unschedulable",
				"4 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation owner matched, name unmatched",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:      3,
						nameUnmatched:     3,
						affinityUnmatched: 0,
					},
					"test-node-1": {
						ownerMatched:      1,
						nameUnmatched:     1,
						affinityUnmatched: 0,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": {},
					"test-node-1": {},
				},
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"4 Reservation(s) didn't match the requested reservation name",
				"4 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation owner matched, taints not tolerated",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:    3,
						taintsUnmatched: 3,
						taintsUnmatchedReasons: map[string]int{
							"{node.kubernetes.io/unreachable: }": 3,
						},
					},
					"test-node-1": {
						ownerMatched:    1,
						taintsUnmatched: 1,
						taintsUnmatchedReasons: map[string]int{
							"{node.kubernetes.io/unreachable: }": 1,
						},
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": {},
					"test-node-1": {},
				},
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"4 Reservation(s) had untolerated taint {node.kubernetes.io/unreachable: }",
				"4 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation matched owner, unschedulable and exact matched",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 0,
						notExactMatched:          3,
					},
					"test-node-1": {
						ownerMatched:             2,
						isUnschedulableUnmatched: 1,
						notExactMatched:          1,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": {},
					"test-node-1": {},
				},
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"1 Reservation(s) is unschedulable",
				"4 Reservation(s) is not exact matched",
				"5 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation matched owner, unschedulable and exact matched",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 0,
						notExactMatched:          3,
					},
					"test-node-1": {
						ownerMatched:             2,
						isUnschedulableUnmatched: 1,
						notExactMatched:          1,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": {},
					"test-node-1": {},
				},
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"1 Reservation(s) is unschedulable",
				"4 Reservation(s) is not exact matched",
				"5 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation matched owner, unschedulable and affinity unmatched",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 0,
						affinityUnmatched:        3,
					},
					"test-node-1": {
						ownerMatched:             2,
						isUnschedulableUnmatched: 1,
						affinityUnmatched:        1,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": {},
					"test-node-1": {},
				},
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"4 Reservation(s) didn't match affinity rules",
				"1 Reservation(s) is unschedulable",
				"5 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation matched owner, name and unschedulable unmatched",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:    3,
						nameMatched:     1,
						nameUnmatched:   2,
						notExactMatched: 1,
					},
					"test-node-1": {
						ownerMatched:  2,
						nameUnmatched: 2,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": {},
					"test-node-1": {},
				},
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"1 Reservation(s) exactly matches the requested reservation name",
				"4 Reservation(s) didn't match the requested reservation name",
				"1 Reservation(s) is not exact matched",
				"5 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation matched owner and filter failures of other plugins",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 0,
						affinityUnmatched:        3,
					},
					"test-node-1": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 1,
						affinityUnmatched:        0,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": {},
					"test-node-1": testFilterStatus,
				},
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"3 Reservation(s) didn't match affinity rules",
				"1 Reservation(s) is unschedulable",
				"2 Reservation(s) for node reason that node(s) didn't match the requested node name",
				"6 Reservation(s) matched owner total"),
		},
		{
			name: "show reservation matched owner and skip reservation-level reasons and filter other failures",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 0,
						affinityUnmatched:        1,
					},
					"test-node-1": {
						ownerMatched:             3,
						isUnschedulableUnmatched: 1,
						affinityUnmatched:        0,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": testFilterReservationStatus,
					"test-node-1": testFilterReservationStatus1,
				},
			},
			want: nil,
			want1: framework.NewStatus(framework.Unschedulable,
				"1 Reservation(s) didn't match affinity rules",
				"1 Reservation(s) is unschedulable",
				"1 Reservation(s) for node reason that Insufficient memory",
				"6 Reservation(s) matched owner total"),
		},
		{
			name: "ignore reservation ignored",
			args: args{
				hasStateData: true,
				nodeReservationDiagnosis: map[string]*nodeDiagnosisState{
					"test-node-0": {
						ignored: 3,
					},
					"test-node-1": {
						ignored: 1,
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{
					"test-node-0": {},
					"test-node-1": {},
				},
			},
			want:  nil,
			want1: framework.NewStatus(framework.Unschedulable),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := &Plugin{}
			cycleState := framework.NewCycleState()
			if tt.args.hasStateData {
				cycleState.Write(stateKey, &stateData{
					schedulingStateData: schedulingStateData{
						hasAffinity:              tt.args.hasAffinity,
						nodeReservationDiagnosis: tt.args.nodeReservationDiagnosis,
					},
				})
			}
			got, got1 := pl.PostFilter(context.TODO(), cycleState, nil, tt.args.filteredNodeStatusMap)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestReserve(t *testing.T) {
	reservation2C4G := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation2C4G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
		},
	}

	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
		},
	}

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
		},
	}

	tests := []struct {
		name            string
		pod             *corev1.Pod
		reservation     *schedulingv1alpha1.Reservation
		wantReservation *schedulingv1alpha1.Reservation
		wantStatus      *framework.Status
		wantPods        map[types.UID]*frameworkext.PodRequirement
	}{
		{
			name:        "reserve pod",
			pod:         reservationutil.NewReservePod(reservation),
			reservation: reservation,
			wantStatus:  nil,
			wantPods:    map[types.UID]*frameworkext.PodRequirement{},
		},
		{
			name:       "node without reservations",
			pod:        &corev1.Pod{},
			wantStatus: nil,
		},
		{
			name:            "reserve pod in reservation",
			pod:             testPod,
			reservation:     reservation2C4G,
			wantStatus:      nil,
			wantReservation: reservation2C4G,
			wantPods: map[types.UID]*frameworkext.PodRequirement{
				testPod.UID: {
					Namespace: testPod.Namespace,
					Name:      testPod.Name,
					UID:       testPod.UID,
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t)
			client := suit.extenderFactory.KoordinatorClientSet()
			if tt.reservation != nil {
				_, err := client.SchedulingV1alpha1().Reservations().Create(context.TODO(), tt.reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			if tt.pod != nil {
				_, err := suit.fw.ClientSet().CoreV1().Pods(tt.pod.Namespace).Create(context.TODO(), tt.pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			state := &stateData{}
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, state)

			var rInfo *frameworkext.ReservationInfo
			if tt.reservation != nil {
				rInfo = frameworkext.NewReservationInfo(tt.reservation)
			}
			pl.handle.GetReservationNominator().AddNominatedReservation(tt.pod, "test-node", rInfo)
			status := pl.Reserve(context.TODO(), cycleState, tt.pod, "test-node")
			assert.Equal(t, tt.wantStatus, status)
			if tt.wantReservation == nil {
				assert.Nil(t, state.assumed)
			} else {
				assert.NotNil(t, state.assumed)
				assert.Equal(t, tt.wantReservation, state.assumed.Reservation)
			}
			if tt.reservation != nil {
				rInfo = pl.reservationCache.getReservationInfoByUID(tt.reservation.UID)
				assert.Equal(t, tt.wantPods, rInfo.AssignedPods)
				assert.Equal(t, &schedulingStateData{}, &state.schedulingStateData)
			}
		})
	}
}

func TestUnreserve(t *testing.T) {
	reservation2C4G := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation2C4G",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2"),
									corev1.ResourceMemory: resource.MustParse("4Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "test-node",
		},
	}

	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
		},
	}

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{},
			},
		},
	}

	tests := []struct {
		name        string
		pod         *corev1.Pod
		reservation *schedulingv1alpha1.Reservation
		wantStatus  *framework.Status
	}{
		{
			name:        "unreserve reserve pod",
			pod:         reservationutil.NewReservePod(reservation),
			reservation: reservation,
			wantStatus:  nil,
		},
		{
			name:       "node without reservations",
			pod:        &corev1.Pod{},
			wantStatus: nil,
		},
		{
			name:        "unreserve pod in reservation",
			pod:         testPod,
			reservation: reservation2C4G,
			wantStatus:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t)
			client := suit.extenderFactory.KoordinatorClientSet()
			if tt.reservation != nil {
				_, err := client.SchedulingV1alpha1().Reservations().Create(context.TODO(), tt.reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			state := &stateData{}
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, state)

			var rInfo *frameworkext.ReservationInfo
			if tt.reservation != nil {
				rInfo = frameworkext.NewReservationInfo(tt.reservation)
			}
			pl.handle.GetReservationNominator().AddNominatedReservation(tt.pod, "test-node", rInfo)
			status := pl.Reserve(context.TODO(), cycleState, tt.pod, "test-node")
			pl.Unreserve(context.TODO(), cycleState, tt.pod, "test-node")
			assert.Equal(t, tt.wantStatus, status)
			if tt.reservation != nil {
				rInfo := pl.reservationCache.getReservationInfoByUID(tt.reservation.UID)
				if reservationutil.IsReservePod(tt.pod) {
					assert.Nil(t, rInfo)
				} else {
					assert.Equal(t, map[types.UID]*frameworkext.PodRequirement{}, rInfo.AssignedPods)
				}
			}
		})
	}
}

func TestPreBind(t *testing.T) {
	tests := []struct {
		name               string
		assumedReservation *schedulingv1alpha1.Reservation
		pod                *corev1.Pod
		wantPod            *corev1.Pod
		wantStatus         *framework.Status
	}{
		{
			name: "preBind pod with assumed reservation",
			assumedReservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					UID:  "1234567890",
					Name: "assumed-reservation",
				},
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod",
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod",
					Annotations: map[string]string{
						apiext.AnnotationReservationAllocated: `{"name":"assumed-reservation","uid":"1234567890"}`,
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "preBind pod without assumed reservation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod",
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod",
				},
			},
			wantStatus: nil,
		},
		{
			name: "preBind reserve pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod",
					Annotations: map[string]string{
						reservationutil.AnnotationReservePod: "true",
					},
				},
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod",
					Annotations: map[string]string{
						reservationutil.AnnotationReservePod: "true",
					},
				},
			},
			wantStatus: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t)
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			assert.NotNil(t, p)

			_, err = suit.fw.ClientSet().CoreV1().Pods(tt.pod.Namespace).Create(context.TODO(), tt.pod, metav1.CreateOptions{})
			assert.NoError(t, err)

			pl := p.(*Plugin)

			suit.start()

			cycleState := framework.NewCycleState()
			var assumedRInfo *frameworkext.ReservationInfo
			if tt.assumedReservation != nil {
				assumedRInfo = frameworkext.NewReservationInfo(tt.assumedReservation)
			}
			cycleState.Write(stateKey, &stateData{
				assumed: assumedRInfo,
			})
			status := pl.PreBind(context.TODO(), cycleState, tt.pod, "test-node")
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantPod, tt.pod)
		})
	}
}

func TestBind(t *testing.T) {
	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-1",
		},
	}
	testNodeName := "test-node-0"
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reserve-pod-0",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-0",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"test-resource": *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
		},
	}
	reservePod := reservationutil.NewReservePod(reservation)
	failedReservation := reservation.DeepCopy()
	failedReservation.Status = schedulingv1alpha1.ReservationStatus{
		Phase: schedulingv1alpha1.ReservationFailed,
	}
	activeReservation := reservation.DeepCopy()
	assert.NoError(t, reservationutil.SetReservationAvailable(activeReservation, testNodeName))

	reservationWithResizeAllocatable := reservation.DeepCopy()
	assert.NoError(t, reservationutil.UpdateReservationResizeAllocatable(reservationWithResizeAllocatable, corev1.ResourceList{
		"test-resource": *resource.NewQuantity(200, resource.DecimalSI),
	}))
	activeReservationWithResizedAllocatable := reservationWithResizeAllocatable.DeepCopy()
	assert.NoError(t, reservationutil.SetReservationAvailable(activeReservationWithResizedAllocatable, testNodeName))
	assert.True(t, equality.Semantic.DeepEqual(activeReservationWithResizedAllocatable.Status.Allocatable, corev1.ResourceList{
		"test-resource": *resource.NewQuantity(200, resource.DecimalSI),
	}))

	tests := []struct {
		name            string
		pod             *corev1.Pod
		nodeName        string
		reservation     *schedulingv1alpha1.Reservation
		fakeClient      koordclientset.Interface
		want            *framework.Status
		wantReservation *schedulingv1alpha1.Reservation
	}{
		{
			name: "skip for non-reserve pod",
			pod:  normalPod,
			want: framework.NewStatus(framework.Skip),
		},
		{
			name: "failed to get reservation",
			pod:  reservePod,
			want: framework.AsStatus(apierrors.NewNotFound(schedulingv1alpha1.Resource("reservation"), reservation.Name)),
		},
		{
			name:        "get failed reservation",
			pod:         reservePod,
			nodeName:    testNodeName,
			reservation: failedReservation,
			want:        framework.AsStatus(errors.New(ErrReasonReservationInactive)),
		},
		{
			name:        "failed to update status",
			pod:         reservePod,
			nodeName:    testNodeName,
			reservation: reservation,
			fakeClient:  koordfake.NewSimpleClientset(),
			want:        framework.AsStatus(apierrors.NewNotFound(schedulingv1alpha1.Resource("reservations"), reservation.Name)),
		},
		{
			name:            "bind reservation successfully",
			pod:             reservePod,
			nodeName:        testNodeName,
			reservation:     reservation,
			wantReservation: activeReservation,
			want:            nil,
		},
		{
			name:            "resize reservation status allocatable",
			pod:             reservePod,
			nodeName:        testNodeName,
			reservation:     reservationWithResizeAllocatable,
			wantReservation: activeReservationWithResizedAllocatable,
			want:            nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t)

			client := suit.extenderFactory.KoordinatorClientSet()
			if tt.reservation != nil {
				_, err := client.SchedulingV1alpha1().Reservations().Create(context.TODO(), tt.reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			suit.start()

			if tt.fakeClient != nil {
				pl.client = tt.fakeClient.SchedulingV1alpha1()
			}

			got := pl.Bind(context.TODO(), nil, tt.pod, tt.nodeName)
			assert.Equal(t, tt.want, got)

			if tt.want.IsSuccess() && tt.reservation != nil {
				reservation, err := client.SchedulingV1alpha1().Reservations().Get(context.TODO(), tt.reservation.Name, metav1.GetOptions{})
				assert.NoError(t, err)
				for _, r := range []*schedulingv1alpha1.Reservation{reservation, tt.wantReservation} {
					if r != nil {
						for i := range r.Status.Conditions {
							r.Status.Conditions[i].LastProbeTime = metav1.Time{}
							r.Status.Conditions[i].LastTransitionTime = metav1.Time{}
						}
					}
				}
				assert.Equal(t, tt.wantReservation, reservation)
			}
		})
	}
}

func testGetReservePod(pod *corev1.Pod) *corev1.Pod {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[reservationutil.AnnotationReservePod] = "true"
	pod.Annotations[reservationutil.AnnotationReservationName] = pod.Name
	return pod
}

// nominatedPodMap is a structure that stores pods nominated to run on nodes.
// It exists because nominatedNodeName of pod objects stored in the structure
// may be different than what scheduler has here. We should be able to find pods
// by their UID and update/delete them.
type nominatedPodMap struct {
	// nominatedPods is a map keyed by a node name and the value is a list of
	// pods which are nominated to run on the node. These are pods which can be in
	// the activeQ or unschedulableQ.
	nominatedPods map[string][]*framework.PodInfo
	// nominatedPodToNode is map keyed by a Pod UID to the node name where it is
	// nominated.
	nominatedPodToNode map[types.UID]string

	sync.RWMutex
}

func (npm *nominatedPodMap) add(pi *framework.PodInfo, nodeName string) {
	// always delete the pod if it already exist, to ensure we never store more than
	// one instance of the pod.
	npm.delete(pi.Pod)

	nnn := nodeName
	if len(nnn) == 0 {
		nnn = NominatedNodeName(pi.Pod)
		if len(nnn) == 0 {
			return
		}
	}
	npm.nominatedPodToNode[pi.Pod.UID] = nnn
	for _, npi := range npm.nominatedPods[nnn] {
		if npi.Pod.UID == pi.Pod.UID {
			klog.V(4).InfoS("Pod already exists in the nominated map", "pod", klog.KObj(npi.Pod))
			return
		}
	}
	npm.nominatedPods[nnn] = append(npm.nominatedPods[nnn], pi)
}

func (npm *nominatedPodMap) delete(p *corev1.Pod) {
	nnn, ok := npm.nominatedPodToNode[p.UID]
	if !ok {
		return
	}
	for i, np := range npm.nominatedPods[nnn] {
		if np.Pod.UID == p.UID {
			npm.nominatedPods[nnn] = append(npm.nominatedPods[nnn][:i], npm.nominatedPods[nnn][i+1:]...)
			if len(npm.nominatedPods[nnn]) == 0 {
				delete(npm.nominatedPods, nnn)
			}
			break
		}
	}
	delete(npm.nominatedPodToNode, p.UID)
}

// UpdateNominatedPod updates the <oldPod> with <newPod>.
func (npm *nominatedPodMap) UpdateNominatedPod(logr klog.Logger, oldPod *corev1.Pod, newPodInfo *framework.PodInfo) {
	npm.Lock()
	defer npm.Unlock()
	// In some cases, an Update event with no "NominatedNode" present is received right
	// after a node("NominatedNode") is reserved for this pod in memory.
	// In this case, we need to keep reserving the NominatedNode when updating the pod pointer.
	nodeName := ""
	// We won't fall into below `if` block if the Update event represents:
	// (1) NominatedNode info is added
	// (2) NominatedNode info is updated
	// (3) NominatedNode info is removed
	if NominatedNodeName(oldPod) == "" && NominatedNodeName(newPodInfo.Pod) == "" {
		if nnn, ok := npm.nominatedPodToNode[oldPod.UID]; ok {
			// This is the only case we should continue reserving the NominatedNode
			nodeName = nnn
		}
	}
	// We update irrespective of the nominatedNodeName changed or not, to ensure
	// that pod pointer is updated.
	npm.delete(oldPod)
	npm.add(newPodInfo, nodeName)
}

// NewPodNominator creates a nominatedPodMap as a backing of framework.PodNominator.
func NewPodNominator() framework.PodNominator {
	return &nominatedPodMap{
		nominatedPods:      make(map[string][]*framework.PodInfo),
		nominatedPodToNode: make(map[types.UID]string),
	}
}

// NominatedNodeName returns nominated node name of a Pod.
func NominatedNodeName(pod *corev1.Pod) string {
	return pod.Status.NominatedNodeName
}

// DeleteNominatedPodIfExists deletes <pod> from nominatedPods.
func (npm *nominatedPodMap) DeleteNominatedPodIfExists(pod *corev1.Pod) {
	npm.Lock()
	npm.delete(pod)
	npm.Unlock()
}

// AddNominatedPod adds a pod to the nominated pods of the given node.
// This is called during the preemption process after a node is nominated to run
// the pod. We update the structure before sending a request to update the pod
// object to avoid races with the following scheduling cycles.
func (npm *nominatedPodMap) AddNominatedPod(logger klog.Logger, pi *framework.PodInfo, nominatingInfo *framework.NominatingInfo) {
	npm.Lock()
	npm.add(pi, nominatingInfo.NominatedNodeName)
	npm.Unlock()
}

// NominatedPodsForNode returns pods that are nominated to run on the given node,
// but they are waiting for other pods to be removed from the node.
func (npm *nominatedPodMap) NominatedPodsForNode(nodeName string) []*framework.PodInfo {
	npm.RLock()
	defer npm.RUnlock()
	// TODO: we may need to return a copy of []*Pods to avoid modification
	// on the caller side.
	return npm.nominatedPods[nodeName]
}
