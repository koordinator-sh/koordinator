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

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func Test_hasDeviceResource(t *testing.T) {
	type args struct {
		podRequest corev1.ResourceList
		deviceType schedulingv1alpha1.DeviceType
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "empty pod request",
			args: args{
				podRequest: corev1.ResourceList{},
				deviceType: schedulingv1alpha1.RDMA,
			},
			want: false,
		},
		{
			name: "no device resource",
			args: args{
				podRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10"),
					corev1.ResourceMemory: resource.MustParse("200"),
				},
				deviceType: schedulingv1alpha1.GPU,
			},
			want: false,
		},
		{
			name: "no match device resource",
			args: args{
				podRequest: corev1.ResourceList{
					corev1.ResourceCPU:     resource.MustParse("10"),
					corev1.ResourceMemory:  resource.MustParse("200"),
					apiext.ResourceGPUCore: resource.MustParse("50"),
				},
				deviceType: schedulingv1alpha1.FPGA,
			},
			want: false,
		},
		{
			name: "has match device resource",
			args: args{
				podRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10"),
					corev1.ResourceMemory: resource.MustParse("200"),
					apiext.ResourceRDMA:   resource.MustParse("50"),
				},
				deviceType: schedulingv1alpha1.RDMA,
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasDeviceResource(tt.args.podRequest, tt.args.deviceType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_validateCommonDeviceRequest(t *testing.T) {
	type args struct {
		podRequest corev1.ResourceList
		deviceType schedulingv1alpha1.DeviceType
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "empty pod request",
			args: args{
				podRequest: corev1.ResourceList{},
				deviceType: schedulingv1alpha1.FPGA,
			},
			wantErr: true,
		},
		{
			name: "invalid fpga request",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceFPGA: resource.MustParse("201"),
				},
				deviceType: schedulingv1alpha1.FPGA,
			},
			wantErr: true,
		},
		{
			name: "valid fpga request",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceFPGA: resource.MustParse("50"),
				},
				deviceType: schedulingv1alpha1.FPGA,
			},
			wantErr: false,
		},
		{
			name: "invalid rdma request",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceRDMA: resource.MustParse("201"),
				},
				deviceType: schedulingv1alpha1.RDMA,
			},
			wantErr: true,
		},
		{
			name: "valid rdma request",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceRDMA: resource.MustParse("50"),
				},
				deviceType: schedulingv1alpha1.RDMA,
			},
			wantErr: false,
		},
		{
			name: "not common device type",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceNvidiaGPU: resource.MustParse("2"),
				},
				deviceType: schedulingv1alpha1.GPU,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCommonDeviceRequest(tt.args.podRequest, tt.args.deviceType)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func Test_validateGPURequest(t *testing.T) {
	tests := []struct {
		name       string
		podRequest corev1.ResourceList
		want       uint
		wantErr    bool
	}{
		{
			name:       "empty pod request",
			podRequest: corev1.ResourceList{},
			want:       0,
			wantErr:    true,
		},
		{
			name: "invalid gpu request 1",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore: resource.MustParse("101"),
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "invalid gpu request 2",
			podRequest: corev1.ResourceList{
				apiext.ResourceNvidiaGPU:      resource.MustParse("2"),
				apiext.ResourceGPU:            resource.MustParse("200"),
				apiext.ResourceGPUCore:        resource.MustParse("200"),
				apiext.ResourceGPUMemory:      resource.MustParse("32Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "invalid gpu request 3",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPU: resource.MustParse("101"),
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "invalid gpu request 4",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("101"),
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "valid gpu request 2",
			podRequest: corev1.ResourceList{
				apiext.ResourceNvidiaGPU: resource.MustParse("2"),
			},
			want:    NvidiaGPUExist,
			wantErr: false,
		},
		{
			name: "valid hygon duc request 2",
			podRequest: corev1.ResourceList{
				apiext.ResourceHygonDCU: resource.MustParse("2"),
			},
			want:    HygonDCUExist,
			wantErr: false,
		},
		{
			name: "valid gpu request 2",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPU: resource.MustParse("200"),
			},
			want:    KoordGPUExist,
			wantErr: false,
		},
		{
			name: "valid gpu request 3",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("200"),
				apiext.ResourceGPUMemory: resource.MustParse("64Gi"),
			},
			want:    GPUCoreExist | GPUMemoryExist,
			wantErr: false,
		},
		{
			name: "valid gpu request 4",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("200"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
			},
			want:    GPUCoreExist | GPUMemoryRatioExist,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateGPURequest(tt.podRequest)
			assert.Equal(t, tt.wantErr, err != nil)
			if !tt.wantErr {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_convertCommonDeviceResource(t *testing.T) {
	type args struct {
		podRequest corev1.ResourceList
		deviceType schedulingv1alpha1.DeviceType
	}
	tests := []struct {
		name string
		args args
		want corev1.ResourceList
	}{
		{
			name: "empty pod request",
			args: args{
				podRequest: nil,
				deviceType: "",
			},
			want: nil,
		},
		{
			name: "non common device",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceNvidiaGPU: resource.MustParse("2"),
				},
				deviceType: schedulingv1alpha1.GPU,
			},
			want: nil,
		},
		{
			name: "rdma",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceRDMA: resource.MustParse("80"),
				},
				deviceType: schedulingv1alpha1.RDMA,
			},
			want: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("80"),
			},
		},
		{
			name: "fpga",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceFPGA: resource.MustParse("80"),
				},
				deviceType: schedulingv1alpha1.FPGA,
			},
			want: corev1.ResourceList{
				apiext.ResourceFPGA: resource.MustParse("80"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertCommonDeviceResource(tt.args.podRequest, tt.args.deviceType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_convertGPUResource(t *testing.T) {
	type args struct {
		podRequest     corev1.ResourceList
		gpuCombination uint
	}
	tests := []struct {
		name string
		args args
		want corev1.ResourceList
	}{
		{
			name: "empty pod request",
			args: args{
				podRequest:     nil,
				gpuCombination: 0,
			},
			want: nil,
		},
		{
			name: "invalid combination",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceNvidiaGPU: resource.MustParse("2"),
				},
				gpuCombination: GPUCoreExist | GPUMemoryExist | GPUMemoryRatioExist,
			},
			want: nil,
		},
		{
			name: "nvidiaGpuExist",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceNvidiaGPU: resource.MustParse("2"),
				},
				gpuCombination: NvidiaGPUExist,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:        *resource.NewQuantity(200, resource.DecimalSI),
				apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
		{
			name: "hygonDCUExist",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceHygonDCU: resource.MustParse("2"),
				},
				gpuCombination: HygonDCUExist,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:        *resource.NewQuantity(200, resource.DecimalSI),
				apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
		{
			name: "koordGpuExist",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceGPU: resource.MustParse("50"),
				},
				gpuCombination: KoordGPUExist,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
		{
			name: "gpuCoreExist | gpuMemoryRatioExist",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
				gpuCombination: GPUCoreExist | GPUMemoryRatioExist,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
		{
			name: "gpuCoreExist | gpuMemoryExist",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceGPUCore:   resource.MustParse("50"),
					apiext.ResourceGPUMemory: resource.MustParse("32Gi"),
				},
				gpuCombination: GPUCoreExist | GPUMemoryExist,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("50"),
				apiext.ResourceGPUMemory: resource.MustParse("32Gi"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertGPUResource(tt.args.podRequest, tt.args.gpuCombination)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_isMultipleCommonDevicePod(t *testing.T) {
	type args struct {
		podRequest corev1.ResourceList
		deviceType schedulingv1alpha1.DeviceType
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "empty pod request",
			args: args{
				podRequest: nil,
				deviceType: "",
			},
			want: false,
		},
		{
			name: "non common device",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceGPUCore: resource.MustParse("100"),
				},
				deviceType: schedulingv1alpha1.GPU,
			},
			want: false,
		},
		{
			name: "multiple fpga",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceFPGA: resource.MustParse("300"),
				},
				deviceType: schedulingv1alpha1.FPGA,
			},
			want: true,
		},
		{
			name: "single fpga",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceFPGA: resource.MustParse("30"),
				},
				deviceType: schedulingv1alpha1.FPGA,
			},
			want: false,
		},
		{
			name: "multiple rdma",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceRDMA: resource.MustParse("300"),
				},
				deviceType: schedulingv1alpha1.RDMA,
			},
			want: true,
		},
		{
			name: "single rdma",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceRDMA: resource.MustParse("30"),
				},
				deviceType: schedulingv1alpha1.RDMA,
			},
			want: false,
		},
		{
			name: "empty pod request",
			args: args{
				podRequest: corev1.ResourceList{},
				deviceType: schedulingv1alpha1.GPU,
			},
			want: false,
		},
		{
			name: "single gpu",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceGPUCore: resource.MustParse("80"),
				},
				deviceType: schedulingv1alpha1.GPU,
			},
			want: false,
		},
		{
			name: "multiple gpu",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceGPUCore: resource.MustParse("200"),
				},
				deviceType: schedulingv1alpha1.GPU,
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPodRequestsMultipleDevice(tt.args.podRequest, tt.args.deviceType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_memRatioToBytes(t *testing.T) {
	currentRatio := resource.MustParse("50")
	totalMemory := resource.MustParse("64Gi")
	expectBytes := resource.MustParse("32Gi")
	newBytes := memRatioToBytes(currentRatio, totalMemory)
	assert.Equal(t, expectBytes, newBytes)
}

func Test_memBytesToRatio(t *testing.T) {
	currentBytes := resource.MustParse("32Gi")
	totalMemory := resource.MustParse("64Gi")
	expectRatio := *resource.NewQuantity(50, resource.DecimalSI)
	newRatio := memBytesToRatio(currentBytes, totalMemory)
	assert.Equal(t, expectRatio, newRatio)
}

func Test_patchContainerGPUResource(t *testing.T) {
	tests := []struct {
		name            string
		pod             *corev1.Pod
		gpuContainerNum int
		podRequest      corev1.ResourceList
	}{
		{
			name: "request nvidia gpu 1",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceNvidiaGPU: resource.MustParse("2"),
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			gpuContainerNum: 0,
			podRequest: corev1.ResourceList{
				apiext.ResourceNvidiaGPU:      resource.MustParse("2"),
				apiext.ResourceGPUCore:        resource.MustParse("200"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
				apiext.ResourceGPUMemory:      resource.MustParse("64Gi"),
			},
		},
		{
			name: "request nvidia gpu 2",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
						},
						{
							Name: "test-container-b",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceNvidiaGPU: resource.MustParse("2"),
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			gpuContainerNum: 1,
			podRequest: corev1.ResourceList{
				apiext.ResourceNvidiaGPU:      resource.MustParse("2"),
				apiext.ResourceGPUCore:        resource.MustParse("200"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
				apiext.ResourceGPUMemory:      resource.MustParse("64Gi"),
			},
		},
		{
			name: "request nvidia gpu",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
				Spec: corev1.PodSpec{
					NodeName: "test-node",
					Containers: []corev1.Container{
						{
							Name: "test-container-a",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceNvidiaGPU: resource.MustParse("2"),
								},
							},
						},
						{
							Name: "test-container-b",
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			gpuContainerNum: 0,
			podRequest: corev1.ResourceList{
				apiext.ResourceNvidiaGPU:      resource.MustParse("2"),
				apiext.ResourceGPUCore:        resource.MustParse("200"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
				apiext.ResourceGPUMemory:      resource.MustParse("64Gi"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patchContainerGPUResource(tt.pod, tt.podRequest)
			assert.Equal(t, tt.pod.Spec.Containers[tt.gpuContainerNum].Resources.Requests, tt.podRequest)
		})
	}
}

func Test_fillGPUTotalMem(t *testing.T) {
	type args struct {
	}
	type wants struct {
	}
	tests := []struct {
		name           string
		gpuTotal       deviceResources
		podRequest     corev1.ResourceList
		wantPodRequest corev1.ResourceList
		wantErr        bool
	}{
		{
			name: "ratio to mem",
			gpuTotal: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("32Gi"),
				},
			},
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			wantPodRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
			},
		},
		{
			name: "mem to ratio",
			gpuTotal: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("32Gi"),
				},
			},
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("50"),
				apiext.ResourceGPUMemory: resource.MustParse("16Gi"),
			},
			wantPodRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
				apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
			},
		},
		{
			name: "missing total",
			gpuTotal: deviceResources{
				0: corev1.ResourceList{},
			},
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			wantPodRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fillGPUTotalMem(tt.gpuTotal, tt.podRequest)
			if tt.wantErr != (err != nil) {
				t.Errorf("wantErr %v but got %v", tt.wantErr, err != nil)
			}
			assert.Equal(t, tt.wantPodRequest, tt.podRequest)
		})
	}
}
