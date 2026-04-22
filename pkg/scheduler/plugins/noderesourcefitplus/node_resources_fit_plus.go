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
	fwk "k8s.io/kube-scheduler/framework"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

const (
	// Name is plugin name
	Name = "NodeResourcesFitPlus"

	preScoreStateKey = "PreScore" + Name
)

var (
	_ fwk.ScorePlugin = &Plugin{}
)

type Plugin struct {
	handle fwk.Handle
	args   *config.NodeResourcesFitPlusArgs
}

func New(_ context.Context, args runtime.Object, handle fwk.Handle) (fwk.Plugin, error) {
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
func (s *preScoreState) Clone() fwk.StateData {
	return s
}

func (s *Plugin) PreScore(ctx context.Context, cycleState fwk.CycleState, pod *v1.Pod, nodes []fwk.NodeInfo) *fwk.Status {
	cycleState.Write(preScoreStateKey, computePodResourceRequest(pod))
	return nil
}

func (s *Plugin) Score(ctx context.Context, state fwk.CycleState, p *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {
	n, ok := nodeInfo.(fwktype.NodeInfo)
	if !ok {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("nodeInfo type assertion failed for node %v", nodeInfo))
	}

	r := ResourceAllocationPriority{
		scorer: resourceScorer,
	}

	scoreState, err := getPreScoreState(state)
	if err != nil {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("get state node %q from PreScore: %v", n.Node().Name, err))
	}
	scores := r.getResourceScore(s.args, scoreState.ResourceName, p, n, n.Node().Name)

	return scores, fwk.NewStatus(fwk.Success, "")
}

func (p *Plugin) ScoreExtensions() fwk.ScoreExtensions {
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
