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

package testing

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var _ frameworkext.ReservationNominator = &FakeNominator{}

type FakeNominator struct {
	lock sync.RWMutex
	// nominatedPodToNode is map keyed by a Pod UID to the node name where it is nominated.
	nominatedPodToNode map[types.UID]map[string]types.UID
	reservations       map[types.UID]*frameworkext.ReservationInfo
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
		reservations:              map[types.UID]*frameworkext.ReservationInfo{},
		preAllocatable:            map[types.UID]map[string]*corev1.Pod{},
	}
}

func (nm *FakeNominator) Name() string { return "FakeNominator" }

func (nm *FakeNominator) AddNominatedReservation(pod *corev1.Pod, nodeName string, rInfo *frameworkext.ReservationInfo) {
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

func (nm *FakeNominator) GetNominatedReservation(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	return nm.reservations[nm.nominatedPodToNode[pod.UID][nodeName]]
}

func (nm *FakeNominator) NominateReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (*frameworkext.ReservationInfo, *framework.Status) {
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

func (nm *FakeNominator) NominatePreAllocation(ctx context.Context, cycleState *framework.CycleState, rInfo *frameworkext.ReservationInfo, nodeName string) (*corev1.Pod, *framework.Status) {
	if !rInfo.IsPreAllocation() {
		return nil, nil
	}
	return nm.GetNominatedPreAllocation(rInfo, nodeName), nil
}

func (nm *FakeNominator) AddNominatedPreAllocation(rInfo *frameworkext.ReservationInfo, nodeName string, pod *corev1.Pod) {
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

func (nm *FakeNominator) GetNominatedPreAllocation(rInfo *frameworkext.ReservationInfo, nodeName string) *corev1.Pod {
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
