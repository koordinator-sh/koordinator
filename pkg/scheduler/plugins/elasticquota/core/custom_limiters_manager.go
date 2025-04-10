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

	"golang.org/x/exp/maps"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type CustomState struct {
	customUsed      CustomResourceLists
	parentQuotaName string
	// key: customKey, value: enabled leaf quota names
	enabledQuotaNames map[string]sets.Set[string]
	// topNode represents the top-level node in the quota tree, which is a direct child of the root node.
	topNode *QuotaTopoNode
}

func NewCustomState(quotaInfo *QuotaInfo, topNode *QuotaTopoNode) *CustomState {
	state := &CustomState{
		topNode:           topNode,
		enabledQuotaNames: make(map[string]sets.Set[string]),
	}
	if quotaInfo != nil {
		quotaInfo.lock.Lock()
		state.parentQuotaName = quotaInfo.ParentName
		state.customUsed = quotaInfo.GetCustomUsedNoLock()
		quotaInfo.lock.Unlock()
	}
	return state
}

func (cs *CustomState) setEnabledQuotaNames(customKey string, enabledNames sets.Set[string]) {
	cs.enabledQuotaNames[customKey] = enabledNames
}

func (cs *CustomState) withTopNode(topNode *QuotaTopoNode) *CustomState {
	cs.topNode = topNode
	return cs
}

func (cs *CustomState) deepEquals(other *CustomState) bool {
	if cs == nil || other == nil {
		return cs == other
	}
	if len(cs.enabledQuotaNames) != len(other.enabledQuotaNames) {
		return false
	}
	for customKey, enabledLeafNames := range cs.enabledQuotaNames {
		otherEnabledLeafNames, ok := other.enabledQuotaNames[customKey]
		if !ok {
			return false
		}
		if !enabledLeafNames.Equal(otherEnabledLeafNames) {
			return false
		}
	}
	return true
}

type CustomLimitersManager struct {
	customLimiters map[string]CustomLimiter
}

func NewCustomLimitersManager(customLimiters map[string]CustomLimiter) *CustomLimitersManager {
	return &CustomLimitersManager{
		customLimiters: customLimiters,
	}
}

func (clm *CustomLimitersManager) enabled() bool {
	if clm == nil {
		return false
	}
	return len(clm.customLimiters) > 0
}

// Notice: the following methods must be called while holding hierarchyUpdateLock of group-quota-manager
// and the locks of all the quota chain.

// updateGroupCustomUsedWhenPodChanged updates the custom-used resource for the entire quota chain when a pod changes.
// customKeysOpt is used to filter custom limiters, if it is nil, all custom limiters will be used.
func (clm *CustomLimitersManager) updateGroupCustomUsedWhenPodChanged(curToAllParInfos []*QuotaInfo,
	oldPod, newPod *v1.Pod, customKeysOpt sets.Set[string]) {
	enabledCustomKeys := sets.New[string](curToAllParInfos[0].GetEnabledCustomKeysNoLock()...)
	if customKeysOpt != nil {
		// filter custom keys by non-nil customKeysOpt
		enabledCustomKeys = enabledCustomKeys.Intersection(customKeysOpt)
	}
	if len(enabledCustomKeys) == 0 {
		return
	}
	// calculate and update custom-used by custom limiters
	for _, customKey := range maps.Keys(enabledCustomKeys) {
		customLimiter := clm.customLimiters[customKey]
		if customLimiter == nil {
			// should not happen
			klog.Errorf("custom-limiter %s not found", customKey)
			continue
		}
		// calculate used-delta resource
		var usedDelta v1.ResourceList
		state := NewCustomLimiterState()
		for _, quotaInfo := range curToAllParInfos {
			if quotaInfo.Name == extension.RootQuotaName {
				break
			}
			// calculate used-delta from pod only for the lowest quota with configured limit in this quota chain.
			usedDelta = customLimiter.CalculatePodUsedDelta(quotaInfo, newPod, oldPod, state)
			// skip updating used if no configured limit (used-delta is nil)
			if usedDelta == nil {
				continue
			}
			break
		}
		// skip updating custom-used resource if used-delta is zero
		if quotav1.IsZero(usedDelta) {
			continue
		}
		// update custom-used resource for this quota chain
		for _, quotaInfo := range curToAllParInfos {
			if quotaInfo.Name == extension.RootQuotaName {
				break
			}
			curUsed := quotaInfo.CalculateInfo.CustomUsed[customKey]
			newUsed := quotav1.Add(curUsed, usedDelta)
			quotaInfo.CalculateInfo.CustomUsed[customKey] = newUsed
			if klog.V(4).Enabled() {
				klog.Infof("updated custom-used resource for quota %v, pod=%v, customKey=%v, usedDelta=%v, newUsed=%v",
					quotaInfo.Name, GetPodKey(newPod, oldPod), customKey, util.PrintResourceList(usedDelta),
					util.PrintResourceList(newUsed))
			}
		}
	}
}

