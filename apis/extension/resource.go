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
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	BatchCPU    corev1.ResourceName = ResourceDomainPrefix + "batch-cpu"
	BatchMemory corev1.ResourceName = ResourceDomainPrefix + "batch-memory"
	MidCPU      corev1.ResourceName = ResourceDomainPrefix + "mid-cpu"
	MidMemory   corev1.ResourceName = ResourceDomainPrefix + "mid-memory"
)

const (
	// AnnotationExtendedResourceSpec specifies the resource requirements of extended resources for internal usage.
	// It annotates the requests/limits of extended resources and can be used by runtime proxy and koordlet that
	// cannot get the original pod spec in CRI requests.
	AnnotationExtendedResourceSpec = NodeDomainPrefix + "/extended-resource-spec"

	// AnnotationPodReplaceResources is used to replace or erase the resource name in pod spec.
	// This annotation allows the scheduler and webhook processing flow to update specified resources
	// before evaluation. It's important to note that the usage scenarios must ensure that the resources
	// on the node are not oversold by itself.
	AnnotationPodReplaceResources = PodDomainPrefix + "/replace-resources"
)

var (
	ResourceNameMap = map[PriorityClass]map[corev1.ResourceName]corev1.ResourceName{
		PriorityBatch: {
			corev1.ResourceCPU:    BatchCPU,
			corev1.ResourceMemory: BatchMemory,
		},
		PriorityMid: {
			corev1.ResourceCPU:    MidCPU,
			corev1.ResourceMemory: MidMemory,
		},
	}
)

// TranslateResourceNameByPriorityClass translates defaultResourceName to extend resourceName by PriorityClass
func TranslateResourceNameByPriorityClass(priorityClass PriorityClass, defaultResourceName corev1.ResourceName) corev1.ResourceName {
	if priorityClass == PriorityProd || priorityClass == PriorityNone {
		return defaultResourceName
	}
	return ResourceNameMap[priorityClass][defaultResourceName]
}

type ExtendedResourceSpec struct {
	Containers map[string]ExtendedResourceContainerSpec `json:"containers,omitempty"`
}

type ExtendedResourceContainerSpec struct {
	Limits   corev1.ResourceList `json:"limits,omitempty"`
	Requests corev1.ResourceList `json:"requests,omitempty"`
}

// GetExtendedResourceSpec parses ExtendedResourceSpec from annotations
func GetExtendedResourceSpec(annotations map[string]string) (*ExtendedResourceSpec, error) {
	spec := &ExtendedResourceSpec{}
	if annotations == nil {
		return spec, nil
	}
	data, ok := annotations[AnnotationExtendedResourceSpec]
	if !ok {
		return spec, nil
	}
	err := json.Unmarshal([]byte(data), spec)
	if err != nil {
		return nil, err
	}
	return spec, nil
}

func SetExtendedResourceSpec(pod *corev1.Pod, spec *ExtendedResourceSpec) error {
	if pod == nil {
		return nil
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	pod.Annotations[AnnotationExtendedResourceSpec] = string(data)
	return nil
}

// GetPodReplaceResourcesConfig parses replace and erase resource names from pod annotations.
// Annotation format: "resourceA:resourceB,resourceC:resourceD,resourceE:"
// means replace resourceA with resourceB, replace resourceC with resourceD, erase resourceE
func GetPodReplaceResourcesConfig(pod *corev1.Pod) (eraseNames []corev1.ResourceName,
	replaceMappings map[corev1.ResourceName]corev1.ResourceName) {
	if v, ok := pod.Annotations[AnnotationPodReplaceResources]; ok && v != "" {
		mappings := strings.Split(v, ",")
		replaceMappings = make(map[corev1.ResourceName]corev1.ResourceName)
		for _, mapping := range mappings {
			parts := strings.Split(mapping, ":")
			if len(parts) != 2 {
				continue
			}
			from, to := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			if to == "" {
				eraseNames = append(eraseNames, corev1.ResourceName(from))
			} else {
				replaceMappings[corev1.ResourceName(from)] = corev1.ResourceName(to)
			}
		}
	}
	return
}
