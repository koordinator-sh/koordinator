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

package frameworkext

import (
	"context"
	"time"

	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// TODO: We should refactor these function types with scheduler.FailureHandlerFn

type PreErrorHandlerFilter func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *framework.Status, nominatingInfo *framework.NominatingInfo, start time.Time) bool
type PostErrorHandlerFilter PreErrorHandlerFilter

type errorHandlerDispatcher struct {
	preHandlerFilters  []PreErrorHandlerFilter
	postHandlerFilters []PostErrorHandlerFilter
	defaultHandler     scheduler.FailureHandlerFn
}

func newErrorHandlerDispatcher() *errorHandlerDispatcher {
	return &errorHandlerDispatcher{}
}

func (d *errorHandlerDispatcher) setDefaultHandler(handler scheduler.FailureHandlerFn) {
	d.defaultHandler = handler
}

func (d *errorHandlerDispatcher) RegisterErrorHandlerFilters(preFilter PreErrorHandlerFilter, postFilter PostErrorHandlerFilter) {
	if preFilter != nil {
		d.preHandlerFilters = append(d.preHandlerFilters, preFilter)
	}
	if postFilter != nil {
		d.postHandlerFilters = append(d.postHandlerFilters, postFilter)
	}
}

func (d *errorHandlerDispatcher) Error(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *framework.Status, nominatingInfo *framework.NominatingInfo, start time.Time) {
	defer func() {
		for _, handlerFilter := range d.postHandlerFilters {
			if handlerFilter(ctx, fwk, podInfo, status, nominatingInfo, start) {
				return
			}
		}
	}()

	for _, handlerFilter := range d.preHandlerFilters {
		if handlerFilter(ctx, fwk, podInfo, status, nominatingInfo, start) {
			return
		}
	}
	d.defaultHandler(ctx, fwk, podInfo, status, nominatingInfo, start)
}
