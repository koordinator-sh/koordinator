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

package batchresource

import (
	"fmt"
	"math"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

const ratioDiffEpsilon = 0.01

type Rule struct {
	lock sync.RWMutex

	enableCFSQuota        *bool    // default = true
	cpuNormalizationRatio *float64 // default = -1, -1 means disabled
}

func newRule() *Rule {
	return &Rule{
		enableCFSQuota:        nil,
		cpuNormalizationRatio: nil,
	}
}

// GetCFSQuotaScaleRatio returns
// 1. if the cfs quota should be unlimited (-1 / max)
// 2. the scale ratio for the cfs quota when the quota is limited (-1 means do not scale)
func (r *Rule) GetCFSQuotaScaleRatio() (bool, float64) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	enabled := true
	if r.enableCFSQuota != nil {
		enabled = *r.enableCFSQuota
	}
	ratio := -1.0
	if r.cpuNormalizationRatio != nil {
		ratio = *r.cpuNormalizationRatio
	}
	if enabled {
		return true, ratio
	}
	return false, -1
}

func (r *Rule) UpdateCFSQuotaEnabled(enabled bool) bool {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.enableCFSQuota == nil {
		r.enableCFSQuota = &enabled
		return true
	}
	if *r.enableCFSQuota != enabled {
		*r.enableCFSQuota = enabled
		return true
	}
	return false
}

func (r *Rule) UpdateCPUNormalizationRatio(ratio float64) bool {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.cpuNormalizationRatio == nil {
		r.cpuNormalizationRatio = &ratio
		return true
	}
	if math.Abs(*r.cpuNormalizationRatio-ratio) >= ratioDiffEpsilon {
		*r.cpuNormalizationRatio = ratio
		return true
	}
	return false
}

func (p *plugin) parseRuleForNodeSLO(mergedNodeSLOIf interface{}) (bool, error) {
	mergedNodeSLO := mergedNodeSLOIf.(*slov1alpha1.NodeSLOSpec)

	enableCFSQuota := true
	// NOTE: If CPU Suppress Policy `CPUCfsQuotaPolicy` is enabled for batch pods, batch pods' cfs_quota should be unset
	// since the cfs quota of `kubepods-besteffort` is required to be no less than the children's. Then the cpu usage
	// of Batch is limited by pod-level cpu.shares and qos-level cfs_quota.
	if enable, policy := getCPUSuppressPolicy(mergedNodeSLO); enable && policy == slov1alpha1.CPUCfsQuotaPolicy {
		enableCFSQuota = false
	}

	isUpdated := p.rule.UpdateCFSQuotaEnabled(enableCFSQuota)
	if isUpdated {
		klog.V(4).Infof("runtime hook plugin %s update rule, new rule: CFSQuotaEnabled %v",
			ruleNameForNodeSLO, enableCFSQuota)
	}
	return isUpdated, nil
}

func (p *plugin) parseRuleForNodeMeta(nodeIf interface{}) (bool, error) {
	node, ok := nodeIf.(*corev1.Node)
	if !ok {
		return false, fmt.Errorf("type input %T is not *Node", nodeIf)
	}
	if node == nil {
		return false, fmt.Errorf("got nil node")
	}

	ratio, err := apiext.GetCPUNormalizationRatio(node)
	if err != nil {
		return false, fmt.Errorf("get cpu normalization ratio failed, err: %w", err)
	}

	isUpdated := p.rule.UpdateCPUNormalizationRatio(ratio)
	if isUpdated {
		klog.V(4).Infof("runtime hook plugin %s update rule, enabled %v, ratio %v",
			ruleNameForNodeMeta, ratio != -1, ratio)
	}
	return isUpdated, nil
}

