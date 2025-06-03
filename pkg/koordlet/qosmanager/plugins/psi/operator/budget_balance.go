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

const AnnotationBudgetBalance = "koordinator.sh/budget-balance"

var (
	DefaultCpuBudgetBalance Operator = &BudgetBalance{
		ResourceGetter: podcgroup.Cpu,
		BasePrice:      0.5,
		LowerBound:     0.5,
	}
)

type BudgetBalance struct {
	ResourceGetter podcgroup.ResourceGetter `json:"resourceGetter,omitempty"`
	BasePrice      float64                  `json:"basePrice,omitempty"`
	LowerBound     float64                  `json:"lowerBound,omitempty"`

	budget map[types.UID]int64 `json:"-"`
}

func (bb *BudgetBalance) Init() error { return nil }

func (bb *BudgetBalance) Name() string {
	return bb.ResourceGetter.Name() + "BudgetBalance"
}

func (bb *BudgetBalance) Update(g Operator) error {
	if v, ok := g.(*BudgetBalance); ok {
		bb.ResourceGetter = v.ResourceGetter
		bb.BasePrice = v.BasePrice
		bb.LowerBound = v.LowerBound
		return nil
	}
	return fmt.Errorf("not a BudgetBalance")
}

func (bb *BudgetBalance) Exec(pods map[types.UID]*podcgroup.PodCgroup, node *v1.Node) error {
	var (
		price = bb.BasePrice
		bank  = make(zeroBank)
	)
	for _, pc := range pods {
		if pc.Pod.Annotations[AnnotationBudgetBalance] != "true" {
			continue
		}
		r := bb.ResourceGetter.Get(pc)
		if r.Request == 0 || r.Limit == 0 {
			continue
		}

		price += r.Pressure().Rate()
		acquire := int64(float64(r.Limit-r.Request) * r.Pressure().Rate())
		if acquire < 0 || bb.budget[pc.Pod.UID] <= 0 {
			acquire = 0
		}
		bank[r] = acquire
	}
	bank.balance(bb.LowerBound)
	var show []string
	for r, b := range bank {
		bb.budget[r.Pod.UID] = int64SafeAdd(bb.budget[r.Pod.UID], bb.getBudget(r, price))
		if r.Promise().Int64() != r.Request+b {
			r.SetPromise(r.Request + b)
		}
		show = append(show, fmt.Sprintf("%s: %+.2f%%", klog.KObj(r.Pod), float64(b)/float64(r.Request)*100))
	}
	if len(show) > 0 {
		sort.Strings(show)
		klog.InfoS(bb.Name(), "info", show)
	}
	return nil
}

func (bb *BudgetBalance) getBudget(r *podcgroup.PodResource, price float64) int64 {
	if r.Request == 0 || r.Limit == 0 {
		return 0
	}
	current := r.Current().Int64()
	promise := r.Promise().Int64()
	request := r.Request
	return int64(float64(request-current+request-promise) * price)
}

func (bb *BudgetBalance) AddPod(pc *podcgroup.PodCgroup) error {
	if pc.Pod.Annotations[AnnotationBudgetBalance] != "true" {
		return nil
	}
	if bb.budget == nil {
		bb.budget = make(map[types.UID]int64)
	}
	bb.budget[pc.Pod.UID] = 0
	return nil
}

func (bb *BudgetBalance) DeletePod(pc *podcgroup.PodCgroup) error {
	delete(bb.budget, pc.Pod.UID)
	return nil
}
