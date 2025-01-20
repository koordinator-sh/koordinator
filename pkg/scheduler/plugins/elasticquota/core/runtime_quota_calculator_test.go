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

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

const (
	TestNode1 = "node1"
	TestNode2 = "node2"
	TestNode3 = "node3"
	TestNode4 = "node4"
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
	assertObj.Equal(*resource.NewMilliQuantity(100000, resource.DecimalSI), quotaInfo.getLimitRequestNoLock()[corev1.ResourceCPU])
	assertObj.Equal(*resource.NewQuantity(1000, resource.BinarySI), quotaInfo.getLimitRequestNoLock()[corev1.ResourceMemory])

	req2 := createResourceList(100, 1000)
	quotaInfo.addRequestNonNegativeNoLock(req2, req2, false)
	assertObj.Equal(*resource.NewQuantity(2000, resource.BinarySI), quotaInfo.getLimitRequestNoLock()[corev1.ResourceMemory])
}

func TestQuotaInfo_AddRequestNonNegativeNoLock(t *testing.T) {
	req1 := createResourceList(-100, -100)
	quotaInfo := &QuotaInfo{
		CalculateInfo: QuotaCalculateInfo{
			Request: createResourceList(50, 50),
			Used:    createResourceList(40, 40),
		},
	}
	quotaInfo.addRequestNonNegativeNoLock(req1, req1, false)
	quotaInfo.addUsedNonNegativeNoLock(req1, createResourceList(0, 0), false)
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
	assert.Equal(t, quotaInfo.CalculateInfo.Min, createResourceList(100, 1000))
	assert.Equal(t, quotaInfo.CalculateInfo.Max, createResourceList(1000, 10000))
	if !v12.Equals(quotaInfo.CalculateInfo.SharedWeight, createResourceList(10, 100)) {
		t.Error("error")
	}
	quotaInfo.CalculateInfo.Min["test"] = *resource.NewQuantity(1, resource.DecimalSI)
	if v12.Equals(quotaInfo.CalculateInfo.Min, createResourceList(100, 1000)) {
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
	resourceList[corev1.ResourceCPU] = *resource.NewMilliQuantity(cpu*1000, resource.DecimalSI)
	resourceList[corev1.ResourceMemory] = *resource.NewQuantity(memory, resource.BinarySI)
	return resourceList
}

func createResourceList2(cpu int64, memory int64) corev1.ResourceList {
	resourceList := make(map[corev1.ResourceName]resource.Quantity)
	resourceList[corev1.ResourceCPU] = *resource.NewMilliQuantity(cpu, resource.DecimalSI)
	resourceList[corev1.ResourceMemory] = *resource.NewQuantity(memory, resource.BinarySI)
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

func TestRuntimeQuotaCalculator_IterationAdjustQuota(t *testing.T) {
	type quotaNodeInfo = struct {
		groupName         string
		sharedWeight      int64
		request           int64
		min               int64
		guarantee         int64
		allowLentResource bool
	}
	node1 := &quotaNodeInfo{
		groupName:         TestNode1,
		sharedWeight:      40,
		request:           5,
		min:               10,
		guarantee:         0,
		allowLentResource: true,
	}
	node2 := &quotaNodeInfo{
		groupName:         TestNode2,
		sharedWeight:      60,
		request:           20,
		min:               15,
		guarantee:         0,
		allowLentResource: true,
	}
	node3 := &quotaNodeInfo{
		groupName:         TestNode3,
		sharedWeight:      50,
		request:           40,
		min:               20,
		guarantee:         0,
		allowLentResource: true,
	}
	node4 := &quotaNodeInfo{
		groupName:         TestNode4,
		sharedWeight:      80,
		request:           70,
		min:               15,
		guarantee:         0,
		allowLentResource: true,
	}
	node4_1 := &quotaNodeInfo{
		groupName:         TestNode4,
		sharedWeight:      0,
		request:           70,
		min:               15,
		guarantee:         0,
		allowLentResource: true,
	}
	node4_2 := &quotaNodeInfo{
		groupName:         TestNode4,
		sharedWeight:      0,
		request:           70,
		min:               15,
		guarantee:         45,
		allowLentResource: true,
	}

	testCases := []struct {
		name              string
		totalResource     corev1.ResourceList
		nodes             []*quotaNodeInfo
		expectedRuntimeMp map[string]map[corev1.ResourceName]int64
	}{
		{
			name: "case1-no-guarantee",
			totalResource: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
			},
			nodes: []*quotaNodeInfo{node1, node2, node3, node4},
			expectedRuntimeMp: map[string]map[corev1.ResourceName]int64{
				TestNode1: {corev1.ResourceCPU: 5},
				TestNode2: {corev1.ResourceCPU: 20},
				TestNode3: {corev1.ResourceCPU: 35},
				TestNode4: {corev1.ResourceCPU: 40},
			},
		},
		{
			name: "case2-node4.sharedWeight=0",
			totalResource: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
			},
			nodes: []*quotaNodeInfo{node1, node2, node3, node4_1},
			expectedRuntimeMp: map[string]map[corev1.ResourceName]int64{
				TestNode1: {corev1.ResourceCPU: 5},
				TestNode2: {corev1.ResourceCPU: 20},
				TestNode3: {corev1.ResourceCPU: 40},
				TestNode4: {corev1.ResourceCPU: 15},
			},
		},
		{
			name: "case3-node4.guarantee>min",
			totalResource: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
			},
			nodes: []*quotaNodeInfo{node1, node2, node3, node4_2},
			expectedRuntimeMp: map[string]map[corev1.ResourceName]int64{
				TestNode1: {corev1.ResourceCPU: 5},
				TestNode2: {corev1.ResourceCPU: 20},
				TestNode3: {corev1.ResourceCPU: 30},
				TestNode4: {corev1.ResourceCPU: 45},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			qtw := NewRuntimeQuotaCalculator("testTreeName")
			resourceKey := make(map[corev1.ResourceName]struct{})
			for key, value := range tc.totalResource {
				if !value.IsZero() {
					resourceKey[key] = struct{}{}
				}
			}
			qtw.updateResourceKeys(resourceKey)
			qtw.totalResource = tc.totalResource
			for _, node := range tc.nodes {
				for resKey := range resourceKey {
					qtw.quotaTree[resKey].insert(node.groupName, node.sharedWeight, node.request, node.min, node.guarantee, node.allowLentResource)
				}
			}
			qtw.calculateRuntimeNoLock()
			for node, rq := range tc.expectedRuntimeMp {
				for resKey, q := range rq {
					assert.Equal(t, q, qtw.quotaTree[resKey].quotaNodes[node].runtimeQuota)
				}
			}
		})
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

	qtw.updateResourceKeys(resKeys)
	return qtw
}

