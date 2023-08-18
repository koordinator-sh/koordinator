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
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/controllerfinder"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/util"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/fieldindex"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
	"github.com/storageos/go-api/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"
	kubecontroller "k8s.io/kubernetes/pkg/controller"
	"math"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	AnnotationPassedArbitration = "descheduler.koordinator.sh/passed-arbitrator"
)

// NewNodeGroupFilter returns a GroupFilterFn that groups and filters PodMigrationJobs by Node.
func NewNodeGroupFilter(c client.Client, maxPodMigrationJobNumPerNode int) GroupFilterFn {
	group := func(jobs []*schedulingv1alpha1.PodMigrationJob, podOfJob map[*schedulingv1alpha1.PodMigrationJob]*corev1.Pod) map[string][]*schedulingv1alpha1.PodMigrationJob {
		return groupJobs(jobs, func(job *schedulingv1alpha1.PodMigrationJob) string {
			pod := podOfJob[job]
			if pod == nil {
				return ""
			}
			return pod.Spec.NodeName
		})
	}

	filter := func(nodeName string, jobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		podList := &corev1.PodList{}
		listOpts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexPodByNodeName, nodeName)}
		err := c.List(context.TODO(), podList, listOpts, utilclient.DisableDeepCopy)
		if err != nil {
			klog.Infof("fail to list pods of node %v, err: %v", nodeName, err)
			return jobs
		}

		count := 0
		for i := range podList.Items {
			v := &podList.Items[i]
			if v.Spec.NodeName == nodeName {
				if existMigratingJob(c, v) {
					count++
				}
			}
		}

		passedNum := maxPodMigrationJobNumPerNode - count
		if passedNum < 0 {
			passedNum = 0
		}
		if passedNum < len(jobs) {
			jobs = jobs[:passedNum]
		}
		return jobs
	}

	return func(jobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		if maxPodMigrationJobNumPerNode <= 0 {
			return jobs
		}
		// get pod of jobs
		podOfJob := getPodOfJob(c, jobs)

		jobsOfNodes := group(jobs, podOfJob)
		var remainedJobs []*schedulingv1alpha1.PodMigrationJob
		for node, nodeJobs := range jobsOfNodes {
			if node == "" {
				// for empty node name
				remainedJobs = append(remainedJobs, nodeJobs...)
			} else {
				nodeJobs = filter(node, nodeJobs)
				remainedJobs = append(remainedJobs, nodeJobs...)
			}
		}
		return remainedJobs
	}
}

// NewNamespaceGroupFilter returns a GroupFilterFn that groups and filters PodMigrationJobs by Namespace.
func NewNamespaceGroupFilter(c client.Client, maxPodMigrationJobNum int) GroupFilterFn {
	group := func(jobs []*schedulingv1alpha1.PodMigrationJob, podOfJob map[*schedulingv1alpha1.PodMigrationJob]*corev1.Pod) map[string][]*schedulingv1alpha1.PodMigrationJob {
		return groupJobs(jobs, func(job *schedulingv1alpha1.PodMigrationJob) string {
			pod := podOfJob[job]
			if pod == nil {
				return ""
			}
			if pod.GetNamespace() == "" {
				return types.DefaultNamespace
			}
			return pod.GetNamespace()
		})
	}

	filter := func(namespace string, jobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		podList := &corev1.PodList{}
		listOpts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexJobByPodNamespace, namespace)}
		err := c.List(context.TODO(), podList, listOpts, utilclient.DisableDeepCopy)
		if err != nil {
			klog.Infof("fail to list pods of namespace %v, err: %v", namespace, err)
			return jobs
		}

		count := 0
		for i := range podList.Items {
			v := &podList.Items[i]
			if v.GetNamespace() == namespace {
				if existMigratingJob(c, v) {
					count++
				}
			}
		}

		passedNum := maxPodMigrationJobNum - count
		if passedNum < 0 {
			passedNum = 0
		}
		if passedNum < len(jobs) {
			jobs = jobs[:passedNum]
		}
		return jobs
	}

	return func(jobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		if maxPodMigrationJobNum <= 0 {
			return jobs
		}
		// get pod of jobs
		podOfJob := getPodOfJob(c, jobs)

		jobsOfNamespace := group(jobs, podOfJob)
		var groupFilteredJobs []*schedulingv1alpha1.PodMigrationJob
		for namespace, namespaceJobs := range jobsOfNamespace {
			if namespace == "" {
				// for empty node name
				groupFilteredJobs = append(groupFilteredJobs, namespaceJobs...)
			} else {
				namespaceJobs = filter(namespace, namespaceJobs)
				groupFilteredJobs = append(groupFilteredJobs, namespaceJobs...)
			}
		}
		return groupFilteredJobs
	}
}

