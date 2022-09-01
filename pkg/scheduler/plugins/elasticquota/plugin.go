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

package elasticquota

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/listers/core/v1"
	policylisters "k8s.io/client-go/listers/policy/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/util"
	schedulerv1alpha1 "sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned"
	"sigs.k8s.io/scheduler-plugins/pkg/generated/informers/externalversions"
	"sigs.k8s.io/scheduler-plugins/pkg/generated/listers/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

const (
	Name             = "ElasticQuota"
	RevokePodCycle   = time.Duration(1) * time.Second
	SnapshotStateKey = "ElasticQuotaSnapshot"
)

type Plugin struct {
	sync.RWMutex
	handle      framework.Handle
	pluginArgs  *config.ElasticQuotaArgs
	quotaLister v1alpha1.ElasticQuotaLister
	podLister   v1.PodLister
	pdbLister   policylisters.PodDisruptionBudgetLister
	nodeLister  v1.NodeLister
	//only used in OnNodeAdd,in case Recover and normal Watch double call OnNodeAdd
	nodeResourceMapLock sync.Mutex
	nodeResourceMap     map[string]struct{}
	groupQuotaManager   *core.GroupQuotaManager
	// quotaOverUsedRevokeController's add/erase pod only in Reserve/ Unreserve
	quotaOverUsedRevokeController *QuotaOverUsedRevokeController
	// elasticQuotaController only used to update elastic quota crd
	elasticQuotaController *Controller
	// PreFilter, refresh elasticQuotaInfos' runtime/used according to groupQuotaManager, then write snapshot.
	// Reserve/ Unreserve/ OnPodAdd/ OnPodUpdate/ OnPodDelete, update the corresponding SimpleElasticQuotaInfo's pods.
	elasticQuotaInfos SimpleElasticQuotaInfos
}

// SnapshotState stores the snapshot of elasticQuotaInfos.
type SnapshotState struct {
	elasticQuotaInfos SimpleElasticQuotaInfos
}

// Clone the ElasticQuotaSnapshot state.
func (s *SnapshotState) Clone() framework.StateData {
	return &SnapshotState{
		elasticQuotaInfos: s.elasticQuotaInfos.clone(),
	}
}

var (
	_ framework.PreFilterPlugin  = &Plugin{}
	_ framework.PostFilterPlugin = &Plugin{}
	_ framework.ReservePlugin    = &Plugin{}
)

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*config.ElasticQuotaArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type GangSchedulingArgs, got %T", args)
	}
	if err := validation.ValidateElasticQuotaArgs(pluginArgs); err != nil {
		return nil, err
	}

	client, ok := handle.(versioned.Interface)
	if !ok {
		client = versioned.NewForConfigOrDie(handle.KubeConfig())
	}
	scheSharedInformerFactory := externalversions.NewSharedInformerFactory(client, 0)
	elasticQuotaInformer := scheSharedInformerFactory.Scheduling().V1alpha1().ElasticQuotas()

	elasticQuota := &Plugin{
		handle:            handle,
		pluginArgs:        pluginArgs,
		podLister:         handle.SharedInformerFactory().Core().V1().Pods().Lister(),
		quotaLister:       elasticQuotaInformer.Lister(),
		pdbLister:         getPDBLister(handle.SharedInformerFactory()),
		nodeLister:        handle.SharedInformerFactory().Core().V1().Nodes().Lister(),
		groupQuotaManager: core.NewGroupQuotaManager(pluginArgs.SystemQuotaGroupMax, pluginArgs.DefaultQuotaGroupMax),
		nodeResourceMap:   make(map[string]struct{}),
		elasticQuotaInfos: NewSimpleElasticQuotaInfos(),
	}
	elasticQuota.groupQuotaManager.SetScaleMinQuotaEnabled(true)
	elasticQuota.quotaOverUsedRevokeController = NewQuotaOverUsedRevokeController(
		*elasticQuota.pluginArgs.ContinueOverUseCountTriggerEvict, elasticQuota.groupQuotaManager)

	podInformer := handle.SharedInformerFactory().Core().V1().Pods()
	podInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    elasticQuota.OnPodAdd,
			UpdateFunc: elasticQuota.OnPodUpdate,
			DeleteFunc: elasticQuota.OnPodDelete,
		},
	)

	nodeInformer := handle.SharedInformerFactory().Core().V1().Nodes().Informer()
	nodeInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    elasticQuota.OnNodeAdd,
			UpdateFunc: elasticQuota.OnNodeUpdate,
			DeleteFunc: elasticQuota.OnNodeDelete,
		})

	elasticQuotaInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    elasticQuota.OnQuotaAdd,
			UpdateFunc: elasticQuota.OnQuotaUpdate,
			DeleteFunc: elasticQuota.OnQuotaDelete,
		})

	elasticQuota.elasticQuotaController = NewElasticQuotaController(kubernetes.NewForConfigOrDie(handle.KubeConfig()),
		elasticQuotaInformer.Lister(), client, elasticQuota.groupQuotaManager)
	ctx := context.TODO()
	scheSharedInformerFactory.Start(ctx.Done())
	handle.SharedInformerFactory().Start(ctx.Done())

	scheSharedInformerFactory.WaitForCacheSync(ctx.Done())
	handle.SharedInformerFactory().WaitForCacheSync(ctx.Done())

	elasticQuota.Recover()

	return elasticQuota, nil
}

