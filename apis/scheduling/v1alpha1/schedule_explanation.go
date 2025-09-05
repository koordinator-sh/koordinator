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

type ScheduleExplanationSpec struct {
	/*
		QuestionObjectReference is the object that needs to be questioned. Currently, Reservation and Pod is supported.
		To query the PodGroup or GangGroup, you can simply pass any of its member Pod here
	*/
	QuestionObjectReference *corev1.ObjectReference `json:"questionObjectReference,omitempty"`

	// QuestionObjectTemplate defines the questioned pod template.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Schemaless
	QuestionObjectTemplate *corev1.PodTemplateSpec `json:"questionObjectTemplate,omitempty"`
}

type ScheduleExplanationStatus struct {
	// Schedulable indicates whether the QuestionObject can be scheduled.
	Schedulable bool `json:"schedulable,omitempty"`

	// FailedMessage indicates the reason for the QuestionObject can't be scheduled.
	FailedMessage string `json:"failedMessage,omitempty"`

	// DetailedExplanation indicates the detailed explanation in each topology domain.
	DetailedExplanation []*TopologyDomainLevelExplanation `json:"detailedExplanation,omitempty"`
}

type TopologyDomainLevelExplanation struct {
	// TopologyDomain indicates the topology domain.
	TopologyDomain string `json:"topologyDomain,omitempty"`

	// Schedulable indicates whether the QuestionObject can be scheduled.
	Schedulable bool `json:"schedulable,omitempty"`

	// FailedMessage indicates the reason for the QuestionObject can't be scheduled in this topology domain.
	FailedMessage string `json:"failedMessage,omitempty"`

	ScheduleExplanation NodeLevelExplanations `json:"scheduleExplanation,omitempty"`

	// NodePossibleVictims indicates the possible victims for members that can't be scheduled.
	NodePossibleVictims []NodePossibleVictim `json:"nodePossibleVictims,omitempty"`

	PreemptExplanation NodeLevelExplanations `json:"preemptExplanation,omitempty"`
}

type NodeLevelExplanations struct {
	// Schedulable indicates whether the QuestionObject can be scheduled.
	Schedulable bool `json:"schedulable,omitempty"`

	// FailedMessage indicates the reason for the QuestionObject can't be scheduled in this topology domain.
	FailedMessage string `json:"failedMessage,omitempty"`

	/*
		FeasibleSchedulingResult indicates the possible scheduling results.
		If the QuestionObject can be scheduled, it returns the scheduling result for members that can be partially scheduled.
	*/
	FeasibleSchedulingResult []SchedulingResult `json:"feasibleSchedulingResult,omitempty"`

	// NodeFailedDetails If the QuestionObject cannot be scheduled, provide the reason for each node.
	NodeFailedDetails []NodeFailedDetail `json:"nodeFailedDetails,omitempty"`
}

type NamespacedName struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	UID       string `json:"uid,omitempty"`
}

type SchedulingResult struct {
	NamespacedName `json:",inline"`
	NodeName       string `json:"nodeName,omitempty"`
}

type NodeFailedDetail struct {
	NodeName         string           `json:"nodeName,omitempty"`
	FailedPlugin     string           `json:"failedPlugin,omitempty"`
	Reason           string           `json:"reason,omitempty"`
	NominatedPods    []NamespacedName `json:"nominatedPods,omitempty"`
	PreemptMightHelp bool             `json:"preemptMightHelp,omitempty"`
}

type NodePossibleVictim struct {
	NodeName        string           `json:"nodeName,omitempty"`
	PossibleVictims []PossibleVictim `json:"possibleVictims,omitempty"`
}

type PossibleVictim struct {
	NamespacedName `json:",inline"`
	IsNominatedPod bool `json:"isNominatedPod,omitempty"`
}

// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient
// +kubebuilder:resource:shortName=sched-exp
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

type ScheduleExplanation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   ScheduleExplanationSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status ScheduleExplanationStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +kubebuilder:object:root=true

type ScheduleExplanationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []ScheduleExplanation `json:"items" protobuf:"bytes,2,rep,name=items"`
}

func init() {
	SchemeBuilder.Register(&ScheduleExplanation{}, &ScheduleExplanationList{})
}
