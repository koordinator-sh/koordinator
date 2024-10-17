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
	quotav1 "k8s.io/apiserver/pkg/quota/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestValidateDeviceRequest(t *testing.T) {
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
			want:    NvidiaGPU,
			wantErr: false,
		},
		{
			name: "valid hygon duc request 2",
			podRequest: corev1.ResourceList{
				apiext.ResourceHygonDCU: resource.MustParse("2"),
			},
			want:    HygonDCU,
			wantErr: false,
		},
		{
			name: "valid gpu request 2",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPU: resource.MustParse("200"),
			},
			want:    KoordGPU,
			wantErr: false,
		},
		{
			name: "valid gpu request 3",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("200"),
				apiext.ResourceGPUMemory: resource.MustParse("64Gi"),
			},
			want:    GPUCore | GPUMemory,
			wantErr: false,
		},
		{
			name: "valid gpu request 4",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("200"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
			},
			want:    GPUCore | GPUMemoryRatio,
			wantErr: false,
		},
		{
			name: "valid gpu request 5",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
			},
			want:    GPUMemoryRatio,
			wantErr: false,
		},
		{
			name: "valid gpu request 7",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUMemory: resource.MustParse("64Gi"),
			},
			want:    GPUMemory,
			wantErr: false,
		},
		{
			name: "invalid gpu request 8",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("101"),
				apiext.ResourceGPUShared:      resource.MustParse("1"),
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "invalid gpu request 9",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("101"),
				apiext.ResourceGPUShared:      resource.MustParse("2"),
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "invalid gpu request 10",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("101"),
				apiext.ResourceGPUMemory: resource.MustParse("64Gi"),
				apiext.ResourceGPUShared: resource.MustParse("1"),
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "invalid gpu request 11",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("101"),
				apiext.ResourceGPUMemory: resource.MustParse("64Gi"),
				apiext.ResourceGPUShared: resource.MustParse("2"),
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "valid gpu request 12",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
				apiext.ResourceGPUShared:      resource.MustParse("2"),
			},
			want:    GPUShared | GPUMemoryRatio,
			wantErr: false,
		},
		{
			name: "valid gpu request 13",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				apiext.ResourceGPUShared:      resource.MustParse("2"),
			},
			want:    GPUShared | GPUMemoryRatio,
			wantErr: false,
		},
		{
			name: "invalid gpu request 14",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("100"),
				apiext.ResourceGPUMemory: resource.MustParse("64Gi"),
				apiext.ResourceGPUShared: resource.MustParse("1"),
			},
			want:    GPUShared | GPUMemory | GPUCore,
			wantErr: false,
		},
		{
			name: "invalid gpu request 15",
			podRequest: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("100"),
				apiext.ResourceGPUMemory: resource.MustParse("64Gi"),
				apiext.ResourceGPUShared: resource.MustParse("2"),
			},
			want:    GPUShared | GPUMemory | GPUCore,
			wantErr: false,
		},
		{
			name: "invalid fpga request",
			podRequest: corev1.ResourceList{
				apiext.ResourceFPGA: resource.MustParse("201"),
			},
			want:    FPGA,
			wantErr: true,
		},
		{
			name: "valid fpga request",
			podRequest: corev1.ResourceList{
				apiext.ResourceFPGA: resource.MustParse("50"),
			},
			want:    FPGA,
			wantErr: false,
		},
		{
			name: "invalid rdma request",
			podRequest: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("201"),
			},
			want:    RDMA,
			wantErr: true,
		},
		{
			name: "valid rdma request",
			podRequest: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("50"),
			},
			want:    RDMA,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateDeviceRequest(tt.podRequest)
			assert.Equal(t, tt.wantErr, err != nil)
			if !tt.wantErr {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestConvertDeviceRequest(t *testing.T) {
	type args struct {
		podRequest  corev1.ResourceList
		combination uint
	}
	tests := []struct {
		name string
		args args
		want corev1.ResourceList
	}{
		{
			name: "empty pod request",
			args: args{
				podRequest:  nil,
				combination: 0,
			},
			want: nil,
		},
		{
			name: "invalid combination",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceNvidiaGPU: resource.MustParse("2"),
				},
				combination: GPUCore | GPUMemory | GPUMemoryRatio,
			},
			want: nil,
		},
		{
			name: "nvidiaGPU",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceNvidiaGPU: resource.MustParse("2"),
				},
				combination: NvidiaGPU,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:        *resource.NewQuantity(200, resource.DecimalSI),
				apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
		{
			name: "hygonDCU",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceHygonDCU: resource.MustParse("2"),
				},
				combination: HygonDCU,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:        *resource.NewQuantity(200, resource.DecimalSI),
				apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
		{
			name: "koordGPU",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceGPU: resource.MustParse("50"),
				},
				combination: KoordGPU,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
		{
			name: "gpuCore | gpuMemoryRatio",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
				combination: GPUCore | GPUMemoryRatio,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
		{
			name: "gpuCore | gpuMemory",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceGPUCore:   resource.MustParse("50"),
					apiext.ResourceGPUMemory: resource.MustParse("32Gi"),
				},
				combination: GPUCore | GPUMemory,
			},
			want: corev1.ResourceList{
				apiext.ResourceGPUCore:   resource.MustParse("50"),
				apiext.ResourceGPUMemory: resource.MustParse("32Gi"),
			},
		},
		{
			name: "rdma",
			args: args{
				podRequest: corev1.ResourceList{
					apiext.ResourceRDMA: resource.MustParse("80"),
				},
				combination: RDMA,
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
				combination: FPGA,
			},
			want: corev1.ResourceList{
				apiext.ResourceFPGA: resource.MustParse("80"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertDeviceRequest(tt.args.podRequest, tt.args.combination)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_memoryRatioToBytes(t *testing.T) {
	currentRatio := resource.MustParse("50")
	totalMemory := resource.MustParse("64Gi")
	expectBytes := resource.MustParse("32Gi")
	newBytes := memoryRatioToBytes(currentRatio, totalMemory)
	assert.Equal(t, expectBytes, newBytes)
}

func Test_memoryBytesToRatio(t *testing.T) {
	currentBytes := resource.MustParse("32Gi")
	totalMemory := resource.MustParse("64Gi")
	expectRatio := *resource.NewQuantity(50, resource.DecimalSI)
	newRatio := memoryBytesToRatio(currentBytes, totalMemory)
	assert.Equal(t, expectRatio, newRatio)
}

func Test_parseGPURequirements(t *testing.T) {
	bandWidthOf200Gi := resource.MustParse("200Gi")

	tests := []struct {
		name        string
		pod         *corev1.Pod
		podRequests map[schedulingv1alpha1.DeviceType]corev1.ResourceList
		gpuHints    *apiext.DeviceHint
		want        *GPURequirements
		wantErr     assert.ErrorAssertionFunc
	}{
		{
			name: "No GPU requests",
			pod:  &corev1.Pod{},
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU: {},
			},
			gpuHints: nil,
			want:     nil,
			wantErr:  assert.NoError,
		},
		{
			name: "Invalid GPU partition spec",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationGPUPartitionSpec: `invalid GPU Partition Spec`,
					},
				},
			},
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU: {
					apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
					apiext.ResourceGPUCore:        resource.MustParse("200"),
				},
			},
			want:    nil,
			wantErr: assert.Error,
		},
		{
			name: "Valid GPU requests without hints",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationGPUPartitionSpec: `{"allocatePolicy":"Restricted","ringBusBandwidth":"200Gi"}`,
					},
				},
			},
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU: {
					apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
					apiext.ResourceGPUCore:        resource.MustParse("200"),
				},
			},
			gpuHints: nil,
			want: &GPURequirements{
				numberOfGPUs: 2,
				requestsPerGPU: corev1.ResourceList{
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					apiext.ResourceGPUCore:        resource.MustParse("100"),
				},
				gpuShared:              false,
				honorGPUPartition:      true,
				rindBusBandwidth:       &bandWidthOf200Gi,
				restrictedGPUPartition: true,
			},
			wantErr: assert.NoError,
		},
		{
			name: "Valid GPU requests with hints",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						apiext.AnnotationGPUPartitionSpec: `{"allocatePolicy":"Restricted","ringBusBandwidth":"200Gi"}`,
					},
				},
			},
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU: {
					apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
					apiext.ResourceGPUCore:        resource.MustParse("200"),
				},
			},
			gpuHints: &apiext.DeviceHint{
				RequiredTopologyScope: apiext.DeviceTopologyScopeNUMANode,
			},
			want: &GPURequirements{
				numberOfGPUs: 2,
				requestsPerGPU: corev1.ResourceList{
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					apiext.ResourceGPUCore:        resource.MustParse("100"),
				},
				gpuShared:                  false,
				honorGPUPartition:          true,
				rindBusBandwidth:           &bandWidthOf200Gi,
				restrictedGPUPartition:     true,
				requiredTopologyScopeLevel: 2,
				requiredTopologyScope:      apiext.DeviceTopologyScopeNUMANode,
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGPURequirements(tt.pod, tt.podRequests, tt.gpuHints)
			if !tt.wantErr(t, err) {
				return
			}
			if tt.want != nil {
				assert.True(t, quotav1.Equals(tt.want.requestsPerGPU, got.requestsPerGPU))
				tt.want.requestsPerGPU = nil
				got.requestsPerGPU = nil
			}
			assert.Equal(t, tt.want, got)

		})
	}
}
