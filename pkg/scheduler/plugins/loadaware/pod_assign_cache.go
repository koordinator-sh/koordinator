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

package loadaware

import (
	"reflect"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/utils/clock"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware/estimator"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

// podAssignCache stores the NodeMetric and Pod information that has been successfully scheduled or is about to be bound.
//
// The cache must handle multiple goroutines' object delta events concurrently.
//  1. When NodeMetric or Pod add or update event on a new node is received, a new nodeInfo should be created and stored in the cache.
//  2. When NodeMetric or Pod event on an existing node in cache is received, the corresponding nodeInfo should be updated.
//  3. When NodeMetric and Pod on a node are all deleted, the corresponding nodeInfo will be empty and should be deleted.
//
// # Implementation
//
// We implement the cache with nodeInfo items indexed by nodeName and maintain them in following rules:
//  1. To delete empty nodeInfo from cache correctly, handlers should hold both the cache lock and this nodeInfo lock when deleting.
//  2. To minimize event handlers' lock occupation on the entire cache, especially write lock, we choose to lock the nodeInfo
//     first and the cache then for deletion. We don't need to hold read lock the cache once for update,
//     release it and write lock the cache again for deletion if events makes nodeInfo empty.
//  3. To prevent deadlock risk with Rule 2, we MUST not lock any nodeInfo when holding cache lock already,
//     which means we have to unlock the cache before locking the nodeInfo.
//  4. Goroutine might get a write locked nodeInfo from cache because of Rule 3, which another goroutine is adding
//     or deleting object on it. When it grabs the lock, this nodeInfo might be empty and removed from cache.
//     We add a deleted flag in nodeInfo, which indicates this case. For adding or updating object event, we MUST not use
//     the deleted nodeInfo, instead, we should retry to get or create a new one from cache to prevent object event missing.
//     For deleting object event or reading nodeInfo data, we can ignore this flag.
//  5. When add or update object, we will try to get a loaded nodeInfo from cache, if not exist, we will create and store a new one.
//     When new nodeInfo is stored, it should be locked already to avoid race conditions on reading brought by Rule 3.
//
// # Concurrent Scenarios
// 
// Adding on existing nodeInfo
//  - Alice: <get nodeInfo> - (nodeInfo Lock) - (nodeInfo Update) - (nodeInfo Unlock)
//  - Bob: <get nodeInfo> - (nodeInfo Lock after Alice Unlock) - (nodeInfo Update) - (nodeInfo Unlock)
// Adding for not existing nodeInfo
//  - Alice: <create locked nodeInfo, store, return> - (nodeInfo Update) - (nodeInfo Unlock)
//  - Bob: <get nodeInfo after Alice Unlock cache> - (nodeInfo Lock after Alice Unlock) - (nodeInfo Update) - (nodeInfo Unlock)
// Deleting and adding nodeInfo
//  - Alice: <get nodeInfo> - (nodeInfo Lock) - (nodeInfo Update) - (nodeInfo tag deleted) - <delete nodeInfo> - (nodeInfo Unlock)
//  - Bob: <get nodeInfo when cache is not locked> - (nodeInfo Lock after Alice Unlock) - (nodeInfo find deleted) - (nodeInfo Unlock) - return failed and retry adding
// Reading and writing on existing nodeInfo
//  - Alice: <get nodeInfo> - (nodeInfo Lock) - (nodeInfo Update) - (nodeInfo Unlock)
//  - Bob: <get nodeInfo> - (nodeInfo RLock after Alice Unlock)- (nodeInfo Read) - (nodeInfo RUnlock)
// Reading and writing on not existing nodeInfo
//  - Alice: <create locked nodeInfo, store, return> - (nodeInfo Update) - (nodeInfo Unlock)
//  - Bob: <get nodeInfo> - (nodeInfo RLock after Alice Unlock)- (nodeInfo Read) - (nodeInfo RUnlock)
//
// <...> represents cache is locked before action and unlocked after action
type podAssignCache struct {
	items      sync.Map // items stores nodeInfo, using sync.Map because nodes are not frequently added or deleted.
	estimator  estimator.Estimator
	vectorizer ResourceVectorizer
	clock      clock.Clock
	args       *config.LoadAwareSchedulingArgs
}

// nodeInfo stores
//  1. assigned pods on this node
//  2. this node's metric collected by koordlet
//  3. cached calculation result for node usage and estimation.
type nodeInfo struct {
	sync.RWMutex
	// nodeInfo is marked as deleted when podInfos and nodeMetric is empty.
	// Deleted info should not be updated anymore.
	deleted bool
	// podAssignInfo is indexed using the Pod's types.UID
	podInfos map[types.UID]*podAssignInfo

	nodeMetric     *slov1alpha1.NodeMetric
	updateTime     time.Time
	reportInterval time.Duration

	podUsages     map[NamespacedName]ResourceVector
	prodPods      sets.Set[NamespacedName]
	nodeUsage     ResourceVector
	prodUsage     ResourceVector
	aggUsages     map[aggUsageKey]ResourceVector
	nodeDelta     ResourceVector // delta estimated resources of existing pods
	prodDelta     ResourceVector // delta estimated resources of existing prod pods
	nodeEstimated ResourceVector // sum of full estimated resources of all existing pods

	nodeDeltaPods     sets.Set[NamespacedName] // pods that is part of delta estimated, used in logging only
	prodDeltaPods     sets.Set[NamespacedName] // pods that is part of delta estimated for prod, used in logging only
	nodeEstimatedPods sets.Set[NamespacedName] // pods that is part of full estimated, used in logging only
}

type podAssignInfo struct {
	timestamp         time.Time
	pod               *corev1.Pod
	estimated         ResourceVector
	estimatedDeadline time.Time
}

// implements klog.KMetadata
type NamespacedName struct {
	Namespace string
	Name      string
}

func (n NamespacedName) GetName() string {
	return n.Name
}

func (n NamespacedName) GetNamespace() string {
	return n.Namespace
}

type aggUsageKey struct {
	Type extension.AggregationType
	// aggDuration == 0 means the non-empty maximum period recorded
	Duration time.Duration
}

func newPodAssignCache(estimator estimator.Estimator, vectorizer ResourceVectorizer, args *config.LoadAwareSchedulingArgs) *podAssignCache {
	return &podAssignCache{
		estimator:  estimator,
		vectorizer: vectorizer,
		clock:      clock.RealClock{},
		args:       args,
	}
}

func (p *podAssignCache) GetNodeMetricAndEstimatedOfExisting(name string, prodPod bool,
	aggregatedDuration metav1.Duration, aggregationType extension.AggregationType, logEnabled bool) (
	nodeMetric *slov1alpha1.NodeMetric, estimated ResourceVector, estimatedPods []NamespacedName, _ error) {
	n, exists := p.getNodeInfo(name)
	if !exists || n == nil {
		return nil, nil, nil, errors.NewNotFound(slov1alpha1.Resource("nodemetric"), name)
	}
	n.RLock()
	defer n.RUnlock()
	if nodeMetric = n.nodeMetric; nodeMetric == nil {
		return nil, nil, nil, errors.NewNotFound(slov1alpha1.Resource("nodemetric"), name)
	}
	estimated = p.vectorizer.EmptyVec()
	var pods sets.Set[NamespacedName]
	if prodPod {
		estimated.Add(n.prodUsage)
		estimated.Add(n.prodDelta)
		pods = n.prodDeltaPods
	} else {
		var nodeUsage ResourceVector
		if aggregationType != "" {
			nodeUsage = n.getTargetAggregatedUsage(aggregatedDuration, aggregationType)
		} else {
			nodeUsage = n.nodeUsage
		}
		if nodeUsage != nil {
			estimated.Add(nodeUsage)
			estimated.Add(n.nodeDelta)
			pods = n.nodeDeltaPods
		} else {
			estimated.Add(n.nodeEstimated)
			pods = n.nodeEstimatedPods
		}
	}
	if logEnabled {
		estimatedPods = pods.UnsortedList()
	}
	return
}
func (n *nodeInfo) getTargetAggregatedUsage(aggregatedDuration metav1.Duration, aggregationType extension.AggregationType) ResourceVector {
	// If no specific period is set, the non-empty maximum period recorded by NodeMetrics will be used by default.
	// This is a default policy.
	d := aggregatedDuration.Duration
	vec := n.aggUsages[aggUsageKey{Type: aggregationType, Duration: d}]
	if vec == nil && d == 0 {
		// All values in aggregatedDuration are empty, downgrade to use the values in NodeUsage
		vec = n.nodeUsage
	}
	return vec
}
func (p *podAssignCache) getPodAssignInfo(nodeName string, pod *corev1.Pod) *podAssignInfo {
	if nodeName == "" {
		return nil
	}
	n, exists := p.getNodeInfo(nodeName)
	if !exists || n == nil {
		return nil
	}
	n.RLock()
	defer n.RUnlock()
	return n.podInfos[pod.UID]
}

func (p *podAssignCache) getClonedNodeInfo(nodeName string) *nodeInfo {
	ret := &nodeInfo{}
	if nodeName == "" {
		return ret
	}
	n, exists := p.getNodeInfo(nodeName)
	if !exists || n == nil {
		return ret
	}
	n.RLock()
	defer n.RUnlock()
	*ret = nodeInfo{
		podInfos:          make(map[types.UID]*podAssignInfo, len(n.podInfos)),
		nodeMetric:        n.nodeMetric,
		updateTime:        n.updateTime,
		reportInterval:    n.reportInterval,
		prodUsage:         n.prodUsage.Clone().(ResourceVector),
		nodeDelta:         n.nodeDelta.Clone().(ResourceVector),
		prodDelta:         n.prodDelta.Clone().(ResourceVector),
		nodeEstimated:     n.nodeEstimated.Clone().(ResourceVector),
		nodeDeltaPods:     n.nodeDeltaPods.Clone(),
		prodDeltaPods:     n.prodDeltaPods.Clone(),
		nodeEstimatedPods: n.nodeEstimatedPods.Clone(),
	}
	for uid, pod := range n.podInfos {
		ret.podInfos[uid] = pod
	}
	return ret
}

func (p *podAssignCache) getNodeInfo(nodeName string) (*nodeInfo, bool) {
	v, ok := p.items.Load(nodeName)
	if !ok {
		return nil, ok
	}
	return v.(*nodeInfo), ok
}

// getOrCreateNodeInfo returns the nodeInfo for the given nodeName.
// If the nodeInfo does not exist, it will be created with a locked mutex
// which prevent the empty nodeInfo is read and used by plugin with RLock before objects' updating.
//
// NOTICE: it should only be called in objects' add or update methods.
func (p *podAssignCache) getOrCreateNodeInfo(nodeName string) (_ *nodeInfo, created bool) {
	n := &nodeInfo{}
	n.Lock()
	v, loaded := p.items.LoadOrStore(nodeName, n)
	return v.(*nodeInfo), !loaded
}

// tryCleanup cleans up the nodeInfo from cache.items if it's empty.
//
// NOTICE: nodeInfo should be locked before calling this method.
func (p *podAssignCache) tryCleanup(name string, n *nodeInfo) {
	if n.nodeMetric == nil && len(n.podInfos) == 0 {
		n.deleted = true
		// only delete action has the chance that goroutine holds two locks,
		// and the order always will be nodeInfo lock first, then podAssignCache.items lock
		p.items.CompareAndDelete(name, n)
	}
}

// add or update pod with node name provided
func (p *podAssignCache) assign(nodeName string, pod *corev1.Pod) {
	if nodeName == "" || util.IsPodTerminated(pod) {
		return
	}
	var estimated ResourceVector
	if list, err := p.estimator.EstimatePod(pod); err == nil && len(list) != 0 {
		if vec := p.vectorizer.ToFactorVec(list); !vec.Empty() {
			estimated = vec
		}
	}
	var timestamp time.Time
	// try to use time from PodScheduled condition first
	if _, c := podutil.GetPodCondition(&pod.Status, corev1.PodScheduled); c != nil && c.Status == corev1.ConditionTrue && !c.LastTransitionTime.IsZero() {
		timestamp = c.LastTransitionTime.Time
	} else {
		// if PodScheduled condition not found, fallback to use assign timestamp from scheduler internal, which cannot be zero.
		timestamp = p.clock.Now()
	}
	estimatedDeadline := p.shouldEstimatePodDeadline(pod, timestamp)
	newPod := &podAssignInfo{
		timestamp:         timestamp,
		pod:               pod,
		estimated:         estimated,
		estimatedDeadline: estimatedDeadline,
	}
	for {
		n, created := p.getOrCreateNodeInfo(nodeName)
		// if nodeInfo is created in getOrCreate, it is locked already
		if n.AddOrUpdatePod(newPod, created) {
			return
		}
	}
}

func (p *podAssignCache) shouldEstimatePodDeadline(pod *corev1.Pod, timestamp time.Time) time.Time {
	var afterPodScheduled, afterInitialized int64 = -1, -1
	if p.args.AllowCustomizeEstimation {
		afterPodScheduled = extension.GetCustomEstimatedSecondsAfterPodScheduled(pod)
		afterInitialized = extension.GetCustomEstimatedSecondsAfterInitialized(pod)
	}
	if s := p.args.EstimatedSecondsAfterPodScheduled; s != nil && afterPodScheduled < 0 {
		afterPodScheduled = *s
	}
	if s := p.args.EstimatedSecondsAfterInitialized; s != nil && afterInitialized < 0 {
		afterInitialized = *s
	}
	if afterInitialized > 0 {
		if _, c := podutil.GetPodCondition(&pod.Status, corev1.PodInitialized); c != nil && c.Status == corev1.ConditionTrue {
			// if EstimatedSecondsAfterPodScheduled is set and pod is initialized, ignore EstimatedSecondsAfterPodScheduled
			// EstimatedSecondsAfterPodScheduled might be set to a long duration to wait for time consuming init containers in pod.
			if t := c.LastTransitionTime; !t.IsZero() {
				return t.Add(time.Duration(afterInitialized) * time.Second)
			}
		}
	}
	if afterPodScheduled > 0 && !timestamp.IsZero() {
		return timestamp.Add(time.Duration(afterPodScheduled) * time.Second)
	}
	return time.Time{}
}

func (p *podAssignCache) unAssign(nodeName string, pod *corev1.Pod) {
	if nodeName == "" {
		return
	}
	if n, ok := p.getNodeInfo(nodeName); ok {
		n.DeletePod(nodeName, pod.UID, p)
	}
}

func (p *podAssignCache) OnAdd(obj interface{}, isInInitialList bool) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	p.assign(pod.Spec.NodeName, pod)
}

