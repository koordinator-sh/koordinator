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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	ext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func Test_getResourceThresholdSpec(t *testing.T) {
	defaultSLOCfg := DefaultSLOCfg()
	testingResourceThresholdCfg := &configuration.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.Bool(true),
			CPUSuppressThresholdPercent: pointer.Int64(60),
		},
	}
	testingResourceThresholdCfg1 := &configuration.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.Bool(true),
			CPUSuppressThresholdPercent: pointer.Int64(60),
		},
		NodeStrategies: []configuration.NodeResourceThresholdStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64(50),
				},
			},
		},
	}
	type args struct {
		node *corev1.Node
		cfg  *configuration.ResourceThresholdCfg
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
				CPUSuppressThresholdPercent: pointer.Int64(50),
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

func Test_calculateResourceThresholdCfgMerged(t *testing.T) {
	defaultSLOCfg := DefaultSLOCfg()

	oldSLOCfg := DefaultSLOCfg()
	oldSLOCfg.ThresholdCfgMerged.ClusterStrategy.CPUSuppressThresholdPercent = pointer.Int64(30)

	testingResourceThresholdCfg := &configuration.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.Bool(true),
			CPUSuppressThresholdPercent: pointer.Int64(60),
		},
	}
	testingResourceThresholdCfgStr, _ := json.Marshal(testingResourceThresholdCfg)

	expectTestingResourceThresholdCfg := defaultSLOCfg.ThresholdCfgMerged.DeepCopy()
	expectTestingResourceThresholdCfg.ClusterStrategy.Enable = testingResourceThresholdCfg.ClusterStrategy.Enable
	expectTestingResourceThresholdCfg.ClusterStrategy.CPUSuppressThresholdPercent = testingResourceThresholdCfg.ClusterStrategy.CPUSuppressThresholdPercent

	testingResourceThresholdCfg1 := &configuration.ResourceThresholdCfg{
		ClusterStrategy: &slov1alpha1.ResourceThresholdStrategy{
			Enable:                      pointer.Bool(true),
			CPUSuppressThresholdPercent: pointer.Int64(60),
		},
		NodeStrategies: []configuration.NodeResourceThresholdStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64(40),
				},
			},
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"zzz": "zzz",
						},
					},
				},
				ResourceThresholdStrategy: &slov1alpha1.ResourceThresholdStrategy{
					CPUSuppressThresholdPercent: pointer.Int64(50),
				},
			},
		},
	}
	testingResourceThresholdCfg1Str, _ := json.Marshal(testingResourceThresholdCfg1)

	expectTestingResourceThresholdCfg1 := defaultSLOCfg.ThresholdCfgMerged.DeepCopy()
	expectTestingResourceThresholdCfg1.ClusterStrategy.Enable = testingResourceThresholdCfg1.ClusterStrategy.Enable
	expectTestingResourceThresholdCfg1.ClusterStrategy.CPUSuppressThresholdPercent = testingResourceThresholdCfg1.ClusterStrategy.CPUSuppressThresholdPercent
	expectTestingResourceThresholdCfg1.NodeStrategies = []configuration.NodeResourceThresholdStrategy{
		{
			NodeCfgProfile: configuration.NodeCfgProfile{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"xxx": "yyy",
					},
				},
			},
			ResourceThresholdStrategy: expectTestingResourceThresholdCfg1.ClusterStrategy.DeepCopy(),
		},
		{
			NodeCfgProfile: configuration.NodeCfgProfile{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"zzz": "zzz",
					},
				},
			},
			ResourceThresholdStrategy: expectTestingResourceThresholdCfg1.ClusterStrategy.DeepCopy(),
		},
	}
	expectTestingResourceThresholdCfg1.NodeStrategies[0].CPUSuppressThresholdPercent = pointer.Int64(40)
	expectTestingResourceThresholdCfg1.NodeStrategies[1].CPUSuppressThresholdPercent = pointer.Int64(50)

	type args struct {
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name    string
		args    args
		want    *configuration.ResourceThresholdCfg
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
						configuration.ResourceThresholdConfigKey: "invalid_content",
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
						configuration.ResourceThresholdConfigKey: string(testingResourceThresholdCfgStr),
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
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						configuration.ResourceThresholdConfigKey: string(testingResourceThresholdCfg1Str),
					},
				},
			},
			want:    expectTestingResourceThresholdCfg1,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := calculateResourceThresholdCfgMerged(oldSLOCfg.ThresholdCfgMerged, tt.args.configMap)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, &got)
		})
	}
}

