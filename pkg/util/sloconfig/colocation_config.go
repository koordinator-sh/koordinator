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

package sloconfig

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func NewDefaultColocationCfg() *configuration.ColocationCfg {
	defaultCfg := DefaultColocationCfg()
	return &defaultCfg
}

func DefaultColocationCfg() configuration.ColocationCfg {
	return configuration.ColocationCfg{
		ColocationStrategy: DefaultColocationStrategy(),
	}
}

func DefaultColocationStrategy() configuration.ColocationStrategy {
	var cpuCalculatePolicy, memoryCalculatePolicy = configuration.CalculateByPodUsage, configuration.CalculateByPodUsage
	var defaultMemoryCollectPolicy = slov1alpha1.UsageWithoutPageCache
	cfg := configuration.ColocationStrategy{
		Enable:                         pointer.Bool(false),
		MetricAggregateDurationSeconds: pointer.Int64(300),
		MetricReportIntervalSeconds:    pointer.Int64(60),
		MetricAggregatePolicy: &slov1alpha1.AggregatePolicy{
			Durations: []metav1.Duration{
				{Duration: 5 * time.Minute},
				{Duration: 10 * time.Minute},
				{Duration: 30 * time.Minute},
			},
		},
		MetricMemoryCollectPolicy:     &defaultMemoryCollectPolicy,
		CPUReclaimThresholdPercent:    pointer.Int64(60),
		CPUCalculatePolicy:            &cpuCalculatePolicy,
		MemoryReclaimThresholdPercent: pointer.Int64(65),
		MemoryCalculatePolicy:         &memoryCalculatePolicy,
		DegradeTimeMinutes:            pointer.Int64(15),
		UpdateTimeThresholdSeconds:    pointer.Int64(300),
		ResourceDiffThreshold:         pointer.Float64(0.1),
		MidCPUThresholdPercent:        pointer.Int64(100),
		MidMemoryThresholdPercent:     pointer.Int64(100),
		MidUnallocatedPercent:         pointer.Int64(0),
		BatchCPUThresholdPercent:      nil,
		BatchMemoryThresholdPercent:   nil,
	}
	cfg.ColocationStrategyExtender = defaultColocationStrategyExtender
	return cfg
}

func IsColocationStrategyValid(strategy *configuration.ColocationStrategy) bool {
	return strategy != nil &&
		(strategy.MetricAggregateDurationSeconds == nil || *strategy.MetricAggregateDurationSeconds > 0) &&
		(strategy.MetricReportIntervalSeconds == nil || *strategy.MetricReportIntervalSeconds > 0) &&
		(strategy.CPUReclaimThresholdPercent == nil || *strategy.CPUReclaimThresholdPercent >= 0) &&
		(strategy.MemoryReclaimThresholdPercent == nil || *strategy.MemoryReclaimThresholdPercent >= 0) &&
		(strategy.DegradeTimeMinutes == nil || *strategy.DegradeTimeMinutes > 0) &&
		(strategy.UpdateTimeThresholdSeconds == nil || *strategy.UpdateTimeThresholdSeconds > 0) &&
		(strategy.ResourceDiffThreshold == nil || *strategy.ResourceDiffThreshold > 0) &&
		(strategy.MetricMemoryCollectPolicy == nil || len(*strategy.MetricMemoryCollectPolicy) > 0) &&
		(strategy.MidCPUThresholdPercent == nil || (*strategy.MidCPUThresholdPercent >= 0 && *strategy.MidCPUThresholdPercent <= 100)) &&
		(strategy.MidMemoryThresholdPercent == nil || (*strategy.MidMemoryThresholdPercent >= 0 && *strategy.MidMemoryThresholdPercent <= 100)) &&
		(strategy.MidUnallocatedPercent == nil || (*strategy.MidUnallocatedPercent >= 0 && *strategy.MidUnallocatedPercent <= 100)) &&
		(strategy.BatchCPUThresholdPercent == nil || *strategy.BatchCPUThresholdPercent >= 0) &&
		(strategy.BatchMemoryThresholdPercent == nil || *strategy.BatchMemoryThresholdPercent >= 0)
}

