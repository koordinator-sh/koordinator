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

package core

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/exp/maps"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	schetesting "k8s.io/kubernetes/pkg/scheduler/testing"
	"k8s.io/utils/set"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

const (
	GigaByte                = 1024 * 1048576
	ExtendedResourceKeyXCPU = "x-cpu"
)

func TestGroupQuotaManager_QuotaAdd(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.UpdateClusterTotalResource(createResourceList(10000000, 3000*GigaByte))
	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96000, 160*GigaByte, 50000, 50*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "test2", extension.RootQuotaName, 96000, 160*GigaByte, 80000, 80*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "test3", extension.RootQuotaName, 96000, 160*GigaByte, 40000, 40*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "test4", extension.RootQuotaName, 96000, 160*GigaByte, 0, 0*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "test5", extension.RootQuotaName, 96000, 160*GigaByte, 96000, 96*GigaByte, true, false)

	quotaInfo := gqm.GetQuotaInfoByName("test1")
	if quotaInfo.CalculateInfo.SharedWeight.Name(v1.ResourceCPU, resource.DecimalSI).Value() != 96000 {
		t.Errorf("error")
	}

	assert.Equal(t, 6, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 8, len(gqm.quotaInfoMap))
	assert.Equal(t, 2, len(gqm.resourceKeys))
}

func TestGroupQuotaManager_QuotaAdd_AutoMakeUpSharedWeight(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.UpdateClusterTotalResource(createResourceList(1000000, 200*GigaByte))

	quota := CreateQuota("test", extension.RootQuotaName, 96000, 160*GigaByte, 50000, 80*GigaByte, true, false)
	delete(quota.Annotations, extension.AnnotationSharedWeight)

	err := gqm.UpdateQuota(quota, false)
	assert.Nil(t, err)

	quotaInfo := gqm.quotaInfoMap["test"]
	if quotaInfo.CalculateInfo.SharedWeight.Cpu().Value() != 96000 || quotaInfo.CalculateInfo.SharedWeight.Memory().Value() != 160*GigaByte {
		t.Errorf("error:%v", quotaInfo.CalculateInfo.SharedWeight)
	}
}

func TestGroupQuotaManager_QuotaAdd_AutoMakeUpScaleRatio2(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	quota := &v1alpha1.ElasticQuota{
		TypeMeta: metav1.TypeMeta{
			Kind: "ElasticQuota",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Namespace:   "",
			Annotations: make(map[string]string),
			Labels:      make(map[string]string),
		},
		Spec: v1alpha1.ElasticQuotaSpec{
			Max: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewQuantity(96, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(1000, resource.DecimalSI),
				v1.ResourcePods:   *resource.NewQuantity(1000, resource.DecimalSI),
			},
		},
	}

	quota.Annotations[extension.AnnotationSharedWeight] = fmt.Sprintf("{\"cpu\":%v, \"memory\":\"%v\"}", 0, 0)
	quota.Labels[extension.LabelQuotaParent] = extension.RootQuotaName
	quota.Labels[extension.LabelQuotaIsParent] = "false"
	gqm.UpdateQuota(quota, false)

	quotaInfo := gqm.GetQuotaInfoByName("test")
	if quotaInfo.CalculateInfo.SharedWeight.Cpu().Value() != 96 ||
		quotaInfo.CalculateInfo.SharedWeight.Memory().Value() != 1000 ||
		quotaInfo.CalculateInfo.SharedWeight.Pods().Value() != 1000 {
		t.Errorf("error:%v", quotaInfo.CalculateInfo.SharedWeight)
	}
}

func TestGroupQuotaManager_UpdateQuotaInternal(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	quota := CreateQuota("test1", extension.RootQuotaName, 64, 100*GigaByte, 50, 80*GigaByte, true, false)
	gqm.UpdateQuota(quota, false)
	quotaInfo := gqm.quotaInfoMap["test1"]
	assert.True(t, quotaInfo != nil)
	assert.False(t, quotaInfo.IsParent)
	assert.Equal(t, createResourceList(64, 100*GigaByte), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, createResourceList(50, 80*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)
	assert.Equal(t, int64(64), quotaInfo.CalculateInfo.SharedWeight.Cpu().Value())
	assert.Equal(t, int64(100*GigaByte), quotaInfo.CalculateInfo.SharedWeight.Memory().Value())

	AddQuotaToManager(t, gqm, "test2", extension.RootQuotaName, 96, 160*GigaByte, 80, 80*GigaByte, true, false)

	quota = CreateQuota("test1", extension.RootQuotaName, 84, 120*GigaByte, 60, 100*GigaByte, true, false)
	quota.Labels[extension.LabelQuotaIsParent] = "true"
	err := gqm.UpdateQuota(quota, false)
	assert.Nil(t, err)
	quotaInfo = gqm.quotaInfoMap["test1"]
	assert.True(t, quotaInfo != nil)
	assert.True(t, quotaInfo.IsParent)
	assert.Equal(t, createResourceList(84, 120*GigaByte), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, createResourceList(60, 100*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)
	assert.Equal(t, int64(84), quotaInfo.CalculateInfo.SharedWeight.Cpu().Value())
	assert.Equal(t, int64(120*GigaByte), quotaInfo.CalculateInfo.SharedWeight.Memory().Value())
}

func TestGroupQuotaManager_UpdateQuota(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	quota := CreateQuota("test1", extension.RootQuotaName, 64, 100*GigaByte, 50, 80*GigaByte, true, false)
	gqm.UpdateQuota(quota, false)
	assert.Equal(t, len(gqm.quotaInfoMap), 4)
}

func TestGroupQuotaManager_UpdateQuotaInternalAndRequest(t *testing.T) {
	// add resource to node
	gqm := NewGroupQuotaManagerForTest()
	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	// request[120, 290] > maxQuota, runtime == maxQuota
	request := createResourceList(120, 290*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("test1", request, request, 0)
	runtime := gqm.RefreshRuntime("test1")
	expectCurrentRuntime := deltaRes
	assert.Equal(t, expectCurrentRuntime, runtime)

	// update resourceKey
	quota1 := CreateQuota("test1", extension.RootQuotaName, 64, 100*GigaByte, 60, 90*GigaByte, true, false)
	quota1.Labels[extension.LabelQuotaIsParent] = "false"
	err := gqm.UpdateQuota(quota1, false)
	assert.Nil(t, err)
	quotaInfo := gqm.GetQuotaInfoByName("test1")
	assert.Equal(t, createResourceList(64, 100*GigaByte), quotaInfo.CalculateInfo.Max)
	runtime = gqm.RefreshRuntime("test1")
	expectCurrentRuntime = createResourceList(64, 100*GigaByte)
	assert.Equal(t, expectCurrentRuntime, runtime)

	// added max ExtendedResourceKeyXCPU without node resource added
	// runtime.ExtendedResourceKeyXCPU = 0
	request[ExtendedResourceKeyXCPU] = *resource.NewQuantity(80, resource.DecimalSI)
	gqm.updateGroupDeltaRequestNoLock("test1", request, request, 0)
	xCPUQuantity := resource.NewQuantity(100, resource.DecimalSI)
	quota1.Spec.Max[ExtendedResourceKeyXCPU] = *xCPUQuantity
	maxJson, err := json.Marshal(quota1.Spec.Max)
	assert.Nil(t, err)
	quota1.Annotations[extension.AnnotationSharedWeight] = string(maxJson)
	gqm.UpdateQuota(quota1, false)
	quotaInfo = gqm.quotaInfoMap["test1"]
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, *xCPUQuantity, quotaInfo.CalculateInfo.Max[ExtendedResourceKeyXCPU])
	runtime = gqm.RefreshRuntime("test1")
	expectCurrentRuntime[ExtendedResourceKeyXCPU] = resource.Quantity{Format: resource.DecimalSI}
	assert.Equal(t, expectCurrentRuntime, runtime)

	// add ExtendedResourceKeyXCPU to node resource
	deltaRes[ExtendedResourceKeyXCPU] = *xCPUQuantity
	gqm.UpdateClusterTotalResource(deltaRes)
	runtime = gqm.RefreshRuntime("test1")
	expectCurrentRuntime[ExtendedResourceKeyXCPU] = *resource.NewQuantity(80, resource.DecimalSI)
	assert.Equal(t, expectCurrentRuntime, runtime)

	// delete max ExtendedResourceKeyXCPU
	delete(quota1.Spec.Max, ExtendedResourceKeyXCPU)
	maxJson, err = json.Marshal(quota1.Spec.Max)
	assert.Nil(t, err)
	quota1.Annotations[extension.AnnotationSharedWeight] = string(maxJson)
	gqm.UpdateQuota(quota1, false)
	quotaInfo = gqm.quotaInfoMap["test1"]
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, resource.Quantity{}, quotaInfo.CalculateInfo.Max[ExtendedResourceKeyXCPU])
	runtime = gqm.RefreshRuntime("test1")
	delete(expectCurrentRuntime, ExtendedResourceKeyXCPU)
	assert.Equal(t, expectCurrentRuntime, runtime)
}

func TestGroupQuotaManager_UpdateResourceKeyNoLock(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	// add quotas
	quota1Name := "test1"
	quota1 := CreateQuota(quota1Name, extension.RootQuotaName, 64, 100*GigaByte, 60, 90*GigaByte, true, true)
	quota1.Labels[extension.LabelQuotaIsParent] = "true"
	quota1Info := NewQuotaInfoFromQuota(quota1)
	gqm.quotaInfoMap[quota1Name] = quota1Info
	gqm.runtimeQuotaCalculatorMap[quota1Name] = NewRuntimeQuotaCalculator(quota1Name)

	quota2Name := "test2"
	quota2 := CreateQuota(quota2Name, quota1Name, 64, 100*GigaByte, 60, 90*GigaByte, true, false)
	quota2.Labels[extension.LabelQuotaIsParent] = "false"
	quota2Info := NewQuotaInfoFromQuota(quota2)
	gqm.quotaInfoMap[quota2Name] = quota2Info
	gqm.runtimeQuotaCalculatorMap[quota2Name] = NewRuntimeQuotaCalculator(quota2Name)

	resourceKeyCheckFunc := func(gqmRK, rootRK, quota1RK, quota2RK map[v1.ResourceName]struct{}) {
		assert.Equal(t, gqmRK, gqm.resourceKeys)
		assert.Equal(t, rootRK, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].resourceKeys)
		assert.Equal(t, quota1RK, gqm.runtimeQuotaCalculatorMap[quota1Name].resourceKeys)
		assert.Equal(t, quota2RK, gqm.runtimeQuotaCalculatorMap[quota2Name].resourceKeys)
	}
	// before update firstly
	resourceKeyCheckFunc(
		map[v1.ResourceName]struct{}{},
		map[v1.ResourceName]struct{}{},
		map[v1.ResourceName]struct{}{},
		map[v1.ResourceName]struct{}{},
	)
	// update firstly
	gqm.updateResourceKeyNoLock()
	resourceKeyCheckFunc(
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}},
	)
	// mock standard resourceKeys updated
	// 1. quota1 add resourceKey
	xCPUQuantity := resource.NewQuantity(100, resource.DecimalSI)
	quota1.Spec.Max[ExtendedResourceKeyXCPU] = *xCPUQuantity
	quota1Info = NewQuotaInfoFromQuota(quota1)
	gqm.quotaInfoMap[quota1Name] = quota1Info
	gqm.updateResourceKeyNoLock()
	resourceKeyCheckFunc(
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}, ExtendedResourceKeyXCPU: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}, ExtendedResourceKeyXCPU: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}, ExtendedResourceKeyXCPU: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}},
	)
	// 2.quota2 delete resourceKey
	delete(quota2.Spec.Max, v1.ResourceCPU)
	quota2Info = NewQuotaInfoFromQuota(quota2)
	gqm.quotaInfoMap[quota2Name] = quota2Info
	gqm.updateResourceKeyNoLock()
	resourceKeyCheckFunc(
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}, ExtendedResourceKeyXCPU: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}, ExtendedResourceKeyXCPU: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}, ExtendedResourceKeyXCPU: {}},
		map[v1.ResourceName]struct{}{v1.ResourceMemory: {}},
	)
	// 3.quota1 delete resourceKey
	delete(quota1.Spec.Max, ExtendedResourceKeyXCPU)
	quota1Info = NewQuotaInfoFromQuota(quota1)
	gqm.quotaInfoMap[quota1Name] = quota1Info
	gqm.updateResourceKeyNoLock()
	resourceKeyCheckFunc(
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}},
		map[v1.ResourceName]struct{}{v1.ResourceCPU: {}, v1.ResourceMemory: {}},
		map[v1.ResourceName]struct{}{v1.ResourceMemory: {}},
	)
}
func TestGroupQuotaManager_DeleteOneGroup(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.UpdateClusterTotalResource(createResourceList(1000, 1000*GigaByte))
	quota1 := AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)
	quota2 := AddQuotaToManager(t, gqm, "test2", extension.RootQuotaName, 96, 160*GigaByte, 80, 80*GigaByte, true, false)
	quota3 := AddQuotaToManager(t, gqm, "test3", extension.RootQuotaName, 96, 160*GigaByte, 40, 40*GigaByte, true, false)
	assert.Equal(t, 4, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 6, len(gqm.quotaInfoMap))

	err := gqm.UpdateQuota(quota1, true)
	assert.Nil(t, err)
	quotaInfo := gqm.GetQuotaInfoByName("test1")
	assert.True(t, quotaInfo == nil)

	err = gqm.UpdateQuota(quota2, true)
	assert.Nil(t, err)
	quotaInfo = gqm.GetQuotaInfoByName("test2")
	assert.True(t, quotaInfo == nil)

	err = gqm.UpdateQuota(quota3, true)
	assert.Nil(t, err)
	quotaInfo = gqm.GetQuotaInfoByName("test3")
	assert.True(t, quotaInfo == nil)

	assert.Equal(t, 1, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 3, len(gqm.quotaInfoMap))

	AddQuotaToManager(t, gqm, "youku", extension.RootQuotaName, 96, 160*GigaByte, 70, 70*GigaByte, true, false)
	quotaInfo = gqm.GetQuotaInfoByName("youku")
	assert.NotNil(t, quotaInfo)
	assert.Equal(t, 2, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 4, len(gqm.quotaInfoMap))
}

func TestGroupQuotaManager_DeleteQuotaInternal(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.UpdateClusterTotalResource(createResourceList(1000, 1000*GigaByte))
	parent1 := AddQuotaToManager(t, gqm, "parent1", extension.RootQuotaName, 200, 200*GigaByte, 200, 200*GigaByte, true, true)
	// test1 allow lent min
	test1 := AddQuotaToManager(t, gqm, "test1", "parent1", 96, 160*GigaByte, 50, 80*GigaByte, true, false)
	// test2 not allow lent min
	test2 := AddQuotaToManager(t, gqm, "test2", "parent1", 96, 160*GigaByte, 80, 80*GigaByte, false, false)
	assert.Equal(t, 4, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 4, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 6, len(gqm.quotaInfoMap))
	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap["parent1"].childGroupQuotaInfos))

	parent1Info := gqm.GetQuotaInfoByName("parent1")
	assert.Equal(t, createResourceList(80, 80*GigaByte), parent1Info.CalculateInfo.Request)
	assert.Equal(t, createResourceList(80, 80*GigaByte), parent1Info.CalculateInfo.ChildRequest)

	test1Info := gqm.GetQuotaInfoByName("test1")
	assert.Equal(t, v1.ResourceList{}, test1Info.CalculateInfo.Request)
	assert.Equal(t, v1.ResourceList{}, test1Info.CalculateInfo.ChildRequest)

	test2Info := gqm.GetQuotaInfoByName("test2")
	assert.Equal(t, createResourceList(80, 80*GigaByte), test2Info.CalculateInfo.Request)
	assert.Equal(t, v1.ResourceList{}, test2Info.CalculateInfo.ChildRequest)

	// delete test1 quota
	err := gqm.deleteQuotaNoLock(test1)
	assert.Nil(t, err)
	test1Info = gqm.GetQuotaInfoByName("test1")
	assert.True(t, test1Info == nil)

	assert.Equal(t, 3, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 3, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 5, len(gqm.quotaInfoMap))

	// check parent requests
	parent1Info = gqm.GetQuotaInfoByName("parent1")
	assert.Equal(t, createResourceList(80, 80*GigaByte), parent1Info.CalculateInfo.Request)
	assert.Equal(t, createResourceList(80, 80*GigaByte), parent1Info.CalculateInfo.ChildRequest)
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap["parent1"].childGroupQuotaInfos))

	// delete test2 quota
	err = gqm.deleteQuotaNoLock(test2)
	assert.Nil(t, err)
	test2Info = gqm.GetQuotaInfoByName("test2")
	assert.True(t, test2Info == nil)

	assert.Equal(t, 2, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 4, len(gqm.quotaInfoMap))

	// check parent requests
	parent1Info = gqm.GetQuotaInfoByName("parent1")
	assert.Equal(t, createResourceList(0, 0), parent1Info.CalculateInfo.Request)
	assert.Equal(t, createResourceList(0, 0), parent1Info.CalculateInfo.ChildRequest)
	assert.Equal(t, 0, len(gqm.quotaTopoNodeMap["parent1"].childGroupQuotaInfos))

	// delete parent1 quota
	err = gqm.deleteQuotaNoLock(parent1)
	assert.Nil(t, err)
	parent1Info = gqm.GetQuotaInfoByName("parent1")
	assert.True(t, parent1Info == nil)

	assert.Equal(t, 1, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 3, len(gqm.quotaInfoMap))
}

func TestGroupQuotaManager_UpdateQuotaDeltaRequest(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "test2", extension.RootQuotaName, 96, 160*GigaByte, 40, 80*GigaByte, true, false)

	// test1 request[120, 290]  runtime == maxQuota
	request := createResourceList(120, 200*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("test1", request, request, 0)
	runtime := gqm.RefreshRuntime("test1")
	assert.Equal(t, deltaRes, runtime)

	// test2 request[150, 210]  runtime
	request = createResourceList(150, 210*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("test2", request, request, 0)
	runtime = gqm.RefreshRuntime("test1")
	assert.Equal(t, createResourceList(53, 80*GigaByte), runtime)
	runtime = gqm.RefreshRuntime("test2")
	assert.Equal(t, createResourceList(43, 80*GigaByte), runtime)
}

func TestGroupQuotaManager_NotAllowLentResource(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(100, 0)
	gqm.UpdateClusterTotalResource(deltaRes)

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 0, 60, 0, true, false)
	AddQuotaToManager(t, gqm, "test2", extension.RootQuotaName, 96, 0, 40, 0, false, false)

	request := createResourceList(120, 0)
	gqm.updateGroupDeltaRequestNoLock("test1", request, request, 0)
	runtime := gqm.RefreshRuntime("test1")
	assert.Equal(t, int64(60), runtime.Cpu().Value())
	runtime = gqm.RefreshRuntime("test2")
	assert.Equal(t, int64(40), runtime.Cpu().Value())
}

