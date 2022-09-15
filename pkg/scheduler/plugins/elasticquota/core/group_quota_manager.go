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
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/api/v1/resource"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
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
}

func NewGroupQuotaManager(systemGroupMax, defaultGroupMax v1.ResourceList) *GroupQuotaManager {
	quotaManager := &GroupQuotaManager{
		totalResourceExceptSystemAndDefaultUsed: v1.ResourceList{},
		totalResource:                           v1.ResourceList{},
		resourceKeys:                            make(map[v1.ResourceName]struct{}),
		quotaInfoMap:                            make(map[string]*QuotaInfo),
		runtimeQuotaCalculatorMap:               make(map[string]*RuntimeQuotaCalculator),
		quotaTopoNodeMap:                        make(map[string]*QuotaTopoNode),
		scaleMinQuotaManager:                    NewScaleMinQuotaManager(),
	}
	quotaManager.quotaInfoMap[extension.SystemQuotaName] = NewQuotaInfo(false, true, extension.SystemQuotaName, extension.RootQuotaName)
	quotaManager.quotaInfoMap[extension.SystemQuotaName].setMaxQuotaNoLock(systemGroupMax)
	quotaManager.quotaInfoMap[extension.DefaultQuotaName] = NewQuotaInfo(false, true, extension.DefaultQuotaName, extension.RootQuotaName)
	quotaManager.quotaInfoMap[extension.DefaultQuotaName].setMaxQuotaNoLock(defaultGroupMax)
	quotaManager.runtimeQuotaCalculatorMap[extension.RootQuotaName] = NewRuntimeQuotaCalculator(extension.RootQuotaName)
	quotaManager.setScaleMinQuotaEnabled(true)
	return quotaManager
}

func (gqm *GroupQuotaManager) setScaleMinQuotaEnabled(flag bool) {
	gqm.hierarchyUpdateLock.Lock()
	defer gqm.hierarchyUpdateLock.Unlock()

	gqm.scaleMinQuotaEnabled = flag
	klog.V(3).Infof("Set ScaleMinQuotaEnabled, flag:%v", gqm.scaleMinQuotaEnabled)
}

func (gqm *GroupQuotaManager) UpdateClusterTotalResource(deltaRes v1.ResourceList) {
	gqm.hierarchyUpdateLock.Lock()
	defer gqm.hierarchyUpdateLock.Unlock()

	klog.V(3).Infof("UpdateClusterResource deltaRes:%v", deltaRes)
	gqm.updateClusterTotalResourceNoLock(deltaRes)
}

func (gqm *GroupQuotaManager) updateClusterTotalResourceNoLock(deltaRes v1.ResourceList) {
	gqm.totalResource = quotav1.Add(gqm.totalResource, deltaRes)

	sysAndDefaultUsed := gqm.quotaInfoMap[extension.DefaultQuotaName].GetUsed()
	sysAndDefaultUsed = quotav1.Add(sysAndDefaultUsed, gqm.quotaInfoMap[extension.SystemQuotaName].GetUsed())
	totalResNoSysOrDefault := quotav1.Subtract(gqm.totalResource, sysAndDefaultUsed)

	diffRes := quotav1.Subtract(totalResNoSysOrDefault, gqm.totalResourceExceptSystemAndDefaultUsed)

	if !quotav1.IsZero(diffRes) {
		gqm.totalResourceExceptSystemAndDefaultUsed = totalResNoSysOrDefault.DeepCopy()
		gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].setClusterTotalResource(totalResNoSysOrDefault)
		klog.V(3).Infof("UpdateClusterResource finish totalResourceExceptSystemAndDefaultUsed:%v", gqm.totalResourceExceptSystemAndDefaultUsed)
	}
}

func (gqm *GroupQuotaManager) GetClusterTotalResource() v1.ResourceList {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	return gqm.totalResource.DeepCopy()
}

func (gqm *GroupQuotaManager) UpdateGroupDeltaRequest(quotaName string, deltaReq v1.ResourceList) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	gqm.updateGroupDeltaRequestNoLock(quotaName, deltaReq)
}

