package rdma

import (
	"fmt"

	"github.com/Mellanox/rdmamap"
	"github.com/jaypipes/ghw"
	"k8s.io/klog"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/devices/helper"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
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
		//klog.Warningf("rdma netDevices.Address: %+v", device.Address)
		if !IsNetDevice(device.Class.ID) || helper.IsSriovVF(device.Address) {
			continue
		}
		netDevice := util.RDMADeviceInfo{
			ID:            device.Address,
			RDMAResources: rdmamap.GetRdmaDevicesForPcidev(device.Address),
			VFEnabled:     helper.SriovConfigured(device.Address),
			VFMap:         nil,
			Minor:         0,
			Labels:        nil,
			VendorCode:    device.Vendor.ID,
			DeviceCode:    device.Product.ID,
			BusID:         device.Address,
		}
		//log.Warningf("rdma netDevices2: %+v", netDevice)
		if len(netDevice.RDMAResources) == 0 {
			klog.Warningf("getNetDevice(): no rdma device for pci device %s", device.Address)
			continue
		}
		nodeID, pcie, _, err := helper.ParsePCIInfo(device.Address)
		//klog.Warningf("getNetDevice(): nodeID:%d pcie:%s", nodeID, pcie)
		if err != nil {
			klog.Errorf("getNetDevice(): parse pci device %s error, %v", device.Address, err)
			return nil, err
		}
		netDevice.NodeID = nodeID
		netDevice.PCIE = pcie
		//klog.Warningf("getNetDevice(): netDevice:%v", netDevice)
		rdmaUVerbs, _ := GetUVerbsViaPciAdd(device.Address)
		netDevice.DevicePaths = append(netDevice.DevicePaths, rdmaUVerbs)
		//klog.Warningf("getNetDevice(): netDevice.VFEnabled:%s", netDevice.VFEnabled)
		if netDevice.VFEnabled {
			vfList, err := helper.GetVFList(netDevice.ID)
			if err != nil {
				return nil, err
			}
			for _, vfBDF := range vfList {
				vf := util.VirtualFunction{
					ID: vfBDF,
				}
				if len(netDevice.RDMAResources) != 0 {
					rdmaUVerbs, _ := GetUVerbsViaPciAdd(vfBDF)
					vf.DevicePaths = append(vf.DevicePaths, rdmaUVerbs)
				}
				if netDevice.VFMap == nil {
					netDevice.VFMap = map[string]*util.VirtualFunction{}
				}
				netDevice.VFMap[vf.ID] = &vf
			}
		}
		netDevices = append(netDevices, netDevice)
	}
	klog.Warningf("rdma netDevices: %+v", netDevices)
	return netDevices, nil
}
