package gang

import (
	"fmt"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"strconv"
	"strings"
	"sync"
	"time"
)

type gangCache struct {
	lock      *sync.RWMutex
	gangItems map[string]*Gang
}

func NewGangCache() *gangCache {
	return &gangCache{
		gangItems: make(map[string]*Gang),
		lock:      new(sync.RWMutex),
	}
}

func (gangCache *gangCache) onPodAdd(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}
	gangCache.AddPod(pod)
}

func (gangCache *gangCache) onPodDelete(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}
	gangCache.DeletePod(pod)
	gangName := pod.Annotations[GangNameAnnotation]
	//whether need to delete the gang from the gangCache
	if num, found := gangCache.GetChildrenNum(gangName); found && num == 0 {
		gangCache.DeleteGang(gangName)
	}
}

func (gangCache *gangCache) DeleteGang(gangName string) {
	if gangName == "" {
		return
	}
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	delete(gangCache.gangItems, gangName)
}

func (gangCache *gangCache) HasGang(gangName string) bool {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if _, ok := gangCache.gangItems[gangName]; !ok {
		return false
	}
	return true
}

//Get functions
func (gangCache *gangCache) GetGangWaitTime(gangName string) (time.Duration, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return 0, false
	} else {
		return gang.WaitTime, true
	}
}

func (gangCache *gangCache) GetChildrenNum(gangName string) (int, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return 0, false
	} else {
		return len(gang.Children), true
	}
}

func (gangCache *gangCache) GetGangMinNum(gangName string) (int, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return 0, false
	} else {
		return gang.MinRequiredNumber, true
	}
}

func (gangCache *gangCache) GetGangTotalNum(gangName string) (int, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return 0, false
	} else {
		return gang.TotalChildrenNum, true
	}
}

func (gangCache *gangCache) GetGangMode(gangName string) (string, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return "", false
	} else {
		return gang.Mode, true
	}
}

func (gangCache *gangCache) GetGangAssumedPods(gangName string) (int, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return 0, false
	} else {
		return len(gang.WaitingForBindChildren) + len(gang.BoundChildren), true
	}
}

func (gangCache *gangCache) GetGangScheduleCycle(gangName string) (int, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return 0, false
	} else {
		return gang.ScheduleCycle, true
	}
}

func (gangCache *gangCache) GetChildScheduleCycle(gangName string, childName string) (int, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return 0, false
	} else {
		if cycle, found := gang.ChildrenScheduleRoundMap[childName]; !found {
			return 0, false
		} else {
			return cycle, true
		}
	}
}

func (gangCache *gangCache) GetCreateTime(gangName string) (time.Time, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return time.Time{}, false
	} else {
		return gang.CreateTime, true
	}
}

func (gangCache *gangCache) GetGangGroup(gangName string) ([]string, bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return nil, false
	} else {
		return gang.GangGroup, true
	}
}

func (gangCache *gangCache) IsGangResourceSatisfied(gangName string) (isSatisfied bool, found bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return false, false
	} else {
		return gang.ResourceSatisfied, true
	}
}

func (gangCache *gangCache) IsGangScheduleCycleValid(gangName string) (valid bool, found bool) {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	if gang, ok := gangCache.gangItems[gangName]; !ok {
		return false, false
	} else {
		return gang.ScheduleCycleValid, true
	}
}

//Set functions
func (gangCache *gangCache) SetScheduleCycle(gangName string, scheduleCycle int) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	gang := gangCache.gangItems[gangName]
	gang.ScheduleCycle = scheduleCycle
}

func (gangCache *gangCache) SetScheduleCycleValid(gangName string, valid bool) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	gang := gangCache.gangItems[gangName]
	gang.ScheduleCycleValid = valid
}

func (gangCache *gangCache) SetChildCycle(gangName, childName string, childCycle int) {
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	gang := gangCache.gangItems[gangName]
	gang.ChildrenScheduleRoundMap[childName] = childCycle
}

// CountChildNumWithCycle  return how many children with the childCycle in the ChildrenScheduleRoundMap
func (gangCache *gangCache) CountChildNumWithCycle(gangName string, childCycle int) int {
	gangCache.lock.RLock()
	defer gangCache.lock.RUnlock()
	gang := gangCache.gangItems[gangName]
	num := 0
	for _, cycle := range gang.ChildrenScheduleRoundMap {
		if cycle == childCycle {
			num++
		}
	}
	return num
}

func (gangCache *gangCache) AddPod(pod *v1.Pod) {
	if util.IsPodTerminated(pod) {
		gangCache.DeletePod(pod)
		return
	}
	gangName := pod.Annotations[GangNameAnnotation]
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	var gang *Gang
	if _, ok := gangCache.gangItems[gangName]; ok {
		gang = gangCache.gangItems[gangName]
	} else {
		gang = gangCache.NewGangWithPod(pod)
	}
	podName := pod.Name
	gang.Children[podName] = pod
	gang.ChildrenScheduleRoundMap[podName] = 0
}

