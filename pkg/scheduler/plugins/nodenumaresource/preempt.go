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

package nodenumaresource

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

type preemptibleAlloc struct {
	cpusToAdd     cpuset.CPUSet
	cpusToRemove  cpuset.CPUSet // only one CPUSet cannot store the removing info
	numaResources map[int]corev1.ResourceList
}

func newPreemptibleAlloc() *preemptibleAlloc {
	return &preemptibleAlloc{
		cpusToAdd:     cpuset.NewCPUSet(),
		cpusToRemove:  cpuset.NewCPUSet(),
		numaResources: map[int]corev1.ResourceList{},
	}
}

func (a *preemptibleAlloc) Clone() *preemptibleAlloc {
	return &preemptibleAlloc{
		cpusToAdd:     a.cpusToAdd.Clone(),
		cpusToRemove:  a.cpusToRemove.Clone(),
		numaResources: copyAllocated(a.numaResources),
	}
}

func (a *preemptibleAlloc) AppendCPUSet(cpus cpuset.CPUSet) cpuset.CPUSet {
	return cpus.Union(a.cpusToAdd).Difference(a.cpusToRemove)
}

func (a *preemptibleAlloc) AppendNUMAResources(numaResources map[int]corev1.ResourceList) map[int]corev1.ResourceList {
	return appendAllocated(nil, a.numaResources, numaResources)
}

func (a *preemptibleAlloc) Accumulate(cpus cpuset.CPUSet, numaResources map[int]corev1.ResourceList) {
	if len(numaResources) > 0 {
		a.numaResources = appendAllocated(a.numaResources, numaResources)
	}

	if !cpus.IsEmpty() {
		if !a.cpusToRemove.IsEmpty() {
			cpusNotAdd := a.cpusToRemove.Intersection(cpus)
			a.cpusToRemove = a.cpusToRemove.Difference(cpusNotAdd)
			cpus = cpus.Difference(cpusNotAdd)
		}
		a.cpusToAdd = a.cpusToAdd.Union(cpus)
	}
}

func (a *preemptibleAlloc) Subtract(cpus cpuset.CPUSet, numaResources map[int]corev1.ResourceList) {
	if len(numaResources) > 0 {
		a.numaResources = subtractAllocated(a.numaResources, numaResources, false)
	}

	if !cpus.IsEmpty() {
		if !a.cpusToAdd.IsEmpty() {
			cpusNotRemove := a.cpusToAdd.Intersection(cpus)
			a.cpusToAdd = a.cpusToAdd.Difference(cpusNotRemove)
			cpus = cpus.Difference(cpusNotRemove)
		}
		a.cpusToRemove = a.cpusToRemove.Union(cpus)
	}
}

type preemptibleNodeState struct {
	// always use a clone so that we don't need to lock the whole state
	nodeAlloc         *preemptibleAlloc               // unallocated and unreserved resources of the node
	reservationsAlloc map[types.UID]*preemptibleAlloc // reservation ID to unallocated resources of the reservation
}

func (ns *preemptibleNodeState) Clone() *preemptibleNodeState {
	stateCopy := &preemptibleNodeState{}
	if ns.nodeAlloc != nil {
		stateCopy.nodeAlloc = ns.nodeAlloc.Clone()
	}
	if ns.reservationsAlloc != nil {
		stateCopy.reservationsAlloc = map[types.UID]*preemptibleAlloc{}
		for uid, alloc := range ns.reservationsAlloc {
			stateCopy.reservationsAlloc[uid] = alloc.Clone()
		}
	}
	return stateCopy
}