func TestRuntimeQuotaCalculator_UpdateResourceKeys(t *testing.T) {
	assertObj := assert.New(t)
	qtw := NewRuntimeQuotaCalculator("0")
	resKeys := make(map[corev1.ResourceName]struct{})
	resKeys[corev1.ResourceCPU] = struct{}{}
	resKeys[corev1.ResourceMemory] = struct{}{}

	qtw.updateResourceKeys(resKeys)
	assertObj.Equal(len(resKeys), len(qtw.resourceKeys), "UpdateResourceKeys failed")
	assertObj.Equal(len(resKeys), len(qtw.quotaTree), "update quota tree failed")
	_, exist := qtw.resourceKeys[corev1.ResourceCPU]
	assertObj.True(exist, "update quota tree failed")

	delete(resKeys, corev1.ResourceMemory)
	assertObj.Equal(2, len(qtw.resourceKeys), "UpdateResourceKeys failed")
	qtw.updateResourceKeys(resKeys)
	assertObj.Equal(len(resKeys), len(qtw.resourceKeys), "UpdateResourceKeys failed")
	assertObj.Equal(len(resKeys), len(qtw.quotaTree), "update quota tree failed")
	_, exist = qtw.resourceKeys[corev1.ResourceCPU]
	assertObj.True(exist, "update quota tree failed")

	resKeys[corev1.ResourceEphemeralStorage] = struct{}{}
	qtw.updateResourceKeys(resKeys)
	assertObj.Equal(len(resKeys), len(qtw.resourceKeys), "UpdateResourceKeys failed")
	assertObj.Equal(len(resKeys), len(qtw.quotaTree), "update quota tree failed")
	_, exist = qtw.resourceKeys[corev1.ResourceEphemeralStorage]
	assertObj.True(exist, "update quota tree failed")
}

