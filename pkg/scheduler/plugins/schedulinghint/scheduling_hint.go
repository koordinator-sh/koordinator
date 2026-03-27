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
	fwktype "k8s.io/kube-scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	Name = "SchedulingHint"
)

var (
	_ fwktype.PreFilterPlugin                = &Plugin{}
	_ frameworkext.PreFilterTransformer      = &Plugin{}
	_ frameworkext.PreferNodesPluginProvider = &Plugin{}
	_ frameworkext.PreferNodesPlugin         = &Plugin{}
)

type Plugin struct {
	handle       frameworkext.ExtendedHandle
	maxHintNodes int32
}

func New(_ context.Context, args runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
	extendedHandle, ok := handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, fmt.Errorf("handle is not a frameworkext.ExtendedHandle")
	}
	pluginArgs, ok := args.(*config.SchedulingHintArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type SchedulingHintArgs, got %T", args)
	}
	if err := validation.ValidateSchedulingHintArgs(nil, pluginArgs); err != nil {
		return nil, err
	}
	return &Plugin{
		handle:       extendedHandle,
		maxHintNodes: pluginArgs.MaxHintNodes,
	}, nil
}

func (p *Plugin) Name() string {
	return Name
}

func (p *Plugin) PreFilter(ctx context.Context, state fwktype.CycleState, pod *corev1.Pod, nodes []fwktype.NodeInfo) (*fwktype.PreFilterResult, *fwktype.Status) {
	hintState := hinter.GetSchedulingHintState(state)
	if hintState == nil || len(hintState.PreFilterNodes) <= 0 {
		return nil, nil
	}
	return &fwktype.PreFilterResult{
		NodeNames: sets.New(hintState.PreFilterNodes...),
	}, nil
}

func (p *Plugin) PreFilterExtensions() fwktype.PreFilterExtensions {
	return nil
}

func (p *Plugin) BeforePreFilter(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod) (*corev1.Pod, bool, *fwktype.Status) {
	hint, err := extension.GetSchedulingHint(pod)
	if err != nil {
		return nil, false, fwktype.NewStatus(fwktype.Error, err.Error())
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

func (p *Plugin) AfterPreFilter(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, preRes *fwktype.PreFilterResult) *fwktype.Status {
	return nil
}

func (p *Plugin) PreferNodesPlugin() frameworkext.PreferNodesPlugin {
	return p
}

func (p *Plugin) PreferNodes(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, result *fwktype.PreFilterResult) ([]string, *fwktype.Status) {
	hintState := hinter.GetSchedulingHintState(cycleState)
	if hintState == nil || len(hintState.PreferredNodes) <= 0 {
		return nil, fwktype.NewStatus(fwktype.Skip, "")
	}

	preferredNodes := hintState.PreferredNodes
	if int32(len(hintState.PreferredNodes)) > p.maxHintNodes { // truncate nodes exceeding the max length
		klog.V(4).InfoS("Preferred nodes are more than maxHintNodes, truncate trailing nodes", "pod", klog.KObj(pod), "preferredNodes", hintState.PreferredNodes, "maxLength", p.maxHintNodes)
		preferredNodes = hintState.PreferredNodes[:p.maxHintNodes]
	}

	// Check if the preferred nodes are in the prefilter result.
	// We suppose the hint nodes should be no larger than the prefilter result.
	if result != nil && !result.AllNodes() {
		filteredPreferNodes := make([]string, 0, len(preferredNodes))
		for _, nodeName := range preferredNodes {
			if result.NodeNames.Has(nodeName) {
				filteredPreferNodes = append(filteredPreferNodes, nodeName)
			}
		}
		if len(filteredPreferNodes) <= 0 {
			klog.V(4).InfoS("Preferred nodes are filtered out by PreFilter, skip", "pod", klog.KObj(pod), "preferredNodes", hintState.PreferredNodes)
			return nil, fwktype.NewStatus(fwktype.Skip, "")
		}
		klog.V(5).InfoS("Use the filtered preferred nodes", "pod", klog.KObj(pod), "filteredPreferNodes", filteredPreferNodes)
		return filteredPreferNodes, nil
	}

	return preferredNodes, nil
}
