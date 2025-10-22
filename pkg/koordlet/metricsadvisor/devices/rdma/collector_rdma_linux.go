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

package rdma

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Mellanox/rdmamap"
	"github.com/jaypipes/ghw"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/devices/helper"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	SysInfinibandDir = "/sys/class/infiniband"
	RDMAStateActive  = "ACTIVE"
)

var (
	rdmaRegex = regexp.MustCompile(`^mlx\d+_(\d+)$`)
)

func GetNetDevice() (metriccache.Devices, error) {
	pci, err := ghw.PCI()
	if err != nil {
		return nil, fmt.Errorf("getNetDevice(): new PCI instance error, %v", err)
	}
	devices := pci.ListDevices()
	if len(devices) == 0 {
		klog.Warningf("getNetDevice(): no pci devices")
		return nil, nil
	}
	var netDevices util.RDMADevices
	for _, device := range devices {
		if !isNetDevice(device.Class.ID) || system.IsSriovVF(device.Address) {
			continue
		}

		rdmaResources := rdmamap.GetRdmaDevicesForPcidev(device.Address)
		if len(rdmaResources) == 0 {
			klog.Warningf("getNetDevice(): no rdma device for pci device %s", device.Address)
			continue
		}

		sort.Slice(rdmaResources, func(i, j int) bool {
			return rdmaResources[i] < rdmaResources[j]
		})
		// pf is loaded first, so the ibdev name is smaller
		rdmaResource := rdmaResources[0]
		minor, err := getRDMAMinor(rdmaResource)
		if err != nil {
			klog.Errorf("getNetDevice(): get rdma minorID for rdma device %s error, %v", rdmaResource, err)
			return nil, err
		}

		netDevice := util.RDMADeviceInfo{
			ID:            device.Address,
			RDMAResources: rdmamap.GetRdmaDevicesForPcidev(device.Address),
			VFEnabled:     system.SriovConfigured(device.Address),
			VFMap:         nil,
			Minor:         minor,
			Labels:        nil,
			VendorCode:    device.Vendor.ID,
			DeviceCode:    device.Product.ID,
			BusID:         device.Address,
			Health:        isRDMADeviceHealthy(rdmaResource),
		}

		nodeID, pcie, _, err := helper.ParsePCIInfo(device.Address)
		if err != nil {
			klog.Errorf("getNetDevice(): parse pci device %s error, %v", device.Address, err)
			return nil, err
		}
		netDevice.NodeID = nodeID
		netDevice.PCIE = pcie
		if netDevice.VFEnabled {
			vfList, err := system.GetVFList(netDevice.ID)
			if err != nil {
				return nil, err
			}
			for _, vfBDF := range vfList {
				vf := util.VirtualFunction{
					ID: vfBDF,
				}
				if netDevice.VFMap == nil {
					netDevice.VFMap = map[string]*util.VirtualFunction{}
				}
				netDevice.VFMap[vf.ID] = &vf
			}
		}
		netDevices = append(netDevices, netDevice)
	}
	klog.Infof("rdma netDevices: %+v", netDevices)
	return netDevices, nil
}

const (
	classIDBaseInt = 16
	classIDBitSize = 64
	netDevClassID  = 0x02
)

func isNetDevice(devClassID string) bool {
	devClass, err := parseDeviceClassID(devClassID)
	if err != nil {
		klog.Warningf("getNetDevice(): unable to parse device class for device %+v %q", devClassID, err)
		return false
	}
	return devClass == netDevClassID
}

// parseDeviceClassID returns device ID parsed from the string as 64bit integer
func parseDeviceClassID(deviceID string) (int64, error) {
	return strconv.ParseInt(deviceID, classIDBaseInt, classIDBitSize)
}

// if ibdev is mlx5_1, return 1
func getRDMAMinor(rdmaDevice string) (int32, error) {
	matches := rdmaRegex.FindStringSubmatch(rdmaDevice)
	if len(matches) != 2 {
		return -1, fmt.Errorf("rdma device %s format is invalid", rdmaDevice)
	}
	minorID, err := strconv.Atoi(matches[1])
	if err != nil {
		return -1, fmt.Errorf("rdma device %s minorID parse error: %w", rdmaDevice, err)
	}
	return int32(minorID), nil
}

func isRDMADeviceHealthy(rdmaResource string) bool {
	// cat /sys/class/infiniband/mlx5_1/ports/1/state
	portsPath := filepath.Join(SysInfinibandDir, rdmaResource, "ports")
	ports, err := os.ReadDir(portsPath)
	if err != nil || len(ports) == 0 {
		klog.Errorf("isRDMADeviceHealthy(): read rdma device ports dir %s error, %v", portsPath, err)
		return false
	}
	for _, port := range ports {
		stateFile := filepath.Join(portsPath, port.Name(), "state")
		// read info from state file
		rawState, err := os.ReadFile(stateFile)
		if err != nil {
			klog.Errorf("isRDMADeviceHealthy(): read rdma device state file %s error, %v", stateFile, err)
			return false
		}
		state := string(rawState)
		if !strings.Contains(state, RDMAStateActive) {
			return false
		}
	}
	return true
}
