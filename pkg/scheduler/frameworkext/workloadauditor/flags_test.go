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

package workloadauditor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMetricLabels_Empty(t *testing.T) {
	names, podKeys := parseMetricLabels("")
	assert.Equal(t, []string{"priority"}, names)
	assert.Nil(t, podKeys)
}

func TestParseMetricLabels_Whitespace(t *testing.T) {
	names, podKeys := parseMetricLabels("   ")
	assert.Equal(t, []string{"priority"}, names)
	assert.Nil(t, podKeys)
}

func TestParseMetricLabels_SinglePair(t *testing.T) {
	names, podKeys := parseMetricLabels("gpu=hardware.gpu")
	assert.Equal(t, []string{"priority", "gpu"}, names)
	assert.Equal(t, []string{"hardware.gpu"}, podKeys)
}

func TestParseMetricLabels_MultiplePairs(t *testing.T) {
	names, podKeys := parseMetricLabels("gpu=hw.gpu, tenant=org.tenant")
	assert.Equal(t, []string{"priority", "gpu", "tenant"}, names)
	assert.Equal(t, []string{"hw.gpu", "org.tenant"}, podKeys)
}

func TestParseMetricLabels_Malformed(t *testing.T) {
	// "badpair" has no '=', "=empty" has empty name, "nokey=" has empty value
	names, podKeys := parseMetricLabels("badpair,good=val,=empty,nokey=")
	assert.Equal(t, []string{"priority", "good"}, names)
	assert.Equal(t, []string{"val"}, podKeys)
}

func TestParseMetricLabels_WhitespaceAroundPair(t *testing.T) {
	names, podKeys := parseMetricLabels("  gpu = hw.gpu  ")
	assert.Equal(t, []string{"priority", "gpu"}, names)
	assert.Equal(t, []string{"hw.gpu"}, podKeys)
}

func TestDefaultWorkloadAuditorConfig(t *testing.T) {
	old := WorkloadAuditorMetricLabels
	defer func() { WorkloadAuditorMetricLabels = old }()
	WorkloadAuditorMetricLabels = "gpu=hw.gpu"

	config := DefaultWorkloadAuditorConfig()
	assert.Equal(t, []string{"priority", "gpu"}, config.MetricLabelNames)
	assert.Equal(t, []string{"hw.gpu"}, config.PodLabelKeys)
}
