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

func TestHygonDCUDevicePluginAdapter_Adapt(t *testing.T) {
	now := time.Now()
	dpAdapterClock = fakeclock.NewFakeClock(now)

	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "master",
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
			name: "single DCU device with single container",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "sample-pod-hami",
						Namespace:   "default",
						Annotations: map[string]string{},
						Labels:      map[string]string{},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "demo-container",
							},
						},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						ID:    "DCU-TS5V0916070401",
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("50"),
							apiext.ResourceGPUMemory: resource.MustParse("3072Mi"),
						},
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sample-pod-hami",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationHygonDCUDevicesAllocated:  "DCU-TS5V0916070401,DCU,3072,50:0;",
						AnnotationHygonDCUDevicesToAllocate: ";",
						AnnotationHygonBindPhase:            "success",
						AnnotationHygonBindTime:             strconv.FormatInt(now.Unix(), 10),
						AnnotationHygonVgpuTime:             strconv.FormatInt(now.Unix(), 10),
						AnnotationHygonContainerIndex:       "0,",
						"demo-container-hygon.com/dcucores": "50",
						"demo-container-hygon.com/dcumem":   "3072",
						"demo-container-hygon.com/dcunum":   "1",
					},
					Labels: map[string]string{
						apiext.LabelHAMIVGPUNodeName: "master",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "demo-container",
						},
					},
				},
			},
		},
		{
			name: "multiple DCU devices with single container",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod-multi",
						Namespace:   "default",
						Annotations: map[string]string{},
						Labels:      map[string]string{},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "demo-container",
							},
						},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						ID:    "DCU-DEVICE-0",
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("30"),
							apiext.ResourceGPUMemory: resource.MustParse("2048Mi"),
						},
					},
					{
						ID:    "DCU-DEVICE-1",
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("30"),
							apiext.ResourceGPUMemory: resource.MustParse("2048Mi"),
						},
					},
				},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-multi",
					Namespace: "default",
					Annotations: map[string]string{
						AnnotationHygonDCUDevicesAllocated:  "DCU-DEVICE-0,DCU,2048,30:0;DCU-DEVICE-1,DCU,2048,30:0;",
						AnnotationHygonDCUDevicesToAllocate: ";",
						AnnotationHygonBindPhase:            "success",
						AnnotationHygonBindTime:             strconv.FormatInt(now.Unix(), 10),
						AnnotationHygonVgpuTime:             strconv.FormatInt(now.Unix(), 10),
						AnnotationHygonContainerIndex:       "0,",
						"demo-container-hygon.com/dcucores": "60",
						"demo-container-hygon.com/dcumem":   "4096",
						"demo-container-hygon.com/dcunum":   "2",
					},
					Labels: map[string]string{
						apiext.LabelHAMIVGPUNodeName: "master",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "demo-container",
						},
					},
				},
			},
		},
		{
			name: "missing gpu core resource",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod-no-core",
						Namespace:   "default",
						Annotations: map[string]string{},
						Labels:      map[string]string{},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "demo-container",
							},
						},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						ID:    "DCU-DEVICE-0",
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUMemory: resource.MustParse("3072Mi"),
						},
					},
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod-no-core",
					Namespace:   "default",
					Annotations: map[string]string{},
					Labels:      map[string]string{},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "demo-container",
						},
					},
				},
			},
		},
		{
			name: "missing gpu memory resource",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod-no-mem",
						Namespace:   "default",
						Annotations: map[string]string{},
						Labels:      map[string]string{},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "demo-container",
							},
						},
					},
				},
				allocation: []*apiext.DeviceAllocation{
					{
						ID:    "DCU-DEVICE-0",
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore: resource.MustParse("50"),
						},
					},
				},
			},
			wantErr: true,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod-no-mem",
					Namespace:   "default",
					Annotations: map[string]string{},
					Labels:      map[string]string{},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "demo-container",
						},
					},
				},
			},
		},
		{
			name: "empty allocation",
			args: args{
				object: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod-empty",
						Namespace:   "default",
						Annotations: map[string]string{},
						Labels:      map[string]string{},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "demo-container",
							},
						},
					},
				},
				allocation: []*apiext.DeviceAllocation{},
			},
			wantErr: false,
			wantObject: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod-empty",
					Namespace:   "default",
					Annotations: map[string]string{},
					Labels:      map[string]string{},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "demo-container",
						},
					},
				},
			},
		},
	}

	adapter := &hygonDCUDevicePluginAdapter{}
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

