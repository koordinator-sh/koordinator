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

package core

import (
	"sync"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	schedulingv1alpha1 "k8s.io/api/scheduling/v1alpha1"
	listerv1 "k8s.io/client-go/listers/core/v1"
	listerschedulingv1alpha1 "k8s.io/client-go/listers/scheduling/v1alpha1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	pgclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"
	pglister "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/workloadauditor"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
	koordutil "github.com/koordinator-sh/koordinator/pkg/util"
)

type GangCache struct {
	lock             *sync.RWMutex
	gangItems        map[string]*Gang
	gangGroupInfoMap map[string]*GangGroupInfo
	pluginArgs       *config.CoschedulingArgs
	podLister        listerv1.PodLister
	pgLister         pglister.PodGroupLister
	workloadLister   listerschedulingv1alpha1.WorkloadLister
	pgClient         pgclientset.Interface
	handle           fwktype.Handle

	workloadAuditor workloadauditor.WorkloadAuditor
}

func NewGangCache(args *config.CoschedulingArgs, podLister listerv1.PodLister, pgLister pglister.PodGroupLister, workloadLister listerschedulingv1alpha1.WorkloadLister, client pgclientset.Interface, handle fwktype.Handle) *GangCache {
	return &GangCache{
		gangItems:        make(map[string]*Gang),
		gangGroupInfoMap: make(map[string]*GangGroupInfo),
		lock:             new(sync.RWMutex),
		pluginArgs:       args,
		podLister:        podLister,
		pgLister:         pgLister,
		workloadLister:   workloadLister,
		pgClient:         client,
		handle:           handle,
	}
}

func (gangCache *GangCache) getGangGroupInfo(gangGroupId string, gangGroup []string, createIfNotExist bool) (gangGroupInfo *GangGroupInfo, created bool) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	if gangCache.gangGroupInfoMap[gangGroupId] == nil {
		if createIfNotExist {
			gangGroupInfo = NewGangGroupInfo(gangGroupId, gangGroup)
			gangGroupInfo.SetInitialized()
			gangCache.gangGroupInfoMap[gangGroupId] = gangGroupInfo
			klog.Infof("add gangGroupInfo to cache, gangGroupId: %v", gangGroupId)
			return gangGroupInfo, true
		}
	} else {
		gangGroupInfo = gangCache.gangGroupInfoMap[gangGroupId]
	}

	return gangGroupInfo, false
}

func (gangCache *GangCache) deleteGangGroupInfo(gangGroupId string) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	delete(gangCache.gangGroupInfoMap, gangGroupId)
	klog.Infof("delete gangGroupInfo from cache, gangGroupId: %v", gangGroupId)
}

func (gangCache *GangCache) getGangFromCacheByGangId(gangId string, createIfNotExist bool) *Gang {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	gang := gangCache.gangItems[gangId]
	if gang == nil && createIfNotExist {
		gang = NewGang(gangId)
		gangCache.gangItems[gangId] = gang
		klog.Infof("getGangFromCache create new gang, gang: %v", gangId)
	}
	return gang
}

func (gangCache *GangCache) getAllGangsFromCache() map[string]*Gang {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()

	result := make(map[string]*Gang)
	for gangId, gang := range gangCache.gangItems {
		result[gangId] = gang
	}

	return result
}

func (gangCache *GangCache) deleteGangFromCacheByGangId(gangId string) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()

	delete(gangCache.gangItems, gangId)
	klog.Infof("delete gang from cache, gang: %v", gangId)
}

func (gangCache *GangCache) onPodAdd(obj interface{}) {
	gangCache.onPodAddInternal(obj, "create")
}