func (p *Plugin) AddPod(_ context.Context, cycleState *framework.CycleState, preemptor *corev1.Pod, podInfoToAdd *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}
	if podInfoToAdd == nil || podInfoToAdd.Pod == nil {
		return nil
	}

	pod := podInfoToAdd.Pod
	nodeName := nodeInfo.Node().Name
	podAllocatedCPUs, podAllocatedNUMAResources := p.getPodAllocated(pod, nodeName)
	if podAllocatedCPUs.IsEmpty() && len(podAllocatedNUMAResources) == 0 {
		return nil
	}

	state.schedulingStateData.lock.Lock()
	if state.preemptibleState == nil {
		state.preemptibleState = map[string]*preemptibleNodeState{}
	}
	nodeState := state.preemptibleState[nodeName]
	if nodeState == nil {
		nodeState = &preemptibleNodeState{
			nodeAlloc:         nil,
			reservationsAlloc: map[types.UID]*preemptibleAlloc{},
		}
		state.preemptibleState[nodeName] = nodeState
	}
	state.schedulingStateData.lock.Unlock()

	rInfo := p.getPodNominatedReservationInfo(pod, nodeName)
	if rInfo == nil || p.notNUMAAwareReservation(rInfo) { // preempt node unallocated resources
		if nodeState.nodeAlloc == nil {
			nodeState.nodeAlloc = newPreemptibleAlloc()
		}
		nodeState.nodeAlloc.Subtract(podAllocatedCPUs, podAllocatedNUMAResources)
	} else { // preempt reservation resources
		rAlloc := nodeState.reservationsAlloc[rInfo.UID()]
		if rAlloc == nil {
			rAlloc = newPreemptibleAlloc()
			nodeState.reservationsAlloc[rInfo.UID()] = rAlloc
		}
		rAlloc.Subtract(podAllocatedCPUs, podAllocatedNUMAResources)
	}

	cycleState.Write(stateKey, state)
	klog.V(6).InfoS("NodeNUMAResource add pod to preemption", "node", nodeName,
		"preemptor", klog.KObj(preemptor), "podToAdd", klog.KObj(pod),
		"podAllocatedCPUs", podAllocatedCPUs, "podAllocatedNUMAResources", podAllocatedNUMAResources,
		"reservationInfo", rInfo)
	return nil
}

func (p *Plugin) RemovePod(_ context.Context, cycleState *framework.CycleState, preemptor *corev1.Pod, podInfoToRemove *framework.PodInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}
	if podInfoToRemove == nil || podInfoToRemove.Pod == nil {
		return nil
	}

	pod := podInfoToRemove.Pod
	nodeName := nodeInfo.Node().Name
	podAllocatedCPUs, podAllocatedNUMAResources := p.getPodAllocated(pod, nodeName)
	if podAllocatedCPUs.IsEmpty() && len(podAllocatedNUMAResources) == 0 {
		return nil
	}

	state.schedulingStateData.lock.Lock()
	if state.preemptibleState == nil {
		state.preemptibleState = map[string]*preemptibleNodeState{}
	}
	nodeState := state.preemptibleState[nodeName]
	if nodeState == nil {
		nodeState = &preemptibleNodeState{
			nodeAlloc:         nil,
			reservationsAlloc: map[types.UID]*preemptibleAlloc{},
		}
		state.preemptibleState[nodeName] = nodeState
	}
	state.schedulingStateData.lock.Unlock()

	rInfo := p.getPodNominatedReservationInfo(pod, nodeName)
	if rInfo == nil || p.notNUMAAwareReservation(rInfo) { // preempt node unallocated resources
		if nodeState.nodeAlloc == nil {
			nodeState.nodeAlloc = newPreemptibleAlloc()
		}
		nodeState.nodeAlloc.Accumulate(podAllocatedCPUs, podAllocatedNUMAResources)
	} else { // preempt reservation resources
		rAlloc := nodeState.reservationsAlloc[rInfo.UID()]
		if rAlloc == nil {
			rAlloc = newPreemptibleAlloc()
			nodeState.reservationsAlloc[rInfo.UID()] = rAlloc
		}
		rAlloc.Accumulate(podAllocatedCPUs, podAllocatedNUMAResources)
	}

	cycleState.Write(stateKey, state)
	klog.V(6).InfoS("NodeNUMAResource remove pod to preemption", "node", nodeName,
		"preemptor", klog.KObj(preemptor), "podToRemove", klog.KObj(pod),
		"podAllocatedCPUs", podAllocatedCPUs, "podAllocatedNUMAResources", podAllocatedNUMAResources,
		"reservationInfo", rInfo)
	return nil
}

func (p *Plugin) notNUMAAwareReservation(rInfo *frameworkext.ReservationInfo) bool {
	podAllocatedCPUs, podAllocatedNUMAResources := p.getPodAllocated(rInfo.Pod, rInfo.GetNodeName())
	if podAllocatedCPUs.IsEmpty() && len(podAllocatedNUMAResources) == 0 {
		return true
	}
	return false
}

func (p *Plugin) getPodAllocated(pod *corev1.Pod, nodeName string) (cpus cpuset.CPUSet, numaResources map[int]corev1.ResourceList) {
	podAllocatedCPUs, ok := p.resourceManager.GetAllocatedCPUSet(nodeName, pod.UID)
	if ok && !podAllocatedCPUs.IsEmpty() {
		cpus = podAllocatedCPUs
	}
	podAllocatedNUMAResources, ok := p.resourceManager.GetAllocatedNUMAResource(nodeName, pod.UID)
	if ok && len(podAllocatedNUMAResources) > 0 {
		numaResources = podAllocatedNUMAResources
	}
	return
}
