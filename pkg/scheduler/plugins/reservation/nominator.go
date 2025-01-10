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
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type nominator struct {
	podLister corelisters.PodLister
	rLister   listerschedulingv1alpha1.ReservationLister
	// nominatedPodToNode is map keyed by a Pod UID to the node name where it is nominated.
	nominatedPodToNode map[types.UID]map[string]types.UID
	// nominatedReservePod is map keyed by nodeName, value is the nominated reservation's PodInfo
	nominatedReservePod       map[string][]*framework.PodInfo
	nominatedReservePodToNode map[types.UID]string
	lock                      sync.RWMutex
}

func newNominator(podLister corelisters.PodLister, rLister listerschedulingv1alpha1.ReservationLister) *nominator {
	return &nominator{
		podLister:                 podLister,
		rLister:                   rLister,
		nominatedPodToNode:        map[types.UID]map[string]types.UID{},
		nominatedReservePod:       map[string][]*framework.PodInfo{},
		nominatedReservePodToNode: map[types.UID]string{},
	}
}

func (nm *nominator) AddNominatedReservation(pod *corev1.Pod, nodeName string, rInfo *frameworkext.ReservationInfo) {
	if rInfo == nil {
		return
	}
	nm.lock.Lock()
	defer nm.lock.Unlock()

	// nominate it only if the pod is unscheduled and reservation is active
	rName := rInfo.GetName()
	if nm.podLister != nil {
		p, err := nm.podLister.Pods(pod.Namespace).Get(pod.Name)
		if err != nil {
			klog.V(4).InfoS("Pod doesn't exist in podLister, aborted nominating it to the reservation",
				"node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
		if p.Spec.NodeName != "" {
			klog.V(4).InfoS("Pod is already scheduled to a node, aborted nominating it to the reservation",
				"current node", p.Spec.NodeName, "node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
	}
	if nm.rLister != nil {
		r, err := nm.rLister.Get(rName)
		if err != nil {
			klog.V(4).InfoS("reservation doesn't exist in rLister, aborted nominating pod to it",
				"node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
		if !reservationutil.IsReservationActive(r) { // cannot nominate to an inactive reservation
			klog.V(4).InfoS("reservation is inactive, aborted nominating pod to it",
				"node", nodeName, "pod", klog.KObj(pod), "reservation", rName)
			return
		}
	}

	nodeToReservation := nm.nominatedPodToNode[pod.UID]
	if nodeToReservation == nil {
		nodeToReservation = map[string]types.UID{}
		nm.nominatedPodToNode[pod.UID] = nodeToReservation
	}
	nodeToReservation[nodeName] = rInfo.UID()
}

func (nm *nominator) AddNominatedReservePod(pi *framework.PodInfo, nodeName string) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	// Always delete the reservation if it already exists, to ensure we never store more than
	// one instance of the reservation.
	nm.deleteReservePod(pi)

	rName := reservationutil.GetReservationNameFromReservePod(pi.Pod)
	if len(rName) <= 0 { // not a reserve pod
		klog.V(4).InfoS("reservation nominator aborts nominating pod which is not a reserve pod",
			"pod", klog.KObj(pi.Pod), "reservation", rName, "nominatedNodeName", nodeName)
		return
	}
	if nodeName == "" {
		klog.V(4).InfoS("reservation nominated node is removed",
			"pod", klog.KObj(pi.Pod), "reservation", rName, "nominatedNodeName", nodeName)
		return
	}

	if nm.rLister != nil {
		// If a reservation is just deleted or scheduled, don't nominate it.
		r, err := nm.rLister.Get(rName)
		if err != nil {
			klog.V(4).InfoS("reservation doesn't exist in rLister, aborted adding it to the nominator",
				"pod", klog.KObj(pi.Pod), "reservation", rName)
			return
		}
		if reservationutil.IsReservationActive(r) {
			klog.V(4).InfoS("reservation is already scheduled to a node and become active, aborted to adding it to the nominator",
				"pod", klog.KObj(pi.Pod), "reservation", rName)
			return
		}
	}

	nm.nominatedReservePodToNode[pi.Pod.UID] = nodeName
	for _, npi := range nm.nominatedReservePod[nodeName] {
		if npi.Pod.UID == pi.Pod.UID {
			klog.V(4).InfoS("reservation already exists in the nominator", "pod", klog.KObj(npi.Pod), "reservation", rName)
			return
		}
	}
	nm.nominatedReservePod[nodeName] = append(nm.nominatedReservePod[nodeName], pi)
}

func (nm *nominator) NominatedReservePodForNode(nodeName string) []*framework.PodInfo {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	// Make a copy of the nominated Pods so the caller can mutate safely.
	reservePods := make([]*framework.PodInfo, len(nm.nominatedReservePod[nodeName]))
	for i := 0; i < len(reservePods); i++ {
		reservePods[i] = nm.nominatedReservePod[nodeName][i].DeepCopy()
	}
	return reservePods
}

func (nm *nominator) DeleteReservePod(pi *framework.PodInfo) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	nm.deleteReservePod(pi)
}

func (nm *nominator) deleteReservePod(pi *framework.PodInfo) {
	nnn, ok := nm.nominatedReservePodToNode[pi.Pod.UID]
	if !ok {
		return
	}
	for i, np := range nm.nominatedReservePod[nnn] {
		if np.Pod.UID == pi.Pod.UID {
			nm.nominatedReservePod[nnn] = append(nm.nominatedReservePod[nnn][:i], nm.nominatedReservePod[nnn][i+1:]...)
			if len(nm.nominatedReservePod[nnn]) == 0 {
				delete(nm.nominatedReservePod, nnn)
			}
			break
		}
	}
	delete(nm.nominatedReservePodToNode, pi.Pod.UID)
}

func (nm *nominator) RemoveNominatedReservation(pod *corev1.Pod) {
	nm.lock.Lock()
	defer nm.lock.Unlock()

	delete(nm.nominatedPodToNode, pod.UID)
}

func (nm *nominator) GetNominatedReservation(pod *corev1.Pod, nodeName string) types.UID {
	nm.lock.RLock()
	defer nm.lock.RUnlock()
	return nm.nominatedPodToNode[pod.UID][nodeName]
}

// TODO(joseph): Should move the function into frameworkext package as default nominator

func (pl *Plugin) NominateReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (*frameworkext.ReservationInfo, *framework.Status) {
	if reservationutil.IsReservePod(pod) {
		return nil, nil
	}

	state := getStateData(cycleState)

	var reservationInfos []*frameworkext.ReservationInfo
	if nodeRState := state.nodeReservationStates[nodeName]; nodeRState != nil {
		reservationInfos = nodeRState.matchedOrIgnored
	}

	if len(reservationInfos) == 0 {
		return nil, nil
	}

	if len(reservationInfos) == 1 && state.hasAffinity {
		return reservationInfos[0], nil
	}

	rInfo := pl.GetNominatedReservation(pod, nodeName)
	if rInfo != nil {
		return rInfo, nil
	}

	extender, ok := pl.handle.(frameworkext.FrameworkExtender)
	if !ok {
		return nil, framework.AsStatus(fmt.Errorf("not implemented frameworkext.FrameworkExtender"))
	}

	reservations := make([]*frameworkext.ReservationInfo, 0, len(reservationInfos))
	for i := range reservationInfos {
		status := extender.RunNominateReservationFilterPlugins(ctx, cycleState, pod, reservationInfos[i], nodeName)
		if !status.IsSuccess() {
			continue
		}
		reservations = append(reservations, reservationInfos[i])
	}
	if len(reservations) == 0 {
		return nil, nil
	}

	if len(reservations) == 1 {
		return reservations[0], nil
	}

	nominated, _ := findMostPreferredReservationByOrder(reservations)
	if nominated != nil {
		return nominated, nil
	}

	reservationScoreList, err := prioritizeReservations(ctx, extender, cycleState, pod, reservations, nodeName)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	sort.Slice(reservationScoreList, func(i, j int) bool {
		return reservationScoreList[i].Score > reservationScoreList[j].Score
	})

	nominated = nil
	for _, v := range reservations {
		if v.UID() == reservationScoreList[0].UID {
			nominated = v
			break
		}
	}
	if nominated == nil {
		return nil, framework.AsStatus(fmt.Errorf("missing the most suitable reservation %v(%v)",
			klog.KRef(reservationScoreList[0].Namespace, reservationScoreList[0].Name), reservationScoreList[0].UID))
	}
	return nominated, nil
}

func (pl *Plugin) AddNominatedReservation(pod *corev1.Pod, nodeName string, rInfo *frameworkext.ReservationInfo) {
	pl.nominator.AddNominatedReservation(pod, nodeName, rInfo)
}

func (pl *Plugin) RemoveNominatedReservations(pod *corev1.Pod) {
	pl.nominator.RemoveNominatedReservation(pod)
}

func (pl *Plugin) AddNominatedReservePod(pod *corev1.Pod, nodeName string) {
	podInfo, _ := framework.NewPodInfo(pod)
	pl.nominator.AddNominatedReservePod(podInfo, nodeName)
}

func (pl *Plugin) DeleteNominatedReservePod(pod *corev1.Pod) {
	podInfo, _ := framework.NewPodInfo(pod)
	pl.nominator.DeleteReservePod(podInfo)
}

func (pl *Plugin) GetNominatedReservation(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	reservationID := pl.nominator.GetNominatedReservation(pod, nodeName)
	if reservationID == "" {
		return nil
	}
	return pl.reservationCache.getReservationInfoByUID(reservationID)
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
