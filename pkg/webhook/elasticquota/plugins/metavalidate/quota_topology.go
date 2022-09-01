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
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned"
	"sigs.k8s.io/scheduler-plugins/pkg/generated/informers/externalversions"
	genv1alpha1 "sigs.k8s.io/scheduler-plugins/pkg/generated/listers/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

type quotaTopology struct {
	lock sync.Mutex
	// quotaInfoMap stores all quota information
	quotaInfoMap map[string]*core.QuotaInfo
	// quotaHierarchy stores the quota's all children
	quotaHierarchyInfo map[string]map[string]struct{}
	// podCache stores the quota's all pods
	podCache map[string]map[string]struct{}
	eqLister genv1alpha1.ElasticQuotaLister
}

func NewQuotaTopology() *quotaTopology {
	topology := &quotaTopology{
		quotaInfoMap:       make(map[string]*core.QuotaInfo),
		quotaHierarchyInfo: make(map[string]map[string]struct{}),
		podCache:           make(map[string]map[string]struct{}),
	}

	quotaCliSet, kubeCliSet := topology.createClient()
	kubeSharedInformerFactory := informers.NewSharedInformerFactory(kubeCliSet, 0)
	podInformer := kubeSharedInformerFactory.Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    topology.OnPodAdd,
		UpdateFunc: topology.OnPodUpdate,
		DeleteFunc: topology.OnPodDelete,
	})

	scheSharedInformerFactory := externalversions.NewSharedInformerFactory(quotaCliSet, 0)
	quotaInformer := scheSharedInformerFactory.Scheduling().V1alpha1().ElasticQuotas().Informer()
	topology.eqLister = scheSharedInformerFactory.Scheduling().V1alpha1().ElasticQuotas().Lister()
	quotaInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    topology.OnQuotaAdd,
		UpdateFunc: topology.OnQuotaUpdate,
		DeleteFunc: topology.OnQuotaDelete,
	})

	ctx := context.TODO()
	kubeSharedInformerFactory.Start(ctx.Done())
	scheSharedInformerFactory.Start(ctx.Done())

	kubeSharedInformerFactory.WaitForCacheSync(ctx.Done())
	scheSharedInformerFactory.WaitForCacheSync(ctx.Done())

	return topology
}

func (qt *quotaTopology) OnQuotaAdd(obj interface{}) {
	klog.V(2).Infof("OnAdd Quota to cache:%v", obj)

	quota := toElasticQuota(obj)
	if quota == nil {
		klog.Errorf("cannot convert to *ElasticQuota:%v", obj)
		return
	}

	quotaInfo := core.NewQuotaInfoFromQuota(quota)
	qt.lock.Lock()
	defer qt.lock.Unlock()

	qt.quotaInfoMap[quotaInfo.Name] = quotaInfo
	qt.podCache[quotaInfo.Name] = make(map[string]struct{})
	if _, ok := qt.quotaHierarchyInfo[quotaInfo.Name]; !ok {
		qt.quotaHierarchyInfo[quotaInfo.Name] = make(map[string]struct{})
	}
	if quotaInfo.ParentName != extension.RootQuotaName {
		if qt.quotaHierarchyInfo[quotaInfo.ParentName] == nil {
			qt.quotaHierarchyInfo[quotaInfo.ParentName] = make(map[string]struct{})
		}
		qt.quotaHierarchyInfo[quotaInfo.ParentName][quotaInfo.Name] = struct{}{}
	}
}

func (qt *quotaTopology) OnQuotaUpdate(oldObj, newObj interface{}) {
	oldQuota := toElasticQuota(oldObj)
	if oldQuota == nil {
		klog.Errorf("cannot convert to *v1.Quota:%v", oldObj)
		return
	}
	newQuota := toElasticQuota(newObj)
	if newQuota == nil {
		klog.Errorf("cannot convert to *v1.Quota:%v", newObj)
		return
	}

	oldQuotaInfo := core.NewQuotaInfoFromQuota(oldQuota)
	newQuotaInfo := core.NewQuotaInfoFromQuota(newQuota)

	qt.quotaInfoMap[newQuotaInfo.Name] = newQuotaInfo
	// parentQuotaName change
	if oldQuotaInfo.ParentName != newQuotaInfo.ParentName {
		delete(qt.quotaHierarchyInfo[oldQuotaInfo.ParentName], oldQuotaInfo.Name)
		if qt.quotaHierarchyInfo[newQuotaInfo.ParentName] == nil {
			qt.quotaHierarchyInfo[newQuotaInfo.ParentName] = make(map[string]struct{})
		}
		qt.quotaHierarchyInfo[newQuotaInfo.ParentName][newQuotaInfo.Name] = struct{}{}
	}
}

