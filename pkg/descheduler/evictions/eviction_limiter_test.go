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

package evictions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func makeTestPod(namespace, name, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
		},
	}
}

type evictionTestCase struct {
	name string

	nodeLimit   *uint
	nsLimit     *uint
	totalLimit  *uint
	evictSeq    []*corev1.Pod
	expectAllow []bool

	expectedNodeCount map[string]uint
	expectedNsCount   map[string]uint
	expectedTotal     uint
}

func TestEvictionLimiter_TableDriven(t *testing.T) {
	tests := []evictionTestCase{
		{
			name: "no limits",
			evictSeq: []*corev1.Pod{
				makeTestPod("default", "pod-1", "node-1"),
				makeTestPod("kube-system", "pod-2", "node-2"),
			},
			expectAllow: []bool{true, true},
			expectedNodeCount: map[string]uint{
				"node-1": 1,
				"node-2": 1,
			},
			expectedNsCount: map[string]uint{
				"default":     1,
				"kube-system": 1,
			},
			expectedTotal: 2,
		},
		{
			name:      "node limit=1",
			nodeLimit: uintPtr(1),
			evictSeq: []*corev1.Pod{
				makeTestPod("default", "pod-1", "node-1"),
				makeTestPod("default", "pod-2", "node-1"),
			},
			expectAllow: []bool{true, false},
			expectedNodeCount: map[string]uint{
				"node-1": 1,
			},
			expectedNsCount: map[string]uint{
				"default": 1,
			},
			expectedTotal: 1,
		},
		{
			name:    "namespace limit=1",
			nsLimit: uintPtr(1),
			evictSeq: []*corev1.Pod{
				makeTestPod("default", "pod-1", "node-1"),
				makeTestPod("default", "pod-2", "node-2"),
			},
			expectAllow: []bool{true, false},
			expectedNodeCount: map[string]uint{
				"node-1": 1,
				"node-2": 0,
			},
			expectedNsCount: map[string]uint{
				"default": 1,
			},
			expectedTotal: 1,
		},
		{
			name:       "total limit=2",
			totalLimit: uintPtr(2),
			evictSeq: []*corev1.Pod{
				makeTestPod("default", "pod-1", "node-1"),
				makeTestPod("default", "pod-2", "node-1"),
				makeTestPod("kube-system", "pod-3", "node-2"),
			},
			expectAllow: []bool{true, true, false},
			expectedNodeCount: map[string]uint{
				"node-1": 2,
				"node-2": 0,
			},
			expectedNsCount: map[string]uint{
				"default":     2,
				"kube-system": 0,
			},
			expectedTotal: 2,
		},
		{
			name:       "all limits hit",
			nodeLimit:  uintPtr(1),
			nsLimit:    uintPtr(1),
			totalLimit: uintPtr(2),
			evictSeq: []*corev1.Pod{
				makeTestPod("default", "pod-1", "node-1"),
				makeTestPod("default", "pod-2", "node-1"),     // node limit hit
				makeTestPod("default", "pod-3", "node-2"),     // ns limit hit
				makeTestPod("kube-system", "pod-4", "node-1"), // total limit hit
			},
			expectAllow: []bool{true, false, false, false},
			expectedNodeCount: map[string]uint{
				"node-1": 1,
			},
			expectedNsCount: map[string]uint{
				"default": 1,
			},
			expectedTotal: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewEvictionLimiter(tt.nodeLimit, tt.nsLimit, tt.totalLimit)

			for i, pod := range tt.evictSeq {
				allowed := limiter.AllowEvict(pod)
				assert.Equal(t, tt.expectAllow[i], allowed)
				if allowed {
					limiter.Done(pod)
				}
			}

			for node, expected := range tt.expectedNodeCount {
				assert.Equal(t, expected, limiter.NodeEvicted(node))
			}

			for ns, expected := range tt.expectedNsCount {
				assert.Equal(t, expected, limiter.NamespaceEvicted(ns))
			}

			assert.Equal(t, tt.expectedTotal, limiter.TotalEvicted())
		})
	}
}

func uintPtr(i uint) *uint {
	return &i
}