func TestRuntimeQuotaCalculator_UpdateOneGroupMaxQuota(t *testing.T) {
	max := createResourceList(100, 1000)
	min := createResourceList(70, 7000)
	quotaInfo := createQuotaInfoWithRes("aliyun", max, min)
	qtw := createRuntimeQuotaCalculator()
	quotaInfo.setMaxQuotaNoLock(max)
	qtw.updateOneGroupMaxQuota(quotaInfo)

	assert.Equal(t, len(max), len(qtw.resourceKeys))
	assert.Equal(t, int64(2), qtw.globalRuntimeVersion)
	assert.Equal(t, 2, len(qtw.quotaTree))
	_, exist := qtw.quotaTree["cpu"].quotaNodes["aliyun"]
	assert.True(t, exist)

	newMax := createResourceList(200, 9000)
	request := createResourceList(30, 3000)
	quotaInfo.addRequestNonNegativeNoLock(request, request, false)
	assert.Equal(t, request, quotaInfo.CalculateInfo.Request)

	qtw.setClusterTotalResource(max)
	assert.Equal(t, max, qtw.totalResource)

	quotaInfo.setMaxQuotaNoLock(newMax)
	qtw.groupReqLimit[quotaInfo.Name] = request.DeepCopy()
	qtw.updateOneGroupMaxQuota(quotaInfo)
	assert.Equal(t, request, qtw.groupReqLimit[quotaInfo.Name])
	assert.Equal(t, max, qtw.totalResource)
}

func TestRuntimeQuotaCalculator_UpdateOneGroupMinQuota(t *testing.T) {
	assertObj := assert.New(t)
	max := createResourceList(100, 10000)
	minQuota := createResourceList(70, 7000)
	quotaInfo := createQuotaInfoWithRes("test1", max, minQuota)

	// totalRequest = request = min,  totalResource = max
	quotaInfo.CalculateInfo.Request = minQuota.DeepCopy()
	qtw := createRuntimeQuotaCalculator()
	qtw.groupReqLimit[quotaInfo.Name] = minQuota
	qtw.setClusterTotalResource(max)
	quotaInfo.setAutoScaleMinQuotaNoLock(minQuota)
	qtw.updateOneGroupMinQuota(quotaInfo)

	assertObj.Equal(2, len(qtw.resourceKeys))
	assertObj.Equal(max.Name(corev1.ResourceCPU, resource.DecimalSI), qtw.totalResource.Name(corev1.ResourceCPU, resource.DecimalSI))
	assertObj.Equal(max.Name(corev1.ResourceMemory, resource.DecimalSI), qtw.totalResource.Name(corev1.ResourceMemory, resource.DecimalSI))
	qtw.updateOneGroupRuntimeQuota(quotaInfo)
	assertObj.Equal(qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].runtimeQuota, int64(70000))
	assertObj.Equal(qtw.quotaTree["memory"].quotaNodes[quotaInfo.Name].runtimeQuota, int64(7000))
	assertObj.Equal(qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].min, int64(70000))

	newMin := createResourceList(50, 5000)
	quotaInfo.setAutoScaleMinQuotaNoLock(newMin)
	qtw.updateOneGroupMinQuota(quotaInfo)
	qtw.updateOneGroupRuntimeQuota(quotaInfo)
	assertObj.Equal(qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].runtimeQuota, int64(70000))
	assertObj.Equal(qtw.quotaTree["memory"].quotaNodes[quotaInfo.Name].runtimeQuota, int64(7000))
	assertObj.Equal(qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].min, int64(50000))
}