func Test_getResourceQOSSpec(t *testing.T) {
	defaultSLOCfg := DefaultSLOCfg()
	testingResourceQOSCfg := &configuration.ResourceQOSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
			BEClass: &slov1alpha1.ResourceQOS{
				CPUQOS: &slov1alpha1.CPUQOSCfg{
					CPUQOS: slov1alpha1.CPUQOS{
						GroupIdentity: pointer.Int64(0),
					},
				},
			},
		},
	}
	testingResourceQOSCfg1 := &configuration.ResourceQOSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
			BEClass: &slov1alpha1.ResourceQOS{
				CPUQOS: &slov1alpha1.CPUQOSCfg{
					CPUQOS: slov1alpha1.CPUQOS{
						GroupIdentity: pointer.Int64(0),
					},
				},
			},
		},
		NodeStrategies: []configuration.NodeResourceQOSStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
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
								GroupIdentity: pointer.Int64(1),
							},
						},
					},
				},
			},
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"zzz": "zzz",
						},
					},
				},
				ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
					BEClass: &slov1alpha1.ResourceQOS{
						CPUQOS: &slov1alpha1.CPUQOSCfg{
							CPUQOS: slov1alpha1.CPUQOS{
								GroupIdentity: pointer.Int64(2),
							},
						},
					},
				},
			},
		},
	}
	type args struct {
		node *corev1.Node
		cfg  *configuration.ResourceQOSCfg
	}
	tests := []struct {
		name    string
		args    args
		want    *slov1alpha1.ResourceQOSStrategy
		wantErr bool
	}{
		{
			name: "node empty, use cluster config",
			args: args{
				node: &corev1.Node{},
				cfg:  &defaultSLOCfg.ResourceQOSCfgMerged,
			},
			want:    &slov1alpha1.ResourceQOSStrategy{},
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
				cfg: testingResourceQOSCfg,
			},
			want: testingResourceQOSCfg.ClusterStrategy,
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
				cfg: testingResourceQOSCfg1,
			},
			want: testingResourceQOSCfg1.NodeStrategies[1].ResourceQOSStrategy,
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
				cfg: testingResourceQOSCfg1,
			},
			want: testingResourceQOSCfg1.NodeStrategies[0].ResourceQOSStrategy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := getResourceQOSSpec(tt.args.node, tt.args.cfg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_calculateResourceQOSCfgMerged(t *testing.T) {
	defaultSLOCfg := DefaultSLOCfg().ResourceQOSCfgMerged
	oldSLOConfig := &configuration.ResourceQOSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
			BEClass: &slov1alpha1.ResourceQOS{
				CPUQOS: &slov1alpha1.CPUQOSCfg{
					CPUQOS: slov1alpha1.CPUQOS{
						GroupIdentity: pointer.Int64(2),
					},
				},
				MemoryQOS: &slov1alpha1.MemoryQOSCfg{
					MemoryQOS: slov1alpha1.MemoryQOS{
						MinLimitPercent: pointer.Int64(40),
					},
				},
			},
		},
	}

	testingOnlyCluster := &configuration.ResourceQOSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
			BEClass: &slov1alpha1.ResourceQOS{
				CPUQOS: &slov1alpha1.CPUQOSCfg{
					CPUQOS: slov1alpha1.CPUQOS{
						GroupIdentity: pointer.Int64(0),
					},
				},
			},
		},
	}
	testingOnlyClusterStr, _ := json.Marshal(testingOnlyCluster)
	expectTestingOnlyCluster := testingOnlyCluster.DeepCopy()

	testingResourceQOSCfg1 := &configuration.ResourceQOSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
			BEClass: &slov1alpha1.ResourceQOS{
				CPUQOS: &slov1alpha1.CPUQOSCfg{
					CPUQOS: slov1alpha1.CPUQOS{
						GroupIdentity: pointer.Int64(0),
					},
				},
			},
		},
		NodeStrategies: []configuration.NodeResourceQOSStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
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
								GroupIdentity: pointer.Int64(0),
							},
						},
					},
				},
			},
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"zzz": "zzz",
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
	testingResourceQOSCfgStr1, _ := json.Marshal(testingResourceQOSCfg1)
	expectTestingResourceQOSCfg1 := testingResourceQOSCfg1.DeepCopy()
	expectTestingResourceQOSCfg1.NodeStrategies[0].BEClass.CPUQOS.GroupIdentity = pointer.Int64(0)
	expectTestingResourceQOSCfg1.NodeStrategies[1].BEClass.CPUQOS.GroupIdentity = pointer.Int64(-1)

	type args struct {
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name    string
		args    args
		want    *configuration.ResourceQOSCfg
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
						configuration.ResourceQOSConfigKey: "invalid_content",
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
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						configuration.ResourceQOSConfigKey: string(testingOnlyClusterStr),
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
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						configuration.ResourceQOSConfigKey: string(testingResourceQOSCfgStr1),
					},
				},
			},
			want: expectTestingResourceQOSCfg1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := calculateResourceQOSCfgMerged(*oldSLOConfig, tt.args.configMap)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, &got)
		})
	}
}