func TestGroupQuotaManager_NotAllowLentResource_2(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(100, 0)
	gqm.UpdateClusterTotalResource(deltaRes)

	// root not allow  min:60
	//  - child1 not allow  min:20
	//  - child2 not allow  min:20
	AddQuotaToManager(t, gqm, "test-root", extension.RootQuotaName, 96, 0, 60, 0, false, true)
	AddQuotaToManager(t, gqm, "test-child1", "test-root", 96, 0, 20, 0, false, false)
	AddQuotaToManager(t, gqm, "test-child2", "test-root", 96, 0, 20, 0, false, false)

	// no request
	rootRuntime := gqm.RefreshRuntime("test-root")
	child1Runtime := gqm.RefreshRuntime("test-child1")
	child2Runtime := gqm.RefreshRuntime("test-child2")

	assert.Equal(t, int64(60), rootRuntime.Cpu().Value())
	assert.Equal(t, int64(20), child1Runtime.Cpu().Value())
	assert.Equal(t, int64(20), child2Runtime.Cpu().Value())

	// add 40 request
	request := createResourceList(40, 0)
	gqm.updateGroupDeltaRequestNoLock("test-child1", request, request, 0)

	rootRuntime = gqm.RefreshRuntime("test-root")
	child1Runtime = gqm.RefreshRuntime("test-child1")
	child2Runtime = gqm.RefreshRuntime("test-child2")

	assert.Equal(t, int64(60), rootRuntime.Cpu().Value())
	assert.Equal(t, int64(40), child1Runtime.Cpu().Value())
	assert.Equal(t, int64(20), child2Runtime.Cpu().Value())

	// add 20 request
	request2 := createResourceList(20, 0)
	gqm.updateGroupDeltaRequestNoLock("test-child1", request2, request2, 0)

	rootRuntime = gqm.RefreshRuntime("test-root")
	child1Runtime = gqm.RefreshRuntime("test-child1")
	child2Runtime = gqm.RefreshRuntime("test-child2")

	assert.Equal(t, int64(80), rootRuntime.Cpu().Value())
	assert.Equal(t, int64(60), child1Runtime.Cpu().Value())
	assert.Equal(t, int64(20), child2Runtime.Cpu().Value())
}

func TestGroupQuotaManager_NotAllowLentResource_3(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(100, 0)
	gqm.UpdateClusterTotalResource(deltaRes)

	// root allow  min:60
	//  - child1 not allow  min:20
	//  - child2 allow  min:20
	AddQuotaToManager(t, gqm, "test-root", extension.RootQuotaName, 96, 0, 60, 0, true, true)
	AddQuotaToManager(t, gqm, "test-child1", "test-root", 96, 0, 20, 0, false, false)
	AddQuotaToManager(t, gqm, "test-child2", "test-root", 96, 0, 20, 0, true, false)

	// no request
	rootRuntime := gqm.RefreshRuntime("test-root")
	child1Runtime := gqm.RefreshRuntime("test-child1")
	child2Runtime := gqm.RefreshRuntime("test-child2")

	assert.Equal(t, int64(20), rootRuntime.Cpu().Value())
	assert.Equal(t, int64(20), child1Runtime.Cpu().Value())
	assert.Equal(t, int64(0), child2Runtime.Cpu().Value())

	// add 40 request
	request := createResourceList(40, 0)
	gqm.updateGroupDeltaRequestNoLock("test-child1", request, request, 0)

	rootRuntime = gqm.RefreshRuntime("test-root")
	child1Runtime = gqm.RefreshRuntime("test-child1")
	child2Runtime = gqm.RefreshRuntime("test-child2")

	assert.Equal(t, int64(40), rootRuntime.Cpu().Value())
	assert.Equal(t, int64(40), child1Runtime.Cpu().Value())
	assert.Equal(t, int64(0), child2Runtime.Cpu().Value())

	// add 20 request
	request2 := createResourceList(20, 0)
	gqm.updateGroupDeltaRequestNoLock("test-child1", request2, request2, 0)

	rootRuntime = gqm.RefreshRuntime("test-root")
	child1Runtime = gqm.RefreshRuntime("test-child1")
	child2Runtime = gqm.RefreshRuntime("test-child2")

	assert.Equal(t, int64(60), rootRuntime.Cpu().Value())
	assert.Equal(t, int64(60), child1Runtime.Cpu().Value())
	assert.Equal(t, int64(0), child2Runtime.Cpu().Value())
}

func TestGroupQuotaManager_NotAllowLentResource_4(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(100, 0)
	gqm.UpdateClusterTotalResource(deltaRes)

	// root allow  min:60
	//  - child1 not allow  min:20
	//  - child2 not allow  min:20
	AddQuotaToManager(t, gqm, "test-root", extension.RootQuotaName, 96, 0, 60, 0, true, true)
	AddQuotaToManager(t, gqm, "test-child1", "test-root", 96, 0, 20, 0, false, false)
	AddQuotaToManager(t, gqm, "test-child2", "test-root", 96, 0, 20, 0, false, false)

	// no request
	rootRuntime := gqm.RefreshRuntime("test-root")
	child1Runtime := gqm.RefreshRuntime("test-child1")
	child2Runtime := gqm.RefreshRuntime("test-child2")

	assert.Equal(t, int64(40), rootRuntime.Cpu().Value())
	assert.Equal(t, int64(20), child1Runtime.Cpu().Value())
	assert.Equal(t, int64(20), child2Runtime.Cpu().Value())

	// add 40 request
	request := createResourceList(40, 0)
	gqm.updateGroupDeltaRequestNoLock("test-child1", request, request, 0)

	rootRuntime = gqm.RefreshRuntime("test-root")
	child1Runtime = gqm.RefreshRuntime("test-child1")
	child2Runtime = gqm.RefreshRuntime("test-child2")

	assert.Equal(t, int64(60), rootRuntime.Cpu().Value())
	assert.Equal(t, int64(40), child1Runtime.Cpu().Value())
	assert.Equal(t, int64(20), child2Runtime.Cpu().Value())

	// add 20 request
	request2 := createResourceList(20, 0)
	gqm.updateGroupDeltaRequestNoLock("test-child1", request2, request2, 0)

	rootRuntime = gqm.RefreshRuntime("test-root")
	child1Runtime = gqm.RefreshRuntime("test-child1")
	child2Runtime = gqm.RefreshRuntime("test-child2")

	assert.Equal(t, int64(80), rootRuntime.Cpu().Value())
	assert.Equal(t, int64(60), child1Runtime.Cpu().Value())
	assert.Equal(t, int64(20), child2Runtime.Cpu().Value())

}

func TestGroupQuotaManager_UpdateQuotaRequest(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "test2", extension.RootQuotaName, 96, 160*GigaByte, 40, 80*GigaByte, true, false)

	// 1. initial test1 request [60, 100]
	request := createResourceList(60, 100*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("test1", request, request, 0)
	runtime := gqm.RefreshRuntime("test1")
	assert.Equal(t, request, runtime)

	// test1 request[120, 290]  runtime == maxQuota
	newRequest := createResourceList(120, 200*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("test1", request, newRequest, 0)
	runtime = gqm.RefreshRuntime("test1")
	assert.Equal(t, deltaRes, runtime)

	// test2 request[150, 210]  runtime
	request = createResourceList(150, 210*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("test2", request, request, 0)
	runtime = gqm.RefreshRuntime("test1")
	assert.Equal(t, createResourceList(53, 80*GigaByte), runtime)
	runtime = gqm.RefreshRuntime("test2")
	assert.Equal(t, createResourceList(43, 80*GigaByte), runtime)
}

func TestGroupQuotaManager_UpdateGroupDeltaUsedAndNonPreemptibleUsed(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	// 1. test1 used[120, 290]  runtime == maxQuota nonPreemptibleUsed[20, 30]
	used := createResourceList(120, 290*GigaByte)
	nonPreemptibleUsed := createResourceList(20, 30*GigaByte)
	gqm.updateGroupDeltaUsedNoLock("test1", used, nonPreemptibleUsed, 0)
	quotaInfo := gqm.GetQuotaInfoByName("test1")
	assert.NotNil(t, quotaInfo)
	assert.Equal(t, used, quotaInfo.CalculateInfo.Used)
	assert.Equal(t, nonPreemptibleUsed, quotaInfo.CalculateInfo.NonPreemptibleUsed)
	assert.Equal(t, nonPreemptibleUsed, quotaInfo.CalculateInfo.SelfNonPreemptibleUsed)
	assert.Equal(t, used, quotaInfo.CalculateInfo.SelfUsed)

	// 2. used increases to [130,300]
	used = createResourceList(10, 10*GigaByte)
	nonPreemptibleUsed = createResourceList(10, 10*GigaByte)
	gqm.updateGroupDeltaUsedNoLock("test1", used, nonPreemptibleUsed, 0)
	quotaInfo = gqm.GetQuotaInfoByName("test1")
	assert.NotNil(t, quotaInfo)
	assert.Equal(t, createResourceList(130, 300*GigaByte), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(30, 40*GigaByte), quotaInfo.CalculateInfo.NonPreemptibleUsed)
	assert.Equal(t, createResourceList(130, 300*GigaByte), quotaInfo.CalculateInfo.SelfUsed)
	assert.Equal(t, createResourceList(30, 40*GigaByte), quotaInfo.CalculateInfo.SelfNonPreemptibleUsed)

	// 3. used decreases to [90,100]
	used = createResourceList(-40, -200*GigaByte)
	nonPreemptibleUsed = createResourceList(-15, -20*GigaByte)
	gqm.updateGroupDeltaUsedNoLock("test1", used, nonPreemptibleUsed, 0)
	quotaInfo = gqm.GetQuotaInfoByName("test1")
	assert.NotNil(t, quotaInfo)
	assert.Equal(t, createResourceList(90, 100*GigaByte), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(15, 20*GigaByte), quotaInfo.CalculateInfo.NonPreemptibleUsed)
	assert.Equal(t, createResourceList(90, 100*GigaByte), quotaInfo.CalculateInfo.SelfUsed)
	assert.Equal(t, createResourceList(15, 20*GigaByte), quotaInfo.CalculateInfo.SelfNonPreemptibleUsed)
}

func TestGroupQuotaManager_MultiQuotaAdd(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 60, 100*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test1-sub1", "test1", 96, 160*GigaByte, 10, 30*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "test1-sub2", "test1", 96, 160*GigaByte, 20, 30*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "test1-sub3", "test1", 96, 160*GigaByte, 30, 40*GigaByte, true, false)

	AddQuotaToManager(t, gqm, "test2", extension.RootQuotaName, 300, 400*GigaByte, 200, 300*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test2-sub1", "test2", 96, 160*GigaByte, 96, 160*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "test2-sub2", "test2", 82, 100*GigaByte, 80, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test2-sub2-1", "test2-sub2", 96, 160*GigaByte, 0, 0*GigaByte, true, false)

	assert.Equal(t, 9, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 11, len(gqm.quotaInfoMap))
	assert.Equal(t, 2, len(gqm.resourceKeys))
	assert.Equal(t, 2, len(gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].quotaTree[v1.ResourceCPU].quotaNodes))
	assert.Equal(t, 2, len(gqm.runtimeQuotaCalculatorMap["test2"].quotaTree[v1.ResourceCPU].quotaNodes))
	assert.Equal(t, 1, len(gqm.runtimeQuotaCalculatorMap["test2-sub2"].quotaTree[v1.ResourceCPU].quotaNodes))
}

// TestGroupQuotaManager_MultiUpdateQuotaRequest  test the relationship of request and max
// parentQuotaGroup's request is the sum of limitedRequest of its child.
func TestGroupQuotaManager_MultiUpdateQuotaRequest(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	// test1 Max[96, 160]  Min[50,80] request[96,130]
	//   |-- test1-a Max[96, 160]  Min[50,80] request[96,130]
	//         |-- a-123 Max[96, 160]  Min[50,80] request[96,130]
	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test1-a", "test1", 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "a-123", "test1-a", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	request := createResourceList(96, 130*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("a-123", request, request, 0)
	runtime := gqm.RefreshRuntime("a-123")
	assert.Equal(t, request, runtime)
	runtime = gqm.RefreshRuntime("test1-a")
	assert.Equal(t, request, runtime)
	runtime = gqm.RefreshRuntime("test1")
	assert.Equal(t, request, runtime)

	// decrease a-123 Max[64, 128]  Min[50,80] request[96,130]
	quota1 := CreateQuota("a-123", "test1-a", 64, 128*GigaByte, 50, 80*GigaByte, true, false)
	gqm.UpdateQuota(quota1, false)
	quotaInfo := gqm.GetQuotaInfoByName("test1-a")
	assert.Equal(t, createResourceList(96, 160*GigaByte), quotaInfo.CalculateInfo.Max)
	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, runtime, quotaInfo.CalculateInfo.Request)
	assert.Equal(t, createResourceList(64, 128*GigaByte), runtime)
	quotaInfo = gqm.GetQuotaInfoByName("a-123")
	assert.Equal(t, request, quotaInfo.CalculateInfo.Request)

	// increase
	quota1 = CreateQuota("a-123", "test1-a", 100, 200*GigaByte, 90, 160*GigaByte, true, false)
	gqm.UpdateQuota(quota1, false)
	quotaInfo = gqm.GetQuotaInfoByName("test1-a")
	assert.Equal(t, createResourceList(96, 160*GigaByte), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, createResourceList(96, 130*GigaByte), quotaInfo.CalculateInfo.Request)
	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, runtime, quotaInfo.CalculateInfo.Request)
	assert.Equal(t, request, runtime)
	quotaInfo = gqm.GetQuotaInfoByName("a-123")
	assert.Equal(t, request, quotaInfo.CalculateInfo.Request)
}

func TestGroupQuotaManager_UpdateDefaultQuotaGroup(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	gqm.UpdateQuota(&v1alpha1.ElasticQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name: extension.DefaultQuotaName,
		},
		Spec: v1alpha1.ElasticQuotaSpec{
			Max: createResourceList(100, 1000),
		},
	}, false)
	assert.Equal(t, gqm.GetQuotaInfoByName(extension.DefaultQuotaName).CalculateInfo.Max, createResourceList(100, 1000))
	runtime := gqm.RefreshRuntime(extension.DefaultQuotaName)
	assert.Equal(t, runtime, createResourceList(100, 1000))
}

