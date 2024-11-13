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
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/informers"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	defaultGCPeriod = 3 * time.Second
)

type nodeDevice struct {
	lock          sync.RWMutex
	deviceTotal   map[schedulingv1alpha1.DeviceType]deviceResources
	deviceFree    map[schedulingv1alpha1.DeviceType]deviceResources
	deviceUsed    map[schedulingv1alpha1.DeviceType]deviceResources
	vfAllocations map[schedulingv1alpha1.DeviceType]*VFAllocation
	allocateSet   map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources
	deviceInfos   map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo

	numaTopology               *NUMATopology
	secondaryDeviceWellPlanned bool

	nodeHonorGPUPartition bool
	gpuPartitionIndexer   GPUPartitionIndexer
	gpuTopologyScope      *GPUTopologyScope
}

type VFAllocation struct {
	allocatedVFs map[int]sets.String
}

func newNodeDevice() *nodeDevice {
	return &nodeDevice{
		deviceTotal:   make(map[schedulingv1alpha1.DeviceType]deviceResources),
		deviceFree:    make(map[schedulingv1alpha1.DeviceType]deviceResources),
		deviceUsed:    make(map[schedulingv1alpha1.DeviceType]deviceResources),
		vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
		allocateSet:   make(map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources),
		numaTopology:  &NUMATopology{},
	}
}

func (n *nodeDevice) getNodeDeviceSummary() *NodeDeviceSummary {
	n.lock.RLock()
	defer n.lock.RUnlock()

	nodeDeviceSummary := NewNodeDeviceSummary()
	calFunc := func(localDeviceRes map[schedulingv1alpha1.DeviceType]deviceResources,
		deviceResSummary map[corev1.ResourceName]*resource.Quantity,
		deviceResDetailSummary map[schedulingv1alpha1.DeviceType]deviceResources) {

		for deviceType, resourceMap := range localDeviceRes {
			deviceResDetailSummary[deviceType] = make(deviceResources)
			for minor, deviceResource := range resourceMap {
				deviceResDetailSummary[deviceType][minor] = deviceResource.DeepCopy()
				for key, value := range deviceResource {
					if _, exist := deviceResSummary[key]; !exist {
						deviceResSummary[key] = &resource.Quantity{}
						*deviceResSummary[key] = value.DeepCopy()
					} else {
						deviceResSummary[key].Add(value)
					}
				}
			}
		}
	}

	calFunc(n.deviceTotal, nodeDeviceSummary.DeviceTotal, nodeDeviceSummary.DeviceTotalDetail)
	calFunc(n.deviceFree, nodeDeviceSummary.DeviceFree, nodeDeviceSummary.DeviceFreeDetail)
	calFunc(n.deviceUsed, nodeDeviceSummary.DeviceUsed, nodeDeviceSummary.DeviceUsedDetail)

	for deviceType, allocateSet := range n.allocateSet {
		nodeDeviceSummary.AllocateSet[deviceType] = make(map[string]map[int]corev1.ResourceList)
		for podNamespacedName, allocations := range allocateSet {
			nodeDeviceSummary.AllocateSet[deviceType][podNamespacedName.String()] = make(map[int]corev1.ResourceList)
			for minor, resource := range allocations {
				nodeDeviceSummary.AllocateSet[deviceType][podNamespacedName.String()][minor] = resource.DeepCopy()
			}
		}
	}

	return nodeDeviceSummary
}

func (n *nodeDevice) resetDeviceTotal(resources map[schedulingv1alpha1.DeviceType]deviceResources) {
	for deviceType := range n.deviceTotal {
		if _, ok := resources[deviceType]; !ok {
			resources[deviceType] = make(deviceResources)
		}
	}
	n.deviceTotal = resources
	for deviceType := range resources {
		n.resetDeviceFree(deviceType)
	}
}

// updateCacheUsed is used to update deviceUsed when there is a new pod created/deleted
func (n *nodeDevice) updateCacheUsed(deviceAllocations apiext.DeviceAllocations, pod *corev1.Pod, add bool) {
	if len(deviceAllocations) > 0 {
		for deviceType, allocations := range deviceAllocations {
			if !n.isValid(deviceType, pod.Namespace, pod.Name, add) {
				continue
			}
			n.updateDeviceUsed(deviceType, allocations, add)
			n.resetDeviceFree(deviceType)
			n.updateAllocateSet(deviceType, allocations, pod, add)
		}
	}
}

