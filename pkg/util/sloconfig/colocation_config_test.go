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
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func Test_GetNodeColocationStrategy(t *testing.T) {
	memoryCalcPolicyByUsage := configuration.CalculateByPodUsage
	cpuCalcPolicyByUsage := configuration.CalculateByPodUsage
	var defaultMemoryCollectPolicy = slov1alpha1.UsageWithoutPageCache
	defaultCfg := NewDefaultColocationCfg()
	type args struct {
		cfg  *configuration.ColocationCfg
		node *corev1.Node
	}
	tests := []struct {
		name string
		args args
		want *configuration.ColocationStrategy
	}{
		{
			name: "does not panic but return nil for empty input",
			want: nil,
		},
		{
			name: "does not panic but return nil for empty node",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable: ptr.To[bool](false),
					},
				},
			},
			want: nil,
		},
		{
			name: "return partial cluster strategy",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable: ptr.To[bool](false),
					},
				},
				node: &corev1.Node{},
			},
			want: &configuration.ColocationStrategy{
				Enable: ptr.To[bool](false),
			},
		},
		{
			name: "get cluster strategy for empty node configs",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                        ptr.To[bool](false),
						CPUReclaimThresholdPercent:    ptr.To[int64](65),
						MemoryReclaimThresholdPercent: ptr.To[int64](65),
						DegradeTimeMinutes:            ptr.To[int64](15),
						UpdateTimeThresholdSeconds:    ptr.To[int64](300),
						ResourceDiffThreshold:         ptr.To[float64](0.1),
					},
				},
				node: &corev1.Node{},
			},
			want: &configuration.ColocationStrategy{
				Enable:                        ptr.To[bool](false),
				CPUReclaimThresholdPercent:    ptr.To[int64](65),
				MemoryReclaimThresholdPercent: ptr.To[int64](65),
				DegradeTimeMinutes:            ptr.To[int64](15),
				UpdateTimeThresholdSeconds:    ptr.To[int64](300),
				ResourceDiffThreshold:         ptr.To[float64](0.1),
			},
		},
		{
			name: "get merged node strategy 1",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                        ptr.To[bool](false),
						CPUReclaimThresholdPercent:    ptr.To[int64](65),
						MemoryReclaimThresholdPercent: ptr.To[int64](65),
						MemoryCalculatePolicy:         &memoryCalcPolicyByUsage,
						DegradeTimeMinutes:            ptr.To[int64](15),
						UpdateTimeThresholdSeconds:    ptr.To[int64](300),
						ResourceDiffThreshold:         ptr.To[float64](0.1),
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
								Enable: ptr.To[bool](true),
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
			want: &configuration.ColocationStrategy{
				Enable:                        ptr.To[bool](true),
				CPUReclaimThresholdPercent:    ptr.To[int64](65),
				MemoryReclaimThresholdPercent: ptr.To[int64](65),
				MemoryCalculatePolicy:         &memoryCalcPolicyByUsage,
				DegradeTimeMinutes:            ptr.To[int64](15),
				UpdateTimeThresholdSeconds:    ptr.To[int64](300),
				ResourceDiffThreshold:         ptr.To[float64](0.1),
			},
		},
		{
			name: "get merged node strategy 2",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: defaultCfg.ColocationStrategy,
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
								Enable: ptr.To[bool](false),
							},
						},
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "zzz",
									},
								},
							},
							ColocationStrategy: configuration.ColocationStrategy{
								Enable: ptr.To[bool](true),
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
			want: &configuration.ColocationStrategy{
				Enable:                         ptr.To[bool](true),
				MetricAggregateDurationSeconds: ptr.To[int64](300),
				MetricReportIntervalSeconds:    ptr.To[int64](60),
				MetricAggregatePolicy:          DefaultColocationStrategy().MetricAggregatePolicy,
				CPUReclaimThresholdPercent:     ptr.To[int64](60),
				CPUCalculatePolicy:             &cpuCalcPolicyByUsage,
				MidStaticCPUReservedPercent:    ptr.To[int64](0),
				MidStaticMemoryReservedPercent: ptr.To[int64](0),
				MemoryReclaimThresholdPercent:  ptr.To[int64](65),
				MemoryCalculatePolicy:          &memoryCalcPolicyByUsage,
				DegradeTimeMinutes:             ptr.To[int64](15),
				UpdateTimeThresholdSeconds:     ptr.To[int64](300),
				ResourceDiffThreshold:          ptr.To[float64](0.1),
				MetricMemoryCollectPolicy:      &defaultMemoryCollectPolicy,
				MidCPUThresholdPercent:         ptr.To[int64](100),
				MidMemoryThresholdPercent:      ptr.To[int64](100),
				MidUnallocatedPercent:          ptr.To[int64](0),
			},
		},
		{
			name: "get merged node strategy and ignore invalid selector",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                        ptr.To[bool](false),
						CPUReclaimThresholdPercent:    ptr.To[int64](65),
						MemoryReclaimThresholdPercent: ptr.To[int64](65),
						DegradeTimeMinutes:            ptr.To[int64](15),
						UpdateTimeThresholdSeconds:    ptr.To[int64](300),
						ResourceDiffThreshold:         ptr.To[float64](0.1),
					},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "xxx",
											Operator: "out",
											Values:   []string{"yyy"},
										},
									},
								},
								Name: "xxx-out-yyy",
							},
							ColocationStrategy: configuration.ColocationStrategy{
								Enable: ptr.To[bool](false),
							},
						},
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
								Enable: ptr.To[bool](true),
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
			want: &configuration.ColocationStrategy{
				Enable:                        ptr.To[bool](true),
				CPUReclaimThresholdPercent:    ptr.To[int64](65),
				MemoryReclaimThresholdPercent: ptr.To[int64](65),
				DegradeTimeMinutes:            ptr.To[int64](15),
				UpdateTimeThresholdSeconds:    ptr.To[int64](300),
				ResourceDiffThreshold:         ptr.To[float64](0.1),
			},
		},
		{
			name: "get strategy merged with node ratios",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                         ptr.To[bool](false),
						CPUReclaimThresholdPercent:     ptr.To[int64](65),
						MidStaticCPUReservedPercent:    ptr.To[int64](0),
						MidStaticMemoryReservedPercent: ptr.To[int64](0),
						MemoryReclaimThresholdPercent:  ptr.To[int64](65),
						DegradeTimeMinutes:             ptr.To[int64](15),
						UpdateTimeThresholdSeconds:     ptr.To[int64](300),
						ResourceDiffThreshold:          ptr.To[float64](0.1),
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelCPUReclaimRatio:              "0.7",
							extension.LabelMemoryReclaimRatio:           "0.75",
							extension.LabelMidStaticCPUReservedRatio:    "0.1",
							extension.LabelMidStaticMemoryReservedRatio: "0.12",
						},
					},
				},
			},
			want: &configuration.ColocationStrategy{
				Enable:                         ptr.To[bool](false),
				CPUReclaimThresholdPercent:     ptr.To[int64](70),
				MidStaticCPUReservedPercent:    ptr.To[int64](10),
				MidStaticMemoryReservedPercent: ptr.To[int64](12),
				MemoryReclaimThresholdPercent:  ptr.To[int64](75),
				DegradeTimeMinutes:             ptr.To[int64](15),
				UpdateTimeThresholdSeconds:     ptr.To[int64](300),
				ResourceDiffThreshold:          ptr.To[float64](0.1),
			},
		},
		{
			name: "get strategy while parse node reclaim ratios failed",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                        ptr.To[bool](false),
						CPUReclaimThresholdPercent:    ptr.To[int64](65),
						MemoryReclaimThresholdPercent: ptr.To[int64](65),
						DegradeTimeMinutes:            ptr.To[int64](15),
						UpdateTimeThresholdSeconds:    ptr.To[int64](300),
						ResourceDiffThreshold:         ptr.To[float64](0.1),
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelCPUReclaimRatio:    "-1",
							extension.LabelMemoryReclaimRatio: "invalidField",
						},
					},
				},
			},
			want: &configuration.ColocationStrategy{
				Enable:                        ptr.To[bool](false),
				CPUReclaimThresholdPercent:    ptr.To[int64](65),
				MemoryReclaimThresholdPercent: ptr.To[int64](65),
				DegradeTimeMinutes:            ptr.To[int64](15),
				UpdateTimeThresholdSeconds:    ptr.To[int64](300),
				ResourceDiffThreshold:         ptr.To[float64](0.1),
			},
		},
		{
			name: "get strategy merged with node strategy on annotations",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                        ptr.To[bool](false),
						CPUReclaimThresholdPercent:    ptr.To[int64](65),
						MemoryReclaimThresholdPercent: ptr.To[int64](65),
						DegradeTimeMinutes:            ptr.To[int64](15),
						UpdateTimeThresholdSeconds:    ptr.To[int64](300),
						ResourceDiffThreshold:         ptr.To[float64](0.1),
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationNodeColocationStrategy: `
{
  "cpuReclaimThresholdPercent": 70,
  "memoryReclaimThresholdPercent": 75,
  "midStaticCPUReservedPercent": 10,
  "midStaticMemoryReservedPercent": 15
}
`,
						},
					},
				},
			},
			want: &configuration.ColocationStrategy{
				Enable:                         ptr.To[bool](false),
				CPUReclaimThresholdPercent:     ptr.To[int64](70),
				MidStaticCPUReservedPercent:    ptr.To[int64](10),
				MidStaticMemoryReservedPercent: ptr.To[int64](15),
				MemoryReclaimThresholdPercent:  ptr.To[int64](75),
				DegradeTimeMinutes:             ptr.To[int64](15),
				UpdateTimeThresholdSeconds:     ptr.To[int64](300),
				ResourceDiffThreshold:          ptr.To[float64](0.1),
			},
		},
		{
			name: "get strategy disabled by node strategy on annotations",
			args: args{
				cfg: &configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                        ptr.To[bool](true),
						CPUReclaimThresholdPercent:    ptr.To[int64](65),
						MemoryReclaimThresholdPercent: ptr.To[int64](65),
						DegradeTimeMinutes:            ptr.To[int64](15),
						UpdateTimeThresholdSeconds:    ptr.To[int64](300),
						ResourceDiffThreshold:         ptr.To[float64](0.1),
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationNodeColocationStrategy: `{"enable": false}`,
						},
						Labels: map[string]string{},
					},
				},
			},
			want: &configuration.ColocationStrategy{
				Enable:                        ptr.To[bool](false),
				CPUReclaimThresholdPercent:    ptr.To[int64](65),
				MemoryReclaimThresholdPercent: ptr.To[int64](65),
				DegradeTimeMinutes:            ptr.To[int64](15),
				UpdateTimeThresholdSeconds:    ptr.To[int64](300),
				ResourceDiffThreshold:         ptr.To[float64](0.1),
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

func TestUpdateColocationStrategyForNode(t *testing.T) {
	defaultCfg := DefaultColocationStrategy()
	disabledCfg := defaultCfg.DeepCopy()
	disabledCfg.Enable = ptr.To[bool](false)
	cfg1 := defaultCfg.DeepCopy()
	cfg1.CPUReclaimThresholdPercent = ptr.To[int64](100)
	cfg2 := defaultCfg.DeepCopy()
	cfg2.CPUReclaimThresholdPercent = ptr.To[int64](80)
	cfg2.MidStaticCPUReservedPercent = ptr.To[int64](10)
	cfg2.MidStaticMemoryReservedPercent = ptr.To[int64](12)
	type args struct {
		strategy *configuration.ColocationStrategy
		node     *corev1.Node
	}
	tests := []struct {
		name      string
		args      args
		wantField *configuration.ColocationStrategy
	}{
		{
			name: "no node-level modification",
			args: args{
				strategy: defaultCfg.DeepCopy(),
				node:     &corev1.Node{},
			},
			wantField: defaultCfg.DeepCopy(),
		},
		{
			name: "update strategy according to annotations",
			args: args{
				strategy: defaultCfg.DeepCopy(),
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationNodeColocationStrategy: `{"enable": false}`,
						},
					},
				},
			},
			wantField: disabledCfg,
		},
		{
			name: "update strategy according to ratios",
			args: args{
				strategy: defaultCfg.DeepCopy(),
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{},
						Labels: map[string]string{
							extension.LabelCPUReclaimRatio: `1.0`,
						},
					},
				},
			},
			wantField: cfg1,
		},
		{
			name: "update strategy according to mixed node configs",
			args: args{
				strategy: defaultCfg.DeepCopy(),
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationNodeColocationStrategy: `{"cpuReclaimThresholdPercent": 100}`,
						},
						Labels: map[string]string{
							extension.LabelCPUReclaimRatio:              `0.8`,
							extension.LabelMidStaticCPUReservedRatio:    `0.1`,
							extension.LabelMidStaticMemoryReservedRatio: `0.12`,
						},
					},
				},
			},
			wantField: cfg2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			UpdateColocationStrategyForNode(tt.args.strategy, tt.args.node)
			assert.Equal(t, tt.wantField, tt.args.strategy)
		})
	}
}