func (p *podAssignCache) OnUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*corev1.Pod)
	if !ok || pod == nil {
		return
	}
	switch oldPodInfo := p.getPodAssignInfo(pod.Spec.NodeName, pod); {
	case oldPodInfo == nil: // pod was not cached
		p.assign(pod.Spec.NodeName, pod)
	case util.IsPodTerminated(pod): // pod has nodeName & pod become terminated
		p.unAssign(pod.Spec.NodeName, pod)
	case !reflect.DeepEqual(&pod.Spec, &oldPodInfo.pod.Spec) ||
		!reflect.DeepEqual(pod.Status.Conditions, oldPodInfo.pod.Status.Conditions):
		// pod spec or pod conditions changed, renew cached pod
		p.assign(pod.Spec.NodeName, pod)
	}
}

func (p *podAssignCache) OnDelete(obj interface{}) {
	var pod *corev1.Pod
	switch t := obj.(type) {
	case *corev1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		pod, ok = t.Obj.(*corev1.Pod)
		if !ok {
			return
		}
	default:
		return
	}
	p.unAssign(pod.Spec.NodeName, pod)
}

// AddOrUpdatePod add or update pod to nodeInfo.
// It returns false is nodeInfo is already deleted and caller should get a new nodeInfo and retry.
// Unlock is called whether nodeInfo is locked before calling or not.
func (n *nodeInfo) AddOrUpdatePod(pod *podAssignInfo, locked bool) bool {
	if n.deleted {
		if locked {
			n.Unlock()
		}
		return false
	}
	if !locked {
		n.Lock()
	}
	defer n.Unlock()
	if n.deleted {
		return false
	}
	var oldPod *podAssignInfo
	if n.podInfos == nil {
		n.podInfos = map[types.UID]*podAssignInfo{}
	} else {
		oldPod = n.podInfos[pod.pod.UID]
	}
	n.podInfos[pod.pod.UID] = pod
	if n.nodeMetric != nil {
		if oldPod == nil {
			n.addPod(pod)
		} else {
			n.updatePod(oldPod, pod)
		}
	}
	return true
}

