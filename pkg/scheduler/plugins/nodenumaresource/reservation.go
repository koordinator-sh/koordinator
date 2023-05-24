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
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

const reservationRestoreStateKey = Name + "/reservationRestoreState"

type reservationRestoreStateData struct {
	skip        bool
	nodeToState frameworkext.NodeReservationRestoreStates
}

type nodeReservationRestoreStateData struct {
	reservedCPUs map[types.UID]cpuset.CPUSet
}

func getReservationRestoreState(cycleState *framework.CycleState) *reservationRestoreStateData {
	var state *reservationRestoreStateData
	value, err := cycleState.Read(reservationRestoreStateKey)
	if err == nil {
		state, _ = value.(*reservationRestoreStateData)
	}
	if state == nil {
		state = &reservationRestoreStateData{
			skip: true,
		}
	}
	return state
}

func (s *reservationRestoreStateData) Clone() framework.StateData {
	return s
}

func (s *reservationRestoreStateData) getNodeState(nodeName string) *nodeReservationRestoreStateData {
	val := s.nodeToState[nodeName]
	ns, ok := val.(*nodeReservationRestoreStateData)
	if !ok {
		ns = &nodeReservationRestoreStateData{}
	}
	return ns
}

func (p *Plugin) PreRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	state := &reservationRestoreStateData{
		skip: !AllowUseCPUSet(pod),
	}
	cycleState.Write(reservationRestoreStateKey, state)
	return nil
}

func (p *Plugin) RestoreReservation(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, matched []*frameworkext.ReservationInfo, unmatched []*frameworkext.ReservationInfo, nodeInfo *framework.NodeInfo) (interface{}, *framework.Status) {
	state := getReservationRestoreState(cycleState)
	if state.skip {
		return nil, nil
	}

	nodeName := nodeInfo.Node().Name
	reservedCPUs := map[types.UID]cpuset.CPUSet{}
	for _, rInfo := range matched {
		allocatedCPUs, ok := p.cpuManager.GetAllocatedCPUSet(nodeName, rInfo.Reservation.UID)
		if !ok || allocatedCPUs.IsEmpty() {
			continue
		}

		for _, pod := range rInfo.Pods {
			podCPUs, ok := p.cpuManager.GetAllocatedCPUSet(nodeName, pod.UID)
			if !ok || podCPUs.IsEmpty() {
				continue
			}

			allocatedCPUs = allocatedCPUs.Difference(podCPUs)
		}

		if !allocatedCPUs.IsEmpty() {
			reservedCPUs[rInfo.Reservation.UID] = allocatedCPUs
		} else {
			delete(reservedCPUs, rInfo.Reservation.UID)
		}
	}

	if len(reservedCPUs) == 0 {
		return nil, nil
	}

	return &nodeReservationRestoreStateData{
		reservedCPUs: reservedCPUs,
	}, nil
}

func (p *Plugin) FinalRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeToStates frameworkext.NodeReservationRestoreStates) *framework.Status {
	state := getReservationRestoreState(cycleState)
	if state.skip {
		return nil
	}
	state.nodeToState = nodeToStates
	return nil
}
