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
	"fmt"
	"math"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

const ratioDiffEpsilon = 0.01

type Rule struct {
	lock     sync.RWMutex
	enable   bool
	curRatio float64
}

func newRule() *Rule {
	return &Rule{
		enable:   false,
		curRatio: -1,
	}
}

func (r *Rule) IsEnabled() bool {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return r.enable
}

func (r *Rule) GetCPUNormalizationRatio() float64 {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return r.curRatio
}

func (r *Rule) UpdateRule(ratio float64) bool {
	r.lock.Lock()
	defer r.lock.Unlock()
	enabled := true
	if ratio == -1 {
		enabled = false
	}
	if r.enable == enabled && math.Abs(r.curRatio-ratio) < ratioDiffEpsilon {
		return false
	}
	r.enable = enabled
	r.curRatio = ratio
	klog.V(6).Infof("update %s rule to [enable %v, curRatio %v]", name, enabled, ratio)
	return true
}

func (p *Plugin) parseRule(nodeIf interface{}) (bool, error) {
	node, ok := nodeIf.(*corev1.Node)
	if !ok {
		return false, fmt.Errorf("type input %T is not *Node", nodeIf)
	}
	if node == nil {
		return false, fmt.Errorf("got nil node")
	}

	ratio, err := extension.GetCPUNormalizationRatio(node)
	if err != nil {
		return false, fmt.Errorf("get cpu normalization ratio failed, err: %w", err)
	}

	isUpdated := p.rule.UpdateRule(ratio)
	if isUpdated {
		klog.V(4).Infof("runtime hook plugin %s update rule, enabled %v, ratio %v", name, ratio != -1, ratio)
	}
	return isUpdated, nil
}

func (p *Plugin) ruleUpdateCb(target *statesinformer.CallbackTarget) error {
	if target == nil {
		klog.Warningf("callback target is nil")
		return nil
	}
	// NOTE: if the ratio becomes bigger, scale top down, otherwise, scale bottom up
	r := p.rule
	if r == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return nil
	}
	filter := reconciler.PodQOSFilter()
	var podUpdaters, containerUpdaters []resourceexecutor.ResourceUpdater
	for _, podMeta := range target.Pods {
		if qos := extension.QoSClass(filter.Filter(podMeta)); qos != extension.QoSLS && qos != extension.QoSNone {
			continue
		}
		if !podMeta.IsRunningOrPending() {
			continue
		}

		// pod-level
		podCtx := &protocol.PodContext{}
		podCtx.FromReconciler(podMeta)
		if err := p.AdjustPodCFSQuota(podCtx); err != nil {
			klog.V(4).Infof("failed to adjust pod resources during callback %s, pod %s, err: %s",
				name, podMeta.Key(), err)
			continue
		}
		podCtx.ReconcilerProcess(p.executor)
		podUpdaters = append(podUpdaters, podCtx.GetUpdaters()...)
		klog.V(6).Infof("got %v updaters for pod %s during callback %s", len(podUpdaters), podMeta.Key(), name)

		// container-level
		for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
			containerCtx := &protocol.ContainerContext{}
			containerCtx.FromReconciler(podMeta, containerStat.Name, false)
			if err := p.AdjustContainerCFSQuota(containerCtx); err != nil {
				klog.V(4).Infof("failed to adjust container resources during callback %s, container %s/%s, err: %s",
					name, podMeta.Key(), containerStat.Name, err)
				continue
			}
			containerCtx.ReconcilerProcess(p.executor)
			containerUpdaters = append(containerUpdaters, containerCtx.GetUpdaters()...)
			klog.V(6).Infof("got %v updaters for container %s/%s during callback %s",
				len(podUpdaters), podMeta.Key(), containerStat.Name, name)
		}
		// ignore sandbox containers
	}

	// NOTE: Update cgroups by the level since some resources like cfs quota requires the upper level value is no less
	//       than the lower.
	p.executor.LeveledUpdateBatch([][]resourceexecutor.ResourceUpdater{
		podUpdaters,
		containerUpdaters,
	})

	return nil
}
