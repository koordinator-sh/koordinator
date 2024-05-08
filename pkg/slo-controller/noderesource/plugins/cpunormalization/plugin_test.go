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

package cpunormalization

import (
	"testing"

	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/scheme"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util/testutil"
)

func TestPlugin(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		defer testPluginCleanup()
		p := &Plugin{}
		assert.Equal(t, PluginName, p.Name())
		testScheme := runtime.NewScheme()
		testOpt := &framework.Option{
			Scheme:   testScheme,
			Client:   fake.NewClientBuilder().WithScheme(testScheme).Build(),
			Builder:  builder.ControllerManagedBy(&testutil.FakeManager{}),
			Recorder: &record.FakeRecorder{},
		}
		err := p.Setup(testOpt)
		assert.NoError(t, err)
		got := p.Reset(nil, "")
		assert.Nil(t, got)
	})
}

func TestPluginNeedSyncMeta(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Annotations: map[string]string{
				extension.AnnotationCPUNormalizationRatio: "1.10",
			},
		},
	}
	testNodeHasNoRatio := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	testNodeRatioClose := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Annotations: map[string]string{
				extension.AnnotationCPUNormalizationRatio: "1.099999999",
			},
		},
	}
	type args struct {
		oldNode *corev1.Node
		newNode *corev1.Node
	}
	tests := []struct {
		name  string
		args  args
		want  bool
		want1 string
	}{
		{
			name: "need sync when failed to parse old ratio",
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "[}",
						},
					},
				},
				newNode: testNode,
			},
			want:  true,
			want1: "old ratio parsed error",
		},
		{
			name: "abort sync when failed to parse new ratio",
			args: args{
				oldNode: testNode,
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "[}",
						},
					},
				},
			},
			want:  false,
			want1: "new ratio parsed error",
		},
		{
			name: "no need sync when both have no ratio",
			args: args{
				oldNode: testNodeHasNoRatio,
				newNode: testNodeHasNoRatio,
			},
			want:  false,
			want1: "ratios are nil",
		},
		{
			name: "need sync when old has no ratio",
			args: args{
				oldNode: testNodeHasNoRatio,
				newNode: testNode,
			},
			want:  true,
			want1: "old ratio is nil",
		},
		{
			name: "need sync when new has no ratio",
			args: args{
				oldNode: testNode,
				newNode: testNodeHasNoRatio,
			},
			want:  true,
			want1: "new ratio is nil",
		},
		{
			name: "skip sync when ratio is unchanged",
			args: args{
				oldNode: testNode,
				newNode: testNode,
			},
			want:  false,
			want1: "ratios are close",
		},
		{
			name: "skip sync when ratio are close",
			args: args{
				oldNode: testNode,
				newNode: testNodeRatioClose,
			},
			want:  false,
			want1: "ratios are close",
		},
		{
			name: "need sync when ratio is different",
			args: args{
				oldNode: testNode,
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							extension.AnnotationCPUNormalizationRatio: "1.20",
						},
					},
				},
			},
			want:  true,
			want1: "ratio is different",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			got, got1 := p.NeedSyncMeta(nil, tt.args.oldNode, tt.args.newNode)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestPluginPrepare(t *testing.T) {
	type args struct {
		node *corev1.Node
		nr   *framework.NodeResource
	}
	tests := []struct {
		name       string
		args       args
		wantErr    bool
		wantField  *corev1.Node
		wantField1 *framework.NodeResource
	}{
		{
			name: "skip when no annotation to prepare",
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
			name: "prepare ratio successfully",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
				},
				nr: &framework.NodeResource{
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
		},
		{
			name: "prepare ratio successfully 1",
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
						extension.AnnotationCPUNormalizationRatio: "1.20",
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						"xxx": "yyy",
						extension.AnnotationCPUNormalizationRatio: "1.20",
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
			if tt.wantField1 != nil {
				assert.Equal(t, tt.wantField1, tt.args.nr)
			}
		})
	}
}

