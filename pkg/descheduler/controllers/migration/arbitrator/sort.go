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
	"math"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/fieldindex"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

const (
	JobKind = "Job"
)

// SortJobsByPod sort a SortFn that sorts PodMigrationJobs by their Pods, including priority, QoS.
func SortJobsByPod(sorter func(pods []*corev1.Pod)) SortFn {
	return func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
		var pods []*corev1.Pod
		jobOfPod := map[*corev1.Pod]*v1alpha1.PodMigrationJob{}
		rankOfJob := map[*v1alpha1.PodMigrationJob]int{}
		for _, job := range jobs {
			pod := podOfJob[job]
			if pod == nil {
				rankOfJob[job] = math.MinInt
				continue
			}
			pods = append(pods, pod)
			jobOfPod[pod] = job
		}

		// using PodSorter to sort
		sorter(pods)

		for i, pod := range pods {
			rankOfJob[jobOfPod[pod]] = i
		}

		sort.SliceStable(jobs, func(i, j int) bool {
			return rankOfJob[jobs[i]] < rankOfJob[jobs[j]]
		})
		return jobs
	}
}

// SortJobsByCreationTime returns a SortFn that stably sorts PodMigrationJobs by create time.
func SortJobsByCreationTime() SortFn {
	return func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
		sort.SliceStable(jobs, func(i, j int) bool {
			return jobs[i].GetCreationTimestamp().Unix() > jobs[j].GetCreationTimestamp().Unix()
		})
		return jobs
	}
}

// SortJobsByMigratingNum returns a SortFn that stably sorts PodMigrationJobs the number of migrating PMJs in the same Job.
func SortJobsByMigratingNum(c client.Client) SortFn {
	return func(podMigrationJobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
		// get owner of jobs
		rankOfPodMigrationJobs := map[*v1alpha1.PodMigrationJob]int{}
		migratingJobNumOfOwners := map[types.UID]int{}
		for _, job := range podMigrationJobs {
			owner, ok := getJobControllerOfPod(podOfJob[job])
			if !ok {
				continue
			}
			if num, ok := migratingJobNumOfOwners[owner.UID]; ok {
				rankOfPodMigrationJobs[job] = num
			} else {
				num = getMigratingJobNum(c, owner.UID)
				rankOfPodMigrationJobs[job] = num
				migratingJobNumOfOwners[owner.UID] = num
			}
		}

		sort.SliceStable(podMigrationJobs, func(i, j int) bool {
			return rankOfPodMigrationJobs[podMigrationJobs[i]] > rankOfPodMigrationJobs[podMigrationJobs[j]]
		})
		return podMigrationJobs
	}
}

// SortJobsByController returns a SortFn that places PodMigrationJobs in the same job in adjacent positions.
func SortJobsByController() SortFn {
	return func(podMigrationJobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) []*v1alpha1.PodMigrationJob {
		highestRankOfJob := map[types.UID]int{}
		rankOfPodMigrationJobs := map[*v1alpha1.PodMigrationJob]int{}
		for i, job := range podMigrationJobs {
			owner, ok := getJobControllerOfPod(podOfJob[job])
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

func getMigratingJobNum(c client.Client, ownerUID types.UID) int {
	opts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexPodByOwnerRefUID, string(ownerUID))}
	podList := &corev1.PodList{}
	err := c.List(context.TODO(), podList, opts, utilclient.DisableDeepCopy)
	if err != nil {
		klog.ErrorS(err, "failed to list pods by IndexPodByOwnerRefUID", "OwnerRefUID", ownerUID)
		return 0
	}
	cnt := 0
	for _, pod := range podList.Items {
		opts = &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexJobByPodUID, string(pod.UID))}
		jobList := &v1alpha1.PodMigrationJobList{}
		err = c.List(context.TODO(), jobList, opts, utilclient.DisableDeepCopy)
		if err != nil {
			klog.ErrorS(err, "failed to list jobs by IndexJobByPodUID", "pod", klog.KObj(&pod))
			continue
		}
		for _, job := range jobList.Items {
			if isJobMigrating(&job) {
				cnt++
			}
		}
	}
	return cnt
}

func getJobControllerOfPod(pod *corev1.Pod) (*metav1.OwnerReference, bool) {
	if pod == nil {
		return nil, false
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return nil, false
	}
	if owner.Kind != JobKind {
		return nil, false
	}
	return owner, true
}

func isJobMigrating(job *v1alpha1.PodMigrationJob) bool {
	return job.Annotations[AnnotationPassedArbitration] == "true" || job.Status.Phase == v1alpha1.PodMigrationJobRunning
}
