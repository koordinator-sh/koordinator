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
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulerv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

func TestGetQuotaName(t *testing.T) {
	tests := []struct {
		name            string
		pod             *corev1.Pod
		elasticQuotas   []*schedulerv1alpha1.ElasticQuota
		expectQuotaName string
	}{
		{
			name: "default quota",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-pod",
				},
			},
			expectQuotaName: extension.DefaultQuotaName,
		},
		{
			name: "quota name from label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-pod",
					Labels: map[string]string{
						extension.LabelQuotaName: "test",
					},
				},
			},
			expectQuotaName: "test",
		},
		{
			name: "quota name from namespace",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-pod",
				},
			},
			elasticQuotas: []*schedulerv1alpha1.ElasticQuota{
				MakeEQ("test-ns", "parent-quota").Annotations(map[string]string{extension.LabelQuotaIsParent: "true"}).Obj(),
				MakeEQ("test-ns", "test-ns").Obj(),
			},
			expectQuotaName: "test-ns",
		},
		{
			name: "quota name from namespace",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-pod",
				},
			},
			elasticQuotas: []*schedulerv1alpha1.ElasticQuota{
				MakeEQ("test-ns", "parent-quota").Annotations(map[string]string{extension.LabelQuotaIsParent: "true"}).Obj(),
				MakeEQ("test-ns", "test-ns1").Annotations(map[string]string{extension.AnnotationQuotaNamespaces: "[\"test-ns\"]"}).Obj(),
			},
			expectQuotaName: "test-ns1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)
			eQP := p.(*Plugin)
			for _, eq := range tt.elasticQuotas {
				_, err := eQP.client.SchedulingV1alpha1().ElasticQuotas(eq.Namespace).Create(context.TODO(), eq, metav1.CreateOptions{})
				assert.NoError(t, err)
			}
			time.Sleep(100 * time.Millisecond)
			quotaName := eQP.GetQuotaName(tt.pod)
			assert.Equal(t, tt.expectQuotaName, quotaName)
		})
	}
}

func TestPlugin_getQuotaInfoRuntime(t *testing.T) {
	type args struct {
		quotaInfo          *core.QuotaInfo
		enableRuntimeQuota bool
	}
	tests := []struct {
		name string
		args args
		want corev1.ResourceList
	}{
		{
			name: "get runtime",
			args: args{
				enableRuntimeQuota: true,
				quotaInfo: &core.QuotaInfo{
					CalculateInfo: core.QuotaCalculateInfo{
						Max:     createResourceList(100, 100),
						Runtime: createResourceList(1, 1),
					},
				},
			},
			want: createResourceList(1, 1),
		},
		{
			name: "get max",
			args: args{
				enableRuntimeQuota: false,
				quotaInfo: &core.QuotaInfo{
					CalculateInfo: core.QuotaCalculateInfo{
						Max:     createResourceList(100, 100),
						Runtime: createResourceList(1, 1),
					},
				},
			},
			want: createResourceList(100, 100),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Plugin{
				pluginArgs: &config.ElasticQuotaArgs{
					EnableRuntimeQuota: tt.args.enableRuntimeQuota,
				},
			}
			assert.Equalf(t, tt.want, g.getQuotaInfoUsedLimit(tt.args.quotaInfo), "getQuotaInfoUsedLimit(%v)", tt.args.quotaInfo)
		})
	}
}