func (gangCache *GangCache) onPodAddInternal(obj interface{}, action string) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}

	gangName := util.GetGangNameByPod(pod)
	if gangName == "" {
		return
	}

	gangNamespace := pod.Namespace
	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, true)

	// the gang is created in Annotation way
	if pod.Labels[v1alpha1.PodGroupLabel] == "" {
		gang.tryInitByPodConfig(pod, gangCache.pluginArgs)

		gangGroup := gang.getGangGroup()
		gangGroupId := util.GetGangGroupId(gangGroup)
		gangGroupInfo, created := gangCache.getGangGroupInfo(gangGroupId, gangGroup, true)
		gang.SetGangGroupInfo(gangGroupInfo)
		if created && pod.Spec.NodeName == "" && gangCache.isResponsibleForPod(pod) && gangCache.workloadAuditor != nil {
			gangCache.workloadAuditor.AddGangGroup(gangGroupId)
		}
	}

	gang.setChild(pod)
	if pod.Spec.NodeName != "" {
		gang.addBoundPod(pod)
		gang.setResourceSatisfied()
	} else if action == "create" {
		// Detect initial gating state for the newly added pod
		if gang.GangGroupId != "" && gangCache.workloadAuditor != nil {
			gangCache.workloadAuditor.RecordGangGating(gang.GangGroupId, pod, workloadauditor.PodIsGated(pod))
		}
		if gang.isGangWorthRequeue() {
			if gangCache.handle == nil {
				// only UT will go here
				return
			}
			if extendedHandle := gangCache.handle.(frameworkext.ExtendedHandle); extendedHandle != nil && extendedHandle.Scheduler() != nil && extendedHandle.Scheduler().GetSchedulingQueue() != nil {
				addedPod, ok := obj.(*v1.Pod)
				if !ok {
					return
				}
				klog.V(4).Infof("gang basic check pass, delivery an activate for gang: %s, pod: %s", gangId, addedPod.Name)
				if gangCache.workloadAuditor != nil {
					gangCache.workloadAuditor.RecordGangGroup(gang.GangGroupId, addedPod, workloadauditor.RecordTypeGangMinMemberSatisfied, gangId)
				}
				extendedHandle.Scheduler().GetSchedulingQueue().Activate(logr.Discard(), map[string]*v1.Pod{util.GetId(addedPod.Namespace, addedPod.Name): addedPod})
			}
		}
	}

	klog.Infof("watch pod %v, Name:%v, pgLabel:%v", action, pod.Name, pod.Labels[v1alpha1.PodGroupLabel])
}

func (gangCache *GangCache) onPodUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*v1.Pod)
	if !ok {
		return
	}

	gangName := util.GetGangNameByPod(pod)
	if gangName == "" {
		return
	}

	if koordutil.IsPodTerminated(pod) {
		return
	}

	// Detect gating transitions for gang pods
	if oldPod, ok := oldObj.(*v1.Pod); ok {
		oldGated := workloadauditor.PodIsGated(oldPod)
		newGated := workloadauditor.PodIsGated(pod)
		if oldGated != newGated {
			gangId := util.GetId(pod.Namespace, gangName)
			if gang := gangCache.getGangFromCacheByGangId(gangId, false); gang != nil && gang.GangGroupId != "" && gangCache.workloadAuditor != nil {
				gangCache.workloadAuditor.RecordGangGating(gang.GangGroupId, pod, newGated)
			}
		}
	}

	gangCache.onPodAddInternal(newObj, "update")
}

func (gangCache *GangCache) onPodDelete(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("onPodDelete: couldn't get object from tombstone %+v", obj)
			return
		}
		pod, ok = tombstone.Obj.(*v1.Pod)
		if !ok {
			klog.Errorf("onPodDelete: tombstone contained object that is not a Pod %+v", tombstone.Obj)
			return
		}
	}
	gangName := util.GetGangNameByPod(pod)
	if gangName == "" {
		return
	}

	gangNamespace := pod.Namespace
	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		return
	}

	shouldDeleteGang := gang.deletePod(pod)
	if shouldDeleteGang {
		gangCache.deleteGangFromCacheByGangId(gangId)

		allGangDeleted := true
		for _, gangId := range gang.GangGroup {
			if gangCache.getGangFromCacheByGangId(gangId, false) != nil {
				allGangDeleted = false
				break
			}
		}
		if allGangDeleted {
			gangCache.deleteGangGroupInfo(gang.GangGroupInfo.GangGroupId)
			if gangCache.workloadAuditor != nil {
				gangCache.workloadAuditor.DeleteGangGroup(gang.GangGroupInfo.GangGroupId)
			}
		}
	}

	klog.Infof("watch pod deleted, Name:%v, pgLabel:%v", pod.Name, pod.Labels[v1alpha1.PodGroupLabel])
}