func (n *nodeInfo) DeletePod(name string, uid types.UID, p *podAssignCache) {
	if n.deleted {
		return
	}
	n.Lock()
	defer n.Unlock()
	if n.deleted {
		return
	}
	oldPod := n.podInfos[uid]
	if oldPod != nil {
		delete(n.podInfos, uid)
	}
	if n.nodeMetric != nil && oldPod != nil {
		n.deletePod(oldPod)
	}
	p.tryCleanup(name, n)
}

func (p *podAssignCache) NodeMetricHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if m, ok := obj.(*slov1alpha1.NodeMetric); ok && m != nil {
				p.AddOrUpdateNodeMetric(m)
			}
		},
		UpdateFunc: func(_ any, obj any) {
			if m, ok := obj.(*slov1alpha1.NodeMetric); ok && m != nil {
				p.AddOrUpdateNodeMetric(m)
			}
		},
		DeleteFunc: func(obj any) {
			var m *slov1alpha1.NodeMetric
			switch o := obj.(type) {
			case *slov1alpha1.NodeMetric:
				m = o
			case cache.DeletedFinalStateUnknown:
				var ok bool
				if m, ok = o.Obj.(*slov1alpha1.NodeMetric); !ok {
					return
				}
			default:
				return
			}
			p.DeleteNodeMetric(m.Name)
		},
	}
}

