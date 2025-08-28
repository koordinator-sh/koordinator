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
	"encoding/json"
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func (qt *quotaTopology) validateQuotaSelfItem(quota *v1alpha1.ElasticQuota) error {
	// min and max's each dimension should not have negative value
	if resourceNames := quotav1.IsNegative(quota.Spec.Max); len(resourceNames) > 0 {
		return fmt.Errorf("%v quota.Spec.Max's value < 0, in dimensions :%v", quota.Name, resourceNames)
	}

	if resourceNames := quotav1.IsNegative(quota.Spec.Min); len(resourceNames) > 0 {
		return fmt.Errorf("%v quota.Spec.Min's value < 0, in dimensions :%v", quota.Name, resourceNames)
	}

	var sharedRatio v1.ResourceList
	// 1.check if sharewight is equal to
	if quota.Annotations[extension.AnnotationSharedWeight] != "" {
		if err := json.Unmarshal([]byte(quota.Annotations[extension.AnnotationSharedWeight]), &sharedRatio); err != nil {
			return err
		}

		if resourceNames := quotav1.IsNegative(sharedRatio); len(resourceNames) > 0 {
			return fmt.Errorf("%v quota.Annotation[%v]'s value < 0, in dimension :%v", quota.Name, extension.AnnotationSharedWeight, resourceNames)
		}
	}

	// 1. check if all key in min are included in max
	// 2. check if all quantities in min <= that in max
	for key, val := range quota.Spec.Min {
		if maxVal, exist := quota.Spec.Max[key]; exist {
			if maxVal.Cmp(val) == -1 {
				return fmt.Errorf("resourceKey %v of quota %v min %v > max %v", key, quota.Name, val.String(), maxVal.String())
			}
		} else {
			return fmt.Errorf("resourceKey %v of quota %v is included in min, which is not included in max", key, quota.Name)
		}
	}

	// 1. check if all key in AnnotationMaxStrictCheckResourceKeys in max >= that in used
	resourceKeys, err := extension.GetMaxStrictCheckResourceKeys(quota)
	if err != nil {
		return fmt.Errorf("%v quota.Annotation[%v]'s value is invalid: %w", quota.Name, extension.AnnotationMaxStrictCheckResourceKeys, err)
	}
	for _, key := range resourceKeys {
		if usedVal, exist := quota.Status.Used[key]; !exist {
			continue // check should be skipped if current used never counts
		} else if maxVal, exist := quota.Spec.Max[key]; !exist {
			return fmt.Errorf("resourceKey %v of quota %v is included in used, which is not included in max but should check max >= used", key, quota.Name)
		} else if maxVal.Cmp(usedVal) == -1 {
			return fmt.Errorf("resourceKey %v of quota %v max %v < used %v", key, quota.Name, maxVal.String(), usedVal.String())
		}
	}
	return nil
}

// validateQuotaTopology checks the quotaInfo's topology with its parent and its children.
// oldQuotaInfo is null when validate a new create request, and is the current quotaInfo when validate a update request.
func (qt *quotaTopology) validateQuotaTopology(oldQuotaInfo, newQuotaInfo *QuotaInfo, oldNamespaces []string) error {
	if newQuotaInfo.Name == extension.RootQuotaName {
		return nil
	}

	if err := qt.checkIsParentChange(oldQuotaInfo, newQuotaInfo, oldNamespaces); err != nil {
		return err
	}

	if err := qt.checkTreeID(oldQuotaInfo, newQuotaInfo); err != nil {
		return err
	}

	// if the quotaInfo's parent is root and its IsParent is false, the following checks will be true, just return nil.
	if newQuotaInfo.ParentName == extension.RootQuotaName && !newQuotaInfo.IsParent {
		return nil
	}

	if err := qt.checkParentQuotaInfo(newQuotaInfo.Name, newQuotaInfo.ParentName); err != nil {
		return err
	}

	if err := qt.checkSubAndParentGroupQuotaKey(newQuotaInfo, utilfeature.DefaultFeatureGate.Enabled(features.ElasticQuotaEnableUpdateResourceKey)); err != nil {
		return fmt.Errorf("failed to check sub and parent group quotaKey, err: %w", err)
	}

	if err := qt.checkMinQuotaValidate(newQuotaInfo); err != nil {
		return err
	}

	if utilfeature.DefaultFeatureGate.Enabled(features.ElasticQuotaGuaranteeUsage) {
		if err := qt.checkGuaranteedForMin(newQuotaInfo); err != nil {
			return fmt.Errorf("%v %v", err.Error(), newQuotaInfo.Name)
		}
	}

	return nil
}

