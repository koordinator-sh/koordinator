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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

type podAssignCache struct {
	lock         sync.RWMutex
	podInfoItems map[string]map[types.UID]*podInfo
}

type podInfo struct {
	timestamp time.Time
	pod       *corev1.Pod
}

func newPodAssignCache() *podAssignCache {
	return &podAssignCache{
		podInfoItems: map[string]map[types.UID]*podInfo{},
	}
}

func (p *podAssignCache) assign(nodeName string, pod *corev1.Pod) {
	if nodeName == "" {
		return
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	m := p.podInfoItems[nodeName]
	if m == nil {
		m = make(map[types.UID]*podInfo)
		p.podInfoItems[nodeName] = m
	}
	m[pod.UID] = &podInfo{
		timestamp: time.Now(),
		pod:       pod,
	}
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

func (p *podAssignCache) OnAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	p.assign(pod.Spec.NodeName, pod)
}

func (p *podAssignCache) OnUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}
	p.assign(pod.Spec.NodeName, pod)
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
