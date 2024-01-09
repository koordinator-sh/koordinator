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

package prom

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

func TestDecoder(t *testing.T) {
	testNow := model.Now()
	testOneHourAgo := testNow.Add(-time.Hour)
	testNodeMetricResource := metriccache.GenMetricResource("node_load_1m")
	testMetricSample, err := testNodeMetricResource.GenerateSample(nil, testNow.Time(), 20)
	assert.NoError(t, err)
	testQoSMetricResource := metriccache.GenMetricResource("node_qos_load_1m", metriccache.MetricPropertyQoS)
	testMetricSample1, err := testQoSMetricResource.GenerateSample(metriccache.MetricPropertiesFunc.QoS("BE"), testNow.Time(), 20)
	assert.NoError(t, err)
	testMetricSample2, err := testQoSMetricResource.GenerateSample(metriccache.MetricPropertiesFunc.QoS("LS"), testNow.Time(), 10)
	assert.NoError(t, err)
	testPodMetricResource := metriccache.GenMetricResource("pod_load_1m", metriccache.MetricPropertyPodUID)
	testMetricSample3, err := testPodMetricResource.GenerateSample(metriccache.MetricPropertiesFunc.Pod("uid-xxxxxx"), testNow.Time(), 20)
	assert.NoError(t, err)
	testMetricSample4, err := testPodMetricResource.GenerateSample(metriccache.MetricPropertiesFunc.Pod("uid-yyyyyy"), testNow.Time(), 10)
	assert.NoError(t, err)
	testContainerMetricResource := metriccache.GenMetricResource("container_load_1m", metriccache.MetricPropertyContainerID)
	testMetricSample5, err := testContainerMetricResource.GenerateSample(metriccache.MetricPropertiesFunc.Container("containerd://aaa"), testNow.Time(), 20)
	assert.NoError(t, err)
	testMetricSample6, err := testContainerMetricResource.GenerateSample(metriccache.MetricPropertiesFunc.Container("containerd://bbb"), testNow.Time(), 10)
	assert.NoError(t, err)
	type args struct {
		samples  []*model.Sample
		node     *corev1.Node
		podMetas []*statesinformer.PodMeta
	}
	tests := []struct {
		name    string
		field   []*DecodeConfig
		args    args
		want    []metriccache.MetricSample
		wantErr bool
	}{
		{
			name:    "nothing to decode",
			wantErr: false,
		},
		{
			name: "decode node metrics",
			field: []*DecodeConfig{
				{
					TargetMetric:   "node_load_1m",
					TargetType:     MetricTypeNode,
					SourceMetric:   "node_load_1m",
					ValidTimeRange: 10 * time.Second,
				},
			},
			args: args{
				samples: []*model.Sample{
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_1m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(20),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_5m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				testMetricSample,
			},
			wantErr: false,
		},
		{
			name: "decode and filter expired node metrics",
			field: []*DecodeConfig{
				{
					TargetMetric:   "node_load_1m",
					TargetType:     MetricTypeNode,
					SourceMetric:   "node_load_1m",
					ValidTimeRange: 15 * time.Second,
				},
			},
			args: args{
				samples: []*model.Sample{
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_1m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(20),
						Timestamp: testOneHourAgo,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_5m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(10),
						Timestamp: testOneHourAgo,
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
					},
				},
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "decode and filter qos metrics",
			field: []*DecodeConfig{
				{
					TargetMetric:   "node_qos_load_1m",
					TargetType:     MetricTypeQoS,
					SourceMetric:   "node_qos_load_1m",
					ValidTimeRange: 15 * time.Second,
				},
			},
			args: args{
				samples: []*model.Sample{
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_1m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_qos_load_1m",
							"node":                "test-node",
							"qos":                 "BE",
						},
						Value:     model.SampleValue(20),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_qos_load_1m",
							"node":                "test-node",
							"qos":                 "LS",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_qos_load_1m",
							"node":                "test-node",
							"qos":                 "LSR",
						},
						Value:     model.SampleValue(30),
						Timestamp: testOneHourAgo,
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				testMetricSample1,
				testMetricSample2,
			},
			wantErr: false,
		},
		{
			name: "decode and filter pod metrics",
			field: []*DecodeConfig{
				{
					TargetMetric: "pod_load_1m",
					TargetType:   MetricTypePod,
					TargetLabels: model.LabelSet{
						"pod":       "pod_name",
						"namespace": "pod_namespace",
					},
					SourceMetric:   "pod_cpu_load_1m",
					ValidTimeRange: 10 * time.Second,
				},
			},
			args: args{
				samples: []*model.Sample{
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_1m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_cpu_load_1m",
							"node":                "test-node",
							"qos":                 "BE",
							"pod_namespace":       "test-ns",
							"pod_name":            "test-be-pod",
						},
						Value:     model.SampleValue(20),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_cpu_load_1m",
							"node":                "test-node",
							"qos":                 "LS",
							"pod_namespace":       "test-ns",
							"pod_name":            "test-ls-pod",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_cpu_load_1m",
							"node":                "test-node",
							"qos":                 "LSR",
							"pod_namespace":       "test-ns",
							"pod_name":            "test-lsr-pod",
						},
						Value:     model.SampleValue(30),
						Timestamp: testOneHourAgo,
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-be-pod",
								Namespace: "test-ns",
								UID:       "uid-xxxxxx",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-ls-pod",
								Namespace: "test-ns",
								UID:       "uid-yyyyyy",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				testMetricSample3,
				testMetricSample4,
			},
			wantErr: false,
		},
		{
			name: "decode and filter pod uid metrics",
			field: []*DecodeConfig{
				{
					TargetMetric:   "pod_load_1m",
					TargetType:     MetricTypePodUID,
					SourceMetric:   "pod_cpu_load_1m",
					ValidTimeRange: 10 * time.Second,
				},
			},
			args: args{
				samples: []*model.Sample{
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_1m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_cpu_load_1m",
							"node":                "test-node",
							"qos":                 "BE",
							"pod_namespace":       "test-ns",
							"pod_name":            "test-be-pod",
							"uid":                 "uid-xxxxxx",
						},
						Value:     model.SampleValue(20),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_cpu_load_1m",
							"node":                "test-node",
							"qos":                 "LS",
							"pod_namespace":       "test-ns",
							"pod_name":            "test-ls-pod",
							"uid":                 "uid-yyyyyy",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_cpu_load_1m",
							"node":                "test-node",
							"qos":                 "LSR",
							"pod_namespace":       "test-ns",
							"pod_name":            "test-lsr-pod",
							"uid":                 "uid-zzzzzz",
						},
						Value:     model.SampleValue(30),
						Timestamp: testOneHourAgo,
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-be-pod",
								Namespace: "test-ns",
								UID:       "uid-xxxxxx",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-ls-pod",
								Namespace: "test-ns",
								UID:       "uid-yyyyyy",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				testMetricSample3,
				testMetricSample4,
			},
			wantErr: false,
		},
		{
			name: "decode and filter pod uid metrics 1",
			field: []*DecodeConfig{
				{
					TargetMetric: "pod_load_1m",
					TargetType:   MetricTypePodUID,
					TargetLabels: model.LabelSet{
						"uid": "pod_uid",
					},
					SourceMetric:   "pod_cpu_load_1m",
					ValidTimeRange: 10 * time.Second,
				},
			},
			args: args{
				samples: []*model.Sample{
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_1m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_cpu_load_1m",
							"node":                "test-node",
							"qos":                 "BE",
							"pod_namespace":       "test-ns",
							"pod_name":            "test-be-pod",
							"pod_uid":             "uid-xxxxxx",
						},
						Value:     model.SampleValue(20),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_cpu_load_1m",
							"node":                "test-node",
							"qos":                 "LS",
							"pod_namespace":       "test-ns",
							"pod_name":            "test-ls-pod",
							"pod_uid":             "uid-yyyyyy",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_cpu_load_1m",
							"node":                "test-node",
							"qos":                 "LSR",
							"pod_namespace":       "test-ns",
							"pod_name":            "test-lsr-pod",
							"pod_uid":             "uid-zzzzzz",
						},
						Value:     model.SampleValue(30),
						Timestamp: testOneHourAgo,
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-be-pod",
								Namespace: "test-ns",
								UID:       "uid-xxxxxx",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-ls-pod",
								Namespace: "test-ns",
								UID:       "uid-yyyyyy",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				testMetricSample3,
				testMetricSample4,
			},
			wantErr: false,
		},
		{
			name: "decode and filter container metrics",
			field: []*DecodeConfig{
				{
					TargetMetric: "container_load_1m",
					TargetType:   MetricTypeContainer,
					TargetLabels: model.LabelSet{
						"container": "container_name",
					},
					SourceMetric:   "container_load_1m",
					ValidTimeRange: 10 * time.Second,
				},
			},
			args: args{
				samples: []*model.Sample{
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_1m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "container_load_1m",
							"node":                "test-node",
							"qos":                 "BE",
							"namespace":           "test-ns",
							"pod":                 "test-be-pod",
							"uid":                 "uid-xxxxxx",
							"container_name":      "test-be-container",
						},
						Value:     model.SampleValue(20),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "container_load_1m",
							"node":                "test-node",
							"qos":                 "LS",
							"namespace":           "test-ns",
							"pod":                 "test-ls-pod",
							"uid":                 "uid-yyyyyy",
							"container_name":      "test-ls-container",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "container_load_1m",
							"node":                "test-node",
							"qos":                 "LSR",
							"namespace":           "test-ns",
							"pod":                 "test-lsr-pod",
							"uid":                 "uid-zzzzzz",
							"container_name":      "test-lsr-container",
						},
						Value:     model.SampleValue(30),
						Timestamp: testOneHourAgo,
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-be-pod",
								Namespace: "test-ns",
								UID:       "uid-xxxxxx",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Status: corev1.PodStatus{
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-be-container",
										ContainerID: "containerd://aaa",
									},
								},
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-ls-pod",
								Namespace: "test-ns",
								UID:       "uid-yyyyyy",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Status: corev1.PodStatus{
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-ls-container",
										ContainerID: "containerd://bbb",
									},
								},
							},
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				testMetricSample5,
				testMetricSample6,
			},
			wantErr: false,
		},
		{
			name: "decode and filter container id metrics",
			field: []*DecodeConfig{
				{
					TargetMetric: "container_load_1m",
					TargetType:   MetricTypeContainerID,
					TargetLabels: model.LabelSet{
						"container_id": "id",
					},
					SourceMetric:   "container_load_1m",
					ValidTimeRange: 10 * time.Second,
				},
			},
			args: args{
				samples: []*model.Sample{
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_1m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "container_load_1m",
							"node":                "test-node",
							"qos":                 "BE",
							"namespace":           "test-ns",
							"pod":                 "test-be-pod",
							"uid":                 "uid-xxxxxx",
							"id":                  "containerd://aaa",
						},
						Value:     model.SampleValue(20),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "container_load_1m",
							"node":                "test-node",
							"qos":                 "LS",
							"namespace":           "test-ns",
							"pod":                 "test-ls-pod",
							"uid":                 "uid-yyyyyy",
							"id":                  "containerd://bbb",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "container_load_1m",
							"node":                "test-node",
							"qos":                 "LSR",
							"namespace":           "test-ns",
							"pod":                 "test-lsr-pod",
							"uid":                 "uid-zzzzzz",
							"id":                  "containerd://ccc",
						},
						Value:     model.SampleValue(30),
						Timestamp: testOneHourAgo,
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-be-pod",
								Namespace: "test-ns",
								UID:       "uid-xxxxxx",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Status: corev1.PodStatus{
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-be-container",
										ContainerID: "containerd://aaa",
									},
								},
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-ls-pod",
								Namespace: "test-ns",
								UID:       "uid-yyyyyy",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Status: corev1.PodStatus{
								ContainerStatuses: []corev1.ContainerStatus{
									{
										Name:        "test-ls-container",
										ContainerID: "containerd://bbb",
									},
								},
							},
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				testMetricSample5,
				testMetricSample6,
			},
			wantErr: false,
		},
		{
			name: "multi decoder for node and pod metrics",
			field: []*DecodeConfig{
				{
					TargetMetric:   "node_load_1m",
					TargetType:     MetricTypeNode,
					SourceMetric:   "node_load_1m",
					ValidTimeRange: 15 * time.Second,
				},
				{
					TargetMetric: "pod_load_1m",
					TargetType:   MetricTypePod,
					SourceMetric: "pod_load_1m",
					SourceLabels: model.LabelSet{
						"namespace": "test-ns",
					},
					ValidTimeRange: 10 * time.Second,
				},
			},
			args: args{
				samples: []*model.Sample{
					{
						Metric: model.Metric{
							model.MetricNameLabel: "node_load_1m",
							"node":                "test-node",
						},
						Value:     model.SampleValue(20),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_load_1m",
							"node":                "test-node",
							"qos":                 "BE",
							"namespace":           "test-ns",
							"pod":                 "test-be-pod",
							"uid":                 "uid-xxxxxx",
						},
						Value:     model.SampleValue(20),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_load_1m",
							"node":                "test-node",
							"qos":                 "LS",
							"namespace":           "test-ns",
							"pod":                 "test-ls-pod",
							"uid":                 "uid-yyyyyy",
						},
						Value:     model.SampleValue(10),
						Timestamp: testNow,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_load_1m",
							"node":                "test-node",
							"qos":                 "LSR",
							"namespace":           "test-ns",
							"pod":                 "test-lsr-pod",
							"uid":                 "uid-zzzzzz",
						},
						Value:     model.SampleValue(10),
						Timestamp: testOneHourAgo,
					},
					{
						Metric: model.Metric{
							model.MetricNameLabel: "pod_load_1m",
							"node":                "test-node",
							"namespace":           "test-ns-1",
							"pod":                 "test-ls-pod-1",
							"uid":                 "uid-mmmmmm",
						},
						Value:     model.SampleValue(0),
						Timestamp: testNow,
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
					},
				},
				podMetas: []*statesinformer.PodMeta{
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-be-pod",
								Namespace: "test-ns",
								UID:       "uid-xxxxxx",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-ls-pod",
								Namespace: "test-ns",
								UID:       "uid-yyyyyy",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
						},
					},
					{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-ls-pod-1",
								Namespace: "test-ns-1",
								UID:       "uid-mmmmmm",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				testMetricSample,
				testMetricSample3,
				testMetricSample4,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewMultiDecoder(tt.field...)
			got, gotErr := d.Decode(tt.args.samples, tt.args.node, tt.args.podMetas)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assertEqualMetricSamplesOutOfOrder(t, tt.want, got)
		})
	}
}

