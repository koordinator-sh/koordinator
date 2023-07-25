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

package limitaware

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
)

const (
	Name = "LimitAware"
)

var (
	_ framework.EnqueueExtensions = &Plugin{}
	_ framework.PreFilterPlugin   = &Plugin{}
	_ framework.FilterPlugin      = &Plugin{}
	_ framework.PreScorePlugin    = &Plugin{}
	_ framework.ScorePlugin       = &Plugin{}
	_ framework.ScoreExtensions   = &Plugin{}
	_ framework.ReservePlugin     = &Plugin{}
)

type Plugin struct {
	handle                         framework.Handle
	defaultLimitToAllocatableRatio extension.LimitToAllocatableRatio
	resourceToWeightMap            map[corev1.ResourceName]int64
	leastResourceScorer            func(resourceToValueMap, resourceToValueMap) int64
	nodeLimitsCache                *Cache
}

func New(obj runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	args, ok := obj.(*config.LimitAwareArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type LimitAwareArgs, got %T", obj)
	}
	if err := validation.ValidateLimitAwareArgs(args); err != nil {
		return nil, err
	}
	if args.ScoringResourceWeights == nil {
		return nil, fmt.Errorf("scoring strategy not specified")
	}
	plugin := &Plugin{
		handle:                         handle,
		defaultLimitToAllocatableRatio: args.DefaultLimitToAllocatableRatio,
		resourceToWeightMap:            args.ScoringResourceWeights,
		leastResourceScorer:            leastResourceScorer(args.ScoringResourceWeights),
		nodeLimitsCache:                newCache(),
	}
	registerPodEventHandler(handle, plugin.nodeLimitsCache)
	return plugin, nil
}

func (p *Plugin) Name() string {
	return Name
}

func (p *Plugin) EventsToRegister() []framework.ClusterEvent {
	return []framework.ClusterEvent{
		{Resource: framework.Pod, ActionType: framework.Delete},
		{Resource: framework.Node, ActionType: framework.Add | framework.Update},
	}
}

func (p *Plugin) Reserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	p.nodeLimitsCache.AddPod(nodeName, pod)
	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	p.nodeLimitsCache.DeletePod(nodeName, pod)
}