func TestPluginCalculate(t *testing.T) {
	testScheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = topov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}
	testNRT := &topov1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Annotations: map[string]string{
				extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX", "hyperThreadEnabled": true, "turboEnabled": true}`,
			},
		},
	}
	type fields struct {
		handler *configHandler
	}
	type args struct {
		node *corev1.Node
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []framework.ResourceItem
		wantErr bool
	}{
		{
			name: "get configmap cache failed",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(),
					cache: &cfgCache{
						available: false,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "failed to get node config",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
					cache: &cfgCache{
						available: true,
						config:    DefaultCPUNormalizationCfg(),
					},
				},
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelCPUNormalizationEnabled: "{]",
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "node strategy is disabled",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable:     pointer.Bool(false),
								RatioModel: map[string]configuration.ModelRatioCfg{},
							},
						},
					},
				},
			},
			args: args{
				node: testNode,
			},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: defaultRatioStr,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "failed to get NRT",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable:     pointer.Bool(true),
								RatioModel: map[string]configuration.ModelRatioCfg{},
							},
						},
					},
				},
			},
			args: args{
				node: testNode,
			},
			wantErr: true,
		},
		{
			name: "failed to get CPUBasicInfo",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-node",
						},
					}).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable:     pointer.Bool(true),
								RatioModel: map[string]configuration.ModelRatioCfg{},
							},
						},
					},
				},
			},
			args: args{
				node: testNode,
			},
			wantErr: true,
		},
		{
			name: "failed to parse CPUBasicInfo",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-node",
							Annotations: map[string]string{
								extension.AnnotationCPUBasicInfo: "{]",
							},
						},
					}).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable:     pointer.Bool(true),
								RatioModel: map[string]configuration.ModelRatioCfg{},
							},
						},
					},
				},
			},
			args: args{
				node: testNode,
			},
			wantErr: true,
		},
		{
			name: "failed to get ratio",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(testNRT).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable: pointer.Bool(true),
								RatioModel: map[string]configuration.ModelRatioCfg{
									"XXX": {
										BaseRatio: pointer.Float64(1.0),
									},
								},
							},
						},
					},
				},
			},
			args: args{
				node: testNode,
			},
			wantErr: true,
		},
		{
			name: "failed to validate ratio",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(testNRT).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable: pointer.Bool(true),
								RatioModel: map[string]configuration.ModelRatioCfg{
									"XXX": {
										HyperThreadTurboEnabledRatio: pointer.Float64(10),
									},
								},
							},
						},
					},
				},
			},
			args: args{
				node: testNode,
			},
			wantErr: true,
		},
		{
			name: "get ratio correctly",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(testNRT).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable: pointer.Bool(true),
								RatioModel: map[string]configuration.ModelRatioCfg{
									"XXX": {
										BaseRatio:                    pointer.Float64(1.5),
										TurboEnabledRatio:            pointer.Float64(1.65),
										HyperThreadEnabledRatio:      pointer.Float64(1.0),
										HyperThreadTurboEnabledRatio: pointer.Float64(1.1),
									},
								},
							},
						},
					},
				},
			},
			args: args{
				node: testNode,
			},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "get ratio correctly 1",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-node-1",
							Annotations: map[string]string{
								extension.AnnotationCPUBasicInfo: `{"cpuModel": "XXX", "hyperThreadEnabled": true, "turboEnabled": true}`,
							},
						},
					}).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable: pointer.Bool(false),
								RatioModel: map[string]configuration.ModelRatioCfg{
									"XXX": {
										BaseRatio:                    pointer.Float64(1.0),
										TurboEnabledRatio:            pointer.Float64(1.0),
										HyperThreadEnabledRatio:      pointer.Float64(1.0),
										HyperThreadTurboEnabledRatio: pointer.Float64(1.0),
									},
								},
							},
							NodeConfigs: []configuration.NodeCPUNormalizationCfg{
								{
									NodeCfgProfile: configuration.NodeCfgProfile{
										NodeSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"xxx": "yyy",
											},
										},
									},
									CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
										Enable: pointer.Bool(true),
										RatioModel: map[string]configuration.ModelRatioCfg{
											"XXX": {
												BaseRatio:                    pointer.Float64(1.5),
												TurboEnabledRatio:            pointer.Float64(1.65),
												HyperThreadEnabledRatio:      pointer.Float64(1.0),
												HyperThreadTurboEnabledRatio: pointer.Float64(1.1),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-1",
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "get ratio when strategy disable but not enabled",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(testNRT).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable: pointer.Bool(false),
								RatioModel: map[string]configuration.ModelRatioCfg{
									"XXX": {
										BaseRatio:                    pointer.Float64(1.5),
										TurboEnabledRatio:            pointer.Float64(1.65),
										HyperThreadEnabledRatio:      pointer.Float64(1.0),
										HyperThreadTurboEnabledRatio: pointer.Float64(1.1),
									},
								},
							},
						},
					},
				},
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelCPUNormalizationEnabled: "true",
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "1.10",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "strategy enabled but node disabled",
			fields: fields{
				handler: &configHandler{
					Client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(testNRT).Build(),
					cache: &cfgCache{
						available: true,
						config: &configuration.CPUNormalizationCfg{
							CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
								Enable: pointer.Bool(true),
								RatioModel: map[string]configuration.ModelRatioCfg{
									"XXX": {
										BaseRatio:                    pointer.Float64(1.5),
										TurboEnabledRatio:            pointer.Float64(1.65),
										HyperThreadEnabledRatio:      pointer.Float64(1.0),
										HyperThreadTurboEnabledRatio: pointer.Float64(1.1),
									},
								},
							},
						},
					},
				},
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Labels: map[string]string{
							extension.LabelCPUNormalizationEnabled: "false",
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name: PluginName,
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: defaultRatioStr,
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer testPluginCleanup()
			p := Plugin{}
			if tt.fields.handler != nil {
				cfgHandler = tt.fields.handler
				if tt.fields.handler.Client != nil {
					client = tt.fields.handler.Client
				}
			}

			got, gotErr := p.Calculate(nil, tt.args.node, nil, nil)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}

