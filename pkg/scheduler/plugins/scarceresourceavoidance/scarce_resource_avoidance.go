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

package scarceresourceavoidance

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/component-helpers/resource"
	fwk "k8s.io/kube-scheduler/framework"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

const (
	// Name is plugin name
	Name = "ScarceResourceAvoidance"

	preScoreStateKey = "PreScore" + Name
)

var (
	_ fwk.ScorePlugin = &Plugin{}
)

type Plugin struct {
	handle fwk.Handle
	args   *config.ScarceResourceAvoidanceArgs
}

func New(_ context.Context, args runtime.Object, handle fwk.Handle) (fwk.Plugin, error) {
	scarceResourceAvoidanceArgs, ok := args.(*config.ScarceResourceAvoidanceArgs)

	if !ok {
		return nil, fmt.Errorf("want args to be of type ResourceTypesArgs, got %T", scarceResourceAvoidanceArgs)
	}

	return &Plugin{
		handle: handle,
		args:   scarceResourceAvoidanceArgs,
	}, nil
}

func (s *Plugin) Name() string {
	return Name
}

func (s *Plugin) PreScore(ctx context.Context, cycleState fwk.CycleState, pod *v1.Pod, nodes []fwk.NodeInfo) *fwk.Status {
	cycleState.Write(preScoreStateKey, computePodResourceRequest(pod))
	return nil
}

func (s *Plugin) Score(ctx context.Context, state fwk.CycleState, p *v1.Pod, nodeInfoArg fwk.NodeInfo) (int64, *fwk.Status) {
	nodeInfo, ok := nodeInfoArg.(fwktype.NodeInfo)
	if !ok {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("nodeInfo type assertion failed for node %v", nodeInfoArg))
	}

	scoreState, err := getPreScoreState(state)
	if err != nil {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("get State node %q from PreScore: %v", nodeInfo.Node().Name, err))
	}
	podRequestResource, nodeAllocatableResource := fitsRequest(scoreState.Resource, nodeInfo)
	diffNames := quotav1.Difference(nodeAllocatableResource, podRequestResource)
	intersectNames := quotav1.Intersection(diffNames, s.args.Resources)

	if len(diffNames) == 0 || len(intersectNames) == 0 {
		return fwk.MaxNodeScore, fwk.NewStatus(fwk.Success, "")
	}
	scores := resourceTypesScore(int64(len(intersectNames)), int64(len(diffNames)))

	return scores, fwk.NewStatus(fwk.Success, "")
}

func (p *Plugin) ScoreExtensions() fwk.ScoreExtensions {
	return nil
}

type preScoreState struct {
	framework.Resource
}

// Clone the prefilter state.
func (s *preScoreState) Clone() fwk.StateData {
	return s
}

func computePodResourceRequest(pod *v1.Pod) *preScoreState {
	// pod hasn't scheduled yet so we don't need to worry about InPlacePodVerticalScalingEnabled
	reqs := resource.PodRequests(pod, resource.PodResourcesOptions{})
	result := &preScoreState{}
	result.SetMaxResource(reqs)
	return result
}

func fitsRequest(podRequest framework.Resource, nodeInfo fwktype.NodeInfo) ([]v1.ResourceName, []v1.ResourceName) {
	var podRequestResource []v1.ResourceName
	var nodeRequestResource []v1.ResourceName

	if podRequest.MilliCPU > 0 {
		podRequestResource = append(podRequestResource, v1.ResourceCPU)
	}

	if nodeInfo.GetAllocatable().GetMilliCPU() > 0 {
		nodeRequestResource = append(nodeRequestResource, v1.ResourceCPU)
	}

	if podRequest.Memory > 0 {
		podRequestResource = append(podRequestResource, v1.ResourceMemory)
	}

	if nodeInfo.GetAllocatable().GetMemory() > 0 {
		nodeRequestResource = append(nodeRequestResource, v1.ResourceMemory)
	}

	if podRequest.EphemeralStorage > 0 {
		podRequestResource = append(podRequestResource, v1.ResourceEphemeralStorage)
	}

	if nodeInfo.GetAllocatable().GetEphemeralStorage() > 0 {
		nodeRequestResource = append(nodeRequestResource, v1.ResourceEphemeralStorage)
	}

	for rName, rQuant := range podRequest.ScalarResources {
		if rQuant > 0 {
			podRequestResource = append(podRequestResource, rName)
		}
	}

	for rName, rQuant := range nodeInfo.GetAllocatable().GetScalarResources() {
		if rQuant > 0 {
			nodeRequestResource = append(nodeRequestResource, rName)
		}
	}

	return podRequestResource, nodeRequestResource
}

func resourceTypesScore(requestsSourcesNum, allocatablesSourcesNum int64) int64 {
	return (allocatablesSourcesNum - requestsSourcesNum) * fwk.MaxNodeScore / allocatablesSourcesNum
}

func getPreScoreState(cycleState fwk.CycleState) (*preScoreState, error) {
	c, err := cycleState.Read(preScoreStateKey)
	if err != nil {
		// preFilterState doesn't exist, likely PreFilter wasn't invoked.
		return nil, fmt.Errorf("error reading %q from cycleState: %w", preScoreStateKey, err)
	}

	s, ok := c.(*preScoreState)
	if !ok {
		return nil, fmt.Errorf("%+v  convert to NodeResourcesFit.preFilterState error", c)
	}
	return s, nil
}
