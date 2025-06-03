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

package operator

import (
	"fmt"
	"sort"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/podcgroup"
)

const AnnotationGroupHash = "koordinator.sh/group-hash"

var DefaultCpuGroupShare Operator = &GroupShare{
	GroupingAnnotationKey: AnnotationGroupHash,
	ResourceGetter:        podcgroup.Cpu,
	LowerBound:            0.5,
}

type GroupShare struct {
	GroupingAnnotationKey string                   `json:"groupingAnnotationKey,omitempty"`
	ResourceGetter        podcgroup.ResourceGetter `json:"resourceGetter,omitempty"`
	LowerBound            float64                  `json:"lowerBound,omitempty"`
}

func (gs *GroupShare) Init() error { return nil }

func (gs *GroupShare) Name() string {
	return gs.ResourceGetter.Name() + "GroupShare"
}

func (gs *GroupShare) Update(g Operator) error {
	if v, ok := g.(*GroupShare); ok {
		*gs = *v
		return nil
	}
	return fmt.Errorf("not a GroupShare")
}

func (gs *GroupShare) Exec(pods map[types.UID]*podcgroup.PodCgroup, node *v1.Node) error {
	groups := make(map[string][]*podcgroup.PodResource)
	for _, pc := range pods {
		group := pc.Pod.Annotations[gs.GroupingAnnotationKey]
		if group == "" {
			continue
		}
		groups[group] = append(groups[group], gs.ResourceGetter.Get(pc))
	}
	for name, group := range groups {
		bank := make(zeroBank, len(group))
		for _, r := range group {
			if r.Request == 0 || r.Limit == 0 {
				continue
			}
			ideal := r.Current().Int64() + int64(float64(r.Limit-r.Current().Int64())*r.Pressure().Rate())
			bank[r] = ideal - r.Request
		}
		bank.balance(gs.LowerBound)
		var show []string
		for r, b := range bank {
			if r.Promise().Int64() != r.Request+b {
				r.SetPromise(r.Request + b)
			}
			show = append(show, fmt.Sprintf("%s: %+.2f%%", klog.KObj(r.Pod), float64(b)/float64(r.Request)*100))
		}
		sort.Strings(show)
		klog.InfoS(gs.Name(), "group", name, "info", show)
	}
	return nil
}

func (gs *GroupShare) AddPod(pc *podcgroup.PodCgroup) error { return nil }

func (gs *GroupShare) DeletePod(pc *podcgroup.PodCgroup) error { return nil }