func (p *podAssignCache) AddOrUpdateNodeMetric(metric *slov1alpha1.NodeMetric) {
	for {
		n, created := p.getOrCreateNodeInfo(metric.Name)
		// if nodeInfo is created in getOrCreate, it is locked already
		if n.AddOrUpdateNodeMetric(metric, p, created) {
			return
		}
	}
}

func (p *podAssignCache) DeleteNodeMetric(name string) {
	if n, ok := p.getNodeInfo(name); ok {
		n.DeleteNodeMetric(name, p)
	}
}

// AddOrUpdateNodeMetric add or update node metric to nodeInfo.
// It returns false is nodeInfo is already deleted and caller should get a new nodeInfo and retry.
// Unlock is called whether nodeInfo is locked before calling or not.
func (n *nodeInfo) AddOrUpdateNodeMetric(metric *slov1alpha1.NodeMetric, p *podAssignCache, locked bool) bool {
	if n.deleted {
		if locked {
			n.Unlock()
		}
		return false
	}
	var podUsages map[NamespacedName]ResourceVector
	var prodPods sets.Set[NamespacedName]
	var nodeUsage, prodUsage ResourceVector = nil, p.vectorizer.EmptyVec()
	var aggUsages map[aggUsageKey]ResourceVector
	if info := metric.Status.NodeMetric; info != nil {
		nodeUsage = p.vectorizer.ToVec(info.NodeUsage.ResourceList)
		if aggLen := len(info.AggregatedNodeUsages); aggLen > 0 {
			typeLen := len(info.AggregatedNodeUsages[0].Usage)
			aggUsages = make(map[aggUsageKey]ResourceVector, (aggLen+1)*typeLen)
			maxDurations := make(map[extension.AggregationType]time.Duration, typeLen)
			for _, aggInfo := range info.AggregatedNodeUsages {
				d := aggInfo.Duration.Duration
				for t, u := range aggInfo.Usage {
					if len(u.ResourceList) == 0 {
						continue
					}
					key := aggUsageKey{Type: t, Duration: d}
					vec := p.vectorizer.ToVec(u.ResourceList)
					aggUsages[key] = vec
					if md := maxDurations[t]; d > md {
						maxDurations[t] = d
					}
				}
			}
			for t, d := range maxDurations {
				aggUsages[aggUsageKey{Type: t}] = aggUsages[aggUsageKey{Type: t, Duration: d}]
			}
		}
		if p.args.ProdUsageIncludeSys {
			prodUsage.Add(p.vectorizer.ToVec(info.SystemUsage.ResourceList))
		}
	}
	if infos := metric.Status.PodsMetric; len(infos) != 0 {
		podUsages = make(map[NamespacedName]ResourceVector, len(infos))
		prodPods = sets.New[NamespacedName]()
		for _, info := range infos {
			if info == nil {
				continue
			}
			if len(info.PodUsage.ResourceList) == 0 {
				continue
			}
			key := NamespacedName{Namespace: info.Namespace, Name: info.Name}
			vec := p.vectorizer.ToVec(info.PodUsage.ResourceList)
			podUsages[key] = vec
			if info.Priority == extension.PriorityProd {
				prodPods.Insert(key)
			}
		}
	}
	if !locked {
		n.Lock()
	}
	defer n.Unlock()
	if n.deleted {
		return false
	}
	n.nodeMetric = metric
	n.reportInterval = getNodeMetricReportInterval(metric)
	if metric.Status.UpdateTime != nil {
		n.updateTime = metric.Status.UpdateTime.Time
	}
	n.podUsages, n.prodPods = podUsages, prodPods
	n.nodeUsage = nodeUsage
	n.prodUsage = prodUsage
	n.aggUsages = aggUsages
	n.nodeDelta = p.vectorizer.EmptyVec()
	n.prodDelta = p.vectorizer.EmptyVec()
	n.nodeEstimated = p.vectorizer.EmptyVec()
	n.nodeDeltaPods = sets.New[NamespacedName]()
	n.prodDeltaPods = sets.New[NamespacedName]()
	n.nodeEstimatedPods = sets.New[NamespacedName]()
	for _, pod := range n.podInfos {
		n.addPod(pod)
	}
	return true
}

