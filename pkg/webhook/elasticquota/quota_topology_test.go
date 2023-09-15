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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testing2 "k8s.io/kubernetes/pkg/scheduler/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

func newFakeQuotaTopology() *quotaTopology {
	qt := &quotaTopology{
		quotaInfoMap:        make(map[string]*QuotaInfo),
		quotaHierarchyInfo:  make(map[string]map[string]struct{}),
		namespaceToQuotaMap: make(map[string]string),
	}
	qt.quotaHierarchyInfo[extension.RootQuotaName] = make(map[string]struct{})
	return qt
}

func TestNew(t *testing.T) {
	qt := newFakeQuotaTopology()
	qt.quotaInfoMap["1"] = NewQuotaInfo(false, false, "tmp", "root")
	qt.quotaHierarchyInfo["1"] = make(map[string]struct{})
	assert.NotNil(t, qt)
}

func TestQuotaTopology_basicItemCheck(t *testing.T) {
	tests := []struct {
		name  string
		quota *v1alpha1.ElasticQuota
		err   error
	}{
		{
			name:  "admit",
			quota: MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).Obj(),
			err:   nil,
		},
		{
			name:  "max <0",
			quota: MakeQuota("temp").Max(MakeResourceList().CPU(-1).Mem(1048576).Obj()).Obj(),
			err:   fmt.Errorf("%v quota.Spec.Max's value < 0, in dimensions :%v", "temp", "[cpu]"),
		},
		{
			name: "min <0",
			quota: MakeQuota("temp").Min(MakeResourceList().CPU(-1).Mem(1048576).Obj()).
				Max(MakeResourceList().CPU(1).Mem(1048576).Obj()).Obj(),
			err: fmt.Errorf("%v quota.Spec.Min's value < 0, in dimensions :%v", "temp", "[cpu]"),
		},
		{
			name: "min dimension larger than max",
			quota: MakeQuota("temp").Min(MakeResourceList().CPU(1).Mem(1048576).Obj()).
				Max(MakeResourceList().CPU(10).Obj()).Obj(),
			err: fmt.Errorf("%v min :%v > max,%v", "temp",
				MakeResourceList().CPU(1).Mem(1048576).Obj(), MakeResourceList().CPU(10).Obj()),
		},
		{
			name: "min > max",
			quota: MakeQuota("temp").Min(MakeResourceList().CPU(12).Obj()).
				Max(MakeResourceList().CPU(10).Obj()).Obj(),
			err: fmt.Errorf("%v min :%v > max,%v", "temp",
				MakeResourceList().CPU(12).Obj(), MakeResourceList().CPU(10).Obj()),
		},
		{
			name:  "min dimension larger than max",
			quota: MakeQuota("temp").sharedWeight(MakeResourceList().CPU(-1).Mem(1048576).Obj()).Obj(),
			err:   fmt.Errorf("%v quota.Annotation[%v]'s value < 0, in dimension :%v", "temp", extension.AnnotationSharedWeight, "[cpu]"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qt := newFakeQuotaTopology()
			qt.fillQuotaDefaultInformation(tt.quota)
			err := qt.validateQuotaSelfItem(tt.quota)
			assert.Equal(t, err, tt.err)
		})
	}
}

func TestQuotaTopology_fillDefaultQuotaInfo(t *testing.T) {
	qt := newFakeQuotaTopology()
	quota := MakeQuota("temp2").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).Obj()
	quota.Labels = nil
	quota.Annotations = nil
	err := qt.fillQuotaDefaultInformation(quota)
	assert.Nil(t, err)
	assert.Equal(t, extension.RootQuotaName, quota.Labels[extension.LabelQuotaParent])
	maxQuota, _ := json.Marshal(quota.Spec.Max)
	assert.Equal(t, string(maxQuota), quota.Annotations[extension.AnnotationSharedWeight])

	qt.OnQuotaAdd(quota)

	quota = MakeQuota("temp2-bu1").ParentName("temp2").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).Obj()
	err = qt.fillQuotaDefaultInformation(quota)
	assert.Nil(t, err)
	assert.Equal(t, "temp2", quota.Labels[extension.LabelQuotaParent])
	assert.Equal(t, string(maxQuota), quota.Annotations[extension.AnnotationSharedWeight])
}

