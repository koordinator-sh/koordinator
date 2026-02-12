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

package memoryevict

import (
	"context"
	"flag"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/component-base/featuregate"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	critesting "k8s.io/cri-api/pkg/apis/testing"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	maframework "github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	qosmanagerUtil "github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/util"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime/handler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/utils"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

type podMemSample struct {
	UID     string
	MemUsed resource.Quantity
}

func Test_memoryEvict_Enabled(t *testing.T) {
	m := memoryEvictor{}
	type args struct {
		BEMemoryEvictEnabled          bool
		MemoryEvictEnabled            bool
		MemoryAllocatableEvictEnabled bool
		evictInterval                 time.Duration
	}
	tests := []struct {
		name   string
		args   args
		expect bool
	}{
		{
			name: "BEMemoryEvictEnabled/MemoryEvictEnabled/MemoryAllocatableEvictEnabled=false",
			args: args{
				BEMemoryEvictEnabled:          false,
				MemoryEvictEnabled:            false,
				MemoryAllocatableEvictEnabled: false,
				evictInterval:                 10 * time.Second,
			},
			expect: false,
		},
		{
			name: "MemoryEvictEnabled=true",
			args: args{
				BEMemoryEvictEnabled:          false,
				MemoryEvictEnabled:            true,
				MemoryAllocatableEvictEnabled: false,
				evictInterval:                 10 * time.Second,
			},
			expect: true,
		},
		{
			name: "BEMemoryEvictEnabled=true",
			args: args{
				BEMemoryEvictEnabled:          true,
				MemoryEvictEnabled:            false,
				MemoryAllocatableEvictEnabled: false,
				evictInterval:                 10 * time.Second,
			},
			expect: true,
		},
		{
			name: "MemoryAllocatableEvictEnabled=true",
			args: args{
				BEMemoryEvictEnabled:          false,
				MemoryEvictEnabled:            false,
				MemoryAllocatableEvictEnabled: true,
				evictInterval:                 10 * time.Second,
			},
			expect: true,
		},
		{
			name: "evictInterval<0",
			args: args{
				BEMemoryEvictEnabled: false,
				MemoryEvictEnabled:   true,
				evictInterval:        -10 * time.Second,
			},
			expect: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.evictInterval = tt.args.evictInterval
			defer utilfeature.SetFeatureGateDuringTest(t, features.DefaultMutableKoordletFeatureGate, features.BEMemoryEvict, tt.args.BEMemoryEvictEnabled)()
			defer utilfeature.SetFeatureGateDuringTest(t, features.DefaultMutableKoordletFeatureGate, features.MemoryEvict, tt.args.MemoryEvictEnabled)()
			defer utilfeature.SetFeatureGateDuringTest(t, features.DefaultMutableKoordletFeatureGate, features.MemoryAllocatableEvict, tt.args.MemoryAllocatableEvictEnabled)()
			assert.Equal(t, tt.expect, m.Enabled())
		})
	}

}

