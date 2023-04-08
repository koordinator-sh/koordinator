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

package numatopology

import (
	"context"

	nrtclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned"
	nrtinformers "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/informers/externalversions"
	topologylister "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/listers/topology/v1alpha1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/util/format"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	Name = "NUMATopology"
)

var _ framework.FilterPlugin = &Plugin{}
var _ framework.ReservePlugin = &Plugin{}

type Plugin struct {
	nrtLister topologylister.NodeResourceTopologyLister
	handle    frameworkext.ExtendedHandle
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	nrtLister, err := createAndSyncNodeResourceTopologyLister(handle)
	if err != nil {
		return nil, err
	}
	extendHandle := handle.(frameworkext.ExtendedHandle)
	return &Plugin{
		nrtLister: nrtLister,
		handle:    extendHandle,
	}, nil
}

func (pl *Plugin) Name() string { return Name }

func (pl *Plugin) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "node not found")
	}
	policyType := apiext.GetNodeNUMATopologyPolicy(node.Labels)
	if policyType == apiext.NUMATopologyPolicyNone {
		return nil
	}

	numaNodes, err := pl.getNUMANodes(node.Name)
	if err != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "node(s) invalid topology")
	}
	policy := createNUMATopologyPolicy(policyType, numaNodes)

	bestHint, admit := pl.calculateAffinity(ctx, cycleState, policy, pod, node.Name)
	klog.Infof("[topologymanager] Best TopologyHint for (pod: %v): %v", format.Pod(pod), bestHint)
	if !admit {
		return framework.NewStatus(framework.Unschedulable, "node(s) TopologyAffinityError")
	}

	err = pl.allocateAlignedResources(ctx, cycleState, bestHint, pod, node.Name, false)
	if err != nil {
		return framework.NewStatus(framework.Unschedulable, "node(s) insufficient resources on numa node")
	}
	return nil
}

func (pl *Plugin) Reserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return framework.AsStatus(err)
	}
	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "node not found")
	}

	policyType := apiext.GetNodeNUMATopologyPolicy(node.Labels)
	if policyType == apiext.NUMATopologyPolicyNone {
		return nil
	}

	numaNodes, err := pl.getNUMANodes(node.Name)
	if err != nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, "node(s) invalid topology")
	}
	policy := createNUMATopologyPolicy(policyType, numaNodes)

	bestHint, admit := pl.calculateAffinity(ctx, cycleState, policy, pod, node.Name)
	klog.Infof("[topologymanager] Best TopologyHint for (pod: %v): %v", format.Pod(pod), bestHint)
	if !admit {
		return framework.NewStatus(framework.Unschedulable, "node(s) TopologyAffinityError")
	}

	err = pl.allocateAlignedResources(ctx, cycleState, bestHint, pod, node.Name, true)
	if err != nil {
		return framework.NewStatus(framework.Unschedulable, "node(s) insufficient resources on numa node")
	}
	return nil
}

func (pl *Plugin) Unreserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) {
	return
}

func (pl *Plugin) accumulateProvidersHints(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) []map[string][]frameworkext.NUMATopologyHint {
	var providersHints []map[string][]frameworkext.NUMATopologyHint

	providerGetter := pl.handle.(frameworkext.NUMATopologyHintProviderGetter)
	hintProviders := providerGetter.GetHintProviders()
	for _, provider := range hintProviders {
		// Get the TopologyHints for a Pod from a provider.
		hints := provider.GetPodTopologyHints(ctx, cycleState, pod, nodeName)
		providersHints = append(providersHints, hints)
		klog.Infof("[topologymanager] TopologyHints for pod '%v': %v", format.Pod(pod), hints)
	}
	return providersHints
}

func (pl *Plugin) calculateAffinity(ctx context.Context, cycleState *framework.CycleState, policy Policy, pod *corev1.Pod, nodeName string) (frameworkext.NUMATopologyHint, bool) {
	providersHints := pl.accumulateProvidersHints(ctx, cycleState, pod, nodeName)
	bestHint, admit := policy.Merge(providersHints)
	klog.Infof("[topologymanager] PodTopologyHint: %v", bestHint)
	return bestHint, admit
}

func (pl *Plugin) allocateAlignedResources(ctx context.Context, cycleState *framework.CycleState, affinity frameworkext.NUMATopologyHint, pod *corev1.Pod, nodeName string, assume bool) error {
	providerGetter := pl.handle.(frameworkext.NUMATopologyHintProviderGetter)
	hintProviders := providerGetter.GetHintProviders()

	for _, provider := range hintProviders {
		err := provider.Allocate(ctx, cycleState, affinity, pod, nodeName, assume)
		if err != nil {
			return err
		}
	}
	return nil
}

func createAndSyncNodeResourceTopologyLister(handle framework.Handle) (topologylister.NodeResourceTopologyLister, error) {
	nrtClient, ok := handle.(nrtclientset.Interface)
	if !ok {
		kubeConfig := *handle.KubeConfig()
		kubeConfig.ContentType = runtime.ContentTypeJSON
		kubeConfig.AcceptContentTypes = runtime.ContentTypeJSON
		var err error
		nrtClient, err = nrtclientset.NewForConfig(&kubeConfig)
		if err != nil {
			return nil, err
		}
	}

	nodeResTopologyInformerFactory := nrtinformers.NewSharedInformerFactoryWithOptions(nrtClient, 0)
	lister := nodeResTopologyInformerFactory.Topology().V1alpha1().NodeResourceTopologies().Lister()
	nodeResTopologyInformerFactory.Start(nil)
	nodeResTopologyInformerFactory.WaitForCacheSync(nil)
	return lister, nil
}

func (pl *Plugin) getNUMANodes(nodeName string) ([]int, error) {
	topology, err := pl.nrtLister.Get(nodeName)
	if err != nil {
		return nil, err
	}
	cpuTopology, err := apiext.GetCPUTopology(topology.Annotations)
	if err != nil {
		return nil, err
	}
	nodes := sets.NewInt()
	for _, v := range cpuTopology.Detail {
		nodes.Insert(int(v.Node))
	}
	return nodes.List(), nil
}

func createNUMATopologyPolicy(policyType string, numaNodes []int) Policy {
	var policy Policy
	switch policyType {
	case apiext.NUMATopologyPolicyBestEffort:
		policy = NewBestEffortPolicy(numaNodes)
	case apiext.NUMATopologyPolicyRestricted:
		policy = NewRestrictedPolicy(numaNodes)
	case apiext.NUMATopologyPolicySingleNUMANode:
		policy = NewSingleNumaNodePolicy(numaNodes)
	}
	return policy
}