func (qt *quotaTopology) checkTreeID(oldQuotaInfo, quotaInfo *QuotaInfo) error {
	if oldQuotaInfo != nil {
		if oldQuotaInfo.TreeID != quotaInfo.TreeID {
			return fmt.Errorf("%v tree id changed [%v] vs [%v]", quotaInfo.Name, oldQuotaInfo.TreeID, quotaInfo.TreeID)
		}
	}

	// check the parent tree id
	if quotaInfo.ParentName != extension.RootQuotaName {
		// checkParentQuotaInfo has check parent exist
		parentInfo := qt.quotaInfoMap[quotaInfo.ParentName]
		if parentInfo != nil && quotaInfo.TreeID != parentInfo.TreeID {
			return fmt.Errorf("%v tree id is different from parent %v, [%v] vs [%v]", quotaInfo.Name, parentInfo.Name, quotaInfo.TreeID, parentInfo.TreeID)
		}
	}

	// check the children tree id
	children, exist := qt.quotaHierarchyInfo[quotaInfo.Name]
	if !exist || len(children) == 0 {
		return nil
	}

	for name := range children {
		childInfo := qt.quotaInfoMap[name]
		if childInfo != nil && childInfo.TreeID != quotaInfo.TreeID {
			return fmt.Errorf("%v tree id is different from child %v, [%v] vs [%v]", quotaInfo.Name, childInfo.Name, quotaInfo.TreeID, childInfo.TreeID)
		}
	}

	return nil
}

func (qt *quotaTopology) checkIsParentChange(oldQuotaInfo, quotaInfo *QuotaInfo, oldNamespaces []string) error {
	// means create quota, no need check
	if oldQuotaInfo == nil || oldQuotaInfo.IsParent == quotaInfo.IsParent {
		return nil
	}

	if len(qt.quotaHierarchyInfo[oldQuotaInfo.Name]) > 0 && !quotaInfo.IsParent {
		return fmt.Errorf("quota has children, isParent is forbidden to modify as false, quotaName:%v", oldQuotaInfo.Name)
	}

	if quotaInfo.IsParent {
		hasPods, err := hasQuotaBoundedPods(qt.client, oldQuotaInfo.Name, oldNamespaces)
		if err != nil {
			return err
		}
		if hasPods {
			return fmt.Errorf("quota has bound pods, isParent is forbidden to modify as true, quotaName: %v", oldQuotaInfo.Name)
		}
	}

	return nil
}

// checkParentQuotaInfo check parent exist
func (qt *quotaTopology) checkParentQuotaInfo(quotaName, parentName string) error {
	if parentName != extension.RootQuotaName {
		parentInfo, find := qt.quotaInfoMap[parentName]
		if !find {
			return fmt.Errorf("%v has parentName %v but not find parentInfo in quotaInfoMap", quotaName, parentName)
		}
		if _, exist := qt.quotaHierarchyInfo[parentName]; !exist {
			return fmt.Errorf("%v has parentName %v but not find parentInfo in quotaHierarchyInfo", quotaName, parentName)
		}
		if !parentInfo.IsParent {
			return fmt.Errorf("%v has parentName %v but the parentQuotaInfo's IsParent is false", quotaName, parentName)
		}
	}
	return nil
}

// checkSubAndParentGroupQuotaKey check the quotaInfo's quota with its parent and its children
//
//	while enableResourceTypeUpdate=false, the quotaInfo's max quota key must be same as its children and parent's quota key
//	while enableResourceTypeUpdate=true, the quotaInfo's max quota key only need be included in its parent's quota key
//
// the quotaInfo's min quota key only need be included in its parent's quota key no matter when
func (qt *quotaTopology) checkSubAndParentGroupQuotaKey(quotaInfo *QuotaInfo, enableUpdateResourceKey bool) error {
	if quotaInfo.Name == extension.RootQuotaName {
		return nil
	}
	if quotaInfo.ParentName != extension.RootQuotaName {
		parentInfo := qt.quotaInfoMap[quotaInfo.ParentName]
		if enableUpdateResourceKey {
			if !checkQuotaKeyIncluded(parentInfo.CalculateInfo.Max, quotaInfo.CalculateInfo.Max) {
				return fmt.Errorf("checkSubAndParentGroupQuotaKey failed: %v's max keys are not all included in %v's",
					quotaInfo.Name, quotaInfo.ParentName)
			}
		} else {
			if !checkQuotaKeySame(parentInfo.CalculateInfo.Max, quotaInfo.CalculateInfo.Max) {
				return fmt.Errorf("checkSubAndParentGroupQuotaKey failed: %v's max keys are not the same with %v's",
					quotaInfo.ParentName, quotaInfo.Name)
			}
		}
		if !checkQuotaKeyIncluded(parentInfo.CalculateInfo.Min, quotaInfo.CalculateInfo.Min) {
			return fmt.Errorf("checkSubAndParentGroupQuotaKey failed: %v's min keys are not all included in %v's",
				quotaInfo.Name, quotaInfo.ParentName)
		}
	}

	children, find := qt.quotaHierarchyInfo[quotaInfo.Name]
	if !find || len(children) == 0 {
		return nil
	}

	for name := range children {
		if child, exist := qt.quotaInfoMap[name]; exist {
			if enableUpdateResourceKey {
				if !checkQuotaKeyIncluded(quotaInfo.CalculateInfo.Max, child.CalculateInfo.Max) {
					return fmt.Errorf("checkSubAndParentGroupQuotaKey failed: %v's max keys are not all included in %v's",
						name, quotaInfo.Name)
				}
			} else {
				if !checkQuotaKeySame(quotaInfo.CalculateInfo.Max, child.CalculateInfo.Max) {
					return fmt.Errorf("checkSubAndParentGroupQuotaKey failed: %v's max keys are not the same with %v's",
						name, quotaInfo.Name)
				}
			}
			if !checkQuotaKeyIncluded(quotaInfo.CalculateInfo.Min, child.CalculateInfo.Min) {
				return fmt.Errorf("checkSubAndParentGroupQuotaKey failed: %v's min keys are not all included in %v's",
					name, quotaInfo.Name)
			}
		} else {
			return fmt.Errorf("internal error: quotaInfoMap and quotaTree information out of sync, losed :%v", name)
		}
	}

	return nil
}