func (n *nodeInfo) DeleteNodeMetric(name string, p *podAssignCache) {
	if n.deleted {
		return
	}
	n.Lock()
	defer n.Unlock()
	if n.deleted {
		return
	}
	n.nodeMetric = nil
	p.tryCleanup(name, n)
}

func (n *nodeInfo) addPod(pod *podAssignInfo) {
	key := NamespacedName{Namespace: pod.pod.Namespace, Name: pod.pod.Name}
	u := n.podUsages[key]
	prod := extension.GetPodPriorityClassWithDefault(pod.pod) == extension.PriorityProd
	// Only use prod pod's usage when both pod claims and node metric reports it as prod.
	// 1. pod priority class are updated dynamically
	// 2. prod / non prod pod is wrongly reported or terminated pod is leaked in node metrics status
	activeProd := prod && n.prodPods.Has(key)
	if activeProd {
		n.prodUsage.Add(u)
	}

	e := pod.estimated
	if e == nil {
		return
	}
	// 1. when usage is not collected
	// 2. when pod miss lastest metrics update
	// 3. when pod metrics is still in the report interval
	// 4. when pod is configured in estimation
	should := u == nil ||
		n.updateTime.Add(-n.reportInterval).Before(pod.timestamp) ||
		(!pod.estimatedDeadline.IsZero() && pod.estimatedDeadline.After(n.updateTime))
	if should {
		if n.nodeDelta.AddDelta(e, u) && n.nodeDeltaPods != nil {
			n.nodeDeltaPods.Insert(key)
		}
	}
	n.nodeEstimated.Add(e)
	if n.nodeEstimatedPods != nil {
		n.nodeEstimatedPods.Insert(key)
	}

	if !prod {
		return
	}
	if !activeProd && u != nil {
		u, should = nil, true
	}
	if should {
		if n.prodDelta.AddDelta(e, u) && n.prodDeltaPods != nil {
			n.prodDeltaPods.Insert(key)
		}
	}
}

