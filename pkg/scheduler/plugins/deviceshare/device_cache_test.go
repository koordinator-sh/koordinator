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
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func Test_newNodeDeviceCache(t *testing.T) {
	expectNodeDeviceCache := &nodeDeviceCache{
		nodeDeviceInfos: map[string]*nodeDevice{},
	}
	assert.Equal(t, expectNodeDeviceCache, newNodeDeviceCache())
}

func Test_newNodeDevice(t *testing.T) {
	expectNodeDevice := &nodeDevice{
		deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{},
		deviceFree:  map[schedulingv1alpha1.DeviceType]deviceResources{},
		deviceUsed:  map[schedulingv1alpha1.DeviceType]deviceResources{},
		allocateSet: map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
	}
	assert.Equal(t, expectNodeDevice, newNodeDevice())
}

func Test_nodeDevice_getUsed(t *testing.T) {
	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}
	nd := newNodeDevice()
	nd.updateCacheUsed(allocations, pod, true)
	used := nd.getUsed(pod.Namespace, pod.Name)
	expectUsed := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
	}
	assert.Equal(t, expectUsed, used)
}

func Test_nodeDevice_replaceWith(t *testing.T) {
	nd := newNodeDevice()
	nd.resetDeviceTotal(map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
		schedulingv1alpha1.RDMA: {
			1: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
		},
	})

	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 2,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("5Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}
	nd.updateCacheUsed(allocations, pod, true)

	nnd := nd.replaceWith(map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemory:      resource.MustParse("3Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
	})
	expectTotal := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
		schedulingv1alpha1.RDMA: {
			1: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
		},
	}
	assert.Equal(t, expectTotal, nnd.deviceTotal)

	expectUsed := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemory:      resource.MustParse("5Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectUsed, nnd.deviceUsed))

	expectFree := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemory:      resource.MustParse("3Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
		schedulingv1alpha1.RDMA: {
			1: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectFree, nnd.deviceFree))
}

func Test_nodeDevice_allocateGPU(t *testing.T) {
	nd := newNodeDevice()
	nd.resetDeviceTotal(map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
		schedulingv1alpha1.RDMA: {
			1: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
		},
	})

	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
			{
				Minor: 2,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}
	nd.updateCacheUsed(allocations, pod, true)

	podRequests := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("50"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
	}
	preemptible := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
	}
	allocateResult, err := nd.tryAllocateDevice(podRequests, nil, nil, nil, preemptible, nil)
	assert.NoError(t, err)
	expectAllocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult))

	podRequests = corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("200"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
	}
	allocateResult, err = nd.tryAllocateDevice(podRequests, nil, nil, nil, preemptible, nil)
	assert.NoError(t, err)
	expectAllocations = allocations
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult))
}

func Test_nodeDevice_allocateGPUWithLeastAllocatedScorer(t *testing.T) {
	nd := newNodeDevice()
	nd.resetDeviceTotal(map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			3: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			4: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
	})

	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
			{
				Minor: 2,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}
	nd.updateCacheUsed(allocations, pod, true)

	podRequests := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("50"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
	}
	allocateResult := apiext.DeviceAllocations{}
	args := getDefaultArgs()
	args.ScoringStrategy.Type = schedulerconfig.LeastAllocated
	allocationScorer := deviceResourceStrategyTypeMap[args.ScoringStrategy.Type](args)
	err := nd.tryAllocateByDeviceType(podRequests, 2, schedulingv1alpha1.GPU, nil, nil, allocateResult, nil, nil, allocationScorer)
	assert.NoError(t, err)
	expectAllocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 3,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
			{
				Minor: 4,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult))
}

func Test_nodeDevice_allocateGPUWithMostAllocatedScorer(t *testing.T) {
	nd := newNodeDevice()
	nd.resetDeviceTotal(map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			3: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			4: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
	})

	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 3,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
			{
				Minor: 4,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}
	nd.updateCacheUsed(allocations, pod, true)

	podRequests := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("50"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
	}
	allocateResult := apiext.DeviceAllocations{}
	args := getDefaultArgs()
	args.ScoringStrategy.Type = schedulerconfig.MostAllocated
	allocationScorer := deviceResourceStrategyTypeMap[args.ScoringStrategy.Type](args)
	err := nd.tryAllocateByDeviceType(podRequests, 2, schedulingv1alpha1.GPU, nil, nil, allocateResult, nil, nil, allocationScorer)
	assert.NoError(t, err)
	expectAllocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 3,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
			{
				Minor: 4,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult))
}

