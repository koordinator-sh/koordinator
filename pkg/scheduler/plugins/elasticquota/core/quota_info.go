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
	"fmt"
	"sync"

	v1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
)

type QuotaCalculateInfo struct {
	// The semantics of "max" is the quota group's upper limit of resources.
	Max v1.ResourceList
	// The semantics of "min" is the quota group's guaranteed resources, if quota group's "request" less than or
	// equal to "min", the quota group can obtain equivalent resources to the "request"
	Min v1.ResourceList
	// If Child's sumMin is larger than totalResource, the value of Min should be scaled in equal proportion
	// to ensure the correctness and fairness of min
	AutoScaleMin v1.ResourceList
	// All assigned pods used
	Used v1.ResourceList
	// All non-preemptible pods used
	NonPreemptibleUsed v1.ResourceList
	// All pods request
	Request v1.ResourceList
	// All non-preemptible pods request
	NonPreemptibleRequest v1.ResourceList
	// ChildRquest is the sum of child quota requests.
	// If the quota is leaf, it's the sum of pods requests
	ChildRequest              v1.ResourceList
	SelfRequest               v1.ResourceList
	SelfUsed                  v1.ResourceList
	SelfNonPreemptibleUsed    v1.ResourceList
	SelfNonPreemptibleRequest v1.ResourceList
	// SharedWeight determines the ability of quota groups to compete for shared resources
	SharedWeight v1.ResourceList
	// Runtime is the current actual resource that can be used by the quota group
	Runtime v1.ResourceList

	// If the allocated is greater than min, the guaranteed resource is the allocated,
	// else the guaranteed is the min.
	Guaranteed v1.ResourceList
	// Allocated is the allocated resource. It's the sum of children quota guarantee. If the quota is leaf, it's
	// the sum of scheduled pods
	Allocated v1.ResourceList
}

type QuotaInfo struct {
	// Name
	Name string
	// Quota's ParentName
	ParentName string
	// IsParent quota group
	IsParent bool
	// If runtimeVersion not equal to quotaTree runtimeVersion, means runtime has been updated.
	RuntimeVersion int64
	// Allow lent resource to other quota group
	AllowLentResource bool
	CalculateInfo     QuotaCalculateInfo
	PodCache          map[string]*PodInfo
	lock              sync.Mutex
}

func NewQuotaInfo(isParent, allowLentResource bool, name, parentName string) *QuotaInfo {
	return &QuotaInfo{
		Name:              name,
		ParentName:        parentName,
		IsParent:          isParent,
		AllowLentResource: allowLentResource,
		RuntimeVersion:    0,
		PodCache:          make(map[string]*PodInfo),
		CalculateInfo: QuotaCalculateInfo{
			Max:                       v1.ResourceList{},
			AutoScaleMin:              v1.ResourceList{},
			Min:                       v1.ResourceList{},
			Used:                      v1.ResourceList{},
			NonPreemptibleUsed:        v1.ResourceList{},
			Request:                   v1.ResourceList{},
			NonPreemptibleRequest:     v1.ResourceList{},
			SharedWeight:              v1.ResourceList{},
			Runtime:                   v1.ResourceList{},
			ChildRequest:              v1.ResourceList{},
			Guaranteed:                v1.ResourceList{},
			Allocated:                 v1.ResourceList{},
			SelfRequest:               v1.ResourceList{},
			SelfUsed:                  v1.ResourceList{},
			SelfNonPreemptibleRequest: v1.ResourceList{},
			SelfNonPreemptibleUsed:    v1.ResourceList{},
		},
	}
}

