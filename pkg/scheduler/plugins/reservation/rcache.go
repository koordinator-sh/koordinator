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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	durationToExpireAssumedReservation = 2 * time.Minute
	defaultCacheCheckInterval          = 60 * time.Second
)

// AvailableCache is for efficiently querying the reservation allocation results.
// Typical usages are as below:
// 1. check if a pod match any of available reservations (ownership and resource requirements).
// 2. check which nodes a pod match available reservation are at.
// 3. check which reservations are on a node.
type AvailableCache struct {
	reservations map[string]*schedulingv1alpha1.Reservation               // reservation key -> reservation meta (including r, node, resource, labelSelector)
	nodeToR      map[string]map[types.UID]*schedulingv1alpha1.Reservation // node name -> reservation meta (of same node)
}

func newAvailableCache(rList ...*schedulingv1alpha1.Reservation) *AvailableCache {
	a := &AvailableCache{
		reservations: map[string]*schedulingv1alpha1.Reservation{},
		nodeToR:      map[string]map[types.UID]*schedulingv1alpha1.Reservation{},
	}
	for _, r := range rList {
		if !reservationutil.IsReservationAvailable(r) {
			continue
		}
		a.reservations[reservationutil.GetReservationKey(r)] = r
		nodeName := reservationutil.GetReservationNodeName(r)
		rOnNode := a.nodeToR[nodeName]
		if rOnNode == nil {
			rOnNode = map[types.UID]*schedulingv1alpha1.Reservation{}
			a.nodeToR[nodeName] = rOnNode
		}
		rOnNode[r.UID] = r
	}
	return a
}

func (a *AvailableCache) Len() int {
	return len(a.reservations)
}

func (a *AvailableCache) Add(r *schedulingv1alpha1.Reservation) {
	// NOTE: the caller should ensure the reservation is valid and available.
	// such as phase=Available, nodeName != "", requests > 0
	a.reservations[reservationutil.GetReservationKey(r)] = r
	nodeName := reservationutil.GetReservationNodeName(r)

	rOnNode := a.nodeToR[nodeName]
	if rOnNode == nil {
		rOnNode = map[types.UID]*schedulingv1alpha1.Reservation{}
		a.nodeToR[nodeName] = rOnNode
	}
	rOnNode[r.UID] = r
}

func (a *AvailableCache) Delete(r *schedulingv1alpha1.Reservation) {
	if r == nil || len(reservationutil.GetReservationNodeName(r)) == 0 {
		return
	}
	// cleanup r map
	delete(a.reservations, reservationutil.GetReservationKey(r))
	// cleanup nodeToR
	nodeName := reservationutil.GetReservationNodeName(r)
	rOnNode := a.nodeToR[nodeName]
	delete(rOnNode, r.UID)
	if len(rOnNode) == 0 {
		delete(a.nodeToR, nodeName)
	}
}

func (a *AvailableCache) Get(key string) *schedulingv1alpha1.Reservation {
	return a.reservations[key]
}

func (a *AvailableCache) GetOnNode(nodeName string) []*schedulingv1alpha1.Reservation {
	rOnNode := a.nodeToR[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	result := make([]*schedulingv1alpha1.Reservation, 0, len(rOnNode))
	for _, v := range rOnNode {
		result = append(result, v)
	}
	return result
}

type assumedInfo struct {
	reservation *schedulingv1alpha1.Reservation // previous version of
	shared      int                             // the sharing count of the Reservation
	ddl         *time.Time                      // marked as not shared and should get deleted in an expiring interval
}

// reservationCache caches the active, failed and assumed reservations synced from informer and plugin reserve.
// returned objects are for read-only usage
type reservationCache struct {
	lock     sync.RWMutex
	inactive map[string]*schedulingv1alpha1.Reservation // UID -> *object; failed & succeeded reservations
	active   *AvailableCache                            // available & waiting reservations, sync by informer
	assumed  map[string]*assumedInfo                    // reservation key -> assumed (pod allocated) reservation meta
}

var rCache *reservationCache = newReservationCache()

func getReservationCache() *reservationCache {
	return rCache
}

func newReservationCache() *reservationCache {
	return &reservationCache{
		inactive: map[string]*schedulingv1alpha1.Reservation{},
		active:   newAvailableCache(),
		assumed:  map[string]*assumedInfo{},
	}
}

func (c *reservationCache) Run() {
	c.lock.Lock()
	defer c.lock.Unlock()
	// cleanup expired assumed infos; normally there is only a few assumed
	for key, assumed := range c.assumed {
		if ddl := assumed.ddl; ddl != nil && time.Now().After(*ddl) {
			delete(c.assumed, key)
		}
	}
}

func (c *reservationCache) AddToActive(r *schedulingv1alpha1.Reservation) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.active.Add(r)
	// directly remove the assumed state if the reservation is in assumed cache but not shared any more
	key := reservationutil.GetReservationKey(r)
	assumed, ok := c.assumed[key]
	if ok && assumed.shared <= 0 {
		delete(c.assumed, key)
	}
}

