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
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/podcgroup"
)

func podResource(request, limit int64) *podcgroup.PodResource {
	return &podcgroup.PodResource{
		Request: request,
		Limit:   limit,
	}
}

func TestInt64SafeAdd(t *testing.T) {
	tests := []struct {
		name string
		a    int64
		b    int64
		want int64
	}{
		// normal
		{name: "positive_no_overflow", a: 100, b: 200, want: 300},
		{name: "negative_no_underflow", a: -100, b: -200, want: -300},
		{name: "mixed_sign", a: 500, b: -200, want: 300},

		// positive overflow
		{name: "max_positive_overflow", a: math.MaxInt64, b: 1, want: math.MaxInt64},
		{name: "edge_positive_overflow", a: math.MaxInt64 - 5, b: 6, want: math.MaxInt64},

		// negative underflow
		{name: "min_negative_underflow", a: math.MinInt64, b: -1, want: math.MinInt64},
		{name: "edge_negative_underflow", a: math.MinInt64 + 5, b: -6, want: math.MinInt64},

		// cornor cases
		{name: "zero_add", a: 0, b: 0, want: 0},
		{name: "max_int64_add_zero", a: math.MaxInt64, b: 0, want: math.MaxInt64},
		{name: "min_int64_add_zero", a: math.MinInt64, b: 0, want: math.MinInt64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := int64SafeAdd(tt.a, tt.b); got != tt.want {
				t.Errorf("int64SafeAdd(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestZeroBank(t *testing.T) {
	reference := []*podcgroup.PodResource{
		podResource(5, 10),  // for lowerBound=0.5, bound is [2.5, 10], i.e. delta bound [-2.5, 5]
		podResource(2, 6),   // for lowerBound=0.5, bound is [1, 6], i.e. delta bound [-1, 4]
		podResource(10, 20), // for lowerBound=0.5, bound is [5, 20], i.e. delta bound [-5, 10]
	}
	for _, tc := range []struct {
		name       string
		input      zeroBank
		lowerBound float64
		want       zeroBank
	}{
		{
			name: "single in bound",
			input: zeroBank{
				reference[0]: 5, // bound [-2.5, 5]
			},
			lowerBound: 0.5,
			want: zeroBank{
				reference[0]: 0,
			},
		},
		{
			name: "single out of bound",
			input: zeroBank{
				reference[0]: -3, // bound [-2.5, 5]
			},
			lowerBound: 0.5,
			want: zeroBank{
				reference[0]: 0,
			},
		},
		{
			name: "unbalanced in bound",
			input: zeroBank{
				reference[0]: 5,  // bound [-2.5, 5]
				reference[1]: 3,  // bound [-1, 4]
				reference[2]: -2, // bound [-5, 10]
			},
			lowerBound: 0.5,
			want: zeroBank{
				reference[0]: 1,
				reference[1]: 1,
				reference[2]: -2,
			},
		},
		{
			name: "unbalanced out of bound",
			input: zeroBank{
				reference[0]: 8,  // bound [-2.5, 5]
				reference[1]: 3,  // bound [-1, 4]
				reference[2]: -2, // bound [-5, 10]
			},
			lowerBound: 0.5,
			want: zeroBank{
				reference[0]: 1,
				reference[1]: 1,
				reference[2]: -2,
			},
		},
		{
			name: "balanced in bound",
			input: zeroBank{
				reference[0]: 1,  // bound [-2.5, 5]
				reference[1]: -1, // bound [-1, 4]
			},
			lowerBound: 0.5,
			want: zeroBank{
				reference[0]: 1,
				reference[1]: -1,
			},
		},
		{
			name: "balanced out of bound",
			input: zeroBank{
				reference[0]: 8,  // bound [-2.5, 5]
				reference[1]: -2, // bound [-1, 4]
			},
			lowerBound: 0.5,
			want: zeroBank{
				reference[0]: 1,
				reference[1]: -1,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.input.balance(tc.lowerBound)
			if !cmp.Equal(tc.input, tc.want) {
				t.Errorf("unexpect, diff(+want, -got): %v", cmp.Diff(tc.want, tc.input))
			}
		})
	}
}
