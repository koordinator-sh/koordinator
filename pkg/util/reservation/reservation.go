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
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	corev1helper "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/component-helpers/scheduling/corev1/nodeaffinity"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var (
	// AnnotationReservePod indicates whether the pod is a reserved pod.
	AnnotationReservePod = extension.SchedulingDomainPrefix + "/reserve-pod"
	// AnnotationReservationName indicates the name of the reservation.
	AnnotationReservationName = extension.SchedulingDomainPrefix + "/reservation-name"
	// AnnotationReservationNode indicates the node name if the reservation specifies a node.
	AnnotationReservationNode = extension.SchedulingDomainPrefix + "/reservation-node"
	// AnnotationReservationResizeAllocatable indicates the desired allocatable are to be updated.
	AnnotationReservationResizeAllocatable = extension.SchedulingDomainPrefix + "/reservation-resize-allocatable"
)

// ErrReasonPrefix is the prefix of the reservation-level scheduling errors.
const ErrReasonPrefix = "Reservation(s) "

// NewReservePod returns a fake pod set as the reservation's specifications.
// The reserve pod is only visible for the scheduler and does not make actual creation on nodes.
func NewReservePod(r *schedulingv1alpha1.Reservation) *corev1.Pod {
	reservePod := &corev1.Pod{}
	if r.Spec.Template != nil {
		reservePod.ObjectMeta = *r.Spec.Template.ObjectMeta.DeepCopy()
		reservePod.Spec = *r.Spec.Template.Spec.DeepCopy()
	} else {
		klog.V(4).InfoS("failed to set valid spec for new reserve pod, template is nil", "spec", r.Spec)
	}
	// name, uid: reservation uid
	reservePod.Name = GetReservationKey(r)
	reservePod.UID = r.UID
	if len(reservePod.Namespace) == 0 {
		reservePod.Namespace = corev1.NamespaceDefault
	}

	// labels, annotations: `objectMeta` overwrites `template.objectMeta`
	if reservePod.Labels == nil {
		reservePod.Labels = map[string]string{}
	}
	for k, v := range r.Labels {
		reservePod.Labels[k] = v
	}
	if reservePod.Annotations == nil {
		reservePod.Annotations = map[string]string{}
	}
	for k, v := range r.Annotations {
		reservePod.Annotations[k] = v
	}
	// annotate the reservePod
	reservePod.Annotations[AnnotationReservePod] = "true"
	reservePod.Annotations[AnnotationReservationName] = r.Name // for search inversely

	// annotate node name specified
	if len(reservePod.Spec.NodeName) > 0 {
		// if the reservation specifies a nodeName, annotate it and cleanup spec.nodeName for other plugins not
		// processing the nodeName before binding
		reservePod.Annotations[AnnotationReservationNode] = reservePod.Spec.NodeName
		reservePod.Spec.NodeName = ""
	}
	// use reservation status.nodeName as the real scheduled result
	if nodeName := GetReservationNodeName(r); len(nodeName) > 0 {
		reservePod.Spec.NodeName = nodeName
	}

	if reservePod.Spec.Priority == nil {
		if priorityVal, ok := r.Labels[extension.LabelPodPriority]; ok && priorityVal != "" {
			priority, err := strconv.ParseInt(priorityVal, 10, 32)
			if err == nil {
				reservePod.Spec.Priority = pointer.Int32(int32(priority))
			}
		}
	}

	if reservePod.Spec.Priority == nil {
		// Forces priority to be set to maximum to prevent preemption.
		reservePod.Spec.Priority = pointer.Int32(math.MaxInt32)
	}

	reservePod.Spec.SchedulerName = GetReservationSchedulerName(r)

	if IsReservationSucceeded(r) {
		reservePod.Status.Phase = corev1.PodSucceeded
	} else if IsReservationExpired(r) || IsReservationFailed(r) {
		reservePod.Status.Phase = corev1.PodFailed
	}
	if IsReservationAvailable(r) {
		podRequests := resource.PodRequests(reservePod, resource.PodResourcesOptions{})
		if !quotav1.Equals(podRequests, r.Status.Allocatable) {
			//
			// PodRequests is different from r.Status.Allocatable,
			// which means that the scheduler allocates additional resources during scheduling
			// or Reservation has changed the resource specifications through VPA.
			//
			UpdateReservePodWithAllocatable(reservePod, podRequests, r.Status.Allocatable)
		}
	}
	return reservePod
}