func (gangCache *GangCache) onPodGroupAdd(obj interface{}) {
	pg, ok := obj.(*v1alpha1.PodGroup)
	if !ok {
		return
	}
	gangNamespace := pg.Namespace
	gangName := pg.Name

	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, true)
	gang.tryInitByPodGroup(pg, gangCache.pluginArgs)
	gangGroup := gang.getGangGroup()
	gangGroupId := util.GetGangGroupId(gangGroup)
	gangGroupInfo, _ := gangCache.getGangGroupInfo(gangGroupId, gangGroup, true)
	gang.SetGangGroupInfo(gangGroupInfo)
	if gangCache.workloadAuditor != nil {
		phase := pg.Status.Phase
		if isPodGroupPendingPhase(phase) {
			gangCache.workloadAuditor.AddGangGroup(gang.GangGroupId)
		}
	}
	if gang.isGangWorthRequeue() {
		if gangCache.handle == nil {
			// only UT will go here
			return
		}
		if extendedHandle := gangCache.handle.(frameworkext.ExtendedHandle); extendedHandle != nil && extendedHandle.Scheduler() != nil && extendedHandle.Scheduler().GetSchedulingQueue() != nil {
			someChildren := gang.pickSomeChildren()
			if someChildren == nil {
				return
			}
			if gangCache.workloadAuditor != nil {
				gangCache.workloadAuditor.RecordGangGroup(gang.GangGroupId, someChildren, workloadauditor.RecordTypeGangMinMemberSatisfied, gangId)
			}
			klog.V(4).Infof("gang basic check pass, delivery an activate for gang: %s, pod: %s", gangId, someChildren.Name)
			extendedHandle.Scheduler().GetSchedulingQueue().Activate(logr.Discard(), map[string]*v1.Pod{util.GetId(someChildren.Namespace, someChildren.Name): someChildren})
		}
	}

	klog.Infof("watch podGroup created, Name:%v", pg.Name)
}

func (gangCache *GangCache) onPodGroupUpdate(oldObj interface{}, newObj interface{}) {
	pg, ok := newObj.(*v1alpha1.PodGroup)
	if !ok {
		return
	}
	gangNamespace := pg.Namespace
	gangName := pg.Name

	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		klog.Errorf("Gang object isn't exist when got Update Event")
		return
	}

	// When PodGroup transitions from a pending phase to a non-pending phase,
	// delete the gang record from the workload auditor.
	if gangCache.workloadAuditor != nil {
		oldPg, ok := oldObj.(*v1alpha1.PodGroup)
		if ok && isPodGroupPendingPhase(oldPg.Status.Phase) && !isPodGroupPendingPhase(pg.Status.Phase) {
			gangCache.workloadAuditor.DeleteGangGroup(gang.GangGroupId)
		}
	}

	isGangWorthRequeueBefore := gang.isGangWorthRequeue()
	gang.tryInitByPodGroup(pg, gangCache.pluginArgs)
	if !isGangWorthRequeueBefore && gang.isGangWorthRequeue() {
		if gangCache.handle == nil {
			// only UT will go here
			return
		}
		if extendedHandle := gangCache.handle.(frameworkext.ExtendedHandle); extendedHandle != nil && extendedHandle.Scheduler() != nil && extendedHandle.Scheduler().GetSchedulingQueue() != nil {
			someChildren := gang.pickSomeChildren()
			if someChildren == nil {
				return
			}
			klog.V(4).Infof("gang basic check pass, delivery an activate for gang: %s, pod: %s", gangId, someChildren.Name)
			if gangCache.workloadAuditor != nil {
				gangCache.workloadAuditor.RecordGangGroup(gang.GangGroupId, someChildren, workloadauditor.RecordTypeGangMinMemberSatisfied, gangId)
			}
			extendedHandle.Scheduler().GetSchedulingQueue().Activate(logr.Discard(), map[string]*v1.Pod{util.GetId(someChildren.Namespace, someChildren.Name): someChildren})
		}
	}
	gangGroup := gang.getGangGroup()
	gangGroupId := util.GetGangGroupId(gangGroup)
	gangGroupInfo, _ := gangCache.getGangGroupInfo(gangGroupId, gangGroup, true)
	gang.SetGangGroupInfo(gangGroupInfo)
}

