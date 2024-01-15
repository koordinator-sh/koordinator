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
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var _ frameworkext.ReservationNominator = &FakeNominator{}

type FakeNominator struct {
	// nominatedPodToNode is map keyed by a Pod UID to the node name where it is nominated.
	nominatedPodToNode map[types.UID]map[string]types.UID
	reservations       map[types.UID]*frameworkext.ReservationInfo
	lock               sync.RWMutex
}

func NewFakeReservationNominator() *FakeNominator {
	return &FakeNominator{
		nominatedPodToNode: map[types.UID]map[string]types.UID{},
		reservations:       map[types.UID]*frameworkext.ReservationInfo{},
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
