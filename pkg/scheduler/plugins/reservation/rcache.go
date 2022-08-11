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

package reservation

import (
	"sync"

	corev1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

// reservationInfo stores the basic info of an active reservation
type reservationInfo struct {
	Reservation *schedulingv1alpha1.Reservation

	Resources corev1.ResourceList
	Port      framework.HostPortInfo

	Score int64
}

func newReservationInfo(r *schedulingv1alpha1.Reservation) *reservationInfo {
	requests := getReservationRequests(r)
	portInfo := framework.HostPortInfo{}
	for _, container := range r.Spec.Template.Spec.Containers {
		for _, podPort := range container.Ports {
			portInfo.Add(podPort.HostIP, string(podPort.Protocol), podPort.HostPort)
		}
	}
	return &reservationInfo{
		Reservation: r,
		Resources:   requests,
		Port:        portInfo,
	}
}

func (m *reservationInfo) GetReservation() *schedulingv1alpha1.Reservation {
	if m == nil {
		return nil
	}
	return m.Reservation
}

func (m *reservationInfo) ScoreForPod(pod *corev1.Pod) {
	// assert pod.request <= r.request
	// score := sum_i (w_i * sum(pod.request_i) / r.allocatable_i)) / sum_i(w_i)
	requests, _ := resourceapi.PodRequestsAndLimits(pod)
	if allocated := m.Reservation.Status.Allocated; allocated != nil {
		// consider multi owners sharing one reservation
		requests = quotav1.Add(requests, allocated)
	}

	w := int64(len(m.Resources))
	if w <= 0 {
		m.Score = 0
		return
	}
	var s int64
	for resource, alloc := range m.Resources {
		req := requests[resource]
		s += framework.MaxNodeScore * req.MilliValue() / alloc.MilliValue()
	}
	m.Score = s / w
}

// AvailableCache is for efficiently querying the reservation allocation results.
// Typical usages are as below:
// 1. check if a pod match any of available reservations (ownership and resource requirements).
// 2. check which nodes a pod match available reservation are at.
// 3. check which reservations are on a node.
type AvailableCache struct {
	lock         sync.RWMutex
	reservations map[string]*reservationInfo   // reservation key -> reservation meta (including r, node, resource, labelSelector)
	nodeToR      map[string][]*reservationInfo // node name -> reservation meta (of same node)
}

func newAvailableCache(rList ...*schedulingv1alpha1.Reservation) *AvailableCache {
	a := &AvailableCache{
		reservations: map[string]*reservationInfo{},
		nodeToR:      map[string][]*reservationInfo{},
	}
	for _, r := range rList {
		if !IsReservationScheduled(r) {
			continue
		}
		meta := newReservationInfo(r)
		a.reservations[GetReservationKey(r)] = meta
		nodeName := GetReservationNodeName(r)
		a.nodeToR[nodeName] = append(a.nodeToR[nodeName], meta)
	}
	return a
}

func (a *AvailableCache) Add(r *schedulingv1alpha1.Reservation) {
	// NOTE: the caller should ensure the reservation is valid and available.
	// such as phase=Available, nodeName != "", requests > 0
	a.lock.Lock()
	defer a.lock.Unlock()
	meta := newReservationInfo(r)
	a.reservations[GetReservationKey(r)] = meta
	nodeName := GetReservationNodeName(r)
	a.nodeToR[nodeName] = append(a.nodeToR[nodeName], meta)
}

func (a *AvailableCache) Delete(r *schedulingv1alpha1.Reservation) {
	a.lock.Lock()
	defer a.lock.Unlock()
	if r == nil || len(GetReservationNodeName(r)) <= 0 {
		return
	}
	delete(a.reservations, GetReservationKey(r))
	nodeName := GetReservationNodeName(r)
	rOnNode := a.nodeToR[nodeName]
	for i, rInfo := range rOnNode {
		if rInfo.Reservation.Name == r.Name && rInfo.Reservation.Namespace == r.Namespace {
			a.nodeToR[nodeName] = append(rOnNode[:i], rOnNode[i+1:]...)
			break
		}
	}
	if len(a.nodeToR[nodeName]) <= 0 {
		delete(a.nodeToR, nodeName)
	}
}

func (a *AvailableCache) Get(key string) *reservationInfo {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return a.reservations[key]
}

func (a *AvailableCache) GetOnNode(nodeName string) []*reservationInfo {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return a.nodeToR[nodeName]
}

func (a *AvailableCache) Len() int {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return len(a.reservations)
}

// reservationCache caches the active and failed reservations synced from informer and plugin reserve.
// returned objects are for read-only usage
type reservationCache struct {
	lock   sync.RWMutex
	failed map[string]*schedulingv1alpha1.Reservation // UID -> *object; failed reservations
	active *AvailableCache                            // available & waiting reservations, sync by informer
}

func newReservationCache() *reservationCache {
	return &reservationCache{
		failed: map[string]*schedulingv1alpha1.Reservation{},
		active: newAvailableCache(),
	}
}

func (c *reservationCache) AddToActive(r *schedulingv1alpha1.Reservation) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.active.Add(r)
}

