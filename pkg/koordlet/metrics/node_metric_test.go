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
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testGaugeValue(t *testing.T, gauge prometheus.Gauge) float64 {
	m := &dto.Metric{}
	assert.NoError(t, gauge.Write(m))
	return m.GetGauge().GetValue()
}

func TestNodeMetricCollectors(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	t.Run("test not panic", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)

		RecordNodeMetricNodeUsage(string(corev1.ResourceCPU), UnitCore, 2.5)
		RecordNodeMetricNodeUsage(string(corev1.ResourceMemory), UnitByte, float64(10*1024*1024*1024))
		RecordNodeMetricSystemUsage(string(corev1.ResourceCPU), UnitCore, 0.5)
		RecordNodeMetricSystemUsage(string(corev1.ResourceMemory), UnitByte, float64(2*1024*1024*1024))
		RecordNodeMetricNUMANodeUsage(0, string(corev1.ResourceCPU), UnitCore, 1.5)
		RecordNodeMetricNUMANodeUsage(0, string(corev1.ResourceMemory), UnitByte, float64(5*1024*1024*1024))
		RecordNodeMetricNUMASystemUsage(0, string(corev1.ResourceCPU), UnitCore, 0.25)
		RecordNodeMetricNUMASystemUsage(0, string(corev1.ResourceMemory), UnitByte, float64(1024*1024*1024))
	})

	t.Run("test value collection", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)

		RecordNodeMetricNodeUsage(string(corev1.ResourceCPU), UnitCore, 2.5)
		gauge, err := NodeMetricNodeUsage.GetMetricWithLabelValues("test-node", string(corev1.ResourceCPU), UnitCore)
		assert.NoError(t, err)
		assert.Equal(t, 2.5, testGaugeValue(t, gauge))

		RecordNodeMetricNUMASystemUsage(1, string(corev1.ResourceMemory), UnitByte, 1024)
		gauge, err = NodeMetricNUMASystemUsage.GetMetricWithLabelValues("test-node", "1", string(corev1.ResourceMemory), UnitByte)
		assert.NoError(t, err)
		assert.Equal(t, float64(1024), testGaugeValue(t, gauge))

		// reset cleans up all the series
		ResetNodeMetricUsages()
		gauge, err = NodeMetricNodeUsage.GetMetricWithLabelValues("test-node", string(corev1.ResourceCPU), UnitCore)
		assert.NoError(t, err)
		assert.Equal(t, float64(0), testGaugeValue(t, gauge))
	})
}
