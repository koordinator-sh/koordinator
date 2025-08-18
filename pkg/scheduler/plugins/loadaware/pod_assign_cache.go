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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware/estimator"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var (
	timeNowFn = time.Now
)

// podAssignCache stores the Pod information that has been successfully scheduled or is about to be bound
type podAssignCache struct {
	lock  sync.RWMutex
	items map[string]*nodeMetric
	// podInfoItems stores podAssignInfo according to each node.
	// podAssignInfo is indexed using the Pod's types.UID
	podInfoItems map[string]map[types.UID]*podAssignInfo
	estimator    estimator.Estimator
	vectorizer   ResourceVectorizer
	args         *config.LoadAwareSchedulingArgs
}

type podAssignInfo struct {
	timestamp         time.Time
	pod               *corev1.Pod
	estimated         ResourceVector
	estimatedDeadline time.Time
}

func newPodAssignCache(estimator estimator.Estimator, vectorizer ResourceVectorizer, args *config.LoadAwareSchedulingArgs) *podAssignCache {
	return &podAssignCache{
		items:        map[string]*nodeMetric{},
		podInfoItems: map[string]map[types.UID]*podAssignInfo{},
		estimator:    estimator,
		vectorizer:   vectorizer,
		args:         args,
	}
}

func (p *podAssignCache) getPodAssignInfo(nodeName string, pod *corev1.Pod) *podAssignInfo {
	if nodeName == "" {
		return nil
	}
	p.lock.RLock()
	defer p.lock.RUnlock()
	m := p.podInfoItems[nodeName]
	if m == nil {
		return nil
	}
	if info, ok := m[pod.UID]; ok {
		return info
	}
	return nil
}

func (p *podAssignCache) getPodsAssignInfoOnNode(nodeName string) []*podAssignInfo {
	p.lock.RLock()
	defer p.lock.RUnlock()
	m := p.podInfoItems[nodeName]
	if m == nil {
		return nil
	}
	podInfos := make([]*podAssignInfo, 0, len(m))
	for _, info := range m {
		podInfos = append(podInfos, info)
	}
	return podInfos
}

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
		timestamp = timeNowFn()
	}
	estimatedDeadline := p.shouldEstimatePodDeadline(pod, timestamp)
	newPod := &podAssignInfo{
		timestamp:         timestamp,
		pod:               pod,
		estimated:         estimated,
		estimatedDeadline: estimatedDeadline,
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	m := p.podInfoItems[nodeName]
	if m == nil {
		m = make(map[types.UID]*podAssignInfo)
		p.podInfoItems[nodeName] = m
	}
	var oldPod *podAssignInfo
	if pod, ok := m[pod.UID]; ok {
		oldPod = pod
	}
	if nm := p.items[nodeName]; nm != nil {
		if oldPod == nil {
			p.addPod(nm, newPod)
		} else {
			p.updatePod(nm, oldPod, newPod)
		}
	}
	m[pod.UID] = newPod
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
	p.lock.Lock()
	defer p.lock.Unlock()
	if nm := p.items[nodeName]; nm != nil {
		if pod := p.podInfoItems[nodeName][pod.UID]; pod != nil {
			p.deletePod(nm, pod)
		}
	}
	delete(p.podInfoItems[nodeName], pod.UID)
	if len(p.podInfoItems[nodeName]) == 0 {
		delete(p.podInfoItems, nodeName)
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

func (p *podAssignCache) NodeMetricHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if m, ok := obj.(*slov1alpha1.NodeMetric); ok && m != nil {
				p.AddOrUpdate(m)
			}
		},
		UpdateFunc: func(_ any, obj any) {
			if m, ok := obj.(*slov1alpha1.NodeMetric); ok && m != nil {
				p.AddOrUpdate(m)
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
			p.Delete(m.Name)
		},
	}
}

func (p *podAssignCache) GetNodeMetric(name string) (*nodeMetric, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	item, exists := p.items[name]
	if !exists {
		return nil, errors.NewNotFound(slov1alpha1.Resource("nodemetric"), name)
	}
	return item, nil
}

func (p *podAssignCache) AddOrUpdate(metric *slov1alpha1.NodeMetric) {
	m := p.new(metric)
	p.lock.Lock()
	defer p.lock.Unlock()
	p.initPods(m, p.podInfoItems[metric.Name])
	p.items[metric.Name] = m
}

func (p *podAssignCache) Delete(name string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	delete(p.items, name)
}

