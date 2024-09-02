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
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
)

func (p *Plugin) FilterByNUMANode(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string, policyType apiext.NUMATopologyPolicy, exclusivePolicy apiext.NumaTopologyExclusive, topologyOptions TopologyOptions) *framework.Status {
	if policyType == apiext.NUMATopologyPolicyNone {
		return nil
	}
	numaNodes := topologyOptions.getNUMANodes()
	if len(numaNodes) == 0 {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "node(s) missing NUMA resources")
	}
	numaNodesStatus := p.resourceManager.GetNodeAllocation(nodeName).GetAllNUMANodeStatus(len(numaNodes))
	return p.handle.(frameworkext.FrameworkExtender).RunNUMATopologyManagerAdmit(ctx, cycleState, pod, nodeName, numaNodes, policyType, exclusivePolicy, numaNodesStatus)
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
	topologyOptions := p.topologyOptionsManager.GetTopologyOptions(nodeName)
	podNUMATopologyPolicy := state.podNUMATopologyPolicy
	numaTopologyPolicy := getNUMATopologyPolicy(node.Labels, topologyOptions.NUMATopologyPolicy)
	// we have check in filter, so we will not get error in reserve
	numaTopologyPolicy, _ = mergeTopologyPolicy(numaTopologyPolicy, podNUMATopologyPolicy)
	nodeCPUBindPolicy := apiext.GetNodeCPUBindPolicy(node.Labels, topologyOptions.Policy)
	requestCPUBind, status := requestCPUBind(state, nodeCPUBindPolicy)
	if !status.IsSuccess() {
		return nil, status
	}
	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)
	resourceOptions, err := p.getResourceOptions(state, node, pod, requestCPUBind, topologymanager.NUMATopologyHint{}, topologyOptions)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	resourceOptions.numaScorer = p.numaScorer
	hints, err := p.resourceManager.GetTopologyHints(node, pod, resourceOptions, numaTopologyPolicy, restoreState)
	if err != nil {
		klog.V(5).ErrorS(err, "failed to get topology hints", "pod", klog.KObj(pod), "node", nodeName)
		return nil, framework.NewStatus(framework.Unschedulable, "node(s) Insufficient NUMA Node resources")
	}
	return hints, nil
}

func (p *Plugin) Allocate(ctx context.Context, cycleState *framework.CycleState, affinity topologymanager.NUMATopologyHint, pod *corev1.Pod, nodeName string) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return framework.AsStatus(err)
	}
	node := nodeInfo.Node()
	topologyOptions := p.topologyOptionsManager.GetTopologyOptions(nodeName)
	nodeCPUBindPolicy := apiext.GetNodeCPUBindPolicy(node.Labels, topologyOptions.Policy)
	requestCPUBind, status := requestCPUBind(state, nodeCPUBindPolicy)
	if !status.IsSuccess() {
		return status
	}

	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState(nodeName)

	resourceOptions, err := p.getResourceOptions(state, node, pod, requestCPUBind, affinity, topologyOptions)
	if err != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}

	podAllocation, status := tryAllocateFromReservation(p.resourceManager, restoreState, resourceOptions, restoreState.matched, pod, node)
	if !status.IsSuccess() {
		return status
	}
	if podAllocation != nil {
		return nil
	}

	_, status = tryAllocateFromNode(p.resourceManager, restoreState, resourceOptions, pod, node)
	if !status.IsSuccess() {
		return status
	}
	return nil
}