func (g *Plugin) Start() {
	g.createDefaultQuotaIfNotPresent()
	go wait.Until(g.RevokePodDueToQuotaOverUsed, RevokePodCycle, nil)
	go wait.Until(g.elasticQuotaController.Run, time.Second, context.TODO().Done())
}

func (g *Plugin) Name() string {
	return Name
}

//PreFilter performs the following validations.
// 1  refresh plugin's elasticQuotaInfos before Write state
// 2. Check if the (pod.request + eq.used) is less than eq.runtime.
func (g *Plugin) PreFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod) *framework.Status {
	quotaName := g.getPodAssociateQuotaName(pod)

	// update elasticQuotaInfo
	quotaInfo := g.groupQuotaManager.GetQuotaInfoByName(quotaName)
	quotaUsed := quotaInfo.GetUsed()
	quotaRuntime := quotaInfo.GetRuntime()
	g.elasticQuotaInfos[quotaName].setUsed(quotaUsed)
	g.elasticQuotaInfos[quotaName].setRuntime(quotaRuntime)

	snapshotElasticQuota := g.snapshotElasticQuota()
	state.Write(SnapshotStateKey, snapshotElasticQuota)

	podRequest, _ := resource.PodRequestsAndLimits(pod)
	newUsed := quotav1.Add(podRequest, quotaUsed)

	if noOverUse, _ := quotav1.LessThanOrEqual(newUsed, quotaRuntime); !noOverUse {
		return framework.NewStatus(framework.Unschedulable, fmt.Sprintf("pod:%v  is rejected in PreFilter because"+
			"ElasticQuota Used is more than Runtime, quotaName :%s, quotaRuntime:%v quotaUsed:%v, podRequest:%v",
			pod.Name, quotaName, quotaRuntime, quotaUsed, podRequest))
	}

	return framework.NewStatus(framework.Success, "")
}

func (g *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return g
}

