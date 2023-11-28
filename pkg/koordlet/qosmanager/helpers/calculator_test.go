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

package helpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

func TestCalculateFilterPodsUsed(t *testing.T) {
	type args struct {
		nodeUsed        float64
		nodeReserved    float64
		podMetas        []*statesinformer.PodMeta
		podUsedMap      map[string]float64
		hostApps        []slov1alpha1.HostApplicationSpec
		hostAppMetrics  map[string]float64
		podFilterFn     func(*corev1.Pod) bool
		hostAppFilterFn func(*slov1alpha1.HostApplicationSpec) bool
	}
	tests := []struct {
		name  string
		args  args
		want  float64
		want1 float64
		want2 float64
	}{
		{
			name: "no pod to calculate",
			args: args{
				nodeUsed:        10.0,
				nodeReserved:    0,
				podMetas:        nil,
				podUsedMap:      nil,
				hostApps:        nil,
				hostAppMetrics:  nil,
				podFilterFn:     NonePodHighPriority,
				hostAppFilterFn: NoneHostAppHighPriority,
			},
			want:  0,
			want1: 0,
			want2: 10.0,
		},
		{
			name: "calculate for non-BE",
			args: args{
				nodeUsed:     80.0,
				nodeReserved: 8.0,
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "ls-pod",
								UID:  "xxxxxx",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSBurstable,
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "be-pod",
								UID:  "yyyyyy",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSBestEffort,
							},
						},
					},
				},
				podUsedMap: map[string]float64{
					"xxxxxx": 20.0,
					"yyyyyy": 32.0,
				},
				hostApps: []slov1alpha1.HostApplicationSpec{
					{
						Name:     "ls-app",
						Priority: extension.PriorityProd,
						QoS:      extension.QoSLS,
					},
					{
						Name:     "be-app",
						Priority: extension.PriorityBatch,
						QoS:      extension.QoSBE,
						CgroupPath: &slov1alpha1.CgroupPath{
							Base: slov1alpha1.CgroupBaseTypeKubeBesteffort,
						},
					},
				},
				hostAppMetrics: map[string]float64{
					"ls-app": 4.0,
					"be-app": 8.0,
				},
				podFilterFn:     NonBEPodFilter,
				hostAppFilterFn: NonBEHostAppFilter,
			},
			want:  20.0,
			want1: 4.0,
			want2: 16.0,
		},
		{
			name: "calculate for non-Batch",
			args: args{
				nodeUsed:     80.0,
				nodeReserved: 8.0,
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "ls-pod",
								UID:  "xxxxxx",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSBurstable,
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "be-pod",
								UID:  "yyyyyy",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Status: corev1.PodStatus{
								QOSClass: corev1.PodQOSBestEffort,
							},
						},
					},
				},
				podUsedMap: map[string]float64{
					"xxxxxx": 20.0,
					"yyyyyy": 32.0,
				},
				hostApps:        nil,
				hostAppMetrics:  nil,
				podFilterFn:     NotBatchOrFreePodFilter,
				hostAppFilterFn: NoneHostAppHighPriority,
			},
			want:  20.0,
			want1: 0,
			want2: 28.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, got2 := CalculateFilterPodsUsed(tt.args.nodeUsed, tt.args.nodeReserved, tt.args.podMetas,
				tt.args.podUsedMap, tt.args.hostApps, tt.args.hostAppMetrics, tt.args.podFilterFn, tt.args.hostAppFilterFn)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
			assert.Equal(t, tt.want2, got2)
		})
	}
}
