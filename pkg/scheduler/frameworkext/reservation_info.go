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

package frameworkext

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	corev1helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type ReservationInfo struct {
	Reservation      *schedulingv1alpha1.Reservation
	Pod              *corev1.Pod
	ResourceNames    []corev1.ResourceName
	Allocatable      corev1.ResourceList
	Allocated        corev1.ResourceList
	AllocatablePorts framework.HostPortInfo
	AllocatedPorts   framework.HostPortInfo
	AssignedPods     map[types.UID]*PodRequirement
	OwnerMatchers    []reservationutil.ReservationOwnerMatcher
	ParseError       error
}

type PodRequirement struct {
	Namespace string
	Name      string
	UID       types.UID
	Requests  corev1.ResourceList
	Ports     framework.HostPortInfo
}

func NewPodRequirement(pod *corev1.Pod) *PodRequirement {
	requests, _ := resource.PodRequestsAndLimits(pod)
	ports := util.RequestedHostPorts(pod)
	return &PodRequirement{
		Namespace: pod.Namespace,
		Name:      pod.Name,
		UID:       pod.UID,
		Requests:  requests,
		Ports:     ports,
	}
}

func (p *PodRequirement) Clone() *PodRequirement {
	return &PodRequirement{
		Namespace: p.Namespace,
		Name:      p.Name,
		UID:       p.UID,
		Requests:  p.Requests.DeepCopy(),
		Ports:     util.CloneHostPorts(p.Ports),
	}
}

func NewReservationInfo(r *schedulingv1alpha1.Reservation) *ReservationInfo {
	allocatable := reservationutil.ReservationRequests(r)
	resourceNames := quotav1.ResourceNames(allocatable)
	reservedPod := reservationutil.NewReservePod(r)

	ownerMatchers, err := reservationutil.ParseReservationOwnerMatchers(r.Spec.Owners)
	if err != nil {
		klog.ErrorS(err, "Failed to parse reservation owner matchers", "reservation", klog.KObj(r))
	}

	return &ReservationInfo{
		Reservation:      r,
		Pod:              reservedPod,
		ResourceNames:    resourceNames,
		Allocatable:      allocatable,
		AllocatablePorts: util.RequestedHostPorts(reservedPod),
		AssignedPods:     map[types.UID]*PodRequirement{},
		OwnerMatchers:    ownerMatchers,
		ParseError:       err,
	}
}

func NewReservationInfoFromPod(pod *corev1.Pod) *ReservationInfo {
	allocatable, _ := resource.PodRequestsAndLimits(pod)
	resourceNames := quotav1.ResourceNames(allocatable)

	owners, err := apiext.GetReservationOwners(pod.Annotations)
	if err != nil {
		klog.ErrorS(err, "Invalid reservation owners annotation of Pod", "pod", klog.KObj(pod))
	}
	var ownerMatchers []reservationutil.ReservationOwnerMatcher
	if owners != nil {
		ownerMatchers, err = reservationutil.ParseReservationOwnerMatchers(owners)
		if err != nil {
			klog.ErrorS(err, "Failed to parse reservation owner matchers of pod", "pod", klog.KObj(pod))
		}
	}

	return &ReservationInfo{
		Pod:              pod,
		ResourceNames:    resourceNames,
		Allocatable:      allocatable,
		AllocatablePorts: util.RequestedHostPorts(pod),
		AssignedPods:     map[types.UID]*PodRequirement{},
		OwnerMatchers:    ownerMatchers,
		ParseError:       err,
	}
}

func (ri *ReservationInfo) GetName() string {
	if ri.Reservation != nil {
		return ri.Reservation.Name
	}
	if ri.Pod != nil {
		return ri.Pod.Name
	}
	return ""
}

func (ri *ReservationInfo) GetNamespace() string {
	if ri.Reservation != nil {
		return ri.Reservation.Namespace
	}
	if ri.Pod != nil {
		return ri.Pod.Namespace
	}
	return ""
}

func (ri *ReservationInfo) UID() types.UID {
	if ri.Reservation != nil {
		return ri.Reservation.UID
	}
	if ri.Pod != nil {
		return ri.Pod.UID
	}
	return ""
}

func (ri *ReservationInfo) GetObject() metav1.Object {
	if ri.Reservation != nil {
		return ri.Reservation
	}
	if ri.Pod != nil {
		return ri.Pod
	}
	return nil
}

func (ri *ReservationInfo) GetReservePod() *corev1.Pod {
	if ri.Pod != nil {
		return ri.Pod
	}
	return nil
}

func (ri *ReservationInfo) GetNodeName() string {
	if ri.Reservation != nil {
		return reservationutil.GetReservationNodeName(ri.Reservation)
	}
	if ri.Pod != nil {
		return ri.Pod.Spec.NodeName
	}
	return ""
}

func (ri *ReservationInfo) IsAllocateOnce() bool {
	if ri.Reservation != nil {
		return apiext.IsReservationAllocateOnce(ri.Reservation)
	}
	if ri.Pod != nil {
		// Reservation Operating Mode Pod MUST BE AllocateOnce
		return true
	}
	return true
}

func (ri *ReservationInfo) GetAllocatePolicy() schedulingv1alpha1.ReservationAllocatePolicy {
	if ri.Reservation != nil {
		return ri.Reservation.Spec.AllocatePolicy
	}
	if ri.Pod != nil && apiext.IsReservationOperatingMode(ri.Pod) {
		return schedulingv1alpha1.ReservationAllocatePolicyAligned
	}
	return schedulingv1alpha1.ReservationAllocatePolicyDefault
}

