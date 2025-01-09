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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	schetesting "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

const (
	GigaByte = 1024 * 1048576
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
	gqm := NewGroupQuotaManagerForTest()
	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	AddQuotaToManager(t, gqm, "test1", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	// test1 request[120, 290]  runtime == maxQuota
	request := createResourceList(120, 290*GigaByte)
	gqm.updateGroupDeltaRequestNoLock("test1", request, request, 0)
	runtime := gqm.RefreshRuntime("test1")
	assert.Equal(t, deltaRes, runtime)

	quota1 := CreateQuota("test1", extension.RootQuotaName, 64, 100*GigaByte, 60, 90*GigaByte, true, false)
	quota1.Labels[extension.LabelQuotaIsParent] = "false"
	err := gqm.UpdateQuota(quota1, false)
	assert.Nil(t, err)
	quotaInfo := gqm.GetQuotaInfoByName("test1")
	assert.Equal(t, createResourceList(64, 100*GigaByte), quotaInfo.CalculateInfo.Max)

	runtime = gqm.RefreshRuntime("test1")
	assert.Equal(t, createResourceList(64, 100*GigaByte), runtime)
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
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.Request)
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(0, 0), quotaInfo.CalculateInfo.Runtime)

	quotaInfo = gqm.GetQuotaInfoByName("test1")
	gqm.RefreshRuntime("test1")
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.Request)
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.Used)
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

func NewGroupQuotaManagerForTest() *GroupQuotaManager {
	quotaManager := &GroupQuotaManager{
		totalResourceExceptSystemAndDefaultUsed: v1.ResourceList{},
		totalResource:                           v1.ResourceList{},
		resourceKeys:                            make(map[v1.ResourceName]struct{}),
		quotaInfoMap:                            make(map[string]*QuotaInfo),
		runtimeQuotaCalculatorMap:               make(map[string]*RuntimeQuotaCalculator),
		scaleMinQuotaManager:                    NewScaleMinQuotaManager(),
		quotaTopoNodeMap:                        make(map[string]*QuotaTopoNode),
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
	gqm := NewGroupQuotaManager("", createResourceList(100, 100), createResourceList(300, 300))
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

func TestUpdateQuotaInternalNoLock_ParenstSelf(t *testing.T) {
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
