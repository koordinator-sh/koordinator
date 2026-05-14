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

package core

import (
	"math/bits"
	"sort"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

// quotaNode stores the corresponding quotaInfo's information in a specific resource dimension.
type quotaNode struct {
	quotaName         string
	request           int64
	sharedWeight      int64
	min               int64
	runtimeQuota      int64
	guarantee         int64
	allowLentResource bool
}

func NewQuotaNode(quotaName string, sharedWeight, request, min, guarantee int64, allowLentResource bool) *quotaNode {
	return &quotaNode{
		quotaName:         quotaName,
		request:           request,
		sharedWeight:      sharedWeight,
		min:               min,
		runtimeQuota:      0,
		guarantee:         guarantee,
		allowLentResource: allowLentResource,
	}
}

// quotaTree abstract the struct to calculate each resource dimension's runtime Quota independently
type quotaTree struct {
	quotaNodes map[string]*quotaNode
}

func NewQuotaTree() *quotaTree {
	return &quotaTree{
		quotaNodes: make(map[string]*quotaNode),
	}
}

func (qt *quotaTree) insert(groupName string, sharedWeight, request, min, guarantee int64, allowLentResource bool) {
	if _, exist := qt.quotaNodes[groupName]; !exist {
		qt.quotaNodes[groupName] = NewQuotaNode(groupName, sharedWeight, request, min, guarantee, allowLentResource)
	}
}

func (qt *quotaTree) updateMin(groupName string, min int64) {
	if nodeValue, exist := qt.quotaNodes[groupName]; exist {
		if nodeValue.min != min {
			qt.quotaNodes[groupName].min = min
		}
	}
}

func (qt *quotaTree) updateSharedWeight(groupName string, sharedWeight int64) {
	if nodeValue, exist := qt.quotaNodes[groupName]; exist {
		if nodeValue.sharedWeight != sharedWeight {
			qt.quotaNodes[groupName].sharedWeight = sharedWeight
		}
	}
}

func (qt *quotaTree) updateRequest(groupName string, request int64) {
	if nodeValue, exist := qt.quotaNodes[groupName]; exist {
		if nodeValue.request != request {
			qt.quotaNodes[groupName].request = request
		}
	}
}

func (qt *quotaTree) updateGuaranteed(groupName string, guarantee int64) {
	if nodeValue, exist := qt.quotaNodes[groupName]; exist {
		if nodeValue.guarantee != guarantee {
			qt.quotaNodes[groupName].guarantee = guarantee
		}
	}
}

func (qt *quotaTree) erase(groupName string) {
	if _, exist := qt.quotaNodes[groupName]; exist {
		delete(qt.quotaNodes, groupName)
	}
}

func (qt *quotaTree) find(groupName string) (bool, *quotaNode) {
	if nodeValue, exist := qt.quotaNodes[groupName]; exist {
		return exist, nodeValue
	}

	return false, nil
}

// redistribution distribute the parentQuotaGroup's (or totalResource of the cluster (except the
// DefaultQuotaGroup/SystemQuotaGroup) resource to the childQuotaGroup's according to the PR's rule
func (qt *quotaTree) redistribution(totalResource int64) {
	toPartitionResource := totalResource
	totalSharedWeight := int64(0)
	needAdjustQuotaNodes := make([]*quotaNode, 0)
	for _, node := range qt.quotaNodes {
		min := node.min
		// if guarantee greater than min, min is guarantee.
		if node.guarantee > min {
			min = node.guarantee
		}
		if node.request > min {
			// if a node's request > autoScaleMin, the node needs adjustQuota
			// the node's runtime is autoScaleMin
			needAdjustQuotaNodes = append(needAdjustQuotaNodes, node)
			totalSharedWeight += node.sharedWeight
			node.runtimeQuota = min
		} else {
			if node.allowLentResource {
				node.runtimeQuota = node.request
			} else {
				// if node is not allowLentResource, even if the request is smaller
				// than autoScaleMin, runtimeQuota is request.
				node.runtimeQuota = min
			}
		}
		toPartitionResource -= node.runtimeQuota
	}

	if toPartitionResource > 0 {
		qt.iterationForRedistribution(toPartitionResource, totalSharedWeight, needAdjustQuotaNodes)
	}
}

func (qt *quotaTree) iterationForRedistribution(totalRes, totalSharedWeight int64, nodes []*quotaNode) {
	if totalSharedWeight <= 0 || totalRes <= 0 || len(nodes) == 0 {
		// if totalSharedWeight is not larger than 0, no need to iterate anymore.
		return
	}

	// Use the largest remainder (Hamilton) method so that the integer residual left
	// by per-node rounding is redistributed deterministically, guaranteeing that the
	// sum of deltas equals totalRes (no resources lost or double-allocated due to
	// fractional rounding).
	deltas := computeHamiltonDeltas(totalRes, totalSharedWeight, nodes)

	needAdjustQuotaNodes := make([]*quotaNode, 0)
	toPartitionResource, needAdjustTotalSharedWeight := int64(0), int64(0)
	for i, node := range nodes {
		node.runtimeQuota += deltas[i]
		if node.runtimeQuota < node.request {
			// if node's runtime is still less than request, the node still need to iterate.
			needAdjustQuotaNodes = append(needAdjustQuotaNodes, node)
			needAdjustTotalSharedWeight += node.sharedWeight
		} else {
			toPartitionResource += node.runtimeQuota - node.request
			node.runtimeQuota = node.request
		}
	}

	if toPartitionResource > 0 && len(needAdjustQuotaNodes) > 0 {
		qt.iterationForRedistribution(toPartitionResource, needAdjustTotalSharedWeight, needAdjustQuotaNodes)
	}
}

// computeHamiltonDeltas splits totalRes into per-node integer deltas proportional
// to node.sharedWeight using the largest-remainder method:
//  1. base_i      = w_i * totalRes / totalSharedWeight       (integer division via 128-bit)
//  2. remainder_i = w_i * totalRes mod totalSharedWeight
//  3. residual    = totalRes - Σ base_i                       (provably >= 0)
//  4. nodes with the largest remainders get +1 until residual == 0;
//     ties broken by quotaName for determinism.
//
// 128-bit arithmetic (math/bits.Mul64 + Div64) avoids float64 precision loss
// for large operands (e.g. memory in bytes where w*T can exceed 2^53),
// guaranteeing Σ(deltas) == totalRes exactly.
func computeHamiltonDeltas(totalRes, totalSharedWeight int64, nodes []*quotaNode) []int64 {
	deltas := make([]int64, len(nodes))
	if totalSharedWeight <= 0 || totalRes <= 0 || len(nodes) == 0 {
		return deltas
	}

	type remainderEntry struct {
		index     int
		remainder uint64
		name      string
	}
	remainders := make([]remainderEntry, 0, len(nodes))

	uT := uint64(totalRes)
	uW := uint64(totalSharedWeight)

	distributed := int64(0)
	for i, node := range nodes {
		if node.sharedWeight <= 0 {
			continue
		}
		hi, lo := bits.Mul64(uint64(node.sharedWeight), uT)
		q, r := bits.Div64(hi, lo, uW)
		base := int64(q)

		deltas[i] = base
		distributed += base
		remainders = append(remainders, remainderEntry{
			index:     i,
			remainder: r,
			name:      node.quotaName,
		})
	}

	residual := totalRes - distributed
	if residual <= 0 || len(remainders) == 0 {
		return deltas
	}

	sort.SliceStable(remainders, func(a, b int) bool {
		if remainders[a].remainder != remainders[b].remainder {
			return remainders[a].remainder > remainders[b].remainder
		}
		return remainders[a].name < remainders[b].name
	})

	for i := 0; i < len(remainders) && residual > 0; i++ {
		deltas[remainders[i].index]++
		residual--
	}
	return deltas
}

type quotaResMapType map[string]v1.ResourceList
type quotaTreeMapType map[v1.ResourceName]*quotaTree

// RuntimeQuotaCalculator helps to calculate the childGroups' all resource dimensions' runtimeQuota of the
// corresponding quotaInfo(treeName)
type RuntimeQuotaCalculator struct {
	globalRuntimeVersion int64                        // increase as the runtimeQuota changed
	resourceKeys         map[v1.ResourceName]struct{} // the resource dimensions
	groupReqLimit        quotaResMapType              // all childQuotaInfos' limitedRequest
	quotaTree            quotaTreeMapType             // has all resource dimension's information
	totalResource        v1.ResourceList              // the parentQuotaInfo's runtimeQuota or the clusterResource
	lock                 sync.Mutex
	treeName             string // the same as the parentQuotaInfo's Name
	groupGuaranteed      quotaResMapType
}

func NewRuntimeQuotaCalculator(treeName string) *RuntimeQuotaCalculator {
	return &RuntimeQuotaCalculator{
		globalRuntimeVersion: 1,
		resourceKeys:         make(map[v1.ResourceName]struct{}),
		groupReqLimit:        make(quotaResMapType),
		groupGuaranteed:      make(quotaResMapType),
		quotaTree:            make(quotaTreeMapType),
		totalResource:        v1.ResourceList{},
		treeName:             treeName,
	}
}

func (qtw *RuntimeQuotaCalculator) updateResourceKeys(resourceKeys map[v1.ResourceName]struct{}) {
	newResourceKey := make(map[v1.ResourceName]struct{})
	for resKey := range resourceKeys {
		newResourceKey[resKey] = struct{}{}
	}

	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	qtw.resourceKeys = newResourceKey
	qtw.updateQuotaTreeDimensionByResourceKeysNoLock()
}

func (qtw *RuntimeQuotaCalculator) updateQuotaTreeDimensionByResourceKeysNoLock() {
	//lock outside
	for resKey := range qtw.quotaTree {
		if _, exist := qtw.resourceKeys[resKey]; !exist {
			delete(qtw.quotaTree, resKey)
		}
	}

	for resKey := range qtw.resourceKeys {
		if _, exist := qtw.quotaTree[resKey]; !exist {
			qtw.quotaTree[resKey] = NewQuotaTree()
		}
	}
}

// updateOneGroupMaxQuota updates a childGroup's maxQuota, the limitedReq of the quotaGroup may change, so
// should update reqLimit in the process, then increase globalRuntimeVersion
// need use newMaxQuota to adjust dimension.
func (qtw *RuntimeQuotaCalculator) updateOneGroupMaxQuota(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	for resKey := range quotaInfo.CalculateInfo.Max {
		qtw.resourceKeys[resKey] = struct{}{}
		if _, exist := qtw.quotaTree[resKey]; !exist {
			qtw.quotaTree[resKey] = NewQuotaTree()
		}
	}

	localReqLimit := qtw.getGroupRequestLimitNoLock(quotaInfo.Name)
	newRequestLimit := quotaInfo.getLimitRequestNoLock()
	for resKey := range qtw.resourceKeys {
		// update/insert quotaNode
		reqLimitPerKey := *newRequestLimit.Name(resKey, resource.DecimalSI)

		if exist, _ := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			qtw.quotaTree[resKey].updateRequest(quotaInfo.Name, getQuantityValue(reqLimitPerKey, resKey))
		} else {
			sharedWeightPerKey := *quotaInfo.CalculateInfo.SharedWeight.Name(resKey, resource.DecimalSI)
			autoScaleMinQuotaPerKey := *quotaInfo.CalculateInfo.AutoScaleMin.Name(resKey, resource.DecimalSI)
			guaranteePerKey := *quotaInfo.CalculateInfo.Guaranteed.Name(resKey, resource.DecimalSI)
			qtw.quotaTree[resKey].insert(quotaInfo.Name, getQuantityValue(sharedWeightPerKey, resKey), getQuantityValue(reqLimitPerKey, resKey),
				getQuantityValue(autoScaleMinQuotaPerKey, resKey), getQuantityValue(guaranteePerKey, resKey), quotaInfo.AllowLentResource)
		}

		// update reqLimitPerKey
		localReqLimit[resKey] = reqLimitPerKey
	}

	qtw.globalRuntimeVersion++

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupMaxQuota finish", quotaInfo)
	}
}