func TestQuotaTopology_fillDefaultQuotaInfoWithTreeID(t *testing.T) {
	qt := newFakeQuotaTopology()
	quota := MakeQuota("temp2").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).TreeID("tree-1").Obj()
	err := qt.fillQuotaDefaultInformation(quota)
	assert.Nil(t, err)
	assert.Equal(t, extension.RootQuotaName, quota.Labels[extension.LabelQuotaParent])
	assert.Equal(t, "tree-1", quota.Labels[extension.LabelQuotaTreeID])
	maxQuota, _ := json.Marshal(quota.Spec.Max)
	assert.Equal(t, string(maxQuota), quota.Annotations[extension.AnnotationSharedWeight])

	qt.OnQuotaAdd(quota)

	quota = MakeQuota("temp2-bu1").ParentName("temp2").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).Obj()
	err = qt.fillQuotaDefaultInformation(quota)
	assert.Nil(t, err)
	assert.Equal(t, "temp2", quota.Labels[extension.LabelQuotaParent])
	assert.Equal(t, "tree-1", quota.Labels[extension.LabelQuotaTreeID])
	assert.Equal(t, string(maxQuota), quota.Annotations[extension.AnnotationSharedWeight])
}

func TestQuotaTopology_checkSubAndParentGroupMaxQuotaKeySame(t *testing.T) {
	tests := []struct {
		name     string
		parQuota *v1alpha1.ElasticQuota
		quota    *v1alpha1.ElasticQuota
		subQuota *v1alpha1.ElasticQuota
		err      error
		eraseSub bool
	}{
		{
			name:  "same",
			quota: MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).IsParent(true).Obj(),
			subQuota: MakeQuota("temp-bu1").ParentName("temp").
				Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).IsParent(false).Obj(),
			err: nil,
		},
		{
			name: "parent is root",
			quota: MakeQuota("temp-bu1").ParentName(extension.RootQuotaName).
				Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).IsParent(false).Obj(),
			err: nil,
		},
		{
			name:  "parent's key size > child's key size",
			quota: MakeQuota("temp").Max(MakeResourceList().CPU(10).Mem(120).Obj()).IsParent(true).Obj(),
			subQuota: MakeQuota("temp-bu1").ParentName("temp").
				Max(MakeResourceList().CPU(120).Obj()).IsParent(false).Obj(),
			err: fmt.Errorf("error"),
		},
		{
			name:  "size same, dimension is different",
			quota: MakeQuota("temp").Max(MakeResourceList().Mem(120).Obj()).IsParent(true).Obj(),
			subQuota: MakeQuota("temp-bu1").ParentName("temp").
				Max(MakeResourceList().CPU(120).Obj()).IsParent(false).Obj(),
			err: fmt.Errorf("error"),
		},
		{
			name:  "child's key size > parent's key size",
			quota: MakeQuota("temp").Max(MakeResourceList().CPU(10).Obj()).IsParent(true).Obj(),
			subQuota: MakeQuota("temp-bu1").ParentName("temp").
				Max(MakeResourceList().CPU(120).Mem(120).Obj()).IsParent(false).Obj(),
			err: fmt.Errorf("error"),
		},
		{
			name:     "quotaInfo not satisfy",
			parQuota: MakeQuota("temp").Max(MakeResourceList().CPU(10).Obj()).IsParent(true).Obj(),
			quota: MakeQuota("temp-bu1").ParentName("temp").
				Max(MakeResourceList().CPU(120).Mem(120).Obj()).IsParent(false).Obj(),
			err: fmt.Errorf("error"),
		},
		{
			name:  "bug",
			quota: MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).IsParent(true).Obj(),
			subQuota: MakeQuota("temp-bu1").ParentName("temp").
				Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).IsParent(false).Obj(),
			err:      fmt.Errorf("error"),
			eraseSub: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qt := newFakeQuotaTopology()
			qt.OnQuotaAdd(tt.parQuota)
			qt.OnQuotaAdd(tt.quota)
			qt.OnQuotaAdd(tt.subQuota)
			quotaInfo := NewQuotaInfoFromQuota(tt.quota)
			if tt.eraseSub {
				delete(qt.quotaInfoMap, tt.subQuota.Name)
			}
			err := qt.checkSubAndParentGroupMaxQuotaKeySame(quotaInfo)
			if (tt.err != nil && err == nil) || (tt.err == nil && err != nil) {
				t.Errorf("error")
			}
		})
	}
}

