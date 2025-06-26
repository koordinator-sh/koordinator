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
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclock "k8s.io/utils/clock/testing"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestGeneralDevicePluginAdapter_Adapt(t *testing.T) {
	now := time.Now()

	type args struct {
		object metav1.Object
	}
	tests := []struct {
		name       string
		args       args
		wantErr    bool
		wantObject metav1.Object
	}{
		{
			name: "normal",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
					},
				},
			},
		},
	}

	adapter := &generalDevicePluginAdapter{
		clock: fakeclock.NewFakeClock(now),
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := adapter.Adapt(tt.args.object, nil)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.wantObject, tt.args.object)
		})
	}
}

func TestHuaweiGPUDevicePluginAdapter_Adapt(t *testing.T) {
	now := time.Now()

	type args struct {
		object     metav1.Object
		allocation []*apiext.DeviceAllocation
	}
	tests := []struct {
		name       string
		args       args
		wantErr    bool
		wantObject metav1.Object
	}{
		{
			name: "single minor",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{Minor: 0},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationPredicateTime: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationHuaweiNPUCore: "0",
					},
				},
			},
		},
		{
			name: "multiple minors",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{Minor: 0},
					{Minor: 1},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationPredicateTime: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationHuaweiNPUCore: "0,1",
					},
				},
			},
		},
		{
			name: "minor with template",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Extension: &apiext.DeviceAllocationExtension{
							GPUSharedResourceTemplate: "vir02",
						},
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationPredicateTime: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationHuaweiNPUCore: "0-vir02",
					},
				},
			},
		},
	}

	adapter := &huaweiGPUDevicePluginAdapter{
		clock: fakeclock.NewFakeClock(now),
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := adapter.Adapt(tt.args.object, tt.args.allocation)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.wantObject, tt.args.object)
		})
	}
}

func TestPlugin_adaptForDevicePlugin(t *testing.T) {
	now := time.Now()
	defaultDevicePluginAdapter = &generalDevicePluginAdapter{
		clock: fakeclock.NewFakeClock(now),
	}
	gpuDevicePluginAdapterMap = map[string]DevicePluginAdapter{
		apiext.GPUVendorHuawei: &huaweiGPUDevicePluginAdapter{
			clock: fakeclock.NewFakeClock(now),
		},
	}

	type args struct {
		object           metav1.Object
		allocationResult apiext.DeviceAllocations
		nodeName         string
	}
	tests := []struct {
		name       string
		args       args
		device     *schedulingv1alpha1.Device
		wantErr    bool
		wantObject metav1.Object
	}{
		{
			name: "nvidia",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
						{Minor: 0},
					},
				},
				nodeName: "nvidia",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nvidia",
					Labels: map[string]string{
						apiext.LabelGPUVendor: apiext.GPUVendorNVIDIA,
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
					},
				},
			},
		},
		{
			name: "huawei",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
						{Minor: 0},
					},
				},
				nodeName: "huawei",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "huawei",
					Labels: map[string]string{
						apiext.LabelGPUVendor: apiext.GPUVendorHuawei,
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationPredicateTime: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationHuaweiNPUCore: "0",
					},
				},
			},
		},
		{
			name: "unknown vendor",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
						{Minor: 0},
					},
				},
				nodeName: "test",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						apiext.LabelGPUVendor: "test",
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
					},
				},
			},
		},
		{
			name: "non-gpu device allocation",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.FPGA: []*apiext.DeviceAllocation{
						{Minor: 0},
					},
				},
				nodeName: "nvidia",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nvidia",
					Labels: map[string]string{
						apiext.LabelGPUVendor: apiext.GPUVendorNVIDIA,
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
					},
				},
			},
		},
		{
			name: "device not found",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
						{Minor: 0},
					},
				},
				nodeName: "test",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nvidia",
					Labels: map[string]string{
						apiext.LabelGPUVendor: apiext.GPUVendorNVIDIA,
					},
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			if tt.device != nil {
				_, err := suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), tt.device, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			pl, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)

			suit.Framework.SharedInformerFactory().Start(nil)
			suit.koordinatorSharedInformerFactory.Start(nil)
			suit.Framework.SharedInformerFactory().WaitForCacheSync(nil)
			suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)

			err = pl.(*Plugin).adaptForDevicePlugin(tt.args.object, tt.args.allocationResult, tt.args.nodeName)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.wantObject, tt.args.object)
		})
	}
}
