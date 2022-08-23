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

package nodenumaresource

import (
	"sync"

	nrtv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/apis/scheduling/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type nodeNUMAInfo struct {
	lock          sync.Mutex
	nodeName      string
	cpuTopology   *CPUTopology
	allocatedPods map[types.UID]struct{}
	allocatedCPUs CPUDetails
	*extension.KubeletCPUManagerPolicy
}

type NodeNumaInfoCache struct {
	lock  sync.Mutex
	nodes map[string]*nodeNUMAInfo
}

func newNodeNUMAInfo(nodeName string, cpuTopology *CPUTopology) *nodeNUMAInfo {
	return &nodeNUMAInfo{
		nodeName:                nodeName,
		cpuTopology:             cpuTopology,
		allocatedPods:           map[types.UID]struct{}{},
		allocatedCPUs:           NewCPUDetails(),
		KubeletCPUManagerPolicy: &extension.KubeletCPUManagerPolicy{},
	}
}

func newNodeNUMAInfoCache() *NodeNumaInfoCache {
	return &NodeNumaInfoCache{
		nodes: map[string]*nodeNUMAInfo{},
	}
}

func (c *NodeNumaInfoCache) onNodeResourceTopologyAdd(obj interface{}) {
	nodeResTopology, ok := obj.(*nrtv1alpha1.NodeResourceTopology)
	if !ok {
		return
	}
	c.setNodeResourceTopology(nil, nodeResTopology)
}

func (c *NodeNumaInfoCache) onNodeResourceTopologyUpdate(oldObj, newObj interface{}) {
	oldNodeResTopology, ok := oldObj.(*nrtv1alpha1.NodeResourceTopology)
	if !ok {
		return
	}

	nodeResTopology, ok := newObj.(*nrtv1alpha1.NodeResourceTopology)
	if !ok {
		return
	}
	c.setNodeResourceTopology(oldNodeResTopology, nodeResTopology)
}

func (c *NodeNumaInfoCache) onNodeResourceTopologyDelete(obj interface{}) {
	var nodeResTopology *nrtv1alpha1.NodeResourceTopology
	switch t := obj.(type) {
	case *nrtv1alpha1.NodeResourceTopology:
		nodeResTopology = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		nodeResTopology, ok = t.Obj.(*nrtv1alpha1.NodeResourceTopology)
		if !ok {
			return
		}
	default:
		break
	}

	if nodeResTopology == nil {
		return
	}
	c.deleteNodeResourceTopology(nodeResTopology)
}

func (c *NodeNumaInfoCache) onPodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	c.SetPod(pod)
}

func (c *NodeNumaInfoCache) onPodUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}
	c.SetPod(pod)
}

func (c *NodeNumaInfoCache) onPodDelete(obj interface{}) {
	var pod *corev1.Pod
	switch t := obj.(type) {
	case *corev1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		pod, ok = t.Obj.(*corev1.Pod)
		if !ok {
			return
		}
	default:
		break
	}

	if pod == nil {
		return
	}
	c.DeletePod(pod)
}

func (c *NodeNumaInfoCache) getNodeNUMAInfo(nodeName string) *nodeNUMAInfo {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.nodes[nodeName]
}

