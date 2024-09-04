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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type ReservationSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Template defines the scheduling requirements (resources, affinities, images, ...) processed by the scheduler just
	// like a normal pod.
	// If the `template.spec.nodeName` is specified, the scheduler will not choose another node but reserve resources on
	// the specified node.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Required
	Template *corev1.PodTemplateSpec `json:"template" protobuf:"bytes,1,opt,name=template"`
	// Specify the owners who can allocate the reserved resources.
	// Multiple owner selectors and ORed.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Owners []ReservationOwner `json:"owners" protobuf:"bytes,2,rep,name=owners"`
	// Time-to-Live period for the reservation.
	// `expires` and `ttl` are mutually exclusive. Defaults to 24h. Set 0 to disable expiration.
	// +kubebuilder:default="24h"
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty" protobuf:"bytes,3,opt,name=ttl"`
	// Expired timestamp when the reservation is expected to expire.
	// If both `expires` and `ttl` are set, `expires` is checked first.
	// `expires` and `ttl` are mutually exclusive. Defaults to being set dynamically at runtime based on the `ttl`.
	// +optional
	Expires *metav1.Time `json:"expires,omitempty" protobuf:"bytes,4,opt,name=expires"`
	// By default, the resources requirements of reservation (specified in `template.spec`) is filtered by whether the
	// node has sufficient free resources (i.e. Reservation Request <  Node Free).
	// When `preAllocation` is set, the scheduler will skip this validation and allow overcommitment. The scheduled
	// reservation would be waiting to be available until free resources are sufficient.
	// +optional
	PreAllocation bool `json:"preAllocation,omitempty" protobuf:"varint,5,opt,name=preAllocation"`
	// When `AllocateOnce` is set, the reserved resources are only available for the first owner who allocates successfully
	// and are not allocatable to other owners anymore. Defaults to true.
	// +kubebuilder:default=true
	// +optional
	AllocateOnce *bool `json:"allocateOnce,omitempty" protobuf:"varint,6,opt,name=allocateOnce"`
	// AllocatePolicy represents the allocation policy of reserved resources that Reservation expects.
	// +kubebuilder:validation:Enum=Aligned;Restricted
	// +optional
	AllocatePolicy ReservationAllocatePolicy `json:"allocatePolicy,omitempty" protobuf:"bytes,7,opt,name=allocatePolicy,casttype=ReservationAllocatePolicy"`
	// Unschedulable controls reservation schedulability of new pods. By default, reservation is schedulable.
	// +optional
	Unschedulable bool `json:"unschedulable,omitempty" protobuf:"varint,8,opt,name=unschedulable"`
	// Specifies the reservation's taints. This can be toleranted by the reservation tolerance.
	// Eviction is not supported for NoExecute taints
	// +optional
	Taints []corev1.Taint `json:"taints,omitempty" protobuf:"bytes,9,rep,name=taints"`
}

type ReservationAllocatePolicy string

const (
	// ReservationAllocatePolicyDefault means that there is no restriction on the policy of reserved resources,
	// and allocated from the Reservation first, and if it is insufficient, it is allocated from the node.
	// DEPRECATED: ReservationAllocatePolicyDefault is deprecated, it is considered as Aligned if specified.
	// Please try other polices or set LabelReservationIgnored instead.
	ReservationAllocatePolicyDefault ReservationAllocatePolicy = ""
	// ReservationAllocatePolicyAligned indicates that the Pod allocates resources from the Reservation first.
	// If the remaining resources of the Reservation are insufficient, it can be allocated from the node,
	// but it is required to strictly follow the resource specifications of the Pod.
	// This can be used to avoid the problem that a Pod uses multiple Reservations at the same time.
	ReservationAllocatePolicyAligned ReservationAllocatePolicy = "Aligned"
	// ReservationAllocatePolicyRestricted means that the resources
	// requested by the Pod overlap with the resources reserved by the Reservation,
	// then these intersection resources can only be allocated from the Reservation,
	// but resources declared in Pods but not reserved in Reservations can be allocated from Nodes.
	// ReservationAllocatePolicyRestricted includes the semantics of ReservationAllocatePolicyAligned.
	ReservationAllocatePolicyRestricted ReservationAllocatePolicy = "Restricted"
)

// ReservationTemplateSpec describes the data a Reservation should have when created from a template
type ReservationTemplateSpec struct {
	// Standard object's metadata.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// Specification of the desired behavior of the Reservation.
	// +optional
	Spec ReservationSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
}

type ReservationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// The `phase` indicates whether is reservation is waiting for process, available to allocate or failed/expired to
	// get cleanup.
	// +optional
	Phase ReservationPhase `json:"phase,omitempty" protobuf:"bytes,1,opt,name=phase,casttype=ReservationPhase"`
	// The `conditions` indicate the messages of reason why the reservation is still pending.
	// +optional
	Conditions []ReservationCondition `json:"conditions,omitempty" protobuf:"bytes,2,rep,name=conditions"`
	// Current resource owners which allocated the reservation resources.
	// +optional
	CurrentOwners []corev1.ObjectReference `json:"currentOwners,omitempty" protobuf:"bytes,3,rep,name=currentOwners"`
	// Name of node the reservation is scheduled on.
	// +optional
	NodeName string `json:"nodeName,omitempty" protobuf:"bytes,4,opt,name=nodeName"`
	// Resource reserved and allocatable for owners.
	// +optional
	Allocatable corev1.ResourceList `json:"allocatable,omitempty" protobuf:"bytes,5,rep,name=allocatable,casttype=k8s.io/api/core/v1.ResourceList,castkey=k8s.io/api/core/v1.ResourceName"`
	// Resource allocated by current owners.
	// +optional
	Allocated corev1.ResourceList `json:"allocated,omitempty" protobuf:"bytes,6,rep,name=allocated,casttype=k8s.io/api/core/v1.ResourceList,castkey=k8s.io/api/core/v1.ResourceName"`
}

// ReservationOwner indicates the owner specification which can allocate reserved resources.
// +kubebuilder:validation:MinProperties=1
type ReservationOwner struct {
	// Multiple field selectors are ANDed.
	// +optional
	Object *corev1.ObjectReference `json:"object,omitempty" protobuf:"bytes,1,opt,name=object"`
	// +optional
	Controller *ReservationControllerReference `json:"controller,omitempty" protobuf:"bytes,2,opt,name=controller"`
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty" protobuf:"bytes,3,opt,name=labelSelector"`
}

type ReservationControllerReference struct {
	// Extend with a `namespace` field for reference different namespaces.
	metav1.OwnerReference `json:",inline" protobuf:"bytes,1,opt,name=ownerReference"`
	Namespace             string `json:"namespace,omitempty" protobuf:"bytes,2,opt,name=namespace"`
}

type ReservationPhase string

const (
	// ReservationPending indicates the Reservation has not been processed by the scheduler or is unschedulable for
	// some reasons (e.g. the resource requirements cannot get satisfied).
	ReservationPending ReservationPhase = "Pending"
	// ReservationAvailable indicates the Reservation is both scheduled and available for allocation.
	ReservationAvailable ReservationPhase = "Available"
	// ReservationSucceeded indicates the Reservation is scheduled and allocated for a owner, but not allocatable anymore.
	ReservationSucceeded ReservationPhase = "Succeeded"
	// ReservationWaiting indicates the Reservation is scheduled, but the resources to reserve are not ready for
	// allocation (e.g. in pre-allocation for running pods).
	ReservationWaiting ReservationPhase = "Waiting"
	// ReservationFailed indicates the Reservation is failed to reserve resources, due to expiration or marked as
	// unavailable, which the object is not available to allocate and will get cleaned in the future.
	ReservationFailed ReservationPhase = "Failed"
)

type ReservationConditionType string

const (
	ReservationConditionScheduled ReservationConditionType = "Scheduled"
	ReservationConditionReady     ReservationConditionType = "Ready"
)

type ConditionStatus string

const (
	ConditionStatusTrue    ConditionStatus = "True"
	ConditionStatusFalse   ConditionStatus = "False"
	ConditionStatusUnknown ConditionStatus = "Unknown"
)

const (
	ReasonReservationScheduled     = "Scheduled"
	ReasonReservationUnschedulable = "Unschedulable"

	ReasonReservationAvailable = "Available"
	ReasonReservationSucceeded = "Succeeded"
	ReasonReservationExpired   = "Expired"
)

type ReservationCondition struct {
	Type               ReservationConditionType `json:"type,omitempty" protobuf:"bytes,1,opt,name=type,casttype=ReservationConditionType"`
	Status             ConditionStatus          `json:"status,omitempty" protobuf:"bytes,2,opt,name=status,casttype=ConditionStatus"`
	Reason             string                   `json:"reason,omitempty" protobuf:"bytes,3,opt,name=reason"`
	Message            string                   `json:"message,omitempty" protobuf:"bytes,4,opt,name=message"`
	LastProbeTime      metav1.Time              `json:"lastProbeTime,omitempty" protobuf:"bytes,5,opt,name=lastProbeTime"`
	LastTransitionTime metav1.Time              `json:"lastTransitionTime,omitempty" protobuf:"bytes,6,opt,name=lastTransitionTime"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="The phase of reservation"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Node",type="string",JSONPath=".status.nodeName"
// +kubebuilder:printcolumn:name="TTL",type="string",JSONPath=".spec.ttl"
// +kubebuilder:printcolumn:name="Expires",type="string",JSONPath=".spec.expires"

// Reservation is the Schema for the reservation API.
// A Reservation object is non-namespaced.
// Any namespaced affinity/anti-affinity of reservation scheduling can be specified in the spec.template.
type Reservation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   ReservationSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status ReservationStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +kubebuilder:object:root=true

// ReservationList contains a list of Reservation
type ReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []Reservation `json:"items" protobuf:"bytes,2,rep,name=items"`
}

func init() {
	SchemeBuilder.Register(&Reservation{}, &ReservationList{})
}
