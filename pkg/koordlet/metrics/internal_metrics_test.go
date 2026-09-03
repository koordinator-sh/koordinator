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
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	k8smetrics "k8s.io/component-base/metrics"
)

// TestInternalRegistryGathersAllInternalMetrics asserts that every koordlet internal metric is registered
// on InternalRegistry and therefore reported on /internal-metrics.
//
// A metric that is defined but never registered fails silently rather than loudly: an unregistered
// component-base metric returns a no-op from With() and false from Delete(), both without an error. So a
// metric dropped from the registration list would keep compiling and keep passing its own unit tests while
// no longer being reported at all. Each metric is given one sample before gathering because a vec with no
// series emits no metric family.
func TestInternalRegistryGathersAllInternalMetrics(t *testing.T) {
	internalMetrics := []struct {
		name   string
		record func()
	}{
		// common.go
		{"koordlet_start_time", func() { KoordletStartTime.WithLabelValues("node").Set(1) }},
		{"koordlet_collect_node_cpu_info_status", func() {
			CollectNodeCPUInfoStatus.WithLabelValues("node", StatusSucceed).Inc()
		}},
		{"koordlet_collect_node_numa_info_status", func() {
			CollectNodeNUMAInfoStatus.WithLabelValues("node", StatusSucceed).Inc()
		}},
		{"koordlet_collect_node_local_storage_info_status", func() {
			CollectNodeLocalStorageInfoStatus.WithLabelValues("node", StatusSucceed).Inc()
		}},
		{"koordlet_pod_eviction", func() { PodEviction.WithLabelValues("node", "reason").Inc() }},
		{"koordlet_pod_eviction_detail", func() {
			PodEvictionDetail.GetCounterVec().WithLabelValues("node", "namespace", "pod", "reason").Inc()
		}},
		{"koordlet_node_used_cpu_cores", func() { NodeUsedCPU.WithLabelValues("node").Set(1) }},
		{"koordlet_node_used_memory_bytes", func() { NodeUsedMemory.WithLabelValues("node").Set(1) }},

		// cpu_suppress.go
		{"koordlet_be_suppress_cpu_cores", func() { BESuppressCPU.WithLabelValues("node", "type").Set(1) }},
		{"koordlet_be_suppress_ls_used_cpu_cores", func() { BESuppressLSUsedCPU.WithLabelValues("node").Set(1) }},
		{"koordlet_be_suppress_be_used_cpu_cores", func() { BESuppressBEUsedCPU.WithLabelValues("node").Set(1) }},

		// cpu_burst.go
		{"koordlet_container_scaled_cfs_burst_us", func() {
			ContainerScaledCFSBurstUS.WithLabelValues("node", "namespace", "pod", "containerID", "container").Set(1)
		}},
		{"koordlet_container_scaled_cfs_quota_us", func() {
			ContainerScaledCFSQuotaUS.WithLabelValues("node", "namespace", "pod", "containerID", "container").Set(1)
		}},

		// cpu_cpuset.go
		{"koordlet_cpuset_share_pool_cpu_cores", func() { CPUSetSharePoolCPUS.WithLabelValues("node").Set(1) }},
		{"koordlet_cpuset_be_share_pool_cpu_cores", func() { CPUSetBESharePoolCPUS.WithLabelValues("node").Set(1) }},
		{"koordlet_cpuset_share_pool_info", func() { CPUSetSharePoolInfo.WithLabelValues("node", "0").Set(1) }},
		{"koordlet_cpuset_be_share_pool_info", func() { CPUSetBESharePoolInfo.WithLabelValues("node", "0").Set(1) }},

		// prediction.go
		{"koordlet_node_predicted_resource_reclaimable", func() {
			NodePredictedResourceReclaimable.WithLabelValues("node", "predictor", "cpu", UnitCore).Set(1)
		}},
		{"koordlet_node_predicted_resource_peak", func() {
			NodePredictedResourcePeak.WithLabelValues("node", "predictor", "cpu", UnitCore).Set(1)
		}},

		// core_sched.go
		{"koordlet_container_core_sched_cookie", func() {
			ContainerCoreSchedCookie.GetGaugeVec().WithLabelValues(
				"node", "pod", "namespace", "podUID", "container", "containerID", "group", "cookie").Set(1)
		}},
		{"koordlet_core_sched_cookie_manage_status", func() {
			CoreSchedCookieManageStatus.GetCounterVec().WithLabelValues("node", "group", StatusSucceed).Inc()
		}},

		// node_metric.go
		{"koordlet_node_metric_node_usage", func() {
			NodeMetricNodeUsage.WithLabelValues("node", "cpu", UnitCore).Set(1)
		}},
		{"koordlet_node_metric_system_usage", func() {
			NodeMetricSystemUsage.WithLabelValues("node", "cpu", UnitCore).Set(1)
		}},
		{"koordlet_node_metric_numa_node_usage", func() {
			NodeMetricNUMANodeUsage.WithLabelValues("node", "0", "cpu", UnitCore).Set(1)
		}},
		{"koordlet_node_metric_numa_system_usage", func() {
			NodeMetricNUMASystemUsage.WithLabelValues("node", "0", "cpu", UnitCore).Set(1)
		}},

		// oom_score_adj.go
		{"koordlet_container_oom_score_adj", func() {
			ContainerOOMScoreAdj.GetGaugeVec().WithLabelValues(
				"node", "pod", "namespace", "podUID", "container").Set(1)
		}},

		// resource_executor.go
		{"koordlet_resource_update_duration_milliseconds", func() {
			resourceUpdateDurationMilliSeconds.WithLabelValues("updater", StatusSucceed).Observe(1)
		}},

		// kubelet.go
		{"koordlet_kubelet_request_duration_seconds", func() {
			kubeletRequestDurationSeconds.WithLabelValues(HTTPVerbGet, "/pods", "200").Observe(1)
		}},

		// runtime_hook.go
		{"koordlet_runtime_hook_invoked_duration_milliseconds", func() {
			runtimeHookInvokedDurationMilliSeconds.WithLabelValues("node", "hook", "stage", StatusSucceed).Observe(1)
		}},
		{"koordlet_runtime_hook_reconciler_invoked_duration_milliseconds", func() {
			runtimeHookReconcilerInvokedDurationMilliSeconds.WithLabelValues(
				"node", "level", "resourceType", StatusSucceed).Observe(1)
		}},

		// host_application.go
		{"koordlet_host_application_resource_usage", func() {
			HostApplicationResourceUsage.WithLabelValues("node", "app", "cpu", "priorityClass", "qos").Set(1)
		}},
	}

	// The table above is maintained by hand, so on its own it only catches a metric that is removed from a
	// slice, not one that is added. Every internal collector is a single Vec, i.e. exactly one metric family,
	// so the collector count across the registered slices must equal the number of names covered here.
	internalCollectorSlices := [][]prometheus.Collector{
		CommonCollectors,
		CPUSuppressCollector,
		CPUBurstCollector,
		CPUSetCollector,
		PredictionCollectors,
		CoreSchedCollector,
		NodeMetricCollectors,
		OOMScoreAdjCollector,
		ResourceExecutorCollector,
		KubeletStubCollector,
		RuntimeHookCollectors,
		HostApplicationCollectors,
	}
	registered := 0
	for _, slice := range internalCollectorSlices {
		registered += len(slice)
	}
	assert.Len(t, internalMetrics, registered, "a collector was added to an internal slice but not to this test")

	t.Cleanup(func() { resetCollectors(internalCollectorSlices) })
	for _, m := range internalMetrics {
		m.record()
	}

	families, err := InternalRegistry.Gather()
	assert.NoError(t, err)

	gathered := map[string]bool{}
	for _, family := range families {
		gathered[family.GetName()] = true
	}

	for _, m := range internalMetrics {
		assert.True(t, gathered[m.name], "metric %s is not registered on InternalRegistry", m.name)
	}
}

