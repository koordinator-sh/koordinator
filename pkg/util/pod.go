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

package util

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func GetEmptyPodExtendedResources() *apiext.ExtendedResourceSpec {
	return &apiext.ExtendedResourceSpec{
		Containers: map[string]apiext.ExtendedResourceContainerSpec{},
	}
}

func GetPodExtendedResources(pod *corev1.Pod) *apiext.ExtendedResourceSpec {
	return GetPodTargetExtendedResources(pod, ExtendedResourceNames...)
}

// GetPodTargetExtendedResources gets the resource requirements of a pod with given extended resources.
// It returns nil if pod specifies no extended resource.
func GetPodTargetExtendedResources(pod *corev1.Pod, resourceNames ...corev1.ResourceName) *apiext.ExtendedResourceSpec {
	if pod == nil {
		return nil
	}

	extendedResources := GetEmptyPodExtendedResources()

	// TODO: count init containers and pod overhead
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		r := GetContainerTargetExtendedResources(container, resourceNames...)
		if r == nil {
			continue
		}
		extendedResources.Containers[container.Name] = *r
	}

	if len(extendedResources.Containers) <= 0 {
		return nil
	}

	return extendedResources
}

func GetPodKey(pod *corev1.Pod) string {
	return fmt.Sprintf("%v/%v", pod.GetNamespace(), pod.GetName())
}

func GetPodMetricKey(podMetric *slov1alpha1.PodMetricInfo) string {
	return fmt.Sprintf("%v/%v", podMetric.Namespace, podMetric.Name)
}

func IsPodTerminated(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

// IsPodInactive returns true if the pod is not in Pending or Running phase.
// Pods in other states (Succeeded, Failed, Unknown) typically do not have any running containers
// and therefore do not have an associated cgroup directory.
//
// Note: A PodPhase of Pending does NOT mean the pod has not been scheduled.
// It may have already been assigned to this node, but its regular containers
// have not started yet (e.g., still running initContainers).
//
// Also, podMetas are retrieved from Kubelet's /pods endpoint, which only returns
// Pods that have been successfully scheduled to this node.
func IsPodInactive(pod *corev1.Pod) bool {
	return pod == nil ||
		(pod.Status.Phase != corev1.PodPending && pod.Status.Phase != corev1.PodRunning)
}

func GetCPUSetFromPod(podAnnotations map[string]string) (string, error) {
	if podAnnotations == nil {
		return "", nil
	}
	podAlloc, err := apiext.GetResourceStatus(podAnnotations)
	if err != nil {
		return "", err
	}
	return podAlloc.CPUSet, nil
}