func (qt *quotaTopology) OnQuotaDelete(obj interface{}) {
	quota := toElasticQuota(obj)
	if quota == nil {
		klog.Errorf("cannot convert to *v1.Quota:%v", obj)
		return
	}

	parentName := extension.GetParentQuotaName(quota)

	qt.lock.Lock()
	defer qt.lock.Unlock()

	if parentName != extension.RootQuotaName {
		delete(qt.quotaHierarchyInfo[parentName], quota.Name)
	}
	delete(qt.quotaHierarchyInfo, quota.Name)
	delete(qt.quotaInfoMap, quota.Name)
	delete(qt.podCache, quota.Name)
}

func (qt *quotaTopology) OnPodAdd(obj interface{}) {
	pod := obj.(*v1.Pod)

	qt.lock.Lock()
	defer qt.lock.Unlock()

	quotaName := qt.getQuotaNameFromPodNoLock(pod)
	qt.podCache[quotaName][pod.Name] = struct{}{}
	klog.V(5).Infof("onPodAdd, link pod :%v to quota:%v", pod.Name, quotaName)
}

func (qt *quotaTopology) OnPodUpdate(oldObj, newObj interface{}) {
	oldPod := oldObj.(*v1.Pod)
	newPod := newObj.(*v1.Pod)

	qt.lock.Lock()
	defer qt.lock.Unlock()

	oldQuotaName := qt.getQuotaNameFromPodNoLock(oldPod)
	newQuotaName := qt.getQuotaNameFromPodNoLock(newPod)
	delete(qt.podCache[oldQuotaName], oldPod.Name)
	qt.podCache[newQuotaName][newPod.Name] = struct{}{}
}

func (qt *quotaTopology) OnPodDelete(obj interface{}) {
	pod := obj.(*v1.Pod)

	qt.lock.Lock()
	defer qt.lock.Unlock()

	quotaName := qt.getQuotaNameFromPodNoLock(pod)
	delete(qt.podCache[quotaName], pod.Name)
}

func (qt *quotaTopology) AddQuota(quota *v1alpha1.ElasticQuota) (err error) {
	if quota == nil {
		return fmt.Errorf("AddQuota param is nil")
	}

	qt.lock.Lock()
	defer qt.lock.Unlock()

	if _, exist := qt.quotaInfoMap[quota.Name]; exist {
		return fmt.Errorf("quota already exist:%v", quota.Name)
	}

	// fill quota with basic Information
	err = qt.fillDefaultQuotaInfo(quota)
	if err != nil {
		klog.Errorf("AddQuota fillDefaultQuotaInfo failed:%v", err)
		return err
	}

	err = qt.basicItemCheck(quota)
	if err != nil {
		klog.Errorf("AddQuota basicItemCheck failed :%v", err)
		return err
	}

	quotaInfo := core.NewQuotaInfoFromQuota(quota)

	err = qt.validateQuota(quotaInfo)
	if err != nil {
		klog.Errorf("AddQuota validateQuota failed:%v", err)
		return err
	}

	qt.quotaInfoMap[quotaInfo.Name] = quotaInfo
	qt.podCache[quotaInfo.Name] = make(map[string]struct{})
	if _, ok := qt.quotaHierarchyInfo[quotaInfo.Name]; !ok {
		qt.quotaHierarchyInfo[quotaInfo.Name] = make(map[string]struct{})
	}
	if quotaInfo.ParentName != extension.RootQuotaName {
		qt.quotaHierarchyInfo[quotaInfo.ParentName][quotaInfo.Name] = struct{}{}
	}
	return nil
}

