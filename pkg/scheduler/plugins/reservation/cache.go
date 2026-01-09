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
	"fmt"
	"strconv"
	"sync"

	"github.com/google/btree"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulinglister "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

// preAllocatablePodItem implements btree.Item for storing pods with scores
type preAllocatablePodItem struct {
	pod   *corev1.Pod
	score int64
}

// Less implements btree.Item interface
// Returns true if this item should be ordered before the other item
func (p *preAllocatablePodItem) Less(than btree.Item) bool {
	other := than.(*preAllocatablePodItem)
	// Higher score comes first (descending order)
	if p.score != other.score {
		return p.score > other.score
	}
	// If scores are equal, order by UID for stability
	return p.pod.UID < other.pod.UID
}

// preAllocatablePodCache manages sorted pre-allocatable pods for a node using btree
type preAllocatablePodCache struct {
	tree  *btree.BTree                         // Sorted storage by score
	index map[types.UID]*preAllocatablePodItem // UID -> item for fast lookup
}

// newPreAllocatablePodCache creates a new cache instance
func newPreAllocatablePodCache() *preAllocatablePodCache {
	return &preAllocatablePodCache{
		tree:  btree.New(32),
		index: make(map[types.UID]*preAllocatablePodItem),
	}
}

type reservationCache struct {
	reservationLister  schedulinglister.ReservationLister
	lock               sync.RWMutex
	reservationInfos   map[types.UID]*frameworkext.ReservationInfo
	reservationsOnNode map[string]map[types.UID]struct{} // all reservations on node
	matchableOnNode    map[string]map[types.UID]struct{} // look up available reservations on node
	allocatedOnNode    map[string]map[types.UID]struct{} // look up allocated available reservations on node
	// preAllocatablePodsOnNode caches sorted pre-allocatable candidate pods per node
	// Uses btree for automatic ordering by score
	preAllocatablePodsOnNode map[string]*preAllocatablePodCache
}

func newReservationCache(reservationLister schedulinglister.ReservationLister) *reservationCache {
	cache := &reservationCache{
		reservationLister:        reservationLister,
		reservationInfos:         map[types.UID]*frameworkext.ReservationInfo{},
		reservationsOnNode:       map[string]map[types.UID]struct{}{},
		matchableOnNode:          map[string]map[types.UID]struct{}{},
		allocatedOnNode:          map[string]map[types.UID]struct{}{},
		preAllocatablePodsOnNode: map[string]*preAllocatablePodCache{},
	}
	return cache
}

func (cache *reservationCache) updateReservationsOnNode(nodeName string, uid types.UID) {
	if nodeName == "" {
		return
	}

	reservations := cache.reservationsOnNode[nodeName]
	if reservations == nil {
		reservations = map[types.UID]struct{}{}
		cache.reservationsOnNode[nodeName] = reservations
	}
	reservations[uid] = struct{}{}
}

func (cache *reservationCache) deleteReservationOnNode(nodeName string, uid types.UID) {
	if nodeName == "" {
		return
	}
	reservations := cache.reservationsOnNode[nodeName]
	delete(reservations, uid)
	if len(reservations) == 0 {
		delete(cache.reservationsOnNode, nodeName)
	}
}

func (cache *reservationCache) assumeReservation(r *schedulingv1alpha1.Reservation) {
	cache.updateReservation(r)
}

func (cache *reservationCache) forgetReservation(r *schedulingv1alpha1.Reservation) {
	cache.DeleteReservation(r)
}

