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

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergedGather(t *testing.T) {
	// Create two Gatherer objects
	gatherer1 := prometheus.NewRegistry()
	gatherer2 := prometheus.NewRegistry()

	// Add some metrics to the Gatherer objects
	c1 := prometheus.NewCounter(prometheus.CounterOpts{Name: "c1"})
	c2 := prometheus.NewCounter(prometheus.CounterOpts{Name: "c2"})
	gatherer1.MustRegister(c1)
	gatherer2.MustRegister(c2)

	// Create a Merged Gatherer
	mergedGatherer := MergedGatherFunc(gatherer1, gatherer2)

	// Call Gather and check the result
	metrics, err := mergedGatherer.Gather()
	require.NoError(t, err)

	// Verify that the result contains the expected metrics
	assert.Equal(t, 2, len(metrics))
	assert.Equal(t, "c1", *metrics[0].Name)
	assert.Equal(t, "c2", *metrics[1].Name)
}

func TestMergedGatherEmpty(t *testing.T) {
	// Create empty merged gatherer
	mergedGatherer := MergedGatherFunc()

	// Gather metrics
	metricsFamilies, err := mergedGatherer.Gather()
	assert.NoError(t, err)

	// Check if no metric families are returned
	assert.Equal(t, 0, len(metricsFamilies))
}
