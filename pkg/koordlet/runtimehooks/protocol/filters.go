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

package protocol

import corev1 "k8s.io/api/core/v1"

// NOTE: functions in this file can be overwritten for extension

const (
	runtimeClassKata = "kata"
)

func ContainerReconcileIgnoreFilter(pod *corev1.Pod, container *corev1.Container, containerStat *corev1.ContainerStatus) bool {
	// for containers in kata pod, no need to reconcile container cgroup
	// TODO define filters as runtime hook plugin level
	if pod.Spec.RuntimeClassName != nil && *pod.Spec.RuntimeClassName == runtimeClassKata {
		return true
	}
	return false
}
