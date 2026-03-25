package podtype

import (
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

// reservation stores nodeName and podTypes for a reserved pod UID
type reservation struct {
	NodeName string
	PodTypes []string
}

type TypeVector struct {
	// CPU/Mem/IO/Net are count-based short-term vector components.
	CPU float64
	Mem float64
	IO  float64
	Net float64
}

// PodTypeCache tracks pod type distribution across nodes
type PodTypeCache struct {
	// confirmedCounts[nodename][podtype] tracks confirmed pod counts by node and type
	confirmedCounts map[string]map[string]int
	// reservedCounts[nodename][podtype] tracks reserved pod counts by node and type
	reservedCounts map[string]map[string]int
	// reservedBy[podUID] tracks which node a pod with type is reserved on
	reservedBy map[types.UID]reservation
	// confirmedBy[podUID] tracks which node a pod with type is bound
	confirmedBy map[types.UID]reservation
	// ownerAnnotations[ownerUID] = podTypes, it tracks owner to pod types mapping
	ownerAnnotations map[string][]string
	// enableVectorScoring indicates whether vector scoring is enabled
	enableVectorScoring bool
	// mutex for thread safety
	mutex sync.RWMutex
}

func NewPodTypeCache(handle frameworkext.ExtendedHandle, enableVectorScoring bool) *PodTypeCache {
	ptCache := &PodTypeCache{
		confirmedCounts:     make(map[string]map[string]int),
		reservedCounts:      make(map[string]map[string]int),
		reservedBy:          make(map[types.UID]reservation),
		confirmedBy:         make(map[types.UID]reservation),
		ownerAnnotations:    make(map[string][]string),
		enableVectorScoring: enableVectorScoring,
	}
	podInformer := handle.SharedInformerFactory().Core().V1().Pods().Informer()
	podInformer.AddEventHandler(ptCache.ResourceEventHandlerFuncs())
	deployInformer := handle.SharedInformerFactory().Apps().V1().Deployments().Informer()
	deployInformer.AddEventHandler(ptCache.ownerResourceEventHandlerFuncs())
	rsInformer := handle.SharedInformerFactory().Apps().V1().ReplicaSets().Informer()
	rsInformer.AddEventHandler(ptCache.ownerResourceEventHandlerFuncs())
	return ptCache
}

// ResourceEventHandlerFuncs returns event handlers for pod events.
func (c *PodTypeCache) ResourceEventHandlerFuncs() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{AddFunc: c.handlePodAdd, UpdateFunc: c.handlePodUpdate, DeleteFunc: c.handlePodDelete}
}

// ownerResourceEventHandlerFuncs returns handlers for owner resources
// (Deployment/ReplicaSet/...) so we can capture annotations on owner objects.
func (c *PodTypeCache) ownerResourceEventHandlerFuncs() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.handleOwnerAdd(obj) },
		UpdateFunc: func(oldObj, newObj interface{}) { c.handleOwnerUpdate(oldObj, newObj) },
		DeleteFunc: func(obj interface{}) { c.handleOwnerDelete(obj) },
	}
}

