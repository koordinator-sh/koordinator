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

package transformer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

func TestTransformElasticQuota(t *testing.T) {
	tests := []struct {
		name   string
		eq     *v1alpha1.ElasticQuota
		wantEQ *v1alpha1.ElasticQuota
	}{
		{
			name: "normal elastic quota",
			eq: &v1alpha1.ElasticQuota{
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: map[corev1.ResourceName]resource.Quantity{
						apiext.ResourceGPUCore: resource.MustParse("100"),
					},
					Min: map[corev1.ResourceName]resource.Quantity{
						apiext.ResourceGPUCore: resource.MustParse("100"),
					},
				},
			},
			wantEQ: &v1alpha1.ElasticQuota{
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: map[corev1.ResourceName]resource.Quantity{
						apiext.ResourceGPUCore: resource.MustParse("100"),
					},
					Min: map[corev1.ResourceName]resource.Quantity{
						apiext.ResourceGPUCore: resource.MustParse("100"),
					},
				},
			},
		},
		{
			name: "elastic quota with deprecated batch resources and current version resources",
			eq: &v1alpha1.ElasticQuota{
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:      resource.MustParse("32"),
						corev1.ResourceMemory:   resource.MustParse("64Gi"),
						apiext.KoordBatchCPU:    resource.MustParse("1000"),
						apiext.KoordBatchMemory: resource.MustParse("10Gi"),
						apiext.BatchCPU:         resource.MustParse("1000"),
						apiext.BatchMemory:      resource.MustParse("10Gi"),
					},
					Min: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:      resource.MustParse("32"),
						corev1.ResourceMemory:   resource.MustParse("64Gi"),
						apiext.KoordBatchCPU:    resource.MustParse("1000"),
						apiext.KoordBatchMemory: resource.MustParse("10Gi"),
						apiext.BatchCPU:         resource.MustParse("1000"),
						apiext.BatchMemory:      resource.MustParse("10Gi"),
					},
				},
			},
			wantEQ: &v1alpha1.ElasticQuota{
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:      resource.MustParse("32"),
						corev1.ResourceMemory:   resource.MustParse("64Gi"),
						apiext.KoordBatchCPU:    resource.MustParse("1000"),
						apiext.KoordBatchMemory: resource.MustParse("10Gi"),
						apiext.BatchCPU:         resource.MustParse("1000"),
						apiext.BatchMemory:      resource.MustParse("10Gi"),
					},
					Min: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceCPU:      resource.MustParse("32"),
						corev1.ResourceMemory:   resource.MustParse("64Gi"),
						apiext.KoordBatchCPU:    resource.MustParse("1000"),
						apiext.KoordBatchMemory: resource.MustParse("10Gi"),
						apiext.BatchCPU:         resource.MustParse("1000"),
						apiext.BatchMemory:      resource.MustParse("10Gi"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := TransformElasticQuota(tt.eq)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantEQ, obj)
		})
	}
}
