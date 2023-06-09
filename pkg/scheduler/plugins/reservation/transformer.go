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
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func (pl *Plugin) BeforePreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool, *framework.Status) {
	state, restored, err := pl.prepareMatchReservationState(ctx, cycleState, pod)
	if err != nil {
		return nil, false, framework.AsStatus(err)
	}
	cycleState.Write(stateKey, state)
	return pod, restored, nil
}

func (pl *Plugin) prepareMatchReservationState(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*stateData, bool, error) {
	allNodes, err := pl.handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		return nil, false, fmt.Errorf("cannot list NodeInfo, err: %v", err)
	}

	reservationAffinity, err := reservationutil.GetRequiredReservationAffinity(pod)
	if err != nil {
		klog.ErrorS(err, "Failed to parse reservation affinity", "pod", klog.KObj(pod))
		return nil, false, err
	}

	var stateIndex int32
	allNodeReservationStates := make([]*nodeReservationState, len(allNodes))
	allPluginToRestoreState := make([]frameworkext.PluginToReservationRestoreStates, len(allNodes))

	isReservedPod := reservationutil.IsReservePod(pod)
	parallelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := NewErrorChannel()

	extender, _ := pl.handle.(frameworkext.FrameworkExtender)
	if extender != nil {
		status := extender.RunReservationExtensionPreRestoreReservation(ctx, cycleState, pod)
		if !status.IsSuccess() {
			return nil, false, status.AsError()
		}
	}

	processNode := func(i int) {
		nodeInfo := allNodes[i]
		node := nodeInfo.Node()
		if node == nil {
			klog.V(4).InfoS("BeforePreFilter failed to get node", "pod", klog.KObj(pod), "nodeInfo", nodeInfo)
			return
		}

		rOnNode := pl.reservationCache.listAvailableReservationInfosOnNode(node.Name)
		if len(rOnNode) == 0 {
			return
		}

		var unmatched, matched []*frameworkext.ReservationInfo
		for _, rInfo := range rOnNode {
			if !rInfo.IsAvailable() {
				continue
			}

			// In this case, the Controller has not yet updated the status of the Reservation to Succeeded,
			// but in fact it can no longer be used for allocation. So it's better to skip first.
			if rInfo.IsAllocateOnce() && len(rInfo.AssignedPods) > 0 {
				continue
			}

			if !isReservedPod && !rInfo.IsTerminating() && matchReservation(pod, node, rInfo, reservationAffinity) {
				matched = append(matched, rInfo)

			} else if len(rInfo.AssignedPods) > 0 {
				unmatched = append(unmatched, rInfo)
				if !isReservedPod {
					klog.V(6).InfoS("got reservation on node does not match the pod", "reservation", klog.KObj(rInfo), "pod", klog.KObj(pod))
				}
			}
		}
		if len(matched) == 0 && len(unmatched) == 0 {
			return
		}

		for _, rInfo := range unmatched {
			if err = restoreUnmatchedReservations(nodeInfo, rInfo); err != nil {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		}
		// Save requested state after trimmed by unmatched to support reservation allocate policy.
		podRequested := nodeInfo.Requested.Clone()

		podInfoMap := make(map[types.UID]*framework.PodInfo)
		for _, podInfo := range nodeInfo.Pods {
			if !reservationutil.IsReservePod(podInfo.Pod) {
				podInfoMap[podInfo.Pod.UID] = podInfo
			}
		}

		var totalAligned, totalRestricted int
		rAllocated := corev1.ResourceList{}
		for _, rInfo := range matched {
			if err = restoreMatchedReservation(nodeInfo, rInfo, podInfoMap); err != nil {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}

			util.AddResourceList(rAllocated, rInfo.Allocated)
			switch rInfo.GetAllocatePolicy() {
			case schedulingv1alpha1.ReservationAllocatePolicyAligned:
				totalAligned++
			case schedulingv1alpha1.ReservationAllocatePolicyRestricted:
				totalRestricted++
			}
		}

		var pluginToRestoreState frameworkext.PluginToReservationRestoreStates
		if extender != nil {
			var status *framework.Status
			pluginToRestoreState, status = extender.RunReservationExtensionRestoreReservation(ctx, cycleState, pod, matched, unmatched, nodeInfo)
			if !status.IsSuccess() {
				errCh.SendErrorWithCancel(err, cancel)
				return
			}
		}

		if len(matched) > 0 || len(unmatched) > 0 {
			index := atomic.AddInt32(&stateIndex, 1)
			allNodeReservationStates[index-1] = &nodeReservationState{
				nodeName:        node.Name,
				matched:         matched,
				podRequested:    podRequested,
				rAllocated:      framework.NewResource(rAllocated),
				totalAligned:    totalAligned,
				totalRestricted: totalRestricted,
			}
			allPluginToRestoreState[index-1] = pluginToRestoreState
		}
		klog.V(4).Infof("Pod %v has reservations on node %v, %d matched, %d unmatched", klog.KObj(pod), node.Name, len(matched), len(unmatched))
	}
	pl.handle.Parallelizer().Until(parallelCtx, len(allNodes), processNode)
	err = errCh.ReceiveError()
	if err != nil {
		return nil, false, err
	}

	allNodeReservationStates = allNodeReservationStates[:stateIndex]
	allPluginToRestoreState = allPluginToRestoreState[:stateIndex]

	podRequests, _ := resourceapi.PodRequestsAndLimits(pod)
	podRequestResources := framework.NewResource(podRequests)
	state := &stateData{
		hasAffinity:           reservationAffinity != nil,
		podRequests:           podRequests,
		podRequestsResources:  podRequestResources,
		nodeReservationStates: map[string]nodeReservationState{},
	}
	pluginToNodeReservationRestoreState := frameworkext.PluginToNodeReservationRestoreStates{}
	for index, v := range allNodeReservationStates {
		state.nodeReservationStates[v.nodeName] = *v
		for pluginName, pluginState := range allPluginToRestoreState[index] {
			if pluginState == nil {
				continue
			}
			nodeRestoreStates := pluginToNodeReservationRestoreState[pluginName]
			if nodeRestoreStates == nil {
				nodeRestoreStates = frameworkext.NodeReservationRestoreStates{}
				pluginToNodeReservationRestoreState[pluginName] = nodeRestoreStates
			}
			nodeRestoreStates[v.nodeName] = pluginState
		}
	}
	if extender != nil {
		status := extender.RunReservationExtensionFinalRestoreReservation(ctx, cycleState, pod, pluginToNodeReservationRestoreState)
		if !status.IsSuccess() {
			return nil, false, status.AsError()
		}
	}

	return state, len(allNodeReservationStates) > 0, nil
}