// updateOneGroupMinQuota the autoScaleMin change, then increase globalRuntimeVersion
func (qtw *RuntimeQuotaCalculator) updateOneGroupMinQuota(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	reqLimit := quotaInfo.getLimitRequestNoLock()
	minQuota := quotaInfo.CalculateInfo.AutoScaleMin.DeepCopy()
	for resKey := range qtw.resourceKeys {
		// update/insert quotaNode
		newMinQuotaPerKey := *minQuota.Name(resKey, resource.DecimalSI)
		if exist, _ := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			qtw.quotaTree[resKey].updateMin(quotaInfo.Name, getQuantityValue(newMinQuotaPerKey, resKey))
		} else {
			sharedWeightPerKey := *quotaInfo.CalculateInfo.SharedWeight.Name(resKey, resource.DecimalSI)
			reqLimitPerKey := *reqLimit.Name(resKey, resource.DecimalSI)
			guaranteePerKey := *quotaInfo.CalculateInfo.Guaranteed.Name(resKey, resource.DecimalSI)
			qtw.quotaTree[resKey].insert(quotaInfo.Name, getQuantityValue(sharedWeightPerKey, resKey), getQuantityValue(reqLimitPerKey, resKey),
				getQuantityValue(newMinQuotaPerKey, resKey), getQuantityValue(guaranteePerKey, resKey), quotaInfo.AllowLentResource)
		}
	}

	qtw.globalRuntimeVersion++

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupMinQuota finish", quotaInfo)
	}
}

