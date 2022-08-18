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

package migration

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	kubecontroller "k8s.io/kubernetes/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sev1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/controllers/migration/util"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/fieldindex"
	utilclient "github.com/koordinator-sh/koordinator/pkg/util/client"
)

func (r *Reconciler) forEachAvailableMigrationJobs(listOpts *client.ListOptions, handler func(job *sev1alpha1.PodMigrationJob) bool) {
	jobList := &sev1alpha1.PodMigrationJobList{}
	err := r.Client.List(context.TODO(), jobList, listOpts, utilclient.DisableDeepCopy)
	if err != nil {
		klog.Errorf("failed to get PodMigrationJobList, err: %v", err)
		return
	}

	for i := range jobList.Items {
		job := &jobList.Items[i]
		phase := job.Status.Phase
		if phase == "" || phase == sev1alpha1.PodMigrationJobPending || phase == sev1alpha1.PodMigrationJobRunning {
			if !handler(job) {
				break
			}
		}
	}
	return
}

func (r *Reconciler) filterExistingPodMigrationJob(pod *corev1.Pod) bool {
	return !r.existingPodMigrationJob(pod)
}

func (r *Reconciler) existingPodMigrationJob(pod *corev1.Pod) bool {
	opts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexPodUUID, string(pod.UID))}
	existing := false
	r.forEachAvailableMigrationJobs(opts, func(job *sev1alpha1.PodMigrationJob) bool {
		existing = true
		return false
	})
	if !existing {
		opts = &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexPodNamespacedName, fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))}
		r.forEachAvailableMigrationJobs(opts, func(job *sev1alpha1.PodMigrationJob) bool {
			existing = true
			return false
		})
	}
	return existing
}

func (r *Reconciler) filterMaxMigratingPerNode(pod *corev1.Pod) bool {
	if pod.Spec.NodeName == "" || r.args.MaxMigratingPerNode == nil || *r.args.MaxMigratingPerNode <= 0 {
		return true
	}

	podList := &corev1.PodList{}
	listOpts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexNodeName, pod.Spec.NodeName)}
	err := r.Client.List(context.TODO(), podList, listOpts, utilclient.DisableDeepCopy)
	if err != nil {
		return true
	}
	if len(podList.Items) == 0 {
		return true
	}

	count := 0
	for i := range podList.Items {
		pod := &podList.Items[i]
		if r.existingPodMigrationJob(pod) {
			count++
		}
	}
	return count < int(*r.args.MaxMigratingPerNode)
}

func (r *Reconciler) filterMaxMigratingPerNamespace(pod *corev1.Pod) bool {
	if r.args.MaxMigratingPerNamespace == nil || *r.args.MaxMigratingPerNamespace <= 0 {
		return true
	}

	opts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexPodNamespace, pod.Namespace)}
	count := 0
	r.forEachAvailableMigrationJobs(opts, func(job *sev1alpha1.PodMigrationJob) bool {
		count++
		return true
	})
	return count < int(*r.args.MaxMigratingPerNamespace)
}

func (r *Reconciler) filterMaxMigratingOrUnavailablePerWorkload(pod *corev1.Pod) bool {
	if r.existingPodMigrationJob(pod) {
		return true
	}

	ownerRef := metav1.GetControllerOf(pod)
	if ownerRef == nil {
		return true
	}
	pods, expectedReplicas, err := r.controllerFinder.GetPodsForRef(ownerRef.APIVersion, ownerRef.Kind, ownerRef.Name, pod.Namespace, nil, false)
	if err != nil {
		return false
	}

	opts := &client.ListOptions{FieldSelector: fields.OneTermEqualSelector(fieldindex.IndexPodNamespace, pod.Namespace)}
	migratingPods := map[types.NamespacedName]struct{}{}
	r.forEachAvailableMigrationJobs(opts, func(job *sev1alpha1.PodMigrationJob) bool {
		podNamespacedName := types.NamespacedName{
			Namespace: job.Spec.PodRef.Namespace,
			Name:      job.Spec.PodRef.Name,
		}
		pod := &corev1.Pod{}
		err := r.Client.Get(context.TODO(), podNamespacedName, pod)
		if err != nil {
			klog.Errorf("Failed to get Pod %q, err: %v", podNamespacedName, err)
		} else {
			innerPodOwnerRef := metav1.GetControllerOf(pod)
			if innerPodOwnerRef != nil && innerPodOwnerRef.UID == ownerRef.UID {
				migratingPods[podNamespacedName] = struct{}{}
			}
		}
		return true
	})
	if len(migratingPods) == 0 {
		return true
	}
	exceed, maxMigrating, err := r.exceedMaxMigratingReplicas(int(expectedReplicas), len(migratingPods), r.args.MaxMigratingPerWorkload)
	if err != nil {
		return false
	}
	podNamespacedName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
	if exceed {
		klog.V(4).Infof("The workload %s(%s) of Pod %q has %d migration jobs that exceed MaxMigratingPerWorkload %d",
			ownerRef.Name, ownerRef.UID, podNamespacedName, len(migratingPods), maxMigrating)
		return false
	}

	unavailablePods := r.getUnavailablePods(pods)
	mergeUnavailableAndMigratingPods(unavailablePods, migratingPods)
	exceed, maxUnavailable, err := r.exceedMaxUnavailableReplicas(int(expectedReplicas), len(unavailablePods), r.args.MaxUnavailablePerWorkload)
	if err != nil {
		return false
	}
	if exceed {
		klog.V(4).Infof("The workload %s(%s) of Pod %q has %d unavailable Pods that exceed MaxUnavailablePerWorkload %d",
			ownerRef.Name, ownerRef.UID, podNamespacedName, len(migratingPods), maxUnavailable)
		return false
	}
	return true
}

func (r *Reconciler) exceedMaxMigratingReplicas(totalReplicas int, migratingReplicas int, maxMigrating *intstr.IntOrString) (bool, int, error) {
	maxMigratingCount, err := util.GetMaxMigrating(totalReplicas, maxMigrating)
	if err != nil {
		return false, 0, err
	}
	if maxMigratingCount <= 0 {
		return true, 0, nil // don't allow unset maxMigrating
	}
	exceeded := false
	if migratingReplicas >= maxMigratingCount {
		exceeded = true
	}
	return exceeded, maxMigratingCount, nil
}

func (r *Reconciler) exceedMaxUnavailableReplicas(totalReplicas, unavailableReplicas int, maxUnavailable *intstr.IntOrString) (bool, int, error) {
	maxUnavailableCount, err := util.GetMaxUnavailable(totalReplicas, maxUnavailable)
	if err != nil {
		return false, 0, err
	}
	if maxUnavailableCount <= 0 {
		return true, 0, nil // don't allow unset maxUnavailable
	}
	exceeded := false
	if unavailableReplicas >= maxUnavailableCount {
		exceeded = true
	}
	return exceeded, maxUnavailableCount, nil
}

func (r *Reconciler) getUnavailablePods(pods []*corev1.Pod) map[types.NamespacedName]struct{} {
	unavailablePods := make(map[types.NamespacedName]struct{})
	for _, pod := range pods {
		if kubecontroller.IsPodActive(pod) {
			continue
		}
		k := types.NamespacedName{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		}
		unavailablePods[k] = struct{}{}
	}
	return unavailablePods
}

func mergeUnavailableAndMigratingPods(unavailablePods, migratingPods map[types.NamespacedName]struct{}) {
	for k, v := range migratingPods {
		unavailablePods[k] = v
	}
}
