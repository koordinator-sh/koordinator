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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v12 "k8s.io/apiserver/pkg/quota/v1"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func TestQuotaInfo_GetLimitRequest(t *testing.T) {
	max := createResourceList(100, 10000)
	req := createResourceList(1000, 1000)
	quotaInfo := &QuotaInfo{
		CalculateInfo: QuotaCalculateInfo{
			Max:     max,
			Request: req,
		},
	}
	assertObj := assert.New(t)
	assertObj.Equal(*resource.NewQuantity(100, resource.DecimalSI), quotaInfo.getLimitRequestNoLock()[corev1.ResourceCPU])
	assertObj.Equal(*resource.NewQuantity(1000, resource.DecimalSI), quotaInfo.getLimitRequestNoLock()[corev1.ResourceMemory])

	req2 := createResourceList(100, 1000)
	quotaInfo.addRequestNonNegativeNoLock(req2)
	assertObj.Equal(*resource.NewQuantity(2000, resource.DecimalSI), quotaInfo.getLimitRequestNoLock()[corev1.ResourceMemory])
}

func TestQuotaInfo_AddRequestNonNegativeNoLock(t *testing.T) {
	req1 := createResourceList(-100, -100)
	quotaInfo := &QuotaInfo{
		CalculateInfo: QuotaCalculateInfo{
			Request: createResourceList(50, 50),
			Used:    createResourceList(40, 40),
		},
	}
	quotaInfo.addRequestNonNegativeNoLock(req1)
	quotaInfo.addUsedNonNegativeNoLock(req1)
	assert.Equal(t, quotaInfo.CalculateInfo.Request, createResourceList(0, 0))
	assert.Equal(t, quotaInfo.CalculateInfo.Used, createResourceList(0, 0))
}

func TestNewQuotaInfoFromQuota(t *testing.T) {
	eQ := createElasticQuota()
	quotaInfo := NewQuotaInfoFromQuota(eQ)
	if !quotaInfo.AllowLentResource ||
		!quotaInfo.IsParent ||
		quotaInfo.Name != "testQuota" ||
		quotaInfo.ParentName != "test_parent" {
		t.Error("error")
	}
	assert.Equal(t, quotaInfo.CalculateInfo.OriginalMin, createResourceList(100, 1000))
	assert.Equal(t, quotaInfo.CalculateInfo.Max, createResourceList(1000, 10000))
	if !v12.Equals(quotaInfo.CalculateInfo.SharedWeight, createResourceList(10, 100)) {
		t.Error("error")
	}
	quotaInfo.CalculateInfo.OriginalMin["test"] = *resource.NewQuantity(1, resource.DecimalSI)
	if v12.Equals(quotaInfo.CalculateInfo.OriginalMin, createResourceList(100, 1000)) {
		t.Error("error")
	}
	delete(quotaInfo.CalculateInfo.Max, corev1.ResourceCPU)
	if v12.Equals(quotaInfo.CalculateInfo.Max, createResourceList(1000, 10000)) {
		t.Error("error")
	}
	quotaInfo.CalculateInfo.Max["test"] = *resource.NewQuantity(1, resource.DecimalSI)
	if v12.Equals(quotaInfo.CalculateInfo.Max, createResourceList(1000, 10000)) {
		t.Error("error")
	}
}

// createResourceList builds a small resource list of core resources
func createResourceList(cpu int64, memory int64) corev1.ResourceList {
	resourceList := make(map[corev1.ResourceName]resource.Quantity)
	resourceList[corev1.ResourceCPU] = *resource.NewQuantity(cpu, resource.DecimalSI)
	resourceList[corev1.ResourceMemory] = *resource.NewQuantity(memory, resource.DecimalSI)
	return resourceList
}

func createElasticQuota() *v1alpha1.ElasticQuota {
	eQ := &v1alpha1.ElasticQuota{
		Spec: v1alpha1.ElasticQuotaSpec{
			Min: createResourceList(100, 1000),
			Max: createResourceList(1000, 10000),
		},
		ObjectMeta: v1.ObjectMeta{
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			Name:        "testQuota",
		},
		TypeMeta: v1.TypeMeta{
			Kind: "test",
		},
	}
	eQ.Labels[extension.LabelQuotaIsParent] = "true"
	eQ.Labels[extension.LabelQuotaParent] = "test_parent"
	eQ.Labels[extension.LabelAllowLentResource] = "true"
	SharedWeight, _ := json.Marshal(createResourceList(10, 100))
	eQ.Annotations[extension.AnnotationSharedWeight] = string(SharedWeight)
	return eQ
}

