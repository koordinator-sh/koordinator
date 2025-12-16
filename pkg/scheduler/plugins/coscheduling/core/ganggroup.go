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

	ReasonGangGroupEnterIntoScheduling = "GangGroupEnterIntoScheduling"
)

type GangGroupInfo struct {
	lock sync.RWMutex

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
		  - gang is reject on postFilter
		  - all pods are deleted
		  - all gangs are deleted
		  - all pods are unreserved
		  - all pod are bound
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

	// BindingMemberPods is the waiting pods when gang enters binding phase.
	// This value is locked when AllowGangGroup is called and used by all pods during PreBind.
	// It will be cleared when all gangs in the group complete binding.
	BindingMemberPods sets.Set[string]
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
	gg.lock.RLock()
	defer gg.lock.RUnlock()

	return gg.Initialized
}

func (gg *GangGroupInfo) isGangOnceResourceSatisfied() bool {
	gg.lock.RLock()
	defer gg.lock.RUnlock()

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
	if len(gg.WaitingGangIDs) == 0 {
		if isWaitingBefore {
			metrics.WaitingGangGroupNumber.WithLabelValues().Dec()
		}
		if len(gg.BindingMemberPods) > 0 {
			gg.BindingMemberPods = nil
			klog.V(4).InfoS("GangGroupInfo: clear binding member count", "gangGroupId", gg.GangGroupId)
		}
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
	if len(gg.BindingMemberPods) > 0 {
		gg.BindingMemberPods = nil
		klog.V(4).InfoS("GangGroupInfo: clear binding member count", "gangGroupId", gg.GangGroupId)
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

func (gg *GangGroupInfo) DeleteIfRepresentative(pod *corev1.Pod, reason string) {
	gg.lock.Lock()
	defer gg.lock.Unlock()
	podKey := util.GetId(pod.Namespace, pod.Name)
	if gg.RepresentativePodKey == podKey {
		gg.RepresentativePodKey = ""
		klog.Infof("gangGroupInfo: DeleteIfRepresentative, pod: %v, gangGroup: %v, reason: %s", podKey, gg.GangGroupId, reason)
	}
}

func (gg *GangGroupInfo) ClearCurrentRepresentative(reason string) {
	klog.Infof("gangGroupInfo: ClearCurrentRepresentative, pod: %v, gangGroup: %v, reason: %s", gg.RepresentativePodKey, gg.GangGroupId, reason)
	gg.lock.Lock()
	defer gg.lock.Unlock()
	gg.RepresentativePodKey = ""
}

func (gg *GangGroupInfo) SetBindingMembers(pods sets.Set[string]) {
	gg.lock.Lock()
	defer gg.lock.Unlock()
	gg.BindingMemberPods = pods
	klog.V(4).InfoS("GangGroupInfo: set binding member count", "gangGroupId", gg.GangGroupId, "count", pods.Len())
}

func (gg *GangGroupInfo) GetBindingMembers() sets.Set[string] {
	gg.lock.RLock()
	defer gg.lock.RUnlock()
	return gg.BindingMemberPods
}
