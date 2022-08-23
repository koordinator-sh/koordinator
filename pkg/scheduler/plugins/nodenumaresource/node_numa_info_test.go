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
	"encoding/json"
	"testing"

	nrtv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/apis/scheduling/config"
)

func TestNUMAInfoCacheWithNodeResourceTopologyAddOrUpdateOrDelete(t *testing.T) {
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2)
	for _, v := range cpuTopology.CPUDetails {
		v.CoreID = v.SocketID<<16 | v.CoreID
		cpuTopology.CPUDetails[v.CPUID] = v
	}
	topology := &extension.CPUTopology{}
	for _, v := range cpuTopology.CPUDetails {
		topology.Detail = append(topology.Detail, extension.CPUInfo{
			ID:     int32(v.CPUID),
			Core:   int32(v.CoreID & 0xffff),
			Socket: int32(v.SocketID),
			Node:   int32(v.NodeID & 0xffff),
		})
	}
	data, err := json.Marshal(topology)
	assert.NoError(t, err)

	podCPUAllocs := extension.PodCPUAllocs{
		{
			UID:              uuid.NewUUID(),
			CPUSet:           "1-4",
			ManagedByKubelet: true,
		},
	}
	podCPUAllocsData, err := json.Marshal(podCPUAllocs)
	assert.NoError(t, err)

	cache := newNodeNUMAInfoCache()
	nrt := &nrtv1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
			Annotations: map[string]string{
				extension.AnnotationNodeCPUTopology: string(data),
				extension.AnnotationNodeCPUAllocs:   string(podCPUAllocsData),
			},
		},
	}
	cache.onNodeResourceTopologyAdd(nrt)
	numaInfo := cache.getNodeNUMAInfo("test-node-1")
	assert.NotNil(t, numaInfo)
	assert.Equal(t, "test-node-1", numaInfo.nodeName)
	assert.Equal(t, cpuTopology, numaInfo.cpuTopology)
	assert.NotNil(t, numaInfo.allocatedPods)
	assert.NotNil(t, numaInfo.allocatedCPUs)
	expectedAllocatedPods := map[types.UID]struct{}{
		podCPUAllocs[0].UID: {},
	}
	assert.Equal(t, expectedAllocatedPods, numaInfo.allocatedPods)

	expectedAllocatedCPUs := CPUDetails{}
	for _, cpuID := range []int{1, 2, 3, 4} {
		cpuInfo := numaInfo.cpuTopology.CPUDetails[cpuID]
		cpuInfo.ExclusivePolicy = schedulingconfig.CPUExclusivePolicyNone
		cpuInfo.RefCount++
		expectedAllocatedCPUs[cpuID] = cpuInfo
	}
	assert.Equal(t, expectedAllocatedCPUs, numaInfo.allocatedCPUs)

	newNRT := nrt.DeepCopy()
	newNRT.Labels = nil
	newNRT.Annotations = nil
	cache.onNodeResourceTopologyUpdate(nrt, newNRT)

	numaInfo = cache.getNodeNUMAInfo("test-node-1")
	assert.NotNil(t, numaInfo)
	assert.Equal(t, "test-node-1", numaInfo.nodeName)
	assert.Equal(t, &CPUTopology{CPUDetails: NewCPUDetails()}, numaInfo.cpuTopology)
	assert.NotNil(t, numaInfo.allocatedPods)
	assert.NotNil(t, numaInfo.allocatedCPUs)

	expectedAllocatedPods = map[types.UID]struct{}{}
	assert.Equal(t, expectedAllocatedPods, numaInfo.allocatedPods)
	expectedAllocatedCPUs = CPUDetails{}
	assert.Equal(t, expectedAllocatedCPUs, numaInfo.allocatedCPUs)

	cache.onNodeResourceTopologyDelete(newNRT)
	numaInfo = cache.getNodeNUMAInfo("test-node-1")
	assert.Nil(t, numaInfo)
}

func TestNUMAInfoCacheWithPodAddOrUpdateOrDelete(t *testing.T) {
	cpuTopology := buildCPUTopologyForTest(2, 1, 4, 2)
	for _, v := range cpuTopology.CPUDetails {
		v.CoreID = v.SocketID<<16 | v.CoreID
		cpuTopology.CPUDetails[v.CPUID] = v
	}
	topology := &extension.CPUTopology{}
	for _, v := range cpuTopology.CPUDetails {
		topology.Detail = append(topology.Detail, extension.CPUInfo{
			ID:     int32(v.CPUID),
			Core:   int32(v.CoreID & 0xffff),
			Socket: int32(v.SocketID),
			Node:   int32(v.NodeID & 0xffff),
		})
	}
	data, err := json.Marshal(topology)
	assert.NoError(t, err)

	cache := newNodeNUMAInfoCache()
	nrt := &nrtv1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
			Annotations: map[string]string{
				extension.AnnotationNodeCPUTopology: string(data),
			},
		},
	}
	cache.onNodeResourceTopologyAdd(nrt)
	numaInfo := cache.getNodeNUMAInfo("test-node-1")
	assert.NotNil(t, numaInfo)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-1",
			Annotations: map[string]string{
				extension.AnnotationResourceStatus: `{"cpuset": "0-3"}`,
			},
			UID: uuid.NewUUID(),
		},
	}
	cache.onPodAdd(pod)
	assert.Empty(t, numaInfo.allocatedPods)
	assert.Empty(t, numaInfo.allocatedCPUs)

	podClone := pod.DeepCopy()
	podClone.Spec.NodeName = "test-node-1"
	cache.onPodUpdate(pod, podClone)
	_, ok := numaInfo.allocatedPods[pod.UID]
	assert.True(t, ok)

	expectCPUDetails := NewCPUDetails()
	for _, v := range []int{0, 1, 2, 3} {
		info := cpuTopology.CPUDetails[v]
		info.RefCount++
		expectCPUDetails[v] = info
	}
	assert.Equal(t, expectCPUDetails, numaInfo.allocatedCPUs)

	podClone2 := podClone.DeepCopy()
	podClone2.Status.Phase = corev1.PodSucceeded
	cache.onPodUpdate(podClone, podClone2)
	assert.Empty(t, numaInfo.allocatedPods)
	assert.Empty(t, numaInfo.allocatedCPUs)

	cache.onPodUpdate(pod, podClone)
	_, ok = numaInfo.allocatedPods[pod.UID]
	assert.True(t, ok)
	cache.onPodDelete(podClone)
	assert.Empty(t, numaInfo.allocatedPods)
	assert.Empty(t, numaInfo.allocatedCPUs)
}
