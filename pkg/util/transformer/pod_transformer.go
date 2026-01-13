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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

var podTransformers = []func(pod *corev1.Pod){
	TransformDeprecatedBatchResources,
	TransformDeprecatedDeviceResources,
	TransformReplaceResources,
}

var podTransformerFactories = []func() func(pod *corev1.Pod){
	TransformKoordPriorityClassFunc,
	TransformKoordPreemptionPolicyFunc,
	TransformSchedulerName,
	TransformScheduleExplanationObjectKey,
}

func InstallPodTransformer(informer cache.SharedIndexInformer) {
	transformHandler := TransformPodFactory()
	if err := informer.SetTransform(transformHandler); err != nil {
		klog.Fatalf("Failed to SetTransform with pod, err: %v", err)
	}
}

func TransformPodFactory() cache.TransformFunc {
	var podTransformerFns []func(pod *corev1.Pod)
	for _, fn := range podTransformers {
		podTransformerFns = append(podTransformerFns, fn)
	}
	for _, factoryFn := range podTransformerFactories {
		fn := factoryFn()
		if fn == nil {
			continue
		}
		podTransformerFns = append(podTransformerFns, fn)
	}

	return func(obj interface{}) (interface{}, error) {
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

		for _, fn := range podTransformerFns {
			fn(pod)
		}

		if unknown, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			unknown.Obj = pod
			return unknown, nil
		}
		return pod, nil
	}
}

func TransformKoordPriorityClassFunc() func(pod *corev1.Pod) {
	if !k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.PriorityTransformer) &&
		!utilfeature.DefaultFeatureGate.Enabled(koordfeatures.PriorityTransformer) {
		return nil
	}

	return func(pod *corev1.Pod) {
		koordPriorityValue := apiext.GetPodPriorityValueWithDefault(pod)
		pod.Spec.Priority = koordPriorityValue
	}
}

func TransformKoordPreemptionPolicyFunc() func(pod *corev1.Pod) {
	if !k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.PreemptionPolicyTransformer) &&
		!utilfeature.DefaultFeatureGate.Enabled(koordfeatures.PreemptionPolicyTransformer) {
		return nil
	}

	return func(pod *corev1.Pod) {
		preemptionPolicy := apiext.GetPodKoordPreemptionPolicyWithDefault(pod)
		if preemptionPolicy == nil {
			return
		}
		pod.Spec.PreemptionPolicy = preemptionPolicy
	}
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

func TransformSchedulerName() func(pod *corev1.Pod) {
	return func(pod *corev1.Pod) {
		pod.Spec.SchedulerName = apiext.GetSchedulerName(pod)
	}
}

func TransformScheduleExplanationObjectKey() func(pod *corev1.Pod) {
	return func(pod *corev1.Pod) {
		if pod.Labels[apiext.LabelQuestionedObjectKey] != "" {
			return
		}
		objectName := pod.Name
		if podGroupName := pod.Labels[v1alpha1.PodGroupLabel]; podGroupName != "" {
			// TODO adapt to other gangGroupScheduling approaches and onceResourceSatisfied
			objectName = strings.TrimSuffix(strings.TrimSuffix(podGroupName, "-master"), "-worker")
		} else if gangName := apiext.GetGangName(pod); gangName != "" {
			objectName = gangName
		}
		objectKey := util.GetNamespacedName(pod.Namespace, objectName)
		if pod.Annotations[apiext.AnnotationGangGroups] != "" {
			objectKey = pod.Annotations[apiext.AnnotationGangGroups]
		}
		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}
		pod.Labels[apiext.LabelQuestionedObjectKey] = objectKey
	}
}

// TransformReplaceResources transforms pod resources according to the replace-resources annotation.
func TransformReplaceResources(pod *corev1.Pod) {
	if !k8sfeature.DefaultFeatureGate.Enabled(koordfeatures.ReplaceResourcesTransformer) &&
		!utilfeature.DefaultFeatureGate.Enabled(koordfeatures.ReplaceResourcesTransformer) {
		return
	}
	eraseResNames, replaceMappings := apiext.GetPodReplaceResourcesConfig(pod)
	if len(eraseResNames) == 0 && len(replaceMappings) == 0 {
		return
	}
	for _, containers := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for i := range containers {
			container := &containers[i]
			for _, resName := range eraseResNames {
				delete(container.Resources.Requests, resName)
				delete(container.Resources.Limits, resName)
			}
			for k, v := range replaceMappings {
				replaceAndEraseResource(container.Resources.Requests, k, v)
				replaceAndEraseResource(container.Resources.Limits, k, v)
			}
		}
	}
	if pod.Spec.Overhead != nil {
		for _, resName := range eraseResNames {
			delete(pod.Spec.Overhead, resName)
		}
		for k, v := range replaceMappings {
			replaceAndEraseResource(pod.Spec.Overhead, k, v)
		}
	}
}