// handleOwnerAdd handles adding an owner resource and updates ownerAnnotations map.
func (c *PodTypeCache) handleOwnerAdd(obj interface{}) {
	accessor, ok := obj.(metav1.Object)
	if !ok {
		tomb, ok := obj.(cache.DeletedFinalStateUnknown)
		if ok {
			accessor, _ = tomb.Obj.(metav1.Object)
		}
		if accessor == nil {
			return
		}
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.determineAndSetOwnerAnnotationLocked(accessor)
}

// handleOwnerUpdate updates ownerAnnotations when owner resource changes.
func (c *PodTypeCache) handleOwnerUpdate(oldObj, newObj interface{}) {
	accessor, ok := newObj.(metav1.Object)
	if !ok {
		return
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.determineAndSetOwnerAnnotationLocked(accessor)
}

// handleOwnerDelete deletes ownerAnnotations mapping on owner deletion.
func (c *PodTypeCache) handleOwnerDelete(obj interface{}) {
	accessor, ok := obj.(metav1.Object)
	if !ok {
		tomb, ok := obj.(cache.DeletedFinalStateUnknown)
		if ok {
			accessor, _ = tomb.Obj.(metav1.Object)
		}
		if accessor == nil {
			return
		}
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	uid := string(accessor.GetUID())
	if uid == "" {
		return
	}
	delete(c.ownerAnnotations, uid)
}

// determineAndSetOwnerAnnotationLocked determines pod types for owner object and
// sets ownerAnnotations[uid] accordingly. Caller MUST hold c.mutex.
func (c *PodTypeCache) determineAndSetOwnerAnnotationLocked(accessor metav1.Object) {
	uid := string(accessor.GetUID())
	if uid == "" {
		return
	}
	if accessor.GetAnnotations() != nil {
		if raw, ok := accessor.GetAnnotations()[PodTypeAnnotationKey]; ok {
			types := parsePodTypes(raw)
			if len(types) > 0 {
				c.ownerAnnotations[uid] = types
				return
			}
		}
	}
	for _, ownerRef := range accessor.GetOwnerReferences() {
		if ownerTypes, ok := c.ownerAnnotations[string(ownerRef.UID)]; ok && len(ownerTypes) > 0 {
			c.ownerAnnotations[uid] = append([]string(nil), ownerTypes...)
			return
		}
	}
	delete(c.ownerAnnotations, uid)
}

// decrementReservedCountLocked decrements reservedCounts[nodeName][podType] by 1.
// Caller MUST hold c.mutex.
func (c *PodTypeCache) decrementReservedCountLocked(nodeName, podType string) {
	if counts, ok := c.reservedCounts[nodeName]; ok {
		if cnt := counts[podType]; cnt > 0 {
			counts[podType]--
			if counts[podType] == 0 {
				delete(counts, podType)
			}
			if len(counts) == 0 {
				delete(c.reservedCounts, nodeName)
			}
		}
	}
}

// cleanupReservationByUIDLocked removes reservation record for podUID and rolls
// back corresponding reserved counts. Caller MUST hold c.mutex.
func (c *PodTypeCache) cleanupReservationByUIDLocked(podUID types.UID) {
	res, ok := c.reservedBy[podUID]
	if !ok {
		return
	}
	for _, t := range res.PodTypes {
		c.decrementReservedCountLocked(res.NodeName, t)
	}
	delete(c.reservedBy, podUID)
}

// cleanupReservationOnBindLocked ensures reservation is removed when pod binds.
// Caller MUST hold c.mutex.
func (c *PodTypeCache) cleanupReservationOnBindLocked(podUID types.UID) {
	c.cleanupReservationByUIDLocked(podUID)
}

// handlePodAdd handles pod add events.
func (c *PodTypeCache) handlePodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.updateOwnerAnnotationsLocked(pod)
	if pod.Spec.NodeName == "" || pod.DeletionTimestamp != nil {
		return
	}
	podTypes := c.resolvePodTypesForPodLocked(pod)
	if len(podTypes) == 0 {
		c.cleanupReservationOnBindLocked(pod.UID)
		return
	}
	if rec, ok := c.confirmedBy[pod.UID]; ok {
		if rec.NodeName == pod.Spec.NodeName && samePodTypes(rec.PodTypes, podTypes) {
			return
		}
		c.removeConfirmedLocked(rec.NodeName, rec.PodTypes)
		delete(c.confirmedBy, pod.UID)
	}
	c.cleanupReservationOnBindLocked(pod.UID)
	c.addConfirmedLocked(pod.Spec.NodeName, podTypes)
	c.confirmedBy[pod.UID] = reservation{NodeName: pod.Spec.NodeName, PodTypes: append([]string(nil), podTypes...)}
}

// handlePodUpdate handles pod update events.
func (c *PodTypeCache) handlePodUpdate(oldObj, newObj interface{}) {
	oldPod, ok1 := oldObj.(*corev1.Pod)
	newPod, ok2 := newObj.(*corev1.Pod)
	if !ok1 || !ok2 {
		return
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.updateOwnerAnnotationsLocked(newPod)
	oldBound := oldPod.Spec.NodeName != "" && oldPod.DeletionTimestamp == nil
	newBound := newPod.Spec.NodeName != "" && newPod.DeletionTimestamp == nil
	oldRec, oldRecOk := c.confirmedBy[oldPod.UID]
	oldTypes := c.resolvePodTypesForPodLocked(oldPod)
	newTypes := c.resolvePodTypesForPodLocked(newPod)

	if newBound && !oldBound {
		c.cleanupReservationOnBindLocked(newPod.UID)
		if rec, ok := c.confirmedBy[newPod.UID]; ok {
			if rec.NodeName == newPod.Spec.NodeName && samePodTypes(rec.PodTypes, newTypes) {
				return
			}
			c.removeConfirmedLocked(rec.NodeName, rec.PodTypes)
			delete(c.confirmedBy, newPod.UID)
		}
		if len(newTypes) > 0 {
			c.addConfirmedLocked(newPod.Spec.NodeName, newTypes)
			c.confirmedBy[newPod.UID] = reservation{NodeName: newPod.Spec.NodeName, PodTypes: append([]string(nil), newTypes...)}
		}
		return
	}
	if oldPod.Spec.NodeName != newPod.Spec.NodeName {
		if oldRecOk {
			c.removeConfirmedLocked(oldRec.NodeName, oldRec.PodTypes)
			delete(c.confirmedBy, newPod.UID)
		} else {
			c.removeConfirmedLocked(oldPod.Spec.NodeName, oldTypes)
		}
		if newBound && len(newTypes) > 0 {
			c.addConfirmedLocked(newPod.Spec.NodeName, newTypes)
			c.confirmedBy[newPod.UID] = reservation{NodeName: newPod.Spec.NodeName, PodTypes: append([]string(nil), newTypes...)}
		}
		return
	}
	if oldBound && newBound && !samePodTypes(oldTypes, newTypes) && newPod.Spec.NodeName != "" {
		if rec, ok := c.confirmedBy[newPod.UID]; ok {
			c.removeConfirmedLocked(rec.NodeName, rec.PodTypes)
			delete(c.confirmedBy, newPod.UID)
		} else {
			c.removeConfirmedLocked(newPod.Spec.NodeName, oldTypes)
		}
		if len(newTypes) > 0 {
			c.addConfirmedLocked(newPod.Spec.NodeName, newTypes)
			c.confirmedBy[newPod.UID] = reservation{NodeName: newPod.Spec.NodeName, PodTypes: append([]string(nil), newTypes...)}
		}
	}
}

// handlePodDelete handles pod delete events.
func (c *PodTypeCache) handlePodDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		pod, ok = tombstone.Obj.(*corev1.Pod)
		if !ok {
			return
		}
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if rec, ok := c.confirmedBy[pod.UID]; ok {
		c.removeConfirmedLocked(rec.NodeName, rec.PodTypes)
		delete(c.confirmedBy, pod.UID)
		c.cleanupReservationByUIDLocked(pod.UID)
		return
	}
	if pod.Spec.NodeName != "" {
		c.removeConfirmedLocked(pod.Spec.NodeName, c.resolvePodTypesForPodLocked(pod))
	}
	c.cleanupReservationByUIDLocked(pod.UID)
}

// Reserve reserves pod types on a node (idempotent for same podUID).
func (c *PodTypeCache) Reserve(nodeName string, podTypes []string, podUID types.UID) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if _, ok := c.reservedBy[podUID]; ok {
		c.cleanupReservationByUIDLocked(podUID)
	}
	c.ensureNodeReservedCountsLocked(nodeName)
	for _, t := range podTypes {
		if !IsValidPodType(t) {
			continue
		}
		c.reservedCounts[nodeName][t]++
	}
	c.reservedBy[podUID] = reservation{NodeName: nodeName, PodTypes: append([]string(nil), podTypes...)}
}

// Unreserve unreserves a pod using stored reservation info (idempotent).
func (c *PodTypeCache) Unreserve(podUID types.UID) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.cleanupReservationByUIDLocked(podUID)
}