func TestRuntimeQuotaCalculator_Iteration4AdjustQuota(t *testing.T) {
	qtw := NewRuntimeQuotaCalculator("testTreeName")
	resourceKey := make(map[corev1.ResourceName]struct{})
	cpu := corev1.ResourceCPU
	resourceKey[cpu] = struct{}{}
	qtw.UpdateResourceKeys(resourceKey)
	qtw.quotaTree[cpu].insert("node1", 40, 5, 10, true)
	qtw.quotaTree[cpu].insert("node2", 60, 20, 15, true)
	qtw.quotaTree[cpu].insert("node3", 50, 40, 20, true)
	qtw.quotaTree[cpu].insert("node4", 80, 70, 15, true)
	qtw.totalResource = corev1.ResourceList{}
	qtw.totalResource[corev1.ResourceCPU] = *resource.NewQuantity(100, resource.DecimalSI)
	qtw.calculateRuntimeNoLock()
	if qtw.globalRuntimeVersion == 0 {
		t.Error("error")
	}
	if qtw.quotaTree[cpu].quotaNodes["node1"].runtimeQuota != 5 ||
		qtw.quotaTree[cpu].quotaNodes["node2"].runtimeQuota != 20 ||
		qtw.quotaTree[cpu].quotaNodes["node3"].runtimeQuota != 35 ||
		qtw.quotaTree[cpu].quotaNodes["node4"].runtimeQuota != 40 {
		t.Error("error")
	}

}

func createQuotaInfoWithRes(name string, max, min corev1.ResourceList) *QuotaInfo {
	quotaInfo := NewQuotaInfo(true, true, name, "")
	quotaInfo.CalculateInfo.Max = max.DeepCopy()
	quotaInfo.CalculateInfo.AutoScaleMin = min.DeepCopy()
	quotaInfo.CalculateInfo.SharedWeight = max.DeepCopy()
	return quotaInfo
}

func createRuntimeQuotaCalculator() *RuntimeQuotaCalculator {
	qtw := NewRuntimeQuotaCalculator("0")
	resKeys := make(map[corev1.ResourceName]struct{})
	resKeys[corev1.ResourceCPU] = struct{}{}
	resKeys[corev1.ResourceMemory] = struct{}{}

	qtw.UpdateResourceKeys(resKeys)
	return qtw
}

func TestRuntimeQuotaCalculator_UpdateResourceKeys(t *testing.T) {
	assertObj := assert.New(t)
	qtw := NewRuntimeQuotaCalculator("0")
	resKeys := make(map[corev1.ResourceName]struct{})
	resKeys[corev1.ResourceCPU] = struct{}{}
	resKeys[corev1.ResourceMemory] = struct{}{}

	qtw.UpdateResourceKeys(resKeys)
	assertObj.Equal(len(resKeys), len(qtw.resourceKeys), "UpdateResourceKeys failed")
	assertObj.Equal(len(resKeys), len(qtw.quotaTree), "update quota tree failed")
	_, exist := qtw.resourceKeys[corev1.ResourceCPU]
	assertObj.True(exist, "update quota tree failed")

	delete(resKeys, corev1.ResourceMemory)
	assertObj.Equal(2, len(qtw.resourceKeys), "UpdateResourceKeys failed")
	qtw.UpdateResourceKeys(resKeys)
	assertObj.Equal(len(resKeys), len(qtw.resourceKeys), "UpdateResourceKeys failed")
	assertObj.Equal(len(resKeys), len(qtw.quotaTree), "update quota tree failed")
	_, exist = qtw.resourceKeys[corev1.ResourceCPU]
	assertObj.True(exist, "update quota tree failed")

	resKeys[corev1.ResourceEphemeralStorage] = struct{}{}
	qtw.UpdateResourceKeys(resKeys)
	assertObj.Equal(len(resKeys), len(qtw.resourceKeys), "UpdateResourceKeys failed")
	assertObj.Equal(len(resKeys), len(qtw.quotaTree), "update quota tree failed")
	_, exist = qtw.resourceKeys[corev1.ResourceEphemeralStorage]
	assertObj.True(exist, "update quota tree failed")
}

