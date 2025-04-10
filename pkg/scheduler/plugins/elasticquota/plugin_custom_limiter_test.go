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

	"github.com/stretchr/testify/assert"
	"golang.org/x/exp/maps"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schetesting "k8s.io/kubernetes/pkg/scheduler/testing"

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

func TestPlugin_OnPodUpdate_CustomLimiter(t *testing.T) {
	core.RegisterCustomLimiterFactory(core.MockLimiterFactoryKey, core.NewMockLimiter)
	testCustomKey := core.CustomKeyMock
	annoKeyLimitConf := fmt.Sprintf(core.AnnotationKeyMockLimitFmt, testCustomKey)
	annoKeyArgsConf := fmt.Sprintf(core.AnnotationKeyMockArgsFmt, testCustomKey)

	// test suit
	suit := newPluginTestSuit(t, nil,
		func(elasticQuotaArgs *config.ElasticQuotaArgs) {
			elasticQuotaArgs.EnableRuntimeQuota = false
			elasticQuotaArgs.CustomLimiters = map[string]config.CustomLimiterConf{
				testCustomKey: {
					FactoryKey:  core.MockLimiterFactoryKey,
					FactoryArgs: `{"labelSelector":"app=test"}`,
				},
			}
		})
	p, err := suit.proxyNew(suit.elasticQuotaArgs, suit.Handle)
	assert.Nil(t, err)
	plugin := p.(*Plugin)
	gqm := plugin.groupQuotaManager

	// init quotas
	q1 := CreateQuota2("1", extension.RootQuotaName, 100, 100, 80, 80, 80, 80, true, "")
	q2 := CreateQuota2("2", extension.RootQuotaName, 100, 100, 80, 80, 80, 80, true, "")
	q11 := CreateQuota2("11", "1", 50, 50, 30, 30, 30, 30, true, "")
	q12 := CreateQuota2("12", "1", 50, 50, 20, 20, 20, 20, true, "")
	q21 := CreateQuota2("21", "2", 50, 50, 20, 20, 20, 20, true, "")
	q111 := CreateQuota2("111", "11", 50, 50, 20, 20, 20, 20, false, "")
	q121 := CreateQuota2("121", "12", 50, 50, 20, 20, 20, 20, false, "")
	q211 := CreateQuota2("211", "21", 50, 50, 15, 15, 15, 15, false, "")
	quotaInfoMap := make(map[string]*core.QuotaInfo)
	for _, quota := range []*v1alpha1.ElasticQuota{q1, q2, q11, q12, q21, q111, q121, q211} {
		plugin.OnQuotaAdd(quota)
		quotaInfoMap[quota.Name] = gqm.GetQuotaInfoByName(quota.Name)
	}

	// check no custom-used for all quotas
	core.AssertCustomUsedEqual(t, nil, testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, maps.Keys(quotaInfoMap)...)...)

	// add unmatched pod1
	pod1 := schetesting.MakePod().Name("1").Label(extension.LabelQuotaName, q111.Name).Containers(
		[]corev1.Container{schetesting.MakeContainer().Name("0").Resources(map[corev1.ResourceName]string{
			corev1.ResourceCPU: "2", corev1.ResourceMemory: "8", "test": "1"}).Obj()}).Node("node0").Obj()
	plugin.OnPodAdd(pod1)

	// check no custom-used for all quotas
	core.AssertCustomUsedEqual(t, nil, testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, maps.Keys(quotaInfoMap)...)...)

	// add custom limit for quota 11
	q11New := q11.DeepCopy()
	q11New.Annotations[annoKeyLimitConf] = `{"cpu":10,"memory":10}`
	plugin.OnQuotaUpdate(q11, q11New)

	// check empty custom-used for quota chain: 111 -> 11 -> 1
	// quota 1 							 	 	mock-used[0,0]
	//   |-- quota 11 		mock-limit[10,10]  	mock-used[0,0]
	//       |-- quota 111						mock-used[0,0]
	//   |-- quota 12
	//       |-- quota 121
	// quota 2
	//   |-- quota 21
	//       |-- quota 211
	core.AssertCustomUsedEqual(t, corev1.ResourceList{}, testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "1", "11", "111")...)
	core.AssertCustomUsedEqual(t, nil, testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "12", "121", "2", "21", "211")...)

	// add matched pod2
	pod2 := schetesting.MakePod().Name("2").Label(extension.LabelQuotaName, q111.Name).Label("app", "test").Containers(
		[]corev1.Container{schetesting.MakeContainer().Name("0").Resources(map[corev1.ResourceName]string{
			corev1.ResourceCPU: "2", corev1.ResourceMemory: "8", "test": "1"}).Obj()}).Node("node0").Obj()
	plugin.OnPodAdd(pod2)

	// check custom-used=[2,8] for quota chain: 111 -> 11 -> 1
	// quota 1 							 	 	mock-used[2,8]
	//   |-- quota 11 		mock-limit[10,10]  	mock-used[2,8]
	//       |-- quota 111						mock-used[2,8]
	//   |-- quota 12
	//       |-- quota 121
	// quota 2
	//   |-- quota 21
	//       |-- quota 211
	core.AssertCustomUsedEqual(t, createResourceList(2, 8), testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "1", "11", "111")...)
	core.AssertCustomUsedEqual(t, nil, testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "12", "121", "2", "21", "211")...)

	// add matched pod3 to quota 121, without limit for this quota chain (121 -> 12 -> 1)
	pod3 := schetesting.MakePod().Name("3").Label(extension.LabelQuotaName, q121.Name).Label("app", "test").Containers(
		[]corev1.Container{schetesting.MakeContainer().Name("0").Resources(map[corev1.ResourceName]string{
			corev1.ResourceCPU: "1", corev1.ResourceMemory: "4"}).Obj()}).Node("node0").Obj()
	plugin.OnPodAdd(pod3)

	// nothing changed
	core.AssertCustomUsedEqual(t, createResourceList(2, 8), testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "1", "11", "111")...)
	core.AssertCustomUsedEqual(t, nil, testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "12", "121", "2", "21", "211")...)

	// add custom limit for quota 1
	newQ1 := q1.DeepCopy()
	newQ1.Annotations[annoKeyLimitConf] = `{"cpu":10,"memory":10}`
	plugin.OnQuotaUpdate(q1, newQ1)

	// verify that quota 1 won't be rebuilt since its descendant quota 11 has custom-limit
	// quota 1 				mock-limit[10,10]  	mock-used[3,12]
	//   |-- quota 11 		mock-limit[10,10]  	mock-used[2,8]
	//       |-- quota 111						mock-used[2,8]
	//   |-- quota 12							mock-used[1,4]
	//       |-- quota 121						mock-used[1,4]
	// quota 2
	//   |-- quota 21
	//       |-- quota 211
	core.AssertCustomUsedEqual(t, nil, testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "2", "21", "211")...)
	core.AssertCustomUsedEqual(t, createResourceList(1, 4), testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "12", "121")...)
	core.AssertCustomUsedEqual(t, createResourceList(2, 8), testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "11", "111")...)
	core.AssertCustomUsedEqual(t, createResourceList(3, 12), testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "1")...)

	//core.AssertNilResList(t, core.GetBatchCustomUsed(testCustomKey, q2Info, q21Info, q211Info)...)
	//core.AssertResListEquals(t, createResourceList(1, 4),
	//	core.GetBatchCustomUsed(testCustomKey, q121Info, q12Info)...)
	//core.AssertResListEquals(t, createResourceList(2, 8),
	//	core.GetBatchCustomUsed(testCustomKey, q111Info, q11Info)...)
	//core.AssertResListEquals(t, createResourceList(3, 12),
	//	core.GetBatchCustomUsed(testCustomKey, q1Info)...)

	// trigger rebuilding for quota 1
	q1, newQ1 = newQ1, newQ1.DeepCopy()
	newQ1.Annotations[annoKeyArgsConf] = `{"rebuildTriggerID":"1"}`
	plugin.OnQuotaUpdate(q1, newQ1)

	// custom-used should be rebuilt for quota chain: 121 -> 12 -> 1
	core.AssertCustomUsedEqual(t, createResourceList(1, 4), testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "12", "121")...)
	core.AssertCustomUsedEqual(t, createResourceList(2, 8), testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "11", "111")...)
	core.AssertCustomUsedEqual(t, createResourceList(3, 12), testCustomKey,
		core.GetQuotaInfos(quotaInfoMap, "1")...)

	assert.Equal(t, 2, len(gqm.GetQuotaInfoByName(q111.Name).PodCache))
	assert.Equal(t, 1, len(gqm.GetQuotaInfoByName(q121.Name).PodCache))
}