// AddPod is called by the framework while trying to evaluate the impact
// of adding podToAdd to the node while scheduling podToSchedule.
func (g *Plugin) AddPod(ctx context.Context, state *framework.CycleState, podToSchedule *corev1.Pod,
	podInfoToAdd *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	elasticQuotaSnapshotState, err := getElasticQuotaSnapshotState(state)
	if err != nil {
		klog.ErrorS(err, "Failed to read elasticQuotaSnapshot from cycleState", "elasticQuotaSnapshotKey", SnapshotStateKey)
		return framework.NewStatus(framework.Error, err.Error())
	}
	quotaName := g.getPodAssociateQuotaName(podInfoToAdd.Pod)
	elasticQuotaInfo := elasticQuotaSnapshotState.elasticQuotaInfos[quotaName]

	if elasticQuotaInfo != nil {
		if err := elasticQuotaInfo.addPodIfNotPresent(podInfoToAdd.Pod); err != nil {
			klog.ErrorS(err, "Failed to add Pod to its associated elasticQuota", "pod", klog.KObj(podInfoToAdd.Pod))
		}
	}

	return framework.NewStatus(framework.Success, "")
}

// RemovePod is called by the framework while trying to evaluate the impact
// of removing podToRemove from the node while scheduling podToSchedule.
func (g *Plugin) RemovePod(ctx context.Context, state *framework.CycleState, podToSchedule *corev1.Pod,
	podInfoToRemove *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	elasticQuotaSnapshotState, err := getElasticQuotaSnapshotState(state)
	if err != nil {
		klog.ErrorS(err, "Failed to read elasticQuotaSnapshot from cycleState", "elasticQuotaSnapshotKey", SnapshotStateKey)
		return framework.NewStatus(framework.Error, err.Error())
	}

	quotaName := g.getPodAssociateQuotaName(podInfoToRemove.Pod)
	elasticQuotaInfo := elasticQuotaSnapshotState.elasticQuotaInfos[quotaName]

	if elasticQuotaInfo != nil {
		if err := elasticQuotaInfo.deletePodIfPresent(podInfoToRemove.Pod); err != nil {
			klog.Infof("Failed to delete Pod from its associated elasticQuota:%v, pod:%v,err:%v", quotaName, klog.KObj(podInfoToRemove.Pod), err)
		}
	}

	return framework.NewStatus(framework.Success, "")
}

// PostFilter modify the defaultPreemption, only allow pods in the same quota can preempt others.
func (g *Plugin) PostFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod,
	filteredNodeStatusMap framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	nnn, status := g.preempt(ctx, state, pod, filteredNodeStatusMap)
	if !status.IsSuccess() {
		return nil, status
	}
	// This happens when the pod is not eligible for preemption or extenders filtered all candidates.
	if nnn == "" {
		return nil, framework.NewStatus(framework.Unschedulable)
	}

	return &framework.PostFilterResult{NominatedNodeName: nnn}, framework.NewStatus(framework.Success)
}

func (g *Plugin) Reserve(ctx context.Context, state *framework.CycleState, p *corev1.Pod, nodeName string) *framework.Status {
	g.Lock()
	defer g.Unlock()

	quotaName := g.getPodAssociateQuotaName(p)
	// update groupQuotaManager
	quotaInfo := g.groupQuotaManager.GetQuotaInfoByName(quotaName)
	podRequest, _ := resource.PodRequestsAndLimits(p)
	g.groupQuotaManager.UpdateGroupDeltaUsed(quotaInfo.Name, podRequest)
	// update elasticQuotaInfos
	elasticQuotaInfo := g.elasticQuotaInfos[quotaName]
	if elasticQuotaInfo != nil {
		err := elasticQuotaInfo.addPodIfNotPresent(p)
		if err != nil {
			klog.ErrorS(err, "Failed to add Pod to its associated elasticQuota", "pod", klog.KObj(p))
			return framework.NewStatus(framework.Error, err.Error())
		}
	}
	// update quotaOverUsedRevokeController
	g.quotaOverUsedRevokeController.addPodWithQuotaName(quotaName, p)
	return framework.NewStatus(framework.Success, "")
}

