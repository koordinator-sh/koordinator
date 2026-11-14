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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// TODO: We should refactor these function types with scheduler.FailureHandlerFn

type PreErrorHandlerFilter func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) bool
type PostErrorHandlerFilter PreErrorHandlerFilter

type errorHandlerDispatcher struct {
	preHandlerFilters  []PreErrorHandlerFilter
	postHandlerFilters []PostErrorHandlerFilter
	defaultHandler     scheduler.FailureHandlerFn
	// schedulingQueue is used to release the pod's in-flight bookkeeping when a
	// pre-handler filter suppresses the default failure handler.
	schedulingQueue SchedulingQueue
}

func newErrorHandlerDispatcher() *errorHandlerDispatcher {
	return &errorHandlerDispatcher{}
}

func (d *errorHandlerDispatcher) setDefaultHandler(handler scheduler.FailureHandlerFn) {
	d.defaultHandler = handler
}

func (d *errorHandlerDispatcher) setSchedulingQueue(queue SchedulingQueue) {
	d.schedulingQueue = queue
}

func (d *errorHandlerDispatcher) RegisterErrorHandlerFilters(preFilter PreErrorHandlerFilter, postFilter PostErrorHandlerFilter) {
	if preFilter != nil {
		d.preHandlerFilters = append(d.preHandlerFilters, preFilter)
	}
	if postFilter != nil {
		d.postHandlerFilters = append(d.postHandlerFilters, postFilter)
	}
}

func (d *errorHandlerDispatcher) Error(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) {
	defer func() {
		for _, handlerFilter := range d.postHandlerFilters {
			if handlerFilter(ctx, fwk, podInfo, status, nominatingInfo, start) {
				return
			}
		}
	}()

	// FIXME here, we replace nominatingInfo to avoid modifying clearNominatedNode
	nominatingInfo = TakeoverNominatingInfo(ctx, fwk, podInfo, status, nominatingInfo, start)

	for _, handlerFilter := range d.preHandlerFilters {
		if handlerFilter(ctx, fwk, podInfo, status, nominatingInfo, start) {
			// The filter suppresses the default failure handler, which is the only
			// code path calling SchedulingQueue.Done() on the scheduling-cycle
			// failure path. Release the pod's in-flight bookkeeping explicitly,
			// otherwise the pod stays in inFlightPods forever, disables the
			// pruning of inFlightEvents, and all subsequent cluster events
			// accumulate unboundedly (memory leak). Done() is idempotent, so it
			// is also safe on the binding-cycle path where Done() has already
			// been called before Bind.
			if d.schedulingQueue != nil {
				d.schedulingQueue.Done(podInfo.Pod.UID)
			}
			return
		}
	}
	d.defaultHandler(ctx, fwk, podInfo, status, nominatingInfo, start)
	d.dropDeletedPodFromQueue(fwk, podInfo.Pod)
}

// dropDeletedPodFromQueue removes a pod that the default handler requeued after it was deleted.
// The default handler looks the pod up in the informer cache before AddUnschedulableIfNotPresent,
// so a Delete event handled in between leaves the deleted pod in unschedulablePods with nothing
// left to remove it. The informer store drops the pod before delete handlers run, so a pod that
// is missing from the lister here was either already removed from the queue or leaked by that race.
func (d *errorHandlerDispatcher) dropDeletedPodFromQueue(fwk framework.Framework, pod *corev1.Pod) {
	if d.schedulingQueue == nil || fwk == nil || pod == nil || reservationutil.IsReservePod(pod) {
		return
	}
	informerFactory := fwk.SharedInformerFactory()
	if informerFactory == nil {
		return
	}
	_, err := informerFactory.Core().V1().Pods().Lister().Pods(pod.Namespace).Get(pod.Name)
	if !apierrors.IsNotFound(err) {
		return
	}
	klog.V(4).InfoS("Pod was deleted while handling scheduling failure, removing it from the scheduling queue", "pod", klog.KObj(pod))
	d.schedulingQueue.Delete(pod)
}
