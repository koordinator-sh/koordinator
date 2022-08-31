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
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	GigaByte = 1024 * 1048576
)

func TestGroupQuotaManager_QuotaAdd(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()
	gqm.UpdateClusterTotalResource(createResourceList(10000000, 3000*GigaByte))
	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96000, 160*GigaByte, 50000, 50*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "alimm", extension.RootQuotaName, 96000, 160*GigaByte, 80000, 80*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "hema", extension.RootQuotaName, 96000, 160*GigaByte, 40000, 40*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "kaola", extension.RootQuotaName, 96000, 160*GigaByte, 0, 0*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "youku", extension.RootQuotaName, 96000, 160*GigaByte, 96000, 96*GigaByte, true, false)

	quotaInfo := gqm.GetQuotaInfoByName("aliyun")
	if quotaInfo.CalculateInfo.SharedWeight.Name(v1.ResourceCPU, resource.DecimalSI).Value() != 96000 {
		t.Errorf("error")
	}

	assert.Equal(t, 6, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 7, len(gqm.quotaInfoMap))
	assert.Equal(t, 2, len(gqm.resourceKeys))
}

func TestGroupQuotaManager_QuotaAdd_AutoMakeUpSharedWeight(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()
	gqm.UpdateClusterTotalResource(createResourceList(1000000, 200*GigaByte))

	quota := CreateQuota("aliyun", extension.RootQuotaName, 96000, 160*GigaByte, 50000, 80*GigaByte, true, false)
	delete(quota.Annotations, extension.AnnotationSharedWeight)

	err := gqm.UpdateQuota(quota, false)
	if err != nil {
		klog.Infof("error")
	}

	quotaInfo := gqm.quotaInfoMap["aliyun"]
	if quotaInfo.CalculateInfo.SharedWeight.Cpu().Value() != 96000 || quotaInfo.CalculateInfo.SharedWeight.Memory().Value() != 160*GigaByte {
		t.Errorf("error:%v", quotaInfo.CalculateInfo.SharedWeight)
	}
}

func TestGroupQuotaManager_QuotaAdd_AutoMakeUpScaleRatio2(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	quota := &v1alpha1.ElasticQuota{
		TypeMeta: metav1.TypeMeta{
			Kind: "Quota",
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
	gqm := NewGroupQuotaManager4Test()

	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	quota := CreateQuota("aliyun", extension.RootQuotaName, 64, 100*GigaByte, 50, 80*GigaByte, true, false)
	gqm.UpdateQuota(quota, false)
	quotaInfo := gqm.quotaInfoMap["aliyun"]
	assert.True(t, quotaInfo != nil)
	assert.False(t, quotaInfo.IsParent)
	assert.Equal(t, createResourceList(64, 100*GigaByte), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, createResourceList(50, 80*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)
	assert.Equal(t, int64(64), quotaInfo.CalculateInfo.SharedWeight.Cpu().Value())
	assert.Equal(t, int64(100*GigaByte), quotaInfo.CalculateInfo.SharedWeight.Memory().Value())

	AddQuotaToManager(t, gqm, "alimm", extension.RootQuotaName, 96, 160*GigaByte, 80, 80*GigaByte, true, false)

	quota = CreateQuota("aliyun", extension.RootQuotaName, 84, 120*GigaByte, 60, 100*GigaByte, true, false)
	quota.Labels[extension.LabelQuotaIsParent] = "true"
	err := gqm.UpdateQuota(quota, false)
	if err != nil {
		klog.Infof("err:%v", err)
	}
	quotaInfo = gqm.quotaInfoMap["aliyun"]
	assert.True(t, quotaInfo != nil)
	assert.True(t, quotaInfo.IsParent)
	assert.Equal(t, createResourceList(84, 120*GigaByte), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, createResourceList(60, 100*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)
	assert.Equal(t, int64(84), quotaInfo.CalculateInfo.SharedWeight.Cpu().Value())
	assert.Equal(t, int64(120*GigaByte), quotaInfo.CalculateInfo.SharedWeight.Memory().Value())
}

func TestGroupQuotaManager_UpdateQuotaInternalAndRequest(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	setLoglevel("5")
	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	// aliyun request[120, 290]  runtime == maxQuota
	request := createResourceList(120, 290*GigaByte)
	gqm.UpdateGroupDeltaRequest("aliyun", request)
	runtime := gqm.RefreshRuntime("aliyun")
	assert.Equal(t, deltaRes, runtime)

	quota1 := CreateQuota("aliyun", extension.RootQuotaName, 64, 100*GigaByte, 60, 90*GigaByte, true, false)
	quota1.Labels[extension.LabelQuotaIsParent] = "false"
	err := gqm.UpdateQuota(quota1, false)
	if err != nil {
		klog.Infof("error:%v", err)
	}
	quotaInfo := gqm.GetQuotaInfoByName("aliyun")
	assert.Equal(t, createResourceList(64, 100*GigaByte), quotaInfo.CalculateInfo.Max)

	runtime = gqm.RefreshRuntime("aliyun")
	assert.Equal(t, createResourceList(64, 100*GigaByte), runtime)
}

func TestGroupQuotaManager_doDeleteOneGroup(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()
	gqm.UpdateClusterTotalResource(createResourceList(1000, 1000*GigaByte))
	quota1 := AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)
	quota2 := AddQuotaToManager(t, gqm, "alimm", extension.RootQuotaName, 96, 160*GigaByte, 80, 80*GigaByte, true, false)
	quota3 := AddQuotaToManager(t, gqm, "hema", extension.RootQuotaName, 96, 160*GigaByte, 40, 40*GigaByte, true, false)
	assert.Equal(t, 4, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 5, len(gqm.quotaInfoMap))

	//err := gqm.DeleteOneGroup(quota1)
	err := gqm.UpdateQuota(quota1, true)
	if err != nil {
		klog.Infof("err:%v", err)
	}
	quotaInfo := gqm.GetQuotaInfoByName("aliyun")
	assert.True(t, quotaInfo == nil)

	err = gqm.UpdateQuota(quota2, true)
	if err != nil {
		klog.Infof("err:%v", err)
	}
	quotaInfo = gqm.GetQuotaInfoByName("alimm")
	assert.True(t, quotaInfo == nil)

	err = gqm.UpdateQuota(quota3, true)
	if err != nil {
		klog.Infof("err:%v", err)
	}
	quotaInfo = gqm.GetQuotaInfoByName("hema")
	assert.True(t, quotaInfo == nil)

	assert.Equal(t, 1, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 2, len(gqm.quotaInfoMap))

	AddQuotaToManager(t, gqm, "youku", extension.RootQuotaName, 96, 160*GigaByte, 70, 70*GigaByte, true, false)
	quotaInfo = gqm.GetQuotaInfoByName("youku")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, 2, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 3, len(gqm.quotaInfoMap))
}

func TestGroupQuotaManager_UpdateQuotaDeltaRequest(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "alimm", extension.RootQuotaName, 96, 160*GigaByte, 40, 80*GigaByte, true, false)

	// aliyun request[120, 290]  runtime == maxQuota
	request := createResourceList(120, 200*GigaByte)
	gqm.UpdateGroupDeltaRequest("aliyun", request)
	runtime := gqm.RefreshRuntime("aliyun")
	assert.Equal(t, deltaRes, runtime)

	// alimm request[150, 210]  runtime
	request = createResourceList(150, 210*GigaByte)
	gqm.UpdateGroupDeltaRequest("alimm", request)
	runtime = gqm.RefreshRuntime("aliyun")
	assert.Equal(t, createResourceList(53, 80*GigaByte), runtime)
	runtime = gqm.RefreshRuntime("alimm")
	assert.Equal(t, createResourceList(43, 80*GigaByte), runtime)
}

func TestGroupQuotaManager_NotAllowLentResource(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	deltaRes := createResourceList(100, 0)
	gqm.UpdateClusterTotalResource(deltaRes)

	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 0, 60, 0, true, false)
	AddQuotaToManager(t, gqm, "alimm", extension.RootQuotaName, 96, 0, 40, 0, false, false)

	request := createResourceList(120, 0)
	gqm.UpdateGroupDeltaRequest("aliyun", request)
	runtime := gqm.RefreshRuntime("aliyun")
	assert.Equal(t, int64(60), runtime.Cpu().Value())
	runtime = gqm.RefreshRuntime("alimm")
	assert.Equal(t, int64(40), runtime.Cpu().Value())
}