func (gangCache *GangCache) onPodGroupDelete(obj interface{}) {
	pg, ok := obj.(*v1alpha1.PodGroup)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("onPodGroupDelete: couldn't get object from tombstone %+v", obj)
			return
		}
		pg, ok = tombstone.Obj.(*v1alpha1.PodGroup)
		if !ok {
			klog.Errorf("onPodGroupDelete: tombstone contained object that is not a PodGroup %+v", tombstone.Obj)
			return
		}
	}
	gangNamespace := pg.Namespace
	gangName := pg.Name

	gangId := util.GetId(gangNamespace, gangName)
	gang := gangCache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		return
	}
	gang.removeWaitingGang()
	gangCache.deleteGangFromCacheByGangId(gangId)

	allGangDeleted := true
	for _, gangId := range gang.GangGroup {
		if gangCache.getGangFromCacheByGangId(gangId, false) != nil {
			allGangDeleted = false
			break
		}
	}
	if allGangDeleted {
		gangCache.deleteGangGroupInfo(gang.GangGroupInfo.GangGroupId)
		if gangCache.workloadAuditor != nil {
			gangCache.workloadAuditor.DeleteGangGroup(gang.GangGroupInfo.GangGroupId)
		}
	}

	klog.Infof("watch podGroup deleted, Name:%v", pg.Name)
}

func (gangCache *GangCache) getPendingPods(gangGroup []string) []*v1.Pod {
	var pendingPods []*v1.Pod
	for _, gangID := range gangGroup {
		gang := gangCache.getGangFromCacheByGangId(gangID, false)
		if gang == nil {
			continue
		}
		pendingPods = append(pendingPods, gang.getPendingChildrenFromGang()...)
	}
	return pendingPods
}

func (gangCache *GangCache) getPendingPodsNum(gangGroup []string) int {
	pendingPodsNum := 0
	for _, gangID := range gangGroup {
		gang := gangCache.getGangFromCacheByGangId(gangID, false)
		if gang == nil {
			continue
		}
		pendingPodsNum += gang.getPendingChildrenNum()
	}
	return pendingPodsNum
}

func (gangCache *GangCache) getWaitingPods(gangGroup []string) []*v1.Pod {
	var waitingPods []*v1.Pod
	for _, gangID := range gangGroup {
		gang := gangCache.getGangFromCacheByGangId(gangID, false)
		if gang == nil {
			continue
		}
		waitingPods = append(waitingPods, gang.getWaitingChildrenFromGang()...)
	}
	return waitingPods
}

func (gangCache *GangCache) getWaitingPodsNum(gangGroup []string) int {
	waitingPodsNum := 0
	for _, gangID := range gangGroup {
		gang := gangCache.getGangFromCacheByGangId(gangID, false)
		if gang == nil {
			continue
		}
		waitingPodsNum += gang.getGangWaitingPods()
	}
	return waitingPodsNum
}

// isPodGroupPendingPhase returns true if the PodGroup phase indicates
// it has not yet finished scheduling (empty, Pending, PreScheduling, Scheduling).
func isPodGroupPendingPhase(phase v1alpha1.PodGroupPhase) bool {
	return phase == "" || phase == v1alpha1.PodGroupPending || phase == v1alpha1.PodGroupPreScheduling || phase == v1alpha1.PodGroupScheduling
}

// isResponsibleForPod returns true if the pod's scheduler name matches
// the profile name of this scheduler instance.
func (gangCache *GangCache) isResponsibleForPod(pod *v1.Pod) bool {
	if fwk, ok := gangCache.handle.(framework.Framework); ok {
		return pod.Spec.SchedulerName == fwk.ProfileName()
	}
	return true
}

