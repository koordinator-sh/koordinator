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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Defines the pod level annotations and labels
const (
	// AnnotationResourceSpec represents resource allocation API defined by Koordinator.
	// The user specifies the desired CPU orchestration policy by setting the annotation.
	AnnotationResourceSpec = SchedulingDomainPrefix + "/resource-spec"
	// AnnotationResourceStatus represents resource allocation result.
	// koord-scheduler patch Pod with the annotation before binding to node.
	AnnotationResourceStatus = SchedulingDomainPrefix + "/resource-status"
)

// Defines the node level annotations and labels
const (
	// AnnotationNodeCPUTopology describes the detailed CPU topology.
	AnnotationNodeCPUTopology = NodeDomainPrefix + "/cpu-topology"
	// AnnotationNodeCPUAllocs describes K8s Guaranteed Pods.
	AnnotationNodeCPUAllocs = NodeDomainPrefix + "/pod-cpu-allocs"
	// AnnotationNodeCPUSharedPools describes the CPU Shared Pool defined by Koordinator.
	// The shared pool is mainly used by Koordinator LS Pods or K8s Burstable Pods.
	AnnotationNodeCPUSharedPools = NodeDomainPrefix + "/cpu-shared-pools"

	// LabelNodeCPUBindPolicy constrains how to bind CPU logical CPUs when scheduling.
	LabelNodeCPUBindPolicy = NodeDomainPrefix + "/cpu-bind-policy"
	// LabelNodeNUMAAllocateStrategy indicates how to choose satisfied NUMA Nodes when scheduling.
	LabelNodeNUMAAllocateStrategy = NodeDomainPrefix + "/numa-allocate-strategy"
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
type CPUBindPolicy string

const (
	// CPUBindPolicyDefault performs the default bind policy that specified in koord-scheduler configuration
	CPUBindPolicyDefault CPUBindPolicy = "Default"
	// CPUBindPolicyFullPCPUs favor cpuset allocation that pack in few physical cores
	CPUBindPolicyFullPCPUs CPUBindPolicy = "FullPCPUs"
	// CPUBindPolicySpreadByPCPUs favor cpuset allocation that evenly allocate logical cpus across physical cores
	CPUBindPolicySpreadByPCPUs CPUBindPolicy = "SpreadByPCPUs"
	// CPUBindPolicyConstrainedBurst constrains the CPU Shared Pool range of the Burstable Pod
	CPUBindPolicyConstrainedBurst CPUBindPolicy = "ConstrainedBurst"
)

type CPUExclusivePolicy string

const (
	// CPUExclusivePolicyNone does not perform any exclusive policy
	CPUExclusivePolicyNone CPUExclusivePolicy = "None"
	// CPUExclusivePolicyPCPULevel represents mutual exclusion in the physical core dimension
	CPUExclusivePolicyPCPULevel CPUExclusivePolicy = "PCPULevel"
	// CPUExclusivePolicyNUMANodeLevel indicates mutual exclusion in the NUMA topology dimension
	CPUExclusivePolicyNUMANodeLevel CPUExclusivePolicy = "NUMANodeLevel"
)

type NUMACPUSharedPools []CPUSharedPool

type CPUSharedPool struct {
	Socket int32  `json:"socket"`
	Node   int32  `json:"node"`
	CPUSet string `json:"cpuset,omitempty"`
}

type NodeCPUBindPolicy string

const (
	// NodeCPUBindPolicyNone does not perform any bind policy
	NodeCPUBindPolicyNone NodeCPUBindPolicy = "None"
	// NodeCPUBindPolicyFullPCPUsOnly requires that the scheduler must allocate full physical cores.
	// Equivalent to kubelet CPU manager policy option full-pcpus-only=true.
	NodeCPUBindPolicyFullPCPUsOnly NodeCPUBindPolicy = "FullPCPUsOnly"
	// NodeCPUBindPolicySpreadByPCPUs requires that the scheduler must evenly allocate logical cpus across physical cores
	NodeCPUBindPolicySpreadByPCPUs NodeCPUBindPolicy = "SpreadByPCPUs"
)

// NUMAAllocateStrategy indicates how to choose satisfied NUMA Nodes
type NUMAAllocateStrategy string

const (
	// NUMAMostAllocated indicates that allocates from the NUMA Node with the least amount of available resource.
	NUMAMostAllocated NUMAAllocateStrategy = "MostAllocated"
	// NUMALeastAllocated indicates that allocates from the NUMA Node with the most amount of available resource.
	NUMALeastAllocated NUMAAllocateStrategy = "LeastAllocated"
	// NUMADistributeEvenly indicates that evenly distribute CPUs across NUMA Nodes.
	NUMADistributeEvenly NUMAAllocateStrategy = "DistributeEvenly"
)

const (
	NodeNUMAAllocateStrategyLeastAllocated = NUMALeastAllocated
	NodeNUMAAllocateStrategyMostAllocated  = NUMAMostAllocated
)

const (
	// AnnotationKubeletCPUManagerPolicy describes the cpu manager policy options of kubelet
	AnnotationKubeletCPUManagerPolicy = "kubelet.koordinator.sh/cpu-manager-policy"

	KubeletCPUManagerPolicyStatic                         = "static"
	KubeletCPUManagerPolicyNone                           = "none"
	KubeletCPUManagerPolicyFullPCPUsOnlyOption            = "full-pcpus-only"
	KubeletCPUManagerPolicyDistributeCPUsAcrossNUMAOption = "distribute-cpus-across-numa"
)

type CPUTopology struct {
	Detail []CPUInfo `json:"detail,omitempty"`
}

type CPUInfo struct {
	ID     int32 `json:"id"`
	Core   int32 `json:"core"`
	Socket int32 `json:"socket"`
	Node   int32 `json:"node"`
}

type PodCPUAlloc struct {
	Namespace        string    `json:"namespace,omitempty"`
	Name             string    `json:"name,omitempty"`
	UID              types.UID `json:"uid,omitempty"`
	CPUSet           string    `json:"cpuset,omitempty"`
	ManagedByKubelet bool      `json:"managedByKubelet,omitempty"`
}

type PodCPUAllocs []PodCPUAlloc

type KubeletCPUManagerPolicy struct {
	Policy       string            `json:"policy,omitempty"`
	Options      map[string]string `json:"options,omitempty"`
	ReservedCPUs string            `json:"reservedCPUs,omitempty"`
}

// GetResourceSpec parses ResourceSpec from annotations
func GetResourceSpec(annotations map[string]string) (*ResourceSpec, error) {
	resourceSpec := &ResourceSpec{
		PreferredCPUBindPolicy: CPUBindPolicyDefault,
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

func SetResourceSpec(obj metav1.Object, spec *ResourceSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationResourceSpec] = string(data)
	obj.SetAnnotations(annotations)
	return nil
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

func SetResourceStatus(obj metav1.Object, status *ResourceStatus) error {
	if obj == nil {
		return nil
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	annotations[AnnotationResourceStatus] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

func GetCPUTopology(annotations map[string]string) (*CPUTopology, error) {
	topology := &CPUTopology{}
	data, ok := annotations[AnnotationNodeCPUTopology]
	if !ok {
		return topology, nil
	}
	err := json.Unmarshal([]byte(data), topology)
	if err != nil {
		return nil, err
	}
	return topology, nil
}

func GetPodCPUAllocs(annotations map[string]string) (PodCPUAllocs, error) {
	var allocs PodCPUAllocs
	data, ok := annotations[AnnotationNodeCPUAllocs]
	if !ok {
		return allocs, nil
	}
	err := json.Unmarshal([]byte(data), &allocs)
	if err != nil {
		return nil, err
	}
	return allocs, nil
}

func GetNodeCPUSharePools(nodeTopoAnnotations map[string]string) ([]CPUSharedPool, error) {
	var cpuSharePools []CPUSharedPool
	data, ok := nodeTopoAnnotations[AnnotationNodeCPUSharedPools]
	if !ok {
		return cpuSharePools, nil
	}
	err := json.Unmarshal([]byte(data), &cpuSharePools)
	if err != nil {
		return nil, err
	}
	return cpuSharePools, nil
}

func GetKubeletCPUManagerPolicy(annotations map[string]string) (*KubeletCPUManagerPolicy, error) {
	cpuManagerPolicy := &KubeletCPUManagerPolicy{}
	data, ok := annotations[AnnotationKubeletCPUManagerPolicy]
	if !ok {
		return cpuManagerPolicy, nil
	}
	err := json.Unmarshal([]byte(data), cpuManagerPolicy)
	if err != nil {
		return nil, err
	}
	return cpuManagerPolicy, nil
}

func GetNodeCPUBindPolicy(nodeLabels map[string]string, kubeletCPUPolicy *KubeletCPUManagerPolicy) NodeCPUBindPolicy {
	nodeCPUBindPolicy := NodeCPUBindPolicy(nodeLabels[LabelNodeCPUBindPolicy])
	if nodeCPUBindPolicy == NodeCPUBindPolicyFullPCPUsOnly ||
		(kubeletCPUPolicy != nil && kubeletCPUPolicy.Policy == KubeletCPUManagerPolicyStatic &&
			kubeletCPUPolicy.Options[KubeletCPUManagerPolicyFullPCPUsOnlyOption] == "true") {
		return NodeCPUBindPolicyFullPCPUsOnly
	}
	if nodeCPUBindPolicy == NodeCPUBindPolicySpreadByPCPUs {
		return nodeCPUBindPolicy
	}
	return NodeCPUBindPolicyNone
}