// NewWorkloadGroupFilter returns a GroupFilterFn that groups and filters PodMigrationJobs by Workload.
func NewWorkloadGroupFilter(c client.Client, controllerFinder controllerfinder.Interface, maxMigratingPerWorkload intstr.IntOrString, maxUnavailablePerWorkload intstr.IntOrString, skipCheckExpectedReplicas bool) GroupFilterFn {
	group := func(jobs []*schedulingv1alpha1.PodMigrationJob, podOfJob map[*schedulingv1alpha1.PodMigrationJob]*corev1.Pod) (map[string][]*schedulingv1alpha1.PodMigrationJob, map[string]*metav1.OwnerReference) {
		jobsOfWorkload := map[string][]*schedulingv1alpha1.PodMigrationJob{}
		ownerOfUID := map[string]*metav1.OwnerReference{}

		jobsOfWorkload = groupJobs(jobs, func(job *schedulingv1alpha1.PodMigrationJob) string {
			pod := podOfJob[job]
			if pod == nil {
				return ""
			} else {
				ownerRef := metav1.GetControllerOf(pod)
				if ownerRef == nil {
					return ""
				} else {
					ownerOfUID[string(ownerRef.UID)] = ownerRef
					return string(ownerRef.UID)
				}
			}
		})
		return jobsOfWorkload, ownerOfUID
	}

	groupByNamespace := func(jobs []*schedulingv1alpha1.PodMigrationJob, podOfJob map[*schedulingv1alpha1.PodMigrationJob]*corev1.Pod) map[string][]*schedulingv1alpha1.PodMigrationJob {
		return groupJobs(jobs, func(job *schedulingv1alpha1.PodMigrationJob) string {
			pod := podOfJob[job]
			if pod == nil {
				return ""
			}
			return pod.GetNamespace()
		})
	}

	filter := func(owner *metav1.OwnerReference, ns string, jobs []*schedulingv1alpha1.PodMigrationJob, SkipCheckExpectedReplicas bool) []*schedulingv1alpha1.PodMigrationJob {
		pods, expectedReplicas, err := controllerFinder.GetPodsForRef(owner, ns, nil, false)
		if err != nil {
			return jobs
		}
		maxMigrating, err := util.GetMaxMigrating(int(expectedReplicas), &maxMigratingPerWorkload)
		if err != nil {
			return jobs
		}
		maxUnavailable, err := util.GetMaxUnavailable(int(expectedReplicas), &maxUnavailablePerWorkload)
		if err != nil {
			return jobs
		}

		if !SkipCheckExpectedReplicas {
			// TODO(joseph): There are a few special scenarios where should we allow eviction?
			if expectedReplicas == 1 || int(expectedReplicas) == maxMigrating || int(expectedReplicas) == maxUnavailable {
				return jobs
			}
		}

		opts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexJobByPodNamespace, ns)}
		migratingPods := map[apitypes.NamespacedName]struct{}{}
		forEachAvailableMigrationJobs(c, opts, func(job *schedulingv1alpha1.PodMigrationJob) bool {
			podRef := job.Spec.PodRef
			if !isJobMigrating(job) {
				return true
			}
			if podRef == nil {
				return true
			}

			podNamespacedName := apitypes.NamespacedName{
				Namespace: podRef.Namespace,
				Name:      podRef.Name,
			}
			p := &corev1.Pod{}
			err := c.Get(context.TODO(), podNamespacedName, p)
			if err != nil {
				klog.Errorf("Failed to get Pod %q, err: %v", podNamespacedName, err)
			} else {
				innerPodOwnerRef := metav1.GetControllerOf(p)
				if innerPodOwnerRef != nil && innerPodOwnerRef.UID == owner.UID {
					migratingPods[podNamespacedName] = struct{}{}
				}
			}
			return true
		})

		// filer maxMigrationJob
		remainJobNum := len(jobs)
		if maxMigrating-len(migratingPods) < remainJobNum {
			remainJobNum = maxMigrating - len(migratingPods)
		}

		// filer maxUnavailableJob
		unavailablePods := getUnavailablePods(pods)
		mergeUnavailableAndMigratingPods(unavailablePods, migratingPods)
		if maxUnavailable-len(unavailablePods) < remainJobNum {
			remainJobNum = maxUnavailable - len(unavailablePods)
		}
		if remainJobNum < 0 {
			remainJobNum = 0
		}

		if remainJobNum >= len(jobs) {
			return jobs
		}
		return jobs[:remainJobNum]
	}

	return func(jobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		// get pod of jobs
		podOfJobs := getPodOfJob(c, jobs)

		jobsOfWorkload, ownerOfUID := group(jobs, podOfJobs)

		var remainJobs []*schedulingv1alpha1.PodMigrationJob
		for uid, jobs := range jobsOfWorkload {
			owner := ownerOfUID[uid]
			if owner == nil {
				remainJobs = append(remainJobs, jobs...)
				continue
			}

			jobsOfNamespace := groupByNamespace(jobs, podOfJobs)
			for ns, nsJobs := range jobsOfNamespace {
				if ns == "" {
					remainJobs = append(remainJobs, nsJobs...)
					continue
				}
				nsJobs = filter(owner, ns, nsJobs, skipCheckExpectedReplicas)
				remainJobs = append(remainJobs, nsJobs...)
			}
		}
		return remainJobs
	}
}

