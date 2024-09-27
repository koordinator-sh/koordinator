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
	"errors"
	"fmt"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func GetDefaultNUMAAllocateStrategy(pluginArgs *schedulingconfig.NodeNUMAResourceArgs) schedulingconfig.NUMAAllocateStrategy {
	numaAllocateStrategy := schedulingconfig.NUMALeastAllocated
	if pluginArgs != nil && pluginArgs.NUMAScoringStrategy != nil && pluginArgs.NUMAScoringStrategy.Type == schedulingconfig.MostAllocated {
		numaAllocateStrategy = schedulingconfig.NUMAMostAllocated
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

func mergeTopologyPolicy(nodePolicy, podPolicy extension.NUMATopologyPolicy) (extension.NUMATopologyPolicy, error) {
	if nodePolicy != "" && podPolicy != "" && podPolicy != nodePolicy {
		return "", errors.New(ErrNotMatchNUMATopology)
	}
	if podPolicy != "" {
		nodePolicy = podPolicy
	}
	return nodePolicy, nil
}

func getNUMATopologyPolicy(nodeLabels map[string]string, kubeletTopologyManagerPolicy extension.NUMATopologyPolicy) extension.NUMATopologyPolicy {
	policyType := extension.GetNodeNUMATopologyPolicy(nodeLabels)
	if policyType != extension.NUMATopologyPolicyNone {
		return policyType
	}
	return kubeletTopologyManagerPolicy
}

// amplifyNUMANodeResources amplifies the resources per NUMA Node.
// NOTE(joseph): After the NodeResource controller supports amplifying by ratios, should remove the function.
func amplifyNUMANodeResources(node *corev1.Node, topologyOptions *TopologyOptions) error {
	if topologyOptions.AmplificationRatios != nil {
		return nil
	}
	amplificationRatios, err := extension.GetNodeResourceAmplificationRatios(node.Annotations)
	if err != nil {
		return err
	}
	topologyOptions.AmplificationRatios = amplificationRatios

	numaNodeResources := make([]NUMANodeResource, 0, len(topologyOptions.NUMANodeResources))
	for _, v := range topologyOptions.NUMANodeResources {
		numaNode := NUMANodeResource{
			Node:      v.Node,
			Resources: v.Resources.DeepCopy(),
		}
		extension.AmplifyResourceList(numaNode.Resources, amplificationRatios)
		numaNodeResources = append(numaNodeResources, numaNode)
	}
	topologyOptions.NUMANodeResources = numaNodeResources
	return nil
}

func getCPUBindPolicy(topologyOptions *TopologyOptions, node *corev1.Node, requiredCPUBindPolicy, preferredCPUBindPolicy schedulingconfig.CPUBindPolicy) (schedulingconfig.CPUBindPolicy, bool, error) {
	if requiredCPUBindPolicy != "" {
		return requiredCPUBindPolicy, true, nil
	}

	cpuBindPolicy := preferredCPUBindPolicy
	required := false
	kubeletCPUPolicy := topologyOptions.Policy
	nodeCPUBindPolicy := extension.GetNodeCPUBindPolicy(node.Labels, kubeletCPUPolicy)
	switch nodeCPUBindPolicy {
	case extension.NodeCPUBindPolicySpreadByPCPUs:
		cpuBindPolicy = schedulingconfig.CPUBindPolicySpreadByPCPUs
		required = true
	case extension.NodeCPUBindPolicyFullPCPUsOnly:
		cpuBindPolicy = schedulingconfig.CPUBindPolicyFullPCPUs
		required = true
	}
	return cpuBindPolicy, required, nil
}

func requestCPUBind(state *preFilterState, nodeCPUBindPolicy extension.NodeCPUBindPolicy) (bool, *framework.Status) {
	if state.requestCPUBind {
		return true, nil
	}

	requestedCPU := state.requests.Cpu().MilliValue()
	if requestedCPU == 0 {
		return false, nil
	}

	if nodeCPUBindPolicy != "" && nodeCPUBindPolicy != extension.NodeCPUBindPolicyNone {
		if requestedCPU%1000 != 0 {
			return false, framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrInvalidRequestedCPUs)
		}
		return true, nil
	}
	return false, nil
}

func logStruct(v reflect.Value, key string, verbosity klog.Level) {
	rawStr := &strings.Builder{}
	logValue(v, 0, rawStr)
	klog.V(verbosity).Infof("key: %s, value: %s", key, rawStr.String())
}

// logValue is a recursive function that prints the contents of any value.
// For pointers to structs, it recursively unwraps until it reaches the underlying struct.
func logValue(v reflect.Value, depth int, builder *strings.Builder) {
	// Indent for pretty printing
	indent := strings.Repeat(" ", depth)
	switch v.Kind() {
	case reflect.Ptr:
		// For pointers, obtain the value being pointed to
		elem := v.Elem()
		if !elem.IsValid() {
			builder.WriteString(fmt.Sprintf("%s<nil>\t", indent)) // Appends "nil" representation for nil pointers
		} else {
			logValue(elem, depth+1, builder) // Recursively log the pointer element
		}
	case reflect.Struct:
		// For structs, iterate through all fields
		builder.WriteString("\t")
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			fieldType := v.Type().Field(i)
			builder.WriteString(fmt.Sprintf("%s%s:\t", indent, fieldType.Name)) // Appends the field name
			logValue(field, depth+2, builder)                                   // Recursively log each struct field
		}
	case reflect.Slice, reflect.Array:
		// For slices or arrays, iterate through each element
		for i := 0; i < v.Len(); i++ {
			builder.WriteString(fmt.Sprintf("%s[%d]:\t", indent, i)) // Appends the index of the element
			logValue(v.Index(i), depth+2, builder)                   // Recursively log each element
		}
	default:
		// For other types, print the value directly
		builder.WriteString(fmt.Sprintf("%s%v\t", indent, v)) // Appends the value
	}
}
