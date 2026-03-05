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

// reservation stores nodeName and podType for a reserved pod UID
type reservation struct {
	NodeName string
	PodType  string
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
	// ownerAnnotations[ownerUID] = podType, it tracks owner to pod type mapping
	ownerAnnotations map[string]string
	// mutex for thread safety
	mutex sync.RWMutex
}

// NewPodTypeCache creates a new PodTypeCache and registers informers:
func NewPodTypeCache(handle frameworkext.ExtendedHandle) *PodTypeCache {
	ptCache := &PodTypeCache{
		confirmedCounts:  make(map[string]map[string]int),
		reservedCounts:   make(map[string]map[string]int),
		reservedBy:       make(map[types.UID]reservation),
		confirmedBy:      make(map[types.UID]reservation),
		ownerAnnotations: make(map[string]string),
	}

	// Register pod event handlers and keep informer reference
	podInformer := handle.SharedInformerFactory().Core().V1().Pods().Informer()
	podInformer.AddEventHandler(ptCache.ResourceEventHandlerFuncs())

	// Register deployment informer to capture annotations on owner resources.
	deployInformer := handle.SharedInformerFactory().Apps().V1().Deployments().Informer()
	deployInformer.AddEventHandler(ptCache.ownerResourceEventHandlerFuncs())

	// Register replicaset informer too (deployments create ReplicaSets)
	rsInformer := handle.SharedInformerFactory().Apps().V1().ReplicaSets().Informer()
	rsInformer.AddEventHandler(ptCache.ownerResourceEventHandlerFuncs())

	return ptCache
}

// ResourceEventHandlerFuncs returns event handlers for pod events
func (c *PodTypeCache) ResourceEventHandlerFuncs() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    c.handlePodAdd,
		UpdateFunc: c.handlePodUpdate,
		DeleteFunc: c.handlePodDelete,
	}
}

// ownerResourceEventHandlerFuncs returns handlers for owner resources (Deployment/ReplicaSet/...)
// so we can capture annotations placed on owner objects.
func (c *PodTypeCache) ownerResourceEventHandlerFuncs() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) { c.handleOwnerAdd(obj) },
		UpdateFunc: func(oldObj, newObj interface{}) { c.handleOwnerUpdate(oldObj, newObj) },
		DeleteFunc: func(obj interface{}) { c.handleOwnerDelete(obj) },
	}
}