func UpdateReservePodWithAllocatable(reservePod *corev1.Pod, podRequests, allocatable corev1.ResourceList) {
	if podRequests == nil {
		podRequests = resource.PodRequests(reservePod, resource.PodResourcesOptions{})
	} else {
		podRequests = podRequests.DeepCopy()
	}
	for resourceName, quantity := range allocatable {
		podRequests[resourceName] = quantity
	}
	reservePod.Spec.Overhead = nil
	cleanContainerResources(reservePod.Spec.InitContainers)
	cleanContainerResources(reservePod.Spec.Containers)
	reservePod.Spec.Containers = append(reservePod.Spec.Containers, corev1.Container{
		Name: "__internal_fake_container__",
		Resources: corev1.ResourceRequirements{
			Limits:   podRequests.DeepCopy(),
			Requests: podRequests.DeepCopy(),
		},
	})
}

func cleanContainerResources(containers []corev1.Container) {
	for i := range containers {
		c := &containers[i]
		c.Resources = corev1.ResourceRequirements{}
	}
}

func ValidateReservation(r *schedulingv1alpha1.Reservation) error {
	if r == nil {
		return fmt.Errorf("the reservation is nil")
	}
	if r.Spec.Template == nil {
		return fmt.Errorf("the reservation misses the template spec")
	}
	if len(r.Spec.Owners) == 0 {
		return fmt.Errorf("the reservation misses the owner spec")
	}
	if r.Spec.TTL == nil && r.Spec.Expires == nil {
		return fmt.Errorf("the reservation misses the expiration spec")
	}
	return nil
}

func PodPriority(r *schedulingv1alpha1.Reservation) int32 {
	if r.Spec.Template != nil && r.Spec.Template.Spec.Priority != nil {
		return *r.Spec.Template.Spec.Priority
	}
	return 0
}

func IsReservePod(pod *corev1.Pod) bool {
	return pod != nil && pod.Annotations != nil && pod.Annotations[AnnotationReservePod] == "true"
}

func GetReservePodNamespacedName(r *schedulingv1alpha1.Reservation) types.NamespacedName {
	namespacedName := types.NamespacedName{
		Name:      GetReservationKey(r),
		Namespace: corev1.NamespaceDefault,
	}
	if r.Spec.Template != nil && r.Spec.Template.ObjectMeta.Namespace != "" {
		namespacedName.Namespace = r.Spec.Template.ObjectMeta.Namespace
	}
	return namespacedName
}

func GetReservationKey(r *schedulingv1alpha1.Reservation) string {
	return string(r.UID)
}

func GetReservePodNodeName(pod *corev1.Pod) string {
	return pod.Annotations[AnnotationReservationNode]
}

func GetReservationNameFromReservePod(pod *corev1.Pod) string {
	return pod.Annotations[AnnotationReservationName]
}

func GetReservationSchedulerName(r *schedulingv1alpha1.Reservation) string {
	if r == nil || r.Spec.Template == nil || len(r.Spec.Template.Spec.SchedulerName) == 0 {
		return corev1.DefaultSchedulerName
	}
	return r.Spec.Template.Spec.SchedulerName
}

// IsReservationActive checks if the reservation is scheduled and its status is Available/Waiting (active to use).
func IsReservationActive(r *schedulingv1alpha1.Reservation) bool {
	return r != nil && len(GetReservationNodeName(r)) > 0 &&
		(r.Status.Phase == schedulingv1alpha1.ReservationAvailable || r.Status.Phase == schedulingv1alpha1.ReservationWaiting)
}

// IsReservationAvailable checks if the reservation is scheduled on a node and its status is Available.
func IsReservationAvailable(r *schedulingv1alpha1.Reservation) bool {
	return r != nil && len(GetReservationNodeName(r)) > 0 && r.Status.Phase == schedulingv1alpha1.ReservationAvailable
}

func IsReservationSucceeded(r *schedulingv1alpha1.Reservation) bool {
	return r != nil && r.Status.Phase == schedulingv1alpha1.ReservationSucceeded
}

func IsReservationFailed(r *schedulingv1alpha1.Reservation) bool {
	return r != nil && r.Status.Phase == schedulingv1alpha1.ReservationFailed
}

func IsReservationExpired(r *schedulingv1alpha1.Reservation) bool {
	if r == nil || r.Status.Phase != schedulingv1alpha1.ReservationFailed {
		return false
	}
	for _, condition := range r.Status.Conditions {
		if condition.Type == schedulingv1alpha1.ReservationConditionReady {
			return condition.Status == schedulingv1alpha1.ConditionStatusFalse &&
				condition.Reason == schedulingv1alpha1.ReasonReservationExpired
		}
	}
	return false
}

func GetReservationNodeName(r *schedulingv1alpha1.Reservation) string {
	return r.Status.NodeName
}