func Test_getCPUNormalizationRatioFromModel(t *testing.T) {
	type args struct {
		info     *extension.CPUBasicInfo
		strategy *configuration.CPUNormalizationStrategy
	}
	tests := []struct {
		name    string
		args    args
		want    float64
		wantErr bool
	}{
		{
			name: "nil model",
			args: args{
				strategy: &configuration.CPUNormalizationStrategy{
					Enable:     pointer.Bool(true),
					RatioModel: nil,
				},
			},
			want:    -1,
			wantErr: true,
		},
		{
			name: "no ratio for model",
			args: args{
				info: &extension.CPUBasicInfo{
					CPUModel: "CPU XXX",
				},
				strategy: &configuration.CPUNormalizationStrategy{
					Enable: pointer.Bool(true),
					RatioModel: map[string]configuration.ModelRatioCfg{
						"CPU YYY": {
							BaseRatio: pointer.Float64(1.0),
						},
					},
				},
			},
			want:    -1,
			wantErr: true,
		},
		{
			name: "missing ratio for Turbo and HT on",
			args: args{
				info: &extension.CPUBasicInfo{
					CPUModel:           "CPU XXX",
					TurboEnabled:       true,
					HyperThreadEnabled: true,
				},
				strategy: &configuration.CPUNormalizationStrategy{
					Enable: pointer.Bool(true),
					RatioModel: map[string]configuration.ModelRatioCfg{
						"CPU XXX": {
							BaseRatio: pointer.Float64(1.5),
						},
					},
				},
			},
			want:    -1,
			wantErr: true,
		},
		{
			name: "got ratio for Turbo and HT on",
			args: args{
				info: &extension.CPUBasicInfo{
					CPUModel:           "CPU XXX",
					TurboEnabled:       true,
					HyperThreadEnabled: true,
				},
				strategy: &configuration.CPUNormalizationStrategy{
					Enable: pointer.Bool(true),
					RatioModel: map[string]configuration.ModelRatioCfg{
						"CPU XXX": {
							BaseRatio:                    pointer.Float64(1.5),
							TurboEnabledRatio:            pointer.Float64(1.65),
							HyperThreadEnabledRatio:      pointer.Float64(1.0),
							HyperThreadTurboEnabledRatio: pointer.Float64(1.1),
						},
					},
				},
			},
			want:    1.1,
			wantErr: false,
		},
		{
			name: "got ratio for HT on",
			args: args{
				info: &extension.CPUBasicInfo{
					CPUModel:           "CPU XXX",
					HyperThreadEnabled: true,
				},
				strategy: &configuration.CPUNormalizationStrategy{
					Enable: pointer.Bool(true),
					RatioModel: map[string]configuration.ModelRatioCfg{
						"CPU XXX": {
							BaseRatio:                    pointer.Float64(1.5),
							TurboEnabledRatio:            pointer.Float64(1.65),
							HyperThreadEnabledRatio:      pointer.Float64(1.0),
							HyperThreadTurboEnabledRatio: pointer.Float64(1.1),
						},
					},
				},
			},
			want:    1.0,
			wantErr: false,
		},
		{
			name: "got ratio for Turbo on",
			args: args{
				info: &extension.CPUBasicInfo{
					CPUModel:     "CPU XXX",
					TurboEnabled: true,
				},
				strategy: &configuration.CPUNormalizationStrategy{
					Enable: pointer.Bool(true),
					RatioModel: map[string]configuration.ModelRatioCfg{
						"CPU XXX": {
							BaseRatio:                    pointer.Float64(1.5),
							TurboEnabledRatio:            pointer.Float64(1.65),
							HyperThreadEnabledRatio:      pointer.Float64(1.0),
							HyperThreadTurboEnabledRatio: pointer.Float64(1.1),
						},
					},
				},
			},
			want:    1.65,
			wantErr: false,
		},
		{
			name: "got ratio for basic setting",
			args: args{
				info: &extension.CPUBasicInfo{
					CPUModel: "CPU XXX",
				},
				strategy: &configuration.CPUNormalizationStrategy{
					Enable: pointer.Bool(true),
					RatioModel: map[string]configuration.ModelRatioCfg{
						"CPU XXX": {
							BaseRatio:                    pointer.Float64(1.5),
							TurboEnabledRatio:            pointer.Float64(1.65),
							HyperThreadEnabledRatio:      pointer.Float64(1.0),
							HyperThreadTurboEnabledRatio: pointer.Float64(1.1),
						},
					},
				},
			},
			want:    1.5,
			wantErr: false,
		},
		{
			name: "missing ratio for basic setting",
			args: args{
				info: &extension.CPUBasicInfo{
					CPUModel: "CPU XXX",
				},
				strategy: &configuration.CPUNormalizationStrategy{
					Enable: pointer.Bool(true),
					RatioModel: map[string]configuration.ModelRatioCfg{
						"CPU XXX": {},
					},
				},
			},
			want:    -1,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := getCPUNormalizationRatioFromModel(tt.args.info, tt.args.strategy)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func testPluginCleanup() {
	client = nil
	cfgHandler = nil
}
