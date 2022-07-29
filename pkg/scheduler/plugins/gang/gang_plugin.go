package gang

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"time"
)

const (
	Name = "Gang"
)

var (
	_ framework.PreFilterPlugin  = &GangPlugin{}
	_ framework.PostFilterPlugin = &GangPlugin{}
	_ framework.ReservePlugin    = &GangPlugin{}
	_ framework.PostBindPlugin   = &GangPlugin{}
	_ framework.PostBindPlugin   = &GangPlugin{}
	_ framework.QueueSortPlugin  = &GangPlugin{}
)

type GangPlugin struct {
	frameworkHandler framework.Handle
	podLister        v1.PodLister
	gangCache        *gangCache
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	gangCache := NewGangCache()

	podInformer := handle.SharedInformerFactory().Core().V1().Pods().Informer()
	podLister := handle.SharedInformerFactory().Core().V1().Pods().Lister()
	podInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		//FilterFunc: func(obj interface{}) bool {
		//	switch t := obj.(type) {
		//	case *corev1.Pod:
		//		return CheckPodGangInfo(t)
		//	default:
		//		utilruntime.HandleError(fmt.Errorf("unable to handle object %T", obj))
		//		return false
		//	}
		//},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    gangCache.OnPodAdd,
			UpdateFunc: gangCache.OnPodUpdate,
			DeleteFunc: gangCache.OnPodDelete,
		},
	})

	gang := &GangPlugin{
		frameworkHandler: handle,
		podLister:        podLister,
		gangCache:        gangCache,
	}

	//recover the gangCache
	gang.RecoverGangCache()

	return gang, nil
}

func (p *GangPlugin) Name() string { return Name }

//Less is used to sort pods in the scheduling queue in the following order.
//Firstly, compare the priorities of the two pods, the higher priority (if pod's priority is equal,then compare their KoordinatorPriority at labels )is at the front of the queue,
//Secondly, compare creationTimestamp of two pods, if pod belongs to a Gang, then we compare creationTimestamp of the Gang, the one created first will be at the front of the queue.
//Finally, compare pod's namespace, if pod belongs to a Gang, then we compare Gang name.
func (p *GangPlugin) Less(podInfo1, podInfo2 *framework.QueuedPodInfo) bool {
	prio1 := helpers.PodPriority(podInfo1.Pod)
	prio2 := helpers.PodPriority(podInfo2.Pod)
	if prio1 != prio2 {
		return prio1 > prio2
	}
	subPrio1, err := util.GetSubPriority(podInfo1.Pod)
	if err != nil {
		klog.Errorf("GetSubPriority of the pod %s err:%v", podInfo1.Pod.Name, err)
	}
	subPrio2, err := util.GetSubPriority(podInfo2.Pod)
	if err != nil {
		klog.Errorf("GetSubPriority of the pod %s err:%v", podInfo2.Pod.Name, err)
	}
	if subPrio1 != subPrio2 {
		return subPrio1 > subPrio2
	}

	creationTime1 := p.GetCreatTime(podInfo1)
	creationTime2 := p.GetCreatTime(podInfo2)
	if creationTime1.Equal(creationTime2) {
		return util.GetNamespacedName(podInfo1.Pod) < util.GetNamespacedName(podInfo2.Pod)
	}
	return creationTime1.Before(creationTime2)
}

