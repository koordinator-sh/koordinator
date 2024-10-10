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
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestDefaultDeviceHandler_CalcDesiredRequestsAndCount(t *testing.T) {
	fakeDeviceCRCopy := fakeDeviceCR.DeepCopy()
	fakeDeviceCRCopy.Spec.Devices = append(fakeDeviceCRCopy.Spec.Devices, schedulingv1alpha1.DeviceInfo{
		Type: schedulingv1alpha1.RDMA,
		Labels: map[string]string{
			"type": "fakeS",
		},
		UUID:   "123456789",
		Minor:  pointer.Int32(5),
		Health: true,
		Resources: corev1.ResourceList{
			apiext.ResourceRDMA: resource.MustParse("100"),
		},
		Topology: &schedulingv1alpha1.DeviceTopology{
			SocketID: 1,
			NodeID:   1,
			PCIEID:   "4",
		},
	})

	cache := newNodeDeviceCache()
	cache.updateNodeDevice(fakeDeviceCRCopy.Name, fakeDeviceCRCopy)

	resources := corev1.ResourceList{
		apiext.ResourceRDMA: resource.MustParse("100"),
	}
	tests := []struct {
		name              string
		podRequests       corev1.ResourceList
		hint              *apiext.DeviceHint
		wantRequests      corev1.ResourceList
		wantDesiredCount  int
		wantStatusSuccess bool
	}{
		{
			name:              "general request one NIC device",
			podRequests:       resources,
			wantRequests:      resources,
			wantDesiredCount:  1,
			wantStatusSuccess: true,
		},
		{
			name: "apply for all NIC devices that type is fakeW",
			podRequests: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("1"),
			},
			hint: &apiext.DeviceHint{
				Selector:         &metav1.LabelSelector{MatchLabels: map[string]string{"type": "fakeW"}},
				AllocateStrategy: apiext.ApplyForAllDeviceAllocateStrategy,
			},
			wantRequests: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("1"),
			},
			wantDesiredCount:  4,
			wantStatusSuccess: true,
		},
		{
			name: "apply for all NIC devices that type is fakeS",
			podRequests: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("1"),
			},
			hint: &apiext.DeviceHint{
				Selector:         &metav1.LabelSelector{MatchLabels: map[string]string{"type": "fakeS"}},
				AllocateStrategy: apiext.ApplyForAllDeviceAllocateStrategy,
			},
			wantRequests: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("1"),
			},
			wantDesiredCount:  1,
			wantStatusSuccess: true,
		},
		{
			name: "apply for all NIC devices",
			podRequests: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("1"),
			},
			hint: &apiext.DeviceHint{
				Selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "type",
						Operator: metav1.LabelSelectorOpExists,
					},
				}},
				AllocateStrategy: apiext.ApplyForAllDeviceAllocateStrategy,
			},
			wantRequests: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("1"),
			},
			wantDesiredCount:  5,
			wantStatusSuccess: true,
		},
		{
			name: "apply for all unmatched NIC devices",
			podRequests: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("1"),
			},
			hint: &apiext.DeviceHint{
				Selector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "non-exists-label",
						Operator: metav1.LabelSelectorOpExists,
					},
				}},
				AllocateStrategy: apiext.ApplyForAllDeviceAllocateStrategy,
			},
			wantRequests:      nil,
			wantDesiredCount:  0,
			wantStatusSuccess: false,
		},
		{
			name: "request as count",
			podRequests: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("4"),
			},
			hint: &apiext.DeviceHint{
				AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
			},
			wantRequests: corev1.ResourceList{
				apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
			},
			wantDesiredCount:  4,
			wantStatusSuccess: true,
		},
		{
			name: "request as count and exclusive",
			podRequests: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("4"),
			},
			hint: &apiext.DeviceHint{
				AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				ExclusivePolicy:  apiext.DeviceLevelDeviceExclusivePolicy,
			},
			wantRequests: corev1.ResourceList{
				apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
			},
			wantDesiredCount:  4,
			wantStatusSuccess: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &DefaultDeviceHandler{
				deviceType:   schedulingv1alpha1.RDMA,
				resourceName: apiext.ResourceRDMA,
			}
			nodeDevice := cache.getNodeDevice(fakeDeviceCRCopy.Name, false)
			requests, desiredCount, status := handler.CalcDesiredRequestsAndCount(nil, nil, tt.podRequests, nodeDevice, tt.hint, nil)
			assert.Equal(t, tt.wantRequests, requests)
			assert.Equal(t, tt.wantDesiredCount, desiredCount)
			assert.Equal(t, tt.wantStatusSuccess, status.IsSuccess())
		})
	}
}