// TestGroupQuotaManager_MultiUpdateQuotaRequest2 test the relationship of min/max and request
// increase the request to test the case request < min, min < request < max, max < request
func TestGroupQuotaManager_MultiUpdateQuotaRequest2(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	// test1 Max[96, 160]  Min[80,80]
	//   |-- test1-a Max[60, 120]  Min[50,80]
	//         |-- a-123 Max[30, 60]  Min[20,40]
	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 80, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test1-a", "test1", 60, 80*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "a-123", "test1-a", 30, 60*GigaByte, 20, 40*GigaByte, true, false)

	// a-123 request[10,30]  request < min
	request := createResourceList(10, 30*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("a-123", request, request, 0)
	runtime := gqm.RefreshRuntime("a-123")
	assert.Equal(t, request, runtime)
	runtime = gqm.RefreshRuntime("test1-a")
	assert.Equal(t, request, runtime)
	runtime = gqm.RefreshRuntime("test1")
	assert.Equal(t, request, runtime)

	// a-123 add request[15,15]  totalRequest[25,45] request > min
	request = createResourceList(15, 15*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("a-123", request, request, 0)
	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(25, 45*GigaByte), runtime)
	quotaInfo := gqm.GetQuotaInfoByName("test1-a")
	assert.Equal(t, createResourceList(25, 45*GigaByte), quotaInfo.CalculateInfo.Request)
	quotaInfo = gqm.GetQuotaInfoByName("test1")
	assert.Equal(t, createResourceList(25, 45*GigaByte), quotaInfo.CalculateInfo.Request)

	// a-123 add request[30,30]  totalRequest[55,75] request > max
	request = createResourceList(30, 30*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("a-123", request, request, 0)
	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(30, 60*GigaByte), runtime)
	quotaInfo = gqm.GetQuotaInfoByName("test1-a")
	assert.Equal(t, createResourceList(30, 60*GigaByte), quotaInfo.CalculateInfo.Request)
	quotaInfo = gqm.GetQuotaInfoByName("test1")
	assert.Equal(t, createResourceList(30, 60*GigaByte), quotaInfo.CalculateInfo.Request)
}

// TestGroupQuotaManager_MultiUpdateQuotaRequest_WithScaledMinQuota1 test scaledMinQuota when quotaGroup's sum of the minQuota
// is larger than totalRes; and enlarge the totalRes to test whether gqm can recover the oriMinQuota or not.
func TestGroupQuotaManager_MultiUpdateQuotaRequest_WithScaledMinQuota1(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	AddQuotaToManager(t, gqm, "p", extension.RootQuotaName, 1000, 1000*GigaByte, 300, 300*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "a", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "b", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "c", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)

	request := createResourceList(200, 200*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("a", request, request, 0)
	gqm.updateGroupDeltaRequestNoLock("b", request, request, 0)
	gqm.updateGroupDeltaRequestNoLock("c", request, request, 0)

	deltaRes := createResourceList(200, 200*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)

	runtime := gqm.RefreshRuntime("p")
	assert.Equal(t, createResourceList(200, 200*GigaByte), runtime)
	gqm.RefreshRuntime("a")
	gqm.RefreshRuntime("b")
	gqm.RefreshRuntime("c")
	runtime = gqm.RefreshRuntime("a")
	assert.Equal(t, createResourceList2(66667, 200*GigaByte/3+1), runtime)
	// after group "c" refreshRuntime, the runtime of "a" and "b" has changed
	assert.Equal(t, createResourceList2(66667, 200*GigaByte/3+1), gqm.RefreshRuntime("a"))
	assert.Equal(t, createResourceList2(66667, 200*GigaByte/3+1), gqm.RefreshRuntime("b"))

	quotaInfo := gqm.GetQuotaInfoByName("p")
	assert.Equal(t, createResourceList(200, 200*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("a")
	assert.Equal(t, createResourceList2(66666, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("b")
	assert.Equal(t, createResourceList2(66666, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("c")
	assert.Equal(t, createResourceList2(66666, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	// large
	deltaRes = createResourceList(400, 400*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)

	runtime = gqm.RefreshRuntime("p")
	assert.Equal(t, createResourceList(600, 600*GigaByte), runtime)

	runtime = gqm.RefreshRuntime("a")
	assert.Equal(t, createResourceList(200, 200*GigaByte), runtime)

	runtime = gqm.RefreshRuntime("b")
	assert.Equal(t, createResourceList(200, 200*GigaByte), runtime)

	runtime = gqm.RefreshRuntime("c")
	assert.Equal(t, createResourceList(200, 200*GigaByte), runtime)

	quotaInfo = gqm.GetQuotaInfoByName("p")
	assert.Equal(t, createResourceList(300, 300*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("a")
	assert.Equal(t, createResourceList(100, 100*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("b")
	assert.Equal(t, createResourceList(100, 100*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("c")
	assert.Equal(t, createResourceList(100, 100*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)
}

// TestGroupQuotaManager_MultiUpdateQuotaRequest_WithScaledMinQuota1 test scaledMinQuota when quotaGroup's sum of the
// minQuota is larger than totalRes, with one of the quotaGroup's request is zero.
func TestGroupQuotaManager_MultiUpdateQuotaRequest_WithScaledMinQuota2(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true
	deltaRes := createResourceList(1, 1*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)

	AddQuotaToManager(t, gqm, "p", extension.RootQuotaName, 1000, 1000*GigaByte, 300, 300*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "a", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "b", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "c", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)

	request := createResourceList(200, 200*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("a", request, request, 0)
	gqm.updateGroupDeltaRequestNoLock("b", createResourceList(0, 0), createResourceList(0, 0), 0)
	gqm.updateGroupDeltaRequestNoLock("c", request, request, 0)
	gqm.UpdateClusterTotalResource(createResourceList(199, 199*GigaByte))
	runtime := gqm.RefreshRuntime("p")
	assert.Equal(t, createResourceList(200, 200*GigaByte), runtime)

	gqm.RefreshRuntime("a")
	gqm.RefreshRuntime("b")
	gqm.RefreshRuntime("c")

	runtime = gqm.RefreshRuntime("a")
	assert.Equal(t, createResourceList(100, 100*GigaByte), runtime)

	runtime = gqm.RefreshRuntime("b")
	assert.Equal(t, createResourceList(0, 0*GigaByte), runtime)

	runtime = gqm.RefreshRuntime("c")
	assert.Equal(t, createResourceList(100, 100*GigaByte), runtime)

	quotaInfo := gqm.GetQuotaInfoByName("p")
	assert.Equal(t, createResourceList(200, 200*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("a")
	assert.Equal(t, createResourceList2(66666, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("b")
	assert.Equal(t, createResourceList2(66666, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("c")
	assert.Equal(t, createResourceList2(66666, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)
}

func TestGroupQuotaManager_MultiUpdateQuotaUsedAndNonPreemptibleUsed(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test1-sub1", "test1", 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test1-sub1-1", "test1-sub1", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	used := createResourceList(120, 290*GigaByte)
	nonPreemptibleUsed := createResourceList(50, 100*GigaByte)
	gqm.updateGroupDeltaUsedNoLock("test1-sub1", used, nonPreemptibleUsed, 0)
	quotaInfo := gqm.GetQuotaInfoByName("test1-sub1")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, used, quotaInfo.CalculateInfo.Used)
	assert.Equal(t, nonPreemptibleUsed, quotaInfo.CalculateInfo.NonPreemptibleUsed)

	quotaInfo = gqm.GetQuotaInfoByName("test1-sub1")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, used, quotaInfo.CalculateInfo.Used)
	assert.Equal(t, nonPreemptibleUsed, quotaInfo.CalculateInfo.NonPreemptibleUsed)

	quotaInfo = gqm.GetQuotaInfoByName("test1")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, used, quotaInfo.CalculateInfo.Used)
	assert.Equal(t, nonPreemptibleUsed, quotaInfo.CalculateInfo.NonPreemptibleUsed)
}

func TestGroupQuotaManager_MultiUpdateQuotaRequestAndNonPreemptibleRequest(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test1-sub1", "test1", 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test1-sub1-1", "test1-sub1", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	request := createResourceList(120, 290*GigaByte)
	nonPreemptibleRequest := createResourceList(50, 100*GigaByte)
	limitedRequest := createResourceList(96, 160*GigaByte)

	gqm.updateGroupDeltaRequestNoLock("test1-sub1-1", request, nonPreemptibleRequest, 0)
	quotaInfo := gqm.GetQuotaInfoByName("test1-sub1-1")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, request, quotaInfo.CalculateInfo.Request)
	assert.Equal(t, nonPreemptibleRequest, quotaInfo.CalculateInfo.NonPreemptibleRequest)

	quotaInfo = gqm.GetQuotaInfoByName("test1-sub1")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, limitedRequest, quotaInfo.CalculateInfo.Request)
	assert.Equal(t, nonPreemptibleRequest, quotaInfo.CalculateInfo.NonPreemptibleRequest)

	quotaInfo = gqm.GetQuotaInfoByName("test1")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, limitedRequest, quotaInfo.CalculateInfo.Request)
	assert.Equal(t, nonPreemptibleRequest, quotaInfo.CalculateInfo.NonPreemptibleRequest)
}

func TestGroupQuotaManager_UpdateQuotaParentName(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	// test2 Max[96, 160]  Min[50,80] request[20,40]
	//   `-- test2-a Max[96, 160]  Min[50,80] request[20,40]
	// test1 Max[96, 160]  Min[50,80] request[60,100]
	//   `-- test1-a Max[96, 160]  Min[50,80] request[60,100]
	//         `-- a-123 Max[96, 160]  Min[50,80] request[60,100]
	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 100, 160*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test1-a", "test1", 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	changeQuota := AddQuotaToManager(t, gqm, "a-123", "test1-a", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	AddQuotaToManager(t, gqm, "test2", extension.RootQuotaName, 96, 160*GigaByte, 100, 160*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test2-a", "test2", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	// a-123 request [60,100]
	request := createResourceList(60, 100*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("a-123", request, request, 0)
	gqm.updateGroupDeltaUsedNoLock("a-123", request, createResourceList(0, 0), 0)
	qi1 := gqm.GetQuotaInfoByName("test1")
	qi2 := gqm.GetQuotaInfoByName("test1-a")
	qi3 := gqm.GetQuotaInfoByName("a-123")
	qi4 := gqm.GetQuotaInfoByName("test2")
	qi5 := gqm.GetQuotaInfoByName("test2-a")
	assert.Equal(t, request, qi1.CalculateInfo.Request)
	assert.Equal(t, request, qi1.CalculateInfo.Used)
	assert.Equal(t, request, qi2.CalculateInfo.Request)
	assert.Equal(t, request, qi2.CalculateInfo.Used)
	assert.Equal(t, request, qi3.CalculateInfo.SelfRequest)
	assert.Equal(t, request, qi3.CalculateInfo.SelfUsed)
	assert.Equal(t, v1.ResourceList{}, qi1.CalculateInfo.SelfRequest)
	assert.Equal(t, v1.ResourceList{}, qi1.CalculateInfo.SelfUsed)
	assert.Equal(t, v1.ResourceList{}, qi2.CalculateInfo.SelfRequest)
	assert.Equal(t, v1.ResourceList{}, qi2.CalculateInfo.SelfUsed)
	assert.Equal(t, request, qi3.CalculateInfo.SelfRequest)
	assert.Equal(t, request, qi3.CalculateInfo.SelfUsed)

	runtime := gqm.RefreshRuntime("a-123")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("test1-a")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("test1")
	assert.Equal(t, request, runtime)

	// test2-a request [20,40]
	request = createResourceList(20, 40*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("test2-a", request, request, 0)
	gqm.updateGroupDeltaUsedNoLock("test2-a", request, createResourceList(0, 0), 0)
	assert.Equal(t, request, qi4.CalculateInfo.Request)
	assert.Equal(t, request, qi4.CalculateInfo.Used)
	assert.Equal(t, request, qi5.CalculateInfo.Request)
	assert.Equal(t, request, qi5.CalculateInfo.Used)
	assert.Equal(t, v1.ResourceList{}, qi4.CalculateInfo.SelfRequest)
	assert.Equal(t, v1.ResourceList{}, qi4.CalculateInfo.SelfUsed)
	assert.Equal(t, request, qi5.CalculateInfo.SelfRequest)
	assert.Equal(t, request, qi5.CalculateInfo.SelfUsed)

	runtime = gqm.RefreshRuntime("test2-a")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("test2")
	assert.Equal(t, request, runtime)

	// a-123 mv test2
	// test2 Max[96, 160]  Min[100,160] request[80,140]
	//   `-- test2-a Max[96, 160]  Min[50,80] request[20,40]
	//   `-- a-123 Max[96, 160]  Min[50,80] request[60,100]
	// test1 Max[96, 160]  Min[100,160] request[0,0]
	//   `-- test1-a Max[96, 160]  Min[50,80] request[0,0]
	changeQuota.Labels[extension.LabelQuotaParent] = "test2"
	gqm.UpdateQuota(changeQuota, false)

	test2, _ := json.Marshal(gqm.GetQuotaInfoByName("test2"))
	fmt.Println("test2", string(test2))
	test2a, _ := json.Marshal(gqm.GetQuotaInfoByName("test2-a"))
	fmt.Println("test2-a", string(test2a))
	a123, _ := json.Marshal(gqm.GetQuotaInfoByName("a-123"))
	fmt.Println("a-123", string(a123))
	test1, _ := json.Marshal(gqm.GetQuotaInfoByName("test1"))
	fmt.Println("test1", string(test1))
	test1a, _ := json.Marshal(gqm.GetQuotaInfoByName("test1-a"))
	fmt.Println("test1-a", string(test1a))

	quotaInfo := gqm.GetQuotaInfoByName("test1-a")
	gqm.RefreshRuntime("test1-a")
	assert.Equal(t, createResourceList(0, 0), quotaInfo.CalculateInfo.Request)
	assert.Equal(t, createResourceList(0, 0), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(0, 0), quotaInfo.CalculateInfo.Runtime)

	quotaInfo = gqm.GetQuotaInfoByName("test1")
	gqm.RefreshRuntime("test1")
	assert.Equal(t, createResourceList(0, 0), quotaInfo.CalculateInfo.Request)
	assert.Equal(t, createResourceList(0, 0), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(0, 0), quotaInfo.CalculateInfo.Runtime)

	quotaInfo = gqm.GetQuotaInfoByName("a-123")
	gqm.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(60, 100*GigaByte), quotaInfo.CalculateInfo.Request)
	assert.Equal(t, createResourceList(60, 100*GigaByte), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(60, 100*GigaByte), quotaInfo.CalculateInfo.Runtime)
	assert.Equal(t, "test2", quotaInfo.ParentName)

	quotaInfo = gqm.GetQuotaInfoByName("test2-a")
	gqm.RefreshRuntime("test2-a")
	assert.Equal(t, createResourceList(20, 40*GigaByte), quotaInfo.CalculateInfo.Request)
	assert.Equal(t, createResourceList(20, 40*GigaByte), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(20, 40*GigaByte), quotaInfo.CalculateInfo.Runtime)

	quotaInfo = gqm.GetQuotaInfoByName("test2")
	gqm.RefreshRuntime("test2")
	assert.Equal(t, createResourceList(80, 140*GigaByte), quotaInfo.CalculateInfo.Request)
	assert.Equal(t, createResourceList(80, 140*GigaByte), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(80, 140*GigaByte), quotaInfo.CalculateInfo.Runtime)
}

func TestGroupQuotaManager_UpdateClusterTotalResource(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	totalRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(totalRes)
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, totalRes, gqm.totalResourceExceptSystemAndDefaultUsed)
	quotaTotalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, totalRes, quotaTotalRes)

	totalRes = createResourceList(64, 360*GigaByte)
	gqm.UpdateClusterTotalResource(createResourceList(-32, 200*GigaByte))
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, totalRes, gqm.totalResourceExceptSystemAndDefaultUsed)
	quotaTotalRes = gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, totalRes, quotaTotalRes)

	totalRes = createResourceList(100, 540*GigaByte)
	gqm.UpdateClusterTotalResource(createResourceList(36, 180*GigaByte))
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, totalRes, gqm.totalResourceExceptSystemAndDefaultUsed)
	quotaTotalRes = gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, totalRes, quotaTotalRes)

	sysUsed := createResourceList(10, 30*GigaByte)
	gqm.updateGroupDeltaUsedNoLock(extension.SystemQuotaName, sysUsed, createResourceList(0, 0), 0)
	assert.Equal(t, sysUsed, gqm.GetQuotaInfoByName(extension.SystemQuotaName).GetUsed())

	// 90, 510
	delta := totalRes.DeepCopy()
	delta = quotav1.SubtractWithNonNegativeResult(delta, sysUsed)
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, delta, gqm.totalResourceExceptSystemAndDefaultUsed)
	quotaTotalRes = gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, delta, quotaTotalRes)

	// 80, 480
	gqm.updateGroupDeltaUsedNoLock(extension.SystemQuotaName, createResourceList(10, 30), createResourceList(0, 0), 0)
	delta = quotav1.Subtract(delta, createResourceList(10, 30))
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, delta, gqm.totalResourceExceptSystemAndDefaultUsed)

	// 70, 450
	defaultUsed := createResourceList(10, 30)
	gqm.updateGroupDeltaUsedNoLock(extension.DefaultQuotaName, defaultUsed, createResourceList(0, 0), 0)
	assert.Equal(t, defaultUsed, gqm.GetQuotaInfoByName(extension.DefaultQuotaName).GetUsed())
	delta = quotav1.Subtract(delta, defaultUsed)
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, delta, gqm.totalResourceExceptSystemAndDefaultUsed)

	// 60 420
	gqm.updateGroupDeltaUsedNoLock(extension.DefaultQuotaName, defaultUsed, createResourceList(0, 0), 0)
	delta = quotav1.Subtract(delta, defaultUsed)
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, delta, gqm.totalResourceExceptSystemAndDefaultUsed)
}

func AddQuotaToManager(t *testing.T,
	gqm *GroupQuotaManager,
	quotaName string,
	parent string,
	maxCpu, maxMem, minCpu, minMem int64, allowLentResource bool, isParent bool) *v1alpha1.ElasticQuota {
	quota := CreateQuota(quotaName, parent, maxCpu, maxMem, minCpu, minMem, allowLentResource, isParent)
	err := gqm.UpdateQuota(quota, false)
	assert.Nil(t, err)
	quotaInfo := gqm.quotaInfoMap[quotaName]
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, quotaName, quotaInfo.Name)
	assert.Equal(t, quotaInfo.CalculateInfo.Max, createResourceList(maxCpu, maxMem))
	assert.Equal(t, quotaInfo.CalculateInfo.Min, createResourceList(minCpu, minMem))
	assert.Equal(t, quotaInfo.CalculateInfo.SharedWeight.Memory().Value(), maxMem)
	assert.Equal(t, quotaInfo.CalculateInfo.SharedWeight.Cpu().Value(), maxCpu)

	// assert whether the parent quotaTree has the sub quota group
	quotaTreeWrapper := gqm.runtimeQuotaCalculatorMap[quotaName]
	assert.NotNil(t, quotaTreeWrapper)
	quotaTreeWrapper = gqm.runtimeQuotaCalculatorMap[parent]
	assert.NotNil(t, quotaTreeWrapper)
	find, nodeInfo := quotaTreeWrapper.quotaTree[v1.ResourceCPU].find(quotaName)
	assert.True(t, find)
	assert.Equal(t, nodeInfo.quotaName, quotaName)
	return quota
}

func CreateQuota(quotaName string, parent string, maxCpu, maxMem, minCpu, minMem int64, allowLentResource bool, isParent bool) *v1alpha1.ElasticQuota {
	quota := &v1alpha1.ElasticQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:        quotaName,
			Annotations: make(map[string]string),
			Labels:      make(map[string]string),
		},
		Spec: v1alpha1.ElasticQuotaSpec{
			Max: createResourceList(maxCpu, maxMem),
			Min: createResourceList(minCpu, minMem),
		},
	}

	quota.Annotations[extension.AnnotationSharedWeight] = fmt.Sprintf("{\"cpu\":%v, \"memory\":\"%v\"}", maxCpu, maxMem)
	quota.Labels[extension.LabelQuotaParent] = parent
	if allowLentResource {
		quota.Labels[extension.LabelAllowLentResource] = "true"
	} else {
		quota.Labels[extension.LabelAllowLentResource] = "false"
	}

	if isParent {
		quota.Labels[extension.LabelQuotaIsParent] = "true"
	} else {
		quota.Labels[extension.LabelQuotaIsParent] = "false"
	}
	return quota
}

func TestGroupQuotaManager_MultiChildMaxGreaterParentMax_MaxGreaterThanTotalRes(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(300, 8000)
	gqm.UpdateClusterTotalResource(deltaRes)

	// test1 Max[600, 4096]  Min[100, 100]
	//   |-- test1-sub1 Max[500, 2048]  Min[100, 100]  Req[500, 4096]
	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 600, 4096, 100, 100, true, true)
	AddQuotaToManager(t, gqm, "test1-sub1", "test1", 500, 2048, 100, 100, true, false)

	// test1 Request [500, 4096] limitRequest [500, 2048]
	// test1-sub Request [500,2048] limitedRequest [500, 2048] limited by rootRes [300, 8000] -> [300,2048]
	request := createResourceList(500, 4096)
	gqm.updateGroupDeltaRequestNoLock("test1-sub1", request, request, 0)
	runtime := gqm.RefreshRuntime("test1-sub1")
	assert.Equal(t, createResourceList(300, 2048), runtime)
	fmt.Printf("quota1 runtime:%v\n", runtime)

	// test1 Request [1050, 8192] limitRequest [500, 2048]
	// test1-sub1 Request [500,2048] limitedRequest [500, 2048] limited by rootRes [300, 8000] -> [300,2048]
	request = createResourceList(550, 4096)
	gqm.updateGroupDeltaRequestNoLock("test1-sub1", request, request, 0)
	runtime = gqm.RefreshRuntime("test1-sub")
	fmt.Printf("quota1 runtime:%v\n", runtime)

	quotaInfo := gqm.GetQuotaInfoByName("test1")
	assert.Equal(t, quotaInfo.CalculateInfo.Request, createResourceList(500, 2048))
	assert.Equal(t, quotaInfo.getLimitRequestNoLock(), createResourceList(500, 2048))
	assert.Equal(t, quotaInfo.CalculateInfo.Max, createResourceList(600, 4096))
	assert.Equal(t, quotaInfo.CalculateInfo.Runtime, createResourceList(300, 2048))
	quotaInfo = gqm.GetQuotaInfoByName("test1-sub1")
	assert.Equal(t, quotaInfo.CalculateInfo.Request, createResourceList(1050, 8192))
	assert.Equal(t, quotaInfo.getLimitRequestNoLock(), createResourceList(500, 2048))
	assert.Equal(t, quotaInfo.CalculateInfo.Max, createResourceList(500, 2048))
	assert.Equal(t, quotaInfo.CalculateInfo.Runtime, createResourceList(300, 2048))
}

func TestGroupQuotaManager_MultiChildMaxGreaterParentMax(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	deltaRes := createResourceList(350, 1800*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)

	// test1 Max[300, 1024]  Min[176, 756]
	//   |-- test1-sub1 Max[500, 2048]  Min[100, 512]
	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 300, 1024*GigaByte, 176, 756*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "test1-sub1", "test1", 500, 2048*GigaByte, 100, 512*GigaByte, true, false)

	// test1 max < request < test1-sub1 max
	// test1-sub1 Request[400, 1500] limitedRequest [400, 1500]
	// test1 Request [400,1500] limitedRequest [300, 1024]
	request := createResourceList(400, 1500*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("test1-sub1", request, request, 0)
	quotaInfo := gqm.GetQuotaInfoByName("test1")
	assert.Equal(t, quotaInfo.CalculateInfo.Request, createResourceList(400, 1500*GigaByte))
	quotaInfo = gqm.GetQuotaInfoByName("test1-sub1")
	assert.Equal(t, quotaInfo.CalculateInfo.Request, createResourceList(400, 1500*GigaByte))
	runtime := gqm.RefreshRuntime("test1-sub1")
	assert.Equal(t, createResourceList(300, 1024*GigaByte), runtime)
	fmt.Printf("quota1 runtime:%v\n", runtime)

	// test1 max < test1-sub1 max < request
	// test1-sub1 Request[800, 3000] limitedRequest [500, 2048]
	// test1 Request [500, 2048] limitedRequest [300, 1024]
	gqm.updateGroupDeltaRequestNoLock("test1-sub1", request, request, 0)
	runtime = gqm.RefreshRuntime("test1-sub1")
	assert.Equal(t, createResourceList(300, 1024*GigaByte), runtime)
	fmt.Printf("quota1 runtime:%v\n", runtime)
}

func TestGroupQuotaManager_UpdateQuotaTreeDimension_UpdateQuota(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	info3 := CreateQuota("3", extension.RootQuotaName, 1000, 10000, 100, 1000, true, false)
	info3.Spec.Max["tmp"] = *resource.NewQuantity(1, resource.DecimalSI)
	res := createResourceList(1000, 10000)
	gqm.UpdateClusterTotalResource(res)
	err := gqm.UpdateQuota(info3, false)
	if err != nil {
		klog.Infof("%v", err)
	}
	assert.Equal(t, len(gqm.resourceKeys), 3)
}

func TestGroupQuotaManager_RefreshAndGetRuntimeQuota_UpdateQuota(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	gqm.scaleMinQuotaEnabled = true
	cluRes := createResourceList(50, 50)
	gqm.UpdateClusterTotalResource(cluRes)

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, true)
	qi2 := CreateQuota("2", extension.RootQuotaName, 40, 40, 0, 0, true, false)

	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	assert.Equal(t, len(gqm.resourceKeys), 2)
	// case1: there is no request, runtime quota is zero
	runtime := gqm.RefreshRuntime("1")
	assert.Equal(t, runtime, createResourceList(0, 0))

	// case2: no existed group
	assert.Nil(t, gqm.RefreshRuntime("5"))
	gqm.updateGroupDeltaRequestNoLock("1", createResourceList(5, 5), createResourceList(5, 5), 0)
	gqm.updateGroupDeltaRequestNoLock("2", createResourceList(5, 5), createResourceList(5, 5), 0)
	gq1 := gqm.GetQuotaInfoByName("1")
	gq2 := gqm.GetQuotaInfoByName("2")

	// case3: version is same, should not update
	gq1.RuntimeVersion = gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].globalRuntimeVersion
	gq2.RuntimeVersion = gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].globalRuntimeVersion
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(0, 0))
	assert.Equal(t, gqm.RefreshRuntime("2"), v1.ResourceList{})

	// case4: version is different, should update
	gq1.RuntimeVersion = 0
	gq2.RuntimeVersion = 0
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(5, 5))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(5, 5))

	// case5: request is larger than min
	gqm.updateGroupDeltaRequestNoLock("1", createResourceList(25, 25), createResourceList(25, 25), 0)
	gqm.updateGroupDeltaRequestNoLock("2", createResourceList(25, 25), createResourceList(25, 25), 0)
	// 1 min [10,10] -> toPartitionRes [40,40] -> runtime [30,30]
	// 2 min [0,0]	-> toPartitionRes [40,40] -> runtime [20,20]
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(30, 30))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(20, 20))
}

