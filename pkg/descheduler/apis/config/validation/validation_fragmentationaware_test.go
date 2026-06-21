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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
)

func TestValidateFragmentationAwareArgs(t *testing.T) {
	testCases := []struct {
		name          string
		args          *deschedulerconfig.FragmentationAwareArgs
		expectedError string
	}{
		{
			name: "valid args",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory},
				ImbalanceThreshold:      0.15,
				MinImprovementThreshold: 0.02,
			},
		},
		{
			name:          "nil args",
			args:          nil,
			expectedError: "FragmentationAwareArgs must not be nil",
		},
		{
			name: "empty resources",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{},
				ImbalanceThreshold:      0.15,
				MinImprovementThreshold: 0.02,
			},
			expectedError: "resources must not be empty",
		},
		{
			name: "duplicate resources",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceCPU},
				ImbalanceThreshold:      0.15,
				MinImprovementThreshold: 0.02,
			},
			expectedError: "Duplicate value: \"cpu\"",
		},
		{
			name: "negative imbalanceThreshold",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU},
				ImbalanceThreshold:      -0.1,
				MinImprovementThreshold: 0.02,
			},
			expectedError: "must be greater than or equal to 0",
		},
		{
			name: "negative minImprovementThreshold",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU},
				ImbalanceThreshold:      0.15,
				MinImprovementThreshold: -0.05,
			},
			expectedError: "must be greater than or equal to 0",
		},
		{
			name: "invalid node selector",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU},
				ImbalanceThreshold:      0.15,
				MinImprovementThreshold: 0.02,
				NodeSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Operator: "invalid-op",
						},
					},
				},
			},
			expectedError: "invalid",
		},
		{
			name: "invalid pod selector",
			args: &deschedulerconfig.FragmentationAwareArgs{
				Resources:               []corev1.ResourceName{corev1.ResourceCPU},
				ImbalanceThreshold:      0.15,
				MinImprovementThreshold: 0.02,
				PodSelectors: []deschedulerconfig.FragmentationAwarePodSelector{
					{
						Selector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Operator: "invalid-op",
								},
							},
						},
					},
				},
			},
			expectedError: "invalid",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFragmentationAwareArgs(nil, tc.args)
			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
