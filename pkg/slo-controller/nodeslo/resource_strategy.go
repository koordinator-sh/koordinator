package nodeslo

import (
	"encoding/json"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
)

func getResourceThresholdSpec(node *corev1.Node, configMap *corev1.ConfigMap) (*slov1alpha1.ResourceThresholdStrategy, error) {
	mergedStrategy := config.DefaultResourceThresholdStrategy()
	// When the custom parameter is missing, return to the default value
	cfgStr, ok := configMap.Data[config.ResourceThresholdConfigKey]
	if !ok {
		return mergedStrategy, nil
	}

	cfg := config.ResourceThresholdCfg{}
	if err := json.Unmarshal([]byte(cfgStr), &cfg); err != nil {
		klog.Warningf("failed to unmarshal config %s, err: %s", config.ResourceThresholdConfigKey, err)
		return nil, err
	}

	// use cluster strategy if no node strategy matched
	if cfg.ClusterStrategy != nil {
		mergedStrategyInterface, _ := util.MergeCfg(mergedStrategy, cfg.ClusterStrategy)
		mergedStrategy = mergedStrategyInterface.(*slov1alpha1.ResourceThresholdStrategy)
	}

	// NOTE: sort selectors by the string order
	sort.Slice(cfg.NodeStrategies, func(i, j int) bool {
		return cfg.NodeStrategies[i].NodeSelector.String() < cfg.NodeStrategies[j].NodeSelector.String()
	})

	nodeLabels := labels.Set(node.Labels)
	for _, nodeStrategy := range cfg.NodeStrategies {
		selector, err := metav1.LabelSelectorAsSelector(nodeStrategy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %v, err: %v", nodeStrategy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			// merge with the firstly-matched node strategy
			if nodeStrategy.ResourceThresholdStrategy != nil {
				mergedStrategyInterface, _ := util.MergeCfg(mergedStrategy, nodeStrategy.ResourceThresholdStrategy)
				mergedStrategy = mergedStrategyInterface.(*slov1alpha1.ResourceThresholdStrategy)
			}
			break
		}
	}

	return mergedStrategy, nil
}