func (cache *reservationCache) updateReservation(newR *schedulingv1alpha1.Reservation) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newR.UID]
	if rInfo == nil {
		rInfo = frameworkext.NewReservationInfo(newR)
		cache.reservationInfos[newR.UID] = rInfo
	} else {
		rInfo.UpdateReservation(newR)
	}
	uid := newR.UID
	if nodeName := newR.Status.NodeName; nodeName != "" {
		cache.updateReservationsOnNode(nodeName, uid)
		// refresh matchable and allocated
		if rInfo.IsMatchable() { // matchable
			if cache.matchableOnNode[nodeName] == nil {
				cache.matchableOnNode[nodeName] = map[types.UID]struct{}{}
			}
			cache.matchableOnNode[nodeName][uid] = struct{}{}
			if rInfo.GetAllocatedPods() > 0 { // allocated
				if cache.allocatedOnNode[nodeName] == nil {
					cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
				}
				cache.allocatedOnNode[nodeName][uid] = struct{}{}
			} else if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
			}
		} else { // not matchable, neither allocated
			if cache.matchableOnNode[nodeName] != nil {
				delete(cache.matchableOnNode[nodeName], uid)
				if len(cache.matchableOnNode[nodeName]) == 0 {
					delete(cache.matchableOnNode, nodeName)
				}
			}
			if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
}

func (cache *reservationCache) updateReservationIfExists(newR *schedulingv1alpha1.Reservation) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newR.UID]
	if rInfo == nil {
		return
	}
	rInfo.UpdateReservation(newR)
	uid := newR.UID
	if nodeName := newR.Status.NodeName; nodeName != "" {
		// refresh matchable and allocated
		if rInfo.IsMatchable() { // matchable
			if cache.matchableOnNode[nodeName] == nil {
				cache.matchableOnNode[nodeName] = map[types.UID]struct{}{}
			}
			cache.matchableOnNode[nodeName][uid] = struct{}{}
			if rInfo.GetAllocatedPods() > 0 { // allocated
				if cache.allocatedOnNode[nodeName] == nil {
					cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
				}
				cache.allocatedOnNode[nodeName][uid] = struct{}{}
			} else if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
			}
		} else { // not matchable, neither allocated
			if cache.matchableOnNode[nodeName] != nil {
				delete(cache.matchableOnNode[nodeName], uid)
				if len(cache.matchableOnNode[nodeName]) == 0 {
					delete(cache.matchableOnNode, nodeName)
				}
			}
			if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
}

func (cache *reservationCache) DeleteReservation(r *schedulingv1alpha1.Reservation) *frameworkext.ReservationInfo {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	uid := r.UID
	rInfo := cache.reservationInfos[uid]
	delete(cache.reservationInfos, uid)
	nodeName := r.Status.NodeName
	cache.deleteReservationOnNode(nodeName, uid)
	// refresh matchable and allocated
	if cache.matchableOnNode[nodeName] != nil {
		delete(cache.matchableOnNode[nodeName], uid)
		if len(cache.matchableOnNode[nodeName]) == 0 {
			delete(cache.matchableOnNode, nodeName)
		}
	}
	if cache.allocatedOnNode[nodeName] != nil {
		delete(cache.allocatedOnNode[nodeName], uid)
		if len(cache.allocatedOnNode[nodeName]) == 0 {
			delete(cache.allocatedOnNode, nodeName)
		}
	}
	return rInfo
}

func (cache *reservationCache) updateReservationOperatingPod(newPod *corev1.Pod, currentOwner *corev1.ObjectReference) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	rInfo := cache.reservationInfos[newPod.UID]
	if rInfo == nil {
		rInfo = frameworkext.NewReservationInfoFromPod(newPod)
		cache.reservationInfos[newPod.UID] = rInfo
	} else {
		rInfo.UpdatePod(newPod)
	}
	if currentOwner != nil {
		rInfo.AddAssignedPod(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      currentOwner.Name,
				Namespace: currentOwner.Namespace,
				UID:       currentOwner.UID,
			},
		})
	}
	uid := newPod.UID
	if nodeName := newPod.Spec.NodeName; nodeName != "" {
		cache.updateReservationsOnNode(nodeName, uid)
		// refresh matchable and allocated
		if rInfo.IsMatchable() { // matchable
			if cache.matchableOnNode[nodeName] == nil {
				cache.matchableOnNode[nodeName] = map[types.UID]struct{}{}
			}
			cache.matchableOnNode[nodeName][uid] = struct{}{}
			if rInfo.GetAllocatedPods() > 0 { // allocated
				if cache.allocatedOnNode[nodeName] == nil {
					cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
				}
				cache.allocatedOnNode[nodeName][uid] = struct{}{}
			} else if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
			}
		} else { // not matchable, neither allocated
			if cache.matchableOnNode[nodeName] != nil {
				delete(cache.matchableOnNode[nodeName], uid)
				if len(cache.matchableOnNode[nodeName]) == 0 {
					delete(cache.matchableOnNode, nodeName)
				}
			}
			if cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], uid)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
}

