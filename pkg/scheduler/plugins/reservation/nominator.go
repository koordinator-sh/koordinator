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
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func (pl *Plugin) NominateReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (*frameworkext.ReservationInfo, *framework.Status) {
	if reservationutil.IsReservePod(pod) {
		return nil, nil
	}

	state := getStateData(cycleState)
	reservationInfos := state.nodeReservationStates[nodeName].matched
	if len(reservationInfos) == 0 {
		return nil, nil
	}

	highestScorer, _ := findMostPreferredReservationByOrder(reservationInfos)
	if highestScorer != nil {
		return highestScorer, nil
	}

	extender, ok := pl.handle.(frameworkext.FrameworkExtender)
	if !ok {
		return nil, framework.AsStatus(fmt.Errorf("not implemented frameworkext.FrameworkExtender"))
	}

	reservations := make([]*frameworkext.ReservationInfo, 0, len(reservationInfos))
	for _, rInfo := range reservationInfos {
		status := extender.RunReservationFilterPlugins(ctx, cycleState, pod, rInfo, nodeName)
		if !status.IsSuccess() {
			continue
		}
		reservations = append(reservations, rInfo)
	}
	if len(reservations) == 0 {
		return nil, nil
	}

	reservationScoreList, err := prioritizeReservations(ctx, extender, cycleState, pod, reservations, nodeName)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	sort.Slice(reservationScoreList, func(i, j int) bool {
		return reservationScoreList[i].Score > reservationScoreList[j].Score
	})

	highestScorer = nil
	for _, v := range reservations {
		if v.UID() == reservationScoreList[0].UID {
			highestScorer = v
			break
		}
	}
	if highestScorer == nil {
		return nil, framework.AsStatus(fmt.Errorf("missing the most suitable reservation %v(%v)",
			klog.KRef(reservationScoreList[0].Namespace, reservationScoreList[0].Name), reservationScoreList[0].UID))
	}
	return highestScorer, nil
}

func prioritizeReservations(
	ctx context.Context,
	fwk frameworkext.FrameworkExtender,
	state *framework.CycleState,
	pod *corev1.Pod,
	reservations []*frameworkext.ReservationInfo,
	nodeName string,
) (frameworkext.ReservationScoreList, error) {
	scoresMap, scoreStatus := fwk.RunReservationScorePlugins(ctx, state, pod, reservations, nodeName)
	if !scoreStatus.IsSuccess() {
		return nil, scoreStatus.AsError()
	}

	if klog.V(5).Enabled() {
		for plugin, reservationScoreList := range scoresMap {
			for _, score := range reservationScoreList {
				klog.InfoS("Plugin scored reservation for pod", "pod", klog.KObj(pod), "plugin", plugin, "reservation", klog.KRef(score.Namespace, score.Name), "score", score.Score)
			}
		}
	}

	// Summarize all scores.
	result := make(frameworkext.ReservationScoreList, 0, len(reservations))
	for i := range reservations {
		rs := frameworkext.ReservationScore{
			Name:      reservations[i].GetName(),
			Namespace: reservations[i].GetNamespace(),
			UID:       reservations[i].UID(),
			Score:     0,
		}
		result = append(result, rs)
		for j := range scoresMap {
			result[i].Score += scoresMap[j][i].Score
		}
	}

	if klog.V(5).Enabled() {
		for i := range result {
			klog.InfoS("Calculated reservation's final score for pod", "pod", klog.KObj(pod), "reservation", klog.KRef(result[i].Namespace, result[i].Name), "score", result[i].Score)
		}
	}
	return result, nil
}
