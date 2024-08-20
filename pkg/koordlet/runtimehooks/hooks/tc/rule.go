//go:build linux
// +build linux

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

package tc

import (
	"errors"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type tcRule struct {
	enable      bool
	netCfg      *NetQosGlobalConfig
	speed       uint64
	uidToHandle map[types.UID]uint32
	handleToUid map[uint32]types.UID
}

func newRule() *tcRule {
	return &tcRule{
		enable:      false,
		netCfg:      nil,
		speed:       0,
		uidToHandle: map[types.UID]uint32{},
		handleToUid: map[uint32]types.UID{},
	}
}

func (p *tcPlugin) getRule() *tcRule {
	p.ruleRWMutex.RLock()
	defer p.ruleRWMutex.RUnlock()
	if p.rule == nil {
		return nil
	}
	rule := *p.rule
	return &rule
}

func (p *tcPlugin) updateRule(newRule *tcRule) bool {
	p.ruleRWMutex.RLock()
	defer p.ruleRWMutex.RUnlock()
	if !reflect.DeepEqual(newRule, p.rule) {
		p.rule = newRule
		return true
	}
	return false
}

func (p *tcPlugin) parseRuleForNodeSLO(mergedNodeSLOIf interface{}) (bool, error) {
	mergedNodeSLO := mergedNodeSLOIf.(*slov1alpha1.NodeSLOSpec)
	if mergedNodeSLO == nil {
		return false, nil
	}
	qosStrategy := mergedNodeSLO.ResourceQOSStrategy

	// default policy enables
	isNETQOSPolicyTC := qosStrategy == nil || qosStrategy.Policies == nil || qosStrategy.Policies.NETQOSPolicy == nil ||
		*qosStrategy.Policies.NETQOSPolicy == slov1alpha1.NETQOSPolicyTC

	newRule := p.getRule()
	if isNETQOSPolicyTC {
		if mergedNodeSLO.SystemStrategy == nil {
			return false, nil
		}
		newRule.enable = true
		newRule.speed = uint64(mergedNodeSLO.SystemStrategy.TotalNetworkBandwidth.Value())
		newRule.netCfg = loadConfigFromNodeSlo(mergedNodeSLO)
	} else {
		newRule.enable = false
	}

	updated := p.updateRule(newRule)
	return updated, nil
}

func (p *tcPlugin) parseForAllPods(e interface{}) (bool, error) {
	_, ok := e.(*struct{})
	if !ok {
		return false, fmt.Errorf("invalid rule type %T", e)
	}

	return true, nil
}

func (p *tcPlugin) ruleUpdateCbForNodeSlo(target *statesinformer.CallbackTarget) error {
	if err := p.prepare(); err != nil {
		return err
	}

	r := p.getRule()
	if r == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return nil
	}

	if r.enable {
		klog.V(5).Infof("tc plugin is enabled, ready to init related rules.")
		return p.InitRelatedRules()
	}

	klog.V(5).Infof("tc plugin is not enabled, ready to cleanup related rules.")
	return p.CleanUp()
}

func (p *tcPlugin) ruleUpdateCbForPod(target *statesinformer.CallbackTarget) error {
	if target == nil {
		return errors.New("callback target is nil")
	}
	podMetas := target.Pods
	if len(podMetas) <= 0 {
		klog.V(5).Infof("plugin %s skipped for rule update, no pod passed from callback", name)
		return nil
	}

	if err := p.prepare(); err != nil {
		return err
	}

	r := p.getRule()
	if r == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return nil
	}

	// cache classId from net_cls cgroup when first sync.
	p.allPodsSyncOnce.Do(func() {
		// mark the class that has been used
		r.handleToUid[rootClass] = ""
		r.handleToUid[systemClass] = ""
		r.handleToUid[lsClass] = ""
		r.handleToUid[beClass] = ""

		cgroupReader := resourceexecutor.NewCgroupReader()
		for _, pod := range podMetas {
			if value, ok := r.uidToHandle[pod.Pod.UID]; ok || value == 0 {
				continue
			}

			// netClsId is a decimal number.
			netClsId, err := cgroupReader.ReadNetClsId(pod.CgroupDir)
			if err != nil || netClsId == 0 {
				continue
			}
			r.uidToHandle[pod.Pod.UID] = netClsId
			r.handleToUid[netClsId] = pod.Pod.UID
			p.updateRule(r)
		}
	})

	return p.refreshForAllPods(podMetas, r)
}
