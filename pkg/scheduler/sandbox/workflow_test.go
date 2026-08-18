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
	"time"

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

func TestSandboxCustomWorkflowIsEnabled(t *testing.T) {
	w := New()
	assert.False(t, w.IsEnabled(), "workflow should be disabled when the feature gate is off")

	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.SandboxCustomWorkflow, true)()
	assert.True(t, w.IsEnabled(), "workflow should be enabled when the feature gate is on")
}

func TestSandboxCustomWorkflowAddFlags(t *testing.T) {
	w := New()
	assert.Equal(t, defaultMaxConcurrentBindings, w.maxConcurrentBindings)

	fs := pflag.NewFlagSet(Name, pflag.ContinueOnError)
	w.AddFlags(fs)
	require.NoError(t, fs.Set("sandbox-max-concurrent-bindings", "256"))

	assert.Equal(t, 256, w.maxConcurrentBindings)
}

func TestSandboxCustomWorkflowBindingConcurrency(t *testing.T) {
	w := &SandboxCustomWorkflow{bindingSlots: make(chan struct{}, 1)}
	require.NoError(t, w.acquireBindingSlot(context.Background()))

	acquired := make(chan error, 1)
	started := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		close(started)
		acquired <- w.acquireBindingSlot(ctx)
	}()
	<-started

	select {
	case err := <-acquired:
		t.Fatalf("second binding slot acquired before release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	w.releaseBindingSlot()
	require.NoError(t, <-acquired)
	w.releaseBindingSlot()
}

func TestSandboxCustomWorkflowBindingConcurrencyCancellation(t *testing.T) {
	w := &SandboxCustomWorkflow{bindingSlots: make(chan struct{}, 1)}
	require.NoError(t, w.acquireBindingSlot(context.Background()))
	defer w.releaseBindingSlot()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, w.acquireBindingSlot(ctx), context.Canceled)
}

func TestSandboxCustomWorkflowBindingSlotsDisabledWhenChannelIsNil(t *testing.T) {
	w := &SandboxCustomWorkflow{}
	require.NoError(t, w.acquireBindingSlot(context.Background()))
	w.releaseBindingSlot()

	lease := &bindingSlotLease{workflow: w}
	require.NoError(t, lease.reacquire(context.Background()))
	lease.release()
}

func TestSandboxCustomWorkflowSetupDoesNotInitializeBindingSlotsWhenDisabled(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.SandboxCustomWorkflow, false)()

	w := New()
	w.maxConcurrentBindings = 0
	require.NoError(t, w.Setup(context.Background(), &app.CustomWorkflowOptions{}))
	assert.Nil(t, w.bindingSlots)
}

func TestSandboxCustomWorkflowSetupRejectsInvalidBindingConcurrency(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, koordfeatures.SandboxCustomWorkflow, true)()

	w := New()
	w.maxConcurrentBindings = 0
	err := w.Setup(context.Background(), &app.CustomWorkflowOptions{})
	assert.EqualError(t, err, "sandbox max concurrent bindings must be greater than 0")
}
