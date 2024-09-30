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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Defines the pod level annotations and labels
const (
	// AnnotationNUMATopologySpec represents numa allocation API defined by Koordinator.
	// The user specifies the desired numa policy by setting the annotation.
	AnnotationNUMATopologySpec = SchedulingDomainPrefix + "/numa-topology-spec"
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
	// AnnotationNodeBECPUSharedPools describes the CPU Shared Pool defined by Koordinator.
	// The shared pool is mainly used by Koordinator BE Pods or K8s Besteffort Pods.
	AnnotationNodeBECPUSharedPools = NodeDomainPrefix + "/be-cpu-shared-pools"

	// LabelNodeCPUBindPolicy constrains how to bind CPU logical CPUs when scheduling.
	LabelNodeCPUBindPolicy = NodeDomainPrefix + "/cpu-bind-policy"
	// LabelNodeNUMAAllocateStrategy indicates how to choose satisfied NUMA Nodes when scheduling.
	LabelNodeNUMAAllocateStrategy = NodeDomainPrefix + "/numa-allocate-strategy"

	// LabelNUMATopologyPolicy represents that how to align resource allocation according to the NUMA topology
	LabelNUMATopologyPolicy = NodeDomainPrefix + "/numa-topology-policy"
)

// ResourceSpec describes extra attributes of the resource requirements.
type ResourceSpec struct {
	// RequiredCPUBindPolicy indicates that the CPU is allocated strictly
	// according to the specified CPUBindPolicy, otherwise the scheduling fails
	RequiredCPUBindPolicy CPUBindPolicy `json:"requiredCPUBindPolicy,omitempty"`
	// PreferredCPUBindPolicy represents best-effort CPU bind policy.
	PreferredCPUBindPolicy CPUBindPolicy `json:"preferredCPUBindPolicy,omitempty"`
	// PreferredCPUExclusivePolicy represents best-effort CPU exclusive policy.
	PreferredCPUExclusivePolicy CPUExclusivePolicy `json:"preferredCPUExclusivePolicy,omitempty"`
}

type NUMATopologySpec struct {
	// NUMATopologyPolicy represents the numa topology policy when schedule pod
	NUMATopologyPolicy NUMATopologyPolicy `json:"numaTopologyPolicy,omitempty"`
	// SingleNUMANodeExclusive represents whether a Pod that will use a single NUMA node/multiple NUMA nodes
	// on a NUMA node can be scheduled to use the NUMA node when another Pod that uses multiple NUMA nodes/a single NUMA node
	// is already running on the same node.
	SingleNUMANodeExclusive NumaTopologyExclusive `json:"singleNUMANodeExclusive,omitempty"`
}

// ResourceStatus describes resource allocation result, such as how to bind CPU.
type ResourceStatus struct {
	// CPUSet represents the allocated CPUs. It is Linux CPU list formatted string.
	// When LSE/LSR Pod requested, koord-scheduler will update the field.
	CPUSet string `json:"cpuset,omitempty"`
	// NUMANodeResources indicates that the Pod is constrained to run on the specified NUMA Node.
	NUMANodeResources []NUMANodeResource `json:"numaNodeResources,omitempty"`
}

type NUMANodeResource struct {
	Node      int32               `json:"node"`
	Resources corev1.ResourceList `json:"resources,omitempty"`
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

type NumaTopologyExclusive string

const (
	NumaTopologyExclusivePreferred NumaTopologyExclusive = "Preferred"
	NumaTopologyExclusiveRequired  NumaTopologyExclusive = "Required"
)

type NumaNodeStatus string

const (
	NumaNodeStatusIdle   NumaNodeStatus = "idle"
	NumaNodeStatusShared NumaNodeStatus = "shared"
	NumaNodeStatusSingle NumaNodeStatus = "single"
)

type NUMATopologyPolicy string

const (
	NUMATopologyPolicyNone           NUMATopologyPolicy = ""
	NUMATopologyPolicyBestEffort     NUMATopologyPolicy = "BestEffort"
	NUMATopologyPolicyRestricted     NUMATopologyPolicy = "Restricted"
	NUMATopologyPolicySingleNUMANode NUMATopologyPolicy = "SingleNUMANode"
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

type CPUSharedPool struct {
	Socket int32  `json:"socket"`
	Node   int32  `json:"node"`
	CPUSet string `json:"cpuset,omitempty"`
}

type KubeletCPUManagerPolicy struct {
	Policy       string            `json:"policy,omitempty"`
	Options      map[string]string `json:"options,omitempty"`
	ReservedCPUs string            `json:"reservedCPUs,omitempty"`
}

func GetNUMATopologySpec(annotations map[string]string) (*NUMATopologySpec, error) {
	numaSpec := &NUMATopologySpec{}
	data, ok := annotations[AnnotationNUMATopologySpec]
	if !ok {
		return numaSpec, nil
	}
	err := json.Unmarshal([]byte(data), numaSpec)
	if err != nil {
		return nil, err
	}
	return numaSpec, nil
}

// GetResourceSpec parses ResourceSpec from annotations
func GetResourceSpec(annotations map[string]string) (*ResourceSpec, error) {
	resourceSpec := &ResourceSpec{}
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

func GetNodeBECPUSharePools(nodeTopoAnnotations map[string]string) ([]CPUSharedPool, error) {
	var beCPUSharePools []CPUSharedPool
	data, ok := nodeTopoAnnotations[AnnotationNodeBECPUSharedPools]
	if !ok {
		return beCPUSharePools, nil
	}
	err := json.Unmarshal([]byte(data), &beCPUSharePools)
	if err != nil {
		return nil, err
	}
	return beCPUSharePools, nil
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

func GetNodeNUMATopologyPolicy(labels map[string]string) NUMATopologyPolicy {
	return NUMATopologyPolicy(labels[LabelNUMATopologyPolicy])
}

func SetNodeNUMATopologyPolicy(obj metav1.Object, policy NUMATopologyPolicy) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[LabelNUMATopologyPolicy] = string(policy)
	obj.SetLabels(labels)
	return
}