func TestGroupQuotaManager_UpdateQuotaRequest(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "alimm", extension.RootQuotaName, 96, 160*GigaByte, 40, 80*GigaByte, true, false)

	// 1. initial aliyun request [60, 100]
	request := createResourceList(60, 100*GigaByte)
	gqm.UpdateGroupDeltaRequest("aliyun", request)
	runtime := gqm.RefreshRuntime("aliyun")
	assert.Equal(t, request, runtime)

	// aliyun request[120, 290]  runtime == maxQuota
	newRequest := createResourceList(120, 200*GigaByte)
	gqm.UpdateGroupDeltaRequest("aliyun", newRequest)
	runtime = gqm.RefreshRuntime("aliyun")
	assert.Equal(t, deltaRes, runtime)

	// alimm request[150, 210]  runtime
	request = createResourceList(150, 210*GigaByte)
	gqm.UpdateGroupDeltaRequest("alimm", request)
	runtime = gqm.RefreshRuntime("aliyun")
	assert.Equal(t, createResourceList(53, 80*GigaByte), runtime)
	runtime = gqm.RefreshRuntime("alimm")
	assert.Equal(t, createResourceList(43, 80*GigaByte), runtime)
}

func TestGroupQuotaManager_UpdateQuotaDeltaUsed(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	// aliyun request[120, 290]  runtime == maxQuota
	used := createResourceList(120, 290*GigaByte)
	gqm.UpdateGroupDeltaUsed("aliyun", used)
	quotaInfo := gqm.GetQuotaInfoByName("aliyun")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, used, quotaInfo.CalculateInfo.Used)

	gqm.UpdateGroupDeltaUsed("aliyun", used)
	quotaInfo = gqm.GetQuotaInfoByName("aliyun")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, quotav1.Add(used, used), quotaInfo.CalculateInfo.Used)
}

func TestGroupQuotaManager_UpdateGroupFullUsed(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	AddQuotaToManager(t, gqm, "aliyun", "root", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	// 1. aliyun used[120, 290]  runtime == maxQuota
	used := createResourceList(120, 290*GigaByte)
	gqm.UpdateGroupDeltaUsed("aliyun", used)
	quotaInfo := gqm.GetQuotaInfoByName("aliyun")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, used, quotaInfo.CalculateInfo.Used)

	// 2. used increases to [130,300]
	used = createResourceList(10, 10*GigaByte)
	gqm.UpdateGroupDeltaUsed("aliyun", used)
	quotaInfo = gqm.GetQuotaInfoByName("aliyun")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, createResourceList(130, 300*GigaByte), quotaInfo.CalculateInfo.Used)

	// 3. used decreases to [90,100]
	used = createResourceList(-40, -200*GigaByte)
	gqm.UpdateGroupDeltaUsed("aliyun", used)
	quotaInfo = gqm.GetQuotaInfoByName("aliyun")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, createResourceList(90, 100*GigaByte), quotaInfo.CalculateInfo.Used)
}

func TestGroupQuotaManager_MultiQuotaAdd(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 160*GigaByte, 60, 100*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "odps", "aliyun", 96, 160*GigaByte, 10, 30*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "holo", "aliyun", 96, 160*GigaByte, 20, 30*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "blink", "aliyun", 96, 160*GigaByte, 30, 40*GigaByte, true, false)

	AddQuotaToManager(t, gqm, "alimm", extension.RootQuotaName, 300, 400*GigaByte, 200, 300*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "youku", "alimm", 96, 160*GigaByte, 96, 160*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "search", "alimm", 82, 100*GigaByte, 80, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "store", "search", 96, 160*GigaByte, 0, 0*GigaByte, true, false)

	assert.Equal(t, 9, len(gqm.runtimeQuotaCalculatorMap))
	assert.Equal(t, 10, len(gqm.quotaInfoMap))
	assert.Equal(t, 2, len(gqm.resourceKeys))
	assert.Equal(t, 2, len(gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].quotaTree[v1.ResourceCPU].quotaNodes))
	assert.Equal(t, 2, len(gqm.runtimeQuotaCalculatorMap["alimm"].quotaTree[v1.ResourceCPU].quotaNodes))
	assert.Equal(t, 1, len(gqm.runtimeQuotaCalculatorMap["search"].quotaTree[v1.ResourceCPU].quotaNodes))
}

func TestGroupQuotaManager_MultiUpdateQuotaRequest(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	// aliyun Max[96, 160]  Min[50,80] request[96,130]
	//   |-- yun-a Max[96, 160]  Min[50,80] request[96,130]
	//         |-- a-123 Max[96, 160]  Min[50,80] request[96,130]
	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "yun-a", "aliyun", 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "a-123", "yun-a", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	request := createResourceList(96, 130*GigaByte)
	gqm.UpdateGroupDeltaRequest("a-123", request)
	runtime := gqm.RefreshRuntime("a-123")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("yun-a")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("aliyun")
	assert.Equal(t, request, runtime)

	// decrease a-123 Max[64, 128]  Min[50,80] request[96,130]
	quota1 := CreateQuota("a-123", "yun-a", 64, 128*GigaByte, 50, 80*GigaByte, true, false)
	gqm.UpdateQuota(quota1, false)
	quotaInfo := gqm.GetQuotaInfoByName("yun-a")
	assert.Equal(t, createResourceList(96, 160*GigaByte), quotaInfo.CalculateInfo.Max)

	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, runtime, quotaInfo.CalculateInfo.Request)

	assert.Equal(t, createResourceList(64, 128*GigaByte), runtime)
	quotaInfo = gqm.GetQuotaInfoByName("a-123")
	assert.Equal(t, request, quotaInfo.CalculateInfo.Request)

	// increase
	quota1 = CreateQuota("a-123", "yun-a", 100, 200*GigaByte, 90, 160*GigaByte, true, false)
	gqm.UpdateQuota(quota1, false)
	quotaInfo = gqm.GetQuotaInfoByName("yun-a")
	assert.Equal(t, createResourceList(96, 160*GigaByte), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, createResourceList(96, 130*GigaByte), quotaInfo.CalculateInfo.Request)

	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, runtime, quotaInfo.CalculateInfo.Request)

	assert.Equal(t, request, runtime)
	quotaInfo = gqm.GetQuotaInfoByName("a-123")
	assert.Equal(t, request, quotaInfo.CalculateInfo.Request)
}

func TestGroupQuotaManager_UpdateQuotaGroupConfig(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	gqm.UpdateQuota(&v1alpha1.ElasticQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
		},
		Spec: v1alpha1.ElasticQuotaSpec{
			Max: createResourceList(100, 1000),
		},
	}, false)
	assert.Equal(t, gqm.GetQuotaInfoByName("default").CalculateInfo.Max, createResourceList(100, 1000))
	runtime := gqm.RefreshRuntime("default")
	assert.Equal(t, runtime, createResourceList(100, 1000))
}