func TestQuotaTopology_checkTreeID(t *testing.T) {
	tests := []struct {
		name        string
		parentQuota *v1alpha1.ElasticQuota
		quota       *v1alpha1.ElasticQuota
		childQuota  *v1alpha1.ElasticQuota
		expectErr   bool
	}{
		{
			name: "satisfy no tree id",
			parentQuota: MakeQuota("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).Obj(),
			quota: MakeQuota("temp").ParentName("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).Obj(),
			childQuota: MakeQuota("child").ParentName("temp").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(false).Obj(),
			expectErr: false,
		},
		{
			name: "satisfy with tree id",
			parentQuota: MakeQuota("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).TreeID("tree-1").Obj(),
			quota: MakeQuota("temp").ParentName("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).TreeID("tree-1").Obj(),
			childQuota: MakeQuota("child").ParentName("temp").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(false).TreeID("tree-1").Obj(),
			expectErr: false,
		},
		{
			name: "parent no tree id",
			parentQuota: MakeQuota("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).Obj(),
			quota: MakeQuota("temp").ParentName("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).TreeID("tree-1").Obj(),
			childQuota: MakeQuota("child").ParentName("temp").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(false).TreeID("tree-1").Obj(),
			expectErr: true,
		},
		{
			name: "self no tree id",
			parentQuota: MakeQuota("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).TreeID("tree-1").Obj(),
			quota: MakeQuota("temp").ParentName("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).Obj(),
			childQuota: MakeQuota("child").ParentName("temp").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(false).TreeID("tree-1").Obj(),
			expectErr: true,
		},
		{
			name: "child no tree id",
			parentQuota: MakeQuota("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).TreeID("tree-1").Obj(),
			quota: MakeQuota("temp").ParentName("parent").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(true).TreeID("tree-1").Obj(),
			childQuota: MakeQuota("child").ParentName("temp").Max(MakeResourceList().CPU(10).Mem(20).Obj()).
				Min(MakeResourceList().CPU(10).Mem(20).Obj()).IsParent(false).Obj(),
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qt := newFakeQuotaTopology()
			qt.OnQuotaAdd(tt.parentQuota)
			qt.OnQuotaAdd(tt.quota)
			qt.OnQuotaAdd(tt.childQuota)

			quota := NewQuotaInfoFromQuota(tt.quota)
			err := qt.checkTreeID(nil, quota)

			if tt.expectErr && err == nil {
				t.Errorf("expected error, but err is nil")
			}

			if !tt.expectErr && err != nil {
				t.Errorf("expected no error, but got err: %v", err)
			}
		})
	}
}

func TestQuotaTopology_checkMinQuotaSum(t *testing.T) {
	tests := []struct {
		name        string
		parentQuota *v1alpha1.ElasticQuota
		quota       *v1alpha1.ElasticQuota
		subQuota    *v1alpha1.ElasticQuota
		err         error
		eraseSub    bool
	}{
		{
			name: "quotaInfo not satisfy",
			quota: MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
				Min(MakeResourceList().CPU(10).Mem(51200).Obj()).IsParent(true).Obj(),
			subQuota: MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
				Min(MakeResourceList().CPU(16).Mem(12800).Obj()).IsParent(false).Obj(),
			err: fmt.Errorf("error"),
		},
		{
			name: "parentQuotaInfo not satisfy",
			parentQuota: MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
				Min(MakeResourceList().CPU(10).Mem(51200).Obj()).IsParent(true).Obj(),
			quota: MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
				Min(MakeResourceList().CPU(16).Mem(12800).Obj()).IsParent(false).Obj(),
			err: fmt.Errorf("error"),
		},
		{
			name: "satisfy",
			quota: MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
				Min(MakeResourceList().CPU(19).Mem(51200).Obj()).IsParent(true).Obj(),
			subQuota: MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
				Min(MakeResourceList().CPU(16).Mem(12800).Obj()).IsParent(false).Obj(),
		},
		{
			name: "bug",
			quota: MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
				Min(MakeResourceList().CPU(19).Mem(51200).Obj()).IsParent(true).Obj(),
			subQuota: MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
				Min(MakeResourceList().CPU(16).Mem(12800).Obj()).IsParent(false).Obj(),
			eraseSub: true,
			err:      fmt.Errorf("error"),
		},
		{
			name:        "parent no resource",
			parentQuota: MakeQuota("temp-no-min").IsParent(true).Obj(),
			quota: MakeQuota("sub-1").ParentName("temp-no-min").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
				Min(MakeResourceList().CPU(19).Mem(51200).Obj()).IsParent(false).Obj(),
			err: fmt.Errorf("error"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qt := newFakeQuotaTopology()
			qt.OnQuotaAdd(tt.parentQuota)
			qt.OnQuotaAdd(tt.quota)
			qt.OnQuotaAdd(tt.subQuota)
			quota := NewQuotaInfoFromQuota(tt.quota)
			if tt.eraseSub {
				delete(qt.quotaInfoMap, tt.subQuota.Name)
			}
			err := qt.checkMinQuotaSum(quota)
			if (tt.err != nil && err == nil) || (tt.err == nil && err != nil) {
				t.Errorf("error")
			}
		})
	}
}

func TestQuotaTopology_ValidAddQuota(t *testing.T) {
	qt := newFakeQuotaTopology()
	quota := MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(64).Mem(51200).Obj()).IsParent(true).Obj()
	qt.fillQuotaDefaultInformation(quota)
	err := qt.ValidAddQuota(quota)
	assert.Nil(t, err)
	assert.Equal(t, extension.RootQuotaName, quota.Labels[extension.LabelQuotaParent])
	maxQuota, _ := json.Marshal(&quota.Spec.Max)
	assert.Equal(t, string(maxQuota), quota.Annotations[extension.AnnotationSharedWeight])
	assert.Equal(t, 1, len(qt.quotaInfoMap))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo))

	// add repeated quota
	err = qt.ValidAddQuota(quota)
	assert.NotNil(t, err)

	// add sub quota
	sub1 := MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(16).Mem(12800).Obj()).IsParent(false).Obj()
	qt.fillQuotaDefaultInformation(sub1)
	err = qt.ValidAddQuota(sub1)
	assert.Nil(t, err)
	assert.Equal(t, 2, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp"]))

	// add sub quota
	sub2 := MakeQuota("sub-2").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(16).Mem(12800).Obj()).IsParent(false).Obj()
	qt.fillQuotaDefaultInformation(sub2)
	err = qt.ValidAddQuota(sub2)
	assert.Nil(t, err)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 4, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo["temp"]))

	err = qt.ValidAddQuota(nil)
	assert.NotNil(t, err)

	// add temp2 with tree id
	quota2 := MakeQuota("temp2").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(64).Mem(51200).Obj()).IsParent(true).TreeID("tree-1").Obj()
	qt.fillQuotaDefaultInformation(quota2)
	err = qt.ValidAddQuota(quota2)
	assert.Nil(t, err)

	// child has no tree id
	sub3 := MakeQuota("sub-3").ParentName("temp2").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(64).Mem(51200).Obj()).IsParent(true).Obj()
	err = qt.ValidAddQuota(sub3)
	assert.NotNil(t, err)
}

