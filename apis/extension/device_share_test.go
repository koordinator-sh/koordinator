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

package extension

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

var (
	bandWidthOf200Gi = resource.MustParse("200Gi")
)

func Test_GetDeviceAllocations(t *testing.T) {
	tests := []struct {
		name    string
		pod     *corev1.Pod
		want    DeviceAllocations
		wantErr bool
	}{
		{
			name: "nil annotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
			},
		},
		{
			name: "incorrect annotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
					Annotations: map[string]string{
						AnnotationDeviceAllocated: "incorrect-device-allocation",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "correct annotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
					Annotations: map[string]string{
						AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"16Gi","koordinator.sh/gpu-memory-ratio":"100"}}]}`,
					},
				},
			},
			want: DeviceAllocations{
				schedulingv1alpha1.GPU: []*DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							ResourceGPUCore:        resource.MustParse("100"),
							ResourceGPUMemoryRatio: resource.MustParse("100"),
							ResourceGPUMemory:      resource.MustParse("16Gi"),
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "correct annotations with busID",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
					Annotations: map[string]string{
						AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"id": "0000:08.00.0","resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"16Gi","koordinator.sh/gpu-memory-ratio":"100"}}]}`,
					},
				},
			},
			want: DeviceAllocations{
				schedulingv1alpha1.GPU: []*DeviceAllocation{
					{
						Minor: 1,
						ID:    "0000:08.00.0",
						Resources: corev1.ResourceList{
							ResourceGPUCore:        resource.MustParse("100"),
							ResourceGPUMemoryRatio: resource.MustParse("100"),
							ResourceGPUMemory:      resource.MustParse("16Gi"),
						},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetDeviceAllocations(tt.pod.Annotations)
			assert.Equal(t, tt.wantErr, err != nil)
			if !tt.wantErr {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_SetDeviceAllocations(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		allocations    DeviceAllocations
		wantAnnotation string
		wantErr        bool
	}{
		{
			name: "valid allocations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       "123456789",
					Namespace: "default",
					Name:      "test",
				},
			},
			allocations: DeviceAllocations{
				schedulingv1alpha1.GPU: []*DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							ResourceGPUCore:        resource.MustParse("100"),
							ResourceGPUMemoryRatio: resource.MustParse("100"),
							ResourceGPUMemory:      resource.MustParse("16Gi"),
						},
					},
				},
			},
			wantAnnotation: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"16Gi","koordinator.sh/gpu-memory-ratio":"100"}}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SetDeviceAllocations(tt.pod, tt.allocations)
			assert.Equal(t, tt.wantErr, err != nil)
			if !tt.wantErr {
				assert.Equal(t, tt.wantAnnotation, tt.pod.Annotations[AnnotationDeviceAllocated])
			}
		})
	}
}

func TestGetGPUPartitionSpec(t *testing.T) {
	type args struct {
		annotations map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    *GPUPartitionSpec
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "nil partitionSpec",
			args: args{
				annotations: nil,
			},
			want:    nil,
			wantErr: assert.NoError,
		},
		{
			name: "empty partitionSpec",
			args: args{
				annotations: map[string]string{
					AnnotationGPUPartitionSpec: `{}`,
				},
			},
			want: &GPUPartitionSpec{
				AllocatePolicy: GPUPartitionAllocatePolicyBestEffort,
			},
			wantErr: assert.NoError,
		},
		{
			name: "allocatePolicy BestEffort",
			args: args{
				annotations: map[string]string{
					AnnotationGPUPartitionSpec: `{"allocatePolicy":"BestEffort"}`,
				},
			},
			want: &GPUPartitionSpec{
				AllocatePolicy: GPUPartitionAllocatePolicyBestEffort,
			},
			wantErr: assert.NoError,
		},
		{
			name: "allocatePolicy Restricted",
			args: args{
				annotations: map[string]string{
					AnnotationGPUPartitionSpec: `{"allocatePolicy":"Restricted"}`,
				},
			},
			want: &GPUPartitionSpec{
				AllocatePolicy: GPUPartitionAllocatePolicyRestricted,
			},
			wantErr: assert.NoError,
		},
		{
			name: "allocatePolicy Restricted, ringAllReduceBandwidth specified",
			args: args{
				annotations: map[string]string{
					AnnotationGPUPartitionSpec: `{"allocatePolicy":"Restricted", "ringBusBandwidth":"200Gi"}`,
				},
			},
			want: &GPUPartitionSpec{
				AllocatePolicy:   GPUPartitionAllocatePolicyRestricted,
				RingBusBandwidth: &bandWidthOf200Gi,
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetGPUPartitionSpec(tt.args.annotations)
			if !tt.wantErr(t, err, fmt.Sprintf("GetGPUPartitionSpec(%v)", tt.args.annotations)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetGPUPartitionSpec(%v)", tt.args.annotations)
		})
	}
}

func TestGetGPUPartitionTable(t *testing.T) {
	tests := []struct {
		name    string
		device  *schedulingv1alpha1.Device
		want    GPUPartitionTable
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "Valid GPU Partition Table",
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationGPUPartitions: `{"0": [{"minors": [0,1], "gpuLinkType": "NVLink","ringBusBandwidth": "200Gi", "allocationScore": 10}]}`,
					},
				},
			},
			want: GPUPartitionTable{
				0: []GPUPartition{
					{
						Minors:           []int{0, 1},
						GPULinkType:      GPUNVLink,
						RingBusBandwidth: &bandWidthOf200Gi,
						AllocationScore:  10,
						MinorsHash:       0, // This would be calculated.
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name:    "No Annotation",
			device:  &schedulingv1alpha1.Device{},
			want:    nil,
			wantErr: assert.NoError,
		},
		{
			name: "Invalid JSON",
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationGPUPartitions: `Invalid JSON format`,
					},
				},
			},
			want:    nil,
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetGPUPartitionTable(tt.device)
			if !tt.wantErr(t, err, fmt.Sprintf("GetGPUPartitionTable(%v)", tt.device)) {
				return
			}
			assert.Equalf(t, tt.want, got, "GetGPUPartitionTable(%v)", tt.device)
		})
	}
}

// TestGetNodeGPUAllocatePolicy tests the GetGPUPartitionPolicy function.
func TestGetNodeLevelGPUAllocatePolicy(t *testing.T) {
	tests := []struct {
		name     string
		node     *schedulingv1alpha1.Device
		expected GPUPartitionPolicy
	}{
		{
			name: "Node with Honor policy",
			node: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelGPUPartitionPolicy: string(GPUPartitionPolicyHonor),
					},
				},
			},
			expected: GPUPartitionPolicyHonor,
		},
		{
			name: "Node with Prefer policy",
			node: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelGPUPartitionPolicy: string(GPUPartitionPolicyPrefer),
					},
				},
			},
			expected: GPUPartitionPolicyPrefer,
		},
		{
			name: "Node without policy label",
			node: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			},
			expected: GPUPartitionPolicyPrefer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetGPUPartitionPolicy(tt.node); got != tt.expected {
				t.Errorf("GetGPUPartitionPolicy() = %v, want %v", got, tt.expected)
			}
		})
	}
}