func TestGroupQuotaManager_MultiUpdateQuotaRequest2(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	// aliyun Max[96, 160]  Min[80,80]
	//   |-- yun-a Max[60, 120]  Min[50,80]
	//         |-- a-123 Max[30, 60]  Min[20,40]
	AddQuotaToManager(t, gqm, "aliyun", extension.RootQuotaName, 96, 160*GigaByte, 80, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "yun-a", "aliyun", 60, 80*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "a-123", "yun-a", 30, 60*GigaByte, 20, 40*GigaByte, true, false)

	// a-123 request[10,30]  request < min
	request := createResourceList(10, 30*GigaByte)
	gqm.UpdateGroupDeltaRequest("a-123", request)
	runtime := gqm.RefreshRuntime("a-123")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("yun-a")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("aliyun")
	assert.Equal(t, request, runtime)

	// a-123 request[15,15]  totalRequest[25,45] request > min
	request = createResourceList(15, 15*GigaByte)
	gqm.UpdateGroupDeltaRequest("a-123", request)
	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(25, 45*GigaByte), runtime)

	quotaInfo := gqm.GetQuotaInfoByName("yun-a")
	assert.Equal(t, createResourceList(25, 45*GigaByte), quotaInfo.CalculateInfo.Request)

	quotaInfo = gqm.GetQuotaInfoByName("aliyun")
	assert.Equal(t, createResourceList(25, 45*GigaByte), quotaInfo.CalculateInfo.Request)

	// a-123 request[30,30]  totalRequest[55,75] request > max
	request = createResourceList(30, 30*GigaByte)
	gqm.UpdateGroupDeltaRequest("a-123", request)
	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(30, 60*GigaByte), runtime)

	quotaInfo = gqm.GetQuotaInfoByName("yun-a")
	assert.Equal(t, createResourceList(30, 60*GigaByte), quotaInfo.CalculateInfo.Request)

	quotaInfo = gqm.GetQuotaInfoByName("aliyun")
	assert.Equal(t, createResourceList(30, 60*GigaByte), quotaInfo.CalculateInfo.Request)
}

func TestGroupQuotaManager_MultiUpdateQuotaRequest3(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	// aliyun Max[96, 160]  Min[0, 0]
	//   |-- yun-a Max[60, 120]  Min[0, 0]
	//         |-- a-123 Max[30, 60]  Min[0,0]
	AddQuotaToManager(t, gqm, "aliyun", "root", 96, 160*GigaByte, 0, 0*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "yun-a", "aliyun", 60, 80*GigaByte, 0, 0*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "a-123", "yun-a", 30, 60*GigaByte, 0, 0*GigaByte, true, false)

	// a-123 request[10,30]  request > min
	request := createResourceList(10, 30*GigaByte)
	gqm.UpdateGroupDeltaRequest("a-123", request)
	runtime := gqm.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(0, 0), runtime)

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	// a-123 request[10,30]  request > min
	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("yun-a")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("aliyun")
	assert.Equal(t, request, runtime)

	// a-123 request[15,15]  totalRequest[25,45] request > min
	request = createResourceList(15, 15*GigaByte)
	gqm.UpdateGroupDeltaRequest("a-123", request)
	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(25, 45*GigaByte), runtime)

	quotaInfo := gqm.GetQuotaInfoByName("yun-a")
	assert.Equal(t, createResourceList(25, 45*GigaByte), quotaInfo.CalculateInfo.Request)

	quotaInfo = gqm.GetQuotaInfoByName("aliyun")
	assert.Equal(t, createResourceList(25, 45*GigaByte), quotaInfo.CalculateInfo.Request)

	// a-123 request[30,30]  totalRequest[55,75] request > max
	request = createResourceList(30, 30*GigaByte)
	gqm.UpdateGroupDeltaRequest("a-123", request)
	runtime = gqm.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(30, 60*GigaByte), runtime)

	quotaInfo = gqm.GetQuotaInfoByName("yun-a")
	assert.Equal(t, createResourceList(30, 60*GigaByte), quotaInfo.CalculateInfo.Request)

	quotaInfo = gqm.GetQuotaInfoByName("aliyun")
	assert.Equal(t, createResourceList(30, 60*GigaByte), quotaInfo.CalculateInfo.Request)
}

