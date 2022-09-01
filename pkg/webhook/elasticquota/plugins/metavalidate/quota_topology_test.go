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

package metavalidate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	testing2 "k8s.io/kubernetes/pkg/scheduler/testing"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

func newFakeQuotaTopology() *quotaTopology {
	return &quotaTopology{
		podCache:           make(map[string]map[string]struct{}),
		quotaInfoMap:       make(map[string]*core.QuotaInfo),
		quotaHierarchyInfo: make(map[string]map[string]struct{}),
	}
}

func TestNew(t *testing.T) {
	qt := newFakeQuotaTopology()
	qt.podCache["1"] = make(map[string]struct{})
	qt.podCache["1"]["pod1"] = struct{}{}
	qt.quotaInfoMap["1"] = core.NewQuotaInfo(false, false, "tmp", "root")
	qt.quotaHierarchyInfo["1"] = make(map[string]struct{})
	marshal, _ := json.MarshalIndent(qt.GetQuotaTopologyInfo(), "", "")
	klog.Infof(":%v", string(marshal))
	assert.NotNil(t, qt)
}

func TestQuotaTopology_basicItemCheck(t *testing.T) {
	qt := newFakeQuotaTopology()
	quota := createQuotaNoParentInfo("aliyun", 120, 1048576)
	err := qt.basicItemCheck(quota)
	assert.Nil(t, err)

	// quota is forbidden modify
	quota = createQuotaNoParentInfo("root", 120, 1048576)
	err = qt.basicItemCheck(quota)
	assert.NotNil(t, err)
	quota = createQuotaNoParentInfo("system", 120, 1048576)
	err = qt.basicItemCheck(quota)
	assert.NotNil(t, err)

	quota1 := createQuota("alimm", "root", 120, 1048576, 64, 51200, false)
	err = qt.basicItemCheck(quota1)
	assert.Nil(t, err)

	// max < 0
	quota1.Spec.Max = createResourceList(-1, 0)
	err = qt.basicItemCheck(quota1)
	assert.NotNil(t, err)
	quota1.Spec.Max = createResourceList(120, 1048576)

	// min < 0
	quota1.Spec.Min = createResourceList(-1, 0)
	err = qt.basicItemCheck(quota1)
	assert.NotNil(t, err)

	// min dimension larger than max
	quota1.Spec.Min = v1.ResourceList{
		"tmp": *resource.NewQuantity(1, resource.DecimalSI),
	}
	err = qt.basicItemCheck(quota1)
	assert.NotNil(t, err)

	// min > max
	quota1.Spec.Min = createResourceList(150, 234)
	err = qt.basicItemCheck(quota1)
	assert.NotNil(t, err)

	// min == max
	quota1.Spec.Min = createResourceList(120, 1048576)
	err = qt.basicItemCheck(quota1)
	assert.Nil(t, err)

	// check parent QuotaInfo
	err = qt.AddQuota(quota1)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaInfoMap))
	assert.Equal(t, 1, len(qt.podCache))
	quota2 := createQuota("mm-b1", "alimm", 120, 1048576, 64, 51200, false)
	err = qt.basicItemCheck(quota2)
	assert.Nil(t, err)

	// parentInfo's name not equal to the info's ParentName
	qt.quotaInfoMap["alimm"].Name = "odpsbatch"
	err = qt.basicItemCheck(quota2)
	assert.NotNil(t, err)

	// parentInfo not found
	quota2.Labels[extension.LabelQuotaParent] = "odpsbatch"
	err = qt.basicItemCheck(quota2)
	assert.NotNil(t, err)
}

