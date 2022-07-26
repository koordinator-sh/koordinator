package devicecardlevel

import (
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

type NodeDeviceCache struct {
	nodeDevices map[string]*nodeDevice
}

func newNodeDeviceCache() *NodeDeviceCache {
	return &NodeDeviceCache{
		nodeDevices: map[string]*nodeDevice{},
	}
}

func (c *NodeDeviceCache) OnNodeResourceDeviceAdd(obj interface{}) {

}

type nodeDevice struct {
	DeviceTotal map[v1alpha1.DeviceType]*deviceResource
	DeviceFree  map[v1alpha1.DeviceType]*deviceResource
	DeviceUsed  map[v1alpha1.DeviceType]*deviceResource
	AllocateSet map[string]*framework.PodInfo
}

type DeviceAllocation struct {
	Minor     int32
	Resources map[string]resource.Quantity
}

func NewDeviceAllocationWithResources(minor int32, resDesc map[string]resource.Quantity) *DeviceAllocation {
	res := make(map[string]resource.Quantity)
	for resName, rhsQuantity := range resDesc {
		res[resName] = rhsQuantity
	}
	return &DeviceAllocation{
		Resources: res,
		Minor:     minor,
	}
}

type DeviceAllocations map[v1alpha1.DeviceType][]*DeviceAllocation

func (alloc DeviceAllocations) Copy() DeviceAllocations {
	result := make(map[v1alpha1.DeviceType][]*DeviceAllocation)
	for deviceType, deviceAlloc := range alloc {
		deviceAllocCopy := make([]*DeviceAllocation, 0, len(deviceAlloc))
		for _, oneAlloc := range deviceAlloc {
			deviceAllocCopy = append(deviceAllocCopy, NewDeviceAllocationWithResources(oneAlloc.Minor, oneAlloc.Resources))
		}
		result[deviceType] = deviceAllocCopy
	}
	return result
}

/*******************deviceResource*********************/
// 一台机器上会有多张卡
// "gpu1": {GpuMem: 16GB, GpuCore:100, GpuEncode:100, GpuDecode:100}
// "gpu2": {GpuMem: 16GB, GpuCore:100, GpuEncode:100, GpuDecode:100}

type deviceResource struct {
	DeviceKeyValueMap map[int32]map[string]resource.Quantity
}

func NewDeviceResource() *deviceResource {
	return &deviceResource{
		DeviceKeyValueMap: make(map[int32]map[string]resource.Quantity),
	}
}

func (r *deviceResource) Clear() {
	for minor := range r.DeviceKeyValueMap {
		delete(r.DeviceKeyValueMap, minor)
	}
}

func (r *deviceResource) Copy() *deviceResource {
	if r == nil {
		return nil
	}
	newRes := NewDeviceResource()
	newRes.CopyFrom(r)
	return newRes
}

func (r *deviceResource) CopyFrom(rhs *deviceResource) {
	for minor := range r.DeviceKeyValueMap {
		if _, ok := rhs.DeviceKeyValueMap[minor]; !ok {
			delete(r.DeviceKeyValueMap, minor)
		}
	}

	for minor, rhsRes := range rhs.DeviceKeyValueMap {
		//make map and then copy
		r.DeviceKeyValueMap[minor] = make(map[string]resource.Quantity)
		for resName, quantity := range rhsRes {
			r.DeviceKeyValueMap[minor][resName] = quantity
		}
	}
}

func (r *deviceResource) RealSubtract(rhs *deviceResource) {
	for minor, rhsRes := range rhs.DeviceKeyValueMap {
		if localRes, ok := r.DeviceKeyValueMap[minor]; !ok {
			r.DeviceKeyValueMap[minor] = make(map[string]resource.Quantity)
			for resName, quantity := range rhsRes {
				r.DeviceKeyValueMap[minor][resName] = *resource.NewQuantity(0-quantity.Value(), resource.BinarySI)
			}
		} else {
			for resName, quantity := range rhsRes {
				if localQuantity, ok := localRes[resName]; !ok {
					localRes[resName] = *resource.NewQuantity(0-quantity.Value(), resource.BinarySI)
				} else {
					localQuantity.Set(localQuantity.Value() - quantity.Value())
				}
			}
		}
	}
}

func (r *deviceResource) Add(rhs *deviceResource) {
	for minor, rhsRes := range rhs.DeviceKeyValueMap {
		if localRes, ok := r.DeviceKeyValueMap[minor]; !ok {
			r.DeviceKeyValueMap[minor] = make(map[string]resource.Quantity)
			for resName, quantity := range rhsRes {
				r.DeviceKeyValueMap[minor][resName] = *resource.NewQuantity(quantity.Value(), resource.BinarySI)
			}
		} else {
			for resName, quantity := range rhsRes {
				if localQuantity, ok := localRes[resName]; !ok {
					localRes[resName] = *resource.NewQuantity(quantity.Value(), resource.BinarySI)
				} else {
					localQuantity.Set(localQuantity.Value() + quantity.Value())
				}
			}
		}
	}
}

func (r *deviceResource) Subtract(rhs *deviceResource) {
	for minor, rhsRes := range rhs.DeviceKeyValueMap {
		if localRes, ok := r.DeviceKeyValueMap[minor]; ok {
			for resName, quantity := range rhsRes {
				if localQuantity, ok := localRes[resName]; ok {
					val := localQuantity.Value() - quantity.Value()
					if val < 0 {
						val = 0
					}
					localQuantity.Set(val)
				}
			}
		}
	}
}

func (r *deviceResource) Sum() map[string]resource.Quantity {
	result := make(map[string]resource.Quantity)
	for _, res := range r.DeviceKeyValueMap {
		for resName, quantity := range res {
			resultQuantity, ok := result[resName]
			if !ok {
				resultQuantity = *resource.NewQuantity(0, resource.BinarySI)
				result[resName] = resultQuantity
			}
			resultQuantity.Set(resultQuantity.Value() + quantity.Value())
		}
	}
	return result
}
