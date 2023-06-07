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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
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
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1beta2"
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

func newPluginTestSuitWith(t *testing.T, pods []*corev1.Pod, nodes []*corev1.Node) *pluginTestSuit {
	var v1beta2args v1beta2.ReservationArgs
	v1beta2.SetDefaults_ReservationArgs(&v1beta2args)
	var reservationArgs config.ReservationArgs
	err := v1beta2.Convert_v1beta2_ReservationArgs_To_config_ReservationArgs(&v1beta2args, &reservationArgs, nil)
	assert.NoError(t, err)

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, _ := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, New)

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newFakeSharedLister(pods, nodes, false)

	fakeRecorder := record.NewFakeRecorder(1024)
	eventRecorder := record.NewEventRecorderAdapter(fakeRecorder)

	fw, err := schedulertesting.NewFramework(
		registeredPlugins,
		"koord-scheduler",
		frameworkruntime.WithClientSet(cs),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithSnapshotSharedLister(snapshot),
		frameworkruntime.WithEventRecorder(eventRecorder),
	)
	assert.NoError(t, err)

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
	suit := newPluginTestSuit(t)
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
		name        string
		pod         *corev1.Pod
		reservation *schedulingv1alpha1.Reservation
		want        *framework.Status
	}{
		{
			name: "skip for non-reserve pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "not-reserve",
				},
			},
			want: nil,
		},
		{
			name: "get reservation error",
			pod:  reservePod,
			want: framework.NewStatus(framework.Error, fmt.Sprintf("cannot get reservation, err: %v",
				apierrors.NewNotFound(schedulingv1alpha1.Resource("reservation"), reservePod.Name))),
		},
		{
			name:        "failed to validate reservation",
			pod:         reservePod,
			reservation: missTemplateReservation,
			want:        framework.NewStatus(framework.Error, "the reservation misses the template spec"),
		},
		{
			name:        "validate reservation successfully",
			pod:         reservePod,
			reservation: r,
			want:        nil,
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
			want: framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonReservationAffinity),
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
				hasAffinity: reservationAffinity != nil,
			})
			got := pl.PreFilter(context.TODO(), cycleState, tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFilter(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-0",
		},
	}
	testNodeInfo := &framework.NodeInfo{}
	testNodeInfo.SetNode(testNode)

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reservationNotSetNode",
			UID:  uuid.NewUUID(),
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeName: testNode.Name,
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
		ObjectMeta: metav1.ObjectMeta{Name: "aligned-reservation-1"},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			Template:       &corev1.PodTemplateSpec{},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: testNode.Name,
		},
	}

	restrictedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "restricted-reservation-1"},
		Spec: schedulingv1alpha1.ReservationSpec{
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			Template:       &corev1.PodTemplateSpec{},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: testNode.Name,
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
				hasAffinity: true,
			},
			want: framework.NewStatus(framework.Unschedulable, ErrReasonReservationAffinity),
		},
		{
			name:     "normal pod has reservation affinity and filter successfully",
			pod:      &corev1.Pod{},
			nodeInfo: testNodeInfo,
			stateData: &stateData{
				hasAffinity: true,
				nodeReservationStates: map[string]nodeReservationState{
					testNode.Name: {
						matched: []*frameworkext.ReservationInfo{
							frameworkext.NewReservationInfo(reservation),
						},
					},
				},
			},
			want: nil,
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
			cycleState := framework.NewCycleState()
			if tt.stateData != nil {
				cycleState.Write(stateKey, tt.stateData)
			}
			got := pl.Filter(context.TODO(), cycleState, tt.pod, tt.nodeInfo)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_filterWithReservations(t *testing.T) {
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
			name: "filter aligned reservation with nodeInfo",
			stateData: &stateData{
				podRequests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				nodeReservationStates: map[string]nodeReservationState{
					node.Name: {
						podRequested: &framework.Resource{
							MilliCPU: 30 * 1000,
							Memory:   24 * 1024 * 1024 * 1024,
						},
						rAllocated: &framework.Resource{
							MilliCPU: 0,
						},
						totalAligned: 1,
						matched: []*frameworkext.ReservationInfo{
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
			wantStatus: nil,
		},
		{
			name: "failed to filter aligned reservation with nodeInfo",
			stateData: &stateData{
				podRequests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				nodeReservationStates: map[string]nodeReservationState{
					node.Name: {
						podRequested: &framework.Resource{
							MilliCPU: 32 * 1000, // no remaining resources
							Memory:   24 * 1024 * 1024 * 1024,
						},
						rAllocated: &framework.Resource{
							MilliCPU: 0,
						},
						totalAligned: 1,
						matched: []*frameworkext.ReservationInfo{
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
			wantStatus: framework.NewStatus(framework.Unschedulable, ErrReasonReservationInsufficientResources),
		},
		{
			name: "filter restricted reservation with nodeInfo",
			stateData: &stateData{
				podRequests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("6"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				nodeReservationStates: map[string]nodeReservationState{
					node.Name: {
						podRequested: &framework.Resource{
							MilliCPU: 30 * 1000,
							Memory:   24 * 1024 * 1024 * 1024,
						},
						rAllocated: &framework.Resource{
							MilliCPU: 0,
						},
						totalRestricted: 1,
						matched: []*frameworkext.ReservationInfo{
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
			wantStatus: nil,
		},
		{
			name: "failed to filter restricted reservation with nodeInfo",
			stateData: &stateData{
				podRequests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				nodeReservationStates: map[string]nodeReservationState{
					node.Name: {
						podRequested: &framework.Resource{
							MilliCPU: 30 * 1000,
							Memory:   24 * 1024 * 1024 * 1024,
						},
						rAllocated: &framework.Resource{
							MilliCPU: 0,
						},
						totalRestricted: 1,
						matched: []*frameworkext.ReservationInfo{
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
			wantStatus: framework.NewStatus(framework.Unschedulable, ErrReasonReservationInsufficientResources),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := &Plugin{}
			cycleState := framework.NewCycleState()
			resources := framework.NewResource(tt.stateData.podRequests)
			tt.stateData.podRequestsResources = resources
			cycleState.Write(stateKey, tt.stateData)
			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(node)
			got := pl.filterWithReservations(context.TODO(), cycleState, &corev1.Pod{}, nodeInfo)
			assert.Equal(t, tt.wantStatus, got)
		})
	}
}

func TestPostFilter(t *testing.T) {
	highPriority := int32(math.MaxInt32)
	reservePod := testGetReservePod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:  "reserve-pod-0",
			Name: "reserve-pod-0",
		},
		Spec: corev1.PodSpec{
			NodeName: "node1",
		},
	})
	r := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  "reserve-pod-0",
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
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "node1",
			Phase:    schedulingv1alpha1.ReservationAvailable,
		},
	}
	tests := []struct {
		name           string
		pod            *corev1.Pod
		reservation    *schedulingv1alpha1.Reservation
		wantResult     *framework.PostFilterResult
		wantStatus     *framework.Status
		changePriority bool
	}{
		{
			name: "not reserve pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "not-reserve",
				},
			},
			wantResult: nil,
			wantStatus: framework.NewStatus(framework.Unschedulable),
		},
		{
			name:        "reserve pod",
			pod:         reservePod,
			reservation: r,
			wantResult:  nil,
			wantStatus:  framework.NewStatus(framework.Error),
		},
		{
			name: "not reserve pod, and its priority is higher than the reserve",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "not-reserve",
				},
				Spec: corev1.PodSpec{
					Priority: &highPriority,
				},
			},
			reservation:    r,
			wantResult:     nil,
			wantStatus:     framework.NewStatus(framework.Unschedulable),
			changePriority: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, []*corev1.Pod{reservePod}, []*corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "node1"}}})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)
			if tt.reservation != nil {
				pl.reservationCache.updateReservation(tt.reservation)
			}

			gotResult, status := pl.PostFilter(context.TODO(), nil, tt.pod, nil)
			assert.Equal(t, tt.wantResult, gotResult)
			assert.Equal(t, tt.wantStatus, status)
			if tt.changePriority {
				nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get("node1")
				assert.NoError(t, err)
				for _, p := range nodeInfo.Pods {
					if reservationutil.IsReservePod(p.Pod) {
						assert.Equal(t, int32(math.MaxInt32), *p.Pod.Spec.Priority)
					}
				}
			}
		})
	}
}

