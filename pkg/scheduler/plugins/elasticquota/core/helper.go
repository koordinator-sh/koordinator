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

package core

import (
	corev1 "k8s.io/api/core/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	apiresource "k8s.io/kubernetes/pkg/api/v1/resource"

	"github.com/koordinator-sh/koordinator/pkg/features"
)

func PodRequests(pod *corev1.Pod) (reqs corev1.ResourceList) {
	if k8sfeature.DefaultFeatureGate.Enabled(features.ElasticQuotaIgnorePodOverhead) {
		return apiresource.PodRequests(pod, apiresource.PodResourcesOptions{
			ExcludeOverhead: true,
		})
	}
	return apiresource.PodRequests(pod, apiresource.PodResourcesOptions{})
}
