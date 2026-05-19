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

package arbitrator

import (
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	types "k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
	k8sfwk "k8s.io/kubernetes/pkg/scheduler/framework"
)

type Allocation struct {
	PodUID        types.UID
	PodName       string
	Namespace     string
	NodeName      string
	SchedulerName string
	CPUs          cpuset.CPUSet
	GPUMinors     []int
	Resources     corev1.ResourceList
	Expiry        time.Time
}

type ArbitratorCache struct {
	lock            sync.RWMutex
	allocations     map[types.UID]*Allocation
	nodeAllocations map[string]map[types.UID]*Allocation
	ttl             time.Duration
}

var globalArbitratorCache = NewArbitratorCache()

func GetGlobalArbitratorCache() *ArbitratorCache {
	return globalArbitratorCache
}

func NewArbitratorCache() *ArbitratorCache {
	return &ArbitratorCache{
		allocations:     make(map[types.UID]*Allocation),
		nodeAllocations: make(map[string]map[types.UID]*Allocation),
		ttl:             5 * time.Minute,
	}
}

func (c *ArbitratorCache) Reset() {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.allocations = make(map[types.UID]*Allocation)
	c.nodeAllocations = make(map[string]map[types.UID]*Allocation)
}

func (c *ArbitratorCache) ReserveProposal(pod *corev1.Pod, nodeInfo *k8sfwk.NodeInfo, schedulerName string) error {
	if pod == nil || nodeInfo == nil || nodeInfo.Node() == nil {
		return fmt.Errorf("invalid pod or nodeInfo")
	}
	node := nodeInfo.Node()

	c.lock.Lock()
	defer c.lock.Unlock()

	c.cleanExpiredProposalsNoLock()

	// Parse resources from pod
	podCPUset := cpuset.NewCPUSet()
	if resourceStatus, err := apiext.GetResourceStatus(pod.Annotations); err == nil && resourceStatus != nil && resourceStatus.CPUSet != "" {
		if set, parseErr := cpuset.Parse(resourceStatus.CPUSet); parseErr == nil {
			podCPUset = set
		}
	}

	var podGPUMinors []int
	if deviceAllocations, err := apiext.GetDeviceAllocations(pod.Annotations); err == nil && deviceAllocations != nil {
		for _, allocations := range deviceAllocations {
			for _, alloc := range allocations {
				podGPUMinors = append(podGPUMinors, int(alloc.Minor))
			}
		}
	}

	podRequest := getPodRequest(pod)

	// Accumulate resources for standard capacity check
	totalMilliCPU := int64(0)
	totalMemory := int64(0)

	if nodeInfo.Requested != nil {
		totalMilliCPU += nodeInfo.Requested.MilliCPU
		totalMemory += nodeInfo.Requested.Memory
	}

	// Check conflicts against existing active allocations on the same node
	nodeAllocations, ok := c.nodeAllocations[node.Name]
	if ok {
		for _, alloc := range nodeAllocations {
			if alloc.PodUID == pod.UID {
				continue
			}

			// 1. CPUSet conflict check
			if !podCPUset.IsEmpty() && !alloc.CPUs.IsEmpty() {
				intersection := podCPUset.Intersection(alloc.CPUs)
				if intersection.Size() > 0 {
					return fmt.Errorf("CPUSet conflict on node %s with pod %s/%s: overlap CPUs %s", node.Name, alloc.Namespace, alloc.PodName, intersection.String())
				}
			}

			// 2. GPU minor conflict check
			if len(podGPUMinors) > 0 && len(alloc.GPUMinors) > 0 {
				for _, minor := range podGPUMinors {
					for _, otherMinor := range alloc.GPUMinors {
						if minor == otherMinor {
							return fmt.Errorf("GPU minor conflict on node %s with pod %s/%s: double-booking GPU minor %d", node.Name, alloc.Namespace, alloc.PodName, minor)
						}
					}
				}
			}

			// Add requested resources from concurrent proposals
			if cpu := alloc.Resources.Cpu(); cpu != nil {
				totalMilliCPU += cpu.MilliValue()
			}
			if mem := alloc.Resources.Memory(); mem != nil {
				totalMemory += mem.Value()
			}
		}
	}

	// 3. Standard capacity conflict check
	if allocatableCPU := node.Status.Allocatable.Cpu(); allocatableCPU != nil && !allocatableCPU.IsZero() {
		reqCPU := podRequest.Cpu()
		totalMilliCPU += reqCPU.MilliValue()
		if totalMilliCPU > allocatableCPU.MilliValue() {
			return fmt.Errorf("standard CPU capacity conflict on node %s: requested %s, currently reserved (bound+inflight) %dm, allocatable %s", node.Name, reqCPU.String(), totalMilliCPU, allocatableCPU.String())
		}
	}
	if allocatableMem := node.Status.Allocatable.Memory(); allocatableMem != nil && !allocatableMem.IsZero() {
		reqMem := podRequest.Memory()
		totalMemory += reqMem.Value()
		if totalMemory > allocatableMem.Value() {
			return fmt.Errorf("standard Memory capacity conflict on node %s: requested %s, currently reserved (bound+inflight) %v, allocatable %s", node.Name, reqMem.String(), totalMemory, allocatableMem.String())
		}
	}

	// Register the proposal
	alloc := &Allocation{
		PodUID:        pod.UID,
		PodName:       pod.Name,
		Namespace:     pod.Namespace,
		NodeName:      node.Name,
		SchedulerName: schedulerName,
		CPUs:          podCPUset,
		GPUMinors:     podGPUMinors,
		Resources:     podRequest,
		Expiry:        time.Now().Add(c.ttl),
	}

	c.allocations[pod.UID] = alloc
	if !ok {
		nodeAllocations = make(map[types.UID]*Allocation)
		c.nodeAllocations[node.Name] = nodeAllocations
	}
	nodeAllocations[pod.UID] = alloc

	klog.V(4).Infof("Successfully reserved proposal for pod %s/%s on node %s by scheduler %s", pod.Namespace, pod.Name, node.Name, schedulerName)
	return nil
}