// handleOwnerAdd handles adding an owner resource (Deployment/ReplicaSet/...)
// It extracts annotation and updates ownerAnnotations map; if the resource lacks annotation,
// it will attempt to inherit podType from its owners (e.g. ReplicaSet inherits from Deployment).
func (c *PodTypeCache) handleOwnerAdd(obj interface{}) {
	accessor, ok := obj.(metav1.Object)
	if !ok {
		// try tombstone handling
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

// handleOwnerUpdate updates ownerAnnotations when owner resource's annotation changes.
// It also attempts to inherit from its owners if it lacks its own annotation.
func (c *PodTypeCache) handleOwnerUpdate(oldObj, newObj interface{}) {
	accessor, ok := newObj.(metav1.Object)
	if !ok {
		return
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.determineAndSetOwnerAnnotationLocked(accessor)
}

// handleOwnerDelete deletes ownerAnnotations mapping so future pods won't pick stale mapping.
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
	if _, ok := c.ownerAnnotations[uid]; ok {
		delete(c.ownerAnnotations, uid)
		klog.V(4).InfoS("podtype: owner annotation mapping removed on owner delete", "ownerUID", uid)
	}
	// Note: we DO NOT force-delete child mappings here.
	// ReplicaSet->Pod mapping should be deleted when ReplicaSet itself is deleted (its own delete event will be handled).
}

// determineAndSetOwnerAnnotationLocked determines the podType for an owner object (deployment/rs/etc) and sets ownerAnnotations[uid] accordingly. Caller MUST hold write lock.
func (c *PodTypeCache) determineAndSetOwnerAnnotationLocked(accessor metav1.Object) {
	uid := string(accessor.GetUID())
	if uid == "" {
		return
	}
	// Check object's own annotations first
	ann := ""
	if accessor.GetAnnotations() != nil {
		if raw, ok := accessor.GetAnnotations()[PodTypeAnnotationKey]; ok {
			ann = strings.TrimSpace(strings.ToLower(raw))
		}
	}
	if IsValidPodType(ann) {
		c.ownerAnnotations[uid] = ann
		klog.V(4).InfoS("podtype: owner annotation set from self", "ownerUID", uid, "podType", ann)
		return
	}
	// Otherwise, try to inherit from its owners (one level)
	for _, ownerRef := range accessor.GetOwnerReferences() {
		if ownerRef.UID == "" {
			continue
		}
		if parentType, ok := c.ownerAnnotations[string(ownerRef.UID)]; ok && parentType != "" {
			// Inherit parent's podType
			c.ownerAnnotations[uid] = parentType
			klog.V(4).InfoS("podtype: owner annotation inherited from parent", "ownerUID", uid, "parentUID", ownerRef.UID, "podType", parentType)
			return
		}
	}
	// else: no annotation and no owner mapping found -> ensure no stale mapping
	if _, ok := c.ownerAnnotations[uid]; ok {
		delete(c.ownerAnnotations, uid)
		klog.V(4).InfoS("podtype: owner annotation removed (no annotation and no parent mapping)", "ownerUID", uid)
	}
}

// decrementReservedCountLocked decrements reservedCounts[nodeName][podType] by 1.
// Caller MUST hold c.mutex (write lock).
func (c *PodTypeCache) decrementReservedCountLocked(nodeName, podType string) {
	if nodeName == "" || podType == "" {
		return
	}
	if counts, ok := c.reservedCounts[nodeName]; ok {
		if cnt, ok := counts[podType]; ok && cnt > 0 {
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

// cleanupReservationByUIDLocked removes any reservation record for the provided podUID,
// and decrements the corresponding reservedCounts. Caller MUST hold c.mutex (write lock).
func (c *PodTypeCache) cleanupReservationByUIDLocked(podUID types.UID) {
	res, ok := c.reservedBy[podUID]
	if !ok {
		return
	}
	// Rollback reservedCounts for the recorded reservation
	c.decrementReservedCountLocked(res.NodeName, res.PodType)
	// Remove tracking entry
	delete(c.reservedBy, podUID)
	klog.V(4).InfoS("podtype: cleaned reservation (generic)", "podUID", podUID, "node", res.NodeName, "podType", res.PodType)
}

// cleanupReservationOnBindLocked is called when a pod becomes bound, ensure reservedCounts removed for that podUID.
// It always rolls back any prior reservation for that podUID (matching or not) and removes reservedBy entry.
// Caller MUST hold c.mutex (write lock).
func (c *PodTypeCache) cleanupReservationOnBindLocked(podUID types.UID) {
	res, ok := c.reservedBy[podUID]
	if !ok {
		return
	}
	// always decrement reservedCounts for the recorded reservation and remove mapping
	c.decrementReservedCountLocked(res.NodeName, res.PodType)
	delete(c.reservedBy, podUID)
	klog.V(4).InfoS("podtype: rolled back reservation on bind (defensive)", "podUID", podUID, "node", res.NodeName, "podType", res.PodType)
}

// handlePodAdd handles pod add events
func (c *PodTypeCache) handlePodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		klog.V(4).InfoS("podtype: expected pod, got", "obj", obj)
		return
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	// If pod carries its own annotation, update ownerAnnotations mapping for its owners.
	c.updateOwnerAnnotationsLocked(pod)

	// Only count pods that are bound to node and not terminating
	if pod.Spec.NodeName == "" || pod.DeletionTimestamp != nil {
		// If pod is not bound but there is a leftover reservation, do nothing here.
		// Reservation will be cleaned on pod delete or when pod becomes bound.
		return
	}

	podType := c.resolvePodTypeForPodLocked(pod)
	// If no podType resolved -> nothing to increment for confirmedCounts (we still might have a reservation)
	if podType == "" {
		// Still clean reservation if any (defensive)
		c.cleanupReservationOnBindLocked(pod.UID)
		return
	}

	// If we already recorded this pod as confirmed, ensure it's not double counted.
	if rec, ok := c.confirmedBy[pod.UID]; ok {
		// already counted
		if rec.NodeName == pod.Spec.NodeName && rec.PodType == podType {
			// nothing to do
			return
		}
		// If previously recorded but node/type differ, rollback previous then add new
		c.removeConfirmedLocked(rec.NodeName, rec.PodType)
		delete(c.confirmedBy, pod.UID)
	}
	// Always rollback any reservation for this podUID (defensive)
	c.cleanupReservationOnBindLocked(pod.UID)

	// add confirmed count and record mapping
	c.addConfirmedLocked(pod.Spec.NodeName, podType)
	c.confirmedBy[pod.UID] = reservation{NodeName: pod.Spec.NodeName, PodType: podType}
	klog.V(4).InfoS("podtype: pod added to confirmedCounts", "podUID", pod.UID, "node", pod.Spec.NodeName, "podType", podType)
}

// handlePodUpdate handles pod update events
func (c *PodTypeCache) handlePodUpdate(oldObj, newObj interface{}) {
	oldPod, ok1 := oldObj.(*corev1.Pod)
	newPod, ok2 := newObj.(*corev1.Pod)
	if !ok1 || !ok2 {
		klog.V(4).InfoS("podtype: expected pod in update", "old", oldObj, "new", newObj)
		return
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Update ownerAnnotations if newPod has direct annotation
	c.updateOwnerAnnotationsLocked(newPod)

	oldBound := oldPod.Spec.NodeName != "" && oldPod.DeletionTimestamp == nil
	newBound := newPod.Spec.NodeName != "" && newPod.DeletionTimestamp == nil

	oldRec, oldRecOk := c.confirmedBy[oldPod.UID]
	// compute resolved types (using current ownerAnnotations state)
	oldType := c.resolvePodTypeForPodLocked(oldPod)
	newType := c.resolvePodTypeForPodLocked(newPod)

	// Case A: pod just became bound (oldBound=false, newBound=true)
	if newBound && !oldBound {
		// rollback any reservation and record confirmed
		c.cleanupReservationOnBindLocked(newPod.UID)
		// if already confirmed (somehow), avoid double counting
		if rec, ok := c.confirmedBy[newPod.UID]; ok {
			// if rec.NodeName/type matches new state, nothing to do
			if rec.NodeName == newPod.Spec.NodeName && rec.PodType == newType {
				return
			}
			// else rollback old rec
			c.removeConfirmedLocked(rec.NodeName, rec.PodType)
			delete(c.confirmedBy, newPod.UID)
		}
		if newType != "" {
			c.addConfirmedLocked(newPod.Spec.NodeName, newType)
			c.confirmedBy[newPod.UID] = reservation{NodeName: newPod.Spec.NodeName, PodType: newType}
		}
		return
	}

	// Case B: node changed (move)
	if oldPod.Spec.NodeName != newPod.Spec.NodeName {
		// remove old confirmed record if it existed
		if oldRecOk {
			// use previously recorded node/type (most accurate)
			c.removeConfirmedLocked(oldRec.NodeName, oldRec.PodType)
			delete(c.confirmedBy, newPod.UID)
		} else if oldType != "" {
			// best-effort fallback (if somehow confirmedBy missing)
			c.removeConfirmedLocked(oldPod.Spec.NodeName, oldType)
		}
		// add record for new node if still bound and type known
		if newBound && newType != "" {
			c.addConfirmedLocked(newPod.Spec.NodeName, newType)
			c.confirmedBy[newPod.UID] = reservation{NodeName: newPod.Spec.NodeName, PodType: newType}
		}
		return
	}

	// Case C: same node, type changed (or ownerMapping changed)
	if oldBound && newBound && oldType != newType && newPod.Spec.NodeName != "" {
		// If we have recorded the old mapping, remove using confirmedBy data
		if rec, ok := c.confirmedBy[newPod.UID]; ok {
			// if rec.PodType equals oldType (or not), remove the recorded one
			c.removeConfirmedLocked(rec.NodeName, rec.PodType)
			delete(c.confirmedBy, newPod.UID)
		} else if oldType != "" {
			// fallback
			c.removeConfirmedLocked(newPod.Spec.NodeName, oldType)
		}
		// add new confirmed if newType present
		if newType != "" {
			c.addConfirmedLocked(newPod.Spec.NodeName, newType)
			c.confirmedBy[newPod.UID] = reservation{NodeName: newPod.Spec.NodeName, PodType: newType}
		}
		return
	}
	// other updates -> no op
}

// handlePodDelete handles pod delete events
func (c *PodTypeCache) handlePodDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		// Handle tombstone
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

	// If this pod was counted as confirmed earlier, use recorded mapping to rollback (most reliable)
	if rec, ok := c.confirmedBy[pod.UID]; ok {
		c.removeConfirmedLocked(rec.NodeName, rec.PodType)
		delete(c.confirmedBy, pod.UID)
		// remove any lingering reservation too
		c.cleanupReservationByUIDLocked(pod.UID)
		klog.V(4).InfoS("podtype: removed confirmed count via confirmedBy on delete", "podUID", pod.UID, "node", rec.NodeName, "podType", rec.PodType)
		return
	}

	// Not found in confirmedBy: fallback best-effort using annotations/owner mapping
	if pod.Spec.NodeName != "" {
		pt := c.resolvePodTypeForPodLocked(pod)
		if pt != "" {
			c.removeConfirmedLocked(pod.Spec.NodeName, pt)
		}
	}
	// cleanup reservation if any
	c.cleanupReservationByUIDLocked(pod.UID)
}

// Reserve reserves a pod on a node (idempotent and safe on repeated calls)
func (c *PodTypeCache) Reserve(nodeName, podType string, podUID types.UID) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// If an old reservation for this podUID exists, rollback it first.
	if _, ok := c.reservedBy[podUID]; ok {
		c.cleanupReservationByUIDLocked(podUID)
	}

	// Apply new reservation
	c.ensureNodeReservedCountsLocked(nodeName)
	c.reservedCounts[nodeName][podType]++
	c.reservedBy[podUID] = reservation{NodeName: nodeName, PodType: podType}
	klog.V(4).InfoS("podtype: reserved pod", "podUID", podUID, "nodeName", nodeName, "podType", podType)
}

// Unreserve unreserves a pod using stored reservation info (idempotent)
func (c *PodTypeCache) Unreserve(podUID types.UID) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	// Use the central cleanup function (it will no-op if not present)
	c.cleanupReservationByUIDLocked(podUID)
}

// GetMergedCounts returns merged confirmed and reserved counts (deep copy)
func (c *PodTypeCache) GetMergedCounts() map[string]map[string]int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	merged := make(map[string]map[string]int)

	// Start with confirmed counts
	for nodeName, counts := range c.confirmedCounts {
		merged[nodeName] = make(map[string]int)
		for podType, count := range counts {
			merged[nodeName][podType] = count
		}
	}

	// Add reserved counts
	for nodeName, counts := range c.reservedCounts {
		if _, ok := merged[nodeName]; !ok {
			merged[nodeName] = make(map[string]int)
		}
		for podType, count := range counts {
			merged[nodeName][podType] += count
		}
	}

	return merged
}

// GetConfirmedCounts returns a deep copy of confirmedCounts
func (c *PodTypeCache) GetConfirmedCounts() map[string]map[string]int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	res := make(map[string]map[string]int)
	for nodeName, counts := range c.confirmedCounts {
		res[nodeName] = make(map[string]int)
		for podType, count := range counts {
			res[nodeName][podType] = count
		}
	}
	return res
}

