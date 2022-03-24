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

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/common"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var _ handler.EventHandler = &EnqueueRequestForConfigMap{}

type EnqueueRequestForConfigMap struct {
	client.Client
	config *Config
}

func (n *EnqueueRequestForConfigMap) Create(e event.CreateEvent, q workqueue.RateLimitingInterface) {
	configMap, ok := e.Object.(*corev1.ConfigMap)
	if !ok {
		return
	}
	if configMap.Namespace != common.ConfigNameSpace || configMap.Name != common.KoordCtrlConfigMapName {
		return
	}
	if !n.syncColocationCfgIfChanged(configMap) {
		return
	}
	n.reconcileAllNodeMetric(&q)
}

func (n *EnqueueRequestForConfigMap) Update(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	newCM := e.ObjectNew.(*corev1.ConfigMap)
	oldCM := e.ObjectOld.(*corev1.ConfigMap)
	if reflect.DeepEqual(newCM.Data, oldCM.Data) {
		return
	}
	if newCM.Namespace != common.ConfigNameSpace || newCM.Name != common.KoordCtrlConfigMapName {
		return
	}
	if !n.syncColocationCfgIfChanged(newCM) {
		return
	}
	n.reconcileAllNodeMetric(&q)
}

func (n *EnqueueRequestForConfigMap) Delete(e event.DeleteEvent, q workqueue.RateLimitingInterface) {
	configMap, ok := e.Object.(*corev1.ConfigMap)
	if !ok {
		return
	}
	if configMap.Namespace != common.ConfigNameSpace || configMap.Name != common.KoordCtrlConfigMapName {
		return
	}
	if !n.syncColocationCfgIfChanged(configMap) {
		return
	}
	n.reconcileAllNodeMetric(&q)
}

func (n *EnqueueRequestForConfigMap) Generic(e event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (n *EnqueueRequestForConfigMap) syncColocationCfgIfChanged(configMap *corev1.ConfigMap) bool {
	n.config.Lock()
	defer n.config.Unlock()

	if configMap == nil {
		klog.Errorf("configmap is nil")
		return false
	}

	cfg := &config.ColocationCfg{}
	configStr := configMap.Data[common.ColocationConfigKey]
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		klog.Errorf("failed to parse colocation configmap %v/%v, error: %v",
			common.ConfigNameSpace, common.KoordCtrlConfigMapName, err)
		return false
	}

	defaultConfig := config.NewDefaultColocationCfg()
	merged, _ := util.Merge(&defaultConfig.ColocationStrategy, &cfg.ColocationStrategy)
	cfg.ColocationStrategy = *(merged.(*config.ColocationStrategy))

	if !config.IsColocationStrategyValid(&cfg.ColocationStrategy) {
		klog.Errorf("failed to validate colocation strategy")
		return false
	}

	if cfg.NodeConfigs != nil {
		for _, nodeCfg := range cfg.NodeConfigs {
			if !config.IsNodeColocationCfgValid(&nodeCfg) {
				klog.Errorf("failed to validate node colocatoin config %v", nodeCfg)
				return false
			}
		}
		sort.Slice(cfg.NodeConfigs, func(i, j int) bool {
			return cfg.NodeConfigs[i].NodeSelector.String() < cfg.NodeConfigs[j].NodeSelector.String()
		})
	}

	changed := !reflect.DeepEqual(&n.config.ColocationCfg, cfg)
	n.config.ColocationCfg = *cfg
	n.config.isAvailable = true

	return changed
}

func (n *EnqueueRequestForConfigMap) reconcileAllNodeMetric(q *workqueue.RateLimitingInterface) {
	nodeMetricList := &slov1alpha1.NodeMetricList{}
	if err := n.Client.List(context.TODO(), nodeMetricList); err != nil {
		return
	}
	for _, nodeMetric := range nodeMetricList.Items {
		(*q).Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: nodeMetric.Name,
			},
		})
	}
}