func (n *nodeDevice) getUsed(namespace, name string) map[schedulingv1alpha1.DeviceType]deviceResources {
	podNamespacedName := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	allocations := map[schedulingv1alpha1.DeviceType]deviceResources{}
	for deviceType, podAllocated := range n.allocateSet {
		resources := podAllocated[podNamespacedName]
		if len(resources) == 0 {
			continue
		}
		resourcesCopy := make(map[int]corev1.ResourceList, len(resources))
		for minor, res := range resources {
			resourcesCopy[minor] = res.DeepCopy()
		}
		allocations[deviceType] = resourcesCopy
	}
	return allocations
}

func (n *nodeDevice) resetDeviceFree(deviceType schedulingv1alpha1.DeviceType) {
	if n.deviceFree[deviceType] == nil {
		n.deviceFree[deviceType] = make(deviceResources)
	}
	if n.deviceTotal[deviceType] == nil {
		n.deviceTotal[deviceType] = make(deviceResources)
	}
	n.deviceFree[deviceType] = n.deviceTotal[deviceType].DeepCopy()
	for minor, usedResource := range n.deviceUsed[deviceType] {
		if n.deviceFree[deviceType][minor] == nil {
			n.deviceFree[deviceType][minor] = make(corev1.ResourceList)
		}
		if n.deviceTotal[deviceType][minor] == nil {
			n.deviceTotal[deviceType][minor] = make(corev1.ResourceList)
		}
		n.deviceFree[deviceType][minor] = quotav1.SubtractWithNonNegativeResult(n.deviceTotal[deviceType][minor], usedResource)
	}
}

func (n *nodeDevice) updateDeviceUsed(deviceType schedulingv1alpha1.DeviceType, allocations []*apiext.DeviceAllocation, add bool) {
	deviceUsed := n.deviceUsed[deviceType]
	if deviceUsed == nil {
		deviceUsed = make(deviceResources)
		n.deviceUsed[deviceType] = deviceUsed
	}
	for _, allocation := range allocations {
		if deviceUsed[int(allocation.Minor)] == nil {
			deviceUsed[int(allocation.Minor)] = make(corev1.ResourceList)
		}
		if add {
			deviceUsed[int(allocation.Minor)] = quotav1.Add(deviceUsed[int(allocation.Minor)], allocation.Resources)
		} else {
			used := quotav1.SubtractWithNonNegativeResult(deviceUsed[int(allocation.Minor)], allocation.Resources)
			if quotav1.IsZero(used) {
				delete(deviceUsed, int(allocation.Minor))
			} else {
				deviceUsed[int(allocation.Minor)] = used
			}
		}
	}
	if !add && len(deviceUsed) == 0 {
		delete(n.deviceUsed, deviceType)
	}
	n.updateCacheVFAllocations(deviceType, allocations, add)
}

func (n *nodeDevice) isValid(deviceType schedulingv1alpha1.DeviceType, namespace string, name string, add bool) bool {
	allocateSet := n.allocateSet[deviceType]
	if allocateSet == nil {
		allocateSet = make(map[types.NamespacedName]deviceResources)
	}
	n.allocateSet[deviceType] = allocateSet

	podNamespacedName := types.NamespacedName{Namespace: namespace, Name: name}
	if add {
		if _, ok := allocateSet[podNamespacedName]; ok {
			// for non-failover scenario, pod might already exist in cache after Reserve step.
			return false
		}
	} else {
		if _, ok := allocateSet[podNamespacedName]; !ok {
			return false
		}
	}

	return true
}

func (n *nodeDevice) updateAllocateSet(deviceType schedulingv1alpha1.DeviceType, allocations []*apiext.DeviceAllocation, pod *corev1.Pod, add bool) {
	podNamespacedName := types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}

	if n.allocateSet[deviceType] == nil {
		n.allocateSet[deviceType] = make(map[types.NamespacedName]deviceResources)
	}
	if add {
		resources := make(deviceResources)
		for _, allocation := range allocations {
			resources[int(allocation.Minor)] = allocation.Resources.DeepCopy()
		}
		n.allocateSet[deviceType][podNamespacedName] = resources
	} else {
		delete(n.allocateSet[deviceType], podNamespacedName)
	}
}

func (n *nodeDevice) updateCacheVFAllocations(deviceType schedulingv1alpha1.DeviceType, allocations []*apiext.DeviceAllocation, add bool) {
	vfAllocations := getVFAllocations(allocations)
	if vfAllocations == nil {
		return
	}
	if add {
		n.updateVFAllocations(deviceType, vfAllocations)
	} else {
		n.removeVFAllocations(deviceType, vfAllocations)
	}
}