func TestQuotaTopology_ValidUpdateQuota(t *testing.T) {
	qt := newFakeQuotaTopology()
	quota := MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(64).Mem(51200).Obj()).IsParent(true).Obj()
	err := qt.fillQuotaDefaultInformation(quota)
	assert.True(t, err == nil)
	err = qt.ValidAddQuota(quota)
	assert.True(t, err == nil)

	oldQuotaCopy := quota.DeepCopy()
	err = qt.ValidUpdateQuota(oldQuotaCopy, quota)
	assert.Nil(t, err)

	err = qt.ValidUpdateQuota(nil, nil)
	assert.NotNil(t, err)

	quota.Annotations[extension.AnnotationSharedWeight] = fmt.Sprintf("{\"cpu\":%v, \"memory\":\"%v\"}", 96, 655360)
	err = qt.ValidUpdateQuota(oldQuotaCopy, quota)
	assert.True(t, err == nil)

	quota1 := MakeQuota("temp2").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(64).Mem(51200).Obj()).IsParent(true).Obj()
	qt.fillQuotaDefaultInformation(quota1)
	err = qt.ValidAddQuota(quota1)
	assert.True(t, err == nil)

	sub1 := MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(60).Mem(12800).Obj()).IsParent(false).Obj()
	qt.fillQuotaDefaultInformation(sub1)
	err = qt.ValidAddQuota(sub1)
	assert.True(t, err == nil)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 4, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp"]))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["temp2"]))

	oldSub1 := sub1.DeepCopy()
	sub1.Labels[extension.LabelQuotaParent] = "temp2"
	err = qt.ValidUpdateQuota(oldSub1, sub1)
	assert.Nil(t, err)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 4, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["temp"]))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp2"]))

	sub1.Labels[extension.LabelQuotaParent] = "temp"
	sub1.Spec.Min = MakeResourceList().CPU(121).Mem(1048576).Obj()
	err = qt.ValidUpdateQuota(oldSub1, sub1)
	assert.True(t, err != nil)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 4, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["temp"]))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp2"]))

	sub1.Name = "tmp"
	err = qt.ValidUpdateQuota(oldSub1, sub1)
	assert.Equal(t, "UpdateQuota quota not exist in quotaInfoMap:tmp", err.Error())

	sub1.Name = extension.RootQuotaName
	err = qt.ValidUpdateQuota(nil, sub1)
	assert.Equal(t, fmt.Sprintf("invalid quota %s", extension.RootQuotaName), err.Error())

	quota.Labels[extension.LabelQuotaIsParent] = "false"
	err = qt.ValidUpdateQuota(oldQuotaCopy, quota)
	assert.Nil(t, err)

	newQuota1 := quota1.DeepCopy()
	newQuota1.Labels[extension.LabelQuotaIsParent] = "false"
	err = qt.ValidUpdateQuota(quota1, newQuota1)
	assert.Equal(t, fmt.Sprintf("quota has children, isParent is forbidden to modify as false, quotaName:%v", quota1.Name), err.Error())

	pod1 := MakePod("", "pod1").Label(extension.LabelQuotaName, "sub-1").Obj()
	client := fake.NewClientBuilder().Build()
	v1alpha1.AddToScheme(client.Scheme())
	qt.client = client
	qt.client.Create(context.TODO(), pod1)

	sub1.Name = "sub-1"
	sub1.Spec.Min = MakeResourceList().CPU(120).Mem(1048576).Obj()
	newSub1 := sub1.DeepCopy()
	newSub1.Labels[extension.LabelQuotaIsParent] = "true"
	err = qt.ValidUpdateQuota(sub1, newSub1)
	assert.Equal(t, fmt.Sprintf("quota has bound pods, isParent is forbidden to modify as true, quotaName: sub-1"), err.Error())

	qt.client.Delete(context.TODO(), pod1)
	pod2 := MakePod("sub-1", "pod2").Obj()
	qt.client.Create(context.TODO(), pod2)

	err = qt.ValidUpdateQuota(sub1, newSub1)
	assert.Equal(t, fmt.Sprintf("quota has bound pods, isParent is forbidden to modify as true, quotaName: sub-1"), err.Error())

	qt.client.Delete(context.TODO(), pod2)
	pod3 := MakePod("sub-2", "pod3").Obj()
	qt.client.Create(context.TODO(), pod3)

	sub1.Annotations[extension.AnnotationQuotaNamespaces] = "[\"namespace1\",\"namespace2\"]"
	err = qt.ValidUpdateQuota(sub1, newSub1)
	assert.Equal(t, fmt.Sprintf("quota has bound pods, isParent is forbidden to modify as true, quotaName: sub-1"), err.Error())

	// add tree id, reject
	newSub1 = sub1.DeepCopy()
	newSub1.Labels[extension.LabelQuotaTreeID] = "tree-1"
	err = qt.ValidUpdateQuota(sub1, newSub1)
	assert.Equal(t, fmt.Sprint("sub-1 tree id changed [] vs [tree-1]"), err.Error())
}

