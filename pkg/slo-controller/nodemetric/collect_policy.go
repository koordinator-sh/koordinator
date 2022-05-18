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

package nodemetric

import (
	"encoding/json"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func getNodeMetricCollectPolicy(node *corev1.Node, configMap *corev1.ConfigMap) (*slov1alpha1.NodeMetricCollectPolicy, error) {
	mergedPolicy := util.DefaultNodeMetricCollectPolicy()
	// When the custom parameter is missing, return to the default value
	cfgStr, ok := configMap.Data[config.NodeMetricConfigKey]
	if !ok {
		return mergedPolicy, nil
	}

	cfg := config.NodeMetricCollectCfg{}
	if err := json.Unmarshal([]byte(cfgStr), &cfg); err != nil {
		klog.Warningf("failed to unmarshal config %s, err: %s", config.NodeMetricConfigKey, err)
		return nil, err
	}

	// use cluster policy if no node policy matched
	if cfg.ClusterPolicy != nil {
		mergedStrategyInterface, _ := util.MergeCfg(mergedPolicy, cfg.ClusterPolicy)
		mergedPolicy = mergedStrategyInterface.(*slov1alpha1.NodeMetricCollectPolicy)
	}

	// NOTE: sort selectors by the string order
	sort.Slice(cfg.NodePolicies, func(i, j int) bool {
		return cfg.NodePolicies[i].NodeSelector.String() < cfg.NodePolicies[j].NodeSelector.String()
	})

	nodeLabels := labels.Set(node.Labels)
	for _, nodePolicy := range cfg.NodePolicies {
		selector, err := metav1.LabelSelectorAsSelector(nodePolicy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v, err: %v", nodePolicy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			// merge with the firstly-matched node policy
			if nodePolicy.NodeMetricCollectPolicy != nil {
				mergedStrategyInterface, _ := util.MergeCfg(mergedPolicy, nodePolicy.NodeMetricCollectPolicy)
				mergedPolicy = mergedStrategyInterface.(*slov1alpha1.NodeMetricCollectPolicy)
			}
			break
		}
	}

	return mergedPolicy, nil
}
