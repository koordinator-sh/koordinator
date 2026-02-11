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

	"github.com/prometheus/client_golang/prometheus"
)

var (
	CPUSetSharePoolCPUS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "cpuset_share_pool_cpu_cores",
		Help:      "Number of share pool cores",
	}, []string{NodeKey})

	CPUSetBESharePoolCPUS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "cpuset_be_share_pool_cpu_cores",
		Help:      "Number of be share pool cores",
	}, []string{NodeKey})

	CPUSetSharePoolInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "cpuset_share_pool_info",
		Help:      "CPUSet share pool info, means this cpu id is in cpuset share pool",
	}, []string{NodeKey, CPUIDKey})

	CPUSetBESharePoolInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: KoordletSubsystem,
		Name:      "cpuset_be_share_pool_info",
		Help:      "CPUSet be share pool info, means this cpu id is in cpuset be share pool",
	}, []string{NodeKey, CPUIDKey})

	CPUSetCollector = []prometheus.Collector{
		CPUSetSharePoolCPUS,
		CPUSetBESharePoolCPUS,
		CPUSetSharePoolInfo,
		CPUSetBESharePoolInfo,
	}
)

func RecordCPUSetSharePoolCores(value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	CPUSetSharePoolCPUS.With(labels).Set(value)
}

func RecordCPUSetBESharePoolCores(value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	CPUSetBESharePoolCPUS.With(labels).Set(value)
}

func RecordCPUSetSharePoolInfo(cpu int) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[CPUIDKey] = strconv.Itoa(cpu)
	CPUSetSharePoolInfo.With(labels).Set(1)
}

func RecordCPUSetBESharePoolInfo(cpu int) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[CPUIDKey] = strconv.Itoa(cpu)
	CPUSetBESharePoolInfo.With(labels).Set(1)
}