func (qt *quotaTopology) UpdateQuota(oldQuota, originQuota *v1alpha1.ElasticQuota) (err error) {
	if originQuota == nil {
		return fmt.Errorf("AddQuota param is nil")
	}

	if oldQuota != nil && reflect.DeepEqual(quotaFieldsCopy(oldQuota), quotaFieldsCopy(originQuota)) {
		return nil
	}

	quotaName := originQuota.Name
	qt.lock.Lock()
	defer qt.lock.Unlock()
	localQuotaInfo, exist := qt.quotaInfoMap[quotaName]
	if !exist {
		err = fmt.Errorf("quota not exist:%v", quotaName)
		klog.Errorf("UpdateQuota failed:%v", err)
		return err
	}

	err = qt.basicItemCheck(originQuota)
	if err != nil {
		klog.Errorf("UpdateQuota basicItemCheck failed %v", err)
		return err
	}

	newQuotaInfo := core.NewQuotaInfoFromQuota(originQuota)
	// quota 信息检查
	err = qt.validateQuota(newQuotaInfo)
	if err != nil {
		klog.Errorf("UpdateQuota validateQuota failed:%v", err)
		return err
	}

	// quota 层次关系发生改变
	err = qt.checkQuotaHierarchyHasChange(localQuotaInfo, newQuotaInfo)
	if err != nil {
		klog.Errorf("UpdateQuota checkQuotaHierarchyHasChange failed:%v", err)
		return err
	}

	qt.quotaInfoMap[quotaName] = newQuotaInfo
	return nil
}

func (qt *quotaTopology) DeleteQuota(quotaName string) (err error) {
	qt.lock.Lock()
	defer qt.lock.Unlock()

	quotaInfo, exist := qt.quotaInfoMap[quotaName]
	if !exist {
		return fmt.Errorf("not found quota:%v", quotaName)
	}

	// check has child quota
	if childSet, exist := qt.quotaHierarchyInfo[quotaName]; exist {
		if len(childSet) > 0 {
			return fmt.Errorf("delete quota failed:%v, has child quota", quotaName)
		}
	} else {
		return fmt.Errorf("BUG quotaMap and quotaTree information out of sync, losed :%v", quotaName)
	}

	// 从父quota中删除
	if quotaInfo.ParentName != extension.RootQuotaName {
		delete(qt.quotaHierarchyInfo[quotaInfo.ParentName], quotaName)
	}
	// 从拓扑结构中删除
	delete(qt.quotaHierarchyInfo, quotaName)
	delete(qt.quotaInfoMap, quotaName)
	delete(qt.podCache, quotaName)
	return nil
}

func (qt *quotaTopology) ValidateDeleteQuota(quota *v1alpha1.ElasticQuota) (err error) {
	qt.lock.Lock()
	defer qt.lock.Unlock()

	quotaName := quota.Name
	info, exist := qt.quotaInfoMap[quotaName]
	if !exist {
		return fmt.Errorf("not found quota:%v", quotaName)
	}

	// check has child quota
	if childSet, exist := qt.quotaHierarchyInfo[quotaName]; exist {
		if len(childSet) > 0 {
			return fmt.Errorf("delete quota failed:%v, has child quota", quotaName)
		}
	} else {
		return fmt.Errorf("BUG quotaMap and quotaTree information out of sync, losed :%v", quotaName)
	}

	if !info.IsParent {
		if len(qt.podCache[quotaName]) > 0 {
			return fmt.Errorf("delete quota failed:%v, has linked pods", quotaName)
		}
	}
	return nil
}

