package reservation

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

// Snapshot is a snapshot of cache ReservationInfo. The scheduler takes a
// snapshot at the beginning of each scheduling cycle and uses it for its operations in that cycle.
type Snapshot struct {
	generation         int64
	reservationsOnNode map[string]map[types.UID]struct{}
	reservationInfos   map[types.UID]*frameworkext.ReservationInfo
}

// NewEmptySnapshot initializes a Snapshot struct and returns it.
func NewEmptySnapshot() *Snapshot {
	return &Snapshot{
		reservationsOnNode: map[string]map[types.UID]struct{}{},
		reservationInfos:   map[types.UID]*frameworkext.ReservationInfo{},
	}
}

func (snapshot *Snapshot) forEachAvailableReservationOnNode(nodeName string, fn func(rInfo *frameworkext.ReservationInfo) (bool, *framework.Status)) *framework.Status {
	rOnNode := snapshot.reservationsOnNode[nodeName]
	if len(rOnNode) == 0 {
		return nil
	}
	for uid := range rOnNode {
		rInfo := snapshot.reservationInfos[uid]
		if rInfo != nil && rInfo.IsAvailable() {
			beContinue, status := fn(rInfo)
			if !status.IsSuccess() {
				return status
			}
			if !beContinue {
				return nil
			}
		}
	}
	return nil
}

func (cache *reservationCache) UpdateSnapshot() {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	// Get the last generation of the snapshot.
	snapshotGeneration := cache.snapshot.generation

	// Start from the head of the ReservationInfo doubly linked list and update snapshot
	// of ReservationInfos updated after the last snapshot.
	for rInfo := cache.headReservationInfo; rInfo != nil; rInfo = rInfo.next {
		if rInfo.info.Generation <= snapshotGeneration {
			// all the nodes are updated before the existing snapshot. We are done.
			break
		}
		cache.snapshot.reservationInfos[rInfo.info.UID()] = rInfo.info.Clone()
	}
	// Update the snapshot generation with the latest ReservationInfo generation.
	if cache.headReservationInfo != nil {
		cache.snapshot.generation = cache.headReservationInfo.info.Generation
	}

	if len(cache.snapshot.reservationInfos) > len(cache.reservationInfos) {
		cache.removeDeletedNodesFromSnapshot()
	}

	// TODO 这里直接清空，然后 DeepCopy 了, 下个 Commit 记录一下每个节点是否有变化，然后决定是否更新？
	cache.snapshot.reservationsOnNode = make(map[string]map[types.UID]struct{}, len(cache.reservationsOnNode))
	for nodeName, reservationInfos := range cache.reservationsOnNode {
		cache.snapshot.reservationsOnNode[nodeName] = make(map[types.UID]struct{}, len(reservationInfos))
		for uid := range reservationInfos {
			cache.snapshot.reservationsOnNode[nodeName][uid] = struct{}{}
		}
	}
	return
}

// If certain nodes were deleted after the last snapshot was taken, we should remove them from the snapshot.
func (cache *reservationCache) removeDeletedNodesFromSnapshot() {
	toDelete := len(cache.snapshot.reservationInfos) - len(cache.reservationInfos)
	for name := range cache.snapshot.reservationInfos {
		if toDelete <= 0 {
			break
		}
		if _, ok := cache.reservationInfos[name]; !ok {
			delete(cache.snapshot.reservationInfos, name)
			toDelete--
		}
	}
}
