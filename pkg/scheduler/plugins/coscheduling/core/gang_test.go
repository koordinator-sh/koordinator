package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestGangGroupInfo_SetGangGroupInfo(t *testing.T) {
	gangGroupInfo := NewGangGroupInfo("aa_bb", []string{"aa", "bb"})
	gangGroupInfo.SetInitialized()
	assert.Equal(t, "aa_bb", gangGroupInfo.GangGroupId)
	assert.Equal(t, 2, len(gangGroupInfo.GangGroup))
	assert.Equal(t, 1, gangGroupInfo.ScheduleCycle)
	assert.Equal(t, true, gangGroupInfo.ScheduleCycleValid)

	assert.Equal(t, 2, len(gangGroupInfo.GangTotalChildrenNumMap))
	assert.Equal(t, 0, gangGroupInfo.GangTotalChildrenNumMap["aa"])
	assert.Equal(t, 0, gangGroupInfo.GangTotalChildrenNumMap["bb"])

	assert.True(t, !gangGroupInfo.LastScheduleTime.IsZero())
	assert.Equal(t, "aa_bb", gangGroupInfo.GangGroupId)
	assert.Equal(t, 0, len(gangGroupInfo.ChildrenLastScheduleTime))

	gang := &Gang{}
	gang.GangGroupInfo = NewGangGroupInfo("", nil)
	gang.Name = "aa"
	gang.TotalChildrenNum = 2
	gang.SetGangGroupInfo(gangGroupInfo)
	assert.Equal(t, gang.GangGroupInfo.GangTotalChildrenNumMap["aa"], 2)

	gang.BoundChildren = map[string]*corev1.Pod{
		"pod1": {},
		"pod2": {},
	}
	assert.Equal(t, int32(2), gang.getBoundPodNum())
}

func TestDeletePod(t *testing.T) {
	gangGroupInfo := NewGangGroupInfo("aa_bb", []string{"aa", "bb"})
	gangGroupInfo.SetInitialized()
	gangGroupInfo.ChildrenScheduleRoundMap["test/pod1"] = 1
	gangGroupInfo.ChildrenLastScheduleTime["test/pod1"] = time.Now()

	gang := &Gang{}
	gang.GangGroupInfo = NewGangGroupInfo("", nil)
	gang.Name = "aa"
	gang.TotalChildrenNum = 2
	gang.SetGangGroupInfo(gangGroupInfo)

	pod := &corev1.Pod{}
	pod.Namespace = "test"
	pod.Name = "pod1"

	assert.Equal(t, 1, len(gangGroupInfo.ChildrenScheduleRoundMap))
	assert.Equal(t, 1, len(gangGroupInfo.ChildrenLastScheduleTime))
	gang.deletePod(pod)
	assert.Equal(t, 0, len(gangGroupInfo.ChildrenScheduleRoundMap))
	assert.Equal(t, 0, len(gangGroupInfo.ChildrenLastScheduleTime))
}

func TestIsScheduleCycleValid_GetScheduleCycle_GetChildScheduleCycle_SetChildScheduleCycle(t *testing.T) {
	gangGroupInfo := NewGangGroupInfo("aa_bb", []string{"aa", "bb"})
	gangGroupInfo.SetInitialized()
	gangGroupInfo.ChildrenScheduleRoundMap["test/pod1"] = 1
	gangGroupInfo.ScheduleCycle = 2

	gang := &Gang{}
	gang.GangGroupInfo = NewGangGroupInfo("", nil)
	gang.SetGangGroupInfo(gangGroupInfo)

	pod := &corev1.Pod{}
	pod.Namespace = "test"
	pod.Name = "pod1"
	assert.Equal(t, 1, gang.getChildScheduleCycle(pod))
	assert.Equal(t, 2, gang.getScheduleCycle())

	assert.Equal(t, true, gang.isScheduleCycleValid())
	gangGroupInfo.ScheduleCycleValid = false
	assert.Equal(t, false, gang.isScheduleCycleValid())

	gang.setChildScheduleCycle(pod, 33)
	assert.Equal(t, 33, gang.getChildScheduleCycle(pod))
	assert.Equal(t, 33, gang.GangGroupInfo.ChildrenScheduleRoundMap["test/pod1"])
}

