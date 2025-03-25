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
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/rand"
)

type testMetric struct {
	labels     prometheus.Labels
	value      float64
	updateTime int64
}

const testSubsystem = "test"

func Test_GCGaugeVec(t *testing.T) {
	metricName := "test_gauge"
	vec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: testSubsystem,
		Name:      metricName,
	}, []string{"node", "pod_name", "pod_namespace"})

	testDefaultGaugeVec := NewGCGaugeVec(metricName, vec)
	assert.Equal(t, vec, testDefaultGaugeVec.GetGaugeVec())

	testMetricGC := NewMetricGC(DefaultExpireTime, DefaultGCInterval).(*metricGC)
	defer testMetricGC.Stop()

	testGaugeVec := newGCGaugeVec(metricName, vec, testMetricGC)

	//add metric1
	pod1Labels := prometheus.Labels{"node": "node1", "pod_name": "pod1", "pod_namespace": "ns1"}
	testGaugeVec.WithSet(pod1Labels, 1)
	ms := collectMetrics(vec)
	assert.Equal(t, 1, len(ms), "checkMetricsNum")
	assert.Equal(t, 1, testGaugeVec.expireStatus.CountStatus(metricName), "checkStatusNum")

	//add metric2
	pod2Labels := prometheus.Labels{"node": "node2", "pod_name": "pod2", "pod_namespace": "ns2"}
	testGaugeVec.WithSet(pod2Labels, 2)
	ms = collectMetrics(vec)
	assert.Equal(t, 2, len(ms), "checkMetricsNum")
	assert.Equal(t, 2, testGaugeVec.expireStatus.CountStatus(metricName), "checkStatusNum")

	//update metric1
	testGaugeVec.WithSet(pod1Labels, 3)
	ms = collectMetrics(vec)
	assert.Equal(t, 2, len(ms), "checkMetricsNum")
	assert.Equal(t, 2, testGaugeVec.expireStatus.CountStatus(metricName), "checkStatusNum")

	// delete metric1
	testGaugeVec.Delete(pod1Labels)
	ms = collectMetrics(vec)
	assert.Equal(t, 1, len(ms), "checkMetricsNum")
	assert.Equal(t, 1, testGaugeVec.expireStatus.CountStatus(metricName), "checkStatusNum")
}

func Test_GCCounterVec(t *testing.T) {
	metricName := "test_counter"
	vec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: testSubsystem,
		Name:      metricName,
	}, []string{"node", "pod_name", "pod_namespace"})

	testDefaultGaugeVec := NewGCCounterVec(metricName, vec)
	assert.Equal(t, vec, testDefaultGaugeVec.GetCounterVec())

	testMetricGC := NewMetricGC(DefaultExpireTime, DefaultGCInterval).(*metricGC)
	defer testMetricGC.Stop()

	testCounterVec := newGCCounterVec(metricName, vec, testMetricGC)

	//add metric1
	pod1Labels := prometheus.Labels{"node": "node1", "pod_name": "pod1", "pod_namespace": "ns1"}
	testCounterVec.WithInc(pod1Labels)
	ms := collectMetrics(vec)
	assert.Equal(t, 1, len(ms), "checkMetricsNum")
	assert.Equal(t, 1, testCounterVec.expireStatus.CountStatus(metricName), "checkStatusNum")

	//add metric2
	pod2Labels := prometheus.Labels{"node": "node2", "pod_name": "pod2", "pod_namespace": "ns2"}
	testCounterVec.WithInc(pod2Labels)
	ms = collectMetrics(vec)
	assert.Equal(t, 2, len(ms), "checkMetricsNum")
	assert.Equal(t, 2, testCounterVec.expireStatus.CountStatus(metricName), "checkStatusNum")

	//update metric1
	testCounterVec.WithInc(pod1Labels)
	ms = collectMetrics(vec)
	assert.Equal(t, 2, len(ms), "checkMetricsNum")
	assert.Equal(t, 2, testCounterVec.expireStatus.CountStatus(metricName), "checkStatusNum")

	// delete metric1
	testCounterVec.Delete(pod1Labels)
	ms = collectMetrics(vec)
	assert.Equal(t, 1, len(ms), "checkMetricsNum")
	assert.Equal(t, 1, testCounterVec.expireStatus.CountStatus(metricName), "checkStatusNum")
}

