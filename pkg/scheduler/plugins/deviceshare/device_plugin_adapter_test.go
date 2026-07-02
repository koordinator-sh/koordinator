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
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclock "k8s.io/utils/clock/testing"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestGeneralDevicePluginAdapter_Adapt(t *testing.T) {
	now := time.Now()
	dpAdapterClock = fakeclock.NewFakeClock(now)

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

	adapter := &generalDevicePluginAdapter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := adapter.Adapt(nil, tt.args.object, nil)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.wantObject, tt.args.object)
		})
	}
}

func TestGeneralGPUDevicePluginAdapter_Adapt(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

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
			name: "normal",
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
						AnnotationGPUMinors: "0,1",
					},
				},
			},
		},
		{
			name: "hami gpu isolation provider",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							apiext.LabelGPUIsolationProvider: string(apiext.GPUIsolationProviderHAMICore),
						},
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
					Labels: map[string]string{
						apiext.LabelGPUIsolationProvider: string(apiext.GPUIsolationProviderHAMICore),
						apiext.LabelHAMIVGPUNodeName:     testNode.Name,
					},
					Annotations: map[string]string{
						AnnotationGPUMinors: "0",
					},
				},
			},
		},
	}

	adapter := &generalGPUDevicePluginAdapter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &DevicePluginAdaptContext{
				Context: context.TODO(),
				node:    testNode,
			}
			err := adapter.Adapt(ctx, tt.args.object, tt.args.allocation)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.wantObject, tt.args.object)
		})
	}
}

func TestHuaweiGPUDevicePluginAdapter_Adapt(t *testing.T) {
	now := time.Now()
	dpAdapterClock = fakeclock.NewFakeClock(now)

	type args struct {
		object     metav1.Object
		allocation []*apiext.DeviceAllocation
	}
	tests := []struct {
		name       string
		gpuModel   string
		args       args
		wantErr    bool
		wantObject metav1.Object
	}{
		{
			name: "full NPU",
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
			name:     "full NPU - Ascend-310P3-300I-DUO",
			gpuModel: "Ascend-310P3-300I-DUO",
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
						AnnotationPredicateTime:    strconv.FormatInt(now.UnixNano(), 10),
						AnnotationHuaweiAscend310P: "Ascend310P-0,Ascend310P-1",
					},
				},
			},
		},
		{
			name: "vNPU",
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

	adapter := &huaweiGPUDevicePluginAdapter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := adapter.Adapt(&DevicePluginAdaptContext{gpuModel: tt.gpuModel}, tt.args.object, tt.args.allocation)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.wantObject, tt.args.object)
		})
	}
}

func TestCambriconGPUDevicePluginAdapter_Adapt(t *testing.T) {
	now := time.Now()
	dpAdapterClock = fakeclock.NewFakeClock(now)

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
			name: "normal case",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("5"),
							apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationCambriconDsmluAssigned: "false",
						AnnotationCambriconDsmluProfile:  "0_5_4",
					},
				},
			},
		},
		{
			name: "multiple gpu share",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("5"),
							apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
						},
					},
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("5"),
							apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
		},
		{
			name: "missing gpu core",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
		},
		{
			name: "too small gpu memory",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("5"),
							apiext.ResourceGPUMemory: resource.MustParse("128Mi"),
						},
					},
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
		},
	}

	adapter := &cambriconGPUDevicePluginAdapter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := adapter.Adapt(nil, tt.args.object, tt.args.allocation)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.wantObject, tt.args.object)
		})
	}
}

func TestMetaXGPUDevicePluginAdapter_Adapt(t *testing.T) {
	now := time.Now()
	dpAdapterClock = fakeclock.NewFakeClock(now)

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
			name: "normal case",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						ID: "GPU-0",
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("5"),
							apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
						},
					},
					{
						ID: "GPU-1",
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("5"),
							apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationMetaXGPUDevicesAllocated: `[[{"uuid":"GPU-0","compute":5,"vRam":1024},{"uuid":"GPU-1","compute":5,"vRam":1024}]]`,
					},
				},
			},
		},
		{
			name: "missing gpu core",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						ID: "GPU-0",
						Resources: corev1.ResourceList{
							apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
		},
		{
			name: "too small gpu memory",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						ID: "GPU-0",
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("5"),
							apiext.ResourceGPUMemory: resource.MustParse("512Ki"),
						},
					},
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
		},
	}

	adapter := &metaxDevicePluginAdapter{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := adapter.Adapt(nil, tt.args.object, tt.args.allocation)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.wantObject, tt.args.object)
		})
	}
}