func (qi *QuotaInfo) DeepCopy() *QuotaInfo {
	if qi == nil {
		return nil
	}
	qi.lock.Lock()
	defer qi.lock.Unlock()

	quotaInfo := &QuotaInfo{
		Name:              qi.Name,
		ParentName:        qi.ParentName,
		IsParent:          qi.IsParent,
		AllowLentResource: qi.AllowLentResource,
		RuntimeVersion:    qi.RuntimeVersion,
		PodCache:          make(map[string]*PodInfo),
		CalculateInfo: QuotaCalculateInfo{
			Max:                       qi.CalculateInfo.Max.DeepCopy(),
			AutoScaleMin:              qi.CalculateInfo.AutoScaleMin.DeepCopy(),
			Min:                       qi.CalculateInfo.Min.DeepCopy(),
			Used:                      qi.CalculateInfo.Used.DeepCopy(),
			NonPreemptibleUsed:        qi.CalculateInfo.NonPreemptibleUsed.DeepCopy(),
			Request:                   qi.CalculateInfo.Request.DeepCopy(),
			NonPreemptibleRequest:     qi.CalculateInfo.NonPreemptibleRequest.DeepCopy(),
			SharedWeight:              qi.CalculateInfo.SharedWeight.DeepCopy(),
			Runtime:                   qi.CalculateInfo.Runtime.DeepCopy(),
			ChildRequest:              qi.CalculateInfo.ChildRequest.DeepCopy(),
			Guaranteed:                qi.CalculateInfo.Guaranteed.DeepCopy(),
			Allocated:                 qi.CalculateInfo.Allocated.DeepCopy(),
			SelfRequest:               qi.CalculateInfo.SelfRequest.DeepCopy(),
			SelfUsed:                  qi.CalculateInfo.SelfUsed.DeepCopy(),
			SelfNonPreemptibleRequest: qi.CalculateInfo.SelfNonPreemptibleRequest.DeepCopy(),
			SelfNonPreemptibleUsed:    qi.CalculateInfo.SelfNonPreemptibleUsed.DeepCopy(),
		},
	}
	for name, pod := range qi.PodCache {
		quotaInfo.PodCache[name] = pod
	}
	return quotaInfo
}

func (qi *QuotaInfo) GetQuotaSummary(treeID string, includePods bool) *QuotaInfoSummary {
	qi.lock.Lock()
	defer qi.lock.Unlock()

	quotaInfoSummary := NewQuotaInfoSummary()
	quotaInfoSummary.Name = qi.Name
	quotaInfoSummary.ParentName = qi.ParentName
	quotaInfoSummary.IsParent = qi.IsParent
	quotaInfoSummary.RuntimeVersion = qi.RuntimeVersion
	quotaInfoSummary.AllowLentResource = qi.AllowLentResource
	quotaInfoSummary.Tree = treeID
	quotaInfoSummary.Max = qi.CalculateInfo.Max.DeepCopy()
	quotaInfoSummary.Min = qi.CalculateInfo.Min.DeepCopy()
	quotaInfoSummary.AutoScaleMin = qi.CalculateInfo.AutoScaleMin.DeepCopy()
	quotaInfoSummary.Used = qi.CalculateInfo.Used.DeepCopy()
	quotaInfoSummary.NonPreemptibleUsed = qi.CalculateInfo.NonPreemptibleUsed.DeepCopy()
	quotaInfoSummary.Request = qi.CalculateInfo.Request.DeepCopy()
	quotaInfoSummary.NonPreemptibleRequest = qi.CalculateInfo.NonPreemptibleRequest.DeepCopy()
	quotaInfoSummary.SharedWeight = qi.CalculateInfo.SharedWeight.DeepCopy()
	quotaInfoSummary.Runtime = qi.CalculateInfo.Runtime.DeepCopy()
	quotaInfoSummary.ChildRequest = qi.CalculateInfo.ChildRequest.DeepCopy()
	quotaInfoSummary.Allocated = qi.CalculateInfo.Allocated.DeepCopy()
	quotaInfoSummary.Guaranteed = qi.CalculateInfo.Guaranteed.DeepCopy()
	quotaInfoSummary.SelfUsed = qi.CalculateInfo.SelfUsed.DeepCopy()
	quotaInfoSummary.SelfRequest = qi.CalculateInfo.SelfRequest.DeepCopy()
	quotaInfoSummary.SelfNonPreemptibleUsed = qi.CalculateInfo.SelfNonPreemptibleUsed.DeepCopy()
	quotaInfoSummary.SelfNonPreemptibleRequest = qi.CalculateInfo.SelfNonPreemptibleRequest.DeepCopy()

	if includePods {
		for podName, podInfo := range qi.PodCache {
			quotaInfoSummary.PodCache[podName] = &SimplePodInfo{
				IsAssigned: podInfo.isAssigned,
				Resource:   podInfo.resource,
			}
		}
	}

	return quotaInfoSummary
}

