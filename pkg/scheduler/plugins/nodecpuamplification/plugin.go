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

package nodecpuamplification

import (
	"context"
	"fmt"
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/nodenumaresource"
)

const (
	Name = "NodeCPUAmplification"

	preFilterStateKey = "PreFilter" + Name
)

var (
	_ framework.PreFilterPlugin = &Plugin{}
	_ framework.FilterPlugin    = &Plugin{}
	_ framework.ScorePlugin     = &Plugin{}
)

var nodeResourceStrategyTypeMap = map[config.ScoringStrategyType]scorer{
	config.LeastAllocated: func(args *config.NodeCPUAmplificationArgs) *resourceAllocationScorer {
		resToWeightMap := resourcesToWeightMap(args.ScoringStrategy.Resources)
		return &resourceAllocationScorer{
			Name:                string(config.LeastAllocated),
			scorer:              leastResourceScorer(resToWeightMap),
			resourceToWeightMap: resToWeightMap,
		}
	},
	config.MostAllocated: func(args *config.NodeCPUAmplificationArgs) *resourceAllocationScorer {
		resToWeightMap := resourcesToWeightMap(args.ScoringStrategy.Resources)
		return &resourceAllocationScorer{
			Name:                string(config.MostAllocated),
			scorer:              mostResourceScorer(resToWeightMap),
			resourceToWeightMap: resToWeightMap,
		}
	},
}

// Plugin checks if a node has sufficient CPU resources with CPU amplification.
// It works like the NodeResourcesFit plugin but with CPU requests of CPUSet Pods amplified on Nodes with CPU amplification.
type Plugin struct {
	handle framework.Handle
	resourceAllocationScorer
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*config.NodeCPUAmplificationArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type NodeCPUAmplificationArgs, got %T", args)
	}
	if err := validation.ValidateNodeCPUAmplificationArgs(nil, pluginArgs); err != nil {
		return nil, err
	}

	if pluginArgs.ScoringStrategy == nil {
		return nil, fmt.Errorf("scoring strategy not specified")
	}

	strategy := pluginArgs.ScoringStrategy.Type
	scorePlugin, exists := nodeResourceStrategyTypeMap[strategy]
	if !exists {
		return nil, fmt.Errorf("scoring strategy %s is not supported", strategy)
	}

	return &Plugin{
		handle:                   handle,
		resourceAllocationScorer: *scorePlugin(pluginArgs),
	}, nil
}

type preFilterState struct {
	requestedMilliCPU int64
	isCPUSet          bool
}

func (s *preFilterState) Clone() framework.StateData {
	return s
}

func (p *Plugin) Name() string {
	return Name
}

func (p *Plugin) PreFilter(_ context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	cycleState.Write(preFilterStateKey, &preFilterState{
		requestedMilliCPU: calculatePodCPURequest(pod),
		isCPUSet:          nodenumaresource.AllowUseCPUSet(pod),
	})
	return nil, nil
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func (p *Plugin) Filter(_ context.Context, cycleState *framework.CycleState, _ *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	ratio, err := extension.GetNodeResourceAmplificationRatio(node, corev1.ResourceCPU)
	if err != nil {
		return framework.AsStatus(err)
	}
	if ratio == -1 || ratio == 1 {
		// no special filter logic needed without CPU amplification
		return nil
	}

	s, err := getPreFilterState(cycleState)
	if err != nil {
		return framework.AsStatus(err)
	}

	request := s.requestedMilliCPU
	if s.isCPUSet {
		request = amplify(request, ratio)
	}
	if request > nodeInfo.Allocatable.MilliCPU-calculateNodeAmplifiedCPURequested(nodeInfo, ratio, false) {
		return framework.NewStatus(framework.Unschedulable, "Insufficient amplified cpu")
	}
	return nil
}

func (p *Plugin) Score(_ context.Context, _ *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.AsStatus(fmt.Errorf("getting node %q from Snapshot: %w", nodeName, err))
	}

	return p.score(pod, nodeInfo)
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func getPreFilterState(cycleState *framework.CycleState) (*preFilterState, error) {
	c, err := cycleState.Read(preFilterStateKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %w", preFilterStateKey, err)
	}

	s, ok := c.(*preFilterState)
	if !ok {
		return nil, fmt.Errorf("%+v  convert to NodeCPUAmplification.preFilterState error", c)
	}
	return s, nil
}

func amplify(origin int64, ratio extension.Ratio) int64 {
	return int64(math.Ceil(float64(origin) * float64(ratio)))
}

func calculatePodCPURequest(pod *corev1.Pod) int64 {
	requests, _ := resourceapi.PodRequestsAndLimits(pod)
	return requests.Cpu().MilliValue()
}

func calculatePodAmplifiedResourceRequest(pod *corev1.Pod, resource corev1.ResourceName, cpuRatio extension.Ratio, nonZero bool) int64 {
	var podRequest int64
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		value := schedutil.GetRequestForResource(resource, &container.Resources.Requests, nonZero)
		if resource == corev1.ResourceCPU && nodenumaresource.AllowUseCPUSet(pod) {
			value = amplify(value, cpuRatio)
		}
		podRequest += value
	}

	for i := range pod.Spec.InitContainers {
		initContainer := &pod.Spec.InitContainers[i]
		value := schedutil.GetRequestForResource(resource, &initContainer.Resources.Requests, nonZero)
		if resource == corev1.ResourceCPU && nodenumaresource.AllowUseCPUSet(pod) {
			value = amplify(value, cpuRatio)
		}
		if podRequest < value {
			podRequest = value
		}
	}

	// If Overhead is being utilized, add to the total requests for the pod
	if pod.Spec.Overhead != nil {
		if quantity, found := pod.Spec.Overhead[resource]; found {
			value := quantity.Value()
			if resource == corev1.ResourceCPU && nodenumaresource.AllowUseCPUSet(pod) {
				value = amplify(value, cpuRatio)
			}
			podRequest += value
		}
	}

	return podRequest
}

func calculateNodeAmplifiedCPURequested(nodeInfo *framework.NodeInfo, ratio extension.Ratio, nonZero bool) int64 {
	var requested int64
	for _, podInfo := range nodeInfo.Pods {
		requested += calculatePodAmplifiedResourceRequest(podInfo.Pod, corev1.ResourceCPU, ratio, nonZero)
	}
	return requested
}
