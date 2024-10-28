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

package loadaware

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

// Test cases description:
// 1. When nodeMetric contains valid AggregatedNodeUsages and aggregatedDuration is nil, it should return the non-empty longest duration resource usage.
// 2. When aggregatedDuration is not nil and matches a duration in AggregatedNodeUsages, it should return the corresponding resource usage.
// 3. When nodeMetric's NodeUsage contains a valid resource list and AggregatedNodeUsages is empty, it should return the resource usage of NodeUsage.

func TestGetTargetAggregatedUsage(t *testing.T) {
	aggregationType := extension.P95
	tests := []struct {
		name               string
		nodeMetric         *slov1alpha1.NodeMetric
		aggregatedDuration *metav1.Duration

		expectedResult *slov1alpha1.ResourceMap
	}{
		{
			name: "Valid AggregatedNodeUsages and aggregatedDuration is nil",
			nodeMetric: &slov1alpha1.NodeMetric{
				Status: slov1alpha1.NodeMetricStatus{
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						AggregatedNodeUsages: []slov1alpha1.AggregatedUsage{
							{
								Duration: metav1.Duration{Duration: 5 * time.Minute},
								Usage: map[extension.AggregationType]slov1alpha1.ResourceMap{
									aggregationType: {
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("30"),
										},
									},
								},
							},
							{
								Duration: metav1.Duration{Duration: 10 * time.Minute},
								Usage: map[extension.AggregationType]slov1alpha1.ResourceMap{
									aggregationType: {
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("50"),
										},
									},
								},
							},
							{
								Duration: metav1.Duration{Duration: 15 * time.Minute},
								Usage: map[extension.AggregationType]slov1alpha1.ResourceMap{
									aggregationType: {
										ResourceList: nil,
									},
								},
							},
						},
					},
				},
			},
			aggregatedDuration: nil,
			expectedResult: &slov1alpha1.ResourceMap{
				ResourceList: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("50"),
				},
			},
		},
		{
			name: "aggregatedDuration is not nil and matches a duration",
			nodeMetric: &slov1alpha1.NodeMetric{
				Status: slov1alpha1.NodeMetricStatus{
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						AggregatedNodeUsages: []slov1alpha1.AggregatedUsage{
							{
								Duration: metav1.Duration{Duration: 5 * time.Minute},
								Usage: map[extension.AggregationType]slov1alpha1.ResourceMap{
									aggregationType: {
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("30"),
										},
									},
								},
							},
							{
								Duration: metav1.Duration{Duration: 10 * time.Minute},
								Usage: map[extension.AggregationType]slov1alpha1.ResourceMap{
									aggregationType: {
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("50"),
										},
									},
								},
							},
							{
								Duration: metav1.Duration{Duration: 15 * time.Minute},
								Usage: map[extension.AggregationType]slov1alpha1.ResourceMap{
									aggregationType: {
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("70"),
										},
									},
								},
							},
						},
					},
				},
			},
			aggregatedDuration: &metav1.Duration{Duration: 5 * time.Minute},
			expectedResult: &slov1alpha1.ResourceMap{
				ResourceList: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("30"),
				},
			},
		},
		{
			name: "NodeUsage contains a valid resource list and AggregatedNodeUsages is empty",
			nodeMetric: &slov1alpha1.NodeMetric{
				Status: slov1alpha1.NodeMetricStatus{
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse("30"),
							},
						},
						AggregatedNodeUsages: []slov1alpha1.AggregatedUsage{
							{
								Duration: metav1.Duration{Duration: 5 * time.Minute},
								Usage: map[extension.AggregationType]slov1alpha1.ResourceMap{
									aggregationType: {
										ResourceList: nil,
									},
								},
							},
						},
					},
				},
			},
			aggregatedDuration: nil,
			expectedResult: &slov1alpha1.ResourceMap{
				ResourceList: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("30"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getTargetAggregatedUsage(tt.nodeMetric, tt.aggregatedDuration, aggregationType)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}
