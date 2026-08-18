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
	"fmt"

	"github.com/spf13/pflag"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/koordinator-sh/koordinator/cmd/koord-scheduler/app"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
)

const (
	// Name is the name of the sandbox custom workflow.
	Name = "sandbox"

	defaultMaxConcurrentBindings = 1024
)

var _ app.CustomWorkflow = &SandboxCustomWorkflow{}

// SandboxCustomWorkflow schedules sandbox workloads through the equivalence-class path and
// delegates ordinary pods to the default scheduler decision path.
type SandboxCustomWorkflow struct {
	workflow              *Workflow
	scheduling            *equivalenceScheduling
	bindingSlots          chan struct{}
	maxConcurrentBindings int
}

type bindingSlotLease struct {
	workflow *SandboxCustomWorkflow
	held     bool
}

// New creates a sandbox custom workflow.
func New() *SandboxCustomWorkflow {
	return &SandboxCustomWorkflow{
		maxConcurrentBindings: defaultMaxConcurrentBindings,
	}
}

// AddFlags registers the sandbox custom workflow command-line flags.
func (w *SandboxCustomWorkflow) AddFlags(fs *pflag.FlagSet) {
	fs.IntVar(
		&w.maxConcurrentBindings,
		"sandbox-max-concurrent-bindings",
		w.maxConcurrentBindings,
		"Maximum number of concurrent post-Permit binding cycles for the sandbox custom workflow.",
	)
}

func (w *SandboxCustomWorkflow) Name() string {
	return Name
}

func (w *SandboxCustomWorkflow) IsEnabled() bool {
	return utilfeature.DefaultFeatureGate.Enabled(koordfeatures.SandboxCustomWorkflow)
}

func (w *SandboxCustomWorkflow) Setup(_ context.Context, opts *app.CustomWorkflowOptions) error {
	if !w.IsEnabled() {
		w.bindingSlots = nil
		return nil
	}
	if w.maxConcurrentBindings <= 0 {
		return fmt.Errorf("sandbox max concurrent bindings must be greater than 0")
	}
	w.workflow = NewWorkflow(opts.Sched, opts.KubeClient)
	w.scheduling = newEquivalenceScheduling(opts.Sched, opts.PercentageOfNodesToScore)
	if err := w.scheduling.registerNodeEventHandler(opts.SharedInformerFactory.Core().V1().Nodes().Informer()); err != nil {
		return err
	}
	w.bindingSlots = make(chan struct{}, w.maxConcurrentBindings)
	return nil
}

func (w *SandboxCustomWorkflow) acquireBindingSlot(ctx context.Context) error {
	if w.bindingSlots == nil {
		return nil
	}
	select {
	case w.bindingSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *SandboxCustomWorkflow) releaseBindingSlot() {
	if w.bindingSlots == nil {
		return
	}
	<-w.bindingSlots
}

func (s *bindingSlotLease) release() {
	if !s.held {
		return
	}
	s.workflow.releaseBindingSlot()
	s.held = false
}

func (s *bindingSlotLease) reacquire(ctx context.Context) error {
	if s.held || s.workflow == nil || s.workflow.bindingSlots == nil {
		return nil
	}
	if err := s.workflow.acquireBindingSlot(ctx); err != nil {
		return err
	}
	s.held = true
	return nil
}

// Run takes over the scheduler loop while the sandbox custom workflow is enabled.
//
// TODO: support running alongside the default sched.Run so that enabling the workflow does not
// require taking over the whole scheduler.
func (w *SandboxCustomWorkflow) Run(ctx context.Context) {
	w.workflow.Run(ctx, w.ScheduleOne)
}
