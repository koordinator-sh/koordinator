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

package sloconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/configuration"
)

func Test_Colocation_NewCheckerInitStatus(t *testing.T) {
	//clusterOnly
	cfgClusterOnly := &configuration.ColocationCfg{
		ColocationStrategy: configuration.ColocationStrategy{
			MetricAggregateDurationSeconds: pointer.Int64(60),
			CPUReclaimThresholdPercent:     pointer.Int64(70),
			MemoryReclaimThresholdPercent:  pointer.Int64(70),
			UpdateTimeThresholdSeconds:     pointer.Int64(100),
		},
	}
	cfgClusterOnlyBytes, _ := json.Marshal(cfgClusterOnly)
	//nodeSelector is empty
	cfgHaveNodeInvalid := &configuration.ColocationCfg{
		ColocationStrategy: configuration.ColocationStrategy{
			Enable: pointer.Bool(true),
		},
		NodeConfigs: []configuration.NodeColocationCfg{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					Name: "xxx-yyy",
				},
				ColocationStrategy: configuration.ColocationStrategy{
					Enable:                     pointer.Bool(true),
					CPUReclaimThresholdPercent: pointer.Int64(60),
				},
			},
		},
	}
	cfgHaveNodeInvalidBytes, _ := json.Marshal(cfgHaveNodeInvalid)

	//valid node config
	cfgHaveNodeValid := &configuration.ColocationCfg{
		ColocationStrategy: configuration.ColocationStrategy{
			Enable:                         pointer.Bool(true),
			MetricAggregateDurationSeconds: pointer.Int64(30),
			CPUReclaimThresholdPercent:     pointer.Int64(70),
			MemoryReclaimThresholdPercent:  pointer.Int64(80),
			UpdateTimeThresholdSeconds:     pointer.Int64(300),
			DegradeTimeMinutes:             pointer.Int64(5),
			ResourceDiffThreshold:          pointer.Float64(0.1),
		},
		NodeConfigs: []configuration.NodeColocationCfg{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					Name: "xxx-yyy",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				ColocationStrategy: configuration.ColocationStrategy{
					Enable:                     pointer.Bool(true),
					CPUReclaimThresholdPercent: pointer.Int64(60),
				},
			},
		},
	}
	cfgHaveNodeValidBytes, _ := json.Marshal(cfgHaveNodeValid)
	nodeSelectorExpect, _ := metav1.LabelSelectorAsSelector(cfgHaveNodeValid.NodeConfigs[0].NodeCfgProfile.NodeSelector)

	type args struct {
		oldConfigMap  *corev1.ConfigMap
		configMap     *corev1.ConfigMap
		needUnmarshal bool
	}

	tests := []struct {
		name               string
		args               args
		wantCfg            *configuration.ColocationCfg
		wantProfileChecker NodeConfigProfileChecker
		wantStatus         string
	}{
		{
			name: "colocation Config is nil and notNeedInit",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{},
				},
				needUnmarshal: false,
			},
			wantCfg:            nil,
			wantProfileChecker: nil,
			wantStatus:         NotInit,
		},
		{
			name: "colocation Config is nil and needInit",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{},
				},
				needUnmarshal: true,
			},
			wantCfg:            nil,
			wantProfileChecker: nil,
			wantStatus:         "err",
		},
		{
			name: "config notChange and invalid and notNeedInit",
			args: args{
				oldConfigMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ColocationConfigKey: "invalid config",
					},
				},
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ColocationConfigKey: "invalid config",
					},
				},
				needUnmarshal: false,
			},
			wantCfg:            nil,
			wantProfileChecker: nil,
			wantStatus:         NotInit,
		},
		{
			name: "config changed but invalid and notNeedInit",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ColocationConfigKey: "invalid config",
					},
				},
				needUnmarshal: false,
			},
			wantCfg:            nil,
			wantProfileChecker: nil,
			wantStatus:         "err",
		},
		{
			name: "config valid and only clusterStrategy",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ColocationConfigKey: string(cfgClusterOnlyBytes),
					},
				},
			},
			wantCfg:            cfgClusterOnly,
			wantProfileChecker: &nodeConfigProfileChecker{cfgName: configuration.ColocationConfigKey},
			wantStatus:         InitSuccess,
		},
		{
			name: "config valid and only clusterStrategy2",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ColocationConfigKey: "{\"metricAggregateDurationSeconds\":60,\"cpuReclaimThresholdPercent\":70," +
							"\"memoryReclaimThresholdPercent\":70,\"updateTimeThresholdSeconds\":100,\"nodeConfigs\":[]}",
					},
				},
			},
			wantCfg: &configuration.ColocationCfg{
				ColocationStrategy: configuration.ColocationStrategy{
					MetricAggregateDurationSeconds: pointer.Int64(60),
					CPUReclaimThresholdPercent:     pointer.Int64(70),
					MemoryReclaimThresholdPercent:  pointer.Int64(70),
					UpdateTimeThresholdSeconds:     pointer.Int64(100),
				},
				NodeConfigs: []configuration.NodeColocationCfg{},
			},
			wantProfileChecker: &nodeConfigProfileChecker{cfgName: configuration.ColocationConfigKey},
			wantStatus:         InitSuccess,
		},
		{
			name: "config valid and have node strategy invalid",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ColocationConfigKey: string(cfgHaveNodeInvalidBytes),
					},
				},
			},
			wantCfg:            cfgHaveNodeInvalid,
			wantProfileChecker: nil,
			wantStatus:         "err",
		},
		{
			name: "config valid and have node strategy",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ColocationConfigKey: string(cfgHaveNodeValidBytes),
					},
				},
			},
			wantCfg: cfgHaveNodeValid,
			wantProfileChecker: &nodeConfigProfileChecker{
				cfgName: configuration.ColocationConfigKey,
				nodeConfigs: []profileCheckInfo{
					{
						profile:   cfgHaveNodeValid.NodeConfigs[0].NodeCfgProfile,
						selectors: nodeSelectorExpect,
					},
				},
			},
			wantStatus: InitSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewColocationConfigChecker(tt.args.oldConfigMap, tt.args.configMap, tt.args.needUnmarshal)
			gotInitStatus := checker.InitStatus()
			assert.True(t, strings.Contains(gotInitStatus, tt.wantStatus), "gotStatus:%s", gotInitStatus)
			assert.Equal(t, tt.wantCfg, checker.cfg)
			assert.Equal(t, tt.wantProfileChecker, checker.NodeConfigProfileChecker)
		})
	}
}

