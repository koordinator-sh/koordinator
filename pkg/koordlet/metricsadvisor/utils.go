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

package metricsadvisor

import (
	corev1 "k8s.io/api/core/v1"
)

// NOTE: functions in this file can be overwritten for extension

// getRuntimeClassName returns RuntimeClassName which refers to a RuntimeClass object in
// the node.k8s.io group, which should be used to run this pod.
func getRuntimeClassName(pod *corev1.Pod) *string {
	return pod.Spec.RuntimeClassName
}

// TODO: We're developing several collector plugins to support multiple container runtime.
// In the meanwhile, ignoreNonRuncPod will always return false.
func ignoreNonRuncPod(pod *corev1.Pod) bool {
	return false
}
