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

package arbitrator

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func createNode(name string, cpu, memory string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
			},
		},
	}
}

func createPod(uid string, name string, cpuReq string, cpuset string, gpuMinors string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:         types.UID(uid),
			Name:        name,
			Namespace:   "default",
			Annotations: make(map[string]string),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse(cpuReq),
						},
					},
				},
			},
		},
	}

	if cpuset != "" {
		pod.Annotations[apiext.AnnotationResourceStatus] = `{"cpuset":"` + cpuset + `"}`
	}

	if gpuMinors != "" {
		pod.Annotations[apiext.AnnotationDeviceAllocated] = `{"gpu":[`
		minors := []rune(gpuMinors)
		for i, m := range minors {
			if m == ',' {
				continue
			}
			pod.Annotations[apiext.AnnotationDeviceAllocated] += `{"minor":` + string(m) + `}`
			if i < len(minors)-1 {
				pod.Annotations[apiext.AnnotationDeviceAllocated] += ","
			}
		}
		pod.Annotations[apiext.AnnotationDeviceAllocated] += "]}"
	}

	return pod
}

var _ fwktype.SharedLister = &testSharedLister{}

type testSharedLister struct {
	nodes       []*corev1.Node
	nodeInfos   []fwktype.NodeInfo
	nodeInfoMap map[string]*framework.NodeInfo
}

func (f *testSharedLister) StorageInfos() fwktype.StorageInfoLister {
	return f
}

func (f *testSharedLister) IsPVCUsedByPods(key string) bool {
	return false
}

func (f *testSharedLister) NodeInfos() fwktype.NodeInfoLister {
	return f
}

func (f *testSharedLister) List() ([]fwktype.NodeInfo, error) {
	return f.nodeInfos, nil
}

func (f *testSharedLister) HavePodsWithAffinityList() ([]fwktype.NodeInfo, error) {
	return nil, nil
}

func (f *testSharedLister) HavePodsWithRequiredAntiAffinityList() ([]fwktype.NodeInfo, error) {
	return nil, nil
}

func (f *testSharedLister) Get(nodeName string) (fwktype.NodeInfo, error) {
	if info, ok := f.nodeInfoMap[nodeName]; ok {
		return info, nil
	}
	return nil, fmt.Errorf("node %s not found", nodeName)
}

type mockHandle struct {
	fwktype.Handle
	sharedLister *testSharedLister
}

func (m *mockHandle) SnapshotSharedLister() fwktype.SharedLister {
	return m.sharedLister
}

