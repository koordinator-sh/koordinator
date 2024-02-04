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
	"sort"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	k8sutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	QuotaOverUsedRevokeControllerName = "QuotaOverUsedRevokeController"
)

type QuotaOverUsedGroupMonitor struct {
	groupQuotaManger             *core.GroupQuotaManager
	quotaName                    string
	lastUnderUsedTime            time.Time
	overUsedTriggerEvictDuration time.Duration
}

func NewQuotaOverUsedGroupMonitor(quotaName string, manager *core.GroupQuotaManager, overUsedTriggerEvictDuration time.Duration) *QuotaOverUsedGroupMonitor {
	return &QuotaOverUsedGroupMonitor{
		quotaName:                    quotaName,
		groupQuotaManger:             manager,
		overUsedTriggerEvictDuration: overUsedTriggerEvictDuration,
		lastUnderUsedTime:            time.Now(),
	}
}

func (monitor *QuotaOverUsedGroupMonitor) monitor() bool {
	quotaInfo := monitor.groupQuotaManger.GetQuotaInfoByName(monitor.quotaName)
	if quotaInfo == nil {
		return false
	}

	runtime := quotaInfo.GetRuntime()
	used := quotaInfo.GetUsed()

	isLessEqual, exceedDimensions := quotav1.LessThanOrEqual(used, runtime)

	var overUseContinueDuration time.Duration
	if !isLessEqual {
		overUseContinueDuration = time.Since(monitor.lastUnderUsedTime)
		if klog.V(5).Enabled() {
			klog.Infof("Quota used is large than runtime, quotaName: %v, resDimensions: %v, used: %v, runtime: %v",
				monitor.quotaName, exceedDimensions, util.DumpJSON(used), util.DumpJSON(runtime))
		}
	} else {
		monitor.lastUnderUsedTime = time.Now()
	}

	if overUseContinueDuration > monitor.overUsedTriggerEvictDuration {
		klog.V(5).Infof("Quota used continue large than runtime, prepare trigger evict, quotaName: %v, overUseContinueDuration: %v, config: %v",
			monitor.quotaName, overUseContinueDuration, monitor.overUsedTriggerEvictDuration)
		monitor.lastUnderUsedTime = time.Now()
		return true
	}
	return false
}

func (monitor *QuotaOverUsedGroupMonitor) getToRevokePodList(quotaName string) []*v1.Pod {
	quotaInfo := monitor.groupQuotaManger.GetQuotaInfoByName(quotaName)
	if quotaInfo == nil {
		return nil
	}

	runtime := quotaInfo.GetRuntime()
	used := quotaInfo.GetUsed()
	oriUsed := used.DeepCopy()

	// order pod from low priority -> high priority
	priPodCache := quotaInfo.GetPodThatIsAssigned()

	sort.Slice(priPodCache, func(i, j int) bool { return !k8sutil.MoreImportantPod(priPodCache[i], priPodCache[j]) })

	// first try revoke all until used <= runtime
	tryAssignBackPodCache := make([]*v1.Pod, 0)

	for _, pod := range priPodCache {
		if shouldBreak, _ := quotav1.LessThanOrEqual(used, runtime); shouldBreak {
			break
		}
		if extension.IsPodNonPreemptible(pod) {
			continue
		}
		podReq, _ := core.PodRequestsAndLimits(pod)
		used = quotav1.Mask(quotav1.Subtract(used, podReq), quotav1.ResourceNames(podReq))
		tryAssignBackPodCache = append(tryAssignBackPodCache, pod)
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
		pod := tryAssignBackPodCache[index]
		podRequest, _ := core.PodRequestsAndLimits(pod)
		used = quotav1.Mask(quotav1.Add(used, podRequest), quotav1.ResourceNames(podRequest))
		if canAssignBack, _ := quotav1.LessThanOrEqual(used, runtime); !canAssignBack {
			used = quotav1.Subtract(used, podRequest)
			realRevokePodCache = append(realRevokePodCache, pod)
		}
	}
	for _, pod := range realRevokePodCache {
		klog.Infof("pod should be evict by QuotaOverUseGroupMonitor, pod:%v, quotaName:%v,"+
			"used:%v, runtime:%v", pod.Name, quotaName, oriUsed, runtime)
	}
	return realRevokePodCache
}

type QuotaOverUsedRevokeController struct {
	monitorsLock                 sync.RWMutex
	monitors                     map[string]*QuotaOverUsedGroupMonitor
	overUsedTriggerEvictDuration time.Duration
	revokePodCycle               time.Duration
	monitorAllQuotas             bool
	enableRuntimeQuota           bool
	plugin                       *Plugin
}