// updateGroupCustomUsedWhenQuotaChanged updates custom-used resource for all quota chain when quota changed.
func (clm *CustomLimitersManager) updateGroupCustomUsedWhenQuotaChanged(curToAllParInfos []*QuotaInfo,
	customUsedDeltas CustomResourceLists) {
	// update custom used by custom limiters
	for customKey, usedDelta := range customUsedDeltas {
		customLimiter := clm.customLimiters[customKey]
		if customLimiter == nil {
			// should not happen
			klog.Warningf("custom-limiter %s not found", customKey)
			continue
		}
		// skip unnecessary update
		if usedDelta == nil || quotav1.IsZero(usedDelta) {
			continue
		}
		for _, quotaInfo := range curToAllParInfos {
			if quotaInfo.Name == extension.RootQuotaName {
				break
			}
			if !quotaInfo.IsEnabledCustomKeyNoLock(customKey) {
				// The update progress shouldn't be broken since
				// enabled state of specified custom-limiter may be discontinuous for a quota chain,
				// For example:
				// quota1									mock-used[1,1]
				//   |-- quota11							mock-used[1,1]
				//       |-- quota111	mock-limit[10,10]	mock-used[1,1]
				//   |-- quota12		mock-limit[10,10] 	mock-used[]
				// quota2
				//
				// If we move quota111 from quota11 to quota2,
				// custom-limiter mock will be disabled for quota11, but enabled for quota1.
				// quota11 need to be skipped, so that custom-used resource of quota1 can be recalculated.
				//
				// After the moving, expected quotas should be as following:
				// quota1									mock-used[0,0]
				//   |-- quota11
				//   |-- quota12		mock-limit[10,10] 	mock-used[]
				// quota2									mock-used[1,1]
				//   |-- quota111		mock-limit[10,10]	mock-used[1,1]
				continue
			}
			curUsed := quotaInfo.CalculateInfo.CustomUsed[customKey]
			newUsed := quotav1.Add(curUsed, usedDelta)
			quotaInfo.CalculateInfo.CustomUsed[customKey] = newUsed
			if klog.V(5).Enabled() {
				klog.Infof(
					"updated custom-used for quota %v when quota changed, customKey=%v, usedDelta=%v, newUsed=%v",
					quotaInfo.Name, customKey, util.PrintResourceList(usedDelta), util.PrintResourceList(newUsed))
			}
		}
	}
}

// Notice: the following methods must be called while holding hierarchyUpdateLock of group-quota-manager
// and without holding locks of the related quotaInfo.

// getToBeUpdateCustomInfo returns the custom limits that need to be updated for the given quotaInfo.
func (clm *CustomLimitersManager) getToBeUpdateCustomInfo(oldQuotaInfo, newQuotaInfo *QuotaInfo) (
	newCustomLimits CustomLimitConfMap, toBeUpdatedKeys []string) {
	// check if custom conf changed
	for key := range clm.customLimiters {
		newLimitConf := newQuotaInfo.GetCustomLimitConf(key)
		var oldLimitConf *CustomLimitConf
		if oldQuotaInfo != nil {
			oldLimitConf = oldQuotaInfo.GetCustomLimitConf(key)
		}
		if !customLimitConfEquals(oldLimitConf, newLimitConf) {
			toBeUpdatedKeys = append(toBeUpdatedKeys, key)
		}
	}
	// skip updating if no custom conf changed
	if len(toBeUpdatedKeys) == 0 {
		return
	}
	newCustomLimits = newQuotaInfo.GetCustomLimitConfs()
	return
}