func Test_generateConfigCheck(t *testing.T) {
	tests := []struct {
		name            string
		feature         featuregate.Feature
		thresholdConfig *slov1alpha1.ResourceThresholdStrategy
		expectErr       error
	}{
		{
			name:            "BEMemoryEvict - ResourceThresholdStrategy nil",
			feature:         features.BEMemoryEvict,
			thresholdConfig: nil,
			expectErr:       fmt.Errorf("resourceThresholdStrategy not config"),
		},
		{
			name:    "BEMemoryEvict - MemoryEvictThresholdPercent nil",
			feature: features.BEMemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent: nil,
			},
			expectErr: fmt.Errorf("threshold percent is nil"),
		},
		{
			name:    "BEMemoryEvict - MemoryEvictThresholdPercent < 0",
			feature: features.BEMemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent: ptr.To[int64](-1),
			},
			expectErr: fmt.Errorf("threshold percent(-1) should equal or greater than 0"),
		},
		{
			name:    "BEMemoryEvict - MemoryEvictLowerPercent == nil",
			feature: features.BEMemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent: ptr.To[int64](80),
				MemoryEvictLowerPercent:     nil,
			},
			expectErr: nil,
		},
		{
			name:    "BEMemoryEvict - MemoryEvictLowerPercent > MemoryEvictThresholdPercent",
			feature: features.BEMemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent: ptr.To[int64](80),
				MemoryEvictLowerPercent:     ptr.To[int64](90),
			},
			expectErr: fmt.Errorf("lower percent(90) should less than threshold percent(80)"),
		},
		{
			name:    "BEMemoryEvict - valid config",
			feature: features.BEMemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent: ptr.To[int64](80),
				MemoryEvictLowerPercent:     ptr.To[int64](70),
			},
			expectErr: nil,
		},
		{
			name:            "MemoryEvict - ResourceThresholdStrategy nil",
			feature:         features.MemoryEvict,
			thresholdConfig: nil,
			expectErr:       fmt.Errorf("resourceThresholdStrategy not config"),
		},
		{
			name:    "MemoryEvict - MemoryEvictThresholdPercent nil",
			feature: features.MemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent: nil,
			},
			expectErr: fmt.Errorf("threshold percent is nil"),
		},
		{
			name:    "MemoryEvict - MemoryEvictThresholdPercent < 0",
			feature: features.MemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent: ptr.To[int64](-1),
			},
			expectErr: fmt.Errorf("threshold percent(-1) should equal or greater than 0"),
		},
		{
			name:    "MemoryEvict - EvictEnabledPriorityThreshold nil",
			feature: features.MemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent:   ptr.To[int64](80),
				MemoryEvictLowerPercent:       ptr.To[int64](70),
				EvictEnabledPriorityThreshold: nil,
			},
			expectErr: fmt.Errorf("EvictEnabledPriorityThreshold not config"),
		},
		{
			name:    "MemoryEvict - MemoryEvictLowerPercent == MemoryEvictThresholdPercent",
			feature: features.MemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent:   ptr.To[int64](80),
				MemoryEvictLowerPercent:       ptr.To[int64](80),
				EvictEnabledPriorityThreshold: ptr.To[int32](apiext.PriorityBatchValueMax),
			},
			expectErr: fmt.Errorf("lower percent(80) should less than threshold percent(80)"),
		},
		{
			name:    "MemoryEvict - valid config",
			feature: features.MemoryEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryEvictThresholdPercent:   ptr.To[int64](85),
				MemoryEvictLowerPercent:       ptr.To[int64](75),
				EvictEnabledPriorityThreshold: ptr.To[int32](apiext.PriorityBatchValueMax),
			},
			expectErr: nil,
		},
		{
			name:            "MemoryAllocatableEvict - ResourceThresholdStrategy nil",
			feature:         features.MemoryAllocatableEvict,
			thresholdConfig: nil,
			expectErr:       fmt.Errorf("ResourceThresholdStrategy not config"),
		},
		{
			name:    "MemoryAllocatableEvict - MemoryAllocatableEvictThresholdPercent nil",
			feature: features.MemoryAllocatableEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryAllocatableEvictThresholdPercent: nil,
			},
			expectErr: fmt.Errorf("MemoryAllocatableEvictThresholdPercent not config"),
		},
		{
			name:    "MemoryAllocatableEvict - MemoryAllocatableEvictThresholdPercent < 0",
			feature: features.MemoryAllocatableEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](-1),
			},
			expectErr: fmt.Errorf("threshold percent(-1) should greater than 0"),
		},
		{
			name:    "MemoryAllocatableEvict - AllocatableEvictPriorityThreshold nil",
			feature: features.MemoryAllocatableEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](80),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](70),
				AllocatableEvictPriorityThreshold:      nil,
			},
			expectErr: fmt.Errorf("AllocatableEvictPriorityThreshold not config"),
		},
		{
			name:    "MemoryAllocatableEvict - AllocatableEvictPriorityThreshold > PriorityMidValueDefault",
			feature: features.MemoryAllocatableEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](80),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](70),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityMidValueMax + 1),
			},
			expectErr: fmt.Errorf("priorityThresholdPercent(%v) should less than %v, koor-prod pods should not be killed", apiext.PriorityMidValueMax+1, apiext.PriorityMidValueMax),
		},
		{
			name:    "MemoryAllocatableEvict - valid config",
			feature: features.MemoryAllocatableEvict,
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](85),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](75),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityBatchValueMax),
			},
			expectErr: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkFunc := generateConfigCheck(tt.feature)
			if checkFunc == nil {
				t.Errorf("generateConfigCheck() returned nil for feature %v", tt.feature)
				return
			}
			err := checkFunc(tt.thresholdConfig)
			assert.Equal(t, tt.expectErr, err)
		})
	}
}

