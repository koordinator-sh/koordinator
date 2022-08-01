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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	scheduledconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/scheduling/config"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config/v1beta2"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	clientschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/scheduling/v1alpha1"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions/scheduling"
	"github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions/scheduling/v1alpha1"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

var _ listerschedulingv1alpha1.ReservationLister = &fakeReservationLister{}

type fakeReservationLister struct {
	reservations map[string]*schedulingv1alpha1.Reservation
	listErr      bool
	getErr       map[string]bool
}

func (f *fakeReservationLister) List(selector labels.Selector) (ret []*schedulingv1alpha1.Reservation, err error) {
	if f.listErr {
		return nil, fmt.Errorf("list error")
	}
	var rList []*schedulingv1alpha1.Reservation
	for _, r := range f.reservations {
		rList = append(rList, r)
	}
	return rList, nil
}

func (f *fakeReservationLister) Get(name string) (*schedulingv1alpha1.Reservation, error) {
	if f.getErr[name] {
		return nil, fmt.Errorf("get error")
	}
	return f.reservations[name], nil
}

var _ clientschedulingv1alpha1.SchedulingV1alpha1Interface = &fakeReservationClient{}

type fakeReservationClient struct {
	clientschedulingv1alpha1.SchedulingV1alpha1Interface
	clientschedulingv1alpha1.ReservationInterface
	lister          *fakeReservationLister
	updateStatusErr map[string]bool
	deleteErr       map[string]bool
}

func (f *fakeReservationClient) Reservations() clientschedulingv1alpha1.ReservationInterface {
	return f
}

func (f *fakeReservationClient) UpdateStatus(ctx context.Context, reservation *schedulingv1alpha1.Reservation, opts metav1.UpdateOptions) (*schedulingv1alpha1.Reservation, error) {
	if f.updateStatusErr[reservation.Name] {
		return nil, fmt.Errorf("updateStatus error")
	}
	r := f.lister.reservations[reservation.Name].DeepCopy()
	// only update phase for testing
	r.Status.Phase = reservation.Status.Phase
	f.lister.reservations[reservation.Name] = r
	return r, nil
}

func (f *fakeReservationClient) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	if f.deleteErr[name] {
		return fmt.Errorf("delete error")
	}
	delete(f.lister.reservations, name)
	return nil
}

var _ cache.SharedIndexInformer = &fakeIndexedInformer{}

type fakeIndexedInformer struct {
	cache.SharedInformer
	cache.Indexer
	rOnNode    map[string][]*schedulingv1alpha1.Reservation // nodeName -> []*Reservation
	byIndexErr map[string]bool
}

func (f *fakeIndexedInformer) GetIndexer() cache.Indexer {
	return f
}

func (f *fakeIndexedInformer) ByIndex(indexName, indexedValue string) ([]interface{}, error) {
	if f.byIndexErr[indexedValue] {
		return nil, fmt.Errorf("byIndex err")
	}
	out := make([]interface{}, len(f.rOnNode[indexedValue]))
	for i := range f.rOnNode[indexedValue] {
		out[i] = f.rOnNode[indexedValue][i]
	}
	return out, nil
}

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

type fakeKoordinatorSharedInformerFactory struct {
	koordinatorinformers.SharedInformerFactory
	v1alpha1.Interface
	v1alpha1.ReservationInformer

	informer *fakeIndexedInformer
}

func (f *fakeKoordinatorSharedInformerFactory) Scheduling() scheduling.Interface {
	if f.informer != nil {
		return f
	}
	return f.Scheduling()
}

func (f *fakeKoordinatorSharedInformerFactory) V1alpha1() v1alpha1.Interface {
	if f.informer != nil {
		return f
	}
	return f.V1alpha1()
}

func (f *fakeKoordinatorSharedInformerFactory) Reservations() v1alpha1.ReservationInformer {
	if f.informer != nil {
		return f
	}
	return f.Reservations()
}

func (f *fakeKoordinatorSharedInformerFactory) Informer() cache.SharedIndexInformer {
	if f.informer != nil {
		return f.informer
	}
	return f.Informer()
}

type fakeExtendedHandle struct {
	frameworkext.ExtendedHandle

	cs                         *kubefake.Clientset
	sharedLister               *fakeSharedLister
	koordSharedInformerFactory *fakeKoordinatorSharedInformerFactory
}

func (f *fakeExtendedHandle) ClientSet() clientset.Interface {
	return f.cs
}