func TestInitPodLastScheduleTime_GetPodLastScheduleTime_ResetPodLastScheduleTime(t *testing.T) {
	gangGroupInfo := NewGangGroupInfo("aa_bb", []string{"aa", "bb"})
	gangGroupInfo.SetInitialized()
	gangGroupInfo.LastScheduleTime = time.Now()

	gang := &Gang{}
	gang.GangGroupInfo = NewGangGroupInfo("", nil)
	gang.Children = make(map[string]*corev1.Pod)
	gang.SetGangGroupInfo(gangGroupInfo)

	pod1 := &corev1.Pod{}
	pod1.Namespace = "test"
	pod1.Name = "pod1"
	gang.initPodLastScheduleTime(pod1)
	assert.Equal(t, gang.GangGroupInfo.LastScheduleTime, gang.getPodLastScheduleTime(pod1))

	pod2 := &corev1.Pod{}
	pod2.Namespace = "test"
	pod2.Name = "pod2"
	gang.initPodLastScheduleTime(pod2)
	assert.Equal(t, gang.GangGroupInfo.LastScheduleTime, gang.getPodLastScheduleTime(pod2))

	lastScheduleTime1 := gangGroupInfo.LastScheduleTime

	gang.resetPodLastScheduleTime(pod1)
	lastScheduleTime2 := gangGroupInfo.LastScheduleTime
	assert.NotEqual(t, lastScheduleTime1, lastScheduleTime2)
	assert.Equal(t, lastScheduleTime2, gang.GangGroupInfo.LastScheduleTime)
	assert.Equal(t, lastScheduleTime2, gang.getPodLastScheduleTime(pod1))
	assert.Equal(t, lastScheduleTime1, gang.getPodLastScheduleTime(pod2))

	gang.resetPodLastScheduleTime(pod1)
	assert.Equal(t, lastScheduleTime2, gang.GangGroupInfo.LastScheduleTime)
	assert.Equal(t, lastScheduleTime2, gang.getPodLastScheduleTime(pod1))
	assert.Equal(t, lastScheduleTime1, gang.getPodLastScheduleTime(pod2))

	gang.Children[pod2.Name] = pod2
	gang.initAllChildrenPodLastScheduleTime()
	assert.Equal(t, lastScheduleTime2, gang.GangGroupInfo.LastScheduleTime)
	assert.Equal(t, lastScheduleTime2, gang.getPodLastScheduleTime(pod1))
	assert.Equal(t, lastScheduleTime2, gang.getPodLastScheduleTime(pod2))

	gang.resetPodLastScheduleTime(pod2)
	lastScheduleTime3 := gangGroupInfo.LastScheduleTime
	assert.NotEqual(t, lastScheduleTime2, lastScheduleTime3)
	assert.Equal(t, lastScheduleTime3, gang.GangGroupInfo.LastScheduleTime)
	assert.Equal(t, lastScheduleTime2, gang.getPodLastScheduleTime(pod1))
	assert.Equal(t, lastScheduleTime3, gang.getPodLastScheduleTime(pod2))
}

func TestScheduleCycleRelated(t *testing.T) {
	gangGroupInfo := NewGangGroupInfo("aa_bb", []string{"aa", "bb"})
	gangGroupInfo.SetInitialized()
	gangGroupInfo.LastScheduleTime = time.Now()

	gang := &Gang{}
	gang.GangGroupInfo = NewGangGroupInfo("", nil)
	gang.Name = "aa"
	gang.SetGangGroupInfo(gangGroupInfo)
	gangGroupInfo.GangTotalChildrenNumMap["aa"] = 1
	gangGroupInfo.GangTotalChildrenNumMap["bb"] = 1

	pod1 := &corev1.Pod{}
	pod1.Namespace = "test"
	pod1.Name = "pod1"

	pod2 := &corev1.Pod{}
	pod2.Namespace = "test"
	pod2.Name = "pod2"

	gang.setScheduleCycleInvalid()
	assert.Equal(t, false, gangGroupInfo.ScheduleCycleValid)
	assert.Equal(t, false, gang.isScheduleCycleValid())

	assert.Equal(t, 0, gang.GangGroupInfo.ChildrenScheduleRoundMap["test/pod1"])
	gang.setChildScheduleCycle(pod1, 1)
	assert.Equal(t, 1, gang.getChildScheduleCycle(pod1))
	assert.Equal(t, 1, gang.GangGroupInfo.ChildrenScheduleRoundMap["test/pod1"])

	assert.Equal(t, 1, gang.getScheduleCycle())
	assert.Equal(t, 1, gang.GangGroupInfo.ScheduleCycle)

	gang.trySetScheduleCycleValid()
	assert.Equal(t, false, gang.isScheduleCycleValid())
	assert.Equal(t, 1, gang.getScheduleCycle())
	assert.Equal(t, 1, gang.getChildScheduleCycle(pod1))
	assert.Equal(t, 0, gang.getChildScheduleCycle(pod2))

	gang.setChildScheduleCycle(pod2, 1)
	gang.trySetScheduleCycleValid()
	assert.Equal(t, true, gang.isScheduleCycleValid())
	assert.Equal(t, 2, gang.getScheduleCycle())
	assert.Equal(t, 1, gang.getChildScheduleCycle(pod1))
	assert.Equal(t, 1, gang.getChildScheduleCycle(pod2))
}
