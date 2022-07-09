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

	corev1 "k8s.io/api/core/v1"

	schedulingconfig "github.com/koordinator-sh/koordinator/apis/scheduling/config"
)

const (
	BatchCPU    corev1.ResourceName = DomainPrefix + "batch-cpu"
	BatchMemory corev1.ResourceName = DomainPrefix + "batch-memory"

	// AnnotationResourceSpec represents resource allocation API defined by Koordinator.
	// The user specifies the desired CPU orchestration policy by setting the annotation.
	AnnotationResourceSpec = SchedulingDomainPrefix + "/resource-spec"
	// AnnotationResourceStatus represents resource allocation result.
	// koord-scheduler patch Pod with the annotation before binding to node.
	AnnotationResourceStatus = SchedulingDomainPrefix + "/resource-status"
)

var (
	ResourceNameMap = map[PriorityClass]map[corev1.ResourceName]corev1.ResourceName{
		PriorityBatch: {
			corev1.ResourceCPU:    BatchCPU,
			corev1.ResourceMemory: BatchMemory,
		},
	}
)

// ResourceSpec describes extra attributes of the resource requirements.
type ResourceSpec struct {
	// PreferredCPUBindPolicy represents best-effort CPU bind policy.
	PreferredCPUBindPolicy CPUBindPolicy `json:"preferredCPUBindPolicy,omitempty"`
	// PreferredCPUExclusivePolicy represents best-effort CPU exclusive policy.
	PreferredCPUExclusivePolicy CPUExclusivePolicy `json:"preferredCPUExclusivePolicy,omitempty"`
}

// ResourceStatus describes resource allocation result, such as how to bind CPU.
type ResourceStatus struct {
	// CPUSet represents the allocated CPUs. It is Linux CPU list formatted string.
	// When LSE/LSR Pod requested, koord-scheduler will update the field.
	CPUSet string `json:"cpuset,omitempty"`
	// CPUSharedPools represents the desired CPU Shared Pools used by LS Pods.
	CPUSharedPools []CPUSharedPool `json:"cpuSharedPools,omitempty"`
}

// CPUBindPolicy defines the CPU binding policy
type CPUBindPolicy = schedulingconfig.CPUBindPolicy

const (
	// CPUBindPolicyDefault performs the default bind policy that specified in koord-scheduler configuration
	CPUBindPolicyDefault CPUBindPolicy = schedulingconfig.CPUBindPolicyDefault
	// CPUBindPolicyFullPCPUs favor cpuset allocation that pack in few physical cores
	CPUBindPolicyFullPCPUs CPUBindPolicy = schedulingconfig.CPUBindPolicyFullPCPUs
	// CPUBindPolicySpreadByPCPUs favor cpuset allocation that evenly allocate logical cpus across physical cores
	CPUBindPolicySpreadByPCPUs CPUBindPolicy = schedulingconfig.CPUBindPolicySpreadByPCPUs
	// CPUBindPolicyConstrainedBurst constrains the CPU Shared Pool range of the Burstable Pod
	CPUBindPolicyConstrainedBurst CPUBindPolicy = schedulingconfig.CPUBindPolicyConstrainedBurst
)

type CPUExclusivePolicy = schedulingconfig.CPUExclusivePolicy

const (
	// CPUExclusivePolicyNone does not perform any exclusive policy
	CPUExclusivePolicyNone CPUExclusivePolicy = schedulingconfig.CPUExclusivePolicyNone
	// CPUExclusivePolicyPCPULevel represents mutual exclusion in the physical core dimension
	CPUExclusivePolicyPCPULevel CPUExclusivePolicy = schedulingconfig.CPUExclusivePolicyPCPULevel
	// CPUExclusivePolicyNUMANodeLevel indicates mutual exclusion in the NUMA topology dimension
	CPUExclusivePolicyNUMANodeLevel CPUExclusivePolicy = schedulingconfig.CPUExclusivePolicyNUMANodeLevel
)

type NUMACPUSharedPools []CPUSharedPool

type CPUSharedPool struct {
	Socket int32  `json:"socket,omitempty"`
	Node   int32  `json:"node,omitempty"`
	CPUSet string `json:"cpuset,omitempty"`
}

// GetResourceSpec parses ResourceSpec from annotations
func GetResourceSpec(annotations map[string]string) (*ResourceSpec, error) {
	resourceSpec := &ResourceSpec{
		PreferredCPUBindPolicy: schedulingconfig.CPUBindPolicyDefault,
	}
	data, ok := annotations[AnnotationResourceSpec]
	if !ok {
		return resourceSpec, nil
	}
	err := json.Unmarshal([]byte(data), resourceSpec)
	if err != nil {
		return nil, err
	}
	return resourceSpec, nil
}

// GetResourceStatus parses ResourceStatus from annotations
func GetResourceStatus(annotations map[string]string) (*ResourceStatus, error) {
	resourceStatus := &ResourceStatus{}
	data, ok := annotations[AnnotationResourceStatus]
	if !ok {
		return resourceStatus, nil
	}
	err := json.Unmarshal([]byte(data), resourceStatus)
	if err != nil {
		return nil, err
	}
	return resourceStatus, nil
}

// TranslateResourceNameByPriorityClass translates defaultResourceName to extend resourceName by PriorityClass
func TranslateResourceNameByPriorityClass(priorityClass PriorityClass, defaultResourceName corev1.ResourceName) corev1.ResourceName {
	if priorityClass == PriorityProd || priorityClass == PriorityNone {
		return defaultResourceName
	}
	return ResourceNameMap[priorityClass][defaultResourceName]
}
