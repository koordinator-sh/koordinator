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

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func getResourceThresholdSpec(node *corev1.Node, cfg *configuration.ResourceThresholdCfg) (*slov1alpha1.ResourceThresholdStrategy, error) {
	nodeLabels := labels.Set(node.Labels)
	for _, nodeStrategy := range cfg.NodeStrategies {
		selector, err := metav1.LabelSelectorAsSelector(nodeStrategy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v for threshold, err: %v", nodeStrategy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			return nodeStrategy.ResourceThresholdStrategy.DeepCopy(), nil
		}
	}

	return cfg.ClusterStrategy.DeepCopy(), nil
}

func getResourceQOSSpec(node *corev1.Node, cfg *configuration.ResourceQOSCfg) (*slov1alpha1.ResourceQOSStrategy, error) {
	nodeLabels := labels.Set(node.Labels)
	for _, nodeStrategy := range cfg.NodeStrategies {
		selector, err := metav1.LabelSelectorAsSelector(nodeStrategy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v for ResourceQOS, err: %v", nodeStrategy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			return nodeStrategy.ResourceQOSStrategy.DeepCopy(), nil
		}
	}

	return cfg.ClusterStrategy.DeepCopy(), nil
}

func getCPUBurstConfigSpec(node *corev1.Node, cfg *configuration.CPUBurstCfg) (*slov1alpha1.CPUBurstStrategy, error) {

	nodeLabels := labels.Set(node.Labels)
	for _, nodeStrategy := range cfg.NodeStrategies {
		selector, err := metav1.LabelSelectorAsSelector(nodeStrategy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v for CPUBurst, err: %v", nodeStrategy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			return nodeStrategy.CPUBurstStrategy.DeepCopy(), nil
		}

	}
	return cfg.ClusterStrategy.DeepCopy(), nil
}

func getSystemConfigSpec(node *corev1.Node, cfg *configuration.SystemCfg) (*slov1alpha1.SystemStrategy, error) {
	var nodeSystemConfig *slov1alpha1.SystemStrategy

	// Find strategy that matches current node.
	nodeLabels := labels.Set(node.Labels)
	for _, nodeStrategy := range cfg.NodeStrategies {
		selector, err := metav1.LabelSelectorAsSelector(nodeStrategy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v for SystemCfg, err: %v", nodeStrategy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			nodeSystemConfig = nodeStrategy.SystemStrategy.DeepCopy()
			break
		}
	}

	// If there is no matched node strategy, use cluster strategy.
	if nodeSystemConfig == nil {
		nodeSystemConfig = cfg.ClusterStrategy.DeepCopy()
	}

	// Check whether node bandwidth is specified on current node, which takes HIGHER priority
	// than ones configured in cluster strategy or node strategy.
	// Error is returned if failed to parse node total bandwidth from annotation, which is not
	// supposed to happen because we will check the validity of the annotation value in node
	// plugins of validating webhook.
	if nodeBandwidthQuantity, err := extension.GetNodeTotalBandwidth(node.Annotations); err != nil {
		klog.Errorf("failed to get node total bandwidth from annotation, error: %v", err)
		return nil, err
	} else if nodeBandwidthQuantity != nil {
		nodeSystemConfig.TotalNetworkBandwidth = *nodeBandwidthQuantity
	}

	return nodeSystemConfig, nil
}

func getHostApplicationConfig(node *corev1.Node, cfg *configuration.HostApplicationCfg) ([]slov1alpha1.HostApplicationSpec, error) {
	nodeLabels := labels.Set(node.Labels)
	for _, nodeCfg := range cfg.NodeConfigs {
		selector, err := metav1.LabelSelectorAsSelector(nodeCfg.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v for HostApplicationCfg, error: %v", nodeCfg.NodeSelector.String(), err)
			continue
		}
		if selector.Matches(nodeLabels) {
			out := make([]slov1alpha1.HostApplicationSpec, len(nodeCfg.Applications))
			for i := range nodeCfg.Applications {
				nodeCfg.Applications[i].DeepCopyInto(&out[i])
			}
			return out, nil
		}
	}
	out := make([]slov1alpha1.HostApplicationSpec, len(cfg.Applications))
	for i := range cfg.Applications {
		cfg.Applications[i].DeepCopyInto(&out[i])
	}
	return out, nil
}

