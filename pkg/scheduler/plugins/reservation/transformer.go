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
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	index "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/indexer"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func (p *Plugin) BeforePreFilter(handle frameworkext.ExtendedHandle, cycleState *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool) {
	if reservationutil.IsReservePod(pod) {
		return nil, false
	}

	// list reservations and nodes, and check each available reservation whether it matches the pod or not
	state, err := p.prepareMatchReservationState(handle, pod)
	if err != nil {
		klog.Warningf("BeforePreFilter failed to get matched reservations, err: %v", err)
		return nil, false
	}
	klog.V(4).InfoS("BeforePreFilter successfully prepares reservation state", "pod", klog.KObj(pod))

	cycleState.Write(preFilterStateKey, state)
	frameworkext.InitDiscardedReservations(cycleState)

	if state.skip {
		klog.V(5).InfoS("BeforePreFilter skips for no reservation matched", "pod", klog.KObj(pod))
		return nil, false
	}

	return pod, true
}

func (p *Plugin) AfterPreFilter(handle frameworkext.ExtendedHandle, cycleState *framework.CycleState, pod *corev1.Pod) error {
	if reservationutil.IsReservePod(pod) {
		return nil
	}

	state := getPreFilterState(cycleState)
	if state == nil || state.skip {
		return nil
	}

	reservationLister := handle.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Reservations().Lister()
	for nodeName, reservationNames := range state.unmatched {
		nodeInfo, err := handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
		if err != nil {
			continue
		}
		if nodeInfo.Node() == nil {
			continue
		}

		// Reservations and Pods that consume the Reservations are cumulative in resource accounting.
		// For example, on a 32C machine, ReservationA reserves 8C, and then PodA uses ReservationA to allocate 4C,
		// then the record on NodeInfo is that 12C is allocated. But in fact it should be calculated according to 8C,
		// so we need to return some resources.
		for _, reservationName := range reservationNames {
			reservation, err := reservationLister.Get(reservationName)
			if err != nil {
				klog.Errorf("Failed to get reservation %v on node %v, err: %v", reservationName, nodeName, err)
				continue
			}
			cache := getReservationCache()
			rInfo := cache.GetInCache(reservation)
			if rInfo == nil {
				klog.Warningf("Failed to get reservation %v from cache on node %v", reservationName, nodeName)
				continue
			}

			if len(rInfo.Reservation.Status.CurrentOwners) == 0 {
				continue
			}

			reservePod := reservationutil.NewReservePod(rInfo.Reservation)
			if err := nodeInfo.RemovePod(reservePod); err != nil {
				klog.Errorf("Failed to remove reserve pod %v from node %v, err: %v", klog.KObj(reservation), nodeName, err)
				continue
			}

			for i := range reservePod.Spec.Containers {
				reservePod.Spec.Containers[i].Resources.Requests = corev1.ResourceList{}
			}
			remainedResource := quotav1.SubtractWithNonNegativeResult(reservation.Status.Allocatable, rInfo.Reservation.Status.Allocated)
			if !quotav1.IsZero(remainedResource) {
				reservePod.Spec.Containers = append(reservePod.Spec.Containers, corev1.Container{
					Resources: corev1.ResourceRequirements{Requests: remainedResource},
				})
			}
			nodeInfo.AddPod(reservePod)
		}
	}

	podRequests, _ := resourceapi.PodRequestsAndLimits(pod)
	podRequestsResourceNames := quotav1.ResourceNames(podRequests)
	for nodeName, rInfos := range state.matchedCache.nodeToR {
		nodeInfo, err := handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
		if err != nil {
			continue
		}
		if nodeInfo.Node() == nil {
			continue
		}

		podInfoMap := make(map[types.UID]*framework.PodInfo)
		for _, podInfo := range nodeInfo.Pods {
			if !reservationutil.IsReservePod(podInfo.Pod) {
				podInfoMap[podInfo.Pod.UID] = podInfo
			}
		}

		for reservationUID, rInfo := range rInfos {
			if err := restoreReservedResources(handle, cycleState, pod, rInfo, nodeInfo, podInfoMap); err != nil {
				return err
			}
			resourceNames := quotav1.Intersection(quotav1.ResourceNames(rInfo.Reservation.Status.Allocatable), podRequestsResourceNames)
			remainedResource := quotav1.SubtractWithNonNegativeResult(rInfo.Reservation.Status.Allocatable, rInfo.Reservation.Status.Allocated)
			if quotav1.IsZero(quotav1.Mask(remainedResource, resourceNames)) {
				delete(rInfos, reservationUID)
			}
		}
	}

	return nil
}

