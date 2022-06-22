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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func Test_getResourceThresholdSpec(t *testing.T) {
	defaultSLOCfg := DefaultSLOCfg()
	testingResourceThresholdCfg := &config.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.BoolPtr(true),
			CPUSuppressThresholdPercent: pointer.Int64Ptr(60),
		},
	}
	testingResourceThresholdCfg1 := &config.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.BoolPtr(true),
			CPUSuppressThresholdPercent: pointer.Int64Ptr(60),
		},
		NodeStrategies: []config.NodeResourceThresholdStrategy{
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"xxx": "yyy"},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64Ptr(50),
				},
			},
		},
	}
	type args struct {
		node *corev1.Node
		cfg  *config.ResourceThresholdCfg
	}
	tests := []struct {
		name    string
		args    args
		want    *slov1alpha1.ResourceThresholdStrategy
		wantErr bool
	}{
		{
			name: "node empty ,use cluster config",
			args: args{
				node: &corev1.Node{},
				cfg:  &defaultSLOCfg.ThresholdCfgMerged,
			},
			want:    defaultSLOCfg.ThresholdCfgMerged.ClusterStrategy,
			wantErr: false,
		},
		{
			name: "get cluster config correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				cfg: testingResourceThresholdCfg,
			},
			want: testingResourceThresholdCfg.ClusterStrategy,
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
				cfg: testingResourceThresholdCfg1,
			},
			want: &slov1alpha1.ResourceThresholdStrategy{
				CPUSuppressThresholdPercent: pointer.Int64Ptr(50),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := getResourceThresholdSpec(tt.args.node, tt.args.cfg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_caculateResourceThresholdCfgMerged(t *testing.T) {
	defaultSLOCfg := DefaultSLOCfg()

	oldSLOCfg := DefaultSLOCfg()
	oldSLOCfg.ThresholdCfgMerged.ClusterStrategy.CPUSuppressThresholdPercent = pointer.Int64Ptr(30)

	testingResourceThresholdCfg := &config.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.BoolPtr(true),
			CPUSuppressThresholdPercent: pointer.Int64Ptr(60),
		},
	}
	testingResourceThresholdCfgStr, _ := json.Marshal(testingResourceThresholdCfg)

	expectTestingResourceThresholdCfg := defaultSLOCfg.ThresholdCfgMerged.DeepCopy()
	expectTestingResourceThresholdCfg.ClusterStrategy.Enable = testingResourceThresholdCfg.ClusterStrategy.Enable
	expectTestingResourceThresholdCfg.ClusterStrategy.CPUSuppressThresholdPercent = testingResourceThresholdCfg.ClusterStrategy.CPUSuppressThresholdPercent

	testingResourceThresholdCfg1 := &config.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.BoolPtr(true),
			CPUSuppressThresholdPercent: pointer.Int64Ptr(60),
		},
		NodeStrategies: []config.NodeResourceThresholdStrategy{
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"xxx": "yyy"},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64Ptr(40),
				},
			},
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"zzz": "zzz"},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64Ptr(50),
				},
			},
		},
	}
	testingResourceThresholdCfg1Str, _ := json.Marshal(testingResourceThresholdCfg1)

	expectTestingResourceThresholdCfg1 := defaultSLOCfg.ThresholdCfgMerged.DeepCopy()
	expectTestingResourceThresholdCfg1.ClusterStrategy.Enable = testingResourceThresholdCfg1.ClusterStrategy.Enable
	expectTestingResourceThresholdCfg1.ClusterStrategy.CPUSuppressThresholdPercent = testingResourceThresholdCfg1.ClusterStrategy.CPUSuppressThresholdPercent
	expectTestingResourceThresholdCfg1.NodeStrategies = []config.NodeResourceThresholdStrategy{
		{
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"xxx": "yyy"},
			},
			ResourceThresholdStrategy: expectTestingResourceThresholdCfg1.ClusterStrategy.DeepCopy(),
		},
		{
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"zzz": "zzz"},
			},
			ResourceThresholdStrategy: expectTestingResourceThresholdCfg1.ClusterStrategy.DeepCopy(),
		},
	}
	expectTestingResourceThresholdCfg1.NodeStrategies[0].CPUSuppressThresholdPercent = pointer.Int64Ptr(40)
	expectTestingResourceThresholdCfg1.NodeStrategies[1].CPUSuppressThresholdPercent = pointer.Int64Ptr(50)

	type args struct {
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name    string
		args    args
		want    *config.ResourceThresholdCfg
		wantErr bool
	}{
		{
			name: "config contents is empty,then use default",
			args: args{
				configMap: &corev1.ConfigMap{},
			},
			want:    &defaultSLOCfg.ThresholdCfgMerged,
			wantErr: false,
		},
		{
			name: "throw error for configmap unmarshal failed",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						config.ResourceThresholdConfigKey: "invalid_content",
					},
				},
			},
			want:    &oldSLOCfg.ThresholdCfgMerged,
			wantErr: true,
		},
		{
			name: "only cluster config",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						config.ResourceThresholdConfigKey: string(testingResourceThresholdCfgStr),
					},
				},
			},
			want:    expectTestingResourceThresholdCfg,
			wantErr: false,
		},
		{
			name: "node config",
			args: args{
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.SLOCtrlConfigMap,
						Namespace: config.ConfigNameSpace,
					},
					Data: map[string]string{
						config.ResourceThresholdConfigKey: string(testingResourceThresholdCfg1Str),
					},
				},
			},
			want:    expectTestingResourceThresholdCfg1,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := caculateResourceThresholdCfgMerged(oldSLOCfg.ThresholdCfgMerged, tt.args.configMap)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, &got)
		})
	}
}

