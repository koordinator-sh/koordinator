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

package util

import (
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

// DefaultNodeSLOSpecConfig defines the default config of the nodeSLOSpec, which would be used by the resmgr
func DefaultNodeSLOSpecConfig() slov1alpha1.NodeSLOSpec {
	return slov1alpha1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: DefaultResourceThresholdStrategy(),
	}
}

func DefaultResourceThresholdStrategy() *slov1alpha1.ResourceThresholdStrategy {
	return &slov1alpha1.ResourceThresholdStrategy{
		Enable:                      pointer.BoolPtr(false),
		CPUSuppressThresholdPercent: pointer.Int64Ptr(65),
		CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
		MemoryEvictThresholdPercent: pointer.Int64Ptr(70),
	}
}

func DefaultCPUBurstConfig() slov1alpha1.CPUBurstConfig {
	return slov1alpha1.CPUBurstConfig{
		Policy:                     slov1alpha1.CPUBurstNone,
		CPUBurstPercent:            pointer.Int64Ptr(1000),
		CFSQuotaBurstPercent:       pointer.Int64Ptr(300),
		CFSQuotaBurstPeriodSeconds: pointer.Int64Ptr(-1),
	}
}

func DefaultCPUBurstStrategy() *slov1alpha1.CPUBurstStrategy {
	return &slov1alpha1.CPUBurstStrategy{
		CPUBurstConfig:            DefaultCPUBurstConfig(),
		SharePoolThresholdPercent: pointer.Int64Ptr(50),
	}
}