func (n *nodeDevice) updateVFAllocations(deviceType schedulingv1alpha1.DeviceType, vfAllocations *VFAllocation) {
	allocations := n.vfAllocations[deviceType]
	if allocations == nil {
		allocations = &VFAllocation{allocatedVFs: map[int]sets.String{}}
		n.vfAllocations[deviceType] = allocations
	}
	for minor, vfs := range vfAllocations.allocatedVFs {
		s := allocations.allocatedVFs[minor]
		if s == nil {
			allocations.allocatedVFs[minor] = vfs
			continue
		}
		for vf := range vfs {
			s.Insert(vf)
		}
	}
}

func (n *nodeDevice) removeVFAllocations(deviceType schedulingv1alpha1.DeviceType, vfAllocations *VFAllocation) {
	vfAlloc := n.vfAllocations[deviceType]
	if vfAlloc == nil {
		return
	}
	for minor, vfs := range vfAllocations.allocatedVFs {
		old := vfAlloc.allocatedVFs[minor]
		for v := range vfs {
			old.Delete(v)
		}
		if len(old) == 0 {
			delete(vfAlloc.allocatedVFs, minor)
		}
	}
	if len(vfAlloc.allocatedVFs) == 0 {
		delete(n.vfAllocations, deviceType)
	}
}

func getVFAllocations(allocations []*apiext.DeviceAllocation) *VFAllocation {
	var vfAllocation *VFAllocation
	for _, v := range allocations {
		if v.Extension == nil || len(v.Extension.VirtualFunctions) == 0 {
			continue
		}
		if vfAllocation == nil {
			vfAllocation = &VFAllocation{
				allocatedVFs: map[int]sets.String{},
			}
		}
		vfs := sets.NewString()
		for _, vf := range v.Extension.VirtualFunctions {
			vfs.Insert(vf.BusID)
		}
		vfAllocation.allocatedVFs[int(v.Minor)] = vfs
	}
	return vfAllocation
}

func (n *nodeDevice) calcFreeWithPreemptible(deviceType schedulingv1alpha1.DeviceType, preemptible, requiredDeviceResources deviceResources) deviceResources {
	deviceFree := n.deviceFree[deviceType]
	deviceUsed := n.deviceUsed[deviceType]
	deviceTotal := n.deviceTotal[deviceType]
	var mergedFreeDevices deviceResources
	if len(preemptible) > 0 {
		mergedFreeDevices = make(deviceResources)
		for minor, v := range preemptible {
			used := quotav1.SubtractWithNonNegativeResult(deviceUsed[minor], v)
			remaining := quotav1.SubtractWithNonNegativeResult(deviceTotal[minor], used)
			if !quotav1.IsZero(remaining) {
				mergedFreeDevices[minor] = remaining
			}
		}
	}

	// The merging logic is executed only when there is a device that can be preempted,
	// and the remaining idle devices are merged together to participate in the allocation
	if len(mergedFreeDevices) > 0 {
		for minor, v := range deviceFree {
			res := mergedFreeDevices[minor]
			if res == nil {
				mergedFreeDevices[minor] = v.DeepCopy()
			}
		}
		deviceFree = mergedFreeDevices
	}

	// If allocating from a required resources, e.g. a reservation, the free should be no larger than the reserved free.
	if len(requiredDeviceResources) > 0 {
		for minor, v := range deviceFree {
			required, ok := requiredDeviceResources[minor]
			if !ok {
				delete(deviceFree, minor)
				continue
			}
			v = util.MinResourceList(v, required)
			deviceFree[minor] = v
		}
	}

	return deviceFree
}

