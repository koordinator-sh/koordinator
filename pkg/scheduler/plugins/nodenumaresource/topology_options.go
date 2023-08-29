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

package nodenumaresource

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	nrtv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

// TopologyOptionsManager manages the System Topology and resource assignments options.
type TopologyOptionsManager interface {
	GetTopologyOptions(nodeName string) TopologyOptions
	UpdateTopologyOptions(nodeName string, updateFn func(options *TopologyOptions))
	Delete(nodeName string)
}

type TopologyOptions struct {
	CPUTopology        *CPUTopology                       `json:"cpuTopology"`
	ReservedCPUs       cpuset.CPUSet                      `json:"reservedCPUs"`
	MaxRefCount        int                                `json:"maxRefCount"`
	Policy             *extension.KubeletCPUManagerPolicy `json:"policy,omitempty"`
	NUMATopologyPolicy extension.NUMATopologyPolicy       `json:"numaTopologyPolicy"`
	NUMANodeResources  []NUMANodeResource                 `json:"numaNodeResources"`
}

type NUMANodeResource struct {
	Node      int                 `json:"node"`
	Resources corev1.ResourceList `json:"resources,omitempty"`
}

type topologyManager struct {
	lock            sync.Mutex
	topologyOptions map[string]TopologyOptions
}

func NewTopologyOptionsManager() TopologyOptionsManager {
	manager := &topologyManager{
		topologyOptions: map[string]TopologyOptions{},
	}
	return manager
}

func (m *topologyManager) GetTopologyOptions(nodeName string) TopologyOptions {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.topologyOptions[nodeName]
}

func (m *topologyManager) UpdateTopologyOptions(nodeName string, updateFn func(options *TopologyOptions)) {
	m.lock.Lock()
	defer m.lock.Unlock()
	options := m.topologyOptions[nodeName]
	updateFn(&options)
	if options.MaxRefCount == 0 {
		options.MaxRefCount = 1
	}
	m.topologyOptions[nodeName] = options
}

func (m *topologyManager) Delete(nodeName string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.topologyOptions, nodeName)
}

func NewTopologyOptions(nrt *nrtv1alpha1.NodeResourceTopology) TopologyOptions {
	podCPUAllocs, err := extension.GetPodCPUAllocs(nrt.Annotations)
	if err != nil {
		klog.Errorf("Failed to GetPodCPUAllocs from new NodeResourceTopology %s, err: %v", nrt.Name, err)
	}

	kubeletPolicy, err := extension.GetKubeletCPUManagerPolicy(nrt.Annotations)
	if err != nil {
		klog.Errorf("Failed to GetKubeletCPUManagerPolicy from NodeResourceTopology %s, err: %v", nrt.Name, err)
	}
	var kubeletReservedCPUs cpuset.CPUSet
	if kubeletPolicy != nil {
		kubeletReservedCPUs, err = cpuset.Parse(kubeletPolicy.ReservedCPUs)
		if err != nil {
			klog.Errorf("Failed to Parse kubelet reserved CPUs %s, err: %v", kubeletPolicy.ReservedCPUs, err)
		}
	}

	// remove cpus reserved by node.annotation.
	reservedCPUsString, _ := extension.GetReservedCPUs(nrt.Annotations)
	nodeReservationReservedCPUs, err := cpuset.Parse(reservedCPUsString)
	if err != nil {
		klog.Errorf("Failed to parse nodeResourceResource reservedCPUs, name: %v, err: %v", nrt.Name, err)
	}
	reportedCPUTopology, err := extension.GetCPUTopology(nrt.Annotations)
	if err != nil {
		klog.Errorf("Failed to GetCPUTopology, name: %s, err: %v", nrt.Name, err)
	}

	// reservedCPUs = cpus(all) - cpus(guaranteed) - cpus(kubeletReserved) - cpus(nodeReservationReserved) - cpus(systemQOSReserved)
	cpuTopology := convertCPUTopology(reportedCPUTopology)
	reservedCPUs := getPodAllocsCPUSet(podCPUAllocs)
	reservedCPUs = reservedCPUs.Union(kubeletReservedCPUs)
	reservedCPUs = reservedCPUs.Union(nodeReservationReservedCPUs)
	systemQOSResource, err := extension.GetSystemQOSResource(nrt.Annotations)
	if err != nil {
		klog.Errorf("Failed to GetSystemQOSResource, name: %v, err: %v", nrt.Name, err)
	} else if systemQOSResource != nil && systemQOSResource.IsCPUSetExclusive() {
		cpus, err := cpuset.Parse(systemQOSResource.CPUSet)
		if err != nil {
			klog.Errorf("Failed to parse systemQOSResource.CPUSet, name: %s, err: %v", nrt.Name, err)
		} else {
			reservedCPUs = reservedCPUs.Union(cpus)
		}
	}

	policy := convertToNUMATopologyPolicy(nrt)
	numaNodeResources := extractNUMANodeResources(nrt)

	return TopologyOptions{
		CPUTopology:        cpuTopology,
		ReservedCPUs:       reservedCPUs,
		Policy:             kubeletPolicy,
		MaxRefCount:        1,
		NUMATopologyPolicy: policy,
		NUMANodeResources:  numaNodeResources,
	}
}

