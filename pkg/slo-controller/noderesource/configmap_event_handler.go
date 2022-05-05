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

package noderesource

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var _ handler.EventHandler = &EnqueueRequestForConfigMap{}

type EnqueueRequestForConfigMap struct {
	client.Client
	Config *Config
}

func (n *EnqueueRequestForConfigMap) Create(e event.CreateEvent, q workqueue.RateLimitingInterface) {
	configMap, ok := e.Object.(*corev1.ConfigMap)
	if !ok {
		return
	}
	if configMap.Namespace != config.ConfigNameSpace || configMap.Name != config.SLOCtrlConfigMap {
		return
	}
	if !n.syncColocationCfgIfChanged(configMap) {
		return
	}
	n.triggerAllNodeReconcile(&q)
}

func (n *EnqueueRequestForConfigMap) Update(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	newConfigMap := e.ObjectNew.(*corev1.ConfigMap)
	oldConfigMap := e.ObjectOld.(*corev1.ConfigMap)
	if reflect.DeepEqual(newConfigMap.Data, oldConfigMap.Data) {
		return
	}
	if newConfigMap.Namespace != config.ConfigNameSpace || newConfigMap.Name != config.SLOCtrlConfigMap {
		return
	}
	n.triggerAllNodeReconcile(&q)

}

func (n *EnqueueRequestForConfigMap) Delete(e event.DeleteEvent, q workqueue.RateLimitingInterface) {
	configMap, ok := e.Object.(*corev1.ConfigMap)
	if !ok {
		return
	}
	if configMap.Namespace != config.ConfigNameSpace || configMap.Name != config.SLOCtrlConfigMap {
		return
	}
	if !n.syncColocationCfgIfChanged(configMap) {
		return
	}
	n.triggerAllNodeReconcile(&q)
}

func (n *EnqueueRequestForConfigMap) Generic(e event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (n *EnqueueRequestForConfigMap) syncColocationCfgIfChanged(configMap *corev1.ConfigMap) bool {
	n.Config.Lock()
	defer n.Config.Unlock()

	if configMap == nil {
		klog.Errorf("configmap is nil")
		return false
	}

	cfg := &config.ColocationCfg{}
	configStr := configMap.Data[config.ColocationConfigKey]
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		klog.Errorf("failed to parse colocation configmap %v/%v, error: %v",
			config.ConfigNameSpace, config.SLOCtrlConfigMap, err)
		return false
	}

	defaultConfig := config.NewDefaultColocationCfg()
	merged, _ := util.MergeCfg(&defaultConfig.ColocationStrategy, &cfg.ColocationStrategy)
	cfg.ColocationStrategy = *(merged.(*config.ColocationStrategy))

	if !config.IsColocationStrategyValid(&cfg.ColocationStrategy) {
		klog.Errorf("failed to validate colocation strategy")
		return false
	}

	if cfg.NodeConfigs != nil {
		for _, nodeCfg := range cfg.NodeConfigs {
			if !config.IsNodeColocationCfgValid(&nodeCfg) {
				klog.Errorf("failed to validate node colocatoin Config %v", nodeCfg)
				return false
			}
		}
		sort.Slice(cfg.NodeConfigs, func(i, j int) bool {
			return cfg.NodeConfigs[i].NodeSelector.String() < cfg.NodeConfigs[j].NodeSelector.String()
		})
	}

	changed := !reflect.DeepEqual(&n.Config.ColocationCfg, cfg)
	n.Config.ColocationCfg = *cfg
	n.Config.isAvailable = true

	return changed
}

func (n *EnqueueRequestForConfigMap) triggerAllNodeReconcile(q *workqueue.RateLimitingInterface) {
	nodeList := &corev1.NodeList{}
	if err := n.Client.List(context.TODO(), nodeList); err != nil {
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