func (n *nodeDevice) filter(
	devices map[schedulingv1alpha1.DeviceType][]int,
	hints apiext.DeviceAllocateHints,
	requiredDeviceResources, preemptibleDeviceResources map[schedulingv1alpha1.DeviceType]deviceResources,
) *nodeDevice {
	total := map[schedulingv1alpha1.DeviceType]deviceResources{}
	used := map[schedulingv1alpha1.DeviceType]deviceResources{}
	for deviceType, deviceMinors := range devices {
		freeDevices := n.calcFreeWithPreemptible(deviceType, preemptibleDeviceResources[deviceType], requiredDeviceResources[deviceType])

		if freeDevices.isZero() {
			continue
		}

		minors := sets.NewInt(deviceMinors...)
		hint := hints[deviceType]
		if hint != nil && hint.ExclusivePolicy == apiext.PCIExpressLevelDeviceExclusivePolicy {
			freeDeviceMinors := filterFreeDevicesByPCIe(n, freeDevices, deviceType)
			minors.Intersection(freeDeviceMinors)
		}

		originalTotal := n.deviceTotal[deviceType]
		totalByDeviceType := deviceResources{}
		usedByDeviceType := deviceResources{}
		for minor, free := range freeDevices {
			if minors.Has(minor) {
				totalByDeviceType[minor] = originalTotal[minor].DeepCopy()
				usedByDeviceTypeOfMinor := quotav1.SubtractWithNonNegativeResult(originalTotal[minor], free)
				if !quotav1.IsZero(usedByDeviceTypeOfMinor) {
					usedByDeviceType[minor] = usedByDeviceTypeOfMinor
				}

			}
		}

		total[deviceType] = totalByDeviceType
		used[deviceType] = usedByDeviceType
	}
	r := newNodeDevice()
	r.deviceUsed = used
	r.resetDeviceTotal(total)
	r.vfAllocations = n.vfAllocations
	r.numaTopology = n.numaTopology
	r.deviceInfos = n.deviceInfos
	r.gpuPartitionIndexer = n.gpuPartitionIndexer
	r.nodeHonorGPUPartition = n.nodeHonorGPUPartition
	r.secondaryDeviceWellPlanned = n.secondaryDeviceWellPlanned
	r.gpuTopologyScope = n.gpuTopologyScope
	return r
}

func filterFreeDevicesByPCIe(n *nodeDevice, freeDeviceResources deviceResources, deviceType schedulingv1alpha1.DeviceType) sets.Int {
	minors := sets.NewInt()
	totalDeviceResources := n.deviceTotal[deviceType]
	for _, pcies := range n.numaTopology.nodes {
		for _, pcie := range pcies {
			skipped := false
			for _, minor := range pcie.devices[deviceType] {
				totalRes := totalDeviceResources[minor]
				freeRes := freeDeviceResources[minor]
				if !quotav1.Equals(totalRes, freeRes) {
					skipped = true
					break
				}
			}
			if !skipped {
				minors.Insert(pcie.devices[deviceType]...)
			}
		}
	}
	return minors
}

func (n *nodeDevice) split(requestsPerInstance corev1.ResourceList, deviceType schedulingv1alpha1.DeviceType) deviceResources {
	if quotav1.IsZero(requestsPerInstance) {
		return nil
	}
	var r deviceResources
	for minor, free := range n.deviceFree[deviceType] {
		if satisfied, _ := quotav1.LessThanOrEqual(requestsPerInstance, free); satisfied {
			if r == nil {
				r = make(deviceResources)
			}
			r[minor] = requestsPerInstance
		}
	}
	return r
}

type nodeDeviceCache struct {
	lock sync.Mutex
	// nodeDeviceInfos stores nodeDevice for each node.
	nodeDeviceInfos map[string]*nodeDevice
}

func newNodeDeviceCache() *nodeDeviceCache {
	return &nodeDeviceCache{
		nodeDeviceInfos: make(map[string]*nodeDevice),
	}
}

func (n *nodeDeviceCache) getNodeDevice(nodeName string, needInit bool) *nodeDevice {
	n.lock.Lock()
	defer n.lock.Unlock()

	// getNodeDevice will create new `nodeDevice` if needInit is true and nodeDeviceInfos[nodeName] is nil
	if n.nodeDeviceInfos[nodeName] == nil && needInit {
		klog.V(5).Infof("node device cache not found, nodeName: %v, createNodeDevice", nodeName)
		n.nodeDeviceInfos[nodeName] = newNodeDevice()
	}

	return n.nodeDeviceInfos[nodeName]
}

func (n *nodeDeviceCache) removeNodeDevice(nodeName string) {
	if nodeName == "" {
		return
	}
	n.lock.Lock()
	defer n.lock.Unlock()
	delete(n.nodeDeviceInfos, nodeName)
}

func (n *nodeDeviceCache) invalidateNodeDevice(device *schedulingv1alpha1.Device) {
	device = device.DeepCopy()
	for i := range device.Spec.Devices {
		info := &device.Spec.Devices[i]
		info.Health = false
	}

	nodeDeviceResource := buildDeviceResources(device)

	n.lock.Lock()
	defer n.lock.Unlock()
	info := n.nodeDeviceInfos[device.Name]
	if info == nil {
		return
	}
	info.lock.Lock()
	defer info.lock.Unlock()
	info.resetDeviceTotal(nodeDeviceResource)
}

