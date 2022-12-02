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
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

// sortNodes sorts nodes based on usage according to the given plugin.
func sortNodes(nodes []NodeInfo, ascending bool) {
	// TODO(joseph): We should consider giving different weights to different resources for scoring
	sort.Slice(nodes, func(i, j int) bool {
		var ti, tj int64
		for name := range nodes[i].usage {
			if name == corev1.ResourceCPU {
				ti += nodes[i].usage[name].MilliValue()
				tj += nodes[j].usage[name].MilliValue()
			} else {
				ti += nodes[i].usage[name].Value()
				tj += nodes[j].usage[name].Value()
			}
		}

		if ascending {
			return ti < tj
		}
		return ti > tj
	})
}

// The lower the PriorityClass level, the higher the order
var koordPriorityClassOrder = map[extension.PriorityClass]int{
	extension.PriorityProd:  1,
	extension.PriorityMid:   2,
	extension.PriorityBatch: 3,
	extension.PriorityFree:  4,
}

// The lower the QoS level, the higher the order
var koordQoSOrder = map[extension.QoSClass]int{
	extension.QoSSystem: 1,
	extension.QoSLSE:    1,
	extension.QoSLSR:    2,
	extension.QoSLS:     3,
	extension.QoSBE:     4,
}

// The lower the QoS level, the higher the order
var k8sQoSOrder = map[corev1.PodQOSClass]int{
	corev1.PodQOSGuaranteed: 1,
	corev1.PodQOSBurstable:  2,
	corev1.PodQOSBestEffort: 3,
}

func getPodPriority(pod *corev1.Pod) int32 {
	if pod.Spec.Priority == nil {
		return 0
	}
	return *pod.Spec.Priority
}

func sortPods(pods []*corev1.Pod, podMetrics map[types.NamespacedName]*slov1alpha1.ResourceMap, resourceNames []corev1.ResourceName) {
	sort.Slice(pods, func(i, j int) bool {
		iPod, jPod := pods[i], pods[j]
		iKoordPriorityClassOrder := koordPriorityClassOrder[extension.GetPriorityClass(iPod)]
		jKoordPriorityClassOrder := koordPriorityClassOrder[extension.GetPriorityClass(jPod)]
		if iKoordPriorityClassOrder != jKoordPriorityClassOrder {
			return iKoordPriorityClassOrder > jKoordPriorityClassOrder
		}

		iKoordQoSOrder := koordQoSOrder[extension.GetPodQoSClass(iPod)]
		jKoordQosOrder := koordQoSOrder[extension.GetPodQoSClass(jPod)]
		if iKoordQoSOrder != jKoordQosOrder {
			return iKoordQoSOrder > jKoordQosOrder
		}

		iK8sQoSOrder := k8sQoSOrder[util.GetKubeQosClass(iPod)]
		jK8sQoSOrder := k8sQoSOrder[util.GetKubeQosClass(jPod)]
		if iK8sQoSOrder != jK8sQoSOrder {
			return iK8sQoSOrder > jK8sQoSOrder
		}

		iPriority, jPriority := getPodPriority(iPod), getPodPriority(jPod)
		if iPriority != jPriority {
			return iPriority < jPriority
		}

		// TODO(joseph): We should consider giving different weights to different resources for scoring
		iPodMetric := podMetrics[types.NamespacedName{Namespace: iPod.Namespace, Name: iPod.Name}]
		jPodMetric := podMetrics[types.NamespacedName{Namespace: jPod.Namespace, Name: jPod.Name}]
		var iPodUsage, jPodUsage int64
		for _, resourceName := range resourceNames {
			var iQuantity, jQuantity resource.Quantity
			if iPodMetric != nil {
				iQuantity = iPodMetric.ResourceList[resourceName]
			}
			if jPodMetric != nil {
				jQuantity = jPodMetric.ResourceList[resourceName]
			}
			if resourceName == corev1.ResourceCPU {
				iPodUsage += iQuantity.MilliValue()
				jPodUsage += jQuantity.MilliValue()
			} else {
				iPodUsage += iQuantity.Value()
				jPodUsage += jQuantity.Value()
			}
		}
		if iPodUsage != jPodUsage {
			return iPodUsage > jPodUsage
		}

		// empty creation time pods < newer pods < older pods
		if !iPod.CreationTimestamp.Equal(&jPod.CreationTimestamp) {
			return afterOrZero(&iPod.CreationTimestamp, &jPod.CreationTimestamp)
		}
		return false
	})
}

// afterOrZero checks if time t1 is after time t2; if one of them
// is zero, the zero time is seen as after non-zero time.
func afterOrZero(t1, t2 *metav1.Time) bool {
	if t1.Time.IsZero() || t2.Time.IsZero() {
		return t1.Time.IsZero()
	}
	return t1.After(t2.Time)
}
