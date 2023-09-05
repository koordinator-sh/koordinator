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
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func GetDefaultNUMAAllocateStrategy(pluginArgs *schedulingconfig.NodeNUMAResourceArgs) schedulingconfig.NUMAAllocateStrategy {
	numaAllocateStrategy := schedulingconfig.NUMAMostAllocated
	if pluginArgs != nil && pluginArgs.ScoringStrategy != nil && pluginArgs.ScoringStrategy.Type == schedulingconfig.LeastAllocated {
		numaAllocateStrategy = schedulingconfig.NUMALeastAllocated
	}
	return numaAllocateStrategy
}

func GetNUMAAllocateStrategy(node *corev1.Node, defaultNUMAtAllocateStrategy schedulingconfig.NUMAAllocateStrategy) schedulingconfig.NUMAAllocateStrategy {
	numaAllocateStrategy := defaultNUMAtAllocateStrategy
	if val := schedulingconfig.NUMAAllocateStrategy(node.Labels[extension.LabelNodeNUMAAllocateStrategy]); val != "" {
		numaAllocateStrategy = val
	}
	return numaAllocateStrategy
}

func AllowUseCPUSet(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	qosClass := extension.GetPodQoSClassRaw(pod)
	priorityClass := extension.GetPodPriorityClassWithDefault(pod)
	return (qosClass == extension.QoSLSE || qosClass == extension.QoSLSR) && priorityClass == extension.PriorityProd
}

func getNUMATopologyPolicy(nodeLabels map[string]string, kubeletTopologyManagerPolicy extension.NUMATopologyPolicy) extension.NUMATopologyPolicy {
	policyType := extension.GetNodeNUMATopologyPolicy(nodeLabels)
	if policyType != extension.NUMATopologyPolicyNone {
		return policyType
	}
	return kubeletTopologyManagerPolicy
}
