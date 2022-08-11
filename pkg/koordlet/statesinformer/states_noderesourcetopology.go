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

package statesinformer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func (s *statesInformer) syncNodeResourceTopology(node *corev1.Node) {
	topologyName := node.Name
	ctx := context.TODO()
	blocker := true
	_, err := s.topologyClient.TopologyV1alpha1().NodeResourceTopologies().Get(ctx, topologyName, metav1.GetOptions{ResourceVersion: "0"})
	if err == nil {
		return
	}
	if !errors.IsNotFound(err) {
		klog.Errorf("failed to get NodeResourceTopology %s, err: %v", topologyName, err)
		return
	}

	topology := &v1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Name: topologyName,
			Labels: map[string]string{
				extension.LabelManagedBy: "Koordinator",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					Kind:               "Node",
					Name:               node.Name,
					UID:                node.GetUID(),
					Controller:         &blocker,
					BlockOwnerDeletion: &blocker,
				},
			},
		},
		// fields are required
		TopologyPolicies: []string{string(v1alpha1.None)},
		Zones:            v1alpha1.ZoneList{v1alpha1.Zone{Name: "fake-name", Type: "fake-type"}},
	}
	// TODO: add retry if create fail
	_, err = s.topologyClient.TopologyV1alpha1().NodeResourceTopologies().Create(ctx, topology, metav1.CreateOptions{})
	if err != nil {
		klog.Errorf("failed to create NodeResourceTopology %s, err: %v", topologyName, err)
		return
	}
}

func (s *statesInformer) calGuaranteedCpu(usedCPUs map[int32]*extension.CPUInfo, stateJson string) ([]extension.PodCPUAlloc, error) {
	if stateJson == "" {
		return nil, fmt.Errorf("empty state file")
	}
	checkPoint := &state.CPUManagerCheckpoint{}
	err := json.Unmarshal([]byte(stateJson), checkPoint)
	if err != nil {
		return nil, err
	}

	pods := make(map[types.UID]*PodMeta)
	managedPods := make(map[types.UID]struct{})
	for _, podMeta := range s.GetAllPods() {
		pods[podMeta.Pod.UID] = podMeta
		qosClass := extension.GetPodQoSClass(podMeta.Pod)
		if qosClass == extension.QoSLS || qosClass == extension.QoSBE {
			managedPods[podMeta.Pod.UID] = struct{}{}
			continue
		}
		resourceStatus, err := extension.GetResourceStatus(podMeta.Pod.Annotations)
		if err == nil {
			set, err := cpuset.Parse(resourceStatus.CPUSet)
			if err == nil && set.Size() > 0 {
				managedPods[podMeta.Pod.UID] = struct{}{}
			}
		}
	}

	var podAllocs []extension.PodCPUAlloc
	for podUID := range checkPoint.Entries {
		if _, ok := managedPods[types.UID(podUID)]; ok {
			continue
		}
		cpuSet := cpuset.NewCPUSet()
		for container, cpuString := range checkPoint.Entries[podUID] {
			if containerCPUSet, err := cpuset.Parse(cpuString); err != nil {
				klog.Errorf("could not parse cpuset %q for container %q in pod %q: %v", cpuString, container, podUID, err)
				continue
			} else if containerCPUSet.Size() > 0 {
				cpuSet = cpuSet.Union(containerCPUSet)
			}
		}
		if cpuSet.IsEmpty() {
			continue
		}

		// TODO: It is possible that the data in the checkpoint file is invalid
		//  and should be checked with the data in the cgroup to determine whether it is consistent
		podCPUAlloc := extension.PodCPUAlloc{
			UID:              types.UID(podUID),
			CPUSet:           cpuSet.String(),
			ManagedByKubelet: true,
		}
		podMeta := pods[types.UID(podUID)]
		if podMeta != nil {
			podCPUAlloc.Namespace = podMeta.Pod.Namespace
			podCPUAlloc.Name = podMeta.Pod.Name
		}
		podAllocs = append(podAllocs, podCPUAlloc)

		for _, cpuID := range cpuSet.ToSliceNoSort() {
			delete(usedCPUs, int32(cpuID))
		}
	}
	return podAllocs, nil
}