func TestRuntimeQuotaCalculator_UpdateOneGroupSharedWeight(t *testing.T) {
	max := createResourceList(100, 1000)
	min := createResourceList(70, 7000)
	quotaInfo := createQuotaInfoWithRes("test1", max, min)
	qtw := createRuntimeQuotaCalculator()

	qtw.updateOneGroupSharedWeight(quotaInfo)
	maxCpu := max["cpu"]
	assert.Equal(t, getQuantityValue(maxCpu, "cpu"), qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].sharedWeight)

	sharedWeight := createResourceList(60, 6000)
	quotaInfo.setSharedWeightNoLock(sharedWeight)
	qtw.updateOneGroupSharedWeight(quotaInfo)
	sharedCpu := sharedWeight["cpu"]
	assert.Equal(t, getQuantityValue(sharedCpu, "cpu"), qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].sharedWeight)

	sharedWeight = createResourceList(120, 12000)
	quotaInfo.setSharedWeightNoLock(sharedWeight)
	qtw.updateOneGroupSharedWeight(quotaInfo)
	sharedCpu = sharedWeight["cpu"]
	assert.Equal(t, getQuantityValue(sharedCpu, "cpu"), qtw.quotaTree["cpu"].quotaNodes[quotaInfo.Name].sharedWeight)
}

func TestRuntimeQuotaCalculator_NeedUpdateOneGroupRequest(t *testing.T) {
	max := createResourceList(100, 1000)
	min := createResourceList(70, 7000)
	quotaInfo := createQuotaInfoWithRes("test1", max, min)
	qtw := createRuntimeQuotaCalculator()

	update := qtw.needUpdateOneGroupRequest(quotaInfo)
	assert.False(t, update)

	quotaInfo.CalculateInfo.Request = min.DeepCopy()
	update = qtw.needUpdateOneGroupRequest(quotaInfo)
	assert.True(t, update)
}

func TestRuntimeQuotaCalculator_UpdateOneGroupRequest(t *testing.T) {
	qtw := createRuntimeQuotaCalculator()
	totalResource := createResourceList(50, 5000)
	qtw.setClusterTotalResource(totalResource)
	quotaCount := 5
	for i := 1; i <= quotaCount; i++ {
		max := createResourceList(int64(i*100), int64(i*10000))
		min := createResourceList(int64(i*80), int64(i*8000))
		request := createResourceList(int64(i*10), int64(i*1000))
		quotaName := fmt.Sprintf("test-%d", i)
		quotaInfo := createQuotaInfoWithRes(quotaName, max, min)
		quotaInfo.addRequestNonNegativeNoLock(request, request, false)

		qtw.updateOneGroupMaxQuota(quotaInfo)
		qtw.updateOneGroupMinQuota(quotaInfo)
		qtw.updateOneGroupSharedWeight(quotaInfo)
		qtw.updateOneGroupRequest(quotaInfo)

		reqLimit := qtw.getGroupRequestLimitNoLock(quotaInfo.Name)
		assert.Equal(t, reqLimit, request)

		qtw.updateOneGroupRuntimeQuota(quotaInfo)
		// request < min,  runtime == request
		assert.Equal(t, quotaInfo.CalculateInfo.Runtime, quotaInfo.CalculateInfo.Request)
	}
}