// updateQuotaInfoFromRemote the CRD(max/oriMin/sharedWeight/allowLentResource/isParent/ParentName) of the quota maybe changed,
// so need update localQuotaInfo's information from inputQuotaInfo.
func (qi *QuotaInfo) updateQuotaInfoFromRemote(quotaInfo *QuotaInfo) {
	qi.lock.Lock()
	defer qi.lock.Unlock()

	qi.setMaxQuotaNoLock(quotaInfo.CalculateInfo.Max)
	qi.setMinQuotaNoLock(quotaInfo.CalculateInfo.Min)
	sharedWeight := quotaInfo.CalculateInfo.SharedWeight.DeepCopy()
	if quotav1.IsZero(sharedWeight) {
		sharedWeight = quotaInfo.CalculateInfo.Max.DeepCopy()
	}
	qi.CalculateInfo.SharedWeight = sharedWeight
	qi.AllowLentResource = quotaInfo.AllowLentResource
	qi.IsParent = quotaInfo.IsParent
	qi.ParentName = quotaInfo.ParentName
}

// getLimitRequestNoLock returns the min value of request and max, as max is the quotaGroup's upper limit of resources.
// As the multi-hierarchy quota Model described in the PR, when passing a request upwards, passing a request exceeding its
// max will result in a wrong/invalid runtime distribution. For example, parentQuotaGroup's Max is 20, childGroup's Max
// is 10, and the childGroup's request is 30. If the child passes 30 request upwards and get a 20 runtime back
// (limited by the parent's max is 20), the child can only use 10 (limited by its max).
func (qi *QuotaInfo) getLimitRequestNoLock() v1.ResourceList {
	limitRequest := qi.CalculateInfo.Request.DeepCopy()
	for resName, quantity := range limitRequest {
		if maxQuantity, ok := qi.CalculateInfo.Max[resName]; ok {
			if quantity.Cmp(maxQuantity) == 1 {
				// req > max, limitRequest = max
				limitRequest[resName] = maxQuantity.DeepCopy()
			}
		}
	}
	return limitRequest
}

func (qi *QuotaInfo) setMaxNoLock(max v1.ResourceList) {
	qi.CalculateInfo.Max = max.DeepCopy()
}

func (qi *QuotaInfo) setMinNoLock(min v1.ResourceList) {
	qi.CalculateInfo.Min = min.DeepCopy()
}

func (qi *QuotaInfo) addRequestNonNegativeNoLock(delta, deltaNonPreemptibleRequest v1.ResourceList, isSelfRequest bool) {
	qi.CalculateInfo.Request = quotav1.Add(qi.CalculateInfo.Request, delta)
	for _, resName := range quotav1.IsNegative(qi.CalculateInfo.Request) {
		qi.CalculateInfo.Request[resName] = createQuantity(0, resName)
	}
	qi.CalculateInfo.NonPreemptibleRequest = quotav1.Add(qi.CalculateInfo.NonPreemptibleRequest, deltaNonPreemptibleRequest)
	for _, resName := range quotav1.IsNegative(qi.CalculateInfo.NonPreemptibleRequest) {
		qi.CalculateInfo.NonPreemptibleRequest[resName] = createQuantity(0, resName)
	}

	if isSelfRequest {
		qi.CalculateInfo.SelfRequest = quotav1.Add(qi.CalculateInfo.SelfRequest, delta)
		for _, resName := range quotav1.IsNegative(qi.CalculateInfo.SelfRequest) {
			qi.CalculateInfo.SelfRequest[resName] = createQuantity(0, resName)
		}
		qi.CalculateInfo.SelfNonPreemptibleRequest = quotav1.Add(qi.CalculateInfo.SelfNonPreemptibleRequest, deltaNonPreemptibleRequest)
		for _, resName := range quotav1.IsNegative(qi.CalculateInfo.SelfNonPreemptibleRequest) {
			qi.CalculateInfo.SelfNonPreemptibleRequest[resName] = createQuantity(0, resName)
		}
	}
}

