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
	"encoding/json"

	nrtv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func buildCPUTopology(topology *nrtv1alpha1.NodeResourceTopology) *CPUTopology {
	reportedCPUTopology, err := extension.GetCPUTopology(topology.Annotations)
	if err != nil {
		klog.Errorf("Failed to GetCPUTopology, name: %s, err: %v", topology.Name, err)
		return &CPUTopology{}
	}

	cpuTopology := &CPUTopology{}
	details := NewCPUDetails()
	cpuTopoInfo := make(map[int] /*socket*/ map[int] /*node*/ map[int] /*core*/ struct{})
	for _, info := range reportedCPUTopology.Detail {
		cpuID := int(info.ID)
		socketID := int(info.Socket)
		coreID := int(info.Socket)<<16 | int(info.Core)
		nodeID := int(info.Socket)<<16 | int(info.Node)
		cpuInfo := &CPUInfo{
			CPUID:    cpuID,
			CoreID:   coreID,
			NodeID:   nodeID,
			SocketID: socketID,
		}
		details[cpuInfo.CPUID] = *cpuInfo
		if cpuTopoInfo[cpuInfo.SocketID] == nil {
			cpuTopology.NumSockets++
			cpuTopoInfo[cpuInfo.SocketID] = make(map[int]map[int]struct{})
		}
		if cpuTopoInfo[cpuInfo.SocketID][nodeID] == nil {
			cpuTopology.NumNodes++
			cpuTopoInfo[cpuInfo.SocketID][nodeID] = make(map[int]struct{})
		}
		if _, ok := cpuTopoInfo[cpuInfo.SocketID][nodeID][coreID]; !ok {
			cpuTopology.NumCores++
			cpuTopoInfo[cpuInfo.SocketID][nodeID][coreID] = struct{}{}
		}
	}
	cpuTopology.CPUDetails = details
	cpuTopology.NumCPUs = len(details)
	return cpuTopology
}

func generatePodPatch(oldPod, newPod *corev1.Pod) ([]byte, error) {
	oldData, err := json.Marshal(oldPod)
	if err != nil {
		return nil, err
	}

	newData, err := json.Marshal(newPod)
	if err != nil {
		return nil, err
	}
	return strategicpatch.CreateTwoWayMergePatch(oldData, newData, &corev1.Pod{})
}
