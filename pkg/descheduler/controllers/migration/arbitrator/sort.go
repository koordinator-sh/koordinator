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

package arbitrator

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/fieldindex"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/utils/sorter"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

const (
	JobKind = "Job"
)

// CompareFn compares p1 and p2 and returns:
//
//   -1 if p1 <  p2
//    0 if p1 == p2
//   +1 if p1 >  p2
//

// MultiSorter implements the Sort interface
type MultiSorter struct {
	ascending        bool
	podMigrationJobs []*v1alpha1.PodMigrationJob
	cmp              []CompareFn
}

// Sort sorts the podMigrationJobs according to the cmp functions passed to OrderedBy.
func (ms *MultiSorter) Sort(podMigrationJobs []*v1alpha1.PodMigrationJob) {
	ms.podMigrationJobs = podMigrationJobs
	sort.Stable(ms)
}

// OrderedBy returns a Sorter sorted using the cmp functions, sorts in ascending order by default
func OrderedBy(cmp ...CompareFn) *MultiSorter {
	return &MultiSorter{
		ascending: true,
		cmp:       cmp,
	}
}

func (ms *MultiSorter) Ascending() *MultiSorter {
	ms.ascending = true
	return ms
}

func (ms *MultiSorter) Descending() *MultiSorter {
	ms.ascending = false
	return ms
}

// Len is part of sort.Interface.
func (ms *MultiSorter) Len() int {
	return len(ms.podMigrationJobs)
}

// Swap is part of sort.Interface.
func (ms *MultiSorter) Swap(i, j int) {
	ms.podMigrationJobs[i], ms.podMigrationJobs[j] = ms.podMigrationJobs[j], ms.podMigrationJobs[i]
}

// Less is part of sort.Interface.
func (ms *MultiSorter) Less(i, j int) bool {
	p1, p2 := ms.podMigrationJobs[i], ms.podMigrationJobs[j]
	var k int
	for k = 0; k < len(ms.cmp)-1; k++ {
		cmpResult := ms.cmp[k](p1, p2)
		// p1 is less than p2
		if cmpResult < 0 {
			return ms.ascending
		}
		// p1 is greater than p2
		if cmpResult > 0 {
			return !ms.ascending
		}
	}
	cmpResult := ms.cmp[k](p1, p2)
	if cmpResult < 0 {
		return ms.ascending
	}
	return !ms.ascending
}

// SortJobsByPod returns a SortFn that sorts PodMigrationJobs by their Pods, including priority, QoS.
func SortJobsByPod(podSorters []sorter.CompareFn) SortFn {
	return func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) CompareFn {
		return func(p1, p2 *v1alpha1.PodMigrationJob) int {
			for _, podSorter := range podSorters {
				cmpResult := podSorter(podOfJob[p1], podOfJob[p2])
				if cmpResult < 0 {
					return -1
				}
				if cmpResult > 0 {
					return 1
				}
			}
			return 0
		}
	}
}

// SortJobsByCreationTime returns a SortFn that sorts PodMigrationJobs by create time.
func SortJobsByCreationTime() SortFn {
	return func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) CompareFn {
		return func(p1, p2 *v1alpha1.PodMigrationJob) int {
			if p1.CreationTimestamp.Equal(&p2.CreationTimestamp) {
				return 0
			}
			if p1.CreationTimestamp.Before(&p2.CreationTimestamp) {
				return 1
			}
			return -1
		}
	}
}

// SortJobsByMigratingNum returns a SortFn that sorts PodMigrationJobs by the number of migrating PMJs in the same Job,
// if equal, by the owner's uid.
func SortJobsByMigratingNum(c client.Client) SortFn {
	return func(jobs []*v1alpha1.PodMigrationJob, podOfJob map[*v1alpha1.PodMigrationJob]*corev1.Pod) CompareFn {
		// get owner of jobs
		migratingPMJNum := map[*v1alpha1.PodMigrationJob]int{}
		migratingPMJNumOfOwner := map[types.UID]int{}
		podMigrationJobOwner := map[*v1alpha1.PodMigrationJob]types.UID{}

		for _, job := range jobs {
			owner, ok := getJobControllerOfPod(podOfJob[job])
			if !ok {
				podMigrationJobOwner[job] = ""
				continue
			}
			podMigrationJobOwner[job] = owner.UID
			if num, ok := migratingPMJNumOfOwner[owner.UID]; ok {
				migratingPMJNum[job] = num
			} else {
				num = getMigratingJobNum(c, owner.UID)
				migratingPMJNum[job] = num
				migratingPMJNumOfOwner[owner.UID] = num
			}
		}

		return func(p1, p2 *v1alpha1.PodMigrationJob) int {
			// sort by migrating PMJ num
			if migratingPMJNum[p1] > migratingPMJNum[p2] {
				return -1
			}
			if migratingPMJNum[p1] < migratingPMJNum[p2] {
				return 1
			}
			// sort by PMJ owner uid
			if podMigrationJobOwner[p1] < podMigrationJobOwner[p2] {
				return -1
			}
			if podMigrationJobOwner[p1] > podMigrationJobOwner[p2] {
				return 1
			}
			return 0
		}
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
