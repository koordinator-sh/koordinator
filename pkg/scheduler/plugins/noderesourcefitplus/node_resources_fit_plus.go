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

package noderesourcesfitplus

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

const (
	// Name is plugin name
	Name = "NodeResourcesFitPlus"

	preScoreStateKey = "PreScore" + Name
)

var (
	_ framework.ScorePlugin = &Plugin{}
)

type Plugin struct {
	handle framework.Handle
	args   *config.NodeResourcesFitPlusArgs
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	nodeResourcesFitPlusArgs, ok := args.(*config.NodeResourcesFitPlusArgs)

	if !ok {
		return nil, fmt.Errorf("want args to be of type NodeResourcesArgs, got %T", nodeResourcesFitPlusArgs)
	}

	return &Plugin{
		handle: handle,
		args:   nodeResourcesFitPlusArgs,
	}, nil
}

func (s *Plugin) Name() string {
	return Name
}

type preScoreState struct {
	framework.Resource
	ResourceName []v1.ResourceName
}

// Clone the prefilter state.
func (s *preScoreState) Clone() framework.StateData {
	return s
}

func (s *Plugin) PreScore(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodes []*v1.Node) *framework.Status {
	cycleState.Write(preScoreStateKey, computePodResourceRequest(pod))
	return nil
}

func (s *Plugin) Score(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := s.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	r := ResourceAllocationPriority{
		scorer: resourceScorer,
	}

	scoreState, err := getPreScoreState(state)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("get State node %q from PreScore: %v", nodeName, err))
	}
	scores := r.getResourceScore(s.args, scoreState.ResourceName, p, nodeInfo, nodeName)

	return scores, framework.NewStatus(framework.Success, "")
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func fitsPodRequestName(podRequest framework.Resource) []v1.ResourceName {
	var podRequestResource []v1.ResourceName

	if podRequest.MilliCPU > 0 {
		podRequestResource = append(podRequestResource, v1.ResourceCPU)
	}

	if podRequest.Memory > 0 {
		podRequestResource = append(podRequestResource, v1.ResourceMemory)
	}

	if podRequest.EphemeralStorage > 0 {
		podRequestResource = append(podRequestResource, v1.ResourceEphemeralStorage)
	}

	for rName, rQuant := range podRequest.ScalarResources {
		if rQuant > 0 {
			podRequestResource = append(podRequestResource, rName)
		}
	}

	return podRequestResource
}

func getPreScoreState(cycleState *framework.CycleState) (*preScoreState, error) {
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
