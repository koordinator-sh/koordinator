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
	"fmt"
	"reflect"
	"sync"

	v1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type GroupQuotaManager struct {
	// hierarchyUpdateLock used for resourceKeys/quotaInfoMap/quotaTreeWrapper change
	hierarchyUpdateLock sync.RWMutex
	// totalResource without systemQuotaGroup and DefaultQuotaGroup's used Quota
	totalResourceExceptSystemAndDefaultUsed v1.ResourceList
	// totalResource with systemQuotaGroup and DefaultQuotaGroup's used Quota
	totalResource v1.ResourceList
	// resourceKeys helps to store runtimeQuotaCalculators' resourceKey
	resourceKeys map[v1.ResourceName]struct{}
	// quotaInfoMap stores all the nodes, it can help get all parents conveniently
	quotaInfoMap map[string]*QuotaInfo
	// runtimeQuotaCalculatorMap helps calculate the subGroups' runtimeQuota in one quotaGroup
	runtimeQuotaCalculatorMap map[string]*RuntimeQuotaCalculator
	// quotaTopoNodeMap only stores the topology of the quota
	quotaTopoNodeMap     map[string]*QuotaTopoNode
	scaleMinQuotaEnabled bool
	// scaleMinQuotaManager is used when overRootResource
	scaleMinQuotaManager *ScaleMinQuotaManager
	once                 sync.Once

	// nodeResourceMapLock  used to lock the nodeResourceMapLock.
	nodeResourceMapLock sync.Mutex
	// nodeResourceMap store the nodes belong to the manager.
	nodeResourceMap map[string]struct{}

	// treeID is the quota tree id
	treeID string
}

func NewGroupQuotaManager(treeID string, systemGroupMax, defaultGroupMax v1.ResourceList) *GroupQuotaManager {
	quotaManager := &GroupQuotaManager{
		totalResourceExceptSystemAndDefaultUsed: v1.ResourceList{},
		totalResource:                           v1.ResourceList{},
		resourceKeys:                            make(map[v1.ResourceName]struct{}),
		quotaInfoMap:                            make(map[string]*QuotaInfo),
		runtimeQuotaCalculatorMap:               make(map[string]*RuntimeQuotaCalculator),
		quotaTopoNodeMap:                        make(map[string]*QuotaTopoNode),
		scaleMinQuotaManager:                    NewScaleMinQuotaManager(),
		nodeResourceMap:                         make(map[string]struct{}),
		treeID:                                  treeID,
	}
	// only default GroupQuotaManager need system quota and deault quota.
	if treeID == "" {
		quotaManager.quotaInfoMap[extension.SystemQuotaName] = NewQuotaInfo(false, true, extension.SystemQuotaName, extension.RootQuotaName)
		quotaManager.quotaInfoMap[extension.SystemQuotaName].setMaxQuotaNoLock(systemGroupMax)
		quotaManager.quotaInfoMap[extension.DefaultQuotaName] = NewQuotaInfo(false, true, extension.DefaultQuotaName, extension.RootQuotaName)
		quotaManager.quotaInfoMap[extension.DefaultQuotaName].setMaxQuotaNoLock(defaultGroupMax)
	}

	quotaManager.quotaInfoMap[extension.RootQuotaName] = NewQuotaInfo(true, false, extension.RootQuotaName, "")
	quotaManager.runtimeQuotaCalculatorMap[extension.RootQuotaName] = NewRuntimeQuotaCalculator(extension.RootQuotaName)
	quotaManager.setScaleMinQuotaEnabled(true)
	return quotaManager
}

func (gqm *GroupQuotaManager) setScaleMinQuotaEnabled(flag bool) {
	gqm.hierarchyUpdateLock.Lock()
	defer gqm.hierarchyUpdateLock.Unlock()

	gqm.scaleMinQuotaEnabled = flag
	klog.V(5).Infof("Set ScaleMinQuotaEnabled, flag: %v", gqm.scaleMinQuotaEnabled)
}

func (gqm *GroupQuotaManager) UpdateClusterTotalResource(deltaRes v1.ResourceList) {
	gqm.hierarchyUpdateLock.Lock()
	defer gqm.hierarchyUpdateLock.Unlock()

	if klog.V(5).Enabled() {
		klog.Infof("UpdateClusterResource tree: %v deltaRes: %v", gqm.treeID, util.DumpJSON(deltaRes))
	}
	defaultQuota := gqm.getQuotaInfoByNameNoLock(extension.DefaultQuotaName)
	if defaultQuota != nil {
		defaultQuota.lock.Lock()
		defer defaultQuota.lock.Unlock()
	}
	systemQuota := gqm.getQuotaInfoByNameNoLock(extension.SystemQuotaName)
	if systemQuota != nil {
		systemQuota.lock.Lock()
		defer systemQuota.lock.Unlock()
	}

	gqm.updateClusterTotalResourceNoLock(deltaRes)
}

// updateClusterTotalResourceNoLock no need to lock gqm.hierarchyUpdateLock and system/defaultQuotaGroup's lock
func (gqm *GroupQuotaManager) updateClusterTotalResourceNoLock(deltaRes v1.ResourceList) {
	gqm.totalResource = quotav1.Add(gqm.totalResource, deltaRes)

	var sysAndDefaultUsed v1.ResourceList
	defaultQuota := gqm.quotaInfoMap[extension.DefaultQuotaName]
	if defaultQuota != nil {
		sysAndDefaultUsed = quotav1.Add(sysAndDefaultUsed, defaultQuota.CalculateInfo.Used.DeepCopy())
	}
	systemQuota := gqm.quotaInfoMap[extension.SystemQuotaName]
	if systemQuota != nil {
		sysAndDefaultUsed = quotav1.Add(sysAndDefaultUsed, systemQuota.CalculateInfo.Used.DeepCopy())
	}

	totalResNoSysOrDefault := quotav1.Subtract(gqm.totalResource, sysAndDefaultUsed)

	diffRes := quotav1.Subtract(totalResNoSysOrDefault, gqm.totalResourceExceptSystemAndDefaultUsed)

	if !quotav1.IsZero(diffRes) {
		gqm.totalResourceExceptSystemAndDefaultUsed = totalResNoSysOrDefault.DeepCopy()
		gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].setClusterTotalResource(totalResNoSysOrDefault)
		if klog.V(5).Enabled() {
			klog.Infof("UpdateClusterResource tree: %v, finish totalResourceExceptSystemAndDefaultUsed: %v", gqm.treeID, util.DumpJSON(gqm.totalResourceExceptSystemAndDefaultUsed))
		}
	}
}