// NewSingleJobGroupFilter returns a GroupFilterFn that groups PMJs by job and preserves the PMJs of the job with the highest priority.
func NewSingleJobGroupFilter(c client.Client) GroupFilterFn {
	group := func(jobs []*schedulingv1alpha1.PodMigrationJob) map[string][]*schedulingv1alpha1.PodMigrationJob {
		return groupJobs(jobs, func(job *schedulingv1alpha1.PodMigrationJob) string {
			owner, ok := getJobControllerOfMigrationJob(job, c)
			if !ok {
				return ""
			}
			return string(owner.UID)
		})
	}

	filter := func(podMigrationJobsOfJob map[string][]*schedulingv1alpha1.PodMigrationJob, rankOfPodMigrationJob map[*schedulingv1alpha1.PodMigrationJob]int) []*schedulingv1alpha1.PodMigrationJob {
		remainedJobUID, remainedRankAvg := "", float32(math.MaxFloat32)
		for uid, pmjs := range podMigrationJobsOfJob {
			if len(pmjs) == 0 {
				continue
			}
			rankAvg := float32(0.0)
			for _, pmj := range pmjs {
				rankAvg += float32(rankOfPodMigrationJob[pmj])
			}
			rankAvg /= float32(len(pmjs))
			if rankAvg < remainedRankAvg {
				remainedJobUID = uid
				remainedRankAvg = rankAvg
			}
		}
		if remainedJobUID == "" {
			return []*schedulingv1alpha1.PodMigrationJob{}
		} else {
			return podMigrationJobsOfJob[remainedJobUID]
		}
	}

	return func(podMigrationJobs []*schedulingv1alpha1.PodMigrationJob) []*schedulingv1alpha1.PodMigrationJob {
		podMigrationJobsOfJob := group(podMigrationJobs)

		// get rank
		rankOfPodMigrationJob := map[*schedulingv1alpha1.PodMigrationJob]int{}
		for i, job := range podMigrationJobs {
			rankOfPodMigrationJob[job] = i
		}

		var remainedPodMigrationJobs []*schedulingv1alpha1.PodMigrationJob
		if pmjs, ok := podMigrationJobsOfJob[""]; ok {
			remainedPodMigrationJobs = append(remainedPodMigrationJobs, pmjs...)
			delete(podMigrationJobsOfJob, "")
		}
		pmjs := filter(podMigrationJobsOfJob, rankOfPodMigrationJob)
		remainedPodMigrationJobs = append(remainedPodMigrationJobs, pmjs...)
		return remainedPodMigrationJobs
	}
}

