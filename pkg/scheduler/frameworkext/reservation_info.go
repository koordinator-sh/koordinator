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
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	componentresource "k8s.io/component-helpers/resource"
	corev1helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

type ReservationInfo struct {
	Reservation           *schedulingv1alpha1.Reservation
	Pod                   *corev1.Pod
	ResourceNames         []corev1.ResourceName
	Allocatable           corev1.ResourceList // RO
	Allocated             corev1.ResourceList // RO
	Reserved              corev1.ResourceList // reserved inside the reservation
	Available             *framework.Resource // pre-calculated info: Allocatable - Reserved - Allocated
	AllocatedResource     *framework.Resource // pre-calculated info: Allocated
	Non0AllocatedMilliCPU int64               // pre-calculated info: non-zero milli-CPU of Allocated
	Non0AllocatedMem      int64               // pre-calculated info: non-zero Memory of Allocated
	AllocatablePorts      fwktype.HostPortInfo
	AllocatedPorts        fwktype.HostPortInfo
	AssignedPods          map[types.UID]*PodRequirement
	OwnerMatchers         []reservationutil.ReservationOwnerMatcher
	ParseError            error
}

type PodRequirement struct {
	Namespace string
	Name      string
	UID       types.UID
	Requests  corev1.ResourceList
	Ports     fwktype.HostPortInfo
}

func NewPodRequirement(pod *corev1.Pod) *PodRequirement {
	requests := componentresource.PodRequests(pod, componentresource.PodResourcesOptions{})
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
	var parseErrors []error
	allocatable := reservationutil.ReservationRequests(r)
	reserved := util.GetNodeReservationFromAnnotation(r.Annotations)
	resourceNames := quotav1.ResourceNames(allocatable)
	sort.Slice(resourceNames, func(i, j int) bool {
		return resourceNames[i] < resourceNames[j]
	})
	if r.Spec.AllocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyRestricted {
		options, err := apiext.GetReservationRestrictedOptions(r.Annotations)
		if err == nil {
			resourceNames = reservationutil.GetReservationRestrictedResources(resourceNames, options)
		} else {
			parseErrors = append(parseErrors, err)
		}
	}
	reservedPod := reservationutil.NewReservePod(r)

	ownerMatchers, err := reservationutil.ParseReservationOwnerMatchers(r.Spec.Owners)
	if err != nil {
		parseErrors = append(parseErrors, err)
		klog.ErrorS(err, "Failed to parse reservation owner matchers", "reservation", klog.KObj(r))
	}

	var parseError error
	if len(parseErrors) > 0 {
		parseError = utilerrors.NewAggregate(parseErrors)
	}

	return &ReservationInfo{
		Reservation:      r,
		Pod:              reservedPod,
		ResourceNames:    resourceNames,
		Allocatable:      allocatable,
		Reserved:         reserved,
		AllocatablePorts: util.RequestedHostPorts(reservedPod),
		AssignedPods:     map[types.UID]*PodRequirement{},
		OwnerMatchers:    ownerMatchers,
		ParseError:       parseError,
	}
}