// GetReservedCounts returns a deep copy of reservedCounts
func (c *PodTypeCache) GetReservedCounts() map[string]map[string]int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	res := make(map[string]map[string]int)
	for nodeName, counts := range c.reservedCounts {
		res[nodeName] = make(map[string]int)
		for podType, count := range counts {
			res[nodeName][podType] = count
		}
	}
	return res
}

// resolvePodTypeForPodLocked returns pod type for the given pod for cache bookkeeping. 
// It first checks pod annotation, if empty it will look up ownerAnnotations map (thread-safe read).
// Caller MUST hold c.mutex (write lock).
func (c *PodTypeCache) resolvePodTypeForPodLocked(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	// first, check pod annotation (no lock needed since reading pod)
	if t := getPodTypeFromPod(pod); t != "" {
		return t
	}
	// fallback to ownerAnnotations map (caller already locked)
	for _, owner := range pod.OwnerReferences {
		if owner.UID == "" {
			continue
		}
		if t := c.ownerAnnotations[string(owner.UID)]; t != "" {
			return strings.TrimSpace(strings.ToLower(t))
		}
	}
	return ""
}

// GetOwnerPodTypeByUID returns the pod type for an owner by UID
func (c *PodTypeCache) GetOwnerPodTypeByUID(uid string) string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.ownerAnnotations[uid]
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

