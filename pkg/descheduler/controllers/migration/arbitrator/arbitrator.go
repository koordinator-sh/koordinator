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
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/utils/sorter"
)

const (
	AnnotationPassedArbitration = "descheduler.koordinator.sh/passed-arbitration"
)

var enqueueLog = klog.Background().WithName("eventHandler").WithName("arbitratorImpl")

type Arbitrator interface {
	Add(job *v1alpha1.PodMigrationJob)
}

// SortFn stably sorts PodMigrationJobs slice based on a certain strategy. Users
// can implement different SortFn according to their needs.
type SortFn func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob

type arbitratorImpl struct {
	waitingCollection map[types.UID]*v1alpha1.PodMigrationJob
	interval          time.Duration

	sorts                 []SortFn
	nonRetryablePodFilter framework.FilterFunc
	retryablePodFilter    framework.FilterFunc

	client        client.Client
	eventRecorder events.EventRecorder
	mu            sync.Mutex
}

// New creates an arbitratorImpl based on parameters.
func New(args *config.ArbitrationArgs, options Options) (Arbitrator, error) {
	arbitrator := &arbitratorImpl{
		waitingCollection: map[types.UID]*v1alpha1.PodMigrationJob{},
		interval:          args.Interval.Duration,

		sorts: []SortFn{
			SortJobsByCreationTime(),
			SortJobsByPod(sorter.PodSorter().Sort),
			SortJobsByController(),
			SortJobsByMigratingNum(options.Client),
		},
		retryablePodFilter:    options.RetryableFilter,
		nonRetryablePodFilter: options.NonRetryableFilter,

		client:        options.Client,
		eventRecorder: options.EventRecorder,
		mu:            sync.Mutex{},
	}

	err := options.Manager.Add(arbitrator)
	if err != nil {
		return nil, err
	}
	return arbitrator, nil
}

// Add adds a PodMigrationJob to the waitingCollection of arbitratorImpl.
// It is safe to be called concurrently by multiple goroutines.
func (a *arbitratorImpl) Add(job *v1alpha1.PodMigrationJob) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.waitingCollection[job.UID] = job.DeepCopy()
}

// Start starts the goroutine to arbitrate jobs periodically.
func (a *arbitratorImpl) Start(ctx context.Context) error {
	klog.InfoS("Start Arbitrator Arbitrate Goroutine")
	wait.Until(a.doOnceArbitrate, a.interval, ctx.Done())
	return nil
}

// sort stably sorts jobs, outputs the sorted results and corresponding ranking map.
func (a *arbitratorImpl) sort(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
	for _, sortFn := range a.sorts {
		jobs = sortFn(jobs, podOfJob)
	}
	return jobs
}

// filter calls nonRetryablePodFilter and retryablePodFilter to filter one PodMigrationJob.
func (a *arbitratorImpl) filter(pod *corev1.Pod) (isFailed, isPassed bool) {
	if pod != nil {
		if a.nonRetryablePodFilter != nil && !a.nonRetryablePodFilter(pod) {
			isFailed = true
			return
		}
		if a.retryablePodFilter != nil && !a.retryablePodFilter(pod) {
			isPassed = false
			return
		}
	}
	isPassed = true
	return
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
		klog.ErrorS(err, "failed to update job", "job", klog.KObj(job))
	} else {
		// remove job from the waitingCollection
		a.mu.Lock()
		delete(a.waitingCollection, job.UID)
		a.mu.Unlock()
	}
}

// doOnceArbitrate performs an arbitrate operation on PodMigrationJobs in the waitingCollection.
func (a *arbitratorImpl) doOnceArbitrate() {
	// copy jobs from waitingCollection
	jobs := a.copyJobs()
	if len(jobs) == 0 {
		return
	}

	podOfJob := getPodForJob(a.client, jobs)

	// sort
	jobs = a.sort(jobs, podOfJob)

	// filter
	for _, job := range jobs {
		pod := podOfJob[job]
		isFailed, isPassed := a.filter(pod)
		if isFailed {
			a.updateFailedJob(job, pod)
			continue
		}
		if isPassed {
			a.updatePassedJob(job)
		}
	}
}

// copyJobs copy jobs from waitingCollection
func (a *arbitratorImpl) copyJobs() []*v1alpha1.PodMigrationJob {
	a.mu.Lock()
	defer a.mu.Unlock()
	jobs := make([]*v1alpha1.PodMigrationJob, 0, len(a.waitingCollection))
	for _, job := range a.waitingCollection {
		jobs = append(jobs, job)
	}
	return jobs
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

// arbitrationHandler implement handler.EventHandler
type arbitrationHandler struct {
	handler.EnqueueRequestForObject
	c          client.Client
	arbitrator Arbitrator
}

func NewHandler(arbitrator Arbitrator, c client.Client) handler.EventHandler {
	return &arbitrationHandler{
		EnqueueRequestForObject: handler.EnqueueRequestForObject{},
		arbitrator:              arbitrator,
		c:                       c,
	}
}

// Create call Arbitrator.Create
func (h *arbitrationHandler) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	if evt.Object == nil {
		enqueueLog.Error(nil, "CreateEvent received with no metadata", "event", evt)
		return
	}
	// get job
	job := &v1alpha1.PodMigrationJob{}
	err := h.c.Get(context.TODO(), types.NamespacedName{
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
	h.arbitrator.Add(job)
}

type Options struct {
	Client             client.Client
	EventRecorder      events.EventRecorder
	RetryableFilter    framework.FilterFunc
	NonRetryableFilter framework.FilterFunc
	Manager            controllerruntime.Manager
}

func getPodForJob(c client.Client, jobs []*v1alpha1.PodMigrationJob) map[*v1alpha1.PodMigrationJob]*corev1.Pod {
	podOfJob := map[*v1alpha1.PodMigrationJob]*corev1.Pod{}
	for _, job := range jobs {
		pod := &corev1.Pod{}
		if job.Spec.PodRef == nil {
			klog.V(4).InfoS("the podRef is nil", "job", klog.KObj(job))
			continue
		}
		nn := types.NamespacedName{
			Namespace: job.Spec.PodRef.Namespace,
			Name:      job.Spec.PodRef.Name,
		}
		err := c.Get(context.TODO(), nn, pod)
		if err != nil {
			klog.ErrorS(err, "failed to get Pod of PodMigrationJob", "PodMigrationJob", klog.KObj(job))
			continue
		}
		podOfJob[job] = pod
	}
	return podOfJob
}