func Test_memoryEvict(t *testing.T) {
	klog.InitFlags(nil)
	flag.Set("v", "5")
	flag.Set("logtostderr", "true")

	flag.Parse()

	defer func() {
		klog.Flush()
	}()
	type args struct {
		name               string
		triggerFeatures    []featuregate.Feature
		node               *corev1.Node
		nodeMemUsed        resource.Quantity
		podMetrics         []podMemSample
		pods               []*corev1.Pod
		thresholdConfig    *slov1alpha1.ResourceThresholdStrategy
		expectEvictPods    []*corev1.Pod
		expectNotEvictPods []*corev1.Pod
	}

	tests := []args{
		{
			name: "test_memoryevict_no_thresholdConfig",
		},
		{
			name: "test_MemoryEvictThresholdPercent_not_valid",
			// invalid
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: ptr.To[int64](-1)},
		},
		{
			name:            "test_nodeMetric_nil",
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: ptr.To[int64](80)},
		},
		{
			name:            "test_node_nil",
			nodeMemUsed:     resource.MustParse("115G"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: ptr.To[int64](80)},
		},
		{
			name:            "test_node_memorycapacity_invalid",
			node:            testutil.MockTestNode("80", "0"),
			nodeMemUsed:     resource.MustParse("115G"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{MemoryEvictThresholdPercent: ptr.To[int64](80)},
		},
		{
			name: "test_memory_under_evict_line",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("80G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("20G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("4G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("8G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("8G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      ptr.To[bool](true),
				MemoryEvictThresholdPercent: ptr.To[int64](80),
			},
			expectEvictPods: []*corev1.Pod{},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name:            "only feature BEMemoryEvict: MemoryEvictThresholdPercent_sort_by_priority_and_usage_82",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      ptr.To[bool](true),
				MemoryEvictThresholdPercent: ptr.To[int64](82),
			}, // >96G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name:            "only feature BEMemoryEvict: MemoryEvictThresholdPercent_80",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      ptr.To[bool](true),
				MemoryEvictThresholdPercent: ptr.To[int64](80),
			}, // >91.2G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name:            "only feature BEMemoryEvict: MemoryEvictThresholdPercent_50",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      ptr.To[bool](true),
				MemoryEvictThresholdPercent: ptr.To[int64](50),
			}, // >60G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
			},
		},
		{
			name:            "only feature BEMemoryEvict: MemoryEvictLowerPercent_80",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      ptr.To[bool](true),
				MemoryEvictThresholdPercent: ptr.To[int64](82),
				MemoryEvictLowerPercent:     ptr.To[int64](80),
			}, // >96G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name:            "only feature BEMemoryEvict: MemoryEvictLowerPercent_78",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      ptr.To[bool](true),
				MemoryEvictThresholdPercent: ptr.To[int64](82),
				MemoryEvictLowerPercent:     ptr.To[int64](78),
			}, // >93.6G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
		},
		{
			name:            "only feature BEMemoryEvict: MemoryEvictLowerPercent_74",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      ptr.To[bool](true),
				MemoryEvictThresholdPercent: ptr.To[int64](82),
				MemoryEvictLowerPercent:     ptr.To[int64](74),
			}, // >88.8G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
			},
		},
		{
			name:            "only feature MemoryAllocatableEvict: MemoryAllocatableEvictThresholdPercent_80,MemoryAllocatableEvictLowerPercent_70",
			triggerFeatures: []featuregate.Feature{features.MemoryAllocatableEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPodWithPriorityAnnotationLabels("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}, nil, map[string]string{apiext.LabelPodEvictEnabled: "true"}),
				createMemoryEvictTestPodWithPriorityAnnotationLabels("batch-pod-2", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}, nil, map[string]string{apiext.LabelPodEvictEnabled: "true"}),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "batch-pod-1", MemUsed: resource.MustParse("10G")},
				{UID: "batch-pod-2", MemUsed: resource.MustParse("20G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                                 ptr.To[bool](true),
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](80),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](70),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityBatchValueMin),
			},
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPodWithPriorityAnnotationLabels("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}, nil, map[string]string{apiext.LabelPodEvictEnabled: "true"}),
				createMemoryEvictTestPodWithPriorityAnnotationLabels("batch-pod-2", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}, nil, map[string]string{apiext.LabelPodEvictEnabled: "true"}),
			},
			expectNotEvictPods: []*corev1.Pod{},
		},
		{
			name:            "feature BEMemoryEvict/MemoryEvict: MemoryEvictLowerPercent_74 BEMemoryEvict ok, MemoryEvict invalid configuration",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict, features.MemoryEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      ptr.To[bool](true),
				MemoryEvictThresholdPercent: ptr.To[int64](82),
				MemoryEvictLowerPercent:     ptr.To[int64](74),
			}, // >88.8G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
			},
		},
		{
			name:            "feature BEMemoryEvict/MemoryEvict: MemoryEvictLowerPercent_74 BEMemoryEvict/MemoryEvict ok",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict, features.MemoryEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        ptr.To[bool](true),
				MemoryEvictThresholdPercent:   ptr.To[int64](82),
				MemoryEvictLowerPercent:       ptr.To[int64](74),
				EvictEnabledPriorityThreshold: ptr.To[int32](3000),
			}, // >88.8G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
			},
		},
		{
			name:            "feature BEMemoryEvict/MemoryEvict/MemoryAllocatableEvict: MemoryEvictLowerPercent_74 BEMemoryEvict/MemoryEvict ok, MemoryAllocatableEvict invalid configuration",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict, features.MemoryEvict, features.MemoryAllocatableEvict},
			node:            testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        ptr.To[bool](true),
				MemoryEvictThresholdPercent:   ptr.To[int64](82),
				MemoryEvictLowerPercent:       ptr.To[int64](74),
				EvictEnabledPriorityThreshold: ptr.To[int32](3000),
			}, // >88.8G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
			},
		},
		{
			name:            "feature BEMemoryEvict/MemoryEvict/MemoryAllocatableEvict: MemoryEvictLowerPercent_74 BEMemoryEvict/MemoryEvict ok, MemoryAllocatableEvict invalid configuration",
			triggerFeatures: []featuregate.Feature{features.BEMemoryEvict, features.MemoryEvict, features.MemoryAllocatableEvict},
			node: testutil.MockTestNodeWithExtendResource("80", "120G", corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.DecimalSI),
			}, corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.DecimalSI),
			}),
			pods: []*corev1.Pod{
				// following pods: mock no request
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
				// following pods: requested
				createMemoryEvictTestPodWithPriorityAnnotationLabels("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}, nil, map[string]string{apiext.LabelPodEvictEnabled: "true"}),
				createMemoryEvictTestPodWithPriorityAnnotationLabels("batch-pod-2", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}, nil, map[string]string{apiext.LabelPodEvictEnabled: "true"}),
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_lsr_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_ls_pod", MemUsed: resource.MustParse("30G")},
				{UID: "test_noqos_pod", MemUsed: resource.MustParse("10G")},
				{UID: "test_be_pod_priority100_1", MemUsed: resource.MustParse("5G")},
				{UID: "test_be_pod_priority100_2", MemUsed: resource.MustParse("20G")},
				{UID: "test_be_pod_priority120", MemUsed: resource.MustParse("10G")},
				{UID: "batch-pod-1", MemUsed: resource.MustParse("10G")},
				{UID: "batch-pod-2", MemUsed: resource.MustParse("20G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                                 ptr.To[bool](true),
				MemoryEvictThresholdPercent:            ptr.To[int64](82),
				MemoryEvictLowerPercent:                ptr.To[int64](74),
				EvictEnabledPriorityThreshold:          ptr.To[int32](3000),
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](45),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](40),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityBatchValueMin),
			}, // >88.8G
			expectEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_be_pod_priority100_2", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority100_1", apiext.QoSBE, 100),
				createMemoryEvictTestPod("test_be_pod_priority120", apiext.QoSBE, 120),
				createMemoryEvictTestPodWithPriorityAnnotationLabels("batch-pod-2", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}, nil, map[string]string{apiext.LabelPodEvictEnabled: "true"}),
			},
			expectNotEvictPods: []*corev1.Pod{
				createMemoryEvictTestPod("test_lsr_pod", apiext.QoSLSR, 1000),
				createMemoryEvictTestPod("test_ls_pod", apiext.QoSLS, 500),
				createMemoryEvictTestPod("test_noqos_pod", apiext.QoSNone, 100),
				createMemoryEvictTestPodWithPriorityAnnotationLabels("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}, nil, map[string]string{apiext.LabelPodEvictEnabled: "true"}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			if tt.name == "test_memoryevict_MemoryEvictThresholdPercent_sort_by_priority_and_usage_82" {
				t.Log("00000")
			}
			for _, f := range tt.triggerFeatures {
				defer utilfeature.SetFeatureGateDuringTest(t, features.DefaultMutableKoordletFeatureGate, f, true)()
			}
			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas(tt.pods)).AnyTimes()
			mockStatesInformer.EXPECT().GetNode().Return(tt.node).AnyTimes()
			mockStatesInformer.EXPECT().GetNodeSLO().Return(testutil.GetNodeSLOByThreshold(tt.thresholdConfig)).AnyTimes()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			nodeMemQueryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			result := mock_metriccache.NewMockAggregateResult(ctl)
			result.EXPECT().Value(gomock.Any()).Return(float64(tt.nodeMemUsed.Value()), nil).AnyTimes()
			result.EXPECT().Count().Return(1).AnyTimes()
			mockResultFactory.EXPECT().New(nodeMemQueryMeta).Return(result).AnyTimes()
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)
			mockQuerier.EXPECT().QueryAndClose(nodeMemQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			for _, podMetric := range tt.podMetrics {
				result := mock_metriccache.NewMockAggregateResult(ctl)
				result.EXPECT().Value(gomock.Any()).Return(float64(podMetric.MemUsed.Value()), nil).AnyTimes()
				result.EXPECT().Count().Return(1).AnyTimes()
				podQueryMeta, err := metriccache.PodMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(podMetric.UID))
				assert.NoError(t, err)
				mockResultFactory.EXPECT().New(podQueryMeta).Return(result).AnyTimes()
				mockQuerier.EXPECT().QueryAndClose(podQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			}

			fakeRecorder := &testutil.FakeRecorder{}
			client := clientsetfake.NewSimpleClientset()
			stop := make(chan struct{})
			evictor := qosmanagerUtil.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
			evictor.Start(stop)
			defer func() { stop <- struct{}{} }()

			runtime.DockerHandler = handler.NewFakeRuntimeHandler()

			var containers []*critesting.FakeContainer
			for _, pod := range tt.pods {
				_, err := client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err, "createPod ERROR!")
				for _, containerStatus := range pod.Status.ContainerStatuses {
					_, containerId, _ := util.ParseContainerId(containerStatus.ContainerID)
					fakeContainer := &critesting.FakeContainer{
						SandboxID:       string(pod.UID),
						ContainerStatus: runtimeapi.ContainerStatus{Id: containerId},
					}
					containers = append(containers, fakeContainer)
				}
			}
			runtime.DockerHandler.(*handler.FakeRuntimeHandler).SetFakeContainers(containers)

			opt := &framework.Options{
				StatesInformer:      mockStatesInformer,
				MetricCache:         mockMetricCache,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			m := New(opt)
			memoryEvictor := m.(*memoryEvictor)
			memoryEvictor.Setup(&framework.Context{Evictor: evictor, OnlyEvictByAPI: true})
			memoryEvictor.lastEvictTime = time.Now().Add(-30 * time.Second)
			memoryEvictor.memoryEvict()

			// evict subresource will not be creat or update in client go testing, check evict object
			// https://github.com/kubernetes/client-go/blob/v0.28.7/testing/fixture.go#L117
			for _, pod := range tt.expectEvictPods {
				assert.True(t, memoryEvictor.evictExecutor.IsPodEvicted(pod))
			}

			for _, pod := range tt.expectNotEvictPods {
				assert.False(t, memoryEvictor.evictExecutor.IsPodEvicted(pod))
			}
		})
	}
}

