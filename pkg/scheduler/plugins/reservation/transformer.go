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
	"k8s.io/apimachinery/pkg/util/sets"
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
	cycleState.Write(stateKey, state)

	klog.V(4).InfoS("BeforePreFilter successfully prepares reservation state", "pod", klog.KObj(pod))

	if len(state.matched) == 0 {
		klog.V(5).InfoS("BeforePreFilter skips for no reservation matched", "pod", klog.KObj(pod))
	}
	return pod, false
}

func (p *Plugin) AfterPreFilter(handle frameworkext.ExtendedHandle, cycleState *framework.CycleState, pod *corev1.Pod) error {
	if reservationutil.IsReservePod(pod) {
		return nil
	}

	state := getStateData(cycleState)
	for nodeName, reservationKeys := range state.unmatched {
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
		for reservationKey := range reservationKeys {
			reservation := getReservationCache().GetInCacheByKey(reservationKey)
			if reservation == nil {
				// TODO: maybe return error more better
				klog.Warningf("Failed to get reservation %v from cache on node %v", reservationKey, nodeName)
				continue
			}

			if len(reservation.Status.CurrentOwners) == 0 {
				continue
			}

			reservePod := reservationutil.NewReservePod(reservation)
			if err := nodeInfo.RemovePod(reservePod); err != nil {
				klog.Errorf("Failed to remove reserve pod %v from node %v, err: %v", klog.KObj(reservation), nodeName, err)
				return err
			}
			// NOTE: To achieve incremental update with frameworkext.TemporarySnapshot, you need to set Generation to -1
			nodeInfo.Generation = -1

			for i := range reservePod.Spec.Containers {
				reservePod.Spec.Containers[i].Resources.Requests = corev1.ResourceList{}
			}
			remainedResource := quotav1.SubtractWithNonNegativeResult(reservation.Status.Allocatable, reservation.Status.Allocated)
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
	for nodeName, reservationKeys := range state.matched {
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

		for reservationKey := range reservationKeys {
			reservation := getReservationCache().GetInCacheByKey(reservationKey)
			if reservation == nil {
				// TODO: maybe return error more better
				klog.Warningf("Failed to get reservation %v from cache on node %v", reservationKey, nodeName)
				continue
			}
			if err := restoreReservedResources(handle, cycleState, pod, reservation, nodeInfo, podInfoMap); err != nil {
				return err
			}
			resourceNames := quotav1.Intersection(quotav1.ResourceNames(reservation.Status.Allocatable), podRequestsResourceNames)
			remainedResource := quotav1.SubtractWithNonNegativeResult(reservation.Status.Allocatable, reservation.Status.Allocated)
			if quotav1.IsZero(quotav1.Mask(remainedResource, resourceNames)) {
				delete(reservationKeys, reservationKey)
			}
		}
	}

	return nil
}

func restoreReservedResources(handle frameworkext.ExtendedHandle, cycleState *framework.CycleState, pod *corev1.Pod, reservation *schedulingv1alpha1.Reservation, nodeInfo *framework.NodeInfo, podInfoMap map[types.UID]*framework.PodInfo) error {
	reservePod := reservationutil.NewReservePod(reservation)
	reservePodInfo := framework.NewPodInfo(reservePod)

	// Retain ports that are not used by other Pods. These ports need to be erased from NodeInfo.UsedPorts,
	// otherwise it may cause Pod port conflicts
	retainReservePodUnusedPorts(reservePod, reservation, podInfoMap)

	// When AllocateOnce is disabled, some resources may have been allocated,
	// and an additional resource record will be accumulated at this time.
	// Even if the Reservation is not bound by the Pod (e.g. Reservation is enabled with AllocateOnce),
	// these resources held by the Reservation need to be returned, so as to ensure that
	// the Pod can pass through each filter plugin during scheduling.
	// The returned resources include scalar resources such as CPU/Memory, ports etc..
	if err := nodeInfo.RemovePod(reservePod); err != nil {
		return err
	}
	// NOTE: To achieve incremental update with frameworkext.TemporarySnapshot, you need to set Generation to -1
	nodeInfo.Generation = -1

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
		status := extender.RunReservationPreFilterExtensionRemoveReservation(context.Background(), cycleState, pod, reservation, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}

		for _, assignedPod := range reservation.Status.CurrentOwners {
			if assignedPodInfo, ok := podInfoMap[assignedPod.UID]; ok {
				status := extender.RunReservationPreFilterExtensionAddPodInReservation(context.Background(), cycleState, pod, assignedPodInfo, reservation, nodeInfo)
				if !status.IsSuccess() {
					return status.AsError()
				}
			}
		}
	}
	return nil
}

func retainReservePodUnusedPorts(reservePod *corev1.Pod, reservation *schedulingv1alpha1.Reservation, podInfoMap map[types.UID]*framework.PodInfo) {
	port := getReservePorts(reservation)
	if len(port) == 0 {
		return
	}

	// TODO(joseph): maybe we can record allocated Ports by Pods in Reservation.Status
	portReserved := framework.HostPortInfo{}
	for ip, protocolPortMap := range port {
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
	var lock sync.Mutex
	stateData := &stateData{
		matched:   map[string]sets.String{},
		unmatched: map[string]sets.String{},
	}
	processNode := func(i int) {
		matched := sets.NewString()
		unmatched := sets.NewString()
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
			reservation := rCache.GetInCache(r)
			if reservation == nil {
				klog.Warningf("not found reservation %v in cache on node %v", klog.KObj(r), node.Name)
				continue
			}
			// only count available reservations, ignore succeeded ones
			if !reservationutil.IsReservationAvailable(reservation) {
				continue
			}

			if matchReservationOwners(pod, reservation) {
				matched.Insert(reservationutil.GetReservationKey(r))
			} else {
				if len(reservation.Status.CurrentOwners) > 0 {
					unmatched.Insert(reservationutil.GetReservationKey(r))
				}
				klog.V(6).InfoS("got reservation on node does not match the pod", "reservation", klog.KObj(r), "pod", klog.KObj(pod))
			}
		}

		lock.Lock()
		if len(matched) > 0 {
			stateData.matched[node.Name] = matched
		}
		if len(unmatched) > 0 {
			stateData.unmatched[node.Name] = unmatched
		}
		lock.Unlock()

		// no reservation matched on this node
		if matched.Len() == 0 {
			return
		}

		restorePVCRefCounts(handle, nodeInfo, pod, matched)

		klog.V(4).Infof("BeforePreFilter Pod %v has %d matched reservations, %d unmatched reservations on node %v",
			klog.KObj(pod), len(matched), len(unmatched), node.Name)
	}
	p.parallelizeUntil(context.TODO(), len(allNodes), processNode)

	return stateData, nil
}

func genPVCRefKey(pvc *corev1.PersistentVolumeClaim) string {
	return pvc.Namespace + "/" + pvc.Name
}

func restorePVCRefCounts(handle framework.Handle, nodeInfo *framework.NodeInfo, pod *corev1.Pod, matched sets.String) {
	// VolumeRestrictions plugin will check PVCRefCounts in NodeInfo in BeforePreFilter phase.
	// If the scheduling pod declare a PVC with ReadWriteOncePod access mode and the
	// PVC has been used by other scheduled pod, the scheduling pod will be marked
	// as UnschedulableAndUnresolvable.
	// So we need to modify PVCRefCounts that are added by matched reservePod in nodeInfo
	// to schedule the real pod.
	pvcLister := handle.SharedInformerFactory().Core().V1().PersistentVolumeClaims().Lister()
	for reservationKey := range matched {
		reservation := getReservationCache().GetInCacheByKey(reservationKey)
		if reservation == nil {
			continue
		}
		podSpecTemplate := reservation.Spec.Template
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