func TestQuotaTopology_ListQuotaPods(t *testing.T) {
	testCase := []struct {
		name string
		pod  *v1.Pod
		eq   *v1alpha1.ElasticQuota
		want bool
	}{
		{
			name: "by label",
			pod:  MakePod("sub-1", "pod1").Obj(),
			eq:   MakeQuota("sub-1").IsParent(false).Obj(),
		},
	}
	for _, tt := range testCase {
		t.Run(tt.name, func(t *testing.T) {
			kubeClient := fake.NewClientBuilder().Build()
			v1alpha1.AddToScheme(kubeClient.Scheme())
			qt := newFakeQuotaTopology()
			qt.client = kubeClient
			qt.client.Create(context.TODO(), tt.pod)
			podList := &v1.PodList{}
			kubeClient.List(context.TODO(), podList, &client.ListOptions{
				Namespace: tt.eq.Name,
			}, utilclient.DisableDeepCopy)
			assert.Equal(t, len(podList.Items), 1)
			kubeClient.List(context.TODO(), podList, &client.ListOptions{
				Namespace: "sub-2",
			}, utilclient.DisableDeepCopy)
			assert.Equal(t, len(podList.Items), 0)
		})
	}
}

func TestQuotaTopology_AnnotationNamespaces(t *testing.T) {
	quota := MakeQuota("temp").Annotations(map[string]string{extension.AnnotationQuotaNamespaces: "[\"test1\",\"test2\"]"}).Obj()
	qt := newFakeQuotaTopology()
	client := fake.NewClientBuilder().Build()
	v1alpha1.AddToScheme(client.Scheme())
	qt.client = client

	err := qt.ValidAddQuota(quota)
	assert.Nil(t, err)
	qt.lock.Lock()
	assert.Equal(t, qt.namespaceToQuotaMap["test1"], "temp")
	assert.Equal(t, qt.namespaceToQuotaMap["test2"], "temp")
	qt.lock.Unlock()

	newQuota := quota.DeepCopy()
	newQuota.Name = "temp2"
	err = qt.ValidAddQuota(newQuota)
	assert.Equal(t, err, fmt.Errorf("AddQuota quota temp2's annotation namespace test1 is already bound to quota temp"))

	newQuota.Name = "temp"
	newQuota.Annotations[extension.AnnotationQuotaNamespaces] = "[\"test2\",\"test3\"]"
	err = qt.ValidUpdateQuota(quota, newQuota)
	assert.Nil(t, err)
	qt.lock.Lock()
	assert.Equal(t, qt.namespaceToQuotaMap["test2"], "temp")
	assert.Equal(t, qt.namespaceToQuotaMap["test3"], "temp")
	qt.lock.Unlock()

	quota2 := quota.DeepCopy()
	quota2.Name = "temp5"
	quota2.Annotations[extension.AnnotationQuotaNamespaces] = ""
	qt.ValidAddQuota(quota2)
	newQuota2 := quota2.DeepCopy()
	newQuota2.Annotations[extension.AnnotationQuotaNamespaces] = "[\"test2\"]"
	err = qt.ValidUpdateQuota(quota, newQuota2)
	assert.Equal(t, err, fmt.Errorf("UpdadteQuota, quota temp5 update namespaces, but namespace test2 is already bound to quota temp"))

	err = qt.ValidDeleteQuota(newQuota)
	assert.Nil(t, err)
	qt.lock.Lock()
	assert.Equal(t, len(qt.namespaceToQuotaMap), 0)
	qt.lock.Unlock()
}

