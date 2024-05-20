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

package cpunormalization

import (
	"context"
	"fmt"
	"strconv"

	topologyv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
)

const PluginName = "CPUNormalization"

const (
	defaultRatioStr = "1.00"
	// in case of unexpected resource amplification
	// NOTE: Currently we do not support the scaling factor below 1.0.
	defaultMinRatio = 1.0
	defaultMaxRatio = 5.0
)

var (
	client     ctrlclient.Client
	cfgHandler *configHandler
)

type Plugin struct{}

func (p *Plugin) Name() string {
	return PluginName
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=topology.node.k8s.io,resources=noderesourcetopologies,verbs=get;list;watch;create;update;patch;delete

func (p *Plugin) Setup(opt *framework.Option) error {
	client = opt.Client

	if err := topologyv1alpha1.AddToScheme(opt.Scheme); err != nil {
		return fmt.Errorf("failed to add scheme for NodeResourceTopology, err: %w", err)
	}
	if err := topologyv1alpha1.AddToScheme(clientgoscheme.Scheme); err != nil {
		return fmt.Errorf("failed to add client go scheme for NodeResourceTopology, err: %w", err)
	}
	opt.Builder = opt.Builder.Watches(&topologyv1alpha1.NodeResourceTopology{}, &nrtHandler{})

	cfgHandler = newConfigHandler(opt.Client, DefaultCPUNormalizationCfg(), opt.Recorder)
	opt.Builder = opt.Builder.Watches(&corev1.ConfigMap{}, cfgHandler)

	return nil
}

// NeedSyncMeta checks if the node annotation of cpu normalization ratio to update is different from the current
func (p *Plugin) NeedSyncMeta(_ *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	ratioOld, err := extension.GetCPUNormalizationRatio(oldNode)
	if err != nil {
		klog.V(4).Infof("failed to get old cpu normalization ratio for node %s, err: %s", oldNode.Name, err)
		return true, "old ratio parsed error"
	}
	ratioNew, err := extension.GetCPUNormalizationRatio(newNode)
	if err != nil {
		klog.V(4).Infof("failed to get new cpu normalization ratio for node %s, err: %s", newNode.Name, err)
		return false, "new ratio parsed error"
	}

	if ratioNew == -1 && ratioOld == -1 { // no annotation to set
		return false, "ratios are nil"
	}
	if ratioOld == -1 { // annotation to create
		return true, "old ratio is nil"
	}
	if ratioNew == -1 { // annotation to remove
		return true, "new ratio is nil"
	}
	if !extension.IsCPUNormalizationRatioDifferent(ratioOld, ratioNew) {
		return false, "ratios are close"
	}

	return true, "ratio is different"
}

func (p *Plugin) Prepare(_ *configuration.ColocationStrategy, node *corev1.Node, nr *framework.NodeResource) error {
	ratioStr, ok := nr.Annotations[extension.AnnotationCPUNormalizationRatio]
	if !ok {
		klog.V(6).Infof("skip for whose has no cpu normalization ratio to set, node %s", node.Name)
		return nil
	}

	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[extension.AnnotationCPUNormalizationRatio] = ratioStr
	klog.V(6).Infof("prepare node cpu normalization ratio to set, node %s, ratio %s", node.Name, ratioStr)

	return nil
}

func (p *Plugin) Reset(node *corev1.Node, message string) []framework.ResourceItem {
	// TBD: do we need to delete existing annotations/labels?
	return nil
}

// Calculate retrieves the CPUNormalizationStrategy from ConfigMap and the CPUBasicInfo from the NRT, get the cpu
// normalization ratio annotation
func (p *Plugin) Calculate(_ *configuration.ColocationStrategy, node *corev1.Node, _ *corev1.PodList, _ *framework.ResourceMetrics) ([]framework.ResourceItem, error) {
	// If any of the necessary inputs like config, CPUBasicInfo is missing, abort to output and skip updating the
	// node annotation.
	if !cfgHandler.IsCfgAvailable() {
		return nil, fmt.Errorf("failed to get config in cpu normalization calculation, err: config is not available")
	}
	strategy := cfgHandler.GetStrategyCopy(node)
	nodeEnabled, err := extension.GetCPUNormalizationEnabled(node)
	if err != nil {
		return nil, fmt.Errorf("failed to get node label in cpu normalization calculation, err: %w", err)
	}

	// node label take precedence to the strategy, <nodeEnabled, strategyEnabled>:
	// - <true, strategyEnabled> -> true
	// - <false, strategyEnabled> -> false
	// - <nil, true> -> true
	// - otherwise -> false
	isEnabled := false
	if nodeEnabled != nil {
		isEnabled = *nodeEnabled
	} else if strategy.Enable != nil {
		isEnabled = *strategy.Enable
	}
	if !isEnabled {
		klog.V(6).Infof("cpu normalization is disabled for node %s, reset to ratio %s", node.Name, defaultRatioStr)
		return []framework.ResourceItem{
			{
				Name: PluginName,
				Annotations: map[string]string{
					extension.AnnotationCPUNormalizationRatio: defaultRatioStr,
				},
			},
		}, nil
	}

	nrt := &topologyv1alpha1.NodeResourceTopology{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: node.Name}, nrt)
	if err != nil || nrt == nil {
		return nil, fmt.Errorf("failed to get NodeResourceTopology in cpu normalization calculation, err: %w", err)
	}
	basicInfo, err := extension.GetCPUBasicInfo(nrt.Annotations)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CPUBasicInfo in cpu normalization calculation, err: %w", err)
	}
	if basicInfo == nil {
		return nil, fmt.Errorf("failed to get CPUBasicInfo in cpu normalization calculation, err: info is missing")
	}

	ratio, err := getCPUNormalizationRatioFromModel(basicInfo, strategy)
	if err != nil {
		return nil, fmt.Errorf("failed to get ratio in cpu normalization calculation, err: %s", err)
	}
	if err = isCPUNormalizationRatioValid(ratio); err != nil {
		return nil, fmt.Errorf("failed to validate ratio in cpu normalization calculation, ratio %v, err: %s", ratio, err)
	}

	ratioStr := strconv.FormatFloat(ratio, 'f', 2, 64)
	klog.V(6).Infof("calculate cpu normalization ratio %s(%v) for node %s", ratioStr, ratio, node.Name)
	return []framework.ResourceItem{
		{
			Name: PluginName,
			Annotations: map[string]string{
				extension.AnnotationCPUNormalizationRatio: ratioStr,
			},
		},
	}, nil
}

