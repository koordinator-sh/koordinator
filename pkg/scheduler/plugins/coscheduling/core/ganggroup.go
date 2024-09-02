package core

import (
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
)

type GangGroupInfo struct {
	lock sync.Mutex

	Initialized bool
	GangGroupId string
	GangGroup   []string

	// these fields used to count the cycle
	// For example, at the beginning, `scheduleCycle` is 1, and each pod's cycle in `childrenScheduleRoundMap` is 0. When each pod comes to PreFilter,
	// we will check if the pod's value in `childrenScheduleRoundMap` is smaller than Gang's `scheduleCycle`, If result is positive,
	// we set the pod's cycle in `childrenScheduleRoundMap` equal with `scheduleCycle` and pass the check. If result is negative, means
	// the pod has been scheduled in this cycle, so we should reject it. With `totalChildrenNum`'s help, when the last pod comes to make all
	// `childrenScheduleRoundMap`'s values equal to `scheduleCycle`, Gang's `scheduleCycle` will be added by 1, which means a new schedule cycle.
	ScheduleCycle            int
	ScheduleCycleValid       bool
	GangTotalChildrenNumMap  map[string]int
	ChildrenScheduleRoundMap map[string]int

	// OnceResourceSatisfied indicates whether the gang has ever reached the ResourceSatisfied state，which means the
	// children number has reached the minNum in the early step,
	// once this variable is set true, it is irreversible.
	OnceResourceSatisfied bool

	LastScheduleTime         time.Time
	ChildrenLastScheduleTime map[string]time.Time
}

func NewGangGroupInfo(gangGroupId string, gangGroup []string) *GangGroupInfo {
	gangGroupInfo := &GangGroupInfo{
		Initialized:              false,
		GangGroupId:              gangGroupId,
		GangGroup:                gangGroup,
		ScheduleCycle:            1,
		ScheduleCycleValid:       true,
		GangTotalChildrenNumMap:  make(map[string]int),
		ChildrenScheduleRoundMap: make(map[string]int),
		LastScheduleTime:         timeNowFn(),
		ChildrenLastScheduleTime: make(map[string]time.Time),
	}

	for _, gang := range gangGroup {
		gangGroupInfo.GangTotalChildrenNumMap[gang] = 0
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

func (gg *GangGroupInfo) GetScheduleCycle() int {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	return gg.ScheduleCycle
}

func (gg *GangGroupInfo) setScheduleCycleInvalid() {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.Initialized {
		return
	}

	if gg.ScheduleCycleValid {
		gg.ScheduleCycleValid = false
		klog.Infof("setScheduleCycleInvalid, gangGroupName: %v, valid: %v", gg.GangGroupId, gg.ScheduleCycleValid)
	}
}

func (gg *GangGroupInfo) IsScheduleCycleValid() bool {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.Initialized {
		return false
	}

	return gg.ScheduleCycleValid
}

func (gg *GangGroupInfo) trySetScheduleCycleValid() {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.Initialized {
		return
	}

	num := 0
	for _, childScheduleCycle := range gg.ChildrenScheduleRoundMap {
		if childScheduleCycle == gg.ScheduleCycle {
			num++
		}
	}

	totalChildrenNum := 0
	for _, gangTotalNum := range gg.GangTotalChildrenNumMap {
		totalChildrenNum += gangTotalNum
	}

	if num == totalChildrenNum {
		gg.ScheduleCycle += 1
		gg.ScheduleCycleValid = true

		klog.Infof("trySetScheduleCycleTrue, gangGroupName: %v, ScheduleCycle: %v, ScheduleCycleValid: %v",
			gg.GangGroupId, gg.ScheduleCycle, gg.ScheduleCycleValid)
	}
}

func (gg *GangGroupInfo) setChildScheduleCycle(pod *corev1.Pod, childCycle int) {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.Initialized {
		return
	}

	podId := util.GetId(pod.Namespace, pod.Name)
	gg.ChildrenScheduleRoundMap[podId] = childCycle
	klog.Infof("setChildScheduleCycle, pod: %v, childCycle: %v", podId, childCycle)
}

func (gg *GangGroupInfo) getChildScheduleCycle(pod *corev1.Pod) int {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	podId := util.GetId(pod.Namespace, pod.Name)
	return gg.ChildrenScheduleRoundMap[podId]
}

func (gg *GangGroupInfo) deleteChildScheduleCycle(podId string) {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.Initialized {
		return
	}

	delete(gg.ChildrenScheduleRoundMap, podId)
}

func (gg *GangGroupInfo) SetGangTotalChildrenNum(gangName string, totalChildrenNum int) {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.Initialized {
		return
	}

	gg.GangTotalChildrenNumMap[gangName] = totalChildrenNum
}

func (gg *GangGroupInfo) initPodLastScheduleTime(pod *corev1.Pod) {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.Initialized {
		return
	}

	podId := util.GetId(pod.Namespace, pod.Name)
	gg.ChildrenLastScheduleTime[podId] = gg.LastScheduleTime
}

func (gg *GangGroupInfo) getPodLastScheduleTime(pod *corev1.Pod) time.Time {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	podId := util.GetId(pod.Namespace, pod.Name)
	return gg.ChildrenLastScheduleTime[podId]
}

func (gg *GangGroupInfo) deletePodLastScheduleTime(podId string) {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.Initialized {
		return
	}

	delete(gg.ChildrenLastScheduleTime, podId)
}

func (gg *GangGroupInfo) resetPodLastScheduleTime(pod *corev1.Pod) {
	gg.lock.Lock()
	defer gg.lock.Unlock()

	if !gg.Initialized {
		return
	}

	num := 0
	for _, childLastScheduleTime := range gg.ChildrenLastScheduleTime {
		if childLastScheduleTime.Equal(gg.LastScheduleTime) {
			num++
		}
	}

	if num == len(gg.ChildrenLastScheduleTime) {
		gg.LastScheduleTime = time.Now()
		klog.Infof("try resetGangGroupLastScheduleTime, gangGroupName: %v, time:%v", gg.GangGroupId, gg.LastScheduleTime)
	}

	podId := util.GetId(pod.Namespace, pod.Name)
	gg.ChildrenLastScheduleTime[podId] = gg.LastScheduleTime
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
