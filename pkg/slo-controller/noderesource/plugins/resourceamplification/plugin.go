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

package resourceamplification

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
)

const PluginName = "ResourceAmplification"

var (
	cfgHandler *configHandler
)

// Plugin calculates and updates final node resource amplification ratios automatically
// based on user config and node cpu normalization ratio.
type Plugin struct{}

func (p *Plugin) Name() string {
	return PluginName
}

func (p *Plugin) NeedSyncMeta(_ *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	oldRatioStr := oldNode.Annotations[extension.AnnotationNodeResourceAmplificationRatio]
	newRatioStr := newNode.Annotations[extension.AnnotationNodeResourceAmplificationRatio]

	if oldRatioStr == "" && newRatioStr == "" {
		return false, "ratio remains empty"
	}
	if oldRatioStr == "" {
		return true, "old ratio is empty"
	}
	if newRatioStr == "" {
		return true, "new ratio is empty"
	}
	if oldRatioStr == newRatioStr {
		return false, "ratio remains unchanged"
	}
	return true, "ratio changed"
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
func (p *Plugin) Setup(opt *framework.Option) error {
	cfgHandler = newConfigHandler(opt.Client, DefaultResourceAmplificationCfg(), opt.Recorder)
	opt.Builder = opt.Builder.Watches(&corev1.ConfigMap{}, cfgHandler)

	return nil
}

func (p *Plugin) Prepare(_ *configuration.ColocationStrategy, node *corev1.Node, nr *framework.NodeResource) error {
	ratioStr, ok := nr.Annotations[extension.AnnotationNodeResourceAmplificationRatio]
	if !ok {
		klog.V(6).Infof("prepare node resource amplification ratio to unset, node %s", node.Name)
		delete(node.Annotations, extension.AnnotationNodeResourceAmplificationRatio)
		return nil
	}

	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[extension.AnnotationNodeResourceAmplificationRatio] = ratioStr
	klog.V(6).Infof("prepare node resource amplification ratio to set, node %s, ratio %s", node.Name, ratioStr)

	return nil
}

func (p *Plugin) Reset(node *corev1.Node, message string) []framework.ResourceItem {
	// Currently we have no user configurations so there's no need to reset.
	return nil
}

// Calculate calculates resource amplification ratio, resource final ratio should >= 1
// cpuAmplificationRatio = amplificationStrategy.resourceAmplificationRatio["cpu"] * CPUNormalizationRatio
// otherResourceAmplificationRatio = amplificationStrategy.resourceAmplificationRatio["other-resource"]
func (p *Plugin) Calculate(_ *configuration.ColocationStrategy, node *corev1.Node, _ *corev1.PodList, _ *framework.ResourceMetrics) ([]framework.ResourceItem, error) {
	resourceAmplificationRatios, err := getResourceAmplificationRatios(node)
	if err != nil {
		return nil, fmt.Errorf("calculate failed for node %s, getResourceAmplificationRatios failed: %w", node.Name, err)
	}

	cpuAmplificationRatio, err := calculateCPUAmplificationRatio(node, resourceAmplificationRatios[corev1.ResourceCPU])
	if err != nil {
		return nil, fmt.Errorf("calculate failed for node %s, calculateCPUAmplificationRatio failed: %w", node.Name, err)
	}
	if cpuAmplificationRatio > 1 {
		resourceAmplificationRatios[corev1.ResourceCPU] = cpuAmplificationRatio
	}

	if err := validateResourceAmplificationRatios(resourceAmplificationRatios); err != nil {
		return nil, fmt.Errorf("calculate failed for node %s, amplification ratio is invalid: %w", node.Name, err)
	}

	if len(resourceAmplificationRatios) == 0 {
		klog.V(6).Infof("node %s does not need resource amplification ratio", node.Name)
		return []framework.ResourceItem{
			{Name: PluginName},
		}, nil
	}

	ratioBytes, _ := json.Marshal(resourceAmplificationRatios)
	ratioStr := string(ratioBytes)
	klog.V(6).Infof("calculate resource amplification ratio %s for node %s", ratioStr, node.Name)
	return []framework.ResourceItem{
		{
			Name: PluginName,
			Annotations: map[string]string{
				extension.AnnotationNodeResourceAmplificationRatio: ratioStr,
			},
		},
	}, nil
}

func calculateCPUAmplificationRatio(node *corev1.Node, ratio extension.Ratio) (extension.Ratio, error) {
	var finalRatio extension.Ratio = 1
	if ratio > 0 {
		finalRatio = ratio
	}

	cpuNormalizationRatio, err := extension.GetCPUNormalizationRatio(node)
	if err != nil {
		return finalRatio, fmt.Errorf("failed to get cpu normalization ratio, error: %w", err)
	}

	if cpuNormalizationRatio > 0 {
		finalRatio *= extension.Ratio(cpuNormalizationRatio)
	}

	return finalRatio, nil
}

func validateResourceAmplificationRatios(ratios map[corev1.ResourceName]extension.Ratio) error {
	for k, v := range ratios {
		if v < 1 {
			return fmt.Errorf("ratio with resource type %v and value %v is invalid", k, v)
		}
	}

	return nil
}

func getResourceAmplificationRatios(node *corev1.Node) (map[corev1.ResourceName]extension.Ratio, error) {
	if !cfgHandler.IsCfgAvailable() {
		return nil, fmt.Errorf("cfgHandler is not available")
	}

	ratios := map[corev1.ResourceName]extension.Ratio{}
	strategy := cfgHandler.GetStrategyCopy(node)
	if strategy.Enable != nil && *strategy.Enable {
		for k, v := range strategy.ResourceAmplificationRatio {
			ratios[k] = extension.Ratio(v)
		}
	}

	return ratios, nil
}