func TestNormalizeHygonDeviceID(t *testing.T) {
	tests := []struct {
		name  string
		rawID string
		minor int32
		want  string
	}{
		{
			name:  "already has DCU- prefix",
			rawID: "DCU-TS5V0916070401",
			minor: 0,
			want:  "DCU-TS5V0916070401",
		},
		{
			name:  "missing DCU- prefix",
			rawID: "TS5V0916070401",
			minor: 0,
			want:  "DCU-TS5V0916070401",
		},
		{
			name:  "empty rawID falls back to minor",
			rawID: "",
			minor: 3,
			want:  "DCU-3",
		},
		{
			name:  "empty rawID with minor 0",
			rawID: "",
			minor: 0,
			want:  "DCU-0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeHygonDeviceID(tt.rawID, tt.minor)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFindHygonDCUContainerIndex(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want int
	}{
		{
			name: "no DCU resource declared returns 0",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app"},
					},
				},
			},
			want: 0,
		},
		{
			name: "DCU in Limits of first container",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "dcu-container",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									apiext.ResourceHygonDCUNum: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			want: 0,
		},
		{
			name: "DCU in Requests of second container",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "sidecar"},
						{
							Name: "dcu-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceHygonDCUNum: resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
			want: 1,
		},
		{
			name: "multi-container pod with DCU in Limits of third container",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "init"},
						{Name: "sidecar"},
						{
							Name: "worker",
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									apiext.ResourceHygonDCUNum: resource.MustParse("1"),
								},
							},
						},
					},
				},
			},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findHygonDCUContainerIndex(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHygonDCUDevicePluginAdapter_NodeLockKey(t *testing.T) {
	adapter := &hygonDCUDevicePluginAdapter{}
	assert.Equal(t, AnnotationHAMiLock, adapter.NodeLockKey())
}

func TestHygonDCUDevicePluginAdapter_Adapt_NotPod(t *testing.T) {
	adapter := &hygonDCUDevicePluginAdapter{}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	ctx := &DevicePluginAdaptContext{Context: context.TODO(), node: node}
	err := adapter.Adapt(ctx, &metav1.ObjectMeta{Name: "not-a-pod"}, []*apiext.DeviceAllocation{
		{ID: "DCU-0", Minor: 0, Resources: corev1.ResourceList{
			apiext.ResourceGPUCore:   resource.MustParse("50"),
			apiext.ResourceGPUMemory: resource.MustParse("3072Mi"),
		}},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "object is not a pod")
}

func TestBuildHygonAllocationString(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		allocation     []*apiext.DeviceAllocation
		containerIndex int
		want           string
		wantErr        bool
	}{
		{
			name: "device ID without DCU- prefix gets normalized",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
			},
			allocation: []*apiext.DeviceAllocation{
				{
					ID:    "TS5V0916070401",
					Minor: 0,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:   resource.MustParse("50"),
						apiext.ResourceGPUMemory: resource.MustParse("3072Mi"),
					},
				},
			},
			containerIndex: 0,
			want:           "DCU-TS5V0916070401,DCU,3072,50:0;",
			wantErr:        false,
		},
		{
			name: "empty device ID falls back to minor",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
			},
			allocation: []*apiext.DeviceAllocation{
				{
					ID:    "",
					Minor: 5,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:   resource.MustParse("100"),
						apiext.ResourceGPUMemory: resource.MustParse("8192Mi"),
					},
				},
			},
			containerIndex: 0,
			want:           "DCU-5,DCU,8192,100:0;",
			wantErr:        false,
		},
		{
			name: "container index 1 for multi-container pod",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "sidecar"}, {Name: "worker"}}},
			},
			allocation: []*apiext.DeviceAllocation{
				{
					ID:    "DCU-DEVICE-0",
					Minor: 0,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:   resource.MustParse("30"),
						apiext.ResourceGPUMemory: resource.MustParse("2048Mi"),
					},
				},
			},
			containerIndex: 1,
			want:           "DCU-DEVICE-0,DCU,2048,30:1;",
			wantErr:        false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildHygonAllocationString(tt.pod, tt.allocation, tt.containerIndex)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
