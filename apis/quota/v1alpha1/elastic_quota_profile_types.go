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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type ElasticQuotaProfileSpec struct {
	// QuotaName defines the associated quota name of the profile.
	// +required
	QuotaName string `json:"quotaName"`
	// QuotaLabels defines the labels of the quota.
	QuotaLabels map[string]string `json:"quotaLabels,omitempty"`
	// ResourceRatio is a ratio, we will use it to fix the resource fragmentation problem.
	// If the total resource is 100 and the resource ratio is 0.9, the allocable resource is 100*0.9=90
	ResourceRatio *string `json:"resourceRatio,omitempty"`
	// NodeSelector defines a node selector to select nodes.
	// +required
	NodeSelector *metav1.LabelSelector `json:"nodeSelector"`
}

type ElasticQuotaProfileStatus struct {
}

//  ElasticQuotaProfile is the Schema for the ElasticQuotaProfile API
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient
// +kubebuilder:resource:shortName=eqp
// +kubebuilder:object:root=true

type ElasticQuotaProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ElasticQuotaProfileSpec   `json:"spec,omitempty"`
	Status ElasticQuotaProfileStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ElasticQuotaProfileList contains a list of ElasticQuotaProfile
type ElasticQuotaProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ElasticQuotaProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ElasticQuotaProfile{}, &ElasticQuotaProfileList{})
}
