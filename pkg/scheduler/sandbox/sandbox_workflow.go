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
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	// Name is the name of the sandbox custom workflow.
	Name = "sandbox"

	defaultMaxConcurrentBindings = 1024
)

var _ app.WorkflowInitializer = &SandboxCustomWorkflow{}

// SandboxCustomWorkflow installs equivalence-class node selection and shared binding admission
// on each profile's FrameworkExtender without replacing the scheduler loop.
type SandboxCustomWorkflow struct {
	scheduling            *equivalenceScheduling
	limiter               *bindingLimiter
	maxConcurrentBindings int
	equivalenceCacheSize  int
}

// New creates a sandbox custom workflow.
func New() *SandboxCustomWorkflow {
	return &SandboxCustomWorkflow{
		maxConcurrentBindings: defaultMaxConcurrentBindings,
		equivalenceCacheSize:  defaultEquivalenceClassCacheSize,
	}
}

// AddFlags registers the sandbox custom workflow command-line flags.
func (w *SandboxCustomWorkflow) AddFlags(fs *pflag.FlagSet) {
	fs.IntVar(
		&w.maxConcurrentBindings,
		"sandbox-max-concurrent-bindings",
		w.maxConcurrentBindings,
		"Maximum number of concurrent sandbox PreBind/Bind executions; waiting pods are not bounded.",
	)
	fs.IntVar(
		&w.equivalenceCacheSize,
		"sandbox-equivalence-cache-size",
		w.equivalenceCacheSize,
		"Maximum number of sandbox template equivalence-class entries retained by the custom workflow.",
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
		w.limiter = nil
		return nil
	}
	// The equivalence-class path and the inline batch scheduler both take over pods that would
	// otherwise flow through FrameworkExtenderFactory.scheduleOne. Keep them mutually exclusive so a
	// single pod is never claimed by both takeovers.
	if utilfeature.DefaultFeatureGate.Enabled(koordfeatures.EnableInlineBatchSchedule) {
		return fmt.Errorf("feature gates %s and %s are mutually exclusive", koordfeatures.SandboxCustomWorkflow, koordfeatures.EnableInlineBatchSchedule)
	}
	if w.maxConcurrentBindings <= 0 {
		return fmt.Errorf("sandbox max concurrent bindings must be greater than 0")
	}
	if w.equivalenceCacheSize <= 0 {
		return fmt.Errorf("sandbox equivalence cache size must be greater than 0")
	}
	w.scheduling = newEquivalenceScheduling(opts.Sched, opts.PercentageOfNodesToScore, w.equivalenceCacheSize)
	if err := w.scheduling.registerNodeEventHandler(opts.SharedInformerFactory.Core().V1().Nodes().Informer()); err != nil {
		return err
	}
	// One limiter instance is shared across all profiles so its semaphore bounds the total number
	// of concurrent sandbox binding cycles, not the number per profile.
	w.limiter = newBindingLimiter(w.maxConcurrentBindings)
	// Register the equivalence-class path as the scheduling decision provider and the shared binding
	// limiter on every profile's FrameworkExtender, so FrameworkExtenderFactory.scheduleOne routes
	// sandbox pods through the equivalence path and the upstream binding cycle bounds their
	// concurrency through the limiter.
	for _, fwk := range opts.Sched.Profiles {
		if extender, ok := fwk.(frameworkext.FrameworkExtender); ok {
			extender.RegisterSchedulingDecisionProvider(w.scheduling)
			extender.SetBindingLimiter(w.limiter)
		}
	}
	return nil
}