func TestGroupQuotaManager_MultiUpdateQuotaRequest_WithScaledMinQuota1(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()
	gqm.scaleMinQuotaEnabled = true

	AddQuotaToManager(t, gqm, "p", "root", 1000, 1000*GigaByte, 300, 300*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "a", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "b", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "c", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)

	request := createResourceList(200, 200*GigaByte)
	gqm.UpdateGroupDeltaRequest("a", request)
	gqm.UpdateGroupDeltaRequest("b", request)
	gqm.UpdateGroupDeltaRequest("c", request)

	deltaRes := createResourceList(200, 200*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)

	runtime := gqm.RefreshRuntime("p")
	assert.Equal(t, createResourceList(200, 200*GigaByte), runtime)
	gqm.RefreshRuntime("a")
	gqm.RefreshRuntime("b")
	gqm.RefreshRuntime("c")
	runtime = gqm.RefreshRuntime("a")
	assert.Equal(t, createResourceList(67, 200*GigaByte/3+1), runtime)
	// after group "c" refreshRuntime, the runtime of "a" and "b" has changed
	assert.Equal(t, createResourceList(67, 200*GigaByte/3+1), gqm.RefreshRuntime("a"))
	assert.Equal(t, createResourceList(67, 200*GigaByte/3+1), gqm.RefreshRuntime("b"))

	quotaInfo := gqm.GetQuotaInfoByName("p")
	assert.Equal(t, createResourceList(200, 200*GigaByte), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("a")
	assert.Equal(t, createResourceList(66, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("b")
	assert.Equal(t, createResourceList(66, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("c")
	assert.Equal(t, createResourceList(66, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	//large
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

func TestGroupQuotaManager_MultiUpdateQuotaRequest_WithScaledMinQuota2(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()
	gqm.scaleMinQuotaEnabled = true
	deltaRes := createResourceList(1, 1*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)

	AddQuotaToManager(t, gqm, "p", "root", 1000, 1000*GigaByte, 300, 300*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "a", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "b", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)
	AddQuotaToManager(t, gqm, "c", "p", 1000, 1000*GigaByte, 100, 100*GigaByte, true, false)

	request := createResourceList(200, 200*GigaByte)
	gqm.UpdateGroupDeltaRequest("a", request)
	gqm.UpdateGroupDeltaRequest("b", createResourceList(0, 0))
	gqm.UpdateGroupDeltaRequest("c", request)
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
	assert.Equal(t, createResourceList(66, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("b")
	assert.Equal(t, createResourceList(66, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)

	quotaInfo = gqm.GetQuotaInfoByName("c")
	assert.Equal(t, createResourceList(66, 200*GigaByte/3), quotaInfo.CalculateInfo.AutoScaleMin)
}

func TestGroupQuotaManager_MultiUpdateQuotaUsed(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	AddQuotaToManager(t, gqm, "aliyun", "root", 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "yun-1", "aliyun", 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "yun1-1", "yun-1", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	used := createResourceList(120, 290*GigaByte)
	gqm.UpdateGroupDeltaUsed("yun1-1", used)
	quotaInfo := gqm.GetQuotaInfoByName("yun1-1")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, used, quotaInfo.CalculateInfo.Used)

	quotaInfo = gqm.GetQuotaInfoByName("yun-1")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, used, quotaInfo.CalculateInfo.Used)

	quotaInfo = gqm.GetQuotaInfoByName("aliyun")
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, used, quotaInfo.CalculateInfo.Used)
}

func TestGroupQuotaManager_UpdateQuotaParentName(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	deltaRes := createResourceList(96, 160*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)
	assert.Equal(t, deltaRes, gqm.totalResource)
	totalRes := gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, deltaRes, totalRes)

	// alimm Max[96, 160]  Min[50,80] request[20,40]
	//   `-- mm-a Max[96, 160]  Min[50,80] request[20,40]
	// aliyun Max[96, 160]  Min[50,80] request[60,100]
	//   `-- yun-a Max[96, 160]  Min[50,80] request[60,100]
	//         `-- a-123 Max[96, 160]  Min[50,80] request[60,100]
	AddQuotaToManager(t, gqm, "aliyun", "root", 96, 160*GigaByte, 100, 160*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "yun-a", "aliyun", 96, 160*GigaByte, 50, 80*GigaByte, true, true)
	changeQuota := AddQuotaToManager(t, gqm, "a-123", "yun-a", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	AddQuotaToManager(t, gqm, "alimm", "root", 96, 160*GigaByte, 100, 160*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "mm-a", "alimm", 96, 160*GigaByte, 50, 80*GigaByte, true, false)

	// a-123 request [60,100]
	request := createResourceList(60, 100*GigaByte)
	gqm.UpdateGroupDeltaRequest("a-123", request)
	gqm.UpdateGroupDeltaUsed("a-123", request)
	runtime := gqm.RefreshRuntime("a-123")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("yun-a")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("aliyun")
	assert.Equal(t, request, runtime)

	// mm-a request [20,40]
	request = createResourceList(20, 40*GigaByte)
	gqm.UpdateGroupDeltaRequest("mm-a", request)
	gqm.UpdateGroupDeltaUsed("mm-a", request)
	runtime = gqm.RefreshRuntime("mm-a")
	assert.Equal(t, request, runtime)

	runtime = gqm.RefreshRuntime("alimm")
	assert.Equal(t, request, runtime)

	// a-123 mv alimm
	// alimm Max[96, 160]  Min[100,160] request[80,140]
	//   `-- mm-a Max[96, 160]  Min[50,80] request[20,40]
	//   `-- a-123 Max[96, 160]  Min[50,80] request[60,100]
	// aliyun Max[96, 160]  Min[100,160] request[0,0]
	//   `-- yun-a Max[96, 160]  Min[50,80] request[0,0]
	changeQuota.Labels[extension.LabelQuotaParent] = "alimm"
	gqm.UpdateQuota(changeQuota, false)

	quotaInfo := gqm.GetQuotaInfoByName("yun-a")
	gqm.RefreshRuntime("yun-a")
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.Request)
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(0, 0), quotaInfo.CalculateInfo.Runtime)

	quotaInfo = gqm.GetQuotaInfoByName("aliyun")
	gqm.RefreshRuntime("aliyun")
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.Request)
	assert.Equal(t, v1.ResourceList{}, quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(0, 0), quotaInfo.CalculateInfo.Runtime)

	quotaInfo = gqm.GetQuotaInfoByName("a-123")
	gqm.RefreshRuntime("a-123")
	assert.Equal(t, createResourceList(60, 100*GigaByte), quotaInfo.CalculateInfo.Request)
	assert.Equal(t, createResourceList(60, 100*GigaByte), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(60, 100*GigaByte), quotaInfo.CalculateInfo.Runtime)
	assert.Equal(t, "alimm", quotaInfo.ParentName)

	quotaInfo = gqm.GetQuotaInfoByName("mm-a")
	gqm.RefreshRuntime("mm-a")
	assert.Equal(t, createResourceList(20, 40*GigaByte), quotaInfo.CalculateInfo.Request)
	assert.Equal(t, createResourceList(20, 40*GigaByte), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(20, 40*GigaByte), quotaInfo.CalculateInfo.Runtime)

	quotaInfo = gqm.GetQuotaInfoByName("alimm")
	gqm.RefreshRuntime("alimm")
	assert.Equal(t, createResourceList(80, 140*GigaByte), quotaInfo.CalculateInfo.Request)
	assert.Equal(t, createResourceList(80, 140*GigaByte), quotaInfo.CalculateInfo.Used)
	assert.Equal(t, createResourceList(80, 140*GigaByte), quotaInfo.CalculateInfo.Runtime)
}

func TestGroupQuotaManager_SetClusterTotalResource(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

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
	gqm.UpdateGroupDeltaUsed(extension.SystemQuotaName, sysUsed)
	assert.Equal(t, sysUsed, gqm.GetQuotaInfoByName(extension.SystemQuotaName).GetUsed())

	// 90, 510
	delta := totalRes.DeepCopy()
	delta = quotav1.SubtractWithNonNegativeResult(delta, sysUsed)
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, delta, gqm.totalResourceExceptSystemAndDefaultUsed)
	quotaTotalRes = gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource.DeepCopy()
	assert.Equal(t, delta, quotaTotalRes)

	// 80, 480
	gqm.UpdateGroupDeltaUsed(extension.SystemQuotaName, createResourceList(10, 30))
	delta = quotav1.Subtract(delta, createResourceList(10, 30))
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, delta, gqm.totalResourceExceptSystemAndDefaultUsed)

	// 70, 450
	defaultUsed := createResourceList(10, 30)
	gqm.UpdateGroupDeltaUsed(extension.DefaultQuotaName, defaultUsed)
	assert.Equal(t, defaultUsed, gqm.GetQuotaInfoByName(extension.DefaultQuotaName).GetUsed())
	delta = quotav1.Subtract(delta, defaultUsed)
	assert.Equal(t, totalRes, gqm.totalResource)
	assert.Equal(t, delta, gqm.totalResourceExceptSystemAndDefaultUsed)

	// 60 420
	gqm.UpdateGroupDeltaUsed(extension.DefaultQuotaName, defaultUsed)
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
	if err != nil {
		klog.Infof("error:%v", err)
	}
	quotaInfo := gqm.quotaInfoMap[quotaName]
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, quotaName, quotaInfo.Name)
	assert.Equal(t, quotaInfo.CalculateInfo.Max, createResourceList(maxCpu, maxMem))
	assert.Equal(t, quotaInfo.CalculateInfo.OriginalMin, createResourceList(minCpu, minMem))
	assert.Equal(t, quotaInfo.CalculateInfo.SharedWeight.Memory().Value(), maxMem)
	assert.Equal(t, quotaInfo.CalculateInfo.SharedWeight.Cpu().Value(), maxCpu)

	//assert whether the parent quotaTree has the sub quota group
	quotaTreeWrapper := gqm.runtimeQuotaCalculatorMap[quotaName]
	assert.True(t, quotaTreeWrapper != nil)
	quotaTreeWrapper = gqm.runtimeQuotaCalculatorMap[parent]
	assert.True(t, quotaTreeWrapper != nil)
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

func AddQuotaToManager2(t *testing.T,
	gqm *GroupQuotaManager,
	name string,
	parentName string,
	maxCpu, maxMem int64,
	minCpu, minMem int64,
	scaleCpu, scaleMem int64,
	isParGroup bool) *v1alpha1.ElasticQuota {
	quota := CreateQuota2(name, parentName, maxCpu, maxMem, minCpu, minMem, scaleCpu, scaleMem, isParGroup)
	gqm.UpdateQuota(quota, false)
	quotaInfo := gqm.quotaInfoMap[name]
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, name, quotaInfo.Name)
	assert.Equal(t, createResourceList(maxCpu, maxMem), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, quotaInfo.CalculateInfo.SharedWeight.Cpu().Value(), scaleCpu)

	//assert whether the parent quotaTree has the sub quota group
	quotaTreeWrapper := gqm.runtimeQuotaCalculatorMap[name]
	assert.True(t, quotaTreeWrapper != nil)
	quotaTreeWrapper = gqm.runtimeQuotaCalculatorMap[parentName]
	assert.True(t, quotaTreeWrapper != nil)
	find, nodeInfo := quotaTreeWrapper.quotaTree[v1.ResourceCPU].find(name)
	assert.True(t, find)
	assert.Equal(t, nodeInfo.quotaName, name)
	return quota
}

func CreateQuota2(
	name string,
	parentName string,
	maxCpu, maxMem int64,
	minCpu, minMem int64,
	scaleCpu, scaleMem int64,
	isParGroup bool) *v1alpha1.ElasticQuota {
	quota := &v1alpha1.ElasticQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: make(map[string]string),
			Labels:      make(map[string]string),
		},
		Spec: v1alpha1.ElasticQuotaSpec{
			Max: createResourceList(maxCpu, maxMem),
			Min: createResourceList(minCpu, minMem),
		},
	}
	quota.Annotations[extension.AnnotationSharedWeight] = fmt.Sprintf("{\"cpu\":%v, \"memory\":\"%v\"}", scaleCpu, scaleMem)
	quota.Labels[extension.LabelQuotaParent] = parentName
	if isParGroup {
		quota.Labels[extension.LabelQuotaIsParent] = "true"
	} else {
		quota.Labels[extension.LabelQuotaIsParent] = "false"
	}
	return quota
}

func TestGroupQuotaManager_MultiChildMaxGreaterParentMax2(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	deltaRes := createResourceList(300, 8000)
	gqm.UpdateClusterTotalResource(deltaRes)

	// odps Max[600, 4096]  Min[100, 100]
	//   |-- admin Max[500, 2048]  Min[100, 100]  Req[500, 4096]
	AddQuotaToManager(t, gqm, "odps", "root", 600, 4096, 100, 100, true, true)
	AddQuotaToManager(t, gqm, "admin", "odps", 500, 2048, 100, 100, true, false)

	request := createResourceList(500, 4096)
	gqm.UpdateGroupDeltaRequest("admin", request)

	runtime := gqm.RefreshRuntime("admin")
	assert.Equal(t, createResourceList(300, 2048), runtime)
	fmt.Printf("quota1 runtime:%v\n", runtime)

	request = createResourceList(550, 4096)
	gqm.UpdateGroupDeltaRequest("admin", request)
	runtime = gqm.RefreshRuntime("admin")
	fmt.Printf("quota1 runtime:%v\n", runtime)

	quotaInfo := gqm.GetQuotaInfoByName("odps")
	assert.Equal(t, quotaInfo.CalculateInfo.Request, createResourceList(500, 2048))
	assert.Equal(t, quotaInfo.getLimitRequestNoLock(), createResourceList(500, 2048))
	assert.Equal(t, quotaInfo.CalculateInfo.Max, createResourceList(600, 4096))
	assert.Equal(t, quotaInfo.CalculateInfo.Runtime, createResourceList(300, 2048))
	quotaInfo = gqm.GetQuotaInfoByName("admin")
	assert.Equal(t, quotaInfo.CalculateInfo.Request, createResourceList(1050, 8192))
	assert.Equal(t, quotaInfo.getLimitRequestNoLock(), createResourceList(500, 2048))
	assert.Equal(t, quotaInfo.CalculateInfo.Max, createResourceList(500, 2048))
	assert.Equal(t, quotaInfo.CalculateInfo.Runtime, createResourceList(300, 2048))
}

func TestGroupQuotaManager_MultiChildMaxGreaterParentMax(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	deltaRes := createResourceList(350, 1800*GigaByte)
	gqm.UpdateClusterTotalResource(deltaRes)

	// odps Max[300, 1024]  Min[176, 756]
	//   |-- admin Max[500, 2048]  Min[100, 512]
	AddQuotaToManager(t, gqm, "odps", "root", 300, 1024*GigaByte, 176, 756*GigaByte, true, true)
	AddQuotaToManager(t, gqm, "admin", "odps", 500, 2048*GigaByte, 100, 512*GigaByte, true, false)

	// odps.Max < request < admin.Max
	request := createResourceList(400, 1500*GigaByte)
	gqm.UpdateGroupDeltaRequest("admin", request)

	quotaInfo := gqm.GetQuotaInfoByName("odps")
	assert.Equal(t, quotaInfo.CalculateInfo.Request, createResourceList(400, 1500*GigaByte))
	quotaInfo = gqm.GetQuotaInfoByName("admin")
	assert.Equal(t, quotaInfo.CalculateInfo.Request, createResourceList(400, 1500*GigaByte))

	runtime := gqm.RefreshRuntime("admin")
	assert.Equal(t, createResourceList(300, 1024*GigaByte), runtime)
	fmt.Printf("quota1 runtime:%v\n", runtime)

	gqm.UpdateGroupDeltaRequest("admin", request)
	runtime = gqm.RefreshRuntime("admin")
	assert.Equal(t, createResourceList(300, 1024*GigaByte), runtime)
	fmt.Printf("quota1 runtime:%v\n", runtime)
}

func setLoglevel(logLevel string) {
	var level klog.Level
	if err := level.Set(logLevel); err != nil {
		fmt.Printf("failed set klog.logging.verbosity %v: %v", 5, err)
	}
	fmt.Printf("successfully set klog.logging.verbosity to %v", 5)
}

func TestGroupQuotaManager_MultiUpdateQuotaRequest_WithRuntimeNegative(t *testing.T) {
	setLoglevel("5")

	gqm := NewGroupQuotaManager4Test()

	gqm.scaleMinQuotaEnabled = true
	AddQuotaToManager4(t, gqm, "odpsbatch", "root", 0, 20595894597451776, 0, 4604230207799296, 0, 19181421*1024*1024*1024, true)
	AddQuotaToManager4(t, gqm, "other", "odpsbatch", 0, 138860509003776, 0, 0, 0, 129323*1024*1024*1024, true)
	AddQuotaToManager4(t, gqm, "admin", "odpsbatch", 0, 214748364800000, 0, 42949672960000, 0, 215539*1024*1024*1024, true)
	AddQuotaToManager4(t, gqm, "monitor", "odpsbatch", 0, 104857600000, 0, 20971520000, 0, 97*1024*1024*1024, true)
	AddQuotaToManager4(t, gqm, "p-idst", "odpsbatch", 0, 1717986918400000, 0, 343597383680000, 0, 4*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "p-alimm-comm", "odpsbatch", 0, 665719930880000, 0, 146028888064000, 0, 4*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "p-alimm-fund", "odpsbatch", 0, 1245540515840000, 0, 335007449088000, 0, 4*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "p-alimm-data", "odpsbatch", 0, 1610612736000000, 0, 322122547200000, 0, 4*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "p-alimm-report", "odpsbatch", 0, 987842478080000, 0, 292057776128000, 0, 4*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "p-alimm-bnd", "odpsbatch", 0, 987842478080000, 0, 197568495616000, 0, 4*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "hy-other", "odpsbatch", 0, 171798691840000, 0, 34359738368000, 0, 99062*1024*1024*1024, true)
	AddQuotaToManager4(t, gqm, "p-lm", "odpsbatch", 0, 644245094400000, 0, 128849018880000, 0, 600000*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "is-sec-large", "odpsbatch", 0, 8589934592000, 0, 1717986918400, 0, 8000*1024*1024*1024, true)
	AddQuotaToManager4(t, gqm, "p-alimm-lazada", "odpsbatch", 0, 214748364800000, 0, 42949672960000, 0, 100000*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "tbtd-idlefish-algo", "odpsbatch", 0, 429496729600000, 0, 85899345920000, 0, 400000*1024*1024*1024, true)
	AddQuotaToManager4(t, gqm, "p-ucbu", "odpsbatch", 0, 1288490188800000, 0, 257698037760000, 0, 1200000*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "p-ntp", "odpsbatch", 0, 644245094400000, 0, 128849018880000, 0, 600000*1024*1024*1024, false)
	AddQuotaToManager4(t, gqm, "is-secdm", "odpsbatch", 0, 429496729600000, 0, 85899345920000, 0, 400000*1024*1024*1024, true)
	AddQuotaToManager4(t, gqm, "ucbu-alihealth", "odpsbatch", 0, 8589934592000, 0, 2576980377600, 0, 8000*1024*1024*1024, true)
	deltaRes := createResourceList(0, 2188867377611625)
	gqm.UpdateClusterTotalResource(deltaRes)
	requestId := make([]int64, 0)
	requestId2Name := make(map[int64]string)
	requestId2Name[1] = "other"
	requestId2Name[2] = "admin"
	requestId2Name[3] = "monitor"
	requestId2Name[4] = "p-idst"
	requestId2Name[9] = "p-alimm-comm"
	requestId2Name[11] = "p-alimm-fund"
	requestId2Name[12] = "p-alimm-data"
	requestId2Name[13] = "p-alimm-report"
	requestId2Name[14] = "p-alimm-bnd"
	requestId2Name[25] = "hy-other"
	requestId2Name[28] = "p-lm"
	requestId2Name[31] = "is-sec-large"
	requestId2Name[38] = "p-alimm-lazada"
	requestId2Name[48] = "tbtd-idlefish-algo"
	requestId2Name[49] = "p-ucbu"
	requestId2Name[58] = "p-ntp"
	requestId2Name[64] = "is-secdm"
	requestId2Name[65] = "ucbu-alihealth"
	requestId = append(requestId, 1)
	requestId = append(requestId, 2)
	requestId = append(requestId, 3)
	requestId = append(requestId, 4)
	requestId = append(requestId, 9)
	requestId = append(requestId, 11)
	requestId = append(requestId, 12)
	requestId = append(requestId, 13)
	requestId = append(requestId, 14)
	requestId = append(requestId, 25)
	requestId = append(requestId, 28)
	requestId = append(requestId, 31)
	requestId = append(requestId, 38)
	requestId = append(requestId, 48)
	requestId = append(requestId, 49)
	requestId = append(requestId, 58)
	requestId = append(requestId, 64)
	requestId = append(requestId, 65)
	requestMap := make(map[int64]v1.ResourceList)
	requestMap[1] = createResourceList(0, 81726671945728)
	requestMap[2] = createResourceList(0, 488785158602752)
	requestMap[3] = createResourceList(0, 0)
	requestMap[4] = createResourceList(0, 89567900205056)
	requestMap[9] = createResourceList(0, 121016143577088)
	requestMap[11] = createResourceList(0, 0)
	requestMap[12] = createResourceList(0, 545435053719552)
	requestMap[13] = createResourceList(0, 160012305432576)
	requestMap[14] = createResourceList(0, 57392263856128)
	requestMap[25] = createResourceList(0, 2353537220608)
	requestMap[28] = createResourceList(0, 22788471521280)
	requestMap[31] = createResourceList(0, 8589934592000)
	requestMap[38] = createResourceList(0, 35259850686464)
	requestMap[48] = createResourceList(0, 55020961660928)
	requestMap[49] = createResourceList(0, 335238393757696)
	requestMap[58] = createResourceList(0, 475682391982080)
	requestMap[64] = createResourceList(0, 46790958120960)
	requestMap[65] = createResourceList(0, 0)
	for _, quotaId := range requestId {
		name := requestId2Name[quotaId]
		klog.Infof("begin update group request:%v", name)
		gqm.UpdateGroupDeltaRequest(name, requestMap[quotaId])
	}
	runtimeId := make([]int64, 0)
	runtimeId = append(runtimeId, 1)
	runtimeId = append(runtimeId, 2)
	runtimeId = append(runtimeId, 3)
	runtimeId = append(runtimeId, 4)
	runtimeId = append(runtimeId, 9)
	runtimeId = append(runtimeId, 11)
	runtimeId = append(runtimeId, 12)
	runtimeId = append(runtimeId, 13)
	runtimeId = append(runtimeId, 14)
	runtimeId = append(runtimeId, 25)
	runtimeId = append(runtimeId, 28)
	runtimeId = append(runtimeId, 31)
	runtimeId = append(runtimeId, 38)
	runtimeId = append(runtimeId, 48)
	runtimeId = append(runtimeId, 49)
	runtimeId = append(runtimeId, 58)
	runtimeId = append(runtimeId, 64)
	runtimeId = append(runtimeId, 65)
	runtimeResult := make(map[int64]v1.ResourceList)
	for _, quotaId := range runtimeId {
		name := requestId2Name[quotaId]
		klog.Infof("begin update group runtime1:%v", name)
		runtimeResult[quotaId] = gqm.RefreshRuntime(name)
	}
	klog.Infof("begin update group runtime1:%v", "odpsbatch")
	runtimeResult[1073741824] = gqm.RefreshRuntime("odpsbatch")
	for _, quotaId := range runtimeId {
		name := requestId2Name[quotaId]
		klog.Infof("begin update group runtime2:%v", name)
		runtimeResult[quotaId] = gqm.RefreshRuntime(name)
	}
	klog.Infof("begin update group runtime2:%v", "odpabatch")
	runtimeResult[1073741824] = gqm.RefreshRuntime("odpsbatch")
	sumRuntime := v1.ResourceList{}
	for quotaId, value := range runtimeResult {
		if quotaId != 1073741824 {
			sumRuntime = quotav1.Add(sumRuntime, value)
		}
	}
	klog.Infof("1073741824:%v", runtimeResult[1073741824])
	klog.Infof("sum1111111:%v", sumRuntime)
	for quotaId, value := range runtimeResult {
		klog.Infof("quotaId:%v, request:%v, runtime:%v", quotaId, requestMap[quotaId], value)
	}
	odpsRuntime := runtimeResult[1073741824]
	if math.Abs(float64(odpsRuntime.Memory().Value())-float64(sumRuntime.Memory().Value())) > 10 {
		t.Error("error")
	}
}

func AddQuotaToManager4(t *testing.T,
	gqm *GroupQuotaManager,
	name string,
	parentName string,
	maxCpu, maxMem int64,
	minCpu, minMem int64,
	scaleCpu, scaleMem int64,
	isParGroup bool) *v1alpha1.ElasticQuota {
	quota := CreateQuota2(name, parentName, maxCpu, maxMem, minCpu, minMem, scaleCpu, scaleMem, isParGroup)
	gqm.UpdateQuota(quota, false)
	quotaInfo := gqm.quotaInfoMap[name]
	assert.True(t, quotaInfo != nil)
	assert.Equal(t, name, quotaInfo.Name)
	assert.Equal(t, createResourceList(maxCpu, maxMem), quotaInfo.CalculateInfo.Max)
	assert.Equal(t, scaleCpu, quotaInfo.CalculateInfo.SharedWeight.Cpu().Value())

	//assert whether the parent quotaTree has the sub quota group
	quotaTreeWrapper := gqm.runtimeQuotaCalculatorMap[name]
	assert.True(t, quotaTreeWrapper != nil)
	quotaTreeWrapper = gqm.runtimeQuotaCalculatorMap[parentName]
	assert.True(t, quotaTreeWrapper != nil)
	find, nodeInfo := quotaTreeWrapper.quotaTree[v1.ResourceMemory].find(name)
	assert.True(t, find)
	assert.Equal(t, nodeInfo.quotaName, name)
	return quota
}

func TestGroupQuotaManager_MultiUpdateQuotaRequest_WithRuntimeIsSmall(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	gqm.scaleMinQuotaEnabled = true
	AddQuotaToManager2(t, gqm, "odpsbatch", "root", 4797411900, 0, 1085006000, 0, 4797411900, 0, true)
	AddQuotaToManager2(t, gqm, "other", "odpsbatch", 29385900, 0, 0, 0, 29385900, 0, true)
	AddQuotaToManager2(t, gqm, "admin", "odpsbatch", 50000000, 0, 10000000, 0, 48976500, 0, true)
	AddQuotaToManager2(t, gqm, "monitor", "odpsbatch", 25000, 0, 5000, 0, 25000, 0, true)
	AddQuotaToManager2(t, gqm, "p-idst", "odpsbatch", 400000000, 0, 80000000, 0, 1000, 0, false)
	AddQuotaToManager2(t, gqm, "p-alimm-comm", "odpsbatch", 155000000, 0, 34000000, 0, 1000, 0, false)
	AddQuotaToManager2(t, gqm, "p-alimm-kgb", "odpsbatch", 5000000, 0, 1000000, 0, 1000, 0, false)
	AddQuotaToManager2(t, gqm, "p-alimm-fund", "odpsbatch", 290000000, 0, 78000000, 0, 1000, 0, false)
	AddQuotaToManager2(t, gqm, "p-alimm-data", "odpsbatch", 375000000, 0, 75000000, 0, 1000, 0, false)
	AddQuotaToManager2(t, gqm, "p-alimm-report", "odpsbatch", 230000000, 0, 68000000, 0, 1000, 0, false)
	AddQuotaToManager2(t, gqm, "p-alimm-bnd", "odpsbatch", 230000000, 0, 46000000, 0, 1000, 0, false)
	AddQuotaToManager2(t, gqm, "hy-other", "odpsbatch", 50001000, 0, 10000000, 0, 35000000, 0, true)
	AddQuotaToManager2(t, gqm, "p-lm", "odpsbatch", 150000000, 0, 30000000, 0, 150000000, 0, false)
	AddQuotaToManager2(t, gqm, "is-sec-large", "odpsbatch", 2000000, 0, 400000, 0, 2000000, 0, true)
	AddQuotaToManager2(t, gqm, "p-alimm-lazada", "odpsbatch", 50000000, 0, 10000000, 0, 25000000, 0, false)
	AddQuotaToManager2(t, gqm, "tbtd-idlefish-algo", "odpsbatch", 100000000, 0, 20000000, 0, 100000000, 0, true)
	AddQuotaToManager2(t, gqm, "p-ucbu", "odpsbatch", 300000000, 0, 60000000, 0, 300000000, 0, false)
	AddQuotaToManager2(t, gqm, "p-ntp", "odpsbatch", 150000000, 0, 30000000, 0, 150000000, 0, false)
	AddQuotaToManager2(t, gqm, "is-secdm", "odpsbatch", 100000000, 0, 20000000, 0, 100000000, 0, true)
	AddQuotaToManager2(t, gqm, "ucbu-alihealth", "odpsbatch", 2000000, 0, 600000, 0, 2000000, 0, true)
	deltaRes := createResourceList(501952056, 0)
	gqm.UpdateClusterTotalResource(deltaRes)
	requestId2Name := make(map[int64]string)
	requestId2Name[1] = "other"
	requestId2Name[2] = "admin"
	requestId2Name[3] = "monitor"
	requestId2Name[4] = "p-idst"
	requestId2Name[9] = "p-alimm-comm"
	requestId2Name[11] = "p-alimm-fund"
	requestId2Name[12] = "p-alimm-data"
	requestId2Name[13] = "p-alimm-report"
	requestId2Name[14] = "p-alimm-bnd"
	requestId2Name[25] = "hy-other"
	requestId2Name[28] = "p-lm"
	requestId2Name[31] = "is-sec-large"
	requestId2Name[38] = "p-alimm-lazada"
	requestId2Name[48] = "tbtd-idlefish-algo"
	requestId2Name[49] = "p-ucbu"
	requestId2Name[58] = "p-ntp"
	requestId2Name[64] = "is-secdm"
	requestId2Name[65] = "ucbu-alihealth"
	requestId := make([]int64, 0)
	requestId = append(requestId, 1)
	requestId = append(requestId, 2)
	requestId = append(requestId, 3)
	requestId = append(requestId, 4)
	requestId = append(requestId, 9)
	requestId = append(requestId, 10)
	requestId = append(requestId, 11)
	requestId = append(requestId, 12)
	requestId = append(requestId, 13)
	requestId = append(requestId, 14)
	requestId = append(requestId, 25)
	requestId = append(requestId, 28)
	requestId = append(requestId, 31)
	requestId = append(requestId, 38)
	requestId = append(requestId, 48)
	requestId = append(requestId, 49)
	requestId = append(requestId, 58)
	requestId = append(requestId, 64)
	requestId = append(requestId, 65)
	requestMap := make(map[int64]v1.ResourceList)
	requestMap[1] = createResourceList(13403650*1000, 0)
	requestMap[2] = createResourceList(88416850*1000, 0)
	requestMap[3] = createResourceList(0, 0)
	requestMap[4] = createResourceList(24000400*1000, 0)
	requestMap[9] = createResourceList(43898100*1000, 0)
	requestMap[10] = createResourceList(0, 0)
	requestMap[11] = createResourceList(27166300*1000, 0)
	requestMap[12] = createResourceList(111054200*1000, 0)
	requestMap[13] = createResourceList(230000000*1000, 0)
	requestMap[14] = createResourceList(18356950*1000, 0)
	requestMap[25] = createResourceList(1383700*1000, 0)
	requestMap[28] = createResourceList(12842600*1000, 0)
	requestMap[31] = createResourceList(0, 0)
	requestMap[38] = createResourceList(45107600*1000, 0)
	requestMap[48] = createResourceList(205239800*1000, 0)
	requestMap[49] = createResourceList(65080550*1000, 0)
	requestMap[58] = createResourceList(2318700*1000, 0)
	requestMap[64] = createResourceList(36123900*1000, 0)
	requestMap[65] = createResourceList(0, 0)
	for _, quotaId := range requestId {
		name := requestId2Name[quotaId]
		klog.Infof("begin update group request:%v", name)
		gqm.UpdateGroupDeltaRequest(name, requestMap[quotaId])
	}
	runtimeId := make([]int64, 0)
	runtimeId = append(runtimeId, 1)
	runtimeId = append(runtimeId, 2)
	runtimeId = append(runtimeId, 3)
	runtimeId = append(runtimeId, 4)
	runtimeId = append(runtimeId, 9)
	runtimeId = append(runtimeId, 10)
	runtimeId = append(runtimeId, 11)
	runtimeId = append(runtimeId, 12)
	runtimeId = append(runtimeId, 13)
	runtimeId = append(runtimeId, 14)
	runtimeId = append(runtimeId, 25)
	runtimeId = append(runtimeId, 28)
	runtimeId = append(runtimeId, 31)
	runtimeId = append(runtimeId, 38)
	runtimeId = append(runtimeId, 48)
	runtimeId = append(runtimeId, 49)
	runtimeId = append(runtimeId, 58)
	runtimeId = append(runtimeId, 64)
	runtimeId = append(runtimeId, 65)
	runtimeResult := make(map[int64]v1.ResourceList)
	for _, quotaId := range runtimeId {
		name := requestId2Name[quotaId]
		klog.Infof("begin update group runtime1:%v", name)
		runtimeResult[quotaId] = gqm.RefreshRuntime(name)
	}
	klog.Infof("begin update group runtime1:%v", "odpsbatch")
	runtimeResult[1073741824] = gqm.RefreshRuntime("odpsbatch")
	for _, quotaId := range runtimeId {
		name := requestId2Name[quotaId]
		klog.Infof("begin update group runtime2:%v", name)
		runtimeResult[quotaId] = gqm.RefreshRuntime(name)
	}
	klog.Infof("begin update group runtime2:%v", "odpsbatch")
	runtimeResult[1073741824] = gqm.RefreshRuntime("odpsbatch")
	sumRuntime := v1.ResourceList{}
	for quotaId, value := range runtimeResult {
		if quotaId != 1073741824 {
			sumRuntime = quotav1.Add(sumRuntime, value)
		}
	}
	klog.Infof("1073741824:%v", runtimeResult[1073741824])
	klog.Infof("sum1111111:%v", sumRuntime)
	for quotaId, value := range runtimeResult {
		klog.Infof("quotaId:%v, request:%v, runtime:%v", quotaId, requestMap[quotaId], value)
	}

	odpsRuntime := runtimeResult[1073741824]
	if math.Abs(float64(odpsRuntime.Cpu().Value())-float64(sumRuntime.Cpu().Value())) > 1 {
		t.Error("error")
	}
}

func TestGroupQuotaManager_UpdateQuotaTreeDimensionByResourceKeysBatchSetQuota(t *testing.T) {
	setLoglevel("5")
	gqm := NewGroupQuotaManager4Test()

	info3 := createQuota("3", extension.RootQuotaName)
	info3.Spec.Max["tmp"] = *resource.NewQuantity(1, resource.DecimalSI)
	res := createResourceList(1000, 10000)
	gqm.UpdateClusterTotalResource(res)
	err := gqm.UpdateQuota(info3, false)
	if err != nil {
		klog.Infof("%v", err)
	}
	assert.Equal(t, len(gqm.resourceKeys), 3)
}

func createQuota(name, parent string) *v1alpha1.ElasticQuota {
	eq := CreateQuota(name, parent, 1000, 10000, 100, 1000, true, false)
	return eq
}

func createQuota2(name, parent string, cpuMax, memMax, cpuMin, memMin int64) *v1alpha1.ElasticQuota {
	eq := CreateQuota(name, parent, cpuMax, memMax, cpuMin, memMin, true, false)
	return eq
}

func TestGroupQuotaManager_RefreshAndGetRuntimeQuotaBatchSetQuota(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()

	gqm.scaleMinQuotaEnabled = true
	cluRes := createResourceList(50, 50)
	gqm.UpdateClusterTotalResource(cluRes)

	qi1 := createQuota2("1", extension.RootQuotaName, 40, 40, 10, 10)
	qi2 := createQuota2("2", extension.RootQuotaName, 40, 40, 0, 0)

	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	assert.Equal(t, len(gqm.resourceKeys), 2)
	//case1: there is no request, runtime quota is zero
	runtime := gqm.RefreshRuntime("1")
	assert.Equal(t, runtime, createResourceList(0, 0))

	//case2: no existed group
	assert.Nil(t, gqm.RefreshRuntime("5"))

	gqm.UpdateGroupDeltaRequest("1", createResourceList(5, 5))
	gqm.UpdateGroupDeltaRequest("2", createResourceList(5, 5))
	gq1 := gqm.GetQuotaInfoByName("1")
	gq2 := gqm.GetQuotaInfoByName("2")

	//case3: version is same, should not update
	gq1.RuntimeVersion = gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].globalRuntimeVersion
	gq2.RuntimeVersion = gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].globalRuntimeVersion
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(0, 0))
	assert.Equal(t, gqm.RefreshRuntime("2"), v1.ResourceList{})

	//case4: version is different, should update
	gq1.RuntimeVersion = 0
	gq2.RuntimeVersion = 0
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(5, 5))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(5, 5))

	//case5: request is larger than min
	gqm.UpdateGroupDeltaRequest("1", createResourceList(25, 25))
	gqm.UpdateGroupDeltaRequest("2", createResourceList(25, 25))
	//1 min [10,10] -> toPartitionRes [40,40] -> runtime [30,30]
	//2 min [0,0]	-> toPartitionRes [40,40] -> runtime [20,20]
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(30, 30))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(20, 20))
}

