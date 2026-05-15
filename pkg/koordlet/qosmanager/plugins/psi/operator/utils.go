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

package operator

import (
	"math"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/podcgroup"
)

func int64SafeAdd(a, b int64) int64 {
	if a > 0 && b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64

	} else if a < 0 && b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

type zeroBank map[*podcgroup.PodResource]int64

// balance implements a zero-sum redistribution algorithm for resource allocation.
// The algorithm ensures that the total sum of all allocations equals zero by redistributing
// positive (excess) and negative (deficit) values among Pods proportionally.
//
// Algorithm steps:
//  1. Clamp each Pod's budget to [lowerBound, upperBound] range:
//     - lowerBound = request * (lowerBound - 1), ensuring minimum guaranteed allocation
//     - upperBound = limit - request, ensuring not exceeding Pod's limit
//  2. Calculate total sum, positive sum, and negative sum
//  3. If sum > 0 (excess), redistribute proportionally to positive Pods
//  4. If sum < 0 (deficit), redistribute proportionally to negative Pods
//
// Example:
//   - Pod A has excess +100, Pod B has deficit -100
//   - After balancing, both become 0 (sum = 0)
func (bank zeroBank) balance(lowerBound float64) {
	var sum, negative, positive int64
	for r := range bank {
		lower := int64(float64(r.Request) * (lowerBound - 1))
		upper := r.Limit - r.Request
		if bank[r] < lower {
			bank[r] = lower
		} else if bank[r] > upper {
			bank[r] = upper
		}
		sum += bank[r]
		if bank[r] > 0 {
			positive += bank[r]
		} else {
			negative += bank[r]
		}
	}
	if sum > 0 {
		for r, b := range bank {
			if b > 0 {
				bank[r] = int64(math.Round(float64(bank[r]) - float64(sum)*float64(b)/float64(positive)))
			}
		}
	} else if sum < 0 {
		for r, b := range bank {
			if b < 0 {
				bank[r] = int64(math.Round(float64(bank[r]) - float64(sum)*float64(b)/float64(negative)))
			}
		}
	}
}
