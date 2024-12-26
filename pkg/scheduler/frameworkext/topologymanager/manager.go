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
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/schedulingphase"
)

const (
	ErrUnsatisfiedNUMAResource = "Unsatisfied NUMA %s"
	ErrNUMAHintCannotAligned   = "Unaligned NUMA Hint cause %v"
)

type Interface interface {
	Admit(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string, numaNodes []int, policyType apiext.NUMATopologyPolicy, exclusivePolicy apiext.NumaTopologyExclusive, allNUMANodeStatus []apiext.NumaNodeStatus) *framework.Status
}

type NUMATopologyHintProvider interface {
	// GetPodTopologyHints returns a map of resource names to a list of possible
	// concrete resource allocations per Pod in terms of NUMA locality hints.
	GetPodTopologyHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (map[string][]NUMATopologyHint, *framework.Status)
	// Allocate triggers resource allocation to occur on the HintProvider after
	// all hints have been gathered and the aggregated Hint
	Allocate(ctx context.Context, cycleState *framework.CycleState, affinity NUMATopologyHint, pod *corev1.Pod, nodeName string) *framework.Status
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

func (m *topologyManager) Admit(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string, numaNodes []int, policyType apiext.NUMATopologyPolicy, exclusivePolicy apiext.NumaTopologyExclusive, allNUMANodeStatus []apiext.NumaNodeStatus) *framework.Status {
	s, err := cycleState.Read(affinityStateKey)
	if err != nil {
		return framework.AsStatus(err)
	}
	store := s.(*Store)

	policy := createNUMATopologyPolicy(policyType, numaNodes)
	bestHint, ok := store.GetAffinity(nodeName)
	extensionPointBeingExecuted := schedulingphase.GetExtensionPointBeingExecuted(cycleState)
	klog.V(5).Infof("extensionPointBeingExecuted: %v, bestHint: %v, nodeName: %v, pod: %v", extensionPointBeingExecuted, bestHint, nodeName, pod.Name)
	if !ok || extensionPointBeingExecuted == schedulingphase.PostFilter {
		bestHint, admit, reasons := m.calculateAffinity(ctx, cycleState, policy, pod, nodeName, exclusivePolicy, allNUMANodeStatus)
		klog.V(4).Infof("Best TopologyHint for (pod: %v): %+v on node %s, policy %s, exclusivePolicy %s, admit %v, reasons %v",
			klog.KObj(pod), bestHint, nodeName, policy, exclusivePolicy, admit, reasons)
		if !admit {
			if len(reasons) != 0 {
				return framework.NewStatus(framework.Unschedulable, reasons...)
			}
		}
		// TODO 如果上面的 Affinity 确认可以分配出来，这里看起来没必要再调用了
		status := m.allocateResources(ctx, cycleState, bestHint, pod, nodeName)
		if !status.IsSuccess() {
			return status
		}
		store.SetAffinity(nodeName, bestHint)
	} else {
		status := m.allocateResources(ctx, cycleState, bestHint, pod, nodeName)
		if !status.IsSuccess() {
			return status
		}
	}
	return nil
}

func (m *topologyManager) calculateAffinity(ctx context.Context, cycleState *framework.CycleState, policy Policy, pod *corev1.Pod, nodeName string, exclusivePolicy apiext.NumaTopologyExclusive, allNUMANodeStatus []apiext.NumaNodeStatus) (NUMATopologyHint, bool, []string) {
	providersHints, reasons := m.accumulateProvidersHints(ctx, cycleState, pod, nodeName)
	if len(reasons) != 0 {
		return NUMATopologyHint{}, false, reasons
	}
	bestHint, admit, reasons := policy.Merge(providersHints, exclusivePolicy, allNUMANodeStatus)
	if !checkExclusivePolicy(bestHint, exclusivePolicy, allNUMANodeStatus) {
		klog.V(5).Infof("bestHint violated the exclusivePolicy requirement: bestHint: %v, policy: %v, numaStatus: %v, nodeName: %v, pod: %v",
			bestHint, exclusivePolicy, allNUMANodeStatus, nodeName, pod.Name)
	}
	klog.V(5).Infof("PodTopologyHint: %v", bestHint)
	return bestHint, admit, reasons
}

func (m *topologyManager) accumulateProvidersHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) ([]map[string][]NUMATopologyHint, []string) {
	var providersHints []map[string][]NUMATopologyHint

	hintProviders := m.hintProviderFactory.GetNUMATopologyHintProvider()
	var reasons []string
	for _, provider := range hintProviders {
		// Get the TopologyHints for a Pod from a provider.
		hints, status := provider.GetPodTopologyHints(ctx, cycleState, pod, nodeName)
		if !status.IsSuccess() {
			reasons = append(reasons, status.Message())
			continue
		}
		providersHints = append(providersHints, hints)
		klog.V(4).Infof("TopologyHints for pod '%v' by provider %T: %v on node: %v, status: %s/%s", klog.KObj(pod), provider, hints, nodeName, status.Code, status.Message())
	}
	return providersHints, reasons
}

func (m *topologyManager) allocateResources(ctx context.Context, cycleState *framework.CycleState, affinity NUMATopologyHint, pod *corev1.Pod, nodeName string) *framework.Status {
	hintProviders := m.hintProviderFactory.GetNUMATopologyHintProvider()
	for _, provider := range hintProviders {
		status := provider.Allocate(ctx, cycleState, affinity, pod, nodeName)
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
