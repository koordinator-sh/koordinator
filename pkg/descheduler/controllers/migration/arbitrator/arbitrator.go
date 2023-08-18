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
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/controllerfinder"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/utils/sorter"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sort"
	"sync"
	"time"
)

const (
	DefaultArbitrationInterval int = 500
)

var enqueueLog = klog.Background().WithName("eventhandler").WithName("DefaultArbitrator")

type DefaultArbitrator struct {
	collection map[types.UID]*v1alpha1.PodMigrationJob
	queue      workqueue.RateLimitingInterface

	sorts        []SortFn
	groupFilters []GroupFilterFn

	client           client.Client
	mu               sync.Mutex
	controllerFinder controllerfinder.Interface
	podSorter        sorter.MultiSorter

	arbitrationInterval int

	// maxMigratingPerNamespace represents he maximum number of pods that can be migrating during migrate per namespace.
	// Configured by MigrationControllerArgs.MaxMigratingPerNamespace.
	maxMigratingPerNamespace int

	// maxMigratingPerNode represents he maximum number of pods that can be migrating during migrate per node.
	// Configured by MigrationControllerArgs.MaxMigratingPerNode.
	maxMigratingPerNode int

	// maxMigratingPerWorkload represents he maximum number of pods that can be migrating during migrate per workload.
	// Value can be an absolute number (ex: 5) or a percentage of desired pods (ex: 10%).
	// Configured by MigrationControllerArgs.MaxMigratingPerWorkload.
	maxMigratingPerWorkload intstr.IntOrString

	// maxUnavailablePerWorkload represents he maximum number of pods that can be unavailable during migrate per workload.
	// The unavailable state includes NotRunning/NotReady/Migrating/Evicting
	// Value can be an absolute number (ex: 5) or a percentage of desired pods (ex: 10%).
	// Configured by MigrationControllerArgs.MaxUnavailablePerWorkload.
	maxUnavailablePerWorkload intstr.IntOrString
}

// Sort stably sorts jobs, outputs the sorted results and corresponding ranking map.
func (a *DefaultArbitrator) Sort(jobs []*v1alpha1.PodMigrationJob) ([]*v1alpha1.PodMigrationJob, map[*v1alpha1.PodMigrationJob]int) {
	for _, sortFn := range a.sorts {
		jobs = sortFn(jobs)
	}
	rankOfJobs := map[*v1alpha1.PodMigrationJob]int{}
	for i, job := range jobs {
		rankOfJobs[job] = i
	}
	return jobs, rankOfJobs
}

// GroupFilter loop calls GroupFilterFn to perform group and filter operations on PodMigrationJobs.
func (a *DefaultArbitrator) GroupFilter(jobs []*v1alpha1.PodMigrationJob, rankMap map[*v1alpha1.PodMigrationJob]int) []*v1alpha1.PodMigrationJob {
	for _, groupFilter := range a.groupFilters {
		jobs = groupFilter(jobs)
		sort.Slice(jobs, func(i, j int) bool {
			return rankMap[jobs[i]] < rankMap[jobs[j]]
		})
	}
	return jobs
}

// Select some jobs to place into the queue and remove them from the waiting collection.
func (a *DefaultArbitrator) Select(jobs []*v1alpha1.PodMigrationJob) []*v1alpha1.PodMigrationJob {
	if a.queue != nil {
		a.mu.Lock()
		for _, job := range jobs {
			// place jobs into the queue
			a.queue.Add(job)

			// remove job from the waiting collection
			delete(a.collection, job.UID)

			// mark job
			markJobPassedArbitration(job)

		}
		a.mu.Unlock()

		// update job
		for _, job := range jobs {
			err := a.client.Update(context.TODO(), job)
			if err != nil {
				klog.Errorf("failed to update job %v, err: %v", job.Namespace+"/"+job.Name, err)
			}
		}
	} else {
		klog.Warningf("work queue is nil")
	}
	return jobs
}

// AddJob add a PodMigrationJob to the waiting collection of Arbitrator.
func (a *DefaultArbitrator) AddJob(job *v1alpha1.PodMigrationJob) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.collection == nil {
		klog.Errorf("Waiting collection of Arbitrator is nil")
	}
	a.collection[job.UID] = job.DeepCopy()
}

