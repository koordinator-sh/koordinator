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
	"context"
	"fmt"
	"math"
	"strconv"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	resourceapi "k8s.io/component-helpers/resource"
	"k8s.io/kube-scheduler/framework"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"
	pluginhelper "k8s.io/kubernetes/pkg/scheduler/framework/plugins/helper"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	mostPreferredScore = 1000
)

func (pl *Plugin) PreScore(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeInfos []fwktype.NodeInfo) *fwktype.Status {
	nodes := make([]*corev1.Node, 0, len(nodeInfos))
	for _, ni := range nodeInfos {
		if n := ni.Node(); n != nil {
			nodes = append(nodes, n)
		}
	}
	if reservationutil.IsReservePod(pod) {
		if reservationutil.IsReservePodPreAllocation(pod) {
			return pl.preScoreForPreAllocation(ctx, cycleState, pod, nodes)
		}

		return fwktype.NewStatus(fwktype.Skip)
	}

	return pl.preScoreForNormalPod(ctx, cycleState, pod, nodes)
}

func (pl *Plugin) preScoreForNormalPod(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodes []*corev1.Node) *fwktype.Status {
	// if the pod is reservation-ignored, it does not want a nominated reservation
	if apiext.IsReservationIgnored(pod) {
		return fwktype.NewStatus(fwktype.Skip)
	}

	state := getStateData(cycleState)
	if len(state.nodeReservationStates) == 0 {
		return fwktype.NewStatus(fwktype.Skip)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	nominatedReservations := make([]*frameworkext.ReservationInfo, len(state.nodeReservationStates))
	var nominatedNodeIndex int32

	nodeOrders := make([]int64, len(nodes))
	errCh := parallelize.NewErrorChannel()
	pl.handle.Parallelizer().Until(ctx, len(nodes), func(piece int) {
		node := nodes[piece]
		var reservationInfos []*frameworkext.ReservationInfo
		if nodeRState := state.nodeReservationStates[node.Name]; nodeRState != nil {
			reservationInfos = nodeRState.matchedOrIgnored
		}
		if len(reservationInfos) == 0 {
			return
		}
		_, order := findMostPreferredReservationByOrder(reservationInfos)
		nodeOrders[piece] = order

		nominatedReservationInfo, status := pl.handle.GetReservationNominator().NominateReservation(ctx, cycleState, pod, node.Name)
		if !status.IsSuccess() {
			errCh.SendErrorWithCancel(status.AsError(), cancel)
			return
		}
		if nominatedReservationInfo != nil {
			index := atomic.AddInt32(&nominatedNodeIndex, 1)
			nominatedReservations[index-1] = nominatedReservationInfo
		}
	}, "ReservationPreScore")
	if err := errCh.ReceiveError(); err != nil {
		return fwktype.AsStatus(err)
	}

	nominatedReservations = nominatedReservations[:nominatedNodeIndex]
	for _, v := range nominatedReservations {
		pl.handle.GetReservationNominator().AddNominatedReservation(pod, v.GetNodeName(), v)
	}

	var selectOrder int64 = math.MaxInt64
	var nodeIndex int
	for i, order := range nodeOrders {
		if order != 0 && selectOrder > order {
			selectOrder = order
			nodeIndex = i
		}
	}
	if selectOrder != math.MaxInt64 {
		state.preferredNode = nodes[nodeIndex].Name
	}
	return nil
}

func (pl *Plugin) preScoreForPreAllocation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodes []*corev1.Node) *fwktype.Status {
	state := getStateData(cycleState)
	if len(state.nodeReservationStates) == 0 {
		return fwktype.NewStatus(fwktype.Skip)
	}
	if state.rInfo == nil {
		return fwktype.AsStatus(fmt.Errorf("missing PreAllocation Reservation"))
	}

	if state.rInfo.IsMultiplePAPodsEnabled() {
		// For multiple pre-allocatable pods mode, skip the nomination in PreScore phase.
		// The selectedPreAllocatablePods are already determined during Filter phase,
		// and Score phase will use them directly without needing a single nominated pod.
		return fwktype.NewStatus(fwktype.Skip)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	nominatedPreAllocatable := make([]*corev1.Pod, len(state.nodeReservationStates))
	var nominatedNodeIndex int32

	errCh := parallelize.NewErrorChannel()
	pl.handle.Parallelizer().Until(ctx, len(nodes), func(piece int) {
		node := nodes[piece]
		var preAllocatable []*corev1.Pod
		if nodeRState := state.nodeReservationStates[node.Name]; nodeRState != nil {
			preAllocatable = nodeRState.selectedPreAllocatablePods
		}
		if len(preAllocatable) == 0 {
			return
		}

		nominatedPod, status := pl.handle.GetReservationNominator().NominatePreAllocation(ctx, cycleState, state.rInfo, node.Name)
		if !status.IsSuccess() {
			errCh.SendErrorWithCancel(status.AsError(), cancel)
			return
		}
		if nominatedPod != nil {
			index := atomic.AddInt32(&nominatedNodeIndex, 1)
			nominatedPreAllocatable[index-1] = nominatedPod
		}
	}, "ReservationPreScore")
	if err := errCh.ReceiveError(); err != nil {
		return fwktype.AsStatus(err)
	}

	nominatedPreAllocatable = nominatedPreAllocatable[:nominatedNodeIndex]
	for _, v := range nominatedPreAllocatable {
		pl.handle.GetReservationNominator().AddNominatedPreAllocation(state.rInfo, v.Spec.NodeName, v)
	}

	return nil
}

func (pl *Plugin) Score(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeInfo fwktype.NodeInfo) (int64, *fwktype.Status) {
	nodeName := nodeInfo.Node().Name
	if reservationutil.IsReservePod(pod) {
		if reservationutil.IsReservePodPreAllocation(pod) {
			return pl.scoreForPreAllocation(ctx, cycleState, pod, nodeName)
		}

		return fwktype.MinNodeScore, nil
	}

	return pl.scoreForNormalPod(ctx, cycleState, pod, nodeName)
}

func (pl *Plugin) scoreForNormalPod(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) (int64, *fwktype.Status) {
	state := getStateData(cycleState)

	if state.preferredNode == nodeName {
		return mostPreferredScore, nil
	}

	reservationInfo := pl.handle.GetReservationNominator().GetNominatedReservation(pod, nodeName)
	if reservationInfo == nil {
		return fwktype.MinNodeScore, nil
	}
	for _, v := range state.nodeReservationStates[nodeName].matchedOrIgnored {
		if v.UID() == reservationInfo.UID() {
			reservationInfo = v
			break
		}
	}
	if reservationInfo == nil {
		return 0, fwktype.AsStatus(fmt.Errorf("impossible, there is no relevant Reservation information"))
	}

	return pl.ScoreReservation(ctx, cycleState, pod, reservationInfo, nodeName)
}

func (pl *Plugin) scoreForPreAllocation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) (int64, *fwktype.Status) {
	state := getStateData(cycleState)

	if state.preferredNode == nodeName {
		return mostPreferredScore, nil
	}

	if state.rInfo.IsMultiplePAPodsEnabled() {
		// If there is no pre-allocatable pod nominated on this node, give it the highest priority.
		nodeRState := state.nodeReservationStates[nodeName]
		if nodeRState == nil || len(nodeRState.selectedPreAllocatablePods) == 0 {
			return framework.MaxNodeScore, nil
		}
		// The more pre-allocatable pods are selected on this node, the lower priority it will get.
		return framework.MaxNodeScore / int64(1+len(nodeRState.selectedPreAllocatablePods)), nil
	}

	preAllocatable := pl.handle.GetReservationNominator().GetNominatedPreAllocation(state.rInfo, nodeName)
	if preAllocatable == nil {
		return fwktype.MinNodeScore, nil
	}
	for _, v := range state.nodeReservationStates[nodeName].selectedPreAllocatablePods {
		if v.GetUID() == preAllocatable.GetUID() {
			preAllocatable = v
			break
		}
	}
	if preAllocatable == nil {
		return 0, fwktype.AsStatus(fmt.Errorf("impossible, there is no relevant pre-allocatable"))
	}

	return pl.ScoreReservation(ctx, cycleState, preAllocatable, state.rInfo, nodeName)
}

