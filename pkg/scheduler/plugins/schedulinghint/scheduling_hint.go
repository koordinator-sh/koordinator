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

package schedulinghint

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const Name = "SchedulingHint"

var (
	_ framework.PreFilterPlugin              = &Plugin{}
	_ frameworkext.PreFilterTransformer      = &Plugin{}
	_ frameworkext.PreferNodesPluginProvider = &Plugin{}
	_ frameworkext.PreferNodesPlugin         = &Plugin{}
)

type Plugin struct {
	handle frameworkext.ExtendedHandle
}

func New(_ runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	extendedHandle, ok := handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, fmt.Errorf("handle is not a frameworkext.ExtendedHandle")
	}
	return &Plugin{
		handle: extendedHandle,
	}, nil
}

func (p *Plugin) Name() string {
	return Name
}

func (p *Plugin) PreFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	hintState := hinter.GetSchedulingHintState(state)
	if hintState == nil || len(hintState.PreFilterNodes) <= 0 {
		return nil, nil
	}
	return &framework.PreFilterResult{
		NodeNames: sets.New(hintState.PreFilterNodes...),
	}, nil
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func (p *Plugin) BeforePreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool, *framework.Status) {
	hint, err := extension.GetSchedulingHint(pod)
	if err != nil {
		return nil, false, framework.NewStatus(framework.Error, err.Error())
	}
	if hint == nil {
		return nil, false, nil
	}
	hinter.SetSchedulingHintState(cycleState, &hinter.SchedulingHintStateData{
		PreFilterNodes: hint.NodeNames,
		PreferredNodes: hint.PreferredNodeNames,
		Extensions:     hint.Extensions,
	})
	klog.V(4).InfoS("Use scheduling hint", "pod", klog.KObj(pod), "hint", util.DumpJSON(hint))
	return nil, false, nil
}

func (p *Plugin) AfterPreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, preRes *framework.PreFilterResult) *framework.Status {
	return nil
}

func (p *Plugin) PreferNodesPlugin() frameworkext.PreferNodesPlugin {
	return p
}

func (p *Plugin) PreferNodes(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, result *framework.PreFilterResult) ([]string, *framework.Status) {
	hintState := hinter.GetSchedulingHintState(cycleState)
	if hintState == nil || len(hintState.PreferredNodes) <= 0 {
		return nil, framework.NewStatus(framework.Skip, "")
	}

	// Check if the preferred nodes are in the prefilter result.
	// We suppose the hint nodes should be no larger than the prefilter result.
	if result != nil && !result.AllNodes() {
		filteredPreferNodes := make([]string, 0, len(hintState.PreferredNodes))
		for _, nodeName := range hintState.PreferredNodes {
			if result.NodeNames.Has(nodeName) {
				filteredPreferNodes = append(filteredPreferNodes, nodeName)
			}
		}
		if len(filteredPreferNodes) <= 0 {
			klog.V(4).InfoS("Preferred nodes are filtered out by PreFilter, skip", "pod", klog.KObj(pod), "preferredNodes", hintState.PreferredNodes)
			return nil, framework.NewStatus(framework.Skip, "")
		}
		klog.V(5).InfoS("Use the filtered preferred nodes", "pod", klog.KObj(pod), "filteredPreferNodes", filteredPreferNodes)
		return filteredPreferNodes, nil
	}

	return hintState.PreferredNodes, nil
}