func (qt *quotaTopology) basicItemCheck(quota *v1alpha1.ElasticQuota) (err error) {
	if invalid, err := extension.IsForbiddenModify(quota); invalid {
		return err
	}

	if parentName, exist := quota.Labels[extension.LabelQuotaParent]; exist && parentName != extension.RootQuotaName {
		parentInfo, find := qt.quotaInfoMap[parentName]
		if !find {
			err = fmt.Errorf("%v has parentName %v but not found parentInfo", quota.Name, parentName)
			return err
		}
		if parentInfo.Name != parentName {
			err = fmt.Errorf("%v parentInfo.Name != quota.parentName:[%v,%v]", quota.Name, parentInfo.Name, parentName)
			return err
		}
	}

	// min and max's each dimension should not have negative value
	for key, val := range quota.Spec.Max {
		if val.Value() < 0 {
			return fmt.Errorf("%v quota.Spec.Max[:%v < 0 %v", quota.Name, key, val.Value())
		}
	}
	for key, val := range quota.Spec.Min {
		if val.Value() < 0 {
			return fmt.Errorf("%v quota.Spec.Min[:%v < 0 %v", quota.Name, key, val.Value())
		}
	}

	// minQuota <= maxQuota
	valid := true
	for key, val := range quota.Spec.Min {
		if maxVal, exist := quota.Spec.Max[key]; exist {
			if maxVal.Cmp(val) == -1 {
				valid = false
			}
		} else {
			valid = false
		}
		if !valid {
			err = fmt.Errorf("%v min :%v > max,%v", quota.Name, quota.Spec.Min, quota.Spec.Max)
			return err
		}
	}
	return nil
}

func (qt *quotaTopology) fillDefaultQuotaInfo(quota *v1alpha1.ElasticQuota) (err error) {
	if quota.Labels == nil {
		quota.Labels = make(map[string]string)
	}
	if quota.Annotations == nil {
		quota.Annotations = make(map[string]string)
	}

	// if not configure
	if parentName, exist := quota.Labels[extension.LabelQuotaParent]; !exist || len(parentName) == 0 {
		quota.Labels[extension.LabelQuotaParent] = extension.RootQuotaName
		klog.V(3).Infof("fill quota root parent info: %v", quota.Name)
	}
	maxQuota, err := json.Marshal(&quota.Spec.Max)
	if err != nil {
		err = fmt.Errorf("fillDefaultQuotaInfo marshal quota max failed:%v", err)
		return err
	}
	if sharedWeight, exist := quota.Annotations[extension.AnnotationSharedWeight]; !exist || len(sharedWeight) == 0 {
		quota.Annotations[extension.AnnotationSharedWeight] = string(maxQuota)
	}
	return nil
}

func (qt *quotaTopology) validateQuota(quotaInfo *core.QuotaInfo) (err error) {
	if quotaInfo.ParentName == extension.RootQuotaName && !quotaInfo.IsParent {
		return nil
	}

	if !qt.checkSubAndParentGroupMaxQuotaKeySame(quotaInfo) {
		return fmt.Errorf("%v checkSubAndParentmaxQuotaKeySame failed", quotaInfo.Name)
	}

	if !qt.checkMaxQuota(quotaInfo) {
		return fmt.Errorf("%v checkMaxQuota failed", quotaInfo.Name)
	}

	if !qt.checkMinQuotaSum(quotaInfo) {
		return fmt.Errorf("%v checkMinQuotaSum failed", quotaInfo.Name)
	}

	return nil
}

func (qt *quotaTopology) checkSubAndParentGroupMaxQuotaKeySame(quotaInfo *core.QuotaInfo) bool {
	if quotaInfo.ParentName == extension.RootQuotaName {
		return true
	}

	parentInfo, exist := qt.quotaInfoMap[quotaInfo.ParentName]
	if !exist {
		klog.Errorf("checkSubAndParentGroupMaxQuotaKeySame failed:%v not found parent quotaInfo: %v",
			quotaInfo.Name, quotaInfo.ParentName)
		return false
	}

	for k := range quotaInfo.CalculateInfo.Max {
		if _, ok := parentInfo.CalculateInfo.Max[k]; !ok {
			return false
		}
	}

	for k := range parentInfo.CalculateInfo.Max {
		if _, ok := quotaInfo.CalculateInfo.Max[k]; !ok {
			return false
		}
	}

	return true
}

