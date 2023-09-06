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
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
)

func (p *Plugin) FilterByNUMANode(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string, policyType apiext.NUMATopologyPolicy) *framework.Status {
	if policyType == apiext.NUMATopologyPolicyNone {
		return nil
	}
	topologyOptions := p.topologyOptionsManager.GetTopologyOptions(nodeName)
	numaNodes := topologyOptions.getNUMANodes()
	if len(numaNodes) == 0 {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "node(s) missing NUMA resources")
	}
	return p.handle.(frameworkext.FrameworkExtender).RunNUMATopologyManagerAdmit(ctx, cycleState, pod, nodeName, numaNodes, policyType)
}

func (p *Plugin) ReserveByNUMANode(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string, policyType apiext.NUMATopologyPolicy) *framework.Status {
	if policyType == apiext.NUMATopologyPolicyNone {
		return nil
	}
	store := topologymanager.GetStore(cycleState)
	affinity := store.GetAffinity(nodeName)
	_, status := p.allocateByHint(ctx, cycleState, affinity, pod, nodeName, true)
	return status
}

func (p *Plugin) GetPodTopologyHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (map[string][]topologymanager.NUMATopologyHint, *framework.Status) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return nil, status
	}
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	node := nodeInfo.Node()

	resourceOptions, err := p.getResourceOptions(cycleState, state, node, pod, topologymanager.NUMATopologyHint{})
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	hints, err := p.resourceManager.GetTopologyHints(node, pod, resourceOptions)
	if err != nil {
		return nil, framework.NewStatus(framework.Unschedulable, "node(s) Insufficient NUMA Node resources")
	}
	return hints, nil
}

func (p *Plugin) Allocate(ctx context.Context, cycleState *framework.CycleState, affinity topologymanager.NUMATopologyHint, pod *corev1.Pod, nodeName string) *framework.Status {
	_, status := p.allocateByHint(ctx, cycleState, affinity, pod, nodeName, false)
	return status
}

func (p *Plugin) allocateByHint(ctx context.Context, cycleState *framework.CycleState, affinity topologymanager.NUMATopologyHint, pod *corev1.Pod, nodeName string, assume bool) (*PodAllocation, *framework.Status) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return nil, status
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	node := nodeInfo.Node()

	resourceOptions, err := p.getResourceOptions(cycleState, state, node, pod, affinity)
	if err != nil {
		return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}
	result, err := p.resourceManager.Allocate(node, pod, resourceOptions)
	if err != nil {
		return nil, framework.NewStatus(framework.Unschedulable, err.Error())
	}
	if assume {
		p.resourceManager.Update(nodeName, result)
		state.allocation = result
		state.preferredCPUBindPolicy = resourceOptions.cpuBindPolicy
	}
	return result, nil
}