// updateGroupDeltaRequestNoLock no need lock gqm.lock
func (gqm *GroupQuotaManager) updateGroupDeltaRequestNoLock(quotaName string, deltaReq v1.ResourceList) {
	curToAllParInfos := gqm.getCurToAllParentGroupQuotaInfoNoLock(quotaName)
	allQuotaInfoLen := len(curToAllParInfos)
	if allQuotaInfoLen <= 0 {
		return
	}

	defer gqm.scopedLockForQuotaInfo(curToAllParInfos)()

	gqm.updateGroupDeltaRequestTopoRecursiveNoLock(deltaReq, curToAllParInfos)
}

// updateGroupDeltaRequestTopoRecursiveNoLock update the quota of a node, also need update all parentNode, the lock operation
// of all quotaInfo is done by gqm. scopedLockForQuotaInfo, so just get treeWrappers' lock when calling treeWrappers' function
func (gqm *GroupQuotaManager) updateGroupDeltaRequestTopoRecursiveNoLock(deltaReq v1.ResourceList, curToAllParInfos []*QuotaInfo) {
	for i := 0; i < len(curToAllParInfos); i++ {
		curQuotaInfo := curToAllParInfos[i]
		directParRuntimeCalculatorPtr := gqm.getRuntimeQuotaCalculatorByNameNoLock(curQuotaInfo.ParentName)
		if directParRuntimeCalculatorPtr == nil {
			klog.Errorf("treeWrapper not exist! quotaName:%v  parentName:%v", curQuotaInfo.Name, curQuotaInfo.ParentName)
			return
		}
		oldSubLimitReq := curQuotaInfo.getLimitRequestNoLock()
		curQuotaInfo.addRequestNonNegativeNoLock(deltaReq)
		newSubLimitReq := curQuotaInfo.getLimitRequestNoLock()
		deltaReq = quotav1.Subtract(newSubLimitReq, oldSubLimitReq)

		if directParRuntimeCalculatorPtr.needUpdateOneGroupRequest(curQuotaInfo) {
			directParRuntimeCalculatorPtr.updateOneGroupRequest(curQuotaInfo)
		}
	}
}

func (gqm *GroupQuotaManager) UpdateGroupDeltaUsed(quotaName string, delta v1.ResourceList) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	gqm.updateGroupDeltaUsedNoLock(quotaName, delta)

	// if systemQuotaGroup or DefaultQuotaGroup's used change, update cluster total resource.
	if quotaName == extension.SystemQuotaName || quotaName == extension.DefaultQuotaName {
		gqm.updateClusterTotalResourceNoLock(v1.ResourceList{})
	}
}

// updateGroupDeltaUsedNoLock updates the usedQuota of a node, it also updates all parent nodes
// no need to lock gqm.lock
func (gqm *GroupQuotaManager) updateGroupDeltaUsedNoLock(quotaName string, delta v1.ResourceList) {
	curToAllParInfos := gqm.getCurToAllParentGroupQuotaInfoNoLock(quotaName)
	allQuotaInfoLen := len(curToAllParInfos)
	if allQuotaInfoLen <= 0 {
		return
	}

	defer gqm.scopedLockForQuotaInfo(curToAllParInfos)()
	for i := 0; i < allQuotaInfoLen; i++ {
		quotaInfo := curToAllParInfos[i]
		quotaInfo.addUsedNonNegativeNoLock(delta)
	}
}

func (gqm *GroupQuotaManager) RefreshRuntime(quotaName string) v1.ResourceList {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	return gqm.RefreshRuntimeNoLock(quotaName)
}