func SetReservationExpired(r *schedulingv1alpha1.Reservation) {
	r.Status.Phase = schedulingv1alpha1.ReservationFailed
	// not duplicate expired info
	idx := -1
	isReady := false
	for i, condition := range r.Status.Conditions {
		if condition.Type == schedulingv1alpha1.ReservationConditionReady {
			idx = i
			isReady = condition.Status == schedulingv1alpha1.ConditionStatusTrue
		}
	}
	if idx < 0 { // if not set condition
		condition := schedulingv1alpha1.ReservationCondition{
			Type:               schedulingv1alpha1.ReservationConditionReady,
			Status:             schedulingv1alpha1.ConditionStatusFalse,
			Reason:             schedulingv1alpha1.ReasonReservationExpired,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		}
		r.Status.Conditions = append(r.Status.Conditions, condition)
	} else if isReady { // if was ready
		condition := schedulingv1alpha1.ReservationCondition{
			Type:               schedulingv1alpha1.ReservationConditionReady,
			Status:             schedulingv1alpha1.ConditionStatusFalse,
			Reason:             schedulingv1alpha1.ReasonReservationExpired,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		}
		r.Status.Conditions[idx] = condition
	} else { // if already not ready
		r.Status.Conditions[idx].Reason = schedulingv1alpha1.ReasonReservationExpired
		r.Status.Conditions[idx].LastProbeTime = metav1.Now()
	}
}

func SetReservationSucceeded(r *schedulingv1alpha1.Reservation) {
	r.Status.Phase = schedulingv1alpha1.ReservationSucceeded
	idx := -1
	for i, condition := range r.Status.Conditions {
		if condition.Type == schedulingv1alpha1.ReservationConditionReady {
			idx = i
		}
	}
	if idx < 0 { // if not set condition
		condition := schedulingv1alpha1.ReservationCondition{
			Type:               schedulingv1alpha1.ReservationConditionReady,
			Status:             schedulingv1alpha1.ConditionStatusFalse,
			Reason:             schedulingv1alpha1.ReasonReservationSucceeded,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		}
		r.Status.Conditions = append(r.Status.Conditions, condition)
	} else {
		r.Status.Conditions[idx].Status = schedulingv1alpha1.ConditionStatusFalse
		r.Status.Conditions[idx].Reason = schedulingv1alpha1.ReasonReservationSucceeded
		r.Status.Conditions[idx].LastProbeTime = metav1.Now()
	}
}

func SetReservationAvailable(r *schedulingv1alpha1.Reservation, nodeName string) error {
	resizeAllocatable, err := GetReservationResizeAllocatable(r.Annotations)
	if err != nil {
		return err
	}
	requests := ReservationRequests(r)
	for resourceName, quantity := range resizeAllocatable.Resources {
		requests[resourceName] = quantity
	}
	r.Status.Allocatable = requests
	r.Status.NodeName = nodeName
	r.Status.Phase = schedulingv1alpha1.ReservationAvailable

	// initialize the conditions
	r.Status.Conditions = []schedulingv1alpha1.ReservationCondition{
		{
			Type:               schedulingv1alpha1.ReservationConditionScheduled,
			Status:             schedulingv1alpha1.ConditionStatusTrue,
			Reason:             schedulingv1alpha1.ReasonReservationScheduled,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               schedulingv1alpha1.ReservationConditionReady,
			Status:             schedulingv1alpha1.ConditionStatusTrue,
			Reason:             schedulingv1alpha1.ReasonReservationAvailable,
			LastProbeTime:      metav1.Now(),
			LastTransitionTime: metav1.Now(),
		},
	}
	return nil
}

func ReservationRequests(r *schedulingv1alpha1.Reservation) corev1.ResourceList {
	if IsReservationAvailable(r) {
		return r.Status.Allocatable.DeepCopy()
	}
	if r.Spec.Template != nil {
		requests := resource.PodRequests(&corev1.Pod{
			Spec: r.Spec.Template.Spec,
		}, resource.PodResourcesOptions{})
		return requests
	}
	return nil
}