func createMemoryEvictTestPodWithLabels(name string, qosClass apiext.QoSClass, priority int32, labels map[string]string, phase corev1.PodPhase) *corev1.Pod {
	pod := createMemoryEvictTestPod(name, qosClass, priority)
	pod.Labels = utils.MergeMap(pod.Labels, labels)
	pod.Status.Phase = phase
	return pod
}
func createMemoryEvictTestPod(name string, qosClass apiext.QoSClass, priority int32) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID(name),
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qosClass),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: fmt.Sprintf("%s_%s", name, "main"),
				},
			},
			Priority: &priority,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        fmt.Sprintf("%s_%s", name, "main"),
					ContainerID: fmt.Sprintf("docker://%s_%s", name, "main"),
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{StartedAt: metav1.Time{Time: time.Now()}},
					},
				},
			},
		},
	}
}

func Test_getPodEvictInfoAndSortByPriority(t *testing.T) {
	pod0 := createMemoryEvictTestPodWithLabels("test_failed_pod", apiext.QoSLSR, 1000, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "1000"}, corev1.PodFailed)
	pod1 := createMemoryEvictTestPodWithLabels("test_evictDisabled_pod", apiext.QoSLSR, 1000, map[string]string{apiext.LabelPodEvictEnabled: "false", apiext.LabelPodPriority: "1000"}, corev1.PodRunning)
	pod2 := createMemoryEvictTestPodWithLabels("test_higher_priority", apiext.QoSLS, 3400, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "1000"}, corev1.PodRunning)
	pod3 := createMemoryEvictTestPodWithLabels("test_podPriority_120_2000", apiext.QoSNone, 120, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "2000"}, corev1.PodRunning)
	pod4 := createMemoryEvictTestPodWithLabels("test_podPriority_120_3000", apiext.QoSBE, 120, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "3000"}, corev1.PodRunning)
	pod5 := createMemoryEvictTestPodWithLabels("test_podPriority_120_4000", apiext.QoSBE, 120, map[string]string{apiext.LabelPodEvictEnabled: "true", apiext.LabelPodPriority: "4000"}, corev1.PodRunning)
	pod6 := createMemoryEvictTestPodWithLabels("test_podPriority_100_9999", apiext.QoSBE, 100, map[string]string{apiext.LabelPodEvictEnabled: "true"}, corev1.PodRunning)
	pod7 := createMemoryEvictTestPodWithLabels("test_podPriority_100_9999_1", apiext.QoSBE, 100, map[string]string{apiext.LabelPodEvictEnabled: "true"}, corev1.PodRunning)
	tests := []struct {
		name                string
		pods                []*corev1.Pod
		node                *corev1.Node
		nodeMemUsed         resource.Quantity
		thresholdConfig     *slov1alpha1.ResourceThresholdStrategy
		podMetrics          []podMemSample
		expectPodEvictNames []string
	}{
		{
			name: "test_memoryevict_MemoryEvictThresholdPercent_80",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				pod0, pod1, pod2, pod3, pod4, pod5, pod6, pod7,
			},
			nodeMemUsed: resource.MustParse("115G"),
			podMetrics: []podMemSample{
				{UID: "test_evictDisabled_pod", MemUsed: resource.MustParse("40G")},
				{UID: "test_higher_priority", MemUsed: resource.MustParse("30G")},
				{UID: "test_podPriority_120_2000", MemUsed: resource.MustParse("10G")},
				{UID: "test_podPriority_120_3000", MemUsed: resource.MustParse("5G")},
				{UID: "test_podPriority_120_4000", MemUsed: resource.MustParse("20G")},
				{UID: "test_podPriority_100_9999", MemUsed: resource.MustParse("10G")},
				{UID: "test_podPriority_100_9999_1", MemUsed: resource.MustParse("11G")},
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        ptr.To[bool](true),
				MemoryEvictThresholdPercent:   ptr.To[int64](80),
				EvictEnabledPriorityThreshold: ptr.To[int32](3000),
			}, // >91.2G
			expectPodEvictNames: []string{"test_podPriority_100_9999_1", "test_podPriority_100_9999", "test_podPriority_120_2000", "test_podPriority_120_3000", "test_podPriority_120_4000"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas(tt.pods)).AnyTimes()
			mockStatesInformer.EXPECT().GetNode().Return(tt.node).AnyTimes()
			mockStatesInformer.EXPECT().GetNodeSLO().Return(testutil.GetNodeSLOByThreshold(tt.thresholdConfig)).AnyTimes()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			nodeMemQueryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			result := mock_metriccache.NewMockAggregateResult(ctl)
			result.EXPECT().Value(gomock.Any()).Return(float64(tt.nodeMemUsed.Value()), nil).AnyTimes()
			result.EXPECT().Count().Return(1).AnyTimes()
			mockResultFactory.EXPECT().New(nodeMemQueryMeta).Return(result).AnyTimes()
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)
			mockQuerier.EXPECT().QueryAndClose(nodeMemQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			for _, podMetric := range tt.podMetrics {
				result := mock_metriccache.NewMockAggregateResult(ctl)
				result.EXPECT().Value(gomock.Any()).Return(float64(podMetric.MemUsed.Value()), nil).AnyTimes()
				result.EXPECT().Count().Return(1).AnyTimes()
				podQueryMeta, err := metriccache.PodMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(podMetric.UID))
				assert.NoError(t, err)
				mockResultFactory.EXPECT().New(podQueryMeta).Return(result).AnyTimes()
				mockQuerier.EXPECT().QueryAndClose(podQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			}

			fakeRecorder := &testutil.FakeRecorder{}
			client := clientsetfake.NewSimpleClientset()
			stop := make(chan struct{})
			evictor := qosmanagerUtil.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
			evictor.Start(stop)
			defer func() { stop <- struct{}{} }()

			runtime.DockerHandler = handler.NewFakeRuntimeHandler()

			var containers []*critesting.FakeContainer
			for _, pod := range tt.pods {
				_, err := client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err, "createPod ERROR!")
				for _, containerStatus := range pod.Status.ContainerStatuses {
					_, containerId, _ := util.ParseContainerId(containerStatus.ContainerID)
					fakeContainer := &critesting.FakeContainer{
						SandboxID:       string(pod.UID),
						ContainerStatus: runtimeapi.ContainerStatus{Id: containerId},
					}
					containers = append(containers, fakeContainer)
				}
			}
			runtime.DockerHandler.(*handler.FakeRuntimeHandler).SetFakeContainers(containers)

			opt := &framework.Options{
				StatesInformer:      mockStatesInformer,
				MetricCache:         mockMetricCache,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			m := New(opt)
			memoryEvictor := m.(*memoryEvictor)
			memoryEvictor.Setup(&framework.Context{Evictor: evictor, OnlyEvictByAPI: true})
			memoryEvictor.lastEvictTime = time.Now().Add(-30 * time.Second)
			res := memoryEvictor.getPodEvictInfoAndSortByPriority(string(features.BEMemoryEvict), *tt.thresholdConfig.EvictEnabledPriorityThreshold, memoryEvictor.statesInformer.GetAllPods(), func(a, b *qosmanagerUtil.PodEvictInfo) bool {
				return a.MemoryUsed > b.MemoryUsed
			})
			for k, podEvictInfo := range res {
				assert.Equal(t, tt.expectPodEvictNames[k], podEvictInfo.Pod.Name)
			}
		})
	}
}