func (c *ArbitratorCache) ReleaseProposal(podUID types.UID) {
	c.lock.Lock()
	defer c.lock.Unlock()

	alloc, ok := c.allocations[podUID]
	if !ok {
		return
	}

	delete(c.allocations, podUID)
	if nodeAllocations, exists := c.nodeAllocations[alloc.NodeName]; exists {
		delete(nodeAllocations, podUID)
		if len(nodeAllocations) == 0 {
			delete(c.nodeAllocations, alloc.NodeName)
		}
	}
	klog.V(4).Infof("Successfully released proposal for pod UID %s on node %s", podUID, alloc.NodeName)
}

func (c *ArbitratorCache) cleanExpiredProposalsNoLock() {
	now := time.Now()
	for uid, alloc := range c.allocations {
		if now.After(alloc.Expiry) {
			delete(c.allocations, uid)
			if nodeAllocations, exists := c.nodeAllocations[alloc.NodeName]; exists {
				delete(nodeAllocations, uid)
				if len(nodeAllocations) == 0 {
					delete(c.nodeAllocations, alloc.NodeName)
				}
			}
			klog.V(4).Infof("Cleaned expired proposal for pod UID %s on node %s", uid, alloc.NodeName)
		}
	}
}

func (c *ArbitratorCache) CheckPreBind(podUID types.UID, schedulerName string) error {
	c.lock.RLock()
	defer c.lock.RUnlock()

	alloc, ok := c.allocations[podUID]
	if !ok {
		return fmt.Errorf("arbitration proposal not found during pre-bind")
	}

	if alloc.SchedulerName != schedulerName {
		return fmt.Errorf("arbitration check failed: expected scheduler %s, got %s", alloc.SchedulerName, schedulerName)
	}

	return nil
}

func getPodRequest(pod *corev1.Pod) corev1.ResourceList {
	res := corev1.ResourceList{}
	for _, container := range pod.Spec.Containers {
		for k, v := range container.Resources.Requests {
			if quantity, ok := res[k]; ok {
				quantity.Add(v)
				res[k] = quantity
			} else {
				res[k] = v.DeepCopy()
			}
		}
	}
	return res
}