func TestScraper(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		testRespBytes := `# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 50
# HELP go_threads Number of OS threads created.
# TYPE go_threads gauge
go_threads 30
# HELP process_cpu_seconds_total Total user and system CPU time spent in seconds.
# TYPE process_cpu_seconds_total counter
process_cpu_seconds_total 10000.00
`
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(testRespBytes))
		}))
		sURL, err := url.Parse(s.URL)
		assert.NoError(t, err)
		port, err := strconv.Atoi(sURL.Port())
		assert.NoError(t, err)
		testNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node",
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100"),
					corev1.ResourceMemory: resource.MustParse("400Gi"),
				},
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100"),
					corev1.ResourceMemory: resource.MustParse("400Gi"),
				},
			},
		}
		testPodMetas := []*statesinformer.PodMeta{
			{
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-be-pod",
						Namespace: "test-ns",
						UID:       "uid-xxxxxx",
						Labels: map[string]string{
							extension.LabelPodQoS: string(extension.QoSBE),
						},
					},
				},
			},
			{
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ls-pod",
						Namespace: "test-ns",
						UID:       "uid-yyyyyy",
						Labels: map[string]string{
							extension.LabelPodQoS: string(extension.QoSLS),
						},
					},
				},
			},
		}

		cfg := &ScraperConfig{
			JobName:        "test-job",
			Enable:         true,
			ScrapeInterval: 10 * time.Second,
			Client: &ClientConfig{
				Timeout:     3 * time.Second,
				InsecureTLS: true,
				Address:     sURL.Hostname(),
				Port:        port,
				Path:        sURL.Path,
			},
			Decoders: []*DecodeConfig{
				{
					TargetMetric:   "go_goroutines",
					TargetType:     MetricTypeNode,
					SourceMetric:   "go_goroutines",
					ValidTimeRange: 5 * time.Second,
				},
				{
					TargetMetric:   "go_threads",
					TargetType:     MetricTypeNode,
					SourceMetric:   "go_threads",
					ValidTimeRange: 5 * time.Second,
				},
				{
					TargetMetric:   "process_cpu_seconds_total",
					TargetType:     MetricTypeNode,
					SourceMetric:   "process_cpu_seconds_total",
					ValidTimeRange: 5 * time.Second,
				},
			},
		}
		scraper, err := NewGenericScraper(cfg)
		assert.NoError(t, err)
		samples, err := scraper.GetMetricSamples(testNode, testPodMetas)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(samples))

		cfg.Decoders = nil
		scraper, err = NewGenericScraper(cfg)
		assert.Error(t, err)
		assert.Nil(t, scraper)
	})
}

func assertEqualMetricSamplesOutOfOrder(t *testing.T, want, got []metriccache.MetricSample) {
	assert.Equal(t, len(want), len(got))
	for i := range want {
		found := false
		for j := range got {
			if reflect.DeepEqual(want[i], got[j]) {
				found = true
				break
			}
		}
		assert.True(t, found, "metric sample %+v not found", want[i])
	}
}
