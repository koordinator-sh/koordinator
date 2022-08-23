/*
Copyright 2022 The Koordinator Authors.

:Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gang

import (
	"strconv"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"

	schedulingconfig "github.com/koordinator-sh/koordinator/apis/scheduling/config"
)

var (
	timeNowFn = time.Now
)

const (
	GangFromPodGroupCrd   string = "GangFromPodGroupCrd"
	GangFromPodAnnotation string = "GangFromPodAnnotation"
)

// Gang  basic gang info recorded in gangCache:
type Gang struct {
	Name       string
	WaitTime   time.Duration
	CreateTime time.Time

	// time that first pod wait in Permit stage
	TimeoutStartTime time.Time
	// strict-mode or non-strict-mode
	Mode              string
	MinRequiredNumber int
	TotalChildrenNum  int
	GangGroup         []string
	Children          map[string]*v1.Pod
	// pods that have already assumed(waiting in Permit stage)
	WaitingForBindChildren map[string]*v1.Pod
	// pods that have already bound
	BoundChildren map[string]*v1.Pod
	// if assumed  pods number has reached to MinRequiredNumber
	ResourceSatisfied bool

	// if the gang should be passed at PreFilter stage(Strict-Mode)
	ScheduleCycleValid bool
	// these fields used to count the cycle
	ScheduleCycle            int
	ChildrenScheduleRoundMap map[string]int

	GangFrom    string
	HasGangInit bool

	lock sync.Mutex
}

func NewGang(gangName string) *Gang {
	return &Gang{
		Name:                     gangName,
		CreateTime:               timeNowFn(),
		WaitTime:                 0,
		Mode:                     extension.GangModeStrict,
		Children:                 make(map[string]*v1.Pod),
		WaitingForBindChildren:   make(map[string]*v1.Pod),
		BoundChildren:            make(map[string]*v1.Pod),
		ScheduleCycleValid:       true,
		ScheduleCycle:            1,
		ChildrenScheduleRoundMap: make(map[string]int),
		GangFrom:                 GangFromPodAnnotation,
		HasGangInit:              false,
	}
}

func (gang *Gang) tryInitByPodConfig(pod *v1.Pod, args *schedulingconfig.GangArgs) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	if gang.HasGangInit {
		return
	}

	minRequiredNumber, err := strconv.Atoi(pod.Annotations[extension.AnnotationGangMinNum])
	if err != nil {
		klog.Errorf("pod's annotation MinRequiredNumber illegal, gangName: %v, value: %v",
			gang.Name, pod.Annotations[extension.AnnotationGangMinNum])
		return
	}
	gang.MinRequiredNumber = minRequiredNumber

	totalChildrenNum, err := strconv.Atoi(pod.Annotations[extension.AnnotationGangTotalNum])
	if err != nil {
		klog.Errorf("pod's annotation TotalNumber illegal, gangName: %v, value: %v",
			gang.Name, pod.Annotations[extension.AnnotationGangTotalNum])
		totalChildrenNum = minRequiredNumber
	} else if totalChildrenNum != 0 && totalChildrenNum < minRequiredNumber {
		klog.Errorf("pod's annotation TotalNumber cannot less than minRequiredNumber, gangName: %v, TotalNumber: %v,minRequiredNumber: %v",
			gang.Name, pod.Annotations[extension.AnnotationGangTotalNum], minRequiredNumber)
		totalChildrenNum = minRequiredNumber
	}
	gang.TotalChildrenNum = totalChildrenNum

	mode := pod.Annotations[extension.AnnotationGangMode]
	if mode != extension.GangModeStrict && mode != extension.GangModeNonStrict {
		klog.Errorf("pod's annotation GangModeAnnotation illegal, gangName: %v, value: %v",
			gang.Name, pod.Annotations[extension.AnnotationGangMode])
		mode = extension.GangModeStrict
	}
	gang.Mode = mode

	// here we assume that gang's CreateTime equal with the pod's CreateTime
	gang.CreateTime = pod.CreationTimestamp.Time

	waitTime, err := time.ParseDuration(pod.Annotations[extension.AnnotationGangWaitTime])
	if err != nil || waitTime <= 0 {
		klog.Errorf("pod's annotation GangWaitTimeAnnotation illegal, gangName: %v, value: %v",
			gang.Name, pod.Annotations[extension.AnnotationGangWaitTime])
		if args.DefaultTimeoutSeconds != nil {
			waitTime = args.DefaultTimeoutSeconds.Duration
		} else {
			klog.Errorf("gangArgs DefaultTimeoutSeconds is nil")
			waitTime = 0
		}
	}
	gang.WaitTime = waitTime

	groupSlice, err := stringToGangGroupSlice(pod.Annotations[extension.AnnotationGangGroups])
	if err != nil {
		klog.Errorf("pod's annotation GangGroupsAnnotation illegal, gangName: %v, value: %v",
			gang.Name, pod.Annotations[extension.AnnotationGangGroups])
	}
	gang.GangGroup = groupSlice

	gang.HasGangInit = true
	klog.Infof("TryInitByPodConfig done, gangName: %v, minRequiredNumber: %v, totalChildrenNum: %v, "+
		"mode: %v, waitTime: %v, groupSlice: %v", gang.Name, gang.MinRequiredNumber, gang.TotalChildrenNum,
		gang.Mode, gang.WaitTime, gang.GangGroup)
}

func (gang *Gang) tryInitByPodGroup(pg *v1alpha1.PodGroup, args *schedulingconfig.GangArgs) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	if gang.HasGangInit {
		return
	}
	minRequiredNumber := int(pg.Spec.MinMember)
	gang.MinRequiredNumber = minRequiredNumber

	totalChildrenNum, err := strconv.Atoi(pg.Annotations[extension.AnnotationGangTotalNum])
	if err != nil {
		klog.Errorf("podGroup's annotation TotalNumber illegal, gangName: %v, value: %v",
			gang.Name, pg.Annotations[extension.AnnotationGangTotalNum])
		totalChildrenNum = minRequiredNumber
	} else if totalChildrenNum != 0 && totalChildrenNum < minRequiredNumber {
		klog.Errorf("podGroup's annotation TotalNumber cannot less than minRequiredNumber, gangName:%v, TotalNumber: %v,minRequiredNumber: %v",
			gang.Name, pg.Annotations[extension.AnnotationGangTotalNum], minRequiredNumber)
		totalChildrenNum = minRequiredNumber
	}
	gang.TotalChildrenNum = totalChildrenNum

	mode := pg.Annotations[extension.AnnotationGangMode]
	if mode != extension.GangModeStrict && mode != extension.GangModeNonStrict {
		klog.Errorf("podGroup's annotation GangModeAnnotation illegal, gangName: %v, value: %v",
			gang.Name, pg.Annotations[extension.AnnotationGangMode])
		mode = extension.GangModeStrict
	}
	gang.Mode = mode

	// here we assume that gang's CreateTime equal with the podGroup CRD CreateTime
	gang.CreateTime = pg.CreationTimestamp.Time

	waitTime, err := parsePgTimeoutSeconds(*pg.Spec.ScheduleTimeoutSeconds)
	if err != nil {
		klog.Errorf("podGroup's ScheduleTimeoutSeconds illegal, gangName: %v, value: %v",
			gang.Name, pg.Spec.ScheduleTimeoutSeconds)
		if args.DefaultTimeoutSeconds != nil {
			waitTime = args.DefaultTimeoutSeconds.Duration
		}
	}
	gang.WaitTime = waitTime

	groupSlice, err := stringToGangGroupSlice(pg.Annotations[extension.AnnotationGangGroups])
	if err != nil {
		klog.Errorf("podGroup's annotation GangGroupsAnnotation illegal, gangName: %v, value: %v",
			gang.Name, pg.Annotations[extension.AnnotationGangGroups])
	}
	gang.GangGroup = groupSlice
	gang.GangFrom = GangFromPodGroupCrd
	gang.HasGangInit = true
	klog.Infof("TryInitByPodGroup done, gangName: %v, minRequiredNumber: %v, totalChildrenNum: %v, "+
		"mode: %v, waitTime: %v, groupSlice: %v", gang.Name, gang.MinRequiredNumber, gang.TotalChildrenNum,
		gang.Mode, gang.WaitTime, gang.GangGroup)
}

func (gang *Gang) deletePod(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}

	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := getNamespaceSplicingName(pod.Namespace, pod.Name)
	klog.Infof("Delete pod from gang: %v, podName: %v", gang.Name, podId)

	delete(gang.Children, podId)
	delete(gang.WaitingForBindChildren, podId)
	delete(gang.BoundChildren, podId)
	delete(gang.ChildrenScheduleRoundMap, podId)

	if gang.GangFrom == GangFromPodAnnotation {
		if len(gang.Children) == 0 {
			return true
		}
	}
	return false
}

func (gang *Gang) getGangWaitTime() time.Duration {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.WaitTime
}

func (gang *Gang) getChildrenNum() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return len(gang.Children)
}

func (gang *Gang) getGangMinNum() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.MinRequiredNumber
}

func (gang *Gang) getGangTotalNum() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.TotalChildrenNum
}

func (gang *Gang) getGangMode() string {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.Mode
}

func (gang *Gang) getGangAssumedPods() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return len(gang.WaitingForBindChildren) + len(gang.BoundChildren)
}

func (gang *Gang) getGangScheduleCycle() int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.ScheduleCycle
}

func (gang *Gang) getChildScheduleCycle(pod *v1.Pod) int {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := getNamespaceSplicingName(pod.Namespace, pod.Name)
	return gang.ChildrenScheduleRoundMap[podId]
}

func (gang *Gang) getCreateTime() time.Time {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.CreateTime
}

func (gang *Gang) getGangGroup() []string {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.GangGroup
}

func (gang *Gang) isGangResourceSatisfied() bool {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.ResourceSatisfied
}

func (gang *Gang) isGangScheduleCycleValid() bool {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	return gang.ScheduleCycleValid
}

func (gang *Gang) setChild(pod *v1.Pod) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := getNamespaceSplicingName(pod.Namespace, pod.Name)
	gang.Children[podId] = pod
	klog.Infof("SetChild, gangName: %v, childName: %v", gang.Name, podId)
}

func (gang *Gang) setScheduleCycleValid(valid bool) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	gang.ScheduleCycleValid = valid
	klog.Infof("SetScheduleCycleValid, gangName: %v, valid: %v", gang.Name, valid)
}

func (gang *Gang) setChildScheduleCycle(pod *v1.Pod, childCycle int) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := getNamespaceSplicingName(pod.Namespace, pod.Name)
	gang.ChildrenScheduleRoundMap[podId] = childCycle
}

func (gang *Gang) trySetScheduleCycleTrue() {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	num := 0
	for _, childScheduleCycle := range gang.ChildrenScheduleRoundMap {
		if childScheduleCycle == gang.ScheduleCycle {
			num++
		}
	}
	if num == gang.TotalChildrenNum {
		gang.ScheduleCycleValid = true
		gang.ScheduleCycle++
		klog.Infof("trySetScheduleCycleTrue, gangName: %v, ScheduleCycle: %v, ScheduleCycleValid: %v",
			gang.Name, gang.ScheduleCycle, true)
	}
}

func (gang *Gang) addAssumedPod(pod *v1.Pod) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := getNamespaceSplicingName(pod.Namespace, pod.Name)
	gang.WaitingForBindChildren[podId] = pod
	klog.Infof("AddAssumedPod, gangName: %v, podName: %v", gang.Name, podId)

	if len(gang.WaitingForBindChildren)+len(gang.BoundChildren) >= gang.MinRequiredNumber {
		gang.ResourceSatisfied = true
		klog.Infof("Gang ResourceSatisfied, gangName: %v", gang.Name)
	}
}

func (gang *Gang) setResourceSatisfied() {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	if !gang.ResourceSatisfied {
		gang.ResourceSatisfied = true
		klog.Infof("Gang ResourceSatisfied, gangName: %v", gang.Name)
	}
}

func (gang *Gang) setTimeoutStartTime(startTime time.Time) {
	gang.lock.Lock()
	defer gang.lock.Unlock()
	if gang.TimeoutStartTime.IsZero() {
		gang.TimeoutStartTime = startTime
		klog.Infof("Set Gang TimeoutStartTime, gangName: %v,TimeoutStartTime: %v", gang.Name, gang.TimeoutStartTime)
	}
}

func (gang *Gang) clearTimeoutStartTime() {
	gang.lock.Lock()
	defer gang.lock.Unlock()
	gang.TimeoutStartTime = time.Time{}
	klog.Infof("Clear Gang TimeoutStartTime, gangName: %v", gang.Name)
}

func (gang *Gang) isGangTimeout() bool {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	if !gang.TimeoutStartTime.IsZero() {
		passedTime := time.Now().Sub(gang.TimeoutStartTime)
		if passedTime >= gang.WaitTime {
			return true
		}
	}
	return false
}

func (gang *Gang) addBoundPod(pod *v1.Pod) {
	gang.lock.Lock()
	defer gang.lock.Unlock()

	podId := getNamespaceSplicingName(pod.Namespace, pod.Name)
	delete(gang.WaitingForBindChildren, podId)
	gang.BoundChildren[podId] = pod

	klog.Infof("AddBoundPod, gangName: %v, podName: %v", gang.Name, podId)
}
