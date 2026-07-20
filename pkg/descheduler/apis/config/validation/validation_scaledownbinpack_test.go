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

package validation

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
)

func TestValidateScaleDownBinPackArgs(t *testing.T) {
	testCases := []struct {
		name          string
		args          *deschedulerconfig.ScaleDownBinPackArgs
		expectedError bool
		errorMsg      string
	}{
		{
			name: "valid calculate only strategy",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy: deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly,
			},
			expectedError: false,
		},
		{
			name: "valid evict directly strategy",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy:       deschedulerconfig.ScaleDownBinPackStrategyEvictDirectly,
				MaxPodsToEvict: func() *int32 { v := int32(5); return &v }(),
			},
			expectedError: false,
		},
		{
			name: "invalid strategy",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy: "UnknownStrategy",
			},
			expectedError: true,
			errorMsg:      "strategy must be CalculateOnly or EvictDirectly",
		},
		{
			name: "missing maxPodsToEvict for evict directly strategy",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy: deschedulerconfig.ScaleDownBinPackStrategyEvictDirectly,
			},
			expectedError: true,
			errorMsg:      "maxPodsToEvict must be positive when strategy is EvictDirectly",
		},
		{
			name: "negative maxPodsToEvict for evict directly strategy",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy:       deschedulerconfig.ScaleDownBinPackStrategyEvictDirectly,
				MaxPodsToEvict: func() *int32 { v := int32(-1); return &v }(),
			},
			expectedError: true,
			errorMsg:      "maxPodsToEvict must be positive when strategy is EvictDirectly",
		},
		{
			name: "invalid node selector",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy: deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly,
				NodeSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "invalid_key!!",
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{"value"},
						},
					},
				},
			},
			expectedError: true,
			errorMsg:      "Invalid value",
		},
		{
			name: "invalid pod selector",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy: deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly,
				PodSelectors: []deschedulerconfig.ScaleDownBinPackPodSelector{
					{
						Name: "invalid-selector",
						Selector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "invalid_key!!",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"value"},
								},
							},
						},
					},
				},
			},
			expectedError: true,
			errorMsg:      "Invalid value",
		},
		{
			name: "invalid namespaces include and exclude set",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy: deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly,
				EvictableNamespaces: &deschedulerconfig.Namespaces{
					Include: []string{"default"},
					Exclude: []string{"kube-system"},
				},
			},
			expectedError: true,
			errorMsg:      "only one of Include/Exclude namespaces can be set",
		},
		{
			name: "invalid resource weights negative",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy: deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly,
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: -1.0,
				},
			},
			expectedError: true,
			errorMsg:      "weights must be positive finite floats",
		},
		{
			name: "invalid resource weights NaN",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy: deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly,
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU: math.NaN(),
				},
			},
			expectedError: true,
			errorMsg:      "weights must be positive finite floats",
		},
		{
			name: "valid resource weights",
			args: &deschedulerconfig.ScaleDownBinPackArgs{
				Strategy: deschedulerconfig.ScaleDownBinPackStrategyCalculateOnly,
				ResourceWeights: map[corev1.ResourceName]float64{
					corev1.ResourceCPU:    1.0,
					corev1.ResourceMemory: 2.5,
				},
			},
			expectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateScaleDownBinPackArgs(field.NewPath("args"), tc.args)
			if tc.expectedError {
				assert.Error(t, err)
				if tc.errorMsg != "" {
					assert.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