func TestGroupQuotaManager_UpdateSharedWeight_UpdateQuota(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(60, 60))

	// case1: if not config SharedWeight, equal to maxQuota
	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, true)
	qi2 := CreateQuota("2", extension.RootQuotaName, 40, 40, 0, 0, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	assert.Equal(t, gqm.quotaInfoMap["1"].CalculateInfo.SharedWeight.Cpu().Value(), int64(40))
	assert.Equal(t, gqm.quotaInfoMap["2"].CalculateInfo.SharedWeight.Cpu().Value(), int64(40))

	// case2: if config SharedWeight
	sharedWeight1, _ := json.Marshal(createResourceList(30, 30))
	qi1.Annotations[extension.AnnotationSharedWeight] = string(sharedWeight1)
	sharedWeight2, _ := json.Marshal(createResourceList(10, 10))
	qi2.Annotations[extension.AnnotationSharedWeight] = string(sharedWeight2)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	result1, _ := json.Marshal(gqm.quotaInfoMap["1"].CalculateInfo.SharedWeight)
	result2, _ := json.Marshal(gqm.quotaInfoMap["2"].CalculateInfo.SharedWeight)
	assert.Equal(t, result1, sharedWeight1)
	assert.Equal(t, result2, sharedWeight2)
}

func TestGroupQuotaManager_UpdateOneGroupMaxQuota_UpdateQuota(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, true)
	qi2 := CreateQuota("2", extension.RootQuotaName, 40, 40, 10, 10, true, false)

	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	// case1: min < req < max
	gqm.updateGroupDeltaRequestNoLock("1", createResourceList(35, 35), createResourceList(35, 35), 0)
	gqm.updateGroupDeltaRequestNoLock("2", createResourceList(35, 35), createResourceList(35, 35), 0)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(25, 25))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(25, 25))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(35, 35))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(35, 35))

	// case2: decrease max, min < max < request
	qi1.Spec.Max = createResourceList(30, 30)
	qi2.Spec.Max = createResourceList(30, 30)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(25, 25))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(25, 25))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(30, 30))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(30, 30))

	// case3: increase max, min < req < max
	qi1.Spec.Max = createResourceList(50, 50)
	qi2.Spec.Max = createResourceList(50, 50)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(25, 25))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(25, 25))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(35, 35))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(35, 35))
}

func TestGroupQuotaManager_UpdateOneGroupMinQuota_UpdateQuota(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, true)
	qi2 := CreateQuota("2", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	// case1:  min < req < max
	gqm.updateGroupDeltaRequestNoLock("1", createResourceList(15, 15), createResourceList(15, 15), 0)
	gqm.updateGroupDeltaRequestNoLock("2", createResourceList(15, 15), createResourceList(15, 15), 0)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(15, 15))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(15, 15))

	// case2: increase min, req = min
	qi1.Spec.Min = createResourceList(15, 15)
	qi2.Spec.Min = createResourceList(15, 15)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(15, 15))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(15, 15))

	// case3: increase min, req < min
	qi1.Spec.Min = createResourceList(20, 20)
	qi2.Spec.Min = createResourceList(20, 20)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(15, 15))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(15, 15))

	// case4: decrease min, min < req < max
	qi1.Spec.Min = createResourceList(5, 5)
	qi2.Spec.Min = createResourceList(5, 5)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(15, 15))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(15, 15))

	// case5: update min, min == max, request > max
	qi1.Spec.Min = createResourceList(40, 40)
	qi2.Spec.Min = createResourceList(5, 5)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	gqm.updateGroupDeltaRequestNoLock("1", createResourceList(100, 100), createResourceList(100, 100), 0)
	gqm.updateGroupDeltaRequestNoLock("2", createResourceList(100, 100), createResourceList(100, 100), 0)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(40, 40))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(10, 10))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(40, 40))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(40, 40))
}

func TestGroupQuotaManager_DeleteOneGroup_UpdateQuota(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	qi2 := CreateQuota("2", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	gqm.updateGroupDeltaRequestNoLock("1", createResourceList(15, 15), createResourceList(15, 15), 0)
	gqm.updateGroupDeltaRequestNoLock("2", createResourceList(15, 15), createResourceList(15, 15), 0)

	// delete one group
	gqm.UpdateQuota(qi1, true)
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Nil(t, gqm.quotaInfoMap["1"])
}

type GroupQuotaManagerOption func(*GroupQuotaManager)

func WithCustomLimiter(customLimiters map[string]CustomLimiter) GroupQuotaManagerOption {
	return func(gqm *GroupQuotaManager) {
		gqm.customLimitersManager = NewCustomLimitersManager(customLimiters)
	}
}

func NewGroupQuotaManagerForTest(opts ...GroupQuotaManagerOption) *GroupQuotaManager {
	quotaManager := &GroupQuotaManager{
		totalResourceExceptSystemAndDefaultUsed: v1.ResourceList{},
		totalResource:                           v1.ResourceList{},
		resourceKeys:                            make(map[v1.ResourceName]struct{}),
		quotaInfoMap:                            make(map[string]*QuotaInfo),
		runtimeQuotaCalculatorMap:               make(map[string]*RuntimeQuotaCalculator),
		scaleMinQuotaManager:                    NewScaleMinQuotaManager(),
		quotaTopoNodeMap:                        make(map[string]*QuotaTopoNode),
	}
	for _, opt := range opts {
		opt(quotaManager)
	}
	systemQuotaInfo := NewQuotaInfo(false, true, extension.SystemQuotaName, extension.RootQuotaName)
	systemQuotaInfo.CalculateInfo.Max = v1.ResourceList{
		v1.ResourceCPU:    *resource.NewQuantity(math.MaxInt64/5, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity(math.MaxInt64/5, resource.BinarySI),
	}
	defaultQuotaInfo := NewQuotaInfo(false, true, extension.DefaultQuotaName, extension.RootQuotaName)
	defaultQuotaInfo.CalculateInfo.Max = v1.ResourceList{
		v1.ResourceCPU:    *resource.NewQuantity(math.MaxInt64/5, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity(math.MaxInt64/5, resource.BinarySI),
	}
	quotaManager.quotaInfoMap[extension.SystemQuotaName] = systemQuotaInfo
	quotaManager.quotaInfoMap[extension.DefaultQuotaName] = defaultQuotaInfo
	rootQuotaInfo := NewQuotaInfo(true, false, extension.RootQuotaName, "")
	quotaManager.quotaInfoMap[extension.RootQuotaName] = rootQuotaInfo
	quotaManager.quotaTopoNodeMap[extension.RootQuotaName] = NewQuotaTopoNode(extension.RootQuotaName, rootQuotaInfo)
	quotaManager.runtimeQuotaCalculatorMap[extension.RootQuotaName] = NewRuntimeQuotaCalculator(extension.RootQuotaName)
	return quotaManager
}

func BenchmarkGroupQuotaManager_RefreshRuntime(b *testing.B) {
	b.StopTimer()
	gqm := NewGroupQuotaManagerForTest()
	random := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < 2000; i++ {
		AddQuotaToManager2(gqm, fmt.Sprintf("%v", i), extension.RootQuotaName, 96, 160*GigaByte, 0, 0, true, false)
	}

	totalReqMem, totalReqCpu := float64(0), float64(0)
	for j := 0; j < 2000; j++ {
		reqMem := int64(random.Int() % 2000)
		reqCpu := int64(random.Int() % 2000)
		request := createResourceList(reqCpu, reqMem)
		totalReqMem += float64(reqMem)
		totalReqCpu += float64(reqCpu)
		gqm.updateGroupDeltaRequestNoLock(fmt.Sprintf("%v", j), request, request, 0)
	}
	totalRes := createResourceList(int64(totalReqCpu/1.5), int64(totalReqMem/1.5))
	gqm.UpdateClusterTotalResource(totalRes)
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		gqm.RefreshRuntime("0")
		gqm.getQuotaInfoByNameNoLock("0").RuntimeVersion = 0
	}
}

func AddQuotaToManager2(gqm *GroupQuotaManager, quotaName string, parent string,
	maxCpu, maxMem, minCpu, minMem int64, allowLentResource bool, isParent bool) *v1alpha1.ElasticQuota {
	quota := CreateQuota(quotaName, parent, maxCpu, maxMem, minCpu, minMem, allowLentResource, isParent)
	gqm.UpdateQuota(quota, false)
	return quota
}

func TestGroupQuotaManager_GetAllQuotaNames(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	qi2 := CreateQuota("2", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	quotaNames := gqm.GetAllQuotaNames()

	assert.NotNil(t, quotaNames["1"])
	assert.NotNil(t, quotaNames["2"])
}

func TestGroupQuotaManager_UpdatePodCache_UpdatePodIsAssigned_GetPodIsAssigned_UpdatePodRequest_UpdatePodUsed(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	qi2 := CreateQuota("2", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	gqm.updatePodCacheNoLock("1", pod1, true)
	assert.Equal(t, 1, len(gqm.GetQuotaInfoByName("1").GetPodCache()))
	gqm.updatePodCacheNoLock("1", pod1, false)
	assert.Equal(t, 0, len(gqm.GetQuotaInfoByName("1").GetPodCache()))
	gqm.updatePodCacheNoLock("2", pod1, true)
	assert.False(t, gqm.getPodIsAssignedNoLock("2", pod1))
	gqm.updatePodIsAssignedNoLock("2", pod1, true)
	assert.True(t, gqm.getPodIsAssignedNoLock("2", pod1))

	pod1.Labels = map[string]string{
		extension.LabelPreemptible: "false",
	}
	gqm.updatePodRequestNoLock("1", nil, pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetNonPreemptibleRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetSelfRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetSelfNonPreemptibleRequest())
	gqm.updatePodUsedNoLock("2", nil, pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetNonPreemptibleUsed())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetSelfUsed())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetSelfNonPreemptibleUsed())
	gqm.updatePodRequestNoLock("1", pod1, nil)
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetNonPreemptibleRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetSelfRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetSelfNonPreemptibleRequest())
	gqm.updatePodUsedNoLock("2", pod1, nil)
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("2").GetNonPreemptibleUsed())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("2").GetSelfUsed())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("2").GetSelfNonPreemptibleUsed())
}

func TestGroupQuotaManager_OnPodUpdate(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	qi2 := CreateQuota("2", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	// unscheduler pod
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	gqm.OnPodAdd(qi1.Name, pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetUsed())

	// scheduler the pod.
	pod2 := pod1.DeepCopy()
	pod2.Spec.NodeName = "node1"
	gqm.OnPodUpdate("1", "1", pod2, pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetUsed())

	// change the quota
	gqm.OnPodUpdate("2", "1", pod2, pod2)
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetUsed())

}

func TestGroupQuotaManager_OnPodUpdateAfterReserve(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)

	// unscheduler pod
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	gqm.OnPodAdd("1", pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetUsed())

	// reserve the pod.
	gqm.ReservePod("1", pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetUsed())

	// scheduler the pod.
	pod2 := pod1.DeepCopy()
	pod2.Spec.NodeName = "node1"
	gqm.OnPodUpdate("1", "1", pod2, pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetUsed())
}

func TestGroupQuotaManager_OnTerminatingPodUpdateAndDelete(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.ElasticQuotaIgnoreTerminatingPod, true)()
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)

	// unscheduler pod
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	gqm.OnPodAdd(qi1.Name, pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetUsed())

	// scheduler the pod.
	pod2 := pod1.DeepCopy()
	pod2.Spec.NodeName = "node1"
	gqm.OnPodUpdate("1", "1", pod2, pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetUsed())

	// deleting the pod.
	pod3 := pod2.DeepCopy()
	deleteTimestamp := metav1.Time{Time: time.Now().Add(-10 * time.Second)}
	deleteGracePeriodsSeconds := int64(30)
	pod3.DeletionTimestamp = &deleteTimestamp
	pod3.DeletionGracePeriodSeconds = &deleteGracePeriodsSeconds
	gqm.OnPodUpdate("1", "1", pod3, pod2)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetUsed())

	// deleting the pod again.
	pod4 := pod3.DeepCopy()
	deleteTimestamp = metav1.Time{Time: time.Now().Add(-40 * time.Second)}
	pod4.DeletionTimestamp = &deleteTimestamp
	pod4.DeletionGracePeriodSeconds = &deleteGracePeriodsSeconds
	gqm.OnPodUpdate("1", "1", pod4, pod3)
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetUsed())

	// delete the pod.
	gqm.OnPodDelete("1", pod4)
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetUsed())
}

func TestGroupQuotaManager_OnTerminatingPodAdd(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.ElasticQuotaIgnoreTerminatingPod, true)()
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)

	// add deleted pod
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	deleteTimestamp := metav1.Time{Time: time.Now().Add(-40 * time.Second)}
	deleteGracePeriodsSeconds := int64(30)
	pod1.Spec.NodeName = "node1"
	pod1.DeletionTimestamp = &deleteTimestamp
	pod1.DeletionGracePeriodSeconds = &deleteGracePeriodsSeconds
	gqm.OnPodAdd(qi1.Name, pod1)
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetUsed())
}

func TestNewGroupQuotaManager(t *testing.T) {
	gqm := NewGroupQuotaManager("", createResourceList(100, 100), createResourceList(300, 300), nil)
	assert.Equal(t, createResourceList(100, 100), gqm.GetQuotaInfoByName(extension.SystemQuotaName).GetMax())
	assert.Equal(t, createResourceList(300, 300), gqm.GetQuotaInfoByName(extension.DefaultQuotaName).GetMax())
	assert.True(t, gqm.scaleMinQuotaEnabled)
	gqm.UpdateClusterTotalResource(createResourceList(500, 500))
	assert.Equal(t, createResourceList(500, 500), gqm.GetClusterTotalResource())
}

func TestGetPodName(t *testing.T) {
	pod1 := schetesting.MakePod().Name("1").Obj()
	assert.Equal(t, pod1.Name, getPodName(pod1, nil))
	assert.Equal(t, pod1.Name, getPodName(nil, pod1))
}

func TestGroupQuotaManager_IsParent(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, true)
	qi2 := CreateQuota("2", "1", 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)

	qi1Info := gqm.GetQuotaInfoByName("1")
	assert.True(t, qi1Info.IsParent)

	gqm.UpdateQuota(qi2, false)

	qi1Info = gqm.GetQuotaInfoByName("1")
	assert.True(t, qi1Info.IsParent)

	qi2Info := gqm.GetQuotaInfoByName("2")
	assert.False(t, qi2Info.IsParent)

}

func TestGroupQuotaManager_UpdateRootQuotaUsed(t *testing.T) {
	expectedTotalUsed := createResourceList(0, 0)
	expectedTotalNonpreemptibleRequest := createResourceList(0, 0)
	gqm := NewGroupQuotaManagerForTest()
	gqm.UpdateClusterTotalResource(createResourceList(1000, 1000))

	sysUsed := createResourceList(10, 30)
	expectedTotalUsed = quotav1.Add(expectedTotalUsed, sysUsed)
	expectedTotalNonpreemptibleRequest = quotav1.Add(expectedTotalNonpreemptibleRequest, sysUsed)
	gqm.updateGroupDeltaUsedNoLock(extension.SystemQuotaName, sysUsed, createResourceList(0, 0), 0)
	gqm.updateGroupDeltaRequestNoLock(extension.SystemQuotaName, sysUsed, sysUsed, 0)
	assert.Equal(t, sysUsed, gqm.GetQuotaInfoByName(extension.SystemQuotaName).GetUsed())
	assert.Equal(t, sysUsed, gqm.GetQuotaInfoByName(extension.SystemQuotaName).GetNonPreemptibleRequest())

	defaultUsed := createResourceList(2, 5)
	expectedTotalUsed = quotav1.Add(expectedTotalUsed, defaultUsed)
	expectedTotalNonpreemptibleRequest = quotav1.Add(expectedTotalNonpreemptibleRequest, defaultUsed)
	gqm.updateGroupDeltaUsedNoLock(extension.DefaultQuotaName, defaultUsed, createResourceList(0, 0), 0)
	gqm.updateGroupDeltaRequestNoLock(extension.DefaultQuotaName, defaultUsed, defaultUsed, 0)
	assert.Equal(t, defaultUsed, gqm.GetQuotaInfoByName(extension.DefaultQuotaName).GetUsed())
	assert.Equal(t, defaultUsed, gqm.GetQuotaInfoByName(extension.DefaultQuotaName).GetNonPreemptibleRequest())

	//case1: no quota, root quota used
	rootQuotaUsed := gqm.getQuotaInfoByNameNoLock(extension.RootQuotaName).GetUsed()
	sysAndDefaultUsed := quotav1.Add(sysUsed, defaultUsed)
	assert.Equal(t, rootQuotaUsed, sysAndDefaultUsed)
	assert.Equal(t, gqm.GetQuotaInfoByName(extension.RootQuotaName).GetNonPreemptibleRequest(), sysAndDefaultUsed)

	//case2: just pod no quota, root quota used
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	pod1Used := pod1.Spec.Containers[0].Resources.Requests
	expectedTotalUsed = quotav1.Add(expectedTotalUsed, pod1Used)
	pod1.Labels = map[string]string{extension.LabelPreemptible: "false"}
	expectedTotalNonpreemptibleRequest = quotav1.Add(expectedTotalNonpreemptibleRequest, pod1Used)
	gqm.updatePodCacheNoLock(extension.DefaultQuotaName, pod1, true)
	assert.Equal(t, 1, len(gqm.GetQuotaInfoByName(extension.DefaultQuotaName).GetPodCache()))
	gqm.updatePodIsAssignedNoLock(extension.DefaultQuotaName, pod1, true)
	assert.True(t, gqm.getPodIsAssignedNoLock(extension.DefaultQuotaName, pod1))
	gqm.updatePodUsedNoLock(extension.DefaultQuotaName, nil, pod1)
	gqm.updatePodRequestNoLock(extension.DefaultQuotaName, nil, pod1)
	assert.Equal(t, expectedTotalUsed, gqm.GetQuotaInfoByName(extension.RootQuotaName).GetUsed())
	assert.Equal(t, expectedTotalNonpreemptibleRequest, gqm.GetQuotaInfoByName(extension.RootQuotaName).GetNonPreemptibleRequest())

	// case3: when build quota tree, root quota used
	qi1 := CreateQuota("1", extension.RootQuotaName, 20, 20, 10, 10, true, true)
	qi2 := CreateQuota("2", extension.RootQuotaName, 30, 30, 8, 6, true, false)
	qi3 := CreateQuota("3", "1", 30, 20, 8, 5, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	gqm.UpdateQuota(qi3, false)
	gqm.updateGroupDeltaUsedNoLock("2", createResourceList(5, 5), createResourceList(0, 0), 0)
	gqm.updateGroupDeltaUsedNoLock("3", createResourceList(7, 5), createResourceList(0, 0), 0)
	gqm.updateGroupDeltaRequestNoLock("2", createResourceList(0, 0), createResourceList(5, 5), 0)
	gqm.updateGroupDeltaRequestNoLock("3", createResourceList(0, 0), createResourceList(7, 5), 0)

	expectedTotalUsed = quotav1.Add(expectedTotalUsed, createResourceList(5, 5))
	expectedTotalUsed = quotav1.Add(expectedTotalUsed, createResourceList(7, 5))

	expectedTotalNonpreemptibleRequest = quotav1.Add(expectedTotalNonpreemptibleRequest, createResourceList(5, 5))
	expectedTotalNonpreemptibleRequest = quotav1.Add(expectedTotalNonpreemptibleRequest, createResourceList(7, 5))

	rootQuotaUsed = gqm.GetQuotaInfoByName(extension.RootQuotaName).GetUsed()
	assert.Equal(t, expectedTotalUsed, rootQuotaUsed)
	assert.Equal(t, expectedTotalNonpreemptibleRequest, gqm.GetQuotaInfoByName(extension.RootQuotaName).GetNonPreemptibleRequest())
}

func TestGroupQuotaManager_ChildRequestAndRequest_All_not_allowLent(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.UpdateClusterTotalResource(createResourceList(50, 50))
	// quota1 Max[40, 40]  Min[20,20] request[0,0]
	//   |-- quota2 Max[40, 40]  Min[10,10] request[0,0]
	//   |-- quota3 Max[40, 40]  Min[5,5] request[0,0]
	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 20, 20, false, true)
	qi2 := CreateQuota("2", "1", 40, 40, 10, 10, false, false)
	qi3 := CreateQuota("3", "1", 40, 40, 5, 5, false, false)
	gqm.UpdateQuota(qi1, false)
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetChildRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetRequest())
	gqm.UpdateQuota(qi2, false)
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetChildRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetRequest())
	gqm.UpdateQuota(qi3, false)
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("3").GetChildRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("3").GetRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("1").GetChildRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetRequest())
	// add pod1
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(5, 5),
			},
		},
	}
	gqm.OnPodAdd("2", pod1)
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("3").GetChildRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("3").GetRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("1").GetChildRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetRequest())
	// add pod2
	pod2 := schetesting.MakePod().Name("2").Obj()
	pod2.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	gqm.OnPodAdd("2", pod2)
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("3").GetChildRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("3").GetRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetChildRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetRequest())
	// add pod3
	pod3 := schetesting.MakePod().Name("3").Obj()
	pod3.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	gqm.OnPodAdd("2", pod3)
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("3").GetChildRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("3").GetRequest())
	assert.Equal(t, createResourceList(25, 25), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(25, 25), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(30, 30), gqm.GetQuotaInfoByName("1").GetChildRequest())
	assert.Equal(t, createResourceList(30, 30), gqm.GetQuotaInfoByName("1").GetRequest())
}