// PreFilter
//if non-strict-mode, we only do step1 and step2:
// i.Check whether childes in Gang has met the requirements of minimum number under each Gang, and reject the pod if negative.
// ii.Check whether the Gang has been timeout(check the pod's annotation,later introduced at Permit section), and reject the pod if positive.
// iii.Check whether the Gang has met the scheduleCycleValid check, and reject the pod if negative.
// iv.Try update scheduleCycle, scheduleCycleValid, childrenScheduleRoundMap as mentioned above.
func (p *GangPlugin) PreFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod) *framework.Status {
	gangId := GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
		pod.Annotations[extension.GangNameAnnotation])
	gang := p.gangCache.GetGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName:%v, podName:%v", gangId,
			GetNamespaceSplicingName(pod.Namespace, pod.Name))
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "can't find gang")
	}

	if gang.GetChildrenNum() < gang.GetGangMinNum() {
		klog.Errorf("gang child pod not collect enough, gangName:%v, podName:%v", gangId,
			GetNamespaceSplicingName(pod.Namespace, pod.Name))
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "gang child pod not collect enough")
	}

	if pod.Annotations[extension.GangTimeOutAnnotation] == "true" {
		klog.Errorf("gang has timeout, gangName:%v, podName:%v", gangId,
			GetNamespaceSplicingName(pod.Namespace, pod.Name))
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "gang has timeout")
	}

	gangMode := gang.GetGangMode()
	if gangMode == extension.StrictMode {
		defer gang.TryUpdateScheduleCycle()

		podScheduleCycle := gang.GetChildScheduleCycle(pod)
		gangScheduleCycle := gang.GetGangScheduleCycle()
		if podScheduleCycle >= gangScheduleCycle {
			klog.Errorf("pod's schedule cycle too large, gangName:%v, podName:%v, podCycle, gangCycle",
				gangId, GetNamespaceSplicingName(pod.Namespace, pod.Name), podScheduleCycle, gangScheduleCycle)
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, "pod's schedule cycle too large")
		}

		gang.SetChildScheduleCycle(pod, gangScheduleCycle)

		if !gang.IsGangScheduleCycleValid() {
			klog.Errorf("gang scheduleCycle not valid, gangName:%v, podName:%v",
				gangId, GetNamespaceSplicingName(pod.Namespace, pod.Name))
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, "gang scheduleCycle not valid")
		}
	}
	return framework.NewStatus(framework.Success, "")
}

// PreFilterExtensions returns a PreFilterExtensions interface if the plugin implements one.
func (p *GangPlugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// PostFilter
//i. If strict-mode, we will set scheduleCycleValid to false and release all assumed pods.
//ii. If non-strict mode, we will do nothing.
func (p *GangPlugin) PostFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod,
	filteredNodeStatusMap framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	gangId := GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
		pod.Annotations[extension.GangNameAnnotation])
	gang := p.gangCache.GetGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName:%v, podName:%v", gangId,
			GetNamespaceSplicingName(pod.Namespace, pod.Name))
		return &framework.PostFilterResult{}, framework.NewStatus(framework.UnschedulableAndUnresolvable, "can't find gang")
	}

	if gang.IsGangResourceSatisfied() {
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Success, "")
	}

	if gang.GetGangMode() == extension.StrictMode {
		p.frameworkHandler.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
			waitingGangId := GetNamespaceSplicingName(waitingPod.GetPod().Annotations[extension.GangNamespaceAnnotation],
				waitingPod.GetPod().Annotations[extension.GangNameAnnotation])
			if waitingGangId == gangId {
				klog.Errorf("postFilter rejects the pod, gangName:%v, podName:%v",
					GetNamespaceSplicingName(pod.Namespace, pod.Name), gangId)
				waitingPod.Reject(p.Name(), "gang rejection in PostFilter")
			}
		})
		gang.SetScheduleCycleValid(false)
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable,
			fmt.Sprintf("Gang %v gets rejected this cycle due to Pod %v is unschedulable even after "+
				"PostFilter in StrictMode", gangId, pod.Name))
	}

	return &framework.PostFilterResult{}, framework.NewStatus(framework.Success, "")
}

// Permit
//we will calculate all Gangs in GangGroup whether the current number of assumed-pods in each Gang meets the Gang's minimum requirement.
//and decide whether we should let the pod wait in Permit stage or let the whole gangGroup go binding
func (p *GangPlugin) Permit(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) (*framework.Status, time.Duration) {
	waitTime, s := p.PermitCheck(pod)
	var retStatus *framework.Status
	switch s {
	case extension.GangNotFoundInCache:
		return framework.NewStatus(framework.Unschedulable, "Gang not found in gangCache"), 0
	case extension.Wait:
		klog.Infof("Pod %v from gang %v is waiting to be scheduled at Permit stage",
			GetNamespaceSplicingName(pod.Namespace, pod.Name),
			GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
				pod.Annotations[extension.GangNameAnnotation]))
		retStatus = framework.NewStatus(framework.Wait)
		p.ActivateGang(pod, state)
	case extension.Success:
		p.AllowGangGroup(pod)
		retStatus = framework.NewStatus(framework.Success)
		waitTime = 0
	}
	return retStatus, waitTime
}