func (p *plugin) ruleUpdateCbForNodeSLO(target *statesinformer.CallbackTarget) error {
	if target == nil {
		klog.Warningf("callback target is nil")
		return nil
	}
	if p.rule == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return nil
	}
	for _, podMeta := range target.Pods {
		podQOS := apiext.GetPodQoSClassRaw(podMeta.Pod)
		if podQOS != apiext.QoSBE {
			continue
		}

		// pod-level
		podCtx := &protocol.PodContext{}
		podCtx.FromReconciler(podMeta)
		if err := p.SetPodCFSQuota(podCtx); err != nil { // only need to change cfs quota
			klog.V(4).Infof("failed to set pod cfs quota during callback %v, err: %v", ruleNameForNodeSLO, err)
			continue
		}
		podCtx.ReconcilerDone(p.executor)

		// container-level
		for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
			containerCtx := &protocol.ContainerContext{}
			containerCtx.FromReconciler(podMeta, containerStat.Name, false)
			if err := p.SetContainerCFSQuota(containerCtx); err != nil {
				klog.V(4).Infof("failed to set container cfs quota during callback %v, container %v, err: %v",
					ruleNameForNodeSLO, containerStat.Name, err)
				continue
			}
			containerCtx.ReconcilerDone(p.executor)
		}
		// TODO set for sandbox container
	}

	return nil
}

func (p *plugin) ruleUpdateCbForNodeMeta(target *statesinformer.CallbackTarget) error {
	if target == nil {
		klog.Warningf("callback target is nil")
		return nil
	}
	// NOTE: if the ratio becomes bigger, scale top down, otherwise, scale bottom up
	if p.rule == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", ruleNameForNodeMeta)
		return nil
	}
	filter := reconciler.PodQOSFilter()
	var podUpdaters, containerUpdaters []resourceexecutor.ResourceUpdater
	for _, podMeta := range target.Pods {
		if qos := apiext.QoSClass(filter.Filter(podMeta)); qos != apiext.QoSBE {
			continue
		}

		// pod-level
		podCtx := &protocol.PodContext{}
		podCtx.FromReconciler(podMeta)
		if err := p.SetPodCFSQuota(podCtx); err != nil {
			klog.V(4).Infof("failed to set pod cfs quota during callback %s, pod %s, err: %s",
				ruleNameForNodeMeta, podMeta.Key(), err)
			continue
		}
		podCtx.ReconcilerProcess(p.executor)
		podUpdaters = append(podUpdaters, podCtx.GetUpdaters()...)
		klog.V(6).Infof("got %v updaters for pod %s during callback %s",
			len(podUpdaters), podMeta.Key(), ruleNameForNodeMeta)

		// container-level
		for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
			containerCtx := &protocol.ContainerContext{}
			containerCtx.FromReconciler(podMeta, containerStat.Name, false)
			if err := p.SetContainerCFSQuota(containerCtx); err != nil {
				klog.V(4).Infof("failed to set container cfs quota during callback %s, container %s/%s, err: %s",
					ruleNameForNodeMeta, podMeta.Key(), containerStat.Name, err)
				continue
			}
			containerCtx.ReconcilerProcess(p.executor)
			containerUpdaters = append(containerUpdaters, containerCtx.GetUpdaters()...)
			klog.V(6).Infof("got %v updaters for container %s/%s during callback %s",
				len(podUpdaters), podMeta.Key(), containerStat.Name, ruleNameForNodeMeta)
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

func getCPUSuppressPolicy(nodeSLOSpec *slov1alpha1.NodeSLOSpec) (bool, slov1alpha1.CPUSuppressPolicy) {
	if nodeSLOSpec == nil || nodeSLOSpec.ResourceUsedThresholdWithBE == nil ||
		nodeSLOSpec.ResourceUsedThresholdWithBE.CPUSuppressPolicy == "" {
		return *sloconfig.DefaultResourceThresholdStrategy().Enable,
			sloconfig.DefaultResourceThresholdStrategy().CPUSuppressPolicy
	}
	return *nodeSLOSpec.ResourceUsedThresholdWithBE.Enable,
		nodeSLOSpec.ResourceUsedThresholdWithBE.CPUSuppressPolicy
}