func TestPlugin_adaptForDevicePlugin(t *testing.T) {
	now := time.Now()
	dpAdapterClock = fakeclock.NewFakeClock(now)

	type args struct {
		object           metav1.Object
		allocationResult apiext.DeviceAllocations
		nodeName         string
	}
	tests := []struct {
		name        string
		args        args
		device      *schedulingv1alpha1.Device
		node        *corev1.Node
		existingPod *corev1.Pod
		wantErr     bool
		wantObject  metav1.Object
		wantNode    *corev1.Node
	}{
		{
			name: "nvidia",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
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
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nvidia",
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:     "0",
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nvidia",
				},
			},
		},
		{
			name: "huawei",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
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
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "huawei",
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:     "0",
						AnnotationPredicateTime: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationHuaweiNPUCore: "0",
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "huawei",
				},
			},
		},
		{
			name: "cambricon",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:   resource.MustParse("5"),
								apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
				nodeName: "cambricon",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Labels: map[string]string{
						apiext.LabelGPUVendor: apiext.GPUVendorCambricon,
					},
				},
			},
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp:          strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:              "0",
						AnnotationCambriconDsmluAssigned: "false",
						AnnotationCambriconDsmluProfile:  "0_5_4",
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Annotations: map[string]string{
						AnnotationCambriconDsmluLock: fmt.Sprintf("%s,default,test-pod", now.Format(time.RFC3339)),
					},
				},
			},
		},
		{
			name: "metax",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
						{
							Minor: 0,
							ID:    "GPU-0",
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:   resource.MustParse("5"),
								apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
				nodeName: "metax",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "metax",
					Labels: map[string]string{
						apiext.LabelGPUVendor: apiext.GPUVendorMetaX,
					},
				},
			},
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "metax",
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp:            strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:                "0",
						AnnotationMetaXGPUDevicesAllocated: `[[{"uuid":"GPU-0","compute":5,"vRam":1024}]]`,
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "metax",
					Annotations: map[string]string{
						AnnotationHAMiLock: fmt.Sprintf("%s,default,test-pod", now.Format(time.RFC3339)),
					},
				},
			},
		},
		{
			name: "node locked",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:   resource.MustParse("5"),
								apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
				nodeName: "cambricon",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Labels: map[string]string{
						apiext.LabelGPUVendor: apiext.GPUVendorCambricon,
					},
				},
			},
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Annotations: map[string]string{
						AnnotationCambriconDsmluLock: fmt.Sprintf("%s,default,existing-pod", now.Add(-time.Minute).Format(time.RFC3339)),
					},
				},
			},
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "existing-pod",
					Namespace: "default",
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp:          strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:              "0",
						AnnotationCambriconDsmluAssigned: "false",
						AnnotationCambriconDsmluProfile:  "0_5_4",
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Annotations: map[string]string{
						AnnotationCambriconDsmluLock: fmt.Sprintf("%s,default,existing-pod", now.Add(-time.Minute).Format(time.RFC3339)),
					},
				},
			},
		},
		{
			name: "node lock expired",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:   resource.MustParse("5"),
								apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
				nodeName: "cambricon",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Labels: map[string]string{
						apiext.LabelGPUVendor: apiext.GPUVendorCambricon,
					},
				},
			},
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Annotations: map[string]string{
						AnnotationCambriconDsmluLock: fmt.Sprintf("%s,default,existing-pod", now.Add(-nodeLockTimeout-time.Second).Format(time.RFC3339)),
					},
				},
			},
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "existing-pod",
					Namespace: "default",
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp:          strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:              "0",
						AnnotationCambriconDsmluAssigned: "false",
						AnnotationCambriconDsmluProfile:  "0_5_4",
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Annotations: map[string]string{
						AnnotationCambriconDsmluLock: fmt.Sprintf("%s,default,test-pod", now.Format(time.RFC3339)),
					},
				},
			},
		},
		{
			name: "original node locker pod deleted",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
						Annotations: map[string]string{},
					},
				},
				allocationResult: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
						{
							Minor: 0,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:   resource.MustParse("5"),
								apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
							},
						},
					},
				},
				nodeName: "cambricon",
			},
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Labels: map[string]string{
						apiext.LabelGPUVendor: apiext.GPUVendorCambricon,
					},
				},
			},
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Annotations: map[string]string{
						AnnotationCambriconDsmluLock: fmt.Sprintf("%s,default,existing-pod", now.Add(-time.Minute).Format(time.RFC3339)),
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp:          strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:              "0",
						AnnotationCambriconDsmluAssigned: "false",
						AnnotationCambriconDsmluProfile:  "0_5_4",
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cambricon",
					Annotations: map[string]string{
						AnnotationCambriconDsmluLock: fmt.Sprintf("%s,default,test-pod", now.Format(time.RFC3339)),
					},
				},
			},
		},
		{
			name: "unknown vendor",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
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
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:     "0",
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
		},
		{
			name: "hami gpu isolation provider",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						Labels: map[string]string{
							apiext.LabelGPUIsolationProvider: string(apiext.GPUIsolationProviderHAMICore),
						},
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
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nvidia",
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Labels: map[string]string{
						apiext.LabelGPUIsolationProvider: string(apiext.GPUIsolationProviderHAMICore),
						apiext.LabelHAMIVGPUNodeName:     "nvidia",
					},
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:     "0",
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nvidia",
				},
			},
		},
		{
			name: "non-gpu device allocation",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
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
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nvidia",
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nvidia",
				},
			},
		},
		{
			name: "device not found",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
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
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationBindTimestamp: strconv.FormatInt(now.UnixNano(), 10),
						AnnotationGPUMinors:     "0",
					},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
		},
		{
			name: "node not found",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   "default",
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
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
			wantNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, []*corev1.Node{tt.node})
			if tt.device != nil {
				_, err := suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), tt.device, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			if tt.existingPod != nil {
				_, err := suit.ClientSet().CoreV1().Pods(tt.existingPod.Namespace).Create(context.TODO(), tt.existingPod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			pl, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)

			suit.Framework.SharedInformerFactory().Start(nil)
			suit.koordinatorSharedInformerFactory.Start(nil)
			suit.Framework.SharedInformerFactory().WaitForCacheSync(nil)
			suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)

			err = pl.(*Plugin).adaptForDevicePlugin(context.TODO(), tt.args.object, tt.args.allocationResult, tt.args.nodeName)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.wantObject, tt.args.object)
			node, _ := suit.ClientSet().CoreV1().Nodes().Get(context.TODO(), tt.node.Name, metav1.GetOptions{})
			assert.Equal(t, tt.wantNode, node)
		})
	}
}