func Test_getCPBurstConfigSpec(t *testing.T) {
	defaultConfig := DefaultSLOCfg()
	testingCPUBurstCfg := &configuration.CPUBurstCfg{
		ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig: slov1alpha1.CPUBurstConfig{
				CFSQuotaBurstPeriodSeconds: pointer.Int64(120),
			},
		},
	}
	testingCPUBurstCfg1 := &configuration.CPUBurstCfg{
		ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig: slov1alpha1.CPUBurstConfig{
				CPUBurstPercent: pointer.Int64(200),
			},
		},
		NodeStrategies: []configuration.NodeCPUBurstCfg{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent: pointer.Int64(200),
					},
				},
			},
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"zzz": "zzz",
						},
					},
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent: pointer.Int64(100),
					},
				},
			},
		},
	}
	type args struct {
		node *corev1.Node
		cfg  *configuration.CPUBurstCfg
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
			want:    sloconfig.DefaultCPUBurstStrategy(),
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

func Test_calculateCPUBurstCfgMerged(t *testing.T) {

	defaultSLOCfg := DefaultSLOCfg()

	oldSLOConfig := DefaultSLOCfg()
	oldSLOConfig.CPUBurstCfgMerged.ClusterStrategy.CFSQuotaBurstPercent = pointer.Int64(30)

	testingCfgClusterOnly := &configuration.CPUBurstCfg{
		ClusterStrategy: &slov1alpha1.CPUBurstStrategy{
			CPUBurstConfig: slov1alpha1.CPUBurstConfig{
				CFSQuotaBurstPeriodSeconds: pointer.Int64(120),
			},
		},
	}
	testingCfgClusterOnlyStr, _ := json.Marshal(testingCfgClusterOnly)

	expectTestingCfgClusterOnly := defaultSLOCfg.CPUBurstCfgMerged.DeepCopy()
	expectTestingCfgClusterOnly.ClusterStrategy.CPUBurstConfig.CFSQuotaBurstPeriodSeconds = testingCfgClusterOnly.ClusterStrategy.CPUBurstConfig.CFSQuotaBurstPeriodSeconds

	testingCPUBurstCfg1 := &configuration.CPUBurstCfg{
		ClusterStrategy: testingCfgClusterOnly.ClusterStrategy,
		NodeStrategies: []configuration.NodeCPUBurstCfg{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent: pointer.Int64(100),
					},
				},
			},
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"zzz": "zzz",
						},
					},
				},
				CPUBurstStrategy: &slov1alpha1.CPUBurstStrategy{
					CPUBurstConfig: slov1alpha1.CPUBurstConfig{
						CPUBurstPercent: pointer.Int64(200),
					},
				},
			},
		},
	}
	testingCPUBurstCfgStr1, _ := json.Marshal(testingCPUBurstCfg1)

	expectTestingCPUBurstCfg1 := &configuration.CPUBurstCfg{
		ClusterStrategy: expectTestingCfgClusterOnly.ClusterStrategy.DeepCopy(),
		NodeStrategies: []configuration.NodeCPUBurstCfg{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				CPUBurstStrategy: expectTestingCfgClusterOnly.ClusterStrategy.DeepCopy(),
			},
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"zzz": "zzz",
						},
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
		want    *configuration.CPUBurstCfg
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
						configuration.CPUBurstConfigKey: "invalid_content",
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
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						configuration.CPUBurstConfigKey: string(testingCfgClusterOnlyStr),
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
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						configuration.CPUBurstConfigKey: string(testingCPUBurstCfgStr1),
					},
				},
			},
			want: expectTestingCPUBurstCfg1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := calculateCPUBurstCfgMerged(oldSLOConfig.CPUBurstCfgMerged, tt.args.configMap)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, &got)
		})
	}
}