func (qi *QuotaInfo) addChildRequestNonNegativeNoLock(delta v1.ResourceList) {
	qi.CalculateInfo.ChildRequest = quotav1.Add(qi.CalculateInfo.ChildRequest, delta)
	for _, resName := range quotav1.IsNegative(qi.CalculateInfo.ChildRequest) {
		qi.CalculateInfo.ChildRequest[resName] = createQuantity(0, resName)
	}
}

func (qi *QuotaInfo) GetGuaranteed() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.Guaranteed.DeepCopy()
}

func (qi *QuotaInfo) GetAllocated() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.Allocated.DeepCopy()
}

func (qi *QuotaInfo) addUsedNonNegativeNoLock(delta, deltaNonPreemptibleUsed v1.ResourceList, isSelfUsed bool) {
	qi.CalculateInfo.Used = quotav1.Add(qi.CalculateInfo.Used, delta)
	qi.CalculateInfo.NonPreemptibleUsed = quotav1.Add(qi.CalculateInfo.NonPreemptibleUsed, deltaNonPreemptibleUsed)
	for _, resName := range quotav1.IsNegative(qi.CalculateInfo.Used) {
		qi.CalculateInfo.Used[resName] = createQuantity(0, resName)
	}
	for _, resName := range quotav1.IsNegative(qi.CalculateInfo.NonPreemptibleUsed) {
		qi.CalculateInfo.NonPreemptibleUsed[resName] = createQuantity(0, resName)
	}

	if isSelfUsed {
		qi.CalculateInfo.SelfUsed = quotav1.Add(qi.CalculateInfo.SelfUsed, delta)
		for _, resName := range quotav1.IsNegative(qi.CalculateInfo.SelfUsed) {
			qi.CalculateInfo.SelfUsed[resName] = createQuantity(0, resName)
		}
		qi.CalculateInfo.SelfNonPreemptibleUsed = quotav1.Add(qi.CalculateInfo.SelfNonPreemptibleUsed, deltaNonPreemptibleUsed)
		for _, resName := range quotav1.IsNegative(qi.CalculateInfo.SelfNonPreemptibleUsed) {
			qi.CalculateInfo.SelfNonPreemptibleUsed[resName] = createQuantity(0, resName)
		}
	}
}

func (qi *QuotaInfo) addAllocatedQuotaNoLock(delta v1.ResourceList) {
	qi.CalculateInfo.Allocated = quotav1.Add(qi.CalculateInfo.Allocated, delta)
	for _, resName := range quotav1.IsNegative(qi.CalculateInfo.Allocated) {
		qi.CalculateInfo.Allocated[resName] = createQuantity(0, resName)
	}
}

func (qi *QuotaInfo) setMaxQuotaNoLock(res v1.ResourceList) {
	qi.CalculateInfo.Max = res.DeepCopy()
}

func (qi *QuotaInfo) setMinQuotaNoLock(res v1.ResourceList) {
	qi.CalculateInfo.Min = res.DeepCopy()
}

func (qi *QuotaInfo) setAutoScaleMinQuotaNoLock(res v1.ResourceList) {
	qi.CalculateInfo.AutoScaleMin = res.DeepCopy()
}

func (qi *QuotaInfo) setSharedWeightNoLock(res v1.ResourceList) {
	qi.CalculateInfo.SharedWeight = res.DeepCopy()
}

func (qi *QuotaInfo) GetRequest() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.Request.DeepCopy()
}

func (qi *QuotaInfo) GetChildRequest() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.ChildRequest.DeepCopy()
}

func (qi *QuotaInfo) GetUsed() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.Used.DeepCopy()
}

func (qi *QuotaInfo) GetNonPreemptibleUsed() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.NonPreemptibleUsed.DeepCopy()
}

func (qi *QuotaInfo) GetNonPreemptibleRequest() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.NonPreemptibleRequest.DeepCopy()
}