func TestRuntimeQuotaCalculator_DeleteOneGroup(t *testing.T) {
	max := createResourceList(100, 1000)
	min := createResourceList(70, 7000)
	quotaInfo := createQuotaInfoWithRes("aliyun", max, min)
	qtw := createRuntimeQuotaCalculator()

	quotaInfo.setMaxQuotaNoLock(max)
	qtw.UpdateOneGroupMaxQuota(quotaInfo)
	assert.Equal(t, len(max), len(qtw.resourceKeys))
	assert.Equal(t, 1, len(qtw.groupReqLimit))
	assert.Equal(t, 1, len(qtw.quotaTree["cpu"].quotaNodes))

	qtw.DeleteOneGroup(quotaInfo)
	assert.Equal(t, len(max), len(qtw.resourceKeys))
	assert.Equal(t, 0, len(qtw.groupReqLimit))
	assert.Equal(t, 0, len(qtw.quotaTree["cpu"].quotaNodes))
}

func TestRuntimeQuotaCalculator_UpdateOneGroupMaxQuota(t *testing.T) {
	max := createResourceList(100, 1000)
	min := createResourceList(70, 7000)
	quotaInfo := createQuotaInfoWithRes("aliyun", max, min)
	qtw := createRuntimeQuotaCalculator()
	quotaInfo.setMaxQuotaNoLock(max)
	qtw.UpdateOneGroupMaxQuota(quotaInfo)

	assert.Equal(t, len(max), len(qtw.resourceKeys))
	assert.Equal(t, int64(2), qtw.globalRuntimeVersion)
	assert.Equal(t, 2, len(qtw.quotaTree))
	_, exist := qtw.quotaTree["cpu"].quotaNodes["aliyun"]
	assert.True(t, exist)

	newMax := createResourceList(200, 9000)
	request := createResourceList(30, 3000)
	quotaInfo.addRequestNonNegativeNoLock(request)
	assert.Equal(t, request, quotaInfo.CalculateInfo.Request)

	qtw.SetClusterTotalResource(max)
	assert.Equal(t, max, qtw.totalResource)

	quotaInfo.setMaxQuotaNoLock(newMax)
	qtw.groupReqLimit[quotaInfo.Name] = request.DeepCopy()
	qtw.UpdateOneGroupMaxQuota(quotaInfo)
	assert.Equal(t, request, qtw.groupReqLimit[quotaInfo.Name])
	assert.Equal(t, max, qtw.totalResource)
}

func TestRuntimeQuotaCalculator_UpdateOneGroupMinQuota(t *testing.T) {
	assertObj := assert.New(t)
	max := createResourceList(100, 10000)
	minQuota := createResourceList(70, 7000)
	quotaInfo := createQuotaInfoWithRes("aliyun", max, minQuota)

	// totalRequest = request = min,  totalResource = max
	quotaInfo.CalculateInfo.Request = minQuota.DeepCopy()
	qtw := createRuntimeQuotaCalculator()
	qtw.groupReqLimit[quotaInfo.Name] = minQuota
	qtw.SetClusterTotalResource(max)
	quotaInfo.setAutoScaleMinQuotaNoLock(minQuota)
	qtw.UpdateOneGroupMinQuota(quotaInfo)

	assertObj.Equal(2, len(qtw.resourceKeys))
	assertObj.Equal(max.Name(corev1.ResourceCPU, resource.DecimalSI), qtw.totalResource.Name(corev1.ResourceCPU, resource.DecimalSI))
	assertObj.Equal(max.Name(corev1.ResourceMemory, resource.DecimalSI), qtw.totalResource.Name(corev1.ResourceMemory, resource.DecimalSI))
	qtw.UpdateOneGroupRuntimeQuota(quotaInfo)
	assertObj.Equal(qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].runtimeQuota, int64(70))
	assertObj.Equal(qtw.quotaTree["memory"].quotaNodes[quotaInfo.Name].runtimeQuota, int64(7000))
	assertObj.Equal(qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].min, int64(70))

	newMin := createResourceList(50, 5000)
	quotaInfo.setAutoScaleMinQuotaNoLock(newMin)
	qtw.UpdateOneGroupMinQuota(quotaInfo)
	qtw.UpdateOneGroupRuntimeQuota(quotaInfo)
	assertObj.Equal(qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].runtimeQuota, int64(70))
	assertObj.Equal(qtw.quotaTree["memory"].quotaNodes[quotaInfo.Name].runtimeQuota, int64(7000))
	assertObj.Equal(qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].min, int64(50))
}

