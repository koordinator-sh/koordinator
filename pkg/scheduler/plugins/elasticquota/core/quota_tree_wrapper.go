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
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
)

// quotaNode stores the corresponding quotaInfo's information in a specific resource dimension.
type quotaNode struct {
	quotaName         string
	request           int64
	sharedWeight      int64
	min               int64
	runtimeQuota      int64
	allowLentResource bool
}

func NewQuotaNode(quotaName string, sharedWeight, request, min int64, allowLentResource bool) *quotaNode {
	return &quotaNode{
		quotaName:         quotaName,
		request:           request,
		sharedWeight:      sharedWeight,
		min:               min,
		runtimeQuota:      0,
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

func (qt *quotaTree) insert(groupName string, sharedWeight, request, min int64, allowLentResource bool) {
	if _, exist := qt.quotaNodes[groupName]; !exist {
		qt.quotaNodes[groupName] = NewQuotaNode(groupName, sharedWeight, request, min, allowLentResource)
	}
}

func (qt *quotaTree) erase(groupName string) {
	if _, exist := qt.quotaNodes[groupName]; exist {
		delete(qt.quotaNodes, groupName)
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

func (qt *quotaTree) find(groupName string) (bool, *quotaNode) {
	if nodeValue, exist := qt.quotaNodes[groupName]; exist {
		return exist, nodeValue
	}

	return false, nil
}

//redistribution distribute the parentQuotaGroup's (or totalResource of the cluster (except the
// DefaultQuotaGroup/SystemQuotaGroup) resource to the childQuotaGroup's according to the PR's rule
func (qt *quotaTree) redistribution(totalResource int64) {
	toPartitionResource := totalResource
	totalSharedWeight := int64(0)
	needAdjustQuotaNodes := make([]*quotaNode, 0)
	for _, node := range qt.quotaNodes {
		if node.request > node.min {
			// if a node's request > autoScaleMin, the node needs adjustQuota
			// the node's runtime is autoScaleMin
			needAdjustQuotaNodes = append(needAdjustQuotaNodes, node)
			totalSharedWeight += node.sharedWeight
			node.runtimeQuota = node.min
		} else {
			if node.allowLentResource {
				node.runtimeQuota = node.request
			} else {
				// if node is not allowLentResource, even if the request is smaller
				// than autoScaleMin, runtimeQuota is request.
				node.runtimeQuota = node.min
			}
		}
		toPartitionResource -= node.runtimeQuota
	}

	if toPartitionResource > 0 {
		qt.iterationForRedistribution(toPartitionResource, totalSharedWeight, needAdjustQuotaNodes)
	}
}

func (qt *quotaTree) iterationForRedistribution(totalRes, totalSharedWeight int64, nodes []*quotaNode) {
	if totalSharedWeight <= 0 {
		// if totalSharedWeight is not larger than 0, no need to iterate anymore.
		return
	}

	needAdjustQuotaNodes := make([]*quotaNode, 0)
	toPartitionResource, needAdjustTotalSharedWeight := int64(0), int64(0)
	for _, node := range nodes {
		runtimeQuotaDelta := int64(float64(node.sharedWeight)*float64(totalRes)/float64(totalSharedWeight) + 0.5)
		node.runtimeQuota += runtimeQuotaDelta
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

type quotaResMapType map[string]v1.ResourceList
type quotaTreeMapType map[v1.ResourceName]*quotaTree

// QuotaTreeWrapper helps to calculate the childGroups' all resource dimensions' runtimeQuota of the
// corresponding quotaInfo(treeName)
type QuotaTreeWrapper struct {
	globalRuntimeVersion int64                        // increase as the runtimeQuota changed
	resourceKeys         map[v1.ResourceName]struct{} // the resource dimensions
	groupReqLimit        quotaResMapType              // all childQuotaInfos' limitedRequest
	quotaTree            quotaTreeMapType             // has all resource dimension's information
	totalResource        v1.ResourceList              // the parentQuotaInfo's runtimeQuota or the clusterResource
	lock                 sync.Mutex
	treeName             string // the same as the parentQuotaInfo's Name
}

func NewQuotaTreeWrapper(treeName string) *QuotaTreeWrapper {
	return &QuotaTreeWrapper{
		globalRuntimeVersion: 1,
		resourceKeys:         make(map[v1.ResourceName]struct{}),
		groupReqLimit:        make(quotaResMapType),
		quotaTree:            make(quotaTreeMapType),
		totalResource:        v1.ResourceList{},
		treeName:             treeName,
	}
}

func (qtw *QuotaTreeWrapper) UpdateResourceKeys(resourceKeys map[v1.ResourceName]struct{}) {
	newResourceKey := make(map[v1.ResourceName]struct{})
	for resKey := range resourceKeys {
		newResourceKey[resKey] = struct{}{}
	}

	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	qtw.resourceKeys = newResourceKey
	qtw.updateQuotaTreeDimensionByResourceKeysNoLock()
}

func (qtw *QuotaTreeWrapper) updateQuotaTreeDimensionByResourceKeysNoLock() {
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

// DeleteOneGroup delete a childGroup, should delete both the quotaTree and groupReqLimit, then adjustQuota to refresh runtimeQuota.
func (qtw *QuotaTreeWrapper) DeleteOneGroup(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	for key := range qtw.resourceKeys {
		qtw.quotaTree[key].erase(quotaInfo.Name)
	}
	delete(qtw.groupReqLimit, quotaInfo.Name)

	qtw.adjustQuotaNoLock()

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("DeleteOneGroup finish", quotaInfo)
	}
}

// UpdateOneGroupMaxQuota updates a childGroup's maxQuota, the limitedReq of the quotaGroup may change, so
// should update reqLimit in the process, then adjustQuota to refresh runtimeQuota.
// need use newMaxQuota to adjust dimension.
func (qtw *QuotaTreeWrapper) UpdateOneGroupMaxQuota(quotaInfo *QuotaInfo) {
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
			qtw.quotaTree[resKey].updateRequest(quotaInfo.Name, reqLimitPerKey.Value())
		} else {
			sharedWeightPerKey := *quotaInfo.CalculateInfo.SharedWeight.Name(resKey, resource.DecimalSI)
			autoScaleMinQuotaPerKey := *quotaInfo.CalculateInfo.AutoScaleMin.Name(resKey, resource.DecimalSI)
			qtw.quotaTree[resKey].insert(quotaInfo.Name, sharedWeightPerKey.Value(), reqLimitPerKey.Value(),
				autoScaleMinQuotaPerKey.Value(), quotaInfo.AllowLentResource)
		}

		// update reqLimitPerKey
		localReqLimit[resKey] = reqLimitPerKey
	}

	qtw.adjustQuotaNoLock()

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupMaxQuota finish", quotaInfo)
	}
}

// UpdateOneGroupMinQuota the autoScaleMin change, need adjustQuota to refresh runtime.
func (qtw *QuotaTreeWrapper) UpdateOneGroupMinQuota(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	reqLimit := quotaInfo.getLimitRequestNoLock()
	minQuota := quotaInfo.CalculateInfo.AutoScaleMin.DeepCopy()
	for resKey := range qtw.resourceKeys {
		// update/insert quotaNode
		newMinQuotaPerKey := *minQuota.Name(resKey, resource.DecimalSI)
		if exist, _ := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			qtw.quotaTree[resKey].updateMin(quotaInfo.Name, newMinQuotaPerKey.Value())
		} else {
			sharedWeightPerKey := *quotaInfo.CalculateInfo.SharedWeight.Name(resKey, resource.DecimalSI)
			reqLimitPerKey := *reqLimit.Name(resKey, resource.DecimalSI)
			qtw.quotaTree[resKey].insert(quotaInfo.Name, sharedWeightPerKey.Value(), reqLimitPerKey.Value(),
				newMinQuotaPerKey.Value(), quotaInfo.AllowLentResource)
		}
	}

	qtw.adjustQuotaNoLock()

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupMinQuota finish", quotaInfo)
	}
}

// UpdateOneGroupSharedWeight , the ability to share the "lent to" resource change, need adjustQuota to refresh runtime.
func (qtw *QuotaTreeWrapper) UpdateOneGroupSharedWeight(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	reqLimit := quotaInfo.getLimitRequestNoLock()
	sharedWeight := quotaInfo.CalculateInfo.SharedWeight.DeepCopy()
	for resKey := range qtw.resourceKeys {
		// update/insert quotaNode
		newSharedWeightPerKey := *sharedWeight.Name(resKey, resource.DecimalSI)
		if exist, _ := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			qtw.quotaTree[resKey].updateSharedWeight(quotaInfo.Name, newSharedWeightPerKey.Value())
		} else {
			reqLimitPerKey := *reqLimit.Name(resKey, resource.DecimalSI)
			minQuotaPerKey := *quotaInfo.CalculateInfo.AutoScaleMin.Name(resKey, resource.DecimalSI)
			qtw.quotaTree[resKey].insert(quotaInfo.Name, newSharedWeightPerKey.Value(), reqLimitPerKey.Value(),
				minQuotaPerKey.Value(), quotaInfo.AllowLentResource)
		}
	}

	qtw.adjustQuotaNoLock()

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupSharedWeight finish", quotaInfo)
	}
}

