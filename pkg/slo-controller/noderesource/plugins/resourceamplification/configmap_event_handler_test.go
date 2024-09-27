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

func TestDefaultResourceAmplificationCfg(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		expected := &configuration.ResourceAmplificationCfg{
			ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
				Enable: pointer.Bool(false),
			},
		}
		got := DefaultResourceAmplificationCfg()
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
			configuration.ResourceAmplificationConfigKey: `
{
  "enable": true,
  "resourceAmplificationRatio": {
    "cpu": 1.1,
    "memory": 1.2
  }
}
`,
		},
	}
	t.Run("test", func(t *testing.T) {
		h := newConfigHandler(fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
			DefaultResourceAmplificationCfg(), &record.FakeRecorder{})
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
			configuration.ResourceAmplificationConfigKey: `
{
  "enable": true,
  "resourceAmplificationRatio": {
    "cpu": 1.1,
    "memory": 1.2
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
			configuration.ResourceAmplificationConfigKey: `
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
			configuration.ResourceAmplificationConfigKey: `
{
  "enable": true,
  "resourceAmplificationRatio": {
    "cpu": 2.5,
    "memory": 3.5
  },
  "nodeConfigs": [
        {
          "name": "selector-test",
          "nodeSelector": {
              "matchLabels": {
                "node.koordinator.sh/cpunormalization": "true"
              }
          },
          "resourceAmplificationRatio": {
            "cpu": 1.5,
            "memory": 1.6
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
		initCfg *configuration.ResourceAmplificationCfg
	}
	var tests = []struct {
		name      string
		fields    fields
		arg       *corev1.ConfigMap
		want      bool
		wantField *configuration.ResourceAmplificationCfg
	}{
		{
			name: "failed to get configmap",
			fields: fields{
				c:       fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
				initCfg: DefaultResourceAmplificationCfg(),
			},
			arg:       nil,
			want:      false,
			wantField: DefaultResourceAmplificationCfg(),
		},
		{
			name: "failed to get amplification key",
			fields: fields{
				c:       fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
				initCfg: DefaultResourceAmplificationCfg(),
			},
			arg: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sloconfig.SLOCtrlConfigMap,
					Namespace: sloconfig.ConfigNameSpace,
				},
				Data: map[string]string{},
			},
			want:      false,
			wantField: DefaultResourceAmplificationCfg(),
		},
		{
			name: "sync cache changed",
			fields: fields{
				c:       fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(testConfigMap).Build(),
				initCfg: DefaultResourceAmplificationCfg(),
			},
			arg:  testConfigMap,
			want: true,
			wantField: &configuration.ResourceAmplificationCfg{
				ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
					Enable: pointer.Bool(true),
					ResourceAmplificationRatio: map[corev1.ResourceName]float64{
						corev1.ResourceCPU:    1.1,
						corev1.ResourceMemory: 1.2,
					},
				},
			},
		},
		{
			name: "sync cache unchanged",
			fields: fields{
				c:       fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(testConfigMap1).Build(),
				initCfg: DefaultResourceAmplificationCfg(),
			},
			arg:       testConfigMap1,
			want:      false,
			wantField: DefaultResourceAmplificationCfg(),
		},
		{
			name: "sync cache changed with node configs",
			fields: fields{
				c: fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(testConfigMap).Build(),
				initCfg: &configuration.ResourceAmplificationCfg{
					ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
						Enable: pointer.Bool(true),
						ResourceAmplificationRatio: map[corev1.ResourceName]float64{
							corev1.ResourceCPU:    1.1,
							corev1.ResourceMemory: 1.2,
						},
					},
				},
			},
			arg:  testConfigMap2,
			want: true,
			wantField: &configuration.ResourceAmplificationCfg{
				ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
					Enable: pointer.Bool(true),
					ResourceAmplificationRatio: map[corev1.ResourceName]float64{
						corev1.ResourceCPU:    2.5,
						corev1.ResourceMemory: 3.5,
					},
				},
				NodeConfigs: []configuration.NodeResourceAmplificationCfg{
					{
						NodeCfgProfile: configuration.NodeCfgProfile{
							Name: "selector-test",
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"node.koordinator.sh/cpunormalization": "true",
								},
							},
						},
						ResourceAmplificationStrategy: configuration.ResourceAmplificationStrategy{
							Enable: pointer.Bool(true),
							ResourceAmplificationRatio: map[corev1.ResourceName]float64{
								corev1.ResourceCPU:    1.5,
								corev1.ResourceMemory: 1.6,
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
