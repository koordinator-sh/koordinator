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
	fwktype "k8s.io/kube-scheduler/framework"
)

// PostFilterHandleWrapper wraps a framework.Handle to override SnapshotSharedLister
// with a decorated snapshot for preemption evaluation. This ensures the decoration
// is scoped to a single evaluator call, avoiding global state mutation.
type PostFilterHandleWrapper struct {
	fwktype.Handle
	decoratedSnapshot fwktype.SharedLister
}

func (h *PostFilterHandleWrapper) SnapshotSharedLister() fwktype.SharedLister {
	return h.decoratedSnapshot
}

// NewPostFilterHandle creates a handle wrapper with reserve pods injected into the snapshot
// for preemption visibility. This should be called in each PostFilter plugin's method
// before creating a preemption.Evaluator.
func NewPostFilterHandle(
	handle fwktype.Handle,
	decorators []PostFilterNodeDecorator,
	ctx context.Context,
	state fwktype.CycleState,
	pod *corev1.Pod,
) fwktype.Handle {
	if len(decorators) == 0 {
		return handle
	}
	originalSnapshot := handle.SnapshotSharedLister()
	decoratedSnapshot := newPostFilterDecoratedSnapshot(
		originalSnapshot, decorators, ctx, state, pod)
	return &PostFilterHandleWrapper{
		Handle:            handle,
		decoratedSnapshot: decoratedSnapshot,
	}
}

// postFilterDecoratedSnapshot wraps a SharedLister to inject reserve pods
// into nodeInfo for preemption evaluation.
type postFilterDecoratedSnapshot struct {
	inner      fwktype.SharedLister
	decorators []PostFilterNodeDecorator
	ctx        context.Context
	state      fwktype.CycleState
	pod        *corev1.Pod
}

func newPostFilterDecoratedSnapshot(
	inner fwktype.SharedLister,
	decorators []PostFilterNodeDecorator,
	ctx context.Context,
	state fwktype.CycleState,
	pod *corev1.Pod,
) *postFilterDecoratedSnapshot {
	return &postFilterDecoratedSnapshot{
		inner:      inner,
		decorators: decorators,
		ctx:        ctx,
		state:      state,
		pod:        pod,
	}
}

func (s *postFilterDecoratedSnapshot) NodeInfos() fwktype.NodeInfoLister {
	return &postFilterDecoratedNodeInfoLister{
		inner:      s.inner.NodeInfos(),
		decorators: s.decorators,
		ctx:        s.ctx,
		state:      s.state,
		pod:        s.pod,
	}
}

func (s *postFilterDecoratedSnapshot) StorageInfos() fwktype.StorageInfoLister {
	return s.inner.StorageInfos()
}

// postFilterDecoratedNodeInfoLister wraps a NodeInfoLister to inject
// reserve pods per-node via PostFilterNodeDecorator.
type postFilterDecoratedNodeInfoLister struct {
	inner      fwktype.NodeInfoLister
	decorators []PostFilterNodeDecorator
	ctx        context.Context
	state      fwktype.CycleState
	pod        *corev1.Pod
}

func (l *postFilterDecoratedNodeInfoLister) List() ([]fwktype.NodeInfo, error) {
	nodeInfos, err := l.inner.List()
	if err != nil {
		return nil, err
	}
	result := make([]fwktype.NodeInfo, 0, len(nodeInfos))
	for _, ni := range nodeInfos {
		decorated, err := l.decorateNodeInfo(ni)
		if err != nil {
			return nil, err
		}
		result = append(result, decorated)
	}
	return result, nil
}

func (l *postFilterDecoratedNodeInfoLister) HavePodsWithAffinityList() ([]fwktype.NodeInfo, error) {
	return l.inner.HavePodsWithAffinityList()
}

func (l *postFilterDecoratedNodeInfoLister) HavePodsWithRequiredAntiAffinityList() ([]fwktype.NodeInfo, error) {
	return l.inner.HavePodsWithRequiredAntiAffinityList()
}

func (l *postFilterDecoratedNodeInfoLister) Get(nodeName string) (fwktype.NodeInfo, error) {
	ni, err := l.inner.Get(nodeName)
	if err != nil {
		return nil, err
	}
	return l.decorateNodeInfo(ni)
}

func (l *postFilterDecoratedNodeInfoLister) decorateNodeInfo(nodeInfo fwktype.NodeInfo) (fwktype.NodeInfo, error) {
	// Snapshot (clone) the nodeInfo so we don't mutate the original
	cloned := nodeInfo.Snapshot()
	for _, decorator := range l.decorators {
		if err := decorator.DecorateNodeForPostFilter(l.ctx, l.state, l.pod, cloned); err != nil {
			klog.V(4).ErrorS(err, "Failed to decorate nodeInfo for PostFilter",
				"node", nodeInfo.Node().Name)
			return nil, err
		}
	}
	return cloned, nil
}