func Test_getSystemConfigSpec(t *testing.T) {
	defaultConfig := DefaultSLOCfg()
	testingSystemConfig := &configuration.SystemCfg{
		ClusterStrategy: &slov1alpha1.SystemStrategy{
			MinFreeKbytesFactor: pointer.Int64(150),
		},
	}
	testingSystemConfig1 := &configuration.SystemCfg{
		ClusterStrategy: &slov1alpha1.SystemStrategy{
			MinFreeKbytesFactor: pointer.Int64(150),
		},
		NodeStrategies: []configuration.NodeSystemStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				SystemStrategy: &slov1alpha1.SystemStrategy{
					MinFreeKbytesFactor: pointer.Int64(120),
				},
			},
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"zzz": "zzz",
						},
					},
				},
				SystemStrategy: &slov1alpha1.SystemStrategy{
					MinFreeKbytesFactor: pointer.Int64(130),
				},
			},
		},
	}
	type args struct {
		node *corev1.Node
		cfg  *configuration.SystemCfg
	}
	tests := []struct {
		name    string
		args    args
		want    *slov1alpha1.SystemStrategy
		wantErr bool
	}{
		{
			name: "node invalid, use cluster config",
			args: args{
				node: &corev1.Node{},
				cfg:  &defaultConfig.SystemCfgMerged,
			},
			want:    sloconfig.DefaultSystemStrategy(),
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
				cfg: testingSystemConfig,
			},
			want: testingSystemConfig.ClusterStrategy,
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
				cfg: testingSystemConfig1,
			},
			want: testingSystemConfig1.NodeStrategies[1].SystemStrategy,
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
				cfg: testingSystemConfig1,
			},
			want: testingSystemConfig1.NodeStrategies[0].SystemStrategy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := getSystemConfigSpec(tt.args.node, tt.args.cfg)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_calculateSystemConfigMerged(t *testing.T) {

	defaultSLOCfg := DefaultSLOCfg()

	oldSLOCfg := DefaultSLOCfg()
	oldSLOCfg.SystemCfgMerged.ClusterStrategy.WatermarkScaleFactor = pointer.Int64(99)

	testingCfgOnliCluster := &configuration.SystemCfg{
		ClusterStrategy: &slov1alpha1.SystemStrategy{
			WatermarkScaleFactor: pointer.Int64(151),
			MemcgReapBackGround:  pointer.Int64(1),
		},
	}
	testingCfgOnliClusterStr, _ := json.Marshal(testingCfgOnliCluster)
	expectTestingCfgOnlyCluster := defaultSLOCfg.SystemCfgMerged.DeepCopy()
	expectTestingCfgOnlyCluster.ClusterStrategy.WatermarkScaleFactor = testingCfgOnliCluster.ClusterStrategy.WatermarkScaleFactor
	expectTestingCfgOnlyCluster.ClusterStrategy.MemcgReapBackGround = testingCfgOnliCluster.ClusterStrategy.MemcgReapBackGround

	testingSystemConfig1 := &configuration.SystemCfg{
		ClusterStrategy: &slov1alpha1.SystemStrategy{
			WatermarkScaleFactor: pointer.Int64(151),
		},
		NodeStrategies: []configuration.NodeSystemStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				SystemStrategy: &slov1alpha1.SystemStrategy{
					MinFreeKbytesFactor: pointer.Int64(130),
					MemcgReapBackGround: pointer.Int64(1),
				},
			},
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"zzz": "zzz",
						},
					},
				},
				SystemStrategy: &slov1alpha1.SystemStrategy{
					MinFreeKbytesFactor: pointer.Int64(140),
					MemcgReapBackGround: pointer.Int64(0),
				},
			},
		},
	}
	testingSystemConfig1Str, _ := json.Marshal(testingSystemConfig1)
	expectTestingSystemConfig1 := &configuration.SystemCfg{
		ClusterStrategy: &slov1alpha1.SystemStrategy{
			MinFreeKbytesFactor:   oldSLOCfg.SystemCfgMerged.ClusterStrategy.MinFreeKbytesFactor,
			WatermarkScaleFactor:  pointer.Int64(151),
			MemcgReapBackGround:   oldSLOCfg.SystemCfgMerged.ClusterStrategy.MemcgReapBackGround,
			TotalNetworkBandwidth: resource.MustParse("0"),
		},
		NodeStrategies: []configuration.NodeSystemStrategy{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				SystemStrategy: &slov1alpha1.SystemStrategy{
					MinFreeKbytesFactor:   pointer.Int64(130),
					WatermarkScaleFactor:  pointer.Int64(151),
					MemcgReapBackGround:   pointer.Int64(1),
					TotalNetworkBandwidth: resource.MustParse("0"),
				},
			},
			{NodeCfgProfile: configuration.NodeCfgProfile{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"zzz": "zzz",
					},
				},
			},
				SystemStrategy: &slov1alpha1.SystemStrategy{
					MinFreeKbytesFactor:   pointer.Int64(140),
					WatermarkScaleFactor:  pointer.Int64(151),
					MemcgReapBackGround:   pointer.Int64(0),
					TotalNetworkBandwidth: resource.MustParse("0"),
				},
			},
		},
	}

	type args struct {
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name    string
		args    args
		want    *configuration.SystemCfg
		wantErr bool
	}{
		{
			name: "config is null! use oldConfig",
			args: args{
				configMap: &corev1.ConfigMap{},
			},
			want:    &defaultSLOCfg.SystemCfgMerged,
			wantErr: false,
		},
		{
			name: "throw error for configmap unmarshal failed",
			args: args{
				configMap: &corev1.ConfigMap{
					Data: map[string]string{
						configuration.SystemConfigKey: "invalid_content",
					},
				},
			},
			want:    &oldSLOCfg.SystemCfgMerged,
			wantErr: true,
		},
		{
			name: "cluster config only",
			args: args{
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						configuration.SystemConfigKey: string(testingCfgOnliClusterStr),
					},
				},
			},
			want: expectTestingCfgOnlyCluster,
		},
		{
			name: "node config merged",
			args: args{
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						configuration.SystemConfigKey: string(testingSystemConfig1Str),
					},
				},
			},
			want: expectTestingSystemConfig1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := calculateSystemConfigMerged(oldSLOCfg.SystemCfgMerged, tt.args.configMap)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, &got)
		})
	}
}

