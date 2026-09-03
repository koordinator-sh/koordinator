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

package loadaware

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
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

	// For LoadAware, we check if the deleted pod was on a node.
	// If it was assigned, its deletion will decrease the estimated load on that node.
	if deletedPod.Spec.NodeName != "" {
		logger.V(5).Info("Queuing pod because a pod was deleted from a node", "pod", klog.KObj(pod), "deletedPod", klog.KObj(deletedPod), "node", deletedPod.Spec.NodeName)
		return fwktype.Queue, nil
	}

	return fwktype.QueueSkip, nil
}

func (p *Plugin) isSchedulableAfterNodeMetricChanged(logger klog.Logger, pod *corev1.Pod, oldObj, newObj interface{}) (fwktype.QueueingHint, error) {
	oldMetric, newMetric, err := schedutil.As[*slov1alpha1.NodeMetric](oldObj, newObj)
	if err != nil {
		logger.Error(err, "Failed to convert oldObj or newObj to NodeMetric", "oldObj", oldObj, "newObj", newObj)
		return fwktype.Queue, nil
	}

	if oldMetric == nil || newMetric == nil {
		return fwktype.Queue, nil
	}

	// If the usage decreased, the node might be schedulable now.
	if isNodeMetricMoreSchedulable(oldMetric, newMetric) {
		logger.V(5).Info("Queuing pod because node usage decreased", "pod", klog.KObj(pod), "node", newMetric.Name)
		return fwktype.Queue, nil
	}

	return fwktype.QueueSkip, nil
}

func isNodeMetricMoreSchedulable(oldMetric, newMetric *slov1alpha1.NodeMetric) bool {
	if oldMetric.Status.NodeMetric == nil || newMetric.Status.NodeMetric == nil {
		return true
	}
	for res, newVal := range newMetric.Status.NodeMetric.NodeUsage.ResourceList {
		oldVal, ok := oldMetric.Status.NodeMetric.NodeUsage.ResourceList[res]
		if !ok || newVal.Cmp(oldVal) < 0 {
			return true
		}
	}
	return false
}
