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

package reservation

import (
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

var _ framework.StateData = &stateData{}

type stateData struct {
	// scheduling cycle data
	schedulingStateData

	// all cycle data
	// NOTE: The part of data is kept during both the scheduling cycle and the binding cycle. In case too many
	// binding goroutines reference the data causing OOM issues, the memory overhead of this part should be
	// small and its space complexity should be no more than O(1) for pods, reservations and nodes.
	assumed *frameworkext.ReservationInfo
}

// schedulingStateData is the data only kept in the scheduling cycle. It could be cleaned up
// before entering the binding cycle to reduce memory cost.
type schedulingStateData struct {
	preemptLock      sync.RWMutex
	preemptible      map[string]corev1.ResourceList
	preemptibleInRRs map[string]map[types.UID]corev1.ResourceList

	hasAffinity          bool
	reservationName      string
	podRequests          corev1.ResourceList
	podRequestsResources *framework.Resource

	nodeReservationStates    map[string]*nodeReservationState
	nodeReservationDiagnosis map[string]*nodeDiagnosisState
	preferredNode            string
}

type nodeReservationState struct {
	nodeName string
	// matchedOrIgnored represents all matched or ignored reservations for the scheduling pod.
	matchedOrIgnored []*frameworkext.ReservationInfo

	// podRequested represents all Pods(including matched reservation) requested resources
	// but excluding the already allocated from unmatched reservations
	podRequested *framework.Resource
	// rAllocated represents the allocated resources of matched reservations
	rAllocated *framework.Resource

	unmatched []*frameworkext.ReservationInfo

	preRestored   bool // restore in PreFilter or Filter
	finalRestored bool // restore in Filter
}

type nodeDiagnosisState struct {
	nodeName string

	ignored int // resource reservations are ignored due to the pod label

	ownerMatched             int // owner matched
	nameMatched              int // owner matched and reservation name matched
	nameUnmatched            int // owner matched but BeforePreFilter unmatched due to reservation name
	isUnschedulableUnmatched int // owner matched but BeforePreFilter unmatched due to unschedulable
	affinityUnmatched        int // owner matched but BeforePreFilter unmatched due to affinity
	notExactMatched          int // owner matched but BeforePreFilter unmatched due to not exact match
	taintsUnmatched          int // owner matched but BeforePreFilter unmatched due to reservation taints
	taintsUnmatchedReasons   map[string]int
}

func (s *stateData) Clone() framework.StateData {
	ns := &stateData{
		schedulingStateData: schedulingStateData{
			hasAffinity:              s.hasAffinity,
			reservationName:          s.reservationName,
			podRequests:              s.podRequests,
			podRequestsResources:     s.podRequestsResources,
			nodeReservationStates:    s.nodeReservationStates,
			nodeReservationDiagnosis: s.nodeReservationDiagnosis,
			preferredNode:            s.preferredNode,
		},
		assumed: s.assumed,
	}

	s.preemptLock.RLock()
	defer s.preemptLock.RUnlock()

	preemptible := map[string]corev1.ResourceList{}
	for nodeName, returned := range s.preemptible {
		preemptible[nodeName] = returned // no need to copy when the value is immutable
	}
	ns.preemptible = preemptible

	preemptibleInRRs := map[string]map[types.UID]corev1.ResourceList{}
	for nodeName, rrs := range s.preemptibleInRRs {
		rrInNode := preemptibleInRRs[nodeName]
		if rrInNode == nil {
			rrInNode = map[types.UID]corev1.ResourceList{}
			preemptibleInRRs[nodeName] = rrInNode
		}
		for reservationUID, returned := range rrs {
			rrInNode[reservationUID] = returned // no need to copy when the value is immutable
		}
	}
	ns.preemptibleInRRs = preemptibleInRRs

	return ns
}

// CleanSchedulingData clears the scheduling cycle data in the stateData to reduce memory cost before entering
// the binding cycle.
func (s *stateData) CleanSchedulingData() {
	s.schedulingStateData = schedulingStateData{}
}

func getStateData(cycleState *framework.CycleState) *stateData {
	v, err := cycleState.Read(stateKey)
	if err != nil {
		return &stateData{}
	}
	s, ok := v.(*stateData)
	if !ok || s == nil {
		return &stateData{}
	}
	return s
}
