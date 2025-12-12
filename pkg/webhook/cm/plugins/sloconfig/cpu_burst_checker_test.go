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
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func Test_CPUBurst_NewChecker_InitStatus(t *testing.T) {
	//clusterOnly
	cfgClusterOnly := &configuration.CPUBurstCfg{
		ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig: slov1alpha1.CPUBurstConfig{
				CPUBurstPercent:            ptr.To[int64](100),
				CFSQuotaBurstPercent:       ptr.To[int64](200),
				CFSQuotaBurstPeriodSeconds: ptr.To[int64](10000),
			},
		},
	}
	cfgClusterOnlyBytes, _ := json.Marshal(cfgClusterOnly)
	//nodeSelector is empty
	cfgHaveNodeInvalid := &configuration.CPUBurstCfg{
		ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig: slov1alpha1.CPUBurstConfig{
				CPUBurstPercent:            ptr.To[int64](100),
				CFSQuotaBurstPercent:       ptr.To[int64](200),
				CFSQuotaBurstPeriodSeconds: ptr.To[int64](10000),
			},
		},
		NodeStrategies: []configuration.NodeCPUBurstCfg{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					Name: "xxx-yyy",
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent:            ptr.To[int64](100),
						CFSQuotaBurstPercent:       ptr.To[int64](200),
						CFSQuotaBurstPeriodSeconds: ptr.To[int64](10000),
					},
				},
			},
		},
	}
	cfgHaveNodeInvalidBytes, _ := json.Marshal(cfgHaveNodeInvalid)

	//valid node config
	cfgHaveNodeValid := &configuration.CPUBurstCfg{
		ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig: slov1alpha1.CPUBurstConfig{
				CPUBurstPercent:            ptr.To[int64](100),
				CFSQuotaBurstPercent:       ptr.To[int64](200),
				CFSQuotaBurstPeriodSeconds: ptr.To[int64](10000),
			},
		},
		NodeStrategies: []configuration.NodeCPUBurstCfg{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					Name: "xxx-yyy",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent:            ptr.To[int64](100),
						CFSQuotaBurstPercent:       ptr.To[int64](200),
						CFSQuotaBurstPeriodSeconds: ptr.To[int64](10000),
					},
				},
			},
		},
	}
	cfgHaveNodeValidBytes, _ := json.Marshal(cfgHaveNodeValid)
	nodeSelectorExpect, _ := metav1.LabelSelectorAsSelector(cfgHaveNodeValid.NodeStrategies[0].NodeCfgProfile.NodeSelector)

	type args struct {
		oldConfigMap  *corev1.ConfigMap
		configMap     *corev1.ConfigMap
		needUnmarshal bool
	}

	tests := []struct {
		name               string
		args               args
		wantCfg            *configuration.CPUBurstCfg
		wantProfileChecker NodeConfigProfileChecker
		wantStatus         string
	}{
		{
			name: "config invalid, config is nil and notNeedInit",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{},
				},
			},
			wantCfg:            nil,
			wantProfileChecker: nil,
			wantStatus:         NotInit,
		},
		{
			name: "config invalid, config is nil and NeedInit",
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
			name: "config changed and invalid and notNeedInit",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.CPUBurstConfigKey: "invalid config",
					},
				},
			},
			wantCfg:            nil,
			wantProfileChecker: nil,
			wantStatus:         "err",
		},
		{
			name: "config not change and invalid and notNeedInit",
			args: args{
				oldConfigMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.CPUBurstConfigKey: "invalid config",
					},
				},
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.CPUBurstConfigKey: "invalid config",
					},
				},
			},
			wantCfg:            nil,
			wantProfileChecker: nil,
			wantStatus:         NotInit,
		},
		{
			name: "config valid and only clusterStrategy",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.CPUBurstConfigKey: string(cfgClusterOnlyBytes),
					},
				},
			},
			wantCfg:            cfgClusterOnly,
			wantProfileChecker: &nodeConfigProfileChecker{cfgName: configuration.CPUBurstConfigKey},
			wantStatus:         InitSuccess,
		},
		{
			name: "config valid and have node strategy invalid",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.CPUBurstConfigKey: string(cfgHaveNodeInvalidBytes),
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
						configuration.CPUBurstConfigKey: string(cfgHaveNodeValidBytes),
					},
				},
			},
			wantCfg: cfgHaveNodeValid,
			wantProfileChecker: &nodeConfigProfileChecker{
				cfgName: configuration.CPUBurstConfigKey,
				nodeConfigs: []profileCheckInfo{
					{
						profile:   cfgHaveNodeValid.NodeStrategies[0].NodeCfgProfile,
						selectors: nodeSelectorExpect,
					},
				},
			},
			wantStatus: InitSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := NewCPUBurstChecker(tt.args.oldConfigMap, tt.args.configMap, tt.args.needUnmarshal)
			gotInitStatus := checker.InitStatus()
			assert.True(t, strings.Contains(gotInitStatus, tt.wantStatus), "gotStatus:%s", gotInitStatus)
			assert.Equal(t, tt.wantCfg, checker.cfg)
			assert.Equal(t, tt.wantProfileChecker, checker.NodeConfigProfileChecker)
		})
	}
}