func TestQuotaTopology_ValidDeleteQuota(t *testing.T) {
	qt := newFakeQuotaTopology()

	client := fake.NewClientBuilder().Build()
	v1alpha1.AddToScheme(client.Scheme())
	qt.client = client

	quota := MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(64).Mem(51200).Obj()).IsParent(true).Obj()
	qt.fillQuotaDefaultInformation(quota)
	err := qt.ValidAddQuota(quota)
	assert.True(t, err == nil)

	quota1 := MakeQuota("temp2").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(64).Mem(51200).Obj()).IsParent(true).Obj()
	qt.fillQuotaDefaultInformation(quota1)
	err = qt.ValidAddQuota(quota1)
	assert.True(t, err == nil)

	sub1 := MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(60).Mem(12800).Obj()).IsParent(false).Obj()
	qt.fillQuotaDefaultInformation(sub1)
	err = qt.ValidAddQuota(sub1)
	assert.True(t, err == nil)

	err = qt.ValidDeleteQuota(quota)
	assert.True(t, err != nil)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 4, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp"]))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["temp2"]))

	err = qt.ValidDeleteQuota(quota1)
	assert.True(t, err == nil)
	assert.Equal(t, 2, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp"]))

	// add pod to quota sub-1
	pod := MakePod("sub-1", "pod1").Obj()
	err = qt.client.Create(context.TODO(), pod)
	assert.Nil(t, err)

	// forbidden delete quota with pods
	err = qt.ValidDeleteQuota(sub1)
	assert.True(t, err != nil)

	// delete pod
	err = qt.client.Delete(context.TODO(), pod)
	assert.Nil(t, err)

	err = qt.ValidDeleteQuota(sub1)
	assert.True(t, err == nil)
	assert.Equal(t, 1, len(qt.quotaInfoMap))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 0, len(qt.quotaHierarchyInfo["temp"]))

	err = qt.ValidDeleteQuota(quota)
	assert.True(t, err == nil)
	assert.Equal(t, 0, len(qt.quotaInfoMap))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo))

	sysQuota := MakeQuota("system").Obj()
	err = qt.ValidDeleteQuota(sysQuota)
	assert.NotNil(t, err)

	notFoundQuota := MakeQuota("notFound").Max(MakeResourceList().CPU(1).Obj()).Obj()
	err = qt.ValidDeleteQuota(notFoundQuota)
	assert.NotNil(t, err)

	qt.quotaInfoMap[notFoundQuota.Name] = NewQuotaInfoFromQuota(notFoundQuota)
	err = qt.ValidDeleteQuota(notFoundQuota)
	assert.NotNil(t, err)
}

