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

package transformer

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

var podTransformers = []func(pod *corev1.Pod){
	TransformDeprecatedBatchResources,
	TransformDeprecatedDeviceResources,
}

func InstallPodTransformer(informer cache.SharedIndexInformer) {
	if err := informer.SetTransform(TransformPod); err != nil {
		klog.Fatalf("Failed to SetTransform with pod, err: %v", err)
	}
}

func TransformPod(obj interface{}) (interface{}, error) {
	var pod *corev1.Pod
	switch t := obj.(type) {
	case *corev1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		pod, _ = t.Obj.(*corev1.Pod)
	}
	if pod == nil {
		return obj, nil
	}

	for _, fn := range podTransformers {
		fn(pod)
	}

	if unknown, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		unknown.Obj = pod
		return unknown, nil
	}
	return pod, nil
}

func TransformDeprecatedBatchResources(pod *corev1.Pod) {
	transformDeprecatedResources(pod, apiext.DeprecatedBatchResourcesMapper)
}

func TransformDeprecatedDeviceResources(pod *corev1.Pod) {
	transformDeprecatedResources(pod, apiext.DeprecatedDeviceResourcesMapper)
	allocations, err := apiext.GetDeviceAllocations(pod.Annotations)
	if err != nil {
		klog.ErrorS(err, "Failed to get device allocations from pod", "pod", klog.KObj(pod))
		return
	}
	if len(allocations) != 0 {
		if transformDeviceAllocations(allocations) {
			if err := apiext.SetDeviceAllocations(pod, allocations); err != nil {
				klog.ErrorS(err, "Failed to write back transformed allocations to pod", "pod", klog.KObj(pod))
			}
		}
	}
}

func transformDeviceAllocations(deviceAllocations apiext.DeviceAllocations) bool {
	transformed := false
	for _, allocations := range deviceAllocations {
		for _, v := range allocations {
			if replaceAndEraseWithResourcesMapper(v.Resources, apiext.DeprecatedDeviceResourcesMapper) {
				transformed = true
			}
		}
	}
	return transformed
}

func transformDeprecatedResources(pod *corev1.Pod, resourceNames map[corev1.ResourceName]corev1.ResourceName) {
	for _, containers := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for i := range containers {
			container := &containers[i]
			for from, to := range resourceNames {
				replaceAndEraseResource(container.Resources.Requests, from, to)
				replaceAndEraseResource(container.Resources.Limits, from, to)
			}
		}
	}

	if pod.Spec.Overhead != nil {
		for from, to := range resourceNames {
			replaceAndEraseResource(pod.Spec.Overhead, from, to)
		}
	}
}

func replaceAndEraseResource(resourceList corev1.ResourceList, from, to corev1.ResourceName) bool {
	if to == "" {
		return false
	}
	if _, ok := resourceList[to]; ok {
		return false
	}
	quantity, ok := resourceList[from]
	if ok {
		if from == corev1.ResourceCPU {
			quantity = *resource.NewQuantity(quantity.MilliValue(), resource.DecimalSI)
		}
		resourceList[to] = quantity
		delete(resourceList, from)
		return true
	}
	return false
}

func replaceAndEraseWithResourcesMapper(resList corev1.ResourceList, mapper map[corev1.ResourceName]corev1.ResourceName) bool {
	transformed := false
	for from, to := range mapper {
		if replaceAndEraseResource(resList, from, to) {
			transformed = true
		}
	}
	return transformed
}