func NewReservationInfoFromPod(pod *corev1.Pod) *ReservationInfo {
	var parseErrors []error

	allocatable := componentresource.PodRequests(pod, componentresource.PodResourcesOptions{})
	reserved := util.GetNodeReservationFromAnnotation(pod.Annotations)
	resourceNames := quotav1.ResourceNames(allocatable)
	sort.Slice(resourceNames, func(i, j int) bool {
		return resourceNames[i] < resourceNames[j]
	})
	options, err := apiext.GetReservationRestrictedOptions(pod.Annotations)
	if err == nil {
		resourceNames = reservationutil.GetReservationRestrictedResources(resourceNames, options)
	} else {
		parseErrors = append(parseErrors, err)
	}

	owners, err := apiext.GetReservationOwners(pod.Annotations)
	if err != nil {
		klog.ErrorS(err, "Invalid reservation owners annotation of Pod", "pod", klog.KObj(pod))
	}
	var ownerMatchers []reservationutil.ReservationOwnerMatcher
	if owners != nil {
		ownerMatchers, err = reservationutil.ParseReservationOwnerMatchers(owners)
		if err != nil {
			parseErrors = append(parseErrors, err)
			klog.ErrorS(err, "Failed to parse reservation owner matchers of pod", "pod", klog.KObj(pod))
		}
	}

	var parseError error
	if len(parseErrors) > 0 {
		parseError = utilerrors.NewAggregate(parseErrors)
	}

	return &ReservationInfo{
		Pod:              pod,
		ResourceNames:    resourceNames,
		Allocatable:      allocatable,
		Reserved:         reserved,
		AllocatablePorts: util.RequestedHostPorts(pod),
		AssignedPods:     map[types.UID]*PodRequirement{},
		OwnerMatchers:    ownerMatchers,
		ParseError:       parseError,
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

func (ri *ReservationInfo) GetAllocatedPods() int {
	return len(ri.AssignedPods)
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

func (ri *ReservationInfo) MatchOwners(pod *corev1.Pod) bool {
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

func (ri *ReservationInfo) IsPreAllocation() bool {
	if ri.Reservation != nil {
		return ri.Reservation.Spec.PreAllocation
	}
	if ri.Pod != nil {
		return reservationutil.IsReservePodPreAllocation(ri.Pod)
	}
	return false
}

func (ri *ReservationInfo) GetTaints() []corev1.Taint {
	if ri.Reservation != nil {
		return ri.Reservation.Spec.Taints
	}
	return nil
}

// MatchReservationAffinity returns the statuses of whether the reservation affinity matches, whether the reservation
// taints are tolerated, and whether the reservation name matches.
func (ri *ReservationInfo) MatchReservationAffinity(reservationAffinity *reservationutil.RequiredReservationAffinity, node *corev1.Node, omitNodeLabels bool) bool {
	if reservationAffinity == nil {
		return true
	}
	reservationLabels := ri.GetObject().GetLabels()

	var nodeLabels map[string]string
	if !omitNodeLabels {
		nodeLabels = node.GetLabels()
	}

	if !reservationAffinity.MatchLabelSelector(reservationLabelsOverlay{reservation: reservationLabels, node: nodeLabels}) {
		return false
	}

	if reservationAffinity.HasNodeSelector() {
		fakeNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   ri.GetName(),
				Labels: mergeReservationLabels(reservationLabels, nodeLabels),
			},
		}
		return reservationAffinity.MatchAffinity(fakeNode)
	}
	return true
}

type reservationLabelsOverlay struct {
	reservation map[string]string
	node        map[string]string
}

func (o reservationLabelsOverlay) Has(label string) bool {
	if _, ok := o.reservation[label]; ok {
		return true
	}
	_, ok := o.node[label]
	return ok
}

func (o reservationLabelsOverlay) Get(label string) string {
	if v, ok := o.reservation[label]; ok {
		return v
	}
	return o.node[label]
}

func (o reservationLabelsOverlay) Lookup(label string) (string, bool) {
	if v, ok := o.reservation[label]; ok {
		return v, true
	}
	v, ok := o.node[label]
	return v, ok
}

func mergeReservationLabels(reservationLabels, nodeLabels map[string]string) map[string]string {
	if len(nodeLabels) == 0 {
		return reservationLabels
	}
	merged := make(map[string]string, len(nodeLabels)+len(reservationLabels))
	for k, v := range nodeLabels {
		merged[k] = v
	}
	for k, v := range reservationLabels {
		merged[k] = v
	}
	return merged
}

func (ri *ReservationInfo) MatchExactMatchSpec(podRequests corev1.ResourceList, spec *apiext.ExactMatchReservationSpec) bool {
	return apiext.ExactMatchReservation(podRequests, ri.Allocatable, spec)
}

func (ri *ReservationInfo) FindMatchingUntoleratedTaint(reservationAffinity *reservationutil.RequiredReservationAffinity) (corev1.Taint, bool) {
	return reservationAffinity.FindMatchingUntoleratedTaint(ri.GetTaints(), reservationutil.DoNotScheduleTaintsFilter)
}

func (ri *ReservationInfo) Clone() *ReservationInfo {
	// use a shallow copy to reduce overhead
	return &ReservationInfo{
		Reservation:           ri.Reservation,
		Pod:                   ri.Pod,
		ResourceNames:         ri.ResourceNames,
		Allocatable:           ri.Allocatable,
		Allocated:             ri.Allocated,
		Reserved:              ri.Reserved,
		Available:             ri.Available,
		AllocatedResource:     ri.AllocatedResource,
		Non0AllocatedMilliCPU: ri.Non0AllocatedMilliCPU,
		Non0AllocatedMem:      ri.Non0AllocatedMem,
		AllocatablePorts:      ri.AllocatablePorts,
		AllocatedPorts:        util.CloneHostPorts(ri.AllocatedPorts),
		AssignedPods:          ri.AssignedPods,
		OwnerMatchers:         ri.OwnerMatchers,
		ParseError:            ri.ParseError,
	}
}

func (ri *ReservationInfo) UpdateReservation(r *schedulingv1alpha1.Reservation) {
	ri.Allocatable = reservationutil.ReservationRequests(r)
	var parseErrors []error
	resourceNames := quotav1.ResourceNames(ri.Allocatable)
	if r.Spec.AllocatePolicy == schedulingv1alpha1.ReservationAllocatePolicyRestricted {
		options, err := apiext.GetReservationRestrictedOptions(r.Annotations)
		if err == nil {
			resourceNames = reservationutil.GetReservationRestrictedResources(resourceNames, options)
		} else {
			parseErrors = append(parseErrors, err)
		}
	}
	sort.Slice(resourceNames, func(i, j int) bool {
		return resourceNames[i] < resourceNames[j]
	})
	ri.ResourceNames = resourceNames

	ri.Reservation = r
	ri.Pod = reservationutil.NewReservePod(r)
	ri.AllocatablePorts = util.RequestedHostPorts(ri.Pod)
	if ri.Allocated != nil {
		ri.Allocated = quotav1.Mask(ri.Allocated, ri.ResourceNames)
	}
	reserved := util.GetNodeReservationFromAnnotation(r.Annotations)
	if len(reserved) > 0 {
		reserved = quotav1.Mask(reserved, ri.ResourceNames)
	}
	ri.Reserved = reserved
	ri.RefreshPreCalculated()

	ownerMatchers, err := reservationutil.ParseReservationOwnerMatchers(r.Spec.Owners)
	if err != nil {
		klog.ErrorS(err, "Failed to parse reservation owner matchers", "reservation", klog.KObj(r))
		parseErrors = append(parseErrors, err)
	}
	ri.OwnerMatchers = ownerMatchers

	var parseError error
	if len(parseErrors) > 0 {
		parseError = utilerrors.NewAggregate(parseErrors)
	}
	ri.ParseError = parseError
}

func (ri *ReservationInfo) UpdatePod(pod *corev1.Pod) {
	ri.Allocatable = componentresource.PodRequests(pod, componentresource.PodResourcesOptions{})
	var parseErrors []error
	resourceNames := quotav1.ResourceNames(ri.Allocatable)
	options, err := apiext.GetReservationRestrictedOptions(pod.Annotations)
	if err == nil {
		resourceNames = reservationutil.GetReservationRestrictedResources(resourceNames, options)
	} else {
		parseErrors = append(parseErrors, err)
	}
	sort.Slice(resourceNames, func(i, j int) bool {
		return resourceNames[i] < resourceNames[j]
	})
	ri.ResourceNames = resourceNames

	ri.Pod = pod
	ri.AllocatablePorts = util.RequestedHostPorts(pod)
	ri.Allocated = quotav1.Mask(ri.Allocated, ri.ResourceNames)
	reserved := util.GetNodeReservationFromAnnotation(pod.Annotations)
	if len(reserved) > 0 {
		reserved = quotav1.Mask(reserved, ri.ResourceNames)
	}
	ri.Reserved = reserved
	ri.RefreshPreCalculated()

	owners, err := apiext.GetReservationOwners(pod.Annotations)
	if err != nil {
		klog.ErrorS(err, "Invalid reservation owners annotation of Pod", "pod", klog.KObj(pod))
	}
	var ownerMatchers []reservationutil.ReservationOwnerMatcher
	if owners != nil {
		ownerMatchers, err = reservationutil.ParseReservationOwnerMatchers(owners)
		if err != nil {
			klog.ErrorS(err, "Failed to parse reservation owner matchers of pod", "pod", klog.KObj(pod))
			parseErrors = append(parseErrors, err)
		}
	}
	ri.OwnerMatchers = ownerMatchers

	var parseError error
	if len(parseErrors) > 0 {
		parseError = utilerrors.NewAggregate(parseErrors)
	}
	ri.ParseError = parseError
}

func (ri *ReservationInfo) AddAssignedPod(pod *corev1.Pod) {
	if _, ok := ri.AssignedPods[pod.UID]; ok {
		klog.Warningf("Repeatedly add assigned Pod %v in reservation %v, skip it.", klog.KObj(pod), klog.KObj(ri))
		return
	}
	requirement := NewPodRequirement(pod)
	ri.Allocated = quotav1.Add(ri.Allocated, quotav1.Mask(requirement.Requests, ri.ResourceNames))
	ri.AllocatedPorts = util.AppendHostPorts(ri.AllocatedPorts, requirement.Ports)
	assignedPods := make(map[types.UID]*PodRequirement, len(ri.AssignedPods)+1)
	for k, v := range ri.AssignedPods {
		assignedPods[k] = v
	}
	assignedPods[pod.UID] = requirement
	ri.AssignedPods = assignedPods
	ri.RefreshPreCalculated()
}

func (ri *ReservationInfo) RemoveAssignedPod(pod *corev1.Pod) {
	if requirement, ok := ri.AssignedPods[pod.UID]; ok {
		if len(requirement.Requests) > 0 {
			ri.Allocated = quotav1.SubtractWithNonNegativeResult(ri.Allocated, quotav1.Mask(requirement.Requests, ri.ResourceNames))
			ri.RefreshPreCalculated()
		}
		if len(requirement.Ports) > 0 {
			util.RemoveHostPorts(ri.AllocatedPorts, requirement.Ports)
		}

		assignedPods := make(map[types.UID]*PodRequirement, len(ri.AssignedPods))
		for k, v := range ri.AssignedPods {
			if k != pod.UID {
				assignedPods[k] = v
			}
		}
		ri.AssignedPods = assignedPods
	}
}

func (ri *ReservationInfo) RefreshPreCalculated() {
	// Reservation available = Allocatable - Allocated - InnerReserved
	resources := quotav1.SubtractWithNonNegativeResult(quotav1.Subtract(ri.Allocatable, ri.Allocated), ri.Reserved)
	ri.Available = framework.NewResource(resources)
	allocatedResource := framework.NewResource(ri.Allocated)
	ri.AllocatedResource = allocatedResource
	if ri.Allocated != nil {
		non0CPU := GetNonZeroRequestForResource(corev1.ResourceCPU, &ri.Allocated)
		non0Mem := GetNonZeroRequestForResource(corev1.ResourceMemory, &ri.Allocated)
		ri.Non0AllocatedMilliCPU, ri.Non0AllocatedMem = non0CPU.MilliValue(), non0Mem.Value()
	} else {
		ri.Non0AllocatedMilliCPU, ri.Non0AllocatedMem = 0, 0
	}
}

func (ri *ReservationInfo) GetAvailable() *framework.Resource {
	if ri.Available == nil {
		ri.RefreshPreCalculated()
	}
	return ri.Available
}

func (ri *ReservationInfo) GetAllocatedResource() (*framework.Resource, int64, int64) {
	if ri.AllocatedResource == nil {
		ri.RefreshPreCalculated()
	}
	return ri.AllocatedResource, ri.Non0AllocatedMilliCPU, ri.Non0AllocatedMem
}

// IsMatchable checks if the reservation is available to match any pods.
func (ri *ReservationInfo) IsMatchable() bool {
	// if phase is available
	if ri.Reservation != nil { // reservation/reserve pod
		if !reservationutil.IsReservationAvailable(ri.Reservation) {
			return false
		}
	} else if ri.Pod != nil { // operating pod
		if ri.Pod.Status.Phase != corev1.PodRunning || !k8spodutil.IsPodReady(ri.Pod) {
			return false
		}
	}

	if ri.ParseError != nil {
		return false
	}

	// In this case, the Controller has not yet updated the status of the Reservation to Succeeded,
	// but in fact it can no longer be used for allocation. So it's better to skip first.
	if ri.IsAllocateOnce() && ri.GetAllocatedPods() > 0 {
		return false
	}

	return true
}

// IsMultiplePAPodsEnabled checks if multiple pre-allocated pods are enabled for the reservation.
func (ri *ReservationInfo) IsMultiplePAPodsEnabled() bool {
	if ri == nil {
		return false
	}
	return reservationutil.IsMultiplePAPodsEnabled(ri.Reservation)
}

// GetNonZeroRequestForResource returns the requested values,
// if the resource has undefined request for CPU or memory, it returns a default value.
func GetNonZeroRequestForResource(resourceName corev1.ResourceName, requests *corev1.ResourceList) resource.Quantity {
	if requests == nil {
		return resource.Quantity{}
	}
	switch resourceName {
	case corev1.ResourceCPU:
		// Override if un-set, but not if explicitly set to zero
		if _, found := (*requests)[corev1.ResourceCPU]; !found {
			return *resource.NewMilliQuantity(schedutil.DefaultMilliCPURequest, resource.DecimalSI)
		}
		return requests.Cpu().DeepCopy()
	case corev1.ResourceMemory:
		// Override if un-set, but not if explicitly set to zero
		if _, found := (*requests)[corev1.ResourceMemory]; !found {
			return *resource.NewQuantity(schedutil.DefaultMemoryRequest, resource.DecimalSI)
		}
		return requests.Memory().DeepCopy()
	default:
		quantity, found := (*requests)[resourceName]
		if !found {
			return resource.Quantity{}
		}
		return quantity.DeepCopy()
	}
}