func NewQuotaOverUsedRevokeController(plugin *Plugin) *QuotaOverUsedRevokeController {
	controller := &QuotaOverUsedRevokeController{
		plugin:                       plugin,
		overUsedTriggerEvictDuration: plugin.pluginArgs.DelayEvictTime.Duration,
		revokePodCycle:               plugin.pluginArgs.RevokePodInterval.Duration,
		monitors:                     make(map[string]*QuotaOverUsedGroupMonitor),
	}
	controller.monitorAllQuotas = plugin.pluginArgs.MonitorAllQuotas
	controller.enableRuntimeQuota = plugin.pluginArgs.EnableRuntimeQuota

	return controller
}

func (controller *QuotaOverUsedRevokeController) Name() string {
	return QuotaOverUsedRevokeControllerName
}

func (controller *QuotaOverUsedRevokeController) Start() {
	if !controller.monitorAllQuotas {
		klog.Infof("monitorAllQuotas not true. will not start elasticQuota QuotaOverUsedRevokeController")
		return
	}
	if !controller.enableRuntimeQuota {
		klog.Infof("enableRuntimeQuota is false. will not start elasticQuota QuotaOverUsedRevokeController")
		return
	}
	go wait.Until(controller.revokePodDueToQuotaOverUsed, controller.revokePodCycle, nil)
	klog.Infof("start elasticQuota QuotaOverUsedRevokeController")
}

func (controller *QuotaOverUsedRevokeController) revokePodDueToQuotaOverUsed() {
	toRevokePods := controller.monitorAll()
	for _, pod := range toRevokePods {
		if err := EvictPod(context.TODO(), controller.plugin.handle.ClientSet(), pod, &metav1.DeleteOptions{}); err != nil {
			klog.Errorf("failed to revoke pod due to quota overused, pod:%v, error:%s",
				pod.Name, err)
			continue
		}
		klog.V(5).Infof("finish revoke pod due to quota overused, pod: %v",
			pod.Name)
	}
}

func (controller *QuotaOverUsedRevokeController) monitorAll() []*v1.Pod {
	controller.syncQuota()

	monitors := controller.getToMonitorQuotas()

	toRevokePods := make([]*v1.Pod, 0)
	for quotaName, monitor := range monitors {
		toRevokePodsTmp := monitor.getToRevokePodList(quotaName)
		toRevokePods = append(toRevokePods, toRevokePodsTmp...)
	}
	return toRevokePods
}

func (controller *QuotaOverUsedRevokeController) syncQuota() {
	controller.monitorsLock.Lock()
	defer controller.monitorsLock.Unlock()

	managers := []*core.GroupQuotaManager{controller.plugin.groupQuotaManager}
	managers = append(managers, controller.plugin.ListGroupQuotaManagersForQuotaTree()...)

	allQuotaNames := make(map[string]struct{})
	for _, mgr := range managers {
		for quotaName := range mgr.GetAllQuotaNames() {
			if quotaName == extension.SystemQuotaName || quotaName == extension.RootQuotaName {
				continue
			}
			allQuotaNames[quotaName] = struct{}{}
			if controller.monitors[quotaName] == nil {
				controller.addQuota(quotaName, mgr)
			}
		}
	}

	for quotaName := range controller.monitors {
		if _, exist := allQuotaNames[quotaName]; !exist {
			controller.deleteQuota(quotaName)
		}
	}
}

func (controller *QuotaOverUsedRevokeController) addQuota(quotaName string, mgr *core.GroupQuotaManager) {
	controller.monitors[quotaName] = NewQuotaOverUsedGroupMonitor(quotaName, mgr, controller.overUsedTriggerEvictDuration)
	klog.V(5).Infof("QuotaOverUseRescheduleController add quota: %v", quotaName)
}

func (controller *QuotaOverUsedRevokeController) deleteQuota(quotaName string) {
	delete(controller.monitors, quotaName)
	klog.V(5).Infof("QuotaOverUseRescheduleController delete quota: %v", quotaName)
}

func (controller *QuotaOverUsedRevokeController) getToMonitorQuotas() map[string]*QuotaOverUsedGroupMonitor {
	monitors := make(map[string]*QuotaOverUsedGroupMonitor)

	{
		controller.monitorsLock.RLock()
		for key, value := range controller.monitors {
			monitors[key] = value
		}
		controller.monitorsLock.RUnlock()
	}

	result := make(map[string]*QuotaOverUsedGroupMonitor)

	for quotaName, monitor := range monitors {
		shouldTriggerEvict := monitor.monitor()
		if shouldTriggerEvict {
			result[quotaName] = monitor
		}
	}
	return result
}

func EvictPod(ctx context.Context, client clientset.Interface, pod *v1.Pod, deleteOptions *metav1.DeleteOptions) error {
	eviction := &policy.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		DeleteOptions: deleteOptions,
	}
	err := client.PolicyV1beta1().Evictions(eviction.Namespace).Evict(ctx, eviction)
	if apierrors.IsTooManyRequests(err) {
		return fmt.Errorf("error when evicting pod (ignoring) %q: %v", pod.Name, err)
	}
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("pod not found when evicting %q: %v", pod.Name, err)
	}
	return err
}
