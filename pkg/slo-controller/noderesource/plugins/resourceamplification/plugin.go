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

// Plugin calculates and updates final node resource amplification ratios automatically
// based on user config (not implemented) and node cpu normalization ratio.
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

func (p *Plugin) Calculate(_ *configuration.ColocationStrategy, node *corev1.Node, _ *corev1.PodList, _ *framework.ResourceMetrics) ([]framework.ResourceItem, error) {
	normRatio, err := extension.GetCPUNormalizationRatio(node)
	if err != nil {
		return nil, fmt.Errorf("failed to get cpu normalization ratio: %w", err)
	}

	if normRatio <= 1 {
		return []framework.ResourceItem{
			{
				Name: PluginName,
			},
		}, nil
	}

	// Set cpu amplification ratio according to cpu normalization ratio.
	// TODO: In the future, we should read user's amplification config, multiply its cpu amplification ratio
	// with cpu normalization ratio, and apply the final result.
	ampRatios := map[corev1.ResourceName]extension.Ratio{
		corev1.ResourceCPU: extension.Ratio(normRatio),
	}
	ratioBytes, _ := json.Marshal(ampRatios)
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