func TestGroupQuotaManager_UpdateSharedWeightBatchSetQuota(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(60, 60))

	//case1: if do not config SharedWeight, equal to maxQuota
	qi1 := createQuota2("1", extension.RootQuotaName, 40, 40, 10, 10)
	qi2 := createQuota2("2", extension.RootQuotaName, 40, 40, 0, 0)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	assert.Equal(t, gqm.quotaInfoMap["1"].CalculateInfo.SharedWeight.Cpu().Value(), int64(40))
	assert.Equal(t, gqm.quotaInfoMap["2"].CalculateInfo.SharedWeight.Cpu().Value(), int64(40))

	//case2: if config SharedWeight
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

func TestGroupQuotaManager_DoUpdateOneGroupMaxQuotaBatchSetQuota(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := createQuota2("1", extension.RootQuotaName, 40, 40, 10, 10)
	qi2 := createQuota2("2", extension.RootQuotaName, 40, 40, 10, 10)

	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	//case1: req < max, req > min
	gqm.UpdateGroupDeltaRequest("1", createResourceList(35, 35))
	gqm.UpdateGroupDeltaRequest("2", createResourceList(35, 35))

	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(25, 25))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(25, 25))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(35, 35))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(35, 35))

	//case2: decrease max, req > max ,req > min
	qi1.Spec.Max = createResourceList(30, 30)
	qi2.Spec.Max = createResourceList(30, 30)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(25, 25))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(25, 25))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(30, 30))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(30, 30))

	//case3: increase max, req < max, req > min
	qi1.Spec.Max = createResourceList(50, 50)
	qi2.Spec.Max = createResourceList(50, 50)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(25, 25))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(25, 25))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(35, 35))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(35, 35))
}