func TestRuntimeQuotaCalculator_UpdateOneGroupRuntimeQuota(t *testing.T) {
	qtw := createRuntimeQuotaCalculator()
	totalResource := createResourceList(100, 1000)
	qtw.setClusterTotalResource(totalResource)

	// test1 max[80, 800], min[60, 600], request[0, 0], runtime[0, 0]
	// test2 max[100, 1000], min[50, 500], request[90, 900], runtime[90, 900]
	max := createResourceList(80, 800)
	min := createResourceList(60, 600)
	sharedWeight := createResourceList(1, 1)
	test1 := createQuotaInfoWithRes("test1", max, min)
	updateQuotaInfo(qtw, test1, max, min, sharedWeight)

	max = createResourceList(100, 1000)
	min = createResourceList(50, 500)
	request := createResourceList(90, 900)
	test2 := createQuotaInfoWithRes("test2", max, min)
	test2.CalculateInfo.Request = request.DeepCopy()
	updateQuotaInfo(qtw, test2, max, min, sharedWeight)

	qtw.updateOneGroupRequest(test2)
	qtw.updateOneGroupRuntimeQuota(test1)
	qtw.updateOneGroupRuntimeQuota(test2)
	assert.Equal(t, totalResource, qtw.totalResource)
	assert.Equal(t, 2, len(qtw.quotaTree))
	assert.Equal(t, int64(0), test1.CalculateInfo.Runtime.Name("cpu", resource.DecimalSI).Value())
	assert.Equal(t, int64(0), test1.CalculateInfo.Runtime.Name("memory", resource.DecimalSI).Value())
	assert.Equal(t, request, test2.CalculateInfo.Runtime)

	// test1 max[80, 800], min[60, 600], request[30, 300], runtime[30, 300]
	// test2 max[100, 1000], min[50, 500], request[90, 900], runtime[70, 700]
	request = createResourceList(30, 300)
	test1.CalculateInfo.Request = request.DeepCopy()
	qtw.updateOneGroupRequest(test1)
	qtw.updateOneGroupRuntimeQuota(test1)
	qtw.updateOneGroupRuntimeQuota(test2)

	assert.Equal(t, request, test1.CalculateInfo.Runtime)
	assert.Equal(t, v12.Subtract(totalResource, request), test2.CalculateInfo.Runtime)

	// test1 max[80, 800], min[60, 600], request[60, 600], runtime[60, 600]
	// test2 max[100, 1000], min[50, 500], request[90, 900], runtime[50, 500]
	request = createResourceList(60, 600)
	test1.CalculateInfo.Request = request.DeepCopy()
	qtw.updateOneGroupRequest(test1)
	qtw.updateOneGroupRuntimeQuota(test1)

	assert.Equal(t, request, test1.CalculateInfo.Runtime)

	qtw.updateOneGroupRuntimeQuota(test2)
	assert.Equal(t, test2.CalculateInfo.AutoScaleMin, test2.CalculateInfo.Runtime)

	// delete test1
	// test2 max[100, 1000], min[50, 500], request[90, 900], runtime[90, 900]
	qtw.deleteOneGroup(test1)
	assert.Equal(t, int64(12), qtw.globalRuntimeVersion)
	qtw.updateOneGroupRuntimeQuota(test2)
	assert.Equal(t, createResourceList(90, 900), test2.CalculateInfo.Runtime)
	cpu := corev1.ResourceCPU
	assert.Equal(t, 1, len(qtw.groupReqLimit))
	assert.Equal(t, 1, len(qtw.quotaTree[cpu].quotaNodes))
}

func TestRuntimeQuotaCalculator_UpdateOneGroupRuntimeQuota2(t *testing.T) {
	qtw := createRuntimeQuotaCalculator()
	totalResource := createResourceList(120, 1200)
	qtw.setClusterTotalResource(totalResource)

	max := createResourceList(80, 800)
	min := createResourceList(50, 500)
	sharedWeight := createResourceList(1, 1)
	test1 := createQuotaInfoWithRes("test1", max, min)
	updateQuotaInfo(qtw, test1, max, min, sharedWeight)
	request := createResourceList(100, 1000)
	test1.CalculateInfo.Request = request.DeepCopy()

	qtw.updateOneGroupRequest(test1)
	qtw.updateOneGroupRuntimeQuota(test1)

	assert.Equal(t, totalResource, qtw.totalResource)
	assert.Equal(t, max, test1.CalculateInfo.Runtime)

	max = createResourceList(100, 1000)
	min = createResourceList(50, 500)
	test2 := createQuotaInfoWithRes("test2", max, min)
	updateQuotaInfo(qtw, test2, max, min, sharedWeight)
	request = createResourceList(150, 1500)
	test2.CalculateInfo.Request = request.DeepCopy()
	qtw.updateOneGroupRequest(test2)

	qtw.updateOneGroupRuntimeQuota(test2)
	qtw.updateOneGroupRuntimeQuota(test1)

	assert.Equal(t, test1.CalculateInfo.Runtime, createResourceList(60, 600))
	assert.Equal(t, test2.CalculateInfo.Runtime, createResourceList(60, 600))
}

