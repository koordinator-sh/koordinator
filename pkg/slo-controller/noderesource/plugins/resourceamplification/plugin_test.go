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

package resourceamplification

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
)

func TestPluginNeedSyncMeta(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Annotations: map[string]string{
				extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.12}`,
			},
		},
	}
	testNodeHasNoRatio := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	type args struct {
		oldNode *corev1.Node
		newNode *corev1.Node
	}
	tests := []struct {
		name       string
		args       args
		want       bool
		wantReason string
	}{
		{
			name: "no need sync when both have no ratio",
			args: args{
				oldNode: testNodeHasNoRatio,
				newNode: testNodeHasNoRatio,
			},
			want:       false,
			wantReason: "ratio remains empty",
		},
		{
			name: "need sync when old has no ratio",
			args: args{
				oldNode: testNodeHasNoRatio,
				newNode: testNode,
			},
			want:       true,
			wantReason: "old ratio is empty",
		},
		{
			name: "need sync when new has no ratio",
			args: args{
				oldNode: testNode,
				newNode: testNodeHasNoRatio,
			},
			want:       true,
			wantReason: "new ratio is empty",
		},
		{
			name: "skip sync when ratio is unchanged",
			args: args{
				oldNode: testNode,
				newNode: testNode,
			},
			want:       false,
			wantReason: "ratio remains unchanged",
		},
		{
			name: "need sync when ratio is different",
			args: args{
				oldNode: testNode,
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.23}`,
						},
					},
				},
			},
			want:       true,
			wantReason: "ratio changed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			got, gotReason := p.NeedSyncMeta(nil, tt.args.oldNode, tt.args.newNode)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantReason, gotReason)
		})
	}
}

func TestPluginPrepare(t *testing.T) {
	type args struct {
		node *corev1.Node
		nr   *framework.NodeResource
	}
	tests := []struct {
		name      string
		args      args
		wantErr   bool
		wantField *corev1.Node
	}{
		{
			name: "no annotation to prepare",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				nr: framework.NewNodeResource(),
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
			},
		},
		{
			name: "remove old annotation when no annotation prepare",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
						},
					},
				},
				nr: framework.NewNodeResource(),
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-node",
					Annotations: map[string]string{},
				},
			},
		},
		{
			name: "prepare ratio successfully",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				nr: &framework.NodeResource{
					Annotations: map[string]string{
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
		},
		{
			name: "prepare ratio successfully with other existing annotations",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							"xxx": "yyy",
						},
					},
				},
				nr: &framework.NodeResource{
					Annotations: map[string]string{
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						"xxx": "yyy",
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			gotErr := p.Prepare(nil, tt.args.node, tt.args.nr)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.wantField, tt.args.node)
		})
	}
}

func TestPluginCalculate(t *testing.T) {
	type args struct {
		node *corev1.Node
	}
	type fields struct {
		handler *configHandler
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []framework.ResourceItem
		wantErr bool
	}{
		{
			name: "get cpu normalization ratio failed",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config:    &configuration.ResourceAmplificationCfg{},
					},
				}},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "invalid",
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "calculate ratio correctly with cpu normalization",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config:    &configuration.ResourceAmplificationCfg{},
					},
				}},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "1.22",
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.22}`,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate ratio correctly without cpu normalization",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config:    &configuration.ResourceAmplificationCfg{},
					},
				}},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
				},
			},
			wantErr: false,
		},
		{
			name: "calculate ratio with amplification ratio",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
			},
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.ResourceAmplificationCfg{
							ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
								Enable: pointer.Bool(true),
								ResourceAmplificationRatio: map[corev1.ResourceName]float64{
									corev1.ResourceCPU:    1.1,
									corev1.ResourceMemory: 2.1,
								},
							},
						},
					},
				}},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.10,"memory":2.10}`,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate ratio with both cpu normalization ratio and amplification ratio",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "1.20",
						},
					},
				},
			},
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.ResourceAmplificationCfg{
							ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
								Enable: pointer.Bool(true),
								ResourceAmplificationRatio: map[corev1.ResourceName]float64{
									corev1.ResourceCPU:    1.5,
									corev1.ResourceMemory: 2.5,
								},
							},
						},
					},
				}},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.80,"memory":2.50}`,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate ratio with cpu normalization ratio and amplification memory ratio",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "1.50",
						},
					},
				},
			},
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.ResourceAmplificationCfg{
							ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
								Enable: pointer.Bool(true),
								ResourceAmplificationRatio: map[corev1.ResourceName]float64{
									corev1.ResourceMemory: 3.5,
								},
							},
						},
					},
				}},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.50,"memory":3.50}`,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate ratio with cpu normalization ratio and other resource type ratio",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "1.50",
						},
					},
				},
			},
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.ResourceAmplificationCfg{
							ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
								Enable: pointer.Bool(true),
								ResourceAmplificationRatio: map[corev1.ResourceName]float64{
									corev1.ResourceMemory:  3.5,
									corev1.ResourceStorage: 1.8,
								},
							},
						},
					},
				}},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.50,"memory":3.50,"storage":1.80}`,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate ratio with final cpu normalization ratio > 1 correctly",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "1.50",
						},
					},
				},
			},
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.ResourceAmplificationCfg{
							ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
								Enable: pointer.Bool(true),
								ResourceAmplificationRatio: map[corev1.ResourceName]float64{
									corev1.ResourceCPU:     0.8,
									corev1.ResourceMemory:  3.5,
									corev1.ResourceStorage: 1.8,
								},
							},
						},
					},
				}},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationNodeResourceAmplificationRatio: `{"cpu":1.20,"memory":3.50,"storage":1.80}`,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate ratio with final cpu normalization ratio < 1 invalid",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "1.50",
						},
					},
				},
			},
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.ResourceAmplificationCfg{
							ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
								Enable: pointer.Bool(true),
								ResourceAmplificationRatio: map[corev1.ResourceName]float64{
									corev1.ResourceCPU:     0.5,
									corev1.ResourceMemory:  3.5,
									corev1.ResourceStorage: 1.8,
								},
							},
						},
					},
				}},
			wantErr: true,
		},
		{
			name: "calculate ratio with one resource amplification ratio invalid",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "1.50",
						},
					},
				},
			},
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.ResourceAmplificationCfg{
							ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
								Enable: pointer.Bool(true),
								ResourceAmplificationRatio: map[corev1.ResourceName]float64{
									corev1.ResourceMemory:  3.5,
									corev1.ResourceStorage: 0.5,
								},
							},
						},
					},
				}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer testPluginCleanup()
			p := Plugin{}
			if tt.fields.handler != nil {
				cfgHandler = tt.fields.handler
			}
			got, gotErr := p.Calculate(nil, tt.args.node, nil, nil)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}

func testPluginCleanup() {
	cfgHandler = nil
}