func ReservePorts(r *schedulingv1alpha1.Reservation) framework.HostPortInfo {
	portInfo := framework.HostPortInfo{}
	for _, container := range r.Spec.Template.Spec.Containers {
		for _, podPort := range container.Ports {
			portInfo.Add(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
		}
	}
	return portInfo
}

type ReservationOwnerMatcher struct {
	schedulingv1alpha1.ReservationOwner
	Selector labels.Selector
}

func ParseReservationOwnerMatchers(owners []schedulingv1alpha1.ReservationOwner) ([]ReservationOwnerMatcher, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	var errs field.ErrorList
	ownerMatchers := make([]ReservationOwnerMatcher, 0, len(owners))
	for i, v := range owners {
		var selector labels.Selector
		if v.LabelSelector != nil {
			var err error
			selector, err = util.GetFastLabelSelector(v.LabelSelector)
			if err != nil {
				errs = append(errs, field.Invalid(field.NewPath("owners").Index(i), v.LabelSelector, err.Error()))
				continue
			}
		}
		ownerMatchers = append(ownerMatchers, ReservationOwnerMatcher{
			ReservationOwner: v,
			Selector:         selector,
		})
	}
	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return ownerMatchers, nil
}

func (m *ReservationOwnerMatcher) Match(pod *corev1.Pod) bool {
	if MatchObjectRef(pod, m.Object) &&
		MatchReservationControllerReference(pod, m.Controller) &&
		(m.Selector == nil || m.Selector.Matches(labels.Set(pod.Labels))) {
		return true
	}
	return false
}

// MatchReservationOwners checks if the scheduling pod matches the reservation's owner spec.
// `reservation.spec.owners` defines the DNF (disjunctive normal form) of ObjectReference, ControllerReference
// (extended), LabelSelector, which means multiple selectors are firstly ANDed and secondly ORed.
func MatchReservationOwners(pod *corev1.Pod, matchers []ReservationOwnerMatcher) bool {
	// assert pod != nil && r != nil
	// Owners == nil matches nothing, while Owners = [{}] matches everything
	for _, m := range matchers {
		if m.Match(pod) {
			return true
		}
	}
	return false
}

func MatchObjectRef(pod *corev1.Pod, objRef *corev1.ObjectReference) bool {
	// `ResourceVersion`, `FieldPath` are ignored.
	// since only pod type are compared, `Kind` field is also ignored.
	return objRef == nil ||
		(len(objRef.UID) == 0 || pod.UID == objRef.UID) &&
			(len(objRef.Name) == 0 || pod.Name == objRef.Name) &&
			(len(objRef.Namespace) == 0 || pod.Namespace == objRef.Namespace) &&
			(len(objRef.APIVersion) == 0 || pod.APIVersion == objRef.APIVersion)
}

func MatchReservationControllerReference(pod *corev1.Pod, controllerRef *schedulingv1alpha1.ReservationControllerReference) bool {
	// controllerRef matched if any of pod owner references matches the controllerRef;
	// typically a pod has only one controllerRef
	if controllerRef == nil {
		return true
	}
	if len(controllerRef.Namespace) > 0 && controllerRef.Namespace != pod.Namespace { // namespace field is extended
		return false
	}
	// currently `BlockOwnerDeletion` is ignored
	for _, podOwner := range pod.OwnerReferences {
		if (controllerRef.Controller == nil || podOwner.Controller != nil && *controllerRef.Controller == *podOwner.Controller) &&
			(len(controllerRef.UID) == 0 || controllerRef.UID == podOwner.UID) &&
			(len(controllerRef.Name) == 0 || controllerRef.Name == podOwner.Name) &&
			(len(controllerRef.Kind) == 0 || controllerRef.Kind == podOwner.Kind) &&
			(len(controllerRef.APIVersion) == 0 || controllerRef.APIVersion == podOwner.APIVersion) {
			return true
		}
	}
	return false
}

type RequiredReservationAffinity struct {
	labelSelector labels.Selector
	nodeSelector  *nodeaffinity.NodeSelector
	tolerations   []corev1.Toleration
	name          string
}

// GetRequiredReservationAffinity returns the parsing result of pod's nodeSelector and nodeAffinity.
func GetRequiredReservationAffinity(pod *corev1.Pod) (*RequiredReservationAffinity, error) {
	reservationAffinity, err := extension.GetReservationAffinity(pod.Annotations)
	if err != nil {
		return nil, err
	}
	if reservationAffinity == nil {
		return nil, nil
	}
	var selector labels.Selector
	if len(reservationAffinity.ReservationSelector) > 0 {
		selector = labels.SelectorFromSet(reservationAffinity.ReservationSelector)
	}
	var affinity *nodeaffinity.NodeSelector
	if reservationAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		nodeSelector := &corev1.NodeSelector{
			NodeSelectorTerms: reservationAffinity.RequiredDuringSchedulingIgnoredDuringExecution.ReservationSelectorTerms,
		}
		affinity, err = nodeaffinity.NewNodeSelector(nodeSelector)
		if err != nil {
			return nil, err
		}
	}
	return &RequiredReservationAffinity{
		labelSelector: selector,
		nodeSelector:  affinity,
		tolerations:   reservationAffinity.Tolerations,
		name:          reservationAffinity.Name,
	}, nil
}

