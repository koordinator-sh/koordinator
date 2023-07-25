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

package limitaware

import (
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

type Cache struct {
	lock                   sync.RWMutex
	allocSet               sets.String
	limitsAllocated        map[string]*framework.Resource
	nonZeroLimitsAllocated map[string]*framework.Resource
}

func newCache() *Cache {
	return &Cache{
		limitsAllocated:        map[string]*framework.Resource{},
		nonZeroLimitsAllocated: map[string]*framework.Resource{},
		allocSet:               sets.NewString(),
	}
}

func (c *Cache) AddPod(nodeName string, pod *corev1.Pod) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.addPod(nodeName, pod)
}

func (c *Cache) DeletePod(nodeName string, pod *corev1.Pod) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.deletePod(nodeName, pod)
}

func (c *Cache) UpdatePod(nodeName string, oldPod, newPod *corev1.Pod) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.deletePod(nodeName, oldPod)
	c.addPod(nodeName, newPod)
}

func (c *Cache) addPod(nodeName string, pod *corev1.Pod) {
	if pod == nil {
		return
	}
	podKey := util.GetNamespacedName(pod.Namespace, pod.Name)
	if c.allocSet.Has(podKey) {
		return
	}
	c.allocSet.Insert(podKey)
	addPodToAllocated(c.limitsAllocated, nodeName, getPodResourceLimit(pod, false))
	addPodToAllocated(c.nonZeroLimitsAllocated, nodeName, getPodResourceLimit(pod, true))
}

func addPodToAllocated(allocated map[string]*framework.Resource, nodeName string, podResourceLimit *framework.Resource) {
	nodeLimitsAllocated, ok := allocated[nodeName]
	if !ok {
		nodeLimitsAllocated = framework.NewResource(nil)
		allocated[nodeName] = nodeLimitsAllocated
	}

	if podResourceLimit.MilliCPU == 0 &&
		podResourceLimit.Memory == 0 &&
		podResourceLimit.EphemeralStorage == 0 &&
		len(podResourceLimit.ScalarResources) == 0 {
		return
	}
	nodeLimitsAllocated.MilliCPU += podResourceLimit.MilliCPU
	nodeLimitsAllocated.Memory += podResourceLimit.Memory
	nodeLimitsAllocated.EphemeralStorage += podResourceLimit.EphemeralStorage
	for rName, rQuant := range podResourceLimit.ScalarResources {
		if nodeLimitsAllocated.ScalarResources == nil {
			nodeLimitsAllocated.ScalarResources = map[corev1.ResourceName]int64{}
		}
		nodeLimitsAllocated.ScalarResources[rName] += rQuant
	}
}

func (c *Cache) deletePod(nodeName string, pod *corev1.Pod) {
	if pod == nil {
		return
	}
	podKey := util.GetNamespacedName(pod.Namespace, pod.Name)
	if !c.allocSet.Has(podKey) {
		return
	}
	c.allocSet.Delete(podKey)
	c.deletePodFromAllocated(c.limitsAllocated, nodeName, getPodResourceLimit(pod, false))
	c.deletePodFromAllocated(c.nonZeroLimitsAllocated, nodeName, getPodResourceLimit(pod, true))
}

func (c *Cache) deletePodFromAllocated(allocated map[string]*framework.Resource, nodeName string, podResourceLimit *framework.Resource) {
	nodeLimitsAllocated, ok := allocated[nodeName]
	if !ok {
		return
	}

	if podResourceLimit.MilliCPU == 0 &&
		podResourceLimit.Memory == 0 &&
		podResourceLimit.EphemeralStorage == 0 &&
		len(podResourceLimit.ScalarResources) == 0 {
		return
	}

	nodeLimitsAllocated.MilliCPU -= podResourceLimit.MilliCPU
	nodeLimitsAllocated.Memory -= podResourceLimit.Memory
	nodeLimitsAllocated.EphemeralStorage -= podResourceLimit.EphemeralStorage
	if len(nodeLimitsAllocated.ScalarResources) != 0 {
		for rName, rQuant := range podResourceLimit.ScalarResources {
			nodeLimitsAllocated.ScalarResources[rName] -= rQuant
			if nodeLimitsAllocated.ScalarResources[rName] == 0 {
				delete(nodeLimitsAllocated.ScalarResources, rName)
			}
		}
	}
	if podResourceLimit.MilliCPU == 0 &&
		podResourceLimit.Memory == 0 &&
		podResourceLimit.EphemeralStorage == 0 &&
		len(podResourceLimit.ScalarResources) == 0 {
		delete(c.limitsAllocated, nodeName)
	}
}

func (c *Cache) GetNodeLimits(nodeName string) *framework.Resource {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.limitsAllocated[nodeName] == nil {
		return &framework.Resource{
			ScalarResources: map[corev1.ResourceName]int64{},
		}
	}
	return c.limitsAllocated[nodeName].Clone()
}

func (c *Cache) GetNonZeroNodeLimits(nodeName string) *framework.Resource {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.nonZeroLimitsAllocated[nodeName] == nil {
		return &framework.Resource{
			ScalarResources: map[corev1.ResourceName]int64{},
		}
	}
	return c.nonZeroLimitsAllocated[nodeName].Clone()
}
