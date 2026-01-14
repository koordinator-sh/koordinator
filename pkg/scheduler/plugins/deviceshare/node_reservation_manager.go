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

package deviceshare

import (
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

// ReservedNodeInfo contains information about a reserved node
type ReservedNodeInfo struct {
	NodeName string

	// IsReserved indicates if this is a reserved node
	IsReserved bool

	// ReserveReason is the reason for reservation
	ReserveReason string

	// CompleteGPUs is the number of complete (unused) GPUs
	CompleteGPUs int

	// FragmentedGPUs is the number of fragmented (partially used) GPUs
	FragmentedGPUs int

	// LastUpdateTime is the last time this info was updated
	LastUpdateTime time.Time
}

// NodeReservationManager manages node reservation for high-priority tasks
type NodeReservationManager struct {
	sync.RWMutex

	// reservedNodes is the set of reserved nodes
	reservedNodes map[string]*ReservedNodeInfo

	// config is the reservation configuration
	config *config.ReserveCompleteResourcesConfig

	// nodeDeviceCache is the device cache
	nodeDeviceCache *nodeDeviceCache
}

// NewNodeReservationManager creates a new NodeReservationManager
func NewNodeReservationManager(
	config *config.ReserveCompleteResourcesConfig,
	nodeDeviceCache *nodeDeviceCache,
) *NodeReservationManager {

	return &NodeReservationManager{
		reservedNodes:   make(map[string]*ReservedNodeInfo),
		config:          config,
		nodeDeviceCache: nodeDeviceCache,
	}
}

// UpdateReservedNodes updates the list of reserved nodes
func (m *NodeReservationManager) UpdateReservedNodes(nodes []*corev1.Node) {
	m.Lock()
	defer m.Unlock()

	if m.config == nil || !m.config.Enabled {
		return
	}

	// Clear old reservation info
	m.reservedNodes = make(map[string]*ReservedNodeInfo)

	// Filter candidate nodes
	candidateNodes := make([]*corev1.Node, 0)
	for _, node := range nodes {
		if m.matchesReservedSelector(node) {
			candidateNodes = append(candidateNodes, node)
		}
	}

	if len(candidateNodes) == 0 {
		return
	}

	// Sort by number of complete GPUs (descending)
	sort.Slice(candidateNodes, func(i, j int) bool {
		completeI := m.countCompleteGPUs(candidateNodes[i].Name)
		completeJ := m.countCompleteGPUs(candidateNodes[j].Name)
		return completeI > completeJ
	})

	// Determine number of nodes to reserve
	reserveCount := m.calculateReserveCount(len(candidateNodes))

	// Mark reserved nodes
	for i := 0; i < reserveCount && i < len(candidateNodes); i++ {
		node := candidateNodes[i]
		completeGPUs := m.countCompleteGPUs(node.Name)
		fragmentedGPUs := m.countFragmentedGPUs(node.Name)

		m.reservedNodes[node.Name] = &ReservedNodeInfo{
			NodeName:       node.Name,
			IsReserved:     true,
			ReserveReason:  "high-priority-reservation",
			CompleteGPUs:   completeGPUs,
			FragmentedGPUs: fragmentedGPUs,
			LastUpdateTime: time.Now(),
		}

		klog.V(4).InfoS("Reserved node for high-priority tasks",
			"node", node.Name,
			"completeGPUs", completeGPUs,
			"fragmentedGPUs", fragmentedGPUs,
		)
	}
}

// matchesReservedSelector checks if a node matches the reserved selector
func (m *NodeReservationManager) matchesReservedSelector(node *corev1.Node) bool {
	if len(m.config.ReservedNodeSelector) == 0 {
		return true
	}

	for key, value := range m.config.ReservedNodeSelector {
		if node.Labels[key] != value {
			return false
		}
	}

	return true
}

// countCompleteGPUs counts the number of complete (unused) GPUs on a node
func (m *NodeReservationManager) countCompleteGPUs(nodeName string) int {
	nodeDevice := m.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDevice == nil {
		return 0
	}

	nodeDevice.lock.RLock()
	defer nodeDevice.lock.RUnlock()

	completeCount := 0
	gpuUsed, ok := nodeDevice.deviceUsed[schedulingv1alpha1.GPU]
	if !ok {
		// No GPUs used, all are complete
		if gpuTotal, ok := nodeDevice.deviceTotal[schedulingv1alpha1.GPU]; ok {
			return len(gpuTotal)
		}
		return 0
	}

	gpuTotal, ok := nodeDevice.deviceTotal[schedulingv1alpha1.GPU]
	if !ok {
		return 0
	}

	for minor := range gpuTotal {
		usedRes, ok := gpuUsed[minor]
		if !ok {
			completeCount++
			continue
		}

		usedMem := usedRes[apiext.ResourceGPUMemory]
		if usedMem.IsZero() {
			completeCount++
		}
	}

	return completeCount
}

// countFragmentedGPUs counts the number of fragmented (partially used) GPUs on a node
func (m *NodeReservationManager) countFragmentedGPUs(nodeName string) int {
	nodeDevice := m.nodeDeviceCache.getNodeDevice(nodeName, false)
	if nodeDevice == nil {
		return 0
	}

	nodeDevice.lock.RLock()
	defer nodeDevice.lock.RUnlock()

	fragmentedCount := 0
	gpuUsed, ok := nodeDevice.deviceUsed[schedulingv1alpha1.GPU]
	if !ok {
		return 0
	}

	gpuTotal, ok := nodeDevice.deviceTotal[schedulingv1alpha1.GPU]
	if !ok {
		return 0
	}

	for minor, totalRes := range gpuTotal {
		usedRes, ok := gpuUsed[minor]
		if !ok {
			continue
		}

		totalMem := totalRes[apiext.ResourceGPUMemory]
		usedMem := usedRes[apiext.ResourceGPUMemory]

		// Fragmented means partially used (0 < used < total)
		if !usedMem.IsZero() && usedMem.Cmp(totalMem) < 0 {
			fragmentedCount++
		}
	}

	return fragmentedCount
}

// calculateReserveCount calculates the number of nodes to reserve
func (m *NodeReservationManager) calculateReserveCount(totalNodes int) int {
	// Priority: use MinReservedNodes first
	if m.config.MinReservedNodes > 0 {
		return int(m.config.MinReservedNodes)
	}

	// Use percentage calculation
	if m.config.ReservedNodePercentage > 0 {
		count := (totalNodes * int(m.config.ReservedNodePercentage)) / 100
		if count < 1 {
			count = 1
		}
		return count
	}

	// Default: reserve 20% of nodes
	count := totalNodes / 5
	if count < 1 {
		count = 1
	}
	return count
}

// IsReservedNode checks if a node is reserved
func (m *NodeReservationManager) IsReservedNode(nodeName string) bool {
	m.RLock()
	defer m.RUnlock()

	info, exists := m.reservedNodes[nodeName]
	return exists && info.IsReserved
}

// GetReservedNodeInfo gets the reservation info for a node
func (m *NodeReservationManager) GetReservedNodeInfo(nodeName string) *ReservedNodeInfo {
	m.RLock()
	defer m.RUnlock()

	return m.reservedNodes[nodeName]
}

// GetAllReservedNodes returns all reserved nodes
func (m *NodeReservationManager) GetAllReservedNodes() []*ReservedNodeInfo {
	m.RLock()
	defer m.RUnlock()

	result := make([]*ReservedNodeInfo, 0, len(m.reservedNodes))
	for _, info := range m.reservedNodes {
		result = append(result, info)
	}

	return result
}

// GetReservedNodeCount returns the number of reserved nodes
func (m *NodeReservationManager) GetReservedNodeCount() int {
	m.RLock()
	defer m.RUnlock()

	return len(m.reservedNodes)
}