func (cache *reservationCache) deleteReservationOperatingPod(pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	uid := pod.UID
	delete(cache.reservationInfos, uid)
	nodeName := pod.Spec.NodeName
	cache.deleteReservationOnNode(nodeName, uid)
	// refresh matchable and allocated
	if cache.matchableOnNode[nodeName] != nil {
		delete(cache.matchableOnNode[nodeName], uid)
		if len(cache.matchableOnNode[nodeName]) == 0 {
			delete(cache.matchableOnNode, nodeName)
		}
	}
	if cache.allocatedOnNode[nodeName] != nil {
		delete(cache.allocatedOnNode[nodeName], uid)
		if len(cache.allocatedOnNode[nodeName]) == 0 {
			delete(cache.allocatedOnNode, nodeName)
		}
	}
}

func (cache *reservationCache) assumePod(reservationUID types.UID, pod *corev1.Pod) error {
	return cache.addPod(reservationUID, pod)
}

func (cache *reservationCache) assumePods(reservationUID types.UID, pods []*corev1.Pod) error {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo == nil {
		return fmt.Errorf("cannot find target reservation")
	}
	if rInfo.IsTerminating() {
		return fmt.Errorf("target reservation is terminating")
	}
	for _, pod := range pods {
		rInfo.AddAssignedPod(pod)
	}
	return nil
}

func (cache *reservationCache) forgetPod(reservationUID types.UID, pod *corev1.Pod) {
	cache.deletePod(reservationUID, pod)
}

func (cache *reservationCache) addPod(reservationUID types.UID, pod *corev1.Pod) error {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo == nil {
		return fmt.Errorf("cannot find target reservation")
	}
	if rInfo.IsTerminating() {
		return fmt.Errorf("target reservation is terminating")
	}
	rInfo.AddAssignedPod(pod)
	// update allocated cache
	if rInfo.IsMatchable() && rInfo.GetAllocatedPods() > 0 {
		nodeName := rInfo.GetNodeName()
		if nodeName != "" {
			if cache.allocatedOnNode[nodeName] == nil {
				cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
			}
			cache.allocatedOnNode[nodeName][reservationUID] = struct{}{}
		}
	}
	return nil
}

func (cache *reservationCache) updatePod(oldReservationUID, newReservationUID types.UID, oldPod, newPod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	oldRInfo := cache.reservationInfos[oldReservationUID]
	if oldRInfo != nil && oldPod != nil {
		oldRInfo.RemoveAssignedPod(oldPod)
		// update allocated cache for old reservation
		if oldRInfo.GetAllocatedPods() == 0 {
			nodeName := oldRInfo.GetNodeName()
			if nodeName != "" && cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], oldReservationUID)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
	newRInfo := cache.reservationInfos[newReservationUID]
	if newRInfo != nil && newPod != nil {
		newRInfo.AddAssignedPod(newPod)
		// update allocated cache for new reservation
		if newRInfo.IsMatchable() && newRInfo.GetAllocatedPods() > 0 {
			nodeName := newRInfo.GetNodeName()
			if nodeName != "" {
				if cache.allocatedOnNode[nodeName] == nil {
					cache.allocatedOnNode[nodeName] = map[types.UID]struct{}{}
				}
				cache.allocatedOnNode[nodeName][newReservationUID] = struct{}{}
			}
		}
	}
}

