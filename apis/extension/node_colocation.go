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

package extension

const (
	// AnnotationNodeColocationStrategy denotes the annotation key of the node colocation strategy.
	// The value is the ColocationStrategy. It takes precedence to the ColocationStrategy in the slo-controller-config.
	// The illegal value will be ignored.
	AnnotationNodeColocationStrategy = NodeDomainPrefix + "/colocation-strategy"

	// LabelCPUReclaimRatio denotes the CPU reclaim ratio of a node. The value is a float number.
	// It takes precedence to the CPUReclaimThresholdPercent in the slo-controller-config and the node annotations.
	// The illegal value will be ignored.
	LabelCPUReclaimRatio = NodeDomainPrefix + "/cpu-reclaim-ratio"
	// LabelMemoryReclaimRatio denotes the memory reclaim ratio of a node. The value is a float number.
	// It takes precedence to the MemoryReclaimThresholdPercent in the slo-controller-config and the node annotations.
	// The illegal value will be ignored.
	LabelMemoryReclaimRatio = NodeDomainPrefix + "/memory-reclaim-ratio"
)
