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

package nodemetric

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource"
)

func Test_getNodeMetricCollectPolicy(t *testing.T) {
	tests := []struct {
		name    string
		node    *corev1.Node
		config  *config.ColocationCfg
		want    *slov1alpha1.NodeMetricCollectPolicy
		wantErr bool
	}{
		{
			name:    "empty config",
			node:    &corev1.Node{},
			want:    nil,
			wantErr: true,
		},
		{
			name: "config disabled",
			node: &corev1.Node{},
			config: &config.ColocationCfg{
				ColocationStrategy: config.ColocationStrategy{
					Enable: pointer.Bool(false),
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "cluster policy",
			node: &corev1.Node{},
			config: &config.ColocationCfg{
				ColocationStrategy: config.ColocationStrategy{
					Enable:                         pointer.Bool(true),
					MetricAggregateDurationSeconds: pointer.Int64(60),
					MetricReportIntervalSeconds:    pointer.Int64(180),
				},
			},
			want: &slov1alpha1.NodeMetricCollectPolicy{
				AggregateDurationSeconds: pointer.Int64(60),
				ReportIntervalSeconds:    pointer.Int64(180),
			},
		},
		{
			name: "cluster policy and node policy but use cluster",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"test": "true",
					},
				},
			},
			config: &config.ColocationCfg{
				ColocationStrategy: config.ColocationStrategy{
					Enable: pointer.Bool(true),

					MetricAggregateDurationSeconds: pointer.Int64(30),
					MetricReportIntervalSeconds:    pointer.Int64(180),
				},
				NodeConfigs: []config.NodeColocationCfg{
					{
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"test": "false",
							},
						},
						ColocationStrategy: config.ColocationStrategy{
							Enable:                         pointer.Bool(true),
							MetricAggregateDurationSeconds: pointer.Int64(15),
							MetricReportIntervalSeconds:    pointer.Int64(30),
						},
					},
				},
			},
			want: &slov1alpha1.NodeMetricCollectPolicy{
				AggregateDurationSeconds: pointer.Int64(30),
				ReportIntervalSeconds:    pointer.Int64(180),
			},
		},
		{
			name: "node policy",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"test": "false",
					},
				},
			},
			config: &config.ColocationCfg{
				ColocationStrategy: config.ColocationStrategy{
					Enable:                         pointer.Bool(true),
					MetricAggregateDurationSeconds: pointer.Int64(30),
					MetricReportIntervalSeconds:    pointer.Int64(180),
				},
				NodeConfigs: []config.NodeColocationCfg{
					{
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"test": "false",
							},
						},
						ColocationStrategy: config.ColocationStrategy{
							Enable:                         pointer.Bool(true),
							MetricAggregateDurationSeconds: pointer.Int64(15),
							MetricReportIntervalSeconds:    pointer.Int64(30),
						},
					},
				},
			},
			want: &slov1alpha1.NodeMetricCollectPolicy{
				AggregateDurationSeconds: pointer.Int64(15),
				ReportIntervalSeconds:    pointer.Int64(30),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resourceConfig noderesource.Config
			if tt.config != nil {
				resourceConfig.ColocationCfg = *tt.config
			}
			got, err := getNodeMetricCollectPolicy(tt.node, &resourceConfig)
			if (err != nil) != tt.wantErr {
				t.Errorf("getNodeMetricCollectPolicy() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getNodeMetricCollectPolicy() got = %v, want %v", got, tt.want)
			}
		})
	}
}