func calculateResourceThresholdCfgMerged(oldCfg configuration.ResourceThresholdCfg, configMap *corev1.ConfigMap) (configuration.ResourceThresholdCfg, error) {
	cfgStr, ok := configMap.Data[configuration.ResourceThresholdConfigKey]
	if !ok {
		return DefaultSLOCfg().ThresholdCfgMerged, nil
	}

	mergedCfg := configuration.ResourceThresholdCfg{}
	if err := json.Unmarshal([]byte(cfgStr), &mergedCfg); err != nil {
		klog.Errorf("failed to unmarshal config %s, err: %s", configuration.ResourceThresholdConfigKey, err)
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

func calculateResourceQOSCfgMerged(oldCfg configuration.ResourceQOSCfg, configMap *corev1.ConfigMap) (configuration.ResourceQOSCfg, error) {
	cfgStr, ok := configMap.Data[configuration.ResourceQOSConfigKey]
	if !ok {
		return DefaultSLOCfg().ResourceQOSCfgMerged, nil
	}

	mergedCfg := DefaultSLOCfg().ResourceQOSCfgMerged
	if err := json.Unmarshal([]byte(cfgStr), &mergedCfg); err != nil {
		klog.Errorf("failed to unmarshal config %s, err: %s", configuration.ResourceQOSConfigKey, err)
		return oldCfg, err
	}

	// merge ClusterStrategy
	clusterMerged := DefaultSLOCfg().ResourceQOSCfgMerged.ClusterStrategy.DeepCopy()
	if mergedCfg.ClusterStrategy != nil {
		mergedStrategyInterface, _ := util.MergeCfg(clusterMerged, mergedCfg.ClusterStrategy)
		clusterMerged = mergedStrategyInterface.(*slov1alpha1.ResourceQOSStrategy)
	}
	mergedCfg.ClusterStrategy = clusterMerged

	for index, nodeStrategy := range mergedCfg.NodeStrategies {
		// merge with clusterStrategy
		var mergedNodeStrategy *slov1alpha1.ResourceQOSStrategy
		clusterCfgCopy := mergedCfg.ClusterStrategy.DeepCopy()
		if nodeStrategy.ResourceQOSStrategy != nil {
			mergedStrategyInterface, _ := util.MergeCfg(clusterCfgCopy, nodeStrategy.ResourceQOSStrategy)
			mergedNodeStrategy = mergedStrategyInterface.(*slov1alpha1.ResourceQOSStrategy)
		} else {
			mergedNodeStrategy = clusterCfgCopy
		}
		mergedCfg.NodeStrategies[index].ResourceQOSStrategy = mergedNodeStrategy

	}

	return mergedCfg, nil
}

func calculateCPUBurstCfgMerged(oldCfg configuration.CPUBurstCfg, configMap *corev1.ConfigMap) (configuration.CPUBurstCfg, error) {
	cfgStr, ok := configMap.Data[configuration.CPUBurstConfigKey]
	if !ok {
		return DefaultSLOCfg().CPUBurstCfgMerged, nil
	}

	mergedCfg := configuration.CPUBurstCfg{}
	if err := json.Unmarshal([]byte(cfgStr), &mergedCfg); err != nil {
		klog.Errorf("failed to unmarshal config %s, err: %s", configuration.CPUBurstConfigKey, err)
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

func calculateSystemConfigMerged(oldCfg configuration.SystemCfg, configMap *corev1.ConfigMap) (configuration.SystemCfg, error) {
	cfgStr, ok := configMap.Data[configuration.SystemConfigKey]
	if !ok {
		return DefaultSLOCfg().SystemCfgMerged, nil
	}

	mergedCfg := configuration.SystemCfg{}
	if err := json.Unmarshal([]byte(cfgStr), &mergedCfg); err != nil {
		klog.Warningf("failed to unmarshal config %s, err: %s", configuration.SystemConfigKey, err)
		return oldCfg, err
	}

	// merge ClusterStrategy
	clusterMerged := DefaultSLOCfg().SystemCfgMerged.ClusterStrategy.DeepCopy()
	if mergedCfg.ClusterStrategy != nil {
		mergedStrategyInterface, _ := util.MergeCfg(clusterMerged, mergedCfg.ClusterStrategy)
		clusterMerged = mergedStrategyInterface.(*slov1alpha1.SystemStrategy)
	}
	mergedCfg.ClusterStrategy = clusterMerged

	for index, nodeStrategy := range mergedCfg.NodeStrategies {
		// merge with clusterStrategy
		clusterCfgCopy := mergedCfg.ClusterStrategy.DeepCopy()
		if nodeStrategy.SystemStrategy != nil {
			mergedStrategyInterface, _ := util.MergeCfg(clusterCfgCopy, nodeStrategy.SystemStrategy)
			mergedCfg.NodeStrategies[index].SystemStrategy = mergedStrategyInterface.(*slov1alpha1.SystemStrategy)
		} else {
			mergedCfg.NodeStrategies[index].SystemStrategy = clusterCfgCopy
		}

	}

	return mergedCfg, nil
}

func calculateHostAppConfigMerged(oldCfg configuration.HostApplicationCfg, configMap *corev1.ConfigMap) (configuration.HostApplicationCfg, error) {
	cfgStr, ok := configMap.Data[configuration.HostApplicationConfigKey]
	if !ok {
		return configuration.HostApplicationCfg{}, nil
	}

	mergedCfg := configuration.HostApplicationCfg{}
	if err := json.Unmarshal([]byte(cfgStr), &mergedCfg); err != nil {
		klog.Warningf("failed to unmarshal config %s, error: %v", configuration.HostApplicationConfigKey, err)
		return oldCfg, err
	}
	return mergedCfg, nil
}