func (qi *QuotaInfo) GetSelfRequest() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.SelfRequest.DeepCopy()
}

func (qi *QuotaInfo) GetSelfUsed() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.SelfUsed.DeepCopy()
}

func (qi *QuotaInfo) GetSelfNonPreemptibleUsed() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.SelfNonPreemptibleUsed.DeepCopy()
}

func (qi *QuotaInfo) GetSelfNonPreemptibleRequest() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.SelfNonPreemptibleRequest.DeepCopy()
}

func (qi *QuotaInfo) GetRuntime() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.Runtime.DeepCopy()
}

func (qi *QuotaInfo) GetMax() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.Max.DeepCopy()
}

func (qi *QuotaInfo) GetMin() v1.ResourceList {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	return qi.CalculateInfo.Min.DeepCopy()
}

func NewQuotaInfoFromQuota(quota *v1alpha1.ElasticQuota) *QuotaInfo {
	isParent := extension.IsParentQuota(quota)
	parentName := extension.GetParentQuotaName(quota)

	allowLentResource := extension.IsAllowLentResource(quota)
	if utilfeature.DefaultFeatureGate.Enabled(features.ElasticQuotaGuaranteeUsage) {
		allowLentResource = false
	}

	quotaInfo := NewQuotaInfo(isParent, allowLentResource, quota.Name, parentName)
	quotaInfo.setMinQuotaNoLock(quota.Spec.Min)
	quotaInfo.setMaxQuotaNoLock(quota.Spec.Max)
	newSharedWeight := extension.GetSharedWeight(quota)
	quotaInfo.setSharedWeightNoLock(newSharedWeight)

	return quotaInfo
}

func (qi *QuotaInfo) getMaskedRuntimeNoLock() v1.ResourceList {
	return quotav1.Mask(qi.CalculateInfo.Runtime, quotav1.ResourceNames(qi.CalculateInfo.Max))
}

func (qi *QuotaInfo) clearForResetNoLock() {
	qi.CalculateInfo.Request = v1.ResourceList{}
	qi.CalculateInfo.NonPreemptibleRequest = v1.ResourceList{}
	qi.CalculateInfo.Used = v1.ResourceList{}
	qi.CalculateInfo.NonPreemptibleUsed = v1.ResourceList{}
	qi.CalculateInfo.Runtime = v1.ResourceList{}
	qi.CalculateInfo.ChildRequest = v1.ResourceList{}
	qi.CalculateInfo.Guaranteed = v1.ResourceList{}
	qi.CalculateInfo.Allocated = v1.ResourceList{}
	qi.CalculateInfo.SelfUsed = v1.ResourceList{}
	qi.CalculateInfo.SelfRequest = v1.ResourceList{}
	qi.CalculateInfo.SelfNonPreemptibleUsed = v1.ResourceList{}
	qi.CalculateInfo.SelfNonPreemptibleRequest = v1.ResourceList{}
	qi.RuntimeVersion = 0
}

func (qi *QuotaInfo) isQuotaMetaChange(quotaInfo *QuotaInfo) bool {
	qi.lock.Lock()
	defer qi.lock.Unlock()

	if qi.AllowLentResource != quotaInfo.AllowLentResource ||
		qi.IsParent != quotaInfo.IsParent ||
		qi.ParentName != quotaInfo.ParentName {
		return true
	}
	return false
}

func (qi *QuotaInfo) IsPodExist(pod *v1.Pod) bool {
	qi.lock.Lock()
	defer qi.lock.Unlock()
	_, exist := qi.PodCache[generatePodCacheKey(pod)]
	return exist
}

func (qi *QuotaInfo) addPodIfNotPresent(pod *v1.Pod) {
	qi.lock.Lock()
	defer qi.lock.Unlock()

	key := generatePodCacheKey(pod)
	if _, exist := qi.PodCache[key]; exist {
		klog.Errorf("pod already exist in PodCache quota:%v, podKey:%v", qi.Name, key)
		return
	}
	qi.PodCache[key] = NewPodInfo(pod)
}

