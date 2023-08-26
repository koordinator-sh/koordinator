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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
)

const (
	AnnotationPassedArbitration = "descheduler.koordinator.sh/passed-arbitration"
)

var enqueueLog = klog.Background().WithName("eventHandler").WithName("arbitratorImpl")

type arbitratorImpl struct {
	waitingCollection map[types.UID]*v1alpha1.PodMigrationJob
	workQueue         workqueue.RateLimitingInterface
	interval          time.Duration

	sorts                 []SortFn
	nonRetryablePodFilter framework.FilterFunc
	retryablePodFilter    framework.FilterFunc

	client        client.Client
	eventRecorder events.EventRecorder
	mu            sync.Mutex
}

// Add adds a PodMigrationJob to the waitingCollection of arbitratorImpl.
// It is safe to be called concurrently by multiple goroutines.
func (a *arbitratorImpl) Add(job *v1alpha1.PodMigrationJob) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.waitingCollection == nil {
		klog.Errorf("waitingCollection is nil")
	}
	a.waitingCollection[job.UID] = job.DeepCopy()
}

// Arbitrate starts the goroutine to arbitrate jobs periodically.
func (a *arbitratorImpl) Arbitrate(stopCh <-chan struct{}) {
	klog.Infof("Start Arbitrator Arbitrate Goroutine")
	for {
		a.doOnceArbitrate()
		select {
		case <-stopCh:
			return
		case <-time.After(a.interval):
		}
	}
}

func (a *arbitratorImpl) WithSortFn(sort SortFn) {
	a.sorts = append(a.sorts, sort)
}

// sort stably sorts jobs, outputs the sorted results and corresponding ranking map.
func (a *arbitratorImpl) sort(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
	for _, sortFn := range a.sorts {
		jobs = sortFn(jobs, podOfJob)
	}
	return jobs
}

// filter calls nonRetryablePodFilter and retryablePodFilter to filter operations on PodMigrationJobs.
func (a *arbitratorImpl) filter(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) {
	for _, job := range jobs {
		pod := podOfJob[job]
		if pod != nil {
			if a.nonRetryablePodFilter != nil && !a.nonRetryablePodFilter(pod) {
				a.updateFailedJob(job, pod)
				continue
			}
			if a.retryablePodFilter != nil && !a.retryablePodFilter(pod) {
				continue
			}
		}
		a.updatePassedJob(job)
	}
}

// updatePassedJob does something after PodMigrationJob passed the filter.
func (a *arbitratorImpl) updatePassedJob(job *v1alpha1.PodMigrationJob) {
	// add annotation AnnotationPassedArbitration
	if job.Annotations == nil {
		job.Annotations = map[string]string{}
	}
	job.Annotations[AnnotationPassedArbitration] = "true"
	err := a.client.Update(context.TODO(), job)
	if err != nil {
		klog.Errorf("failed to update job %v, err: %v", fmt.Sprintf("%v/%v", job.Namespace, job.Name), err)
	}

	if a.workQueue != nil {
		// add job into the workQueue
		a.workQueue.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      job.GetName(),
			Namespace: job.GetNamespace(),
		}})

		// remove job from the waiting waitingCollection
		a.mu.Lock()
		delete(a.waitingCollection, job.UID)
		a.mu.Unlock()
	} else {
		klog.Errorf("workQueue is nil")
	}
}

// doOnceArbitrate performs an arbitrate operation on PodMigrationJobs in the waitingCollection.
func (a *arbitratorImpl) doOnceArbitrate() {
	// copy jobs from waitingCollection
	a.mu.Lock()
	jobs := make([]*v1alpha1.PodMigrationJob, len(a.waitingCollection))
	i := 0
	for _, job := range a.waitingCollection {
		jobs[i] = job
		i++
	}
	a.mu.Unlock()

	if len(jobs) == 0 {
		return
	}

	podOfJob := getPodForJob(a.client, jobs)

	// sort
	jobs = a.sort(jobs, podOfJob)

	// filter
	a.filter(jobs, podOfJob)
}

func (a *arbitratorImpl) updateFailedJob(job *v1alpha1.PodMigrationJob, pod *corev1.Pod) {
	// change phase to Failed
	job.Status.Phase = v1alpha1.PodMigrationJobFailed
	job.Status.Reason = v1alpha1.PodMigrationJobReasonForbiddenMigratePod
	job.Status.Message = fmt.Sprintf("Pod %q is forbidden to migrate because it does not meet the requirements", klog.KObj(pod))
	err := a.client.Status().Update(context.TODO(), job)
	if err == nil {
		a.eventRecorder.Eventf(job, nil, corev1.EventTypeWarning, v1alpha1.PodMigrationJobReasonForbiddenMigratePod, "Migrating", job.Status.Message)
	}

	// delete from waitingCollection
	a.mu.Lock()
	delete(a.waitingCollection, job.UID)
	a.mu.Unlock()
}

// New creates an arbitratorImpl based on parameters.
func New(args *config.ArbitrationArgs, c client.Client, eventRecorder events.EventRecorder, retryableFilter framework.FilterFunc, nonRetryableFilter framework.FilterFunc) (Arbitrator, bool) {
	if args.Enabled == false {
		return nil, false
	}

	return &arbitratorImpl{
		waitingCollection: map[types.UID]*v1alpha1.PodMigrationJob{},
		workQueue:         nil,
		interval:          args.Interval.Duration,

		sorts:                 []SortFn{},
		retryablePodFilter:    retryableFilter,
		nonRetryablePodFilter: nonRetryableFilter,

		client:        c,
		eventRecorder: eventRecorder,
		mu:            sync.Mutex{},
	}, true
}

// Create implements EventHandler.
func (a *arbitratorImpl) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	if evt.Object == nil {
		enqueueLog.Error(nil, "CreateEvent received with no metadata", "event", evt)
		return
	}
	if a.workQueue == nil {
		a.workQueue = q
	}
	// get job
	job := &v1alpha1.PodMigrationJob{}
	err := a.client.Get(context.TODO(), types.NamespacedName{
		Name:      evt.Object.GetName(),
		Namespace: evt.Object.GetNamespace(),
	}, job)
	if err != nil {
		// if err, add job to the workQueue directly.
		enqueueLog.Error(nil, "Fail to get PodMigrationJob", "PodMigrationJob", types.NamespacedName{
			Name:      evt.Object.GetName(),
			Namespace: evt.Object.GetNamespace(),
		})
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      evt.Object.GetName(),
			Namespace: evt.Object.GetNamespace(),
		}})
		return
	}
	a.Add(job)
}

// Update implements EventHandler.
func (a *arbitratorImpl) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
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
func (a *arbitratorImpl) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
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
func (a *arbitratorImpl) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	if evt.Object == nil {
		enqueueLog.Error(nil, "GenericEvent received with no metadata", "event", evt)
		return
	}
	q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      evt.Object.GetName(),
		Namespace: evt.Object.GetNamespace(),
	}})
}

func getPodForJob(c client.Client, jobs []*v1alpha1.PodMigrationJob) map[*v1alpha1.PodMigrationJob]*corev1.Pod {
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