func Test_Colocation_ConfigContentsValid(t *testing.T) {

	type args struct {
		cfg configuration.ColocationCfg
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "cluster MetricAggregateDurationSeconds invalid",
			args: args{
				cfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						MetricAggregateDurationSeconds: pointer.Int64(0),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster CPUReclaimThresholdPercent invalid",
			args: args{
				cfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						CPUReclaimThresholdPercent: pointer.Int64(-1),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster MemoryReclaimThresholdPercent more than 100 is valid",
			args: args{
				cfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						MemoryReclaimThresholdPercent: pointer.Int64(101),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "cluster DegradeTimeMinutes invalid",
			args: args{
				cfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						DegradeTimeMinutes: pointer.Int64(0),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster UpdateTimeThresholdSeconds invalid",
			args: args{
				cfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						UpdateTimeThresholdSeconds: pointer.Int64(-1),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster ResourceDiffThreshold invalid",
			args: args{
				cfg: configuration.ColocationCfg{

					ColocationStrategy: configuration.ColocationStrategy{
						ResourceDiffThreshold: pointer.Float64(2),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "node ResourceDiffThreshold invalid",
			args: args{
				cfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							ColocationStrategy: configuration.ColocationStrategy{
								ResourceDiffThreshold: pointer.Float64(-1),
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "all is nil",
			args: args{
				cfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								Name: "testNode",
							},
							ColocationStrategy: configuration.ColocationStrategy{},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "config valid",
			args: args{
				cfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         pointer.Bool(true),
						MetricAggregateDurationSeconds: pointer.Int64(30),
						CPUReclaimThresholdPercent:     pointer.Int64(70),
						MemoryReclaimThresholdPercent:  pointer.Int64(80),
						UpdateTimeThresholdSeconds:     pointer.Int64(300),
						DegradeTimeMinutes:             pointer.Int64(5),
						ResourceDiffThreshold:          pointer.Float64(0.1),
					},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								Name: "testNode",
							},
							ColocationStrategy: configuration.ColocationStrategy{
								Enable:                         pointer.Bool(true),
								MetricAggregateDurationSeconds: pointer.Int64(30),
								CPUReclaimThresholdPercent:     pointer.Int64(70),
								MemoryReclaimThresholdPercent:  pointer.Int64(80),
								UpdateTimeThresholdSeconds:     pointer.Int64(300),
								DegradeTimeMinutes:             pointer.Int64(5),
								ResourceDiffThreshold:          pointer.Float64(0.1),
							},
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := ColocationConfigChecker{cfg: &tt.args.cfg}
			gotErr := checker.ConfigParamValid()
			if gotErr != nil {
				fmt.Println(gotErr.Error())
			}
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}
