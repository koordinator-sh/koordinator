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
	"sync"

	"github.com/emirpasic/gods/sets/treeset"
	v1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/api/v1/resource"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

type QuotaOverUsedGroupMonitor struct {
	groupQuotaManger                  *core.GroupQuotaManager
	quotaName                         string
	overUsedContinueCount             int64
	continueOverUsedCountTriggerEvict int64
	podCache                          map[string]*v1.Pod
	lock                              sync.RWMutex
}

func NewQuotaOverUsedGroupMonitor(quotaName string, manager *core.GroupQuotaManager, continueOverUsedCountTriggerEvict int64) *QuotaOverUsedGroupMonitor {
	return &QuotaOverUsedGroupMonitor{
		quotaName:                         quotaName,
		groupQuotaManger:                  manager,
		continueOverUsedCountTriggerEvict: continueOverUsedCountTriggerEvict,
		podCache:                          make(map[string]*v1.Pod),
	}
}

func (monitor *QuotaOverUsedGroupMonitor) addPod(pod *v1.Pod) {
	monitor.lock.Lock()
	defer monitor.lock.Unlock()

	monitor.podCache[pod.Name] = pod
}

func (monitor *QuotaOverUsedGroupMonitor) erasePod(pod *v1.Pod) {
	monitor.lock.Lock()
	defer monitor.lock.Unlock()

	delete(monitor.podCache, pod.Name)
}

func (monitor *QuotaOverUsedGroupMonitor) Monitor() bool {
	quotaInfo := monitor.groupQuotaManger.GetQuotaInfoByName(monitor.quotaName)
	if quotaInfo == nil {
		return false
	}

	runtime := quotaInfo.GetRuntime()
	used := quotaInfo.GetUsed()

	hasExceedRes := false
	for resName, runtimeQuantity := range runtime {
		usedQuantity := used[resName]
		if usedQuantity.Cmp(runtimeQuantity) == 1 {
			hasExceedRes = true
			klog.Infof("Quota used is large than runtime, quotaName:%v, resDimension:%v, used:%v, "+
				"runtime:%v, overUseContinueCount:%v", monitor.quotaName, resName, used, runtime, monitor.overUsedContinueCount+1)
			break
		}
	}

	if hasExceedRes {
		monitor.overUsedContinueCount += 1
	} else {
		monitor.overUsedContinueCount = 0
	}

	if monitor.overUsedContinueCount >= monitor.continueOverUsedCountTriggerEvict {
		klog.Infof("Quota used continue large than runtime, prepare trigger evict, quotaName:%v,"+
			"overUseContinueCount:%v, config:%v", monitor.quotaName, monitor.overUsedContinueCount,
			monitor.continueOverUsedCountTriggerEvict)
		monitor.overUsedContinueCount = 0
		return true
	}
	return false
}

func (monitor *QuotaOverUsedGroupMonitor) GetToRevokePodList(quotaName string) []*v1.Pod {
	monitor.lock.RLock()
	defer monitor.lock.RUnlock()

	quotaInfo := monitor.groupQuotaManger.GetQuotaInfoByName(quotaName)
	if quotaInfo == nil {
		return nil
	}

	runtime := quotaInfo.GetRuntime()
	used := quotaInfo.GetUsed()
	oriUsed := used.DeepCopy()

	// order pod from low priority -> high priority
	priPodCache := treeset.NewWith(PodPriorityComparator)
	for _, pod := range monitor.podCache {
		priPodCache.Add(pod)
	}

	// first try revoke all until used <= runtime
	tryAssignBackPodCache := make([]*v1.Pod, 0)
	iter := priPodCache.Iterator()
	for iter.Next() {
		if shouldBreak, _ := quotav1.LessThanOrEqual(used, runtime); shouldBreak {
			break
		}

		func() {
			pod := iter.Value().(*v1.Pod)
			podReq, _ := resource.PodRequestsAndLimits(pod)

			used = quotav1.Subtract(used, podReq)
			tryAssignBackPodCache = append(tryAssignBackPodCache, pod)
		}()
	}

	// means should evict all
	if lessThanOrEqual, _ := quotav1.LessThanOrEqual(used, runtime); !lessThanOrEqual {
		for _, pod := range tryAssignBackPodCache {
			klog.Infof("pod should be revoked by QuotaOverUsedMonitor, pod:%v, quotaName:%v"+
				"used:%v, runtime:%v", pod.Name, quotaName, oriUsed, runtime)
		}
		return tryAssignBackPodCache
	}

	//try assign back from high->low
	realRevokePodCache := make([]*v1.Pod, 0)
	for index := len(tryAssignBackPodCache) - 1; index >= 0; index-- {
		func() {
			pod := tryAssignBackPodCache[index]
			podRequest, _ := resource.PodRequestsAndLimits(pod)

			used = quotav1.Add(used, podRequest)
			if canAssignBack, _ := quotav1.LessThanOrEqual(used, runtime); !canAssignBack {
				used = quotav1.Subtract(used, podRequest)
				realRevokePodCache = append(realRevokePodCache, pod)
			}
		}()
	}
	for _, pod := range realRevokePodCache {
		klog.Infof("pod should be evict by QuotaOverUseGroupMonitor, pod:%v, quotaName:%v,"+
			"used:%v, runtime:%v", pod.Name, quotaName, oriUsed, runtime)
	}
	return realRevokePodCache
}