func (f *fakeExtendedHandle) SnapshotSharedLister() framework.SharedLister {
	if f.sharedLister != nil {
		return f.sharedLister
	}
	return f.SnapshotSharedLister()
}

func (f *fakeExtendedHandle) KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory {
	if f.koordSharedInformerFactory != nil {
		return f.koordSharedInformerFactory
	}
	return f.KoordinatorSharedInformerFactory()
}

func fakeParallelizeUntil(handle frameworkext.ExtendedHandle) parallelizeUntilFunc {
	return func(ctx context.Context, pieces int, doWorkPiece workqueue.DoWorkPieceFunc) {
		for i := 0; i < pieces; i++ {
			doWorkPiece(i)
		}
	}
}

func TestNew(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		var v1beta2args v1beta2.ReservationArgs
		v1beta2.SetDefaults_ReservationArgs(&v1beta2args)
		var reservationArgs config.ReservationArgs
		err := v1beta2.Convert_v1beta2_ReservationArgs_To_config_ReservationArgs(&v1beta2args, &reservationArgs, nil)
		assert.NoError(t, err)
		reservationPluginConfig := scheduledconfig.PluginConfig{
			Name: Name,
			Args: &reservationArgs,
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
					reservationPluginConfig,
				}
			},
			schedulertesting.RegisterPreFilterPlugin(Name, proxyNew),
			schedulertesting.RegisterFilterPlugin(Name, proxyNew),
			schedulertesting.RegisterScorePlugin(Name, proxyNew, 10),
			schedulertesting.RegisterReservePlugin(Name, proxyNew),
			schedulertesting.RegisterPreBindPlugin(Name, proxyNew),
			schedulertesting.RegisterBindPlugin(Name, proxyNew),
			schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
			schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		}

		cs := kubefake.NewSimpleClientset()
		informerFactory := informers.NewSharedInformerFactory(cs, 0)
		snapshot := newFakeSharedLister(nil, nil, false)
		fh, err := schedulertesting.NewFramework(registeredPlugins, "koord-scheduler",
			runtime.WithClientSet(cs),
			runtime.WithInformerFactory(informerFactory),
			runtime.WithSnapshotSharedLister(snapshot),
		)
		assert.NoError(t, err)

		p, err := proxyNew(&reservationArgs, fh)
		assert.NoError(t, err)
		assert.NotNil(t, p)
		assert.Equal(t, Name, p.Name())
	})
}