func TestGroupQuotaManager_UpdateRootQuotaRequest(t *testing.T) {
	expectedTotalRequest := createResourceList(0, 0)
	gqm := NewGroupQuotaManagerForTest()
	gqm.UpdateClusterTotalResource(createResourceList(1000, 1000))

	sysRequest := createResourceList(10, 30)
	expectedTotalRequest = quotav1.Add(expectedTotalRequest, sysRequest)
	gqm.updateGroupDeltaRequestNoLock(extension.SystemQuotaName, sysRequest, sysRequest, 0)
	assert.Equal(t, sysRequest, gqm.GetQuotaInfoByName(extension.SystemQuotaName).GetRequest())

	defaultRequest := createResourceList(2, 5)
	expectedTotalRequest = quotav1.Add(expectedTotalRequest, defaultRequest)
	gqm.updateGroupDeltaRequestNoLock(extension.DefaultQuotaName, defaultRequest, defaultRequest, 0)
	assert.Equal(t, defaultRequest, gqm.GetQuotaInfoByName(extension.DefaultQuotaName).GetRequest())

	//case1: no other quota, root quota request
	rootQuotaRequest := gqm.getQuotaInfoByNameNoLock(extension.RootQuotaName).GetRequest()
	sysAndDefaultRequest := quotav1.Add(sysRequest, defaultRequest)
	assert.Equal(t, rootQuotaRequest, sysAndDefaultRequest)

	//case2: just pod no quota, root quota request
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	pod1Request := pod1.Spec.Containers[0].Resources.Requests
	expectedTotalRequest = quotav1.Add(expectedTotalRequest, pod1Request)
	gqm.updatePodCacheNoLock(extension.DefaultQuotaName, pod1, true)
	assert.Equal(t, 1, len(gqm.GetQuotaInfoByName(extension.DefaultQuotaName).GetPodCache()))
	gqm.updatePodIsAssignedNoLock(extension.DefaultQuotaName, pod1, true)
	assert.True(t, gqm.getPodIsAssignedNoLock(extension.DefaultQuotaName, pod1))
	gqm.updatePodRequestNoLock(extension.DefaultQuotaName, nil, pod1)
	assert.Equal(t, expectedTotalRequest, gqm.GetQuotaInfoByName(extension.RootQuotaName).GetRequest())

	// case 3. when build quota tree, root quota request
	qi1 := CreateQuota("1", extension.RootQuotaName, 20, 20, 10, 10, true, true)
	qi2 := CreateQuota("2", extension.RootQuotaName, 30, 30, 8, 6, true, false)
	qi3 := CreateQuota("3", "1", 30, 20, 8, 5, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	gqm.UpdateQuota(qi3, false)
	gqm.updateGroupDeltaRequestNoLock("2", createResourceList(5, 5), createResourceList(5, 5), 0)
	gqm.updateGroupDeltaRequestNoLock("3", createResourceList(7, 5), createResourceList(7, 5), 0)

	expectedTotalRequest = quotav1.Add(expectedTotalRequest, createResourceList(5, 5))
	expectedTotalRequest = quotav1.Add(expectedTotalRequest, createResourceList(7, 5))

	rootQuotaRequest = gqm.GetQuotaInfoByName(extension.RootQuotaName).GetRequest()
	assert.Equal(t, expectedTotalRequest, rootQuotaRequest)
}

func TestGroupQuotaManager_RootQuotaRuntime(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.UpdateClusterTotalResource(createResourceList(1000, 1000))

	// case: test root quota runtime
	assert.Equal(t, gqm.RefreshRuntime(extension.RootQuotaName), gqm.totalResourceExceptSystemAndDefaultUsed.DeepCopy())
}

func TestGroupQuotaManager_OnQuotaResourceKeyUpdateForGuarantee(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.ElasticQuotaGuaranteeUsage, true)()
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	rl := createResourceList(50, 50)
	gqm.UpdateClusterTotalResource(rl)

	// quota1 Max[40, 40]  Min[20,20] request[0,0]
	//   |-- quota2 Max[40, 40]  Min[10,10] request[0,0]
	//   |-- quota3 Max[40, 40]  Min[5,5] request[0,0]
	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 20, 20, true, true)
	qi2 := CreateQuota("2", "1", 40, 40, 10, 10, true, false)
	qi3 := CreateQuota("3", "1", 40, 40, 5, 5, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	gqm.UpdateQuota(qi3, false)

	// add pod1
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(5, 5),
			},
		},
	}
	pod1.Spec.NodeName = "node1"
	gqm.OnPodAdd("2", pod1)
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetAllocated())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetGuaranteed())
	// check parent info
	// the parent should guarantee children min
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("1").GetAllocated())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetGuaranteed())

	// mock to standard delete resourceKeys
	// delete resourceKey cpu from bottom to top
	delete(qi1.Spec.Max, v1.ResourceCPU)
	delete(qi1.Spec.Min, v1.ResourceCPU)
	delete(qi2.Spec.Max, v1.ResourceCPU)
	delete(qi2.Spec.Min, v1.ResourceCPU)
	delete(qi3.Spec.Max, v1.ResourceCPU)
	delete(qi3.Spec.Min, v1.ResourceCPU)
	err := gqm.UpdateQuota(qi3, false)
	assert.NoError(t, err)
	err = gqm.UpdateQuota(qi2, false)
	assert.NoError(t, err)
	err = gqm.UpdateQuota(qi1, false)
	assert.NoError(t, err)
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(5, 10), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetAllocated())
	assert.Equal(t, createResourceList(5, 10), gqm.GetQuotaInfoByName("2").GetGuaranteed())
	// check parent info
	// the parent should guarantee children min, and keep resource requested already
	assert.Equal(t, createResourceList(5, 15), gqm.GetQuotaInfoByName("1").GetAllocated())
	assert.Equal(t, createResourceList(5, 20), gqm.GetQuotaInfoByName("1").GetGuaranteed())
}

func TestGroupQuotaManager_OnPodUpdateUsedForGuarantee(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.ElasticQuotaGuaranteeUsage, true)()
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	// quota1 Max[40, 40]  Min[20,20] request[0,0]
	//   |-- quota2 Max[40, 40]  Min[10,10] request[0,0]
	//   |-- quota3 Max[40, 40]  Min[5,5] request[0,0]
	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 20, 20, true, true)
	qi2 := CreateQuota("2", "1", 40, 40, 10, 10, true, false)
	qi3 := CreateQuota("3", "1", 40, 40, 5, 5, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	gqm.UpdateQuota(qi3, false)

	// add pod1
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(5, 5),
			},
		},
	}
	pod1.Spec.NodeName = "node1"
	gqm.OnPodAdd("2", pod1)
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetAllocated())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetGuaranteed())
	// check parent info
	// the parent should guarantee children min
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("1").GetAllocated())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetGuaranteed())

	// add pod2
	pod2 := schetesting.MakePod().Name("2").Obj()
	pod2.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	pod2.Spec.NodeName = "node1"
	gqm.OnPodAdd("2", pod2)
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("2").GetAllocated())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("2").GetGuaranteed())
	// check parent info
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetAllocated())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetGuaranteed())

	// add pod3
	pod3 := schetesting.MakePod().Name("3").Obj()
	pod3.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	pod3.Spec.NodeName = "node1"
	gqm.OnPodAdd("2", pod3)
	assert.Equal(t, createResourceList(25, 25), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(25, 25), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(25, 25), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, createResourceList(25, 25), gqm.GetQuotaInfoByName("2").GetAllocated())
	assert.Equal(t, createResourceList(25, 25), gqm.GetQuotaInfoByName("2").GetGuaranteed())
	// check parent info
	assert.Equal(t, createResourceList(30, 30), gqm.GetQuotaInfoByName("1").GetAllocated())
	assert.Equal(t, createResourceList(30, 30), gqm.GetQuotaInfoByName("1").GetGuaranteed())
}

func TestUpdateQuotaInternalNoLock(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate, features.ElasticQuotaGuaranteeUsage, true)()
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true
	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	// quota1 Max[40, 40]  Min[20,20] request[0,0]
	//   |-- quota2 Max[30, 20]  Min[10,10] request[0,0]
	//   |-- quota3 Max[20, 10]  Min[5,5] request[0,0]
	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 20, 20, true, true)
	qi2 := CreateQuota("2", "1", 30, 10, 10, 10, true, false)
	qi3 := CreateQuota("3", "1", 20, 10, 5, 5, true, false)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	gqm.UpdateQuota(qi3, false)

	// add pod1
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(5, 5),
			},
		},
	}
	pod1.Spec.NodeName = "node1"
	gqm.OnPodAdd("2", pod1)
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetAllocated())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("2").GetGuaranteed())

	// check parent info
	// the parent should guarantee children min
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("1").GetAllocated())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("1").GetGuaranteed())

	// update quota2 min
	// quota1 Max[40, 40]  Min[20,20] request[0,0]
	//   |-- quota2 Max[30, 20]  Min[20,20] request[0,0]
	//   |-- quota3 Max[20, 10]  Min[5,5] request[0,0]
	qi2.Spec.Min = createResourceList(20, 20)
	gqm.UpdateQuota(qi2, false)

	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetAllocated())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("2").GetGuaranteed())

	// check parent info
	// the parent should guarantee children min
	assert.Equal(t, createResourceList(25, 25), gqm.GetQuotaInfoByName("1").GetAllocated())
	assert.Equal(t, createResourceList(25, 25), gqm.GetQuotaInfoByName("1").GetGuaranteed())
}

func TestUpdateQuotaInternalNoLock_ParentsSelf(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true
	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	// quota1 Max[40, 40]  Min[20,20] request[0,0]
	//   |-- quota2 Max[30, 20]  Min[10,10] request[0,0]
	//   |-- quota3 Max[20, 10]  Min[5,5] request[0,0]
	eq1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 20, 20, true, true)
	eq2 := CreateQuota("2", "1", 30, 10, 10, 10, true, true)
	eq3 := CreateQuota("3", "1", 20, 10, 5, 5, true, false)
	eq4 := CreateQuota("4", "2", 30, 10, 10, 10, true, false)
	gqm.UpdateQuota(eq1, false)
	gqm.UpdateQuota(eq2, false)
	gqm.UpdateQuota(eq3, false)
	gqm.UpdateQuota(eq4, false)

	qi1, qi2, qi3, qi4 := gqm.GetQuotaInfoByName(eq1.Name), gqm.GetQuotaInfoByName(eq2.Name), gqm.GetQuotaInfoByName(eq3.Name), gqm.GetQuotaInfoByName(eq4.Name)

	qi2.CalculateInfo.SelfRequest = createResourceList(20, 20)
	qi3.CalculateInfo.ChildRequest = createResourceList(20, 20)
	qi4.CalculateInfo.ChildRequest = createResourceList(20, 20)
	qi2.CalculateInfo.SelfNonPreemptibleRequest = createResourceList(10, 10)
	qi3.CalculateInfo.NonPreemptibleRequest = createResourceList(5, 5)
	qi4.CalculateInfo.NonPreemptibleRequest = createResourceList(10, 10)

	gqm.resetQuotaNoLock()

	assert.Equal(t, createResourceList(10, 10), qi4.CalculateInfo.SelfNonPreemptibleRequest)
	assert.Equal(t, createResourceList(20, 20), qi4.CalculateInfo.SelfRequest)
	assert.Equal(t, createResourceList(5, 5), qi3.CalculateInfo.SelfNonPreemptibleRequest)
	assert.Equal(t, createResourceList(20, 20), qi3.CalculateInfo.SelfRequest)
	assert.Equal(t, createResourceList(10, 10), qi2.CalculateInfo.SelfNonPreemptibleRequest)
	assert.Equal(t, createResourceList(20, 20), qi2.CalculateInfo.SelfRequest)
	assert.Equal(t, v1.ResourceList{}, qi1.CalculateInfo.SelfNonPreemptibleRequest)
	assert.Equal(t, v1.ResourceList{}, qi1.CalculateInfo.SelfRequest)

	assert.Equal(t, createResourceList(10, 10), qi4.CalculateInfo.NonPreemptibleRequest)
	assert.Equal(t, createResourceList(20, 20), qi4.CalculateInfo.Request)
	assert.Equal(t, createResourceList(5, 5), qi3.CalculateInfo.NonPreemptibleRequest)
	assert.Equal(t, createResourceList(20, 20), qi3.CalculateInfo.Request)
	assert.Equal(t, createResourceList(20, 20), qi2.CalculateInfo.NonPreemptibleRequest)
	assert.Equal(t, createResourceList(40, 30), qi2.CalculateInfo.Request)
	assert.Equal(t, createResourceList(25, 25), qi1.CalculateInfo.NonPreemptibleRequest)
	assert.Equal(t, createResourceList(50, 20), qi1.CalculateInfo.Request)
}

func TestUpdatePod_WhenQuotaCreateAfterPodCreate(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true
	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	// create pod first
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	gqm.OnPodAdd("1", pod1)

	// create quota later
	eq1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 20, 20, true, false)
	gqm.UpdateQuota(eq1, false)

	quotaInfo := gqm.GetQuotaInfoByName(eq1.Name)
	assert.Equal(t, 0, len(quotaInfo.GetPodCache()))
	assert.Equal(t, v1.ResourceList{}, quotaInfo.GetRequest())
	assert.Equal(t, v1.ResourceList{}, quotaInfo.GetUsed())

	// update pod.
	newPod1 := pod1.DeepCopy()
	newPod1.Labels = map[string]string{"aaa": "aaa"}

	gqm.OnPodUpdate("1", "1", newPod1, pod1)

	quotaInfo = gqm.GetQuotaInfoByName(eq1.Name)
	assert.Equal(t, 1, len(quotaInfo.GetPodCache()))
	assert.Equal(t, createResourceList(10, 10), quotaInfo.GetRequest())
	assert.Equal(t, v1.ResourceList{}, quotaInfo.GetUsed())

	// update again
	newPod2 := newPod1.DeepCopy()
	newPod2.Spec.NodeName = "node1"
	gqm.OnPodUpdate("1", "1", newPod2, newPod1)
	quotaInfo = gqm.GetQuotaInfoByName(eq1.Name)
	assert.Equal(t, 1, len(quotaInfo.GetPodCache()))
	assert.Equal(t, createResourceList(10, 10), quotaInfo.GetRequest())
	assert.Equal(t, createResourceList(10, 10), quotaInfo.GetUsed())
}

func TestGroupQuotaManager_ImmediateIgnoreTerminatingPod(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultFeatureGate,
		features.ElasticQuotaImmediateIgnoreTerminatingPod, true)()
	gqm := NewGroupQuotaManagerForTest()

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	gqm.UpdateQuota(qi1, false)

	// add pod
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	pod1.Spec.NodeName = "node1"
	gqm.OnPodAdd(qi1.Name, pod1)
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(10, 10), gqm.GetQuotaInfoByName("1").GetUsed())

	// deleting the pod
	pod2 := pod1.DeepCopy()
	deleteTimestamp := metav1.Time{Time: time.Now()}
	deleteGracePeriodsSeconds := int64(30)
	pod2.DeletionTimestamp = &deleteTimestamp
	pod2.DeletionGracePeriodSeconds = &deleteGracePeriodsSeconds
	gqm.OnPodUpdate("1", "1", pod2, pod1)
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetUsed())

	// delete the pod
	gqm.OnPodDelete("1", pod2)
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetUsed())
}

