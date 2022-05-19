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

	"github.com/jinzhu/copier"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type ColocationCfg struct {
	ColocationStrategy `json:",inline"`
	NodeConfigs        []NodeColocationCfg `json:"nodeConfigs,omitempty"`
}

type NodeColocationCfg struct {
	NodeSelector *metav1.LabelSelector
	ColocationStrategy
}

type ResourceThresholdCfg struct {
	ClusterStrategy *slov1alpha1.ResourceThresholdStrategy `json:"clusterStrategy,omitempty"`
	NodeStrategies  []NodeResourceThresholdStrategy        `json:"nodeStrategies,omitempty"`
}

type NodeResourceThresholdStrategy struct {
	// an empty label selector matches all objects while a nil label selector matches no objects
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
	*slov1alpha1.ResourceThresholdStrategy
}

type NodeCPUBurstCfg struct {
	// an empty label selector matches all objects while a nil label selector matches no objects
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
	*slov1alpha1.CPUBurstStrategy
}

type CPUBurstCfg struct {
	ClusterStrategy *slov1alpha1.CPUBurstStrategy `json:"clusterStrategy,omitempty"`
	NodeStrategies  []NodeCPUBurstCfg             `json:"nodeStrategies,omitempty"`
}

type ResourceQoSCfg struct {
	ClusterStrategy *slov1alpha1.ResourceQoSStrategy `json:"clusterStrategy,omitempty"`
	NodeStrategies  []NodeResourceQoSStrategy        `json:"nodeStrategies,omitempty"`
}

type NodeResourceQoSStrategy struct {
	// an empty label selector matches all objects while a nil label selector matches no objects
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
	*slov1alpha1.ResourceQoSStrategy
}

type ColocationStrategy struct {
	Enable                         *bool    `json:"enable,omitempty"`
	MetricAggregateDurationSeconds *int64   `json:"metricAggregateDurationSeconds,omitempty"`
	MetricReportIntervalSeconds    *int64   `json:"metricReportIntervalSeconds,omitempty"`
	CPUReclaimThresholdPercent     *int64   `json:"cpuReclaimThresholdPercent,omitempty"`
	MemoryReclaimThresholdPercent  *int64   `json:"memoryReclaimThresholdPercent,omitempty"`
	DegradeTimeMinutes             *int64   `json:"degradeTimeMinutes,omitempty"`
	UpdateTimeThresholdSeconds     *int64   `json:"updateTimeThresholdSeconds,omitempty"`
	ResourceDiffThreshold          *float64 `json:"resourceDiffThreshold,omitempty"`
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
		Enable:                         pointer.Bool(false),
		MetricAggregateDurationSeconds: pointer.Int64(30),
		MetricReportIntervalSeconds:    pointer.Int64(60),
		CPUReclaimThresholdPercent:     pointer.Int64(60),
		MemoryReclaimThresholdPercent:  pointer.Int64(65),
		DegradeTimeMinutes:             pointer.Int64(15),
		UpdateTimeThresholdSeconds:     pointer.Int64(300),
		ResourceDiffThreshold:          pointer.Float64(0.1),
	}
}

func IsColocationStrategyValid(strategy *ColocationStrategy) bool {
	return strategy != nil &&
		(strategy.MetricAggregateDurationSeconds == nil || *strategy.MetricReportIntervalSeconds > 0) &&
		(strategy.MetricReportIntervalSeconds == nil || *strategy.MetricReportIntervalSeconds > 0) &&
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
				if merged, err := util.MergeCfg(strategy, &nodeCfg.ColocationStrategy); err != nil {
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
