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
	"k8s.io/component-base/metrics/legacyregistry"
)

var (
	ElasticQuotaSpecMetric = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Name: "elastic_quota_spec",
		Help: "ElasticQuota specifications metrics",
	}, []string{"name", "resource", "tree", "is_parent", "parent", "field"})
	ElasticQuotaStatusMetric = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Name: "elastic_quota_status",
		Help: "ElasticQuota current statuc metrics",
	}, []string{"name", "resource", "tree", "is_parent", "parent", "field"})
)

func init() {
	legacyregistry.MustRegister(
		ElasticQuotaSpecMetric,
		ElasticQuotaStatusMetric,
	)
}

func RecordMetricsByResourceList(gaugeVec *metrics.GaugeVec, resources corev1.ResourceList, field string, labels map[string]string) {
	for rname, quantity := range resources {
		switch rname {
		case corev1.ResourceCPU:
			recordResourceMetric(gaugeVec, quantity.MilliValue(), string(rname), field, labels)
		default:
			recordResourceMetric(gaugeVec, quantity.Value(), string(rname), field, labels)
		}
	}
}

func recordResourceMetric(gaugeVec *metrics.GaugeVec, value int64, resourceName, field string, labels map[string]string) {
	labels["resource"] = resourceName
	labels["field"] = field

	gaugeVec.With(labels).Set(float64(value))
}
