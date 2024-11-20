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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func Test_EnqueueRequestForNodeMetricMetric(t *testing.T) {
	tests := []struct {
		name      string
		fn        func(handler handler.EventHandler, q workqueue.RateLimitingInterface)
		hasEvent  bool
		eventName string
	}{
		{
			name: "create device event",
			fn: func(handler handler.EventHandler, q workqueue.RateLimitingInterface) {
				handler.Create(context.TODO(), event.CreateEvent{
					Object: &schedulingv1alpha1.Device{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
						},
					},
				}, q)
			},
			hasEvent:  true,
			eventName: "node1",
		},
		{
			name: "delete device event",
			fn: func(handler handler.EventHandler, q workqueue.RateLimitingInterface) {
				handler.Delete(context.TODO(), event.DeleteEvent{
					Object: &schedulingv1alpha1.Device{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
						},
					},
				}, q)
			},
			hasEvent:  true,
			eventName: "node1",
		},
		{
			name: "delete event not device",
			fn: func(handler handler.EventHandler, q workqueue.RateLimitingInterface) {
				handler.Delete(context.TODO(), event.DeleteEvent{
					Object: &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
						},
					},
				}, q)
			},
			hasEvent: false,
		},
		{
			name: "generic event ignore",
			fn: func(handler handler.EventHandler, q workqueue.RateLimitingInterface) {
				handler.Generic(context.TODO(), event.GenericEvent{}, q)
			},
			hasEvent: false,
		},
		{
			name: "update device event",
			fn: func(handler handler.EventHandler, q workqueue.RateLimitingInterface) {
				handler.Update(context.TODO(), event.UpdateEvent{
					ObjectOld: &schedulingv1alpha1.Device{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "node1",
							ResourceVersion: "100",
						},
						Spec: schedulingv1alpha1.DeviceSpec{
							Devices: []schedulingv1alpha1.DeviceInfo{
								{},
							},
						},
					},
					ObjectNew: &schedulingv1alpha1.Device{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "node1",
							ResourceVersion: "101",
						},
						Spec: schedulingv1alpha1.DeviceSpec{
							Devices: []schedulingv1alpha1.DeviceInfo{
								{},
								{},
							},
						},
					},
				}, q)
			},
			hasEvent:  true,
			eventName: "node1",
		},
		{
			name: "update device event ignore",
			fn: func(handler handler.EventHandler, q workqueue.RateLimitingInterface) {
				handler.Update(context.TODO(), event.UpdateEvent{
					ObjectOld: &schedulingv1alpha1.Device{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "node1",
							ResourceVersion: "100",
						},
						Spec: schedulingv1alpha1.DeviceSpec{
							Devices: []schedulingv1alpha1.DeviceInfo{
								{},
							},
						},
					},
					ObjectNew: &schedulingv1alpha1.Device{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "node1",
							ResourceVersion: "100",
						},
						Spec: schedulingv1alpha1.DeviceSpec{
							Devices: []schedulingv1alpha1.DeviceInfo{
								{},
							},
						},
					},
				}, q)
			},
			hasEvent: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			h := &DeviceHandler{}
			tt.fn(h, queue)
			assert.Equal(t, tt.hasEvent, queue.Len() > 0, "unexpected event")
			if tt.hasEvent {
				assert.True(t, queue.Len() >= 0, "expected event")
				e, _ := queue.Get()
				assert.Equal(t, tt.eventName, e.(reconcile.Request).Name)
			}
		})
	}

}
