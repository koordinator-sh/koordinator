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
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

func TestPlugin_PreFilter_CustomLimiter(t *testing.T) {
	core.RegisterCustomLimiterFactory(core.MockLimiterFactoryKey, core.NewMockLimiter)
	customKey := core.CustomKeyMock
	annotationKeyLimit := fmt.Sprintf(core.AnnotationKeyMockLimitFmt, customKey)
	annotationKeyArgs := fmt.Sprintf(core.AnnotationKeyMockArgsFmt, customKey)

	// test suit
	suit := newPluginTestSuit(t, nil,
		func(elasticQuotaArgs *config.ElasticQuotaArgs) {
			elasticQuotaArgs.EnableRuntimeQuota = false
			elasticQuotaArgs.CustomLimiters = map[string]config.CustomLimiterConf{
				customKey: {
					FactoryKey:  core.MockLimiterFactoryKey,
					FactoryArgs: `{"labelSelector":"app=test"}`,
				},
			}
		})

	quotaName, parentQuotaName := "test-child", "test"
	test := []struct {
		name            string
		pod             *corev1.Pod
		quotaInfo       *v1alpha1.ElasticQuota
		parentQuotaInfo *v1alpha1.ElasticQuota
		expectedStatus  framework.Status
	}{
		{
			name: "accept: without custom limit conf",
			pod: MakePod("t1-ns1", "pod1").Label(extension.LabelQuotaName, "test-child").Container(
				MakeResourceList().CPU(2).Mem(2).Obj()).Obj(),
			quotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: quotaName,
					Labels: map[string]string{
						extension.LabelQuotaParent: parentQuotaName,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(10).Mem(10).Obj(),
					Min: MakeResourceList().CPU(1).Mem(1).Obj(),
				},
			},
			parentQuotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: parentQuotaName,
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(10).Mem(10).Obj(),
					Min: MakeResourceList().CPU(5).Mem(5).Obj(),
				},
			},
			expectedStatus: *framework.NewStatus(framework.Success, ""),
		},
		{
			name: "accept: unmatched",
			pod: MakePod("t1-ns1", "pod1").Label(extension.LabelQuotaName, "test-child").Container(
				MakeResourceList().CPU(5).Mem(5).Obj()).Obj(),
			quotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: quotaName,
					Labels: map[string]string{
						extension.LabelQuotaParent: parentQuotaName,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(10).Mem(10).Obj(),
					Min: MakeResourceList().CPU(1).Mem(1).Obj(),
				},
			},
			parentQuotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: quotaName,
					Annotations: map[string]string{
						annotationKeyLimit: `{"cpu":2,"memory":2}`,
						annotationKeyArgs:  `{"debugEnabled":true}`,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(10).Mem(10).Obj(),
					Min: MakeResourceList().CPU(5).Mem(5).Obj(),
				},
			},
			expectedStatus: *framework.NewStatus(framework.Success, ""),
		},
		{
			name: "accept: used <= limit",
			pod: MakePod("t1-ns1", "pod1").Label(extension.LabelQuotaName, "test-child").Label(
				"app", "test").Container(MakeResourceList().CPU(2).Mem(2).Obj()).Obj(),
			quotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: quotaName,
					Labels: map[string]string{
						extension.LabelQuotaParent: parentQuotaName,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(10).Mem(10).Obj(),
					Min: MakeResourceList().CPU(1).Mem(1).Obj(),
				},
			},
			parentQuotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: parentQuotaName,
					Annotations: map[string]string{
						annotationKeyLimit: `{"cpu":2,"memory":2}`,
						annotationKeyArgs:  `{"debugEnabled":true}`,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(10).Mem(10).Obj(),
					Min: MakeResourceList().CPU(5).Mem(5).Obj(),
				},
			},
			expectedStatus: *framework.NewStatus(framework.Success, ""),
		},
		{
			name: "reject when cpu reach the limit",
			pod: MakePod("t1-ns1", "pod1").Label(extension.LabelQuotaName, "test-child").Label(
				"app", "test").Container(
				MakeResourceList().CPU(3).Mem(3).Obj()).Obj(),
			quotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: quotaName,
					Labels: map[string]string{
						extension.LabelQuotaParent: parentQuotaName,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(10).Mem(10).Obj(),
					Min: MakeResourceList().CPU(1).Mem(1).Obj(),
				},
			},
			parentQuotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: parentQuotaName,
					Annotations: map[string]string{
						annotationKeyLimit: `{"cpu":1,"memory":10}`,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(30).Mem(30).Obj(),
					Min: MakeResourceList().CPU(5).Mem(5).Obj(),
				},
			},
			expectedStatus: *framework.NewStatus(framework.Unschedulable,
				fmt.Sprintf("check failed for quota %s by custom-limiter mock: insufficient resource, "+
					"limit=%v, used=%v, request=%v, exceededResourceNames=[cpu]",
					parentQuotaName, printResourceList(MakeResourceList().CPU(1).Mem(10).Obj()),
					printResourceList(corev1.ResourceList{}),
					printResourceList(MakeResourceList().CPU(3).Mem(3).Obj()))),
		}, {
			name: "reject when memory reach the limit",
			pod: MakePod("t1-ns1", "pod1").Label(extension.LabelQuotaName, "test-child").Label(
				"app", "test").Container(
				MakeResourceList().CPU(3).Mem(3).Obj()).Obj(),
			quotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: quotaName,
					Labels: map[string]string{
						extension.LabelQuotaParent: parentQuotaName,
					},
					Annotations: map[string]string{
						annotationKeyLimit: `{"cpu":10,"memory":1}`,
					},
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(10).Mem(10).Obj(),
					Min: MakeResourceList().CPU(1).Mem(1).Obj(),
				},
			},
			parentQuotaInfo: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: parentQuotaName,
				},
				Spec: v1alpha1.ElasticQuotaSpec{
					Max: MakeResourceList().CPU(30).Mem(30).Obj(),
					Min: MakeResourceList().CPU(5).Mem(5).Obj(),
				},
			},
			expectedStatus: *framework.NewStatus(framework.Unschedulable,
				fmt.Sprintf("check failed for quota %s by custom-limiter mock: insufficient resource, "+
					"limit=%v, used=%v, request=%v, exceededResourceNames=[memory]",
					quotaName, printResourceList(MakeResourceList().CPU(10).Mem(1).Obj()),
					printResourceList(corev1.ResourceList{}),
					printResourceList(MakeResourceList().CPU(3).Mem(3).Obj()))),
		},
	}
	for _, tt := range test {
		t.Run(tt.name, func(t *testing.T) {
			p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
			assert.Nil(t, err)
			gp := p.(*Plugin)
			gp.OnQuotaAdd(tt.parentQuotaInfo)
			gp.OnQuotaAdd(tt.quotaInfo)
			// verify
			state := framework.NewCycleState()
			_, status := gp.PreFilter(context.TODO(), state, tt.pod)
			assert.Equal(t, tt.expectedStatus, *status)
		})
	}
}
