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

package noderesource

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
)

func Test_syncColocationCfgIfChanged(t *testing.T) {
	type fields struct {
		config *Config
	}
	type args struct {
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name      string
		fields    fields
		args      args
		want      bool
		wantField *Config
	}{
		{
			name:      "configmap is nil, does not change",
			fields:    fields{config: &Config{}},
			args:      args{configMap: nil},
			want:      false,
			wantField: &Config{},
		},
		{
			name:   "no colocation config in configmap, abort the sync",
			fields: fields{config: &Config{}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{},
			}},
			want:      false,
			wantField: &Config{},
		},
		{
			name: "no colocation config in configmap, abort the sync, keep the old",
			fields: fields{config: &Config{
				ColocationCfg: *config.NewDefaultColocationCfg(),
			}},
			args: args{configMap: &corev1.ConfigMap{}},
			want: false,
			wantField: &Config{
				ColocationCfg: *config.NewDefaultColocationCfg(),
			},
		},
		{
			name: "unmarshal failed, keep the old",
			fields: fields{config: &Config{
				ColocationCfg: *config.NewDefaultColocationCfg(),
				isAvailable:   true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ColocationConfigKey: "{\"metricAggregateDurationSeconds\":10, \"invalidKey\":\"invalidValue\",}",
				},
			}},
			want: false,
			wantField: &Config{
				ColocationCfg: *config.NewDefaultColocationCfg(),
				isAvailable:   true,
			},
		},
		{
			name: "validate and merge partial config with the default",
			fields: fields{config: &Config{
				ColocationCfg: *config.NewDefaultColocationCfg(),
				isAvailable:   true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ColocationConfigKey: "{\"metricAggregateDurationSeconds\":60,\"cpuReclaimThresholdPercent\":70," +
						"\"memoryReclaimThresholdPercent\":70,\"updateTimeThresholdSeconds\":100}",
				},
			}},
			want: true,
			wantField: &Config{
				ColocationCfg: config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                         pointer.BoolPtr(false),
						MetricAggregateDurationSeconds: pointer.Int64Ptr(60),
						MetricReportIntervalSeconds:    pointer.Int64Ptr(60),
						CPUReclaimThresholdPercent:     pointer.Int64Ptr(70),
						MemoryReclaimThresholdPercent:  pointer.Int64Ptr(70),
						DegradeTimeMinutes:             pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:     pointer.Int64Ptr(100),
						ResourceDiffThreshold:          pointer.Float64Ptr(0.1),
					},
				},
				isAvailable: true,
			},
		},
		{
			name: "got invalid partial config, keep the old",
			fields: fields{config: &Config{
				ColocationCfg: *config.NewDefaultColocationCfg(),
				isAvailable:   true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ColocationConfigKey: "{\"metricAggregateDurationSeconds\":60,\"cpuReclaimThresholdPercent\":70," +
						"\"memoryReclaimThresholdPercent\":70,\"updateTimeThresholdSeconds\":-1}",
				},
			}},
			want: false,
			wantField: &Config{
				ColocationCfg: *config.NewDefaultColocationCfg(),
				isAvailable:   true,
			},
		},
		{
			name: "get full config successfully, set available",
			fields: fields{config: &Config{
				isAvailable: false,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
						"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
						"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
				},
			}},
			want: true,
			wantField: &Config{
				ColocationCfg: config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                         pointer.BoolPtr(true),
						MetricAggregateDurationSeconds: pointer.Int64Ptr(60),
						MetricReportIntervalSeconds:    pointer.Int64Ptr(60),
						CPUReclaimThresholdPercent:     pointer.Int64Ptr(70),
						MemoryReclaimThresholdPercent:  pointer.Int64Ptr(80),
						DegradeTimeMinutes:             pointer.Int64Ptr(5),
						UpdateTimeThresholdSeconds:     pointer.Int64Ptr(300),
						ResourceDiffThreshold:          pointer.Float64Ptr(0.1),
					},
				},
				isAvailable: true,
			},
		},
		{
			name: "get full config successfully with no change",
			fields: fields{config: &Config{
				ColocationCfg: config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                         pointer.BoolPtr(true),
						MetricAggregateDurationSeconds: pointer.Int64Ptr(60),
						MetricReportIntervalSeconds:    pointer.Int64Ptr(15),
						CPUReclaimThresholdPercent:     pointer.Int64Ptr(70),
						MemoryReclaimThresholdPercent:  pointer.Int64Ptr(80),
						DegradeTimeMinutes:             pointer.Int64Ptr(5),
						UpdateTimeThresholdSeconds:     pointer.Int64Ptr(300),
						ResourceDiffThreshold:          pointer.Float64Ptr(0.1),
					},
				},
				isAvailable: true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60,\"metricReportIntervalSeconds\":15," +
						"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
						"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
				},
			}},
			want: false,
			wantField: &Config{
				ColocationCfg: config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                         pointer.BoolPtr(true),
						MetricAggregateDurationSeconds: pointer.Int64Ptr(60),
						MetricReportIntervalSeconds:    pointer.Int64Ptr(15),
						CPUReclaimThresholdPercent:     pointer.Int64Ptr(70),
						MemoryReclaimThresholdPercent:  pointer.Int64Ptr(80),
						DegradeTimeMinutes:             pointer.Int64Ptr(5),
						UpdateTimeThresholdSeconds:     pointer.Int64Ptr(300),
						ResourceDiffThreshold:          pointer.Float64Ptr(0.1),
					},
				},
				isAvailable: true,
			},
		},
		{
			name: "get full config successfully with no change 1",
			fields: fields{config: &Config{
				ColocationCfg: config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                         pointer.BoolPtr(true),
						MetricAggregateDurationSeconds: pointer.Int64Ptr(60),
						MetricReportIntervalSeconds:    pointer.Int64Ptr(45),
						CPUReclaimThresholdPercent:     pointer.Int64Ptr(70),
						MemoryReclaimThresholdPercent:  pointer.Int64Ptr(80),
						DegradeTimeMinutes:             pointer.Int64Ptr(5),
						UpdateTimeThresholdSeconds:     pointer.Int64Ptr(300),
						ResourceDiffThreshold:          pointer.Float64Ptr(0.1),
					},
					NodeConfigs: []config.NodeColocationCfg{
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"xxx": "yyy",
								},
							},
							ColocationStrategy: config.ColocationStrategy{
								Enable: pointer.BoolPtr(true),
							},
						},
					},
				},
				isAvailable: true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60,\"metricReportIntervalSeconds\":45," +
						"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
						"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
						"{\"matchLabels\":{\"xxx\":\"yyy\"}},\"enable\":true}]}",
				},
			}},
			want: false,
			wantField: &Config{
				ColocationCfg: config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                         pointer.BoolPtr(true),
						MetricAggregateDurationSeconds: pointer.Int64Ptr(60),
						MetricReportIntervalSeconds:    pointer.Int64Ptr(45),
						CPUReclaimThresholdPercent:     pointer.Int64Ptr(70),
						MemoryReclaimThresholdPercent:  pointer.Int64Ptr(80),
						DegradeTimeMinutes:             pointer.Int64Ptr(5),
						UpdateTimeThresholdSeconds:     pointer.Int64Ptr(300),
						ResourceDiffThreshold:          pointer.Float64Ptr(0.1),
					},
					NodeConfigs: []config.NodeColocationCfg{
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"xxx": "yyy",
								},
							},
							ColocationStrategy: config.ColocationStrategy{
								Enable: pointer.BoolPtr(true),
							},
						},
					},
				},
				isAvailable: true,
			},
		},
		{
			name: "get node config successfully with change",
			fields: fields{config: &Config{
				ColocationCfg: config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                         pointer.BoolPtr(true),
						MetricAggregateDurationSeconds: pointer.Int64Ptr(60),
						CPUReclaimThresholdPercent:     pointer.Int64Ptr(70),
						MemoryReclaimThresholdPercent:  pointer.Int64Ptr(80),
						DegradeTimeMinutes:             pointer.Int64Ptr(5),
						UpdateTimeThresholdSeconds:     pointer.Int64Ptr(300),
						ResourceDiffThreshold:          pointer.Float64Ptr(0.1),
					},
					NodeConfigs: []config.NodeColocationCfg{
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"aaa": "bbbb",
								},
							},
							ColocationStrategy: config.ColocationStrategy{
								Enable: pointer.BoolPtr(true),
							},
						},
					},
				},
				isAvailable: true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":30," +
						"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
						"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
						"{\"matchLabels\":{\"xxx\":\"yyy\"}},\"enable\":true,\"cpuReclaimThresholdPercent\":60}]}",
				},
			}},
			want: true,
			wantField: &Config{
				ColocationCfg: config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                         pointer.BoolPtr(true),
						MetricAggregateDurationSeconds: pointer.Int64Ptr(30),
						MetricReportIntervalSeconds:    pointer.Int64Ptr(60),
						CPUReclaimThresholdPercent:     pointer.Int64Ptr(70),
						MemoryReclaimThresholdPercent:  pointer.Int64Ptr(80),
						DegradeTimeMinutes:             pointer.Int64Ptr(5),
						UpdateTimeThresholdSeconds:     pointer.Int64Ptr(300),
						ResourceDiffThreshold:          pointer.Float64Ptr(0.1),
					},
					NodeConfigs: []config.NodeColocationCfg{
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"xxx": "yyy",
								},
							},
							ColocationStrategy: config.ColocationStrategy{
								Enable:                     pointer.BoolPtr(true),
								CPUReclaimThresholdPercent: pointer.Int64Ptr(60),
							},
						},
					},
				},
				isAvailable: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := EnqueueRequestForConfigMap{Config: tt.fields.config}
			got := p.syncColocationCfgIfChanged(tt.args.configMap)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantField, p.Config)
		})
	}
}
