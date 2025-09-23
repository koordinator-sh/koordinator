package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/events"
	"k8s.io/component-base/logs"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkfake "k8s.io/kubernetes/pkg/scheduler/framework/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/noderesources"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/pointer"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
)

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

func NewFakeExtendedFramework(t *testing.T, nodes []*corev1.Node, existingPods []*corev1.Pod, existingNominatedPods []*corev1.Pod, pluginFunc schedulertesting.RegisterPluginFunc) frameworkext.FrameworkExtender {
	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithServicesEngine(services.NewEngine(gin.New())),
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	assert.NoError(t, err)
	assert.NotNil(t, extenderFactory)
	assert.Equal(t, koordClientSet, extenderFactory.KoordinatorClientSet())
	assert.Equal(t, koordSharedInformerFactory, extenderFactory.KoordinatorSharedInformerFactory())
	if pluginFunc == nil {
		pluginFunc = schedulertesting.RegisterFilterPlugin("FakeFitPlugin", func(configuration runtime.Object, f framework.Handle) (framework.Plugin, error) {
			return &FakeFitPlugin{}, nil
		})
	}
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		pluginFunc,
	}
	fakeClient := kubefake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)

	var nodeInfos []*framework.NodeInfo
	for _, node := range nodes {
		nodeInfo := framework.NewNodeInfo()
		nodeInfo.SetNode(node)
		nodeInfos = append(nodeInfos, nodeInfo)
	}
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{NodeInfoLister: frameworkfake.NodeInfoLister(nodeInfos)}),
		frameworkruntime.WithClientSet(fakeClient),
		frameworkruntime.WithInformerFactory(sharedInformerFactory),
		frameworkruntime.WithPodNominator(NewPodNominator()),
		frameworkruntime.WithEventRecorder(events.NewFakeRecorder(1000)),
	)
	assert.NoError(t, err)

	podStore := sharedInformerFactory.Core().V1().Nodes().Informer().GetStore()
	for i := range existingPods {
		existingPod := existingPods[i]
		nodeInfo, _ := fh.SnapshotSharedLister().NodeInfos().Get(existingPod.Spec.NodeName)
		nodeInfo.AddPod(existingPod)
		_ = podStore.Add(existingPod)
		_, _ = fakeClient.CoreV1().Pods(existingPod.Namespace).Create(context.TODO(), existingPod, metav1.CreateOptions{})
	}
	logger := klog.FromContext(context.TODO())
	for i := range existingNominatedPods {
		existingNominatedPod := existingNominatedPods[i]
		podInfo, _ := framework.NewPodInfo(existingNominatedPod)
		fh.AddNominatedPod(logger, podInfo, &framework.NominatingInfo{
			NominatedNodeName: existingNominatedPod.Status.NominatedNodeName,
			NominatingMode:    framework.ModeOverride,
		})
		_ = podStore.Add(existingNominatedPod)
		_, _ = fakeClient.CoreV1().Pods(existingNominatedPod.Namespace).Create(context.TODO(), existingNominatedPod, metav1.CreateOptions{})
	}

	frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
	frameworkExtender.SetConfiguredPlugins(fh.ListPlugins())
	return frameworkExtender
}