// parseCustomState parses the custom state for the given quotaInfo.
// quotaInfo - quota-info of the updated quota, could be nil when fetching the old state for newly added quota.
// quotaTopoNode - top topology node of the updated quota, must be a child of root quota.
func (clm *CustomLimitersManager) parseCustomState(quotaInfo *QuotaInfo, quotaTopoNode *QuotaTopoNode) *CustomState {
	if quotaTopoNode == nil {
		return nil
	}
	state := NewCustomState(quotaInfo, quotaTopoNode)
	for customKey := range clm.customLimiters {
		enabledNames := sets.Set[string]{}
		clm.parseCustomStateRecursive(quotaTopoNode, customKey, false, enabledNames)
		state.setEnabledQuotaNames(customKey, enabledNames)
	}
	return state
}

func (clm *CustomLimitersManager) parseCustomStateRecursive(topoNode *QuotaTopoNode,
	customKey string, parentEnabled bool, enabledNames sets.Set[string]) bool {
	currentEnabled := parentEnabled || topoNode.quotaInfo.HasCustomLimit(customKey)
	var childAnyEnabled bool
	for _, subNode := range topoNode.getChildGroupQuotaInfos() {
		childEnabled := clm.parseCustomStateRecursive(subNode, customKey, currentEnabled, enabledNames)
		childAnyEnabled = childEnabled || childAnyEnabled
	}
	currentEnabled = currentEnabled || childAnyEnabled
	if currentEnabled {
		enabledNames.Insert(topoNode.name)
	}
	return currentEnabled
}

// Notice: the following methods do not require any locks.

func (clm *CustomLimitersManager) attachCustomLimitsInfo(quotaInfo *QuotaInfo, quota *v1alpha1.ElasticQuota) error {
	customLimitConfs := make(CustomLimitConfMap)
	var errs []error
	for key, limiter := range clm.customLimiters {
		limitConf, err := limiter.GetLimitConf(quota)
		if err != nil {
			errs = append(errs, fmt.Errorf("key=%s, err=%v", key, err))
			continue
		}
		// skip if no limit configured
		if limitConf == nil || limitConf.Limit == nil {
			continue
		}
		customLimitConfs[key] = limitConf
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to parse custom limits or arguments for quota %s, err=%s",
			quota.Name, errors.NewAggregate(errs).Error())
	}
	if len(customLimitConfs) > 0 {
		quotaInfo.setCustomLimitsNoLock(customLimitConfs)
	}
	return nil
}

func (clm *CustomLimitersManager) parseToBeUpdatedQuotaNames(oldState, newState *CustomState) (
	toBeUpdatedQuotaNames map[string]map[string]bool) {
	if oldState == nil && newState == nil {
		return
	}
	toBeUpdatedQuotaNames = make(map[string]map[string]bool)
	for customKey := range clm.customLimiters {
		var oldEnabledQuotaNames, newEnabledQuotaNames sets.Set[string]
		if oldState != nil {
			oldEnabledQuotaNames = oldState.enabledQuotaNames[customKey]
		}
		if newState != nil {
			newEnabledQuotaNames = newState.enabledQuotaNames[customKey]
		}
		if oldEnabledQuotaNames == nil && newEnabledQuotaNames == nil {
			continue
		}
		// record the to-be-updated quota names
		// disabled: exist in old state -> not exist in new state
		// enabled: not exist in old state -> exist in new state
		var toBeDisabledQuotaNames, toBeEnabledQuotaNames sets.Set[string]
		if oldEnabledQuotaNames != nil {
			toBeDisabledQuotaNames = oldEnabledQuotaNames.Difference(newEnabledQuotaNames)
		}
		if newEnabledQuotaNames != nil {
			toBeEnabledQuotaNames = newEnabledQuotaNames.Difference(oldEnabledQuotaNames)
		}
		if len(toBeDisabledQuotaNames) == 0 && len(toBeEnabledQuotaNames) == 0 {
			continue
		}
		curToBeUpdatedQuotaNames := make(map[string]bool)
		for quotaName := range toBeDisabledQuotaNames {
			curToBeUpdatedQuotaNames[quotaName] = false
		}
		for quotaName := range toBeEnabledQuotaNames {
			curToBeUpdatedQuotaNames[quotaName] = true
		}
		toBeUpdatedQuotaNames[customKey] = curToBeUpdatedQuotaNames
	}
	return
}
