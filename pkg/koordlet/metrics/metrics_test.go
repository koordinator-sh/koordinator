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

package metrics

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func TestGenNodeLabels(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test",
			Labels: map[string]string{},
		},
	}
	Register(node)
	defer Register(nil)
	labels := genNodeLabels()
	assert.Equal(t, 1, len(labels))
	assert.Equal(t, "test", labels[NodeKey])
	RecordCollectNodeCPUInfoStatus(nil)
}

func TestCommonCollectors(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("200"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("200"),
			},
		},
	}
	testingErr := fmt.Errorf("test error")
	testingNow := time.Now()
	testingContainer := &corev1.ContainerStatus{
		ContainerID: "containerd://1",
		Name:        "test_container",
	}
	testingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test_pod",
			Namespace: "test_pod_namespace",
			UID:       "test01",
		},
	}
	testingPSI := &system.PSIByResource{
		CPU: system.PSIStats{
			Some: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			Full: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			FullSupported: true,
		},
		Mem: system.PSIStats{
			Some: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			Full: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			FullSupported: true,
		},
		IO: system.PSIStats{
			Some: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			Full: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			FullSupported: true,
		},
	}

	t.Run("test not panic", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)

		RecordKoordletStartTime(testingNode.Name, float64(testingNow.Unix()))
		RecordCollectNodeCPUInfoStatus(testingErr)
		RecordCollectNodeCPUInfoStatus(nil)
		RecordCollectNodeNUMAInfoStatus(testingErr)
		RecordCollectNodeNUMAInfoStatus(nil)
		RecordCollectNodeLocalStorageInfoStatus(testingErr)
		RecordCollectNodeLocalStorageInfoStatus(nil)
		RecordBESuppressCores("cfsQuota", float64(1000))
		RecordBESuppressLSUsedCPU(1.0)
		RecordBESuppressBEUsedCPU(1.0)
		RecordNodeUsedCPU(2.0)
		RecordNodeUsedMemory(float64(1024))
		RecordContainerScaledCFSBurstUS(testingPod.Namespace, testingPod.Name, testingContainer.ContainerID, testingContainer.Name, 1000000)
		RecordContainerScaledCFSQuotaUS(testingPod.Namespace, testingPod.Name, testingContainer.ContainerID, testingContainer.Name, 1000000)
		RecordPodEviction(testingPod.Namespace, testingPod.Name, "evictByCPU")
		ResetContainerCPI()
		RecordContainerCPI(testingContainer, testingPod, 1, 1)
		ResetContainerPSI()
		RecordContainerPSI(testingContainer, testingPod, testingPSI)
		ResetPodPSI()
		RecordPodPSI(testingPod, testingPSI)
	})
}

func TestResourceSummaryCollectors(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
				apiext.MidCPU:         resource.MustParse("50000"),
				apiext.MidMemory:      resource.MustParse("80Gi"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
				apiext.MidCPU:         resource.MustParse("50000"),
				apiext.MidMemory:      resource.MustParse("80Gi"),
			},
		},
	}
	testingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test_pod",
			Namespace: "test_pod_namespace",
			UID:       "test01",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test_container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test_container",
					ContainerID: "containerd://testxxx",
				},
			},
		},
	}
	testingBatchPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test_batch_pod",
			Namespace: "test_batch_pod_namespace",
			UID:       "batch01",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test_batch_container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("1000"),
							apiext.BatchMemory: resource.MustParse("2Gi"),
						},
						Limits: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("1000"),
							apiext.BatchMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test_batch_container",
					ContainerID: "containerd://batchxxx",
				},
			},
		},
	}
	testingMidPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test_mid_pod",
			Namespace: "test_mid_pod_namespace",
			UID:       "mid01",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "test_mid_container",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.MidCPU:    resource.MustParse("1000"),
							apiext.MidMemory: resource.MustParse("2Gi"),
						},
						Limits: corev1.ResourceList{
							apiext.MidCPU:    resource.MustParse("1000"),
							apiext.MidMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test_mid_container",
					ContainerID: "containerd://midxxx",
				},
			},
		},
	}

	t.Run("test not panic", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)

		RecordNodeResourceAllocatable(string(apiext.BatchCPU), UnitInteger, float64(util.QuantityPtr(testingNode.Status.Allocatable[apiext.BatchCPU]).Value()))
		RecordNodeResourceAllocatable(string(apiext.BatchMemory), UnitByte, float64(util.QuantityPtr(testingNode.Status.Allocatable[apiext.BatchMemory]).Value()))
		RecordNodeResourceAllocatable(string(apiext.MidCPU), UnitInteger, float64(util.QuantityPtr(testingNode.Status.Allocatable[apiext.MidCPU]).Value()))
		RecordNodeResourceAllocatable(string(apiext.MidMemory), UnitByte, float64(util.QuantityPtr(testingNode.Status.Allocatable[apiext.MidMemory]).Value()))
		RecordContainerResourceRequests(string(corev1.ResourceCPU), UnitCore, &testingPod.Status.ContainerStatuses[0], testingPod, float64(testingPod.Spec.Containers[0].Resources.Requests.Cpu().Value()))
		RecordContainerResourceRequests(string(corev1.ResourceMemory), UnitByte, &testingPod.Status.ContainerStatuses[0], testingPod, float64(testingPod.Spec.Containers[0].Resources.Requests.Memory().Value()))
		RecordContainerResourceRequests(string(apiext.BatchCPU), UnitInteger, &testingBatchPod.Status.ContainerStatuses[0], testingBatchPod, float64(util.QuantityPtr(testingBatchPod.Spec.Containers[0].Resources.Requests[apiext.BatchCPU]).Value()))
		RecordContainerResourceRequests(string(apiext.BatchMemory), UnitByte, &testingBatchPod.Status.ContainerStatuses[0], testingBatchPod, float64(util.QuantityPtr(testingBatchPod.Spec.Containers[0].Resources.Requests[apiext.BatchMemory]).Value()))
		RecordContainerResourceRequests(string(apiext.MidCPU), UnitInteger, &testingMidPod.Status.ContainerStatuses[0], testingMidPod, float64(util.QuantityPtr(testingMidPod.Spec.Containers[0].Resources.Requests[apiext.MidCPU]).Value()))
		RecordContainerResourceRequests(string(apiext.MidMemory), UnitByte, &testingMidPod.Status.ContainerStatuses[0], testingMidPod, float64(util.QuantityPtr(testingMidPod.Spec.Containers[0].Resources.Requests[apiext.MidMemory]).Value()))
		RecordContainerResourceLimits(string(apiext.BatchCPU), UnitInteger, &testingBatchPod.Status.ContainerStatuses[0], testingBatchPod, float64(util.QuantityPtr(testingBatchPod.Spec.Containers[0].Resources.Limits[apiext.BatchCPU]).Value()))
		RecordContainerResourceLimits(string(apiext.BatchMemory), UnitByte, &testingBatchPod.Status.ContainerStatuses[0], testingBatchPod, float64(util.QuantityPtr(testingBatchPod.Spec.Containers[0].Resources.Limits[apiext.BatchMemory]).Value()))
		RecordContainerResourceLimits(string(apiext.MidCPU), UnitInteger, &testingMidPod.Status.ContainerStatuses[0], testingMidPod, float64(util.QuantityPtr(testingMidPod.Spec.Containers[0].Resources.Limits[apiext.MidCPU]).Value()))
		RecordContainerResourceLimits(string(apiext.MidMemory), UnitByte, &testingMidPod.Status.ContainerStatuses[0], testingMidPod, float64(util.QuantityPtr(testingMidPod.Spec.Containers[0].Resources.Limits[apiext.MidMemory]).Value()))

		ResetContainerResourceRequests()
		ResetContainerResourceLimits()
	})
}