func Test_calculateReleaseByUsedThresholdPercent(t *testing.T) {
	tests := []struct {
		name            string
		node            *corev1.Node
		nodeMemUsed     resource.Quantity
		thresholdConfig *slov1alpha1.ResourceThresholdStrategy
		expectRelease   int64
	}{
		{
			name:        "test_memoryevict_MemoryEvictThresholdPercent_lower78",
			node:        testutil.MockTestNode("80", "120G"),
			nodeMemUsed: resource.MustParse("115G"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        ptr.To[bool](true),
				MemoryEvictThresholdPercent:   ptr.To[int64](80),
				EvictEnabledPriorityThreshold: ptr.To[int32](3000),
			},
			expectRelease: 20400000000,
		},
		{
			name:        "test_memoryevict_MemoryEvictThresholdPercent_lower80",
			node:        testutil.MockTestNode("80", "120G"),
			nodeMemUsed: resource.MustParse("115G"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        ptr.To[bool](true),
				MemoryEvictThresholdPercent:   ptr.To[int64](80),
				MemoryEvictLowerPercent:       ptr.To[int64](80),
				EvictEnabledPriorityThreshold: ptr.To[int32](3000),
			},
			expectRelease: 18000000000,
		},
		{
			name:        "lower than threshold",
			node:        testutil.MockTestNode("80", "120G"),
			nodeMemUsed: resource.MustParse("80G"),
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                        ptr.To[bool](true),
				MemoryEvictThresholdPercent:   ptr.To[int64](80),
				MemoryEvictLowerPercent:       ptr.To[int64](80),
				EvictEnabledPriorityThreshold: ptr.To[int32](3000),
			},
			expectRelease: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockStatesInformer.EXPECT().GetNode().Return(tt.node).AnyTimes()
			mockStatesInformer.EXPECT().GetNodeSLO().Return(testutil.GetNodeSLOByThreshold(tt.thresholdConfig)).AnyTimes()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			nodeMemQueryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			result := mock_metriccache.NewMockAggregateResult(ctl)
			result.EXPECT().Value(gomock.Any()).Return(float64(tt.nodeMemUsed.Value()), nil).AnyTimes()
			result.EXPECT().Count().Return(1).AnyTimes()
			mockResultFactory.EXPECT().New(nodeMemQueryMeta).Return(result).AnyTimes()
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)
			mockQuerier.EXPECT().QueryAndClose(nodeMemQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			fakeRecorder := &testutil.FakeRecorder{}
			client := clientsetfake.NewSimpleClientset()
			stop := make(chan struct{})
			evictor := qosmanagerUtil.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
			evictor.Start(stop)
			defer func() { stop <- struct{}{} }()

			runtime.DockerHandler = handler.NewFakeRuntimeHandler()

			opt := &framework.Options{
				StatesInformer:      mockStatesInformer,
				MetricCache:         mockMetricCache,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			m := New(opt)
			memoryEvictor := m.(*memoryEvictor)
			memoryEvictor.Setup(&framework.Context{Evictor: evictor, OnlyEvictByAPI: true})
			memoryEvictor.lastEvictTime = time.Now().Add(-30 * time.Second)
			res, _ := memoryEvictor.calculateReleaseByUsedThresholdPercent(tt.thresholdConfig, tt.node, nil)
			if len(res) == 0 {
				assert.Equal(t, tt.expectRelease, int64(0), "checkRelease")
			} else {
				q, _ := res[corev1.ResourceMemory]
				assert.Equal(t, tt.expectRelease, q.Value(), "checkRelease")
			}
		})
	}
}

