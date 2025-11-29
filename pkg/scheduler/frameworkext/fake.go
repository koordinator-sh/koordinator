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

package frameworkext

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// nominatedPodMap is a structure that stores pods nominated to run on nodes.
// It exists because nominatedNodeName of pod objects stored in the structure
// may be different than what scheduler has here. We should be able to find pods
// by their UID and update/delete them.
type nominatedPodMap struct {
	// nominatedPods is a map keyed by a node name and the value is a list of
	// pods which are nominated to run on the node. These are pods which can be in
	// the activeQ or unschedulableQ.
	nominatedPods map[string][]*framework.PodInfo
	// nominatedPodToNode is map keyed by a Pod UID to the node name where it is
	// nominated.
	nominatedPodToNode map[types.UID]string

	sync.RWMutex
}

// NewFakePodNominator creates a nominatedPodMap as a backing of framework.PodNominator.
func NewFakePodNominator() framework.PodNominator {
	return &nominatedPodMap{
		nominatedPods:      make(map[string][]*framework.PodInfo),
		nominatedPodToNode: make(map[types.UID]string),
	}
}

func (npm *nominatedPodMap) add(pi *framework.PodInfo, nodeName string) {
	// always delete the pod if it already exist, to ensure we never store more than
	// one instance of the pod.
	npm.delete(pi.Pod)

	nnn := nodeName
	if len(nnn) == 0 {
		nnn = pi.Pod.Status.NominatedNodeName
		if len(nnn) == 0 {
			return
		}
	}
	npm.nominatedPodToNode[pi.Pod.UID] = nnn
	for _, npi := range npm.nominatedPods[nnn] {
		if npi.Pod.UID == pi.Pod.UID {
			klog.V(4).InfoS("Pod already exists in the nominated map", "pod", klog.KObj(npi.Pod))
			return
		}
	}
	npm.nominatedPods[nnn] = append(npm.nominatedPods[nnn], pi)
}

func (npm *nominatedPodMap) delete(p *corev1.Pod) {
	nnn, ok := npm.nominatedPodToNode[p.UID]
	if !ok {
		return
	}
	for i, np := range npm.nominatedPods[nnn] {
		if np.Pod.UID == p.UID {
			npm.nominatedPods[nnn] = append(npm.nominatedPods[nnn][:i], npm.nominatedPods[nnn][i+1:]...)
			if len(npm.nominatedPods[nnn]) == 0 {
				delete(npm.nominatedPods, nnn)
			}
			break
		}
	}
	delete(npm.nominatedPodToNode, p.UID)
}

// UpdateNominatedPod updates the <oldPod> with <newPod>.
func (npm *nominatedPodMap) UpdateNominatedPod(logr klog.Logger, oldPod *corev1.Pod, newPodInfo *framework.PodInfo) {
	npm.Lock()
	defer npm.Unlock()
	// In some cases, an Update event with no "NominatedNode" present is received right
	// after a node("NominatedNode") is reserved for this pod in memory.
	// In this case, we need to keep reserving the NominatedNode when updating the pod pointer.
	nodeName := ""
	// We won't fall into below `if` block if the Update event represents:
	// (1) NominatedNode info is added
	// (2) NominatedNode info is updated
	// (3) NominatedNode info is removed
	if oldPod.Status.NominatedNodeName == "" && newPodInfo.Pod.Status.NominatedNodeName == "" {
		if nnn, ok := npm.nominatedPodToNode[oldPod.UID]; ok {
			// This is the only case we should continue reserving the NominatedNode
			nodeName = nnn
		}
	}
	// We update irrespective of the nominatedNodeName changed or not, to ensure
	// that pod pointer is updated.
	npm.delete(oldPod)
	npm.add(newPodInfo, nodeName)
}

// DeleteNominatedPodIfExists deletes <pod> from nominatedPods.
func (npm *nominatedPodMap) DeleteNominatedPodIfExists(pod *corev1.Pod) {
	npm.Lock()
	npm.delete(pod)
	npm.Unlock()
}

// AddNominatedPod adds a pod to the nominated pods of the given node.
// This is called during the preemption process after a node is nominated to run
// the pod. We update the structure before sending a request to update the pod
// object to avoid races with the following scheduling cycles.
func (npm *nominatedPodMap) AddNominatedPod(logger klog.Logger, pi *framework.PodInfo, nominatingInfo *framework.NominatingInfo) {
	npm.Lock()
	npm.add(pi, nominatingInfo.NominatedNodeName)
	npm.Unlock()
}

// NominatedPodsForNode returns pods that are nominated to run on the given node,
// but they are waiting for other pods to be removed from the node.
func (npm *nominatedPodMap) NominatedPodsForNode(nodeName string) []*framework.PodInfo {
	npm.RLock()
	defer npm.RUnlock()
	// TODO: we may need to return a copy of []*Pods to avoid modification
	// on the caller side.
	return npm.nominatedPods[nodeName]
}

var _ ReservationNominator = &FakeNominator{}

type FakeNominator struct {
	lock sync.RWMutex
	// nominatedPodToNode is map keyed by a Pod UID to the node name where it is nominated.
	nominatedPodToNode map[types.UID]map[string]types.UID
	reservations       map[types.UID]*ReservationInfo
	preAllocatable     map[types.UID]map[string]*corev1.Pod
	// nominatedReservePod is map keyed by nodeName, value is the nominated reservations
	nominatedReservePod       map[string][]*framework.PodInfo
	nominatedReservePodToNode map[types.UID]string
}

func NewFakeReservationNominator() *FakeNominator {
	return &FakeNominator{
		nominatedPodToNode:        map[types.UID]map[string]types.UID{},
		nominatedReservePod:       map[string][]*framework.PodInfo{},
		nominatedReservePodToNode: map[types.UID]string{},
		reservations:              map[types.UID]*ReservationInfo{},
		preAllocatable:            map[types.UID]map[string]*corev1.Pod{},
	}
}

