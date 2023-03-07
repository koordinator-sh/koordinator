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

package sharedlisterext

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

type NodeInfoTransformerFn func(nodeInfo *framework.NodeInfo)

func init() {
	frameworkext.RegisterDefaultTransformers(NewNodeInfoTransformer())
}

var nodeInfoTransformerFns []NodeInfoTransformerFn

func RegisterNodeInfoTransformer(fn NodeInfoTransformerFn) {
	nodeInfoTransformerFns = append(nodeInfoTransformerFns, fn)
}

func TransformNodeInfos(nodeInfos []*framework.NodeInfo) (transformedNodeInfos []*framework.NodeInfo) {
	for _, nodeInfo := range nodeInfos {
		n := TransformOneNodeInfo(nodeInfo)
		transformedNodeInfos = append(transformedNodeInfos, n)
	}
	return
}

func TransformOneNodeInfo(nodeInfo *framework.NodeInfo) (transformedNodeInfo *framework.NodeInfo) {
	node := nodeInfo.Node()
	if node == nil || len(nodeInfoTransformerFns) == 0 {
		return nodeInfo
	}

	transformedNodeInfo = nodeInfo.Clone()
	for _, fn := range nodeInfoTransformerFns {
		fn(transformedNodeInfo)
	}
	return transformedNodeInfo
}

var _ frameworkext.FilterTransformer = &nodeInfoTransformer{}

type nodeInfoTransformer struct{}

func NewNodeInfoTransformer() frameworkext.SchedulingTransformer {
	return &nodeInfoTransformer{}
}

func (h *nodeInfoTransformer) Name() string { return "defaultNodeInfoTransformer" }

// BeforeFilter transforms nodeInfo if needed. When the scheduler executes the Filter, the passed NodeInfos comes from the cache instead of SnapshotSharedLister.
// This means that when these NodeInfos need to be transformed, they can only be done in the execution of Filter.
func (h *nodeInfoTransformer) BeforeFilter(handle frameworkext.ExtendedHandle, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) (*corev1.Pod, *framework.NodeInfo, bool) {
	transformedNodeInfo := TransformOneNodeInfo(nodeInfo)
	return pod, transformedNodeInfo, true
}
