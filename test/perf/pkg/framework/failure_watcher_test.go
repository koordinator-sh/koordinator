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

package framework

import "testing"

func TestIsQuotaBlockedEvent(t *testing.T) {
	cases := []struct {
		message string
		want    bool
	}{
		{"Insufficient quotas", true},
		{"Insufficient quotas (bench-elasticquota-abcd1234, cpu)", true},
		{"0/100 nodes are available: insufficient cpu", false},
		{"Preemption is not helpful for scheduling", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isQuotaBlockedEvent(c.message); got != c.want {
			t.Errorf("isQuotaBlockedEvent(%q) = %v, want %v", c.message, got, c.want)
		}
	}
}