func TestGroupQuotaManager_DoUpdateOneGroupMinQuotaBatchSetQuota(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := createQuota2("1", extension.RootQuotaName, 40, 40, 10, 10)
	qi2 := createQuota2("2", extension.RootQuotaName, 40, 40, 10, 10)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	//case1: req < max, req > min
	gqm.UpdateGroupDeltaRequest("1", createResourceList(15, 15))
	gqm.UpdateGroupDeltaRequest("2", createResourceList(15, 15))

	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(15, 15))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(15, 15))

	//case2: increase min, req = min
	qi1.Spec.Min = createResourceList(15, 15)
	qi2.Spec.Min = createResourceList(15, 15)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(15, 15))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(15, 15))

	//case3: increase min, req < min
	qi1.Spec.Min = createResourceList(20, 20)
	qi2.Spec.Min = createResourceList(20, 20)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(15, 15))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(15, 15))

	//case4: decrease min, req < max ,req > min
	qi1.Spec.Min = createResourceList(5, 5)
	qi2.Spec.Min = createResourceList(5, 5)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(15, 15))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(15, 15))

	//case5: update min, min == max, request > max
	qi1.Spec.Min = createResourceList(40, 40)
	qi2.Spec.Min = createResourceList(5, 5)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)

	gqm.UpdateGroupDeltaRequest("1", createResourceList(100, 100))
	gqm.UpdateGroupDeltaRequest("2", createResourceList(100, 100))
	assert.Equal(t, gqm.RefreshRuntime("1"), createResourceList(40, 40))
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(10, 10))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["1"], createResourceList(40, 40))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].groupReqLimit["2"], createResourceList(40, 40))
}

