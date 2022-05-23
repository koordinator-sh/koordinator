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

package statesinformer

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func TestRegisterCallbacksAndRun(t *testing.T) {
	type args struct {
		objType     reflect.Type
		name        string
		description string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "register and run",
			args: args{
				objType:     reflect.TypeOf(&slov1alpha1.NodeSLO{}),
				name:        "set-bool-var",
				description: "set test bool var as true",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testVar := pointer.BoolPtr(false)
			callbackFn := func(si StatesInformer, statesObj interface{}) {
				*testVar = true
			}
			si := &statesInformer{
				stateUpdateCallbacks: map[reflect.Type][]updateCallback{
					reflect.TypeOf(&slov1alpha1.NodeSLO{}): {},
				},
			}
			si.RegisterCallbacks(tt.args.objType, tt.args.name, tt.args.description, callbackFn)
			si.runCallbacks(tt.args.objType, &slov1alpha1.NodeSLO{})
			assert.Equal(t, *testVar, true)
		})
	}
}