// updateOneGroupSharedWeight, the ability to share the "lent to" resource change, then increase globalRuntimeVersion
func (qtw *RuntimeQuotaCalculator) updateOneGroupSharedWeight(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	reqLimit := quotaInfo.getLimitRequestNoLock()
	sharedWeight := quotaInfo.CalculateInfo.SharedWeight.DeepCopy()
	for resKey := range qtw.resourceKeys {
		// update/insert quotaNode
		newSharedWeightPerKey := *sharedWeight.Name(resKey, resource.DecimalSI)
		if exist, _ := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			qtw.quotaTree[resKey].updateSharedWeight(quotaInfo.Name, getQuantityValue(newSharedWeightPerKey, resKey))
		} else {
			reqLimitPerKey := *reqLimit.Name(resKey, resource.DecimalSI)
			minQuotaPerKey := *quotaInfo.CalculateInfo.AutoScaleMin.Name(resKey, resource.DecimalSI)
			guaranteePerKey := *quotaInfo.CalculateInfo.Guaranteed.Name(resKey, resource.DecimalSI)
			qtw.quotaTree[resKey].insert(quotaInfo.Name, getQuantityValue(newSharedWeightPerKey, resKey), getQuantityValue(reqLimitPerKey, resKey),
				getQuantityValue(minQuotaPerKey, resKey), getQuantityValue(guaranteePerKey, resKey), quotaInfo.AllowLentResource)
		}
	}

	qtw.globalRuntimeVersion++

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupSharedWeight finish", quotaInfo)
	}
}