func (g *Plugin) Unreserve(ctx context.Context, state *framework.CycleState, p *corev1.Pod, nodeName string) {
	g.Lock()
	defer g.Unlock()

	quotaName := g.getPodAssociateQuotaName(p)
	// update groupQuotaManager
	quotaInfo := g.groupQuotaManager.GetQuotaInfoByName(quotaName)
	podRequest, _ := resource.PodRequestsAndLimits(p)
	podRequestTmp := quotav1.Subtract(corev1.ResourceList{}, podRequest)
	g.groupQuotaManager.UpdateGroupDeltaUsed(quotaInfo.Name, podRequestTmp)
	// update elasticQuotaInfo
	elasticQuotaInfo := g.elasticQuotaInfos[quotaName]
	if elasticQuotaInfo != nil {
		err := elasticQuotaInfo.deletePodIfPresent(p)
		if err != nil {
			klog.ErrorS(err, "failed to add Pod to its associated elasticQuota", "pod", klog.KObj(p))
		}
	}
	g.quotaOverUsedRevokeController.erasePodWithQuotaName(quotaName, p)
}

func (g *Plugin) RevokePodDueToQuotaOverUsed() {
	toRevokePods := g.quotaOverUsedRevokeController.MonitorAll()
	for _, pod := range toRevokePods {
		quotaName := g.getPodAssociateQuotaName(pod)
		if err := util.DeletePod(g.handle.ClientSet(), pod); err != nil {
			klog.Errorf("failed to revoke pod due to quota overused, pod:%v, quota:%v, error:%s",
				pod.Name, quotaName, err)
			continue
		}
		klog.V(5).Infof("finish revoke pod due to quota overused, pod:%v, quota:%v",
			pod.Name, quotaName)
	}
}

func (g *Plugin) OnQuotaAdd(obj interface{}) {
	quota, ok := obj.(*schedulerv1alpha1.ElasticQuota)
	if !ok {
		klog.Errorf("quota is nil")
		return
	}

	if quota.DeletionTimestamp != nil {
		klog.Errorf("quota is deleting:%v.%v", quota.Namespace, quota.Name)
		return
	}

	g.Lock()
	defer g.Unlock()

	if oldQuotaInfo := g.groupQuotaManager.GetQuotaInfoByName(quota.Name); oldQuotaInfo != nil && quota.Name != extension.DefaultQuotaName {
		return
	}

	klog.V(3).Infof("OnQuotaAddFunc add quota:%v.%v", quota.Namespace, quota.Name)

	g.elasticQuotaInfos[quota.Name] = NewSimpleQuotaInfo(quota.Name)
	err := g.groupQuotaManager.UpdateQuota(quota, false)
	if err != nil {
		klog.Errorf("OnQuotaUpdateFunc %v.%v failed:%v", quota.Namespace, quota.Name, err)
	}
}

func (g *Plugin) OnQuotaUpdate(oldObj, newObj interface{}) {
	newQuota := newObj.(*schedulerv1alpha1.ElasticQuota)
	oldQuota := oldObj.(*schedulerv1alpha1.ElasticQuota)

	if newQuota.DeletionTimestamp != nil {
		klog.Warningf("update quota warning, update is deleting:%v", newQuota.Name)
		return
	}
	klog.V(5).Infof("OnQuotaUpdateFunc update quota:%v.%v", newQuota.Namespace, newQuota.Name)

	g.Lock()
	defer g.Unlock()

	err := g.groupQuotaManager.UpdateQuota(newQuota, false)
	if err != nil {
		klog.Errorf("OnQuotaUpdateFunc %v.%v failed:%v", newQuota.Namespace, newQuota.Name, err)
		return
	}
	newEQInfo := NewSimpleQuotaInfo(newQuota.Name)
	oldEQInfo := g.elasticQuotaInfos[oldQuota.Name]
	if oldEQInfo != nil {
		newEQInfo.pods = oldEQInfo.pods
		newEQInfo.used = oldEQInfo.used
		newEQInfo.runtime = oldEQInfo.runtime
	}
	g.elasticQuotaInfos[newQuota.Name] = newEQInfo
	klog.V(3).Infof("OnQuotaUpdateFunc success:%v.%v", newQuota.Namespace, newQuota.Name)
}