func TestGroupQuotaManager_UpdateQuotaInternalNoLock(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.UpdateClusterTotalResource(createResourceList(1000, 1000))
	assert.Equal(t, 1, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 3, len(gqm.quotaInfoMap))

	quota := CreateQuota("test1", extension.RootQuotaName, 64, 100, 50, 80, true, false)
	quotaInfo := NewQuotaInfoFromQuota(quota)

	// quota not exist, add quota info
	// rootQuota requests[0,0]
	//   |-- test1 Max[64, 100]  Min[50,80] request[0,0]
	gqm.updateQuotaInternalNoLock(quotaInfo, nil)
	assert.Equal(t, 2, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 4, len(gqm.quotaInfoMap))

	quotaInfo = gqm.getQuotaInfoByNameNoLock("test1")
	assert.True(t, quotaInfo != nil)
	assert.False(t, quotaInfo.IsParent)
	assert.True(t, quotaInfo.AllowLentResource)
	assert.Equal(t, createResourceList(64, 100), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, createResourceList(50, 80), quotaInfo.CalculateInfo.Min)
	assert.Equal(t, createResourceList(50, 80), quotaInfo.CalculateInfo.AutoScaleMin)
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.ChildRequest)
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.Request)

	rootQuotaInfo := gqm.getQuotaInfoByNameNoLock(extension.RootQuotaName)
	assert.Equal(t, v1.ResourceList{}, rootQuotaInfo.CalculateInfo.Request)

	// add requests
	// rootQuota requests[64,100]
	//   |-- test1 Max[64, 100]  Min[50,80] request[100,100]
	request := createResourceList(100, 100)
	gqm.updateGroupDeltaRequestNoLock("test1", request, request, 0)
	quotaInfo = gqm.getQuotaInfoByNameNoLock("test1")
	assert.Equal(t, createResourceList(100, 100), quotaInfo.CalculateInfo.ChildRequest)
	assert.Equal(t, createResourceList(100, 100), quotaInfo.CalculateInfo.Request)

	rootQuotaInfo = gqm.getQuotaInfoByNameNoLock(extension.RootQuotaName)
	assert.Equal(t, createResourceList(64, 100), rootQuotaInfo.CalculateInfo.Request)

	// change test1 min and max
	// rootQuota requests[100,100]
	//   |-- test1 Max[200, 200]  Min[60,100] request[100,100]
	quota = CreateQuota("test1", extension.RootQuotaName, 200, 200, 60, 100, true, false)
	newQuotaInfo := NewQuotaInfoFromQuota(quota)
	gqm.updateQuotaInternalNoLock(newQuotaInfo, quotaInfo)
	quotaInfo = gqm.getQuotaInfoByNameNoLock("test1")
	assert.Equal(t, createResourceList(200, 200), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, createResourceList(60, 100), quotaInfo.CalculateInfo.Min)
	assert.Equal(t, createResourceList(60, 100), quotaInfo.CalculateInfo.AutoScaleMin)
	assert.Equal(t, createResourceList(100, 100), quotaInfo.CalculateInfo.ChildRequest)
	assert.Equal(t, createResourceList(100, 100), quotaInfo.CalculateInfo.Request)

	rootQuotaInfo = gqm.getQuotaInfoByNameNoLock(extension.RootQuotaName)
	assert.Equal(t, createResourceList(100, 100), rootQuotaInfo.CalculateInfo.Request)
}

func TestGroupQuotaManager_UpdateQuotaTopoNodeNoLock(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()

	test1 := NewQuotaInfo(true, true, "test1", extension.RootQuotaName)
	test2 := NewQuotaInfo(true, true, "test2", extension.RootQuotaName)
	test11 := NewQuotaInfo(false, true, "test11", "test1")

	// add test1
	gqm.updateQuotaTopoNodeNoLock(test1, nil)
	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap[extension.RootQuotaName].childGroupQuotaInfos))

	// add test2
	gqm.updateQuotaTopoNodeNoLock(test2, nil)
	assert.Equal(t, 3, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap[extension.RootQuotaName].childGroupQuotaInfos))

	// add test11
	gqm.updateQuotaTopoNodeNoLock(test11, nil)
	assert.Equal(t, 4, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap[extension.RootQuotaName].childGroupQuotaInfos))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap["test1"].childGroupQuotaInfos))
	assert.Equal(t, 0, len(gqm.quotaTopoNodeMap["test2"].childGroupQuotaInfos))

	// change test11 parent to test2
	test12 := NewQuotaInfo(false, true, "test11", "test2")
	gqm.updateQuotaTopoNodeNoLock(test12, test11)
	assert.Equal(t, 4, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap[extension.RootQuotaName].childGroupQuotaInfos))
	assert.Equal(t, 0, len(gqm.quotaTopoNodeMap["test1"].childGroupQuotaInfos))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap["test2"].childGroupQuotaInfos))
}

func TestGroupQuotaManager_UpdateQuotaNoLockWhenParentChange(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest()
	gqm.scaleMinQuotaEnabled = true
	gqm.UpdateClusterTotalResource(createResourceList(200, 200))

	// quota1 Max[100, 100]  Min[80,80] request[0,0]
	//   |-- quota11 Max[50, 50]  Min[30,30] request[0,0]
	//       |-- quota111 Max[50, 50]  Min[20,20] request[0,0]
	// quota2 Max[100, 100]  Min[60,60] request[0,0]
	//   |-- quota21 Max[50, 50]  Min[40,40] request[0,0]
	//       |-- quota211 Max[50, 50]  Min[15,15] request[0,0]
	eq1 := CreateQuota("1", extension.RootQuotaName, 100, 100, 80, 80, true, true)
	eq2 := CreateQuota("2", extension.RootQuotaName, 100, 100, 60, 60, true, true)
	eq11 := CreateQuota("11", "1", 50, 50, 30, 30, true, true)
	eq21 := CreateQuota("21", "2", 50, 50, 40, 40, true, true)
	eq111 := CreateQuota("111", "11", 50, 50, 20, 20, true, true)
	eq211 := CreateQuota("211", "21", 50, 50, 15, 15, true, true)
	gqm.UpdateQuota(eq1, false)
	gqm.UpdateQuota(eq2, false)
	gqm.UpdateQuota(eq11, false)
	gqm.UpdateQuota(eq21, false)
	gqm.UpdateQuota(eq111, false)
	gqm.UpdateQuota(eq211, false)

	// add pod1 to eq111
	pod1 := schetesting.MakePod().Name("1").Obj()
	pod1.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(10, 10),
			},
		},
	}
	pod1.Spec.NodeName = "node1"
	gqm.OnPodAdd(eq111.Name, pod1)

	// add pod2 to eq111, pod2 is NonPreemptible
	pod2 := schetesting.MakePod().Name("2").Label(extension.LabelPreemptible, "false").Obj()
	pod2.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(5, 5),
			},
		},
	}
	pod2.Spec.NodeName = "node1"
	gqm.OnPodAdd(eq111.Name, pod2)

	// add pod3 to eq11. the eq11 is parent quota
	pod3 := schetesting.MakePod().Name("3").Obj()
	pod3.Spec.Containers = []v1.Container{
		{
			Resources: v1.ResourceRequirements{
				Requests: createResourceList(20, 20),
			},
		},
	}
	pod3.Spec.NodeName = "node3"
	gqm.OnPodAdd(eq11.Name, pod3)

	gqm.RefreshRuntime(eq111.Name)
	gqm.RefreshRuntime(eq211.Name)

	// quota1 Max[100, 100]  Min[80,80] request[35,35] selfRequest[0, 0]
	//   |-- quota11 Max[50, 50]  Min[30,30] request[35,35] selfRequest[20,20]
	//       |-- quota111 Max[50, 50]  Min[20,20] request[15,15] selfRequest[15,15]
	// quota2 Max[100, 100]  Min[100,100] request[0,0]
	//   |-- quota21 Max[50, 50]  Min[40,40] request[0,0]
	//       |-- quota211 Max[50, 50]  Min[15,15] request[0,0]
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetChildRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetSelfRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetNonPreemptibleRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetUsed())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetSelfUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetNonPreemptibleUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetRuntime())

	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("11").GetRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("11").GetChildRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetSelfRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("11").GetNonPreemptibleRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("11").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("11").GetUsed())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetSelfUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("11").GetNonPreemptibleUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("11").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("11").GetRuntime())

	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("1").GetChildRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetSelfRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("1").GetNonPreemptibleRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("1").GetUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetSelfUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("1").GetNonPreemptibleUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("1").GetRuntime())

	assert.True(t, gqm.GetQuotaInfoByName("111").IsPodExist(pod1))
	assert.True(t, gqm.GetQuotaInfoByName("111").IsPodExist(pod2))
	assert.True(t, gqm.GetQuotaInfoByName("11").IsPodExist(pod3))

	assert.Equal(t, 7, len(gqm.quotaTopoNodeMap))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap["2"].childGroupQuotaInfos))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap["1"].childGroupQuotaInfos))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap["11"].childGroupQuotaInfos))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap["21"].childGroupQuotaInfos))

	// move eq11 to eq2
	eq11.Labels[extension.LabelQuotaParent] = eq2.Name
	gqm.UpdateQuota(eq11, false)

	gqm.RefreshRuntime(eq1.Name)
	gqm.RefreshRuntime(eq111.Name)
	gqm.RefreshRuntime(eq211.Name)

	// quota1 Max[100, 100]  Min[80,80] request[0,0] selfRequest[0, 0]
	// quota2 Max[100, 100]  Min[60,60] request[35,35]
	//   |-- quota21 Max[50, 50]  Min[20,20] request[0,0]
	//       |-- quota211 Max[50, 50]  Min[15,15] request[0,0]
	//   |-- quota11 Max[50, 50]  Min[30,30] request[35,35] selfRequest[20,20]
	//       |-- quota111 Max[50, 50]  Min[20,20] request[15,15] selfRequest[15,15]
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetChildRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetSelfRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetNonPreemptibleRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetUsed())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetSelfUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetNonPreemptibleUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetRuntime())

	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("11").GetRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("11").GetChildRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetSelfRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("11").GetNonPreemptibleRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("11").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("11").GetUsed())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetSelfUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("11").GetNonPreemptibleUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("11").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("11").GetRuntime())

	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetSelfRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetNonPreemptibleRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetSelfUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetNonPreemptibleUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("2").GetRuntime())

	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetChildRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetSelfRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetNonPreemptibleRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetSelfUsed())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetNonPreemptibleUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("1").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("1").GetRuntime())

	assert.True(t, gqm.GetQuotaInfoByName("111").IsPodExist(pod1))
	assert.True(t, gqm.GetQuotaInfoByName("111").IsPodExist(pod2))
	assert.True(t, gqm.GetQuotaInfoByName("11").IsPodExist(pod3))

	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap["2"].childGroupQuotaInfos))
	assert.Equal(t, 0, len(gqm.quotaTopoNodeMap["1"].childGroupQuotaInfos))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap["11"].childGroupQuotaInfos))
	assert.Equal(t, 1, len(gqm.quotaTopoNodeMap["21"].childGroupQuotaInfos))

	// move eq111 to eq21
	eq111.Labels[extension.LabelQuotaParent] = eq21.Name
	gqm.UpdateQuota(eq111, false)

	gqm.RefreshRuntime(eq11.Name)
	gqm.RefreshRuntime(eq111.Name)
	gqm.RefreshRuntime(eq211.Name)

	// quota1 Max[100, 100]  Min[80,80] request[0,0] selfRequest[0, 0]
	// quota2 Max[100, 100]  Min[60,60] request[35,35]
	//   |-- quota21 Max[50, 50]  Min[40,40] request[15,15]
	//       |-- quota211 Max[50, 50]  Min[15,15] request[0,0]
	//       |-- quota111 Max[50, 50]  Min[20,20] request[15,15] selfRequest[15,15]
	//   |-- quota11 Max[50, 50]  Min[30,30] request[20,20] selfRequest[20,20]

	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetChildRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetSelfRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetNonPreemptibleRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetUsed())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetSelfUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetNonPreemptibleUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("111").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("111").GetRuntime())

	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetChildRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetSelfRequest())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("11").GetNonPreemptibleRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("11").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetUsed())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetSelfUsed())
	assert.Equal(t, createResourceList(0, 0), gqm.GetQuotaInfoByName("11").GetNonPreemptibleUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("11").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(20, 20), gqm.GetQuotaInfoByName("11").GetRuntime())

	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("2").GetRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("2").GetChildRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetSelfRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetNonPreemptibleRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("2").GetUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetSelfUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("2").GetNonPreemptibleUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("2").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(35, 35), gqm.GetQuotaInfoByName("2").GetRuntime())

	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("21").GetRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("21").GetChildRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("21").GetSelfRequest())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("21").GetNonPreemptibleRequest())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("21").GetSelfNonPreemptibleRequest())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("21").GetUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("21").GetSelfUsed())
	assert.Equal(t, createResourceList(5, 5), gqm.GetQuotaInfoByName("21").GetNonPreemptibleUsed())
	assert.Equal(t, v1.ResourceList{}, gqm.GetQuotaInfoByName("21").GetSelfNonPreemptibleUsed())
	assert.Equal(t, createResourceList(15, 15), gqm.GetQuotaInfoByName("21").GetRuntime())

	assert.True(t, gqm.GetQuotaInfoByName("111").IsPodExist(pod1))
	assert.True(t, gqm.GetQuotaInfoByName("111").IsPodExist(pod2))
	assert.True(t, gqm.GetQuotaInfoByName("11").IsPodExist(pod3))

	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap["2"].childGroupQuotaInfos))
	assert.Equal(t, 0, len(gqm.quotaTopoNodeMap["1"].childGroupQuotaInfos))
	assert.Equal(t, 0, len(gqm.quotaTopoNodeMap["11"].childGroupQuotaInfos))
	assert.Equal(t, 2, len(gqm.quotaTopoNodeMap["21"].childGroupQuotaInfos))
}

func TestGroupQuotaManager_MockCustomLimiter_ConfUpdate(t *testing.T) {
	gqm := NewGroupQuotaManagerForTest(WithCustomLimiter(map[string]CustomLimiter{
		CustomKeyMock: &MockCustomLimiter{
			key: CustomKeyMock,
		},
	}))
	annotationKeyLimit := fmt.Sprintf(AnnotationKeyMockLimitFmt, CustomKeyMock)
	annotationKeyArgs := fmt.Sprintf(AnnotationKeyMockArgsFmt, CustomKeyMock)
	/*
	 * invalid updates
	 */
	// invalid limit
	q1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 20, 20, false, false)
	q1.Annotations[annotationKeyLimit] = `{`
	err := gqm.UpdateQuota(q1, false)
	assert.NoError(t, err)
	q1Info := gqm.GetQuotaInfoByName(q1.Name)
	assert.Equal(t, 0, len(q1Info.CalculateInfo.CustomLimits))

	// invalid args
	q1.Annotations[annotationKeyLimit] = `{"cpu":20,"memory":20}`
	q1.Annotations[annotationKeyArgs] = `invalid`
	err = gqm.UpdateQuota(q1, false)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(q1Info.CalculateInfo.CustomLimits))

	// required argument with invalid format
	q1.Annotations[annotationKeyLimit] = `{"cpu":20,"memory":20}`
	q1.Annotations[annotationKeyArgs] = `{"debugEnabled":"true"}`
	err = gqm.UpdateQuota(q1, false)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(q1Info.CalculateInfo.CustomLimits))

	/*
	 * valid updates
	 */
	q1.Annotations[annotationKeyLimit] = `{"cpu":20,"memory":20}`
	q1.Annotations[annotationKeyArgs] = `{"debugEnabled":true}`
	err = gqm.UpdateQuota(q1, false)
	assert.NoError(t, err)
	assert.Equal(t, len(q1Info.CalculateInfo.CustomLimits), 1)
	AssertCustomLimitEqual(t, createResourceList(20, 20), CustomKeyMock, q1Info)

	args, ok := q1Info.GetCustomArgs(CustomKeyMock).(*MockCustomArgs)
	assert.True(t, ok)
	assert.True(t, args.DebugEnabled)

	// update limit
	q1.Annotations[annotationKeyLimit] = `{"cpu":20,"memory":50}`
	err = gqm.UpdateQuota(q1, false)
	assert.NoError(t, err)
	assert.Equal(t, len(q1Info.CalculateInfo.CustomLimits), 1)
	AssertCustomLimitEqual(t, createResourceList(20, 50), CustomKeyMock, q1Info)

	// update args
	q1.Annotations[annotationKeyArgs] = `{"debugEnabled":false}`
	err = gqm.UpdateQuota(q1, false)
	assert.NoError(t, err)
	args, ok = q1Info.GetCustomArgs(CustomKeyMock).(*MockCustomArgs)
	assert.True(t, ok)
	assert.False(t, args.DebugEnabled)

	// remove limit, args should be ignored if limit is not configured
	delete(q1.Annotations, annotationKeyLimit)
	err = gqm.UpdateQuota(q1, false)
	assert.NoError(t, err)
	assert.Equal(t, len(q1Info.CalculateInfo.CustomLimits), 0)
}

func TestGroupQuotaManager_CustomLimiter_PodUpdate(t *testing.T) {
	testCustomKey := CustomKeyMock
	customLimiter, err := NewMockLimiter(testCustomKey, `{"labelSelector":"app=test"}`)
	assert.NoError(t, err)
	gqm := NewGroupQuotaManagerForTest(WithCustomLimiter(map[string]CustomLimiter{
		testCustomKey: customLimiter,
	}))
	annoKeyLimitConf, annoKeyArgs := fmt.Sprintf(AnnotationKeyMockLimitFmt, testCustomKey),
		fmt.Sprintf(AnnotationKeyMockArgsFmt, testCustomKey)
	limitResList := createResourceList(15, 15)

	// create test quotas
	testQuotas, testQuotaMap := GetTestQuotasForCustomLimiters()
	err = UpdateQuotas(gqm, func(quotasMap map[string]*v1alpha1.ElasticQuota) {
		quotasMap["12"].Annotations[annoKeyLimitConf] = `{"cpu":15,"memory":15}`
		quotasMap["12"].Annotations[annoKeyArgs] = `{"debugEnabled":true}`
		quotasMap["2"].Annotations[annoKeyLimitConf] = `{"cpu":15,"memory":15}`
		quotasMap["2"].Annotations[annoKeyArgs] = `{"debugEnabled":true}`
		quotasMap["211"].Annotations[annoKeyLimitConf] = `{"cpu":15,"memory":15}`
		quotasMap["211"].Annotations[annoKeyArgs] = `{"debugEnabled":true}`
	}, testQuotas...)
	assert.NoError(t, err)

	q111, q121, q122, q211 := testQuotaMap["111"], testQuotaMap["121"], testQuotaMap["122"], testQuotaMap["211"]

	// add unmatched pod1
	pod1 := schetesting.MakePod().Name("1").Label(extension.LabelQuotaName, q121.Name).Containers(
		[]v1.Container{schetesting.MakeContainer().Name("0").Resources(map[v1.ResourceName]string{
			v1.ResourceCPU: "2", v1.ResourceMemory: "8", "test": "1"}).Obj()}).Node("node0").Obj()
	gqm.OnPodAdd(q111.Name, pod1)

	// expected quotas
	// quota1									mock-used[]
	//   |-- quota11
	//       |-- quota111
	//   |-- quota12		mock-limit[15,15] 	mock-used[]
	//       |-- quota121						mock-used[]
	//       |-- quota122						mock-used[]
	// quota2				mock-limit[15,15]	mock-used[]
	//   |-- quota21							mock-used[]
	//       |-- quota211   mock-limit[15,15]	mock-used[]
	limitQuotaNames := []string{"12", "2", "211"}
	enabledQuotaNames := []string{"1", "12", "121", "122", "2", "21", "211"}
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, enabledQuotaNames...)...)

	// update pod1: unmatched --> matched
	newPod1 := pod1.DeepCopy()
	newPod1.Labels["app"] = "test"
	gqm.OnPodUpdate(q121.Name, q121.Name, newPod1, pod1)

	// expected quotas
	// quota1									mock-used[2,8] (update: +mock-used[2,8])
	//   |-- quota11
	//       |-- quota111
	//   |-- quota12		mock-limit[15,15] 	mock-used[2,8] (update: +mock-used[2,8])
	//       |-- quota121						mock-used[2,8] (update: +mock-used[2,8])
	//       |-- quota122						mock-used[]
	// quota2				mock-limit[15,15]	mock-used[]
	//   |-- quota21							mock-used[]
	//       |-- quota211   mock-limit[15,15]	mock-used[]

	// no changes on custom-limiter enabled state and custom-limit conf
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "122", "2", "21", "211")...)
	// check custom-used updated for quota chain: q121 -> q12 -> q1
	AssertCustomUsedEqual(t, createResourceList(2, 8), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1", "12", "121")...)

	// update pod1 resource: <cpu:2, memory:8> --> <cpu:5, memory:5>
	pod1, newPod1 = newPod1, newPod1.DeepCopy()
	newPod1.Spec.Containers[0].Resources.Requests = map[v1.ResourceName]resource.Quantity{
		v1.ResourceCPU: resource.MustParse("5"), v1.ResourceMemory: resource.MustParse("5")}
	gqm.OnPodUpdate(q121.Name, q121.Name, newPod1, pod1)

	// check custom-used updated for quota chain: q121 -> q12 -> q1
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1", "12", "121")...)

	// update pod1 quota-name label: q121 --> q211
	pod1, newPod1 = newPod1, newPod1.DeepCopy()
	newPod1.Labels[extension.LabelQuotaName] = q211.Name
	gqm.OnPodUpdate(q211.Name, q121.Name, newPod1, pod1)

	// expected quotas
	// quota1									mock-used[0,0] (update: -mock-used[5,5])
	//   |-- quota11
	//       |-- quota111
	//   |-- quota12		mock-limit[15,15] 	mock-used[0,0] (update: -mock-used[5,5])
	//       |-- quota121						mock-used[0,0] (update: -mock-used[5,5])
	//       |-- quota122						mock-used[]
	// quota2				mock-limit[15,15]	mock-used[5,5] (update: +mock-used[5,5])
	//   |-- quota21							mock-used[5,5] (update: +mock-used[5,5])
	//       |-- quota211   mock-limit[15,15]	mock-used[5,5] (update: +mock-used[5,5])
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "122")...)
	AssertCustomUsedEqual(t, createResourceList(0, 0), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1", "12", "121")...)
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "2", "21", "211")...)

	// add matched pod to q122
	pod2 := schetesting.MakePod().Name("2").Label(extension.LabelQuotaName, q111.Name).Containers(
		[]v1.Container{schetesting.MakeContainer().Name("0").Resources(map[v1.ResourceName]string{
			v1.ResourceCPU: "1", v1.ResourceMemory: "2"}).Obj()}).Node("node0").Obj()
	pod2.Labels["app"] = "test"
	gqm.OnPodAdd(q122.Name, pod2)

	// expected quotas
	// quota1									mock-used[1,2] (update: +mock-used[1,2])
	//   |-- quota11
	//       |-- quota111
	//   |-- quota12		mock-limit[15,15] 	mock-used[1,2] (update: +mock-used[1,2])
	//       |-- quota121						mock-used[0,0]
	//       |-- quota122						mock-used[1,2] (update: +mock-used[1,2])
	// quota2				mock-limit[15,15]	mock-used[5,5]
	//   |-- quota21							mock-used[5,5]
	//       |-- quota211   mock-limit[15,15]	mock-used[5,5]
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, createResourceList(0, 0), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "121")...)
	AssertCustomUsedEqual(t, createResourceList(1, 2), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1", "12", "122")...)
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "2", "21", "211")...)

	// update pod1: matched --> unmatched
	pod1 = newPod1
	newPod1 = newPod1.DeepCopy()
	delete(newPod1.Labels, "app")
	gqm.OnPodUpdate(q211.Name, q211.Name, newPod1, pod1)

	// expected quotas
	// quota1									mock-used[1,2]
	//   |-- quota11
	//       |-- quota111
	//   |-- quota12		mock-limit[15,15] 	mock-used[1,2]
	//       |-- quota121						mock-used[0,0]
	//       |-- quota122						mock-used[1,2]
	// quota2				mock-limit[15,15]	mock-used[0,0] (update: -mock-used[5,5])
	//   |-- quota21							mock-used[0,0] (update: -mock-used[5,5])
	//       |-- quota211   mock-limit[15,15]	mock-used[0,0] (update: -mock-used[5,5])
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, createResourceList(0, 0), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "121", "2", "21", "211")...)
	AssertCustomUsedEqual(t, createResourceList(1, 2), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1", "12", "122")...)

	// delete pod2
	gqm.OnPodDelete(q122.Name, pod2)

	// expected quotas
	// quota1									mock-used[0,0] (update: -mock-used[1,2])
	//   |-- quota11
	//       |-- quota111
	//   |-- quota12		mock-limit[15,15] 	mock-used[0,0] (update: -mock-used[1,2])
	//       |-- quota121						mock-used[0,0]
	//       |-- quota122						mock-used[0,0] (update: -mock-used[1,2])
	// quota2				mock-limit[15,15]	mock-used[0,0]
	//   |-- quota21							mock-used[0,0]
	//       |-- quota211   mock-limit[15,15]	mock-used[0,0]
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, createResourceList(0, 0), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1", "12", "121", "122", "2", "21", "211")...)
}

