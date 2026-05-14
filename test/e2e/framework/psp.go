/*
Copyright 2022 The Koordinator Authors.
Copyright 2017 The Kubernetes Authors.

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

package framework

import (
	clientset "k8s.io/client-go/kubernetes"
)

// IsPodSecurityPolicyEnabled returns true if PodSecurityPolicy is enabled.
// PodSecurityPolicy was removed in Kubernetes 1.25, so this always returns false.
func IsPodSecurityPolicyEnabled(kubeClient clientset.Interface) bool {
	return false
}

// CreatePrivilegedPSPBinding creates the privileged PSP & role.
// PodSecurityPolicy was removed in Kubernetes 1.25, so this is a no-op.
func CreatePrivilegedPSPBinding(kubeClient clientset.Interface, namespace string) {
	// PodSecurityPolicy removed in Kubernetes 1.25 - no-op
}
