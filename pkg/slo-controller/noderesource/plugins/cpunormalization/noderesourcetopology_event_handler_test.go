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

package cpunormalization

import (
	"context"
	"testing"

	topologyv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func Test_isNRTCPUBasicInfoCreated(t *testing.T) {
	tests := []struct {
		name string
		arg  *topologyv1alpha1.NodeResourceTopology
		want bool
	}{
		{
			name: "parse ratio failed",
			arg: &topologyv1alpha1.NodeResourceTopology{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationCPUBasicInfo: "{]",
					},
				},
			},
			want: false,
		},
		{
			name: "ratio not exist",
			arg: &topologyv1alpha1.NodeResourceTopology{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			want: false,
		},
		{
			name: "ratio is created",
			arg: &topologyv1alpha1.NodeResourceTopology{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX"}`,
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNRTCPUBasicInfoCreated(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_isNRTCPUBasicInfoChanged(t *testing.T) {
	type args struct {
		nrtOld *topologyv1alpha1.NodeResourceTopology
		nrtNew *topologyv1alpha1.NodeResourceTopology
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "both have no ratio",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{},
			},
			want: false,
		},
		{
			name: "old has the ratio",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX"}`,
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{},
			},
			want: true,
		},
		{
			name: "new has the ratio",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX"}`,
						},
					},
				},
			},
			want: true,
		},
		{
			name: "ratios are different",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX"}`,
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX", "turboEnabled": true}`,
						},
					},
				},
			},
			want: true,
		},
		{
			name: "old ratio parse failed",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationCPUBasicInfo: "{{}",
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{},
			},
			want: true,
		},
		{
			name: "new ratio parse failed",
			args: args{
				nrtOld: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"xxx":                            "yyy",
							extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX"}`,
						},
					},
				},
				nrtNew: &topologyv1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"xxx":                            "yyy",
							extension.AnnotationCPUBasicInfo: "{{}",
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNRTCPUBasicInfoChanged(tt.args.nrtOld, tt.args.nrtNew)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_EnqueueRequestForNodeResourceTopology(t *testing.T) {
	tests := []struct {
		name      string
		fn        func(handler *nrtHandler, q workqueue.RateLimitingInterface)
		hasEvent  bool
		eventName string
	}{
		{
			name: "create NRT event",
			fn: func(handler *nrtHandler, q workqueue.RateLimitingInterface) {
				handler.Create(context.TODO(), event.CreateEvent{
					Object: &topologyv1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Annotations: map[string]string{
								"xxx":                            "yyy",
								extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX"}`,
							},
						},
					},
				}, q)
			},
			hasEvent:  true,
			eventName: "node1",
		},
		{
			name: "create event not NRT",
			fn: func(handler *nrtHandler, q workqueue.RateLimitingInterface) {
				handler.Create(context.TODO(), event.CreateEvent{
					Object: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod1",
						},
					},
				}, q)
			},
			hasEvent: false,
		},
		{
			name: "delete NRT event",
			fn: func(handler *nrtHandler, q workqueue.RateLimitingInterface) {
				handler.Delete(context.TODO(), event.DeleteEvent{
					Object: &topologyv1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Annotations: map[string]string{
								"xxx":                            "yyy",
								extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX"}`,
							},
						},
					},
				}, q)
			},
			hasEvent:  false,
			eventName: "node1",
		},
		{
			name: "delete event not NRT",
			fn: func(handler *nrtHandler, q workqueue.RateLimitingInterface) {
				handler.Delete(context.TODO(), event.DeleteEvent{
					Object: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod1",
						},
					},
				}, q)
			},
			hasEvent: false,
		},
		{
			name: "update NRT event",
			fn: func(handler *nrtHandler, q workqueue.RateLimitingInterface) {
				handler.Update(context.TODO(), event.UpdateEvent{
					ObjectOld: &topologyv1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "node1",
							ResourceVersion: "0",
							Annotations: map[string]string{
								extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX"}`,
							},
						},
					},
					ObjectNew: &topologyv1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "node1",
							ResourceVersion: "1",
							Annotations: map[string]string{
								extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX", "turboEnabled": true}`,
							},
						},
					},
				}, q)
			},
			hasEvent:  true,
			eventName: "node1",
		},
		{
			name: "update node event ignore",
			fn: func(handler *nrtHandler, q workqueue.RateLimitingInterface) {
				handler.Update(context.TODO(), event.UpdateEvent{
					ObjectOld: &topologyv1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
						},
					},
					ObjectNew: &topologyv1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
						},
					},
				}, q)
			},
			hasEvent: false,
		},
		{
			name: "generic node event ignore",
			fn: func(handler *nrtHandler, q workqueue.RateLimitingInterface) {
				handler.Generic(context.TODO(), event.GenericEvent{}, q)
			},
			hasEvent: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			handler := &nrtHandler{}
			tt.fn(handler, queue)
			if tt.hasEvent {
				assert.True(t, queue.Len() > 0)
				e, shutdown := queue.Get()
				assert.False(t, shutdown)
				assert.Equal(t, tt.eventName, e.(reconcile.Request).Name)
			}
			if !tt.hasEvent && queue.Len() > 0 {
				e, shutdown := queue.Get()
				assert.False(t, shutdown)
				t.Errorf("unexpeced event: %v", e)
			}
		})
	}
}
