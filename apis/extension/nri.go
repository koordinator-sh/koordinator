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

package extension

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	// AnnotationRequiredPluginsContainerPrefix is the prefix of required plugins annotation key for a container.
	AnnotationRequiredPluginsContainerPrefix = "required-plugins.noderesource.dev/container."

	// DefaultNRIPluginName is the default NRI plugin name of koordlet runtime hooks.
	DefaultNRIPluginName = "koordlet_nri"

	// DefaultRequiredPluginsValue is the default value of required plugins annotation, which is a JSON array of the koordlet NRI plugin name.
	DefaultRequiredPluginsValue = "[\"koordlet_nri\"]"
)

// SetPodRequiredNRIPlugins sets the NRI required plugins annotations for each container and init container of the pod.
func SetPodRequiredNRIPlugins(pod *corev1.Pod) {
	if pod == nil {
		return
	}
	annotations := pod.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	for _, container := range pod.Spec.InitContainers {
		key := AnnotationRequiredPluginsContainerPrefix + container.Name
		annotations[key] = DefaultRequiredPluginsValue
	}
	for _, container := range pod.Spec.Containers {
		key := AnnotationRequiredPluginsContainerPrefix + container.Name
		annotations[key] = DefaultRequiredPluginsValue
	}
	pod.SetAnnotations(annotations)
}
