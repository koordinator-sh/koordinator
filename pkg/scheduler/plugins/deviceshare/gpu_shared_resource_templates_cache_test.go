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

package deviceshare

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

var (
	templatesInfos = map[string]apiext.GPUSharedResourceTemplates{
		"huawei-Ascend-310P": {
			"vir01": corev1.ResourceList{
				apiext.ResourceGPUMemory:     resource.MustParse("3Gi"),
				apiext.ResourceHuaweiNPUCore: resource.MustParse("1"),
				apiext.ResourceHuaweiNPUCPU:  resource.MustParse("1"),
				apiext.ResourceHuaweiNPUDVPP: resource.MustParse("12"),
			},
			"vir02": corev1.ResourceList{
				apiext.ResourceGPUMemory:     resource.MustParse("6Gi"),
				apiext.ResourceHuaweiNPUCore: resource.MustParse("2"),
				apiext.ResourceHuaweiNPUCPU:  resource.MustParse("2"),
				apiext.ResourceHuaweiNPUDVPP: resource.MustParse("25"),
			},
			"vir02_1c": corev1.ResourceList{
				apiext.ResourceGPUMemory:     resource.MustParse("6Gi"),
				apiext.ResourceHuaweiNPUCore: resource.MustParse("2"),
				apiext.ResourceHuaweiNPUCPU:  resource.MustParse("1"),
				apiext.ResourceHuaweiNPUDVPP: resource.MustParse("25"),
			},
			"vir04": corev1.ResourceList{
				apiext.ResourceGPUMemory:     resource.MustParse("12Gi"),
				apiext.ResourceHuaweiNPUCore: resource.MustParse("4"),
				apiext.ResourceHuaweiNPUCPU:  resource.MustParse("4"),
				apiext.ResourceHuaweiNPUDVPP: resource.MustParse("50"),
			},
			"vir04_3c": corev1.ResourceList{
				apiext.ResourceGPUMemory:     resource.MustParse("12Gi"),
				apiext.ResourceHuaweiNPUCore: resource.MustParse("4"),
				apiext.ResourceHuaweiNPUCPU:  resource.MustParse("3"),
				apiext.ResourceHuaweiNPUDVPP: resource.MustParse("50"),
			},
			"vir04_3c_ndvpp": corev1.ResourceList{
				apiext.ResourceGPUMemory:     resource.MustParse("12Gi"),
				apiext.ResourceHuaweiNPUCore: resource.MustParse("4"),
				apiext.ResourceHuaweiNPUCPU:  resource.MustParse("3"),
			},
			"vir04_4c_dvpp": corev1.ResourceList{
				apiext.ResourceGPUMemory:     resource.MustParse("12Gi"),
				apiext.ResourceHuaweiNPUCore: resource.MustParse("4"),
				apiext.ResourceHuaweiNPUCPU:  resource.MustParse("4"),
				apiext.ResourceHuaweiNPUDVPP: resource.MustParse("100"),
			},
		},
	}
)

func Test_newGPUSharedResourceTemplatesCache(t *testing.T) {
	cache := newGPUSharedResourceTemplatesCache()
	assert.NotNil(t, cache)
	assert.Nil(t, cache.gpuSharedResourceTemplatesInfos)
}

func Test_getTemplates(t *testing.T) {
	type args struct {
		vendor string
		model  string
	}
	tests := []struct {
		name    string
		args    args
		infos   map[string]apiext.GPUSharedResourceTemplates
		wantErr bool
		want    apiext.GPUSharedResourceTemplates
	}{
		{
			name: "template found",
			args: args{
				vendor: "huawei",
				model:  "Ascend-310P",
			},
			infos:   templatesInfos,
			wantErr: false,
			want:    templatesInfos["huawei-Ascend-310P"],
		},
		{
			name: "template not found",
			args: args{
				vendor: "huawei",
				model:  "Ascend-910",
			},
			infos:   templatesInfos,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newGPUSharedResourceTemplatesCache()
			cache.setTemplatesInfos(tt.infos)

			result, err := cache.getTemplates(tt.args.vendor, tt.args.model)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

func Test_setTemplatesInfos(t *testing.T) {
	cache := newGPUSharedResourceTemplatesCache()

	cache.setTemplatesInfos(templatesInfos)
	assert.Equal(t, templatesInfos, cache.gpuSharedResourceTemplatesInfos)
}

func Test_setTemplatesInfosFromConfigMap(t *testing.T) {
	data, _ := yaml.Marshal(templatesInfos)
	tests := []struct {
		name      string
		cm        *corev1.ConfigMap
		wantErr   bool
		wantInfos map[string]apiext.GPUSharedResourceTemplates
	}{
		{
			name: "normal case",
			cm: &corev1.ConfigMap{
				Data: map[string]string{
					"data.yaml": string(data),
				},
			},
			wantErr:   false,
			wantInfos: templatesInfos,
		},
		{
			name: "invalid configmap data",
			cm: &corev1.ConfigMap{
				Data: map[string]string{
					"data.yaml": "invalid yaml",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newGPUSharedResourceTemplatesCache()
			err := cache.setTemplatesInfosFromConfigMap(tt.cm)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantInfos, cache.gpuSharedResourceTemplatesInfos)
			}
		})
	}
}
