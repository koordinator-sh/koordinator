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

package v1alpha2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func TestSetDefaults_LowNodeLoadArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     *LowNodeLoadArgs
		expected *LowNodeLoadArgs
	}{
		{
			name: "set nodeFit",
			args: &LowNodeLoadArgs{
				NodeFit: pointer.Bool(false),
			},
			expected: &LowNodeLoadArgs{
				NodeFit:                     pointer.Bool(false),
				NodeMetricExpirationSeconds: pointer.Int64(defaultNodeMetricExpirationSeconds),
				AnomalyCondition:            defaultLoadAnomalyCondition,
				DetectorCacheTimeout:        &metav1.Duration{Duration: 5 * time.Minute},
				ResourceWeights: map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    1,
					corev1.ResourceMemory: 1,
				},
			},
		},
		{
			name: "set detectorCacheTimeout",
			args: &LowNodeLoadArgs{
				DetectorCacheTimeout: &metav1.Duration{Duration: 10 * time.Minute},
			},
			expected: &LowNodeLoadArgs{
				NodeFit:                     pointer.Bool(true),
				NodeMetricExpirationSeconds: pointer.Int64(defaultNodeMetricExpirationSeconds),
				AnomalyCondition:            defaultLoadAnomalyCondition,
				DetectorCacheTimeout:        &metav1.Duration{Duration: 10 * time.Minute},
				ResourceWeights: map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    1,
					corev1.ResourceMemory: 1,
				},
			},
		},
		{
			name: "set anomalyCondition",
			args: &LowNodeLoadArgs{
				AnomalyCondition: &LoadAnomalyCondition{
					Timeout:                  &metav1.Duration{Duration: 10 * time.Second},
					ConsecutiveAbnormalities: 0,
					ConsecutiveNormalities:   3,
				},
			},
			expected: &LowNodeLoadArgs{
				NodeFit:                     pointer.Bool(true),
				NodeMetricExpirationSeconds: pointer.Int64(defaultNodeMetricExpirationSeconds),
				AnomalyCondition: &LoadAnomalyCondition{
					Timeout:                  &metav1.Duration{Duration: 10 * time.Second},
					ConsecutiveAbnormalities: defaultLoadAnomalyCondition.ConsecutiveAbnormalities,
					ConsecutiveNormalities:   3,
				},
				DetectorCacheTimeout: &metav1.Duration{Duration: 5 * time.Minute},
				ResourceWeights: map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    1,
					corev1.ResourceMemory: 1,
				},
			},
		},
		{
			name: "set weights",
			args: &LowNodeLoadArgs{
				LowThresholds: ResourceThresholds{
					corev1.ResourceCPU:    30,
					corev1.ResourceMemory: 30,
					corev1.ResourcePods:   10,
				},
				ResourceWeights: map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    10,
					corev1.ResourceMemory: 5,
				},
			},
			expected: &LowNodeLoadArgs{
				NodeFit:                     pointer.Bool(true),
				NodeMetricExpirationSeconds: pointer.Int64(defaultNodeMetricExpirationSeconds),
				AnomalyCondition:            defaultLoadAnomalyCondition,
				DetectorCacheTimeout:        &metav1.Duration{Duration: 5 * time.Minute},
				LowThresholds: ResourceThresholds{
					corev1.ResourceCPU:    30,
					corev1.ResourceMemory: 30,
					corev1.ResourcePods:   10,
				},
				ResourceWeights: map[corev1.ResourceName]int64{
					corev1.ResourceCPU:    10,
					corev1.ResourceMemory: 5,
					corev1.ResourcePods:   1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetDefaults_LowNodeLoadArgs(tt.args)
			assert.Equal(t, tt.expected, tt.args)
		})
	}
}
