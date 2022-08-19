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

package gang

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	helpers "k8s.io/component-helpers/scheduling/corev1"

	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	pgclientset "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned"
	pgformers "sigs.k8s.io/scheduler-plugins/pkg/generated/informers/externalversions"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/apis/scheduling/config"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config/validation"
)

type Status string

const (
	Name = "Gang"

	// Permit internal status
	NotFoundInCache Status = "Gang not found in cache"
	Success         Status = "Success"
	Wait            Status = "Wait"
)

var (
	_ framework.QueueSortPlugin  = &GangPlugin{}
	_ framework.PreFilterPlugin  = &GangPlugin{}
	_ framework.PostFilterPlugin = &GangPlugin{}
	_ framework.ReservePlugin    = &GangPlugin{}
	_ framework.PostBindPlugin   = &GangPlugin{}
)

type GangPlugin struct {
	frameworkHandle         framework.Handle
	podLister               v1.PodLister
	podGroupInformerFactory pgformers.SharedInformerFactory
	gangCache               *GangCache
	pluginArgs              *schedulingconfig.GangArgs
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*schedulingconfig.GangArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type GangSchedulingArgs, got %T", args)
	}
	if err := validation.ValidateGangSchedulingArgs(pluginArgs); err != nil {
		return nil, err
	}
	// podGroup informer
	pgClient, ok := handle.(pgclientset.Interface)
	if !ok {
		pgClient = pgclientset.NewForConfigOrDie(handle.KubeConfig())
	}
	pgInformerFactory := pgformers.NewSharedInformerFactory(pgClient, 0)
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()

	// pod Informer
	podInformer := handle.SharedInformerFactory().Core().V1().Pods().Informer()
	podLister := handle.SharedInformerFactory().Core().V1().Pods().Lister()

	gangCache := NewGangCache(pluginArgs, podLister, pgInformer.Lister())
	// addEventHandler funcs
	pgInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    gangCache.onPodGroupAdd,
			UpdateFunc: gangCache.onPodGroupUpdate,
			DeleteFunc: gangCache.onPodGroupDelete,
		},
	)
	podInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    gangCache.onPodAdd,
			UpdateFunc: gangCache.onPodUpdate,
			DeleteFunc: gangCache.onPodDelete,
		},
	)

	gang := &GangPlugin{
		frameworkHandle:         handle,
		podLister:               podLister,
		podGroupInformerFactory: pgInformerFactory,
		gangCache:               gangCache,
		pluginArgs:              pluginArgs,
	}

	ctx := context.TODO()
	pgInformerFactory.Start(ctx.Done())
	handle.SharedInformerFactory().Start(ctx.Done())

	pgInformerFactory.WaitForCacheSync(ctx.Done())
	handle.SharedInformerFactory().WaitForCacheSync(ctx.Done())

	gangCache.RecoverGangCache()
	return gang, nil
}

func (p *GangPlugin) Name() string { return Name }

// Less is sorting pods in the scheduling queue in the following order.
// Firstly, compare the priorities of the two pods, the higher priority (if pod's priority is equal,then compare their KoordinatorPriority at labels )is at the front of the queue,
// Secondly, compare creationTimestamp of two pods, if pod belongs to a Gang, then we compare creationTimestamp of the Gang, the one created first will be at the front of the queue.
// Finally, compare pod's namespace, if pod belongs to a Gang, then we compare Gang name.
func (p *GangPlugin) Less(podInfo1, podInfo2 *framework.QueuedPodInfo) bool {
	prio1 := helpers.PodPriority(podInfo1.Pod)
	prio2 := helpers.PodPriority(podInfo2.Pod)
	if prio1 != prio2 {
		return prio1 > prio2
	}
	subPrio1, err := getPodSubPriority(podInfo1.Pod)
	if err != nil {
		klog.Errorf("GetSubPriority of the pod: %s err:%v", getNamespaceSplicingName(podInfo1.Pod.Namespace, podInfo1.Pod.Name), err)
	}
	subPrio2, err := getPodSubPriority(podInfo2.Pod)
	if err != nil {
		klog.Errorf("GetSubPriority of the pod: %s err:%v", getNamespaceSplicingName(podInfo2.Pod.Namespace, podInfo2.Pod.Name), err)
	}
	if subPrio1 != subPrio2 {
		return subPrio1 > subPrio2
	}

	creationTime1 := p.getCreatTime(podInfo1)
	creationTime2 := p.getCreatTime(podInfo2)
	if creationTime1.Equal(creationTime2) {
		return getNamespaceSplicingName(podInfo1.Pod.Name, podInfo1.Pod.Namespace) < getNamespaceSplicingName(podInfo2.Pod.Name, podInfo2.Pod.Namespace)
	}
	return creationTime1.Before(creationTime2)
}