func (gqm *GroupQuotaManager) RefreshRuntimeNoLock(quotaName string) v1.ResourceList {
	quotaInfo := gqm.GetQuotaInfoByNameNoLock(quotaName)
	if quotaInfo == nil {
		return nil
	}

	if quotaName == extension.SystemQuotaName || quotaName == extension.DefaultQuotaName {
		return quotaInfo.getMax()
	}

	curToAllParInfos := gqm.getCurToAllParentGroupQuotaInfoNoLock(quotaInfo.Name)

	defer gqm.scopedLockForQuotaInfo(curToAllParInfos)()

	totalRes := gqm.totalResourceExceptSystemAndDefaultUsed.DeepCopy()
	for i := len(curToAllParInfos) - 1; i >= 0; i-- {
		quotaInfo = curToAllParInfos[i]
		parRuntimeQuotaCalculator := gqm.getRuntimeQuotaCalculatorByNameNoLock(quotaInfo.ParentName)
		if parRuntimeQuotaCalculator == nil {
			klog.Errorf("treeWrapper not exist! parentQuotaName:%v", quotaInfo.ParentName)
			return nil
		}
		subTreeWrapper := gqm.getRuntimeQuotaCalculatorByNameNoLock(quotaInfo.Name)
		if subTreeWrapper == nil {
			klog.Errorf("treeWrapper not exist! parentQuotaName:%v", quotaInfo.Name)
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
	quotaInfo := gqm.GetQuotaInfoByNameNoLock(quotaName)
	if quotaInfo == nil {
		return curToAllParInfos
	}

	for true {
		curToAllParInfos = append(curToAllParInfos, quotaInfo)
		if quotaInfo.ParentName == extension.RootQuotaName {
			break
		}

		quotaInfo = gqm.GetQuotaInfoByNameNoLock(quotaInfo.ParentName)
		if quotaInfo == nil {
			return curToAllParInfos
		}
	}

	return curToAllParInfos
}

func (gqm *GroupQuotaManager) GetQuotaInfoByName(quotaName string) *QuotaInfo {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	return gqm.GetQuotaInfoByNameNoLock(quotaName)
}

func (gqm *GroupQuotaManager) GetQuotaInfoByNameNoLock(quotaName string) *QuotaInfo {
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
			// if the quotaMeta doesn't change, only runtime/used/request change causes update,
			// no need to call updateQuotaGroupConfigNoLock.
			if !localQuotaInfo.isQuotaMetaChange(newQuotaInfo) {
				return nil
			}
			localQuotaInfo.updateQuotaInfoFromRemote(newQuotaInfo)
		} else {
			gqm.quotaInfoMap[quotaName] = newQuotaInfo
		}
	}
	gqm.updateQuotaGroupConfigNoLock()

	return nil
}

func (gqm *GroupQuotaManager) updateQuotaGroupConfigNoLock() {
	// rebuild gqm.quotaTopoNodeMap
	gqm.buildSubParGroupTopoNoLock()
	// reset gqm.runtimeQuotaCalculator
	gqm.resetAllGroupQuotaNoLock()
}

//BuildSubParGroupTopoNoLock reBuild a nodeTree from root, no need to lock gqm.lock
func (gqm *GroupQuotaManager) buildSubParGroupTopoNoLock() {
	//rebuild QuotaTopoNodeMap
	gqm.quotaTopoNodeMap = make(map[string]*QuotaTopoNode)
	rootNode := NewQuotaTopoNode(NewQuotaInfo(false, true, extension.RootQuotaName, extension.RootQuotaName))
	gqm.quotaTopoNodeMap[extension.RootQuotaName] = rootNode

	// add node according to the quotaInfoMap
	for quotaName, quotaInfo := range gqm.quotaInfoMap {
		if quotaName == extension.SystemQuotaName || quotaName == extension.DefaultQuotaName {
			continue
		}
		gqm.quotaTopoNodeMap[quotaName] = NewQuotaTopoNode(quotaInfo)
	}

	// build tree according to the parGroupName
	for _, topoNode := range gqm.quotaTopoNodeMap {
		if topoNode.name == extension.RootQuotaName {
			continue
		}
		parQuotaTopoNode := gqm.quotaTopoNodeMap[topoNode.quotaInfo.ParentName]
		topoNode.parQuotaTopoNode = parQuotaTopoNode
		parQuotaTopoNode.addChildGroupQuotaInfo(topoNode)
	}
}

