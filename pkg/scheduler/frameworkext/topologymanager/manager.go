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

package topologymanager

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

type Interface interface {
	Allocate(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string, numaNodes []int, policyType apiext.NUMATopologyPolicy, assume bool) *framework.Status
}

type NUMATopologyHintProvider interface {
	// GetPodTopologyHints returns a map of resource names to a list of possible
	// concrete resource allocations per Pod in terms of NUMA locality hints.
	GetPodTopologyHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (map[string][]NUMATopologyHint, *framework.Status)
	// Allocate triggers resource allocation to occur on the HintProvider after
	// all hints have been gathered and the aggregated Hint
	Allocate(ctx context.Context, cycleState *framework.CycleState, affinity NUMATopologyHint, pod *corev1.Pod, nodeName string, assume bool) *framework.Status
}

var _ Interface = &topologyManager{}

type topologyManager struct {
	hintProviderFactory NUMATopologyHintProviderFactory
}

type NUMATopologyHintProviderFactory interface {
	GetNUMATopologyHintProvider() []NUMATopologyHintProvider
}

func New(hintProviderFactory NUMATopologyHintProviderFactory) Interface {
	return &topologyManager{
		hintProviderFactory: hintProviderFactory,
	}
}

func (m *topologyManager) Allocate(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string, numaNodes []int, policyType apiext.NUMATopologyPolicy, assume bool) *framework.Status {
	policy := createNUMATopologyPolicy(policyType, numaNodes)

	bestHint, admit := m.calculateAffinity(ctx, cycleState, policy, pod, nodeName)
	klog.V(5).Infof("Best TopologyHint for (pod: %v): %v", format.Pod(pod), bestHint)
	if !admit {
		return framework.NewStatus(framework.Unschedulable, "node(s) NUMA Topology affinity error")
	}

	status := m.allocateAlignedResources(ctx, cycleState, bestHint, pod, nodeName, assume)
	if !status.IsSuccess() {
		return status
	}
	return nil
}

func (m *topologyManager) calculateAffinity(ctx context.Context, cycleState *framework.CycleState, policy Policy, pod *corev1.Pod, nodeName string) (NUMATopologyHint, bool) {
	providersHints := m.accumulateProvidersHints(ctx, cycleState, pod, nodeName)
	bestHint, admit := policy.Merge(providersHints)
	klog.V(5).Infof("PodTopologyHint: %v", bestHint)
	return bestHint, admit
}

func (m *topologyManager) accumulateProvidersHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) []map[string][]NUMATopologyHint {
	var providersHints []map[string][]NUMATopologyHint

	hintProviders := m.hintProviderFactory.GetNUMATopologyHintProvider()
	for _, provider := range hintProviders {
		// Get the TopologyHints for a Pod from a provider.
		hints, _ := provider.GetPodTopologyHints(ctx, cycleState, pod, nodeName)
		providersHints = append(providersHints, hints)
		klog.V(5).Infof("TopologyHints for pod '%v': %v", format.Pod(pod), hints)
	}
	return providersHints
}

func (m *topologyManager) allocateAlignedResources(ctx context.Context, cycleState *framework.CycleState, affinity NUMATopologyHint, pod *corev1.Pod, nodeName string, assume bool) *framework.Status {
	hintProviders := m.hintProviderFactory.GetNUMATopologyHintProvider()
	for _, provider := range hintProviders {
		status := provider.Allocate(ctx, cycleState, affinity, pod, nodeName, assume)
		if !status.IsSuccess() {
			return status
		}
	}
	return nil
}

func createNUMATopologyPolicy(policyType apiext.NUMATopologyPolicy, numaNodes []int) Policy {
	var p Policy
	switch policyType {
	case apiext.NUMATopologyPolicyBestEffort:
		p = NewBestEffortPolicy(numaNodes)
	case apiext.NUMATopologyPolicyRestricted:
		p = NewRestrictedPolicy(numaNodes)
	case apiext.NUMATopologyPolicySingleNUMANode:
		p = NewSingleNumaNodePolicy(numaNodes)
	}
	return p
}