// GetMergedCounts returns merged confirmed and reserved counts (deep copy).
func (c *PodTypeCache) GetMergedCounts() map[string]map[string]int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	merged := map[string]map[string]int{}
	for n, cs := range c.confirmedCounts {
		merged[n] = map[string]int{}
		for t, v := range cs {
			merged[n][t] = v
		}
	}
	for n, cs := range c.reservedCounts {
		if _, ok := merged[n]; !ok {
			merged[n] = map[string]int{}
		}
		for t, v := range cs {
			merged[n][t] += v
		}
	}
	return merged
}

// GetConfirmedCounts returns a deep copy of confirmed counts.
func (c *PodTypeCache) GetConfirmedCounts() map[string]map[string]int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	res := map[string]map[string]int{}
	for n, cs := range c.confirmedCounts {
		res[n] = map[string]int{}
		for t, v := range cs {
			res[n][t] = v
		}
	}
	return res
}

// GetReservedCounts returns a deep copy of reserved counts.
func (c *PodTypeCache) GetReservedCounts() map[string]map[string]int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	res := map[string]map[string]int{}
	for n, cs := range c.reservedCounts {
		res[n] = map[string]int{}
		for t, v := range cs {
			res[n][t] = v
		}
	}
	return res
}

// resolvePodTypesForPodLocked returns pod types for cache bookkeeping.
// It checks pod annotation first; if empty, it looks up ownerAnnotations map.
// Caller MUST hold c.mutex.
func (c *PodTypeCache) resolvePodTypesForPodLocked(pod *corev1.Pod) []string {
	if pod == nil {
		return nil
	}
	if t := getPodTypesFromPod(pod); len(t) > 0 {
		return t
	}
	for _, owner := range pod.OwnerReferences {
		if t := c.ownerAnnotations[string(owner.UID)]; len(t) > 0 {
			return append([]string(nil), t...)
		}
	}
	return nil
}

