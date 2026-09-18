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
	"fmt"

	"github.com/spf13/pflag"
	"k8s.io/client-go/informers"
	kubeclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler"

	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

var KnownWorkflowList []WorkflowInitializer

// WorkflowInitializer installs scheduler extensions before the scheduling loop starts.
type WorkflowInitializer interface {
	Name() string
	IsEnabled() bool
	Setup(ctx context.Context, opts *CustomWorkflowOptions) error
}

// CustomWorkflow additionally replaces the scheduler loop. At most one may be enabled.
type CustomWorkflow interface {
	WorkflowInitializer
	Run(ctx context.Context)
}

type customWorkflowFlagProvider interface {
	AddFlags(fs *pflag.FlagSet)
}

type CustomWorkflowOptions struct {
	Sched                      *scheduler.Scheduler
	SharedInformerFactory      informers.SharedInformerFactory
	KubeClient                 kubeclientset.Interface
	KoordSharedInformerFactory koordinatorinformers.SharedInformerFactory
	KoordClient                koordclientset.Interface
	RecorderFactory            func(name string) events.EventRecorder
	KubeConfig                 *rest.Config
	PercentageOfNodesToScore   *int32
}

func setupWorkflows(ctx context.Context, opts *CustomWorkflowOptions) (CustomWorkflow, error) {
	var enabled []WorkflowInitializer
	var runner CustomWorkflow
	for _, wf := range KnownWorkflowList {
		if !wf.IsEnabled() {
			continue
		}
		if candidate, ok := wf.(CustomWorkflow); ok {
			if runner != nil {
				return nil, fmt.Errorf("multiple custom workflow runners enabled: %q and %q", runner.Name(), candidate.Name())
			}
			runner = candidate
		}
		enabled = append(enabled, wf)
	}

	// Validate runner exclusivity before any initializer mutates the scheduler.
	for _, wf := range enabled {
		if err := wf.Setup(ctx, opts); err != nil {
			return nil, fmt.Errorf("setup workflow %q: %w", wf.Name(), err)
		}
	}
	return runner, nil
}

func RunWorkflow(
	ctx context.Context,
	sched *scheduler.Scheduler,
	wf CustomWorkflow,
) {
	if wf != nil {
		klog.InfoS("Run the custom workflow, the default scheduler workflow is disabled", "name", wf.Name())
		wf.Run(ctx)
		return
	}
	sched.Run(ctx)
}
