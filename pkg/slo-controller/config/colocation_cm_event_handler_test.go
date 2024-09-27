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

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func Test_syncColocationConfigIfChanged(t *testing.T) {
	sloconfig.ClearDefaultColocationExtension()
	oldCfg := *sloconfig.NewDefaultColocationCfg()
	oldCfg.MemoryReclaimThresholdPercent = pointer.Int64(40)
	memoryCalcPolicyByUsage := configuration.CalculateByPodUsage
	memoryCalcPolicyByRequest := configuration.CalculateByPodRequest
	cpuCalcPolicyByUsage := configuration.CalculateByPodUsage
	cpuCalcPolicyNew := configuration.CalculatePolicy("")
	var defaultNodeMemoryCollectPolicy = slov1alpha1.UsageWithoutPageCache

	type fields struct {
		config *colocationCfgCache
	}
	type args struct {
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name        string
		fields      fields
		args        args
		wantChanged bool
		wantField   *colocationCfgCache
	}{
		{
			name:        "configmap is nil,cache have no old cfg,  cfg will be changed to use default cfg",
			fields:      fields{config: &colocationCfgCache{}},
			args:        args{configMap: nil},
			wantChanged: true,
			wantField:   &colocationCfgCache{colocationCfg: *sloconfig.NewDefaultColocationCfg(), available: true, errorStatus: true},
		},
		{
			name:        "configmap is nil,cache have old cfg,  cfg will be changed to use default cfg",
			fields:      fields{config: &colocationCfgCache{colocationCfg: oldCfg}},
			args:        args{configMap: nil},
			wantChanged: true,
			wantField:   &colocationCfgCache{colocationCfg: *sloconfig.NewDefaultColocationCfg(), available: true, errorStatus: true},
		},
		{
			name:        "configmap is nil, cache has been set default cfg ,so not changed",
			fields:      fields{config: &colocationCfgCache{colocationCfg: *sloconfig.NewDefaultColocationCfg()}},
			args:        args{configMap: nil},
			wantChanged: false,
			wantField:   &colocationCfgCache{colocationCfg: *sloconfig.NewDefaultColocationCfg(), available: true, errorStatus: true},
		},
		{
			name:   "no colocation config in configmap, cache have no old cfg, cfg will be changed to use default cfg",
			fields: fields{config: &colocationCfgCache{}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{},
			}},
			wantChanged: true,
			wantField:   &colocationCfgCache{colocationCfg: *sloconfig.NewDefaultColocationCfg(), available: true, errorStatus: false},
		},
		{
			name:   "no colocation config in configmap, cache have old cfg, cfg will be changed to use default cfg",
			fields: fields{config: &colocationCfgCache{colocationCfg: oldCfg}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{},
			}},
			wantChanged: true,
			wantField:   &colocationCfgCache{colocationCfg: *sloconfig.NewDefaultColocationCfg(), available: true, errorStatus: false},
		},
		{
			name:   "no colocation config in configmap, cache has been set default cfg ,so not changed",
			fields: fields{config: &colocationCfgCache{colocationCfg: *sloconfig.NewDefaultColocationCfg()}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{},
			}},
			wantChanged: false,
			wantField:   &colocationCfgCache{colocationCfg: *sloconfig.NewDefaultColocationCfg(), available: true, errorStatus: false},
		},
		{
			name:   "unmarshal failed, for cache unavailable",
			fields: fields{config: &colocationCfgCache{}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: "{\"metricAggregateDurationSeconds\":10, \"invalidKey\":\"invalidValue\",}",
				},
			}},
			wantChanged: false,
			wantField:   &colocationCfgCache{errorStatus: true},
		},
		{
			name: "unmarshal failed, keep the old",
			fields: fields{config: &colocationCfgCache{
				colocationCfg: oldCfg,
				available:     true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: "{\"metricAggregateDurationSeconds\":10, \"invalidKey\":\"invalidValue\",}",
				},
			}},
			wantChanged: false,
			wantField: &colocationCfgCache{
				colocationCfg: oldCfg,
				available:     true,
				errorStatus:   true,
			},
		},
		{
			name: "validate and merge partial config with the default",
			fields: fields{config: &colocationCfgCache{
				colocationCfg: oldCfg,
				available:     true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: "{\"metricAggregateDurationSeconds\":60,\"cpuReclaimThresholdPercent\":70," +
						"\"memoryReclaimThresholdPercent\":70,\"updateTimeThresholdSeconds\":100,\"metricReportIntervalSeconds\":20}",
				},
			}},
			wantChanged: true,
			wantField: &colocationCfgCache{
				colocationCfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(false),
						MetricAggregateDurationSeconds: pointer.Int64(60),
						MetricReportIntervalSeconds:    pointer.Int64(20),
						MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
						MemoryReclaimThresholdPercent:  pointer.Int64(70),
						MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
						DegradeTimeMinutes:             pointer.Int64(15),
						UpdateTimeThresholdSeconds:     pointer.Int64(100),
						ResourceDiffThreshold:          pointer.Float64(0.1),
						MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
						MidCPUThresholdPercent:         pointer.Int64(100),
						MidMemoryThresholdPercent:      pointer.Int64(100),
						MidUnallocatedPercent:          pointer.Int64(0),
					},
				},
				available:   true,
				errorStatus: false,
			},
		},
		{
			name:   "got invalid partial config, if restart and will unavailable",
			fields: fields{config: &colocationCfgCache{}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: "{\"metricAggregateDurationSeconds\":60,\"cpuReclaimThresholdPercent\":70," +
						"\"memoryReclaimThresholdPercent\":70,\"updateTimeThresholdSeconds\":-1,\"metricReportIntervalSeconds\":20}",
				},
			}},
			wantChanged: false,
			wantField:   &colocationCfgCache{errorStatus: true},
		},
		{
			name: "got invalid partial config, keep the old",
			fields: fields{config: &colocationCfgCache{
				colocationCfg: oldCfg,
				available:     true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: "{\"metricAggregateDurationSeconds\":60,\"cpuReclaimThresholdPercent\":70," +
						"\"memoryReclaimThresholdPercent\":70,\"updateTimeThresholdSeconds\":-1,\"metricReportIntervalSeconds\":20}",
				},
			}},
			wantChanged: false,
			wantField: &colocationCfgCache{
				colocationCfg: oldCfg,
				available:     true,
				errorStatus:   true,
			},
		},
		{
			name: "node config invalid, use cluster config",
			fields: fields{config: &colocationCfgCache{
				colocationCfg: oldCfg,
				available:     true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":30,\"metricReportIntervalSeconds\":20," +
						"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
						"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
						"{\"matchLabels\":{\"xxx\":\"yyy\"}},\"metricAggregateDurationSeconds\":-1}]}",
				},
			}},
			wantChanged: true,
			wantField: &colocationCfgCache{
				colocationCfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(true),
						MetricAggregateDurationSeconds: pointer.Int64(30),
						MetricReportIntervalSeconds:    pointer.Int64(20),
						MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
						MemoryReclaimThresholdPercent:  pointer.Int64(80),
						MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
						DegradeTimeMinutes:             pointer.Int64(5),
						UpdateTimeThresholdSeconds:     pointer.Int64(300),
						ResourceDiffThreshold:          pointer.Float64(0.1),
						MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
						MidCPUThresholdPercent:         pointer.Int64(100),
						MidMemoryThresholdPercent:      pointer.Int64(100),
						MidUnallocatedPercent:          pointer.Int64(0),
					},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
							},
							ColocationStrategy: configuration.ColocationStrategy{
								Enable:                         pointer.Bool(true),
								MetricAggregateDurationSeconds: pointer.Int64(30),
								MetricReportIntervalSeconds:    pointer.Int64(20),
								MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
								CPUReclaimThresholdPercent:     pointer.Int64(70),
								CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
								MemoryReclaimThresholdPercent:  pointer.Int64(80),
								MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
								DegradeTimeMinutes:             pointer.Int64(5),
								UpdateTimeThresholdSeconds:     pointer.Int64(300),
								ResourceDiffThreshold:          pointer.Float64(0.1),
								MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
								MidCPUThresholdPercent:         pointer.Int64(100),
								MidMemoryThresholdPercent:      pointer.Int64(100),
								MidUnallocatedPercent:          pointer.Int64(0),
							},
						},
					},
				},
				available:   true,
				errorStatus: false,
			},
		},
		{
			name: "only cluster config change successfully, set available",
			fields: fields{config: &colocationCfgCache{
				colocationCfg: oldCfg,
				available:     false,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60,\"metricReportIntervalSeconds\":20," +
						"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
						"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"metricReportIntervalSeconds\":20}",
				},
			}},
			wantChanged: true,
			wantField: &colocationCfgCache{
				colocationCfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(true),
						MetricAggregateDurationSeconds: pointer.Int64(60),
						MetricReportIntervalSeconds:    pointer.Int64(20),
						MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
						MemoryReclaimThresholdPercent:  pointer.Int64(80),
						MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
						DegradeTimeMinutes:             pointer.Int64(5),
						UpdateTimeThresholdSeconds:     pointer.Int64(300),
						ResourceDiffThreshold:          pointer.Float64(0.1),
						MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
						MidCPUThresholdPercent:         pointer.Int64(100),
						MidMemoryThresholdPercent:      pointer.Int64(100),
						MidUnallocatedPercent:          pointer.Int64(0),
					},
				},
				available:   true,
				errorStatus: false,
			},
		},
		{
			name: "only cluster config with no change",
			fields: fields{config: &colocationCfgCache{
				colocationCfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(true),
						MetricAggregateDurationSeconds: pointer.Int64(60),
						MetricReportIntervalSeconds:    pointer.Int64(60),
						MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
						MemoryReclaimThresholdPercent:  pointer.Int64(80),
						MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
						DegradeTimeMinutes:             pointer.Int64(5),
						UpdateTimeThresholdSeconds:     pointer.Int64(300),
						ResourceDiffThreshold:          pointer.Float64(0.1),
						MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
						MidCPUThresholdPercent:         pointer.Int64(100),
						MidMemoryThresholdPercent:      pointer.Int64(100),
						MidUnallocatedPercent:          pointer.Int64(0),
					},
				},
				available: true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60,\"metricAggregateDurationSeconds\":60," +
						"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
						"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
				},
			}},
			wantChanged: false,
			wantField: &colocationCfgCache{
				colocationCfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(true),
						MetricAggregateDurationSeconds: pointer.Int64(60),
						MetricReportIntervalSeconds:    pointer.Int64(60),
						MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
						MemoryReclaimThresholdPercent:  pointer.Int64(80),
						MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
						DegradeTimeMinutes:             pointer.Int64(5),
						UpdateTimeThresholdSeconds:     pointer.Int64(300),
						ResourceDiffThreshold:          pointer.Float64(0.1),
						MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
						MidCPUThresholdPercent:         pointer.Int64(100),
						MidMemoryThresholdPercent:      pointer.Int64(100),
						MidUnallocatedPercent:          pointer.Int64(0),
					},
				},
				available:   true,
				errorStatus: false,
			},
		},
		{
			name: "full config change successfully",
			fields: fields{config: &colocationCfgCache{
				colocationCfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(true),
						MetricAggregateDurationSeconds: pointer.Int64(60),
						MetricReportIntervalSeconds:    pointer.Int64(60),
						MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						MemoryReclaimThresholdPercent:  pointer.Int64(80),
						DegradeTimeMinutes:             pointer.Int64(5),
						UpdateTimeThresholdSeconds:     pointer.Int64(300),
						ResourceDiffThreshold:          pointer.Float64(0.1),
						MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
					},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
							},
							ColocationStrategy: configuration.ColocationStrategy{
								Enable:                         pointer.Bool(true),
								MetricAggregateDurationSeconds: pointer.Int64(60),
								MetricReportIntervalSeconds:    pointer.Int64(60),
								MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
								CPUReclaimThresholdPercent:     pointer.Int64(70),
								MemoryReclaimThresholdPercent:  pointer.Int64(80),
								DegradeTimeMinutes:             pointer.Int64(5),
								UpdateTimeThresholdSeconds:     pointer.Int64(300),
								ResourceDiffThreshold:          pointer.Float64(0.1),
								MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
							},
						},
					},
				},
				available:   true,
				errorStatus: false,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: `
{
    "enable": true,
    "metricAggregateDurationSeconds": 60,
    "metricReportIntervalSeconds": 20,
    "cpuReclaimThresholdPercent": 70,
    "memoryReclaimThresholdPercent": 80,
    "updateTimeThresholdSeconds": 300,
    "degradeTimeMinutes": 5,
    "resourceDiffThreshold": 0.1,
    "midCPUThresholdPercent": 45,
    "midMemoryThresholdPercent": 65,
	"midUnallocatedPercent": 50,
    "nodeConfigs": [{
        "nodeSelector": {
            "matchLabels": {
                "xxx": "yyy"
            }
        },
        "enable": true
    }]
}
`,
				},
			}},
			wantChanged: true,
			wantField: &colocationCfgCache{
				colocationCfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(true),
						MetricAggregateDurationSeconds: pointer.Int64(60),
						MetricReportIntervalSeconds:    pointer.Int64(20),
						MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
						MemoryReclaimThresholdPercent:  pointer.Int64(80),
						MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
						DegradeTimeMinutes:             pointer.Int64(5),
						UpdateTimeThresholdSeconds:     pointer.Int64(300),
						ResourceDiffThreshold:          pointer.Float64(0.1),
						MidCPUThresholdPercent:         pointer.Int64(45),
						MidMemoryThresholdPercent:      pointer.Int64(65),
						MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
						MidUnallocatedPercent:          pointer.Int64(50),
					},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
							},
							ColocationStrategy: configuration.ColocationStrategy{
								Enable:                         pointer.Bool(true),
								MetricAggregateDurationSeconds: pointer.Int64(60),
								MetricReportIntervalSeconds:    pointer.Int64(20),
								MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
								CPUReclaimThresholdPercent:     pointer.Int64(70),
								CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
								MemoryReclaimThresholdPercent:  pointer.Int64(80),
								MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
								DegradeTimeMinutes:             pointer.Int64(5),
								UpdateTimeThresholdSeconds:     pointer.Int64(300),
								ResourceDiffThreshold:          pointer.Float64(0.1),
								MidCPUThresholdPercent:         pointer.Int64(45),
								MidMemoryThresholdPercent:      pointer.Int64(65),
								MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
								MidUnallocatedPercent:          pointer.Int64(50),
							},
						},
					},
				},
				available:   true,
				errorStatus: false,
			},
		},
		{
			name: "node config with change",
			fields: fields{config: &colocationCfgCache{
				colocationCfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(true),
						MetricAggregateDurationSeconds: pointer.Int64(60),
						MetricReportIntervalSeconds:    pointer.Int64(60),
						MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
						MemoryReclaimThresholdPercent:  pointer.Int64(80),
						MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
						DegradeTimeMinutes:             pointer.Int64(5),
						UpdateTimeThresholdSeconds:     pointer.Int64(300),
						ResourceDiffThreshold:          pointer.Float64(0.1),
						MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
					},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"aaa": "bbbb",
									},
								},
								Name: "aaa-bbbb",
							},
							ColocationStrategy: configuration.ColocationStrategy{
								Enable: pointer.Bool(true),
							},
						},
					},
				},
				available: true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{
					configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":30,\"metricReportIntervalSeconds\":20," +
						"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"memoryCalculatePolicy\":\"request\"," +
						"\"updateTimeThresholdSeconds\":300," +
						"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
						"{\"matchLabels\":{\"xxx\":\"yyy\"}},\"name\":\"xxx-yyy\",\"enable\":true,\"cpuReclaimThresholdPercent\":60, \"cpuCalculatePolicy\": \"\"}]}",
				},
			}},
			wantChanged: true,
			wantField: &colocationCfgCache{
				colocationCfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(true),
						MetricAggregateDurationSeconds: pointer.Int64(30),
						MetricReportIntervalSeconds:    pointer.Int64(20),
						MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
						MemoryReclaimThresholdPercent:  pointer.Int64(80),
						MemoryCalculatePolicy:          &memoryCalcPolicyByRequest,
						DegradeTimeMinutes:             pointer.Int64(5),
						UpdateTimeThresholdSeconds:     pointer.Int64(300),
						ResourceDiffThreshold:          pointer.Float64(0.1),
						MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
						MidCPUThresholdPercent:         pointer.Int64(100),
						MidMemoryThresholdPercent:      pointer.Int64(100),
						MidUnallocatedPercent:          pointer.Int64(0),
					},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
								Name: "xxx-yyy",
							},
							ColocationStrategy: configuration.ColocationStrategy{
								Enable:                         pointer.Bool(true),
								MetricAggregateDurationSeconds: pointer.Int64(30),
								MetricReportIntervalSeconds:    pointer.Int64(20),
								MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
								MemoryReclaimThresholdPercent:  pointer.Int64(80),
								MemoryCalculatePolicy:          &memoryCalcPolicyByRequest,
								DegradeTimeMinutes:             pointer.Int64(5),
								UpdateTimeThresholdSeconds:     pointer.Int64(300),
								ResourceDiffThreshold:          pointer.Float64(0.1),
								MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
								MidCPUThresholdPercent:         pointer.Int64(100),
								MidMemoryThresholdPercent:      pointer.Int64(100),
								MidUnallocatedPercent:          pointer.Int64(0),
								//change
								CPUReclaimThresholdPercent: pointer.Int64(60),
								CPUCalculatePolicy:         &cpuCalcPolicyNew,
							},
						},
					},
				},
				available:   true,
				errorStatus: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
			p := NewColocationHandlerForConfigMapEvent(fakeClient, *sloconfig.NewDefaultColocationCfg(), &record.FakeRecorder{})
			p.cfgCache = colocationCfgCache{available: tt.fields.config.available, colocationCfg: tt.fields.config.colocationCfg}
			got := p.syncColocationCfgIfChanged(tt.args.configMap)
			assert.Equal(t, tt.wantChanged, got)
			assert.Equal(t, tt.wantField.available, p.cfgCache.available)
			assert.Equal(t, tt.wantField.errorStatus, p.cfgCache.errorStatus)
			assert.Equal(t, tt.wantField.colocationCfg, p.cfgCache.colocationCfg)
		})
	}
}

