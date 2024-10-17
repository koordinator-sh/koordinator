package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestGangGroupInfo(t *testing.T) {
	{
		gg := NewGangGroupInfo("aa", []string{"aa"})
		gg.SetInitialized()
		assert.Equal(t, 1, gg.ScheduleCycle)
		assert.Equal(t, true, gg.ScheduleCycleValid)
		assert.True(t, !gg.LastScheduleTime.IsZero())
	}
	{
		gg := NewGangGroupInfo("aa", []string{"aa"})
		gg.SetInitialized()
		gg.setScheduleCycleInvalid()

		gg.SetGangTotalChildrenNum("aa", 1)
		gg.SetGangTotalChildrenNum("bb", 1)

		pod1 := &corev1.Pod{}
		pod1.Namespace = "test"
		pod1.Name = "pod1"

		pod2 := &corev1.Pod{}
		pod2.Namespace = "test"
		pod2.Name = "pod2"

		assert.Equal(t, 0, gg.ChildrenScheduleRoundMap["test/pod1"])
		gg.setChildScheduleCycle(pod1, 1)
		assert.Equal(t, 1, gg.getChildScheduleCycle(pod1))
		assert.Equal(t, 1, gg.ChildrenScheduleRoundMap["test/pod1"])

		assert.Equal(t, 1, gg.GetScheduleCycle())
		assert.Equal(t, 1, gg.ScheduleCycle)

		gg.trySetScheduleCycleValid()
		assert.Equal(t, false, gg.IsScheduleCycleValid())
		assert.Equal(t, 1, gg.GetScheduleCycle())
		assert.Equal(t, 1, gg.getChildScheduleCycle(pod1))
		assert.Equal(t, 0, gg.getChildScheduleCycle(pod2))

		gg.setChildScheduleCycle(pod2, 1)
		gg.trySetScheduleCycleValid()
		assert.Equal(t, true, gg.IsScheduleCycleValid())
		assert.Equal(t, 2, gg.GetScheduleCycle())
		assert.Equal(t, 1, gg.getChildScheduleCycle(pod1))
		assert.Equal(t, 1, gg.getChildScheduleCycle(pod2))

		assert.Equal(t, 2, len(gg.ChildrenScheduleRoundMap))
		gg.deleteChildScheduleCycle("test/pod1")
		assert.Equal(t, 1, len(gg.ChildrenScheduleRoundMap))
	}
	{
		gg := NewGangGroupInfo("aa", []string{"aa"})
		gg.SetInitialized()

		pod1 := &corev1.Pod{}
		pod1.Namespace = "test"
		pod1.Name = "pod1"
		gg.initPodLastScheduleTime(pod1)
		assert.Equal(t, gg.LastScheduleTime, gg.getPodLastScheduleTime(pod1))

		pod2 := &corev1.Pod{}
		pod2.Namespace = "test"
		pod2.Name = "pod2"
		gg.initPodLastScheduleTime(pod2)
		assert.Equal(t, gg.LastScheduleTime, gg.getPodLastScheduleTime(pod2))

		lastScheduleTime1 := gg.LastScheduleTime

		gg.resetPodLastScheduleTime(pod1)
		lastScheduleTime2 := gg.LastScheduleTime
		assert.NotEqual(t, lastScheduleTime1, lastScheduleTime2)
		assert.Equal(t, lastScheduleTime2, gg.LastScheduleTime)
		assert.Equal(t, lastScheduleTime2, gg.getPodLastScheduleTime(pod1))
		assert.Equal(t, lastScheduleTime1, gg.getPodLastScheduleTime(pod2))

		gg.resetPodLastScheduleTime(pod1)
		assert.Equal(t, lastScheduleTime2, gg.LastScheduleTime)
		assert.Equal(t, lastScheduleTime2, gg.getPodLastScheduleTime(pod1))
		assert.Equal(t, lastScheduleTime1, gg.getPodLastScheduleTime(pod2))

		gg.resetPodLastScheduleTime(pod2)
		assert.Equal(t, lastScheduleTime2, gg.LastScheduleTime)
		assert.Equal(t, lastScheduleTime2, gg.getPodLastScheduleTime(pod1))
		assert.Equal(t, lastScheduleTime2, gg.getPodLastScheduleTime(pod2))

		gg.resetPodLastScheduleTime(pod2)
		lastScheduleTime3 := gg.LastScheduleTime
		assert.NotEqual(t, lastScheduleTime2, lastScheduleTime3)
		assert.Equal(t, lastScheduleTime3, gg.LastScheduleTime)
		assert.Equal(t, lastScheduleTime2, gg.getPodLastScheduleTime(pod1))
		assert.Equal(t, lastScheduleTime3, gg.getPodLastScheduleTime(pod2))

		assert.Equal(t, 2, len(gg.ChildrenLastScheduleTime))
		gg.deletePodLastScheduleTime("test/pod1")
		assert.Equal(t, 1, len(gg.ChildrenLastScheduleTime))
	}
	{
		gg := NewGangGroupInfo("aa", []string{"aa"})
		assert.Equal(t, false, gg.isGangOnceResourceSatisfied())

		gg.setResourceSatisfied()
		assert.Equal(t, true, gg.isGangOnceResourceSatisfied())
	}
}
