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

package coresched

import (
	"fmt"
	"reflect"
	"sort"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type Param struct {
	IsPodEnabled bool
	IsExpeller   bool
	IsCPUIdle    bool
}

func newParam(qosCfg *slov1alpha1.CPUQOSCfg, policy slov1alpha1.CPUQOSPolicy) Param {
	isPolicyCoreSched := policy == slov1alpha1.CPUQOSPolicyCoreSched
	return Param{
		IsPodEnabled: isPolicyCoreSched && *qosCfg.Enable,
		IsExpeller:   isPolicyCoreSched && *qosCfg.CoreExpeller,
		IsCPUIdle:    isPolicyCoreSched && *qosCfg.SchedIdle == 1,
	}
}

type Rule struct {
	lock             sync.RWMutex
	enable           bool // node-level switch
	podQOSParams     map[extension.QoSClass]Param
	kubeQOSPodParams map[corev1.PodQOSClass]Param
}

func newRule() *Rule {
	return &Rule{
		enable:           false,
		podQOSParams:     make(map[extension.QoSClass]Param),
		kubeQOSPodParams: make(map[corev1.PodQOSClass]Param),
	}
}

func (r *Rule) IsInited() bool {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return len(r.podQOSParams) > 0 && len(r.kubeQOSPodParams) > 0
}

func (r *Rule) IsEnabled() bool {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return r.enable
}

// IsPodEnabled returns if the pod's core sched is enabled by the rule, and if the QoS-level core expeller is enabled.
func (r *Rule) IsPodEnabled(podQoSClass extension.QoSClass, podKubeQOS corev1.PodQOSClass) (bool, bool) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	if !r.enable {
		return false, false
	}
	if val, exist := r.podQOSParams[podQoSClass]; exist {
		return val.IsPodEnabled, val.IsExpeller
	}
	if val, exist := r.kubeQOSPodParams[podKubeQOS]; exist {
		return val.IsPodEnabled, val.IsExpeller
	}
	// core sched is not needed for all types of pods, so it should be disabled by default
	return false, false
}

func (r *Rule) IsKubeQOSCPUIdle(KubeQOS corev1.PodQOSClass) bool {
	r.lock.RLock()
	defer r.lock.RUnlock()
	if val, exist := r.kubeQOSPodParams[KubeQOS]; exist {
		return val.IsCPUIdle
	}
	// cpu idle disabled by default
	return false
}

func (r *Rule) Update(ruleNew *Rule) bool {
	r.lock.Lock()
	defer r.lock.Unlock()
	isEqual := r.enable == ruleNew.enable &&
		reflect.DeepEqual(r.podQOSParams, ruleNew.podQOSParams) &&
		reflect.DeepEqual(r.kubeQOSPodParams, ruleNew.kubeQOSPodParams)
	if isEqual {
		return false
	}
	r.enable = ruleNew.enable
	r.podQOSParams = ruleNew.podQOSParams
	r.kubeQOSPodParams = ruleNew.kubeQOSPodParams
	return true
}

func (p *Plugin) parseRuleForNodeSLO(mergedNodeSLOIf interface{}) (bool, error) {
	mergedNodeSLO := mergedNodeSLOIf.(*slov1alpha1.NodeSLOSpec)
	qosStrategy := mergedNodeSLO.ResourceQOSStrategy

	// default policy disables
	cpuPolicy := slov1alpha1.CPUQOSPolicy("")
	if qosStrategy.Policies != nil && qosStrategy.Policies.CPUPolicy != nil {
		cpuPolicy = *qosStrategy.Policies.CPUPolicy
	}
	lsrQOS := qosStrategy.LSRClass.CPUQOS
	lsQOS := qosStrategy.LSClass.CPUQOS
	beQOS := qosStrategy.BEClass.CPUQOS

	// setting pod rule by qos config
	lsrValue := newParam(lsrQOS, cpuPolicy)
	lsValue := newParam(lsQOS, cpuPolicy)
	beValue := newParam(beQOS, cpuPolicy)
	// setting guaranteed pod enabled if LS or LSR enabled
	guaranteedPodVal := lsValue
	if lsrValue.IsPodEnabled {
		guaranteedPodVal = lsrValue
	}

	ruleNew := &Rule{
		enable: lsrValue.IsPodEnabled || lsValue.IsPodEnabled || beValue.IsPodEnabled,
		podQOSParams: map[extension.QoSClass]Param{
			extension.QoSLSE: lsrValue,
			extension.QoSLSR: lsrValue,
			extension.QoSLS:  lsValue,
			extension.QoSBE:  beValue,
		},
		kubeQOSPodParams: map[corev1.PodQOSClass]Param{
			corev1.PodQOSGuaranteed: guaranteedPodVal,
			corev1.PodQOSBurstable:  lsValue,
			corev1.PodQOSBestEffort: beValue,
		},
	}

	updated := p.rule.Update(ruleNew)
	if updated {
		klog.V(4).Infof("runtime hook plugin %s parse rule %v, update new rule %+v", name, updated, ruleNew)
	} else {
		klog.V(6).Infof("runtime hook plugin %s parse rule unchanged, rule %+v", name, ruleNew)
	}
	return updated, nil
}