func TestExtensions(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		p := &Plugin{}
		assert.Nil(t, nil, p.PreFilterExtensions())
		assert.Nil(t, nil, p.ScoreExtensions())
	})
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
	type args struct {
		cycleState *framework.CycleState
		pod        *corev1.Pod
	}
	type fields struct {
		lister *fakeReservationLister
	}
	tests := []struct {
		name   string
		args   args
		fields fields
		want   *framework.Status
	}{
		{
			name: "skip for non-reserve pod",
			args: args{
				cycleState: framework.NewCycleState(),
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "not-reserve",
					},
				},
			},
			want: nil,
		},
		{
			name: "get reservation error",
			args: args{
				cycleState: framework.NewCycleState(),
				pod:        reservePod,
			},
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{},
					getErr: map[string]bool{
						reservePod.Name: true,
					},
				},
			},
			want: framework.NewStatus(framework.Error, "cannot get reservation, err: get error"),
		},
		{
			name: "failed to validate reservation",
			args: args{
				cycleState: framework.NewCycleState(),
				pod:        reservePod,
			},
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						reservePod.Name: {
							ObjectMeta: metav1.ObjectMeta{
								Name: "invalid-reservation",
							},
						},
					},
				},
			},
			want: framework.NewStatus(framework.Error, "the reservation misses the template spec"),
		},
		{
			name: "validate reservation successfully",
			args: args{
				cycleState: framework.NewCycleState(),
				pod:        reservePod,
			},
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						reservePod.Name: r,
					},
				},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{lister: tt.fields.lister}
			got := p.PreFilter(context.TODO(), tt.args.cycleState, tt.args.pod)
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
	reservePodForNotSetNode := testGetReservePod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-0",
			Annotations: map[string]string{
				AnnotationReservationNode: testNode.Name,
			},
		},
	})
	reservePodForMatchedNode := testGetReservePod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-1",
			Annotations: map[string]string{
				AnnotationReservationNode: testNode.Name,
			},
		},
	})
	reservePodForNotMatchedNode := testGetReservePod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-1",
			Annotations: map[string]string{
				AnnotationReservationNode: "other-node",
			},
		},
	})
	type args struct {
		cycleState *framework.CycleState
		pod        *corev1.Pod
		nodeInfo   *framework.NodeInfo
	}
	tests := []struct {
		name string
		args args
		want *framework.Status
	}{
		{
			name: "skip for non-reserve pod",
			args: args{
				cycleState: framework.NewCycleState(),
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "not-reserve",
					},
				},
				nodeInfo: testNodeInfo,
			},
			want: nil,
		},
		{
			name: "failed for node is nil",
			args: args{
				cycleState: framework.NewCycleState(),
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "not-reserve",
					},
				},
				nodeInfo: nil,
			},
			want: framework.NewStatus(framework.Error, "node not found"),
		},
		{
			name: "skip for pod not set node",
			args: args{
				cycleState: framework.NewCycleState(),
				pod:        reservePodForNotSetNode,
				nodeInfo:   testNodeInfo,
			},
			want: nil,
		},
		{
			name: "filter pod successfully",
			args: args{
				cycleState: framework.NewCycleState(),
				pod:        reservePodForMatchedNode,
				nodeInfo:   testNodeInfo,
			},
			want: nil,
		},
		{
			name: "failed for node does not matches the pod",
			args: args{
				cycleState: framework.NewCycleState(),
				pod:        reservePodForNotMatchedNode,
				nodeInfo:   testNodeInfo,
			},
			want: framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonNodeNotMatchReservation),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			got := p.Filter(context.TODO(), tt.args.cycleState, tt.args.pod, tt.args.nodeInfo)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPostFilter(t *testing.T) {
	reservePod := testGetReservePod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-0",
		},
	})
	reservePodNoName := testGetReservePod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-no-name",
		},
	})
	delete(reservePodNoName.Annotations, AnnotationReservationName)
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
	type args struct {
		pod                   *corev1.Pod
		filteredNodeStatusMap framework.NodeToStatusMap
	}
	type fields struct {
		lister *fakeReservationLister
		client *fakeReservationClient
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *framework.PostFilterResult
		want1  *framework.Status
	}{
		{
			name: "not reserve pod",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "not-reserve",
					},
				},
				filteredNodeStatusMap: framework.NodeToStatusMap{},
			},
			want:  nil,
			want1: framework.NewStatus(framework.Unschedulable),
		},
		{
			name: "failed to get reservation",
			args: args{
				pod: reservePod,
			},
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{},
					getErr: map[string]bool{
						reservePod.Name: true,
					},
				},
			},
			want:  nil,
			want1: framework.NewStatus(framework.Unschedulable),
		},
		{
			name: "failed to get reservation name",
			args: args{
				pod: reservePodNoName,
			},
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{},
					getErr: map[string]bool{
						"": true,
					},
				},
			},
			want:  nil,
			want1: framework.NewStatus(framework.Unschedulable),
		},
		{
			name: "failed to update status",
			args: args{
				pod: reservePod,
			},
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						r.Name: r,
					},
				},
				client: &fakeReservationClient{
					updateStatusErr: map[string]bool{
						r.Name: true,
					},
				},
			},
			want:  nil,
			want1: framework.NewStatus(framework.Unschedulable),
		},
		{
			name: "update unschedulable status successfully",
			args: args{
				pod: reservePod,
				filteredNodeStatusMap: framework.NodeToStatusMap{
					Name: framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrReasonNodeNotMatchReservation),
				},
			},
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						r.Name: r,
					},
				},
				client: &fakeReservationClient{},
			},
			want:  nil,
			want1: framework.NewStatus(framework.Unschedulable),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				lister: tt.fields.lister,
				client: tt.fields.client,
			}
			if tt.fields.lister != nil && tt.fields.client != nil {
				tt.fields.client.lister = tt.fields.lister
			}
			got, got1 := p.PostFilter(context.TODO(), nil, tt.args.pod, tt.args.filteredNodeStatusMap)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestScore(t *testing.T) {
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
	testNodeNameNotMatched := "test-node-1"
	rScheduled := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-1",
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
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: testNodeName,
		},
	}
	rScheduled1 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-2",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-2",
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					Object: &corev1.ObjectReference{
						Name: "test-pod-2",
					},
				},
			},
			TTL: &metav1.Duration{Duration: 30 * time.Minute},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: "other-node",
		},
	}
	stateSkip := framework.NewCycleState()
	stateSkip.Write(preFilterStateKey, &stateData{
		skip: true,
	})
	stateForMatch := framework.NewCycleState()
	stateForMatch.Write(preFilterStateKey, &stateData{
		matchedCache: newAvailableCache(rScheduled, rScheduled1),
	})
	type args struct {
		cycleState *framework.CycleState
		pod        *corev1.Pod
		nodeName   string
	}
	tests := []struct {
		name  string
		args  args
		want  int64
		want1 *framework.Status
	}{
		{
			name: "skip for reserve pod",
			args: args{
				pod: reservePod,
			},
			want:  framework.MinNodeScore,
			want1: nil,
		},
		{
			name: "no reservation in cluster",
			args: args{
				cycleState: framework.NewCycleState(),
				pod:        normalPod,
			},
			want:  framework.MinNodeScore,
			want1: nil,
		},
		{
			name: "state skip",
			args: args{
				cycleState: stateSkip,
				pod:        normalPod,
			},
			want:  framework.MinNodeScore,
			want1: nil,
		},
		{
			name: "no reservation matched on the node",
			args: args{
				cycleState: stateForMatch,
				pod:        normalPod,
				nodeName:   testNodeNameNotMatched,
			},
			want:  framework.MinNodeScore,
			want1: nil,
		},
		{
			name: "reservation matched",
			args: args{
				cycleState: stateForMatch,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
			want:  framework.MaxNodeScore,
			want1: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			got, got1 := p.Score(context.TODO(), tt.args.cycleState, tt.args.pod, tt.args.nodeName)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestReserve(t *testing.T) {
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
	normalPod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-2",
		},
	}
	testNodeName := "test-node-0"
	testNodeNameNotMatched := "test-node-1"
	rScheduled := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-1",
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
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: testNodeName,
		},
	}
	rScheduled1 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-2",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod-2",
				},
			},
			Owners: []schedulingv1alpha1.ReservationOwner{
				{
					Object: &corev1.ObjectReference{
						Name: "test-pod-2",
					},
				},
			},
			TTL: &metav1.Duration{Duration: 30 * time.Minute},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: "other-node",
		},
	}
	rAllocated := rScheduled.DeepCopy()
	setReservationAllocated(rAllocated, normalPod)
	stateSkip := framework.NewCycleState()
	stateSkip.Write(preFilterStateKey, &stateData{
		skip: true,
	})
	stateForMatch := framework.NewCycleState()
	stateForMatch.Write(preFilterStateKey, &stateData{
		matchedCache: newAvailableCache(rScheduled, rScheduled1),
	})
	stateForMatch1 := stateForMatch.Clone()
	cacheNotActive := newReservationCache()
	cacheNotActive.AddToFailed(rScheduled)
	cacheMatched := newReservationCache()
	cacheMatched.AddToActive(rScheduled)
	type args struct {
		cycleState *framework.CycleState
		pod        *corev1.Pod
		nodeName   string
	}
	type fields struct {
		reservationCache *reservationCache
	}
	tests := []struct {
		name      string
		fields    fields
		args      args
		want      *framework.Status
		wantField *schedulingv1alpha1.Reservation
	}{
		{
			name: "skip for reserve pod",
			fields: fields{
				reservationCache: newReservationCache(),
			},
			args: args{
				pod: reservePod,
			},
			want: nil,
		},
		{
			name: "state skip",
			fields: fields{
				reservationCache: newReservationCache(),
			},
			args: args{
				cycleState: stateSkip,
				pod:        normalPod,
			},
			want: nil,
		},
		{
			name: "no reservation matched on the node",
			fields: fields{
				reservationCache: newReservationCache(),
			},
			args: args{
				cycleState: stateForMatch,
				pod:        normalPod,
				nodeName:   testNodeNameNotMatched,
			},
			want: nil,
		},
		{
			name: "reservation not in active cache",
			fields: fields{
				reservationCache: cacheNotActive,
			},
			args: args{
				cycleState: stateForMatch,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
			want: framework.NewStatus(framework.Error, ErrReasonReservationFailedToReserve),
		},
		{
			name: "reservation matched",
			fields: fields{
				reservationCache: cacheMatched,
			},
			args: args{
				cycleState: stateForMatch,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
			want:      nil,
			wantField: rAllocated,
		},
		{
			name: "reservation not matched anymore",
			fields: fields{
				reservationCache: cacheMatched,
			},
			args: args{
				cycleState: stateForMatch1,
				pod:        normalPod1,
				nodeName:   testNodeName,
			},
			want: framework.NewStatus(framework.Error, ErrReasonReservationFailedToReserve),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{reservationCache: tt.fields.reservationCache}
			got := p.Reserve(context.TODO(), tt.args.cycleState, tt.args.pod, tt.args.nodeName)
			assert.Equal(t, tt.want, got)
			if tt.args.cycleState != nil {
				state := getPreFilterState(tt.args.cycleState)
				assert.Equal(t, tt.wantField, state.assumed)
			}
		})
	}
}