func PodPriorityComparator(a, b interface{}) int {
	lhs := a.(*v1.Pod)
	rhs := b.(*v1.Pod)

	if corev1.PodPriority(lhs) > corev1.PodPriority(rhs) {
		return 1
	} else if corev1.PodPriority(lhs) < corev1.PodPriority(rhs) {
		return -1
	}

	return 0
}

type QuotaOverUsedRevokeController struct {
	groupQuotaManger                  *core.GroupQuotaManager
	group2MonitorMapLock              sync.RWMutex
	group2MonitorMap                  map[string]*QuotaOverUsedGroupMonitor
	continueOverUsedCountTriggerEvict int64
}

func NewQuotaOverUsedRevokeController(continueOverUsedCountTriggerEvict int64, groupQuotaManager *core.GroupQuotaManager) *QuotaOverUsedRevokeController {
	controller := &QuotaOverUsedRevokeController{
		groupQuotaManger:                  groupQuotaManager,
		continueOverUsedCountTriggerEvict: continueOverUsedCountTriggerEvict,
		group2MonitorMap:                  make(map[string]*QuotaOverUsedGroupMonitor),
	}
	return controller
}

func (controller *QuotaOverUsedRevokeController) MonitorAll() []*v1.Pod {
	controller.SyncQuota()

	monitors := controller.GetToMonitorQuotas()

	toRevokePods := make([]*v1.Pod, 0)
	for quotaName, monitor := range monitors {
		toRevokePodsTmp := monitor.GetToRevokePodList(quotaName)
		toRevokePods = append(toRevokePods, toRevokePodsTmp...)
	}
	return toRevokePods
}

// addPodWithQuotaName only called when Reserve
func (controller *QuotaOverUsedRevokeController) addPodWithQuotaName(quotaName string, pod *v1.Pod) {
	controller.group2MonitorMapLock.RLock()
	defer controller.group2MonitorMapLock.RUnlock()

	if monitor, ok := controller.group2MonitorMap[quotaName]; ok {
		monitor.addPod(pod)
	}
}

// erasePodWithQuotaName only called when Unreserve
func (controller *QuotaOverUsedRevokeController) erasePodWithQuotaName(quotaName string, pod *v1.Pod) {
	controller.group2MonitorMapLock.RLock()
	defer controller.group2MonitorMapLock.RUnlock()

	if monitor, ok := controller.group2MonitorMap[quotaName]; ok {
		monitor.erasePod(pod)
	}
}

func (controller *QuotaOverUsedRevokeController) SyncQuota() {
	controller.group2MonitorMapLock.Lock()
	defer controller.group2MonitorMapLock.Unlock()

	allQuotaInfos := controller.groupQuotaManger.GetQuotaInfoMap()

	for quotaName := range allQuotaInfos {
		if quotaName == extension.SystemQuotaName || quotaName == extension.RootQuotaName {
			continue
		}

		if controller.group2MonitorMap[quotaName] == nil {
			controller.addQuota(quotaName)
		}
	}

	for quotaName := range controller.group2MonitorMap {
		if _, exist := allQuotaInfos[quotaName]; !exist {
			controller.deleteQuota(quotaName)
		}
	}
}

func (controller *QuotaOverUsedRevokeController) addQuota(quotaName string) {
	controller.group2MonitorMap[quotaName] = NewQuotaOverUsedGroupMonitor(quotaName, controller.groupQuotaManger, controller.continueOverUsedCountTriggerEvict)
	klog.V(5).Infof("QuotaOverUseRescheduleController add quota:%v", quotaName)
}

func (controller *QuotaOverUsedRevokeController) deleteQuota(quotaName string) {
	delete(controller.group2MonitorMap, quotaName)
	klog.V(5).Infof("QuotaOverUseRescheduleController delete quota:%v", quotaName)
}

func (controller *QuotaOverUsedRevokeController) GetToMonitorQuotas() map[string]*QuotaOverUsedGroupMonitor {
	group2MonitorMap := make(map[string]*QuotaOverUsedGroupMonitor)

	{
		controller.group2MonitorMapLock.RLock()
		for key, value := range controller.group2MonitorMap {
			group2MonitorMap[key] = value
		}
		controller.group2MonitorMapLock.RUnlock()
	}

	result := make(map[string]*QuotaOverUsedGroupMonitor)

	for quotaName, monitor := range group2MonitorMap {
		shouldTriggerEvict := monitor.Monitor()
		if shouldTriggerEvict {
			result[quotaName] = monitor
		}
	}
	return result
}
