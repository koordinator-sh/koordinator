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

package resctrl

import (
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	util "github.com/koordinator-sh/koordinator/pkg/koordlet/util/resctrl"
)

type Rule struct {
	lock sync.RWMutex
}

func newRule() *Rule {
	return &Rule{}
}

func (p *plugin) parseRuleForAllPods(allPods interface{}) (bool, error) {
	return true, nil
}

func (p *plugin) ruleUpdateCbForAllPods(target *statesinformer.CallbackTarget) error {
	if target == nil {
		klog.Warningf("callback target is nil")
		return nil
	}

	if p.rule == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", ruleNameForAllPods)
		return nil
	}

	apps := p.engine.GetApps()

	currentPods := make(map[string]*corev1.Pod)
	for _, podMeta := range target.Pods {
		pod := podMeta.Pod
		if _, ok := podMeta.Pod.Annotations[apiext.AnnotationResctrl]; ok {
			group := string(podMeta.Pod.UID)
			currentPods[group] = pod
		}
	}

	for k, v := range apps {
		if _, ok := currentPods[k]; !ok {
			updater := NewRemoveResctrlUpdater(v.Closid)
			p.engine.UnRegisterApp(strings.TrimPrefix(v.Closid, util.ClosdIdPrefix), false, updater)
		}
	}
	return nil
}
