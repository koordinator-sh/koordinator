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
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
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
	MilliCPURequest int64 // for batch-resource
	MilliCPUUsed    int64

	// mem details
	MemoryUsage   float64
	MemoryRequest int64 // for batch-resource
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

func KillAndEvictPods(evictionExecutor EvictionExecutor, node *corev1.Node, tasks []*EvictTaskInfo) (ReleaseList, bool) {
	releasedAll := make(ReleaseList)
	evictedPodsMp := make(map[string]bool)
	releaseResourceTypeMp := make(map[corev1.ResourceName]bool)
	var releaseTargetTypes []ReleaseTargetType
	for _, task := range tasks {
		if task.ToReleaseResource == nil || len(task.ToReleaseResource) == 0 {
			continue
		}
		if _, ok := releasedAll[task.ReleaseTarget]; !ok {
			releasedAll[task.ReleaseTarget] = make(corev1.ResourceList)
			releaseTargetTypes = append(releaseTargetTypes, task.ReleaseTarget)
		}
		for rType, releaseValue := range task.ToReleaseResource {
			if releaseValue.Cmp(resource.MustParse("0")) <= 0 {
				continue
			}
			releaseResourceTypeMp[rType] = true
		}
	}
	for _, task := range tasks {
		releaseTarget := task.ReleaseTarget
		podInfos := task.SortedEvictPods
		releaseReason := task.Reason
		needToRelease := subReleaseListNoNegative(task.ToReleaseResource, releasedAll[releaseTarget])
		released := make(ReleaseList)
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
				updateReleasedByPod(info, releaseTargetTypes, releaseResourceTypeMp, released)
				if len(subReleaseListNoNegative(needToRelease, released[releaseTarget])) == 0 {
					break
				}
			} else {
				klog.V(4).Infof("failed to pick pod %s to evict, release reason: %v", podKey, releaseReason)
			}
		}
		addReleased(releasedAll, released)
	}
	return releasedAll, len(evictedPodsMp) > 0
}

// EvictTaskCheck check if evict tasks finished,return not finished
func EvictTaskCheck(task *EvictTaskInfo, released ReleaseList) (bool, corev1.ResourceList) {
	if task == nil {
		return true, nil
	}
	if task.ToReleaseResource == nil || len(task.ToReleaseResource) == 0 {
		return true, nil
	}
	failedToRelease := make(corev1.ResourceList)
	if released == nil {
		failedToRelease = task.ToReleaseResource
		return false, failedToRelease
	}
	rl, ok := released[task.ReleaseTarget]
	if !ok {
		rl = make(corev1.ResourceList)
	}
	for rType, rValue := range task.ToReleaseResource {
		if rValue.Cmp(rl[rType]) == 1 {
			rValue.Sub(rl[rType])
			failedToRelease[rType] = rValue
		}
	}
	if len(failedToRelease) > 0 {
		return false, failedToRelease
	}
	return true, nil
}

func updateReleasedByPod(podInfo *PodEvictInfo, releaseTargetTypes []ReleaseTargetType, releaseResourceTypes map[corev1.ResourceName]bool, released ReleaseList) {
	// Note: released can not be nil
	if released == nil {
		klog.Warningf("release can not be nil")
		return
	}
	for _, releaseType := range releaseTargetTypes {
		if _, ok := released[releaseType]; !ok {
			released[releaseType] = make(corev1.ResourceList)
		}
		var memory, milliCPU int64
		switch releaseType {
		case ReleaseTargetTypeBatchResourceRequest:
			milliCPU = podInfo.MilliCPURequest
			memory = podInfo.MemoryRequest
		case ReleaseTargetTypeResourceUsed:
			milliCPU = podInfo.MilliCPUUsed
			memory = podInfo.MemoryUsed
		}
		if val, ok := releaseResourceTypes[corev1.ResourceCPU]; ok && val {
			value := released[releaseType][corev1.ResourceCPU]
			value.Add(*resource.NewMilliQuantity(milliCPU, resource.DecimalSI))
			released[releaseType][corev1.ResourceCPU] = value
		}
		if val, ok := releaseResourceTypes[corev1.ResourceMemory]; ok && val {
			value := released[releaseType][corev1.ResourceMemory]
			value.Add(*resource.NewQuantity(memory, resource.BinarySI))
			released[releaseType][corev1.ResourceMemory] = value
		}
	}
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

// addReleased: add b to a
func addReleased(a, b ReleaseList) {
	if a == nil {
		klog.Warningf("releaseList a can not be nil")
		return
	}
	for releaseType, bValue := range b {
		if _, ok := a[releaseType]; !ok {
			a[releaseType] = make(corev1.ResourceList)
		}
		a[releaseType] = quotav1.Add(a[releaseType], bValue)
	}
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