func (s *statesInformer) reportNodeTopology() {
	klog.Info("start to report node topology")
	s.nodeRWMutex.RLock()
	nodeName := s.node.Name
	s.nodeRWMutex.RUnlock()
	ctx := context.TODO()
	cpuTopology, usedCPUs, err := s.calCpuTopology()
	if err != nil {
		return
	}
	// TODO: support https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/
	cpuPolicy, stateFilePath, cpuManagerOpt, err := system.GuessCPUManagerOptFromKubelet()
	if err != nil {
		klog.Errorf("failed to guess kubelet cpu manager opt, err: %v", err)
	}
	cpuManagerPolicy := extension.KubeletCPUManagerPolicy{
		Policy:  cpuPolicy,
		Options: cpuManagerOpt,
	}
	cpuManagerPolicyJson, err := json.Marshal(cpuManagerPolicy)
	if err != nil {
		klog.Errorf("failed to marshal cpu manager policy, err: %v", err)
	}
	var podAllocsJson []byte
	if stateFilePath != "" {
		data, err := os.ReadFile(stateFilePath)
		if err != nil {
			klog.Errorf("failed to read file, err: %v", err)
		}
		podAllocs, err := s.calGuaranteedCpu(usedCPUs, string(data))
		if err != nil {
			klog.Errorf("failed to cal GuaranteedCpu, err: %v", err)
		}
		if len(podAllocs) != 0 {
			podAllocsJson, err = json.Marshal(podAllocs)
			if err != nil {
				klog.Errorf("failed to marshal pod allocs, err: %v", err)
			}
		}
	}
	// TODO: report lse/lsr pod from cgroup
	sharePools := s.calCPUSharePools(usedCPUs)

	cpuTopologyJson, err := json.Marshal(cpuTopology)
	if err != nil {
		klog.Errorf("failed to marshal cpu topology of node %s, err: %v", nodeName, err)
	}
	cpuSharePoolsJson, err := json.Marshal(sharePools)
	if err != nil {
		klog.Errorf("failed to marshal cpushare pools of node %s, err: %v", nodeName, err)
	}

	err = retry.OnError(retry.DefaultBackoff, errors.IsTooManyRequests, func() error {
		topology, err := s.topologyClient.TopologyV1alpha1().NodeResourceTopologies().Get(ctx, nodeName, metav1.GetOptions{ResourceVersion: "0"})
		if err != nil {
			klog.Errorf("failed to get node resource topology %s, err: %v", nodeName, err)
			return err
		}
		if topology.Annotations == nil {
			topology.Annotations = make(map[string]string)
		}
		// TODO only update if necessary
		s.updateNodeTopo(topology)
		topology.Annotations[extension.AnnotationNodeCPUTopology] = string(cpuTopologyJson)
		topology.Annotations[extension.AnnotationNodeCPUSharedPools] = string(cpuSharePoolsJson)
		topology.Annotations[extension.AnnotationKubeletCPUManagerPolicy] = string(cpuManagerPolicyJson)
		if len(podAllocsJson) != 0 {
			topology.Annotations[extension.AnnotationNodeCPUAllocs] = string(podAllocsJson)
		}
		_, err = s.topologyClient.TopologyV1alpha1().NodeResourceTopologies().Update(context.TODO(), topology, metav1.UpdateOptions{})
		if err != nil {
			klog.Errorf("failed to update cpu info of node %s, err: %v", nodeName, err)
			return err
		}
		return nil
	})
	if err != nil {
		klog.Errorf("failed to update NodeResourceTopology, err: %v", err)
	}
}

func (s *statesInformer) calCPUSharePools(usedCPUs map[int32]*extension.CPUInfo) []extension.CPUSharedPool {
	podMetas := s.GetAllPods()
	for _, podMeta := range podMetas {
		status, err := extension.GetResourceStatus(podMeta.Pod.Annotations)
		if err != nil {
			klog.Errorf("failed to get resource status of pod %s, err: %v", podMeta.Pod.Name, err)
			continue
		}
		if status.CPUSet == "" {
			continue
		}

		set, err := cpuset.Parse(status.CPUSet)
		if err != nil {
			klog.Errorf("failed to parse cpuset info of pod %s, err: %v", podMeta.Pod.Name, err)
			continue
		}
		for _, cpuID := range set.ToSliceNoSort() {
			delete(usedCPUs, int32(cpuID))
		}
	}

	// nodeID -> cpulist
	nodeIDToCpus := make(map[int32][]int)
	for cpuID, info := range usedCPUs {
		if info != nil {
			nodeIDToCpus[info.Node] = append(nodeIDToCpus[info.Node], int(cpuID))
		}
	}

	sharePools := []extension.CPUSharedPool{}
	for nodeID, cpus := range nodeIDToCpus {
		if len(cpus) <= 0 {
			continue
		}
		set := cpuset.NewCPUSet(cpus...)
		sharePools = append(sharePools, extension.CPUSharedPool{
			CPUSet: set.String(),
			Node:   nodeID,
			Socket: usedCPUs[int32(cpus[0])].Socket,
		})
	}
	return sharePools
}

func (s *statesInformer) calCpuTopology() (*extension.CPUTopology, map[int32]*extension.CPUInfo, error) {
	nodeCpuInfo, err := s.metricsCache.GetNodeCPUInfo(&metriccache.QueryParam{})
	if err != nil {
		klog.Errorf("failed to get node cpu info")
		return nil, nil, err
	}

	usedCPUs := make(map[int32]*extension.CPUInfo)
	cpuTopology := &extension.CPUTopology{}
	for _, cpu := range nodeCpuInfo.ProcessorInfos {
		info := extension.CPUInfo{
			ID:     cpu.CPUID,
			Core:   cpu.CoreID,
			Socket: cpu.SocketID,
			Node:   cpu.NodeID,
		}
		cpuTopology.Detail = append(cpuTopology.Detail, info)
		usedCPUs[cpu.CPUID] = &info
	}
	return cpuTopology, usedCPUs, nil
}

func (s *statesInformer) updateNodeTopo(newTopo *v1alpha1.NodeResourceTopology) {
	s.setNodeTopo(newTopo)
	klog.V(5).Infof("local node topology info updated %v", newTopo)
	s.sendCallbacks(RegisterTypeNodeTopology)
}

func (s *statesInformer) setNodeTopo(newTopo *v1alpha1.NodeResourceTopology) {
	s.nodeTopoMutex.Lock()
	defer s.nodeTopoMutex.Unlock()
	s.nodeTopology = newTopo
}

func (s *statesInformer) getNodeTopo() *v1alpha1.NodeResourceTopology {
	s.nodeTopoMutex.RLock()
	defer s.nodeTopoMutex.RUnlock()
	return s.nodeTopology.DeepCopy()
}
