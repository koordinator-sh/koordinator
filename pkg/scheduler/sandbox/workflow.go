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

	"k8s.io/apimachinery/pkg/util/wait"
	kubeclientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler"
)

// Workflow contains the scheduler state used by the copied upstream scheduling loop.
type Workflow struct {
	sched      *scheduler.Scheduler
	kubeClient kubeclientset.Interface

	nominatedNodeNameForExpectationEnabled bool
}

// Run mirrors scheduler.Run (k8s.io/kubernetes/pkg/scheduler/scheduler.go:538): it starts the
// scheduling queue and the per-pod scheduling loop in a dedicated goroutine (the loop hangs on
// Pop when no pods are pending, which would otherwise deadlock the shutdown), then blocks until
// the context is done and closes everything down.
func (w *Workflow) Run(ctx context.Context) {
	logger := klog.FromContext(ctx)
	w.sched.SchedulingQueue.Run(logger)

	if w.sched.APIDispatcher != nil {
		w.sched.APIDispatcher.Run(logger)
	}

	go wait.UntilWithContext(ctx, w.ScheduleOne, 0)

	<-ctx.Done()
	if w.sched.APIDispatcher != nil {
		w.sched.APIDispatcher.Close()
	}
	w.sched.SchedulingQueue.Close()

	// If the plugins satisfy the io.Closer interface, they are closed.
	if err := w.sched.Profiles.Close(); err != nil {
		logger.Error(err, "Failed to close plugins")
	}
}