func TestNewQuotaTopology_QuotaHandler(t *testing.T) {
	qt := newFakeQuotaTopology()

	qt.OnQuotaAdd(nil)

	sub1 := MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(60).Mem(12800).Obj()).IsParent(false).Obj()
	qt.OnQuotaAdd(sub1)

	assert.Equal(t, 1, len(qt.quotaInfoMap))
	assert.Equal(t, 3, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp"]))

	parent := MakeQuota("temp").ParentName("xxx").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(60).Mem(12800).Obj()).IsParent(true).Obj()
	qt.OnQuotaAdd(parent)

	assert.Equal(t, 2, len(qt.quotaInfoMap))
	assert.Equal(t, 4, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp"]))

	sub2 := MakeQuota("sub-2").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(60).Mem(12800).Obj()).IsParent(false).Obj()
	qt.OnQuotaAdd(sub2)

	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 5, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo["temp"]))

	oldSub2 := sub2.DeepCopy()
	sub2.Labels[extension.LabelQuotaParent] = "xxx"
	qt.OnQuotaUpdate(oldSub2, sub2)
	assert.Equal(t, 3, len(qt.quotaInfoMap))
	assert.Equal(t, 5, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp"]))
	assert.Equal(t, 2, len(qt.quotaHierarchyInfo["xxx"]))

	qt.OnQuotaDelete(sub2)
	assert.Equal(t, 2, len(qt.quotaInfoMap))
	assert.Equal(t, 4, len(qt.quotaHierarchyInfo))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["temp"]))
	assert.Equal(t, 1, len(qt.quotaHierarchyInfo["xxx"]))
}

func TestQuotaTopology_AddPod_UpdatePod(t *testing.T) {
	qt := newFakeQuotaTopology()
	par := MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(64).Mem(51200).Obj()).IsParent(true).Obj()
	qt.OnQuotaAdd(par)
	sub1 := MakeQuota("sub-1").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(60).Mem(12800).Obj()).IsParent(false).Obj()
	qt.OnQuotaAdd(sub1)
	sub2 := MakeQuota("sub-2").ParentName("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(60).Mem(12800).Obj()).IsParent(false).Obj()
	qt.OnQuotaAdd(sub2)

	pod1 := MakePod("", "pod1").Label(extension.LabelQuotaName, "sub-1").Obj()
	err := qt.ValidateAddPod(pod1)
	assert.Nil(t, err)

	oldPod1 := pod1.DeepCopy()
	pod1.Labels[extension.LabelQuotaName] = "sub-2"
	err = qt.ValidateUpdatePod(oldPod1, pod1)
	assert.Nil(t, err)

	pod2 := MakePod("", "pod2").Label(extension.LabelQuotaName, "temp").Obj()
	err = qt.ValidateAddPod(pod2)
	assert.NotNil(t, err)

	oldPod2 := pod2.DeepCopy()
	pod2.Labels[extension.LabelQuotaName] = "sub-2"
	err = qt.ValidateUpdatePod(oldPod2, pod2)
	assert.Nil(t, err)

	err = qt.ValidateUpdatePod(pod2, oldPod2)
	assert.NotNil(t, err)

	pod2.Labels[extension.LabelQuotaName] = "default"
	err = qt.ValidateUpdatePod(oldPod2, pod2)
	assert.Nil(t, err)
}

