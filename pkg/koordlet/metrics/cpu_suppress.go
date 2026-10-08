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

import k8smetrics "k8s.io/component-base/metrics"

var (
	BESuppressCPU = k8smetrics.NewGaugeVec(&k8smetrics.GaugeOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "be_suppress_cpu_cores",
		Help:           "Number of cores suppress by koordlet",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey, BESuppressTypeKey})

	BESuppressLSUsedCPU = k8smetrics.NewGaugeVec(&k8smetrics.GaugeOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "be_suppress_ls_used_cpu_cores",
		Help:           "Number of cpu cores used by LS. We consider non-BE pods and podMeta-missing pods as LS.",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey})

	BESuppressBEUsedCPU = k8smetrics.NewGaugeVec(&k8smetrics.GaugeOpts{
		Subsystem:      KoordletSubsystem,
		Name:           "be_suppress_be_used_cpu_cores",
		Help:           "Number of cpu cores used by BE.",
		StabilityLevel: k8smetrics.ALPHA,
	}, []string{NodeKey})

	CPUSuppressRegisterableCollectors = []k8smetrics.Registerable{
		BESuppressCPU,
		BESuppressLSUsedCPU,
		BESuppressBEUsedCPU,
	}
)

func RecordBESuppressCores(suppressType string, value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[BESuppressTypeKey] = suppressType
	BESuppressCPU.With(labels).Set(value)
}

func RecordBESuppressLSUsedCPU(value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	BESuppressLSUsedCPU.With(labels).Set(value)
}

func RecordBESuppressBEUsedCPU(value float64) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	BESuppressBEUsedCPU.With(labels).Set(value)
}