// Tests for nil annotations and labels safety
func TestGeneralDevicePluginAdapter_NilAnnotations(t *testing.T) {
	now := time.Now()
	dpAdapterClock = fakeclock.NewFakeClock(now)

	adapter := &generalDevicePluginAdapter{}

	// Test with nil annotations
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
	}
	// Explicitly set annotations to nil
	pod.Annotations = nil

	err := adapter.Adapt(nil, pod, nil)
	assert.NoError(t, err)
	assert.NotNil(t, pod.Annotations)
	assert.Equal(t, strconv.FormatInt(now.UnixNano(), 10), pod.Annotations[AnnotationBindTimestamp])
}

func TestGeneralGPUDevicePluginAdapter_NilAnnotationsAndLabels(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	adapter := &generalGPUDevicePluginAdapter{}

	tests := []struct {
		name string
		pod  *corev1.Pod
	}{
		{
			name: "nil annotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod-nil-annot",
				},
			},
		},
		{
			name: "nil labels and nil annotations with HAMI isolation provider",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod-hami",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Explicitly set to nil
			tt.pod.Annotations = nil
			tt.pod.Labels = nil

			// For HAMI test case, set the label after creating pod to test nil check
			if tt.name == "nil labels and nil annotations with HAMI isolation provider" {
				tt.pod.Labels = map[string]string{
					apiext.LabelGPUIsolationProvider: string(apiext.GPUIsolationProviderHAMICore),
				}
				tt.pod.Annotations = nil
			}

			allocation := []*apiext.DeviceAllocation{
				{Minor: 0},
			}

			ctx := &DevicePluginAdaptContext{
				Context: context.TODO(),
				node:    testNode,
			}
			err := adapter.Adapt(ctx, tt.pod, allocation)
			assert.NoError(t, err)
			assert.NotNil(t, tt.pod.Annotations)
			assert.Equal(t, "0", tt.pod.Annotations[AnnotationGPUMinors])

			// For HAMI case, verify label was set
			if tt.name == "nil labels and nil annotations with HAMI isolation provider" {
				assert.NotNil(t, tt.pod.Labels)
				assert.Equal(t, testNode.Name, tt.pod.Labels[apiext.LabelHAMIVGPUNodeName])
			}
		})
	}
}

type nilLabelsObject struct {
	metav1.ObjectMeta
	firstLabels map[string]string
	labelCalls  int
}

func (o *nilLabelsObject) GetLabels() map[string]string {
	o.labelCalls++
	if o.labelCalls == 1 {
		return o.firstLabels
	}
	return nil
}

func (o *nilLabelsObject) SetLabels(labels map[string]string) {
	o.ObjectMeta.Labels = labels
}