func TestEvictionLimiter_NodeLimitExceeded(t *testing.T) {
	type testCase struct {
		name             string
		nodeLimit        *uint
		evictPods        []*corev1.Pod
		checkNodeName    string
		expectedExceeded bool
	}

	tests := []testCase{
		{
			name:             "no limit",
			nodeLimit:        nil,
			evictPods:        []*corev1.Pod{makeTestPod("default", "pod-1", "node-1")},
			checkNodeName:    "node-1",
			expectedExceeded: false,
		},
		{
			name:             "limit=1, 0 evicted",
			nodeLimit:        uintPtr(1),
			evictPods:        []*corev1.Pod{},
			checkNodeName:    "node-1",
			expectedExceeded: false,
		},
		{
			name:             "limit=1, 1 evicted",
			nodeLimit:        uintPtr(1),
			evictPods:        []*corev1.Pod{makeTestPod("default", "pod-1", "node-1")},
			checkNodeName:    "node-1",
			expectedExceeded: true,
		},
		{
			name:             "limit=2, 1 evicted",
			nodeLimit:        uintPtr(2),
			evictPods:        []*corev1.Pod{makeTestPod("default", "pod-1", "node-1")},
			checkNodeName:    "node-1",
			expectedExceeded: false,
		},
		{
			name:      "limit=2, 2 evicted",
			nodeLimit: uintPtr(2),
			evictPods: []*corev1.Pod{
				makeTestPod("default", "pod-1", "node-1"),
				makeTestPod("default", "pod-2", "node-1"),
			},
			checkNodeName:    "node-1",
			expectedExceeded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewEvictionLimiter(tt.nodeLimit, nil, nil)

			for _, pod := range tt.evictPods {
				if limiter.AllowEvict(pod) {
					limiter.Done(pod)
				}
			}

			exceeded := limiter.NodeLimitExceeded(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: tt.checkNodeName},
			})
			assert.Equal(t, tt.expectedExceeded, exceeded)
		})
	}
}

func TestEvictionLimiter_NamespaceLimitExceeded(t *testing.T) {
	type testCase struct {
		name             string
		nsLimit          *uint
		evictPods        []*corev1.Pod
		checkNs          string
		expectedExceeded bool
	}

	tests := []testCase{
		{
			name:             "no limit",
			nsLimit:          nil,
			evictPods:        []*corev1.Pod{makeTestPod("default", "pod-1", "node-1")},
			checkNs:          "default",
			expectedExceeded: false,
		},
		{
			name:             "limit=1, 0 evicted",
			nsLimit:          uintPtr(1),
			evictPods:        []*corev1.Pod{},
			checkNs:          "default",
			expectedExceeded: false,
		},
		{
			name:             "limit=1, 1 evicted",
			nsLimit:          uintPtr(1),
			evictPods:        []*corev1.Pod{makeTestPod("default", "pod-1", "node-1")},
			checkNs:          "default",
			expectedExceeded: true,
		},
		{
			name:             "limit=2, 1 evicted",
			nsLimit:          uintPtr(2),
			evictPods:        []*corev1.Pod{makeTestPod("default", "pod-1", "node-1")},
			checkNs:          "default",
			expectedExceeded: false,
		},
		{
			name:    "limit=2, 2 evicted",
			nsLimit: uintPtr(2),
			evictPods: []*corev1.Pod{
				makeTestPod("default", "pod-1", "node-1"),
				makeTestPod("default", "pod-2", "node-2"),
			},
			checkNs:          "default",
			expectedExceeded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewEvictionLimiter(nil, tt.nsLimit, nil)

			for _, pod := range tt.evictPods {
				if limiter.AllowEvict(pod) {
					limiter.Done(pod)
				}
			}

			exceeded := limiter.NamespaceLimitExceeded(tt.checkNs)
			assert.Equal(t, tt.expectedExceeded, exceeded)
		})
	}
}

func TestEvictionLimiter_Reset(t *testing.T) {
	limit := uint(1)
	limiter := NewEvictionLimiter(&limit, &limit, &limit)

	pod := makeTestPod("default", "pod-1", "node-1")
	if limiter.AllowEvict(pod) {
		limiter.Done(pod)
	}

	assert.True(t, limiter.NodeLimitExceeded(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}))
	assert.True(t, limiter.NamespaceLimitExceeded("default"))
	assert.Equal(t, uint(1), limiter.TotalEvicted())

	limiter.Reset()

	assert.False(t, limiter.NodeLimitExceeded(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}))
	assert.False(t, limiter.NamespaceLimitExceeded("default"))
	assert.Equal(t, uint(0), limiter.TotalEvicted())
}