func restoreReservedResources(handle frameworkext.ExtendedHandle, cycleState *framework.CycleState, pod *corev1.Pod, rInfo *reservationInfo, nodeInfo *framework.NodeInfo, podInfoMap map[types.UID]*framework.PodInfo) error {
	reservePod := reservationutil.NewReservePod(rInfo.Reservation)
	reservePodInfo := framework.NewPodInfo(reservePod)

	// Retain ports that are not used by other Pods. These ports need to be erased from NodeInfo.UsedPorts,
	// otherwise it may cause Pod port conflicts
	retainReservePodUnusedPorts(reservePod, rInfo, podInfoMap)

	// When AllocateOnce is disabled, some resources may have been allocated,
	// and an additional resource record will be accumulated at this time.
	// Even if the Reservation is not bound by the Pod (e.g. Reservation is enabled with AllocateOnce),
	// these resources held by the Reservation need to be returned, so as to ensure that
	// the Pod can pass through each filter plugin during scheduling.
	// The returned resources include scalar resources such as CPU/Memory, ports etc..
	if err := nodeInfo.RemovePod(reservePod); err != nil {
		return err
	}

	// Regardless of whether the Reservation enables AllocateOnce
	// and whether the Reservation uses high-availability constraints such as InterPodAffinity/PodTopologySpread,
	// we should trigger the plugins to update the state and erase the Reservation-related state in this round
	// of scheduling to ensure that the Pod can pass these filters.
	// Although Reservation may reserve a large number of resources for a batch of Pods to use,
	// and it is possible to use these high-availability constraints to cause resources to be unallocated and wasteful,
	// users need to bear this waste.
	status := handle.RunPreFilterExtensionRemovePod(context.Background(), cycleState, pod, reservePodInfo, nodeInfo)
	if !status.IsSuccess() {
		return status.AsError()
	}

	// We should find an appropriate time to return resources allocated by custom plugins held by Reservation,
	// such as fine-grained CPUs(CPU Cores), Devices(e.g. GPU/RDMA/FPGA etc.).
	if extender, ok := handle.(frameworkext.FrameworkExtender); ok {
		status := extender.RunReservationPreFilterExtensionRemoveReservation(context.Background(), cycleState, pod, rInfo.Reservation, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}

		for _, assignedPod := range rInfo.Reservation.Status.CurrentOwners {
			if assignedPodInfo, ok := podInfoMap[assignedPod.UID]; ok {
				status := extender.RunReservationPreFilterExtensionAddPodInReservation(context.Background(), cycleState, pod, assignedPodInfo, rInfo.Reservation, nodeInfo)
				if !status.IsSuccess() {
					return status.AsError()
				}
			}
		}
	}
	return nil
}

