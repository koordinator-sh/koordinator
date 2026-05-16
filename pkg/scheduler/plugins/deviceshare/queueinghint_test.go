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

package deviceshare

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestIsSchedulableAfterPodDeletion(t *testing.T) {
	p := &Plugin{}
	logger := klog.Background()

	tests := []struct {
		name       string
		deletedPod *corev1.Pod
		want       fwktype.QueueingHint
	}{
		{
			name: "deleted pod has no device resources",
			deletedPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
			},
			want: fwktype.QueueSkip,
		},
		{
			name: "deleted pod has device requests",
			deletedPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPUCore: *resource.NewQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
				},
			},
			want: fwktype.Queue,
		},
		{
			name: "deleted pod has device allocations",
			deletedPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod1",
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu": [{"minor": 0}]}`,
					},
				},
			},
			want: fwktype.Queue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := p.isSchedulableAfterPodDeletion(logger, nil, tt.deletedPod, nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsSchedulableAfterDeviceChanged(t *testing.T) {
	p := &Plugin{}
	logger := klog.Background()

	tests := []struct {
		name   string
		oldObj *schedulingv1alpha1.Device
		newObj *schedulingv1alpha1.Device
		want   fwktype.QueueingHint
	}{
		{
			name:   "add device",
			oldObj: nil,
			newObj: &schedulingv1alpha1.Device{},
			want:   fwktype.Queue,
		},
		{
			name: "device health increased",
			oldObj: &schedulingv1alpha1.Device{
				Spec: schedulingv1alpha1.DeviceSpec{
					Devices: []schedulingv1alpha1.DeviceInfo{{Health: false}},
				},
			},
			newObj: &schedulingv1alpha1.Device{
				Spec: schedulingv1alpha1.DeviceSpec{
					Devices: []schedulingv1alpha1.DeviceInfo{{Health: true}},
				},
			},
			want: fwktype.Queue,
		},
		{
			name: "device allocations decreased",
			oldObj: &schedulingv1alpha1.Device{
				Status: schedulingv1alpha1.DeviceStatus{
					Allocations: []schedulingv1alpha1.DeviceAllocation{{}, {}},
				},
			},
			newObj: &schedulingv1alpha1.Device{
				Status: schedulingv1alpha1.DeviceStatus{
					Allocations: []schedulingv1alpha1.DeviceAllocation{{}},
				},
			},
			want: fwktype.Queue,
		},
		{
			name: "irrelevant update",
			oldObj: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"a": "b"}},
			},
			newObj: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"a": "c"}},
			},
			want: fwktype.QueueSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := p.isSchedulableAfterDeviceChanged(logger, nil, tt.oldObj, tt.newObj)
			assert.Equal(t, tt.want, got)
		})
	}
}
