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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func Test_newNodeDevice(t *testing.T) {
	expectNodeDevice := &nodeDevice{
		deviceTotal:   map[schedulingv1alpha1.DeviceType]deviceResources{},
		deviceFree:    map[schedulingv1alpha1.DeviceType]deviceResources{},
		deviceUsed:    map[schedulingv1alpha1.DeviceType]deviceResources{},
		vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
		numaTopology:  &NUMATopology{},
		allocateSet:   map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
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
						Minor:  ptr.To[int32](1),
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

func Test_nodeDevice_calcFreeWithPreemptible(t *testing.T) {
	tests := []struct {
		name                    string
		deviceFree              map[schedulingv1alpha1.DeviceType]deviceResources
		requiredDeviceResources deviceResources
		wantFree                deviceResources
		wantOriginalFree        map[schedulingv1alpha1.DeviceType]deviceResources
	}{
		{
			name: "assure free not changed",
			deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					1: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					2: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					3: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					4: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					5: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					6: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					7: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
				},
			},
			requiredDeviceResources: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
				},
			},
			wantFree: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
				},
			},
			wantOriginalFree: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					1: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					2: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					3: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					4: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					5: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					6: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					7: corev1.ResourceList{
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &nodeDevice{
				deviceFree: tt.deviceFree,
			}
			assert.Equal(t, tt.wantFree, n.calcFreeWithPreemptible(schedulingv1alpha1.GPU, nil, tt.requiredDeviceResources))
			assert.Equal(t, tt.wantOriginalFree, n.deviceFree)
		})
	}
}