func (qt *quotaTopology) checkMinQuotaSum(quotaInfo *core.QuotaInfo) bool {
	if quotaInfo.ParentName != extension.RootQuotaName {
		// parent's minQuota >= allChildren's sum
		childMinSumNotIncludeSelf, err := qt.getChildMinQuotaSum(quotaInfo.ParentName)
		if err != nil {
			klog.Errorf("checkMinQuotaSum failed:%v", err)
			return false
		}

		parentInfo, exist := qt.quotaInfoMap[quotaInfo.ParentName]
		if !exist {
			klog.Errorf("checkMinQuotaSum %v not found parentInfo:%v", quotaInfo.Name, quotaInfo.ParentName)
			return false
		}

		childMinSumIncludeSelf := quotav1.Add(childMinSumNotIncludeSelf, quotaInfo.CalculateInfo.OriginalMin)
		if notLarger, _ := quotav1.LessThanOrEqual(childMinSumIncludeSelf, parentInfo.CalculateInfo.OriginalMin); !notLarger {
			klog.Errorf("checkMinQuotaSum allChild SumMinQuota > parentMinQuota %v", quotaInfo.ParentName)
			return false
		}
	}

	children, exist := qt.quotaHierarchyInfo[quotaInfo.Name]
	if !exist || len(children) == 0 {
		// this quotaGroup doesn't have childQuota
		return true
	}

	childMinSum, err := qt.getChildMinQuotaSum(quotaInfo.Name)
	if err != nil {
		klog.Errorf("checkMinQuotaSum failed:%v", err)
	}

	if notLarger, _ := quotav1.LessThanOrEqual(childMinSum, quotaInfo.CalculateInfo.OriginalMin); !notLarger {
		klog.Errorf("checkMinQuotaSum allChild SumMinQuota > MinQuota %v", quotaInfo.Name)
		return false
	}

	return true
}

func (qt *quotaTopology) getChildMinQuotaSum(parentName string) (allChildQuotaSum v1.ResourceList, err error) {
	allChildQuotaSum = v1.ResourceList{}
	if parentName == extension.RootQuotaName {
		return allChildQuotaSum, nil
	}

	childQuotaList, exist := qt.quotaHierarchyInfo[parentName]
	if !exist {
		return nil, fmt.Errorf("not found child quota list:%v", parentName)
	}

	for childName := range childQuotaList {
		if quotaInfo, exist := qt.quotaInfoMap[childName]; exist {
			allChildQuotaSum = quotav1.Add(allChildQuotaSum, quotaInfo.CalculateInfo.OriginalMin)
		} else {
			err = fmt.Errorf("BUG quotaInfoMap and quotaTree information out of sync, losed :%v", childName)
			klog.Error(err)
			return nil, err
		}
	}

	return allChildQuotaSum, nil
}

func (qt *quotaTopology) checkMaxQuota(quotaInfo *core.QuotaInfo) bool {
	if quotaInfo.ParentName != extension.RootQuotaName {
		parentInfo, exist := qt.quotaInfoMap[quotaInfo.ParentName]
		if !exist {
			klog.Errorf("checkQuotaMax %v not found parentInfo:%v", quotaInfo.Name, quotaInfo.ParentName)
			return false
		}
		for key, val := range parentInfo.CalculateInfo.Max {
			childMax := quotaInfo.CalculateInfo.Max[key]
			if childMax.Cmp(val) == 1 {
				klog.Errorf("checkQuotaMax maxQuota(%v) > parentMaxQuota %v(%v)", quotaInfo.CalculateInfo.Max, quotaInfo.ParentName, parentInfo.CalculateInfo.Max)
				return false
			}
		}
	}

	children, exist := qt.quotaHierarchyInfo[quotaInfo.Name]
	if !exist || len(children) == 0 {
		return true
	}

	// maxQuota should be >= each child's maxQuota
	for childName := range children {
		if childQuotaInfo, exist := qt.quotaInfoMap[childName]; exist {
			for key, val := range quotaInfo.CalculateInfo.Max {
				childMax := childQuotaInfo.CalculateInfo.Max[key]
				if childMax.Cmp(val) == 1 {
					klog.Errorf("checkQuotaMax child %v(%v) maxQuota > self maxQuota(%v)", childName, childQuotaInfo.CalculateInfo.Max, quotaInfo.CalculateInfo.Max)
					return false
				}
			}
		} else {
			klog.Errorf("BUG quotaInfoMap and quotaTree information out of sync, losed :%v", childName)
			return false
		}
	}
	return true
}