// Reserve is the functions invoked by the framework at "reserve" extension point.
func (p *GangPlugin) Reserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	return nil
}

// Unreserve
//(1)handle the timeout gang
//(2)do nothing when bound failed
func (p *GangPlugin) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	gangId := GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
		pod.Annotations[extension.GangNameAnnotation])
	gang := p.gangCache.GetGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName:%v, podName:%v", gangId,
			GetNamespaceSplicingName(pod.Namespace, pod.Name))
		return
	}

	//gang time out
	if !gang.IsGangResourceSatisfied() {
		klog.Infof("gang is time out,start to release the assumed resource and add annotations to the "+
			"gang's children, gangName:%v, podName:%v", gangId, GetNamespaceSplicingName(pod.Namespace, pod.Name))
		timeoutAnnotations := map[string]interface{}{
			"metadata": map[string]map[string]string{
				"Annotations": {
					extension.GangTimeOutAnnotation: "true",
				}},
		}

		pods, err := p.podLister.List(nil)
		if err != nil {
			klog.Errorf("unReserve list pod err:%v", err.Error())
			return
		}
		//add timeout annotation to all the children of the gang
		for _, pod := range pods {
			if GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
				pod.Annotations[extension.GangNameAnnotation]) == gangId {
				updateAnnotation, _ := json.Marshal(timeoutAnnotations)
				_, err := p.frameworkHandler.ClientSet().CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name,
					types.StrategicMergePatchType, updateAnnotation, metav1.PatchOptions{})
				if err != nil {
					klog.Errorf("unReserve when patch gang timeout annotation to pod:%v, err:%v",
						GetNamespaceSplicingName(pod.Namespace, pod.Name), err.Error())
				}
			}
		}

		//release resource of all assumed children of the gang
		p.frameworkHandler.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
			if GetNamespaceSplicingName(waitingPod.GetPod().Annotations[extension.GangNamespaceAnnotation],
				waitingPod.GetPod().Annotations[extension.GangNameAnnotation]) == gangId {
				klog.Errorf("unReserve rejects the pod name:%v from Gang %s due to timeout",
					GetNamespaceSplicingName(pod.Namespace, pod.Name), gangId)
				waitingPod.Reject(p.Name(), "optimistic rejection in unReserve due to timeout")
			}
		})
	}
}

// PostBind just update the gang's BoundChildren
func (p *GangPlugin) PostBind(ctx context.Context, _ *framework.CycleState, pod *corev1.Pod, nodeName string) {
	gangId := GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
		pod.Annotations[extension.GangNameAnnotation])
	gang := p.gangCache.GetGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName:%v, podName:%v", gangId,
			GetNamespaceSplicingName(pod.Namespace, pod.Name))
		return
	}

	gang.AddBoundPod(pod)
}

func (p *GangPlugin) RecoverGangCache() {
	podLister := p.frameworkHandler.SharedInformerFactory().Core().V1().Pods().Lister()
	podsList, err := podLister.List(nil)
	if err != nil {
		klog.Errorf("RecoverGangCache podsList List error %+v", err)
	}
	for _, pod := range podsList {
		p.gangCache.OnPodAdd(pod)

		if pod.Spec.NodeName != "" {
			gangId := GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
				pod.Annotations[extension.GangNameAnnotation])
			gang := p.gangCache.GetGangFromCache(gangId)
			gang.AddAssumedPod(pod)
			gang.SetResourceSatisfied()
		}
	}

	//todo recover podGroup
}

