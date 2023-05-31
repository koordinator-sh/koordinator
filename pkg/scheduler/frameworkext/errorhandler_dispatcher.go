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

import "k8s.io/kubernetes/pkg/scheduler/framework"

type ErrorHandler func(*framework.QueuedPodInfo, error) bool

type errorHandlerDispatcher struct {
	handlers       []ErrorHandler
	defaultHandler func(*framework.QueuedPodInfo, error)
}

func newErrorHandlerDispatcher() *errorHandlerDispatcher {
	return &errorHandlerDispatcher{}
}

func (d *errorHandlerDispatcher) setDefaultHandler(handler func(*framework.QueuedPodInfo, error)) {
	d.defaultHandler = handler
}

func (d *errorHandlerDispatcher) RegisterErrorHandler(handler ErrorHandler) {
	d.handlers = append(d.handlers, handler)
}

func (d *errorHandlerDispatcher) Error(podInfo *framework.QueuedPodInfo, err error) {
	for _, handler := range d.handlers {
		if handler(podInfo, err) {
			return
		}
	}
	d.defaultHandler(podInfo, err)
}
