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

package frameworkext

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

var _ FrameworkExtender = &frameworkExtenderImpl{}

type frameworkExtenderImpl struct {
	framework.Framework

	sharedListerAdapter              SharedListerAdapter
	koordinatorClientSet             koordinatorclientset.Interface
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory

	preFilterTransformers []PreFilterTransformer
	filterTransformers    []FilterTransformer
	scoreTransformers     []ScoreTransformer
}

func (ext *frameworkExtenderImpl) updateTransformer(transformers ...SchedulingTransformer) {
	for _, transformer := range transformers {
		preFilterTransformer, ok := transformer.(PreFilterTransformer)
		if ok {
			ext.preFilterTransformers = append(ext.preFilterTransformers, preFilterTransformer)
			klog.V(4).InfoS("framework extender got scheduling transformer registered", "preFilter", preFilterTransformer.Name())
		}
		filterTransformer, ok := transformer.(FilterTransformer)
		if ok {
			ext.filterTransformers = append(ext.filterTransformers, filterTransformer)
			klog.V(4).InfoS("framework extender got scheduling transformer registered", "filter", filterTransformer.Name())
		}
		scoreTransformer, ok := transformer.(ScoreTransformer)
		if ok {
			ext.scoreTransformers = append(ext.scoreTransformers, scoreTransformer)
			klog.V(4).InfoS("framework extender got scheduling transformer registered", "score", scoreTransformer.Name())
		}
	}
}

func (ext *frameworkExtenderImpl) updatePlugins(pl framework.Plugin) {
	if transformer, ok := pl.(SchedulingTransformer); ok {
		ext.updateTransformer(transformer)
	}
}

func (ext *frameworkExtenderImpl) KoordinatorClientSet() koordinatorclientset.Interface {
	return ext.koordinatorClientSet
}

func (ext *frameworkExtenderImpl) KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory {
	return ext.koordinatorSharedInformerFactory
}

func (ext *frameworkExtenderImpl) SnapshotSharedLister() framework.SharedLister {
	if ext.sharedListerAdapter != nil {
		return ext.sharedListerAdapter(ext.Framework.SnapshotSharedLister())
	}
	return ext.Framework.SnapshotSharedLister()
}

// RunPreFilterPlugins transforms the PreFilter phase of framework with pre-filter transformers.
func (ext *frameworkExtenderImpl) RunPreFilterPlugins(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	for _, transformer := range ext.preFilterTransformers {
		newPod, transformed := transformer.BeforePreFilter(ext, cycleState, pod)
		if transformed {
			klog.V(5).InfoS("RunPreFilterPlugins transformed", "transformer", transformer.Name(), "pod", klog.KObj(pod))
			pod = newPod
		}
	}
	return ext.Framework.RunPreFilterPlugins(ctx, cycleState, pod)
}

// RunFilterPluginsWithNominatedPods transforms the Filter phase of framework with filter transformers.
// We don't transform RunFilterPlugins since framework's RunFilterPluginsWithNominatedPods just calls its RunFilterPlugins.
func (ext *frameworkExtenderImpl) RunFilterPluginsWithNominatedPods(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	for _, transformer := range ext.filterTransformers {
		newPod, newNodeInfo, transformed := transformer.BeforeFilter(ext, cycleState, pod, nodeInfo)
		if transformed {
			klog.V(5).InfoS("RunFilterPluginsWithNominatedPods transformed", "transformer", transformer.Name(), "pod", klog.KObj(pod))
			pod = newPod
			nodeInfo = newNodeInfo
		}
	}
	status := ext.Framework.RunFilterPluginsWithNominatedPods(ctx, cycleState, pod, nodeInfo)
	if !status.IsSuccess() && debugFilterFailure {
		klog.Infof("Failed to filter for Pod %q on Node %q, failedPlugin: %s, reason: %s", klog.KObj(pod), klog.KObj(nodeInfo.Node()), status.FailedPlugin(), status.Message())
	}
	return status
}

func (ext *frameworkExtenderImpl) RunScorePlugins(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodes []*corev1.Node) (framework.PluginToNodeScores, *framework.Status) {
	for _, transformer := range ext.scoreTransformers {
		newPod, newNodes, transformed := transformer.BeforeScore(ext, state, pod, nodes)
		if transformed {
			klog.V(5).InfoS("RunScorePlugins transformed", "transformer", transformer.Name(), "pod", klog.KObj(pod))
			pod = newPod
			nodes = newNodes
		}
	}
	pluginToNodeScores, status := ext.Framework.RunScorePlugins(ctx, state, pod, nodes)
	if status.IsSuccess() && debugTopNScores > 0 {
		debugScores(debugTopNScores, pod, pluginToNodeScores, nodes)
	}
	return pluginToNodeScores, status
}
