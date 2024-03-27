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

import "github.com/prometheus/client_golang/prometheus"

const (
	// RuntimeHookName represents the hook plugin name of runtime hook.
	RuntimeHookName = "hook"
	// RuntimeHookStage represents the stage of invoked runtime hook.
	RuntimeHookStage = "stage"
	// RuntimeHookReconcilerLevel represents the level (e.g. pod-level) of invoked runtime hook reconciler.
	RuntimeHookReconcilerLevel = "level"
	// RuntimeHookReconcilerResourceType represents the resource type (e.g. cpu.cfs_quota_us) of invoked runtime hook reconciler.
	RuntimeHookReconcilerResourceType = "resource_type"
)

var (
	runtimeHookInvokedDurationMilliSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: KoordletSubsystem,
		Name:      "runtime_hook_invoked_duration_milliseconds",
		Help:      "time duration of invocations of runtime hook plugins",
		// 10us ~ 10.24ms, cgroup <= 40us
		Buckets: prometheus.ExponentialBuckets(0.01, 4, 8),
	}, []string{NodeKey, RuntimeHookName, RuntimeHookStage, StatusKey})

	runtimeHookReconcilerInvokedDurationMilliSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: KoordletSubsystem,
		Name:      "runtime_hook_reconciler_invoked_duration_milliseconds",
		Help:      "time duration of invocations of runtime hook reconciler plugins",
		// 10us ~ 10.24ms, cgroup <= 40us
		Buckets: prometheus.ExponentialBuckets(0.01, 4, 8),
	}, []string{NodeKey, RuntimeHookReconcilerLevel, RuntimeHookReconcilerResourceType, StatusKey})

	RuntimeHookCollectors = []prometheus.Collector{
		runtimeHookInvokedDurationMilliSeconds,
		runtimeHookReconcilerInvokedDurationMilliSeconds,
	}
)

func RecordRuntimeHookInvokedDurationMilliSeconds(hookName, stage string, err error, seconds float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[RuntimeHookName] = hookName
	labels[RuntimeHookStage] = stage
	labels[StatusKey] = StatusSucceed
	if err != nil {
		labels[StatusKey] = StatusFailed
	}
	// convert seconds to milliseconds
	runtimeHookInvokedDurationMilliSeconds.With(labels).Observe(seconds * 1000)
}

func RecordRuntimeHookReconcilerInvokedDurationMilliSeconds(level, resourceType string, err error, seconds float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[RuntimeHookReconcilerLevel] = level
	labels[RuntimeHookReconcilerResourceType] = resourceType
	labels[StatusKey] = StatusSucceed
	if err != nil {
		labels[StatusKey] = StatusFailed
	}
	// convert seconds to milliseconds
	runtimeHookReconcilerInvokedDurationMilliSeconds.With(labels).Observe(seconds * 1000)
}