func (gqm *GroupQuotaManager) GetClusterTotalResource() v1.ResourceList {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	return gqm.totalResource.DeepCopy()
}

func (gqm *GroupQuotaManager) SetTotalResourceForTree(total v1.ResourceList) v1.ResourceList {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	delta := quotav1.Subtract(total, gqm.totalResource)
	if !quotav1.IsZero(delta) {
		gqm.updateClusterTotalResourceNoLock(delta)
		if klog.V(5).Enabled() {
			klog.Infof("SetTotalResourceForTree tree: %v, total: %v, totalResourceExceptSystemAndDefaultUsed: %v", gqm.treeID,
				util.DumpJSON(gqm.totalResource), util.DumpJSON(gqm.totalResourceExceptSystemAndDefaultUsed))
		}
	}

	return delta
}

// updateGroupDeltaRequestNoLock no need lock gqm.lock
func (gqm *GroupQuotaManager) updateGroupDeltaRequestNoLock(quotaName string, deltaReq, deltaNonPreemptibleRequest v1.ResourceList) {
	curToAllParInfos := gqm.getCurToAllParentGroupQuotaInfoNoLock(quotaName)
	allQuotaInfoLen := len(curToAllParInfos)
	if allQuotaInfoLen <= 0 {
		return
	}

	defer gqm.scopedLockForQuotaInfo(curToAllParInfos)()

	gqm.recursiveUpdateGroupTreeWithDeltaRequest(deltaReq, deltaNonPreemptibleRequest, curToAllParInfos)
}

// recursiveUpdateGroupTreeWithDeltaRequest update the quota of a node, also need update all parentNode, the lock operation
// of all quotaInfo is done by gqm. scopedLockForQuotaInfo, so just get treeWrappers' lock when calling treeWrappers' function
func (gqm *GroupQuotaManager) recursiveUpdateGroupTreeWithDeltaRequest(deltaReq, deltaNonPreemptibleRequest v1.ResourceList, curToAllParInfos []*QuotaInfo) {
	for i := 0; i < len(curToAllParInfos); i++ {
		curQuotaInfo := curToAllParInfos[i]
		oldSubLimitReq := curQuotaInfo.getLimitRequestNoLock()
		curQuotaInfo.addRequestNonNegativeNoLock(deltaReq, deltaNonPreemptibleRequest)
		if curQuotaInfo.Name == extension.RootQuotaName {
			return
		}

		curQuotaInfo.addChildRequestNonNegativeNoLock(deltaReq)
		realRequest := curQuotaInfo.CalculateInfo.ChildRequest.DeepCopy()
		// If the quota not allow to lent resource. we should request for min
		if !curQuotaInfo.AllowLentResource {
			if realRequest == nil {
				realRequest = v1.ResourceList{}
			}
			for r, q := range curQuotaInfo.CalculateInfo.Min {
				p, ok := realRequest[r]
				if !ok {
					realRequest[r] = q
				}
				if q.Cmp(p) == 1 {
					realRequest[r] = q
				}
			}
		}
		curQuotaInfo.CalculateInfo.Request = realRequest
		newSubLimitReq := curQuotaInfo.getLimitRequestNoLock()
		deltaReq = quotav1.Subtract(newSubLimitReq, oldSubLimitReq)

		directParRuntimeCalculatorPtr := gqm.getRuntimeQuotaCalculatorByNameNoLock(curQuotaInfo.ParentName)
		if directParRuntimeCalculatorPtr == nil {
			klog.Errorf("treeWrapper not exist! quotaName: %v, parentName: %v", curQuotaInfo.Name, curQuotaInfo.ParentName)
			return
		}
		if directParRuntimeCalculatorPtr.needUpdateOneGroupRequest(curQuotaInfo) {
			directParRuntimeCalculatorPtr.updateOneGroupRequest(curQuotaInfo)
		}
	}
}

// updateGroupDeltaUsedNoLock updates the usedQuota of a node, it also updates all parent nodes
// no need to lock gqm.hierarchyUpdateLock
func (gqm *GroupQuotaManager) updateGroupDeltaUsedNoLock(quotaName string, delta, deltaNonPreemptibleUsed v1.ResourceList) {
	curToAllParInfos := gqm.getCurToAllParentGroupQuotaInfoNoLock(quotaName)
	allQuotaInfoLen := len(curToAllParInfos)
	if allQuotaInfoLen <= 0 {
		return
	}

	defer gqm.scopedLockForQuotaInfo(curToAllParInfos)()
	for i := 0; i < allQuotaInfoLen; i++ {
		quotaInfo := curToAllParInfos[i]
		quotaInfo.addUsedNonNegativeNoLock(delta, deltaNonPreemptibleUsed)
	}

	if utilfeature.DefaultFeatureGate.Enabled(features.ElasticQuotaGuaranteeUsage) {
		deltaAllocated := v1.ResourceList{}
		for resKey := range gqm.resourceKeys {
			q, ok := delta[resKey]
			if ok {
				deltaAllocated[resKey] = q
			}
		}
		gqm.recursiveUpdateGroupTreeWithDeltaAllocated(deltaAllocated, curToAllParInfos)
	}

	// if systemQuotaGroup or DefaultQuotaGroup's used change, update cluster total resource.
	if quotaName == extension.SystemQuotaName || quotaName == extension.DefaultQuotaName {
		gqm.updateClusterTotalResourceNoLock(nil)
	}
}

func (gqm *GroupQuotaManager) RefreshRuntime(quotaName string) v1.ResourceList {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	return gqm.RefreshRuntimeNoLock(quotaName)
}

