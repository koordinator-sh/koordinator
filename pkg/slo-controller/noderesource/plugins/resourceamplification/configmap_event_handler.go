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
	"context"
	"encoding/json"
	"reflect"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

const (
	ReasonResourceAmplificationConfigUnmarshalFailed = "ResourceAmplificationCfgUnmarshalFailed"
)

type cfgCache struct {
	sync.RWMutex

	config    *configuration.ResourceAmplificationCfg
	available bool
}

func DefaultResourceAmplificationCfg() *configuration.ResourceAmplificationCfg {
	return &configuration.ResourceAmplificationCfg{
		ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
			Enable: pointer.Bool(false),
		},
	}
}

type configHandler struct {
	config.EnqueueRequestForConfigMap

	Client   ctrlclient.Client
	cache    *cfgCache
	recorder record.EventRecorder
}

func newConfigHandler(c ctrlclient.Client, initCfg *configuration.ResourceAmplificationCfg, recorder record.EventRecorder) *configHandler {
	h := &configHandler{
		cache: &cfgCache{
			config: initCfg,
		},
		Client:   c,
		recorder: recorder,
	}
	h.SyncCacheIfChanged = h.syncCacheIfCfgChanged
	h.EnqueueRequest = h.enqueueAllNodes
	return h
}

func (h *configHandler) IsCfgAvailable() bool {
	h.cache.Lock()
	defer h.cache.Unlock()

	if h.cache.available {
		return true
	}

	// if config is not available, try to get the configmap from informer cache;
	// set available if configmap is found or get not found error
	configMap, err := config.GetConfigMapForCache(h.Client)
	if err != nil {
		klog.Errorf("failed to get configmap %s/%s, ResourceAmplificationCfg cache is unavailable, err: %s",
			sloconfig.ConfigNameSpace, sloconfig.SLOCtrlConfigMap, err)
		return false
	}
	h.syncConfig(configMap)
	klog.V(5).Infof("sync ResourceAmplificationCfg cache from configmap %s/%s, available %v",
		sloconfig.ConfigNameSpace, sloconfig.SLOCtrlConfigMap, h.cache.available)

	return h.cache.available
}

func (h *configHandler) GetCfgCopy() *configuration.ResourceAmplificationCfg {
	h.cache.RLock()
	defer h.cache.RUnlock()
	return h.cache.config.DeepCopy()
}

func (h *configHandler) GetStrategyCopy(node *corev1.Node) *configuration.ResourceAmplificationStrategy {
	h.cache.RLock()
	defer h.cache.RUnlock()
	// assert cache is available
	nodeLabels := labels.Set(node.Labels)
	for _, nodeStrategy := range h.cache.config.NodeConfigs {
		selector, err := metav1.LabelSelectorAsSelector(nodeStrategy.NodeSelector)
		if err != nil {
			klog.Errorf("failed to parse node selector %+v for resource amplification, err: %v", nodeStrategy.NodeSelector, err)
			continue
		}
		if selector.Matches(nodeLabels) {
			return nodeStrategy.ResourceAmplificationStrategy.DeepCopy()
		}
	}

	// use cluster strategy
	return h.cache.config.ResourceAmplificationStrategy.DeepCopy()
}

func (h *configHandler) enqueueAllNodes(q *workqueue.RateLimitingInterface) {
	nodeList := &corev1.NodeList{}
	if err := h.Client.List(context.TODO(), nodeList); err != nil {
		return
	}

	for _, node := range nodeList.Items {
		(*q).Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: node.Name,
			},
		})
	}
}

func (h *configHandler) syncCacheIfCfgChanged(configMap *corev1.ConfigMap) bool {
	h.cache.Lock()
	defer h.cache.Unlock()
	return h.syncConfig(configMap)
}

func (h *configHandler) syncConfig(configMap *corev1.ConfigMap) bool {
	if configMap == nil {
		klog.Errorf("failed to sync configmap for resource amplification, use default config, err: configmap is missing")
		return h.updateCacheIfChanged(DefaultResourceAmplificationCfg())
	}

	mergedCfg := &configuration.ResourceAmplificationCfg{}
	cfgStr, ok := configMap.Data[configuration.ResourceAmplificationConfigKey]
	if !ok {
		klog.V(5).Infof("aborted to sync resource amplification config since no config key, use the default config")
		return h.updateCacheIfChanged(DefaultResourceAmplificationCfg())
	}

	if err := json.Unmarshal([]byte(cfgStr), &mergedCfg); err != nil {
		klog.Errorf("failed to unmarshal config %s, keep the old config, err: %s", configuration.ResourceAmplificationConfigKey, err)
		h.recorder.Eventf(configMap, "Warning", ReasonResourceAmplificationConfigUnmarshalFailed, "failed to unmarshal amplificationCfg, err: %s", err)
		return false
	}

	clusterMerged := DefaultResourceAmplificationCfg().ResourceAmplificationStrategy
	mergedIf, _ := util.MergeCfg(&clusterMerged, &mergedCfg.ResourceAmplificationStrategy)
	mergedCfg.ResourceAmplificationStrategy = *(mergedIf.(*configuration.ResourceAmplificationStrategy))

	for i, nodeStrategy := range mergedCfg.NodeConfigs {
		// merge with clusterStrategy
		clusterCfgCopy := mergedCfg.ResourceAmplificationStrategy.DeepCopy()
		mergedNodeStrategyIf, _ := util.MergeCfg(clusterCfgCopy, &nodeStrategy.ResourceAmplificationStrategy)
		mergedCfg.NodeConfigs[i].ResourceAmplificationStrategy = *(mergedNodeStrategyIf.(*configuration.ResourceAmplificationStrategy))
	}

	return h.updateCacheIfChanged(mergedCfg)
}

func (h *configHandler) updateCacheIfChanged(newCfg *configuration.ResourceAmplificationCfg) bool {
	changed := !reflect.DeepEqual(h.cache.config, newCfg)
	if changed {
		oldInfoFmt, _ := json.MarshalIndent(h.cache.config, "", "\t")
		newInfoFmt, _ := json.MarshalIndent(newCfg, "", "\t")
		klog.V(4).Infof("ResourceAmplificationCfg changed successfully, oldCfg: %s\n, newCfg: %s", string(oldInfoFmt), string(newInfoFmt))
		h.cache.config = newCfg
	}
	h.cache.available = true
	return changed
}