func TestQuotaTopology_getQuotaNameFromPod(t *testing.T) {
	tests := []struct {
		name              string
		pod               *v1.Pod
		elasticQuotas     []*v1alpha1.ElasticQuota
		expectedQuotaName string
	}{
		{
			name:              "default quota",
			pod:               &v1.Pod{},
			expectedQuotaName: extension.DefaultQuotaName,
		},
		{
			name: "quota name from label",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test",
					Labels: map[string]string{
						extension.LabelQuotaName: "test-quota",
					},
				},
			},
			elasticQuotas: []*v1alpha1.ElasticQuota{
				MakeQuota("test-quota").Namespace("test-ns").IsParent(false).Obj(),
			},
			expectedQuotaName: "test-quota",
		},
		{
			name: "quota name from namespace",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test",
				},
			},
			elasticQuotas: []*v1alpha1.ElasticQuota{
				MakeQuota("parent-quota").Namespace("test-ns").IsParent(true).Obj(),
				MakeQuota("test-ns").Namespace("test-ns").IsParent(false).Obj(),
			},
			expectedQuotaName: "test-ns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().Build()
			v1alpha1.AddToScheme(client.Scheme())
			qt := newFakeQuotaTopology()
			qt.client = client
			for _, eq := range tt.elasticQuotas {
				err := client.Create(context.TODO(), eq)
				assert.NoError(t, err)
				qt.OnQuotaAdd(eq)
			}
			quotaName := qt.getQuotaNameFromPodNoLock(tt.pod)
			assert.Equal(t, tt.expectedQuotaName, quotaName)
		})
	}
}

func TestQuotaTopology_checkParentQuotaInfoExist(t *testing.T) {
	qt := newFakeQuotaTopology()
	par := MakeQuota("temp").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(64).Mem(51200).Obj()).IsParent(false).Obj()
	qt.OnQuotaAdd(par)

	err := qt.checkParentQuotaInfo("", "temp")
	assert.Equal(t, fmt.Errorf("%v has parentName %v but the parentQuotaInfo's IsParent is false", "", "temp"), err)

	delete(qt.quotaHierarchyInfo, "temp")
	err = qt.checkParentQuotaInfo("", "temp")
	assert.Equal(t, fmt.Errorf("%v has parentName %v but not find parentInfo in quotaHierarchyInfo", "", "temp"), err)

	delete(qt.quotaInfoMap, "temp")
	err = qt.checkParentQuotaInfo("", "temp")
	assert.Equal(t, fmt.Errorf("%v has parentName %v but not find parentInfo in quotaInfoMap", "", "temp"), err)
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

type quotaWrapper struct {
	*v1alpha1.ElasticQuota
}

func MakeQuota(name string) *quotaWrapper {
	eq := &v1alpha1.ElasticQuota{
		TypeMeta: metav1.TypeMeta{Kind: "ElasticQuota", APIVersion: "scheduling.sigs.k8s.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
	}
	return &quotaWrapper{eq}
}

func (q *quotaWrapper) Namespace(ns string) *quotaWrapper {
	q.ElasticQuota.Namespace = ns
	return q
}

func (q *quotaWrapper) Min(min v1.ResourceList) *quotaWrapper {
	q.ElasticQuota.Spec.Min = min
	return q
}

func (q *quotaWrapper) Max(max v1.ResourceList) *quotaWrapper {
	q.ElasticQuota.Spec.Max = max
	return q
}

func (q *quotaWrapper) TreeID(tree string) *quotaWrapper {
	q.ElasticQuota.Labels[extension.LabelQuotaTreeID] = tree
	return q
}

func (q *quotaWrapper) sharedWeight(sharedWeight v1.ResourceList) *quotaWrapper {
	sharedWeightBytes, _ := json.Marshal(sharedWeight)
	q.ElasticQuota.Annotations[extension.AnnotationSharedWeight] = string(sharedWeightBytes)
	return q
}

func (q *quotaWrapper) IsParent(isParent bool) *quotaWrapper {
	if isParent {
		q.Labels[extension.LabelQuotaIsParent] = "true"
	} else {
		q.Labels[extension.LabelQuotaIsParent] = "false"
	}
	return q
}

func (q *quotaWrapper) ParentName(parentName string) *quotaWrapper {
	q.Labels[extension.LabelQuotaParent] = parentName
	return q
}

func (q *quotaWrapper) Annotations(annotations map[string]string) *quotaWrapper {
	for k, v := range annotations {
		q.ElasticQuota.Annotations[k] = v
	}
	return q
}

func (q *quotaWrapper) Obj() *v1alpha1.ElasticQuota {
	return q.ElasticQuota
}

type resourceWrapper struct{ v1.ResourceList }

func MakeResourceList() *resourceWrapper {
	return &resourceWrapper{v1.ResourceList{}}
}

func (r *resourceWrapper) CPU(val int64) *resourceWrapper {
	r.ResourceList[v1.ResourceCPU] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) Mem(val int64) *resourceWrapper {
	r.ResourceList[v1.ResourceMemory] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) GPU(val int64) *resourceWrapper {
	r.ResourceList["nvidia.com/gpu"] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) Obj() v1.ResourceList {
	return r.ResourceList
}