// ResetAllGroupQuotaNoLock no need to lock gqm.lock
func (gqm *GroupQuotaManager) resetAllGroupQuotaNoLock() {
	childRequestMap, childUsedMap := make(quotaResMapType), make(quotaResMapType)
	for quotaName, topoNode := range gqm.quotaTopoNodeMap {
		if quotaName == extension.RootQuotaName {
			continue
		}
		topoNode.quotaInfo.lock.Lock()
		if !topoNode.quotaInfo.IsParent {
			childRequestMap[quotaName] = topoNode.quotaInfo.CalculateInfo.Request.DeepCopy()
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
			gqm.updateGroupDeltaRequestNoLock(quotaName, childRequestMap[quotaName])
			gqm.updateGroupDeltaUsedNoLock(quotaName, childUsedMap[quotaName])
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

//updateOneGroupMaxQuotaNoLock no need to lock gqm.lock
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
		quotaInfo.CalculateInfo.OriginalMin, gqm.scaleMinQuotaEnabled)
}

// updateOneGroupOriginalMinQuotaNoLock no need to lock gqm.lock
func (gqm *GroupQuotaManager) updateOneGroupOriginalMinQuotaNoLock(quotaInfo *QuotaInfo) {
	quotaInfo.lock.Lock()
	defer quotaInfo.lock.Unlock()

	quotaInfo.setAutoScaleMinQuotaNoLock(quotaInfo.CalculateInfo.OriginalMin)
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

func (gqm *GroupQuotaManager) UpdatePodRequest(quotaName string, oldPod, newPod *v1.Pod) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	var oldPodReq, newPodReq v1.ResourceList
	if oldPod != nil {
		oldPodReq, _ = resource.PodRequestsAndLimits(oldPod)
	} else {
		oldPodReq = make(v1.ResourceList)
	}

	if newPod != nil {
		newPodReq, _ = resource.PodRequestsAndLimits(newPod)
	} else {
		newPodReq = make(v1.ResourceList)
	}

	deltaReq := quotav1.Subtract(newPodReq, oldPodReq)
	if quotav1.IsZero(deltaReq) {
		return
	}
	gqm.updateGroupDeltaRequestNoLock(quotaName, deltaReq)
}

func (gqm *GroupQuotaManager) UpdatePodUsed(quotaName string, oldPod, newPod *v1.Pod) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	quotaInfo := gqm.GetQuotaInfoByNameNoLock(quotaName)
	if !quotaInfo.GetPodIsAssigned(newPod) && !quotaInfo.GetPodIsAssigned(oldPod) {
		return
	}

	var oldPodUsed, newPodUsed v1.ResourceList
	if oldPod != nil {
		oldPodUsed, _ = resource.PodRequestsAndLimits(oldPod)
	} else {
		oldPodUsed = make(v1.ResourceList)
	}

	if newPod != nil {
		newPodUsed, _ = resource.PodRequestsAndLimits(newPod)
	} else {
		newPodUsed = make(v1.ResourceList)
	}

	deltaUsed := quotav1.Subtract(newPodUsed, oldPodUsed)
	if quotav1.IsZero(deltaUsed) {
		return
	}
	gqm.updateGroupDeltaUsedNoLock(quotaName, deltaUsed)
}

func (gqm *GroupQuotaManager) UpdatePodCache(quotaName string, pod *v1.Pod, isAdd bool) {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	quotaInfo := gqm.GetQuotaInfoByNameNoLock(quotaName)

	if isAdd {
		quotaInfo.AddPodIfNotPresent(pod)
	} else {
		quotaInfo.RemovePodIfPreSent(pod.Name)
	}
}

func (gqm *GroupQuotaManager) UpdatePodIsAssigned(quotaName string, pod *v1.Pod, isAssigned bool) error {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	quotaInfo := gqm.GetQuotaInfoByNameNoLock(quotaName)
	return quotaInfo.UpdatePodIsAssigned(pod.Name, isAssigned)
}

func (gqm *GroupQuotaManager) GetPodIsAssigned(quotaName string, pod *v1.Pod) bool {
	gqm.hierarchyUpdateLock.RLock()
	defer gqm.hierarchyUpdateLock.RUnlock()

	quotaInfo := gqm.GetQuotaInfoByNameNoLock(quotaName)
	return quotaInfo.GetPodIsAssigned(pod)
}

func (gqm *GroupQuotaManager) RLock() {
	gqm.hierarchyUpdateLock.RLock()
}

func (gqm *GroupQuotaManager) RUnLock() {
	gqm.hierarchyUpdateLock.RUnlock()
}
