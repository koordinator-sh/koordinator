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
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type customWorkflowWithFlags struct {
	value int
}

func (w *customWorkflowWithFlags) Name() string {
	return "test"
}

func (w *customWorkflowWithFlags) IsEnabled() bool {
	return false
}

func (w *customWorkflowWithFlags) Setup(context.Context, *CustomWorkflowOptions) error {
	return nil
}

func (w *customWorkflowWithFlags) Run(context.Context) {}

func (w *customWorkflowWithFlags) AddFlags(fs *pflag.FlagSet) {
	fs.IntVar(&w.value, "test-custom-workflow-value", 7, "test custom workflow value")
}

func TestNewSchedulerCommandRegistersCustomWorkflowFlags(t *testing.T) {
	originalWorkflows := KnownWorkflowList
	t.Cleanup(func() {
		KnownWorkflowList = originalWorkflows
	})

	workflow := &customWorkflowWithFlags{}
	KnownWorkflowList = []CustomWorkflow{workflow}

	command := NewSchedulerCommand()
	require.NotNil(t, command.Flags().Lookup("test-custom-workflow-value"))
	require.NoError(t, command.Flags().Set("test-custom-workflow-value", "11"))
	assert.Equal(t, 11, workflow.value)
}
