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

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/metrics/koordmanager"
)

func TestMustRegister(t *testing.T) {
	testMetricVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "test",
		Name:      "test_counter",
		Help:      "test counter",
	}, []string{StatusKey, ReasonKey})
	assert.NotPanics(t, func() {
		koordmanager.InternalMustRegister(testMetricVec)
	})

	testExternalMetricVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "test",
		Name:      "test_external_counter",
		Help:      "test counter",
	}, []string{StatusKey, ReasonKey})
	assert.NotPanics(t, func() {
		koordmanager.ExternalMustRegister(testExternalMetricVec)
	})
}

func TestCommonCollectors(t *testing.T) {
	testReason := "test"
	RecordNodeResourceReconcileCount(false, testReason)
	RecordNodeResourceReconcileCount(true, testReason)
	RecordNodeMetricReconcileCount(true, testReason)
	RecordNodeMetricSpecParseCount(true, testReason)
	RecordNodeSLOReconcileCount(true, testReason)
	RecordNodeSLOSpecParseCount(true, testReason)
}

func TestNodeResourceCollectors(t *testing.T) {
	testReason := "test"
	testPlugin := "testPlugin"
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				extension.BatchCPU:    resource.MustParse("30000"),
				extension.BatchMemory: resource.MustParse("60Gi"),
			},
		},
	}
	RecordNodeResourceRunPluginStatus(testPlugin, false, testReason)
	RecordNodeResourceRunPluginStatus(testPlugin, true, testReason)
	RecordNodeExtendedResourceAllocatableInternal(testNode, string(extension.BatchCPU), UnitInteger, 30000)
	RecordNodeExtendedResourceAllocatableInternal(testNode, string(extension.BatchMemory), UnitInteger, 60<<30)
}