func (ri *ReservationInfo) GetPriority() int32 {
	if ri.Reservation != nil {
		return reservationutil.PodPriority(ri.Reservation)
	}
	if ri.Pod != nil {
		return corev1helpers.PodPriority(ri.Pod)
	}
	return 0
}

func (ri *ReservationInfo) GetPodOwners() []schedulingv1alpha1.ReservationOwner {
	if ri.Reservation != nil {
		return ri.Reservation.Spec.Owners
	}
	if ri.Pod != nil {
		owners, err := apiext.GetReservationOwners(ri.Pod.Annotations)
		if err != nil {
			klog.ErrorS(err, "Failed to get ReservationOwners from Pod", "pod", klog.KObj(ri.Pod))
			return nil
		}
		return owners
	}
	return nil
}

func (ri *ReservationInfo) Match(pod *corev1.Pod) bool {
	if ri.ParseError != nil {
		return false
	}
	return reservationutil.MatchReservationOwners(pod, ri.OwnerMatchers)
}

func (ri *ReservationInfo) IsAvailable() bool {
	if ri.Reservation != nil {
		return reservationutil.IsReservationAvailable(ri.Reservation)
	}
	if ri.Pod != nil && ri.Pod.Status.Phase == corev1.PodRunning && k8spodutil.IsPodReady(ri.Pod) {
		return true
	}
	return false
}

func (ri *ReservationInfo) IsUnschedulable() bool {
	isUnschedulable := ri.Reservation != nil && ri.Reservation.Spec.Unschedulable
	return isUnschedulable || ri.IsTerminating()
}

func (ri *ReservationInfo) IsTerminating() bool {
	return !ri.GetObject().GetDeletionTimestamp().IsZero()
}

func (ri *ReservationInfo) Clone() *ReservationInfo {
	resourceNames := make([]corev1.ResourceName, 0, len(ri.ResourceNames))
	for _, v := range ri.ResourceNames {
		resourceNames = append(resourceNames, v)
	}

	assignedPods := make(map[types.UID]*PodRequirement, len(ri.AssignedPods))
	for k, v := range ri.AssignedPods {
		assignedPods[k] = v
	}

	return &ReservationInfo{
		Reservation:      ri.Reservation,
		Pod:              ri.Pod,
		ResourceNames:    resourceNames,
		Allocatable:      ri.Allocatable.DeepCopy(),
		Allocated:        ri.Allocated.DeepCopy(),
		AllocatablePorts: util.CloneHostPorts(ri.AllocatablePorts),
		AllocatedPorts:   util.CloneHostPorts(ri.AllocatedPorts),
		AssignedPods:     assignedPods,
	}
}

func (ri *ReservationInfo) UpdateReservation(r *schedulingv1alpha1.Reservation) {
	ri.Reservation = r
	ri.Pod = reservationutil.NewReservePod(r)
	ri.Allocatable = reservationutil.ReservationRequests(r)
	ri.AllocatablePorts = util.RequestedHostPorts(ri.Pod)
	ri.ResourceNames = quotav1.ResourceNames(ri.Allocatable)
	ri.Allocated = quotav1.Mask(ri.Allocated, ri.ResourceNames)
	ownerMatchers, err := reservationutil.ParseReservationOwnerMatchers(r.Spec.Owners)
	if err != nil {
		klog.ErrorS(err, "Failed to parse reservation owner matchers", "reservation", klog.KObj(r))
	}
	ri.OwnerMatchers = ownerMatchers
	ri.ParseError = err
}

func (ri *ReservationInfo) UpdatePod(pod *corev1.Pod) {
	ri.Pod = pod
	ri.Allocatable, _ = resource.PodRequestsAndLimits(pod)
	ri.AllocatablePorts = util.RequestedHostPorts(pod)
	ri.ResourceNames = quotav1.ResourceNames(ri.Allocatable)
	ri.Allocated = quotav1.Mask(ri.Allocated, ri.ResourceNames)

	owners, err := apiext.GetReservationOwners(pod.Annotations)
	if err != nil {
		klog.ErrorS(err, "Invalid reservation owners annotation of Pod", "pod", klog.KObj(pod))
	}
	var ownerMatchers []reservationutil.ReservationOwnerMatcher
	if owners != nil {
		ownerMatchers, err = reservationutil.ParseReservationOwnerMatchers(owners)
		if err != nil {
			klog.ErrorS(err, "Failed to parse reservation owner matchers of pod", "pod", klog.KObj(pod))
		}
	}
	ri.OwnerMatchers = ownerMatchers
	ri.ParseError = err
}

func (ri *ReservationInfo) AddAssignedPod(pod *corev1.Pod) {
	if _, ok := ri.AssignedPods[pod.UID]; ok {
		klog.Warningf("Repeatedly add assigned Pod %v in reservation %v, skip it.", klog.KObj(pod), klog.KObj(ri))
		return
	}
	requirement := NewPodRequirement(pod)
	ri.Allocated = quotav1.Add(ri.Allocated, quotav1.Mask(requirement.Requests, ri.ResourceNames))
	ri.AllocatedPorts = util.AppendHostPorts(ri.AllocatedPorts, requirement.Ports)
	ri.AssignedPods[pod.UID] = requirement
}

func (ri *ReservationInfo) RemoveAssignedPod(pod *corev1.Pod) {
	if requirement, ok := ri.AssignedPods[pod.UID]; ok {
		if len(requirement.Requests) > 0 {
			ri.Allocated = quotav1.SubtractWithNonNegativeResult(ri.Allocated, quotav1.Mask(requirement.Requests, ri.ResourceNames))
		}
		if len(requirement.Ports) > 0 {
			util.RemoveHostPorts(ri.AllocatedPorts, requirement.Ports)
		}

		delete(ri.AssignedPods, pod.UID)
	}
}