// needUpdateOneGroupRequest if oldReqLimit is the same as newReqLimit, no need to adjustQuota.
// the request of one group may change frequently, but the cost of adjustQuota is high, so here
// need to judge whether you need to update QuotaNode's request or not.
func (qtw *RuntimeQuotaCalculator) needUpdateOneGroupRequest(quotaInfo *QuotaInfo) bool {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	reqLimit := qtw.getGroupRequestLimitNoLock(quotaInfo.Name)
	newLimitedReq := quotaInfo.getLimitRequestNoLock()
	for resKey := range qtw.resourceKeys {
		oldReqLimitPerKey := reqLimit.Name(resKey, resource.DecimalSI)
		newReqLimitPerKey := *newLimitedReq.Name(resKey, resource.DecimalSI)
		if !oldReqLimitPerKey.Equal(newReqLimitPerKey) {
			return true
		}
	}
	return false
}

// updateOneGroupRequest the request of one group change, need increase globalRuntimeVersion
func (qtw *RuntimeQuotaCalculator) updateOneGroupRequest(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	reqLimit := qtw.getGroupRequestLimitNoLock(quotaInfo.Name)
	newReqLimit := quotaInfo.getLimitRequestNoLock()
	for resKey := range qtw.resourceKeys {
		// update/insert quotaNode
		reqLimitPerKey := *newReqLimit.Name(resKey, resource.DecimalSI)

		if exist, _ := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			qtw.quotaTree[resKey].updateRequest(quotaInfo.Name, getQuantityValue(reqLimitPerKey, resKey))
		} else {
			sharedWeightPerKey := *quotaInfo.CalculateInfo.SharedWeight.Name(resKey, resource.DecimalSI)
			minQuotaPerKey := *quotaInfo.CalculateInfo.AutoScaleMin.Name(resKey, resource.DecimalSI)
			guaranteePerKey := *quotaInfo.CalculateInfo.Guaranteed.Name(resKey, resource.DecimalSI)
			qtw.quotaTree[resKey].insert(quotaInfo.Name, getQuantityValue(sharedWeightPerKey, resKey), getQuantityValue(reqLimitPerKey, resKey),
				getQuantityValue(minQuotaPerKey, resKey), getQuantityValue(guaranteePerKey, resKey), quotaInfo.AllowLentResource)
		}

		// update reqLimitPerKey
		reqLimit[resKey] = reqLimitPerKey
	}

	qtw.globalRuntimeVersion++

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupRequest finish", quotaInfo)
	}
}

