package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling"
)

// PodGroupPhase is the phase of a pod group at the current time.
type PodGroupPhase string

// These are the valid phase of podGroups.
const (
	// PodGroupPending means the pod group has been accepted by the system, but scheduler can not allocate
	// enough resources to it.
	PodGroupPending PodGroupPhase = "Pending"

	// PodGroupRunning means the `spec.minMember` pods of the pod group are in running phase.
	PodGroupRunning PodGroupPhase = "Running"

	// PodGroupPreScheduling means all pods of the pod group have enqueued and are waiting to be scheduled.
	PodGroupPreScheduling PodGroupPhase = "PreScheduling"

	// PodGroupScheduling means partial pods of the pod group have been scheduled and are in running phase
	// but the number of running pods has not reached the `spec.minMember` pods of PodGroups.
	PodGroupScheduling PodGroupPhase = "Scheduling"

	// PodGroupScheduled means the `spec.minMember` pods of the pod group have been scheduled and are in running phase.
	PodGroupScheduled PodGroupPhase = "Scheduled"

	// PodGroupUnknown means a part of `spec.minMember` pods of the pod group have been scheduled but the others can not
	// be scheduled due to, e.g. not enough resource; scheduler will wait for related controllers to recover them.
	PodGroupUnknown PodGroupPhase = "Unknown"

	// PodGroupFinished means the `spec.minMember` pods of the pod group are successfully finished.
	PodGroupFinished PodGroupPhase = "Finished"

	// PodGroupFailed means at least one of `spec.minMember` pods have failed.
	PodGroupFailed PodGroupPhase = "Failed"

	// PodGroupLabel is the default label of coscheduling
	PodGroupLabel = "pod-group." + scheduling.GroupName
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

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
	//+kubebuilder:validation:Minimum=0
	MinMember int32 `json:"minMember,omitempty"`

	// MinResources defines the minimal resource of members/tasks to run the pod group;
	// if there's not enough resources to start all tasks, the scheduler
	// will not start anyone.
	MinResources *v1.ResourceList `json:"minResources,omitempty"`

	// ScheduleTimeoutSeconds defines the maximal time of members/tasks to wait before run the pod group;
	//+kubebuilder:validation:Minimum=0
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

// +kubebuilder:object:root=true

// PodGroupList is a collection of pod groups.
type PodGroupList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of PodGroup
	Items []PodGroup `json:"items"`
}