func (p *Plugin) parseForAllPods(e interface{}) (bool, error) {
	_, ok := e.(*struct{})
	if !ok {
		return false, fmt.Errorf("invalid rule type %T", e)
	}

	needSync := false
	p.allPodsSyncOnce.Do(func() {
		needSync = true
		klog.V(5).Infof("plugin %s callback the first all pods update", name)
	})
	return needSync, nil
}

func (p *Plugin) ruleUpdateCb(target *statesinformer.CallbackTarget) error {
	if target == nil {
		return fmt.Errorf("callback target is nil")
	}
	if !p.rule.IsInited() {
		klog.V(4).Infof("plugin %s skipped for rule not initialized", name)
		return nil
	}

	// check the kernel feature and enable if needed
	if !p.SystemSupported() {
		klog.V(4).Infof("plugin %s is not supported by system, msg: %s", name, p.supportedMsg)
		return nil
	}

	podMetas := target.Pods
	if len(podMetas) <= 0 {
		klog.V(5).Infof("plugin %s skipped for rule update, no pod passed from callback", name)
		return nil
	}

	if err := p.initSystem(p.rule.IsEnabled()); err != nil {
		klog.Warningf("plugin %s failed to initialize system, err: %s", name, err)
		return nil
	}
	klog.V(6).Infof("plugin %s initialize system successfully", name)

	if !p.initCache(podMetas) {
		klog.V(4).Infof("plugin %s aborted for cookie cache has not been initialized", name)
		return nil
	}

	return p.refreshForAllPods(podMetas)
}

func (p *Plugin) refreshForAllPods(podMetas []*statesinformer.PodMeta) error {
	for _, kubeQOS := range []corev1.PodQOSClass{
		corev1.PodQOSGuaranteed, corev1.PodQOSBurstable, corev1.PodQOSBestEffort} {
		kubeQOSCtx := &protocol.KubeQOSContext{}
		kubeQOSCtx.FromReconciler(kubeQOS)

		if err := p.SetKubeQOSCPUIdle(kubeQOSCtx); err != nil {
			klog.V(4).Infof("callback %s set cpu idle for kube qos %s failed, err: %v", name, kubeQOS, err)
		} else {
			kubeQOSCtx.ReconcilerDone(p.executor)
			klog.V(5).Infof("callback %s set cpu idle for kube qos %s finished", name, kubeQOS)
		}
	}

	sort.Slice(podMetas, func(i, j int) bool {
		if podMetas[i].Pod == nil || podMetas[j].Pod == nil {
			return podMetas[j].Pod == nil
		}
		return podMetas[i].Pod.CreationTimestamp.Before(&podMetas[j].Pod.CreationTimestamp)
	})

	filter := reconciler.PodQOSFilter()
	for _, podMeta := range podMetas {
		if podMeta.Pod == nil {
			continue
		}
		if qos := extension.QoSClass(filter.Filter(podMeta)); qos == extension.QoSSystem {
			klog.V(6).Infof("skip refresh core sched cookie for pod %s whose QoS is SYSTEM", podMeta.Key())
			continue
		}

		// sandbox-container-level
		sandboxContainerCtx := &protocol.ContainerContext{}
		sandboxContainerCtx.FromReconciler(podMeta, "", true)
		if err := p.SetContainerCookie(sandboxContainerCtx); err != nil {
			klog.Warningf("failed to set core sched cookie for pod sandbox %v, err: %s", podMeta.Key(), err)
		} else {
			klog.V(5).Infof("set core sched cookie for pod sandbox %v finished", podMeta.Key())
		}

		// container-level
		for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
			containerCtx := &protocol.ContainerContext{}
			containerCtx.FromReconciler(podMeta, containerStat.Name, false)
			if err := p.SetContainerCookie(containerCtx); err != nil {
				klog.Warningf("failed to set core sched cookie for container %s/%s, err: %s",
					podMeta.Key(), containerStat.Name, err)
				continue
			} else {
				klog.V(5).Infof("set core sched cookie for container %s/%s finished",
					podMeta.Key(), containerStat.Name)
			}
		}
	}

	return nil
}
