package gang

import (
	"github.com/koordinator-sh/koordinator/apis/extension"
	"k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"strconv"
	"sync"
	"time"
)

const (
	GangFromCrd        string = "GangFromCrd"
	GangFromAnnotation string = "GangFromAnnotation"
)

// Gang  basic gang info recorded in gangCache
type Gang struct {
	Name       string
	WaitTime   time.Duration
	CreateTime time.Time
	//strict-mode or non-strict-mode
	Mode              string
	MinRequiredNumber int
	TotalChildrenNum  int
	GangGroup         []string
	Children          map[string]*v1.Pod
	//pods that have already assumed(waiting in Permit stage)
	WaitingForBindChildren map[string]*v1.Pod
	//pods that have already bound
	BoundChildren map[string]*v1.Pod
	//if assumed  pods number has reached to MinRequiredNumber
	ResourceSatisfied bool

	//if the gang should be passed at PreFilter stage(Strict-Mode)
	ScheduleCycleValid bool
	//these fields used to count the cycle
	ScheduleCycle            int
	ChildrenScheduleRoundMap map[string]int

	GangFrom    string
	HasGangInit bool

	lock sync.Mutex
}

func NewGang(gangName string) *Gang {
	return &Gang{
		Name:                     gangName,
		CreateTime:               time.Now(),
		WaitTime:                 extension.DefaultGangWaitTime,
		Mode:                     extension.StrictMode,
		Children:                 make(map[string]*v1.Pod),
		WaitingForBindChildren:   make(map[string]*v1.Pod),
		BoundChildren:            make(map[string]*v1.Pod),
		ScheduleCycleValid:       true,
		ScheduleCycle:            1,
		ChildrenScheduleRoundMap: make(map[string]int),
		GangFrom:                 GangFromAnnotation,
		HasGangInit:              false,
	}
}

/******************Pod\PodGroup Event BEG******************/
func (gang *Gang) TryInitByPodConfig(pod *v1.Pod) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	if gang.HasGangInit {
		return
	}

	minRequiredNumber, err := strconv.Atoi(pod.Annotations[extension.GangMinNumAnnotation])
	if err != nil {
		klog.Errorf("pod's annotation MinRequiredNumber illegal, gangName:%v, value:%v",
			gang.Name, pod.Annotations[extension.GangMinNumAnnotation])
		return
	}
	gang.MinRequiredNumber = minRequiredNumber

	totalChildrenNum, err := strconv.Atoi(pod.Annotations[extension.GangTotalNumAnnotation])
	if err != nil {
		klog.Errorf("pod's annotation TotalNumber illegal, gangName:%v, value:%v",
			gang.Name, pod.Annotations[extension.GangTotalNumAnnotation])
		totalChildrenNum = minRequiredNumber
	}
	gang.TotalChildrenNum = totalChildrenNum

	mode := pod.Annotations[extension.GangModeAnnotation]
	if mode != extension.StrictMode || mode != extension.NonStrictMode {
		klog.Errorf("pod's annotation GangModeAnnotation illegal, gangName:%v, value:%v",
			gang.Name, pod.Annotations[extension.GangModeAnnotation])
		mode = extension.StrictMode
	}
	gang.Mode = mode

	waitTime, err := time.ParseDuration(pod.Annotations[extension.GangWaitTimeAnnotation])
	if err != nil {
		klog.Errorf("pod's annotation GangWaitTimeAnnotation illegal, gangName:%v, value:%v",
			gang.Name, pod.Annotations[extension.GangWaitTimeAnnotation])
		waitTime = 1200 //todo, default by global config
	}
	gang.WaitTime = waitTime

	groupSlice, err := util.StringToGangGroupSlice(pod.Annotations[extension.GangGroupsAnnotation])
	if err != nil {
		klog.Errorf("pod's annotation GangGroupsAnnotation illegal, gangName:%v, value:%v",
			gang.Name, pod.Annotations[extension.GangGroupsAnnotation])
	}
	gang.GangGroup = groupSlice

	gang.HasGangInit = true
	klog.Info("TryInitByPodConfig done, gangName:%v, minRequiredNumber:%v, totalChildrenNum:%v, "+
		"mode:%v, waitTime:%v, groupSlice:%v", gang.Name, gang.MinRequiredNumber, gang.TotalChildrenNum,
		gang.Mode, gang.WaitTime, gang.GangGroup)
}

