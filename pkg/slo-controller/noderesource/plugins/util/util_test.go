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

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func makeResourceList(cpu, memory string) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(memory),
	}
}

func makeResourceReq(cpu, memory string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: makeResourceList(cpu, memory),
		Limits:   makeResourceList(cpu, memory),
	}
}

func Test_getPodMetricUsage(t *testing.T) {
	type args struct {
		info *slov1alpha1.PodMetricInfo
	}
	tests := []struct {
		name string
		args args
		want corev1.ResourceList
	}{
		{
			name: "get correct scaled resource quantity",
			args: args{
				info: &slov1alpha1.PodMetricInfo{
					PodUsage: slov1alpha1.ResourceMap{
						ResourceList: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("10Gi"),
							"unknown_resource":    resource.MustParse("1"),
						},
					},
				},
			},
			want: makeResourceList("4", "10Gi"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPodMetricUsage(tt.args.info)
			testingCorrectResourceList(t, &tt.want, &got)
		})
	}
}

func Test_getResourceListForCPUAndMemory(t *testing.T) {
	type args struct {
		rl corev1.ResourceList
	}
	tests := []struct {
		name string
		args args
		want corev1.ResourceList
	}{
		{
			name: "get correct scaled resource quantity",
			args: args{
				rl: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("40"),
					corev1.ResourceMemory: resource.MustParse("80Gi"),
					"unknown_resource":    resource.MustParse("10"),
				},
			},
			want: makeResourceList("40", "80Gi"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetResourceListForCPUAndMemory(tt.args.rl)
			testingCorrectResourceList(t, &tt.want, &got)
		})
	}
}

func Test_getNodeReservation(t *testing.T) {
	type args struct {
		strategy        *configuration.ColocationStrategy
		nodeAllocatable corev1.ResourceList
	}
	tests := []struct {
		name string
		args args
		want corev1.ResourceList
	}{
		{
			name: "get correct reserved node resource quantity",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				nodeAllocatable: makeResourceList("100", "100Gi"),
			},
			want: makeResourceList("35", "35Gi"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetNodeSafetyMargin(tt.args.strategy, tt.args.nodeAllocatable)
			testingCorrectResourceList(t, &tt.want, &got)
		})
	}
}