func (p *GangPlugin) PermitCheck(pod *corev1.Pod) (time.Duration, extension.Status) {
	gangId := GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
		pod.Annotations[extension.GangNameAnnotation])
	gang := p.gangCache.GetGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName:%v, podName:%v", gangId,
			GetNamespaceSplicingName(pod.Namespace, pod.Name))
		return 0, extension.GangNotFoundInCache
	}

	//first we need to add the pod to assumedMap of gang
	gang.AddAssumedPod(pod)
	gangGroup := gang.GetGangGroup()
	allGangGroupSatisfied := true
	//only the gang itself
	if len(gangGroup) == 0 {
		allGangGroupSatisfied = gang.IsGangResourceSatisfied()
	} else {
		//check each gang group
		for _, groupName := range gangGroup {
			gangTmp := p.gangCache.GetGangFromCache(groupName)
			if gangTmp != nil {
				if !gangTmp.IsGangResourceSatisfied() {
					allGangGroupSatisfied = false
					break
				}
			}
		}
	}
	if !allGangGroupSatisfied {
		return gang.WaitTime, extension.Wait
	}
	return 0, extension.Success
}

// ActivateGang
//Put all the pods belong to the Gang which in UnSchedulableQueue or backoffQueue back to activeQueue,
func (p *GangPlugin) ActivateGang(pod *corev1.Pod, state *framework.CycleState) {
	gangId := GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
		pod.Annotations[extension.GangNameAnnotation])

	pods, err := p.podLister.Pods(pod.Namespace).List(nil)
	if err != nil {
		klog.Errorf("ActivateGang Failed to list pods belong to a Gang: %v", gangId)
		return
	}

	toActivePods := make([]*corev1.Pod, 0)
	for i := range pods {
		gangIdTmp := GetNamespaceSplicingName(pods[i].Annotations[extension.GangNamespaceAnnotation],
			pods[i].Annotations[extension.GangNameAnnotation])
		if gangIdTmp == gangId {
			toActivePods = append(toActivePods, pods[i])
		}
	}

	if len(toActivePods) != 0 {
		if c, err := state.Read(framework.PodsToActivateKey); err == nil {
			if s, ok := c.(*framework.PodsToActivate); ok {
				s.Lock()
				for _, pod := range toActivePods {
					namespacedName := util.GetNamespacedName(pod)
					s.Map[namespacedName] = pod
				}
				s.Unlock()
			}
		}
	}
}

func (p *GangPlugin) AllowGangGroup(pod *corev1.Pod) {
	gangId := GetNamespaceSplicingName(pod.Annotations[extension.GangNamespaceAnnotation],
		pod.Annotations[extension.GangNameAnnotation])
	gang := p.gangCache.GetGangFromCache(gangId)
	if gang == nil {
		klog.Errorf("can't find gang, gangName:%v, podName:%v", gangId,
			GetNamespaceSplicingName(pod.Namespace, pod.Name))
		return
	}

	//allow only the gang itself
	gangSlices := make([]string, 0)
	if len(gang.GetGangGroup()) == 0 {
		gangSlices = append(gangSlices, gangId)
	} else {
		gangSlices = gang.GetGangGroup()
	}

	//allow each gang group
	for _, gangIdTmp := range gangSlices {
		p.frameworkHandler.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
			podGangId := GetNamespaceSplicingName(waitingPod.GetPod().Annotations[extension.GangNamespaceAnnotation],
				waitingPod.GetPod().Annotations[extension.GangNameAnnotation])

			if podGangId == gangIdTmp {
				klog.Infof("Permit allows pod %v from gang %v",
					GetNamespaceSplicingName(waitingPod.GetPod().Namespace, waitingPod.GetPod().Name), podGangId)
				waitingPod.Allow(p.Name())
			}
		})

		klog.Infof("Permit allows for gang", gangIdTmp)
	}
}

func (p *GangPlugin) GetCreatTime(podInfo *framework.QueuedPodInfo) time.Time {
	gangName := podInfo.Pod.Annotations[extension.GangNameAnnotation]
	//it doesn't belong to the gang,we get the creation time of the pod
	if gangName == "" {
		return podInfo.InitialAttemptTimestamp
	}
	//it belongs to a gang,we get the creation time of the Gang
	gangId := GetNamespaceSplicingName(podInfo.Pod.Annotations[extension.GangNamespaceAnnotation],
		podInfo.Pod.Annotations[extension.GangNameAnnotation])
	gang := p.gangCache.GetGangFromCache(gangId)
	if gang != nil {
		return gang.CreateTime
	}
	return time.Now()
}
