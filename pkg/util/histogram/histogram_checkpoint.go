/*
Copyright 2023 The Koordinator Authors.
Copyright 2018 The Kubernetes Authors.

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

package histogram

import "time"

// HistogramCheckpoint contains data needed to reconstruct the histogram.
type HistogramCheckpoint struct {
	// Reference timestamp for samples collected within this histogram.
	ReferenceTimestamp time.Time `json:"referenceTimestamp,omitempty"`

	// Map from bucket index to bucket weight.
	BucketWeights map[int]uint32 `json:"bucketWeights,omitempty"`

	// Sum of samples to be used as denominator for weights from BucketWeights.
	TotalWeight float64 `json:"totalWeight,omitempty"`
}