func TestGetColocationStrategyOnNode(t *testing.T) {
	tests := []struct {
		name    string
		arg     *corev1.Node
		want    *configuration.ColocationStrategy
		wantErr bool
	}{
		{
			name: "get no strategy",
			arg:  &corev1.Node{},
		},
		{
			name: "get no strategy 1",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
		},
		{
			name: "parse strategy failed",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationNodeColocationStrategy: `invalidField`,
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "parse strategy correctly",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationNodeColocationStrategy: `{"enable": true}`,
					},
				},
			},
			want: &configuration.ColocationStrategy{
				Enable: ptr.To[bool](true),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetColocationStrategyOnNode(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}

func Test_IsColocationStrategyValid(t *testing.T) {
	type args struct {
		strategy *configuration.ColocationStrategy
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
				strategy: &configuration.ColocationStrategy{
					Enable: ptr.To[bool](true),
				},
			},
			want: true,
		},
		{
			name: "partial strategy is valid 1",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                     ptr.To[bool](true),
					DegradeTimeMinutes:         ptr.To[int64](15),
					UpdateTimeThresholdSeconds: ptr.To[int64](300),
					ResourceDiffThreshold:      ptr.To[float64](0.1),
				},
			},
			want: true,
		},
		{
			name: "partial strategy is valid 2",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
				},
			},
			want: true,
		},
		{
			name: "partial strategy is valid 3",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
				},
			},
			want: true,
		},
		{
			name: "default strategy is valid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
				},
			},
			want: true,
		},
		{
			name: "midCPUThresholdPercent less than 0 strategy is invalid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
					MidCPUThresholdPercent:        ptr.To[int64](-1),
				},
			},
			want: false,
		},
		{
			name: "midCPUThresholdPercent more than 100 strategy is invalid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
					MidCPUThresholdPercent:        ptr.To[int64](150),
				},
			},
			want: false,
		},
		{
			name: "midMemoryThresholdPercent less than 0 strategy is invalid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
					MidMemoryThresholdPercent:     ptr.To[int64](-20),
				},
			},
			want: false,
		},
		{
			name: "midMemoryThresholdPercent more than 100 strategy is invalid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
					MidMemoryThresholdPercent:     ptr.To[int64](101),
				},
			},
			want: false,
		},
		{
			name: "midUnallocatedPercent less than 0 strategy is invalid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
					MidUnallocatedPercent:         ptr.To[int64](-10),
				},
			},
			want: false,
		},
		{
			name: "midUnallocatedPercent more than 100 strategy is invalid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
					MidUnallocatedPercent:         ptr.To[int64](200),
				},
			},
			want: false,
		},
		{
			name: "batchCPUThresholdPercent more than 100 strategy is invalid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
					MidUnallocatedPercent:         ptr.To[int64](200),
					BatchCPUThresholdPercent:      ptr.To[int64](200),
					BatchMemoryThresholdPercent:   ptr.To[int64](20),
				},
			},
			want: false,
		},
		{
			name: "batchCPUThresholdPercent less than 0 strategy is invalid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
					MidUnallocatedPercent:         ptr.To[int64](200),
					BatchCPUThresholdPercent:      ptr.To[int64](-1),
					BatchMemoryThresholdPercent:   ptr.To[int64](20),
				},
			},
			want: false,
		},
		{
			name: "default strategy + batchXXXThresholdPercent[0,100] is valid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        ptr.To[bool](true),
					CPUReclaimThresholdPercent:    ptr.To[int64](65),
					MemoryReclaimThresholdPercent: ptr.To[int64](65),
					DegradeTimeMinutes:            ptr.To[int64](15),
					UpdateTimeThresholdSeconds:    ptr.To[int64](300),
					ResourceDiffThreshold:         ptr.To[float64](0.1),
					BatchCPUThresholdPercent:      ptr.To[int64](100),
					BatchMemoryThresholdPercent:   ptr.To[int64](0),
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
		nodeCfg *configuration.NodeColocationCfg
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
				nodeCfg: &configuration.NodeColocationCfg{
					NodeCfgProfile: configuration.NodeCfgProfile{
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: "out",
									Values:   []string{"yyy"},
								},
							},
						},
						Name: "xxx-out-yyy",
					},
					ColocationStrategy: configuration.ColocationStrategy{
						Enable: ptr.To[bool](false),
					},
				},
			},
			want: false,
		},
		{
			name: "label selector should not be nil",
			args: args{
				nodeCfg: &configuration.NodeColocationCfg{
					NodeCfgProfile: configuration.NodeCfgProfile{
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "xxx",
									Operator: "In",
									Values:   []string{"yyy"},
								},
							},
						},
						Name: "xxx-in-yyy",
					},
					ColocationStrategy: configuration.ColocationStrategy{
						Enable: ptr.To[bool](false),
					},
				},
			},
			want: false,
		},
		{
			name: "label selector should not be nil",
			args: args{
				nodeCfg: &configuration.NodeColocationCfg{
					NodeCfgProfile: configuration.NodeCfgProfile{
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"aaa": "bbb",
							},
						},
						Name: "aaa-bbb",
					},
					ColocationStrategy: configuration.ColocationStrategy{},
				},
			},
			want: false,
		},
		{
			name: "a valid node config has a valid label selector and non-empty strategy",
			args: args{
				nodeCfg: &configuration.NodeColocationCfg{
					NodeCfgProfile: configuration.NodeCfgProfile{
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"xxx": "yyy",
							},
						},
						Name: "xxx-yyy",
					},
					ColocationStrategy: configuration.ColocationStrategy{
						Enable: ptr.To[bool](false),
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

func TestInitFlags(t *testing.T) {
	cmdArgs := []string{
		"",
		"--slo-config-name=self-defined-slo-config",
		"--config-namespace=self-defined-ns",
	}
	fs := flag.NewFlagSet(cmdArgs[0], flag.ExitOnError)
	type args struct {
		fs *flag.FlagSet
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "parse config",
			args: args{
				fs: fs,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantSLOName := "self-defined-slo-config"
			wantSLONs := "self-defined-ns"
			InitFlags(tt.args.fs)
			fs.Parse(cmdArgs[1:])
			assert.Equal(t, wantSLOName, SLOCtrlConfigMap, "config map name should be equal")
			assert.Equal(t, wantSLONs, ConfigNameSpace, "config map ns should be equal")
		})
	}
}
