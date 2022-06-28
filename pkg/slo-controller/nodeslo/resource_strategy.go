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

package nodeslo

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func getResourceThresholdSpec(node *corev1.Node, cfg *config.ResourceThresholdCfg) (*slov1alpha1.ResourceThresholdStrategy, error) {

	nodeLabels := labels.Set(node.Labels)
	for _, nodeStrategy := range cfg.NodeStrategies {
		selector, err := metav1.LabelSelectorAsSelector(nodeStrategy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v, err: %v", nodeStrategy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			return nodeStrategy.ResourceThresholdStrategy.DeepCopy(), nil
		}
	}

	return cfg.ClusterStrategy.DeepCopy(), nil
}

func getResourceQoSSpec(node *corev1.Node, cfg *config.ResourceQoSCfg) (*slov1alpha1.ResourceQoSStrategy, error) {
	nodeLabels := labels.Set(node.Labels)
	for _, nodeStrategy := range cfg.NodeStrategies {
		selector, err := metav1.LabelSelectorAsSelector(nodeStrategy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v, err: %v", nodeStrategy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			return nodeStrategy.ResourceQoSStrategy.DeepCopy(), nil
		}
	}

	return cfg.ClusterStrategy.DeepCopy(), nil
}

func getCPUBurstConfigSpec(node *corev1.Node, cfg *config.CPUBurstCfg) (*slov1alpha1.CPUBurstStrategy, error) {

	nodeLabels := labels.Set(node.Labels)
	for _, nodeStrategy := range cfg.NodeStrategies {
		selector, err := metav1.LabelSelectorAsSelector(nodeStrategy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v, err: %v", nodeStrategy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			return nodeStrategy.CPUBurstStrategy.DeepCopy(), nil
		}

	}
	return cfg.ClusterStrategy.DeepCopy(), nil
}

func caculateResourceThresholdCfgMerged(oldCfg config.ResourceThresholdCfg, configMap *corev1.ConfigMap) (config.ResourceThresholdCfg, error) {
	cfgStr, ok := configMap.Data[config.ResourceThresholdConfigKey]
	if !ok {
		return DefaultSLOCfg().ThresholdCfgMerged, nil
	}

	mergedCfg := config.ResourceThresholdCfg{}
	if err := json.Unmarshal([]byte(cfgStr), &mergedCfg); err != nil {
		klog.Errorf("failed to unmarshal config %s, err: %s", config.ResourceThresholdConfigKey, err)
		return oldCfg, err
	}

	// merge ClusterStrategy
	clusterMerged := DefaultSLOCfg().ThresholdCfgMerged.ClusterStrategy.DeepCopy()
	if mergedCfg.ClusterStrategy != nil {
		mergedStrategyInterface, _ := util.MergeCfg(clusterMerged, mergedCfg.ClusterStrategy)
		clusterMerged = mergedStrategyInterface.(*slov1alpha1.ResourceThresholdStrategy)
	}
	mergedCfg.ClusterStrategy = clusterMerged

	for index, nodeStrategy := range mergedCfg.NodeStrategies {
		// merge with clusterStrategy
		clusterCfgCopy := mergedCfg.ClusterStrategy.DeepCopy()
		if nodeStrategy.ResourceThresholdStrategy != nil {
			mergedNodeStrategyInterface, _ := util.MergeCfg(clusterCfgCopy, nodeStrategy.ResourceThresholdStrategy)
			mergedCfg.NodeStrategies[index].ResourceThresholdStrategy = mergedNodeStrategyInterface.(*slov1alpha1.ResourceThresholdStrategy)
		} else {
			mergedCfg.NodeStrategies[index].ResourceThresholdStrategy = clusterCfgCopy
		}

	}

	return mergedCfg, nil
}

func caculateResourceQoSCfgMerged(oldCfg config.ResourceQoSCfg, configMap *corev1.ConfigMap) (config.ResourceQoSCfg, error) {
	cfgStr, ok := configMap.Data[config.ResourceQoSConfigKey]
	if !ok {
		return DefaultSLOCfg().ResourceQoSCfgMerged, nil
	}

	mergedCfg := DefaultSLOCfg().ResourceQoSCfgMerged
	if err := json.Unmarshal([]byte(cfgStr), &mergedCfg); err != nil {
		klog.Errorf("failed to unmarshal config %s, err: %s", config.ResourceQoSConfigKey, err)
		return oldCfg, err
	}

	// merge ClusterStrategy
	clusterMerged := DefaultSLOCfg().ResourceQoSCfgMerged.ClusterStrategy.DeepCopy()
	if mergedCfg.ClusterStrategy != nil {
		mergedStrategyInterface, _ := util.MergeCfg(clusterMerged, mergedCfg.ClusterStrategy)
		clusterMerged = mergedStrategyInterface.(*slov1alpha1.ResourceQoSStrategy)
	}
	mergedCfg.ClusterStrategy = clusterMerged

	for index, nodeStrategy := range mergedCfg.NodeStrategies {
		// merge with clusterStrategy
		var mergedNodeStrategy *slov1alpha1.ResourceQoSStrategy
		clusterCfgCopy := mergedCfg.ClusterStrategy.DeepCopy()
		if nodeStrategy.ResourceQoSStrategy != nil {
			mergedStrategyInterface, _ := util.MergeCfg(clusterCfgCopy, nodeStrategy.ResourceQoSStrategy)
			mergedNodeStrategy = mergedStrategyInterface.(*slov1alpha1.ResourceQoSStrategy)
		} else {
			mergedNodeStrategy = clusterCfgCopy
		}
		mergedCfg.NodeStrategies[index].ResourceQoSStrategy = mergedNodeStrategy

	}

	return mergedCfg, nil
}

func caculateCPUBurstCfgMerged(oldCfg config.CPUBurstCfg, configMap *corev1.ConfigMap) (config.CPUBurstCfg, error) {
	cfgStr, ok := configMap.Data[config.CPUBurstConfigKey]
	if !ok {
		return DefaultSLOCfg().CPUBurstCfgMerged, nil
	}

	mergedCfg := config.CPUBurstCfg{}
	if err := json.Unmarshal([]byte(cfgStr), &mergedCfg); err != nil {
		klog.Errorf("failed to unmarshal config %s, err: %s", config.CPUBurstConfigKey, err)
		return oldCfg, err
	}

	// merge ClusterStrategy
	clusterMerged := DefaultSLOCfg().CPUBurstCfgMerged.ClusterStrategy.DeepCopy()
	if mergedCfg.ClusterStrategy != nil {
		mergedStrategyInterface, _ := util.MergeCfg(clusterMerged, mergedCfg.ClusterStrategy)
		clusterMerged = mergedStrategyInterface.(*slov1alpha1.CPUBurstStrategy)
	}
	mergedCfg.ClusterStrategy = clusterMerged

	for index, nodeStrategy := range mergedCfg.NodeStrategies {
		// merge with clusterStrategy
		clusterCfgCopy := mergedCfg.ClusterStrategy.DeepCopy()
		if nodeStrategy.CPUBurstStrategy != nil {
			mergedStrategyInterface, _ := util.MergeCfg(clusterCfgCopy, nodeStrategy.CPUBurstStrategy)
			mergedCfg.NodeStrategies[index].CPUBurstStrategy = mergedStrategyInterface.(*slov1alpha1.CPUBurstStrategy)
		} else {
			mergedCfg.NodeStrategies[index].CPUBurstStrategy = clusterCfgCopy
		}

	}

	return mergedCfg, nil
}