func (gqm *GroupQuotaManager) RefreshRuntimeNoLock(quotaName string) v1.ResourceList {
	quotaInfo := gqm.getQuotaInfoByNameNoLock(quotaName)
	if quotaInfo == nil {
		return nil
	}

	if quotaName == extension.RootQuotaName {
		return gqm.totalResourceExceptSystemAndDefaultUsed.DeepCopy()
	}

	if quotaName == extension.SystemQuotaName || quotaName == extension.DefaultQuotaName {
		return quotaInfo.GetMax()
	}

	curToAllParInfos := gqm.getCurToAllParentGroupQuotaInfoNoLock(quotaInfo.Name)

	defer gqm.scopedLockForQuotaInfo(curToAllParInfos)()

	totalRes := gqm.totalResourceExceptSystemAndDefaultUsed.DeepCopy()
	for i := len(curToAllParInfos) - 1; i >= 0; i-- {
		quotaInfo = curToAllParInfos[i]
		if quotaInfo.Name == extension.RootQuotaName {
			continue
		}
		parRuntimeQuotaCalculator := gqm.getRuntimeQuotaCalculatorByNameNoLock(quotaInfo.ParentName)
		if parRuntimeQuotaCalculator == nil {
			klog.Errorf("treeWrapper not exist! parentQuotaName: %v", quotaInfo.ParentName)
			return nil
		}
		subTreeWrapper := gqm.getRuntimeQuotaCalculatorByNameNoLock(quotaInfo.Name)
		if subTreeWrapper == nil {
			klog.Errorf("treeWrapper not exist! parentQuotaName: %v", quotaInfo.Name)
			return nil
		}

		// 1. execute scaleMin logic with totalRes and update scaledMin if needed
		if gqm.scaleMinQuotaEnabled {
			needScale, newMinQuota := gqm.scaleMinQuotaManager.getScaledMinQuota(
				totalRes, quotaInfo.ParentName, quotaInfo.Name)
			if needScale {
				gqm.updateOneGroupAutoScaleMinQuotaNoLock(quotaInfo, newMinQuota)
			}
		}

		// 2. update parent's runtimeQuota
		if quotaInfo.RuntimeVersion != parRuntimeQuotaCalculator.getVersion() {
			parRuntimeQuotaCalculator.updateOneGroupRuntimeQuota(quotaInfo)
		}
		newSubGroupsTotalRes := quotaInfo.CalculateInfo.Runtime.DeepCopy()

		// 3. update subGroup's cluster resource  when i >= 1 (still has children)
		if i >= 1 {
			subTreeWrapper.setClusterTotalResource(newSubGroupsTotalRes)
		}

		// 4. update totalRes
		totalRes = newSubGroupsTotalRes
	}

	return curToAllParInfos[0].getMaskedRuntimeNoLock()
}

// updateOneGroupAutoScaleMinQuotaNoLock no need to lock gqm.lock
func (gqm *GroupQuotaManager) updateOneGroupAutoScaleMinQuotaNoLock(quotaInfo *QuotaInfo, newMinRes v1.ResourceList) {
	if !quotav1.Equals(quotaInfo.CalculateInfo.AutoScaleMin, newMinRes) {
		quotaInfo.setAutoScaleMinQuotaNoLock(newMinRes)
		gqm.runtimeQuotaCalculatorMap[quotaInfo.ParentName].updateOneGroupMinQuota(quotaInfo)
	}
}

func (gqm *GroupQuotaManager) getCurToAllParentGroupQuotaInfoNoLock(quotaName string) []*QuotaInfo {
	curToAllParInfos := make([]*QuotaInfo, 0)
	quotaInfo := gqm.getQuotaInfoByNameNoLock(quotaName)
	if quotaInfo == nil {
		return curToAllParInfos
	}

	for true {
		curToAllParInfos = append(curToAllParInfos, quotaInfo)
		if quotaInfo.Name == extension.RootQuotaName {
			break
		}

		quotaInfo = gqm.getQuotaInfoByNameNoLock(quotaInfo.ParentName)
		if quotaInfo == nil {
			return curToAllParInfos
		}
	}

	return curToAllParInfos
}

func (gqm *GroupQuotaManager) GetQuotaInfoByName(quotaName string) *QuotaInfo {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	return gqm.getQuotaInfoByNameNoLock(quotaName)
}

func (gqm *GroupQuotaManager) getQuotaInfoByNameNoLock(quotaName string) *QuotaInfo {
	return gqm.quotaInfoMap[quotaName]
}

func (gqm *GroupQuotaManager) getRuntimeQuotaCalculatorByNameNoLock(quotaName string) *RuntimeQuotaCalculator {
	return gqm.runtimeQuotaCalculatorMap[quotaName]
}

func (gqm *GroupQuotaManager) scopedLockForQuotaInfo(quotaList []*QuotaInfo) func() {
	listLen := len(quotaList)
	for i := listLen - 1; i >= 0; i-- {
		quotaList[i].lock.Lock()
	}

	return func() {
		for i := 0; i < listLen; i++ {
			quotaList[i].lock.Unlock()
		}
	}
}

func (gqm *GroupQuotaManager) UpdateQuota(quota *v1alpha1.ElasticQuota, isDelete bool) error {
	gqm.hierarchyUpdateLock.Lock()
	defer gqm.hierarchyUpdateLock.Unlock()

	quotaName := quota.Name
	if isDelete {
		_, exist := gqm.quotaInfoMap[quotaName]
		if !exist {
			return fmt.Errorf("get quota info failed, quotaName:%v", quotaName)
		}
		delete(gqm.quotaInfoMap, quotaName)
	} else {
		newQuotaInfo := NewQuotaInfoFromQuota(quota)
		// update the local quotaInfo's crd
		if localQuotaInfo, exist := gqm.quotaInfoMap[quotaName]; exist {
			// if the quotaMeta doesn't change, only runtime/used/request/min/max/sharedWeight change causes update,
			// no need to call updateQuotaGroupConfigNoLock.
			if !localQuotaInfo.isQuotaMetaChange(newQuotaInfo) {
				gqm.updateQuotaInternalNoLock(newQuotaInfo, localQuotaInfo)
				return nil
			}
			localQuotaInfo.updateQuotaInfoFromRemote(newQuotaInfo)
		} else {
			gqm.quotaInfoMap[quotaName] = newQuotaInfo
		}
	}

	klog.Infof("reset quota tree %v, for quota %v updated", gqm.treeID, quota.Name)
	gqm.updateQuotaGroupConfigNoLock()

	return nil
}

