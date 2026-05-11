/*
Copyright 2026 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package core

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/component-base/metrics/testutil"

	schedulermetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

func setupMetrics() {
	schedulermetrics.Register()
	schedulermetrics.WaitingGangGroupNumber.Reset()
}

func gaugeValue(t *testing.T) float64 {
	t.Helper()
	v, err := testutil.GetGaugeMetricValue(schedulermetrics.WaitingGangGroupNumber.WithLabelValues())
	assert.NoError(t, err)
	return v
}

func makePod(ns, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
	}
}

func TestNewGangGroupInfo_FieldInitialization(t *testing.T) {
	gangGroup := []string{"gang-a", "gang-b"}
	gg := NewGangGroupInfo("group-1", gangGroup)

	assert.Equal(t, "group-1", gg.GangGroupId)
	assert.Equal(t, gangGroup, gg.GangGroup)
	assert.False(t, gg.Initialized)
	assert.False(t, gg.OnceResourceSatisfied)
	assert.NotNil(t, gg.WaitingGangIDs)
	assert.Equal(t, 0, gg.WaitingGangIDs.Len())
	assert.Equal(t, "", gg.RepresentativePodKey)
	assert.Nil(t, gg.BindingMemberPods)
}

func TestSetInitialized_TransitionsToTrue(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	assert.False(t, gg.IsInitialized())
	gg.SetInitialized()
	assert.True(t, gg.IsInitialized())
}

func TestSetInitialized_ConcurrentSafe(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gg.SetInitialized()
			_ = gg.IsInitialized()
		}()
	}
	wg.Wait()
	assert.True(t, gg.IsInitialized())
}

func TestSetResourceSatisfied_OnceTrue_IsIrreversible(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	assert.False(t, gg.isGangOnceResourceSatisfied())
	gg.setResourceSatisfied()
	assert.True(t, gg.isGangOnceResourceSatisfied())
	gg.setResourceSatisfied()
	assert.True(t, gg.isGangOnceResourceSatisfied())
}

func TestSetResourceSatisfied_ConcurrentSafe(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gg.setResourceSatisfied()
			_ = gg.isGangOnceResourceSatisfied()
		}()
	}
	wg.Wait()
	assert.True(t, gg.isGangOnceResourceSatisfied())
}

func TestAddWaitingGang_InsertsAllGroupIDs(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a", "gang-b", "gang-c"})
	gg.AddWaitingGang()
	assert.True(t, gg.WaitingGangIDs.Has("gang-a"))
	assert.True(t, gg.WaitingGangIDs.Has("gang-b"))
	assert.True(t, gg.WaitingGangIDs.Has("gang-c"))
}

func TestAddWaitingGang_MetricIncrementedOnFirstCall(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a"})
	before := gaugeValue(t)
	gg.AddWaitingGang()
	assert.Equal(t, before+1, gaugeValue(t))
}

func TestAddWaitingGang_MetricNotIncrementedOnSubsequentCall(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a"})
	gg.AddWaitingGang()
	afterFirst := gaugeValue(t)
	gg.AddWaitingGang()
	assert.Equal(t, afterFirst, gaugeValue(t))
}

func TestAddWaitingGang_EmptyGroupNoMetricChange(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{})
	before := gaugeValue(t)
	gg.AddWaitingGang()
	assert.Equal(t, before, gaugeValue(t))
}

func TestRemoveWaitingGang_RemovesSingleID(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a", "gang-b"})
	gg.AddWaitingGang()
	gg.RemoveWaitingGang("gang-a")
	assert.False(t, gg.WaitingGangIDs.Has("gang-a"))
	assert.True(t, gg.WaitingGangIDs.Has("gang-b"))
}

func TestRemoveWaitingGang_MetricDecrementedWhenSetBecomesEmpty(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a"})
	gg.AddWaitingGang()
	beforeRemove := gaugeValue(t)
	gg.RemoveWaitingGang("gang-a")
	assert.Equal(t, beforeRemove-1, gaugeValue(t))
}

func TestRemoveWaitingGang_MetricNotDecrementedWhenSetStillNonEmpty(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a", "gang-b"})
	gg.AddWaitingGang()
	afterAdd := gaugeValue(t)
	gg.RemoveWaitingGang("gang-a")
	assert.Equal(t, afterAdd, gaugeValue(t))
}

func TestRemoveWaitingGang_ClearsBindingMemberPodsWhenEmpty(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a"})
	gg.AddWaitingGang()
	gg.SetBindingMembers(sets.New[string]("pod1", "pod2"))
	gg.RemoveWaitingGang("gang-a")
	assert.Nil(t, gg.BindingMemberPods)
}

func TestRemoveWaitingGang_DoesNotClearBindingMemberPodsWhenNonEmpty(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a", "gang-b"})
	gg.AddWaitingGang()
	gg.SetBindingMembers(sets.New[string]("pod1"))
	gg.RemoveWaitingGang("gang-a")
	assert.NotNil(t, gg.BindingMemberPods)
}

func TestRemoveWaitingGang_NoMetricChangeWhenAlreadyEmpty(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a"})
	before := gaugeValue(t)
	gg.RemoveWaitingGang("gang-a")
	assert.Equal(t, before, gaugeValue(t))
}

func TestClearWaitingGang_ClearsAllIDs(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a", "gang-b"})
	gg.AddWaitingGang()
	gg.ClearWaitingGang()
	assert.Equal(t, 0, gg.WaitingGangIDs.Len())
}

func TestClearWaitingGang_MetricDecrementedWhenWasNonEmpty(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a"})
	gg.AddWaitingGang()
	afterAdd := gaugeValue(t)
	gg.ClearWaitingGang()
	assert.Equal(t, afterAdd-1, gaugeValue(t))
}

func TestClearWaitingGang_MetricNotDecrementedWhenAlreadyEmpty(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a"})
	before := gaugeValue(t)
	gg.ClearWaitingGang()
	assert.Equal(t, before, gaugeValue(t))
}

func TestClearWaitingGang_ClearsBindingMemberPods(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a"})
	gg.AddWaitingGang()
	gg.SetBindingMembers(sets.New[string]("pod1"))
	gg.ClearWaitingGang()
	assert.Nil(t, gg.BindingMemberPods)
}

func TestClearWaitingGang_NilBindingMemberPodsIsNoop(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("g1", []string{"gang-a"})
	gg.AddWaitingGang()
	assert.NotPanics(t, func() { gg.ClearWaitingGang() })
}

func TestRecordIfNoRepresentatives_SetsKeyWhenEmpty(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	pod := makePod("default", "pod-a")
	key := gg.RecordIfNoRepresentatives(pod)
	assert.Equal(t, "default/pod-a", key)
	assert.Equal(t, "default/pod-a", gg.RepresentativePodKey)
}

func TestRecordIfNoRepresentatives_ReturnsExistingKeyForDifferentPod(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	podA := makePod("default", "pod-a")
	podB := makePod("default", "pod-b")
	gg.RecordIfNoRepresentatives(podA)
	returned := gg.RecordIfNoRepresentatives(podB)
	assert.Equal(t, "default/pod-a", returned)
	assert.Equal(t, "default/pod-a", gg.RepresentativePodKey)
}

func TestDeleteIfRepresentative_ClearsKeyWhenPodMatches(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	pod := makePod("default", "pod-a")
	gg.RecordIfNoRepresentatives(pod)
	gg.DeleteIfRepresentative(pod, ReasonPodDeleted)
	assert.Equal(t, "", gg.RepresentativePodKey)
}

func TestDeleteIfRepresentative_NoOpWhenPodDoesNotMatch(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	podA := makePod("default", "pod-a")
	podB := makePod("default", "pod-b")
	gg.RecordIfNoRepresentatives(podA)
	gg.DeleteIfRepresentative(podB, ReasonPodDeleted)
	assert.Equal(t, "default/pod-a", gg.RepresentativePodKey)
}

func TestDeleteIfRepresentative_NoOpWhenKeyAlreadyEmpty(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	pod := makePod("default", "pod-a")
	assert.NotPanics(t, func() { gg.DeleteIfRepresentative(pod, ReasonPodBound) })
	assert.Equal(t, "", gg.RepresentativePodKey)
}

func TestIsRepresentative_TrueForRepresentativePod(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	pod := makePod("default", "pod-a")
	gg.RecordIfNoRepresentatives(pod)
	assert.True(t, gg.IsRepresentative(pod))
}

func TestIsRepresentative_FalseForNonRepresentativePod(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	podA := makePod("default", "pod-a")
	podB := makePod("default", "pod-b")
	gg.RecordIfNoRepresentatives(podA)
	assert.False(t, gg.IsRepresentative(podB))
}

func TestIsRepresentative_FalseWhenNoRepresentativeSet(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	pod := makePod("default", "pod-a")
	assert.False(t, gg.IsRepresentative(pod))
}

func TestClearCurrentRepresentative_AlwaysClears(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	pod := makePod("default", "pod-a")
	gg.RecordIfNoRepresentatives(pod)
	gg.ClearCurrentRepresentative(ReasonGangGroupEnterIntoScheduling)
	assert.Equal(t, "", gg.RepresentativePodKey)
}

func TestClearCurrentRepresentative_NoopWhenAlreadyEmpty(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	assert.NotPanics(t, func() {
		gg.ClearCurrentRepresentative(ReasonGangBasicCheckUnsatisfied)
	})
	assert.Equal(t, "", gg.RepresentativePodKey)
}

func TestSetBindingMembers_GetBindingMembers_RoundTrip(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	pods := sets.New[string]("ns/pod-1", "ns/pod-2", "ns/pod-3")
	gg.SetBindingMembers(pods)
	assert.Equal(t, pods, gg.GetBindingMembers())
}

func TestSetBindingMembers_CanOverwrite(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	gg.SetBindingMembers(sets.New[string]("ns/pod-1"))
	second := sets.New[string]("ns/pod-2", "ns/pod-3")
	gg.SetBindingMembers(second)
	assert.Equal(t, second, gg.GetBindingMembers())
}

func TestGetBindingMembers_NilWhenNotSet(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	assert.Nil(t, gg.GetBindingMembers())
}

func TestSetBindingMembers_ConcurrentSafe(t *testing.T) {
	gg := NewGangGroupInfo("g1", nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gg.SetBindingMembers(sets.New[string]("pod1"))
			_ = gg.GetBindingMembers()
		}()
	}
	wg.Wait()
}

func TestGangGroupInfo_FullLifecycle(t *testing.T) {
	setupMetrics()
	gg := NewGangGroupInfo("lifecycle-group", []string{"gang-a", "gang-b"})
	rep := makePod("default", "leader")

	key := gg.RecordIfNoRepresentatives(rep)
	assert.Equal(t, "default/leader", key)

	gg.AddWaitingGang()
	assert.Equal(t, float64(1), gaugeValue(t))

	gg.setResourceSatisfied()
	assert.True(t, gg.isGangOnceResourceSatisfied())

	bindingPods := sets.New[string]("default/pod-1", "default/pod-2")
	gg.SetBindingMembers(bindingPods)
	assert.Equal(t, bindingPods, gg.GetBindingMembers())

	gg.RemoveWaitingGang("gang-a")
	assert.Equal(t, float64(1), gaugeValue(t))
	assert.NotNil(t, gg.BindingMemberPods)

	gg.RemoveWaitingGang("gang-b")
	assert.Equal(t, float64(0), gaugeValue(t))
	assert.Nil(t, gg.BindingMemberPods)

	gg.DeleteIfRepresentative(rep, ReasonPodBound)
	assert.Equal(t, "", gg.RepresentativePodKey)
}