var (
	// registrationProbe is a test-only metric used to exercise the Registerable path. It is defined with
	// component-base and ALPHA stability, exactly as the metrics migrated in the follow-up PRs will be.
	registrationProbe = k8smetrics.NewGaugeVec(&k8smetrics.GaugeOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "registration_probe",
		Help:           "test-only metric asserting internalMustRegister publishes to InternalRegistry",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey})

	// Registration is global and one-way, so it must happen once even when the test binary repeats a test.
	registrationProbeOnce sync.Once
)

// TestInternalMustRegisterPublishesToInternalRegistry exercises the Registerable registration path that the
// per-file migration will move each collector slice onto, so the path is proven before anything depends on it.
// The check is worth making because failure here is silent: a component-base metric that never reaches the
// registry is not created, and an uncreated metric returns a no-op from With() rather than an error, so it
// would simply stop being reported.
func TestInternalMustRegisterPublishesToInternalRegistry(t *testing.T) {
	registrationProbeOnce.Do(func() { internalMustRegister(registrationProbe) })

	t.Cleanup(registrationProbe.Reset)
	registrationProbe.WithLabelValues("node").Set(1)

	families, err := InternalRegistry.Gather()
	assert.NoError(t, err)

	gathered := map[string]bool{}
	for _, family := range families {
		gathered[family.GetName()] = true
	}
	assert.True(t, gathered["koordlet_registration_probe"],
		"a component-base metric registered via internalMustRegister is not reported by InternalRegistry")
}

// resetCollectors clears the series written by a test so later tests in the package do not inherit them.
func resetCollectors(slices [][]prometheus.Collector) {
	for _, slice := range slices {
		for _, collector := range slice {
			switch vec := collector.(type) {
			case *prometheus.GaugeVec:
				vec.Reset()
			case *prometheus.CounterVec:
				vec.Reset()
			case *prometheus.HistogramVec:
				vec.Reset()
			}
		}
	}
}