func TestGroupQuotaManager_DoDeleteOneGroupBatchSetQuota(t *testing.T) {
	gqm := NewGroupQuotaManager4Test()
	gqm.scaleMinQuotaEnabled = true

	gqm.UpdateClusterTotalResource(createResourceList(50, 50))

	qi1 := createQuota2("1", extension.RootQuotaName, 40, 40, 10, 10)
	q1 := CreateQuota("1", extension.RootQuotaName, 40, 40, 10, 10, true, false)
	qi2 := createQuota2("2", extension.RootQuotaName, 40, 40, 10, 10)
	gqm.UpdateQuota(qi1, false)
	gqm.UpdateQuota(qi2, false)
	gqm.UpdateGroupDeltaRequest("1", createResourceList(15, 15))
	gqm.UpdateGroupDeltaRequest("2", createResourceList(15, 15))

	//case1: delete one group
	gqm.UpdateQuota(q1, true)
	assert.Equal(t, gqm.RefreshRuntime("2"), createResourceList(15, 15))
	assert.Equal(t, gqm.runtimeQuotaCalculatorMap[extension.RootQuotaName].totalResource, createResourceList(50, 50))
	assert.Nil(t, gqm.quotaInfoMap["1"])
}

func NewGroupQuotaManager4Test() *GroupQuotaManager {
	quotaManager := &GroupQuotaManager{
		totalResourceExceptSystemAndDefaultUsed: v1.ResourceList{},
		totalResource:                           v1.ResourceList{},
		resourceKeys:                            make(map[v1.ResourceName]struct{}),
		quotaInfoMap:                            make(map[string]*QuotaInfo),
		runtimeQuotaCalculatorMap:               make(map[string]*RuntimeQuotaCalculator),
		scaleMinQuotaWhenOverRootResManager:     NewScaleMinQuotaWhenOverRootResManager(),
		quotaTopoNodeMap:                        make(map[string]*QuotaTopoNode),
	}
	quotaManager.quotaInfoMap[extension.SystemQuotaName] = NewQuotaInfo(false, true, extension.SystemQuotaName, "")
	quotaManager.quotaInfoMap[extension.DefaultQuotaName] = NewQuotaInfo(false, true, extension.DefaultQuotaName, "")
	quotaManager.runtimeQuotaCalculatorMap[extension.RootQuotaName] = NewRuntimeQuotaCalculator(extension.RootQuotaName)
	return quotaManager
}