func (pl *Plugin) AfterPreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	return nil
}

func restoreMatchedReservation(nodeInfo *framework.NodeInfo, rInfo *frameworkext.ReservationInfo, podInfoMap map[types.UID]*framework.PodInfo) error {
	reservePod := rInfo.GetReservePod()

	// Retain ports that are not used by other Pods. These ports need to be erased from NodeInfo.UsedPorts,
	// otherwise it may cause Pod port conflicts
	allocatablePorts := util.CloneHostPorts(rInfo.AllocatablePorts)
	util.RemoveHostPorts(allocatablePorts, rInfo.AllocatedPorts)
	util.ResetHostPorts(reservePod, allocatablePorts)

	// When AllocateOnce is disabled, some resources may have been allocated,
	// and an additional resource record will be accumulated at this time.
	// Even if the Reservation is not bound by the Pod (e.g. Reservation is enabled with AllocateOnce),
	// these resources held by the Reservation need to be returned, to ensure that
	// the Pod can pass through each filter plugin during scheduling.
	// The returned resources include scalar resources such as CPU/Memory, ports etc..
	if err := nodeInfo.RemovePod(reservePod); err != nil {
		return err
	}

	return nil
}

func restoreUnmatchedReservations(nodeInfo *framework.NodeInfo, rInfo *frameworkext.ReservationInfo) error {
	// Reservations and Pods that consume the Reservations are cumulative in resource accounting.
	// For example, on a 32C machine, ReservationA reserves 8C, and then PodA uses ReservationA to allocate 4C,
	// then the record on NodeInfo is that 12C is allocated. But in fact it should be calculated according to 8C,
	// so we need to return some resources.
	reservePod := rInfo.GetReservePod().DeepCopy()
	if err := nodeInfo.RemovePod(reservePod); err != nil {
		klog.Errorf("Failed to remove reserve pod %v from node %v, err: %v", klog.KObj(rInfo), nodeInfo.Node().Name, err)
		return err
	}
	occupyUnallocatedResources(rInfo, reservePod, nodeInfo)
	return nil
}

func occupyUnallocatedResources(rInfo *frameworkext.ReservationInfo, reservePod *corev1.Pod, nodeInfo *framework.NodeInfo) {
	if len(rInfo.AssignedPods) == 0 {
		nodeInfo.AddPod(reservePod)
	} else {
		for i := range reservePod.Spec.Containers {
			reservePod.Spec.Containers[i].Resources.Requests = corev1.ResourceList{}
		}
		remainedResource := quotav1.SubtractWithNonNegativeResult(rInfo.Allocatable, rInfo.Allocated)
		if !quotav1.IsZero(remainedResource) {
			reservePod.Spec.Containers = append(reservePod.Spec.Containers, corev1.Container{
				Resources: corev1.ResourceRequirements{Requests: remainedResource},
			})
		}
		nodeInfo.AddPod(reservePod)
	}
}

func matchReservation(pod *corev1.Pod, node *corev1.Node, reservation *frameworkext.ReservationInfo, reservationAffinity *reservationutil.RequiredReservationAffinity) bool {
	if !reservationutil.MatchReservationOwners(pod, reservation.GetPodOwners()) {
		return false
	}

	if reservationAffinity != nil {
		// NOTE: There are some special scenarios.
		// For example, the AZ where the Pod wants to select the Reservation is cn-hangzhou, but the Reservation itself
		// does not have this information, so it needs to perceive the label of the Node when Matching Affinity.
		fakeNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   reservation.GetName(),
				Labels: map[string]string{},
			},
		}
		for k, v := range node.Labels {
			fakeNode.Labels[k] = v
		}
		for k, v := range reservation.GetObject().GetLabels() {
			fakeNode.Labels[k] = v
		}
		return reservationAffinity.Match(fakeNode)
	}
	return true
}
