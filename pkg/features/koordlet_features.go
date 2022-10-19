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

package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"
)

const (
	// AuditEvents is used to audit recent events
	AuditEvents featuregate.Feature = "AuditEvents"

	// AuditEventsHTTPHandler is used to get recent events from koordlet port
	AuditEventsHTTPHandler featuregate.Feature = "AuditEventsHTTPHandler"

	// BECgroupReconcile sets cpu memory limit for best-effort pod
	BECgroupReconcile featuregate.Feature = "BECgroupReconcile"

	// BECPUSuppress suppresses for best-effort pod
	BECPUSuppress featuregate.Feature = "BECPUSuppress"

	// BECPUEvict for best-effort pod
	BECPUEvict featuregate.Feature = "BECPUEvict"

	// BEMemoryEvict evict best-effort pod based on Memory
	BEMemoryEvict featuregate.Feature = "BEMemoryEvict"

	// CPUBurst set cpu.cfs_burst_us; scale up cpu.cfs_quota_us if pod cpu throttled
	CPUBurst featuregate.Feature = "CPUBurst"

	// RdtResctrl sets intel rdt resctrl for processes belonging to ls or be pods
	RdtResctrl featuregate.Feature = "RdtResctrl"

	// CgroupReconcile reconciles qos config for resources like cpu, memory, disk, etc.
	CgroupReconcile featuregate.Feature = "CgroupReconcile"

	// CgroupReconcile report node topology info to api-server through crd.
	NodeTopologyReport featuregate.Feature = "NodeTopologyReport"

	// Accelerators enables GPU related feature in koordlet.
	// Only Nvidia GPUs are supported as of v0.6.
	Accelerators featuregate.Feature = "Accelerators"
)

func init() {
	runtime.Must(DefaultMutableKoordletFeatureGate.Add(defaultKoordletFeatureGates))
}

var (
	DefaultMutableKoordletFeatureGate featuregate.MutableFeatureGate = featuregate.NewFeatureGate()
	DefaultKoordletFeatureGate        featuregate.FeatureGate        = DefaultMutableKoordletFeatureGate

	defaultKoordletFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
		AuditEvents:            {Default: false, PreRelease: featuregate.Alpha},
		AuditEventsHTTPHandler: {Default: false, PreRelease: featuregate.Alpha},
		BECgroupReconcile:      {Default: false, PreRelease: featuregate.Alpha},
		BECPUSuppress:          {Default: false, PreRelease: featuregate.Alpha},
		BECPUEvict:             {Default: false, PreRelease: featuregate.Alpha},
		BEMemoryEvict:          {Default: false, PreRelease: featuregate.Alpha},
		CPUBurst:               {Default: false, PreRelease: featuregate.Alpha},
		RdtResctrl:             {Default: false, PreRelease: featuregate.Alpha},
		CgroupReconcile:        {Default: false, PreRelease: featuregate.Alpha},
		NodeTopologyReport:     {Default: false, PreRelease: featuregate.Alpha},
		Accelerators:           {Default: false, PreRelease: featuregate.Alpha},
	}
)
