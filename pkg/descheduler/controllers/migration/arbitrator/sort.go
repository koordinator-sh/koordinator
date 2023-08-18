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
	"container/heap"
	"context"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/utils/sorter"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"math"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sort"
)

const (
	JobKind         = "Job"
	ReplicaSetKind  = "ReplicaSet"
	StatefulSetKind = "StatefulSet"
)

// NewPodSortFn sort a SortFn that sorts PodMigrationJobs by their Pods, including priority, QoS.
func NewPodSortFn(c client.Client, sorter *sorter.MultiSorter) SortFn {
	return func(jobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		var pods []*corev1.Pod
		jobOfPod := map[*corev1.Pod]*schedulingv1alpha1.PodMigrationJob{}
		rankOfJob := map[*schedulingv1alpha1.PodMigrationJob]int{}
		for _, job := range jobs {
			pod := &corev1.Pod{}
			err := c.Get(context.TODO(), types.NamespacedName{
				Namespace: job.GetNamespace(),
				Name:      job.GetName(),
			}, pod)
			if err != nil {
				rankOfJob[job] = math.MaxInt
				continue
			}
			pods = append(pods, pod)
			jobOfPod[pod] = job
		}

		// using PodSorter to sort
		sorter.Sort(pods)

		for i, pod := range pods {
			rankOfJob[jobOfPod[pod]] = i
		}

		sort.SliceStable(jobs, func(i, j int) bool {
			return rankOfJob[jobs[i]] < rankOfJob[jobs[j]]
		})
		return jobs
	}
}

// NewTimeSortFn returns a SortFn that stably sorts PodMigrationJobs by create time.
func NewTimeSortFn() SortFn {
	return func(jobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		// calculate the time interval between the creation of migration job and the current for each job.
		timeMap := map[*schedulingv1alpha1.PodMigrationJob]int64{}
		for _, job := range jobs {
			timeMap[job] = job.GetCreationTimestamp().Unix()
		}
		// perform stable sorting.
		sort.SliceStable(jobs, func(i, j int) bool {
			return timeMap[jobs[i]] > timeMap[jobs[j]]
		})
		return jobs
	}
}

// NewDisperseByWorkloadSortFn returns a SortFn that disperses jobs by workload.
func NewDisperseByWorkloadSortFn(c client.Client) SortFn {
	return func(jobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		jobsOfWorkloads := map[types.UID][]*schedulingv1alpha1.PodMigrationJob{}
		var ranks []int
		rankOfJobs := map[*schedulingv1alpha1.PodMigrationJob]int{}
		// group jobs by workload
		for i, job := range jobs {
			uid, ok := getWorkloadUIDForPodMigrationJob(job, c)
			if ok {
				ranks = append(ranks, i)
				rankOfJobs[job] = i
				if workloadJobs, ok := jobsOfWorkloads[uid]; ok {
					jobsOfWorkloads[uid] = append(workloadJobs, job)
				} else {
					jobsOfWorkloads[uid] = []*schedulingv1alpha1.PodMigrationJob{job}
				}
			}
		}
		// disperse jobs
		disperseJobs := make([]*disperseItem, len(ranks))
		i := 0
		for _, jobs := range jobsOfWorkloads {
			for k, job := range jobs {
				disperseJobs[i] = &disperseItem{
					value: float64(k+1) / float64(len(jobs)),
					rank:  rankOfJobs[job],
					job:   job,
				}
				i++
			}
		}

		h := dispersePQ(disperseJobs)
		heap.Init(&h)
		for _, index := range ranks {
			// put the job in the heap to the queue
			x := h[0].job
			heap.Pop(&h)
			jobs[index] = x
		}
		return jobs
	}
}