// GetOwnerPodTypesByUID returns owner pod types by UID.
func (c *PodTypeCache) GetOwnerPodTypesByUID(uid string) []string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	t := c.ownerAnnotations[uid]
	return append([]string(nil), t...)
}

// add/remove helpers (locked variants used by event handlers)
func (c *PodTypeCache) ensureNodeCountsLocked(nodeName string) {
	if _, ok := c.confirmedCounts[nodeName]; !ok {
		c.confirmedCounts[nodeName] = make(map[string]int)
	}
}
func (c *PodTypeCache) ensureNodeReservedCountsLocked(nodeName string) {
	if _, ok := c.reservedCounts[nodeName]; !ok {
		c.reservedCounts[nodeName] = make(map[string]int)
	}
}
func (c *PodTypeCache) addConfirmedLocked(nodeName string, podTypes []string) {
	if nodeName == "" {
		return
	}
	c.ensureNodeCountsLocked(nodeName)
	for _, t := range podTypes {
		if !IsValidPodType(t) {
			continue
		}
		c.confirmedCounts[nodeName][t]++
	}
}
func (c *PodTypeCache) removeConfirmedLocked(nodeName string, podTypes []string) {
	if nodeName == "" {
		return
	}
	if counts, ok := c.confirmedCounts[nodeName]; ok {
		for _, t := range podTypes {
			if count := counts[t]; count > 0 {
				counts[t]--
				if counts[t] == 0 {
					delete(counts, t)
				}
			}
		}
		if len(counts) == 0 {
			delete(c.confirmedCounts, nodeName)
		}
	}
}

// updateOwnerAnnotationsLocked updates owner to pod-types mapping.
// Caller MUST hold c.mutex.
func (c *PodTypeCache) updateOwnerAnnotationsLocked(pod *corev1.Pod) {
	if pod == nil {
		return
	}
	podTypes := getPodTypesFromPod(pod)
	if len(podTypes) == 0 {
		return
	}
	for _, owner := range pod.OwnerReferences {
		if owner.UID != "" {
			c.ownerAnnotations[string(owner.UID)] = append([]string(nil), podTypes...)
			klog.V(4).InfoS("podtype: updated owner annotation mapping (from pod)", "ownerUID", owner.UID, "podTypes", strings.Join(podTypes, ","))
		}
	}
}

func samePodTypes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, t := range a {
		m[t]++
	}
	for _, t := range b {
		m[t]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}
