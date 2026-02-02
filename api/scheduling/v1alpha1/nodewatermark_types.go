package v1alpha1

import (
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// NodeWatermarkSpec defines the desired state of NodeWatermark
type NodeWatermarkSpec struct {
	// Type value is Hot, Normal, idle type
	Type            string           `json:"type,omitempty"`
	WillEvictedPods []WillEvictedPod `json:"willEvictedPods"`
	// HighThresholds defines the target usage threshold of node resources
	HighThresholds v1alpha2.ResourceThresholds `json:"highThresholds,omitempty"`
	// LowThresholds defines the low usage threshold of node resources
	LowThresholds v1alpha2.ResourceThresholds `json:"lowThresholds,omitempty"`
}

type WillEvictedPod struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace"`
}

// NodeWatermarkStatus defines the observed state of NodeWatermark
type NodeWatermarkStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// NodeWatermark is the Schema for the nodewatermarks API
type NodeWatermark struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeWatermarkSpec   `json:"spec,omitempty"`
	Status NodeWatermarkStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NodeWatermarkList contains a list of NodeWatermark
type NodeWatermarkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeWatermark `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeWatermark{}, &NodeWatermarkList{})
}