// OnQuotaDelete only allows to delete leafQuotaNode.
func (g *Plugin) OnQuotaDelete(obj interface{}) {
	quota := obj.(*schedulerv1alpha1.ElasticQuota)

	if quota == nil {
		klog.Errorf("quota is nil")
		return
	}

	klog.V(3).Infof("OnQuotaDeleteFunc delete quota:%+v", quota)

	g.Lock()
	defer g.Unlock()

	err := g.groupQuotaManager.UpdateQuota(quota, true)
	if err != nil {
		klog.Errorf("OnQuotaDeleteFunc %v failed:%v", quota.Name, err)
		return
	}
	delete(g.elasticQuotaInfos, quota.Name)
	klog.V(3).Infof("OnQuotaDeleteFunc success:%v", quota.Name)
}

func (g *Plugin) OnPodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	g.Lock()
	defer g.Unlock()

	quotaName := g.getPodAssociateQuotaName(pod)
	elasticQuotaInfo := g.elasticQuotaInfos[quotaName]
	podReq, _ := resource.PodRequestsAndLimits(pod)
	g.groupQuotaManager.UpdateGroupDeltaRequest(quotaName, podReq)
	if err := elasticQuotaInfo.addPodIfNotPresent(pod); err != nil {
		return
	}
	klog.V(3).Infof("OnPodAddFunc %v.%v add request success, quotaName:%v", pod.Namespace, pod.Name, quotaName)
}

func (g *Plugin) OnPodUpdate(oldObj, newObj interface{}) {
	oldPod := oldObj.(*corev1.Pod)
	newPod := newObj.(*corev1.Pod)

	if oldPod.ResourceVersion == newPod.ResourceVersion {
		klog.Warningf("update pod warning, update version for the same:%v", newPod.Name)
		return
	}

	g.Lock()
	defer g.Unlock()

	oldQuotaName := g.getPodAssociateQuotaName(oldPod)
	podReq, _ := resource.PodRequestsAndLimits(oldPod)
	podReqTmp := quotav1.Subtract(corev1.ResourceList{}, podReq)
	g.groupQuotaManager.UpdateGroupDeltaRequest(oldQuotaName, podReqTmp)

	newQuotaName := g.getPodAssociateQuotaName(newPod)
	podReq, _ = resource.PodRequestsAndLimits(newPod)
	g.groupQuotaManager.UpdateGroupDeltaRequest(newQuotaName, podReq)

	if oldQuotaName != newQuotaName {
		g.elasticQuotaInfos[oldQuotaName].deletePodIfPresent(oldPod)
		g.elasticQuotaInfos[newQuotaName].addPodIfNotPresent(newPod)
	}
	klog.V(3).Infof("OnPodUpdateFunc %v.%v add request success, quotaName:%v", newPod.Namespace, newPod.Name, newQuotaName)
}

func (g *Plugin) OnPodDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}

	g.Lock()
	defer g.Unlock()

	quotaName := g.getPodAssociateQuotaName(pod)
	podReq, _ := resource.PodRequestsAndLimits(pod)
	podReqTmp := quotav1.Subtract(corev1.ResourceList{}, podReq)
	g.groupQuotaManager.UpdateGroupDeltaRequest(quotaName, podReqTmp)
	g.elasticQuotaInfos[quotaName].deletePodIfPresent(pod)
	klog.V(3).Infof("OnPodDeleteFunc %v.%v delete request success", pod.Namespace, pod.Name)
}

