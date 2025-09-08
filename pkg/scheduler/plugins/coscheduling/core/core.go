/*
Copyright 2022 The Koordinator Authors.
Copyright 2020 The Kubernetes Authors.

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
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	pgclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"
	pgformers "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/informers/externalversions"
	pglister "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/listers/scheduling/v1alpha1"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type Status string

const (
	Name     = "Coscheduling"
	stateKey = Name

	// PodGroupNotSpecified denotes no PodGroup is specified in the Pod spec.
	PodGroupNotSpecified Status = "PodGroup not specified"
	// PodGroupNotFound denotes the specified PodGroup in the Pod spec is
	// not found in API server.
	PodGroupNotFound Status = "PodGroup not found"
	Success          Status = "Success"
	Wait             Status = "Wait"
)

// Manager defines the interfaces for PodGroup management.
type Manager interface {
	NextPod() *corev1.Pod
	SucceedGangScheduling()
	PreEnqueue(context.Context, *corev1.Pod) (err error)
	PreFilter(context.Context, *framework.CycleState, *corev1.Pod) (err error)
	Permit(context.Context, *corev1.Pod) (time.Duration, Status)
	PostBind(context.Context, *corev1.Pod, string)
	AfterPostFilter(context.Context, *framework.CycleState, *corev1.Pod, framework.Handle, string, framework.NodeToStatusMap, *framework.Status) (*framework.PostFilterResult, *framework.Status)
	GetAllPodsFromGang(string) []*corev1.Pod
	AllowGangGroup(*corev1.Pod, framework.Handle, string)
	Unreserve(context.Context, *framework.CycleState, *corev1.Pod, string, framework.Handle, string)

	GetGangSummary(gangId string) (*GangSummary, bool)
	GetGangSummaries() map[string]*GangSummary

	GetBoundPodNumber(gangId string) int32
}

// PodGroupManager defines the scheduling operation called
type PodGroupManager struct {
	handle framework.Handle
	args   *config.CoschedulingArgs
	// pgClient is a podGroup client
	pgClient pgclientset.Interface
	// pgLister is podgroup lister
	pgLister pglister.PodGroupLister
	// podLister is pod lister
	podLister listerv1.PodLister
	// cache stores gang info
	cache  *GangCache
	holder GangSchedulingContextHolder
}

// NewPodGroupManager creates a new operation object.
func NewPodGroupManager(
	handle framework.Handle,
	args *config.CoschedulingArgs,
	pgClient pgclientset.Interface,
	pgSharedInformerFactory pgformers.SharedInformerFactory,
	sharedInformerFactory informers.SharedInformerFactory,
	koordSharedInformerFactory koordinatorinformers.SharedInformerFactory,
) *PodGroupManager {
	pgInformer := pgSharedInformerFactory.Scheduling().V1alpha1().PodGroups()
	podInformer := sharedInformerFactory.Core().V1().Pods()
	gangCache := NewGangCache(args, podInformer.Lister(), pgInformer.Lister(), pgClient, handle)
	pgMgr := &PodGroupManager{
		handle:    handle,
		args:      args,
		pgClient:  pgClient,
		pgLister:  pgInformer.Lister(),
		podLister: podInformer.Lister(),
		cache:     gangCache,
	}

	podGroupEventHandler := cache.ResourceEventHandlerFuncs{
		AddFunc:    gangCache.onPodGroupAdd,
		UpdateFunc: gangCache.onPodGroupUpdate,
		DeleteFunc: gangCache.onPodGroupDelete,
	}
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), pgSharedInformerFactory, pgInformer.Informer(), podGroupEventHandler)

	podEventHandler := cache.ResourceEventHandlerFuncs{
		AddFunc:    gangCache.onPodAdd,
		UpdateFunc: gangCache.onPodUpdate,
		DeleteFunc: gangCache.onPodDelete,
	}
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), sharedInformerFactory, podInformer.Informer(), podEventHandler)
	reservationInformer := koordSharedInformerFactory.Scheduling().V1alpha1().Reservations()
	reservationEventHandler := reservationutil.NewReservationToPodEventHandler(podEventHandler)
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), koordSharedInformerFactory, reservationInformer.Informer(), reservationEventHandler)
	return pgMgr
}

func (pgMgr *PodGroupManager) NextPod() *corev1.Pod {
	gangSchedulingContext := pgMgr.holder.getCurrentGangSchedulingContext()
	if gangSchedulingContext == nil {
		klog.V(4).Infof("NextPod: return nil, gangSchedulingContext is nil")
		return nil
	}
	firstPod := gangSchedulingContext.firstPod
	gang := pgMgr.GetGangByPod(firstPod)
	if gang == nil {
		// the podGroup is deleted
		pgMgr.rejectGangGroup(pgMgr.handle, gangSchedulingContext.gangGroup, ReasonGangIsNil)
		pgMgr.holder.clearGangSchedulingContext(ReasonGangIsNil)
		return nil
	}

	// iterate over each gangGroup, get all the pods
	gangGroup := gang.getGangGroup()
	for _, groupGangId := range gangGroup {
		groupGang := pgMgr.cache.getGangFromCacheByGangId(groupGangId, false)
		if groupGang == nil {
			continue
		}
		pods := groupGang.getPendingChildrenFromGang()
		for _, pod := range pods {
			podKey := util.GetId(pod.Namespace, pod.Name)

			if !gangSchedulingContext.alreadyAttemptedPods.Has(podKey) {
				gangSchedulingContext.Lock()
				gangSchedulingContext.alreadyAttemptedPods.Insert(podKey)
				gangSchedulingContext.Unlock()
				klog.Infof("NextPod: return pod %s/%s/%s, gangGroup: %+v, gangGroupStartTime: %+v", pod.Namespace, pod.Name, pod.UID, gangGroup, gangSchedulingContext.startTime)
				// correct podInfo.Time and podInfo.Attempts
				return frameworkext.CopyQueueInfoToPod(firstPod, pod)
			}
		}
	}
	pgMgr.rejectGangGroup(pgMgr.handle, gangSchedulingContext.gangGroup, ReasonAllPendingPodsIsAlreadyAttempted)
	pgMgr.holder.clearGangSchedulingContext(ReasonAllPendingPodsIsAlreadyAttempted)

	return nil
}

func (pgMgr *PodGroupManager) SucceedGangScheduling() {
	pgMgr.holder.clearGangSchedulingContext(ReasonGangIsSucceed)
}

// PreEnqueue
// TODO Turning it on may result in no Pod scheduling events, and an external check should be done through the controller later.
func (pgMgr *PodGroupManager) PreEnqueue(ctx context.Context, pod *corev1.Pod) (err error) {
	if !util.IsPodNeedGang(pod) {
		return nil
	}
	gang := pgMgr.GetGangByPod(pod)
	if gang == nil {
		return fmt.Errorf("can't find gang, gangName: %v, podName: %v", util.GetId(pod.Namespace, util.GetGangNameByPod(pod)),
			util.GetId(pod.Namespace, pod.Name))
	}

	// check if gang is initialized
	if !gang.HasGangInit {
		return fmt.Errorf("gang has not init, gangName: %v, podName: %v", gang.Name,
			util.GetId(pod.Namespace, pod.Name))
	}
	// resourceSatisfied means pod will directly pass the PreFilter
	if gang.getGangMatchPolicy() == extension.GangMatchPolicyOnceSatisfied && gang.isGangOnceResourceSatisfied() {
		return nil
	}
	err = pgMgr.basicGangRequirementsCheck(gang, pod)
	if err != nil {
		return err
	}

	gangSchedulingContext := pgMgr.holder.getCurrentGangSchedulingContext()
	if gangSchedulingContext != nil && gangSchedulingContext.gangGroup.Has(gang.Name) {
		podKey := util.GetId(pod.Namespace, pod.Name)
		gangSchedulingContext.RLock()
		// Subsequent Pods should be prevented from entering ActiveQ or BackoffQ to avoid the time-consuming deletion of them
		if !gangSchedulingContext.alreadyAttemptedPods.Has(podKey) {
			gangSchedulingContext.RUnlock()
			return fmt.Errorf(ErrPodHasNotBeenAttempted, gang.GangGroupId)
		}
		gangSchedulingContext.RUnlock()
	}

	return gang.RecordIfNoRepresentatives(pod)
}

// PreEnqueue
// i.Check whether children in Gang has met the requirements of minimum number under each Gang, and reject the pod if negative.
// ii.Check whether the Gang is inited, and reject the pod if positive.
// iii.Check whether the Gang is OnceResourceSatisfied
func (pgMgr *PodGroupManager) basicGangRequirementsCheck(gang *Gang, pod *corev1.Pod) error {
	gangGroup := gang.getGangGroup()
	var gangsOfMinNumUnSatisfied, gangsOfGangIsNil, gangsOfGangNotInit []string
	for _, gangID := range gangGroup {
		gangTmp := pgMgr.cache.getGangFromCacheByGangId(gangID, false)
		if gangTmp == nil {
			gangsOfGangIsNil = append(gangsOfGangIsNil, gangID)
			continue
		}
		if !gangTmp.HasGangInit {
			gangsOfGangNotInit = append(gangsOfGangNotInit, gangID)
			continue
		}
		if gang.getChildrenNum() < gang.getGangMinNum() {
			gangsOfMinNumUnSatisfied = append(gangsOfMinNumUnSatisfied, gangID)
			continue
		}
	}
	var failedMsg []string
	if len(gangsOfGangIsNil) > 0 {
		failedMsg = append(failedMsg, fmt.Sprintf("memberGangs %+v doesn't exists", gangsOfGangIsNil))
	}
	if len(gangsOfGangNotInit) > 0 {
		failedMsg = append(failedMsg, fmt.Sprintf("memberGangs %+v has not init", gangsOfGangNotInit))
	}
	if len(gangsOfMinNumUnSatisfied) > 0 {
		failedMsg = append(failedMsg, fmt.Sprintf("memberGangs %+v child pod not collect enough", gangGroup))
	}
	if len(failedMsg) > 0 {
		return fmt.Errorf("gangGroup %v basic check: %s, current gang: %s, podName: %v",
			gangGroup,
			strings.Join(failedMsg, ", "),
			gang.Name,
			util.GetId(pod.Namespace, pod.Name))
	}

	return nil
}

func (pgMgr *PodGroupManager) PreFilter(ctx context.Context, _ *framework.CycleState, pod *corev1.Pod) (err error) {
	if !util.IsPodNeedGang(pod) {
		return nil
	}
	gang := pgMgr.GetGangByPod(pod)
	if gang == nil {
		return fmt.Errorf("can't find gang, gangName: %v, podName: %v", util.GetId(pod.Namespace, util.GetGangNameByPod(pod)),
			util.GetId(pod.Namespace, pod.Name))
	}

	// check if gang is initialized
	if !gang.HasGangInit {
		return fmt.Errorf("gang has not init, gangName: %v, podName: %v", gang.Name,
			util.GetId(pod.Namespace, pod.Name))
	}
	// resourceSatisfied means pod will directly pass the PreFilter
	if gang.getGangMatchPolicy() == extension.GangMatchPolicyOnceSatisfied && gang.isGangOnceResourceSatisfied() {
		return nil
	}
	err = pgMgr.basicGangRequirementsCheck(gang, pod)
	if err != nil {
		return err
	}
	gangSchedulingContext := pgMgr.holder.getCurrentGangSchedulingContext()
	if gangSchedulingContext == nil {
		gangSchedulingContext = &GangSchedulingContext{firstPod: pod, gangGroup: sets.New[string](gang.GangGroup...)}
		pgMgr.holder.setGangSchedulingContext(gangSchedulingContext, ReasonFirstPodPassPreFilter)
		// clear the current representative because representative is already enter into scheduling
		gang.ClearCurrentRepresentative(ReasonGangGroupEnterIntoScheduling)
		return nil
	}
	if gangSchedulingContext.failedMessage != "" {
		return fmt.Errorf(gangSchedulingContext.failedMessage)
	}
	return nil
}

// PostFilter
// i. If strict-mode, we will set scheduleCycleValid to false and release all assumed pods.
// ii. If non-strict mode, we will do nothing.
func (pgMgr *PodGroupManager) AfterPostFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, handle framework.Handle, pluginName string, filteredNodeStatusMap framework.NodeToStatusMap, postFilterStatus *framework.Status) (*framework.PostFilterResult, *framework.Status) {
	if !util.IsPodNeedGang(pod) {
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable)
	}
	gang := pgMgr.GetGangByPod(pod)
	if gang == nil {
		message := fmt.Sprintf("Pod %q cannot find Gang %q", klog.KObj(pod), util.GetGangNameByPod(pod))
		klog.Warningf(message)
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable, message)
	}
	if gang.getGangMatchPolicy() == extension.GangMatchPolicyOnceSatisfied && gang.isGangOnceResourceSatisfied() {
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable)
	}

	nodeInfos, _ := handle.SnapshotSharedLister().NodeInfos().List()
	fitErr := &framework.FitError{
		Pod:         pod,
		NumAllNodes: len(nodeInfos),
		Diagnosis: framework.Diagnosis{
			NodeToStatusMap: filteredNodeStatusMap,
		},
	}
	message := fmt.Sprintf("Gang %q gets rejected due to member Pod %q is unschedulable with reason %q, alreadyWaitForBound: %d", gang.Name, pod.Name, fitErr, gang.getGangWaitingPods())

	if gangSchedulingContext := pgMgr.holder.getCurrentGangSchedulingContext(); gangSchedulingContext != nil && gangSchedulingContext.failedMessage == "" {
		// first failed pod
		gangSchedulingContext.failedMessage = message
	}

	// When Job-level preemption succeeds, the WaitingPod is retained for partial preemption
	if gang.getGangMode() == extension.GangModeStrict && !postFilterStatus.IsSuccess() {
		gang.clearWaitingGang()
		pgMgr.rejectGangGroupById(handle, pluginName, gang.Name, message)
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable,
			fmt.Sprintf("Gang %q gets rejected due to pod is unschedulable", gang.Name))
	}

	return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable)
}

// Permit
// we will calculate all Gangs in GangGroup whether the current number of assumed-pods in each Gang meets the Gang's minimum requirement.
// and decide whether we should let the pod wait in Permit stage or let the whole gangGroup go binding
func (pgMgr *PodGroupManager) Permit(ctx context.Context, pod *corev1.Pod) (time.Duration, Status) {
	if !util.IsPodNeedGang(pod) {
		return 0, PodGroupNotSpecified
	}
	gang := pgMgr.GetGangByPod(pod)

	if gang == nil {
		klog.Warningf("Pod %q missing Gang", klog.KObj(pod))
		return 0, PodGroupNotFound
	}
	// first add pod to the gang's WaitingPodsMap
	gang.addAssumedPod(pod)

	allGangGroupAssumed := true
	gangGroup := gang.getGangGroup()
	// check each gang group
	for _, groupName := range gangGroup {
		gangTmp := pgMgr.cache.getGangFromCacheByGangId(groupName, false)
		if gangTmp == nil || !gangTmp.isGangValidForPermit() {
			allGangGroupAssumed = false
			break
		}
	}
	if !allGangGroupAssumed {
		gang.addWaitingGang()
		return gang.WaitTime, Wait
	}
	return 0, Success
}

// Unreserve
// if gang is resourceSatisfied, we only delAssumedPod
// if gang is not resourceSatisfied and is in StrictMode, we release all the assumed pods
func (pgMgr *PodGroupManager) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string, handle framework.Handle, pluginName string) {
	if !util.IsPodNeedGang(pod) {
		return
	}
	gang := pgMgr.GetGangByPod(pod)
	if gang == nil {
		klog.Warningf("Pod %q missing Gang", klog.KObj(pod))
		return
	}
	// first delete the pod from gang's waitingFroBindChildren map
	gang.delAssumedPod(pod)

	// TODO we should record failed message when current pod is the first failed pod of gang, now we just let it go, so quick fail is not supported

	if !(gang.getGangMatchPolicy() == extension.GangMatchPolicyOnceSatisfied && gang.isGangOnceResourceSatisfied()) &&
		gang.getGangMode() == extension.GangModeStrict {
		message := fmt.Sprintf("Gang %q gets rejected due to Pod %q in Unreserve", gang.Name, pod.Name)
		pgMgr.rejectGangGroupById(handle, pluginName, gang.Name, message)
	}
}

func (pgMgr *PodGroupManager) rejectGangGroupById(handle framework.Handle, pluginName, gangId, message string) {
	gang := pgMgr.cache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		return
	}

	// iterate over each gangGroup, get all the pods
	gangGroup := gang.getGangGroup()
	gangSet := sets.New[string](gangGroup...)
	pgMgr.rejectGangGroup(handle, gangSet, message)
}

func (pgMgr *PodGroupManager) rejectGangGroup(handle framework.Handle, gangSet sets.Set[string], message string) {
	if handle != nil {
		handle.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
			waitingGangId := util.GetId(waitingPod.GetPod().Namespace, util.GetGangNameByPod(waitingPod.GetPod()))
			if gangSet.Has(waitingGangId) {
				klog.V(1).InfoS("GangGroup gets rejected due to",
					"waitingGang", waitingGangId,
					"waitingPod", klog.KObj(waitingPod.GetPod()),
					"message", message,
				)
				waitingPod.Reject(Name, message)
			}
		})
	}
}

// PostBind updates a PodGroup's status.
func (pgMgr *PodGroupManager) PostBind(ctx context.Context, pod *corev1.Pod, nodeName string) {
	if !util.IsPodNeedGang(pod) {
		return
	}
	gang := pgMgr.GetGangByPod(pod)
	if gang == nil {
		klog.Warningf("Pod %q missing Gang", klog.KObj(pod))
		return
	}
	// update gang in cache
	gang.addBoundPod(pod)
}

func (pgMgr *PodGroupManager) AllowGangGroup(pod *corev1.Pod, handle framework.Handle, pluginName string) {
	gang := pgMgr.GetGangByPod(pod)
	if gang == nil {
		klog.Warningf("Pod %q missing Gang", klog.KObj(pod))
		return
	}

	gangSlices := gang.getGangGroup()

	handle.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
		podGangId := util.GetId(waitingPod.GetPod().Namespace, util.GetGangNameByPod(waitingPod.GetPod()))
		for _, gangIdTmp := range gangSlices {
			if podGangId == gangIdTmp {
				klog.V(4).InfoS("Permit allows pod from gang", "gang", podGangId, "pod", klog.KObj(waitingPod.GetPod()))
				waitingPod.Allow(pluginName)
				break
			}
		}
	})

	gang.clearWaitingGang()

}

func (pgMgr *PodGroupManager) GetGangByPod(pod *corev1.Pod) *Gang {
	gangName := util.GetGangNameByPod(pod)
	if gangName == "" {
		return nil
	}
	gangId := util.GetId(pod.Namespace, gangName)
	gang := pgMgr.cache.getGangFromCacheByGangId(gangId, false)
	return gang
}

func (pgMgr *PodGroupManager) GetAllPodsFromGang(gangId string) []*corev1.Pod {
	pods := make([]*corev1.Pod, 0)
	gang := pgMgr.cache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		return pods
	}
	pods = gang.getChildrenFromGang()
	return pods
}

func (pgMgr *PodGroupManager) GetGangSummary(gangId string) (*GangSummary, bool) {
	gang := pgMgr.cache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		return nil, false
	}
	return gang.GetGangSummary(), true
}

func (pgMgr *PodGroupManager) GetGangSummaries() map[string]*GangSummary {
	result := make(map[string]*GangSummary)
	allGangs := pgMgr.cache.getAllGangsFromCache()
	for gangName, gang := range allGangs {
		result[gangName] = gang.GetGangSummary()
	}

	return result
}

func (pgMgr *PodGroupManager) GetBoundPodNumber(gangId string) int32 {
	gang := pgMgr.cache.getGangFromCacheByGangId(gangId, false)
	if gang == nil {
		return 0
	}
	return gang.getBoundPodNum()
}
