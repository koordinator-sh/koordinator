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

package adaptor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
)

// mockEvictor implements framework.Evictor with independently-controlled return
// values for Filter and PreEvictionFilter so that the two methods can be
// distinguished in tests.
type mockEvictor struct {
	filterResult            bool
	preEvictionFilterResult bool
}

var _ framework.Evictor = &mockEvictor{}

func (m *mockEvictor) Filter(_ *corev1.Pod) bool            { return m.filterResult }
func (m *mockEvictor) PreEvictionFilter(_ *corev1.Pod) bool { return m.preEvictionFilterResult }
func (m *mockEvictor) Evict(_ context.Context, _ *corev1.Pod, _ framework.EvictOptions) bool {
	return false
}

func TestEvictorAdaptor_Filter(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}}

	tests := []struct {
		name         string
		filterResult bool
		want         bool
	}{
		{name: "filter allows", filterResult: true, want: true},
		{name: "filter denies", filterResult: false, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &evictorAdaptor{evictor: &mockEvictor{filterResult: tc.filterResult}}
			assert.Equal(t, tc.want, a.Filter(pod))
		})
	}
}

func TestEvictorAdaptor_PreEvictionFilter(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}}

	tests := []struct {
		name                    string
		filterResult            bool
		preEvictionFilterResult bool
		want                    bool
	}{
		{
			// Key test case: pod is structurally evictable (Filter=true)
			// but NodeFit says no room on other nodes (PreEvictionFilter=false).
			// The adaptor must return false, not the Filter value.
			name:                    "passes Filter but fails PreEvictionFilter (NodeFit blocked)",
			filterResult:            true,
			preEvictionFilterResult: false,
			want:                    false,
		},
		{
			name:                    "passes both Filter and PreEvictionFilter",
			filterResult:            true,
			preEvictionFilterResult: true,
			want:                    true,
		},
		{
			name:                    "fails both Filter and PreEvictionFilter",
			filterResult:            false,
			preEvictionFilterResult: false,
			want:                    false,
		},
		{
			// Inverse: structurally blocked but pre-eviction says ok.
			// Unusual in practice but the adaptor must still delegate faithfully.
			name:                    "fails Filter but passes PreEvictionFilter",
			filterResult:            false,
			preEvictionFilterResult: true,
			want:                    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &evictorAdaptor{
				evictor: &mockEvictor{
					filterResult:            tc.filterResult,
					preEvictionFilterResult: tc.preEvictionFilterResult,
				},
			}
			assert.Equal(t, tc.want, a.PreEvictionFilter(pod))
		})
	}
}