func TestUnreserve(t *testing.T) {
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
	rScheduled := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-1",
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
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: testNodeName,
		},
	}
	rAllocated := rScheduled.DeepCopy()
	setReservationAllocated(rAllocated, normalPod)
	stateSkip := framework.NewCycleState()
	stateSkip.Write(preFilterStateKey, &stateData{
		skip: true,
	})
	stateNoAssumed := framework.NewCycleState()
	stateNoAssumed.Write(preFilterStateKey, &stateData{
		assumed: nil,
	})
	stateAssumed := framework.NewCycleState()
	stateAssumed.Write(preFilterStateKey, &stateData{
		assumed: rAllocated,
	})
	cacheNotActive := newReservationCache()
	cacheNotActive.AddToFailed(rScheduled)
	cacheMatched := newReservationCache()
	cacheMatched.AddToActive(rScheduled)
	type args struct {
		cycleState *framework.CycleState
		pod        *corev1.Pod
		nodeName   string
	}
	type fields struct {
		reservationCache *reservationCache
	}
	tests := []struct {
		name      string
		fields    fields
		args      args
		wantField *schedulingv1alpha1.Reservation
	}{
		{
			name: "skip for reserve pod",
			args: args{
				pod: reservePod,
			},
		},
		{
			name: "state skip",
			args: args{
				cycleState: stateSkip,
				pod:        normalPod,
			},
		},
		{
			name: "state no assumed",
			args: args{
				cycleState: stateNoAssumed,
				pod:        normalPod,
			},
		},
		{
			name: "not in active cache",
			fields: fields{
				reservationCache: cacheNotActive,
			},
			args: args{
				cycleState: stateAssumed,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
		},
		{
			name: "state clean allocated successfully",
			fields: fields{
				reservationCache: cacheMatched,
			},
			args: args{
				cycleState: stateAssumed,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{reservationCache: tt.fields.reservationCache}
			p.Unreserve(context.TODO(), tt.args.cycleState, tt.args.pod, tt.args.nodeName)
			if tt.args.cycleState != nil {
				state := getPreFilterState(tt.args.cycleState)
				assert.Equal(t, tt.wantField, state.assumed)
			}
		})
	}
}

