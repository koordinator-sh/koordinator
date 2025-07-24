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

package elasticquota

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/component-base/metrics"
	schedulermetrics "k8s.io/kubernetes/pkg/scheduler/metrics"

	koordschedulermetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

var (
	ElasticQuotaSpecMetric = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem: schedulermetrics.SchedulerSubsystem,
			Name:      "elastic_quota_spec",
			Help:      "ElasticQuota specifications",
		},
		[]string{"name", "resource", "tree", "is_parent", "parent", "field"},
	)

	ElasticQuotaStatusMetric = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem: schedulermetrics.SchedulerSubsystem,
			Name:      "elastic_quota_status",
			Help:      "ElasticQuota current status",
		},
		[]string{"name", "resource", "tree", "is_parent", "parent", "field"},
	)

	UpdateElasticQuotaStatusLatency = metrics.NewHistogram(
		&metrics.HistogramOpts{
			Subsystem: schedulermetrics.SchedulerSubsystem,
			Name:      "update_elastic_quota_status_duration_seconds",
			Help:      "Update ElasticQuota status latency in seconds",
			Buckets:   metrics.ExponentialBuckets(0.001, 2, 15),
		},
	)
)

func init() {
	koordschedulermetrics.RegisterMetrics(
		ElasticQuotaSpecMetric,
		ElasticQuotaStatusMetric,
		UpdateElasticQuotaStatusLatency,
	)
}

func RecordElasticQuotaMetric(gaugeVec *metrics.GaugeVec, resources corev1.ResourceList, field string, labels map[string]string) {
	for resourceName, quantity := range resources {
		switch resourceName {
		case corev1.ResourceCPU:
			recordResourceMetric(gaugeVec, quantity.MilliValue(), string(resourceName), field, labels)
		default:
			recordResourceMetric(gaugeVec, quantity.Value(), string(resourceName), field, labels)
		}
	}
}

func recordResourceMetric(gaugeVec *metrics.GaugeVec, value int64, resourceName, field string, labels map[string]string) {
	labels["resource"] = resourceName
	labels["field"] = field
	gaugeVec.With(labels).Set(float64(value))
}

func DeleteElasticQuotaMetric(gaugeVec *metrics.GaugeVec, resources corev1.ResourceList, field string, labels map[string]string) {
	for resourceName := range resources {
		deleteResourceMetric(gaugeVec, string(resourceName), field, labels)
	}
}

func deleteResourceMetric(gaugeVec *metrics.GaugeVec, resourceName, field string, labels map[string]string) {
	labels["resource"] = resourceName
	labels["field"] = field
	gaugeVec.Delete(labels)
}