func Test_IsCfgAvailable(t *testing.T) {
	defaultConfig := sloconfig.DefaultColocationCfg()
	memoryCalcPolicyByUsage := configuration.CalculateByPodUsage
	cpuCalcPolicyByUsage := configuration.CalculateByPodUsage
	var defaultNodeMemoryCollectPolicy = slov1alpha1.UsageWithoutPageCache
	type fields struct {
		config    *colocationCfgCache
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name      string
		fields    fields
		want      bool
		wantField *configuration.ColocationCfg
	}{
		{
			name: "directly return available",
			fields: fields{
				config: &colocationCfgCache{
					available: true,
				},
			},
			want:      true,
			wantField: &configuration.ColocationCfg{},
		},
		{
			name: "set default when config is not found",
			fields: fields{
				config: &colocationCfgCache{
					colocationCfg: configuration.ColocationCfg{
						ColocationStrategy: configuration.ColocationStrategy{
							MetricAggregateDurationSeconds: pointer.Int64(60),
						},
					},
					available: false,
				},
			},
			want:      true,
			wantField: &defaultConfig,
		},
		{
			name: "use cluster config",
			fields: fields{
				config: &colocationCfgCache{
					colocationCfg: configuration.ColocationCfg{
						ColocationStrategy: configuration.ColocationStrategy{
							MetricAggregateDurationSeconds: pointer.Int64(60),
						},
					},
					available: false,
				},
				configMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
					},
				},
			},
			want: true,
			wantField: &configuration.ColocationCfg{
				ColocationStrategy: configuration.ColocationStrategy{
					Enable:                         pointer.Bool(true),
					MetricAggregateDurationSeconds: pointer.Int64(60),
					MetricAggregatePolicy:          sloconfig.DefaultColocationStrategy().MetricAggregatePolicy,
					CPUReclaimThresholdPercent:     pointer.Int64(70),
					CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
					MemoryReclaimThresholdPercent:  pointer.Int64(80),
					MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
					DegradeTimeMinutes:             pointer.Int64(5),
					UpdateTimeThresholdSeconds:     pointer.Int64(300),
					ResourceDiffThreshold:          pointer.Float64(0.1),
					MetricReportIntervalSeconds:    pointer.Int64(60),
					MetricMemoryCollectPolicy:      &defaultNodeMemoryCollectPolicy,
					MidCPUThresholdPercent:         pointer.Int64(100),
					MidMemoryThresholdPercent:      pointer.Int64(100),
					MidUnallocatedPercent:          pointer.Int64(0),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
			if tt.fields.configMap != nil {
				fakeClient = fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.fields.configMap).Build()
			}
			p := NewColocationHandlerForConfigMapEvent(fakeClient, *sloconfig.NewDefaultColocationCfg(), &record.FakeRecorder{})
			p.cfgCache = colocationCfgCache{available: tt.fields.config.available, colocationCfg: tt.fields.config.colocationCfg}
			got := p.IsCfgAvailable()
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantField, &p.cfgCache.colocationCfg)
		})
	}
}
