/*
Copyright 2023 The Koordinator Authors.

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

package arbitrator

import (
	"context"
	"fmt"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/utils/sorter"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sync"
	"time"
)

const (
	AnnotationPassedArbitration = "descheduler.koordinator.sh/passed-arbitrator"
)

var enqueueLog = klog.Background().WithName("eventhandler").WithName("DefaultArbitrator")

type DefaultArbitrator struct {
	waitCollection map[types.UID]*v1alpha1.PodMigrationJob
	workQueue      workqueue.RateLimitingInterface

	sorts                 []SortFn
	nonRetryablePodFilter framework.FilterFunc
	retryablePodFilter    framework.FilterFunc

	client        client.Client
	mu            sync.Mutex
	eventRecorder events.EventRecorder

	arbitrationInterval int
}

// Sort stably sorts jobs, outputs the sorted results and corresponding ranking map.
func (a *DefaultArbitrator) Sort(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
	for _, sortFn := range a.sorts {
		jobs = sortFn(jobs, podOfJob)
	}
	return jobs
}

// Filter calls nonRetryablePodFilter and retryablePodFilter to filter operations on PodMigrationJobs.
func (a *DefaultArbitrator) Filter(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) {
	for _, job := range jobs {
		pod := podOfJob[job]
		if pod == nil {
			// TODO: nil处理
			continue
		}
		if a.nonRetryablePodFilter != nil && !a.nonRetryablePodFilter(pod) {
			a.updateNonRetryableFailedJob(job, pod)
			continue
		}
		if a.retryablePodFilter != nil && !a.retryablePodFilter(pod) {
			continue
		}
		a.updatePassedJob(job)
	}
}

// updatePassedJob do something after PMJ passed the Filter.
func (a *DefaultArbitrator) updatePassedJob(job *v1alpha1.PodMigrationJob) {
	if job.Annotations == nil {
		job.Annotations = map[string]string{}
	}
	job.Annotations[AnnotationPassedArbitration] = "true"

	err := a.client.Update(context.TODO(), job)
	if err != nil {
		klog.Errorf("failed to update job %v, err: %v", job.Namespace+"/"+job.Name, err)
	}

	if a.workQueue != nil {
		a.mu.Lock()
		// place jobs into the workQueue
		a.workQueue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      job.GetName(),
			Namespace: job.GetNamespace(),
		}})

		// remove job from the waiting waitCollection
		delete(a.waitCollection, job.UID)
		a.mu.Unlock()
	} else {
		klog.Warningf("work workQueue is nil")
	}
}

// AddJob add a PodMigrationJob to the waiting waitCollection of Arbitrator.
func (a *DefaultArbitrator) AddJob(job *v1alpha1.PodMigrationJob) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.waitCollection == nil {
		klog.Errorf("Waiting waitCollection of Arbitrator is nil")
	}
	a.waitCollection[job.UID] = job.DeepCopy()
}

// doOnceArbitrate performs an arbitrator operation on PodMigrationJobs in the waiting waitCollection.
func (a *DefaultArbitrator) doOnceArbitrate() {
	// copy jobs from waiting waitCollection
	a.mu.Lock()
	jobs := make([]*v1alpha1.PodMigrationJob, len(a.waitCollection))
	i := 0
	for _, job := range a.waitCollection {
		jobs[i] = job
		i++
	}
	a.mu.Unlock()

	podOfJob := getPodOfJob(a.client, jobs)
	// Sort
	jobs = a.Sort(jobs, podOfJob)

	// GroupFilter
	a.Filter(jobs, podOfJob)
}

func (a *DefaultArbitrator) updateNonRetryableFailedJob(job *v1alpha1.PodMigrationJob, pod *corev1.Pod) {
	// delete from waitCollection
	a.mu.Lock()
	delete(a.waitCollection, job.UID)
	a.mu.Unlock()

	// change phase to Failed
	job.Status.Phase = v1alpha1.PodMigrationJobFailed
	job.Status.Reason = v1alpha1.PodMigrationJobReasonForbiddenMigratePod
	job.Status.Message = fmt.Sprintf("Pod %q is forbidden to migrate because it does not meet the requirements", klog.KObj(pod))
	err := a.client.Status().Update(context.TODO(), job)
	if err == nil {
		a.eventRecorder.Eventf(job, nil, corev1.EventTypeWarning, v1alpha1.PodMigrationJobReasonForbiddenMigratePod, "Migrating", job.Status.Message)
	}
}

// Arbitrate the goroutine to arbitrate jobs periodically.
func (a *DefaultArbitrator) Arbitrate(stopCh <-chan struct{}) {
	klog.Infof("Start Arbitrator Arbitrate Goroutine")
	for {
		a.doOnceArbitrate()
		select {
		case <-stopCh:
			return
		case <-time.After(time.Duration(a.arbitrationInterval) * time.Millisecond):
		}
	}
}

// NewArbitration creates an DefaultArbitrator structure based on parameters.
func NewArbitration(args *config.ArbitrationArgs, c client.Client, eventRecorder events.EventRecorder, retryableFilter framework.FilterFunc, nonRetryableFilter framework.FilterFunc) (Arbitrator, bool) {
	if args.Enabled == false {
		return nil, false
	}

	return &DefaultArbitrator{
		client:              c,
		workQueue:           nil,
		mu:                  sync.Mutex{},
		waitCollection:      map[types.UID]*v1alpha1.PodMigrationJob{},
		arbitrationInterval: *args.Interval,
		eventRecorder:       eventRecorder,

		sorts: []SortFn{
			NewPodSortFn(sorter.PodSorter()),
			NewDisperseByWorkloadSortFn(),
			NewJobGatherSortFn(),
			NewJobMigratingSortFn(c),
		},
		retryablePodFilter:    retryableFilter,
		nonRetryablePodFilter: nonRetryableFilter,
	}, true
}

func (a *DefaultArbitrator) WithSortFn(sort SortFn) {
	a.sorts = append(a.sorts, sort)
}

// Create implements EventHandler.
func (a *DefaultArbitrator) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	if evt.Object == nil {
		klog.Error(nil, "CreateEvent received with no metadata", "event", evt)
		return
	}
	if a.workQueue == nil {
		a.workQueue = q
	}
	job := &v1alpha1.PodMigrationJob{}
	err := a.client.Get(context.TODO(), types.NamespacedName{
		Name:      evt.Object.GetName(),
		Namespace: evt.Object.GetNamespace(),
	}, job)
	if err != nil {
		enqueueLog.Error(nil, "Fail to get PodMigrationJob", "PodMigrationJob", types.NamespacedName{
			Name:      evt.Object.GetName(),
			Namespace: evt.Object.GetNamespace(),
		})
		return
	}
	a.AddJob(job)
}

// Update implements EventHandler.
func (a *DefaultArbitrator) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	if a.workQueue == nil {
		a.workQueue = q
	}
	switch {
	case evt.ObjectNew != nil:
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      evt.ObjectNew.GetName(),
			Namespace: evt.ObjectNew.GetNamespace(),
		}})
	case evt.ObjectOld != nil:
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      evt.ObjectOld.GetName(),
			Namespace: evt.ObjectOld.GetNamespace(),
		}})
	default:
		enqueueLog.Error(nil, "UpdateEvent received with no metadata", "event", evt)
	}
}

// Delete implements EventHandler.
func (a *DefaultArbitrator) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	if a.workQueue == nil {
		a.workQueue = q
	}
	if evt.Object == nil {
		enqueueLog.Error(nil, "DeleteEvent received with no metadata", "event", evt)
		return
	}
	q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      evt.Object.GetName(),
		Namespace: evt.Object.GetNamespace(),
	}})
}

// Generic implements EventHandler.
func (a *DefaultArbitrator) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	if a.workQueue == nil {
		a.workQueue = q
	}
	if evt.Object == nil {
		enqueueLog.Error(nil, "GenericEvent received with no metadata", "event", evt)
		return
	}
	q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      evt.Object.GetName(),
		Namespace: evt.Object.GetNamespace(),
	}})
}

func getPodOfJob(c client.Client, jobs []*v1alpha1.PodMigrationJob) map[*v1alpha1.PodMigrationJob]*corev1.Pod {
	podOfJob := map[*v1alpha1.PodMigrationJob]*corev1.Pod{}
	for _, job := range jobs {
		pod := &corev1.Pod{}
		if job.Spec.PodRef == nil {
			klog.Infof("the podRef of job %v is nil", job.Name)
			continue
		}
		nn := types.NamespacedName{
			Namespace: job.Spec.PodRef.Namespace,
			Name:      job.Spec.PodRef.Name,
		}
		err := c.Get(context.TODO(), nn, pod)
		if err != nil {
			klog.Infof("failed to get Pod of PodMigrationJob %v, err: %v", nn, err)
			continue
		}
		podOfJob[job] = pod
	}
	return podOfJob
}
