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
	"k8s.io/utils/pointer"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
)

func Test_syncNodeSLOSpecIfChanged(t *testing.T) {
	oldSLOCfg := DefaultSLOCfg()
	testingConfigMap1 := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.SLOCtrlConfigMap,
			Namespace: config.ConfigNameSpace,
		},
		Data: map[string]string{
			config.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
			config.ResourceQoSConfigKey: `
{
  "clusterStrategy": {
    "be": {
      "cpuQoS": {
        "groupIdentity": 0
      }
    }
  }
}
`,
			config.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
		},
	}

	expectTestingCfg1 := oldSLOCfg.DeepCopy()
	expectTestingCfg1.ThresholdCfgMerged.ClusterStrategy.Enable = pointer.BoolPtr(true)
	expectTestingCfg1.ThresholdCfgMerged.ClusterStrategy.CPUSuppressThresholdPercent = pointer.Int64Ptr(60)

	expectTestingCfg1.ResourceQoSCfgMerged.ClusterStrategy = &slov1alpha1.ResourceQoSStrategy{
		BE: &slov1alpha1.ResourceQoS{
			CPUQoS: &slov1alpha1.CPUQoSCfg{
				CPUQoS: slov1alpha1.CPUQoS{
					GroupIdentity: pointer.Int64Ptr(0),
				},
			},
		},
	}

	expectTestingCfg1.CPUBurstCfgMerged.ClusterStrategy.CFSQuotaBurstPeriodSeconds = pointer.Int64Ptr(60)

	type fields struct {
		oldCfg *SLOCfgCache
	}
	type args struct {
		configMap *corev1.ConfigMap
	}
	tests := []struct {
		name        string
		fields      fields
		args        args
		wantChanged bool
		wantField   *SLOCfgCache
	}{
		{
			name:        "configmap is nil, use default cfg",
			fields:      fields{oldCfg: &SLOCfgCache{}},
			args:        args{configMap: nil},
			wantChanged: true,
			wantField:   &SLOCfgCache{sloCfg: DefaultSLOCfg(), isAvailable: true},
		},
		{
			name:        "configmap is nil, old is default, then not changed",
			fields:      fields{oldCfg: &SLOCfgCache{sloCfg: DefaultSLOCfg(), isAvailable: true}},
			args:        args{configMap: nil},
			wantChanged: false,
			wantField:   &SLOCfgCache{sloCfg: DefaultSLOCfg(), isAvailable: true},
		},
		{
			name: "no slo config in configmap, keep the old,and set available",
			fields: fields{oldCfg: &SLOCfgCache{
				sloCfg:      DefaultSLOCfg(),
				isAvailable: false,
			}},
			args:        args{configMap: &corev1.ConfigMap{}},
			wantChanged: false,
			wantField: &SLOCfgCache{
				sloCfg:      DefaultSLOCfg(),
				isAvailable: true,
			},
		},
		{
			name: "unmarshal config failed, keep the old",
			fields: fields{oldCfg: &SLOCfgCache{
				sloCfg:      *oldSLOCfg.DeepCopy(),
				isAvailable: true,
			}},
			args: args{configMap: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.SLOCtrlConfigMap,
					Namespace: config.ConfigNameSpace,
				},
				Data: map[string]string{
					config.ResourceThresholdConfigKey: "invalid_content",
					config.ResourceQoSConfigKey:       "invalid_content",
					config.CPUBurstConfigKey:          "invalid_content",
				},
			}},
			wantChanged: false,
			wantField: &SLOCfgCache{
				sloCfg:      DefaultSLOCfg(),
				isAvailable: true,
			},
		},
		{
			name: "config with no change",
			fields: fields{oldCfg: &SLOCfgCache{
				sloCfg:      *expectTestingCfg1,
				isAvailable: true,
			}},
			args:        args{configMap: testingConfigMap1},
			wantChanged: false,
			wantField: &SLOCfgCache{
				sloCfg:      *expectTestingCfg1,
				isAvailable: true,
			},
		},
		{
			name: "config changed",
			fields: fields{oldCfg: &SLOCfgCache{
				sloCfg:      oldSLOCfg,
				isAvailable: true,
			}},
			args:        args{configMap: testingConfigMap1},
			wantChanged: true,
			wantField: &SLOCfgCache{
				sloCfg:      *expectTestingCfg1,
				isAvailable: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
			p := NewSLOCfgHandlerForConfigMapEvent(fakeClient, tt.fields.oldCfg.sloCfg)
			p.SLOCfgCache = SLOCfgCache{isAvailable: tt.fields.oldCfg.isAvailable, sloCfg: tt.fields.oldCfg.sloCfg}
			p.SLOCfgCache.isAvailable = tt.fields.oldCfg.isAvailable
			got := p.syncNodeSLOSpecIfChanged(tt.args.configMap)
			assert.Equal(t, tt.wantChanged, got)
			assert.Equal(t, tt.wantField.isAvailable, p.SLOCfgCache.isAvailable)
			assert.Equal(t, tt.wantField.sloCfg, p.SLOCfgCache.sloCfg)
		})
	}
}