func (gang *Gang) TryInitByPodGroup() {
	//todo, parse podGroup
}

func (gang *Gang) deletePod(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}

	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := GetNamespaceSplicingName(pod.Namespace, pod.Name)
	klog.Infof("Delete pod from gang:%v, podName:%v", gang.Name, podId)

	delete(gang.Children, podId)
	delete(gang.WaitingForBindChildren, podId)
	delete(gang.BoundChildren, podId)
	delete(gang.ChildrenScheduleRoundMap, podId)

	if gang.GangFrom == GangFromAnnotation {
		if len(gang.Children) == 0 {
			return true
		}
	}
	return false
}

/******************Pod\PodGroup Event END******************/

/******************Schedule Related BEG******************/
func (gang *Gang) GetGangWaitTime() time.Duration {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.WaitTime
}

func (gang *Gang) GetChildrenNum() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return len(gang.Children)
}

func (gang *Gang) GetGangMinNum() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.MinRequiredNumber
}

func (gang *Gang) GetGangTotalNum() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.TotalChildrenNum
}

func (gang *Gang) GetGangMode() string {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.Mode
}

func (gang *Gang) GetGangAssumedPods() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return len(gang.WaitingForBindChildren) + len(gang.BoundChildren)
}

func (gang *Gang) GetGangScheduleCycle() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.ScheduleCycle
}

func (gang *Gang) GetChildScheduleCycle(pod *v1.Pod) int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := GetNamespaceSplicingName(pod.Namespace, pod.Name)
	return gang.ChildrenScheduleRoundMap[podId]
}

func (gang *Gang) GetCreateTime() time.Time {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.CreateTime
}

func (gang *Gang) GetGangGroup() []string {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.GangGroup
}

func (gang *Gang) IsGangResourceSatisfied() bool {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.ResourceSatisfied
}

func (gang *Gang) IsGangScheduleCycleValid() bool {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.ScheduleCycleValid
}

func (gang *Gang) SetScheduleCycleValid(valid bool) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	gang.ScheduleCycleValid = valid
	klog.Infof("SetScheduleCycleValid, gangName:%v, valid:%v", gang.Name, valid)
}

func (gang *Gang) SetChildScheduleCycle(pod *v1.Pod, childCycle int) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := GetNamespaceSplicingName(pod.Namespace, pod.Name)
	gang.ChildrenScheduleRoundMap[podId] = childCycle
}

func (gang *Gang) TryUpdateScheduleCycle() {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	num := 0
	for _, childScheduleCycle := range gang.ChildrenScheduleRoundMap {
		if childScheduleCycle == gang.ScheduleCycle {
			num++
		}
	}

	if num == gang.TotalChildrenNum {
		gang.SetScheduleCycleValid(true)
		gang.ScheduleCycle += 1
	}

	klog.Infof("TryUpdateScheduleCycle, gangName:%v, ScheduleCycle:%v",
		gang.Name, gang.ScheduleCycle)
}

func (gang *Gang) AddAssumedPod(pod *v1.Pod) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := GetNamespaceSplicingName(pod.Namespace, pod.Name)
	gang.WaitingForBindChildren[podId] = pod
	klog.Infof("AddAssumedPod, gangName:%v, podName:%v", gang.Name, podId)

	if len(gang.WaitingForBindChildren)+len(gang.BoundChildren) >= gang.MinRequiredNumber {
		gang.SetResourceSatisfied()
	}
}

func (gang *Gang) SetResourceSatisfied() {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	if !gang.ResourceSatisfied {
		gang.ResourceSatisfied = true
		klog.Infof("Gang ResourceSatisfied, gangName:%v", gang.Name)
	}
}

func (gang *Gang) AddBoundPod(pod *v1.Pod) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := GetNamespaceSplicingName(pod.Namespace, pod.Name)
	delete(gang.WaitingForBindChildren, podId)
	gang.BoundChildren[podId] = pod

	klog.Infof("AddBoundPod, gangName:%v, podName:%v", gang.Name, podId)
}

/******************Schedule Related END******************/

func GetNamespaceSplicingName(namespace, name string) string {
	return namespace + "/" + name
}
