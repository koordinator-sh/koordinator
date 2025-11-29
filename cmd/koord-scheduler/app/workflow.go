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

	"k8s.io/client-go/informers"
	kubeclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler"

	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

var KnownWorkflowList []CustomWorkflow

// CustomWorkflow defines a custom workflow for the scheduler.
// If the workflow is enabled, the default workflow of the scheduler will not run.
type CustomWorkflow interface {
	Name() string
	IsEnabled() bool
	Setup(ctx context.Context, opts *CustomWorkflowOptions) error
	Run(ctx context.Context)
}

type CustomWorkflowOptions struct {
	Sched                      *scheduler.Scheduler
	SharedInformerFactory      informers.SharedInformerFactory
	KubeClient                 kubeclientset.Interface
	KoordSharedInformerFactory koordinatorinformers.SharedInformerFactory
	KoordClient                koordclientset.Interface
	RecorderFactory            func(name string) events.EventRecorder
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
