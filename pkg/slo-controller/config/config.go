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

package config

import (
	"reflect"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/jinzhu/copier"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

type ColocationCfg struct {
	ColocationStrategy
	NodeConfigs []NodeColocationCfg `json:"nodeConfigs,omitempty"`
}

type NodeColocationCfg struct {
	NodeSelector *metav1.LabelSelector
	ColocationCfg
}

type ColocationStrategy struct {
	Enable                        *bool    `json:"enable,omitempty"`
	CPUReclaimThresholdPercent    *int64   `json:"cpuReclaimThresholdPercent,omitempty"`
	MemoryReclaimThresholdPercent *int64   `json:"memoryReclaimThresholdPercent,omitempty"`
	DegradeTimeMinutes            *int64   `json:"degradeTimeMinutes,omitempty"`
	UpdateTimeThresholdSeconds    *int64   `json:"updateTimeThresholdSeconds,omitempty"`
	ResourceDiffThreshold         *float64 `json:"resourceDiffThreshold,omitempty"`
}

func NewDefaultColocationCfg() *ColocationCfg {
	defaultCfg := DefaultColocationCfg()
	return &defaultCfg
}

func DefaultColocationCfg() ColocationCfg {
	return ColocationCfg{
		ColocationStrategy: DefaultColocationStrategy(),
	}
}

func DefaultColocationStrategy() ColocationStrategy {
	return ColocationStrategy{
		Enable:                        util.BoolPtr(false),
		CPUReclaimThresholdPercent:    util.Int64Ptr(65),
		MemoryReclaimThresholdPercent: util.Int64Ptr(65),
		DegradeTimeMinutes:            util.Int64Ptr(15),
		UpdateTimeThresholdSeconds:    util.Int64Ptr(300),
		ResourceDiffThreshold:         util.Float64Ptr(0.1),
	}
}

func IsColocationStrategyValid(strategy *ColocationStrategy) bool {
	return strategy != nil &&
		(strategy.CPUReclaimThresholdPercent == nil || *strategy.CPUReclaimThresholdPercent > 0) &&
		(strategy.MemoryReclaimThresholdPercent == nil || *strategy.MemoryReclaimThresholdPercent > 0) &&
		(strategy.DegradeTimeMinutes == nil || *strategy.DegradeTimeMinutes > 0) &&
		(strategy.UpdateTimeThresholdSeconds == nil || *strategy.UpdateTimeThresholdSeconds > 0) &&
		(strategy.ResourceDiffThreshold == nil || *strategy.ResourceDiffThreshold > 0)
}

func IsNodeColocationCfgValid(nodeCfg *NodeColocationCfg) bool {
	if nodeCfg == nil {
		return false
	}
	if nodeCfg.NodeSelector.MatchLabels == nil {
		return false
	}
	if _, err := metav1.LabelSelectorAsSelector(nodeCfg.NodeSelector); err != nil {
		return false
	}
	// node colocation should not be empty
	return !reflect.DeepEqual(&nodeCfg.ColocationStrategy, &ColocationStrategy{})
}

func GetNodeColocationStrategy(cfg *ColocationCfg, node *corev1.Node) *ColocationStrategy {
	if cfg == nil || node == nil {
		return nil
	}

	strategy := &ColocationStrategy{}
	if err := copier.Copy(&strategy, &cfg.ColocationStrategy); err != nil {
		return nil
	}

	nodeLabels := labels.Set(node.Labels)
	for _, nodeCfg := range cfg.NodeConfigs {
		selector, err := metav1.LabelSelectorAsSelector(nodeCfg.NodeSelector)
		if err != nil {
			continue
		}
		if selector.Matches(nodeLabels) {
			if nodeCfg.NodeSelector != nil {
				if merged, err := util.Merge(strategy, &nodeCfg.ColocationStrategy); err != nil {
					continue
				} else {
					strategy, _ = merged.(*ColocationStrategy)
				}
			}
			break
		}
	}

	return strategy
}