func (gqm *GroupQuotaManager) updateQuotaGroupConfigNoLock() {
	// rebuild gqm.quotaTopoNodeMap
	gqm.buildSubParGroupTopoNoLock()
	// reset gqm.runtimeQuotaCalculator
	gqm.resetAllGroupQuotaNoLock()
}

// buildSubParGroupTopoNoLock reBuild a nodeTree from root, no need to lock gqm.lock
func (gqm *GroupQuotaManager) buildSubParGroupTopoNoLock() {
	// rebuild QuotaTopoNodeMap
	gqm.quotaTopoNodeMap = make(map[string]*QuotaTopoNode)
	rootNode := NewQuotaTopoNode(extension.RootQuotaName, NewQuotaInfo(false, true, extension.RootQuotaName, extension.RootQuotaName))
	gqm.quotaTopoNodeMap[extension.RootQuotaName] = rootNode

	// add node according to the quotaInfoMap
	for quotaName, quotaInfo := range gqm.quotaInfoMap {
		if quotaName == extension.SystemQuotaName || quotaName == extension.DefaultQuotaName {
			continue
		}
		gqm.quotaTopoNodeMap[quotaName] = NewQuotaTopoNode(quotaName, quotaInfo)
	}

	// build tree according to the parGroupName
	for _, topoNode := range gqm.quotaTopoNodeMap {
		if topoNode.name == extension.RootQuotaName {
			continue
		}
		parQuotaTopoNode := gqm.quotaTopoNodeMap[topoNode.quotaInfo.ParentName]
		// incase load child before its parent
		if parQuotaTopoNode == nil {
			parQuotaTopoNode = NewQuotaTopoNode(topoNode.quotaInfo.ParentName, &QuotaInfo{
				Name: topoNode.quotaInfo.ParentName,
			})
		}
		topoNode.parQuotaTopoNode = parQuotaTopoNode
		parQuotaTopoNode.addChildGroupQuotaInfo(topoNode)
	}

	for _, topoNode := range gqm.quotaTopoNodeMap {
		if topoNode.name == extension.RootQuotaName {
			continue
		}
		if len(topoNode.childGroupQuotaInfos) > 0 {
			topoNode.quotaInfo.IsParent = true
		} else {
			// the parent node become leaf node. clean used and childRequest
			if topoNode.quotaInfo.IsParent == true {
				topoNode.quotaInfo.IsParent = false
				topoNode.quotaInfo.CalculateInfo.ChildRequest = v1.ResourceList{}
				topoNode.quotaInfo.CalculateInfo.Used = v1.ResourceList{}
			}
		}
	}

}

// ResetAllGroupQuotaNoLock no need to lock gqm.lock
func (gqm *GroupQuotaManager) resetAllGroupQuotaNoLock() {
	childRequestMap, childNonPreemptibleUsedMap, childUsedMap, childNonPreemptibleRequestMap :=
		make(quotaResMapType), make(quotaResMapType), make(quotaResMapType), make(quotaResMapType)
	for quotaName, topoNode := range gqm.quotaTopoNodeMap {
		if quotaName == extension.RootQuotaName {
			gqm.resetRootQuotaUsedAndRequest()
			continue
		}
		topoNode.quotaInfo.lock.Lock()
		if !topoNode.quotaInfo.IsParent {
			childRequestMap[quotaName] = topoNode.quotaInfo.CalculateInfo.ChildRequest.DeepCopy()
			childNonPreemptibleRequestMap[quotaName] = topoNode.quotaInfo.CalculateInfo.NonPreemptibleRequest.DeepCopy()
			childNonPreemptibleUsedMap[quotaName] = topoNode.quotaInfo.CalculateInfo.NonPreemptibleUsed.DeepCopy()
			childUsedMap[quotaName] = topoNode.quotaInfo.CalculateInfo.Used.DeepCopy()
		}
		topoNode.quotaInfo.clearForResetNoLock()
		topoNode.quotaInfo.lock.Unlock()
	}

	// clear old runtimeQuotaCalculator
	gqm.runtimeQuotaCalculatorMap = make(map[string]*RuntimeQuotaCalculator)
	// reset runtimeQuotaCalculator
	gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName] = NewRuntimeQuotaCalculator(extension.RootQuotaName)
	gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].setClusterTotalResource(gqm.totalResourceExceptSystemAndDefaultUsed)
	rootNode := gqm.quotaTopoNodeMap[extension.RootQuotaName]
	gqm.resetAllGroupQuotaRecursiveNoLock(rootNode)
	gqm.updateResourceKeyNoLock()

	// subGroup's topo relation may change; refresh the request/used from bottom to top
	for quotaName, topoNode := range gqm.quotaTopoNodeMap {
		if !topoNode.quotaInfo.IsParent {
			gqm.updateGroupDeltaRequestNoLock(quotaName, childRequestMap[quotaName], childNonPreemptibleRequestMap[quotaName])
			gqm.updateGroupDeltaUsedNoLock(quotaName, childUsedMap[quotaName], childNonPreemptibleUsedMap[quotaName])
		}
	}
}

// ResetAllGroupQuotaRecursiveNoLock no need to lock gqm.lock
func (gqm *GroupQuotaManager) resetAllGroupQuotaRecursiveNoLock(rootNode *QuotaTopoNode) {
	childGroupQuotaInfos := rootNode.getChildGroupQuotaInfos()
	for subName, topoNode := range childGroupQuotaInfos {
		gqm.runtimeQuotaCalculatorMap[subName] = NewRuntimeQuotaCalculator(subName)

		gqm.updateOneGroupMaxQuotaNoLock(topoNode.quotaInfo)
		gqm.updateMinQuotaNoLock(topoNode.quotaInfo)
		gqm.updateOneGroupSharedWeightNoLock(topoNode.quotaInfo)

		gqm.resetAllGroupQuotaRecursiveNoLock(topoNode)
	}
}

// updateOneGroupMaxQuotaNoLock no need to lock gqm.lock
func (gqm *GroupQuotaManager) updateOneGroupMaxQuotaNoLock(quotaInfo *QuotaInfo) {
	quotaInfo.lock.Lock()
	defer quotaInfo.lock.Unlock()

	runtimeQuotaCalculator := gqm.getRuntimeQuotaCalculatorByNameNoLock(quotaInfo.ParentName)
	runtimeQuotaCalculator.updateOneGroupMaxQuota(quotaInfo)
}

