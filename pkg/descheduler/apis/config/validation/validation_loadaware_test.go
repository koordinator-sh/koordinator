/*
Copyright 2023 The Koordinator Authors.

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

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/stretchr/testify/assert"
)

func TestValidateLowLoadUtilizationArgs_NumerOfNodes(t *testing.T) {
	testCases := []struct {
		numOfNodes    int
		expectedError bool
	}{
		{
			numOfNodes:    10,
			expectedError: false,
		},
		{
			numOfNodes:    5,
			expectedError: false,
		},
		{
			numOfNodes:    0,
			expectedError: false,
		},
		{
			numOfNodes:    -1,
			expectedError: true,
		},
		{
			numOfNodes:    -5,
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		args := &deschedulerconfig.LowNodeLoadArgs{
			NumberOfNodes: int32(tc.numOfNodes),
		}
		err := ValidateLowLoadUtilizationArgs(nil, args)
		if tc.expectedError {
			assert.Error(t, err, "Expected an error for invalid NumberOfNodes")
			assert.Contains(t, err.Error(), "must be greater than or equal to 0", "Expected specific error message")
		} else {
			assert.Nil(t, err, "Expected no error for valid configuration")
		}

	}
}

func TestValidateLowLoadUtilizationArgs_EvictableNamespaces(t *testing.T) {
	testCases := []struct {
		include       []string
		exclude       []string
		expectedError bool
	}{
		{
			include:       []string{"namespace1"},
			expectedError: false,
		},
		{
			exclude:       []string{"namespace1"},
			expectedError: false,
		},
		{
			include:       []string{"namespace1", "namespace2"},
			expectedError: false,
		},
		{
			include:       []string{"namespace1"},
			exclude:       []string{"namespace2"},
			expectedError: true,
		},
		{
			include:       []string{"namespace1", "namespace11"},
			exclude:       []string{"namespace2"},
			expectedError: true,
		},
		{
			include:       []string{"namespace1"},
			exclude:       []string{"namespace2", "namespace22"},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		args := &deschedulerconfig.LowNodeLoadArgs{
			EvictableNamespaces: &deschedulerconfig.Namespaces{
				Include: tc.include,
				Exclude: tc.exclude,
			},
		}
		err := ValidateLowLoadUtilizationArgs(nil, args)
		if tc.expectedError {
			assert.Error(t, err, "Expected an error for invalid EvictableNamespaces", tc.include, tc.exclude)
			assert.Contains(t, err.Error(), "only one of Include/Exclude namespaces can be set", "Expected specific error message")
		} else {
			assert.Nil(t, err, "Expected no error for valid configuration")
		}
	}
}

func TestValidateLowLoadUtilizationArgs_NodePoolThresholds(t *testing.T) {
	testCases := []struct {
		highThresholds   int
		lowThresholds    int
		anomalyCondition *deschedulerconfig.LoadAnomalyCondition
		expectedError    bool
	}{
		{
			highThresholds: 100,
			lowThresholds:  90,
			expectedError:  false,
		},
		{
			highThresholds: 0,
			lowThresholds:  0,
			expectedError:  false,
		},
		{
			highThresholds: 0,
			lowThresholds:  -1,
			expectedError:  true,
		},
		{
			highThresholds: 100,
			lowThresholds:  -10,
			expectedError:  true,
		},
		{
			highThresholds: -10,
			lowThresholds:  -100,
			expectedError:  true,
		},
		{
			highThresholds: 100,
			lowThresholds:  50,
			anomalyCondition: &deschedulerconfig.LoadAnomalyCondition{
				ConsecutiveAbnormalities: 5,
			},
			expectedError: false,
		},
		{
			highThresholds: 100,
			lowThresholds:  50,
			anomalyCondition: &deschedulerconfig.LoadAnomalyCondition{
				ConsecutiveAbnormalities: 0,
			},
			expectedError: true,
		},
		{
			highThresholds: 120, // we do not check threshold larger than 100
			lowThresholds:  50,
			expectedError:  false,
		},
		{
			highThresholds: 120, // we do not check threshold larger than 100
			lowThresholds:  120,
			expectedError:  false,
		},
	}

	for _, tc := range testCases {
		anomalyCondition := &deschedulerconfig.LoadAnomalyCondition{}
		if tc.anomalyCondition != nil {
			anomalyCondition.ConsecutiveAbnormalities = tc.anomalyCondition.ConsecutiveAbnormalities
		} else {
			anomalyCondition.ConsecutiveAbnormalities = 5
		}
		args := &deschedulerconfig.LowNodeLoadArgs{
			NodePools: []deschedulerconfig.LowNodeLoadNodePool{
				{
					HighThresholds:   deschedulerconfig.ResourceThresholds{"cpu": deschedulerconfig.Percentage(tc.highThresholds)},
					LowThresholds:    deschedulerconfig.ResourceThresholds{"cpu": deschedulerconfig.Percentage(tc.lowThresholds)},
					AnomalyCondition: anomalyCondition,
				},
			},
		}
		err := ValidateLowLoadUtilizationArgs(nil, args)
		if tc.expectedError {
			assert.Error(t, err, "Expected an error for invalid NodePool thresholds")
		} else {
			assert.Nil(t, err, "Expected no error for valid configuration")
		}
	}
}
