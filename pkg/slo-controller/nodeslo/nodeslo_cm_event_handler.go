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

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var _ handler.EventHandler = &SLOCfgHandlerForConfigMapEvent{}

type SLOCfg struct {
	ThresholdCfgMerged   config.ResourceThresholdCfg `json:"thresholdCfgMerged,omitempty"`
	ResourceQoSCfgMerged config.ResourceQoSCfg       `json:"resourceQoSCfgMerged,omitempty"`
	CPUBurstCfgMerged    config.CPUBurstCfg          `json:"cpuBurstCfgMerged,omitempty"`
}

func (in *SLOCfg) DeepCopy() *SLOCfg {
	out := &SLOCfg{}
	out.ThresholdCfgMerged = *in.ThresholdCfgMerged.DeepCopy()
	out.CPUBurstCfgMerged = *in.CPUBurstCfgMerged.DeepCopy()
	out.ResourceQoSCfgMerged = *in.ResourceQoSCfgMerged.DeepCopy()
	return out
}

type SLOCfgCache struct {
	lock sync.RWMutex
	// Config could be concurrently used by the Reconciliation and EventHandler
	sloCfg    SLOCfg
	available bool
}

func (c *SLOCfgCache) GetSLOCfgCopy() *SLOCfg {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.sloCfg.DeepCopy()
}

func (c *SLOCfgCache) IsAvailable() bool {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.available
}

func DefaultSLOCfg() SLOCfg {
	return SLOCfg{
		ThresholdCfgMerged:   config.ResourceThresholdCfg{ClusterStrategy: util.DefaultResourceThresholdStrategy()},
		ResourceQoSCfgMerged: config.ResourceQoSCfg{ClusterStrategy: &slov1alpha1.ResourceQoSStrategy{}},
		CPUBurstCfgMerged:    config.CPUBurstCfg{ClusterStrategy: util.DefaultCPUBurstStrategy()},
	}
}

type SLOCfgHandlerForConfigMapEvent struct {
	config.EnqueueRequestForConfigMap

	Client      client.Client
	SLOCfgCache SLOCfgCache
}

func NewSLOCfgHandlerForConfigMapEvent(client client.Client, initCfg SLOCfg) *SLOCfgHandlerForConfigMapEvent {
	sloHandler := &SLOCfgHandlerForConfigMapEvent{SLOCfgCache: SLOCfgCache{sloCfg: initCfg}, Client: client}
	sloHandler.SyncCacheIfChanged = sloHandler.syncNodeSLOSpecIfChanged
	sloHandler.EnqueueRequest = sloHandler.triggerAllNodeEnqueue
	return sloHandler
}

func (p *SLOCfgHandlerForConfigMapEvent) triggerAllNodeEnqueue(q *workqueue.RateLimitingInterface) {
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

func (p *SLOCfgHandlerForConfigMapEvent) syncNodeSLOSpecIfChanged(configMap *corev1.ConfigMap) bool {
	p.SLOCfgCache.lock.Lock()
	defer p.SLOCfgCache.lock.Unlock()

	if configMap == nil {
		klog.Warningf("config map is deleted!,use default config")
		return p.updateCacheIfChanged(DefaultSLOCfg())
	}

	var newSLOCfg SLOCfg
	oldSLOCfgCopy := p.SLOCfgCache.sloCfg.DeepCopy()
	newSLOCfg.ThresholdCfgMerged, _ = caculateResourceThresholdCfgMerged(oldSLOCfgCopy.ThresholdCfgMerged, configMap)
	newSLOCfg.ResourceQoSCfgMerged, _ = caculateResourceQoSCfgMerged(oldSLOCfgCopy.ResourceQoSCfgMerged, configMap)
	newSLOCfg.CPUBurstCfgMerged, _ = caculateCPUBurstCfgMerged(oldSLOCfgCopy.CPUBurstCfgMerged, configMap)

	return p.updateCacheIfChanged(newSLOCfg)
}

func (p *SLOCfgHandlerForConfigMapEvent) updateCacheIfChanged(newSLOCfg SLOCfg) bool {
	changed := !reflect.DeepEqual(p.SLOCfgCache.sloCfg, newSLOCfg)

	if changed {
		oldInfoFmt, _ := json.MarshalIndent(p.SLOCfgCache.sloCfg, "", "\t")
		newInfoFmt, _ := json.MarshalIndent(newSLOCfg, "", "\t")
		klog.V(2).InfoS("NodeSLO config Changed success!", "oldCfg", string(oldInfoFmt), "newCfg", string(newInfoFmt))
		p.SLOCfgCache.sloCfg = newSLOCfg
	}
	// set the available flag and never change it
	p.SLOCfgCache.available = true
	return changed
}
