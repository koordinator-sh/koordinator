/*
Copyright 2022 The Koordinator Authors.

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

package frameworkext

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

var AssignedPodDelete = framework.ClusterEvent{Resource: framework.Pod, ActionType: framework.Delete, Label: "AssignedPodDelete"}

// Scheduler exports scheduler internal cache and queue interface for testability.
type Scheduler interface {
	GetCache() SchedulerCache
	GetSchedulingQueue() SchedulingQueue
}

type SchedulerCache interface {
	AddPod(pod *corev1.Pod) error
	UpdatePod(oldPod, newPod *corev1.Pod) error
	RemovePod(pod *corev1.Pod) error
	AssumePod(pod *corev1.Pod) error
	IsAssumedPod(pod *corev1.Pod) (bool, error)
	GetPod(pod *corev1.Pod) (*corev1.Pod, error)
	ForgetPod(pod *corev1.Pod) error
}

type SchedulingQueue interface {
	Add(pod *corev1.Pod) error
	Update(oldPod, newPod *corev1.Pod) error
	Delete(pod *corev1.Pod) error
	AddUnschedulableIfNotPresent(pod *framework.QueuedPodInfo, podSchedulingCycle int64) error
	SchedulingCycle() int64
	AssignedPodAdded(pod *corev1.Pod)
	AssignedPodUpdated(pod *corev1.Pod)
	MoveAllToActiveOrBackoffQueue(event framework.ClusterEvent)
}

var _ Scheduler = &SchedulerAdapter{}

type SchedulerAdapter struct {
	Scheduler *scheduler.Scheduler
}

func (s *SchedulerAdapter) GetCache() SchedulerCache {
	return s.Scheduler.SchedulerCache
}

func (s *SchedulerAdapter) GetSchedulingQueue() SchedulingQueue {
	return &queueAdapter{s.Scheduler}
}

func (s *SchedulerAdapter) MoveAllToActiveOrBackoffQueue(event framework.ClusterEvent) {
	s.Scheduler.SchedulingQueue.MoveAllToActiveOrBackoffQueue(event, nil)
}

var _ SchedulingQueue = &queueAdapter{}

type queueAdapter struct {
	scheduler *scheduler.Scheduler
}

func (q *queueAdapter) Add(pod *corev1.Pod) error {
	return q.scheduler.SchedulingQueue.Add(pod)
}

func (q *queueAdapter) Update(oldPod, newPod *corev1.Pod) error {
	return q.scheduler.SchedulingQueue.Update(oldPod, newPod)
}

func (q *queueAdapter) Delete(pod *corev1.Pod) error {
	return q.scheduler.SchedulingQueue.Delete(pod)
}

func (q *queueAdapter) AddUnschedulableIfNotPresent(pod *framework.QueuedPodInfo, podSchedulingCycle int64) error {
	return q.scheduler.SchedulingQueue.AddUnschedulableIfNotPresent(pod, podSchedulingCycle)
}

func (q *queueAdapter) SchedulingCycle() int64 {
	return q.scheduler.SchedulingQueue.SchedulingCycle()
}

func (q *queueAdapter) AssignedPodAdded(pod *corev1.Pod) {
	q.scheduler.SchedulingQueue.AssignedPodAdded(pod)
}

func (q *queueAdapter) AssignedPodUpdated(pod *corev1.Pod) {
	q.scheduler.SchedulingQueue.AssignedPodUpdated(pod)
}

func (q *queueAdapter) MoveAllToActiveOrBackoffQueue(event framework.ClusterEvent) {
	q.scheduler.SchedulingQueue.MoveAllToActiveOrBackoffQueue(event, nil)
}

var _ Scheduler = &FakeScheduler{}
var _ SchedulingQueue = &FakeQueue{}

type FakeScheduler struct {
	Pods       map[string]*corev1.Pod
	AssumedPod map[string]*corev1.Pod
	Queue      *FakeQueue
}

func NewFakeScheduler() *FakeScheduler {
	return &FakeScheduler{
		Pods:       map[string]*corev1.Pod{},
		AssumedPod: map[string]*corev1.Pod{},
		Queue: &FakeQueue{
			Pods:                map[string]*corev1.Pod{},
			UnschedulablePods:   map[string]*corev1.Pod{},
			AssignedPods:        map[string]*corev1.Pod{},
			AssignedUpdatedPods: map[string]*corev1.Pod{},
		},
	}
}

type FakeQueue struct {
	Pods                map[string]*corev1.Pod
	UnschedulablePods   map[string]*corev1.Pod
	AssignedPods        map[string]*corev1.Pod
	AssignedUpdatedPods map[string]*corev1.Pod
}

func (f *FakeScheduler) GetCache() SchedulerCache {
	return f
}

func (f *FakeScheduler) GetSchedulingQueue() SchedulingQueue {
	return f.Queue
}

func (f *FakeScheduler) AddPod(pod *corev1.Pod) error {
	key, _ := framework.GetPodKey(pod)
	f.Pods[key] = pod
	delete(f.AssumedPod, key)
	return nil
}

func (f *FakeScheduler) UpdatePod(oldPod, newPod *corev1.Pod) error {
	key, _ := framework.GetPodKey(newPod)
	f.Pods[key] = newPod
	return nil
}

func (f *FakeScheduler) RemovePod(pod *corev1.Pod) error {
	key, _ := framework.GetPodKey(pod)
	delete(f.Pods, key)
	return nil
}

func (f *FakeScheduler) AssumePod(pod *corev1.Pod) error {
	key, _ := framework.GetPodKey(pod)
	f.AssumedPod[key] = pod
	return nil
}

func (f *FakeScheduler) IsAssumedPod(pod *corev1.Pod) (bool, error) {
	key, _ := framework.GetPodKey(pod)
	_, ok := f.AssumedPod[key]
	return ok, nil
}

func (f *FakeScheduler) GetPod(pod *corev1.Pod) (*corev1.Pod, error) {
	key, _ := framework.GetPodKey(pod)
	p := f.Pods[key]
	return p, nil
}

func (f *FakeScheduler) ForgetPod(pod *corev1.Pod) error {
	key, _ := framework.GetPodKey(pod)
	delete(f.AssumedPod, key)
	return nil
}

func (f *FakeQueue) Add(pod *corev1.Pod) error {
	key, _ := framework.GetPodKey(pod)
	f.Pods[key] = pod
	return nil
}

func (f *FakeQueue) Update(oldPod, newPod *corev1.Pod) error {
	key, _ := framework.GetPodKey(newPod)
	f.Pods[key] = newPod
	return nil
}

func (f *FakeQueue) Delete(pod *corev1.Pod) error {
	key, _ := framework.GetPodKey(pod)
	delete(f.Pods, key)
	delete(f.UnschedulablePods, key)
	return nil
}

func (f *FakeQueue) AddUnschedulableIfNotPresent(pod *framework.QueuedPodInfo, podSchedulingCycle int64) error {
	key, _ := framework.GetPodKey(pod.Pod)
	f.UnschedulablePods[key] = pod.Pod
	return nil
}

func (f *FakeQueue) SchedulingCycle() int64 {
	return 0
}

func (f *FakeQueue) AssignedPodAdded(pod *corev1.Pod) {
	key, _ := framework.GetPodKey(pod)
	f.AssignedPods[key] = pod
}

func (f *FakeQueue) AssignedPodUpdated(pod *corev1.Pod) {
	key, _ := framework.GetPodKey(pod)
	f.AssignedUpdatedPods[key] = pod
}

func (f *FakeQueue) MoveAllToActiveOrBackoffQueue(event framework.ClusterEvent) {

}
