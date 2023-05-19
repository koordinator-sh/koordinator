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

package options

import (
	"fmt"
	"sort"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type fakeManager struct {
	manager.Manager
	ControllerAdded []string
}

type fakeController struct {
	manager.Runnable
	ControllerName string
}

func (c *fakeController) Add(mgr ctrl.Manager) error {
	m, ok := mgr.(*fakeManager)
	if !ok {
		return fmt.Errorf("fakeController only supports fakeManager")
	}
	m.ControllerAdded = append(m.ControllerAdded, c.ControllerName)
	return nil
}

func TestOptions(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		opt := NewOptions()
		assert.NotNil(t, opt)

		args := []string{
			"",
			"--controllers=noderesource,nodemetric",
		}
		opt.InitFlags(nil)
		pflag.NewFlagSet(args[0], pflag.ExitOnError)
		err := pflag.CommandLine.Parse(args[1:])
		assert.NoError(t, err)
		assert.Equal(t, []string{"noderesource", "nodemetric"}, opt.Controllers)
	})
}

func TestOptionsApplyTo(t *testing.T) {
	controllerA := &fakeController{
		ControllerName: "a",
	}
	controllerB := &fakeController{
		ControllerName: "b",
	}
	type args struct {
		controllersToAdd []string
		controllers      map[string]func(manager.Manager) error
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "add each controllers",
			args: args{
				controllersToAdd: []string{"a", "b"},
				controllers: map[string]func(manager.Manager) error{
					"a": controllerA.Add,
					"b": controllerB.Add,
				},
			},
			want: []string{"a", "b"},
		},
		{
			name: "add all controllers",
			args: args{
				controllersToAdd: []string{"*"},
				controllers: map[string]func(manager.Manager) error{
					"a": controllerA.Add,
					"b": controllerB.Add,
				},
			},
			want: []string{"a", "b"},
		},
		{
			name: "add some controllers",
			args: args{
				controllersToAdd: []string{"b"},
				controllers: map[string]func(manager.Manager) error{
					"a": controllerA.Add,
					"b": controllerB.Add,
				},
			},
			want: []string{"b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := &Options{
				Controllers:        tt.args.controllersToAdd,
				ControllerAddFuncs: tt.args.controllers,
			}

			mgr := &fakeManager{}
			err := opt.ApplyTo(mgr)
			assert.NoError(t, err)
			sort.Strings(mgr.ControllerAdded)
			assert.Equal(t, tt.want, mgr.ControllerAdded)
		})
	}
}