// PreFilter
// if non-strict-mode, we only do step1 and step2:
// i.Check whether childes in Gang has met the requirements of minimum number under each Gang, and reject the pod if negative.
// ii.Check whether the Gang has been timeout(check the pod's annotation,later introduced at Permit section) or is inited, and reject the pod if positive.
// iii.Check whether the Gang has met the scheduleCycleValid check, and reject the pod if negative.
// iv.Try update scheduleCycle, scheduleCycleValid, childrenScheduleRoundMap as mentioned above.
func (p *GangPlugin) PreFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod) *framework.Status {
	gangName := getGangNameByPod(pod)
	if gangName == "" {
		return framework.NewStatus(framework.Success, "")
	}
	gangId := getNamespaceSplicingName(pod.Namespace,
		gangName)
	gang := p.gangCache.getGangFromCache(gangId)
	if gang == nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("can't find gang, gangName: %v, podName: %v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name)))
	}
	// check if gang is inited
	if !gang.HasGangInit {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("gang has not init, gangName: %v, podName: %v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name)))
	}
	// check if gang is timeout by pod's Annotation
	if pod.Annotations[extension.AnnotationGangTimeout] == "true" {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("gang has timeout, gangName: %v, podName: %v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name)))
	}

	if gang.getChildrenNum() < gang.getGangMinNum() {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("gang child pod not collect enough, gangName: %v, podName: %v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name)))
	}

	gangMode := gang.getGangMode()
	if gangMode == extension.GangModeStrict {
		podScheduleCycle := gang.getChildScheduleCycle(pod)
		gangScheduleCycle := gang.getGangScheduleCycle()
		defer func() {
			gang.setChildScheduleCycle(pod, gangScheduleCycle)
			gang.tryUpdateScheduleCycle()
		}()
		if !gang.isGangScheduleCycleValid() {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("gang scheduleCycle not valid, gangName: %v, podName: %v",
				gangId, getNamespaceSplicingName(pod.Namespace, pod.Name)))
		}
		if podScheduleCycle >= gangScheduleCycle {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("pod's schedule cycle too large, gangName: %v, podName: %v, podCycle: %v, gangCycle: %v",
				gangId, getNamespaceSplicingName(pod.Namespace, pod.Name), podScheduleCycle, gangScheduleCycle))
		}
	}
	return framework.NewStatus(framework.Success, "")
}

// PreFilterExtensions returns a PreFilterExtensions interface if the plugin implements one.
func (p *GangPlugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// PostFilter
// i. If strict-mode, we will set scheduleCycleValid to false and release all assumed pods.
// ii. If non-strict mode, we will do nothing.
func (p *GangPlugin) PostFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod,
	filteredNodeStatusMap framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	gangName := getGangNameByPod(pod)
	if gangName == "" {
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable, "")
	}
	gangId := getNamespaceSplicingName(pod.Namespace,
		gangName)
	gang := p.gangCache.getGangFromCache(gangId)
	if gang == nil {
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable, fmt.Sprintf("can't find gang, gangName: %v, podName: %v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name)))
	}

	if gang.isGangResourceSatisfied() {
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable, "")
	}

	if gang.getGangMode() == extension.GangModeStrict {
		p.frameworkHandle.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
			waitingGangId := getNamespaceSplicingName(waitingPod.GetPod().Namespace,
				getGangNameByPod(waitingPod.GetPod()))
			if waitingGangId == gangId {
				klog.Errorf("postFilter rejects the pod, gangName: %v, podName: %v",
					gangId, getNamespaceSplicingName(waitingPod.GetPod().Namespace, waitingPod.GetPod().Name))
				waitingPod.Reject(p.Name(), "gang rejection in PostFilter")
			}
		})
		gang.setScheduleCycleValid(false)
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable,
			fmt.Sprintf("Gang: %v gets rejected this cycle due to Pod: %v is unschedulable even after "+
				"PostFilter in StrictMode", gangId, pod.Name))
	}

	return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable, "")
}

