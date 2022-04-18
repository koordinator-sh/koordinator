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

package nodeslo

import (
	"encoding/json"
	"fmt"
	"testing"

	"k8s.io/utils/pointer"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func Test_getResourceThresholdSpec(t *testing.T) {
	testingResourceThresholdCfg := &config.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.BoolPtr(true),
			CPUSuppressThresholdPercent: pointer.Int64Ptr(60),
		},
	}
	testingResourceThresholdCfgStr, _ := json.Marshal(testingResourceThresholdCfg)
	testingResourceThresholdCfg1 := &config.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.BoolPtr(true),
			CPUSuppressThresholdPercent: pointer.Int64Ptr(60),
		},
		NodeStrategies: []config.NodeResourceThresholdStrategy{
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"xxx": "yyy",
					},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64Ptr(50),
				},
			},
		},
	}
	testingResourceThresholdCfgStr1, _ := json.Marshal(testingResourceThresholdCfg1)
	testingResourceThresholdCfg2 := &config.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.BoolPtr(true),
			CPUSuppressThresholdPercent: pointer.Int64Ptr(60),
		},
		NodeStrategies: []config.NodeResourceThresholdStrategy{
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"xxx": "yyy",
					},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					Enable:                      pointer.BoolPtr(false),
					CPUSuppressThresholdPercent: pointer.Int64Ptr(50),
				},
			},
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"zzz": "zzz",
					},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64Ptr(40),
				},
			},
		},
	}
	testingResourceThresholdCfgStr2, _ := json.Marshal(testingResourceThresholdCfg2)
	type args struct {
		node      *corev1.Node
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name    string
		args    args
		want    *slov1alpha1.ResourceThresholdStrategy
		wantErr bool
	}{
		{
			name: "throw error for invalid configmap",
			args: args{
				node:      &corev1.Node{},
				configMap: &corev1.ConfigMap{},
			},
			want:    util.DefaultResourceThresholdStrategy(),
			wantErr: false,
		},
		{
			name: "throw error for configmap unmarshal failed",
			args: args{
				node: &corev1.Node{},
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						config.ResourceThresholdConfigKey: "invalid_content",
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "get cluster config correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.SLOCtrlConfigMap,
						Namespace: config.ConfigNameSpace,
					},
					Data: map[string]string{
						config.ResourceThresholdConfigKey: string(testingResourceThresholdCfgStr),
					},
				},
			},
			want: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.BoolPtr(true),
				CPUSuppressThresholdPercent: pointer.Int64Ptr(60),
				CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
			},
		},
		{
			name: "get node config correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.SLOCtrlConfigMap,
						Namespace: config.ConfigNameSpace,
					},
					Data: map[string]string{
						config.ResourceThresholdConfigKey: string(testingResourceThresholdCfgStr1),
					},
				},
			},
			want: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.BoolPtr(true),
				CPUSuppressThresholdPercent: pointer.Int64Ptr(50),
				CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
			},
		},
		{
			name: "get firstly-matched node config",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.SLOCtrlConfigMap,
						Namespace: config.ConfigNameSpace,
					},
					Data: map[string]string{
						config.ResourceThresholdConfigKey: string(testingResourceThresholdCfgStr2),
					},
				},
			},
			want: &slov1alpha1.ResourceThresholdStrategy{
				Enable:                      pointer.BoolPtr(false),
				CPUSuppressThresholdPercent: pointer.Int64Ptr(50),
				CPUSuppressPolicy:           slov1alpha1.CPUSetPolicy,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := getResourceThresholdSpec(tt.args.node, tt.args.configMap)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_generateThresholdCfg(t *testing.T) {
	cfg := config.ResourceThresholdCfg{}
	cfg.ClusterStrategy = util.DefaultResourceThresholdStrategy()
	labelSelector := &metav1.LabelSelector{MatchLabels: map[string]string{}}
	labelSelector.MatchLabels["machineType"] = "F53"
	selectCfg := config.NodeResourceThresholdStrategy{NodeSelector: labelSelector, ResourceThresholdStrategy: util.DefaultResourceThresholdStrategy()}
	cfg.NodeStrategies = []config.NodeResourceThresholdStrategy{selectCfg}

	cfgJson, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Print(string(cfgJson))
}