func (n *nodeDeviceCache) updateNodeDevice(nodeName string, device *schedulingv1alpha1.Device) {
	if nodeName == "" || device == nil {
		return
	}

	nodeDeviceResource := buildDeviceResources(device)
	numaTopology := newNUMATopology(device)
	deviceInfos := map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{}
	for i := range device.Spec.Devices {
		info := &device.Spec.Devices[i]
		deviceInfos[info.Type] = append(deviceInfos[info.Type], info)
	}
	gpuPartitionTable, err := apiext.GetGPUPartitionTable(device)
	if err != nil {
		klog.Errorf("invalid gpu partition table, err: %s", err.Error())
	}
	gpuPartitionIndexer := GetGPUPartitionIndexer(gpuPartitionTable)
	gpuTopologyScope := GetGPUTopologyScope(deviceInfos[schedulingv1alpha1.GPU], nodeDeviceResource[schedulingv1alpha1.GPU])
	info := n.getNodeDevice(nodeName, true)
	info.lock.Lock()
	defer info.lock.Unlock()
	info.resetDeviceTotal(nodeDeviceResource)
	info.numaTopology = numaTopology
	info.deviceInfos = deviceInfos
	info.gpuPartitionIndexer = gpuPartitionIndexer
	info.nodeHonorGPUPartition = apiext.GetGPUPartitionPolicy(device) == apiext.GPUPartitionPolicyHonor
	info.secondaryDeviceWellPlanned = apiext.IsSecondaryDeviceWellPlanned(device)
	info.gpuTopologyScope = gpuTopologyScope
}

func buildDeviceResources(device *schedulingv1alpha1.Device) map[schedulingv1alpha1.DeviceType]deviceResources {
	nodeDeviceResource := map[schedulingv1alpha1.DeviceType]deviceResources{}
	for _, deviceInfo := range device.Spec.Devices {
		if nodeDeviceResource[deviceInfo.Type] == nil {
			nodeDeviceResource[deviceInfo.Type] = make(deviceResources)
		}

		var resources corev1.ResourceList
		if !deviceInfo.Health {
			resources = make(corev1.ResourceList)
			klog.Errorf("Find device unhealthy, nodeName:%v, deviceType:%v, minor:%v", device.Name, deviceInfo.Type, deviceInfo.Minor)
		} else {
			resources = deviceInfo.Resources
			klog.V(5).Infof("Find device resource update, nodeName:%v, deviceType:%v, minor:%v, res:%v", device.Name, deviceInfo.Type, deviceInfo.Minor, resources)
		}
		nodeDeviceResource[deviceInfo.Type][int(*deviceInfo.Minor)] = resources
	}
	return nodeDeviceResource
}

func (n *nodeDeviceCache) getNodeDeviceSummary(nodeName string) (*NodeDeviceSummary, bool) {
	n.lock.Lock()
	defer n.lock.Unlock()

	if _, exist := n.nodeDeviceInfos[nodeName]; !exist {
		return nil, false
	}

	nodeDeviceSummary := n.nodeDeviceInfos[nodeName].getNodeDeviceSummary()
	return nodeDeviceSummary, true
}

func (n *nodeDeviceCache) getAllNodeDeviceSummary() map[string]*NodeDeviceSummary {
	n.lock.Lock()
	defer n.lock.Unlock()

	nodeDeviceSummaries := make(map[string]*NodeDeviceSummary)
	for nodeName, nodeDeviceInfo := range n.nodeDeviceInfos {
		nodeDeviceSummaries[nodeName] = nodeDeviceInfo.getNodeDeviceSummary()
	}
	return nodeDeviceSummaries
}

func (n *nodeDeviceCache) gcNodeDevice(ctx context.Context, informerFactory informers.SharedInformerFactory, period time.Duration) {
	nodeInformer := informerFactory.Core().V1().Nodes().Informer()
	frameworkexthelper.ForceSyncFromInformer(ctx.Done(), informerFactory, nodeInformer, nil)

	wait.UntilWithContext(ctx, func(ctx context.Context) {
		nodeLister := informerFactory.Core().V1().Nodes().Lister()
		nodes, err := nodeLister.List(labels.Everything())
		if err != nil {
			klog.ErrorS(err, "Failed to nodeLister.List")
			return
		}
		nodeNames := make(sets.String, len(nodes))
		for _, v := range nodes {
			nodeNames.Insert(v.Name)
		}

		n.lock.Lock()
		defer n.lock.Unlock()
		for name := range n.nodeDeviceInfos {
			if !nodeNames.Has(name) {
				delete(n.nodeDeviceInfos, name)
				klog.InfoS("nodeDevice has been removed since missing Node object", "node", name)
			}
		}
	}, period)
}