// updateMinQuotaNoLock no need to lock gqm.lock
func (gqm *GroupQuotaManager) updateMinQuotaNoLock(quotaInfo *QuotaInfo) {
	gqm.updateOneGroupOriginalMinQuotaNoLock(quotaInfo)
	gqm.scaleMinQuotaManager.update(quotaInfo.ParentName, quotaInfo.Name,
		quotaInfo.CalculateInfo.Min.DeepCopy(), gqm.scaleMinQuotaEnabled)
}

// updateOneGroupOriginalMinQuotaNoLock no need to lock gqm.lock
func (gqm *GroupQuotaManager) updateOneGroupOriginalMinQuotaNoLock(quotaInfo *QuotaInfo) {
	quotaInfo.lock.Lock()
	defer quotaInfo.lock.Unlock()

	quotaInfo.setAutoScaleMinQuotaNoLock(quotaInfo.CalculateInfo.Min)
	gqm.runtimeQuotaCalculatorMap[quotaInfo.ParentName].updateOneGroupMinQuota(quotaInfo)
}

// updateOneGroupSharedWeightNoLock no need to lock gqm.lock
func (gqm *GroupQuotaManager) updateOneGroupSharedWeightNoLock(quotaInfo *QuotaInfo) {
	quotaInfo.lock.Lock()
	defer quotaInfo.lock.Unlock()

	gqm.runtimeQuotaCalculatorMap[quotaInfo.ParentName].updateOneGroupSharedWeight(quotaInfo)
}

func (gqm *GroupQuotaManager) updateResourceKeyNoLock() {
	// collect all dimensions
	resourceKeys := make(map[v1.ResourceName]struct{})
	for quotaName, quotaInfo := range gqm.quotaInfoMap {
		if quotaName == extension.DefaultQuotaName || quotaName == extension.SystemQuotaName {
			continue
		}
		for resName := range quotaInfo.CalculateInfo.Max {
			resourceKeys[resName] = struct{}{}
		}
	}

	if !reflect.DeepEqual(resourceKeys, gqm.resourceKeys) {
		gqm.resourceKeys = resourceKeys
		for _, runtimeQuotaCalculator := range gqm.runtimeQuotaCalculatorMap {
			runtimeQuotaCalculator.updateResourceKeys(resourceKeys)
		}
	}
}

func (gqm *GroupQuotaManager) GetAllQuotaNames() map[string]struct{} {
	quotaInfoMap := make(map[string]struct{})
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	for name := range gqm.quotaInfoMap {
		quotaInfoMap[name] = struct{}{}
	}
	return quotaInfoMap
}

func (gqm *GroupQuotaManager) updatePodRequestNoLock(quotaName string, oldPod, newPod *v1.Pod) {
	var oldPodReq, newPodReq, oldNonPreemptibleRequest, newNonPreemptibleRequest v1.ResourceList
	if oldPod != nil {
		oldPodReq, _ = PodRequestsAndLimits(oldPod)
		if extension.IsPodNonPreemptible(oldPod) {
			oldNonPreemptibleRequest = oldPodReq
		}
	}

	if newPod != nil {
		newPodReq, _ = PodRequestsAndLimits(newPod)
		if extension.IsPodNonPreemptible(newPod) {
			newNonPreemptibleRequest = newPodReq
		}
	}

	deltaReq := quotav1.Subtract(newPodReq, oldPodReq)
	deltaNonPreemptibleRequest := quotav1.Subtract(newNonPreemptibleRequest, oldNonPreemptibleRequest)
	if quotav1.IsZero(deltaReq) && quotav1.IsZero(deltaNonPreemptibleRequest) {
		return
	}
	gqm.updateGroupDeltaRequestNoLock(quotaName, deltaReq, deltaNonPreemptibleRequest)
}

func (gqm *GroupQuotaManager) updatePodUsedNoLock(quotaName string, oldPod, newPod *v1.Pod) {
	quotaInfo := gqm.getQuotaInfoByNameNoLock(quotaName)
	if quotaInfo == nil {
		return
	}
	if !quotaInfo.CheckPodIsAssigned(newPod) && !quotaInfo.CheckPodIsAssigned(oldPod) {
		klog.V(5).Infof("updatePodUsed, isAssigned is false, quotaName: %v, podName: %v",
			quotaName, getPodName(oldPod, newPod))
		return
	}

	var oldPodUsed, newPodUsed, oldNonPreemptibleUsed, newNonPreemptibleUsed v1.ResourceList
	if oldPod != nil {
		oldPodUsed, _ = PodRequestsAndLimits(oldPod)
		if extension.IsPodNonPreemptible(oldPod) {
			oldNonPreemptibleUsed = oldPodUsed
		}
	}

	if newPod != nil {
		newPodUsed, _ = PodRequestsAndLimits(newPod)
		if extension.IsPodNonPreemptible(newPod) {
			newNonPreemptibleUsed = newPodUsed
		}
	}

	deltaUsed := quotav1.Subtract(newPodUsed, oldPodUsed)
	deltaNonPreemptibleUsed := quotav1.Subtract(newNonPreemptibleUsed, oldNonPreemptibleUsed)
	if quotav1.IsZero(deltaUsed) && quotav1.IsZero(deltaNonPreemptibleUsed) {
		if klog.V(5).Enabled() {
			klog.Infof("updatePodUsed, deltaUsedIsZero and deltaNonPreemptibleUsedIsZero, quotaName: %v, podName: %v, podUsed: %v, podNonPreemptibleUsed: %v",
				quotaName, getPodName(oldPod, newPod), util.DumpJSON(newPodUsed), util.DumpJSON(newNonPreemptibleUsed))
		}
	}
	gqm.updateGroupDeltaUsedNoLock(quotaName, deltaUsed, deltaNonPreemptibleUsed)
}

func (gqm *GroupQuotaManager) updatePodCacheNoLock(quotaName string, pod *v1.Pod, isAdd bool) {
	quotaInfo := gqm.getQuotaInfoByNameNoLock(quotaName)
	if quotaInfo == nil {
		return
	}

	if isAdd {
		quotaInfo.addPodIfNotPresent(pod)
	} else {
		quotaInfo.removePodIfPresent(pod)
	}
}

