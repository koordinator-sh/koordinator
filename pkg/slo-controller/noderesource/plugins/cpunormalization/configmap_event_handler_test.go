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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func TestDefaultCPUNormalizationCfg(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		expected := &configuration.CPUNormalizationCfg{
			CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
				Enable:     pointer.Bool(false),
				RatioModel: map[string]configuration.ModelRatioCfg{},
			},
		}
		got := DefaultCPUNormalizationCfg()
		assert.Equal(t, expected, got)
	})
}

func Test_configHandler_IsCfgAvailable(t *testing.T) {
	testConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sloconfig.SLOCtrlConfigMap,
			Namespace: sloconfig.ConfigNameSpace,
		},
		Data: map[string]string{
			configuration.CPUNormalizationConfigKey: `
{
  "enable": true,
  "ratioModel": {
    "Intel(R) Xeon(R) Platinum XXX CPU @ 2.50GHz": {
      "baseRatio": 1.5,
      "hyperThreadEnabledRatio": 1.0,
      "turboEnabledRatio": 1.8,
      "hyperThreadTurboEnabledRatio": 1.2
    },
    "Intel(R) Xeon(R) Platinum YYY CPU @ 2.50GHz": {
      "baseRatio": 1.8,
      "hyperThreadEnabledRatio": 1.2,
      "turboEnabledRatio": 2.16,
      "hyperThreadTurboEnabledRatio": 1.44
    }
  }
}
`,
		},
	}
	t.Run("test", func(t *testing.T) {
		h := newConfigHandler(fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
			DefaultCPUNormalizationCfg(), &record.FakeRecorder{})
		got := h.IsCfgAvailable()
		assert.True(t, got)

		err := h.Client.Create(context.TODO(), testConfigMap)
		assert.NoError(t, err)
		got = h.IsCfgAvailable()
		assert.True(t, got)
	})
}