func (c *NodeNumaInfoCache) setNodeResourceTopology(oldNodeResTopology, nodeResTopology *nrtv1alpha1.NodeResourceTopology) {
	cpuTopology := buildCPUTopology(nodeResTopology)
	podCPUAllocs, err := extension.GetPodCPUAllocs(nodeResTopology.Annotations)
	if err != nil {
		klog.Errorf("Failed to GetPodCPUAllocs from new NodeResourceTopology %s, err: %v", nodeResTopology.Name, err)
	}
	kubeletCPUManagerPolicy, err := extension.GetKubeletCPUManagerPolicy(nodeResTopology.Annotations)
	if err != nil {
		klog.Errorf("Failed to GetKubeletCPUManagerPolicy from NodeResourceTopology %s, err: %v", nodeResTopology.Name, err)
	}

	var oldPodCPUAllocs extension.PodCPUAllocs
	if oldNodeResTopology != nil {
		oldPodCPUAllocs, err = extension.GetPodCPUAllocs(oldNodeResTopology.Annotations)
		if err != nil {
			klog.Errorf("Failed to GetPodCPUAllocs from old NodeResourceTopology %s, err: %v", nodeResTopology.Name, err)
		}
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	nodeName := nodeResTopology.Name
	numaInfo := c.nodes[nodeName]
	if numaInfo == nil {
		numaInfo = newNodeNUMAInfo(nodeName, cpuTopology)
		c.nodes[nodeName] = numaInfo
	}

	numaInfo.lock.Lock()
	defer numaInfo.lock.Unlock()
	numaInfo.updateCPUTopology(cpuTopology)
	numaInfo.updateKubeletCPUManagerPolicy(kubeletCPUManagerPolicy)
	numaInfo.releaseCPUsManagedByKubelet(oldPodCPUAllocs)
	numaInfo.updateCPUsManagedByKubelet(podCPUAllocs)
}

func (c *NodeNumaInfoCache) deleteNodeResourceTopology(nodeResTopology *nrtv1alpha1.NodeResourceTopology) {
	c.lock.Lock()
	defer c.lock.Unlock()
	nodeName := nodeResTopology.Name
	delete(c.nodes, nodeName)
}

func (c *NodeNumaInfoCache) SetPod(pod *corev1.Pod) {
	if pod.Spec.NodeName == "" {
		return
	}
	if util.IsPodTerminated(pod) {
		c.DeletePod(pod)
		return
	}

	numaInfo := c.getNodeNUMAInfo(pod.Spec.NodeName)
	if numaInfo == nil {
		return
	}

	resourceStatus, err := GetResourceStatus(pod.Annotations)
	if err != nil {
		return
	}

	resourceSpec, err := GetResourceSpec(pod.Annotations)
	if err != nil {
		return
	}

	cpuset, err := Parse(resourceStatus.CPUSet)
	if err != nil || cpuset.IsEmpty() {
		return
	}

	numaInfo.lock.Lock()
	defer numaInfo.lock.Unlock()
	numaInfo.allocateCPUs(pod.UID, cpuset, resourceSpec.PreferredCPUExclusivePolicy)
}

func (c *NodeNumaInfoCache) DeletePod(pod *corev1.Pod) {
	if pod.Spec.NodeName == "" {
		return
	}

	numaInfo := c.getNodeNUMAInfo(pod.Spec.NodeName)
	if numaInfo == nil {
		return
	}

	resourceStatus, err := GetResourceStatus(pod.Annotations)
	if err != nil {
		return
	}
	cpuset, err := Parse(resourceStatus.CPUSet)
	if err != nil || cpuset.IsEmpty() {
		return
	}

	numaInfo.lock.Lock()
	defer numaInfo.lock.Unlock()
	numaInfo.releaseCPUs(pod.UID, cpuset)
}

func (n *nodeNUMAInfo) updateCPUTopology(topology *CPUTopology) {
	n.cpuTopology = topology
}

func (n *nodeNUMAInfo) updateKubeletCPUManagerPolicy(policy *extension.KubeletCPUManagerPolicy) {
	if policy == nil {
		policy = &extension.KubeletCPUManagerPolicy{}
	}
	n.KubeletCPUManagerPolicy = policy
}

func (n *nodeNUMAInfo) updateCPUsManagedByKubelet(podCPUAllocs extension.PodCPUAllocs) {
	for _, v := range podCPUAllocs {
		if !v.ManagedByKubelet || v.UID == "" || v.CPUSet == "" {
			continue
		}
		cpuset, err := Parse(v.CPUSet)
		if err != nil || cpuset.IsEmpty() {
			continue
		}
		n.allocateCPUs(v.UID, cpuset, schedulingconfig.CPUExclusivePolicyNone)
	}
}

func (n *nodeNUMAInfo) releaseCPUsManagedByKubelet(podCPUAllocs extension.PodCPUAllocs) {
	for _, v := range podCPUAllocs {
		if !v.ManagedByKubelet || v.UID == "" || v.CPUSet == "" {
			continue
		}
		cpuset, err := Parse(v.CPUSet)
		if err != nil || cpuset.IsEmpty() {
			continue
		}
		n.releaseCPUs(v.UID, cpuset)
	}
}

func (n *nodeNUMAInfo) allocateCPUs(podUID types.UID, cpuset CPUSet, exclusivePolicy schedulingconfig.CPUExclusivePolicy) {
	if _, ok := n.allocatedPods[podUID]; ok {
		return
	}
	n.allocatedPods[podUID] = struct{}{}

	for _, cpuID := range cpuset.ToSliceNoSort() {
		cpuInfo, ok := n.allocatedCPUs[cpuID]
		if !ok {
			cpuInfo = n.cpuTopology.CPUDetails[cpuID]
		}
		cpuInfo.ExclusivePolicy = exclusivePolicy
		cpuInfo.RefCount++
		n.allocatedCPUs[cpuID] = cpuInfo
	}
}

func (n *nodeNUMAInfo) releaseCPUs(podUID types.UID, cpuset CPUSet) {
	if _, ok := n.allocatedPods[podUID]; !ok {
		return
	}
	delete(n.allocatedPods, podUID)

	for _, cpuID := range cpuset.ToSliceNoSort() {
		cpuInfo, ok := n.allocatedCPUs[cpuID]
		if !ok {
			continue
		}
		cpuInfo.RefCount--
		if cpuInfo.RefCount == 0 {
			delete(n.allocatedCPUs, cpuID)
		} else {
			n.allocatedCPUs[cpuID] = cpuInfo
		}
	}
}
