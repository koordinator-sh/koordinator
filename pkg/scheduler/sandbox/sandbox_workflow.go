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

	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/koordinator-sh/koordinator/cmd/koord-scheduler/app"
	koordfeatures "github.com/koordinator-sh/koordinator/pkg/features"
)

// Name is the name of the sandbox custom workflow.
const Name = "sandbox"

var _ app.CustomWorkflow = &SandboxCustomWorkflow{}

// SandboxCustomWorkflow schedules sandbox workloads through the equivalence-class path and
// delegates ordinary pods to the default scheduler decision path.
type SandboxCustomWorkflow struct {
	workflow   *Workflow
	scheduling *equivalenceScheduling
}

// New creates a sandbox custom workflow.
func New() *SandboxCustomWorkflow {
	return &SandboxCustomWorkflow{}
}

func (w *SandboxCustomWorkflow) Name() string {
	return Name
}

func (w *SandboxCustomWorkflow) IsEnabled() bool {
	return utilfeature.DefaultFeatureGate.Enabled(koordfeatures.SandboxCustomWorkflow)
}

func (w *SandboxCustomWorkflow) Setup(_ context.Context, opts *app.CustomWorkflowOptions) error {
	w.workflow = NewWorkflow(opts.Sched, opts.KubeClient)
	w.scheduling = newEquivalenceScheduling(opts.Sched, opts.PercentageOfNodesToScore)
	return w.scheduling.registerNodeEventHandler(opts.SharedInformerFactory.Core().V1().Nodes().Informer())
}

// Run takes over the scheduler loop while the sandbox custom workflow is enabled.
//
// TODO: support running alongside the default sched.Run so that enabling the workflow does not
// require taking over the whole scheduler.
func (w *SandboxCustomWorkflow) Run(ctx context.Context) {
	w.workflow.Run(ctx, w.ScheduleOne)
}