func (qtw *RuntimeQuotaCalculator) needUpdateOneGroupGuaranteed(quotaInfo *QuotaInfo) bool {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	guarantee := qtw.getGroupGuaranteedNoLock(quotaInfo.Name)
	newGuaranteed := quotaInfo.CalculateInfo.Guaranteed
	for resKey := range qtw.resourceKeys {
		oldGuaranteedPerKey := guarantee.Name(resKey, resource.DecimalSI)
		newGuaranteedPerKey := *newGuaranteed.Name(resKey, resource.DecimalSI)
		if !oldGuaranteedPerKey.Equal(newGuaranteedPerKey) {
			return true
		}
	}
	return false
}

// updateOneGroupGuaranteed the guarantee of one group change, need increase globalRuntimeVersion
func (qtw *RuntimeQuotaCalculator) updateOneGroupGuaranteed(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	reqLimit := quotaInfo.getLimitRequestNoLock()
	localGuaranteed := qtw.getGroupGuaranteedNoLock(quotaInfo.Name)
	newGuaranteed := quotaInfo.CalculateInfo.Guaranteed
	for resKey := range qtw.resourceKeys {
		// update/insert quotaNode
		guaranteePerKey := *newGuaranteed.Name(resKey, resource.DecimalSI)

		if exist, _ := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			qtw.quotaTree[resKey].updateGuaranteed(quotaInfo.Name, getQuantityValue(guaranteePerKey, resKey))
		} else {
			reqLimitPerKey := *reqLimit.Name(resKey, resource.DecimalSI)
			sharedWeightPerKey := *quotaInfo.CalculateInfo.SharedWeight.Name(resKey, resource.DecimalSI)
			minQuotaPerKey := *quotaInfo.CalculateInfo.AutoScaleMin.Name(resKey, resource.DecimalSI)
			qtw.quotaTree[resKey].insert(quotaInfo.Name, getQuantityValue(sharedWeightPerKey, resKey), getQuantityValue(reqLimitPerKey, resKey),
				getQuantityValue(minQuotaPerKey, resKey), getQuantityValue(guaranteePerKey, resKey), quotaInfo.AllowLentResource)
		}

		// update guaranteePerKey
		localGuaranteed[resKey] = guaranteePerKey
	}

	qtw.globalRuntimeVersion++

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupGuaranteed finish", quotaInfo)
	}
}

func (qtw *RuntimeQuotaCalculator) getGroupGuaranteedNoLock(quotaName string) v1.ResourceList {
	res, exist := qtw.groupGuaranteed[quotaName]
	if !exist {
		res = v1.ResourceList{}
		qtw.groupGuaranteed[quotaName] = res
	}
	return res
}