func TestGroupQuotaManager_CustomLimiter_QuotaUpdate(t *testing.T) {
	testCustomKey := CustomKeyMock
	customLimiter, err := NewMockLimiter(testCustomKey, `{"labelSelector":"app=test"}`)
	assert.NoError(t, err)
	gqm := NewGroupQuotaManagerForTest(WithCustomLimiter(map[string]CustomLimiter{
		testCustomKey: customLimiter,
	}))
	annoKeyLimitConf, annoKeyArgs := fmt.Sprintf(AnnotationKeyMockLimitFmt, testCustomKey),
		fmt.Sprintf(AnnotationKeyMockArgsFmt, testCustomKey)
	limitConfV := `{"cpu":20,"memory":20}`
	limitResList := createResourceList(20, 20)

	// create test quotas
	testQuotas, testQuotaMap := GetTestQuotasForCustomLimiters()
	err = UpdateQuotas(gqm, nil, testQuotas...)
	assert.NoError(t, err)

	q1, q11, q111, q121, q2, q21, q211 := testQuotaMap["1"], testQuotaMap["11"], testQuotaMap["111"],
		testQuotaMap["121"], testQuotaMap["2"], testQuotaMap["21"], testQuotaMap["211"]
	q11Info := gqm.getQuotaInfoByNameNoLock(q11.Name)

	// add matched pod with quota=q111, no custom-used
	pod1 := schetesting.MakePod().Name("1").Label(extension.LabelQuotaName, q111.Name).Label(
		"app", "test").Containers([]v1.Container{schetesting.MakeContainer().Name("0").Resources(
		map[v1.ResourceName]string{v1.ResourceCPU: "5", v1.ResourceMemory: "5"}).Obj()}).Node("node0").Obj()
	gqm.OnPodAdd(q111.Name, pod1)

	// check no quotas with enabled custom-limiter and custom-limit conf
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList, nil, nil)

	// add custom limit for q11, rebuild and have custom-used
	q11.Annotations[annoKeyLimitConf] = limitConfV
	q11.Annotations[annoKeyArgs] = `{"debugEnabled":true}`
	err = gqm.UpdateQuota(q11, false)
	assert.NoError(t, err)

	// expected quotas
	// quota1     								mock-used[5,5] (updated: mock-used +[5,5])
	//   |-- quota11		mock-limit[20,20]  	mock-used[5,5] (updated: mock-used +[5,5])
	//       |-- quota111						mock-used[5,5] (updated: mock-used +[5,5])
	//   |-- quota12
	//       |-- quota121
	//       |-- quota122
	// quota2
	//   |-- quota21
	//       |-- quota211
	limitQuotaNames := []string{"11"}
	enabledQuotaNames := []string{"1", "11", "111"}
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, enabledQuotaNames...)...)

	customArgs := q11Info.GetCustomArgs(testCustomKey)
	assert.NotNil(t, customArgs)
	assert.True(t, customArgs.(*MockCustomArgs).DebugEnabled)

	// add custom limit for q211
	q211.Annotations[annoKeyLimitConf] = limitConfV
	err = gqm.UpdateQuota(q211, false)
	assert.NoError(t, err)

	// expected quotas
	// quota1     								mock-used[5,5]
	//   |-- quota11		mock-limit[20,20]  	mock-used[5,5]
	//       |-- quota111						mock-used[5,5]
	//   |-- quota12
	//       |-- quota121
	//       |-- quota122
	// quota2									mock-used[0,0] (updated: mock-used +[0,0])
	//   |-- quota21							mock-used[0,0] (updated: mock-used +[0,0])
	//       |-- quota211 	mock-limit[15,15]  	mock-used[0,0] (updated: mock-used +[0,0])
	limitQuotaNames = []string{"11", "211"}
	enabledQuotaNames = []string{"1", "11", "111", "2", "21", "211"}
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1", "11", "111")...)
	AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "2", "21", "211")...)

	// add matched pod with quota=211
	pod2 := schetesting.MakePod().Name("2").Label(extension.LabelQuotaName, q211.Name).Label(
		"app", "test").Containers([]v1.Container{schetesting.MakeContainer().Name("0").Resources(
		map[v1.ResourceName]string{v1.ResourceCPU: "12", v1.ResourceMemory: "12"}).Obj()}).Node("node0").Obj()
	gqm.OnPodAdd(q211.Name, pod2)

	// expected quotas
	// quota1     								mock-used[5,5]
	//   |-- quota11		mock-limit[20,20]  	mock-used[5,5]
	//       |-- quota111						mock-used[5,5]
	//   |-- quota12
	//       |-- quota121
	//       |-- quota122
	// quota2									mock-used[12,12] (updated: mock-used +[12,12])
	//   |-- quota21							mock-used[12,12] (updated: mock-used +[12,12])
	//       |-- quota211 	mock-limit[15,15]  	mock-used[12,12] (updated: mock-used +[12,12])
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1", "11", "111")...)
	AssertCustomUsedEqual(t, createResourceList(12, 12), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "2", "21", "211")...)

	// add matched pod with quota=q121, no custom-used
	pod3 := schetesting.MakePod().Name("3").Label(extension.LabelQuotaName, q121.Name).Label(
		"app", "test").Containers([]v1.Container{schetesting.MakeContainer().Name("0").Resources(
		map[v1.ResourceName]string{v1.ResourceCPU: "3", v1.ResourceMemory: "3"}).Obj()}).Node("node0").Obj()
	gqm.OnPodAdd(q121.Name, pod3)
	// verify: no changes on custom-used
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1", "11", "111")...)
	AssertCustomUsedEqual(t, createResourceList(12, 12), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "2", "21", "211")...)

	// add custom limit for quota 121
	q121.Annotations[annoKeyLimitConf] = limitConfV
	err = gqm.UpdateQuota(q121, false)
	assert.NoError(t, err)

	// expected quotas
	// quota1     								mock-used[8,8] (updated: mock-used +[3,3])
	//   |-- quota11		mock-limit[20,20]  	mock-used[5,5]
	//       |-- quota111						mock-used[5,5]
	//   |-- quota12							mock-used[3,3] (updated: mock-used +[3,3])
	//       |-- quota121	mock-limit[20,20]	mock-used[3,3] (updated: mock-used +[3,3])
	//       |-- quota122
	// quota2									mock-used[12,12]
	//   |-- quota21							mock-used[12,12]
	//       |-- quota211 	mock-limit[20,20]  	mock-used[12,12]

	// quota chain will be rebuilt: q121 -> q12 -> q1
	limitQuotaNames = append(limitQuotaNames, "121")
	enabledQuotaNames = append(enabledQuotaNames, "121", "12")
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)
	AssertCustomUsedEqual(t, createResourceList(3, 3), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "121", "12")...)
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "11", "111")...)
	AssertCustomUsedEqual(t, createResourceList(8, 8), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1")...)
	AssertCustomUsedEqual(t, createResourceList(12, 12), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "2", "21", "211")...)

	// move quota 21 to be a child of quota 1
	q21.Labels[extension.LabelQuotaParent] = q1.Name
	err = gqm.UpdateQuota(q21, false)
	assert.NoError(t, err)

	// expected quotas
	// quota1     								mock-used[20,20] (updated: mock-used +[12,12])
	//   |-- quota11		mock-limit[20,20]  	mock-used[5,5]
	//       |-- quota111						mock-used[5,5]
	//   |-- quota12							mock-used[3,3]
	//       |-- quota121	mock-limit[10,10]	mock-used[3,3]
	//       |-- quota122
	//   |-- quota21							mock-used[12,12]
	//       |-- quota211 	mock-limit[15,15]  	mock-used[12,12]
	// quota2													 (updated: remove mock-used)

	// no changes on custom-limit
	// quota chain will be rebuilt: q211 -> q21 -> q1  (remove mock-used from q2)
	enabledQuotaNames = []string{"1", "11", "111", "12", "121", "21", "211"}
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)

	AssertCustomUsedEqual(t, createResourceList(3, 3), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "12", "121")...)
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "11", "111")...)
	AssertCustomUsedEqual(t, createResourceList(20, 20), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1")...)
	AssertCustomUsedEqual(t, createResourceList(12, 12), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "21", "211")...)

	// move quota 11 to be a child of quota 2
	q11.Labels[extension.LabelQuotaParent] = q2.Name
	err = gqm.UpdateQuota(q11, false)
	assert.NoError(t, err)

	// expected quotas
	// quota1     								mock-used[15,15] (updated: mock-used -[5,5])
	//   |-- quota12							mock-used[3,3]
	//       |-- quota121	mock-limit[10,10]	mock-used[3,3]
	//       |-- quota122
	//   |-- quota21							mock-used[12,12]
	//       |-- quota211 	mock-limit[15,15]  	mock-used[12,12]
	// quota2									mock-used[5,5]	 (updated: + mock-used [5,5])
	//   |-- quota11		mock-limit[20,20]  	mock-used[5,5]
	//       |-- quota111						mock-used[5,5]

	// no changes on custom-limit
	// quota chain will be rebuilt: q211 -> q21 -> q1  (remove mock-used from q2)
	enabledQuotaNames = []string{"1", "12", "121", "21", "211", "2", "11", "111"}
	checkCustomState(t, testCustomKey, gqm.quotaInfoMap, limitResList,
		limitQuotaNames, enabledQuotaNames)

	AssertCustomUsedEqual(t, createResourceList(3, 3), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "12", "121")...)
	AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "2", "11", "111")...)
	AssertCustomUsedEqual(t, createResourceList(15, 15), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "1")...)
	AssertCustomUsedEqual(t, createResourceList(12, 12), testCustomKey,
		GetQuotaInfos(gqm.quotaInfoMap, "21", "211")...)
}