func (gangCache *GangCache) onWorkloadAdd(obj interface{}) {
	workload, ok := obj.(*schedulingv1alpha1.Workload)
	if !ok {
		return
	}

	for _, pg := range workload.Spec.PodGroups {
		gangId := util.GetId(workload.Namespace, pg.Name)
		gang := gangCache.getGangFromCacheByGangId(gangId, true)
		gang.InitByGangInfo(NewWorkloadGangInfo(workload, pg.Name, gangCache.pluginArgs))
		gangGroup := gang.getGangGroup()
		gangGroupId := util.GetGangGroupId(gangGroup)
		gangGroupInfo, _ := gangCache.getGangGroupInfo(gangGroupId, gangGroup, true)
		gang.SetGangGroupInfo(gangGroupInfo)

		if gangCache.workloadAuditor != nil {
			// For native Workload, we use the Workload UID or name as the primary identifier
			gangCache.workloadAuditor.AddGangGroup(gang.GangGroupId)
		}

		if gang.isGangWorthRequeue() {
			if gangCache.handle == nil {
				return
			}
			if extendedHandle, ok := gangCache.handle.(frameworkext.ExtendedHandle); ok && extendedHandle.Scheduler() != nil && extendedHandle.Scheduler().GetSchedulingQueue() != nil {
				someChildren := gang.pickSomeChildren()
				if someChildren == nil {
					continue
				}
				if gangCache.workloadAuditor != nil {
					gangCache.workloadAuditor.RecordGangGroup(gang.GangGroupId, someChildren, workloadauditor.RecordTypeGangMinMemberSatisfied, gangId)
				}
				extendedHandle.Scheduler().GetSchedulingQueue().Activate(logr.Discard(), map[string]*v1.Pod{util.GetId(someChildren.Namespace, someChildren.Name): someChildren})
			}
		}

	}

	klog.Infof("watch native workload created, Name:%v", workload.Name)
}

func (gangCache *GangCache) onWorkloadUpdate(oldObj interface{}, newObj interface{}) {
	workload, ok := newObj.(*schedulingv1alpha1.Workload)
	if !ok {
		return
	}

	for _, pg := range workload.Spec.PodGroups {
		gangId := util.GetId(workload.Namespace, pg.Name)
		gang := gangCache.getGangFromCacheByGangId(gangId, false)
		if gang == nil {
			continue
		}

		isGangWorthRequeueBefore := gang.isGangWorthRequeue()
		gang.UpdateByGangInfo(NewWorkloadGangInfo(workload, pg.Name, gangCache.pluginArgs))

		if !isGangWorthRequeueBefore && gang.isGangWorthRequeue() {
			if gangCache.handle == nil {
				return
			}
			if extendedHandle, ok := gangCache.handle.(frameworkext.ExtendedHandle); ok && extendedHandle.Scheduler() != nil && extendedHandle.Scheduler().GetSchedulingQueue() != nil {
				someChildren := gang.pickSomeChildren()
				if someChildren == nil {
					continue
				}
				if gangCache.workloadAuditor != nil {
					gangCache.workloadAuditor.RecordGangGroup(gang.GangGroupId, someChildren, workloadauditor.RecordTypeGangMinMemberSatisfied, gangId)
				}
				extendedHandle.Scheduler().GetSchedulingQueue().Activate(logr.Discard(), map[string]*v1.Pod{util.GetId(someChildren.Namespace, someChildren.Name): someChildren})
			}
		}
	}
}

func (gangCache *GangCache) onWorkloadDelete(obj interface{}) {
	workload, ok := obj.(*schedulingv1alpha1.Workload)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		workload, ok = tombstone.Obj.(*schedulingv1alpha1.Workload)
		if !ok {
			return
		}
	}

	for _, pg := range workload.Spec.PodGroups {
		gangId := util.GetId(workload.Namespace, pg.Name)
		gang := gangCache.getGangFromCacheByGangId(gangId, false)
		if gang == nil {
			continue
		}
		gang.removeWaitingGang()
		gangCache.deleteGangFromCacheByGangId(gangId)

		allGangDeleted := true
		for _, gId := range gang.GangGroup {
			if gangCache.getGangFromCacheByGangId(gId, false) != nil {
				allGangDeleted = false
				break
			}
		}
		if allGangDeleted {
			gangCache.deleteGangGroupInfo(gang.GangGroupInfo.GangGroupId)
			if gangCache.workloadAuditor != nil {
				gangCache.workloadAuditor.DeleteGangGroup(gang.GangGroupInfo.GangGroupId)
			}
		}
	}

	klog.Infof("watch native workload deleted, Name:%v", workload.Name)
}