func (c *PodTypeCache) addConfirmedLocked(nodeName, podType string) {
	if nodeName == "" || podType == "" {
		return
	}
	c.ensureNodeCountsLocked(nodeName)
	c.confirmedCounts[nodeName][podType]++
}

func (c *PodTypeCache) removeConfirmedLocked(nodeName, podType string) {
	if nodeName == "" || podType == "" {
		return
	}
	if counts, ok := c.confirmedCounts[nodeName]; ok {
		if count, ok := counts[podType]; ok && count > 0 {
			counts[podType]--
			if counts[podType] == 0 {
				delete(counts, podType)
			}
			if len(counts) == 0 {
				delete(c.confirmedCounts, nodeName)
			}
		}
	}
}

// updateOwnerAnnotations updates owner to pod type mapping (locked)
func (c *PodTypeCache) updateOwnerAnnotations(pod *corev1.Pod) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.updateOwnerAnnotationsLocked(pod)
}

func (c *PodTypeCache) updateOwnerAnnotationsLocked(pod *corev1.Pod) {
	if pod == nil {
		return
	}
	podType := getPodTypeFromPod(pod)
	if podType == "" {
		return
	}
	for _, owner := range pod.OwnerReferences {
		if owner.UID != "" {
			c.ownerAnnotations[string(owner.UID)] = strings.TrimSpace(strings.ToLower(podType))
			klog.V(4).InfoS("podtype: updated owner annotation mapping (from pod)", "ownerUID", owner.UID, "podType", podType)
		}
	}
}
