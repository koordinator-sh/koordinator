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

package resmanager

import (
	"encoding/json"

	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

// mergeSLOSpecResourceUsedThresholdWithBE merges the nodeSLO ResourceUsedThresholdWithBE with default configs
func mergeSLOSpecResourceUsedThresholdWithBE(defaultSpec, newSpec *slov1alpha1.ResourceThresholdStrategy) *slov1alpha1.ResourceThresholdStrategy {
	spec := &slov1alpha1.ResourceThresholdStrategy{}
	if newSpec != nil {
		spec = newSpec
	}
	// ignore err for serializing/deserializing the same struct type
	data, _ := json.Marshal(spec)
	// NOTE: use deepcopy to avoid a overwrite to the global default
	out := defaultSpec.DeepCopy()
	_ = json.Unmarshal(data, &out)
	return out
}

func mergeSLOSpecResourceQoSStrategy(defaultSpec,
	newSpec *slov1alpha1.ResourceQoSStrategy) *slov1alpha1.ResourceQoSStrategy {
	spec := &slov1alpha1.ResourceQoSStrategy{}
	if newSpec != nil {
		spec = newSpec
	}
	// ignore err for serializing/deserializing the same struct type
	data, _ := json.Marshal(spec)
	// NOTE: use deepcopy to avoid a overwrite to the global default
	out := defaultSpec.DeepCopy()
	_ = json.Unmarshal(data, &out)
	return out
}

// mergeNoneResourceQoSIfDisabled complete ResourceQoSStrategy according to enable statuses of qos features
func mergeNoneResourceQoSIfDisabled(resourceQoS *slov1alpha1.ResourceQoSStrategy) {
	mergeNoneResctrlQoSIfDisabled(resourceQoS)
	mergeNoneMemoryQoSIfDisabled(resourceQoS)
	klog.V(5).Infof("get merged node ResourceQoS %v", util.DumpJSON(resourceQoS))
}

// mergeNoneResctrlQoSIfDisabled completes node's resctrl qos config according to Enable options in ResctrlQoS
func mergeNoneResctrlQoSIfDisabled(resourceQoS *slov1alpha1.ResourceQoSStrategy) {
	if resourceQoS.LSR != nil && resourceQoS.LSR.ResctrlQoS != nil &&
		resourceQoS.LSR.ResctrlQoS.Enable != nil && !(*resourceQoS.LSR.ResctrlQoS.Enable) {
		resourceQoS.LSR.ResctrlQoS.ResctrlQoS = *util.NoneResctrlQoS()
	}
	if resourceQoS.LS != nil && resourceQoS.LS.ResctrlQoS != nil &&
		resourceQoS.LS.ResctrlQoS.Enable != nil && !(*resourceQoS.LS.ResctrlQoS.Enable) {
		resourceQoS.LS.ResctrlQoS.ResctrlQoS = *util.NoneResctrlQoS()
	}
	if resourceQoS.BE != nil && resourceQoS.BE.ResctrlQoS != nil &&
		resourceQoS.BE.ResctrlQoS.Enable != nil && !(*resourceQoS.BE.ResctrlQoS.Enable) {
		resourceQoS.BE.ResctrlQoS.ResctrlQoS = *util.NoneResctrlQoS()
	}
}

// mergeNoneMemoryQoSIfDisabled completes node's memory qos config according to Enable options in MemoryQoS
func mergeNoneMemoryQoSIfDisabled(resourceQoS *slov1alpha1.ResourceQoSStrategy) {
	// if MemoryQoS.Enable=false, merge with NoneMemoryQoS
	if resourceQoS.LSR != nil && resourceQoS.LSR.MemoryQoS != nil &&
		resourceQoS.LSR.MemoryQoS.Enable != nil && !(*resourceQoS.LSR.MemoryQoS.Enable) {
		resourceQoS.LSR.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
	}
	if resourceQoS.LS != nil && resourceQoS.LS.MemoryQoS != nil &&
		resourceQoS.LS.MemoryQoS.Enable != nil && !(*resourceQoS.LS.MemoryQoS.Enable) {
		resourceQoS.LS.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
	}
	if resourceQoS.BE != nil && resourceQoS.BE.MemoryQoS != nil &&
		resourceQoS.BE.MemoryQoS.Enable != nil && !(*resourceQoS.BE.MemoryQoS.Enable) {
		resourceQoS.BE.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
	}
}
