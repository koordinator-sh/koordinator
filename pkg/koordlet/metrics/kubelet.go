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
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	HTTPVerbKey = "verb"
	HTTPPathKey = "path"
	HTTPCodeKey = "code"
)

const (
	HTTPVerbGet = "get"
)

var (
	kubeletRequestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: KoordletSubsystem,
			Name:      "kubelet_request_duration_seconds",
			Help:      "kubelet http request duration in seconds",
			// 10s ~ 4s, /config <= 4ms, /pods <= 16ms
			Buckets: prometheus.ExponentialBuckets(0.001, 4, 7),
		},
		[]string{HTTPVerbKey, HTTPPathKey, HTTPCodeKey},
	)

	KubeletStubCollector = []prometheus.Collector{
		kubeletRequestDurationSeconds,
	}
)

// RecordKubeletRequestDuration records the duration of kubelet http request
func RecordKubeletRequestDuration(verb, path, code string, seconds float64) {
	kubeletRequestDurationSeconds.WithLabelValues(verb, path, code).Observe(seconds)
}

func SinceInSeconds(start time.Time) float64 {
	return time.Since(start).Seconds()
}
