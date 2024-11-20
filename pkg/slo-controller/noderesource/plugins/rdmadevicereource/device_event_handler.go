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

package rdmadeviceresource

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

var _ handler.EventHandler = &DeviceHandler{}

type DeviceHandler struct{}

func (d *DeviceHandler) Create(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
	device := e.Object.(*schedulingv1alpha1.Device)
	q.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: device.Name,
		},
	})
}

func (d *DeviceHandler) Update(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	newDevice := e.ObjectNew.(*schedulingv1alpha1.Device)
	oldDevice := e.ObjectOld.(*schedulingv1alpha1.Device)
	if reflect.DeepEqual(newDevice.Spec, oldDevice.Spec) {
		return
	}
	q.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: newDevice.Name,
		},
	})
}

func (d *DeviceHandler) Delete(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
	device, ok := e.Object.(*schedulingv1alpha1.Device)
	if !ok {
		return
	}
	q.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: device.Name,
		},
	})
}

func (d *DeviceHandler) Generic(ctx context.Context, e event.GenericEvent, q workqueue.RateLimitingInterface) {
}
