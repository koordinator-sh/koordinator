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

package nodenumaresource

import (
	nrtv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func (p *Plugin) isSchedulableAfterPodDeletion(logger klog.Logger, pod *corev1.Pod, oldObj, newObj interface{}) (fwktype.QueueingHint, error) {
	deletedPod, _, err := schedutil.As[*corev1.Pod](oldObj, newObj)
	if err != nil {
		logger.Error(err, "Failed to convert oldObj to Pod in isSchedulableAfterPodDeletion", "oldObj", oldObj)
		return fwktype.Queue, nil
	}

	if deletedPod == nil {
		return fwktype.Queue, nil
	}

	// Any pod deletion might free up resources on a NUMA node.
	// To be precise, we check if it had any resource status related to NUMA.
	if hasNUMAResourceStatus(deletedPod) {
		logger.V(5).Info("Queuing pod because a pod with NUMA resources was deleted", "pod", klog.KObj(pod), "deletedPod", klog.KObj(deletedPod))
		return fwktype.Queue, nil
	}

	return fwktype.QueueSkip, nil
}

func (p *Plugin) isSchedulableAfterNRTChanged(logger klog.Logger, pod *corev1.Pod, oldObj, newObj interface{}) (fwktype.QueueingHint, error) {
	oldNRT, newNRT, err := schedutil.As[*nrtv1alpha1.NodeResourceTopology](oldObj, newObj)
	if err != nil {
		logger.Error(err, "Failed to convert oldObj or newObj to NodeResourceTopology", "oldObj", oldObj, "newObj", newObj)
		return fwktype.Queue, nil
	}

	if oldNRT == nil || newNRT == nil {
		return fwktype.Queue, nil
	}

	// If available resources increased in any zone, the pod might be schedulable now.
	if isNRTMoreSchedulable(oldNRT, newNRT) {
		logger.V(5).Info("Queuing pod because NRT resources increased", "pod", klog.KObj(pod), "node", newNRT.Name)
		return fwktype.Queue, nil
	}

	return fwktype.QueueSkip, nil
}

func isNRTMoreSchedulable(oldNRT, newNRT *nrtv1alpha1.NodeResourceTopology) bool {
	for i := range newNRT.Zones {
		newZone := &newNRT.Zones[i]
		var oldZone *nrtv1alpha1.Zone
		for j := range oldNRT.Zones {
			if oldNRT.Zones[j].Name == newZone.Name {
				oldZone = &oldNRT.Zones[j]
				break
			}
		}
		if oldZone == nil {
			return true
		}
		for _, newRes := range newZone.Resources {
			for _, oldRes := range oldZone.Resources {
				if newRes.Name == oldRes.Name {
					if newRes.Available.Cmp(oldRes.Available) > 0 {
						return true
					}
					break
				}
			}
		}
	}
	return false
}

func hasNUMAResourceStatus(pod *corev1.Pod) bool {
	status, err := extension.GetResourceStatus(pod.Annotations)
	if err != nil || status == nil {
		return false
	}
	if len(status.NUMANodeResources) > 0 || status.CPUSet != "" {
		return true
	}
	return false
}