func TestPreBind(t *testing.T) {
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
	rScheduled := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-1",
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
			Phase:    schedulingv1alpha1.ReasonReservationAvailable,
			NodeName: testNodeName,
		},
	}
	rAllocated := rScheduled.DeepCopy()
	setReservationAllocated(rAllocated, normalPod)
	rExpired := rScheduled.DeepCopy()
	setReservationExpired(rExpired)
	stateSkip := framework.NewCycleState()
	stateSkip.Write(preFilterStateKey, &stateData{
		skip: true,
	})
	stateNoAssumed := framework.NewCycleState()
	stateNoAssumed.Write(preFilterStateKey, &stateData{
		assumed: nil,
	})
	stateAssumed := framework.NewCycleState()
	stateAssumed.Write(preFilterStateKey, &stateData{
		assumed: rAllocated,
	})
	type args struct {
		cycleState *framework.CycleState
		pod        *corev1.Pod
		nodeName   string
	}
	type fields struct {
		lister *fakeReservationLister
		client *fakeReservationClient
		handle frameworkext.ExtendedHandle
	}
	tests := []struct {
		name   string
		args   args
		fields fields
		want   *framework.Status
	}{
		{
			name: "skip for reserve pod",
			args: args{
				pod: reservePod,
			},
			want: nil,
		},
		{
			name: "state skip",
			args: args{
				cycleState: stateSkip,
				pod:        normalPod,
			},
			want: nil,
		},
		{
			name: "state no assumed",
			args: args{
				cycleState: stateNoAssumed,
				pod:        normalPod,
			},
			want: nil,
		},
		{
			name: "failed to get",
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{},
					getErr: map[string]bool{
						rScheduled.Name: true,
					},
				},
			},
			args: args{
				cycleState: stateAssumed,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
			want: framework.NewStatus(framework.Error, "get error"),
		},
		{
			name: "get expired",
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						rExpired.Name: rExpired,
					},
				},
			},
			args: args{
				cycleState: stateAssumed,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
			want: framework.NewStatus(framework.Error, ErrReasonReservationFailed),
		},
		{
			name: "failed to update status",
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						rScheduled.Name: rScheduled,
					},
				},
				client: &fakeReservationClient{
					updateStatusErr: map[string]bool{
						rScheduled.Name: true,
					},
				},
			},
			args: args{
				cycleState: stateAssumed,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
			want: framework.NewStatus(framework.Error, "updateStatus error"),
		},
		{
			name: "failed to patch pod",
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						rScheduled.Name: rScheduled,
					},
				},
				client: &fakeReservationClient{},
				handle: &fakeExtendedHandle{cs: kubefake.NewSimpleClientset()},
			},
			args: args{
				cycleState: stateAssumed,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
			want: framework.NewStatus(framework.Error, fmt.Sprintf("pods \"%s\" not found", normalPod.Name)),
		},
		{
			name: "pre-bind pod successfully",
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						rScheduled.Name: rScheduled,
					},
				},
				client: &fakeReservationClient{},
				handle: &fakeExtendedHandle{cs: kubefake.NewSimpleClientset(normalPod)},
			},
			args: args{
				cycleState: stateAssumed,
				pod:        normalPod,
				nodeName:   testNodeName,
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				lister: tt.fields.lister,
				client: tt.fields.client,
				handle: tt.fields.handle,
			}
			if tt.fields.lister != nil && tt.fields.client != nil {
				tt.fields.client.lister = tt.fields.lister
			}
			got := p.PreBind(context.TODO(), tt.args.cycleState, tt.args.pod, tt.args.nodeName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBind(t *testing.T) {
	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-1",
		},
	}
	reservePod := testGetReservePod(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "reserve-pod-0",
		},
	})
	testNodeName := "test-node-0"
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
						Name: "test-pod-1",
					},
				},
			},
			TTL: &metav1.Duration{Duration: 30 * time.Minute},
		},
	}
	rFailed := r.DeepCopy()
	rFailed.Status = schedulingv1alpha1.ReservationStatus{
		Phase: schedulingv1alpha1.ReservationFailed,
	}
	type args struct {
		pod      *corev1.Pod
		nodeName string
	}
	type fields struct {
		lister *fakeReservationLister
		client *fakeReservationClient
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *framework.Status
	}{
		{
			name: "skip for non-reserve pod",
			args: args{
				pod: normalPod,
			},
			want: framework.NewStatus(framework.Skip, SkipReasonNotReservation),
		},
		{
			name: "failed to get reservation",
			fields: fields{
				lister: &fakeReservationLister{
					getErr: map[string]bool{
						r.Name: true,
					},
				},
			},
			args: args{
				pod: reservePod,
			},
			want: framework.NewStatus(framework.Error, "failed to bind reservation, err: get error"),
		},
		{
			name: "get failed reservation",
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						rFailed.Name: rFailed,
					},
				},
			},
			args: args{
				pod:      reservePod,
				nodeName: testNodeName,
			},
			want: framework.NewStatus(framework.Error, "failed to bind reservation, err: "+ErrReasonReservationFailed),
		},
		{
			name: "failed to update status",
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						r.Name: r,
					},
				},
				client: &fakeReservationClient{
					updateStatusErr: map[string]bool{
						r.Name: true,
					},
				},
			},
			args: args{
				pod:      reservePod,
				nodeName: testNodeName,
			},
			want: framework.NewStatus(framework.Error, "failed to bind reservation, err: updateStatus error"),
		},
		{
			name: "bind reservation successfully",
			fields: fields{
				lister: &fakeReservationLister{
					reservations: map[string]*schedulingv1alpha1.Reservation{
						r.Name: r,
					},
				},
				client: &fakeReservationClient{},
			},
			args: args{
				pod:      reservePod,
				nodeName: testNodeName,
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				lister:           tt.fields.lister,
				client:           tt.fields.client,
				reservationCache: newReservationCache(),
			}
			if tt.fields.lister != nil && tt.fields.client != nil {
				tt.fields.client.lister = tt.fields.lister
			}
			got := p.Bind(context.TODO(), nil, tt.args.pod, tt.args.nodeName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func testGetReservePod(pod *corev1.Pod) *corev1.Pod {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[AnnotationReservePod] = "true"
	pod.Annotations[AnnotationReservationName] = pod.Name
	return pod
}