func TestArbitratorCache_ReserveAndConflict(t *testing.T) {
	cache := GetGlobalArbitratorCache()

	node := createNode("node-1", "8", "16Gi")
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	t.Run("Idempotent reservation", func(t *testing.T) {
		cache.Reset()
		pod := createPod("uid-1", "pod-1", "2", "", "")

		err := cache.ReserveProposal(pod, nodeInfo, "scheduler-1")
		if err != nil {
			t.Fatalf("unexpected error on first reserve: %v", err)
		}

		err = cache.ReserveProposal(pod, nodeInfo, "scheduler-1")
		if err != nil {
			t.Errorf("expected idempotent reserve to pass, but got: %v", err)
		}
	})

	t.Run("Standard CPU capacity conflict", func(t *testing.T) {
		cache.Reset()
		pod1 := createPod("uid-1", "pod-1", "6", "", "")
		pod2 := createPod("uid-2", "pod-2", "4", "", "")

		err := cache.ReserveProposal(pod1, nodeInfo, "scheduler-1")
		if err != nil {
			t.Fatalf("unexpected error reserving pod1: %v", err)
		}

		err = cache.ReserveProposal(pod2, nodeInfo, "scheduler-2")
		if err == nil {
			t.Errorf("expected CPU capacity conflict error, but got nil")
		}
	})

	t.Run("Standard CPU capacity conflict with bound pods", func(t *testing.T) {
		cache.Reset()
		boundPod := createPod("uid-bound", "pod-bound", "6", "", "")
		nodeInfoWithBound := framework.NewNodeInfo(boundPod)
		nodeInfoWithBound.SetNode(node)

		podNew := createPod("uid-new", "pod-new", "4", "", "")

		err := cache.ReserveProposal(podNew, nodeInfoWithBound, "scheduler-1")
		if err == nil {
			t.Errorf("expected CPU capacity conflict error due to bound pod, but got nil")
		}
	})

	t.Run("CPUSet allocation conflict", func(t *testing.T) {
		cache.Reset()
		pod1 := createPod("uid-1", "pod-1", "2", "0-3", "")
		pod2 := createPod("uid-2", "pod-2", "2", "2-5", "") // overlaps on CPUs 2 and 3
		pod3 := createPod("uid-3", "pod-3", "2", "4-7", "") // clean

		err := cache.ReserveProposal(pod1, nodeInfo, "scheduler-1")
		if err != nil {
			t.Fatalf("unexpected error reserving pod1: %v", err)
		}

		err = cache.ReserveProposal(pod2, nodeInfo, "scheduler-2")
		if err == nil {
			t.Errorf("expected CPUSet conflict error, but got nil")
		}

		err = cache.ReserveProposal(pod3, nodeInfo, "scheduler-2")
		if err != nil {
			t.Errorf("expected pod3 reserve to succeed, but got error: %v", err)
		}
	})

	t.Run("GPU minor allocation conflict", func(t *testing.T) {
		cache.Reset()
		pod1 := createPod("uid-1", "pod-1", "2", "", "0")
		pod2 := createPod("uid-2", "pod-2", "2", "", "0") // overlaps minor 0
		pod3 := createPod("uid-3", "pod-3", "2", "", "1") // clean

		err := cache.ReserveProposal(pod1, nodeInfo, "scheduler-1")
		if err != nil {
			t.Fatalf("unexpected error reserving pod1: %v", err)
		}

		err = cache.ReserveProposal(pod2, nodeInfo, "scheduler-2")
		if err == nil {
			t.Errorf("expected GPU minor conflict error, but got nil")
		}

		err = cache.ReserveProposal(pod3, nodeInfo, "scheduler-2")
		if err != nil {
			t.Errorf("expected pod3 reserve to succeed, but got error: %v", err)
		}
	})

	t.Run("Unreserve and rollback", func(t *testing.T) {
		cache.Reset()
		pod1 := createPod("uid-1", "pod-1", "6", "", "")
		pod2 := createPod("uid-2", "pod-2", "4", "", "")

		err := cache.ReserveProposal(pod1, nodeInfo, "scheduler-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		err = cache.ReserveProposal(pod2, nodeInfo, "scheduler-2")
		if err == nil {
			t.Errorf("expected conflict")
		}

		// Rollback pod1 reservation
		cache.ReleaseProposal(pod1.UID)

		// Reserving pod2 should now succeed
		err = cache.ReserveProposal(pod2, nodeInfo, "scheduler-2")
		if err != nil {
			t.Errorf("expected reserve to succeed after rollback, got error: %v", err)
		}
	})
}

func TestConflictArbitratorPlugin(t *testing.T) {
	cache := GetGlobalArbitratorCache()
	cache.Reset()

	node := createNode("node-1", "8", "16Gi")
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	lister := &testSharedLister{
		nodeInfos: []fwktype.NodeInfo{nodeInfo},
		nodeInfoMap: map[string]*framework.NodeInfo{
			"node-1": nodeInfo,
		},
	}
	handle := &mockHandle{
		sharedLister: lister,
	}

	plugin1Obj, err := New(context.Background(), nil, handle)
	if err != nil {
		t.Fatalf("failed to create plugin1: %v", err)
	}
	plugin1 := plugin1Obj.(*Plugin)
	plugin1.schedulerName = "scheduler-1"

	plugin2Obj, err := New(context.Background(), nil, handle)
	if err != nil {
		t.Fatalf("failed to create plugin2: %v", err)
	}
	plugin2 := plugin2Obj.(*Plugin)
	plugin2.schedulerName = "scheduler-2"

	t.Run("Plugin reserve and pre-bind lifecycle", func(t *testing.T) {
		cache.Reset()
		pod1 := createPod("uid-1", "pod-1", "6", "", "")
		pod2 := createPod("uid-2", "pod-2", "4", "", "")

		state := framework.NewCycleState()

		// Reserve pod1 via plugin1 (succeeds)
		status := plugin1.Reserve(context.Background(), state, pod1, "node-1")
		if !status.IsSuccess() {
			t.Fatalf("expected reserve success, got status: %v", status)
		}

		// Reserve pod2 via plugin2 (fails due to CPU capacity conflict)
		status = plugin2.Reserve(context.Background(), state, pod2, "node-1")
		if status.IsSuccess() {
			t.Errorf("expected reserve to fail due to conflict")
		}

		// PreBind pod1 via plugin1 (succeeds because scheduler-1 owns reservation)
		status = plugin1.PreBind(context.Background(), state, pod1, "node-1")
		if !status.IsSuccess() {
			t.Errorf("expected PreBind success, got: %v", status)
		}

		// PreBind pod1 via plugin2 (fails because scheduler-2 does not own reservation)
		status = plugin2.PreBind(context.Background(), state, pod1, "node-1")
		if status.IsSuccess() {
			t.Errorf("expected PreBind to fail due to ownership mismatch")
		}

		// Unreserve pod1 via plugin1
		plugin1.Unreserve(context.Background(), state, pod1, "node-1")

		// Now pod2 reserve should succeed
		status = plugin2.Reserve(context.Background(), state, pod2, "node-1")
		if !status.IsSuccess() {
			t.Errorf("expected reserve success after unreserve, got: %v", status)
		}
	})
}

func TestConflictArbitratorRPC(t *testing.T) {
	cache := GetGlobalArbitratorCache()
	cache.Reset()

	node := createNode("node-1", "8", "16Gi")
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	lister := &testSharedLister{
		nodeInfos: []fwktype.NodeInfo{nodeInfo},
		nodeInfoMap: map[string]*framework.NodeInfo{
			"node-1": nodeInfo,
		},
	}
	handle := &mockHandle{
		sharedLister: lister,
	}

	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Start RPC server
	StartArbitratorRPCServer(strconv.Itoa(port), handle, cache)
	// Allow a moment for the server to start
	time.Sleep(100 * time.Millisecond)

	// Set env vars for clients
	os.Setenv("KOORD_ARBITRATOR_ROLE", "client")
	os.Setenv("KOORD_ARBITRATOR_URL", fmt.Sprintf("http://127.0.0.1:%d", port))
	defer func() {
		os.Unsetenv("KOORD_ARBITRATOR_ROLE")
		os.Unsetenv("KOORD_ARBITRATOR_URL")
	}()

	plugin1Obj, err := New(context.Background(), nil, handle)
	if err != nil {
		t.Fatalf("failed to create plugin1: %v", err)
	}
	plugin1 := plugin1Obj.(*Plugin)
	plugin1.schedulerName = "scheduler-1"

	plugin2Obj, err := New(context.Background(), nil, handle)
	if err != nil {
		t.Fatalf("failed to create plugin2: %v", err)
	}
	plugin2 := plugin2Obj.(*Plugin)
	plugin2.schedulerName = "scheduler-2"

	t.Run("RPC Client-Server reserve and pre-bind lifecycle", func(t *testing.T) {
		cache.Reset()
		pod1 := createPod("uid-1", "pod-1", "6", "", "")
		pod2 := createPod("uid-2", "pod-2", "4", "", "")

		state := framework.NewCycleState()

		// Reserve pod1 via plugin1 (succeeds via RPC)
		status := plugin1.Reserve(context.Background(), state, pod1, "node-1")
		if status != nil && !status.IsSuccess() {
			t.Fatalf("expected RPC reserve success, got status: %v", status)
		}

		// Reserve pod2 via plugin2 (fails due to CPU capacity conflict via RPC)
		status = plugin2.Reserve(context.Background(), state, pod2, "node-1")
		if status == nil || status.IsSuccess() {
			t.Errorf("expected RPC reserve to fail due to conflict")
		}

		// PreBind pod1 via plugin1 (succeeds via RPC)
		status = plugin1.PreBind(context.Background(), state, pod1, "node-1")
		if status != nil && !status.IsSuccess() {
			t.Errorf("expected RPC PreBind success, got: %v", status)
		}

		// PreBind pod1 via plugin2 (fails due to ownership mismatch via RPC)
		status = plugin2.PreBind(context.Background(), state, pod1, "node-1")
		if status == nil || status.IsSuccess() {
			t.Errorf("expected RPC PreBind to fail due to ownership mismatch")
		}

		// Unreserve pod1 via plugin1
		plugin1.Unreserve(context.Background(), state, pod1, "node-1")

		// Now pod2 reserve should succeed
		status = plugin2.Reserve(context.Background(), state, pod2, "node-1")
		if status != nil && !status.IsSuccess() {
			t.Errorf("expected RPC reserve success after unreserve, got: %v", status)
		}
	})
}
