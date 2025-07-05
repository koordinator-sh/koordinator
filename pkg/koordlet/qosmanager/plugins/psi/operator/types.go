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

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/podcgroup"
)

type Operator interface {
	Init() error
	Name() string
	Update(Operator) error
	Exec(map[types.UID]*podcgroup.PodCgroup, *v1.Node) error
	AddPod(*podcgroup.PodCgroup) error
	DeletePod(*podcgroup.PodCgroup) error
}

var _ Operator = &FuncOperator{}

type FuncOperator struct {
	fn func(map[types.UID]*podcgroup.PodCgroup, *v1.Node) error
}

func (f *FuncOperator) Init() error { return nil }
func (f *FuncOperator) Name() string {
	return "FuncOperator"
}
func (f *FuncOperator) Update(g Operator) error {
	if v, ok := g.(*FuncOperator); ok {
		f.fn = v.fn
		return nil
	}
	return fmt.Errorf("not a FuncOperator")
}
func (f *FuncOperator) Exec(pods map[types.UID]*podcgroup.PodCgroup, node *v1.Node) error {
	return f.fn(pods, node)
}
func (f *FuncOperator) AddPod(*podcgroup.PodCgroup) error    { return nil }
func (f *FuncOperator) DeletePod(*podcgroup.PodCgroup) error { return nil }
