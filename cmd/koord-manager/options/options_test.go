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
	"net/http"
	"sort"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type fakeManager struct {
	manager.Manager
	ControllerAdded     []string
	MetricsExtraHandler map[string]http.Handler
}

func newFakeManager() *fakeManager {
	return &fakeManager{}
}

func (c *fakeManager) AddMetricsExtraHandler(path string, handler http.Handler) error {
	if c.MetricsExtraHandler == nil {
		c.MetricsExtraHandler = map[string]http.Handler{}
	}
	c.MetricsExtraHandler[path] = handler
	return nil
}

func (c *fakeManager) EqualMetricsExtraHandler(extraHandler map[string]http.Handler) bool {
	if extraHandler == nil && c.MetricsExtraHandler == nil {
		return true
	}
	if extraHandler == nil || c.MetricsExtraHandler == nil {
		return false
	}
	for p := range extraHandler {
		_, ok := c.MetricsExtraHandler[p]
		if !ok {
			return false
		}
	}
	for p := range c.MetricsExtraHandler {
		_, ok := extraHandler[p]
		if !ok {
			return false
		}
	}
	return true
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
			"--enable-prom-metrics=true",
			"--metrics-handler-path=/test-metrics",
		}
		opt.InitFlags(nil)
		pflag.NewFlagSet(args[0], pflag.ExitOnError)
		err := pflag.CommandLine.Parse(args[1:])
		assert.NoError(t, err)
		assert.Equal(t, []string{"noderesource", "nodemetric"}, opt.Controllers)
		assert.Equal(t, true, opt.EnablePromMetrics)
		assert.Equal(t, "/test-metrics", opt.MetricsHandlerPath)
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
		controllersToAdd   []string
		controllers        map[string]func(manager.Manager) error
		enablePromMetrics  bool
		metricsHandlerPath string
	}
	type wants struct {
		ControllerAdded     []string
		MetricsExtraHandler map[string]http.Handler
	}
	tests := []struct {
		name    string
		args    args
		want    wants
		wantErr bool
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
			want: wants{
				ControllerAdded: []string{"a", "b"},
			},
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
			want: wants{
				ControllerAdded: []string{"a", "b"},
			},
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
			want: wants{
				ControllerAdded: []string{"b"},
			},
		},
		{
			name: "skip register metrics handler",
			args: args{
				enablePromMetrics:  false,
				metricsHandlerPath: "/test-path",
			},
			want: wants{},
		},
		{
			name: "register a prom metrics handler",
			args: args{
				enablePromMetrics:  true,
				metricsHandlerPath: "/test-path",
			},
			want: wants{
				MetricsExtraHandler: map[string]http.Handler{
					"/test-path": promhttp.Handler(),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := &Options{
				Controllers:        tt.args.controllersToAdd,
				ControllerAddFuncs: tt.args.controllers,
				EnablePromMetrics:  tt.args.enablePromMetrics,
				MetricsHandlerPath: tt.args.metricsHandlerPath,
			}

			mgr := newFakeManager()
			err := opt.ApplyTo(mgr)
			assert.NoError(t, err)
			sort.Strings(mgr.ControllerAdded)
			assert.Equal(t, tt.want.ControllerAdded, mgr.ControllerAdded)
			assert.True(t, mgr.EqualMetricsExtraHandler(tt.want.MetricsExtraHandler))
		})
	}
}