func (g *Plugin) OnNodeAdd(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		return
	}

	klog.V(4).Infof("OnNodeAddFunc add node[%v]", node.Name)
	klog.V(6).Infof("OnNodeAddFunc add node: %+v", node)
	if node.DeletionTimestamp != nil {
		klog.Errorf("node is deleting:%v", node.Name)
		return
	}

	allocatable := node.Status.Allocatable

	g.nodeResourceMapLock.Lock()
	defer g.nodeResourceMapLock.Unlock()
	if _, ok := g.nodeResourceMap[node.Name]; ok {
		klog.Infof("OnNodeAddFunc skip due OnAdd Twice:%v", node.Name)
		return
	} else {
		g.nodeResourceMap[node.Name] = struct{}{}
	}

	g.groupQuotaManager.UpdateClusterTotalResource(allocatable)
	klog.V(4).Infof("OnNodeAddFunc success:%v [%+v]", node.Name, allocatable)
}

func (g *Plugin) OnNodeUpdate(oldObj, newObj interface{}) {
	newNode := newObj.(*corev1.Node)
	oldNode := oldObj.(*corev1.Node)

	g.nodeResourceMapLock.Lock()
	defer g.nodeResourceMapLock.Unlock()

	if _, exist := g.nodeResourceMap[newNode.Name]; !exist {
		return
	}

	if newNode.ResourceVersion == oldNode.ResourceVersion {
		klog.Warningf("update node warning, update version for the same, nodeName:%v", newNode.Name)
		return
	}

	klog.V(3).Infof("OnNodeUpdateFunc update:%v delete:%v", newNode.Name, newNode.DeletionTimestamp)

	if newNode.DeletionTimestamp != nil {
		return
	}

	oldNodeAllocatable := oldNode.Status.Allocatable
	newNodeAllocatable := newNode.Status.Allocatable

	if quotav1.Equals(oldNodeAllocatable, newNodeAllocatable) {
		return
	}

	deltaNodeAllocatable := quotav1.Subtract(newNodeAllocatable, oldNodeAllocatable)
	g.groupQuotaManager.UpdateClusterTotalResource(deltaNodeAllocatable)
	klog.V(3).Infof("OnNodeUpdateFunc success:%v [%v]", newNode.Name, newNodeAllocatable)
}

func (g *Plugin) OnNodeDelete(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		klog.Errorf("node is nil")
	}

	allocatable := node.Status.Allocatable
	klog.V(4).Infof("OnNodeDeleteFunc:%v", node.Name)
	delta := quotav1.Subtract(corev1.ResourceList{}, allocatable)
	g.groupQuotaManager.UpdateClusterTotalResource(delta)
	klog.V(4).Infof("OnNodeDeleteFunc success:%v [%v]", node.Name, delta)
}

func (g *Plugin) Recover() {
	// first load node
	klog.V(3).Infof("[Recover] plugin:%v begin load node", g.Name())
	g.loadNode()

	// recover quota
	klog.V(3).Infof("[Recover] plugin:%v begin load quota", g.Name())
	g.loadQuota()

	// recover pod
	klog.V(3).Infof("[Recover] plugin:%v begin load pod", g.Name())
	g.loadPod()
}

func (g *Plugin) loadNode() {
	nodeList, err := g.nodeLister.List(labels.NewSelector())
	if err != nil {
		klog.Errorf("Recover load Node List error: %v", err.Error())
	}
	for _, node := range nodeList {
		g.OnNodeAdd(node)
		klog.V(3).Infof("loadNode success:%v", node.Name)
	}
	klog.V(4).Infof("loadNode:%v", len(nodeList))
}

func (g *Plugin) loadQuota() {
	quotaList, err := g.quotaLister.List(labels.NewSelector())
	if err != nil {
		klog.Errorf("Recover load ElasticQuota List error: %v", err.Error())
	}
	for _, quota := range quotaList {
		g.OnQuotaAdd(quota)
		klog.V(3).Infof("loadNode success:%v", quota.Name)
	}
}

func (g *Plugin) loadPod() {
	podList, err := g.podLister.List(labels.NewSelector())
	if err != nil {
		klog.Errorf("Recover load pod List error: %v", err.Error())
	}
	for _, pod := range podList {
		g.OnPodAdd(pod)
	}
}