func (cache *reservationCache) deletePod(reservationUID types.UID, pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	rInfo := cache.reservationInfos[reservationUID]
	if rInfo != nil {
		rInfo.RemoveAssignedPod(pod)
		// update allocated cache
		if rInfo.GetAllocatedPods() == 0 {
			nodeName := rInfo.GetNodeName()
			if nodeName != "" && cache.allocatedOnNode[nodeName] != nil {
				delete(cache.allocatedOnNode[nodeName], reservationUID)
				if len(cache.allocatedOnNode[nodeName]) == 0 {
					delete(cache.allocatedOnNode, nodeName)
				}
			}
		}
	}
}

func (cache *reservationCache) getReservationInfo(name string) *frameworkext.ReservationInfo {
	reservation, err := cache.reservationLister.Get(name)
	if err != nil {
		return nil
	}
	return cache.getReservationInfoByUID(reservation.UID)
}

func (cache *reservationCache) getReservationInfoByUID(uid types.UID) *frameworkext.ReservationInfo {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rInfo := cache.reservationInfos[uid]
	if rInfo != nil {
		return rInfo.Clone()
	}
	return nil
}

func (cache *reservationCache) GetReservationInfoByPod(pod *corev1.Pod, nodeName string) *frameworkext.ReservationInfo {
	var target *frameworkext.ReservationInfo
	// TODO: fast lookup pods assigned to reservations
	cache.ForEachMatchableReservationOnNode(nodeName, func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
		if _, ok := rInfo.AssignedPods[pod.UID]; ok {
			target = rInfo
			return false, nil
		}
		return true, nil
	})
	return target
}

func (cache *reservationCache) ListAllNodes(matchable bool) []string {
	// list a subset of nodes which has any available reservations.
	// If matchable = false, we suppose the caller wants only the available reservations with allocated pods.
	// If matchable = true, where the caller can match any available reservations, we return nodes having available reservations.
	// To efficiently implement this, we may need to maintain two maps: one for allocated reservations and one for available reservations.
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	if len(cache.matchableOnNode) == 0 {
		return nil
	}
	if matchable {
		nodes := make([]string, 0, len(cache.matchableOnNode))
		for k := range cache.matchableOnNode {
			nodes = append(nodes, k)
		}
		return nodes
	}
	nodes := make([]string, 0, len(cache.allocatedOnNode))
	for k := range cache.allocatedOnNode {
		nodes = append(nodes, k)
	}
	return nodes
}

