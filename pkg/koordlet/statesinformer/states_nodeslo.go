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

package statesinformer

import (
	"encoding/json"
	"reflect"

	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func (s *statesInformer) GetNodeSLO() *slov1alpha1.NodeSLO {
	s.nodeSLORWMutex.RLock()
	defer s.nodeSLORWMutex.RUnlock()
	return s.nodeSLO.DeepCopy()
}

func (s *statesInformer) setupNodeSLOInformer() {
	s.nodeSLOInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			nodeSLO, ok := obj.(*slov1alpha1.NodeSLO)
			if ok {
				s.updateNodeSLOSpec(nodeSLO)
				klog.Infof("create NodeSLO %v", nodeSLO)
			} else {
				klog.Errorf("node slo informer add func parse nodeSLO failed")
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNodeSLO, oldOK := oldObj.(*slov1alpha1.NodeSLO)
			newNodeSLO, newOK := newObj.(*slov1alpha1.NodeSLO)
			if !oldOK || !newOK {
				klog.Errorf("unable to convert object to *slov1alpha1.NodeSLO, old %T, new %T", oldObj, newObj)
				return
			}
			if reflect.DeepEqual(oldNodeSLO.Spec, newNodeSLO.Spec) {
				klog.V(5).Infof("find NodeSLO spec %s has not changed", newNodeSLO.Name)
				return
			}
			klog.Infof("update NodeSLO spec %v", newNodeSLO.Spec)
			s.updateNodeSLOSpec(newNodeSLO)
		},
	})
}

func (s *statesInformer) updateNodeSLOSpec(nodeSLO *slov1alpha1.NodeSLO) {
	s.setNodeSLOSpec(nodeSLO)
	s.runCallbacks(reflect.TypeOf(&slov1alpha1.NodeSLO{}), s.GetNodeSLO())
}

func (s *statesInformer) setNodeSLOSpec(nodeSLO *slov1alpha1.NodeSLO) {
	s.nodeSLORWMutex.Lock()
	defer s.nodeSLORWMutex.Unlock()

	oldNodeSLOStr := util.DumpJSON(s.nodeSLO)

	if s.nodeSLO == nil {
		s.nodeSLO = nodeSLO.DeepCopy()
	} else {
		s.nodeSLO.Spec = nodeSLO.Spec
	}

	// merge nodeSLO spec with the default config
	s.mergeNodeSLOSpec(nodeSLO)

	newNodeSLOStr := util.DumpJSON(s.nodeSLO)
	klog.Infof("update nodeSLO content: old %s, new %s", oldNodeSLOStr, newNodeSLOStr)
}

func (s *statesInformer) mergeNodeSLOSpec(nodeSLO *slov1alpha1.NodeSLO) {
	if s.nodeSLO == nil || nodeSLO == nil {
		klog.Errorf("failed to merge with nil nodeSLO, old is nil: %v, new is nil: %v", s.nodeSLO == nil, nodeSLO == nil)
		return
	}

	// merge ResourceUsedThresholdWithBE individually for nil-ResourceUsedThresholdWithBE case
	mergedResourceUsedThresholdWithBESpec := mergeSLOSpecResourceUsedThresholdWithBE(util.DefaultNodeSLOSpecConfig().ResourceUsedThresholdWithBE,
		nodeSLO.Spec.ResourceUsedThresholdWithBE)
	if mergedResourceUsedThresholdWithBESpec != nil {
		s.nodeSLO.Spec.ResourceUsedThresholdWithBE = mergedResourceUsedThresholdWithBESpec
	}

	// merge ResourceQoSStrategy
	mergedResourceQoSStrategySpec := mergeSLOSpecResourceQoSStrategy(util.DefaultNodeSLOSpecConfig().ResourceQoSStrategy,
		nodeSLO.Spec.ResourceQoSStrategy)
	mergeNoneResourceQoSIfDisabled(mergedResourceQoSStrategySpec)
	if mergedResourceQoSStrategySpec != nil {
		s.nodeSLO.Spec.ResourceQoSStrategy = mergedResourceQoSStrategySpec
	}

	// merge CPUBurstStrategy
	mergedCPUBurstStrategySpec := mergeSLOSpecCPUBurstStrategy(util.DefaultNodeSLOSpecConfig().CPUBurstStrategy,
		nodeSLO.Spec.CPUBurstStrategy)
	if mergedCPUBurstStrategySpec != nil {
		s.nodeSLO.Spec.CPUBurstStrategy = mergedCPUBurstStrategySpec
	}
}

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

func mergeSLOSpecCPUBurstStrategy(defaultSpec,
	newSpec *slov1alpha1.CPUBurstStrategy) *slov1alpha1.CPUBurstStrategy {
	spec := &slov1alpha1.CPUBurstStrategy{}
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
	mergeNoneCPUQoSIfDisabled(resourceQoS)
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

func mergeNoneCPUQoSIfDisabled(resourceQoS *slov1alpha1.ResourceQoSStrategy) {
	// if CPUQoS.Enabled=false, merge with NoneCPUQoS
	if resourceQoS.LSR != nil && resourceQoS.LSR.CPUQoS != nil &&
		resourceQoS.LSR.CPUQoS.Enable != nil && !(*resourceQoS.LSR.CPUQoS.Enable) {
		resourceQoS.LSR.CPUQoS.CPUQoS = *util.NoneCPUQoS()
	}
	if resourceQoS.LS != nil && resourceQoS.LS.CPUQoS != nil &&
		resourceQoS.LS.CPUQoS.Enable != nil && !(*resourceQoS.LS.CPUQoS.Enable) {
		resourceQoS.LS.CPUQoS.CPUQoS = *util.NoneCPUQoS()
	}
	if resourceQoS.BE != nil && resourceQoS.BE.CPUQoS != nil &&
		resourceQoS.BE.CPUQoS.Enable != nil && !(*resourceQoS.BE.CPUQoS.Enable) {
		resourceQoS.BE.CPUQoS.CPUQoS = *util.NoneCPUQoS()
	}
}