// setClusterTotalResource increase/decrease the totalResource of the RuntimeQuotaCalculator, the resource that can be "lent to" will
// change, then increase globalRuntimeVersion
func (qtw *RuntimeQuotaCalculator) setClusterTotalResource(full v1.ResourceList) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	oldTotalRes := qtw.totalResource.DeepCopy()
	qtw.totalResource = full.DeepCopy()
	qtw.globalRuntimeVersion++

	if klog.V(5).Enabled() {
		klog.Infof("setClusterTotalResource, treeName: %v, oldTotalResource: %v, newTotalResource: %v, reqLimit: %v, refreshedVersion: %v",
			qtw.treeName, util.DumpJSON(oldTotalRes), util.DumpJSON(qtw.totalResource), util.DumpJSON(qtw.groupReqLimit), qtw.globalRuntimeVersion)
	}
}

// updateOneGroupRuntimeQuota update the quotaInfo's runtimeQuota as the quotaNode's runtime.
func (qtw *RuntimeQuotaCalculator) updateOneGroupRuntimeQuota(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	if quotaInfo.RuntimeVersion == qtw.globalRuntimeVersion {
		return
	}

	qtw.calculateRuntimeNoLock()

	for resKey := range qtw.resourceKeys {
		if exist, quotaNode := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			quotaInfo.CalculateInfo.Runtime[resKey] = createQuantity(quotaNode.runtimeQuota, resKey)
		}
	}
	quotaInfo.RuntimeVersion = qtw.globalRuntimeVersion

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupRuntimeQuota finish", quotaInfo)
	}
}

func (qtw *RuntimeQuotaCalculator) getGroupRequestLimitNoLock(quotaName string) v1.ResourceList {
	res, exist := qtw.groupReqLimit[quotaName]
	if !exist {
		res = v1.ResourceList{}
		qtw.groupReqLimit[quotaName] = res
	}
	return res
}

func (qtw *RuntimeQuotaCalculator) getVersion() int64 {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()
	return qtw.globalRuntimeVersion
}

func (qtw *RuntimeQuotaCalculator) calculateRuntimeNoLock() {
	//lock outside
	for resKey := range qtw.resourceKeys {
		totalResourcePerKey := *qtw.totalResource.Name(resKey, resource.DecimalSI)
		qtw.quotaTree[resKey].redistribution(getQuantityValue(totalResourcePerKey, resKey))
	}
}

func (qtw *RuntimeQuotaCalculator) logQuotaInfoNoLock(verb string, quotaInfo *QuotaInfo) {
	klog.Infof("[%v] quotaName: %v, quotaParentName: %v, IsParent: %v, CalculateInfo: %v, treeName: %v, totalResource: %v, reqLimit: %v, refreshedVersion: %v",
		verb, quotaInfo.Name, quotaInfo.ParentName, quotaInfo.IsParent, util.DumpJSON(quotaInfo.CalculateInfo),
		qtw.treeName, util.DumpJSON(qtw.totalResource), util.DumpJSON(qtw.groupReqLimit), qtw.globalRuntimeVersion)
}

func getQuantityValue(res resource.Quantity, resName v1.ResourceName) int64 {
	if resName == v1.ResourceCPU {
		return res.MilliValue()
	}
	return res.Value()
}

func createQuantity(value int64, resName v1.ResourceName) resource.Quantity {
	var q resource.Quantity
	switch resName {
	case v1.ResourceCPU:
		q = *resource.NewMilliQuantity(value, resource.DecimalSI)
	case v1.ResourceMemory:
		q = *resource.NewQuantity(value, resource.BinarySI)
	default:
		q = *resource.NewQuantity(value, resource.DecimalSI)
	}
	return q
}

func (qtw *RuntimeQuotaCalculator) deleteOneGroup(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	for resKey := range qtw.resourceKeys {
		if exist, _ := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			qtw.quotaTree[resKey].erase(quotaInfo.Name)
		}
	}
	delete(qtw.groupReqLimit, quotaInfo.Name)
	delete(qtw.groupGuaranteed, quotaInfo.Name)

	qtw.globalRuntimeVersion++

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("deleteOneGroup finish", quotaInfo)
	}
}