func TestPredictorCollectors(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
		},
	}
	testNodeReclaimable := &corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("50"),
		corev1.ResourceMemory: resource.MustParse("200Gi"),
	}

	t.Run("test", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)
		RecordNodePredictedResourceReclaimable(string(corev1.ResourceCPU), UnitCore, "testPredictor", float64(testNodeReclaimable.Cpu().MilliValue())/1000)
		RecordNodePredictedResourceReclaimable(string(corev1.ResourceMemory), UnitByte, "testPredictor", float64(testNodeReclaimable.Memory().Value()))
	})
}

func TestCoreSchedCollector(t *testing.T) {
	testCoreSchedGroup := "test-core-sched-group"
	testCoreSchedCookie := uint64(2000000000)
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
		},
	}
	testingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			UID:       "xxxxxx",
			Labels: map[string]string{
				slov1alpha1.LabelCoreSchedGroupID: testCoreSchedGroup,
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test-container",
					ContainerID: "containerd://ccccccccc",
				},
			},
		},
	}
	t.Run("test", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)
		RecordContainerCoreSchedCookie(testingPod.Namespace, testingPod.Name, string(testingPod.UID),
			testingPod.Status.ContainerStatuses[0].Name, testingPod.Status.ContainerStatuses[0].ContainerID,
			testCoreSchedGroup, testCoreSchedCookie)
	})
}

func TestRuntimeHookCollector(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				apiext.BatchCPU:       resource.MustParse("50000"),
				apiext.BatchMemory:    resource.MustParse("80Gi"),
			},
		},
	}
	testErr := fmt.Errorf("expected error")
	t.Run("test", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)
		RecordRuntimeHookInvokedDurationMilliSeconds("testHook", "testStage", nil, 10.0)
		RecordRuntimeHookInvokedDurationMilliSeconds("testHook", "testStage", testErr, 5.0)
		RecordRuntimeHookReconcilerInvokedDurationMilliSeconds("pod", "cpu.cfs_quota_us", nil, 10.0)
		RecordRuntimeHookReconcilerInvokedDurationMilliSeconds("pod", "cpu.cfs_quota_us", testErr, 5.0)
	})
}

func TestHostApplicationCollectors(t *testing.T) {
	type resourceUsage struct {
		cpu float64
		mem float64
	}
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
	}
	testingHostApplication := &slov1alpha1.HostApplicationSpec{
		Name:     "test-app",
		QoS:      apiext.QoSBE,
		Priority: apiext.PriorityBatch,
	}
	testingResourceUsage := resourceUsage{
		cpu: 33740549972770,
		mem: 7806574592,
	}

	t.Run("test", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)

		RecordHostApplicationResourceUsage(string(corev1.ResourceCPU), testingHostApplication, testingResourceUsage.cpu)
		RecordHostApplicationResourceUsage(string(corev1.ResourceMemory), testingHostApplication, testingResourceUsage.mem)

		ResetHostApplicationResourceUsage()
	})
}