// NewJobMigratingSortFn returns a SortFn that .
func NewJobMigratingSortFn(c client.Client) SortFn {
	return func(podMigrationJobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		// get all the PodMigrationJobs
		podMigrationJobList := &schedulingv1alpha1.PodMigrationJobList{}
		err := c.List(context.TODO(), podMigrationJobList)
		if err != nil {
			klog.Infof("fail to list PodMigrationJobs %v", err)
		}

		// get all migrating job UID
		migratingJobUID := map[types.UID]struct{}{}
		for _, job := range podMigrationJobList.Items {
			if isJobMigrating(&job) {
				owner, ok := getJobControllerOfMigrationJob(&job, c)
				if !ok {
					continue
				}
				migratingJobUID[owner.UID] = struct{}{}
			}
		}

		// check jobs in arbitrator queue
		rankOfPodMigrationJobs := map[*schedulingv1alpha1.PodMigrationJob]int{}
		for i, job := range podMigrationJobs {
			owner, ok := getJobControllerOfMigrationJob(job, c)
			if !ok {
				rankOfPodMigrationJobs[job] = 0
				continue
			}
			if _, ok := migratingJobUID[owner.UID]; ok {
				rankOfPodMigrationJobs[job] = 0
			} else {
				rankOfPodMigrationJobs[job] = i
			}
		}

		sort.SliceStable(podMigrationJobs, func(i, j int) bool {
			return rankOfPodMigrationJobs[podMigrationJobs[i]] < rankOfPodMigrationJobs[podMigrationJobs[j]]
		})
		return podMigrationJobs
	}
}

// NewJobGatherSortFn returns a SortFn that places PodMigrationJobs in the same job in adjacent positions.
func NewJobGatherSortFn(c client.Client) SortFn {
	return func(podMigrationJobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		highestRankOfJob := map[types.UID]int{}
		rankOfPodMigrationJobs := map[*schedulingv1alpha1.PodMigrationJob]int{}
		for i, job := range podMigrationJobs {
			owner, ok := getJobControllerOfMigrationJob(job, c)
			if !ok {
				rankOfPodMigrationJobs[job] = i
				continue
			}
			if rank, ok := highestRankOfJob[owner.UID]; ok {
				rankOfPodMigrationJobs[job] = rank
			} else {
				highestRankOfJob[owner.UID] = i
				rankOfPodMigrationJobs[job] = i
			}
		}
		sort.SliceStable(podMigrationJobs, func(i, j int) bool {
			return rankOfPodMigrationJobs[podMigrationJobs[i]] < rankOfPodMigrationJobs[podMigrationJobs[j]]
		})
		return podMigrationJobs
	}
}

func getJobControllerOfMigrationJob(job *schedulingv1alpha1.PodMigrationJob, c client.Client) (*metav1.OwnerReference, bool) {
	owner, ok := getControllerOfMigrationJob(job, c)
	if !ok {
		return owner, false
	}
	if owner.Kind != JobKind {
		return nil, false
	}
	return owner, true
}

func getControllerOfMigrationJob(job *schedulingv1alpha1.PodMigrationJob, c client.Client) (*metav1.OwnerReference, bool) {
	if job.Spec.PodRef == nil {
		klog.Infof("the PodRef of job %v is nil", job.GetNamespace()+"/"+job.GetName())
		return nil, false
	}
	nn := types.NamespacedName{
		Namespace: job.Spec.PodRef.Namespace,
		Name:      job.Spec.PodRef.Name,
	}
	pod := &corev1.Pod{}
	err := c.Get(context.TODO(), nn, pod)
	if err != nil {
		klog.Infof("fail to get pod %v", nn)
		return nil, false
	}

	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return owner, false
	}
	return owner, true
}

func getWorkloadUIDForPodMigrationJob(job *schedulingv1alpha1.PodMigrationJob, c client.Client) (types.UID, bool) {
	owner, ok := getControllerOfMigrationJob(job, c)
	if !ok {
		return "", false
	}
	switch owner.Kind {
	case ReplicaSetKind:
		return owner.UID, true
	case StatefulSetKind:
		return owner.UID, true
	}
	return "", false
}

type disperseItem struct {
	value float64
	rank  int
	job   *schedulingv1alpha1.PodMigrationJob
}

type dispersePQ []*disperseItem

func (d *dispersePQ) Len() int {
	return len(*d)
}

func (d *dispersePQ) Less(i, j int) bool {
	if math.Abs((*d)[i].value-(*d)[j].value) <= 1e-8 {
		return (*d)[i].rank < (*d)[j].rank
	} else {
		return (*d)[i].value < (*d)[j].value
	}
}

func (d *dispersePQ) Swap(i, j int) {
	(*d)[i], (*d)[j] = (*d)[j], (*d)[i]
}

func (d *dispersePQ) Push(x any) {
	*d = append(*d, x.(*disperseItem))
}

func (d *dispersePQ) Pop() any {
	x := (*d)[len(*d)-1]
	*d = (*d)[:len(*d)-1]
	return x
}
