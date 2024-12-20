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
	"k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

const (
	// Name is plugin name
	Name = "ScarceResourceAvoidance"
)

var (
	_ framework.ScorePlugin = &Plugin{}
)

type Plugin struct {
	handle framework.Handle
	args   *config.ScarceResourceAvoidanceArgs
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {

	sampleArgs2, ok := args.(*config.ScarceResourceAvoidanceArgs)

	if !ok {
		return nil, fmt.Errorf("want args to be of type ResourceTypesArgs, got %T", args)
	}

	return &Plugin{
		handle: handle,
		args:   sampleArgs2,
	}, nil
}

func (s *Plugin) Name() string {
	return Name
}

func (s *Plugin) Score(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := s.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	podRequest := computePodResourceRequest(p)
	podRequestResource, nodeAllocatableResource := fitsRequest(podRequest.Resource, nodeInfo)
	diffNames := difference(nodeAllocatableResource, podRequestResource)
	intersectNames := intersection(diffNames, s.args.Resources)

	if len(diffNames) == 0 || len(intersectNames) == 0 {
		return framework.MaxNodeScore, framework.NewStatus(framework.Success, "")
	}
	scores := resourceTypesScore(int64(len(intersectNames)), int64(len(diffNames)))

	return scores, framework.NewStatus(framework.Success, "")
}

func intersection(slice1, slice2 []v1.ResourceName) []v1.ResourceName {
	m := make(map[v1.ResourceName]struct{})
	result := []v1.ResourceName{}

	for _, v := range slice2 {
		m[v] = struct{}{}
	}

	for _, v := range slice1 {
		if _, found := m[v]; found {
			result = append(result, v)
		}
	}

	return result
}

func difference(slice1, slice2 []v1.ResourceName) []v1.ResourceName {
	var result []v1.ResourceName
	m := make(map[v1.ResourceName]struct{})
	for _, v := range slice2 {
		m[v] = struct{}{}
	}

	for _, v := range slice1 {
		if _, found := m[v]; !found {
			result = append(result, v)
		}
	}

	return result
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

type preFilterState struct {
	framework.Resource
}

func computePodResourceRequest(pod *v1.Pod) *preFilterState {
	// pod hasn't scheduled yet so we don't need to worry about InPlacePodVerticalScalingEnabled
	reqs := resource.PodRequests(pod, resource.PodResourcesOptions{})
	result := &preFilterState{}
	result.SetMaxResource(reqs)
	return result
}

func fitsRequest(podRequest framework.Resource, nodeInfo *framework.NodeInfo) ([]v1.ResourceName, []v1.ResourceName) {
	var podRequestResource []v1.ResourceName
	var nodeRequestResource []v1.ResourceName

	if podRequest.MilliCPU > 0 {
		podRequestResource = append(podRequestResource, v1.ResourceCPU)
	}

	if nodeInfo.Allocatable.MilliCPU > 0 {
		nodeRequestResource = append(nodeRequestResource, v1.ResourceCPU)
	}

	if podRequest.Memory > 0 {
		podRequestResource = append(podRequestResource, v1.ResourceMemory)
	}

	if nodeInfo.Allocatable.Memory > 0 {
		nodeRequestResource = append(nodeRequestResource, v1.ResourceMemory)
	}

	if podRequest.EphemeralStorage > 0 {
		podRequestResource = append(podRequestResource, v1.ResourceEphemeralStorage)
	}

	if nodeInfo.Allocatable.EphemeralStorage > 0 {
		nodeRequestResource = append(nodeRequestResource, v1.ResourceEphemeralStorage)
	}

	for rName, rQuant := range podRequest.ScalarResources {
		if rQuant > 0 {
			podRequestResource = append(podRequestResource, rName)
		}
	}

	for rName, rQuant := range nodeInfo.Allocatable.ScalarResources {
		if rQuant > 0 {
			nodeRequestResource = append(nodeRequestResource, rName)
		}
	}

	return podRequestResource, nodeRequestResource
}
func resourceTypesScore(requestsSourcesNum, allocatablesSourcesNum int64) int64 {
	return (allocatablesSourcesNum - requestsSourcesNum) * framework.MaxNodeScore / allocatablesSourcesNum
}
