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

package util

import (
	"encoding/json"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type ReleaseTargetType string
type ReleaseList map[ReleaseTargetType]corev1.ResourceList

const (
	ReleaseTargetTypeBatchResourceRequest ReleaseTargetType = "podBatchResourceRequest"
	ReleaseTargetTypeResourceUsed         ReleaseTargetType = "podUsed"
	ReleaseTargetTypeResourceRequest      ReleaseTargetType = "podResourceRequest"
	EvictedStr                                              = "evicted"
	EvictReasonPrefix                                       = "trigger by koordlet feature "
)

type PodEvictInfo struct {
	Pod *corev1.Pod

	// cpu details
	CpuUsage        float64
	MilliCPURequest int64 // cpu/mid-cpu/batch-cpu
	MilliCPUUsed    int64

	// mem details
	MemoryUsage   float64
	MemoryRequest int64 // memory/mid-memory/batch-memory
	MemoryUsed    int64

	// sort helper
	Priority      int32
	LabelPriority int64
}

type EvictTaskInfo struct {
	Reason            string
	SortedEvictPods   []*PodEvictInfo
	ReleaseTarget     ReleaseTargetType
	ToReleaseResource corev1.ResourceList
	// get relative resource list from pod for task
	GetPodResourceFunc func(*PodEvictInfo) corev1.ResourceList
}

type EvictionExecutor interface {
	Evict(pod *corev1.Pod, node *corev1.Node, releaseReason string, message string) bool
	IsPodEvicted(*corev1.Pod) bool
}

var customExecutorInitializer func(evictor *Evictor, onlyEvictByAPI bool) EvictionExecutor

type DefaultEvictionExecutor struct {
	OnlyEvictByAPI bool
	Evictor        *Evictor
}

func (d *DefaultEvictionExecutor) Evict(pod *corev1.Pod, node *corev1.Node, releaseReason string, message string) bool {
	if d.OnlyEvictByAPI {
		if d.Evictor.EvictPodIfNotEvicted(pod, releaseReason, message) {
			return true
		}
	} else {
		helpers.KillContainers(pod, releaseReason, message)
		return true
	}
	return false
}

func (d *DefaultEvictionExecutor) IsPodEvicted(pod *corev1.Pod) bool {
	return d.Evictor.IsPodEvicted(pod)
}

func SetCustomEvictionExecutorInitializer(initializer func(*Evictor, bool) EvictionExecutor) {
	customExecutorInitializer = initializer
}

func InitializeEvictionExecutor(evictor *Evictor, onlyEvictByAPI bool) EvictionExecutor {
	var executor EvictionExecutor
	if customExecutorInitializer != nil {
		executor = customExecutorInitializer(evictor, onlyEvictByAPI)
	}
	if executor != nil {
		return executor
	}
	return &DefaultEvictionExecutor{
		OnlyEvictByAPI: onlyEvictByAPI,
		Evictor:        evictor,
	}
}
func KillAndEvictPods(evictionExecutor EvictionExecutor, node *corev1.Node, tasks []*EvictTaskInfo) (map[ReleaseTargetType]corev1.ResourceList, bool) {
	releasedAll := make(map[ReleaseTargetType]corev1.ResourceList)
	evictedPodsMp := make(map[string]bool)
	releaseTypes := make(map[ReleaseTargetType][]corev1.ResourceName)
	var getPodResourceFuncs []func(*PodEvictInfo) map[ReleaseTargetType]corev1.ResourceList
	for _, task := range tasks {
		if task.ToReleaseResource == nil || len(task.ToReleaseResource) == 0 {
			continue
		}
		target := task.ReleaseTarget
		for rType, rq := range task.ToReleaseResource {
			if rq.Cmp(resource.MustParse("0")) <= 0 {
				continue
			}
			releaseTypes[target] = append(releaseTypes[target], rType)
			if releasedAll[target] == nil {
				releasedAll[target] = make(corev1.ResourceList)
			}
		}
		getPodResourceFunc := task.GetPodResourceFunc
		if _, ok := releaseTypes[target]; ok {
			getPodResourceFuncs = append(getPodResourceFuncs, func(info *PodEvictInfo) map[ReleaseTargetType]corev1.ResourceList {
				return map[ReleaseTargetType]corev1.ResourceList{
					target: getPodResourceFunc(info),
				}
			})
		}
	}
	aggregateReleaseFunc := func(info *PodEvictInfo) map[ReleaseTargetType]corev1.ResourceList {
		sum := make(map[ReleaseTargetType]corev1.ResourceList)
		for _, f := range getPodResourceFuncs {
			resource := f(info)
			// note: same content only fetched from pod once only, used max instead of added
			for t, rl := range resource {
				if _, ok := sum[t]; !ok {
					sum[t] = make(corev1.ResourceList)
				}
				sum[t] = mergeResourceListByMax(sum[t], rl)
			}
		}
		return sum
	}
	for _, task := range tasks {
		releaseTarget := task.ReleaseTarget
		podInfos := task.SortedEvictPods
		releaseReason := task.Reason
		needToRelease := subReleaseListNoNegative(task.ToReleaseResource, releasedAll[releaseTarget])
		if len(needToRelease) == 0 || isZeroResourceList(needToRelease) {
			continue
		}
		for _, info := range podInfos {
			if evictionExecutor.IsPodEvicted(info.Pod) {
				continue
			}
			podKey := util.GetPodKey(info.Pod)
			if evictedPodsMp[podKey] {
				continue
			}
			successEvict := evictionExecutor.Evict(info.Pod, node, EvictedStr, fmt.Sprintf("%v, kill pod: %v", releaseReason, info.Pod.Name))
			if successEvict {
				klog.V(4).Infof("successfully picked pod %s to evict, release reason: %v", podKey, releaseReason)
				evictedPodsMp[podKey] = true
				resource := aggregateReleaseFunc(info)
				addResource(releasedAll, resource)
				if len(subReleaseListNoNegative(needToRelease, releasedAll[releaseTarget])) == 0 {
					break
				}
			} else {
				klog.V(4).Infof("failed to pick pod %s to evict, release reason: %v", podKey, releaseReason)
			}
		}
	}
	return releasedAll, len(evictedPodsMp) > 0
}

func IsEvictionPolicyAllowed(policy string, pod *corev1.Pod) bool {
	if pod == nil || pod.Annotations == nil {
		return true
	}
	content, ok := pod.Annotations[apiext.AnnotationPodEvictPolicy]
	if !ok {
		return true
	}
	var evictPolicies []string
	err := json.Unmarshal([]byte(content), &evictPolicies)
	if err != nil {
		klog.ErrorS(fmt.Errorf("invalid evict policy"), "failed to parse pod eviction policy", "pod", klog.KObj(pod), "evictPolicy", content)
		return false
	}
	for _, p := range evictPolicies {
		if p == policy {
			return true
		}
	}
	return false
}

// EvictTaskCheck check if evict tasks finished,return not finished
func EvictTaskCheck(task *EvictTaskInfo, released ReleaseList) (bool, corev1.ResourceList) {
	if task == nil {
		return true, nil
	}
	if task.ToReleaseResource == nil || len(task.ToReleaseResource) == 0 {
		return true, nil
	}
	if released == nil {
		return false, task.ToReleaseResource
	}
	failedToRelease := subReleaseListNoNegative(task.ToReleaseResource, released[task.ReleaseTarget])
	if len(failedToRelease) > 0 {
		return false, failedToRelease
	}
	return true, nil
}

// GetRequestTypeAndValueFromPod cpu return millvalue, memory return value
func GetRequestTypeAndValueFromPod(pod *corev1.Pod, name corev1.ResourceName) (corev1.ResourceName, int64) {
	getPodResourceFunc := func(getCRequest func(*corev1.Container) int64) int64 {
		var resContainerReq int64
		for _, container := range pod.Spec.Containers {
			containerReq := getCRequest(&container)
			if containerReq <= 0 {
				containerReq = 0
			}
			resContainerReq += containerReq
		}
		return resContainerReq
	}
	var getCRequest func(*corev1.Container) int64
	priority := apiext.GetPodPriorityClassWithDefault(pod)
	var resV int64
	var resT corev1.ResourceName
	switch priority {
	case apiext.PriorityMid:
		if name == corev1.ResourceCPU {
			getCRequest = util.GetContainerMidMilliCPURequest
			resT = apiext.MidCPU
		} else if name == corev1.ResourceMemory {
			getCRequest = util.GetContainerMidMemoryByteRequest
			resT = apiext.MidMemory
		}
	case apiext.PriorityBatch:
		if name == corev1.ResourceCPU {
			getCRequest = util.GetContainerBatchMilliCPURequest
			resT = apiext.BatchCPU
		} else if name == corev1.ResourceMemory {
			getCRequest = util.GetContainerBatchMemoryByteRequest
			resT = apiext.BatchMemory
		}
	default:
		if name == corev1.ResourceCPU {
			rq := util.GetPodRequest(pod, corev1.ResourceCPU)
			resV = rq.Cpu().MilliValue()
			resT = corev1.ResourceCPU
		} else if name == corev1.ResourceMemory {
			rq := util.GetPodRequest(pod, corev1.ResourceMemory)
			resV = rq.Memory().Value()
			resT = corev1.ResourceMemory
		}
		return resT, resV
	}
	resV = getPodResourceFunc(getCRequest)
	return resT, resV
}
func GetRequestFromPod(pod *corev1.Pod, name corev1.ResourceName) corev1.ResourceList {
	resT, resV := GetRequestTypeAndValueFromPod(pod, name)
	return corev1.ResourceList{
		resT: ConvertInt64ToQuantity(resT, resV),
	}
}
func isZeroResourceList(a corev1.ResourceList) bool {
	for _, q := range a {
		if q.Cmp(resource.MustParse("0")) != 0 {
			return false
		}
	}
	return true
}
func subReleaseListNoNegative(a, b corev1.ResourceList) corev1.ResourceList {
	if a == nil {
		return make(corev1.ResourceList)
	}
	if b == nil {
		b = make(corev1.ResourceList)
	}
	res := make(corev1.ResourceList)
	for resourceName, aq := range a {
		bq, ok := b[resourceName]
		if !ok {
			bq = resource.MustParse("0")
		}
		if aq.Cmp(bq) == 1 {
			aq.Sub(bq)
			res[resourceName] = aq
		}
	}
	return res
}

func GetPodPriorityLabel(pod *corev1.Pod, defaultPriority int64) int64 {
	if pod == nil || pod.Labels == nil {
		return defaultPriority
	}
	if fractionStr, ok := pod.Labels[apiext.LabelPodPriority]; !ok {
		return defaultPriority
	} else {
		num, err := strconv.Atoi(fractionStr)
		if err != nil {
			return defaultPriority
		}
		return int64(num)
	}
}

// merge b to a
func mergeResourceListByMax(a, b corev1.ResourceList) corev1.ResourceList {
	res := a.DeepCopy()
	if res == nil {
		res = make(corev1.ResourceList)
	}
	for rt, rqb := range b {
		if rq, _ := res[rt]; rq.Cmp(rqb) < 0 {
			res[rt] = rqb
		}
	}
	return res
}

func addResource(a, b map[ReleaseTargetType]corev1.ResourceList) {
	if a == nil {
		a = make(map[ReleaseTargetType]corev1.ResourceList)
	}
	for t, bRL := range b {
		if _, ok := a[t]; !ok {
			a[t] = make(corev1.ResourceList)
		}
		util.AddResourceList(a[t], bRL)
	}
}

func ConvertQuantityToInt64(resourceName corev1.ResourceName, quantity resource.Quantity) int64 {
	switch resourceName {
	case corev1.ResourceCPU:
		return quantity.MilliValue()
	default:
		// include corev1.ResourceMemory, apiext.BatchCPU, apiext.BatchMemory, apiext.MidCPU, apiext.BatchMemory
		return quantity.Value()
	}
}

func ConvertInt64ToQuantity(resourceName corev1.ResourceName, value int64) resource.Quantity {
	switch resourceName {
	case corev1.ResourceCPU:
		return *resource.NewMilliQuantity(value, resource.DecimalSI)
	case apiext.BatchCPU, apiext.MidCPU:
		return *resource.NewQuantity(value, resource.DecimalSI)
	default:
		// include corev1.ResourceMemory, apiext.BatchMemory, apiext.BatchMemory
		return *resource.NewQuantity(value, resource.BinarySI)
	}
}