func Test_preemptionEvaluatorImpl_preempt(t *testing.T) {
	highPriority := int32(1000)
	lowPriority := int32(1)
	gangName := "gangA"
	tests := []struct {
		name                  string
		triggerPod            *corev1.Pod
		gangSchedulingContext *GangSchedulingContext
		preFilterStatus       *framework.Status
		allWaitingPods        []*corev1.Pod
		allPendingPods        []*corev1.Pod
		nodes                 []*corev1.Node
		existingPods          []*corev1.Pod
		existingNominatedPods []*corev1.Pod
		filterPlugin          schedulertesting.RegisterPluginFunc
		wantPreemptionState   *JobPreemptionState
		wantPreemptMessage    string
		wantResult            *framework.PostFilterResult
		wantStatus            *framework.Status
		wantPossibleVictims   []schedulingv1alpha1.PossibleVictim
		wantVictims           []schedulingv1alpha1.PossibleVictim
		wantJobPods           []corev1.Pod
	}{
		{
			name: "already preempted",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "trigger-pod",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:         sets.New[string]("default/gangA", "default/gangB"),
				gangGroupID:       "default/gangA,default/gangB",
				failedMessage:     "failedMessage",
				preemptionMessage: "preemption already attempted by default/trigger-pod-1 with message",
			},
			preFilterStatus: framework.NewStatus(framework.Unschedulable, "failedMessage"),
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:                 "default/trigger-pod",
				Reason:                        ReasonAlreadyPreempted,
				Message:                       "preemption already attempted by default/trigger-pod-1 with message",
				ClearNominatedNodeFailedMsg:   map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{},
			},
			wantPreemptMessage: "preemption already attempted by default/trigger-pod-1 with message",
			wantResult:         nil,
			wantStatus:         framework.NewStatus(framework.Unschedulable, "preemption already attempted by default/trigger-pod-1 with message"),
		},
		{
			name: "no pending pods",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "trigger-pod",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
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
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:     sets.New[string]("default/gangA", "default/gangB"),
				gangGroupID:   "default/gangA,default/gangB",
				failedMessage: "failedMessage",
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			existingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-1",
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
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-2",
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
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-3",
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
						NodeName: "node-2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-4",
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
						NodeName: "node-2",
					},
				},
			},
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:                 "default/trigger-pod",
				PreemptorKey:                  "default/gangA,default/gangB",
				Reason:                        ReasonNoPendingPods,
				Message:                       "no pending pods",
				ClearNominatedNodeFailedMsg:   map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{},
			},
			wantPreemptMessage: "preemption already attempted by default/trigger-pod with message no pending pods",
			wantResult:         nil,
			wantStatus:         framework.NewStatus(framework.Unschedulable, ReasonNoPendingPods),
		},
		{
			name: "job not eligible due to preemption policy",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					PreemptionPolicy: (*corev1.PreemptionPolicy)(pointer.String(string(corev1.PreemptNever))),
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
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
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
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
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
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:     sets.New[string]("default/gangA", "default/gangB"),
				gangGroupID:   "default/gangA,default/gangB",
				failedMessage: "failedMessage",
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			existingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-1",
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
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-2",
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
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-3",
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
						NodeName: "node-2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-4",
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
						NodeName: "node-2",
					},
				},
			},
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:                 "default/pending-pod-1",
				PreemptorKey:                  "default/gangA,default/gangB",
				Reason:                        ReasonPreemptionPolicyNever,
				Message:                       ReasonPreemptionPolicyNever,
				ClearNominatedNodeFailedMsg:   map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{},
			},
			wantPreemptMessage: "preemption already attempted by default/pending-pod-1 with message not eligible due to preemptionPolicy=Never.",
			wantResult:         nil,
			wantStatus:         framework.NewStatus(framework.Unschedulable, ReasonPreemptionPolicyNever),
		},
		{
			name: "job not eligible due to terminating pod",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
					NominatedNodeName: "node-1",
				},
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:     sets.New[string]("default/gangA", "default/gangB"),
				gangGroupID:   "default/gangA,default/gangB",
				failedMessage: "failedMessage",
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			existingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "existing-pod-1",
						Namespace:         "default",
						DeletionTimestamp: &metav1.Time{},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.DisruptionTarget,
								Status: corev1.ConditionTrue,
								Reason: corev1.PodReasonPreemptionByScheduler,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-2",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-3",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-4",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-2",
					},
				},
			},
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:               "default/pending-pod-1",
				PreemptorKey:                "default/gangA,default/gangB",
				Reason:                      ReasonTerminatingVictimOnNominatedNode,
				Message:                     ReasonTerminatingVictimOnNominatedNode,
				ClearNominatedNodeFailedMsg: map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{
					"default/existing-pod-1": "node-1",
				},
			},
			wantPreemptMessage: "preemption already attempted by default/pending-pod-1 with message not eligible due to terminating pod on the nominated node.",
			wantResult:         nil,
			wantStatus:         framework.NewStatus(framework.Unschedulable, ReasonTerminatingVictimOnNominatedNode),
		},
		{
			name: "unschedulableAndUnResolvable",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
					NominatedNodeName: "node-1",
				},
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:     sets.New[string]("default/gangA", "default/gangB"),
				gangGroupID:   "default/gangA,default/gangB",
				failedMessage: "failedMessage",
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			existingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-1",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-2",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-3",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-4",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-2",
					},
				},
			},
			filterPlugin: schedulertesting.RegisterFilterPlugin("FakeFilter", schedulertesting.NewFakeFilterPlugin(
				map[string]framework.Code{
					"node-1": framework.UnschedulableAndUnresolvable,
					"node-2": framework.UnschedulableAndUnresolvable,
				},
			)),
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:                 "default/pending-pod-1",
				PreemptorKey:                  "default/gangA,default/gangB",
				Reason:                        ReasonPreemptionNotHelpful,
				Message:                       "0/2 nodes are available: 2 Preemption is not helpful for scheduling.",
				ClearNominatedNodeFailedMsg:   map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{},
			},
			wantPreemptMessage: "preemption already attempted by default/pending-pod-1 with message 0/2 nodes are available: 2 Preemption is not helpful for scheduling.",
			wantResult:         framework.NewPostFilterResultWithNominatedNode(""),
			wantStatus:         framework.NewStatus(framework.Unschedulable, "0/2 nodes are available: 2 Preemption is not helpful for scheduling."),
		},
		{
			name: "no potential victims",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
					NominatedNodeName: "node-1",
				},
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:     sets.New[string]("default/gangA", "default/gangB"),
				gangGroupID:   "default/gangA,default/gangB",
				failedMessage: "failedMessage",
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			existingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-1",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-2",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-3",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-4",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-2",
					},
				},
			},
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:                 "default/pending-pod-1",
				PreemptorKey:                  "default/gangA,default/gangB",
				Reason:                        ReasonPreemptionNotHelpful,
				Message:                       "0/2 nodes are available: 2 no potential victims.",
				ClearNominatedNodeFailedMsg:   map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{},
			},
			wantPreemptMessage: "preemption already attempted by default/pending-pod-1 with message 0/2 nodes are available: 2 no potential victims.",
			wantResult:         framework.NewPostFilterResultWithNominatedNode(""),
			wantStatus:         framework.NewStatus(framework.Unschedulable, "0/2 nodes are available: 2 no potential victims."),
		},
		{
			name: "preempt success",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
					NominatedNodeName: "node-1",
				},
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:     sets.New[string]("default/gangA", "default/gangB"),
				gangGroupID:   "default/gangA,default/gangB",
				failedMessage: "failedMessage",
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			existingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-1",
						Namespace: "default",
						UID:       "existing-pod-1",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-2",
						Namespace: "default",
						UID:       "existing-pod-2",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-3",
						Namespace: "default",
						UID:       "existing-pod-3",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-4",
						Namespace: "default",
						UID:       "existing-pod-4",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-2",
					},
				},
			},
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:                 "default/pending-pod-1",
				PreemptorKey:                  "default/gangA,default/gangB",
				Reason:                        ReasonTriggerPodPreemptSuccess,
				Message:                       "preempt success, alreadyWaitingForBound: 0/2",
				ClearNominatedNodeFailedMsg:   map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{},
				statusMap:                     map[string]*framework.Status{},
				PodToNominatedNode: map[string]string{
					"default/pending-pod-1": "node-1",
					"default/pending-pod-2": "node-1",
				},
			},
			wantPreemptMessage: "preemption already attempted by default/pending-pod-1 with message preempt success, alreadyWaitingForBound: 0/2",
			wantResult:         framework.NewPostFilterResultWithNominatedNode("node-1"),
			wantStatus:         framework.NewStatus(framework.Success),
			wantPossibleVictims: []schedulingv1alpha1.PossibleVictim{
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-1", Namespace: "default"}},
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-2", Namespace: "default"}},
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-3", Namespace: "default"}},
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-4", Namespace: "default"}},
			},
			wantVictims: []schedulingv1alpha1.PossibleVictim{
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-1", Namespace: "default"}},
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-2", Namespace: "default"}},
			},
			wantJobPods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
			},
		},
		{
			name: "preempt success with one waiting pod",
			triggerPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod-1",
					Namespace: "default",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: gangName,
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32(highPriority),
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
					NominatedNodeName: "node-1",
				},
			},
			allPendingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-2",
					},
				},
			},
			allWaitingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						NodeName: "node-2",
						Priority: pointer.Int32(highPriority),
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
			},
			gangSchedulingContext: &GangSchedulingContext{
				gangGroup:     sets.New[string]("default/gangA", "default/gangB"),
				gangGroupID:   "default/gangA,default/gangB",
				failedMessage: "failedMessage",
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:  resource.MustParse("32"),
							corev1.ResourcePods: resource.MustParse("110"),
						},
					},
				},
			},
			existingPods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-1",
						Namespace: "default",
						UID:       "existing-pod-1",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-2",
						Namespace: "default",
						UID:       "existing-pod-2",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-pod-4",
						Namespace: "default",
						UID:       "existing-pod-4",
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(lowPriority),
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU: resource.MustParse("16"),
									},
								},
							},
						},
						NodeName: "node-2",
					},
				},
			},
			wantPreemptionState: &JobPreemptionState{
				TriggerPodKey:                 "default/pending-pod-1",
				PreemptorKey:                  "default/gangA,default/gangB",
				Reason:                        ReasonTriggerPodPreemptSuccess,
				Message:                       "preempt success, alreadyWaitingForBound: 1/2",
				ClearNominatedNodeFailedMsg:   map[string]string{},
				TerminatingPodOnNominatedNode: map[string]string{},
				statusMap:                     map[string]*framework.Status{},
				PodToNominatedNode: map[string]string{
					"default/pending-pod-1": "node-1",
				},
			},
			wantPreemptMessage: "preemption already attempted by default/pending-pod-1 with message preempt success, alreadyWaitingForBound: 1/2",
			wantResult:         framework.NewPostFilterResultWithNominatedNode("node-1"),
			wantStatus:         framework.NewStatus(framework.Success),
			wantPossibleVictims: []schedulingv1alpha1.PossibleVictim{
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-1", Namespace: "default"}},
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-2", Namespace: "default"}},
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-4", Namespace: "default"}},
			},
			wantVictims: []schedulingv1alpha1.PossibleVictim{
				{NamespacedName: schedulingv1alpha1.NamespacedName{Name: "existing-pod-2", Namespace: "default"}},
			},
			wantJobPods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-1",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pod-2",
						Namespace: "default",
						Labels: map[string]string{
							v1alpha1.PodGroupLabel: gangName,
						},
					},
					Spec: corev1.PodSpec{
						Priority: pointer.Int32(highPriority),
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
						NominatedNodeName: "node-2",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extendedFramework := NewFakeExtendedFramework(t, tt.nodes, tt.existingPods, tt.existingNominatedPods, tt.filterPlugin)
			podStore := extendedFramework.SharedInformerFactory().Core().V1().Pods().Informer().GetStore()
			logger := klog.FromContext(context.TODO())
			gangSchedulingContextHolder := &GangSchedulingContextHolder{gangSchedulingContext: tt.gangSchedulingContext}
			gangCache := NewGangCache(nil, nil, nil, nil, nil)
			for i := range tt.allPendingPods {
				pod := tt.allPendingPods[i]
				gangCache.onPodAdd(pod)
				err := podStore.Add(pod)
				assert.NoError(t, err)
				_, _ = extendedFramework.ClientSet().CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				if pod.Status.NominatedNodeName != "" {
					podInfo, _ := framework.NewPodInfo(pod)
					extendedFramework.AddNominatedPod(logger, podInfo, &framework.NominatingInfo{
						NominatingMode:    framework.ModeOverride,
						NominatedNodeName: pod.Status.NominatedNodeName,
					})
				}
			}
			for i := range tt.allWaitingPods {
				pod := tt.allWaitingPods[i]
				gangName := util.GetId(pod.Namespace, util.GetGangNameByPod(pod))
				gang := gangCache.getGangFromCacheByGangId(gangName, true)
				gang.addAssumedPod(pod)
				nodeInfo, _ := extendedFramework.SnapshotSharedLister().NodeInfos().Get(pod.Spec.NodeName)
				nodeInfo.AddPod(pod)
				pod = pod.DeepCopy()
				pod.Spec.NodeName = ""
				err := podStore.Add(pod)
				assert.NoError(t, err)
				_, _ = extendedFramework.ClientSet().CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
			}

			ev := NewPreemptionEvaluator(extendedFramework, gangCache, gangSchedulingContextHolder).(*preemptionEvaluatorImpl)
			preemptionState := &JobPreemptionState{
				TerminatingPodOnNominatedNode: map[string]string{},
				ClearNominatedNodeFailedMsg:   map[string]string{},
			}

			ctx := context.Background()
			cycleState := framework.NewCycleState()
			m := framework.NodeToStatusMap{}

			if tt.preFilterStatus.IsSuccess() {
				for _, node := range tt.nodes {
					nodeInfo, _ := extendedFramework.SnapshotSharedLister().NodeInfos().Get(node.Name)
					status := extendedFramework.RunFilterPluginsWithNominatedPods(ctx, cycleState, tt.triggerPod, nodeInfo)
					m[node.Name] = status
				}
			}
			if tt.gangSchedulingContext != nil {
				tt.gangSchedulingContext.triggerPod = tt.triggerPod
			}

			ctx = contextWithJobPreemptionState(context.Background(), preemptionState)
			gotResult, gotStatus := ev.preempt(ctx, cycleState, tt.triggerPod, m)
			assert.Equal(t, tt.wantResult, gotResult)
			assert.Equal(t, tt.wantStatus, gotStatus)
			sort.Slice(preemptionState.allPendingPods, func(i, j int) bool {
				return preemptionState.allPendingPods[i].Name < preemptionState.allPendingPods[j].Name
			})
			sort.Slice(preemptionState.allWaitingPods, func(i, j int) bool {
				return preemptionState.allWaitingPods[i].Name < preemptionState.allWaitingPods[j].Name
			})
			assert.Equal(t, tt.allPendingPods, preemptionState.allPendingPods)
			assert.Equal(t, tt.allWaitingPods, preemptionState.allWaitingPods)
			preemptionState.allPendingPods = nil
			preemptionState.allWaitingPods = nil
			preemptionState.allPods = nil
			preemptionState.DurationOfCycleStateClone = metav1.Duration{}
			preemptionState.DurationOfNodeInfoClone = metav1.Duration{}
			preemptionState.DurationOfRemovePossibleVictims = metav1.Duration{}
			preemptionState.DurationOfPlaceToSchedulePods = metav1.Duration{}
			preemptionState.DurationOfSelectVictimsOnNode = metav1.Duration{}
			preemptionState.DurationOfPrepareCandidates = metav1.Duration{}
			preemptionState.DurationOfMakeNomination = metav1.Duration{}
			preemptionState.DurationOfCancelNomination = metav1.Duration{}
			var gotPossibleVictims []schedulingv1alpha1.PossibleVictim
			for _, possibleVictims := range preemptionState.possibleVictims {
				for _, victim := range possibleVictims {
					gotPossibleVictims = append(gotPossibleVictims, schedulingv1alpha1.PossibleVictim{
						NamespacedName: schedulingv1alpha1.NamespacedName{
							Name:      victim.Pod.Name,
							Namespace: victim.Pod.Namespace,
						},
					})
				}

			}
			sort.Slice(gotPossibleVictims, func(i, j int) bool {
				return gotPossibleVictims[i].NamespacedName.Name < gotPossibleVictims[j].NamespacedName.Name
			})
			assert.Equal(t, tt.wantPossibleVictims, gotPossibleVictims)
			preemptionState.possibleVictims = nil
			var gotVictims []schedulingv1alpha1.PossibleVictim
			for _, victims := range preemptionState.victims {
				for _, victim := range victims {
					gotVictims = append(gotVictims, schedulingv1alpha1.PossibleVictim{
						NamespacedName: schedulingv1alpha1.NamespacedName{
							Name:      victim.Name,
							Namespace: victim.Namespace,
						},
					})
				}
			}
			sort.Slice(gotVictims, func(i, j int) bool {
				return gotVictims[i].NamespacedName.Name < gotVictims[j].NamespacedName.Name
			})
			assert.Equal(t, tt.wantVictims, gotVictims)
			for _, victim := range gotVictims {
				_, err := extendedFramework.ClientSet().CoreV1().Pods(victim.Namespace).Get(context.TODO(), victim.Name, metav1.GetOptions{})
				assert.True(t, errors.IsNotFound(err))
			}
			preemptionState.victims = nil
			assert.Equal(t, tt.wantPreemptionState, preemptionState)
			if tt.gangSchedulingContext != nil {
				assert.Equal(t, tt.wantPreemptMessage, tt.gangSchedulingContext.preemptionMessage)
			}
			if len(tt.wantJobPods) > 0 {
				gotJobPods, _ := extendedFramework.ClientSet().CoreV1().Pods(tt.wantJobPods[0].Namespace).List(context.TODO(), metav1.ListOptions{
					LabelSelector: fmt.Sprintf(v1alpha1.PodGroupLabel + "=" + gangName),
				})
				sort.Slice(gotJobPods.Items, func(i, j int) bool {
					return gotJobPods.Items[i].Name < gotJobPods.Items[j].Name
				})
				assert.Equal(t, tt.wantJobPods, gotJobPods.Items)
			}
		})
	}
}

