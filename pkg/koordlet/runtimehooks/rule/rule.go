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

package rule

import (
	"reflect"
	"runtime"
	"sync"

	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type Rule struct {
	name            string
	description     string
	parseFunc       ParseRuleFn
	callbacks       []UpdateCbFn
	systemSupported bool
}

type ParseRuleFn func(nodeSLO *slov1alpha1.NodeSLOSpec) (bool, error)
type UpdateCbFn func(pods []*statesinformer.PodMeta) error
type SysSupportFn func() bool

var globalHookRules map[string]*Rule
var globalRWMutex sync.RWMutex

func Register(name, description string, injectOpts ...InjectOption) *Rule {
	r, exist := find(name)
	if exist {
		klog.Fatalf("rule %s is conflict since name is already registered")
		return r
	}
	r.description = description
	for _, opt := range injectOpts {
		opt.Apply(r)
	}
	return r
}

func (r *Rule) runUpdateCallbacks(pods []*statesinformer.PodMeta) {
	for _, callbackFn := range r.callbacks {
		if err := callbackFn(pods); err != nil {
			cbName := runtime.FuncForPC(reflect.ValueOf(callbackFn).Pointer()).Name()
			klog.Warningf("executing %s callback function %s failed, error %v", r.name, cbName)
		}
	}
}

func find(name string) (*Rule, bool) {
	globalRWMutex.Lock()
	defer globalRWMutex.Unlock()
	if r, exist := globalHookRules[name]; exist {
		return r, true
	}
	newRule := &Rule{name: name}
	globalHookRules[name] = newRule
	return newRule, false
}

func UpdateRules(s statesinformer.StatesInformer, nodeSLOIf interface{}) {
	nodeSLO, ok := nodeSLOIf.(*slov1alpha1.NodeSLO)
	if !ok {
		klog.Errorf("update rules with type %T is illegal", nodeSLOIf)
		return
	}
	klog.Infof("applying rules with new NodeSLO %v", nodeSLO)
	for _, r := range globalHookRules {
		if !r.systemSupported {
			klog.Infof("system unsupported for rule %s, do nothing during UpdateRules", r.name)
			return
		}
		updated, err := r.parseFunc(&nodeSLO.Spec)
		if err != nil {
			klog.Warningf("parse rule %s from nodeSLO failed, error: %v", r.name, err)
			continue
		}
		if updated {
			pods := s.GetAllPods()
			r.runUpdateCallbacks(pods)
		}
	}
}

func init() {
	globalHookRules = map[string]*Rule{}
}