func (gqm *GroupQuotaManager) UpdatePodIsAssigned(quotaName string, pod *v1.Pod, isAssigned bool) error {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	return gqm.updatePodIsAssignedNoLock(quotaName, pod, isAssigned)
}

func (gqm *GroupQuotaManager) updatePodIsAssignedNoLock(quotaName string, pod *v1.Pod, isAssigned bool) error {
	quotaInfo := gqm.getQuotaInfoByNameNoLock(quotaName)
	return quotaInfo.UpdatePodIsAssigned(pod, isAssigned)
}

func (gqm *GroupQuotaManager) getPodIsAssignedNoLock(quotaName string, pod *v1.Pod) bool {
	quotaInfo := gqm.getQuotaInfoByNameNoLock(quotaName)
	if quotaInfo == nil {
		return false
	}
	return quotaInfo.CheckPodIsAssigned(pod)
}

func (gqm *GroupQuotaManager) MigratePod(pod *v1.Pod, out, in string) {
	gqm.hierarchyUpdateLock.Lock()
	defer gqm.hierarchyUpdateLock.Unlock()

	isAssigned := gqm.getPodIsAssignedNoLock(out, pod)
	gqm.updatePodRequestNoLock(out, pod, nil)
	gqm.updatePodUsedNoLock(out, pod, nil)
	gqm.updatePodCacheNoLock(out, pod, false)

	gqm.updatePodCacheNoLock(in, pod, true)
	gqm.updatePodIsAssignedNoLock(in, pod, isAssigned)
	gqm.updatePodRequestNoLock(in, nil, pod)
	gqm.updatePodUsedNoLock(in, nil, pod)
	klog.V(5).Infof("migrate pod %v from quota %v to quota %v, podPhase: %v", pod.Name, out, in, pod.Status.Phase)
}

func (gqm *GroupQuotaManager) GetQuotaSummary(quotaName string, includePods bool) (*QuotaInfoSummary, bool) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	quotaInfo := gqm.getQuotaInfoByNameNoLock(quotaName)
	if quotaInfo == nil {
		return nil, false
	}

	quotaSummary := quotaInfo.GetQuotaSummary(includePods)
	runtime := gqm.RefreshRuntimeNoLock(quotaName)
	quotaSummary.Runtime = runtime.DeepCopy()
	return quotaSummary, true
}

func (gqm *GroupQuotaManager) GetQuotaSummaries(includePods bool) map[string]*QuotaInfoSummary {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	result := make(map[string]*QuotaInfoSummary)
	for quotaName, quotaInfo := range gqm.quotaInfoMap {
		// Skip koordinator-root-quota since it's an abstract entity
		if quotaName == extension.RootQuotaName {
			continue
		}
		quotaSummary := quotaInfo.GetQuotaSummary(includePods)
		runtime := gqm.RefreshRuntimeNoLock(quotaName)
		quotaSummary.Runtime = runtime.DeepCopy()
		quotaSummary.Tree = gqm.treeID
		result[quotaName] = quotaSummary
	}

	return result
}

func (gqm *GroupQuotaManager) OnPodAdd(quotaName string, pod *v1.Pod) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	quotaInfo := gqm.getQuotaInfoByNameNoLock(quotaName)
	if quotaInfo != nil && quotaInfo.IsPodExist(pod) {
		return
	}

	gqm.updatePodCacheNoLock(quotaName, pod, true)
	gqm.updatePodRequestNoLock(quotaName, nil, pod)
	// in case failOver, update pod isAssigned explicitly according to its phase and NodeName.
	if pod.Spec.NodeName != "" && !util.IsPodTerminated(pod) {
		gqm.updatePodIsAssignedNoLock(quotaName, pod, true)
		gqm.updatePodUsedNoLock(quotaName, nil, pod)
	}
}

func (gqm *GroupQuotaManager) OnPodUpdate(newQuotaName, oldQuotaName string, newPod, oldPod *v1.Pod) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	if oldQuotaName == newQuotaName {
		isAssigned := gqm.getPodIsAssignedNoLock(newQuotaName, newPod)
		if isAssigned {
			// reserve phase will assign the pod. Just update it.
			// upgrade will change the resource.
			gqm.updatePodUsedNoLock(newQuotaName, oldPod, newPod)
		} else {
			if newPod.Spec.NodeName != "" && !util.IsPodTerminated(newPod) {
				// assign it
				gqm.updatePodIsAssignedNoLock(newQuotaName, newPod, true)
				gqm.updatePodUsedNoLock(newQuotaName, nil, newPod)
			}
		}
		gqm.updatePodRequestNoLock(newQuotaName, oldPod, newPod)
	} else {
		isAssigned := gqm.getPodIsAssignedNoLock(oldQuotaName, oldPod)
		if isAssigned {
			gqm.updatePodUsedNoLock(oldQuotaName, oldPod, nil)
		}
		gqm.updatePodRequestNoLock(oldQuotaName, oldPod, nil)
		gqm.updatePodCacheNoLock(oldQuotaName, oldPod, false)

		gqm.updatePodCacheNoLock(newQuotaName, newPod, true)
		gqm.updatePodRequestNoLock(newQuotaName, nil, newPod)
		if newPod.Spec.NodeName != "" && !util.IsPodTerminated(newPod) {
			gqm.updatePodIsAssignedNoLock(newQuotaName, newPod, true)
			gqm.updatePodUsedNoLock(newQuotaName, nil, newPod)
		}
	}
}

func (gqm *GroupQuotaManager) OnPodDelete(quotaName string, pod *v1.Pod) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	gqm.updatePodRequestNoLock(quotaName, pod, nil)
	gqm.updatePodUsedNoLock(quotaName, pod, nil)
	gqm.updatePodCacheNoLock(quotaName, pod, false)
}

func (gqm *GroupQuotaManager) ReservePod(quotaName string, p *v1.Pod) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	gqm.updatePodIsAssignedNoLock(quotaName, p, true)
	gqm.updatePodUsedNoLock(quotaName, nil, p)
}

func (gqm *GroupQuotaManager) UnreservePod(quotaName string, p *v1.Pod) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	gqm.updatePodUsedNoLock(quotaName, p, nil)
	gqm.updatePodIsAssignedNoLock(quotaName, p, false)
}