func updateQuotaInfo(wrapper *RuntimeQuotaCalculator, info *QuotaInfo, max, min, sharedWeight corev1.ResourceList) {
	info.setMaxQuotaNoLock(max)
	wrapper.updateOneGroupMaxQuota(info)
	info.setAutoScaleMinQuotaNoLock(min)
	wrapper.updateOneGroupMinQuota(info)
	info.setSharedWeightNoLock(sharedWeight)
	wrapper.updateOneGroupSharedWeight(info)
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

func TestRuntimeQuotaCalculator_DeleteOneGroup(t *testing.T) {
	cpu := corev1.ResourceCPU

	qtw := createRuntimeQuotaCalculator()
	totalResource := createResourceList(100, 1000)
	qtw.setClusterTotalResource(totalResource)

	// test1 max[80, 800], min[60, 600], request[0, 0], runtime[0, 0]
	// test2 max[100, 1000], min[50, 500], request[90, 900], runtime[90, 900]
	max := createResourceList(80, 800)
	min := createResourceList(60, 600)
	sharedWeight := createResourceList(1, 1)
	test1 := createQuotaInfoWithRes("test1", max, min)
	updateQuotaInfo(qtw, test1, max, min, sharedWeight)

	max = createResourceList(100, 1000)
	min = createResourceList(50, 500)
	request := createResourceList(90, 900)
	test2 := createQuotaInfoWithRes("test2", max, min)
	test2.CalculateInfo.Request = request.DeepCopy()
	updateQuotaInfo(qtw, test2, max, min, sharedWeight)

	qtw.updateOneGroupRequest(test2)
	qtw.updateOneGroupRuntimeQuota(test1)
	qtw.updateOneGroupRuntimeQuota(test2)
	assert.Equal(t, totalResource, qtw.totalResource)
	assert.Equal(t, 2, len(qtw.quotaTree))
	assert.Equal(t, int64(0), test1.CalculateInfo.Runtime.Name("cpu", resource.DecimalSI).Value())
	assert.Equal(t, int64(0), test1.CalculateInfo.Runtime.Name("memory", resource.DecimalSI).Value())
	assert.Equal(t, request, test2.CalculateInfo.Runtime)

	// test1 max[80, 800], min[60, 600], request[30, 300], runtime[30, 300]
	// test2 max[100, 1000], min[50, 500], request[90, 900], runtime[70, 700]
	request = createResourceList(30, 300)
	test1.CalculateInfo.Request = request.DeepCopy()
	qtw.updateOneGroupRequest(test1)
	qtw.updateOneGroupRuntimeQuota(test1)
	qtw.updateOneGroupRuntimeQuota(test2)

	assert.Equal(t, request, test1.CalculateInfo.Runtime)
	assert.Equal(t, v12.Subtract(totalResource, request), test2.CalculateInfo.Runtime)

	// test1 max[80, 800], min[60, 600], request[60, 600], runtime[60, 600]
	// test2 max[100, 1000], min[50, 500], request[90, 900], runtime[50, 500]
	request = createResourceList(60, 600)
	test1.CalculateInfo.Request = request.DeepCopy()
	qtw.updateOneGroupRequest(test1)
	qtw.updateOneGroupRuntimeQuota(test1)

	assert.Equal(t, request, test1.CalculateInfo.Runtime)

	qtw.updateOneGroupRuntimeQuota(test2)
	assert.Equal(t, test2.CalculateInfo.AutoScaleMin, test2.CalculateInfo.Runtime)

	assert.Equal(t, int64(11), qtw.globalRuntimeVersion)
	assert.Equal(t, 2, len(qtw.groupReqLimit))
	assert.Equal(t, 2, len(qtw.quotaTree[cpu].quotaNodes))

	// delete test1
	// test2 max[100, 1000], min[50, 500], request[90, 900], runtime[90, 900]
	qtw.deleteOneGroup(test1)
	assert.Equal(t, int64(12), qtw.globalRuntimeVersion)
	qtw.updateOneGroupRuntimeQuota(test2)
	assert.Equal(t, createResourceList(90, 900), test2.CalculateInfo.Runtime)
	assert.Equal(t, 1, len(qtw.groupReqLimit))
	assert.Equal(t, 1, len(qtw.quotaTree[cpu].quotaNodes))
}
