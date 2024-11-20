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
	"github.com/prometheus/client_golang/prometheus"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

const (
	hostApplicationName = "host_application_name"
	priorityClass       = "priority_class"
	qos                 = "qos"
)

var (
	HostApplicationResourceUsage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "host_application_resource_usage",
		Help:      "Host application resource usage collected by koordlet",
	}, []string{NodeKey, hostApplicationName, ResourceKey, priorityClass, qos})

	HostApplicationCollectors = []prometheus.Collector{
		HostApplicationResourceUsage,
	}
)

func ResetHostApplicationResourceUsage() {
	HostApplicationResourceUsage.Reset()
}

func RecordHostApplicationResourceUsage(resourceName string, hostAppSpec *slov1alpha1.HostApplicationSpec, value float64) {
	if hostAppSpec == nil {
		return
	}
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[hostApplicationName] = hostAppSpec.Name
	labels[ResourceKey] = resourceName
	labels[priorityClass] = string(hostAppSpec.Priority)
	labels[qos] = string(hostAppSpec.QoS)
	HostApplicationResourceUsage.With(labels).Set(value)
}
