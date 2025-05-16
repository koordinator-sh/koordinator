package core

import (
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
)

const (
	ReasonPodDeleted = "PodDeleted"
	ReasonPodBound   = "PodBound"

	ReasonGangGroupFailureCauseThisPod = "GangGroupFailureCauseThisPod"
)

type GangGroupInfo struct {
	lock sync.Mutex

	Initialized bool
	GangGroupId string
	GangGroup   []string

	// OnceResourceSatisfied indicates whether the gang has ever reached the ResourceSatisfied state，which means the
	// children number has reached the minNum in the early step,
	// once this variable is set true, it is irreversible.
	OnceResourceSatisfied bool

	/*
		WaitingGangIDs
		- is recorded when permit return wait
		- is cleared when
		  - permit allow gangGroup
		  - gang is reject on postFilter
		  - pod is deleted
		  - gang is deleted
		  - pod is unreserved
		  - pod is bound
	*/

	WaitingGangIDs sets.Set[string]

	/*
		RepresentativePodKey
		- is recorded when the first pod of gangGroup pass the PreEnqueue
		- is deleted when
		  - the pod is bound
		  - the pod is deleted
		- is replaced when some memberPod of gangGroup is firstly failed during some gangGroup schedulingContext
	*/
	RepresentativePodKey string
}

func NewGangGroupInfo(gangGroupId string, gangGroup []string) *GangGroupInfo {
	gangGroupInfo := &GangGroupInfo{
		Initialized:    false,
		GangGroupId:    gangGroupId,
		GangGroup:      gangGroup,
		WaitingGangIDs: sets.Set[string]{},
	}
	return gangGroupInfo
}

func (gg *GangGroupInfo) SetInitialized() {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	gg.Initialized = true
}

func (gg *GangGroupInfo) IsInitialized() bool {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	return gg.Initialized
}

func (gg *GangGroupInfo) isGangOnceResourceSatisfied() bool {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	return gg.OnceResourceSatisfied
}

func (gg *GangGroupInfo) setResourceSatisfied() {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.OnceResourceSatisfied {
		gg.OnceResourceSatisfied = true
		klog.Infof("Gang ResourceSatisfied, gangName: %v", gg.GangGroupId)
	}
}

func (gg *GangGroupInfo) AddWaitingGang() {
	gg.lock.Lock()
	defer gg.lock.Unlock()
	isWaitingBefore := len(gg.WaitingGangIDs) > 0
	gg.WaitingGangIDs.Insert(gg.GangGroup...)
	if !isWaitingBefore && len(gg.WaitingGangIDs) > 0 {
		metrics.WaitingGangGroupNumber.WithLabelValues().Inc()
	}
}

func (gg *GangGroupInfo) RemoveWaitingGang(gangID string) {
	gg.lock.Lock()
	defer gg.lock.Unlock()
	isWaitingBefore := len(gg.WaitingGangIDs) > 0
	gg.WaitingGangIDs.Delete(gangID)
	if isWaitingBefore && len(gg.WaitingGangIDs) == 0 {
		metrics.WaitingGangGroupNumber.WithLabelValues().Dec()
	}
}

func (gg *GangGroupInfo) ClearWaitingGang() {
	gg.lock.Lock()
	defer gg.lock.Unlock()
	isWaitingBefore := len(gg.WaitingGangIDs) > 0
	gg.WaitingGangIDs.Clear()
	if isWaitingBefore && len(gg.WaitingGangIDs) == 0 {
		metrics.WaitingGangGroupNumber.WithLabelValues().Dec()
	}
}

func (gg *GangGroupInfo) RecordIfNoRepresentatives(pod *corev1.Pod) string {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	podKey := util.GetId(pod.Namespace, pod.Name)
	if gg.RepresentativePodKey != "" && gg.RepresentativePodKey != podKey {
		return gg.RepresentativePodKey
	}
	if gg.RepresentativePodKey == "" {
		gg.RepresentativePodKey = podKey
		klog.V(4).Infof("gangGroupInfo: RecordIfNoRepresentatives, pod: %v, gangGroup: %v", podKey, gg.GangGroupId)
	}
	return gg.RepresentativePodKey
}

func (gg *GangGroupInfo) ReplaceRepresentative(pod *corev1.Pod, reason string) {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	podKey := util.GetId(pod.Namespace, pod.Name)
	if podKey != gg.RepresentativePodKey {
		klog.Infof("gangGroupInfo: ReplaceRepresentative, original: %v, now: %s, gangGroup: %v, reason: %s", gg.RepresentativePodKey, podKey, gg.GangGroupId, reason)
	}
	gg.RepresentativePodKey = podKey
}

func (gg *GangGroupInfo) DeleteIfRepresentative(pod *corev1.Pod, reason string) {
	gg.lock.Lock()
	defer gg.lock.Unlock()
	podKey := util.GetId(pod.Namespace, pod.Name)
	if gg.RepresentativePodKey == podKey {
		gg.RepresentativePodKey = ""
		klog.Infof("gangGroupInfo: DeleteIfRepresentative, pod: %v, gangGroup: %v, reason: %s", podKey, gg.GangGroupId, reason)
	}
}