func TestFilterReservation(t *testing.T) {
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
			wantStatus:        framework.AsStatus(fmt.Errorf("no intersection resources")),
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
			wantStatus:        framework.AsStatus(fmt.Errorf("reservation has allocateOnce enabled and has already been allocated")),
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
			suit := newPluginTestSuit(t)
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			assert.NotNil(t, p)
			pl := p.(*Plugin)
			cycleState := framework.NewCycleState()
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
				nodeReservationStates: map[string]nodeReservationState{},
			}
			for _, v := range tt.reservations {
				pl.reservationCache.updateReservation(v)
				if apiext.IsReservationAllocateOnce(v) && len(v.Status.Allocated) > 0 {
					pl.reservationCache.addPod(v.UID, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "allocated-pod", UID: uuid.NewUUID()}})
				}
				rInfo := pl.reservationCache.getReservationInfoByUID(v.UID)
				nodeRState := state.nodeReservationStates[v.Status.NodeName]
				nodeRState.nodeName = v.Status.NodeName
				nodeRState.matched = append(nodeRState.matched, rInfo)
				state.nodeReservationStates[v.Status.NodeName] = nodeRState
			}
			cycleState.Write(stateKey, state)

			rInfo := frameworkext.NewReservationInfo(tt.targetReservation)
			status := pl.FilterReservation(context.TODO(), cycleState, pod, rInfo, "test-node")
			assert.Equal(t, tt.wantStatus, status)
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
			frameworkext.SetNominatedReservation(cycleState, rInfo)
			status := pl.Reserve(context.TODO(), cycleState, tt.pod, "test-node")
			assert.Equal(t, tt.wantStatus, status)
			if tt.wantReservation == nil {
				assert.Nil(t, state.assumed)
			} else {
				assert.Equal(t, tt.wantReservation, state.assumed.Reservation)
			}
			if tt.reservation != nil {
				rInfo := pl.reservationCache.getReservationInfoByUID(tt.reservation.UID)
				assert.Equal(t, tt.wantPods, rInfo.AssignedPods)
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
			frameworkext.SetNominatedReservation(cycleState, rInfo)
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
			},
		},
	}
	reservePod := reservationutil.NewReservePod(reservation)
	failedReservation := reservation.DeepCopy()
	failedReservation.Status = schedulingv1alpha1.ReservationStatus{
		Phase: schedulingv1alpha1.ReservationFailed,
	}
	activeReservation := reservation.DeepCopy()
	reservationutil.SetReservationAvailable(activeReservation, testNodeName)

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