func (nm *FakeNominator) Name() string { return "FakeNominator" }

func (nm *FakeNominator) AddNominatedReservation(pod *corev1.Pod, nodeName string, rInfo *ReservationInfo) {
	if rInfo == nil {
		return
	}
	nm.lock.Lock()
	defer nm.lock.Unlock()

	nodeToReservation := nm.nominatedPodToNode[pod.UID]
	if nodeToReservation == nil {
		nodeToReservation = map[string]types.UID{}
		nm.nominatedPodToNode[pod.UID] = nodeToReservation
	}
	nodeToReservation[nodeName] = rInfo.UID()
	nm.reservations[rInfo.UID()] = rInfo
}

func (nm *FakeNominator) RemoveNominatedReservations(pod *corev1.Pod) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	nodeToReservation := nm.nominatedPodToNode[pod.UID]
	delete(nm.nominatedPodToNode, pod.UID)
	for _, reservationUID := range nodeToReservation {
		delete(nm.reservations, reservationUID)
	}
}

func (nm *FakeNominator) NominatedReservePodForNode(nodeName string) []*framework.PodInfo {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	pods := make([]*framework.PodInfo, len(nm.nominatedReservePod[nodeName]))
	for i := 0; i < len(pods); i++ {
		pods[i] = nm.nominatedReservePod[nodeName][i].DeepCopy()
	}
	return pods
}

func (nm *FakeNominator) GetNominatedReservation(pod *corev1.Pod, nodeName string) *ReservationInfo {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	return nm.reservations[nm.nominatedPodToNode[pod.UID][nodeName]]
}

func (nm *FakeNominator) NominateReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (*ReservationInfo, *framework.Status) {
	if reservationutil.IsReservePod(pod) {
		return nil, nil
	}

	rInfo := nm.GetNominatedReservation(pod, nodeName)
	return rInfo, nil
}

func (nm *FakeNominator) AddNominatedReservePod(pod *corev1.Pod, nodeName string) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	// Always delete the reservation if it already exists, to ensure we never store more than
	// one instance of the reservation.
	nm.deleteReservePod(pod)

	nm.nominatedReservePodToNode[pod.UID] = nodeName
	for _, npi := range nm.nominatedReservePod[nodeName] {
		if npi.Pod.UID == pod.UID {
			klog.V(4).InfoS("reservation already exists in the nominator", "pod", klog.KObj(npi.Pod))
			return
		}
	}
	podInfo, _ := framework.NewPodInfo(pod)
	nm.nominatedReservePod[nodeName] = append(nm.nominatedReservePod[nodeName], podInfo)
}

func (nm *FakeNominator) DeleteNominatedReservePod(pod *corev1.Pod) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	nm.deleteReservePod(pod)
	nm.deletePreAllocation(pod)
}

func (nm *FakeNominator) deleteReservePod(pod *corev1.Pod) {
	nnn, ok := nm.nominatedReservePodToNode[pod.UID]
	if !ok {
		return
	}
	for i, np := range nm.nominatedReservePod[nnn] {
		if np.Pod.UID == pod.UID {
			nm.nominatedReservePod[nnn] = append(nm.nominatedReservePod[nnn][:i], nm.nominatedReservePod[nnn][i+1:]...)
			if len(nm.nominatedReservePod[nnn]) == 0 {
				delete(nm.nominatedReservePod, nnn)
			}
			break
		}
	}
	delete(nm.nominatedReservePodToNode, pod.UID)
}

func (nm *FakeNominator) DeleteNominatedReservePodOrReservation(pod *corev1.Pod) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	nodeToReservation := nm.nominatedPodToNode[pod.UID]
	delete(nm.nominatedPodToNode, pod.UID)
	for _, reservationUID := range nodeToReservation {
		delete(nm.reservations, reservationUID)
	}

	nm.deleteReservePod(pod)
}

func (nm *FakeNominator) NominatePreAllocation(ctx context.Context, cycleState *framework.CycleState, rInfo *ReservationInfo, nodeName string) (*corev1.Pod, *framework.Status) {
	if !rInfo.IsPreAllocation() {
		return nil, nil
	}
	return nm.GetNominatedPreAllocation(rInfo, nodeName), nil
}

func (nm *FakeNominator) AddNominatedPreAllocation(rInfo *ReservationInfo, nodeName string, pod *corev1.Pod) {
	if !rInfo.IsPreAllocation() {
		return
	}
	nm.lock.Lock()
	defer nm.lock.Unlock()
	nodeToPreAllocatable := nm.preAllocatable[rInfo.UID()]
	if nodeToPreAllocatable == nil {
		nodeToPreAllocatable = map[string]*corev1.Pod{}
		nm.preAllocatable[rInfo.UID()] = nodeToPreAllocatable
	}
	nodeToPreAllocatable[nodeName] = pod
}

func (nm *FakeNominator) GetNominatedPreAllocation(rInfo *ReservationInfo, nodeName string) *corev1.Pod {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	nodeToPreAllocatable := nm.preAllocatable[rInfo.UID()]
	if nodeToPreAllocatable == nil {
		return nil
	}
	return nodeToPreAllocatable[nodeName]
}

func (nm *FakeNominator) deletePreAllocation(pod *corev1.Pod) {
	delete(nm.preAllocatable, pod.UID)
}

// GetNominatedNodeForReservePod returns the node name that the reserve pod is nominated to.
// This is only for testing purposes.
func (nm *FakeNominator) GetNominatedNodeForReservePod(pod *corev1.Pod) string {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	return nm.nominatedReservePodToNode[pod.UID]
}
