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

package deviceshare

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

// BenchmarkPreFilter measures the cost of PreFilter for a pod requesting a whole GPU.
func BenchmarkPreFilter(b *testing.B) {
	const numNodes = 256
	nodes := makeGPUNodes(numNodes, 8)
	suit := newPluginTestSuit(b, nodes)
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	p, err := suit.proxyNew(ctx, getDefaultArgs(), suit.Framework)
	if err != nil {
		b.Fatalf("failed to create plugin: %v", err)
	}
	pl := p.(*Plugin)

	for i := 0; i < numNodes; i++ {
		pl.nodeDeviceCache.updateNodeDevice(fmt.Sprintf("node-%d", i), makeGPUDevice(fmt.Sprintf("node-%d", i), 8))
	}

	pod := makeGPUPod(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		_, status := pl.PreFilter(ctx, cycleState, pod, nil)
		if !status.IsSuccess() && !status.IsSkip() {
			b.Fatalf("PreFilter failed: %v", status)
		}
	}
}

// BenchmarkFilter_GPU measures the cost of Filter for a pod requesting a whole GPU
// against a node with 8 available GPUs.
func BenchmarkFilter_GPU(b *testing.B) {
	const numNodes = 256
	nodes := makeGPUNodes(numNodes, 8)
	suit := newPluginTestSuit(b, nodes)
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	p, err := suit.proxyNew(ctx, getDefaultArgs(), suit.Framework)
	if err != nil {
		b.Fatalf("failed to create plugin: %v", err)
	}
	pl := p.(*Plugin)

	for i := 0; i < numNodes; i++ {
		pl.nodeDeviceCache.updateNodeDevice(fmt.Sprintf("node-%d", i), makeGPUDevice(fmt.Sprintf("node-%d", i), 8))
	}

	pod := makeGPUPod(1)
	nodeInfo, err := suit.Framework.SnapshotSharedLister().NodeInfos().Get("node-0")
	if err != nil {
		b.Fatalf("failed to get node info: %v", err)
	}

	baseState, status := preparePod(pod, pl.gpuSharedResourceTemplatesCache, pl.gpuSharedResourceTemplatesMatchedResources)
	if !status.IsSuccess() {
		b.Fatalf("preparePod failed: %v", status)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		cycleState.Write(stateKey, baseState.Clone())
		filterStatus := pl.Filter(ctx, cycleState, pod, nodeInfo)
		if !filterStatus.IsSuccess() {
			b.Fatalf("Filter failed: %v", filterStatus)
		}
	}
}

// BenchmarkFilter_GPUShare measures the cost of Filter for a pod requesting a shared GPU slice (50 cores).
func BenchmarkFilter_GPUShare(b *testing.B) {
	const numNodes = 256
	nodes := makeGPUNodes(numNodes, 8)
	suit := newPluginTestSuit(b, nodes)
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	p, err := suit.proxyNew(ctx, getDefaultArgs(), suit.Framework)
	if err != nil {
		b.Fatalf("failed to create plugin: %v", err)
	}
	pl := p.(*Plugin)

	for i := 0; i < numNodes; i++ {
		pl.nodeDeviceCache.updateNodeDevice(fmt.Sprintf("node-%d", i), makeGPUDevice(fmt.Sprintf("node-%d", i), 8))
	}

	pod := makeGPUSharePod(50, 50)
	nodeInfo, err := suit.Framework.SnapshotSharedLister().NodeInfos().Get("node-0")
	if err != nil {
		b.Fatalf("failed to get node info: %v", err)
	}

	baseState, status := preparePod(pod, pl.gpuSharedResourceTemplatesCache, pl.gpuSharedResourceTemplatesMatchedResources)
	if !status.IsSuccess() {
		b.Fatalf("preparePod failed: %v", status)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		cycleState.Write(stateKey, baseState.Clone())
		filterStatus := pl.Filter(ctx, cycleState, pod, nodeInfo)
		if !filterStatus.IsSuccess() {
			b.Fatalf("Filter failed: %v", filterStatus)
		}
	}
}

// BenchmarkFilter_LargeCluster benchmarks Filter across a large cluster (1024 nodes, 8 GPUs each).
func BenchmarkFilter_LargeCluster(b *testing.B) {
	const numNodes = 1024
	nodes := makeGPUNodes(numNodes, 8)
	suit := newPluginTestSuit(b, nodes)
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	p, err := suit.proxyNew(ctx, getDefaultArgs(), suit.Framework)
	if err != nil {
		b.Fatalf("failed to create plugin: %v", err)
	}
	pl := p.(*Plugin)

	for i := 0; i < numNodes; i++ {
		pl.nodeDeviceCache.updateNodeDevice(fmt.Sprintf("node-%d", i), makeGPUDevice(fmt.Sprintf("node-%d", i), 8))
	}

	pod := makeGPUPod(1)
	nodeInfo, err := suit.Framework.SnapshotSharedLister().NodeInfos().Get("node-0")
	if err != nil {
		b.Fatalf("failed to get node info: %v", err)
	}

	baseState, status := preparePod(pod, pl.gpuSharedResourceTemplatesCache, pl.gpuSharedResourceTemplatesMatchedResources)
	if !status.IsSuccess() {
		b.Fatalf("preparePod failed: %v", status)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		cycleState.Write(stateKey, baseState.Clone())
		filterStatus := pl.Filter(ctx, cycleState, pod, nodeInfo)
		if !filterStatus.IsSuccess() {
			b.Fatalf("Filter failed: %v", filterStatus)
		}
	}
}

func makeGPUNodes(n, gpusPerNode int) []*corev1.Node {
	nodes := make([]*corev1.Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("96"),
					corev1.ResourceMemory: resource.MustParse("512Gi"),
					apiext.ResourceGPU:    *resource.NewQuantity(int64(gpusPerNode*100), resource.DecimalSI),
				},
			},
		}
	}
	return nodes
}

func makeGPUDevice(nodeName string, gpuCount int) *schedulingv1alpha1.Device {
	devices := make([]schedulingv1alpha1.DeviceInfo, gpuCount)
	for i := 0; i < gpuCount; i++ {
		devices[i] = schedulingv1alpha1.DeviceInfo{
			Type:   schedulingv1alpha1.GPU,
			Minor:  ptr.To(int32(i)),
			Health: true,
			UUID:   fmt.Sprintf("%s-gpu-%d", nodeName, i),
			Resources: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		}
	}
	return &schedulingv1alpha1.Device{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: devices,
		},
	}
}

func makeGPUPod(gpuCount int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bench-gpu-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "bench-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: *resource.NewQuantity(int64(gpuCount*100), resource.DecimalSI),
						},
					},
				},
			},
		},
	}
}

func makeGPUSharePod(corePercent, memRatioPercent int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bench-gpushare-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "bench-container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPUCore:        *resource.NewQuantity(int64(corePercent), resource.DecimalSI),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(int64(memRatioPercent), resource.DecimalSI),
						},
					},
				},
			},
		},
	}
}
