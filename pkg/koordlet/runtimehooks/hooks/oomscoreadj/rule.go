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

package oomscoreadj

import (
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

// Rule is stateless for OOMScoreAdj: the target value is driven purely by pod annotations.
// It tracks whether the initial AllPods sync has occurred so the reconciler can begin operating.
type Rule struct {
	allPodsSyncOnce sync.Once
}

func newRule() *Rule {
	return &Rule{}
}

func (p *Plugin) parseForAllPods(e interface{}) (bool, error) {
	_, ok := e.(*struct{})
	if !ok {
		return false, fmt.Errorf("invalid rule type %T", e)
	}

	needSync := false
	p.rule.allPodsSyncOnce.Do(func() {
		needSync = true
		klog.V(5).Infof("plugin %s received the first all-pods update callback", name)
	})
	return needSync, nil
}

func (p *Plugin) ruleUpdateCb(target *statesinformer.CallbackTarget) error {
	if target == nil {
		return fmt.Errorf("callback target is nil")
	}

	podMetas := target.Pods
	if len(podMetas) <= 0 {
		klog.V(5).Infof("plugin %s skipped for rule update, no pod passed from callback", name)
		return nil
	}

	return p.refreshForAllPods(podMetas)
}

func (p *Plugin) refreshForAllPods(podMetas []*statesinformer.PodMeta) error {
	for _, podMeta := range podMetas {
		if podMeta.Pod == nil {
			continue
		}
		if !podMeta.IsRunningOrPending() {
			klog.V(6).Infof("skip oom_score_adj for pod %s, pod is non-running, phase %s",
				podMeta.Key(), podMeta.Pod.Status.Phase)
			continue
		}

		// init containers (e.g. running sidecars during Pending) and regular containers;
		// the sandbox (pause) container is deliberately skipped since its oom_score_adj is
		// managed by the runtime
		containerStatuses := make([]corev1.ContainerStatus, 0,
			len(podMeta.Pod.Status.InitContainerStatuses)+len(podMeta.Pod.Status.ContainerStatuses))
		containerStatuses = append(containerStatuses, podMeta.Pod.Status.InitContainerStatuses...)
		containerStatuses = append(containerStatuses, podMeta.Pod.Status.ContainerStatuses...)
		for _, containerStat := range containerStatuses {
			containerCtx := &protocol.ContainerContext{}
			containerCtx.FromReconciler(podMeta, containerStat.Name, false)
			if err := p.SetContainerOOMScoreAdj(containerCtx); err != nil {
				klog.V(4).Infof("failed to set oom_score_adj for container %s/%s: %s",
					podMeta.Key(), containerStat.Name, err)
			}
		}
	}
	return nil
}