func TestRuntimeQuotaCalculator_UpdateOneGroupSharedWeight(t *testing.T) {
	max := createResourceList(100, 1000)
	min := createResourceList(70, 7000)
	quotaInfo := createQuotaInfoWithRes("aliyun", max, min)
	qtw := createRuntimeQuotaCalculator()

	qtw.UpdateOneGroupSharedWeight(quotaInfo)
	maxCpu := max["cpu"]
	assert.Equal(t, maxCpu.Value(), qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].sharedWeight)

	sharedWeight := createResourceList(60, 6000)
	quotaInfo.setSharedWeightNoLock(sharedWeight)
	qtw.UpdateOneGroupSharedWeight(quotaInfo)
	sharedCpu := sharedWeight["cpu"]
	assert.Equal(t, sharedCpu.Value(), qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].sharedWeight)

	sharedWeight = createResourceList(120, 12000)
	quotaInfo.setSharedWeightNoLock(sharedWeight)
	qtw.UpdateOneGroupSharedWeight(quotaInfo)
	sharedCpu = sharedWeight["cpu"]
	assert.Equal(t, sharedCpu.Value(), qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].sharedWeight)
}

func TestRuntimeQuotaCalculator_NeedUpdateOneGroupRequest(t *testing.T) {
	max := createResourceList(100, 1000)
	min := createResourceList(70, 7000)
	quotaInfo := createQuotaInfoWithRes("aliyun", max, min)
	qtw := createRuntimeQuotaCalculator()

	update := qtw.NeedUpdateOneGroupRequest(quotaInfo)
	assert.False(t, update)

	quotaInfo.CalculateInfo.Request = min.DeepCopy()
	update = qtw.NeedUpdateOneGroupRequest(quotaInfo)
	assert.True(t, update)
}

func TestRuntimeQuotaCalculator_UpdateOneGroupRequest(t *testing.T) {
	qtw := createRuntimeQuotaCalculator()
	totalResource := createResourceList(50, 5000)
	qtw.SetClusterTotalResource(totalResource)
	quotaCount := 5
	for i := 1; i <= quotaCount; i++ {
		max := createResourceList(int64(i*100), int64(i*10000))
		min := createResourceList(int64(i*80), int64(i*8000))
		request := createResourceList(int64(i*10), int64(i*1000))
		quotaName := fmt.Sprintf("aliyun-%d", i)
		quotaInfo := createQuotaInfoWithRes(quotaName, max, min)
		quotaInfo.addRequestNonNegativeNoLock(request)

		qtw.UpdateOneGroupMaxQuota(quotaInfo)
		qtw.UpdateOneGroupMinQuota(quotaInfo)
		qtw.UpdateOneGroupSharedWeight(quotaInfo)
		qtw.UpdateOneGroupRequest(quotaInfo)

		reqLimit := qtw.getGroupRequestLimitNoLock(quotaInfo.Name)
		assert.Equal(t, reqLimit, request)

		qtw.UpdateOneGroupRuntimeQuota(quotaInfo)
		// request < min,  runtime == request
		assert.Equal(t, quotaInfo.CalculateInfo.Runtime, quotaInfo.CalculateInfo.Request)
	}
}