func (pl *Plugin) ScoreExtensions() fwktype.ScoreExtensions {
	return pl
}

func (pl *Plugin) NormalizeScore(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, scores fwktype.NodeScoreList) *fwktype.Status {
	if reservationutil.IsReservePod(pod) {
		return nil
	}
	return pluginhelper.DefaultNormalizeScore(fwktype.MaxNodeScore, false, scores)
}

func (pl *Plugin) ScoreReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, rInfo *frameworkext.ReservationInfo, nodeName string) (int64, *fwktype.Status) {
	state := getStateData(cycleState)
	preemptibleInRR := state.preemptibleInRRs[nodeName][rInfo.UID()]
	allocated := rInfo.Allocated
	if len(preemptibleInRR) > 0 {
		allocated = quotav1.SubtractWithNonNegativeResult(allocated, preemptibleInRR)
		allocated = quotav1.Mask(allocated, rInfo.ResourceNames)
	}

	requested := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
	requested = quotav1.Add(requested, allocated)
	resources := quotav1.RemoveZeros(rInfo.Allocatable)

	w := int64(len(resources))
	if w <= 0 {
		return 0, nil
	}

	// Here we use MostAllocated (simply set all weights as 1.0)
	var s int64
	for resource, capacity := range resources {
		req := requested[resource]
		if req.Cmp(capacity) <= 0 {
			s += fwktype.MaxNodeScore * req.MilliValue() / capacity.MilliValue()
		}
	}
	return s / w, nil
}

func (pl *Plugin) ReservationScoreExtensions() frameworkext.ReservationScoreExtensions {
	return nil
}

func findMostPreferredReservationByOrder(rOnNode []*frameworkext.ReservationInfo) (*frameworkext.ReservationInfo, int64) {
	var selectOrder int64 = math.MaxInt64
	var highOrder *frameworkext.ReservationInfo
	for _, rInfo := range rOnNode {
		s := rInfo.GetObject().GetLabels()[apiext.LabelReservationOrder]
		if s == "" {
			continue
		}
		order, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			continue
		}
		// The smaller the order value is, the reservation will be selected first
		if order != 0 && selectOrder > order {
			selectOrder = order
			highOrder = rInfo
		}
	}
	return highOrder, selectOrder
}