func (qt *quotaTopology) checkQuotaHierarchyHasChange(oldQuotaInfo, newQuotaInfo *core.QuotaInfo) error {
	// parentQuota -> childQuota, check whether the quotaInfo has child or not
	if oldQuotaInfo.IsParent == true && newQuotaInfo.IsParent == false {
		childQuotaSet, exist := qt.quotaHierarchyInfo[newQuotaInfo.Name]
		if !exist {
			return fmt.Errorf(":%v not hound quota hierarchy", newQuotaInfo.Name)
		}
		if len(childQuotaSet) > 0 {
			return fmt.Errorf("can not modify IsParent to false, because this groups still has child quota")
		}
	}

	if oldQuotaInfo.ParentName != newQuotaInfo.ParentName {
		if oldQuotaInfo.ParentName != extension.RootQuotaName {
			delete(qt.quotaHierarchyInfo[oldQuotaInfo.ParentName], oldQuotaInfo.Name)
		}

		if newQuotaInfo.ParentName != extension.RootQuotaName {
			qt.quotaHierarchyInfo[newQuotaInfo.ParentName][newQuotaInfo.Name] = struct{}{}
		}
	}
	return nil
}

func (qt *quotaTopology) createClient() (quotaCliSet *versioned.Clientset, kubeCliSet *clientset.Clientset) {
	kubeConfig := config.GetConfigOrDie()
	quotaCliSet = versioned.NewForConfigOrDie(kubeConfig)
	kubeCliSet = clientset.NewForConfigOrDie(kubeConfig)
	return quotaCliSet, kubeCliSet
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
				extension.LabelQuotaParent:       q.Labels[extension.LabelQuotaParent],
				extension.LabelQuotaIsParent:     q.Labels[extension.LabelQuotaIsParent],
				extension.LabelAllowLentResource: q.Labels[extension.LabelAllowLentResource],
			},
			Annotations: map[string]string{
				extension.AnnotationSharedWeight: q.Annotations[extension.AnnotationSharedWeight],
			},
		},
		Spec: *q.Spec.DeepCopy(),
	}
}

func (qt *quotaTopology) getQuotaNameFromPodNoLock(pod *v1.Pod) string {
	quotaLabelName := pod.Labels[extension.LabelQuotaName]
	if quotaLabelName == "" {
		list, err := qt.eqLister.ElasticQuotas(pod.Namespace).List(labels.Everything())
		if err != nil {
			runtime.HandleError(err)
			return extension.DefaultQuotaName
		}
		if len(list) == 0 {
			return extension.DefaultQuotaName
		}
		quotaLabelName = list[0].Name
	}

	if _, exist := qt.quotaInfoMap[quotaLabelName]; !exist {
		quotaLabelName = extension.DefaultQuotaName
	}
	return quotaLabelName
}

type QuotaTopologyForMarshal struct {
	QuotaInfoMap       map[string]*core.QuotaInfo
	QuotaHierarchyInfo map[string][]string
	PodCache           map[string][]string
}

func (qt *quotaTopology) GetQuotaTopologyInfo() *QuotaTopologyForMarshal {
	result := &QuotaTopologyForMarshal{
		QuotaInfoMap:       make(map[string]*core.QuotaInfo),
		QuotaHierarchyInfo: make(map[string][]string),
		PodCache:           make(map[string][]string),
	}
	qt.lock.Lock()
	defer qt.lock.Unlock()
	for key, val := range qt.quotaInfoMap {
		result.QuotaInfoMap[key] = val
	}
	for key, val := range qt.quotaHierarchyInfo {
		childQuotas := make([]string, 0, len(val))
		for name := range val {
			childQuotas = append(childQuotas, name)
		}
		result.QuotaHierarchyInfo[key] = childQuotas
	}
	for key, val := range qt.podCache {
		pods := make([]string, 0, len(val))
		for name := range val {
			pods = append(pods, name)
		}
		result.PodCache[key] = pods
	}
	return result
}