func TestGroupQuotaManager_CustomLimiter_QuotaUpdateScenarios(t *testing.T) {
	testCustomKey1, testCustomKey2 := CustomKeyMock, CustomKeyMock+"2"
	annoKeyLimitConf1, annoKeyLimitConf2 := fmt.Sprintf(AnnotationKeyMockLimitFmt, testCustomKey1),
		fmt.Sprintf(AnnotationKeyMockLimitFmt, testCustomKey2)
	limitResConfV1, limitResConfV2 := `{"cpu":15,"memory":15}`, `{"cpu":10,"memory":10}`
	limitResList1, limitResList2 := createResourceList(15, 15), createResourceList(10, 10)
	customLimiter, err := NewMockLimiter(testCustomKey1, `{"labelSelector":"app=test"}`)
	assert.NoError(t, err)
	customLimiter2, err := NewMockLimiter(testCustomKey2, `{"labelSelector":"app=test"}`)
	assert.NoError(t, err)

	// init function to add matched pod
	addMatchedPodFn := func(gqm *GroupQuotaManager, podName, quotaName string, cpu, memory int) {
		pod := schetesting.MakePod().Name(podName).Label(extension.LabelQuotaName, quotaName).Label(
			"app", "test").Containers([]v1.Container{schetesting.MakeContainer().Name("0").Resources(
			map[v1.ResourceName]string{v1.ResourceCPU: strconv.Itoa(cpu),
				v1.ResourceMemory: strconv.Itoa(memory), "test": "2"}).Obj()}).Node("node0").Obj()
		gqm.OnPodAdd(quotaName, pod)
	}
	// init preHook function to set limit conf for quota 12, 2, 211
	preHookFn1 := func(quotas map[string]*v1alpha1.ElasticQuota) {
		quotas["12"].Annotations[annoKeyLimitConf1] = limitResConfV1
		quotas["2"].Annotations[annoKeyLimitConf1] = limitResConfV1
		quotas["211"].Annotations[annoKeyLimitConf1] = limitResConfV1
	}
	preHookFn2 := func(quotas map[string]*v1alpha1.ElasticQuota) {
		quotas["12"].Annotations[annoKeyLimitConf1] = limitResConfV1
		quotas["2"].Annotations[annoKeyLimitConf1] = limitResConfV1
		quotas["211"].Annotations[annoKeyLimitConf1] = limitResConfV1
		quotas["12"].Annotations[annoKeyLimitConf2] = limitResConfV2
		quotas["111"].Annotations[annoKeyLimitConf2] = limitResConfV2

	}
	// test cases
	cases := []struct {
		name      string
		preHookFn func(quotas map[string]*v1alpha1.ElasticQuota)
		updateFn  func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota)
		checkFn   func(quotaInfos map[string]*QuotaInfo)
	}{
		{
			// expected quotas
			// quota1									mock-used[]
			//   |-- quota11
			//       |-- quota111
			//   |-- quota12		mock-limit[15,15] 	mock-used[]
			//       |-- quota121						mock-used[]
			//       |-- quota122						mock-used[]
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			name:      "no update",
			preHookFn: preHookFn1,
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				limitQuotaNames := []string{"12", "2", "211"}
				enabledQuotaNames := []string{"1", "12", "121", "122", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[1,1]	(updated: +mock-used[1,1])
			//   |-- quota11
			//       |-- quota111
			//       |-- quota112									  	// add quota 112, add pod2 [2,2]
			//   |-- quota12		mock-limit[15,15] 	mock-used[1,1]  (updated: +mock-used[1,1])
			//       |-- quota121						mock-used[]
			//       |-- quota122						mock-used[1,1]	(updated: +mock-used[1,1]) // add pod1 [1,1]
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			name:      "add quota 112 without limit conf",
			preHookFn: preHookFn1,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				q112 := CreateQuota("112", "11", 100, 100, 0, 0, true, false)
				err = gqm.UpdateQuota(q112, false)
				assert.NoError(t, err)
				// add pod to quota 122 and 112
				addMatchedPodFn(gqm, "1", "122", 1, 1)
				addMatchedPodFn(gqm, "2", "112", 2, 2)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				limitQuotaNames := []string{"12", "2", "211"}
				enabledQuotaNames := []string{"1", "12", "121", "122", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, "121", "2", "21", "211")...)
				AssertCustomUsedEqual(t, createResourceList(1, 1), testCustomKey1,
					GetQuotaInfos(quotaInfos, "1", "12", "122")...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[3,3]  (updated: +mock-used[3,3])
			//   |-- quota11							mock-used[2,2]  (updated: +mock-used[2,2])
			//       |-- quota111
			//       |-- quota112	mock-limit[15,15]	mock-used[2,2]	 // add quota 112, add pod2 [2,2]
			//   |-- quota12		mock-limit[15,15] 	mock-used[1,1]
			//       |-- quota121						mock-used[]
			//       |-- quota122						mock-used[1,1]   (updated: +mock-used[1,1]) // add pod1 [1,1]
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			name:      "add quota 112 with limit conf",
			preHookFn: preHookFn1,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				q112 := CreateQuota("112", "11", 100, 100, 0, 0, true, false)
				q112.Annotations[annoKeyLimitConf1] = limitResConfV1
				err = gqm.UpdateQuota(q112, false)
				assert.NoError(t, err)
				// add pod to quota 122 and 112
				addMatchedPodFn(gqm, "1", "122", 1, 1)
				addMatchedPodFn(gqm, "2", "112", 2, 2)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				limitQuotaNames := []string{"12", "2", "211", "112"}
				enabledQuotaNames := []string{"1", "11", "112", "12", "121", "122", "12", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, "121", "2", "21", "211")...)
				AssertCustomUsedEqual(t, createResourceList(1, 1), testCustomKey1,
					GetQuotaInfos(quotaInfos, "122", "12")...)
				AssertCustomUsedEqual(t, createResourceList(2, 2), testCustomKey1,
					GetQuotaInfos(quotaInfos, "112", "11")...)
				AssertCustomUsedEqual(t, createResourceList(3, 3), testCustomKey1,
					GetQuotaInfos(quotaInfos, "1")...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[1,1]	(updated +mock-used[1,1])
			//   |-- quota11
			//       |-- quota111
			//   |-- quota12		mock-limit[15,15] 	mock-used[1,1]	(updated +mock-used[1,1])
			//       |-- quota121						mock-used[]
			//       |-- quota122						mock-used[]
			//       |-- quota123						mock-used[1,1]  (updated +mock-used[1,1]) // add quota 123, add pod1 [1,1]
			// quota2				mock-limit[15,15]	mock-used[2,2]	(updated +mock-used[2,2])
			//   |-- quota21							mock-used[2,2]	(updated +mock-used[2,2])
			//       |-- quota211   mock-limit[15,15]	mock-used[2,2] 	(updated +mock-used[2,2]) // add pod2 [2,2]
			name:      "add quota 123 without limit conf",
			preHookFn: preHookFn1,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				q123 := CreateQuota("123", "12", 100, 100, 0, 0, true, false)
				err = gqm.UpdateQuota(q123, false)
				assert.NoError(t, err)
				// add pod to quota 123 and 211
				addMatchedPodFn(gqm, "1", "123", 1, 1)
				addMatchedPodFn(gqm, "2", "211", 2, 2)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				limitQuotaNames := []string{"12", "2", "211"}
				enabledQuotaNames := []string{"1", "12", "121", "122", "123", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, "121", "122")...)
				AssertCustomUsedEqual(t, createResourceList(1, 1), testCustomKey1,
					GetQuotaInfos(quotaInfos, "1", "12", "123")...)
				AssertCustomUsedEqual(t, createResourceList(2, 2), testCustomKey1,
					GetQuotaInfos(quotaInfos, "2", "21", "211")...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[5,5]	(updated: + mock-used[5,5])
			//   |-- quota11							mock-used[2,2]	(updated: + mock-used[2,2])
			//       |-- quota111
			//       |-- quota112	mock-limit[15,15]	mock-used[2,2]	 // add quota 113, add pod2 [2,2]
			//   |-- quota12		mock-limit[15,15] 	mock-used[3,3]
			//       |-- quota121						mock-used[]
			//       |-- quota122						mock-used[3,3]	(updated: + mock-used[3,3]) //add pod1 [3,3]
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			name:      "add quota 112 with limit conf",
			preHookFn: preHookFn1,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				q112 := CreateQuota("112", "11", 100, 100, 0, 0, true, false)
				q112.Annotations[annoKeyLimitConf1] = limitResConfV1
				err = gqm.UpdateQuota(q112, false)
				assert.NoError(t, err)
				// add pod to quota 123 and 211
				addMatchedPodFn(gqm, "1", "122", 3, 3)
				addMatchedPodFn(gqm, "2", "112", 2, 2)

			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				limitQuotaNames := []string{"12", "2", "211", "112"}
				enabledQuotaNames := []string{"1", "11", "112", "12", "121", "122", "12", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, "121", "2", "21", "211")...)
				AssertCustomUsedEqual(t, createResourceList(2, 2), testCustomKey1,
					GetQuotaInfos(quotaInfos, "11", "112")...)
				AssertCustomUsedEqual(t, createResourceList(3, 3), testCustomKey1,
					GetQuotaInfos(quotaInfos, "12", "122")...)
				AssertCustomUsedEqual(t, createResourceList(5, 5), testCustomKey1,
					GetQuotaInfos(quotaInfos, "1")...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[]
			//   |-- quota11
			//       |-- quota111 										// delete quota 111
			//   |-- quota12		mock-limit[15,15] 	mock-used[]
			//       |-- quota121						mock-used[]
			//       |-- quota122						mock-used[]
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			name:      "delete quota 111",
			preHookFn: preHookFn1,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				err = gqm.UpdateQuota(quotas["111"], true)
				assert.NoError(t, err)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				limitQuotaNames := []string{"12", "2", "211"}
				enabledQuotaNames := []string{"1", "12", "121", "122", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[]
			//   |-- quota11
			//       |-- quota111
			//   |-- quota12		mock-limit[15,15] 	mock-used[]
			//       |-- quota121						mock-used[]
			//       |-- quota122						mock-used[]	  	// delete quota 122
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			name:      "delete quota 122",
			preHookFn: preHookFn1,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				err = gqm.UpdateQuota(quotas["122"], true)
				assert.NoError(t, err)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				limitQuotaNames := []string{"12", "2", "211"}
				enabledQuotaNames := []string{"1", "12", "121", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[]
			//   |-- quota11
			//       |-- quota111
			//   |-- quota12		mock-limit[15,15] 	mock-used[]
			//       |-- quota121						mock-used[]
			//       |-- quota122						mock-used[]
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]	  	// delete quota 211
			name:      "delete quota 211",
			preHookFn: preHookFn1,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				err = gqm.UpdateQuota(quotas["211"], true)
				assert.NoError(t, err)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				limitQuotaNames := []string{"12", "2"}
				enabledQuotaNames := []string{"1", "12", "121", "122", "2", "21"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[]  (updated: - mock-used[])
			//   |-- quota11
			//       |-- quota111
			//   |-- quota12		mock-limit[15,15] 	mock-used[]	  	// delete quota 12
			//       |-- quota121						mock-used[]	  	// delete quota 121
			//       |-- quota122						mock-used[]	  	// delete quota 122
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			name:      "delete quota 12",
			preHookFn: preHookFn1,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				err = gqm.UpdateQuota(quotas["122"], true)
				assert.NoError(t, err)
				err = gqm.UpdateQuota(quotas["121"], true)
				assert.NoError(t, err)
				err = gqm.UpdateQuota(quotas["12"], true)
				assert.NoError(t, err)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				limitQuotaNames := []string{"211", "2"}
				enabledQuotaNames := []string{"12", "121", "122", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[]
			// 											mock2-used[]
			//   |-- quota11
			// 											mock2-used[]
			//       |-- quota111
			// 						mock2-limit[10,10] 	mock2-used[]
			//   |-- quota12		mock-limit[15,15] 	mock-used[]
			// 						mock2-limit[10,10] 	mock2-used[]
			//       |-- quota121						mock-used[]
			// 											mock2-used[]
			//       |-- quota122						mock-used[]
			// 											mock2-used[]
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			name:      "2 custom-limiter",
			preHookFn: preHookFn2,
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				// for custom-key: mock
				limitQuotaNames := []string{"12", "2", "211"}
				enabledQuotaNames := []string{"1", "12", "121", "122", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)

				// for custom-key: mock2
				limitQuotaNames = []string{"111", "12"}
				enabledQuotaNames = []string{"1", "11", "111", "12", "121", "122"}
				checkCustomState(t, testCustomKey2, quotaInfos, limitResList2,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey2,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
			},
		},
		{
			// expected quotas
			// quota1									(updated: - mock-used)
			// 											mock2-used[]
			//   |-- quota11
			// 											mock2-used[]
			//       |-- quota111
			// 						mock2-limit[10,10] 	mock2-used[]
			//   |-- quota12							(updated: - mock-limit, - mock-used) // remove mock-limit
			// 											(updated: - mock2-limit, - mock2-used) // remove mock2-limit
			//       |-- quota121						(updated: - mock-used)
			// 											(updated: - mock2-used)
			//       |-- quota122						(updated: - mock-used)
			// 											(updated: - mock2-used)
			// quota2				mock-limit[15,15]	mock-used[]
			//   |-- quota21							mock-used[]
			//       |-- quota211   					mock-used[]		// remove mock-limit
			name:      "2 custom-limiter: update quota 211 & 12: remove mock-limit / mock2-limit",
			preHookFn: preHookFn2,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				q211 := quotas["211"].DeepCopy()
				delete(q211.Annotations, annoKeyLimitConf1)
				err = gqm.UpdateQuota(q211, false)
				assert.NoError(t, err)
				q12 := quotas["12"].DeepCopy()
				delete(q12.Annotations, annoKeyLimitConf1)
				delete(q12.Annotations, annoKeyLimitConf2)
				err = gqm.UpdateQuota(q12, false)
				assert.NoError(t, err)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				// for custom-key: mock
				limitQuotaNames := []string{"2"}
				enabledQuotaNames := []string{"2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)

				// for custom-key: mock2
				limitQuotaNames = []string{"111"}
				enabledQuotaNames = []string{"1", "11", "111"}
				checkCustomState(t, testCustomKey2, quotaInfos, limitResList2,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey2,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
			},
		},
		{
			// expected quotas
			// quota1									mock-used[]
			// 											mock2-used[]
			//   |-- quota11
			// 						mock-limit[15,15]	mock-used[]  (updated: + mock-limit mock-used) // add mock-limit
			// 											mock2-used[]
			//       |-- quota111						mock-used[]  (updated: + mock-used)
			// 						mock2-limit[10,10] 	mock2-used[]
			//   |-- quota12		mock-limit[15,15] 	mock-used[]
			// 						mock2-limit[10,10] 	mock2-used[]
			//       |-- quota121						mock-used[]
			// 											mock2-used[]
			//       |-- quota122						mock-used[]
			// 											mock2-used[]
			// quota2				mock-limit[15,15]	mock-used[]
			// 											mock2-used[] (updated: + mock2-used)
			//   |-- quota21							mock-used[]
			// 						mock2-limit[10,10] 	mock2-used[] (updated: + mock2-limit mock2-used) // add mock2-limit
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			// 											mock2-used[] (updated: + mock2-used)
			name:      "2 custom-limiter: update quota 21 & 11: add mock-limit / mock2-limit",
			preHookFn: preHookFn2,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				q11 := quotas["11"].DeepCopy()
				delete(q11.Annotations, annoKeyLimitConf1)
				q11.Annotations[annoKeyLimitConf1] = limitResConfV1
				err = gqm.UpdateQuota(q11, false)
				assert.NoError(t, err)
				q21 := quotas["21"].DeepCopy()
				q21.Annotations[annoKeyLimitConf2] = limitResConfV2
				err = gqm.UpdateQuota(q21, false)
				assert.NoError(t, err)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				// for custom-key: mock
				limitQuotaNames := []string{"11", "12", "2", "211"}
				enabledQuotaNames := []string{"1", "11", "111", "12", "121", "122", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)

				// for custom-key: mock2
				limitQuotaNames = []string{"111", "12", "21"}
				enabledQuotaNames = []string{"1", "11", "111", "12", "121", "122", "2", "21", "211"}
				checkCustomState(t, testCustomKey2, quotaInfos, limitResList2,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey2,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
			},
		},
		{
			// expected quotas before moving:
			// quota1									mock-used[]
			// 											mock2-used[1,1]
			//   |-- quota11
			// 											mock2-used[1,1]
			//       |-- quota111
			// 						mock2-limit[10,10] 	mock2-used[1,1]
			//   |-- quota12		mock-limit[15,15] 	mock-used[]
			// 						mock2-limit[10,10] 	mock2-used[]
			//       |-- quota121						mock-used[]
			// 											mock2-used[]
			//       |-- quota122						mock-used[]
			// 											mock2-used[]
			// quota2				mock-limit[15,15]	mock-used[2,3]
			//   |-- quota21							mock-used[2,3]
			//       |-- quota211   mock-limit[15,15]	mock-used[2,3]

			// expected quotas after moving:
			// quota1									mock-used[]
			// 											mock2-used[] (update: - mock2-used [1,1])
			//   |-- quota11
			// 													  	(updated: - mock2-used)
			//   |-- quota12		mock-limit[15,15] 	mock-used[]
			// 						mock2-limit[10,10] 	mock2-used[]
			//       |-- quota121						mock-used[]
			// 											mock2-used[]
			//       |-- quota122						mock-used[]
			// 											mock2-used[]
			// quota2				mock-limit[15,15]	mock-used[2,3]
			// 											mock2-used[1,1] (updated: + mock2-used[1,1])
			//   |-- quota21							mock-used[2,3]
			// 											mock2-used[1,1] (updated: + mock2-used[1,1])
			//       |-- quota211   mock-limit[15,15]	mock-used[2,3]
			//       |-- quota111						mock-used[]  (updated: + mock-used)
			// 						mock2-limit[10,10] 	mock2-used[1,1] // move 111 to 21
			name:      "2 custom-limiter: add pods to quota 111 and 211",
			preHookFn: preHookFn2,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				// add pods to quota 111 and 211
				addMatchedPodFn(gqm, "1", "111", 1, 1)
				addMatchedPodFn(gqm, "2", "211", 2, 3)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				// check state before moving
				limitQuotaNames1 := []string{"12", "2", "211"}
				enabledQuotaNames1 := []string{"1", "12", "121", "122", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames1, enabledQuotaNames1)
				AssertCustomUsedEqual(t, createResourceList(2, 3), testCustomKey1,
					GetQuotaInfos(quotaInfos, "2", "21", "211")...)

				limitQuotaNames2 := []string{"12", "111"}
				enabledQuotaNames2 := []string{"1", "11", "111", "12", "121", "122"}
				checkCustomState(t, testCustomKey2, quotaInfos, limitResList2,
					limitQuotaNames2, enabledQuotaNames2)
				AssertCustomUsedEqual(t, createResourceList(1, 1), testCustomKey2,
					GetQuotaInfos(quotaInfos, "1", "11", "111")...)
			},
		},
		{
			// expected quotas after moving:
			// quota1									mock-used[]
			// 											mock2-used[0,0] (update: - mock2-used [1,1])
			//   |-- quota11
			// 													  	(updated: - mock2-used)
			//   |-- quota12		mock-limit[15,15] 	mock-used[]
			// 						mock2-limit[10,10] 	mock2-used[]
			//       |-- quota121						mock-used[]
			// 											mock2-used[]
			//       |-- quota122						mock-used[]
			// 											mock2-used[]
			// quota2				mock-limit[15,15]	mock-used[3,4] (updated: + mock-used[1,1])
			// 											mock2-used[1,1] (updated: + mock2-used[1,1])
			//   |-- quota21							mock-used[3,4]  (updated: + mock-used[1,1])
			// 											mock2-used[1,1] (updated: + mock2-used[1,1])
			//       |-- quota211   mock-limit[15,15]	mock-used[2,3]
			//       |-- quota111						mock-used[1,1]  (updated: + mock-used[1,1])
			// 						mock2-limit[10,10] 	mock2-used[1,1] // move 111 to 21
			name:      "2 custom-limiter: add pods to quota 111 and 211, then move quota 111 to 21",
			preHookFn: preHookFn2,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				// add pods to quota 111 and 211
				addMatchedPodFn(gqm, "1", "111", 1, 1)
				addMatchedPodFn(gqm, "2", "211", 2, 3)

				// move 111 to 21
				q111 := quotas["111"].DeepCopy()
				q111.Labels[extension.LabelQuotaParent] = "21"
				err = gqm.UpdateQuota(q111, false)
				assert.NoError(t, err)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				// for custom-key: mock
				limitQuotaNames := []string{"12", "2", "211"}
				enabledQuotaNames := []string{"1", "12", "121", "122", "2", "21", "211", "111"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, "1", "12", "121", "122")...)
				AssertCustomUsedEqual(t, createResourceList(1, 1), testCustomKey1,
					GetQuotaInfos(quotaInfos, "111")...)
				AssertCustomUsedEqual(t, createResourceList(2, 3), testCustomKey1,
					GetQuotaInfos(quotaInfos, "211")...)
				AssertCustomUsedEqual(t, createResourceList(3, 4), testCustomKey1,
					GetQuotaInfos(quotaInfos, "2", "21")...)

				// for custom-key: mock2
				limitQuotaNames = []string{"111", "12"}
				enabledQuotaNames = []string{"1", "12", "121", "122", "2", "21", "111"}
				checkCustomState(t, testCustomKey2, quotaInfos, limitResList2,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey2,
					GetQuotaInfos(quotaInfos, "12", "121", "122")...)
				AssertCustomUsedEqual(t, createResourceList(0, 0), testCustomKey2,
					GetQuotaInfos(quotaInfos, "1")...)
				AssertCustomUsedEqual(t, createResourceList(1, 1), testCustomKey2,
					GetQuotaInfos(quotaInfos, "2", "21", "111")...)
			},
		},
		{
			// expected quotas
			// quota1												(updated: - mock-used)
			// 											mock2-used[]
			//   |-- quota11
			// 											mock2-used[]
			//       |-- quota111
			// 						mock2-limit[10,10] 	mock2-used[]
			// quota2				mock-limit[15,15]	mock-used[]
			// 											mock2-used[] (updated: + mock2-used)
			//   |-- quota21							mock-used[]
			//       |-- quota211   mock-limit[15,15]	mock-used[]
			//   |-- quota12		mock-limit[15,15] 	mock-used[]  // move 12 to 2
			// 						mock2-limit[10,10] 	mock2-used[]
			//       |-- quota121						mock-used[]
			// 											mock2-used[]
			//       |-- quota122						mock-used[]
			// 											mock2-used[]
			name:      "move quota 12 to 2",
			preHookFn: preHookFn2,
			updateFn: func(gqm *GroupQuotaManager, quotas map[string]*v1alpha1.ElasticQuota) {
				q12 := quotas["12"].DeepCopy()
				q12.Labels[extension.LabelQuotaParent] = "2"
				err = gqm.UpdateQuota(q12, false)
				assert.NoError(t, err)
			},
			checkFn: func(quotaInfos map[string]*QuotaInfo) {
				// for custom-key: mock
				limitQuotaNames := []string{"12", "2", "211"}
				enabledQuotaNames := []string{"12", "121", "122", "2", "21", "211"}
				checkCustomState(t, testCustomKey1, quotaInfos, limitResList1,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey1,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)

				// for custom-key: mock2
				limitQuotaNames = []string{"111", "12"}
				enabledQuotaNames = []string{"1", "11", "111", "12", "121", "122", "2"}
				checkCustomState(t, testCustomKey2, quotaInfos, limitResList2,
					limitQuotaNames, enabledQuotaNames)
				AssertCustomUsedEqual(t, v1.ResourceList{}, testCustomKey2,
					GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gqm := NewGroupQuotaManagerForTest(WithCustomLimiter(map[string]CustomLimiter{
				testCustomKey1: customLimiter,
				testCustomKey2: customLimiter2,
			}))
			// create test quotas
			testQuotas, testQuotaMap := GetTestQuotasForCustomLimiters()
			err = UpdateQuotas(gqm, c.preHookFn, testQuotas...)
			assert.NoError(t, err)
			// update
			if c.updateFn != nil {
				c.updateFn(gqm, testQuotaMap)
			}
			// check
			c.checkFn(gqm.quotaInfoMap)
		})
	}
}

func getOthers(allItems, excludeItems []string) (rst []string) {
	excludeSet := set.New[string](excludeItems...)
	for _, e := range allItems {
		if excludeSet.Has(e) {
			continue
		}
		rst = append(rst, e)
	}
	return
}

func checkCustomState(t *testing.T, customKey string, quotaInfos map[string]*QuotaInfo,
	expectedLimitResList v1.ResourceList, limitQuotaNames, enabledQuotaNames []string) {
	noLimitQuotaNames := getOthers(maps.Keys(quotaInfos), limitQuotaNames)
	disabledQuotaNames := getOthers(maps.Keys(quotaInfos), enabledQuotaNames)

	AssertCustomLimitEqual(t, expectedLimitResList, customKey,
		GetQuotaInfos(quotaInfos, limitQuotaNames...)...)
	AssertCustomLimitEqual(t, nil, customKey, GetQuotaInfos(quotaInfos, noLimitQuotaNames...)...)

	AssertCustomLimiterState(t, customKey, true,
		GetQuotaInfos(quotaInfos, enabledQuotaNames...)...)
	AssertCustomLimiterState(t, customKey, false,
		GetQuotaInfos(quotaInfos, disabledQuotaNames...)...)

	AssertCustomUsedEqual(t, nil, customKey,
		GetQuotaInfos(quotaInfos, disabledQuotaNames...)...)
}