func isCPUBasicInfoChanged(infoOld, infoNew *extension.CPUBasicInfo) (bool, string) {
	if infoOld == nil && infoNew == nil {
		return false, "infos are nil"
	}
	if infoOld == nil {
		return true, "info is created"
	}
	if infoNew == nil {
		return true, "info is deleted"
	}

	if infoOld.CPUModel != infoNew.CPUModel {
		return true, "cpu model changed"
	}
	if infoOld.HyperThreadEnabled != infoNew.HyperThreadEnabled {
		return true, "HyperThread status changed"
	}
	if infoOld.TurboEnabled != infoNew.TurboEnabled {
		return true, "Turbo status changed"
	}

	return false, ""
}

func getCPUNormalizationRatioFromModel(info *extension.CPUBasicInfo, strategy *configuration.CPUNormalizationStrategy) (float64, error) {
	if strategy.RatioModel == nil {
		return -1, fmt.Errorf("ratio model is nil")
	}
	ratioCfg, ok := strategy.RatioModel[info.CPUModel]
	if !ok {
		return -1, fmt.Errorf("no ratio for CPU %s", info.CPUModel)
	}

	if info.HyperThreadEnabled && info.TurboEnabled { // HT=on, Turbo=on
		if ratioCfg.HyperThreadTurboEnabledRatio == nil {
			return -1, fmt.Errorf("missing HyperThreadTurboEnabledRatio for CPU %s", info.CPUModel)
		}
		return *ratioCfg.HyperThreadTurboEnabledRatio, nil
	}
	if info.HyperThreadEnabled { // HT=on, Turbo=off
		if ratioCfg.HyperThreadEnabledRatio == nil {
			return -1, fmt.Errorf("missing HyperThreadEnabledRatio for CPU %s", info.CPUModel)
		}
		return *ratioCfg.HyperThreadEnabledRatio, nil
	}
	if info.TurboEnabled { // HT=off, Turbo=on
		if ratioCfg.TurboEnabledRatio == nil {
			return -1, fmt.Errorf("missing TurboEnabledRatio for CPU %s", info.CPUModel)
		}
		return *ratioCfg.TurboEnabledRatio, nil
	}
	if ratioCfg.BaseRatio == nil { // HT=off, Turbo=off
		return -1, fmt.Errorf("missing BaseRatio for CPU %s", info.CPUModel)
	}
	return *ratioCfg.BaseRatio, nil
}

func isCPUNormalizationRatioValid(ratio float64) error {
	if ratio < defaultMinRatio {
		return fmt.Errorf("the ratio is too small, cur[%v] < min[%v]", ratio, defaultMinRatio)
	}
	if ratio > defaultMaxRatio {
		return fmt.Errorf("the ratio is too big, cur[%v] > max[%v]", ratio, defaultMaxRatio)
	}

	return nil
}