func retainReservePodUnusedPorts(reservePod *corev1.Pod, rInfo *reservationInfo, podInfoMap map[types.UID]*framework.PodInfo) {
	if len(rInfo.Port) == 0 {
		return
	}

	// TODO(joseph): maybe we can record allocated Ports by Pods in Reservation.Status
	portReserved := framework.HostPortInfo{}
	for ip, protocolPortMap := range rInfo.Port {
		for protocolPort := range protocolPortMap {
			portReserved.Add(ip, protocolPort.Protocol, protocolPort.Port)
		}
	}

	removed := false
	for _, assignedPodInfo := range podInfoMap {
		for i := range assignedPodInfo.Pod.Spec.Containers {
			container := &assignedPodInfo.Pod.Spec.Containers[i]
			for j := range container.Ports {
				podPort := &container.Ports[j]
				portReserved.Remove(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
				removed = true
			}
		}
	}
	if !removed {
		return
	}

	for i := range reservePod.Spec.Containers {
		container := &reservePod.Spec.Containers[i]
		if len(container.Ports) > 0 {
			container.Ports = nil
		}
	}

	container := &reservePod.Spec.Containers[0]
	for ip, protocolPortMap := range portReserved {
		for ports := range protocolPortMap {
			container.Ports = append(container.Ports, corev1.ContainerPort{
				HostPort: ports.Port,
				Protocol: corev1.Protocol(ports.Protocol),
				HostIP:   ip,
			})
		}
	}
}

func (p *Plugin) prepareMatchReservationState(handle frameworkext.ExtendedHandle, pod *corev1.Pod) (*stateData, error) {
	allNodes, err := handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		return nil, fmt.Errorf("cannot list NodeInfo, err: %v", err)
	}

	indexer := handle.KoordinatorSharedInformerFactory().Scheduling().V1alpha1().Reservations().Informer().GetIndexer()
	matchedCache := newAvailableCache()
	var lock sync.Mutex
	unmatched := map[string][]string{}
	processNode := func(i int) {
		var otherReservations []string
		nodeInfo := allNodes[i]
		node := nodeInfo.Node()
		if node == nil {
			klog.V(4).InfoS("BeforePreFilter failed to get node", "pod", klog.KObj(pod), "nodeInfo", nodeInfo)
			return
		}

		rOnNode, err := indexer.ByIndex(index.ReservationStatusNodeNameIndex, node.Name)
		if err != nil {
			klog.V(3).InfoS("BeforePreFilter failed to list reservations", "node", node.Name, "index", index.ReservationStatusNodeNameIndex, "err", err)
			return
		}
		klog.V(6).InfoS("BeforePreFilter indexer list reservation on node", "node", node.Name, "count", len(rOnNode))
		rCache := getReservationCache()
		for _, obj := range rOnNode {
			r, ok := obj.(*schedulingv1alpha1.Reservation)
			if !ok {
				klog.V(5).Infof("unable to convert to *schedulingv1alpha1.Reservation, obj %T", obj)
				continue
			}
			rInfo := rCache.GetInCache(r)
			if rInfo == nil {
				rInfo = newReservationInfo(r)
			}
			// only count available reservations, ignore succeeded ones
			if !reservationutil.IsReservationAvailable(rInfo.Reservation) {
				continue
			}

			if matchReservationOwners(pod, rInfo.Reservation) {
				matchedCache.Add(rInfo.Reservation)
			} else {
				if len(rInfo.Reservation.Status.CurrentOwners) > 0 {
					otherReservations = append(otherReservations, r.Name)
				}
				if klog.V(6).Enabled() {
					klog.InfoS("got reservation on node does not match the pod",
						"reservation", klog.KObj(r), "pod", klog.KObj(pod),
						"reason", dumpMatchReservationReason(pod, newReservationInfo(rInfo.Reservation)))
				}
			}
		}
		if len(otherReservations) > 0 {
			lock.Lock()
			unmatched[node.Name] = otherReservations
			lock.Unlock()
		}
		// no reservation matched on this node
		if matchedCache.Len() == 0 {
			return
		}

		restorePVCRefCounts(handle, nodeInfo, pod, matchedCache)

		klog.V(4).InfoS("BeforePreFilter get matched reservations",
			"pod", klog.KObj(pod), "node", node.Name, "count", matchedCache.Len())
	}
	p.parallelizeUntil(context.TODO(), len(allNodes), processNode)

	state := &stateData{
		skip:         matchedCache.Len() == 0, // skip if no reservation matched
		matchedCache: matchedCache,
		unmatched:    unmatched,
	}

	return state, nil
}

func genPVCRefKey(pvc *corev1.PersistentVolumeClaim) string {
	return pvc.Namespace + "/" + pvc.Name
}

func restorePVCRefCounts(handle framework.Handle, nodeInfo *framework.NodeInfo, pod *corev1.Pod, matchedCache *AvailableCache) {
	// VolumeRestrictions plugin will check PVCRefCounts in NodeInfo in BeforePreFilter phase.
	// If the scheduling pod declare a PVC with ReadWriteOncePod access mode and the
	// PVC has been used by other scheduled pod, the scheduling pod will be marked
	// as UnschedulableAndUnresolvable.
	// So we need to modify PVCRefCounts that are added by matched reservePod in nodeInfo
	// to schedule the real pod.
	pvcLister := handle.SharedInformerFactory().Core().V1().PersistentVolumeClaims().Lister()
	for _, rr := range matchedCache.nodeToR[nodeInfo.Node().Name] {
		podSpecTemplate := rr.Reservation.Spec.Template
		for _, volume := range podSpecTemplate.Spec.Volumes {
			if volume.PersistentVolumeClaim == nil {
				continue
			}
			pvc, err := pvcLister.PersistentVolumeClaims(pod.Namespace).Get(volume.PersistentVolumeClaim.ClaimName)
			if err != nil {
				continue
			}

			if !v1helper.ContainsAccessMode(pvc.Spec.AccessModes, corev1.ReadWriteOncePod) {
				continue
			}

			nodeInfo.PVCRefCounts[genPVCRefKey(pvc)] -= 1
		}
	}
}