func getPodName(oldPod, newPod *v1.Pod) string {
	if oldPod != nil {
		return oldPod.Name
	}
	if newPod != nil {
		return newPod.Name
	}
	return ""
}

func (gqm *GroupQuotaManager) OnNodeAdd(node *v1.Node) {
	gqm.nodeResourceMapLock.Lock()
	defer gqm.nodeResourceMapLock.Unlock()

	if _, ok := gqm.nodeResourceMap[node.Name]; ok {
		return
	}

	gqm.nodeResourceMap[node.Name] = struct{}{}
	gqm.UpdateClusterTotalResource(node.Status.Allocatable)
	klog.V(5).Infof("OnNodeAddFunc success %v", node.Name)
}

func (gqm *GroupQuotaManager) OnNodeUpdate(oldNode, newNode *v1.Node) {
	gqm.nodeResourceMapLock.Lock()
	defer gqm.nodeResourceMapLock.Unlock()

	if _, exist := gqm.nodeResourceMap[newNode.Name]; !exist {
		gqm.nodeResourceMap[newNode.Name] = struct{}{}
		gqm.UpdateClusterTotalResource(newNode.Status.Allocatable)
		return
	}

	oldNodeAllocatable := oldNode.Status.Allocatable
	newNodeAllocatable := newNode.Status.Allocatable
	if quotav1.Equals(oldNodeAllocatable, newNodeAllocatable) {
		return
	}

	deltaNodeAllocatable := quotav1.Subtract(newNodeAllocatable, oldNodeAllocatable)
	gqm.UpdateClusterTotalResource(deltaNodeAllocatable)
	klog.V(5).Infof("OnNodeUpdateFunc success, add resource :%v [%v]", newNode.Name, newNodeAllocatable)
}

func (gqm *GroupQuotaManager) OnNodeDelete(node *v1.Node) {
	gqm.nodeResourceMapLock.Lock()
	defer gqm.nodeResourceMapLock.Unlock()

	if _, exist := gqm.nodeResourceMap[node.Name]; !exist {
		return
	}

	delta := quotav1.Subtract(nil, node.Status.Allocatable)
	gqm.UpdateClusterTotalResource(delta)
	delete(gqm.nodeResourceMap, node.Name)
	klog.V(5).Infof("OnNodeDeleteFunc success: %v [%v]", node.Name, delta)
}

func (gqm *GroupQuotaManager) GetTreeID() string {
	return gqm.treeID
}

func (gqm *GroupQuotaManager) resetRootQuotaUsedAndRequest() {
	rootQuotaInfo := gqm.getQuotaInfoByNameNoLock(extension.RootQuotaName)
	rootQuotaInfo.lock.Lock()
	defer rootQuotaInfo.lock.Unlock()

	var used, request, nonPreemptUsed, nonPreemptRequest v1.ResourceList

	systemQuotaInfo := gqm.getQuotaInfoByNameNoLock(extension.SystemQuotaName)
	if systemQuotaInfo != nil {
		used = quotav1.Add(used, systemQuotaInfo.GetUsed())
		request = quotav1.Add(request, systemQuotaInfo.GetRequest())
		nonPreemptUsed = quotav1.Add(nonPreemptUsed, systemQuotaInfo.GetNonPreemptibleUsed())
		nonPreemptRequest = quotav1.Add(nonPreemptRequest, systemQuotaInfo.GetNonPreemptibleRequest())
	}

	defaultQuotaInfo := gqm.getQuotaInfoByNameNoLock(extension.DefaultQuotaName)
	if defaultQuotaInfo != nil {
		used = quotav1.Add(used, defaultQuotaInfo.GetUsed())
		request = quotav1.Add(request, defaultQuotaInfo.GetRequest())
		nonPreemptUsed = quotav1.Add(nonPreemptUsed, defaultQuotaInfo.GetNonPreemptibleUsed())
		nonPreemptRequest = quotav1.Add(nonPreemptRequest, defaultQuotaInfo.GetNonPreemptibleRequest())
	}

	rootQuotaInfo.CalculateInfo.Used = used
	rootQuotaInfo.CalculateInfo.Request = request
	rootQuotaInfo.CalculateInfo.NonPreemptibleUsed = nonPreemptUsed
	rootQuotaInfo.CalculateInfo.NonPreemptibleRequest = nonPreemptRequest
}

func (gqm *GroupQuotaManager) recursiveUpdateGroupTreeWithDeltaAllocated(deltaAllocated v1.ResourceList, curToAllParInfos []*QuotaInfo) {
	for i := 0; i < len(curToAllParInfos); i++ {
		curQuotaInfo := curToAllParInfos[i]
		oldGuaranteed := curQuotaInfo.CalculateInfo.Guaranteed
		curQuotaInfo.addAllocatedQuotaNoLock(deltaAllocated)
		if curQuotaInfo.Name == extension.RootQuotaName {
			return
		}

		// update the guarantee.
		guaranteed := curQuotaInfo.CalculateInfo.Allocated.DeepCopy()
		for r, q := range curQuotaInfo.CalculateInfo.Min {
			p, ok := guaranteed[r]
			if !ok {
				guaranteed[r] = q
				continue
			}
			if q.Cmp(p) == 1 {
				guaranteed[r] = q
			}
		}
		curQuotaInfo.CalculateInfo.Guaranteed = guaranteed

		directParRuntimeCalculatorPtr := gqm.getRuntimeQuotaCalculatorByNameNoLock(curQuotaInfo.ParentName)
		if directParRuntimeCalculatorPtr == nil {
			klog.Errorf("treeWrapper not exist! quotaName: %v, parentName: %v", curQuotaInfo.Name, curQuotaInfo.ParentName)
			return
		}
		if directParRuntimeCalculatorPtr.needUpdateOneGroupGuaranteed(curQuotaInfo) {
			directParRuntimeCalculatorPtr.updateOneGroupGuaranteed(curQuotaInfo)
		}

		deltaAllocated = quotav1.Subtract(guaranteed, oldGuaranteed)
	}
}