func TestGeneralGPUDevicePluginAdapter_NilLabelsMapInit(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	adapter := &generalGPUDevicePluginAdapter{}
	object := &nilLabelsObject{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
		firstLabels: map[string]string{
			apiext.LabelGPUIsolationProvider: string(apiext.GPUIsolationProviderHAMICore),
		},
	}

	allocation := []*apiext.DeviceAllocation{
		{Minor: 0},
	}

	ctx := &DevicePluginAdaptContext{
		Context: context.TODO(),
		node:    testNode,
	}
	err := adapter.Adapt(ctx, object, allocation)
	assert.NoError(t, err)
	assert.NotNil(t, object.Annotations)
	assert.Equal(t, "0", object.Annotations[AnnotationGPUMinors])
	assert.NotNil(t, object.Labels)
	assert.Equal(t, testNode.Name, object.Labels[apiext.LabelHAMIVGPUNodeName])
}

func TestHuaweiGPUDevicePluginAdapter_NilAnnotations(t *testing.T) {
	now := time.Now()
	dpAdapterClock = fakeclock.NewFakeClock(now)

	adapter := &huaweiGPUDevicePluginAdapter{}

	tests := []struct {
		name     string
		gpuModel string
	}{
		{
			name:     "full NPU with nil annotations",
			gpuModel: "",
		},
		{
			name:     "Ascend-310P3-300I-DUO with nil annotations",
			gpuModel: "Ascend-310P3-300I-DUO",
		},
		{
			name:     "vNPU with nil annotations",
			gpuModel: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			}
			// Explicitly set annotations to nil
			pod.Annotations = nil

			allocation := []*apiext.DeviceAllocation{
				{Minor: 0},
			}

			if tt.name == "vNPU with nil annotations" {
				allocation[0].Extension = &apiext.DeviceAllocationExtension{
					GPUSharedResourceTemplate: "vir02",
				}
			}

			err := adapter.Adapt(&DevicePluginAdaptContext{gpuModel: tt.gpuModel}, pod, allocation)
			assert.NoError(t, err)
			assert.NotNil(t, pod.Annotations)
			assert.NotEmpty(t, pod.Annotations[AnnotationPredicateTime])

			if tt.name == "Ascend-310P3-300I-DUO with nil annotations" {
				assert.Equal(t, "Ascend310P-0", pod.Annotations[AnnotationHuaweiAscend310P])
			} else if tt.name == "vNPU with nil annotations" {
				assert.Equal(t, "0-vir02", pod.Annotations[AnnotationHuaweiNPUCore])
			} else {
				assert.Equal(t, "0", pod.Annotations[AnnotationHuaweiNPUCore])
			}
		})
	}
}

func TestCambriconGPUDevicePluginAdapter_NilAnnotations(t *testing.T) {
	adapter := &cambriconGPUDevicePluginAdapter{}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
	}
	// Explicitly set annotations to nil
	pod.Annotations = nil

	allocation := []*apiext.DeviceAllocation{
		{
			Minor: 0,
			Resources: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("5"),
				apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
			},
		},
	}

	err := adapter.Adapt(nil, pod, allocation)
	assert.NoError(t, err)
	assert.NotNil(t, pod.Annotations)
	assert.Equal(t, "false", pod.Annotations[AnnotationCambriconDsmluAssigned])
	assert.Equal(t, "0_5_4", pod.Annotations[AnnotationCambriconDsmluProfile])
}

func TestMetaXGPUDevicePluginAdapter_NilAnnotations(t *testing.T) {
	adapter := &metaxDevicePluginAdapter{}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
	}
	// Explicitly set annotations to nil
	pod.Annotations = nil

	allocation := []*apiext.DeviceAllocation{
		{
			ID: "GPU-0",
			Resources: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("5"),
				apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
			},
		},
	}

	err := adapter.Adapt(nil, pod, allocation)
	assert.NoError(t, err)
	assert.NotNil(t, pod.Annotations)
	assert.Equal(t, `[[{"uuid":"GPU-0","compute":5,"vRam":1024}]]`, pod.Annotations[AnnotationMetaXGPUDevicesAllocated])
}

func Test_buildGPUMinorsStr(t *testing.T) {
	type args struct {
		allocation []*apiext.DeviceAllocation
		prefix     string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "single minor",
			args: args{
				allocation: []*apiext.DeviceAllocation{
					{Minor: 0},
				},
			},
			want: "0",
		},
		{
			name: "multiple minors",
			args: args{
				allocation: []*apiext.DeviceAllocation{
					{Minor: 0},
					{Minor: 1},
				},
			},
			want: "0,1",
		},
		{
			name: "multiple minors with prefix",
			args: args{
				allocation: []*apiext.DeviceAllocation{
					{Minor: 0},
					{Minor: 1},
				},
				prefix: "test-",
			},
			want: "test-0,test-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildGPUMinorsStr(tt.args.allocation, tt.args.prefix))
		})
	}
}
