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

package config

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

var _ handler.EventHandler = &ColocationHandlerForConfigMapEvent{}

type ColocationCfgCache interface {
	GetCfgCopy() *ColocationCfg
	IsAvailable() bool
	IsErrorStatus() bool
}

type colocationCfgCache struct {
	lock          sync.RWMutex
	colocationCfg ColocationCfg
	available     bool
	errorStatus   bool
}

func (cache *colocationCfgCache) GetCfgCopy() *ColocationCfg {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	return cache.colocationCfg.DeepCopy()
}

func (cache *colocationCfgCache) IsAvailable() bool {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	return cache.available
}

func (cache *colocationCfgCache) IsErrorStatus() bool {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	return cache.errorStatus
}

type ColocationHandlerForConfigMapEvent struct {
	EnqueueRequestForConfigMap

	Client   client.Client
	cfgCache colocationCfgCache
}

func NewColocationHandlerForConfigMapEvent(client client.Client, initCfg ColocationCfg) *ColocationHandlerForConfigMapEvent {
	colocationHandler := &ColocationHandlerForConfigMapEvent{cfgCache: colocationCfgCache{colocationCfg: initCfg}, Client: client}
	colocationHandler.SyncCacheIfChanged = colocationHandler.syncColocationCfgIfChanged
	colocationHandler.EnqueueRequest = colocationHandler.triggerAllNodeEnqueue
	return colocationHandler
}

func (p *ColocationHandlerForConfigMapEvent) GetCache() ColocationCfgCache {
	return &p.cfgCache
}

// syncColocationCfgIfChanged syncs valid colocation config from the configmap request
func (p *ColocationHandlerForConfigMapEvent) syncColocationCfgIfChanged(configMap *corev1.ConfigMap) bool {
	// get co-location config from the configmap
	// if the configmap does not exist, use the default
	p.cfgCache.lock.Lock()
	defer p.cfgCache.lock.Unlock()

	if configMap == nil {
		klog.Errorf("configmap is deleted!,use default config")
		return p.updateCacheIfChanged(NewDefaultColocationCfg(), true)
	}

	newCfg := &ColocationCfg{}
	configStr := configMap.Data[ColocationConfigKey]
	if configStr == "" {
		klog.Warningf("colocation config is empty!,use default config")
		return p.updateCacheIfChanged(NewDefaultColocationCfg(), false)
	}

	err := json.Unmarshal([]byte(configStr), &newCfg)
	if err != nil {
		//if controller restart ,cache will unavailable, else use old cfg
		klog.Errorf("syncColocationCfgIfChanged failed! parse colocation error then use old Cfg ,configmap %s/%s, err: %s",
			ConfigNameSpace, SLOCtrlConfigMap, err)
		p.cfgCache.errorStatus = true
		return false
	}

	defaultCfg := NewDefaultColocationCfg()
	// merge default cluster strategy
	mergedClusterCfg := defaultCfg.ColocationStrategy.DeepCopy()
	mergedInterface, _ := util.MergeCfg(mergedClusterCfg, &newCfg.ColocationStrategy)
	newCfg.ColocationStrategy = *(mergedInterface.(*ColocationStrategy))

	if !IsColocationStrategyValid(&newCfg.ColocationStrategy) {
		//if controller restart ,cache will unavailable, else use old cfg
		klog.Errorf("syncColocationCfgIfChanged failed!  invalid cluster config,%+v", newCfg.ColocationStrategy)
		p.cfgCache.errorStatus = true
		return false
	}

	for index, nodeStrategy := range newCfg.NodeConfigs {
		// merge with clusterStrategy
		clusteStrategyCopy := newCfg.ColocationStrategy.DeepCopy()
		mergedNodeStrategyInterface, _ := util.MergeCfg(clusteStrategyCopy, &nodeStrategy.ColocationStrategy)
		newNodeStrategy := *mergedNodeStrategyInterface.(*ColocationStrategy)
		if !IsColocationStrategyValid(&newNodeStrategy) {
			klog.Errorf("syncColocationCfgIfChanged failed! invalid node config,then use clusterCfg,nodeCfg:%+v", nodeStrategy)
			newCfg.NodeConfigs[index].ColocationStrategy = *newCfg.ColocationStrategy.DeepCopy()
		} else {
			newCfg.NodeConfigs[index].ColocationStrategy = newNodeStrategy
		}
	}

	changed := p.updateCacheIfChanged(newCfg, false)
	return changed
}

func (p *ColocationHandlerForConfigMapEvent) updateCacheIfChanged(newCfg *ColocationCfg, errorStatus bool) bool {
	changed := !reflect.DeepEqual(&p.cfgCache.colocationCfg, newCfg)
	if changed {
		oldInfoFmt, _ := json.MarshalIndent(p.cfgCache.colocationCfg, "", "\t")
		newInfoFmt, _ := json.MarshalIndent(newCfg, "", "\t")
		klog.V(3).Infof("ColocationCfg changed success! oldCfg:%s\n,newCfg:%s", string(oldInfoFmt), string(newInfoFmt))
		p.cfgCache.colocationCfg = *newCfg
	}
	p.cfgCache.available = true
	p.cfgCache.errorStatus = errorStatus
	return changed
}

func (p *ColocationHandlerForConfigMapEvent) triggerAllNodeEnqueue(q *workqueue.RateLimitingInterface) {
	nodeList := &corev1.NodeList{}
	if err := p.Client.List(context.TODO(), nodeList); err != nil {
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