func groupJobs(jobs []*schedulingv1alpha1.PodMigrationJob, handle func(job *schedulingv1alpha1.PodMigrationJob) string) map[string][]*schedulingv1alpha1.PodMigrationJob {
	jobsOfKey := map[string][]*schedulingv1alpha1.PodMigrationJob{}
	for _, job := range jobs {
		key := handle(job)
		if nodeJobs, ok := jobsOfKey[key]; ok {
			jobsOfKey[key] = append(nodeJobs, job)
		} else {
			jobsOfKey[key] = []*schedulingv1alpha1.PodMigrationJob{job}
		}
	}
	return jobsOfKey
}

func isJobMigrating(job *schedulingv1alpha1.PodMigrationJob) bool {
	return job.Annotations[AnnotationPassedArbitration] == "true" || job.Status.Phase == schedulingv1alpha1.PodMigrationJobRunning
}

func existMigratingJob(c client.Client, pod *corev1.Pod) bool {
	opts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexJobByPodUID, string(pod.UID))}
	existing := false
	forEachAvailableMigrationJobs(c, opts, func(job *schedulingv1alpha1.PodMigrationJob) bool {
		if podRef := job.Spec.PodRef; podRef != nil && podRef.UID == pod.UID {
			existing = true
		}
		return !existing
	})
	if !existing {
		opts = &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexJobPodNamespacedName, apitypes.NamespacedName{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		}.String())}
		forEachAvailableMigrationJobs(c, opts, func(job *schedulingv1alpha1.PodMigrationJob) bool {
			if podRef := job.Spec.PodRef; podRef != nil && podRef.Namespace == pod.Namespace && podRef.Name == pod.Name {
				existing = true
			}
			return !existing
		})
	}
	return existing
}

func getPodOfJob(c client.Client, jobs []*schedulingv1alpha1.PodMigrationJob) map[*schedulingv1alpha1.PodMigrationJob]*corev1.Pod {
	podOfJob := map[*schedulingv1alpha1.PodMigrationJob]*corev1.Pod{}
	for _, job := range jobs {
		pod := &corev1.Pod{}
		if job.Spec.PodRef == nil {
			klog.Infof("the podRef of job %v is nil", job.Name)
			continue
		}
		nn := apitypes.NamespacedName{
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

func forEachAvailableMigrationJobs(c client.Client, listOpts *client.ListOptions, handler func(job *schedulingv1alpha1.PodMigrationJob) bool, expectPhases ...schedulingv1alpha1.PodMigrationJobPhase) {
	jobList := &schedulingv1alpha1.PodMigrationJobList{}
	err := c.List(context.TODO(), jobList, listOpts, utilclient.DisableDeepCopy)
	if err != nil {
		klog.Errorf("failed to get PodMigrationJobList, err: %v", err)
		return
	}

	for i := range jobList.Items {
		job := &jobList.Items[i]
		found := isJobMigrating(job)
		if found {
			if !handler(job) {
				break
			}
		}
	}
	return
}

func getUnavailablePods(pods []*corev1.Pod) map[apitypes.NamespacedName]struct{} {
	unavailablePods := make(map[apitypes.NamespacedName]struct{})
	for _, pod := range pods {
		if kubecontroller.IsPodActive(pod) && k8spodutil.IsPodReady(pod) {
			continue
		}
		k := apitypes.NamespacedName{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		}
		unavailablePods[k] = struct{}{}
	}
	return unavailablePods
}

func mergeUnavailableAndMigratingPods(unavailablePods, migratingPods map[apitypes.NamespacedName]struct{}) {
	for k, v := range migratingPods {
		unavailablePods[k] = v
	}
}