func (c *reservationCache) AddToInactive(r *schedulingv1alpha1.Reservation) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.inactive[reservationutil.GetReservationKey(r)] = r
	c.active.Delete(r)
}

func (c *reservationCache) Assume(r *schedulingv1alpha1.Reservation) {
	c.lock.Lock()
	defer c.lock.Unlock()
	key := reservationutil.GetReservationKey(r)
	assumed, ok := c.assumed[key]
	if ok {
		assumed.shared++
		assumed.reservation = r
	} else {
		assumed = &assumedInfo{
			reservation: r,
			shared:      1,
		}
		c.assumed[key] = assumed
	}
}

func (c *reservationCache) unassume(r *schedulingv1alpha1.Reservation, update bool, ddl time.Time) {
	// Limitations:
	// To get the assumed version consistent with the object sync from informer, the caller MUST input a correct
	// unassumed version which does not relay on the calling order.
	// e.g. Caller A firstly assume R from R0 to R1 (denote the change as _R0_R1, and the reversal change is _R1_R0).
	//      Then caller B assume R from R1 to R2 (denote the change as _R1_R2, and the reversal change is _R2_R1).
	//      So even if the caller A and caller B do the unassume out of order,
	//      the R is make from R2 to (R2 + _R1_R0 + _R2_R1 = R2 + _R2_R1 + _R1_R0 = R0).
	// Here are the common operations for unassuming:
	// 1. (update=true) Restore: set assumed object into a version without the caller's assuming change.
	// 2. (update=false) Accept: keep assumed object since the the caller's assuming change is accepted.
	key := reservationutil.GetReservationKey(r)
	assumed, ok := c.assumed[key]
	if ok {
		assumed.shared--
		if assumed.shared <= 0 { // if this info is no longer in use, expire it from the assumed cache
			assumed.ddl = &ddl
		}
		if update { // if `update` is set, update with the current
			assumed.reservation = r
		}
	} // otherwise the info has been removed, ignore
}

func (c *reservationCache) Unassume(r *schedulingv1alpha1.Reservation, update bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.unassume(r, update, time.Now().Add(durationToExpireAssumedReservation))
}

func (c *reservationCache) Delete(r *schedulingv1alpha1.Reservation) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.active.Delete(r)
	delete(c.inactive, reservationutil.GetReservationKey(r))
}

func (c *reservationCache) GetOwned(pod *corev1.Pod) *schedulingv1alpha1.Reservation {
	allocated, err := apiext.GetReservationAllocated(pod)
	if allocated == nil {
		if err != nil {
			klog.Errorf("Failed to GetReservationAllocated, pod %v, err: %v", klog.KObj(pod), err)
		}
		return nil
	}

	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.GetInCacheByKey(string(allocated.UID))
}

func (c *reservationCache) GetInCache(r *schedulingv1alpha1.Reservation) *schedulingv1alpha1.Reservation {
	key := reservationutil.GetReservationKey(r)
	return c.GetInCacheByKey(key)
}

func (c *reservationCache) GetInCacheByKey(key string) *schedulingv1alpha1.Reservation {
	c.lock.RLock()
	defer c.lock.RUnlock()
	// if assumed, use the assumed state
	assumed, ok := c.assumed[key]
	if ok {
		return assumed.reservation
	}
	// otherwise, use in active cache
	return c.active.Get(key)
}

func (c *reservationCache) GetAllInactive() map[string]*schedulingv1alpha1.Reservation {
	c.lock.RLock()
	defer c.lock.RUnlock()
	m := map[string]*schedulingv1alpha1.Reservation{}
	for k, v := range c.inactive {
		m[k] = v // for readonly usage
	}
	return m
}

func (c *reservationCache) IsInactive(r *schedulingv1alpha1.Reservation) bool {
	c.lock.RLock()
	defer c.lock.RUnlock()
	_, ok := c.inactive[reservationutil.GetReservationKey(r)]
	return ok
}