// Reserve we add the pod to gang's WaitingForBindChildren map
func (p *GangPlugin) Reserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	gangName := getGangNameByPod(pod)
	if gangName == "" {
		return framework.NewStatus(framework.Success, "")
	}
	gangId := getNamespaceSplicingName(pod.Namespace,
		gangName)
	gang := p.gangCache.getGangFromCache(gangId)
	if gang == nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("can't find gang, gangName: %v, podName: %v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name)))
	}
	gang.addAssumedPod(pod)
	return framework.NewStatus(framework.Success, "")
}

// Permit
// we will calculate all Gangs in GangGroup whether the current number of assumed-pods in each Gang meets the Gang's minimum requirement.
// and decide whether we should let the pod wait in Permit stage or let the whole gangGroup go binding
func (p *GangPlugin) Permit(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) (*framework.Status, time.Duration) {
	waitTime, s := p.PermitCheck(pod)
	var retStatus *framework.Status
	switch s {
	case NotFoundInCache:
		return framework.NewStatus(framework.Unschedulable, "Gang not found in gangCache"), 0
	case Wait:
		klog.Infof("Pod: %v from gang: %v is waiting to be scheduled at Permit stage",
			getNamespaceSplicingName(pod.Namespace, pod.Name),
			getNamespaceSplicingName(pod.Namespace,
				getGangNameByPod(pod)))
		retStatus = framework.NewStatus(framework.Wait)
		p.ActivateGang(pod, state)
	case Success:
		p.AllowGangGroup(pod)
		retStatus = framework.NewStatus(framework.Success)
		waitTime = 0
	}
	return retStatus, waitTime
}

func (p *GangPlugin) PermitCheck(pod *corev1.Pod) (time.Duration, Status) {
	gangName := getGangNameByPod(pod)
	if gangName == "" {
		return 0, Success
	}
	gangId := getNamespaceSplicingName(pod.Namespace,
		gangName)
	gang := p.gangCache.getGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName:%v, podName:%v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name))
		return 0, NotFoundInCache
	}

	gangGroup := gang.getGangGroup()
	allGangGroupSatisfied := true
	// only the gang itself
	if len(gangGroup) == 0 {
		allGangGroupSatisfied = gang.isGangResourceSatisfied()
	} else {
		// check each gang group
		for _, groupName := range gangGroup {
			gangTmp := p.gangCache.getGangFromCache(groupName)
			if gangTmp != nil {
				if !gangTmp.isGangResourceSatisfied() {
					allGangGroupSatisfied = false
					break
				}
			}
		}
	}
	if !allGangGroupSatisfied {
		gang.setTimeoutStartTime(timeNowFn())
		return gang.WaitTime, Wait
	}
	return 0, Success
}

// ActivateGang
// Put all the pods belong to the Gang which in UnSchedulableQueue or backoffQueue back to activeQueue,
func (p *GangPlugin) ActivateGang(pod *corev1.Pod, state *framework.CycleState) {
	gangId := getNamespaceSplicingName(pod.Namespace,
		getGangNameByPod(pod))

	pods, err := p.podLister.Pods(pod.Namespace).List(labels.NewSelector())
	if err != nil {
		klog.Errorf("ActivateGang Failed to list pods belong to a Gang: %v", gangId)
		return
	}
	toActivePods := make([]*corev1.Pod, 0)
	for i := range pods {
		gangIdTmp := getNamespaceSplicingName(pods[i].Namespace,
			getGangNameByPod(pods[i]))
		if gangIdTmp == gangId {
			if pods[i].UID == pod.UID {
				continue
			}
			toActivePods = append(toActivePods, pods[i])
		}
	}

	if len(toActivePods) != 0 {
		if c, err := state.Read(framework.PodsToActivateKey); err == nil {
			if s, ok := c.(*framework.PodsToActivate); ok {
				s.Lock()
				for _, pod := range toActivePods {
					namespacedName := getNamespaceSplicingName(pod.Namespace, pod.Name)
					s.Map[namespacedName] = pod
				}
				s.Unlock()
			}
		}
	}
}

func (p *GangPlugin) AllowGangGroup(pod *corev1.Pod) {
	gangId := getNamespaceSplicingName(pod.Namespace,
		getGangNameByPod(pod))
	gang := p.gangCache.getGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName: %v, podName: %v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name))
		return
	}

	// allow only the gang itself
	gangSlices := make([]string, 0)
	if len(gang.getGangGroup()) == 0 {
		gangSlices = append(gangSlices, gangId)
	} else {
		gangSlices = gang.getGangGroup()
	}

	// allow each gang group
	for _, gangIdTmp := range gangSlices {
		p.frameworkHandle.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
			podGangId := getNamespaceSplicingName(waitingPod.GetPod().Namespace,
				getGangNameByPod(waitingPod.GetPod()))

			if podGangId == gangIdTmp {
				klog.Infof("Permit allows pod: %v from gang: %v",
					getNamespaceSplicingName(waitingPod.GetPod().Namespace, waitingPod.GetPod().Name), podGangId)
				waitingPod.Allow(p.Name())
			}
		})

		klog.Infof("Permit allows for gang: %v", gangIdTmp)
	}
}

