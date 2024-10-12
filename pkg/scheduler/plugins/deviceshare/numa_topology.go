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
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/pointer"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

type NUMATopology struct {
	numNodePerSocket int
	nodes            map[int][]PCIe
}

type PCIe struct {
	PCIeIndex
	devices map[schedulingv1alpha1.DeviceType][]int
}

type PCIeIndex struct {
	socket int
	node   int
	pcie   string
}

func newNUMATopology(deviceObj *schedulingv1alpha1.Device) *NUMATopology {
	devicesInPCIe := map[PCIeIndex]map[schedulingv1alpha1.DeviceType][]int{}
	for i := range deviceObj.Spec.Devices {
		deviceInfo := &deviceObj.Spec.Devices[i]
		if deviceInfo.Topology == nil || deviceInfo.Topology.NodeID == -1 {
			//
			// NOTE: By default, it must be assigned according to the topology,
			// and the Required/Preferred strategy should be provided later.
			//
			continue
		}
		index := PCIeIndex{
			socket: int(deviceInfo.Topology.SocketID),
			node:   int(deviceInfo.Topology.NodeID),
			pcie:   deviceInfo.Topology.PCIEID,
		}
		devices := devicesInPCIe[index]
		if devices == nil {
			devices = make(map[schedulingv1alpha1.DeviceType][]int)
			devicesInPCIe[index] = devices
		}
		minor := pointer.Int32Deref(deviceInfo.Minor, 0)
		devices[deviceInfo.Type] = append(devices[deviceInfo.Type], int(minor))
	}

	topology := &NUMATopology{}
	nodeCounter := map[int]sets.Int{}
	for pcieIndex, devices := range devicesInPCIe {
		pcies := topology.nodes[pcieIndex.node]
		pcies = append(pcies, PCIe{
			PCIeIndex: pcieIndex,
			devices:   devices,
		})
		if topology.nodes == nil {
			topology.nodes = map[int][]PCIe{}
		}
		topology.nodes[pcieIndex.node] = pcies

		nodes := nodeCounter[pcieIndex.socket]
		if nodes == nil {
			nodes = sets.NewInt()
			nodeCounter[pcieIndex.socket] = nodes
		}
		nodes.Insert(pcieIndex.node)
	}
	for _, v := range nodeCounter {
		topology.numNodePerSocket = v.Len()
		break
	}
	return topology
}