func TestRuntimeQuotaCalculator_UpdateOneGroupRuntimeQuota(t *testing.T) {
	qtw := createRuntimeQuotaCalculator()
	totalResource := createResourceList(100, 1000)
	qtw.SetClusterTotalResource(totalResource)

	// aliMM max[80, 800], min[60, 600], request[0, 0], runtime[0, 0]
	// aliYun max[100, 1000], min[50, 500], request[90, 900], runtime[90, 900]
	max := createResourceList(80, 800)
	min := createResourceList(60, 600)
	sharedWeight := createResourceList(1, 1)
	aliMM := createQuotaInfoWithRes("alimama", max, min)
	updateQuotaInfo(qtw, aliMM, max, min, sharedWeight)

	max = createResourceList(100, 1000)
	min = createResourceList(50, 500)
	request := createResourceList(90, 900)
	aliYun := createQuotaInfoWithRes("aliyun", max, min)
	aliYun.CalculateInfo.Request = request.DeepCopy()
	updateQuotaInfo(qtw, aliYun, max, min, sharedWeight)

	qtw.UpdateOneGroupRequest(aliYun)
	qtw.UpdateOneGroupRuntimeQuota(aliMM)
	qtw.UpdateOneGroupRuntimeQuota(aliYun)
	assert.Equal(t, totalResource, qtw.totalResource)
	assert.Equal(t, 2, len(qtw.quotaTree))
	assert.Equal(t, int64(0), aliMM.CalculateInfo.Runtime.Name("cpu", resource.DecimalSI).Value())
	assert.Equal(t, int64(0), aliMM.CalculateInfo.Runtime.Name("memory", resource.DecimalSI).Value())
	assert.Equal(t, request, aliYun.CalculateInfo.Runtime)

	// aliMM max[80, 800], min[60, 600], request[30, 300], runtime[30, 300]
	// aliYun max[100, 1000], min[50, 500], request[90, 900], runtime[70, 700]
	request = createResourceList(30, 300)
	aliMM.CalculateInfo.Request = request.DeepCopy()
	qtw.UpdateOneGroupRequest(aliMM)
	qtw.UpdateOneGroupRuntimeQuota(aliMM)
	qtw.UpdateOneGroupRuntimeQuota(aliYun)

	assert.Equal(t, request, aliMM.CalculateInfo.Runtime)
	assert.Equal(t, v12.Subtract(totalResource, request), aliYun.CalculateInfo.Runtime)

	// aliMM max[80, 800], min[60, 600], request[60, 600], runtime[60, 600]
	// aliYun max[100, 1000], min[50, 500], request[90, 900], runtime[50, 500]
	request = createResourceList(60, 600)
	aliMM.CalculateInfo.Request = request.DeepCopy()
	qtw.UpdateOneGroupRequest(aliMM)
	qtw.UpdateOneGroupRuntimeQuota(aliMM)

	assert.Equal(t, request, aliMM.CalculateInfo.Runtime)

	qtw.UpdateOneGroupRuntimeQuota(aliYun)
	assert.Equal(t, aliYun.CalculateInfo.AutoScaleMin, aliYun.CalculateInfo.Runtime)
}

func TestRuntimeQuotaCalculator_UpdateOneGroupRuntimeQuota2(t *testing.T) {
	qtw := createRuntimeQuotaCalculator()
	totalResource := createResourceList(120, 1200)
	qtw.SetClusterTotalResource(totalResource)

	max := createResourceList(80, 800)
	min := createResourceList(50, 500)
	sharedWeight := createResourceList(1, 1)
	aliMM := createQuotaInfoWithRes("alimama", max, min)
	updateQuotaInfo(qtw, aliMM, max, min, sharedWeight)
	request := createResourceList(100, 1000)
	aliMM.CalculateInfo.Request = request.DeepCopy()

	qtw.UpdateOneGroupRequest(aliMM)
	qtw.UpdateOneGroupRuntimeQuota(aliMM)

	assert.Equal(t, totalResource, qtw.totalResource)
	assert.Equal(t, max, aliMM.CalculateInfo.Runtime)

	max = createResourceList(100, 1000)
	min = createResourceList(50, 500)
	aliYun := createQuotaInfoWithRes("aliyun", max, min)
	updateQuotaInfo(qtw, aliYun, max, min, sharedWeight)
	request = createResourceList(150, 1500)
	aliYun.CalculateInfo.Request = request.DeepCopy()
	qtw.UpdateOneGroupRequest(aliYun)

	qtw.UpdateOneGroupRuntimeQuota(aliYun)
	qtw.UpdateOneGroupRuntimeQuota(aliMM)

	assert.Equal(t, aliMM.CalculateInfo.Runtime, createResourceList(60, 600))
	assert.Equal(t, aliYun.CalculateInfo.Runtime, createResourceList(60, 600))
}

func updateQuotaInfo(wrapper *RuntimeQuotaCalculator, info *QuotaInfo, max, min, sharedWeight corev1.ResourceList) {
	info.setMaxQuotaNoLock(max)
	wrapper.UpdateOneGroupMaxQuota(info)
	info.setAutoScaleMinQuotaNoLock(min)
	wrapper.UpdateOneGroupMinQuota(info)
	info.setSharedWeightNoLock(sharedWeight)
	wrapper.UpdateOneGroupSharedWeight(info)
}

func TestQuotaInfo_GetRuntime(t *testing.T) {
	qi := &QuotaInfo{
		Name: "3",
		CalculateInfo: QuotaCalculateInfo{
			Max: createResourceList(100, 200),
			Runtime: corev1.ResourceList{
				"GPU": *resource.NewQuantity(20, resource.DecimalSI),
				"cpu": *resource.NewQuantity(10, resource.DecimalSI),
			},
		},
	}
	assert.Equal(t, qi.getMaskedRuntimeNoLock(), corev1.ResourceList{
		"cpu": *resource.NewQuantity(10, resource.DecimalSI),
	})
}