// GetName returns the reservation name if it is specified in the reservation affinity.
func (s *RequiredReservationAffinity) GetName() string {
	if s == nil {
		return ""
	}
	return s.name
}

// MatchName checks if the reservation affinity specifies a reservation name, and it matches the given reservation's.
func (s *RequiredReservationAffinity) MatchName(reservationName string) bool {
	return s != nil && len(s.name) > 0 && s.name == reservationName
}

// Match checks whether the pod is schedulable onto nodes according to
// the requirements in both nodeSelector and nodeAffinity.
// DEPRECATED: use MatchAffinity instead.
func (s *RequiredReservationAffinity) Match(node *corev1.Node) bool {
	return s.MatchAffinity(node)
}

// MatchAffinity checks whether the pod is schedulable onto nodes according to
// the requirements in both nodeSelector and nodeAffinity.
func (s *RequiredReservationAffinity) MatchAffinity(node *corev1.Node) bool {
	if s == nil {
		return true
	}
	if s.labelSelector != nil {
		if !s.labelSelector.Matches(labels.Set(node.Labels)) {
			return false
		}
	}
	if s.nodeSelector != nil {
		return s.nodeSelector.Match(node)
	}
	return true
}

// FindMatchingUntoleratedTaint checks if the reservation tolerations tolerates all the filtered reservation taints.
// It returns the first taint without a toleration and the status if any taint is NOT tolerated.
func (s *RequiredReservationAffinity) FindMatchingUntoleratedTaint(taints []corev1.Taint, inclusionFilter func(*corev1.Taint) bool) (corev1.Taint, bool) {
	if s == nil {
		return corev1.Taint{}, false
	}
	return corev1helper.FindMatchingUntoleratedTaint(taints, s.tolerations, inclusionFilter)
}

type ReservationResizeAllocatable struct {
	Resources corev1.ResourceList `json:"resources,omitempty"`
}

func GetReservationResizeAllocatable(annotations map[string]string) (*ReservationResizeAllocatable, error) {
	var allocatable ReservationResizeAllocatable
	if s := annotations[AnnotationReservationResizeAllocatable]; s != "" {
		if err := json.Unmarshal([]byte(s), &allocatable); err != nil {
			return nil, err
		}
	}
	return &allocatable, nil
}

func SetReservationResizeAllocatable(obj metav1.Object, resizeAllocatable *ReservationResizeAllocatable) error {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	data, err := json.Marshal(resizeAllocatable)
	if err != nil {
		return err
	}
	annotations[AnnotationReservationResizeAllocatable] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

// UpdateReservationResizeAllocatable replaces or add the resources to the resizeAllocatable.
func UpdateReservationResizeAllocatable(obj metav1.Object, resources corev1.ResourceList) error {
	resizeAllocatable, err := GetReservationResizeAllocatable(obj.GetAnnotations())
	if err != nil {
		return err
	}
	if resizeAllocatable.Resources == nil {
		resizeAllocatable.Resources = corev1.ResourceList{}
	}
	for resourceName, quantity := range resources {
		resizeAllocatable.Resources[resourceName] = quantity
	}
	return SetReservationResizeAllocatable(obj, resizeAllocatable)
}

func GetReservationRestrictedResources(allocatableResources []corev1.ResourceName, options *extension.ReservationRestrictedOptions) []corev1.ResourceName {
	if options == nil {
		return allocatableResources
	}
	result := make([]corev1.ResourceName, 0, len(allocatableResources))
	for _, resourceName := range allocatableResources {
		for _, v := range options.Resources {
			if resourceName == v {
				result = append(result, resourceName)
				break
			}
		}
	}
	if len(result) == 0 {
		result = allocatableResources
	}
	return result
}

// NewReservationReason creates a reservation-level error reason with the given message.
func NewReservationReason(fmtMsg string, args ...interface{}) string {
	if len(args) <= 0 {
		return ErrReasonPrefix + fmtMsg
	}
	return fmt.Sprintf(ErrReasonPrefix+fmtMsg, args...)
}

// IsReservationReason checks if the error reason is at the reservation-level.
func IsReservationReason(reason string) bool {
	return strings.HasPrefix(reason, ErrReasonPrefix)
}

// DoNotScheduleTaintsFilter can filter out the node taints that reject scheduling Pod on a Node.
func DoNotScheduleTaintsFilter(t *corev1.Taint) bool {
	// PodToleratesNodeTaints is only interested in NoSchedule and NoExecute taints.
	return t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute
}