func Test_CPUBurst_ConfigContentsValid(t *testing.T) {

	type args struct {
		cfg configuration.CPUBurstCfg
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "cluster CPUBurstPercent invalid",
			args: args{
				cfg: configuration.CPUBurstCfg{
					ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
						CPUBurstConfig: slov1alpha1.CPUBurstConfig{
							CPUBurstPercent: ptr.To[int64](0),
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster CFSQuotaBurstPercent invalid",
			args: args{
				cfg: configuration.CPUBurstCfg{
					ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
						CPUBurstConfig: slov1alpha1.CPUBurstConfig{
							CFSQuotaBurstPercent: ptr.To[int64](-1),
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster CFSQuotaBurstPeriodSeconds invalid",
			args: args{
				cfg: configuration.CPUBurstCfg{
					ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
						CPUBurstConfig: slov1alpha1.CPUBurstConfig{
							CFSQuotaBurstPeriodSeconds: ptr.To[int64](-2),
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "node CFSQuotaBurstPercent invalid",
			args: args{
				cfg: configuration.CPUBurstCfg{
					ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
						CPUBurstConfig: slov1alpha1.CPUBurstConfig{},
					},
					NodeStrategies: []configuration.NodeCPUBurstCfg{
						{
							CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
								CPUBurstConfig: slov1alpha1.CPUBurstConfig{
									CFSQuotaBurstPeriodSeconds: ptr.To[int64](-2),
								},
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
				cfg: configuration.CPUBurstCfg{
					ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
						CPUBurstConfig: slov1alpha1.CPUBurstConfig{},
					},
					NodeStrategies: []configuration.NodeCPUBurstCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								Name: "testNode",
							},
							CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
								CPUBurstConfig: slov1alpha1.CPUBurstConfig{},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "config valid",
			args: args{
				cfg: configuration.CPUBurstCfg{
					ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
						CPUBurstConfig: slov1alpha1.CPUBurstConfig{
							CPUBurstPercent:            ptr.To[int64](100),
							CFSQuotaBurstPercent:       ptr.To[int64](200),
							CFSQuotaBurstPeriodSeconds: ptr.To[int64](10000),
						},
					},
					NodeStrategies: []configuration.NodeCPUBurstCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								Name: "xxx-yyy",
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
							},
							CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
								CPUBurstConfig: slov1alpha1.CPUBurstConfig{
									CPUBurstPercent:            ptr.To[int64](100),
									CFSQuotaBurstPercent:       ptr.To[int64](200),
									CFSQuotaBurstPeriodSeconds: ptr.To[int64](10000),
								},
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
			checker := CPUBurstChecker{cfg: &tt.args.cfg}
			gotErr := checker.ConfigParamValid()
			if gotErr != nil {
				fmt.Println(gotErr.Error())
			}
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}