func (n *nodeInfo) updatePod(oldPod, newPod *podAssignInfo) {
	n.deletePod(oldPod)
	n.addPod(newPod)
}

// reverse procedure of addPod
func (n *nodeInfo) deletePod(pod *podAssignInfo) {
	key := NamespacedName{Namespace: pod.pod.Namespace, Name: pod.pod.Name}
	u := n.podUsages[key]
	prod := extension.GetPodPriorityClassWithDefault(pod.pod) == extension.PriorityProd
	activeProd := prod && n.prodPods.Has(key)
	if activeProd {
		n.prodUsage.Sub(u)
	}

	e := pod.estimated
	if e == nil {
		return
	}
	should := u == nil ||
		n.updateTime.Add(-n.reportInterval).Before(pod.timestamp) ||
		(!pod.estimatedDeadline.IsZero() && pod.estimatedDeadline.After(n.updateTime))
	if should {
		if n.nodeDelta.SubDelta(e, u) && n.nodeDeltaPods != nil {
			n.nodeDeltaPods.Delete(key)
		}
	}
	n.nodeEstimated.Sub(e)
	if n.nodeEstimatedPods != nil {
		n.nodeEstimatedPods.Delete(key)
	}

	if !prod {
		return
	}
	if !activeProd && u != nil {
		u, should = nil, true
	}
	if should {
		if n.prodDelta.SubDelta(e, u) && n.prodDeltaPods != nil {
			n.prodDeltaPods.Delete(key)
		}
	}
}