// NeedUpdateOneGroupRequest if oldReqLimit is the same as newReqLimit, no need to adjustQuota.
// the request of one group may change frequently, but the cost of adjustQuota is high, so here
// need to judge whether you need to update QuotaNode's request or not.
func (qtw *QuotaTreeWrapper) NeedUpdateOneGroupRequest(quotaInfo *QuotaInfo) bool {
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

func (qtw *QuotaTreeWrapper) UpdateOneGroupRequest(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	reqLimit := qtw.getGroupRequestLimitNoLock(quotaInfo.Name)
	newReqLimit := quotaInfo.getLimitRequestNoLock()
	for resKey := range qtw.resourceKeys {
		// update/insert quotaNode
		reqLimitPerKey := *newReqLimit.Name(resKey, resource.DecimalSI)

		if exist, _ := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			qtw.quotaTree[resKey].updateRequest(quotaInfo.Name, reqLimitPerKey.Value())
		} else {
			sharedWeightPerKey := *quotaInfo.CalculateInfo.SharedWeight.Name(resKey, resource.DecimalSI)
			minQuotaPerKey := *quotaInfo.CalculateInfo.AutoScaleMin.Name(resKey, resource.DecimalSI)
			qtw.quotaTree[resKey].insert(quotaInfo.Name, sharedWeightPerKey.Value(), reqLimitPerKey.Value(),
				minQuotaPerKey.Value(), quotaInfo.AllowLentResource)
		}

		// update reqLimitPerKey
		reqLimit[resKey] = reqLimitPerKey
	}

	qtw.adjustQuotaNoLock()

	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupRequest finish", quotaInfo)
	}
}

