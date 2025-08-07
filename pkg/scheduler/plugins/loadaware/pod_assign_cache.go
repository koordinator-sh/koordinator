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

package loadaware

import (
	"reflect"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware/estimator"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var (
	timeNowFn = time.Now
)

// podAssignCache stores the Pod information that has been successfully scheduled or is about to be bound
type podAssignCache struct {
	lock sync.RWMutex
	// podInfoItems stores podAssignInfo according to each node.
	// podAssignInfo is indexed using the Pod's types.UID
	podInfoItems map[string]map[types.UID]*podAssignInfo
	estimator    estimator.Estimator
	args         *config.LoadAwareSchedulingArgs
}

type podAssignInfo struct {
	timestamp         time.Time
	pod               *corev1.Pod
	estimated         map[corev1.ResourceName]int64
	estimatedDeadline time.Time
}

func newPodAssignCache(estimator estimator.Estimator, args *config.LoadAwareSchedulingArgs) *podAssignCache {
	return &podAssignCache{
		podInfoItems: map[string]map[types.UID]*podAssignInfo{},
		estimator:    estimator,
		args:         args,
	}
}

func (p *podAssignCache) getPodAssignInfo(nodeName string, pod *corev1.Pod) *podAssignInfo {
	if nodeName == "" {
		return nil
	}
	p.lock.RLock()
	defer p.lock.RUnlock()
	m := p.podInfoItems[nodeName]
	if m == nil {
		return nil
	}
	if info, ok := m[pod.UID]; ok {
		return info
	}
	return nil
}

func (p *podAssignCache) getPodsAssignInfoOnNode(nodeName string) []*podAssignInfo {
	p.lock.RLock()
	defer p.lock.RUnlock()
	m := p.podInfoItems[nodeName]
	if m == nil {
		return nil
	}
	podInfos := make([]*podAssignInfo, 0, len(m))
	for _, info := range m {
		podInfos = append(podInfos, info)
	}
	return podInfos
}

func (p *podAssignCache) assign(nodeName string, pod *corev1.Pod) {
	if nodeName == "" || util.IsPodTerminated(pod) {
		return
	}
	estimated, err := p.estimator.EstimatePod(pod)
	if err != nil || len(estimated) == 0 {
		estimated = nil
	}
	var timestamp time.Time
	// try to use time from PodScheduled condition first
	if _, c := podutil.GetPodCondition(&pod.Status, corev1.PodScheduled); c != nil && c.Status == corev1.ConditionTrue && !c.LastTransitionTime.IsZero() {
		timestamp = c.LastTransitionTime.Time
	} else {
		// if PodScheduled condition not found, fallback to use assign timestamp from scheduler internal, which cannot be zero.
		timestamp = timeNowFn()
	}
	estimatedDeadline := p.shouldEstimatePodDeadline(pod, timestamp)
	p.lock.Lock()
	defer p.lock.Unlock()
	m := p.podInfoItems[nodeName]
	if m == nil {
		m = make(map[types.UID]*podAssignInfo)
		p.podInfoItems[nodeName] = m
	}

	if info, ok := m[pod.UID]; ok {
		info.timestamp = timestamp
		info.pod = pod
		info.estimated = estimated
		info.estimatedDeadline = estimatedDeadline
	} else {
		m[pod.UID] = &podAssignInfo{
			timestamp:         timestamp,
			pod:               pod,
			estimated:         estimated,
			estimatedDeadline: estimatedDeadline,
		}
	}
}

func (p *podAssignCache) shouldEstimatePodDeadline(pod *corev1.Pod, timestamp time.Time) time.Time {
	var afterPodScheduled, afterInitialized int64 = -1, -1
	if p.args.AllowCustomizeEstimation {
		afterPodScheduled = extension.GetCustomEstimatedSecondsAfterPodScheduled(pod)
		afterInitialized = extension.GetCustomEstimatedSecondsAfterInitialized(pod)
	}
	if s := p.args.EstimatedSecondsAfterPodScheduled; s != nil && afterPodScheduled < 0 {
		afterPodScheduled = *s
	}
	if s := p.args.EstimatedSecondsAfterInitialized; s != nil && afterInitialized < 0 {
		afterInitialized = *s
	}
	if afterInitialized > 0 {
		if _, c := podutil.GetPodCondition(&pod.Status, corev1.PodInitialized); c != nil && c.Status == corev1.ConditionTrue {
			// if EstimatedSecondsAfterPodScheduled is set and pod is initialized, ignore EstimatedSecondsAfterPodScheduled
			// EstimatedSecondsAfterPodScheduled might be set to a long duration to wait for time consuming init containers in pod.
			if t := c.LastTransitionTime; !t.IsZero() {
				return t.Add(time.Duration(afterInitialized) * time.Second)
			}
		}
	}
	if afterPodScheduled > 0 && !timestamp.IsZero() {
		return timestamp.Add(time.Duration(afterPodScheduled) * time.Second)
	}
	return time.Time{}
}

func (p *podAssignCache) unAssign(nodeName string, pod *corev1.Pod) {
	if nodeName == "" {
		return
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	delete(p.podInfoItems[nodeName], pod.UID)
	if len(p.podInfoItems[nodeName]) == 0 {
		delete(p.podInfoItems, nodeName)
	}
}

func (p *podAssignCache) OnAdd(obj interface{}, isInInitialList bool) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	p.assign(pod.Spec.NodeName, pod)
}

func (p *podAssignCache) OnUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*corev1.Pod)
	if !ok || pod == nil {
		return
	}
	switch oldPodInfo := p.getPodAssignInfo(pod.Spec.NodeName, pod); {
	case oldPodInfo == nil: // pod was not cached
		p.assign(pod.Spec.NodeName, pod)
	case util.IsPodTerminated(pod): // pod has nodeName & pod become terminated
		p.unAssign(pod.Spec.NodeName, pod)
	case !reflect.DeepEqual(&pod.Spec, &oldPodInfo.pod.Spec) ||
		!reflect.DeepEqual(pod.Status.Conditions, oldPodInfo.pod.Status.Conditions):
		// pod spec or pod conditions changed, renew cached pod
		p.assign(pod.Spec.NodeName, pod)
	}
}

func (p *podAssignCache) OnDelete(obj interface{}) {
	var pod *corev1.Pod
	switch t := obj.(type) {
	case *corev1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		pod, ok = t.Obj.(*corev1.Pod)
		if !ok {
			return
		}
	default:
		return
	}
	p.unAssign(pod.Spec.NodeName, pod)
}