type nodeMetric struct {
	*slov1alpha1.NodeMetric
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
	prodEstimated ResourceVector // sum of full estimated resources of all existing pod pods

	nodeDeltaPods     sets.Set[NamespacedName] // pods that is part of delta estimated, used in logging only
	prodDeltaPods     sets.Set[NamespacedName] // pods that is part of delta estimated for prod, used in logging only
	nodeEstimatedPods sets.Set[NamespacedName] // pods that is part of full estimated, used in logging only
	prodEstimatedPods sets.Set[NamespacedName] // pods that is part of full estimated for prod, used in logging only
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

func (p *podAssignCache) new(metric *slov1alpha1.NodeMetric) *nodeMetric {
	m := &nodeMetric{
		NodeMetric:     metric,
		reportInterval: getNodeMetricReportInterval(metric),
	}
	if metric.Status.UpdateTime != nil {
		m.updateTime = m.Status.UpdateTime.Time
	}
	info := metric.Status.NodeMetric
	if info != nil {
		m.nodeUsage = p.vectorizer.ToVec(info.NodeUsage.ResourceList)
		if aggLen := len(info.AggregatedNodeUsages); aggLen > 0 {
			typeLen := len(info.AggregatedNodeUsages[0].Usage)
			aggUsages := make(map[aggUsageKey]ResourceVector, (aggLen+1)*typeLen)
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
			m.aggUsages = aggUsages
		}
	}
	if infos := metric.Status.PodsMetric; len(infos) != 0 {
		podUsages := make(map[NamespacedName]ResourceVector, len(infos))
		prodPods := sets.New[NamespacedName]()
		prodUsage := p.vectorizer.EmptyVec()
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
				prodUsage.Add(vec)
			}
		}
		if p.args.ProdUsageIncludeSys && info != nil {
			prodUsage.Add(p.vectorizer.ToVec(info.SystemUsage.ResourceList))
		}
		m.podUsages, m.prodPods, m.prodUsage = podUsages, prodPods, prodUsage
	}
	return m
}

func (p *podAssignCache) initPods(m *nodeMetric, pods map[types.UID]*podAssignInfo) {
	m.nodeDelta = p.vectorizer.EmptyVec()
	m.prodDelta = p.vectorizer.EmptyVec()
	m.nodeEstimated = p.vectorizer.EmptyVec()
	m.prodEstimated = p.vectorizer.EmptyVec()

	if klog.V(6).Enabled() {
		m.nodeDeltaPods = sets.New[NamespacedName]()
		m.prodDeltaPods = sets.New[NamespacedName]()
		m.nodeEstimatedPods = sets.New[NamespacedName]()
		m.prodEstimatedPods = sets.New[NamespacedName]()
	}

	for _, pod := range pods {
		p.addPod(m, pod)
	}
}

func (p *podAssignCache) addPod(m *nodeMetric, pod *podAssignInfo) {
	key := NamespacedName{Namespace: pod.pod.Namespace, Name: pod.pod.Name}
	u := m.podUsages[key]
	// 1. when usage is not collected
	// 2. when pod miss lastest metrics update
	// 3. when pod metrics is still in the report interval
	// 4. when pod is configured in estimation
	should := u == nil ||
		m.updateTime.Add(-m.reportInterval).Before(pod.timestamp) ||
		(!pod.estimatedDeadline.IsZero() && pod.estimatedDeadline.After(m.updateTime))
	e := pod.estimated
	if should {
		if m.nodeDelta.AddDelta(e, u) && m.nodeDeltaPods != nil {
			m.nodeDeltaPods.Insert(key)
		}
	}
	m.nodeEstimated.Add(e)
	if m.nodeEstimatedPods != nil {
		m.nodeEstimatedPods.Insert(key)
	}

	if extension.GetPodPriorityClassWithDefault(pod.pod) != extension.PriorityProd {
		return
	}
	// in case prod pod is wrongly reported as non-prod in node metrics,
	// which we don't sum it up in prod node usage cache.
	// we should estimate this pod as it does not exist.
	if !m.prodPods.Has(key) {
		u, should = nil, true
	}
	if should {
		if m.prodDelta.AddDelta(e, u) && m.prodDeltaPods != nil {
			m.prodDeltaPods.Insert(key)
		}
	}
	m.prodEstimated.Add(e)
	if m.prodEstimatedPods != nil {
		m.prodEstimatedPods.Insert(key)
	}
}

func (p *podAssignCache) updatePod(m *nodeMetric, oldPod, newPod *podAssignInfo) {
	p.deletePod(m, oldPod)
	p.addPod(m, newPod)
}

// reverse procedure of addPod
func (p *podAssignCache) deletePod(m *nodeMetric, pod *podAssignInfo) {
	key := NamespacedName{Namespace: pod.pod.Namespace, Name: pod.pod.Name}
	u := m.podUsages[key]
	should := u == nil ||
		m.updateTime.Add(-m.reportInterval).Before(pod.timestamp) ||
		(!pod.estimatedDeadline.IsZero() && pod.estimatedDeadline.After(m.updateTime))
	e := pod.estimated
	if should {
		if m.nodeDelta.SubDelta(e, u) && m.nodeDeltaPods != nil {
			m.nodeDeltaPods.Delete(key)
		}
	}
	m.nodeEstimated.Sub(e)
	if m.nodeEstimatedPods != nil {
		m.nodeEstimatedPods.Delete(key)
	}

	if extension.GetPodPriorityClassWithDefault(pod.pod) != extension.PriorityProd {
		return
	}
	if !m.prodPods.Has(key) {
		u, should = nil, true
	}
	if should {
		if m.prodDelta.SubDelta(e, u) && m.prodDeltaPods != nil {
			m.prodDeltaPods.Delete(key)
		}
	}
	m.prodEstimated.Sub(e)
	if m.prodEstimatedPods != nil {
		m.prodEstimatedPods.Delete(key)
	}
}