// checkMinQuotaValidate will do two checks:
//  1. the sum of brothers' minquota should less than or equal to parentMinQuota.
//  2. the sum of children's minquota should less than or equal to newQuotaMin.
func (qt *quotaTopology) checkMinQuotaValidate(newQuotaInfo *QuotaInfo) error {
	if newQuotaInfo.AllowForceUpdate {
		return nil
	}

	// If the quota is tree root, we don't check it's min
	if newQuotaInfo.IsTreeRoot {
		return nil
	}

	if newQuotaInfo.ParentName != extension.RootQuotaName {
		childMinSumNotIncludeSelf, err := qt.getChildMinQuotaSumExceptSpecificChild(newQuotaInfo.ParentName, newQuotaInfo.Name)
		if err != nil {
			return fmt.Errorf("checkMinQuotaSum failed: %v", err)
		}

		childMinSumIncludeSelf := quotav1.Add(childMinSumNotIncludeSelf, newQuotaInfo.CalculateInfo.Min)
		if !util.LessThanOrEqualCompletely(childMinSumIncludeSelf, qt.quotaInfoMap[newQuotaInfo.ParentName].CalculateInfo.Min) {
			return fmt.Errorf("checkMinQuotaSum all brothers' MinQuota > parent MinQuota, parent: %v", newQuotaInfo.ParentName)
		}
	}

	// check children's minquota sum
	children, exist := qt.quotaHierarchyInfo[newQuotaInfo.Name]
	if !exist || len(children) == 0 {
		return nil
	}

	childMinSum, err := qt.getChildMinQuotaSumExceptSpecificChild(newQuotaInfo.Name, "")
	if err != nil {
		return fmt.Errorf("checkMinQuotaSum failed:%v", err)
	}

	if !util.LessThanOrEqualCompletely(childMinSum, newQuotaInfo.CalculateInfo.Min) {
		return fmt.Errorf("checkMinQuotaSum all children's MinQuota > current MinQuota, current: %v", newQuotaInfo.Name)
	}

	return nil
}

func (qt *quotaTopology) getChildMinQuotaSumExceptSpecificChild(parentName, skipQuota string) (allChildQuotaSum v1.ResourceList, err error) {
	allChildQuotaSum = v1.ResourceList{}
	if parentName == extension.RootQuotaName {
		return allChildQuotaSum, nil
	}

	children, exist := qt.quotaHierarchyInfo[parentName]
	if !exist {
		return nil, fmt.Errorf("not found child quota list, parent: %v", parentName)
	}

	for childName := range children {
		if childName == skipQuota {
			continue
		}

		if quotaInfo, exist := qt.quotaInfoMap[childName]; exist {
			allChildQuotaSum = quotav1.Add(allChildQuotaSum, quotaInfo.CalculateInfo.Min)
		} else {
			err = fmt.Errorf("BUG quotaInfoMap and quotaTree information out of sync, losed :%v", childName)
			return nil, err
		}
	}

	return allChildQuotaSum, nil
}

