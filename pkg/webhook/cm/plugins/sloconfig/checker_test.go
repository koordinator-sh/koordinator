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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func Test_CommonChecker_IsCfgChanged(t *testing.T) {
	type args struct {
		oldConfigMap *corev1.ConfigMap
		configMap    *corev1.ConfigMap
	}

	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "old configMap is nil",
			args: args{
				oldConfigMap: nil,
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						"test": "test",
					},
				},
			},
			want: true,
		},
		{
			name: "Colocation Config not changed",
			args: args{
				oldConfigMap: &corev1.ConfigMap{
					Data: map[string]string{
						"test": "test",
					},
				},
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						"test": "test",
					},
				},
			},
			want: false,
		},
		{
			name: "Colocation Config changed",
			args: args{
				oldConfigMap: &corev1.ConfigMap{
					Data: map[string]string{
						"test": "test",
					},
				},
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						"test": "testNew",
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := CommonChecker{OldConfigMap: tt.args.oldConfigMap, NewConfigMap: tt.args.configMap, configKey: "test"}
			got := checker.IsCfgNotEmptyAndChanged()
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_CreateNodeConfigProfileChecker(t *testing.T) {

	nodeSelectorValid := &metav1.LabelSelector{
		MatchLabels: map[string]string{"xxx": "yyy"},
	}
	nodeSelectorExpect, _ := metav1.LabelSelectorAsSelector(nodeSelectorValid)

	type args struct {
		profiles func() []configuration.NodeCfgProfile
	}
	tests := []struct {
		name    string
		args    args
		want    NodeConfigProfileChecker
		wantErr bool
	}{
		{
			name: "profiles return nil",
			args: args{profiles: func() []configuration.NodeCfgProfile {
				return nil
			}},
			want:    &nodeConfigProfileChecker{cfgName: "test"},
			wantErr: false,
		},
		{
			name: "profiles return empty",
			args: args{profiles: func() []configuration.NodeCfgProfile {
				return []configuration.NodeCfgProfile{}
			}},
			want:    &nodeConfigProfileChecker{cfgName: "test"},
			wantErr: false,
		},
		{
			name: "profiles have nodeCfg with no labels",
			args: args{profiles: func() []configuration.NodeCfgProfile {
				return []configuration.NodeCfgProfile{
					{NodeSelector: &metav1.LabelSelector{}},
				}
			}},
			want:    nil,
			wantErr: true,
		},
		{
			name: "profiles have nodeCfg with labels",
			args: args{profiles: func() []configuration.NodeCfgProfile {
				return []configuration.NodeCfgProfile{
					{NodeSelector: nodeSelectorValid},
				}
			}},
			want: &nodeConfigProfileChecker{cfgName: "test", nodeConfigs: []profileCheckInfo{{
				profile: configuration.NodeCfgProfile{
					NodeSelector: nodeSelectorValid,
				},
				selectors: nodeSelectorExpect,
			}}},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotChecker, gotErr := CreateNodeConfigProfileChecker("test", tt.args.profiles)
			assert.Equal(t, tt.want, gotChecker)
			assert.Equal(t, tt.wantErr, gotErr != nil, "err:%v", gotErr)
		})
	}
}

func Test_nodeConfigProfileChecker_ProfileParamValid(t *testing.T) {
	sloconfig.NodeStrategyNameNeedCheck = "true"
	type args struct {
		nodeConfigs []profileCheckInfo
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "nodeConfigs is empty",
			args: args{
				nodeConfigs: []profileCheckInfo{},
			},
			wantErr: false,
		},
		{
			name: "profile name is empty",
			args: args{
				nodeConfigs: []profileCheckInfo{
					{profile: configuration.NodeCfgProfile{}},
				},
			},
			wantErr: true,
		},
		{
			name: "profile name conflict",
			args: args{
				nodeConfigs: []profileCheckInfo{
					{profile: configuration.NodeCfgProfile{Name: "test"}},
					{profile: configuration.NodeCfgProfile{Name: "test"}},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := nodeConfigProfileChecker{nodeConfigs: tt.args.nodeConfigs}
			gotErr := checker.ProfileParamValid()
			assert.Equal(t, tt.wantErr, gotErr != nil, "err:%v", gotErr)
		})
	}
}

func Test_nodeConfigProfileChecker_NodeSelectorOverlap(t *testing.T) {

	type args struct {
		nodeConfigs []configuration.NodeCfgProfile
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "not overlap",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"yyy": "yyy",
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "overlap: strategy1(xxx:xxx) , strategy2(xxx:xxx,yyy:yyy)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
								"yyy": "yyy",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "overlap: strategy1(xxx:xxx) , strategy2(xxx:xxx|yyy)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"xxx", "yyy"},
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "overlap: strategy1(xxx:xxx), strategy2 (xxx NotIn yyy)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: metav1.LabelSelectorOpNotIn,
									Values:   []string{"yyy"},
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "overlap: strategy1(xxx:xxx), strategy2 (xxx Exist)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "not overlap: strategy1(xxx:xxx), strategy2 (xxx NotExist)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: metav1.LabelSelectorOpDoesNotExist,
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "overlap: strategy1(xxx:xxx), strategy2 (yyy NotExist)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "yyy",
									Operator: metav1.LabelSelectorOpDoesNotExist,
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker, _ := CreateNodeConfigProfileChecker(configuration.ColocationConfigKey, func() []configuration.NodeCfgProfile {
				return tt.args.nodeConfigs
			})
			gotErr := checker.NodeSelectorOverlap()
			if gotErr != nil {
				fmt.Println(gotErr.Error())
			}
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func Test_ExistNodeConflict(t *testing.T) {
	type args struct {
		nodeConfigs []configuration.NodeCfgProfile
		nodes       *corev1.Node
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "node config not conflict:node(xxx:xxx),strategy1(xxx:xxx),strategy2(yyy:yyy)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"yyy": "yyy",
							},
						},
					},
				},
				nodes: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"xxx": "xxx",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "node config conflict:node(xxx:xxx,yyy:yyy),strategy1(xxx:xxx),strategy2(yyy:yyy)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"yyy": "yyy",
							},
						},
					},
				},
				nodes: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"xxx": "xxx",
							"yyy": "yyy",
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "node config conflict:node(xxx:xxx,yyy:yyy),strategy1(xxx:xxx),strategy2(xxx:xxx,yyy:yyy)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"yyy": "yyy",
							},
						},
					},
				},
				nodes: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"xxx": "xxx",
							"yyy": "yyy",
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "node config not conflict:node(xxx:xxx),strategy1(xxx:xxx),strategy2(xxx:xxx,yyy:yyy)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"yyy": "yyy",
							},
						},
					},
				},
				nodes: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"xxx": "xxx",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "node config conflict:node(xxx:xxx),strategy1(xxx:xxx),strategy2(xxx In xxx,yyy)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"xxx", "yyy"},
								},
							},
						},
					},
				},
				nodes: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"xxx": "xxx",
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "node config not conflict:node(xxx:yyy),strategy1(xxx:xxx),strategy2(xxx In xxx,yyy)",
			args: args{
				nodeConfigs: []configuration.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "xxx",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"xxx", "yyy"},
								},
							},
						},
					},
				},
				nodes: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker, _ := CreateNodeConfigProfileChecker(configuration.ColocationConfigKey, func() []configuration.NodeCfgProfile {
				return tt.args.nodeConfigs
			})
			gotErr := checker.ExistNodeConflict(tt.args.nodes)
			if gotErr != nil {
				fmt.Println(gotErr.Error())
			}
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}
