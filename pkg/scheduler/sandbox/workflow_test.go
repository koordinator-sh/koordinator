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

package sandbox

import (
	"context"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/koordinator-sh/koordinator/cmd/koord-scheduler/app"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func TestSandboxCustomWorkflowName(t *testing.T) {
	assert.Equal(t, Name, New().Name())
}

func TestSandboxCustomWorkflowDoesNotReplaceSchedulerLoop(t *testing.T) {
	_, ownsLoop := interface{}(New()).(app.CustomWorkflow)
	assert.False(t, ownsLoop)
}

func TestSandboxCustomWorkflowIsEnabled(t *testing.T) {
	w := New()
	assert.False(t, w.IsEnabled(), "workflow should be disabled when the feature gate is off")

	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.SandboxCustomWorkflow, true)()
	assert.True(t, w.IsEnabled(), "workflow should be enabled when the feature gate is on")
}

func TestSandboxCustomWorkflowAddFlags(t *testing.T) {
	w := New()
	assert.Equal(t, defaultMaxConcurrentBindings, w.maxConcurrentBindings)
	assert.Equal(t, defaultEquivalenceClassCacheSize, w.equivalenceCacheSize)

	fs := pflag.NewFlagSet(Name, pflag.ContinueOnError)
	w.AddFlags(fs)
	require.NoError(t, fs.Set("sandbox-max-concurrent-bindings", "256"))
	require.NoError(t, fs.Set("sandbox-equivalence-cache-size", "32"))

	assert.Equal(t, 256, w.maxConcurrentBindings)
	assert.Equal(t, 32, w.equivalenceCacheSize)
}

func TestSandboxCustomWorkflowSetupDoesNotInitializeLimiterWhenDisabled(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.SandboxCustomWorkflow, false)()

	w := New()
	w.maxConcurrentBindings = 0
	w.equivalenceCacheSize = 0
	require.NoError(t, w.Setup(context.Background(), &app.CustomWorkflowOptions{}))
	assert.Nil(t, w.limiter)
}

func TestSandboxCustomWorkflowSetupRejectsInvalidBindingConcurrency(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.SandboxCustomWorkflow, true)()

	w := New()
	w.maxConcurrentBindings = 0
	err := w.Setup(context.Background(), &app.CustomWorkflowOptions{})
	assert.EqualError(t, err, "sandbox max concurrent bindings must be greater than 0")
}

func TestSandboxCustomWorkflowSetupRejectsInvalidEquivalenceCacheSize(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.SandboxCustomWorkflow, true)()

	w := New()
	w.equivalenceCacheSize = 0
	err := w.Setup(context.Background(), &app.CustomWorkflowOptions{})
	assert.EqualError(t, err, "sandbox equivalence cache size must be greater than 0")
}

func TestSandboxCustomWorkflowSetupRejectsInlineBatchSchedule(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.SandboxCustomWorkflow, true)()
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.EnableInlineBatchSchedule, true)()

	w := New()
	err := w.Setup(context.Background(), &app.CustomWorkflowOptions{})
	require.Error(t, err, "sandbox workflow and inline batch schedule must be mutually exclusive")
	assert.Contains(t, err.Error(), string(koordfeatures.SandboxCustomWorkflow))
	assert.Contains(t, err.Error(), string(koordfeatures.EnableInlineBatchSchedule))
	assert.Nil(t, w.limiter, "binding limiter must not be initialized when setup fails")
}
