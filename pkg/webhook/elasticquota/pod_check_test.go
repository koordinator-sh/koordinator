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

package elasticquota

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

func TestGetQuotaName(t *testing.T) {
	tests := []struct {
		name   string
		pod    *v1.Pod
		quotas []*v1alpha1.ElasticQuota
		want   string
	}{
		{
			name: "pod has quota annotation",
			pod: func() *v1.Pod {
				p := MakePod("team-a", "pod1").Obj()
				p.Annotations = map[string]string{extension.LabelQuotaName: "annotated-quota"}
				return p
			}(),
			want: "annotated-quota",
		},
		{
			name: "pod has quota label",
			pod:  MakePod("team-a", "pod1").Label(extension.LabelQuotaName, "my-quota").Obj(),
			want: "my-quota",
		},
		{
			name: "no label, quota in pod namespace with same name as namespace",
			pod:  MakePod("team-a", "pod1").Obj(),
			quotas: []*v1alpha1.ElasticQuota{
				MakeQuota("team-a").Namespace("team-a").Obj(),
			},
			want: "team-a",
		},
		{
			name: "no label, quota in pod namespace with different name from namespace",
			pod:  MakePod("team-a", "pod1").Obj(),
			quotas: []*v1alpha1.ElasticQuota{
				MakeQuota("team-a-quota").Namespace("team-a").Obj(),
			},
			want: "team-a-quota",
		},
		{
			name: "no label, cross-namespace quota covers pod namespace",
			pod:  MakePod("team-a", "pod1").Obj(),
			quotas: []*v1alpha1.ElasticQuota{
				MakeQuota("shared-quota").Namespace("kube-system").
					Annotations(map[string]string{
						extension.AnnotationQuotaNamespaces: `["team-a","team-b"]`,
					}).Obj(),
			},
			want: "shared-quota",
		},
		{
			name: "no label, no quota anywhere",
			pod:  MakePod("team-a", "pod1").Obj(),
			want: extension.DefaultQuotaName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := fake.NewClientBuilder().Build()
			sche := kubeClient.Scheme()
			v1alpha1.AddToScheme(sche)
			for _, q := range tt.quotas {
				assert.NoError(t, kubeClient.Create(context.TODO(), q))
			}
			got := GetQuotaName(tt.pod, kubeClient)
			assert.Equal(t, tt.want, got)
		})
	}
}
