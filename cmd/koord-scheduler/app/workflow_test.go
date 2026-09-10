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

package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler"
	internalqueue "k8s.io/kubernetes/pkg/scheduler/backend/queue"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
)

type testWorkflowInitializer struct {
	name    string
	enabled bool
	setup   func(context.Context, *CustomWorkflowOptions) error
}

func (w *testWorkflowInitializer) Name() string    { return w.name }
func (w *testWorkflowInitializer) IsEnabled() bool { return w.enabled }
func (w *testWorkflowInitializer) Setup(ctx context.Context, opts *CustomWorkflowOptions) error {
	return w.setup(ctx, opts)
}

type testRunningWorkflow struct {
	*testWorkflowInitializer
	runCalls int
}

func (w *testRunningWorkflow) Run(context.Context) { w.runCalls++ }

func TestSetupWorkflows(t *testing.T) {
	setupError := errors.New("setup failed")
	type workflowSpec struct {
		name     string
		disabled bool
		runner   bool
		setupErr error
	}
	for _, tt := range []struct {
		name       string
		workflows  []workflowSpec
		wantSetup  []string
		wantRunner string
		wantError  string
	}{
		{name: "none enabled"},
		{
			name:      "all enabled initializers run in registration order",
			workflows: []workflowSpec{{name: "first"}, {name: "disabled", disabled: true}, {name: "second"}},
			wantSetup: []string{"first", "second"},
		},
		{
			name:       "initializers on both sides of the runner",
			workflows:  []workflowSpec{{name: "first"}, {name: "runner", runner: true}, {name: "last"}},
			wantSetup:  []string{"first", "runner", "last"},
			wantRunner: "runner",
		},
		{
			name:       "disabled runner is ignored",
			workflows:  []workflowSpec{{name: "disabled", runner: true, disabled: true}, {name: "runner", runner: true}},
			wantSetup:  []string{"runner"},
			wantRunner: "runner",
		},
		{
			name:      "multiple runners fail before setup",
			workflows: []workflowSpec{{name: "initializer"}, {name: "first", runner: true}, {name: "second", runner: true}},
			wantError: `multiple custom workflow runners enabled: "first" and "second"`,
		},
		{
			name:      "setup failure stops initialization and discards runner",
			workflows: []workflowSpec{{name: "runner", runner: true}, {name: "failed", setupErr: setupError}, {name: "last"}},
			wantSetup: []string{"runner", "failed"},
			wantError: `setup workflow "failed": setup failed`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := KnownWorkflowList
			t.Cleanup(func() { KnownWorkflowList = original })
			KnownWorkflowList = nil
			opts := &CustomWorkflowOptions{}
			var setupOrder []string
			for _, spec := range tt.workflows {
				initializer := &testWorkflowInitializer{
					name: spec.name, enabled: !spec.disabled,
					setup: func(ctx context.Context, got *CustomWorkflowOptions) error {
						assert.Same(t, opts, got)
						assert.Equal(t, context.Background(), ctx)
						setupOrder = append(setupOrder, spec.name)
						return spec.setupErr
					},
				}
				if spec.runner {
					KnownWorkflowList = append(KnownWorkflowList, &testRunningWorkflow{testWorkflowInitializer: initializer})
				} else {
					KnownWorkflowList = append(KnownWorkflowList, initializer)
				}
			}

			runner, err := setupWorkflows(context.Background(), opts)

			assert.Equal(t, tt.wantSetup, setupOrder)
			if tt.wantError != "" {
				require.EqualError(t, err, tt.wantError)
				assert.Nil(t, runner)
				if len(tt.wantSetup) > 0 {
					assert.ErrorIs(t, err, setupError)
				}
			} else {
				require.NoError(t, err)
				if tt.wantRunner == "" {
					assert.Nil(t, runner)
				} else {
					require.NotNil(t, runner)
					assert.Equal(t, tt.wantRunner, runner.Name())
				}
			}
			for _, wf := range KnownWorkflowList {
				if runner, ok := wf.(*testRunningWorkflow); ok {
					assert.Zero(t, runner.runCalls, "setup must not start a scheduling loop")
				}
			}
		})
	}
}

func TestRunWorkflow(t *testing.T) {
	t.Run("custom runner is called once", func(t *testing.T) {
		runner := &testRunningWorkflow{testWorkflowInitializer: &testWorkflowInitializer{name: "runner"}}
		RunWorkflow(context.Background(), nil, runner)
		assert.Equal(t, 1, runner.runCalls)
	})

	t.Run("multiple initializers share the upstream loop", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var nextCalls atomic.Int32
		sched := &scheduler.Scheduler{
			NextPod: func(klog.Logger) (*framework.QueuedPodInfo, error) {
				nextCalls.Add(1)
				cancel()
				return nil, nil
			},
		}
		original := KnownWorkflowList
		t.Cleanup(func() { KnownWorkflowList = original })
		setups := 0
		KnownWorkflowList = nil
		for _, name := range []string{"first", "second"} {
			KnownWorkflowList = append(KnownWorkflowList, &testWorkflowInitializer{
				name: name, enabled: true,
				setup: func(context.Context, *CustomWorkflowOptions) error {
					setups++
					return nil
				},
			})
		}
		runner, err := setupWorkflows(ctx, &CustomWorkflowOptions{Sched: sched})
		require.NoError(t, err)
		require.Nil(t, runner)

		metrics.Register()
		sched.SchedulingQueue = internalqueue.NewTestQueue(ctx, nil)
		RunWorkflow(ctx, sched, runner)

		assert.Equal(t, 2, setups)
		assert.Equal(t, int32(1), nextCalls.Load())
	})
}

func TestCustomWorkflowOptionsDoesNotExposeSandboxConfiguration(t *testing.T) {
	optionsType := reflect.TypeOf(CustomWorkflowOptions{})
	for i := 0; i < optionsType.NumField(); i++ {
		field := optionsType.Field(i)
		assert.Falsef(t, strings.HasPrefix(field.Name, "Sandbox"),
			"CustomWorkflowOptions must not expose sandbox-specific field %q", field.Name)
	}
}