func TestQuotaTopology_fillDefaultQuotaInfo(t *testing.T) {
	qt := newFakeQuotaTopology()
	quota := createQuotaNoParentInfo("aliyun", 120, 1048576)
	quota.Labels = nil
	quota.Annotations = nil
	err := qt.fillDefaultQuotaInfo(quota)
	assert.Nil(t, err)
	assert.Equal(t, extension.RootQuotaName, quota.Labels[extension.LabelQuotaParent])
	maxQuota, _ := json.Marshal(quota.Spec.Max)
	assert.Equal(t, string(maxQuota), quota.Annotations[extension.AnnotationSharedWeight])

	quota = createQuota("mm-bu1", "alimm", 120, 1048576, 64, 51200, false)
	err = qt.fillDefaultQuotaInfo(quota)
	assert.Nil(t, err)
	assert.Equal(t, "alimm", quota.Labels[extension.LabelQuotaParent])
	assert.Equal(t, string(maxQuota), quota.Annotations[extension.AnnotationSharedWeight])
}

func TestQuotaTopology_checkSubAndParentGroupMaxQuotaKeySame(t *testing.T) {
	// same
	qt := newFakeQuotaTopology()
	quota := createQuota("aliyun", "", 120, 1048576, 120, 1048576, true)
	err := qt.AddQuota(quota)
	assert.Nil(t, err)
	subQuota := createQuota("yun-bu1", "aliyun", 120, 1048576, 64, 51200, false)
	subQuotaInfo := core.NewQuotaInfoFromQuota(subQuota)
	success := qt.checkSubAndParentGroupMaxQuotaKeySame(subQuotaInfo)
	assert.True(t, success)

	// can't get parent
	subQuota.Labels[extension.LabelQuotaParent] = "tmp"
	subQuotaInfo = core.NewQuotaInfoFromQuota(subQuota)
	success = qt.checkSubAndParentGroupMaxQuotaKeySame(subQuotaInfo)
	assert.False(t, success)
	subQuota.Labels[extension.LabelQuotaParent] = "aliyun"

	// parent's key size > child's key size
	quota = createQuota("alimm", "", 120, 1048576, 120, 1048576, true)
	quota.Spec.Max[v1.ResourceEphemeralStorage] = *resource.NewQuantity(1234567, resource.DecimalSI)
	err = qt.AddQuota(quota)
	assert.Nil(t, err)
	subQuota = createQuota("mm-bu1", "alimm", 120, 1048576, 64, 51200, false)
	subQuotaInfo = core.NewQuotaInfoFromQuota(subQuota)
	success = qt.checkSubAndParentGroupMaxQuotaKeySame(subQuotaInfo)
	assert.False(t, success)

	// size same, but dimension is different
	subQuota.Spec.Max[v1.ResourceLimitsEphemeralStorage] = *resource.NewQuantity(1234567, resource.DecimalSI)
	subQuotaInfo = core.NewQuotaInfoFromQuota(subQuota)
	success = qt.checkSubAndParentGroupMaxQuotaKeySame(subQuotaInfo)
	assert.False(t, success)

	// child's key size > parent's key size
	subQuota.Spec.Max[v1.ResourceEphemeralStorage] = *resource.NewQuantity(1234567, resource.DecimalSI)
	subQuotaInfo = core.NewQuotaInfoFromQuota(subQuota)
	success = qt.checkSubAndParentGroupMaxQuotaKeySame(subQuotaInfo)
	assert.False(t, success)
}

func TestQuotaTopology_checkMinQuotaSum(t *testing.T) {
	// aliyun min[Cpu:64, mem:51200]
	qt := newFakeQuotaTopology()
	quota := createQuota("aliyun", "", 120, 1048576, 64, 51200, true)
	err := qt.AddQuota(quota)
	assert.Nil(t, err)

	// sub-1 min[C:16, M:12800]
	sub1 := createQuota("sub-1", "aliyun", 120, 1048576, 16, 12800, false)
	err = qt.AddQuota(sub1)
	assert.Nil(t, err)

	// sub-2 min[C:16, M:12800]
	sub2 := createQuota("sub-2", "aliyun", 120, 1048576, 16, 12800, false)
	err = qt.AddQuota(sub2)
	assert.Nil(t, err)

	// sub-3 min[C:16, M:25600]  sub min-sum[C:48, M:51200]
	sub3 := createQuota("sub-3", "aliyun", 120, 1048576, 16, 25600, false)
	err = qt.AddQuota(sub3)
	assert.Nil(t, err)

	// sub-4 min[C:32, M:0] sub min-sum[C:80, M:51200]
	sub4 := createQuota("sub-4", "aliyun", 120, 1048576, 32, 0, false)
	err = qt.AddQuota(sub4)
	assert.NotNil(t, err)
	find := strings.Index(err.Error(), "checkMinQuotaSum")
	assert.True(t, find > 0)
}