func toElasticQuota(obj interface{}) *v1alpha1.ElasticQuota {
	if obj == nil {
		return nil
	}

	var unstructuredObj *unstructured.Unstructured
	switch t := obj.(type) {
	case *v1alpha1.ElasticQuota:
		return obj.(*v1alpha1.ElasticQuota)
	case *unstructured.Unstructured:
		unstructuredObj = obj.(*unstructured.Unstructured)
	case cache.DeletedFinalStateUnknown:
		var ok bool
		unstructuredObj, ok = t.Obj.(*unstructured.Unstructured)
		if !ok {
			klog.Errorf("Fail to convert quota object %T to *unstructured.Unstructured", obj)
			return nil
		}
	default:
		klog.Errorf("Unable to handle quota object in %T", obj)
		return nil
	}

	quota := &v1alpha1.ElasticQuota{}
	err := scheme.Scheme.Convert(unstructuredObj, quota, nil)
	if err != nil {
		klog.Errorf("Fail to convert unstructed object %v to Quota: %v", obj, err)
		return nil
	}
	return quota
}

func quotaFieldsCopy(q *v1alpha1.ElasticQuota) v1alpha1.ElasticQuota {
	return v1alpha1.ElasticQuota{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				extension.LabelQuotaParent:   q.Labels[extension.LabelQuotaParent],
				extension.LabelQuotaIsParent: q.Labels[extension.LabelQuotaIsParent],
				extension.LabelQuotaTreeID:   q.Labels[extension.LabelQuotaTreeID],
			},
			Annotations: map[string]string{
				extension.AnnotationQuotaNamespaces: q.Annotations[extension.AnnotationQuotaNamespaces],
			},
		},
		Spec: *q.Spec.DeepCopy(),
	}
}

func checkQuotaKeySame(parent, child v1.ResourceList) bool {
	for k := range parent {
		if _, ok := child[k]; !ok {
			return false
		}
	}
	for k := range child {
		if _, ok := parent[k]; !ok {
			return false
		}
	}
	return true
}

// checkQuotaKeyIncluded will check whether the parent quota includes all keys of child quota.
func checkQuotaKeyIncluded(parent, child v1.ResourceList) bool {
	for k := range child {
		if _, ok := parent[k]; !ok {
			return false
		}
	}
	return true
}

func (qt *quotaTopology) checkGuaranteedForMin(quotaInfo *QuotaInfo) error {
	if quotaInfo.AllowForceUpdate {
		return nil
	}

	if quotaInfo.TreeID == "" {
		return nil
	}

	// If the quota is tree root, allow it change the min.
	if quotaInfo.IsTreeRoot {
		return nil
	}

	// if the new min less than guaranteed, which means that no more resource guarantee is needed, so it is allowed directly.
	if util.LessThanOrEqualCompletely(quotaInfo.CalculateInfo.Min, quotaInfo.CalculateInfo.Guaranteed) {
		return nil
	}
	newGuaranteed := quotav1.Max(quotaInfo.CalculateInfo.Min, quotaInfo.CalculateInfo.Guaranteed)

	return qt.checkParentGuaranteed(newGuaranteed, quotaInfo.Name, quotaInfo.ParentName)
}

// Guaranteed resources are searched starting from the parent node of the current node until the root node of the tree.
// We need to meet guaranteed resources at least at a certain level to guarantee the resources of the current node.
// During the search process, there may be situations where min is not used up at the intermediate nodes.
// Therefore, we need to recursively accumulate the guaranteed resources that need to be satisfied until the root node is reached.
func (qt *quotaTopology) checkParentGuaranteed(newGuarantee v1.ResourceList, self, parentName string) error {
	if parentName == extension.RootQuotaName {
		return fmt.Errorf("tree root quota %v can't guarantee for min", self)
	}

	parentInfo, ok := qt.quotaInfoMap[parentName]
	if !ok {
		return fmt.Errorf("parent %v not found", parentName)
	}

	children, ok := qt.quotaHierarchyInfo[parentName]
	if !ok {
		return fmt.Errorf("child quota list not found, parent: %v", parentName)
	}

	allChildrenGuaranteed := newGuarantee
	for childName := range children {
		if childName == self {
			continue
		}

		if quotaInfo, exist := qt.quotaInfoMap[childName]; exist {
			allChildrenGuaranteed = quotav1.Add(allChildrenGuaranteed, quotaInfo.CalculateInfo.Guaranteed)
		} else {
			return fmt.Errorf("BUG quotaInfoMap and quotaTree information out of sync, losed :%v", childName)
		}
	}

	newParentGuaranteed := quotav1.Max(parentInfo.CalculateInfo.Min, allChildrenGuaranteed)
	if util.LessThanOrEqualCompletely(newParentGuaranteed, parentInfo.CalculateInfo.Guaranteed) {
		return nil
	}

	return qt.checkParentGuaranteed(newParentGuaranteed, parentInfo.Name, parentInfo.ParentName)
}