// Unreserve
// i. handle the timeout gang
// ii. do nothing when bound failed
func (p *GangPlugin) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	gangName := getGangNameByPod(pod)
	if gangName == "" {
		return
	}
	gangId := getNamespaceSplicingName(pod.Namespace,
		gangName)
	gang := p.gangCache.getGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName: %v, podName: %v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name))
		return
	}

	// gang time out
	if gang.isGangTimeout() {
		klog.Infof("gang is time out,start to release the assumed resource and add annotations to the "+
			"gang's children, gangName: %v, podName: %v", gangId, getNamespaceSplicingName(pod.Namespace, pod.Name))
		// gang is timeout,we clear the TimeoutStartTime
		gang.clearTimeoutStartTime()
		pods, err := p.podLister.List(labels.NewSelector())
		if err != nil {
			klog.Errorf("unReserve list pod err: %v", err.Error())
			return
		}
		for _, podOriginal := range pods {
			if getNamespaceSplicingName(pod.Namespace,
				getGangNameByPod(pod)) == gangId {
				pod = podOriginal.DeepCopy()
				if pod.Annotations == nil {
					pod.Annotations = map[string]string{}
				}
				pod.Annotations[extension.AnnotationGangTimeout] = "true"
				patchBytes, err := generatePodPatch(podOriginal, pod)
				if err != nil {
					klog.Errorf("unReserve generatePodPatch got err: %v", err.Error())
					continue
				}
				if string(patchBytes) == "{}" {
					continue
				}
				err = retry.OnError(
					retry.DefaultRetry,
					errors.IsTooManyRequests,
					func() error {
						_, err = p.frameworkHandle.ClientSet().CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name,
							types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
						return err
					})
				if err != nil {
					klog.Errorf("Failed to patch gang timeout annotation after retry to pod: %v, gang: %v, err: %v",
						getNamespaceSplicingName(pod.Namespace, pod.Name), gangId, err.Error())
				} else {
					klog.Infof("unReserve patch gang timeout annotation to pod: %v, gang: %v success",
						getNamespaceSplicingName(pod.Namespace, pod.Name), gangId)
				}
			}
		}

		// release resource of all assumed children of the gang
		p.frameworkHandle.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
			if getNamespaceSplicingName(waitingPod.GetPod().Namespace,
				getGangNameByPod(waitingPod.GetPod())) == gangId {
				klog.Errorf("unReserve rejects the pod name: %v from Gang: %s due to timeout",
					getNamespaceSplicingName(pod.Namespace, pod.Name), gangId)
				waitingPod.Reject(p.Name(), "optimistic rejection in unReserve due to timeout")
			}
		})
	}
}

// PostBind just update the gang's BoundChildren
func (p *GangPlugin) PostBind(ctx context.Context, _ *framework.CycleState, pod *corev1.Pod, nodeName string) {
	gangName := getGangNameByPod(pod)
	if gangName == "" {
		return
	}
	gangId := getNamespaceSplicingName(pod.Namespace,
		gangName)
	gang := p.gangCache.getGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName: %v, podName: %v", gangId,
			getNamespaceSplicingName(pod.Namespace, pod.Name))
		return
	}

	gang.addBoundPod(pod)
}

func (p *GangPlugin) getCreatTime(podInfo *framework.QueuedPodInfo) time.Time {
	// first check if the pod belongs to the Gang
	gangName := getGangNameByPod(podInfo.Pod)
	// it doesn't belong to the gang,we get the creation time of the pod
	if gangName == "" {
		return podInfo.InitialAttemptTimestamp
	}
	// it belongs to a gang,we get the creation time of the Gang
	gangId := getNamespaceSplicingName(podInfo.Pod.Namespace,
		gangName)
	gang := p.gangCache.getGangFromCache(gangId)
	if gang != nil {
		return gang.CreateTime
	}
	klog.Errorf("getCreatTime didn't find gang: %v in gangCache, pod name: %v",
		gangId, podInfo.Pod.Name)
	return time.Now()
}
