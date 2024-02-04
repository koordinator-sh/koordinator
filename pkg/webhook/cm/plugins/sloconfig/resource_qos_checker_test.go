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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func Test_ResourceQOS_NewChecker_InitStatus(t *testing.T) {
	//clusterOnly
	cfgClusterOnly := &configuration.ResourceQOSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
			BEClass: &slov1alpha1.ResourceQOS{
				CPUQOS: &slov1alpha1.CPUQOSCfg{
					CPUQOS: slov1alpha1.CPUQOS{
						GroupIdentity: pointer.Int64(-1),
					},
				},
			},
		},
	}
	cfgClusterOnlyBytes, _ := json.Marshal(cfgClusterOnly)
	//nodeSelector is empty
	cfgHaveNodeInvalid := &configuration.ResourceQOSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
			BEClass: &slov1alpha1.ResourceQOS{
				CPUQOS: &slov1alpha1.CPUQOSCfg{
					CPUQOS: slov1alpha1.CPUQOS{
						GroupIdentity: pointer.Int64(-1),
					},
				},
			},
		},
		NodeStrategies: []configuration.NodeResourceQOSStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					Name: "xxx-yyy",
				},
				ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
					BEClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							CPUQOS: slov1alpha1.CPUQOS{
								GroupIdentity: pointer.Int64(-1),
							},
						},
					},
				},
			},
		},
	}
	cfgHaveNodeInvalidBytes, _ := json.Marshal(cfgHaveNodeInvalid)

	//valid node config
	cfgHaveNodeValid := &configuration.ResourceQOSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
			BEClass: &slov1alpha1.ResourceQOS{
				CPUQOS: &slov1alpha1.CPUQOSCfg{
					CPUQOS: slov1alpha1.CPUQOS{
						GroupIdentity: pointer.Int64(-1),
					},
				},
			},
		},
		NodeStrategies: []configuration.NodeResourceQOSStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					Name: "xxx-yyy",
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
					BEClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							CPUQOS: slov1alpha1.CPUQOS{
								GroupIdentity: pointer.Int64(-1),
							},
						},
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
		wantCfg            *configuration.ResourceQOSCfg
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
						configuration.ResourceQOSConfigKey: "invalid config",
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
						configuration.ResourceQOSConfigKey: "invalid config",
					},
				},
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ResourceQOSConfigKey: "invalid config",
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
						configuration.ResourceQOSConfigKey: string(cfgClusterOnlyBytes),
					},
				},
			},
			wantCfg:            cfgClusterOnly,
			wantProfileChecker: &nodeConfigProfileChecker{cfgName: configuration.ResourceQOSConfigKey},
			wantStatus:         InitSuccess,
		},
		{
			name: "config valid and have node strategy invalid",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.ResourceQOSConfigKey: string(cfgHaveNodeInvalidBytes),
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
						configuration.ResourceQOSConfigKey: string(cfgHaveNodeValidBytes),
					},
				},
			},
			wantCfg: cfgHaveNodeValid,
			wantProfileChecker: &nodeConfigProfileChecker{
				cfgName: configuration.ResourceQOSConfigKey,
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
			checker := NewResourceQOSChecker(tt.args.oldConfigMap, tt.args.configMap, tt.args.needUnmarshal)
			gotInitStatus := checker.InitStatus()
			assert.True(t, strings.Contains(gotInitStatus, tt.wantStatus), "gotStatus:%s", gotInitStatus)
			assert.Equal(t, tt.wantCfg, checker.cfg)
			assert.Equal(t, tt.wantProfileChecker, checker.NodeConfigProfileChecker)
		})
	}
}

func Test_ResourceQOS_ConfigContentsValid(t *testing.T) {

	type args struct {
		cfg configuration.ResourceQOSCfg
	}

	fromInt := func(in int) *intstr.IntOrString {
		res := intstr.FromInt(in)
		return &res
	}

	fromString := func(in string) *intstr.IntOrString {
		res := intstr.FromString(in)
		return &res
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "cluster CPUQOS GroupIdentity invalid",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(3),
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster MemoryQOS OomKillGroup invalid",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						BEClass: &slov1alpha1.ResourceQOS{
							MemoryQOS: &slov1alpha1.MemoryQOSCfg{
								MemoryQOS: slov1alpha1.MemoryQOS{
									OomKillGroup: pointer.Int64(3),
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "cluster ResctrlQOS CATRangeStartPercent invalid",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						BEClass: &slov1alpha1.ResourceQOS{
							ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
								ResctrlQOS: slov1alpha1.ResctrlQOS{
									CATRangeStartPercent: pointer.Int64(-1),
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "node ResctrlQOS CATRangeStartPercent invalid",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{},
					NodeStrategies: []configuration.NodeResourceQOSStrategy{
						{
							ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
								BEClass: &slov1alpha1.ResourceQOS{
									ResctrlQOS: &slov1alpha1.ResctrlQOSCfg{
										ResctrlQOS: slov1alpha1.ResctrlQOS{
											CATRangeStartPercent: pointer.Int64(101),
										},
									},
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
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{},
					NodeStrategies: []configuration.NodeResourceQOSStrategy{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								Name: "testNode",
							},
							ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "config valid",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						BEClass: &slov1alpha1.ResourceQOS{
							CPUQOS: &slov1alpha1.CPUQOSCfg{
								CPUQOS: slov1alpha1.CPUQOS{
									GroupIdentity: pointer.Int64(-1),
								},
							},
						},
					},
					NodeStrategies: []configuration.NodeResourceQOSStrategy{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								Name: "xxx-yyy",
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
							},
							ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
								BEClass: &slov1alpha1.ResourceQOS{
									CPUQOS: &slov1alpha1.CPUQOSCfg{
										CPUQOS: slov1alpha1.CPUQOS{
											GroupIdentity: pointer.Int64(-1),
										},
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "netqos config not valid for percentage less than 0",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSRClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: fromInt(-1),
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "netqos config not valid for percentage more than 100",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSRClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: fromInt(101),
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "netqos config percentage format is valid",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSRClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: fromInt(50),
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "netqos config quantity format invalid",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSRClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: fromString("50a"),
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "netqos config quantity format is nil",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSRClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: fromString(""),
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "netqos config quantity format valid",
			args: args{
				cfg: configuration.ResourceQOSCfg{
					ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSRClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: fromString("50m"),
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
			checker := ResourceQOSChecker{cfg: &tt.args.cfg}
			gotErr := checker.ConfigParamValid()
			if gotErr != nil {
				fmt.Println(gotErr.Error())
			}
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}