func (qi *QuotaInfo) removePodIfPresent(pod *v1.Pod) {
	qi.lock.Lock()
	defer qi.lock.Unlock()

	key := generatePodCacheKey(pod)
	if _, exist := qi.PodCache[key]; !exist {
		klog.Errorf("pod not exist in PodRequestMap quota:%v, podName:%v", qi.Name, key)
		return
	}

	delete(qi.PodCache, key)
}

func (qi *QuotaInfo) UpdatePodIsAssigned(pod *v1.Pod, isAssigned bool) error {
	qi.lock.Lock()
	defer qi.lock.Unlock()

	key := generatePodCacheKey(pod)
	podInfo, exist := qi.PodCache[key]
	if !exist {
		return fmt.Errorf("pod is not in PodCache quota:%v, podName:%v", qi.Name, key)
	}
	if podInfo.isAssigned == isAssigned {
		return fmt.Errorf("pod's running phase doesn't change, quota:%v, pod:%v", qi.Name, key)
	}
	qi.PodCache[key].isAssigned = isAssigned
	return nil
}

func (qi *QuotaInfo) GetPodCache() map[string]*v1.Pod {
	qi.lock.Lock()
	defer qi.lock.Unlock()

	pods := make(map[string]*v1.Pod)
	for name, podInfo := range qi.PodCache {
		pods[name] = podInfo.pod
	}
	return pods
}

func (qi *QuotaInfo) CheckPodIsAssigned(pod *v1.Pod) bool {
	qi.lock.Lock()
	defer qi.lock.Unlock()

	if pod == nil {
		return false
	}

	if podInfo, exist := qi.PodCache[generatePodCacheKey(pod)]; exist {
		return podInfo.isAssigned
	}
	return false
}

func (qi *QuotaInfo) GetPodThatIsAssigned() []*v1.Pod {
	qi.lock.Lock()
	defer qi.lock.Unlock()

	pods := make([]*v1.Pod, 0)
	for _, podInfo := range qi.PodCache {
		if podInfo.isAssigned {
			pods = append(pods, podInfo.pod)
		}
	}
	return pods
}

func (qi *QuotaInfo) Lock() {
	qi.lock.Lock()
}

func (qi *QuotaInfo) UnLock() {
	qi.lock.Unlock()
}

// QuotaTopoNode only contains the topology of the parent/child relationship,
// helps to reconstruct quotaTree from the rootQuotaGroup to all the leafQuotaNode.
type QuotaTopoNode struct {
	name                 string
	quotaInfo            *QuotaInfo
	parQuotaTopoNode     *QuotaTopoNode
	childGroupQuotaInfos map[string]*QuotaTopoNode
}

func NewQuotaTopoNode(name string, quotaInfo *QuotaInfo) *QuotaTopoNode {
	return &QuotaTopoNode{
		name:                 name,
		quotaInfo:            quotaInfo, // not deepCopy
		childGroupQuotaInfos: make(map[string]*QuotaTopoNode),
	}
}

func (qtn *QuotaTopoNode) addChildGroupQuotaInfo(childNode *QuotaTopoNode) {
	qtn.childGroupQuotaInfos[childNode.name] = childNode
}

func (qtn *QuotaTopoNode) getChildGroupQuotaInfos() map[string]*QuotaTopoNode {
	group := make(map[string]*QuotaTopoNode)
	for key, v := range qtn.childGroupQuotaInfos {
		group[key] = v
	}
	return group
}

type PodInfo struct {
	pod        *v1.Pod
	isAssigned bool
	resource   v1.ResourceList
}

func NewPodInfo(pod *v1.Pod) *PodInfo {
	res := PodRequests(pod)
	return &PodInfo{
		pod:      pod,
		resource: res,
	}
}

func (pInfo *PodInfo) DeepCopy() *PodInfo {
	newPodInfo := &PodInfo{
		pod:        pInfo.pod.DeepCopy(),
		isAssigned: pInfo.isAssigned,
		resource:   pInfo.resource.DeepCopy(),
	}
	return newPodInfo
}

func generatePodCacheKey(pod *v1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}