func Test_nodeDevice_failedPreemptGPUFromReservation(t *testing.T) {
	nd := newNodeDevice()
	nd.resetDeviceTotal(map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
		schedulingv1alpha1.RDMA: {
			1: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
		},
	})

	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("200"),
					apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
				},
			},
			{
				Minor: 2,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("200"),
					apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}
	nd.updateCacheUsed(allocations, pod, true)

	podRequests := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("50"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
	}
	preemptible := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
	}
	allocateResult, err := nd.tryAllocateDevice(podRequests, nil, nil, nil, preemptible, nil)
	assert.EqualError(t, err, fmt.Sprintf("node does not have enough %v", schedulingv1alpha1.GPU))
	assert.Nil(t, allocateResult)
}

func Test_nodeDevice_allocateGPUWithUnhealthyInstance(t *testing.T) {
	nd := newNodeDevice()
	nd.resetDeviceTotal(map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{}, // mock unhealthy state
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
	})

	podRequests := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("50"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
	}
	allocateResult, err := nd.tryAllocateDevice(podRequests, nil, nil, nil, nil, nil)
	assert.NoError(t, err)
	expectAllocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 2,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult))
}

func Test_nodeDevice_allocateRDMA(t *testing.T) {
	nd := newNodeDevice()
	nd.resetDeviceTotal(map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			1: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
		schedulingv1alpha1.RDMA: {
			1: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
		},
	})

	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.RDMA: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceRDMA: resource.MustParse("100"),
				},
			},
			{
				Minor: 2,
				Resources: corev1.ResourceList{
					apiext.ResourceRDMA: resource.MustParse("100"),
				},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
	}
	nd.updateCacheUsed(allocations, pod, true)

	podRequests := corev1.ResourceList{
		apiext.ResourceRDMA: resource.MustParse("50"),
	}
	allocateResult := apiext.DeviceAllocations{}
	preemptible := deviceResources{
		1: corev1.ResourceList{
			apiext.ResourceRDMA: resource.MustParse("100"),
		},
		2: corev1.ResourceList{
			apiext.ResourceRDMA: resource.MustParse("100"),
		},
	}
	err := nd.tryAllocateByDeviceType(podRequests, 1, schedulingv1alpha1.RDMA, nil, nil, allocateResult, nil, preemptible, nil)
	assert.NoError(t, err)
	expectAllocations := apiext.DeviceAllocations{
		schedulingv1alpha1.RDMA: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceRDMA: resource.MustParse("50"),
				},
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult))

	podRequests = corev1.ResourceList{
		apiext.ResourceRDMA: resource.MustParse("100"),
	}
	allocateResult = apiext.DeviceAllocations{}
	err = nd.tryAllocateByDeviceType(podRequests, 2, schedulingv1alpha1.RDMA, nil, nil, allocateResult, nil, preemptible, nil)
	assert.NoError(t, err)
	expectAllocations = allocations
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult))
}

func Test_gcNodeDevices(t *testing.T) {
	cache := newNodeDeviceCache()
	fakeClient := kubefake.NewSimpleClientset()
	expectedNodeNames := sets.NewString()
	for i := 0; i < 10; i++ {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
			},
		}
		_, err := fakeClient.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
		assert.NoError(t, err)
		device := &schedulingv1alpha1.Device{
			ObjectMeta: metav1.ObjectMeta{
				Name: node.Name,
			},
		}
		cache.updateNodeDevice(node.Name, device)
		expectedNodeNames.Insert(node.Name)
	}

	for i := 0; i < 3; i++ {
		device := &schedulingv1alpha1.Device{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("invalid-node-%d", i),
			},
			Spec: schedulingv1alpha1.DeviceSpec{
				Devices: []schedulingv1alpha1.DeviceInfo{
					{
						Type:   schedulingv1alpha1.GPU,
						Minor:  pointer.Int32(1),
						Health: true,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
		}
		cache.updateNodeDevice(device.Name, device)
		info := cache.getNodeDevice(device.Name, false)
		assert.NotNil(t, info)
		total := buildDeviceResources(device)
		assert.Equal(t, total, info.deviceTotal)
		cache.invalidateNodeDevice(device)
		for _, v := range total {
			for k := range v {
				v[k] = make(corev1.ResourceList)
			}
		}
		assert.Equal(t, total, info.deviceTotal)
	}

	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	ctx, cancel := context.WithTimeout(context.TODO(), 1*time.Second)
	defer cancel()
	cache.gcNodeDevice(ctx, informerFactory, defaultGCPeriod)
	nodeNames := sets.StringKeySet(cache.nodeDeviceInfos)
	assert.Equal(t, expectedNodeNames, nodeNames)
}