func (c *reservationCache) AddToFailed(r *schedulingv1alpha1.Reservation) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.failed[GetReservationKey(r)] = r
	c.active.Delete(r)
}

func (c *reservationCache) Delete(r *schedulingv1alpha1.Reservation) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.active.Delete(r)
	delete(c.failed, GetReservationKey(r))
}

func (c *reservationCache) GetActive(r *schedulingv1alpha1.Reservation) *reservationInfo {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.active.Get(GetReservationKey(r))
}

func (c *reservationCache) GetAllFailed() map[string]*schedulingv1alpha1.Reservation {
	c.lock.RLock()
	defer c.lock.RUnlock()
	m := map[string]*schedulingv1alpha1.Reservation{}
	for k, v := range c.failed {
		m[k] = v // for readonly usage
	}
	return m
}

func (c *reservationCache) IsFailed(r *schedulingv1alpha1.Reservation) bool {
	c.lock.RLock()
	defer c.lock.RUnlock()
	_, ok := c.failed[GetReservationKey(r)]
	return ok
}

func (c *reservationCache) HandleOnAdd(obj interface{}) {
	r, ok := obj.(*schedulingv1alpha1.Reservation)
	if !ok {
		klog.V(3).Infof("reservation cache add failed to parse, obj %T", obj)
		return
	}
	if IsReservationActive(r) {
		c.AddToActive(r)
	} else if IsReservationFailed(r) {
		c.AddToFailed(r)
	}
	klog.V(5).InfoS("reservation cache add", "reservation", klog.KObj(r))
}

func (c *reservationCache) HandleOnUpdate(oldObj, newObj interface{}) {
	oldR, oldOK := oldObj.(*schedulingv1alpha1.Reservation)
	newR, newOK := newObj.(*schedulingv1alpha1.Reservation)
	if !oldOK || !newOK {
		klog.V(3).InfoS("reservation cache update failed to parse, old %T, new %T", oldObj, newObj)
		return
	}
	if oldR == nil || newR == nil {
		klog.V(4).InfoS("reservation cache update get nil object", "old", oldObj, "new", newObj)
		return
	}

	// in case delete and created of two reservations with same namespaced name are merged into update
	if oldR.UID != newR.UID {
		klog.V(4).InfoS("reservation cache update get merged update event",
			"reservation", klog.KObj(newR), "oldUID", oldR.UID, "newUID", newR.UID)
		c.HandleOnDelete(oldObj)
		c.HandleOnAdd(newObj)
		return
	}

	if IsReservationActive(newR) {
		c.AddToActive(newR)
	} else if IsReservationFailed(newR) {
		c.AddToFailed(newR)
	}
	klog.V(5).InfoS("reservation cache update", "reservation", klog.KObj(newR))
}

func (c *reservationCache) HandleOnDelete(obj interface{}) {
	var r *schedulingv1alpha1.Reservation
	switch t := obj.(type) {
	case *schedulingv1alpha1.Reservation:
		r = t
	case cache.DeletedFinalStateUnknown:
		deletedReservation, ok := t.Obj.(*schedulingv1alpha1.Reservation)
		if ok {
			r = deletedReservation
		}
	}
	if r == nil {
		klog.V(4).InfoS("reservation cache delete failed to parse, obj %T", obj)
	}
	c.Delete(r)
	klog.V(5).InfoS("reservation cache delete", "reservation", klog.KObj(r))
}

var _ framework.StateData = &stateData{}

type stateData struct {
	skip         bool                            // set true if pod does not allocate reserved resources
	matchedCache *AvailableCache                 // matched reservations for the scheduling pod
	assumed      *schedulingv1alpha1.Reservation // assumed reservation to be allocated by the pod
}

func (d *stateData) Clone() framework.StateData {
	cacheCopy := newAvailableCache()
	for k, v := range d.matchedCache.reservations {
		cacheCopy.reservations[k] = v
	}
	for k, v := range d.matchedCache.nodeToR {
		rs := make([]*reservationInfo, len(v))
		copy(rs, v)
		cacheCopy.nodeToR[k] = v
	}
	return &stateData{
		skip:         d.skip,
		matchedCache: cacheCopy,
		assumed:      d.assumed,
	}
}