func TestQuotaTopology_checkMaxQuota(t *testing.T) {
	// aliyun max[Cpu:120, mem:1048576]
	qt := newFakeQuotaTopology()
	quota := createQuota("aliyun", "root", 120, 1048576, 64, 51200, false)
	err := qt.AddQuota(quota)
	assert.True(t, err == nil)

	// sub-1 max[C:120, M:51200]
	sub1 := createQuota("sub-1", "aliyun", 120, 51200, 16, 12800, false)
	err = qt.AddQuota(sub1)
	assert.True(t, err == nil)

	// sub-2 min[C:140, M:51200]
	sub2 := createQuota("sub-2", "aliyun", 140, 51200, 16, 12800, false)
	err = qt.AddQuota(sub2)
	assert.True(t, err != nil)
	find := strings.Index(err.Error(), "checkMaxQuota")
	assert.True(t, find > 0)

	quota.Spec.Max = createResourceList(1, 1)
	quotaInfo := core.NewQuotaInfoFromQuota(quota)
	if qt.checkMaxQuota(quotaInfo) {
		t.Errorf("error")
	}
	qt.quotaHierarchyInfo["aliyun"]["sub-2"] = struct{}{}
	if qt.checkMaxQuota(quotaInfo) {
		t.Errorf("error")
	}
}

func TestQuotaTopology_AddQuota(t *testing.T) {
	qt := newFakeQuotaTopology()
	quota := createQuotaNoParentInfo("aliyun", 120, 1048576)
	err := qt.AddQuota(quota)
	assert.True(t, err == nil)
	assert.Equal(t, extension.RootQuotaName, quota.Labels[extension.LabelQuotaParent])
	maxQuota, _ := json.Marshal(&quota.Spec.Max)
	assert.Equal(t, string(maxQuota), quota.Annotations[extension.AnnotationSharedWeight])
	assert.Equal(t, 1, len(qt.quotaInfoMap))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.podCache))

	// add repeated quota
	err = qt.AddQuota(quota)
	assert.True(t, err != nil)

	// add sub quota
	sub1 := createQuota("yun-sub1", "aliyun", 120, 1048576, 0, 0, false)
	err = qt.AddQuota(sub1)
	assert.True(t, err == nil)
	assert.Equal(t, 2, len(qt.quotaInfoMap))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["aliyun"]))
	assert.Equal(t, 2, len(qt.podCache))

	// add sub quota
	sub2 := createQuota("yun-sub2", "aliyun", 120, 1048576, 0, 0, false)
	err = qt.AddQuota(sub2)
	assert.True(t, err == nil)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo["aliyun"]))
	assert.Equal(t, 3, len(qt.podCache))
}