// SetClusterTotalResource increase/decrease the totalResource of the quotaTreeWrapper, the resource that can be "lent to" will
// change, need adjustQuota to refresh runtime.
func (qtw *QuotaTreeWrapper) SetClusterTotalResource(full v1.ResourceList) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	oldTotalRes := qtw.totalResource.DeepCopy()
	qtw.totalResource = full.DeepCopy()
	qtw.adjustQuotaNoLock()

	klog.V(5).Infof("UpdateClusterTotalResource"+
		"treeName:%v oldTotalResource:%v newTotalResource:%v reqLimit:%v refreshedVersion:%v",
		qtw.treeName, oldTotalRes, qtw.totalResource, qtw.groupReqLimit, qtw.globalRuntimeVersion)
}

// UpdateOneGroupRuntimeQuota update the quotaInfo's runtimeQuota as the quotaNode's runtime.
func (qtw *QuotaTreeWrapper) UpdateOneGroupRuntimeQuota(quotaInfo *QuotaInfo) {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()

	for resKey := range qtw.resourceKeys {
		if exist, quotaNode := qtw.quotaTree[resKey].find(quotaInfo.Name); exist {
			quotaInfo.CalculateInfo.Runtime[resKey] = *resource.NewQuantity(quotaNode.runtimeQuota, resource.DecimalSI)
		}
	}

	quotaInfo.RuntimeVersion = qtw.globalRuntimeVersion
	if klog.V(5).Enabled() {
		qtw.logQuotaInfoNoLock("UpdateOneGroupRuntimeQuota finish", quotaInfo)
	}
}

func (qtw *QuotaTreeWrapper) getGroupRequestLimitNoLock(quotaName string) v1.ResourceList {
	res, exist := qtw.groupReqLimit[quotaName]
	if !exist {
		res = v1.ResourceList{}
		qtw.groupReqLimit[quotaName] = res
	}
	return res
}

func (qtw *QuotaTreeWrapper) GetVersion() int64 {
	qtw.lock.Lock()
	defer qtw.lock.Unlock()
	return qtw.globalRuntimeVersion
}

func (qtw *QuotaTreeWrapper) adjustQuotaNoLock() {
	//lock outside
	for resKey := range qtw.resourceKeys {
		totalResourcePerKey := *qtw.totalResource.Name(resKey, resource.DecimalSI)
		qtw.quotaTree[resKey].redistribution(totalResourcePerKey.Value())
	}

	qtw.globalRuntimeVersion++
}

func (qtw *QuotaTreeWrapper) logQuotaInfoNoLock(verb string, quotaInfo *QuotaInfo) {
	klog.Infof("%s\n"+
		"quotaName:%v quotaParentName:%v IsParent:%v request:%v maxQuota:%v OriginalMinQuota:%v"+
		"autoScaleMinQuota:%v  SharedWeight:%v runtime:%v used:%v treeName:%v totalResource:%v reqLimit:%v refreshedVersion:%v", verb,
		quotaInfo.Name, quotaInfo.ParentName, quotaInfo.IsParent, quotaInfo.CalculateInfo.Request,
		quotaInfo.CalculateInfo.Max, quotaInfo.CalculateInfo.OriginalMin, quotaInfo.CalculateInfo.AutoScaleMin, quotaInfo.CalculateInfo.SharedWeight,
		quotaInfo.CalculateInfo.Runtime, quotaInfo.CalculateInfo.Used, qtw.treeName, qtw.totalResource, qtw.groupReqLimit,
		qtw.globalRuntimeVersion)
}