func Test_getResourceQoSSpec(t *testing.T) {
	defaultSLOCfg := DefaultSLOCfg()
	testingResourceQoSCfg := &config.ResourceQoSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQoSStrategy{
			BE: &slov1alpha1.ResourceQoS{
				CPUQoS: &slov1alpha1.CPUQoSCfg{
					CPUQoS: slov1alpha1.CPUQoS{
						GroupIdentity: pointer.Int64Ptr(0),
					},
				},
			},
		},
	}
	testingResourceQoSCfg1 := &config.ResourceQoSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQoSStrategy{
			BE: &slov1alpha1.ResourceQoS{
				CPUQoS: &slov1alpha1.CPUQoSCfg{
					CPUQoS: slov1alpha1.CPUQoS{
						GroupIdentity: pointer.Int64Ptr(0),
					},
				},
			},
		},
		NodeStrategies: []config.NodeResourceQoSStrategy{
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"xxx": "yyy",
					},
				},
				ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
					BE: &slov1alpha1.ResourceQoS{
						CPUQoS: &slov1alpha1.CPUQoSCfg{
							CPUQoS: slov1alpha1.CPUQoS{
								GroupIdentity: pointer.Int64Ptr(1),
							},
						},
					},
				},
			},
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"zzz": "zzz",
					},
				},
				ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
					BE: &slov1alpha1.ResourceQoS{
						CPUQoS: &slov1alpha1.CPUQoSCfg{
							CPUQoS: slov1alpha1.CPUQoS{
								GroupIdentity: pointer.Int64Ptr(2),
							},
						},
					},
				},
			},
		},
	}
	type args struct {
		node *corev1.Node
		cfg  *config.ResourceQoSCfg
	}
	tests := []struct {
		name    string
		args    args
		want    *slov1alpha1.ResourceQoSStrategy
		wantErr bool
	}{
		{
			name: "node empty, use cluster config",
			args: args{
				node: &corev1.Node{},
				cfg:  &defaultSLOCfg.ResourceQoSCfgMerged,
			},
			want:    &slov1alpha1.ResourceQoSStrategy{},
			wantErr: false,
		},
		{
			name: "get cluster config correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				cfg: testingResourceQoSCfg,
			},
			want: testingResourceQoSCfg.ClusterStrategy,
		},
		{
			name: "get node config correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							"zzz": "zzz",
						},
					},
				},
				cfg: testingResourceQoSCfg1,
			},
			want: testingResourceQoSCfg1.NodeStrategies[1].ResourceQoSStrategy,
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
				cfg: testingResourceQoSCfg1,
			},
			want: testingResourceQoSCfg1.NodeStrategies[0].ResourceQoSStrategy,
		},
		// TODO: add cases for compatible ResourceQoS configuration
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := getResourceQoSSpec(tt.args.node, tt.args.cfg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_caculateResourceQoSCfgMerged(t *testing.T) {
	defaultSLOCfg := DefaultSLOCfg().ResourceQoSCfgMerged
	oldSLOConfig := &config.ResourceQoSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQoSStrategy{
			BE: &slov1alpha1.ResourceQoS{
				CPUQoS: &slov1alpha1.CPUQoSCfg{
					CPUQoS: slov1alpha1.CPUQoS{
						GroupIdentity: pointer.Int64Ptr(2),
					},
				},
				MemoryQoS: &slov1alpha1.MemoryQoSCfg{
					MemoryQoS: slov1alpha1.MemoryQoS{
						MinLimitPercent: pointer.Int64Ptr(40),
					},
				},
			},
		},
	}

	testingOnlyCluster := &config.ResourceQoSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQoSStrategy{
			BE: &slov1alpha1.ResourceQoS{
				CPUQoS: &slov1alpha1.CPUQoSCfg{
					CPUQoS: slov1alpha1.CPUQoS{
						GroupIdentity: pointer.Int64Ptr(0),
					},
				},
			},
		},
	}
	testingOnlyClusterStr, _ := json.Marshal(testingOnlyCluster)
	expectTestingOnlyCluster := testingOnlyCluster.DeepCopy()

	testingResourceQoSCfg1 := &config.ResourceQoSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQoSStrategy{
			BE: &slov1alpha1.ResourceQoS{
				CPUQoS: &slov1alpha1.CPUQoSCfg{
					CPUQoS: slov1alpha1.CPUQoS{
						GroupIdentity: pointer.Int64Ptr(0),
					},
				},
			},
		},
		NodeStrategies: []config.NodeResourceQoSStrategy{
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"xxx": "yyy",
					},
				},
				ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
					BE: &slov1alpha1.ResourceQoS{
						CPUQoS: &slov1alpha1.CPUQoSCfg{
							CPUQoS: slov1alpha1.CPUQoS{
								GroupIdentity: pointer.Int64Ptr(0),
							},
						},
					},
				},
			},
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"zzz": "zzz",
					},
				},
				ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
					BE: &slov1alpha1.ResourceQoS{
						CPUQoS: &slov1alpha1.CPUQoSCfg{
							CPUQoS: slov1alpha1.CPUQoS{
								GroupIdentity: pointer.Int64Ptr(-1),
							},
						},
					},
				},
			},
		},
	}
	testingResourceQoSCfgStr1, _ := json.Marshal(testingResourceQoSCfg1)
	expectTestingResourceQoSCfg1 := testingResourceQoSCfg1.DeepCopy()
	expectTestingResourceQoSCfg1.NodeStrategies[0].BE.CPUQoS.GroupIdentity = pointer.Int64Ptr(0)
	expectTestingResourceQoSCfg1.NodeStrategies[1].BE.CPUQoS.GroupIdentity = pointer.Int64Ptr(-1)

	type args struct {
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name    string
		args    args
		want    *config.ResourceQoSCfg
		wantErr bool
	}{
		{
			name: "config is null! use old",
			args: args{
				configMap: &corev1.ConfigMap{},
			},
			want:    &defaultSLOCfg,
			wantErr: false,
		},
		{
			name: "throw error for configmap unmarshal failed",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						config.ResourceQoSConfigKey: "invalid_content",
					},
				},
			},
			want:    oldSLOConfig,
			wantErr: true,
		},
		{
			name: "get cluster config correctly",
			args: args{
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.SLOCtrlConfigMap,
						Namespace: config.ConfigNameSpace,
					},
					Data: map[string]string{
						config.ResourceQoSConfigKey: string(testingOnlyClusterStr),
					},
				},
			},
			want: expectTestingOnlyCluster,
		},
		{
			name: "get node config correctly",
			args: args{
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.SLOCtrlConfigMap,
						Namespace: config.ConfigNameSpace,
					},
					Data: map[string]string{
						config.ResourceQoSConfigKey: string(testingResourceQoSCfgStr1),
					},
				},
			},
			want: expectTestingResourceQoSCfg1,
		},
		// TODO: add cases for compatible ResourceQoS configuration
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := caculateResourceQoSCfgMerged(*oldSLOConfig, tt.args.configMap)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, &got)
		})
	}
}

