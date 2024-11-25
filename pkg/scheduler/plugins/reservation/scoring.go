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
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/parallelize"
	pluginhelper "k8s.io/kubernetes/pkg/scheduler/framework/plugins/helper"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	mostPreferredScore = 1000
)

func (pl *Plugin) PreScore(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodes []*corev1.Node) *framework.Status {
	if reservationutil.IsReservePod(pod) {
		return framework.NewStatus(framework.Skip)
	}

	// if the pod is reservation-ignored, it does not want a nominated reservation
	if apiext.IsReservationIgnored(pod) {
		return framework.NewStatus(framework.Skip)
	}

	state := getStateData(cycleState)
	if len(state.nodeReservationStates) == 0 {
		return framework.NewStatus(framework.Skip)
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
		return framework.AsStatus(err)
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

func (pl *Plugin) Score(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	if reservationutil.IsReservePod(pod) {
		return framework.MinNodeScore, nil
	}

	state := getStateData(cycleState)

	if state.preferredNode == nodeName {
		return mostPreferredScore, nil
	}

	reservationInfo := pl.handle.GetReservationNominator().GetNominatedReservation(pod, nodeName)
	if reservationInfo == nil {
		return framework.MinNodeScore, nil
	}

	return pl.ScoreReservation(ctx, cycleState, pod, reservationInfo, nodeName)
}

func (pl *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return pl
}

func (pl *Plugin) NormalizeScore(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, scores framework.NodeScoreList) *framework.Status {
	if reservationutil.IsReservePod(pod) {
		return nil
	}
	return pluginhelper.DefaultNormalizeScore(framework.MaxNodeScore, false, scores)
}

func (pl *Plugin) ScoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *frameworkext.ReservationInfo, nodeName string) (int64, *framework.Status) {
	state := getStateData(cycleState)
	reservationInfos := state.nodeReservationStates[nodeName].matchedOrIgnored

	var rInfo *frameworkext.ReservationInfo
	for _, v := range reservationInfos {
		if v.UID() == reservationInfo.UID() {
			rInfo = v
			break
		}
	}
	if rInfo == nil {
		return 0, framework.AsStatus(fmt.Errorf("impossible, there is no relevant Reservation information"))
	}

	preemptibleInRR := state.preemptibleInRRs[nodeName][rInfo.UID()]
	allocated := rInfo.Allocated
	if len(preemptibleInRR) > 0 {
		allocated = quotav1.SubtractWithNonNegativeResult(allocated, preemptibleInRR)
		allocated = quotav1.Mask(allocated, rInfo.ResourceNames)
	}

	return scoreReservation(pod, rInfo, allocated), nil
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

func scoreReservation(pod *corev1.Pod, rInfo *frameworkext.ReservationInfo, allocated corev1.ResourceList) int64 {
	requested := resourceapi.PodRequests(pod, resourceapi.PodResourcesOptions{})
	requested = quotav1.Add(requested, allocated)
	resources := quotav1.RemoveZeros(rInfo.Allocatable)

	w := int64(len(resources))
	if w <= 0 {
		return 0
	}

	// Here we use MostAllocated (simply set all weights as 1.0)
	var s int64
	for resource, capacity := range resources {
		req := requested[resource]
		if req.Cmp(capacity) <= 0 {
			s += framework.MaxNodeScore * req.MilliValue() / capacity.MilliValue()
		}
	}
	return s / w
}
