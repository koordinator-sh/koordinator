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
)

const (
	// ResourceUpdaterType represents the type of resource udpater, including cgroup files, resctrl files, etc
	ResourceUpdaterType = "type"
	// ResourceUpdateStatusKey represents the status of resource update
	ResourceUpdateStatusKey = "status"
)

const (
	ResourceUpdateStatusSuccess = "success"
	ResourceUpdateStatusFailed  = "failed"
)

var (
	resourceUpdateDurationMilliSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: KoordletSubsystem,
		Name:      "resource_update_duration_milliseconds",
		Help:      "time duration of resource update such as cgroup files",
		// 10us ~ 10.24ms, cgroup <= 40us
		Buckets: prometheus.ExponentialBuckets(0.01, 4, 8),
	}, []string{ResourceUpdaterType, ResourceUpdateStatusKey})

	ResourceExecutorCollector = []prometheus.Collector{
		resourceUpdateDurationMilliSeconds,
	}
)

func RecordResourceUpdateDuration(updaterType, status string, seconds float64) {
	resourceUpdateDurationMilliSeconds.WithLabelValues(updaterType, status).Observe(seconds * 1000)
}