func (gqm *GroupQuotaManager) updateQuotaInternalNoLock(newQuotaInfo, oldQuotaInfo *QuotaInfo) {
	// max changed
	if !quotav1.Equals(newQuotaInfo.CalculateInfo.Max, oldQuotaInfo.CalculateInfo.Max) {
		gqm.doUpdateOneGroupMaxQuotaNoLock(newQuotaInfo.Name, newQuotaInfo.CalculateInfo.Max)
	}

	// min changed
	if !quotav1.Equals(newQuotaInfo.CalculateInfo.Min, oldQuotaInfo.CalculateInfo.Min) {
		gqm.doUpdateOneGroupMinQuotaNoLock(newQuotaInfo.Name, newQuotaInfo.CalculateInfo.Min)
	}

	// sharedweight changed
	if !quotav1.Equals(newQuotaInfo.CalculateInfo.SharedWeight, oldQuotaInfo.CalculateInfo.SharedWeight) {
		gqm.doUpdateOneGroupSharedWeightNoLock(newQuotaInfo.Name, newQuotaInfo.CalculateInfo.SharedWeight)
	}

}

func (gqm *GroupQuotaManager) doUpdateOneGroupMaxQuotaNoLock(quotaName string, newMax v1.ResourceList) {
	curToAllParInfos := gqm.getCurToAllParentGroupQuotaInfoNoLock(quotaName)
	quotaInfoLen := len(curToAllParInfos)
	if quotaInfoLen <= 0 {
		return
	}

	defer gqm.scopedLockForQuotaInfo(curToAllParInfos)()

	curQuotaInfo := curToAllParInfos[0]
	oldSubLimitReq := curQuotaInfo.getLimitRequestNoLock()
	curQuotaInfo.setMaxNoLock(newMax)

	if quotaInfoLen > 1 {
		parentRuntimeCalculator := gqm.getRuntimeQuotaCalculatorByNameNoLock(curQuotaInfo.ParentName)
		if parentRuntimeCalculator == nil {
			klog.Errorf("runtimeQuotaCalculator not exist! quotaName: %v, parentName: %v", curQuotaInfo.Name, curQuotaInfo.ParentName)
			return
		}
		parentRuntimeCalculator.updateOneGroupMaxQuota(curQuotaInfo)

		newSubLimitReq := curQuotaInfo.getLimitRequestNoLock()
		deltaRequest := quotav1.Subtract(newSubLimitReq, oldSubLimitReq)
		gqm.recursiveUpdateGroupTreeWithDeltaRequest(deltaRequest, nil, curToAllParInfos[1:])
	}
}

func (gqm *GroupQuotaManager) doUpdateOneGroupMinQuotaNoLock(quotaName string, newMin v1.ResourceList) {
	curToAllParInfos := gqm.getCurToAllParentGroupQuotaInfoNoLock(quotaName)
	quotaInfoLen := len(curToAllParInfos)
	if quotaInfoLen <= 0 {
		return
	}

	defer gqm.scopedLockForQuotaInfo(curToAllParInfos)()

	// Update quota info
	curQuotaInfo := curToAllParInfos[0]
	curQuotaInfo.setMinNoLock(newMin)
	curQuotaInfo.setAutoScaleMinQuotaNoLock(newMin)
	gqm.scaleMinQuotaManager.update(curQuotaInfo.ParentName, quotaName, newMin, gqm.scaleMinQuotaEnabled)

	// Update request. If the quota not allow to lent resource, the new min will effect the request.
	oldSubLimitReq := curQuotaInfo.getLimitRequestNoLock()
	realRequest := curQuotaInfo.CalculateInfo.ChildRequest.DeepCopy()
	if !curQuotaInfo.AllowLentResource {
		realRequest = quotav1.Max(realRequest, curQuotaInfo.CalculateInfo.Min)
	}
	curQuotaInfo.CalculateInfo.Request = realRequest

	if quotaInfoLen > 1 {
		// update parent runtime calculator for min changed
		parentRuntimeCalculator := gqm.getRuntimeQuotaCalculatorByNameNoLock(curQuotaInfo.ParentName)
		if parentRuntimeCalculator == nil {
			klog.Errorf("runtimeQuotaCalculator not exist! quotaName: %v, parentName: %v", curQuotaInfo.Name, curQuotaInfo.ParentName)
			return
		}
		parentRuntimeCalculator.updateOneGroupMinQuota(curQuotaInfo)

		newSubLimitReq := curQuotaInfo.getLimitRequestNoLock()
		deltaRequest := quotav1.Subtract(newSubLimitReq, oldSubLimitReq)
		gqm.recursiveUpdateGroupTreeWithDeltaRequest(deltaRequest, nil, curToAllParInfos[1:])
	}

	// update the guarantee.
	if utilfeature.DefaultFeatureGate.Enabled(features.ElasticQuotaGuaranteeUsage) {
		oldGuaranteed := curQuotaInfo.CalculateInfo.Guaranteed
		newGuaranteed := quotav1.Max(curQuotaInfo.CalculateInfo.Allocated, curQuotaInfo.CalculateInfo.Min)
		curQuotaInfo.CalculateInfo.Guaranteed = newGuaranteed

		if quotaInfoLen > 1 {
			parentRuntimeCalculator := gqm.getRuntimeQuotaCalculatorByNameNoLock(curQuotaInfo.ParentName)
			if parentRuntimeCalculator == nil {
				klog.Errorf("runtimeQuotaCalculator not exist! quotaName: %v, parentName: %v", curQuotaInfo.Name, curQuotaInfo.ParentName)
				return
			}
			if parentRuntimeCalculator.needUpdateOneGroupGuaranteed(curQuotaInfo) {
				parentRuntimeCalculator.updateOneGroupGuaranteed(curQuotaInfo)
			}
			deltaAllocated := quotav1.Subtract(newGuaranteed, oldGuaranteed)
			gqm.recursiveUpdateGroupTreeWithDeltaAllocated(deltaAllocated, curToAllParInfos[1:])
		}
	}
}

func (gqm *GroupQuotaManager) doUpdateOneGroupSharedWeightNoLock(quotaName string, newSharedWeight v1.ResourceList) {
	quotaInfo := gqm.getQuotaInfoByNameNoLock(quotaName)
	quotaInfo.setSharedWeightNoLock(newSharedWeight)

	gqm.updateOneGroupSharedWeightNoLock(quotaInfo)
}
