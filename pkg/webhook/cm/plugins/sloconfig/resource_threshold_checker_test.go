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
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func Test_ResourceThreshold_NewChecker_InitStatus(t *testing.T) {
	//clusterOnly
	cfgClusterOnly := &configuration.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.Bool(true),
			CPUSuppressThresholdPercent: pointer.Int64(60),
		},
	}
	cfgClusterOnlyBytes, _ := json.Marshal(cfgClusterOnly)
	//nodeSelector is empty
	cfgHaveNodeInvalid := &configuration.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.Bool(true),
			CPUSuppressThresholdPercent: pointer.Int64(60),
		},
		NodeStrategies: []configuration.NodeResourceThresholdStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					Name: "xxx-yyy",
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      pointer.Bool(true),
					CPUSuppressThresholdPercent: pointer.Int64(60),
				},
			},
		},
	}
	cfgHaveNodeInvalidBytes, _ := json.Marshal(cfgHaveNodeInvalid)

	//valid node config
	cfgHaveNodeValid := &configuration.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.Bool(true),
			CPUSuppressThresholdPercent: pointer.Int64(60),
		},
		NodeStrategies: []configuration.NodeResourceThresholdStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					Name: "xxx-yyy",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      pointer.Bool(true),
					CPUSuppressThresholdPercent: pointer.Int64(60),
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
		wantCfg            *configuration.ResourceThresholdCfg
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
						configuration.ResourceThresholdConfigKey: "invalid config",
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
						configuration.ResourceThresholdConfigKey: "invalid config",
					},
				},
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ResourceThresholdConfigKey: "invalid config",
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
						configuration.ResourceThresholdConfigKey: string(cfgClusterOnlyBytes),
					},
				},
			},
			wantCfg:            cfgClusterOnly,
			wantProfileChecker: &nodeConfigProfileChecker{cfgName: configuration.ResourceThresholdConfigKey},
			wantStatus:         InitSuccess,
		},
		{
			name: "config valid and have node strategy invalid",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ResourceThresholdConfigKey: string(cfgHaveNodeInvalidBytes),
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
						configuration.ResourceThresholdConfigKey: string(cfgHaveNodeValidBytes),
					},
				},
			},
			wantCfg: cfgHaveNodeValid,
			wantProfileChecker: &nodeConfigProfileChecker{
				cfgName: configuration.ResourceThresholdConfigKey,
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
			checker := NewResourceThresholdChecker(tt.args.oldConfigMap, tt.args.configMap, tt.args.needUnmarshal)
			gotInitStatus := checker.InitStatus()
			assert.True(t, strings.Contains(gotInitStatus, tt.wantStatus), "gotStatus:%s", gotInitStatus)
			assert.Equal(t, tt.wantCfg, checker.cfg)
			assert.Equal(t, tt.wantProfileChecker, checker.NodeConfigProfileChecker)
		})
	}
}

func Test_ResourceThreshold_ConfigContentsValid(t *testing.T) {

	type args struct {
		cfg configuration.ResourceThresholdCfg
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "cluster CPUSuppressThresholdPercent invalid",
			args: args{
				cfg: configuration.ResourceThresholdCfg{
					ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
						Enable:                      pointer.Bool(true),
						CPUSuppressThresholdPercent: pointer.Int64(-1),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster CPUEvictBESatisfactionUpperPercent invalid",
			args: args{
				cfg: configuration.ResourceThresholdCfg{
					ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
						CPUEvictBESatisfactionUpperPercent: pointer.Int64(102),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster CPUEvictBESatisfactionUpperPercent invalid, upper must not nil when  lower not nil",
			args: args{
				cfg: configuration.ResourceThresholdCfg{
					ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
						CPUEvictBESatisfactionLowerPercent: pointer.Int64(35),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster CPUEvictBESatisfactionPercent invalid, upper <  lower",
			args: args{
				cfg: configuration.ResourceThresholdCfg{
					ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
						CPUEvictBESatisfactionUpperPercent: pointer.Int64(22),
						CPUEvictBESatisfactionLowerPercent: pointer.Int64(35),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "all is nil",
			args: args{
				cfg: configuration.ResourceThresholdCfg{
					ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{},
					NodeStrategies: []configuration.NodeResourceThresholdStrategy{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								Name: "xxx-yyy",
							},
							ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "config valid",
			args: args{
				cfg: configuration.ResourceThresholdCfg{
					ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
						Enable:                      pointer.Bool(true),
						CPUSuppressThresholdPercent: pointer.Int64(60),
					},
					NodeStrategies: []configuration.NodeResourceThresholdStrategy{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								Name: "xxx-yyy",
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
							},
							ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
								Enable:                      pointer.Bool(true),
								CPUSuppressThresholdPercent: pointer.Int64(60),
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
			checker := ResourceThresholdChecker{cfg: &tt.args.cfg}
			gotErr := checker.ConfigParamValid()
			if gotErr != nil {
				fmt.Println(gotErr.Error())
			}
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}
