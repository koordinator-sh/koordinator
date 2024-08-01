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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
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
		if deviceInfo.Topology == nil {
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

type deviceTopologyGuide struct {
	pcieSwitches       []*pcieSwitch
	groupedNodeDevices map[int]*groupedNodeDevice
	primaryDeviceType  schedulingv1alpha1.DeviceType
}

type pcieSwitch struct {
	PCIeIndex
	nodeDevice  *nodeDevice
	freeDevices map[schedulingv1alpha1.DeviceType]deviceResources
	isEmpty     bool
	preferred   bool
}

type groupedNodeDevice struct {
	node           int
	nodeDevice     *nodeDevice
	freeDevices    map[schedulingv1alpha1.DeviceType]deviceResources
	isEmpty        bool
	preferred      bool
	preferredPCIes sets.String
}

func newDeviceTopologyGuide(
	nodeDevice *nodeDevice,
	requestPerInstance map[schedulingv1alpha1.DeviceType]corev1.ResourceList,
	primaryDeviceType schedulingv1alpha1.DeviceType,
	jointAllocate *apiext.DeviceJointAllocate,
) *deviceTopologyGuide {
	var pcieSwitches []*pcieSwitch
	for _, pcies := range nodeDevice.numaTopology.nodes {
		for _, pcie := range pcies {
			filteredNodeDevice := nodeDevice.filter(pcie.devices, nil, nil, nil)

			freeDevices := map[schedulingv1alpha1.DeviceType]deviceResources{}
			for deviceType, requests := range requestPerInstance {
				free := filteredNodeDevice.split(requests, deviceType)
				freeDevices[deviceType] = free
			}

			preferred := false
			if jointAllocate != nil {
				for _, deviceType := range jointAllocate.DeviceTypes {
					preferred = len(freeDevices[deviceType]) > 0
				}
			}

			pcieSwitches = append(pcieSwitches, &pcieSwitch{
				PCIeIndex:   pcie.PCIeIndex,
				nodeDevice:  filteredNodeDevice,
				freeDevices: freeDevices,
				isEmpty:     len(freeDevices[primaryDeviceType]) == len(filteredNodeDevice.deviceTotal[primaryDeviceType]),
				preferred:   preferred,
			})
		}
	}

	groupedNodeDevices := make(map[int]*groupedNodeDevice, len(nodeDevice.numaTopology.nodes))
	for node, pcies := range nodeDevice.numaTopology.nodes {
		deviceMinors := map[schedulingv1alpha1.DeviceType][]int{}
		preferredPCIe := sets.NewString()
		for _, pcie := range pcies {
			for deviceType, minors := range pcie.devices {
				deviceMinors[deviceType] = append(deviceMinors[deviceType], minors...)
			}
			for _, v := range pcieSwitches {
				if v.preferred && v.PCIeIndex == pcie.PCIeIndex {
					preferredPCIe.Insert(v.PCIeIndex.pcie)
				}
			}
		}

		filteredNodeDevice := nodeDevice.filter(deviceMinors, nil, nil, nil)

		freeDevices := map[schedulingv1alpha1.DeviceType]deviceResources{}
		for deviceType, requests := range requestPerInstance {
			free := filteredNodeDevice.split(requests, deviceType)
			freeDevices[deviceType] = free
		}

		preferred := false
		if jointAllocate != nil {
			for _, deviceType := range jointAllocate.DeviceTypes {
				preferred = len(freeDevices[deviceType]) > 0
			}
		}

		groupedNodeDevices[node] = &groupedNodeDevice{
			node:           node,
			nodeDevice:     filteredNodeDevice,
			freeDevices:    freeDevices,
			isEmpty:        len(freeDevices[primaryDeviceType]) == len(filteredNodeDevice.deviceTotal[primaryDeviceType]),
			preferred:      preferred,
			preferredPCIes: preferredPCIe,
		}
	}

	return &deviceTopologyGuide{
		pcieSwitches:       pcieSwitches,
		groupedNodeDevices: groupedNodeDevices,
		primaryDeviceType:  primaryDeviceType,
	}
}

func (a *deviceTopologyGuide) freeNodeDevicesInPCIe() []*pcieSwitch {
	pcieSwitches := a.pcieSwitches
	sort.Slice(pcieSwitches, func(i, j int) bool {
		iPCIE := pcieSwitches[i]
		jPCIE := pcieSwitches[j]

		if iPCIE.preferred && !jPCIE.preferred {
			return true
		} else if !iPCIE.preferred && jPCIE.preferred {
			return false
		}
		if a.groupedNodeDevices[iPCIE.node].isEmpty != a.groupedNodeDevices[jPCIE.node].isEmpty {
			return !a.groupedNodeDevices[iPCIE.node].isEmpty
		}
		if iPCIE.isEmpty != jPCIE.isEmpty {
			return !iPCIE.isEmpty
		}
		if iPCIE.socket != jPCIE.socket {
			return iPCIE.socket < jPCIE.socket
		}
		if iPCIE.node != jPCIE.node {
			return iPCIE.node < jPCIE.node
		}
		return iPCIE.pcie < jPCIE.pcie
	})
	return pcieSwitches
}

func (a *deviceTopologyGuide) freeNodeDevicesInNode() []*groupedNodeDevice {
	var groupedNodeDevices []*groupedNodeDevice
	for _, device := range a.groupedNodeDevices {
		groupedNodeDevices = append(groupedNodeDevices, device)
	}
	sort.Slice(groupedNodeDevices, func(i, j int) bool {
		iGroup := groupedNodeDevices[i]
		jGroup := groupedNodeDevices[j]
		if iGroup.preferredPCIes.Len() != jGroup.preferredPCIes.Len() {
			return iGroup.preferredPCIes.Len() > jGroup.preferredPCIes.Len()
		}
		if iGroup.preferred && !jGroup.preferred {
			return true
		} else if !iGroup.preferred && jGroup.preferred {
			return false
		}
		if iGroup.isEmpty != jGroup.isEmpty {
			return !iGroup.isEmpty
		}
		return iGroup.node < jGroup.node
	})
	return groupedNodeDevices
}
