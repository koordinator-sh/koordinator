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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling"
)

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:metadata:annotations="api-approved.kubernetes.io=https://github.com/kubernetes-sigs/scheduler-plugins/pull/52"

// ElasticQuota sets elastic quota restrictions per namespace
type ElasticQuota struct {
	metav1.TypeMeta `json:",inline"`

	// Standard object's metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// ElasticQuotaSpec defines the Min and Max for Quota.
	// +optional
	Spec ElasticQuotaSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`

	// ElasticQuotaStatus defines the observed use.
	// +optional
	Status ElasticQuotaStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// ElasticQuotaSpec defines the Min and Max for Quota.
type ElasticQuotaSpec struct {
	// Min is the set of desired guaranteed limits for each named resource.
	// +optional
	Min v1.ResourceList `json:"min,omitempty" protobuf:"bytes,1,rep,name=min, casttype=ResourceList,castkey=ResourceName"`

	// Max is the set of desired max limits for each named resource. The usage of max is based on the resource configurations of
	// successfully scheduled pods.
	// +optional
	Max v1.ResourceList `json:"max,omitempty" protobuf:"bytes,2,rep,name=max, casttype=ResourceList,castkey=ResourceName"`
}

// ElasticQuotaStatus defines the observed use.
type ElasticQuotaStatus struct {
	// Used is the current observed total usage of the resource in the namespace.
	// +optional
	Used v1.ResourceList `json:"used,omitempty" protobuf:"bytes,1,rep,name=used,casttype=ResourceList,castkey=ResourceName"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ElasticQuotaList is a list of ElasticQuota items.
type ElasticQuotaList struct {
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// Items is a list of ElasticQuota objects.
	Items []ElasticQuota `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// PodGroupPhase is the phase of a pod group at the current time.
type PodGroupPhase string

// These are the valid phase of podGroups.
const (
	// PodGroupPending means the pod group has been accepted by the system, but scheduler can not allocate
	// enough resources to it.
	PodGroupPending PodGroupPhase = "Pending"

	// PodGroupRunning means `spec.minMember` pods of PodGroups has been in running phase.
	PodGroupRunning PodGroupPhase = "Running"

	// PodGroupPreScheduling means all of pods has been are waiting to be scheduled, enqueue waitingPod
	PodGroupPreScheduling PodGroupPhase = "PreScheduling"

	// PodGroupScheduling means some of pods has been scheduling in running phase but have not reach the `spec.
	// minMember` pods of PodGroups.
	PodGroupScheduling PodGroupPhase = "Scheduling"

	// PodGroupScheduled means `spec.minMember` pods of PodGroups have been scheduled finished and pods have been in running
	// phase.
	PodGroupScheduled PodGroupPhase = "Scheduled"

	// PodGroupUnknown means part of `spec.minMember` pods are running but the other part can not
	// be scheduled, e.g. not enough resource; scheduler will wait for related controller to recover it.
	PodGroupUnknown PodGroupPhase = "Unknown"

	// PodGroupFinished means all of `spec.minMember` pods are successfully.
	PodGroupFinished PodGroupPhase = "Finished"

	// PodGroupFailed means at least one of `spec.minMember` pods is failed.
	PodGroupFailed PodGroupPhase = "Failed"

	// PodGroupLabel is the default label of coscheduling
	PodGroupLabel = "pod-group." + scheduling.GroupName
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:metadata:annotations="api-approved.kubernetes.io=https://github.com/kubernetes-sigs/scheduler-plugins/pull/52"

// PodGroup is a collection of Pod; used for batch workload.
type PodGroup struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Specification of the desired behavior of the pod group.
	// +optional
	Spec PodGroupSpec `json:"spec,omitempty"`

	// Status represents the current information about a pod group.
	// This data may not be up to date.
	// +optional
	Status PodGroupStatus `json:"status,omitempty"`
}

// PodGroupSpec represents the template of a pod group.
type PodGroupSpec struct {
	// MinMember defines the minimal number of members/tasks to run the pod group;
	// if there's not enough resources to start all tasks, the scheduler
	// will not start anyone.
	MinMember int32 `json:"minMember,omitempty"`

	// MinResources defines the minimal resource of members/tasks to run the pod group;
	// if there's not enough resources to start all tasks, the scheduler
	// will not start anyone.
	MinResources v1.ResourceList `json:"minResources,omitempty"`

	// ScheduleTimeoutSeconds defines the maximal time of members/tasks to wait before run the pod group;
	ScheduleTimeoutSeconds *int32 `json:"scheduleTimeoutSeconds,omitempty"`
}

// PodGroupStatus represents the current state of a pod group.
type PodGroupStatus struct {
	// Current phase of PodGroup.
	Phase PodGroupPhase `json:"phase,omitempty"`

	// OccupiedBy marks the workload (e.g., deployment, statefulset) UID that occupy the podgroup.
	// It is empty if not initialized.
	OccupiedBy string `json:"occupiedBy,omitempty"`

	// The number of actively running pods.
	// +optional
	Scheduled int32 `json:"scheduled,omitempty"`

	// The number of actively running pods.
	// +optional
	Running int32 `json:"running,omitempty"`

	// The number of pods which reached phase Succeeded.
	// +optional
	Succeeded int32 `json:"succeeded,omitempty"`

	// The number of pods which reached phase Failed.
	// +optional
	Failed int32 `json:"failed,omitempty"`

	// ScheduleStartTime of the group
	ScheduleStartTime metav1.Time `json:"scheduleStartTime,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PodGroupList is a collection of pod groups.
type PodGroupList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of PodGroup
	Items []PodGroup `json:"items"`
}