func Test_GCHistogramVec(t *testing.T) {
	metricName := "test_histogram"
	vec := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: testSubsystem,
		Name:      metricName,
		Buckets:   []float64{0.1, 0.5, 1, 2},
	}, []string{"node", "pod_name", "pod_namespace"})

	testDefaultHistogramVec := NewGCHistogramVec(metricName, vec)
	assert.Equal(t, vec, testDefaultHistogramVec.GetHistogramVec())

	// Custom MetricGC
	testMetricGC := NewMetricGC(DefaultExpireTime, DefaultGCInterval).(*metricGC)
	defer testMetricGC.Stop()

	testHistogramVec := newGCHistogramVec(metricName, vec, testMetricGC)

	// Add metric1
	pod1Labels := prometheus.Labels{"node": "node1", "pod_name": "pod1", "pod_namespace": "ns1"}
	testHistogramVec.WithObserve(pod1Labels, 0.3)
	ms := collectMetrics(vec)
	assert.Equal(t, 1, len(ms), "checkMetricsNum")
	assert.Equal(t, 1, testHistogramVec.expireStatus.CountStatus(metricName), "checkStatusNum")

	// Add metric2
	pod2Labels := prometheus.Labels{"node": "node2", "pod_name": "pod2", "pod_namespace": "ns2"}
	testHistogramVec.WithObserve(pod2Labels, 1.5)
	ms = collectMetrics(vec)
	assert.Equal(t, 2, len(ms), "checkMetricsNum")
	assert.Equal(t, 2, testHistogramVec.expireStatus.CountStatus(metricName), "checkStatusNum")

	// Update metric1
	testHistogramVec.WithObserve(pod1Labels, 0.7)
	ms = collectMetrics(vec)
	assert.Equal(t, 2, len(ms), "checkMetricsNum")
	assert.Equal(t, 2, testHistogramVec.expireStatus.CountStatus(metricName), "checkStatusNum")

	// Delete metric1
	testHistogramVec.Delete(pod1Labels)
	ms = collectMetrics(vec)
	assert.Equal(t, 1, len(ms), "checkMetricsNum")
	assert.Equal(t, 1, testHistogramVec.expireStatus.CountStatus(metricName), "checkStatusNum")
}

func Test_MetricGC_GC(t *testing.T) {
	testMetricGC := NewMetricGC(DefaultExpireTime, 1*time.Microsecond).(*metricGC)
	defer testMetricGC.Stop()

	metricName := "test_gauge"
	gaugeVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: testSubsystem,
		Name:      metricName,
	}, []string{"node", "pod_name", "pod_namespace"})
	gcGaugeVec := newGCGaugeVec(metricName, gaugeVec, testMetricGC)

	// metric should not expire
	ms := generatePodMetrics(10, time.Now().Unix())
	for _, m := range ms {
		gcGaugeVec.WithSet(m.labels, m.value)
	}
	gotMetrics := collectMetrics(gaugeVec)
	assert.Equal(t, len(ms), len(gotMetrics), "checkMetricsNum")
	assert.Equal(t, len(ms), testMetricGC.statusLen(), "checkStatusNum")

	// metric should expire
	metricsUpdate := generatePodMetrics(5, time.Now().Unix()-int64(DefaultExpireTime/time.Second))
	for _, m := range metricsUpdate {
		gcGaugeVec.WithSet(m.labels, m.value)
		err := testMetricGC.updateStatus(m.updateTime, metricName, m.labels)
		assert.NoError(t, err)
	}
	time.Sleep(10 * time.Millisecond)
	gotMetrics = collectMetrics(gaugeVec)
	assert.Equal(t, len(metricsUpdate), len(gotMetrics), "checkMetricsNum")
	assert.Equal(t, len(metricsUpdate), testMetricGC.statusLen(), "checkStatusNum")
}

func generatePodMetrics(num int, baseUpdateTime int64) []testMetric {
	var ms []testMetric
	for i := 0; i < num; i++ {
		iStr := strconv.Itoa(i)
		ms = append(ms, testMetric{labels: prometheus.Labels{"node": "node" + iStr, "pod_name": "pod" + iStr, "pod_namespace": "ns" + iStr},
			value:      1,
			updateTime: baseUpdateTime - rand.Int63nRange(1, 100)})
	}
	return ms
}

func collectMetrics(vec prometheus.Collector) []prometheus.Metric {
	metricsCh := make(chan prometheus.Metric, 10)
	go func() {
		vec.Collect(metricsCh)
		close(metricsCh)
	}()
	var ms []prometheus.Metric
	for {
		select {
		case metric, ok := <-metricsCh:
			if !ok {
				return ms
			}
			ms = append(ms, metric)
		}
	}
}
