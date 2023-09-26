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

package arbitrator

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

// arbitrationHandler implement handler.EventHandler
type arbitrationHandler struct {
	handler.EnqueueRequestForObject
	client     client.Client
	arbitrator Arbitrator
}

func NewHandler(arbitrator Arbitrator, client client.Client) handler.EventHandler {
	return &arbitrationHandler{
		EnqueueRequestForObject: handler.EnqueueRequestForObject{},
		arbitrator:              arbitrator,
		client:                  client,
	}
}

// Create call Arbitrator.Create
func (h *arbitrationHandler) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	if evt.Object == nil {
		enqueueLog.Error(nil, "CreateEvent received with no metadata", "event", evt)
		return
	}
	job := evt.Object.(*v1alpha1.PodMigrationJob)
	h.arbitrator.AddPodMigrationJob(job)
}

// Update implements EventHandler.
func (h *arbitrationHandler) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	switch {
	case evt.ObjectNew != nil:
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      evt.ObjectNew.GetName(),
			Namespace: evt.ObjectNew.GetNamespace(),
		}})
		job := evt.ObjectNew.(*v1alpha1.PodMigrationJob)
		if job.Status.Phase == v1alpha1.PodMigrationJobFailed ||
			job.Status.Phase == v1alpha1.PodMigrationJobSucceeded ||
			job.Status.Phase == v1alpha1.PodMigrationJobAborted {
			h.arbitrator.DeletePodMigrationJob(job)
		}
	case evt.ObjectOld != nil:
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      evt.ObjectOld.GetName(),
			Namespace: evt.ObjectOld.GetNamespace(),
		}})
	default:
		enqueueLog.Error(nil, "UpdateEvent received with no metadata", "event", evt)
	}
}

// Delete implements EventHandler.
func (h *arbitrationHandler) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	if evt.Object == nil {
		enqueueLog.Error(nil, "DeleteEvent received with no metadata", "event", evt)
		return
	}
	q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      evt.Object.GetName(),
		Namespace: evt.Object.GetNamespace(),
	}})
	h.arbitrator.DeletePodMigrationJob(evt.Object.(*v1alpha1.PodMigrationJob))
}
