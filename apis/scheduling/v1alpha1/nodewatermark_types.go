package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type (
	Percentage         float64
	ResourceThresholds map[corev1.ResourceName]Percentage
)

// NodeWatermarkSpec defines the desired state of NodeWatermark
type NodeWatermarkSpec struct {
	// Type value is Hot, Normal, idle type
	Type string `json:"type,omitempty"`

	WillEvictedPods []WillEvictedPod `json:"willEvictedPods,omitempty"`
	// HighThresholds defines the target usage threshold of node resources
	HighThresholds ResourceThresholds `json:"highThresholds,omitempty"`
	// LowThresholds defines the low usage threshold of node resources
	LowThresholds ResourceThresholds `json:"lowThresholds,omitempty"`
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

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="nodeType",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="nodeHighThresholds",type=string,JSONPath=`.spec.highThresholds`
// +kubebuilder:printcolumn:name="nodeLowThresholds",type=string,JSONPath=`.spec.lowThresholds`

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