// NewGangWithPod
//create Gang depending on its first pod's Annotations
func (gangCache *gangCache) NewGangWithPod(pod *v1.Pod) *Gang {
	gangName := pod.Annotations[GangNameAnnotation]
	minRequiredNumber := pod.Annotations[GangMinNumAnnotaion]
	totalChildrenNum := pod.Annotations[GangTotalNumAnnotation]
	mode := pod.Annotations[GangModeAnnotation]
	waitTime := pod.Annotations[GangWaitTimeAnnotaion]
	gangGroup := pod.Annotations[GangGourpsAnnotation]
	rawGang := NewGang(gangName)
	if minRequiredNumber != "" {
		num, err := strconv.Atoi(minRequiredNumber)
		if err != nil {
			klog.Errorf("pod's annotation MinRequiredNumber illegal,err:%v", err.Error())
		} else {
			rawGang.MinRequiredNumber = num
		}
	}
	if totalChildrenNum != "" {
		num, err := strconv.Atoi(totalChildrenNum)
		if err != nil {
			klog.Errorf("pod's annotation totalChildrenNum illegal,err:%v", err.Error())
		} else {
			rawGang.TotalChildrenNum = num
		}
	} else {
		rawGang.TotalChildrenNum = rawGang.MinRequiredNumber
	}
	if mode != "" {
		if mode != StrictMode || mode != NonStrictMode {
			klog.Errorf("pod's annotation mode illegal,err:%v")
		} else {
			rawGang.Mode = mode
		}
	}
	if waitTime != "" {
		num, err := strconv.Atoi(waitTime)
		if err != nil {
			klog.Errorf("pod's annotation waitTime illegal,err:%v", err.Error())
		} else {
			rawGang.WaitTime = time.Duration(num) * time.Second
		}
	}
	if gangGroup != "" {
		groupSlice, err := stringToGangGroup(gangGroup)
		if err != nil {
			klog.Errorf("pod's annotation gangGroup illegal")
		} else {
			rawGang.GangGroup = groupSlice
		}
	}
	return rawGang
}

func (gangCache *gangCache) AddAssumedPod(pod *v1.Pod) {
	if pod == nil {
		return
	}
	gangName := pod.Annotations[GangNameAnnotation]
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	gang := gangCache.gangItems[gangName]
	podName := pod.Name
	delete(gang.BoundChildren, podName)
	gang.WaitingForBindChildren[podName] = pod
	if len(gang.WaitingForBindChildren)+len(gang.BoundChildren) >= gang.MinRequiredNumber {
		gang.ResourceSatisfied = true
	}
}

func (gangCache *gangCache) AddBoundPod(pod *v1.Pod) {
	if pod == nil {
		return
	}
	gangName := pod.Annotations[GangNameAnnotation]
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	gang := gangCache.gangItems[gangName]
	podName := pod.Name
	delete(gang.WaitingForBindChildren, podName)
	gang.BoundChildren[podName] = pod
}

func (gangCache *gangCache) DeletePod(pod *v1.Pod) {
	if pod == nil {
		return
	}
	gangName := pod.Annotations[GangNameAnnotation]
	gangCache.lock.Lock()
	defer gangCache.lock.Unlock()
	gang := gangCache.gangItems[gangName]
	podName := pod.Name
	delete(gang.Children, podName)
	delete(gang.WaitingForBindChildren, podName)
	delete(gang.BoundChildren, podName)
	delete(gang.ChildrenScheduleRoundMap, podName)
	if len(gang.WaitingForBindChildren)+len(gang.BoundChildren) < gang.MinRequiredNumber {
		gang.ResourceSatisfied = false
	}
}

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
}

func NewGang(gangName string) *Gang {
	return &Gang{
		Name:                     gangName,
		CreateTime:               time.Now(),
		WaitTime:                 GangWaitTime,
		Mode:                     StrictMode,
		Children:                 make(map[string]*v1.Pod),
		WaitingForBindChildren:   make(map[string]*v1.Pod),
		BoundChildren:            make(map[string]*v1.Pod),
		ScheduleCycleValid:       true,
		ScheduleCycle:            1,
		ChildrenScheduleRoundMap: make(map[string]int),
	}
}

//parse string like :"[gangA,gangB]"  => []string{"gangA"."gangB"}
func stringToGangGroup(s string) ([]string, error) {
	defaultSlice := make([]string, 0)
	if s == "" {
		return defaultSlice, nil
	}
	length := len(s)
	if s[0] != '[' && s[length-1] != ']' {
		return defaultSlice, fmt.Errorf("gangGroup info illegal")
	}
	s = s[1 : length-1]
	if strings.Contains(s, "[") || strings.Contains(s, "]") {
		return defaultSlice, fmt.Errorf("gangGroup info illegal")
	}
	return strings.Split(s, ","), nil
}