// doOnceArbitrate performs an arbitrator operation on PodMigrationJobs in the waiting collection.
func (a *DefaultArbitrator) doOnceArbitrate() {
	// copy jobs from waiting collection
	a.mu.Lock()
	jobs := make([]*v1alpha1.PodMigrationJob, len(a.collection))
	i := 0
	for _, job := range a.collection {
		jobs[i] = job
		i++
	}
	a.mu.Unlock()

	// Sort
	jobs, rankOfJobs := a.Sort(jobs)

	// GroupFilter
	pastJobs := a.GroupFilter(jobs, rankOfJobs)

	// Select
	a.Select(pastJobs)
}

// Arbitrate the goroutine to arbitrate jobs periodically.
func (a *DefaultArbitrator) Arbitrate(stopCh <-chan struct{}) {
	klog.Infof("Start Arbitrator Arbitrate Goroutine")
	for {
		a.doOnceArbitrate()
		select {
		case <-stopCh:
			return
		case <-time.After(time.Duration(a.arbitrationInterval) * time.Second):
		}
	}
}

// NewArbitration creates an DefaultArbitrator structure based on parameters.
func NewArbitration(config *config.MigrationControllerArgs, finder controllerfinder.Interface, c client.Client) (Arbitrator, error) {
	arbitrationInterval := DefaultArbitrationInterval
	maxMigratingPerNamespace := 0
	maxMigratingPerNode := 0
	maxMigratingPerWorkload := intstr.IntOrString{}
	maxUnavailablePerWorkload := intstr.IntOrString{}
	skipCheckExpectedReplicas := false

	if args := config.ArbitrationArgs; args != nil {
		if args.Interval != 0 {
			arbitrationInterval = args.Interval
		}
	}
	if config.MaxMigratingPerNamespace != nil {
		maxMigratingPerNamespace = int(*config.MaxMigratingPerNamespace)
	}
	if config.MaxMigratingPerNode != nil {
		maxMigratingPerNode = int(*config.MaxMigratingPerNode)
	}
	if config.MaxMigratingPerWorkload != nil {
		maxMigratingPerWorkload = *config.MaxMigratingPerWorkload
	}
	if config.MaxUnavailablePerWorkload != nil {
		maxUnavailablePerWorkload = *config.MaxUnavailablePerWorkload
	}
	if config.SkipCheckExpectedReplicas != nil {
		skipCheckExpectedReplicas = *config.SkipCheckExpectedReplicas
	}

	return &DefaultArbitrator{
		client:              c,
		queue:               nil,
		mu:                  sync.Mutex{},
		collection:          map[types.UID]*v1alpha1.PodMigrationJob{},
		controllerFinder:    finder,
		arbitrationInterval: arbitrationInterval,

		sorts: []SortFn{
			NewPodSortFn(c, sorter.PodSorter()),
			NewDisperseByWorkloadSortFn(c),
			NewJobGatherSortFn(c),
			NewJobMigratingSortFn(c),
		},
		groupFilters: []GroupFilterFn{
			NewNamespaceGroupFilter(c, maxMigratingPerNamespace),
			NewNodeGroupFilter(c, maxMigratingPerNode),
			NewWorkloadGroupFilter(c, finder, maxMigratingPerWorkload, maxUnavailablePerWorkload, skipCheckExpectedReplicas),
		},
	}, nil
}

// Create implements EventHandler.
func (a *DefaultArbitrator) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	if evt.Object == nil {
		klog.Error(nil, "CreateEvent received with no metadata", "event", evt)
		return
	}
	if a.queue == nil {
		a.queue = q
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
	if a.queue == nil {
		a.queue = q
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
	if a.queue == nil {
		a.queue = q
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
	if a.queue == nil {
		a.queue = q
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

func markJobPassedArbitration(job *v1alpha1.PodMigrationJob) {
	if job.Annotations == nil {
		job.Annotations = make(map[string]string)
	}
	job.Annotations[AnnotationPassedArbitration] = "true"
}