func Test_getPodNUMARequestAndUsage(t *testing.T) {
	type args struct {
		pod        *corev1.Pod
		podRequest corev1.ResourceList
		podUsage   corev1.ResourceList
		numaNum    int
	}
	tests := []struct {
		name  string
		args  args
		want  []corev1.ResourceList // request
		want1 []corev1.ResourceList // usage
	}{
		{
			name: "pod has no alloc status should have averaged result",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: makeResourceReq("4", "8Gi"),
							},
						},
					},
				},
				podRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				podUsage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1500m"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				numaNum: 2,
			},
			want: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
			want1: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("750m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("750m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
		{
			name: "pod has no alloc status should have averaged result 1",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: makeResourceReq("1", "4Gi"),
							},
							{
								Resources: makeResourceReq("1", "4Gi"),
							},
						},
					},
				},
				podRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				podUsage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("0"),
					corev1.ResourceMemory: resource.MustParse("0"),
				},
				numaNum: 1,
			},
			want: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
			want1: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("0"),
					corev1.ResourceMemory: resource.MustParse("0"),
				},
			},
		},
		{
			name: "pod allocate 2 NUMA nodes on 2 total nodes",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
						Annotations: map[string]string{
							extension.AnnotationResourceStatus: `{"numaNodeResources": [{"node": 0}, {"node": 1}]}`,
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: makeResourceReq("4", "8Gi"),
							},
						},
					},
				},
				podRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				podUsage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1500m"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				numaNum: 2,
			},
			want: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
			want1: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("750m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("750m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
		{
			name: "pod allocate 1 NUMA nodes on 4 total nodes",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
						Annotations: map[string]string{
							extension.AnnotationResourceStatus: `{"cpuset": "35-36,67-68", "numaNodeResources": [{"node": 1}]}`,
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: makeResourceReq("4", "8Gi"),
							},
						},
					},
				},
				podRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				podUsage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1500m"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				numaNum: 4,
			},
			want: []corev1.ResourceList{
				util.NewZeroResourceList(),
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				util.NewZeroResourceList(),
				util.NewZeroResourceList(),
			},
			want1: []corev1.ResourceList{
				util.NewZeroResourceList(),
				{
					corev1.ResourceCPU:    resource.MustParse("1500m"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				util.NewZeroResourceList(),
				util.NewZeroResourceList(),
			},
		},
		{
			name: "pod allocate 2 NUMA nodes on 4 total nodes",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
						Annotations: map[string]string{
							extension.AnnotationResourceStatus: `{"cpuset": "35-36,67-68", "numaNodeResources": [{"node": 1}, {"node": 3}]}`,
						},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: makeResourceReq("4", "8Gi"),
							},
						},
					},
				},
				podRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				podUsage: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1500m"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				numaNum: 4,
			},
			want: []corev1.ResourceList{
				util.NewZeroResourceList(),
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				util.NewZeroResourceList(),
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
			want1: []corev1.ResourceList{
				util.NewZeroResourceList(),
				{
					corev1.ResourceCPU:    resource.MustParse("750m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
				util.NewZeroResourceList(),
				{
					corev1.ResourceCPU:    resource.MustParse("750m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := GetPodNUMARequestAndUsage(tt.args.pod, tt.args.podRequest, tt.args.podUsage, tt.args.numaNum)
			assertEqualNUMAResourceList(t, tt.want, got)
			assertEqualNUMAResourceList(t, tt.want1, got1)
		})
	}
}

func Test_addZoneResourceList(t *testing.T) {
	type args struct {
		a       []corev1.ResourceList
		b       []corev1.ResourceList
		zoneNum int
	}
	tests := []struct {
		name string
		args args
		want []corev1.ResourceList
	}{
		{
			name: "add zero",
			args: args{
				a: []corev1.ResourceList{
					{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
				b: []corev1.ResourceList{
					{},
					{},
				},
				zoneNum: 2,
			},
			want: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
		{
			name: "add zero 1",
			args: args{
				a: []corev1.ResourceList{
					util.NewZeroResourceList(),
					util.NewZeroResourceList(),
				},
				b: []corev1.ResourceList{
					{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
				zoneNum: 2,
			},
			want: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
		{
			name: "normal add",
			args: args{
				a: []corev1.ResourceList{
					{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					util.NewZeroResourceList(),
					{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
					util.NewZeroResourceList(),
				},
				b: []corev1.ResourceList{
					{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
					{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
					{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
					{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
				zoneNum: 4,
			},
			want: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("3"),
					corev1.ResourceMemory: resource.MustParse("10Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("3"),
					corev1.ResourceMemory: resource.MustParse("10Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AddZoneResourceList(tt.args.a, tt.args.b, tt.args.zoneNum)
			assertEqualNUMAResourceList(t, tt.want, got)
		})
	}
}

func Test_getPodUnknownNUMAUsage(t *testing.T) {
	tests := []struct {
		name string
		arg  corev1.ResourceList
		arg1 int
		want []corev1.ResourceList
	}{
		{
			name: "no NUMA node resources",
			arg: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1000m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			arg1: 0,
			want: nil,
		},
		{
			name: "average on one NUMA",
			arg: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			arg1: 1,
			want: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
		},
		{
			name: "average on multiple NUMAs",
			arg: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
			arg1: 4,
			want: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPodUnknownNUMAUsage(tt.arg, tt.arg1)
			assertEqualNUMAResourceList(t, tt.want, got)
		})
	}
}

func Test_divideResourceList(t *testing.T) {
	tests := []struct {
		name string
		arg  corev1.ResourceList
		arg1 float64
		want corev1.ResourceList
	}{
		{
			name: "do not panic for divide by zero",
			arg: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1000m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			arg1: 0,
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1000m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		},
		{
			name: "normal case",
			arg: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1000m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			arg1: 2,
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		},
		{
			name: "normal case 1",
			arg: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1000m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
			arg1: 1,
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1000m"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		},
		{
			name: "normal case 2",
			arg: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2000m"),
				corev1.ResourceMemory: resource.MustParse("6Gi"),
			},
			arg1: 3,
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("667m"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DivideResourceList(tt.arg, tt.arg1)
			assert.True(t, util.IsResourceListEqual(tt.want, got))
		})
	}
}

func assertEqualNUMAResourceList(t *testing.T, want, got []corev1.ResourceList) {
	assert.Equal(t, len(want), len(got))
	for i := range want {
		assert.True(t, util.IsResourceListEqual(want[i], got[i]))
	}
}

func Test_getHostAppMetricUsage(t *testing.T) {
	type args struct {
		info *slov1alpha1.HostApplicationMetricInfo
	}
	tests := []struct {
		name string
		args args
		want corev1.ResourceList
	}{
		{
			name: "get correct scaled resource quantity",
			args: args{
				info: &slov1alpha1.HostApplicationMetricInfo{
					Usage: slov1alpha1.ResourceMap{
						ResourceList: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("10Gi"),
							"unknown_resource":    resource.MustParse("1"),
						},
					},
				},
			},
			want: makeResourceList("4", "10Gi"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetHostAppMetricUsage(tt.args.info)
			testingCorrectResourceList(t, &tt.want, &got)
		})
	}
}

func testingCorrectResourceList(t *testing.T, want, got *corev1.ResourceList) {
	assert.Equal(t, want.Cpu().MilliValue(), got.Cpu().MilliValue(), "should get correct cpu request")
	assert.Equal(t, want.Memory().Value(), got.Memory().Value(), "should get correct memory request")
	if _, ok := (*want)[extension.BatchCPU]; ok {
		qWant, qGot := (*want)[extension.BatchCPU], (*got)[extension.BatchCPU]
		assert.Equal(t, qWant.MilliValue(), qGot.MilliValue(), "should get correct batch-cpu")
	}
	if _, ok := (*want)[extension.BatchMemory]; ok {
		qWant, qGot := (*want)[extension.BatchMemory], (*got)[extension.BatchMemory]
		assert.Equal(t, qWant.Value(), qGot.Value(), "should get correct batch-memory")
	}
}