func Test_calculateReleaseByAllocatableThresholdPercent(t *testing.T) {
	tests := []struct {
		name                   string
		node                   *corev1.Node
		pods                   []*corev1.Pod
		thresholdConfig        *slov1alpha1.ResourceThresholdStrategy
		expectRelease          corev1.ResourceList
		expectReleaseResources []corev1.ResourceName
		expectPodResourceList  map[string]corev1.ResourceList
	}{
		{
			name: "allocatable threshold not exceeded",
			node: testutil.MockTestNodeWithExtendResource("80", "120G", corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}, corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}),
			pods: []*corev1.Pod{
				createMemoryEvictTestPodWithPriority("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(20*1024*1024*1024, resource.BinarySI),
				}),
				createMemoryEvictTestPodWithPriority("batch-pod-2", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(30*1024*1024*1024, resource.BinarySI),
				}),
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                                 ptr.To[bool](true),
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](80),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](70),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityBatchValueMax),
			},
			expectRelease:          corev1.ResourceList{},
			expectReleaseResources: []corev1.ResourceName{},
			expectPodResourceList: map[string]corev1.ResourceList{
				"batch-pod-1": nil,
				"batch-pod-2": nil,
			},
		},
		{
			name: "allocatable threshold exceeded - batch memory",
			node: testutil.MockTestNodeWithExtendResource("80", "120G", corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}, corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}),
			pods: []*corev1.Pod{
				createMemoryEvictTestPodWithPriority("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(45*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(45*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(45*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(45*1024*1024*1024, resource.BinarySI),
				}),
				createMemoryEvictTestPodWithPriority("batch-pod-2", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
				}),
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                                 ptr.To[bool](true),
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](80),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](70),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityBatchValueMax),
			},
			expectRelease: corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(16106131456, resource.BinarySI),
			},
			expectReleaseResources: []corev1.ResourceName{apiext.BatchMemory},
			expectPodResourceList: map[string]corev1.ResourceList{
				"batch-pod-1": {
					apiext.BatchMemory: *resource.NewQuantity(45*1024*1024*1024, resource.BinarySI),
				},
				"batch-pod-2": {
					apiext.BatchMemory: *resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
				},
			},
		},
		{
			name: "allocatable threshold exceeded - mixed priority",
			node: testutil.MockTestNodeWithExtendResource("80", "120G", corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				apiext.MidMemory:   *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}, corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				apiext.MidMemory:   *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}),
			pods: []*corev1.Pod{
				createMemoryEvictTestPodWithPriority("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				}),
				createMemoryEvictTestPodWithPriority("mid-pod-1", apiext.QoSLS, 7000, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
					apiext.MidMemory:      *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
					apiext.MidMemory:      *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				}),
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                                 ptr.To[bool](true),
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](80),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](70),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityMidValueMax),
			},
			expectRelease: corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(16106131456, resource.BinarySI),
				apiext.MidMemory:   *resource.NewQuantity(16106131456, resource.BinarySI),
			},
			expectReleaseResources: []corev1.ResourceName{apiext.BatchMemory, apiext.MidMemory},
			expectPodResourceList: map[string]corev1.ResourceList{
				"batch-pod-1": {
					apiext.BatchMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				},
				"mid-pod-1": {
					apiext.MidMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				},
			},
		},
		{
			name: "allocatable threshold exceeded - mixed priority, lower AllocatableEvictPriorityThreshold",
			node: testutil.MockTestNodeWithExtendResource("80", "120G", corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				apiext.MidMemory:   *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}, corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
				apiext.MidMemory:   *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}),
			pods: []*corev1.Pod{
				createMemoryEvictTestPodWithPriority("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				}),
				createMemoryEvictTestPodWithPriority("mid-pod-1", apiext.QoSLS, 7000, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
					apiext.MidMemory:      *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
					apiext.MidMemory:      *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				}),
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                                 ptr.To[bool](true),
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](80),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](70),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityBatchValueMax),
			},
			expectRelease: corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(16106131456, resource.BinarySI),
			},
			expectReleaseResources: []corev1.ResourceName{apiext.BatchMemory},
			expectPodResourceList: map[string]corev1.ResourceList{
				"batch-pod-1": {
					apiext.BatchMemory: *resource.NewQuantity(85*1024*1024*1024, resource.BinarySI),
				},
			},
		},
		{
			name: "allocatable threshold = 0",
			node: testutil.MockTestNodeWithExtendResource("80", "120G", corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}, corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(100*1024*1024*1024, resource.BinarySI),
			}),
			pods: []*corev1.Pod{
				createMemoryEvictTestPodWithPriority("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
				}),
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                                 ptr.To[bool](true),
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](0),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](0),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityBatchValueMax),
			},
			expectRelease: corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
			},
			expectReleaseResources: []corev1.ResourceName{apiext.BatchMemory},
			expectPodResourceList: map[string]corev1.ResourceList{
				"batch-pod-1": {
					apiext.BatchMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
				},
			},
		},
		{
			name: "node without allocatable batch-memory resource",
			node: testutil.MockTestNode("80", "120G"),
			pods: []*corev1.Pod{
				createMemoryEvictTestPodWithPriority("batch-pod-1", apiext.QoSBE, apiext.PriorityBatchValueMin, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
				}, corev1.ResourceList{
					corev1.ResourceMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					apiext.BatchMemory:    *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
				}),
			},
			thresholdConfig: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                                 ptr.To[bool](true),
				MemoryAllocatableEvictThresholdPercent: ptr.To[int64](80),
				MemoryAllocatableEvictLowerPercent:     ptr.To[int64](70),
				AllocatableEvictPriorityThreshold:      ptr.To[int32](apiext.PriorityBatchValueMax),
			},
			expectRelease: corev1.ResourceList{
				apiext.BatchMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
			},
			expectReleaseResources: []corev1.ResourceName{apiext.BatchMemory},
			expectPodResourceList: map[string]corev1.ResourceList{
				"batch-pod-1": {
					apiext.BatchMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()

			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas(tt.pods)).AnyTimes()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)

			fakeRecorder := &testutil.FakeRecorder{}
			client := clientsetfake.NewSimpleClientset()
			stop := make(chan struct{})
			evictor := qosmanagerUtil.NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
			evictor.Start(stop)
			defer func() { stop <- struct{}{} }()

			runtime.DockerHandler = handler.NewFakeRuntimeHandler()

			opt := &framework.Options{
				StatesInformer:      mockStatesInformer,
				MetricCache:         mockMetricCache,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			m := New(opt)
			memoryEvictor := m.(*memoryEvictor)
			memoryEvictor.Setup(&framework.Context{Evictor: evictor, OnlyEvictByAPI: true})
			memoryEvictor.lastEvictTime = time.Now().Add(-30 * time.Second)

			res, calculateFunc := memoryEvictor.calculateReleaseByAllocatableThresholdPercent(tt.thresholdConfig, tt.node, memoryEvictor.statesInformer.GetAllPods())

			if len(tt.expectRelease) == 0 {
				assert.Equal(t, len(res), 0, "checkRelease")
			} else {
				// Verify expected resources are present
				for _, expectedRes := range tt.expectReleaseResources {
					_, exists := res[expectedRes]
					assert.True(t, exists, "expected resource %s should exist in result", expectedRes)
				}

				// Verify release amounts
				for resName, expectedQuantity := range tt.expectRelease {
					actualQuantity, exists := res[resName]
					assert.True(t, exists, "resource %s should exist in result", resName)
					if exists {
						assert.Equal(t, expectedQuantity.Value(), actualQuantity.Value(), "release amount for %s should be equal", resName)
					}
				}
			}

			// Verify calculateFunc behavior
			if tt.expectPodResourceList != nil && calculateFunc != nil {
				for _, pod := range tt.pods {
					podInfo := &qosmanagerUtil.PodEvictInfo{Pod: pod}
					podResource := calculateFunc(podInfo)
					expectedResource := tt.expectPodResourceList[pod.Name]

					if expectedResource == nil {
						assert.Nil(t, podResource, "pod %s should return nil resource", pod.Name)
					} else {
						assert.NotNil(t, podResource, "pod %s should return resource", pod.Name)
						for resName, expectedQty := range expectedResource {
							actualQty, exists := podResource[resName]
							assert.True(t, exists, "pod %s should have resource %s", pod.Name, resName)
							if exists {
								assert.Equal(t, expectedQty.Value(), actualQty.Value(), "pod %s resource %s should match", pod.Name, resName)
							}
						}
					}
				}
			}
		})
	}
}

func createMemoryEvictTestPodWithPriorityAnnotationLabels(name string, qosClass apiext.QoSClass, priority int32,
	requests corev1.ResourceList, limits corev1.ResourceList, annotations, labels map[string]string) *corev1.Pod {
	pod := createMemoryEvictTestPodWithPriority(name, qosClass, priority, requests, limits)
	if pod.Labels != nil {
		for k, v := range labels {
			pod.Labels[k] = v
		}
	}
	if pod.Annotations != nil {
		for k, v := range annotations {
			pod.Annotations[k] = v
		}
	}
	return pod
}
func createMemoryEvictTestPodWithPriority(name string, qosClass apiext.QoSClass, priority int32,
	requests corev1.ResourceList, limits corev1.ResourceList) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      name,
			UID:       types.UID(name),
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qosClass),
			},
		},
		Spec: corev1.PodSpec{
			Priority: &priority,
			Containers: []corev1.Container{
				{
					Name: fmt.Sprintf("%s_container", name),
					Resources: corev1.ResourceRequirements{
						Requests: requests,
						Limits:   limits,
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	return pod
}
