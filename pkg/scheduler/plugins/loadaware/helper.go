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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func isNodeMetricExpired(nodeMetric *slov1alpha1.NodeMetric, nodeMetricExpirationSeconds int64) bool {
	return nodeMetric == nil ||
		nodeMetric.Status.UpdateTime == nil ||
		nodeMetricExpirationSeconds > 0 &&
			time.Since(nodeMetric.Status.UpdateTime.Time) >= time.Duration(nodeMetricExpirationSeconds)*time.Second
}

func getNodeMetricReportInterval(nodeMetric *slov1alpha1.NodeMetric) time.Duration {
	if nodeMetric.Spec.CollectPolicy == nil || nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds == nil {
		return DefaultNodeMetricReportInterval
	}
	return time.Duration(*nodeMetric.Spec.CollectPolicy.ReportIntervalSeconds) * time.Second
}

func getPodNamespacedName(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

func getResourceValue(resourceName corev1.ResourceName, quantity resource.Quantity) int64 {
	if resourceName == corev1.ResourceCPU {
		return quantity.MilliValue()
	}
	return quantity.Value()
}

func buildPodMetricMap(podLister corev1listers.PodLister, nodeMetric *slov1alpha1.NodeMetric, filterProdPod bool) map[string]corev1.ResourceList {
	if len(nodeMetric.Status.PodsMetric) == 0 {
		return nil
	}
	podMetrics := make(map[string]corev1.ResourceList)
	for _, podMetric := range nodeMetric.Status.PodsMetric {
		pod, err := podLister.Pods(podMetric.Namespace).Get(podMetric.Name)
		if err != nil {
			continue
		}
		if filterProdPod && extension.GetPriorityClass(pod) != extension.PriorityProd {
			continue
		}
		name := getPodNamespacedName(podMetric.Namespace, podMetric.Name)
		podMetrics[name] = podMetric.PodUsage.ResourceList
	}
	return podMetrics
}

func sumPodUsages(podMetrics map[string]corev1.ResourceList, estimatedPods sets.String) (podUsages, estimatedPodsUsages corev1.ResourceList) {
	if len(podMetrics) == 0 {
		return nil, nil
	}
	podUsages = make(corev1.ResourceList)
	estimatedPodsUsages = make(corev1.ResourceList)
	for podName, usage := range podMetrics {
		if estimatedPods.Has(podName) {
			addResourceList(estimatedPodsUsages, usage)
			continue
		}
		addResourceList(podUsages, usage)
	}
	return podUsages, estimatedPodsUsages
}

// addResourceList adds the resources in newList to list.
func addResourceList(list, newList corev1.ResourceList) {
	for name, quantity := range newList {
		if value, ok := list[name]; !ok {
			list[name] = quantity.DeepCopy()
		} else {
			value.Add(quantity)
			list[name] = value
		}
	}
}