func getPodAllocsCPUSet(podCPUAllocs extension.PodCPUAllocs) cpuset.CPUSet {
	if len(podCPUAllocs) == 0 {
		return cpuset.CPUSet{}
	}
	builder := cpuset.NewCPUSetBuilder()
	for _, v := range podCPUAllocs {
		if !v.ManagedByKubelet || v.UID == "" || v.CPUSet == "" {
			continue
		}
		cpuset, err := cpuset.Parse(v.CPUSet)
		if err != nil || cpuset.IsEmpty() {
			continue
		}
		builder.Add(cpuset.ToSliceNoSort()...)
	}
	return builder.Result()
}

func convertCPUTopology(reportedCPUTopology *extension.CPUTopology) *CPUTopology {
	builder := NewCPUTopologyBuilder()
	for _, info := range reportedCPUTopology.Detail {
		builder.AddCPUInfo(int(info.Socket), int(info.Node), int(info.Core), int(info.ID))
	}
	return builder.Result()
}

func extractNUMANodeResources(nrt *nrtv1alpha1.NodeResourceTopology) []NUMANodeResource {
	numaNodeResources := make([]NUMANodeResource, 0, len(nrt.Zones))
	for i := range nrt.Zones {
		zone := &nrt.Zones[i]
		if zone.Type != "Node" {
			continue
		}
		parts := strings.Split(zone.Name, "node-")
		if len(parts) != 2 {
			continue
		}
		nodeID, err := strconv.Atoi(parts[1])
		if err != nil {
			klog.ErrorS(err, "Failed to parse zone name of NodeResourceTopology", "node", nrt.Name, "zoneName", zone.Name)
			continue
		}
		resources := make(corev1.ResourceList)
		for _, res := range zone.Resources {
			resName := corev1.ResourceName(res.Name)
			resources[resName] = res.Allocatable
		}
		numaNodeResources = append(numaNodeResources, NUMANodeResource{
			Node:      nodeID,
			Resources: resources,
		})
	}
	sort.Slice(numaNodeResources, func(i, j int) bool {
		return numaNodeResources[i].Node < numaNodeResources[j].Node
	})
	return numaNodeResources
}

func convertToNUMATopologyPolicy(nrt *nrtv1alpha1.NodeResourceTopology) extension.NUMATopologyPolicy {
	for _, policy := range nrt.TopologyPolicies {
		switch nrtv1alpha1.TopologyManagerPolicy(policy) {
		case nrtv1alpha1.BestEffort:
			return extension.NUMATopologyPolicyBestEffort
		case nrtv1alpha1.Restricted:
			return extension.NUMATopologyPolicyRestricted
		case nrtv1alpha1.SingleNUMANodePodLevel:
			return extension.NUMATopologyPolicySingleNUMANode
		}
	}
	return extension.NUMATopologyPolicyNone
}

func (opts *TopologyOptions) getNUMANodes() []int {
	if len(opts.NUMANodeResources) == 0 {
		return nil
	}
	nodes := make([]int, 0, len(opts.NUMANodeResources))
	for _, v := range opts.NUMANodeResources {
		nodes = append(nodes, v.Node)
	}
	return nodes
}