func TestQuotaTopology_UpdateQuota(t *testing.T) {
	qt := newFakeQuotaTopology()
	quota := createQuotaNoParentInfo("aliyun", 120, 1048576)
	err := qt.AddQuota(quota)
	assert.True(t, err == nil)

	oldQuotaCopy := quota.DeepCopy()
	err = qt.UpdateQuota(oldQuotaCopy, quota)
	assert.Nil(t, err)

	err = qt.UpdateQuota(nil, nil)
	assert.NotNil(t, err)

	quota.Annotations[extension.AnnotationSharedWeight] = fmt.Sprintf("{\"cpu\":%v, \"memory\":\"%v\"}", 96, 655360)
	err = qt.UpdateQuota(nil, quota)
	assert.True(t, err == nil)

	quotaInfo := qt.quotaInfoMap["aliyun"]
	assert.Equal(t, int64(96), quotaInfo.CalculateInfo.SharedWeight.Name("cpu", resource.DecimalSI).Value())
	assert.Equal(t, int64(655360), quotaInfo.CalculateInfo.SharedWeight.Name("memory", resource.DecimalSI).Value())

	quota1 := createQuotaNoParentInfo("alimm", 120, 1048576)
	err = qt.AddQuota(quota1)
	assert.True(t, err == nil)

	sub1 := createQuota("yun-sub1", "aliyun", 120, 1048576, 60, 51200, false)
	err = qt.AddQuota(sub1)
	assert.True(t, err == nil)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["aliyun"]))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["alimm"]))

	sub1.Labels[extension.LabelQuotaParent] = "alimm"
	err = qt.UpdateQuota(nil, sub1)
	assert.True(t, err == nil)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["aliyun"]))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["alimm"]))

	sub1.Labels[extension.LabelQuotaParent] = "aliyun"
	sub1.Spec.Min = createResourceList(121, 1048576)
	err = qt.UpdateQuota(nil, sub1)
	assert.True(t, err != nil)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["aliyun"]))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["alimm"]))

	sub1.Name = "tmp"
	err = qt.UpdateQuota(nil, sub1)
	assert.Equal(t, "quota not exist:tmp", err.Error())
}

func TestQuotaTopology_DeleteQuota(t *testing.T) {
	qt := newFakeQuotaTopology()
	quota := createQuotaNoParentInfo("aliyun", 120, 1048576)
	err := qt.AddQuota(quota)
	assert.True(t, err == nil)

	quota1 := createQuotaNoParentInfo("alimm", 120, 1048576)
	err = qt.AddQuota(quota1)
	assert.True(t, err == nil)

	sub1 := createQuota("yun-sub1", "aliyun", 120, 1048576, 60, 51200, false)
	err = qt.AddQuota(sub1)
	assert.True(t, err == nil)

	err = qt.DeleteQuota(quota.Name)
	assert.True(t, err != nil)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["aliyun"]))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["alimm"]))
	assert.Equal(t, 3, len(qt.podCache))

	err = qt.DeleteQuota(quota1.Name)
	assert.True(t, err == nil)
	assert.Equal(t, 2, len(qt.quotaInfoMap))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 2, len(qt.podCache))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["aliyun"]))

	err = qt.DeleteQuota(sub1.Name)
	assert.True(t, err == nil)
	assert.Equal(t, 1, len(qt.quotaInfoMap))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.podCache))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["aliyun"]))

	err = qt.DeleteQuota(quota.Name)
	assert.True(t, err == nil)
	assert.Equal(t, 0, len(qt.quotaInfoMap))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 0, len(qt.podCache))

}

func createQuota(name, parentName string, maxCpu, maxMem, minCpu, minMem int64, isParent bool) *v1alpha1.ElasticQuota {
	quota := &v1alpha1.ElasticQuota{
		TypeMeta: metav1.TypeMeta{
			Kind: "ElasticQuota",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "",
			Annotations: make(map[string]string),
			Labels:      make(map[string]string),
		},
		Spec: v1alpha1.ElasticQuotaSpec{
			Max: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewQuantity(maxCpu, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(maxMem, resource.DecimalSI),
			},
			Min: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewQuantity(minCpu, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(minMem, resource.DecimalSI),
			},
		},
	}
	quota.Labels[extension.LabelQuotaParent] = parentName
	quota.Labels[extension.LabelQuotaIsParent] = "false"
	if isParent {
		quota.Labels[extension.LabelQuotaIsParent] = "true"
	}
	return quota
}

func createQuotaNoParentInfo(name string, maxCpu, maxMem int64) *v1alpha1.ElasticQuota {
	quota := &v1alpha1.ElasticQuota{
		TypeMeta: metav1.TypeMeta{
			Kind: "ElasticQuota",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "",
			Annotations: make(map[string]string),
			Labels:      make(map[string]string),
		},
		Spec: v1alpha1.ElasticQuotaSpec{
			Max: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewQuantity(maxCpu, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(maxMem, resource.DecimalSI),
			},
		},
	}
	return quota
}