func TestJobPreemptionState_addMoreDetailForStateToMarshal(t *testing.T) {
	tests := []struct {
		name            string
		preemptionState *JobPreemptionState
		wantJSONStr     string
	}{
		{
			name: "normal flow",
			preemptionState: &JobPreemptionState{
				TriggerPodKey: "triggerPodKey",
				PreemptorKey:  "preemptorKey",
				allPendingPods: []*corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pendingPod1",
							Namespace: "default",
						},
					},
				},
				allWaitingPods: []*corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "waitingPod1",
							Namespace: "default",
						},
					},
				},
				allPods: []*corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pendingPod1",
							Namespace: "default",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "waitingPod1",
							Namespace: "default",
						},
					},
				},
				Reason:  "failedMessage",
				Message: "message",
				TerminatingPodOnNominatedNode: map[string]string{
					"node1": "pod1",
				},
				DurationOfNodeInfoClone:   metav1.Duration{},
				DurationOfCycleStateClone: metav1.Duration{},
				PodToNominatedNode: map[string]string{
					"pendingPod1": "node1",
					"waitingPod1": "node2",
				},
				selectVictimError:           fmt.Errorf("selectVictimError"),
				ClearNominatedNodeFailedMsg: map[string]string{"node1": "clearNominatedNodeFailedMsg1"},
				possibleVictims: map[string][]*framework.PodInfo{
					"node1": {
						{
							Pod: &corev1.Pod{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "pendingPod1",
									Namespace: "default",
								},
							},
						},
					},
				},
				unschedulablePods: []*corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "unschedulablePod1",
							Namespace: "default",
						},
					},
				},
				statusMap: map[string]*framework.Status{
					"node2": framework.NewStatus(framework.Unschedulable, "unschedulable"),
					"node1": framework.NewStatus(framework.Unschedulable, "unschedulable"),
				},
				victims: map[string][]*corev1.Pod{
					"node1": {
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "pendingPod1",
								Namespace: "default",
							},
						},
					},
				},
			},
			wantJSONStr: `{"TriggerPodKey":"triggerPodKey","preemptorKey":"preemptorKey","reason":"failedMessage","message":"message","terminatingPodOnNominatedNode":{"node1":"pod1"},"durationOfNodeInfoClone":"0s","durationOfCycleStateClone":"0s","durationOfRemovePossibleVictims":"0s","podToNominatedNode":{"pendingPod1":"node1","waitingPod1":"node2"},"durationOfPlaceToSchedulePods":"0s","durationOfSelectVictimsOnNode":"0s","durationOfPrepareCandidates":"0s","clearNominatedNodeFailedMsg":{"node1":"clearNominatedNodeFailedMsg1"},"durationOfMakeNomination":"0s","durationOfCancelNomination":"0s","possibleVictims":[{"nodeName":"node1","possibleVictims":[{"namespace":"default","name":"pendingPod1"}]}],"unschedulablePodsNumber":1,"nodeFailedDetail":[{"nodeName":"node1","reason":"unschedulable"},{"nodeName":"node2","reason":"unschedulable"}],"selectVictimError":"selectVictimError","victims":[{"nodeName":"node1","possibleVictims":[{"namespace":"default","name":"pendingPod1"}]}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _ = logs.GlogSetter("6")
			defer func() {
				_, _ = logs.GlogSetter("4")
			}()
			tt.preemptionState.addMoreDetailForStateToMarshal()
			jsonStr, err := json.Marshal(tt.preemptionState)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantJSONStr, string(jsonStr))
		})
	}
}
