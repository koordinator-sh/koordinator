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
	"k8s.io/utils/pointer"
)

func Test_GetNodeColocationStrategy(t *testing.T) {
	defaultCfg := NewDefaultColocationCfg()
	type args struct {
		cfg  *ColocationCfg
		node *corev1.Node
	}
	tests := []struct {
		name string
		args args
		want *ColocationStrategy
	}{
		{
			name: "does not panic but return nil for empty input",
			want: nil,
		},
		{
			name: "does not panic but return nil for empty node",
			args: args{
				cfg: &ColocationCfg{
					ColocationStrategy: ColocationStrategy{
						Enable: pointer.BoolPtr(false),
					},
				},
			},
			want: nil,
		},
		{
			name: "return partial cluster strategy",
			args: args{
				cfg: &ColocationCfg{
					ColocationStrategy: ColocationStrategy{
						Enable: pointer.BoolPtr(false),
					},
				},
				node: &corev1.Node{},
			},
			want: &ColocationStrategy{
				Enable: pointer.BoolPtr(false),
			},
		},
		{
			name: "get cluster strategy for empty node configs",
			args: args{
				cfg: &ColocationCfg{
					ColocationStrategy: ColocationStrategy{
						Enable:                        pointer.BoolPtr(false),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
				},
				node: &corev1.Node{},
			},
			want: &ColocationStrategy{
				Enable:                        pointer.BoolPtr(false),
				CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
				MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
				DegradeTimeMinutes:            pointer.Int64Ptr(15),
				UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
				ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
			},
		},
		{
			name: "get merged node strategy 1",
			args: args{
				cfg: &ColocationCfg{
					ColocationStrategy: ColocationStrategy{
						Enable:                        pointer.BoolPtr(false),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
					NodeConfigs: []NodeColocationCfg{
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"xxx": "yyy",
								},
							},
							ColocationStrategy: ColocationStrategy{
								Enable: pointer.BoolPtr(true),
							},
						},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
			},
			want: &ColocationStrategy{
				Enable:                        pointer.BoolPtr(true),
				CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
				MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
				DegradeTimeMinutes:            pointer.Int64Ptr(15),
				UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
				ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
			},
		},
		{
			name: "get merged node strategy 2",
			args: args{
				cfg: &ColocationCfg{
					ColocationStrategy: defaultCfg.ColocationStrategy,
					NodeConfigs: []NodeColocationCfg{
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"xxx": "yyy",
								},
							},
							ColocationStrategy: ColocationStrategy{
								Enable: pointer.BoolPtr(false),
							},
						},
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"xxx": "zzz",
								},
							},
							ColocationStrategy: ColocationStrategy{
								Enable: pointer.BoolPtr(true),
							},
						},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							"xxx": "zzz",
						},
					},
				},
			},
			want: &ColocationStrategy{
				Enable:                         pointer.BoolPtr(true),
				MetricAggregateDurationSeconds: pointer.Int64Ptr(30),
				MetricReportIntervalSeconds:    pointer.Int64Ptr(60),
				CPUReclaimThresholdPercent:     pointer.Int64Ptr(60),
				MemoryReclaimThresholdPercent:  pointer.Int64Ptr(65),
				DegradeTimeMinutes:             pointer.Int64Ptr(15),
				UpdateTimeThresholdSeconds:     pointer.Int64Ptr(300),
				ResourceDiffThreshold:          pointer.Float64Ptr(0.1),
			},
		},
		{
			name: "get merged node strategy and ignore invalid selector",
			args: args{
				cfg: &ColocationCfg{
					ColocationStrategy: ColocationStrategy{
						Enable:                        pointer.BoolPtr(false),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
					NodeConfigs: []NodeColocationCfg{
						{
							NodeSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "xxx",
										Operator: "out",
										Values:   []string{"yyy"},
									},
								},
							},
							ColocationStrategy: ColocationStrategy{
								Enable: pointer.BoolPtr(false),
							},
						},
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"xxx": "yyy",
								},
							},
							ColocationStrategy: ColocationStrategy{
								Enable: pointer.BoolPtr(true),
							},
						},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
			},
			want: &ColocationStrategy{
				Enable:                        pointer.BoolPtr(true),
				CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
				MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
				DegradeTimeMinutes:            pointer.Int64Ptr(15),
				UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
				ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetNodeColocationStrategy(tt.args.cfg, tt.args.node)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_IsColocationStrategyValid(t *testing.T) {
	type args struct {
		strategy *ColocationStrategy
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "nil strategy is invalid",
			args: args{},
			want: false,
		},
		{
			name: "partial strategy is valid",
			args: args{
				strategy: &ColocationStrategy{
					Enable: pointer.BoolPtr(true),
				},
			},
			want: true,
		},
		{
			name: "partial strategy is valid 1",
			args: args{
				strategy: &ColocationStrategy{
					Enable:                     pointer.BoolPtr(true),
					DegradeTimeMinutes:         pointer.Int64Ptr(15),
					UpdateTimeThresholdSeconds: pointer.Int64Ptr(300),
					ResourceDiffThreshold:      pointer.Float64Ptr(0.1),
				},
			},
			want: true,
		},
		{
			name: "partial strategy is valid 2",
			args: args{
				strategy: &ColocationStrategy{
					Enable:                        pointer.BoolPtr(true),
					CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
					MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
					DegradeTimeMinutes:            pointer.Int64Ptr(15),
					ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
				},
			},
			want: true,
		},
		{
			name: "partial strategy is valid 3",
			args: args{
				strategy: &ColocationStrategy{
					Enable:                        pointer.BoolPtr(true),
					CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
					MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
					DegradeTimeMinutes:            pointer.Int64Ptr(15),
					UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
				},
			},
			want: true,
		},
		{
			name: "default strategy is valid",
			args: args{
				strategy: &ColocationStrategy{
					Enable:                        pointer.BoolPtr(true),
					CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
					MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
					DegradeTimeMinutes:            pointer.Int64Ptr(15),
					UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
					ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsColocationStrategyValid(tt.args.strategy)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_IsNodeColocationCfgValid(t *testing.T) {
	type args struct {
		nodeCfg *NodeColocationCfg
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "nil node config is invalid",
			args: args{},
			want: false,
		},
		{
			name: "node selector is valid",
			args: args{
				nodeCfg: &NodeColocationCfg{
					NodeSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "xxx",
								Operator: "Out",
								Values:   []string{"yyy"},
							},
						},
					},
					ColocationStrategy: ColocationStrategy{
						Enable: pointer.BoolPtr(false),
					},
				},
			},
			want: false,
		},
		{
			name: "label selector should not be nil",
			args: args{
				nodeCfg: &NodeColocationCfg{
					NodeSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "xxx",
								Operator: "In",
								Values:   []string{"yyy"},
							},
						},
					},
					ColocationStrategy: ColocationStrategy{
						Enable: pointer.BoolPtr(false),
					},
				},
			},
			want: false,
		},
		{
			name: "label selector should not be nil",
			args: args{
				nodeCfg: &NodeColocationCfg{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"aaa": "bbb",
						},
					},
					ColocationStrategy: ColocationStrategy{},
				},
			},
			want: false,
		},
		{
			name: "a valid node config has a valid label selector and non-empty strategy",
			args: args{
				nodeCfg: &NodeColocationCfg{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
					ColocationStrategy: ColocationStrategy{
						Enable: pointer.BoolPtr(false),
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNodeColocationCfgValid(tt.args.nodeCfg)
			assert.Equal(t, tt.want, got)
		})
	}
}