func Test_getCPBurstConfigSpec(t *testing.T) {
	defaultConfig := DefaultSLOCfg()
	testingCPUBurstCfg := &config.CPUBurstCfg{
		ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig: slov1alpha1.CPUBurstConfig{
				CFSQuotaBurstPeriodSeconds: pointer.Int64Ptr(120),
			},
		},
	}
	testingCPUBurstCfg1 := &config.CPUBurstCfg{
		ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig: slov1alpha1.CPUBurstConfig{
				CPUBurstPercent: pointer.Int64Ptr(200),
			},
		},
		NodeStrategies: []config.NodeCPUBurstCfg{
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"xxx": "yyy",
					},
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent: pointer.Int64Ptr(200),
					},
				},
			},
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"zzz": "zzz",
					},
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent: pointer.Int64Ptr(100),
					},
				},
			},
		},
	}
	type args struct {
		node *corev1.Node
		cfg  *config.CPUBurstCfg
	}
	tests := []struct {
		name    string
		args    args
		want    *slov1alpha1.CPUBurstStrategy
		wantErr bool
	}{
		{
			name: "default value for empty config",
			args: args{
				node: &corev1.Node{},
				cfg:  &defaultConfig.CPUBurstCfgMerged,
			},
			want:    util.DefaultCPUBurstStrategy(),
			wantErr: false,
		},
		{
			name: "get cluster config correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				cfg: testingCPUBurstCfg,
			},
			want: testingCPUBurstCfg.ClusterStrategy,
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
				cfg: testingCPUBurstCfg1,
			},
			want: testingCPUBurstCfg1.NodeStrategies[0].CPUBurstStrategy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := getCPUBurstConfigSpec(tt.args.node, tt.args.cfg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_caculateCPUBurstCfgMerged(t *testing.T) {

	defaultSLOCfg := DefaultSLOCfg()

	oldSLOConfig := DefaultSLOCfg()
	oldSLOConfig.CPUBurstCfgMerged.ClusterStrategy.CFSQuotaBurstPercent = pointer.Int64Ptr(30)

	testingCfgClusterOnly := &config.CPUBurstCfg{
		ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig: slov1alpha1.CPUBurstConfig{
				CFSQuotaBurstPeriodSeconds: pointer.Int64Ptr(120),
			},
		},
	}
	testingCfgClusterOnlyStr, _ := json.Marshal(testingCfgClusterOnly)

	expectTestingCfgClusterOnly := defaultSLOCfg.CPUBurstCfgMerged.DeepCopy()
	expectTestingCfgClusterOnly.ClusterStrategy.CPUBurstConfig.CFSQuotaBurstPeriodSeconds = testingCfgClusterOnly.ClusterStrategy.CPUBurstConfig.CFSQuotaBurstPeriodSeconds

	testingCPUBurstCfg1 := &config.CPUBurstCfg{
		ClusterStrategy: testingCfgClusterOnly.ClusterStrategy,
		NodeStrategies: []config.NodeCPUBurstCfg{
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"xxx": "yyy",
					},
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent: pointer.Int64Ptr(100),
					},
				},
			},
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"zzz": "zzz",
					},
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent: pointer.Int64Ptr(200),
					},
				},
			},
		},
	}
	testingCPUBurstCfgStr1, _ := json.Marshal(testingCPUBurstCfg1)

	expectTestingCPUBurstCfg1 := &config.CPUBurstCfg{
		ClusterStrategy: expectTestingCfgClusterOnly.ClusterStrategy.DeepCopy(),
		NodeStrategies: []config.NodeCPUBurstCfg{
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"xxx": "yyy",
					},
				},
				CPUBurstStrategy: expectTestingCfgClusterOnly.ClusterStrategy.DeepCopy(),
			},
			{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"zzz": "zzz",
					},
				},
				CPUBurstStrategy: expectTestingCfgClusterOnly.ClusterStrategy.DeepCopy(),
			},
		},
	}
	expectTestingCPUBurstCfg1.NodeStrategies[0].CPUBurstPercent = testingCPUBurstCfg1.NodeStrategies[0].CPUBurstPercent
	expectTestingCPUBurstCfg1.NodeStrategies[1].CPUBurstPercent = testingCPUBurstCfg1.NodeStrategies[1].CPUBurstPercent

	type args struct {
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name    string
		args    args
		want    *config.CPUBurstCfg
		wantErr bool
	}{
		{
			name: "config is null! use cluster config",
			args: args{
				configMap: &corev1.ConfigMap{},
			},
			want:    &defaultSLOCfg.CPUBurstCfgMerged,
			wantErr: false,
		},
		{
			name: "throw error for configmap unmarshal failed",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						config.CPUBurstConfigKey: "invalid_content",
					},
				},
			},
			want:    &oldSLOConfig.CPUBurstCfgMerged,
			wantErr: true,
		},
		{
			name: "get cluster config correctly",
			args: args{
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.SLOCtrlConfigMap,
						Namespace: config.ConfigNameSpace,
					},
					Data: map[string]string{
						config.CPUBurstConfigKey: string(testingCfgClusterOnlyStr),
					},
				},
			},
			want: expectTestingCfgClusterOnly,
		},
		{
			name: "get config merged correctly",
			args: args{
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.SLOCtrlConfigMap,
						Namespace: config.ConfigNameSpace,
					},
					Data: map[string]string{
						config.CPUBurstConfigKey: string(testingCPUBurstCfgStr1),
					},
				},
			},
			want: expectTestingCPUBurstCfg1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := caculateCPUBurstCfgMerged(oldSLOConfig.CPUBurstCfgMerged, tt.args.configMap)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, &got)
		})
	}
}