func Test_getHostApplicationConfig(t *testing.T) {
	type args struct {
		node *corev1.Node
		cfg  *configuration.HostApplicationCfg
	}
	testHostApp := &configuration.HostApplicationCfg{
		Applications: []slov1alpha1.HostApplicationSpec{
			{
				Name: "test-app-default",
			},
		},
		NodeConfigs: []configuration.NodeHostApplicationCfg{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-node-key": "test-node-value-A",
						},
					},
				},
				Applications: []slov1alpha1.HostApplicationSpec{
					{
						Name: "test-app-ls",
						QoS:  ext.QoSLS,
					},
				},
			},
		},
	}
	testHostAppMultiNodes := &configuration.HostApplicationCfg{
		Applications: []slov1alpha1.HostApplicationSpec{
			{
				Name: "test-app-default",
			},
		},
		NodeConfigs: []configuration.NodeHostApplicationCfg{
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-node-key-A": "test-node-value-A",
						},
					},
				},
				Applications: []slov1alpha1.HostApplicationSpec{
					{
						Name: "test-app-ls",
						QoS:  ext.QoSLS,
					},
				},
			},
			{
				NodeCfgProfile: configuration.NodeCfgProfile{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"test-node-key-B": "test-node-value-B",
						},
					},
				},
				Applications: []slov1alpha1.HostApplicationSpec{
					{
						Name: "test-app-lsr",
						QoS:  ext.QoSLSR,
					},
				},
			},
		},
	}
	tests := []struct {
		name    string
		args    args
		want    []slov1alpha1.HostApplicationSpec
		wantErr bool
	}{
		{
			name: "invalid node, use cluster config",
			args: args{
				node: &corev1.Node{},
				cfg:  testHostApp,
			},
			want:    testHostApp.Applications,
			wantErr: false,
		},
		{
			name: "use cluster config",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				cfg: testHostApp,
			},
			want:    testHostApp.Applications,
			wantErr: false,
		},
		{
			name: "use first matched node config",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							"test-node-key-A": "test-node-value-A",
							"test-node-key-B": "test-node-value-B",
						},
					},
				},
				cfg: testHostAppMultiNodes,
			},
			want:    testHostAppMultiNodes.NodeConfigs[0].Applications,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getHostApplicationConfig(tt.args.node, tt.args.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("getHostApplicationConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getHostApplicationConfig() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_calculateHostAppConfigMerged(t *testing.T) {
	hostAppOrigin := configuration.HostApplicationCfg{
		Applications: []slov1alpha1.HostApplicationSpec{
			{
				Name: "origin-host-app",
				QoS:  ext.QoSLS,
			},
		},
	}
	hostAppNew := configuration.HostApplicationCfg{
		Applications: []slov1alpha1.HostApplicationSpec{
			{
				Name: "new-host-app",
				QoS:  ext.QoSLS,
			},
		},
	}
	hostAppNewBytes, _ := json.Marshal(&hostAppNew)
	hostAppNewStr := string(hostAppNewBytes)
	hostAppBadStr := "bad-string"
	type args struct {
		oldCfg    configuration.HostApplicationCfg
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name    string
		args    args
		want    configuration.HostApplicationCfg
		wantErr bool
	}{
		{
			name: "configmap key not exist, use empty",
			args: args{
				oldCfg:    hostAppOrigin,
				configMap: &corev1.ConfigMap{Data: map[string]string{}},
			},
			want:    configuration.HostApplicationCfg{},
			wantErr: false,
		},
		{
			name: "bad configmap key, use old",
			args: args{
				oldCfg: hostAppOrigin,
				configMap: &corev1.ConfigMap{Data: map[string]string{
					configuration.HostApplicationConfigKey: hostAppBadStr,
				}},
			},
			want:    hostAppOrigin,
			wantErr: true,
		},
		{
			name: "parse from new host application",
			args: args{
				oldCfg: hostAppOrigin,
				configMap: &corev1.ConfigMap{Data: map[string]string{
					configuration.HostApplicationConfigKey: hostAppNewStr,
				}},
			},
			want:    hostAppNew,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := calculateHostAppConfigMerged(tt.args.oldCfg, tt.args.configMap)
			if (err != nil) != tt.wantErr {
				t.Errorf("calculateHostAppConfigMerged() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("calculateHostAppConfigMerged() got = %v, want %v", got, tt.want)
			}
		})
	}
}