func (cache *reservationCache) ForEachMatchableReservationOnNode(nodeName string, fn func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status)) *framework.Status {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rOnNode := cache.matchableOnNode[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	for uid := range rOnNode {
		rInfo := cache.reservationInfos[uid]
		beContinue, status := fn(rInfo)
		if !status.IsSuccess() {
			return status
		}
		if !beContinue {
			return nil
		}
	}
	return nil
}

func (cache *reservationCache) ListAvailableReservationInfosOnNode(nodeName string, listAll bool) []*frameworkext.ReservationInfo {
	var result []*frameworkext.ReservationInfo
	if !listAll {
		cache.ForEachMatchableReservationOnNode(nodeName, func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status) {
			result = append(result, rInfo.Clone())
			return true, nil
		})
		return result
	}

	cache.lock.RLock()
	defer cache.lock.RUnlock()
	rOnNode := cache.reservationsOnNode[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	for uid := range rOnNode {
		rInfo := cache.reservationInfos[uid]
		if rInfo != nil {
			result = append(result, rInfo.Clone())
		}
	}
	return result
}

func (cache *reservationCache) listAllNodes() []string {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	if len(cache.reservationsOnNode) == 0 {
		return nil
	}
	nodes := make([]string, 0, len(cache.reservationsOnNode))
	for k := range cache.reservationsOnNode {
		nodes = append(nodes, k)
	}
	return nodes
}

// getAllPreAllocatableCandidates retrieves all cached pre-allocatable candidates for all nodes
// Returns a map of nodeName -> sorted list of pods (by score descending)
func (cache *reservationCache) getAllPreAllocatableCandidates() map[string][]*corev1.Pod {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	result := make(map[string][]*corev1.Pod, len(cache.preAllocatablePodsOnNode))
	for nodeName, podCache := range cache.preAllocatablePodsOnNode {
		if podCache == nil || podCache.tree.Len() == 0 {
			continue
		}
		// Collect pods in sorted order for this node
		pods := make([]*corev1.Pod, 0, podCache.tree.Len())
		podCache.tree.Ascend(func(item btree.Item) bool {
			pods = append(pods, item.(*preAllocatablePodItem).pod)
			return true
		})
		result[nodeName] = pods
	}
	return result
}

// deletePreAllocatableCandidateOnNode removes a specific pod from the cached candidates for a node
func (cache *reservationCache) deletePreAllocatableCandidateOnNode(nodeName string, podUID types.UID) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	if nodeName == "" {
		return
	}
	podCache := cache.preAllocatablePodsOnNode[nodeName]
	if podCache == nil {
		return
	}
	// Find and delete the item
	if item, exists := podCache.index[podUID]; exists {
		podCache.tree.Delete(item)
		delete(podCache.index, podUID)
	}
	// Clean up empty cache
	if podCache.tree.Len() == 0 {
		delete(cache.preAllocatablePodsOnNode, nodeName)
	}
}

// addPreAllocatableCandidateOnNode adds a pod to the cached pre-allocatable candidates for a node
func (cache *reservationCache) addPreAllocatableCandidateOnNode(pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	if pod == nil || pod.Spec.NodeName == "" {
		return
	}
	nodeName := pod.Spec.NodeName
	// Get or create cache for this node
	podCache := cache.preAllocatablePodsOnNode[nodeName]
	if podCache == nil {
		podCache = newPreAllocatablePodCache()
		cache.preAllocatablePodsOnNode[nodeName] = podCache
	}
	// Add or update the pod
	score := getPreAllocatableScoreFromPod(pod)
	item := &preAllocatablePodItem{
		pod:   pod,
		score: score,
	}
	podCache.tree.ReplaceOrInsert(item)
	podCache.index[pod.UID] = item
}

// updatePreAllocatableCandidateScore updates the score of a pod in the cache
func (cache *reservationCache) updatePreAllocatableCandidateScore(pod *corev1.Pod) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	if pod == nil || pod.Spec.NodeName == "" {
		return
	}
	nodeName := pod.Spec.NodeName
	podCache := cache.preAllocatablePodsOnNode[nodeName]
	if podCache == nil {
		return
	}
	// Delete old item
	if oldItem, exists := podCache.index[pod.UID]; exists {
		podCache.tree.Delete(oldItem)
	}
	// Insert new item with updated score
	score := getPreAllocatableScoreFromPod(pod)
	newItem := &preAllocatablePodItem{
		pod:   pod,
		score: score,
	}
	podCache.tree.ReplaceOrInsert(newItem)
	podCache.index[pod.UID] = newItem
}

// getPreAllocatableScoreFromPod retrieves the pre-allocatable score from pod annotation
func getPreAllocatableScoreFromPod(pod *corev1.Pod) int64 {
	if pod == nil || pod.Annotations == nil {
		return 0
	}
	scoreStr, ok := pod.Annotations[apiext.AnnotationPodPreAllocatableScore]
	if !ok {
		return 0
	}
	score, err := strconv.ParseInt(scoreStr, 10, 64)
	if err != nil {
		return 0
	}
	return score
}