func Test_configHandler_syncCacheIfCfgChanged(t *testing.T) {
	testConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sloconfig.SLOCtrlConfigMap,
			Namespace: sloconfig.ConfigNameSpace,
		},
		Data: map[string]string{
			configuration.CPUNormalizationConfigKey: `
{
  "enable": true,
  "ratioModel": {
    "Intel(R) Xeon(R) Platinum XXX CPU @ 2.50GHz": {
      "baseRatio": 1.5,
      "hyperThreadEnabledRatio": 1.0,
      "turboEnabledRatio": 1.8,
      "hyperThreadTurboEnabledRatio": 1.2
    },
    "Intel(R) Xeon(R) Platinum YYY CPU @ 2.50GHz": {
      "baseRatio": 1.8,
      "hyperThreadEnabledRatio": 1.2,
      "turboEnabledRatio": 2.16,
      "hyperThreadTurboEnabledRatio": 1.44
    }
  }
}
`,
		},
	}
	testConfigMap1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sloconfig.SLOCtrlConfigMap,
			Namespace: sloconfig.ConfigNameSpace,
		},
		Data: map[string]string{
			configuration.CPUNormalizationConfigKey: `
{
  "enable": false
}
`,
		},
	}
	testConfigMap2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sloconfig.SLOCtrlConfigMap,
			Namespace: sloconfig.ConfigNameSpace,
		},
		Data: map[string]string{
			configuration.CPUNormalizationConfigKey: `
{
  "enable": false,
  "ratioModel": {
    "Intel(R) Xeon(R) Platinum XXX CPU @ 2.50GHz": {
      "baseRatio": 1.5,
      "hyperThreadEnabledRatio": 1.0,
      "turboEnabledRatio": 1.8,
      "hyperThreadTurboEnabledRatio": 1.2
    },
    "Intel(R) Xeon(R) Platinum YYY CPU @ 2.50GHz": {
      "baseRatio": 1.8,
      "hyperThreadEnabledRatio": 1.2,
      "turboEnabledRatio": 2.16,
      "hyperThreadTurboEnabledRatio": 1.44
    }
  },
  "nodeConfigs": [
    {
      "nodeSelector": {
        "matchLabels": {
          "test-cpu-normalization": "true"
        }
      },
      "enable": true
    }
  ]
}
`,
		},
	}
	type fields struct {
		c       ctrlclient.Client
		initCfg *configuration.CPUNormalizationCfg
	}
	tests := []struct {
		name      string
		fields    fields
		arg       *corev1.ConfigMap
		want      bool
		wantField *configuration.CPUNormalizationCfg
	}{
		{
			name: "failed to get configmap",
			fields: fields{
				c:       fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
				initCfg: DefaultCPUNormalizationCfg(),
			},
			arg:       nil,
			want:      false,
			wantField: DefaultCPUNormalizationCfg(),
		},
		{
			name: "failed to get cpu normalization key",
			fields: fields{
				c:       fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
				initCfg: DefaultCPUNormalizationCfg(),
			},
			arg: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{},
			},
			want:      false,
			wantField: DefaultCPUNormalizationCfg(),
		},
		{
			name: "sync cache changed",
			fields: fields{
				c:       fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(testConfigMap).Build(),
				initCfg: DefaultCPUNormalizationCfg(),
			},
			arg:  testConfigMap,
			want: true,
			wantField: &configuration.CPUNormalizationCfg{
				CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
					Enable: pointer.Bool(true),
					RatioModel: map[string]configuration.ModelRatioCfg{
						"Intel(R) Xeon(R) Platinum XXX CPU @ 2.50GHz": {
							BaseRatio:                    pointer.Float64(1.5),
							HyperThreadEnabledRatio:      pointer.Float64(1.0),
							TurboEnabledRatio:            pointer.Float64(1.8),
							HyperThreadTurboEnabledRatio: pointer.Float64(1.2),
						},
						"Intel(R) Xeon(R) Platinum YYY CPU @ 2.50GHz": {
							BaseRatio:                    pointer.Float64(1.8),
							HyperThreadEnabledRatio:      pointer.Float64(1.2),
							TurboEnabledRatio:            pointer.Float64(2.16),
							HyperThreadTurboEnabledRatio: pointer.Float64(1.44),
						},
					},
				},
			},
		},
		{
			name: "sync cache unchanged",
			fields: fields{
				c:       fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(testConfigMap1).Build(),
				initCfg: DefaultCPUNormalizationCfg(),
			},
			arg:       testConfigMap1,
			want:      false,
			wantField: DefaultCPUNormalizationCfg(),
		},
		{
			name: "sync cache changed with node configs",
			fields: fields{
				c: fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(testConfigMap).Build(),
				initCfg: &configuration.CPUNormalizationCfg{
					CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
						Enable: pointer.Bool(false),
						RatioModel: map[string]configuration.ModelRatioCfg{
							"Intel(R) Xeon(R) Platinum XXX CPU @ 2.50GHz": {
								BaseRatio:                    pointer.Float64(1.5),
								HyperThreadEnabledRatio:      pointer.Float64(1.0),
								TurboEnabledRatio:            pointer.Float64(1.8),
								HyperThreadTurboEnabledRatio: pointer.Float64(1.2),
							},
							"Intel(R) Xeon(R) Platinum YYY CPU @ 2.50GHz": {
								BaseRatio:                    pointer.Float64(1.8),
								HyperThreadEnabledRatio:      pointer.Float64(1.2),
								TurboEnabledRatio:            pointer.Float64(2.16),
								HyperThreadTurboEnabledRatio: pointer.Float64(1.44),
							},
						},
					},
				},
			},
			arg:  testConfigMap2,
			want: true,
			wantField: &configuration.CPUNormalizationCfg{
				CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
					Enable: pointer.Bool(false),
					RatioModel: map[string]configuration.ModelRatioCfg{
						"Intel(R) Xeon(R) Platinum XXX CPU @ 2.50GHz": {
							BaseRatio:                    pointer.Float64(1.5),
							HyperThreadEnabledRatio:      pointer.Float64(1.0),
							TurboEnabledRatio:            pointer.Float64(1.8),
							HyperThreadTurboEnabledRatio: pointer.Float64(1.2),
						},
						"Intel(R) Xeon(R) Platinum YYY CPU @ 2.50GHz": {
							BaseRatio:                    pointer.Float64(1.8),
							HyperThreadEnabledRatio:      pointer.Float64(1.2),
							TurboEnabledRatio:            pointer.Float64(2.16),
							HyperThreadTurboEnabledRatio: pointer.Float64(1.44),
						},
					},
				},
				NodeConfigs: []configuration.NodeCPUNormalizationCfg{
					{
						NodeCfgProfile: configuration.NodeCfgProfile{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"test-cpu-normalization": "true",
								},
							},
						},
						CPUNormalizationStrategy: configuration.CPUNormalizationStrategy{
							Enable: pointer.Bool(true),
							RatioModel: map[string]configuration.ModelRatioCfg{
								"Intel(R) Xeon(R) Platinum XXX CPU @ 2.50GHz": {
									BaseRatio:                    pointer.Float64(1.5),
									HyperThreadEnabledRatio:      pointer.Float64(1.0),
									TurboEnabledRatio:            pointer.Float64(1.8),
									HyperThreadTurboEnabledRatio: pointer.Float64(1.2),
								},
								"Intel(R) Xeon(R) Platinum YYY CPU @ 2.50GHz": {
									BaseRatio:                    pointer.Float64(1.8),
									HyperThreadEnabledRatio:      pointer.Float64(1.2),
									TurboEnabledRatio:            pointer.Float64(2.16),
									HyperThreadTurboEnabledRatio: pointer.Float64(1.44),
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newConfigHandler(tt.fields.c, tt.fields.initCfg, &record.FakeRecorder{})
			got := h.syncCacheIfCfgChanged(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantField, h.GetCfgCopy())
		})
	}
}
