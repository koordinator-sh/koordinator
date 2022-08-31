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
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
)

type ScaleMinQuotaWhenOverRootResManager struct {
	lock sync.RWMutex
	// parentToEnableScaleSubsSumMinQuotaMap key: quotaName, val: sum of its enableScale children's minQuota
	parentToEnableScaleSubsSumMinQuotaMap map[string]v1.ResourceList
	// parentToDisableScaleSubsSumMinQuotaMap key: quotaName, val: sum of its disableScale children's minQuota
	parentToDisableScaleSubsSumMinQuotaMap map[string]v1.ResourceList
	// originalMinQuotaMap stores the original minQuota, when children's sum minQuota is smaller than the
	// totalRes, just return the originalMinQuota.
	originalMinQuotaMap         map[string]v1.ResourceList
	quotaEnableMinQuotaScaleMap map[string]bool
}

func NewScaleMinQuotaWhenOverRootResManager() *ScaleMinQuotaWhenOverRootResManager {
	info := &ScaleMinQuotaWhenOverRootResManager{
		originalMinQuotaMap:                    make(map[string]v1.ResourceList),
		parentToEnableScaleSubsSumMinQuotaMap:  make(map[string]v1.ResourceList),
		parentToDisableScaleSubsSumMinQuotaMap: make(map[string]v1.ResourceList),
		quotaEnableMinQuotaScaleMap:            make(map[string]bool),
	}
	return info
}

func (info *ScaleMinQuotaWhenOverRootResManager) Update(parQuotaName, subQuotaName string, subMinQuota v1.ResourceList, enableScaleMinQuota bool) {
	info.lock.Lock()
	defer info.lock.Unlock()

	if _, ok := info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName]; !ok {
		info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName] = v1.ResourceList{}
	}
	if _, ok := info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName]; !ok {
		info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName] = v1.ResourceList{}
	}

	// step1: delete the oldMinQuota if present
	if enable, ok := info.quotaEnableMinQuotaScaleMap[subQuotaName]; ok {
		if enable {
			info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName] = quotav1.SubtractWithNonNegativeResult(info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName],
				info.originalMinQuotaMap[subQuotaName])
		} else {
			info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName] = quotav1.SubtractWithNonNegativeResult(info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName],
				info.originalMinQuotaMap[subQuotaName])
		}
	}

	// step2: add the newMinQuota
	if enableScaleMinQuota {
		info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName] = quotav1.Add(info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName], subMinQuota)
	} else {
		info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName] = quotav1.Add(info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName], subMinQuota)
	}

	klog.V(5).Infof("UpdateScaleMinQuota, quota:%v originalMinQuota change from :%v to %v,"+
		"enableMinQuotaScale change from :%v to :%v", subQuotaName, info.originalMinQuotaMap[subQuotaName],
		subMinQuota, info.quotaEnableMinQuotaScaleMap[subQuotaName], enableScaleMinQuota)
	// step3: record the newMinQuota
	info.originalMinQuotaMap[subQuotaName] = subMinQuota
	info.quotaEnableMinQuotaScaleMap[subQuotaName] = enableScaleMinQuota
}

func (info *ScaleMinQuotaWhenOverRootResManager) Delete(parQuotaName, subQuotaName string) {
	info.lock.Lock()
	defer info.lock.Unlock()

	if info.quotaEnableMinQuotaScaleMap[subQuotaName] == true {
		if _, ok := info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName]; ok {
			info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName] = quotav1.SubtractWithNonNegativeResult(
				info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName], info.originalMinQuotaMap[subQuotaName])
		}
	} else {
		if _, ok := info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName]; ok {
			info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName] = quotav1.SubtractWithNonNegativeResult(
				info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName], info.originalMinQuotaMap[subQuotaName])
		}
	}

	delete(info.originalMinQuotaMap, subQuotaName)
	delete(info.quotaEnableMinQuotaScaleMap, subQuotaName)
	klog.V(5).Infof("DeleteScaleMinQuota quota:%v originalMnQuota:%v, enableMinQuotaScale:%v",
		subQuotaName, info.originalMinQuotaMap[subQuotaName], info.quotaEnableMinQuotaScaleMap[subQuotaName])
}

func (info *ScaleMinQuotaWhenOverRootResManager) GetScaledMinQuota(newTotalRes v1.ResourceList, parQuotaName, subQuotaName string) (bool, v1.ResourceList) {
	info.lock.Lock()
	defer info.lock.Unlock()

	if newTotalRes == nil || info.originalMinQuotaMap[subQuotaName] == nil {
		return false, nil
	}
	if info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName] == nil || info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName] == nil {
		return false, nil
	}

	if !info.quotaEnableMinQuotaScaleMap[subQuotaName] {
		return false, nil
	}

	// get the dimensions where children's minQuota sum is larger than newTotalRes
	needScaleDimensions := make([]v1.ResourceName, 0)
	for resName := range newTotalRes {
		sum := quotav1.Add(info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName], info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName])
		if newTotalRes.Name(resName, resource.DecimalSI).Cmp(*sum.Name(resName, resource.DecimalSI)) == -1 {
			needScaleDimensions = append(needScaleDimensions, resName)
		}
	}

	//  children's minQuota sum is smaller than totalRes in all dimensions
	if len(needScaleDimensions) == 0 {
		return true, info.originalMinQuotaMap[subQuotaName].DeepCopy()
	}

	// ensure the disableScale children's minQuota first
	newMinQuota := info.originalMinQuotaMap[subQuotaName].DeepCopy()
	for _, resourceDimension := range needScaleDimensions {
		needScaleTotal := *newTotalRes.Name(resourceDimension, resource.DecimalSI)
		disableTotal := info.parentToDisableScaleSubsSumMinQuotaMap[parQuotaName]
		needScaleTotal.Sub(*disableTotal.Name(resourceDimension, resource.DecimalSI))

		if needScaleTotal.Value() <= 0 {
			newMinQuota[resourceDimension] = *resource.NewQuantity(0, resource.DecimalSI)
		} else {
			// if still has minQuota left, enableScaleMinQuota children partition it according to their minQuotaValue.
			originalMinQuota := info.originalMinQuotaMap[subQuotaName]
			originalMinQuotaValue := originalMinQuota.Name(resourceDimension, resource.DecimalSI)

			enableTotal := info.parentToEnableScaleSubsSumMinQuotaMap[parQuotaName]
			enableTotalValue := enableTotal.Name(resourceDimension, resource.DecimalSI)

			newMinQuotaValue := int64(0)
			if enableTotalValue.Value() > 0 {
				newMinQuotaValue = int64(float64(needScaleTotal.Value()) *
					float64(originalMinQuotaValue.Value()) / float64(enableTotalValue.Value()))
			}

			newMinQuota[resourceDimension] = *resource.NewQuantity(newMinQuotaValue, resource.DecimalSI)
		}
	}
	return true, newMinQuota
}