func IsNodeColocationCfgValid(nodeCfg *configuration.NodeColocationCfg) bool {
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
	return !reflect.DeepEqual(&nodeCfg.ColocationStrategy, &configuration.ColocationStrategy{})
}

func GetNodeColocationStrategy(cfg *configuration.ColocationCfg, node *corev1.Node) *configuration.ColocationStrategy {
	if cfg == nil || node == nil {
		return nil
	}

	strategy := cfg.ColocationStrategy.DeepCopy()

	nodeLabels := labels.Set(node.Labels)
	for _, nodeCfg := range cfg.NodeConfigs {
		selector, err := metav1.LabelSelectorAsSelector(nodeCfg.NodeSelector)
		if err != nil || !selector.Matches(nodeLabels) {
			continue
		}

		merged, err := util.MergeCfg(strategy, &nodeCfg.ColocationStrategy)
		if err != nil {
			continue
		}

		strategy, _ = merged.(*configuration.ColocationStrategy)
		break
	}

	// update strategy according to node metadata
	UpdateColocationStrategyForNode(strategy, node)

	return strategy
}

func UpdateColocationStrategyForNode(strategy *configuration.ColocationStrategy, node *corev1.Node) {
	strategyOnNode, err := GetColocationStrategyOnNode(node)
	if err != nil {
		klog.V(5).Infof("failed to parse node colocation strategy for node %s, err: %s", node.Name, err)
	} else if strategyOnNode != nil {
		merged, _ := util.MergeCfg(strategy, strategyOnNode)
		*strategy = *(merged.(*configuration.ColocationStrategy))
		klog.V(6).Infof("node %s use merged colocation strategy from node annotations, merged: %+v",
			node.Name, strategy)
	}

	cpuReclaimPercent := getNodeReclaimPercent(node, extension.LabelCPUReclaimRatio)
	if cpuReclaimPercent != nil {
		klog.V(6).Infof("node %s use cpu reclaim percent from node metadata, original: %+v, new: %v",
			node.Name, strategy.CPUReclaimThresholdPercent, *cpuReclaimPercent)
		strategy.CPUReclaimThresholdPercent = cpuReclaimPercent
	}

	memReclaimPercent := getNodeReclaimPercent(node, extension.LabelMemoryReclaimRatio)
	if memReclaimPercent != nil {
		klog.V(6).Infof("node %s use memory reclaim percent from node metadata, original: %+v, new: %v",
			node.Name, strategy.MemoryReclaimThresholdPercent, *memReclaimPercent)
		strategy.MemoryReclaimThresholdPercent = memReclaimPercent
	}
}

// GetColocationStrategyOnNode gets the colocation strategy in the node annotations.
func GetColocationStrategyOnNode(node *corev1.Node) (*configuration.ColocationStrategy, error) {
	if node.Annotations == nil {
		return nil, nil
	}

	s, ok := node.Annotations[extension.AnnotationNodeColocationStrategy]
	if !ok {
		return nil, nil
	}

	strategy := &configuration.ColocationStrategy{}
	if err := json.Unmarshal([]byte(s), strategy); err != nil {
		return nil, fmt.Errorf("parse node colocation strategy failed, err: %w", err)
	}

	return strategy, nil
}

func getNodeReclaimPercent(node *corev1.Node, key string) *int64 {
	if node.Labels == nil {
		return nil
	}

	s, ok := node.Labels[key]
	if !ok {
		return nil
	}

	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		klog.V(5).Infof("failed to parse reclaim ratio for node %s, key %s, err: %s",
			node.Name, key, err)
		return nil
	}
	if v < 0 {
		klog.V(5).Infof("failed to validate reclaim ratio for node %s, key %s, ratio %v",
			node.Name, key, v)
		return nil
	}

	return pointer.Int64(int64(v * 100))
}