func createResourceList(cpu, mem int64) v1.ResourceList {
	return v1.ResourceList{
		v1.ResourceCPU:    *resource.NewQuantity(cpu, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity(mem, resource.DecimalSI),
	}
}

func TestNewQuotaTopology_QuotaHandler(t *testing.T) {
	qt := newFakeQuotaTopology()

	qt.OnQuotaAdd(nil)

	sub1 := createQuota("yun-sub1", "aliyun", 120, 1048576, 60, 51200, false)
	qt.OnQuotaAdd(sub1)

	assert.Equal(t, 1, len(qt.quotaInfoMap))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.podCache))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["aliyun"]))

	parent := createQuota("aliyun", "xxx", 120, 1048576, 60, 51200, false)
	qt.OnQuotaAdd(parent)

	assert.Equal(t, 2, len(qt.quotaInfoMap))
	assert.Equal(t, 2, len(qt.podCache))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["aliyun"]))

	sub2 := createQuota("yun-sub2", "aliyun", 120, 1048576, 60, 51200, false)
	qt.OnQuotaAdd(sub2)

	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.podCache))
	assert.Equal(t, 4, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo["aliyun"]))

	oldSub2 := sub2.DeepCopy()
	sub2.Labels[extension.LabelQuotaParent] = "xxx"
	qt.OnQuotaUpdate(oldSub2, sub2)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.podCache))
	assert.Equal(t, 4, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["aliyun"]))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo["xxx"]))

	qt.OnQuotaDelete(sub2)
	assert.Equal(t, 2, len(qt.quotaInfoMap))
	assert.Equal(t, 2, len(qt.podCache))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["aliyun"]))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["xxx"]))
}

func TestNewQuotaTopology_PodHandler(t *testing.T) {
	qt := newFakeQuotaTopology()
	par := createQuota("aliyun", "root", 120, 1048576, 300, 231200, true)
	qt.OnQuotaAdd(par)
	sub1 := createQuota("yun-sub1", "aliyun", 120, 1048576, 60, 51200, false)
	qt.OnQuotaAdd(sub1)
	sub2 := createQuota("yun-sub2", "aliyun", 120, 1048576, 60, 51200, false)
	qt.OnQuotaAdd(sub2)
	pod1 := MakePod("", "pod1").Label(extension.LabelQuotaName, "yun-sub1").Obj()
	qt.OnPodAdd(pod1)
	assert.Equal(t, 1, len(qt.podCache["yun-sub1"]))
	oldPod1 := pod1.DeepCopy()
	pod1.Labels[extension.LabelQuotaName] = "yun-sub2"
	qt.OnPodUpdate(oldPod1, pod1)
	assert.Equal(t, 1, len(qt.podCache["yun-sub2"]))
	assert.Equal(t, 0, len(qt.podCache["yun-sub1"]))
	err := qt.ValidateDeleteQuota(par)
	assert.NotNil(t, err)
	err = qt.ValidateDeleteQuota(sub1)
	assert.Nil(t, err)
	err = qt.ValidateDeleteQuota(sub2)
	assert.NotNil(t, err)
	qt.OnPodDelete(pod1)
	err = qt.ValidateDeleteQuota(sub1)
	assert.Nil(t, err)
	qt.OnQuotaDelete(sub1)
	err = qt.ValidateDeleteQuota(sub1)
	assert.NotNil(t, err)
	qt.quotaInfoMap["yun-sub1"] = core.NewQuotaInfoFromQuota(sub1)
	err = qt.ValidateDeleteQuota(sub1)
	assert.NotNil(t, err)
}

type podWrapper struct{ *v1.Pod }

func MakePod(namespace, name string) *podWrapper {
	pod := testing2.MakePod().Namespace(namespace).Name(name).Obj()

	return &podWrapper{pod}
}

func (p *podWrapper) Label(string1, string2 string) *podWrapper {
	if p.Labels == nil {
		p.Labels = make(map[string]string)
	}
	p.Labels[string1] = string2
	return p
}

func (p *podWrapper) Obj() *v1.Pod {
	return p.Pod
}
