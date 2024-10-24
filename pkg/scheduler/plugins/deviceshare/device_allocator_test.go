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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

var (
	gpuResourceList = corev1.ResourceList{
		apiext.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
		apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
		apiext.ResourceGPUMemory:      *resource.NewQuantity(85198045184, resource.BinarySI),
	}

	gpuSharedResourceList = corev1.ResourceList{
		apiext.ResourceGPUCore:        *resource.NewQuantity(50, resource.DecimalSI),
		apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
		apiext.ResourceGPUMemory:      *resource.NewQuantity(42599022592, resource.BinarySI),
	}

	fakeDeviceCR = func() *schedulingv1alpha1.Device {
		var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"annotations":{}},"spec":{"devices":[{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:1f:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:1f:00.2"},{"minor":1,"busID":"0000:1f:00.3"},{"minor":2,"busID":"0000:1f:00.4"},{"minor":3,"busID":"0000:1f:00.5"},{"minor":4,"busID":"0000:1f:00.6"},{"minor":5,"busID":"0000:1f:00.7"},{"minor":6,"busID":"0000:1f:01.0"},{"minor":7,"busID":"0000:1f:01.1"},{"minor":8,"busID":"0000:1f:01.2"},{"minor":9,"busID":"0000:1f:01.3"},{"minor":10,"busID":"0000:1f:01.4"},{"minor":11,"busID":"0000:1f:01.5"},{"minor":12,"busID":"0000:1f:01.6"},{"minor":13,"busID":"0000:1f:01.7"},{"minor":14,"busID":"0000:1f:02.0"},{"minor":15,"busID":"0000:1f:02.1"},{"minor":16,"busID":"0000:1f:02.2"},{"minor":17,"busID":"0000:1f:02.3"},{"minor":18,"busID":"0000:1f:02.4"},{"minor":19,"busID":"0000:1f:02.5"},{"minor":20,"busID":"0000:1f:02.6"},{"minor":21,"busID":"0000:1f:02.7"},{"minor":22,"busID":"0000:1f:03.0"},{"minor":23,"busID":"0000:1f:03.1"},{"minor":24,"busID":"0000:1f:03.2"},{"minor":25,"busID":"0000:1f:03.3"},{"minor":26,"busID":"0000:1f:03.4"},{"minor":27,"busID":"0000:1f:03.5"},{"minor":28,"busID":"0000:1f:03.6"},{"minor":29,"busID":"0000:1f:03.7"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:90:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:90:00.2"},{"minor":1,"busID":"0000:90:00.3"},{"minor":2,"busID":"0000:90:00.4"},{"minor":3,"busID":"0000:90:00.5"},{"minor":4,"busID":"0000:90:00.6"},{"minor":5,"busID":"0000:90:00.7"},{"minor":6,"busID":"0000:90:01.0"},{"minor":7,"busID":"0000:90:01.1"},{"minor":8,"busID":"0000:90:01.2"},{"minor":9,"busID":"0000:90:01.3"},{"minor":10,"busID":"0000:90:01.4"},{"minor":11,"busID":"0000:90:01.5"},{"minor":12,"busID":"0000:90:01.6"},{"minor":13,"busID":"0000:90:01.7"},{"minor":14,"busID":"0000:90:02.0"},{"minor":15,"busID":"0000:90:02.1"},{"minor":16,"busID":"0000:90:02.2"},{"minor":17,"busID":"0000:90:02.3"},{"minor":18,"busID":"0000:90:02.4"},{"minor":19,"busID":"0000:90:02.5"},{"minor":20,"busID":"0000:90:02.6"},{"minor":21,"busID":"0000:90:02.7"},{"minor":22,"busID":"0000:90:03.0"},{"minor":23,"busID":"0000:90:03.1"},{"minor":24,"busID":"0000:90:03.2"},{"minor":25,"busID":"0000:90:03.3"},{"minor":26,"busID":"0000:90:03.4"},{"minor":27,"busID":"0000:90:03.5"},{"minor":28,"busID":"0000:90:03.6"},{"minor":29,"busID":"0000:90:03.7"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:51:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:51:00.2"},{"minor":1,"busID":"0000:51:00.3"},{"minor":2,"busID":"0000:51:00.4"},{"minor":3,"busID":"0000:51:00.5"},{"minor":4,"busID":"0000:51:00.6"},{"minor":5,"busID":"0000:51:00.7"},{"minor":6,"busID":"0000:51:01.0"},{"minor":7,"busID":"0000:51:01.1"},{"minor":8,"busID":"0000:51:01.2"},{"minor":9,"busID":"0000:51:01.3"},{"minor":10,"busID":"0000:51:01.4"},{"minor":11,"busID":"0000:51:01.5"},{"minor":12,"busID":"0000:51:01.6"},{"minor":13,"busID":"0000:51:01.7"},{"minor":14,"busID":"0000:51:02.0"},{"minor":15,"busID":"0000:51:02.1"},{"minor":16,"busID":"0000:51:02.2"},{"minor":17,"busID":"0000:51:02.3"},{"minor":18,"busID":"0000:51:02.4"},{"minor":19,"busID":"0000:51:02.5"},{"minor":20,"busID":"0000:51:02.6"},{"minor":21,"busID":"0000:51:02.7"},{"minor":22,"busID":"0000:51:03.0"},{"minor":23,"busID":"0000:51:03.1"},{"minor":24,"busID":"0000:51:03.2"},{"minor":25,"busID":"0000:51:03.3"},{"minor":26,"busID":"0000:51:03.4"},{"minor":27,"busID":"0000:51:03.5"},{"minor":28,"busID":"0000:51:03.6"},{"minor":29,"busID":"0000:51:03.7"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:b9:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:b9:00.2"},{"minor":1,"busID":"0000:b9:00.3"},{"minor":2,"busID":"0000:b9:00.4"},{"minor":3,"busID":"0000:b9:00.5"},{"minor":4,"busID":"0000:b9:00.6"},{"minor":5,"busID":"0000:b9:00.7"},{"minor":6,"busID":"0000:b9:01.0"},{"minor":7,"busID":"0000:b9:01.1"},{"minor":8,"busID":"0000:b9:01.2"},{"minor":9,"busID":"0000:b9:01.3"},{"minor":10,"busID":"0000:b9:01.4"},{"minor":11,"busID":"0000:b9:01.5"},{"minor":12,"busID":"0000:b9:01.6"},{"minor":13,"busID":"0000:b9:01.7"},{"minor":14,"busID":"0000:b9:02.0"},{"minor":15,"busID":"0000:b9:02.1"},{"minor":16,"busID":"0000:b9:02.2"},{"minor":17,"busID":"0000:b9:02.3"},{"minor":18,"busID":"0000:b9:02.4"},{"minor":19,"busID":"0000:b9:02.5"},{"minor":20,"busID":"0000:b9:02.6"},{"minor":21,"busID":"0000:b9:02.7"},{"minor":22,"busID":"0000:b9:03.0"},{"minor":23,"busID":"0000:b9:03.1"},{"minor":24,"busID":"0000:b9:03.2"},{"minor":25,"busID":"0000:b9:03.3"},{"minor":26,"busID":"0000:b9:03.4"},{"minor":27,"busID":"0000:b9:03.5"},{"minor":28,"busID":"0000:b9:03.6"},{"minor":29,"busID":"0000:b9:03.7"}]}]},{"type":"gpu","id":"GPU-8c25ea37-2909-6e62-b7bf-e2fcadebea8d","minor":0,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"}},{"type":"gpu","id":"GPU-befd76c3-8a36-7b8a-179c-eae75aa7d9f2","minor":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"}},{"type":"gpu","id":"GPU-87a9047b-dade-e08c-c067-7fedfd2e2750","minor":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"}},{"type":"gpu","id":"GPU-44a68f77-c18d-85a6-5425-e314c0e8e182","minor":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"}},{"type":"gpu","id":"GPU-ac53dc25-2cb7-a11d-417f-ce23331dcea0","minor":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"}},{"type":"gpu","id":"GPU-3908dbfd-6e0b-013d-549b-fca246a16fa0","minor":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"}},{"type":"gpu","id":"GPU-7a87e98a-a1a7-28bc-c880-28c870bf0c7d","minor":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"}},{"type":"gpu","id":"GPU-c3b7de0e-8a41-9bdb-3f71-8175c3438890","minor":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"}}]},"status":{}}`)
		var device schedulingv1alpha1.Device
		err := json.Unmarshal(data, &device)
		if err != nil {
			panic(err)
		}
		return &device
	}()

	fakeDeviceCRWithoutTopology = func() *schedulingv1alpha1.Device {
		var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"anntations":{}},"spec":{"devices":[{"type":"rdma","id":"0000:1f:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"}},{"type":"rdma","id":"0000:90:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"}},{"type":"rdma","id":"0000:51:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"}},{"type":"rdma","id":"0000:b9:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"}},{"type":"gpu","id":"GPU-8c25ea37-2909-6e62-b7bf-e2fcadebea8d","minor":0,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"}},{"type":"gpu","id":"GPU-befd76c3-8a36-7b8a-179c-eae75aa7d9f2","minor":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"}},{"type":"gpu","id":"GPU-87a9047b-dade-e08c-c067-7fedfd2e2750","minor":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"}},{"type":"gpu","id":"GPU-44a68f77-c18d-85a6-5425-e314c0e8e182","minor":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"}},{"type":"gpu","id":"GPU-ac53dc25-2cb7-a11d-417f-ce23331dcea0","minor":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"}},{"type":"gpu","id":"GPU-3908dbfd-6e0b-013d-549b-fca246a16fa0","minor":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"}},{"type":"gpu","id":"GPU-7a87e98a-a1a7-28bc-c880-28c870bf0c7d","minor":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"}},{"type":"gpu","id":"GPU-c3b7de0e-8a41-9bdb-3f71-8175c3438890","minor":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"}}]},"status":{}}`)
		var device schedulingv1alpha1.Device
		err := json.Unmarshal(data, &device)
		if err != nil {
			panic(err)
		}
		return &device
	}()
)

func allocateHintWithVFType(vfType string) func(hints apiext.DeviceAllocateHints) {
	return func(hints apiext.DeviceAllocateHints) {
		rdmaHint := hints[schedulingv1alpha1.RDMA]
		if rdmaHint == nil {
			rdmaHint = &apiext.DeviceHint{}
			hints[schedulingv1alpha1.RDMA] = rdmaHint
		}
		rdmaHint.VFSelector = &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      "type",
					Operator: metav1.LabelSelectorOpIn,
					Values: []string{
						"general",
						vfType,
					},
				},
			},
		}
	}
}

func allocateHintWithExclusivePolicy(deviceType schedulingv1alpha1.DeviceType, policy apiext.DeviceExclusivePolicy) func(hints apiext.DeviceAllocateHints) {
	return func(hints apiext.DeviceAllocateHints) {
		hint := hints[deviceType]
		if hint == nil {
			hint = &apiext.DeviceHint{}
			hints[deviceType] = hint
		}
		hint.ExclusivePolicy = policy
	}
}

func allocateHintWithAllocateStrategy(deviceType schedulingv1alpha1.DeviceType, strategy apiext.DeviceAllocateStrategy) func(hints apiext.DeviceAllocateHints) {
	return func(hints apiext.DeviceAllocateHints) {
		hint := hints[deviceType]
		if hint == nil {
			hint = &apiext.DeviceHint{}
			hints[deviceType] = hint
		}
		hint.AllocateStrategy = strategy
	}
}

func setDefaultTestAllocateHints(t *testing.T, pod *corev1.Pod, options ...func(hints apiext.DeviceAllocateHints)) {
	hints := apiext.DeviceAllocateHints{
		schedulingv1alpha1.RDMA: {
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"type": "fakeW",
				},
			},
		},
	}
	for _, opt := range options {
		opt(hints)
	}
	assert.NoError(t, apiext.SetDeviceAllocateHints(pod, hints))
}

func setDefaultTestDeviceJointAllocate(t *testing.T, pod *corev1.Pod, options ...func(allocate *apiext.DeviceJointAllocate)) {
	jointAllocate := &apiext.DeviceJointAllocate{
		DeviceTypes: []schedulingv1alpha1.DeviceType{
			schedulingv1alpha1.GPU,
			schedulingv1alpha1.RDMA,
		},
	}
	for _, opt := range options {
		opt(jointAllocate)
	}
	assert.NoError(t, apiext.SetDeviceJointAllocate(pod, jointAllocate))
}

func TestAutopilotAllocator(t *testing.T) {
	tests := []struct {
		name                       string
		deviceCR                   *schedulingv1alpha1.Device
		gpuWanted                  int
		rdmaWanted                 int
		hostNetwork                bool
		secondaryDeviceWellPlanned bool
		assignedDevices            apiext.DeviceAllocations
		want                       apiext.DeviceAllocations
		wantErr                    bool
	}{
		{
			name:      "request 1 GPU and 1 VF but invalid Device Topology",
			deviceCR:  fakeDeviceCRWithoutTopology,
			gpuWanted: 1,
			want:      nil,
			wantErr:   true,
		},
		{
			name:      "allocate 0 GPU and 1 VF but invalid Device Topology",
			deviceCR:  fakeDeviceCRWithoutTopology,
			gpuWanted: 0,
			want:      nil,
			wantErr:   true,
		},
		{
			name:      "allocate 0 GPU and 1 VF",
			deviceCR:  fakeDeviceCR,
			gpuWanted: 0,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:      "allocate 1 GPU and 1 VF",
			deviceCR:  fakeDeviceCR,
			gpuWanted: 1,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:      "allocate 2 GPU and 1 VF",
			deviceCR:  fakeDeviceCR,
			gpuWanted: 2,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:      "allocate 3 GPU and 2 VF",
			deviceCR:  fakeDeviceCR,
			gpuWanted: 3,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:      "allocate 4 GPU and 2 VF",
			deviceCR:  fakeDeviceCR,
			gpuWanted: 4,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:      "allocate 6 GPU and 3 VF",
			deviceCR:  fakeDeviceCR,
			gpuWanted: 6,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 3,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:51:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:      "allocate 8 GPU and 4 VF",
			deviceCR:  fakeDeviceCR,
			gpuWanted: 8,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
					{
						Minor:     6,
						Resources: gpuResourceList,
					},
					{
						Minor:     7,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 3,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:51:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 4,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:b9:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:      "allocate 2 GPU and 1 VF with assigned devices",
			deviceCR:  fakeDeviceCR,
			gpuWanted: 2,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:      "allocate 3 GPU and 2 VF with assigned devices",
			deviceCR:  fakeDeviceCR,
			gpuWanted: 3,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.3",
									Minor: 1,
								},
							},
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "Only 1 RDMA and 4 PCIE with 2 GPUs Per PCIE, allocate 4 GPUs",
			deviceCR: func() *schedulingv1alpha1.Device {
				var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"annotations":{}},"spec":{"devices":[{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:1f:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:20:00.0"},{"minor":1,"busID":"0000:20:00.1"},{"minor":2,"busID":"0000:20:00.2"},{"minor":3,"busID":"0000:20:00.3"},{"minor":4,"busID":"0000:20:00.4"},{"minor":5,"busID":"0000:20:00.5"},{"minor":6,"busID":"0000:20:00.6"},{"minor":7,"busID":"0000:20:00.7"},{"minor":8,"busID":"0000:20:00.8"},{"minor":9,"busID":"0000:20:00.9"},{"minor":10,"busID":"0000:20:00.a"},{"minor":11,"busID":"0000:20:00.b"},{"minor":12,"busID":"0000:20:00.c"},{"minor":13,"busID":"0000:20:00.d"},{"minor":14,"busID":"0000:20:00.e"},{"minor":15,"busID":"0000:20:00.f"},{"minor":16,"busID":"0000:20:00.10"},{"minor":17,"busID":"0000:20:00.11"},{"minor":18,"busID":"0000:20:00.12"},{"minor":19,"busID":"0000:20:00.13"},{"minor":20,"busID":"0000:20:00.14"},{"minor":21,"busID":"0000:20:00.15"},{"minor":22,"busID":"0000:20:00.16"},{"minor":23,"busID":"0000:20:00.17"},{"minor":24,"busID":"0000:20:00.18"},{"minor":25,"busID":"0000:20:00.19"},{"minor":26,"busID":"0000:20:00.1a"},{"minor":27,"busID":"0000:20:00.1b"},{"minor":28,"busID":"0000:20:00.1c"},{"minor":29,"busID":"0000:20:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:90:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:51:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:b9:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}},{"type":"gpu","id":"GPU-8c25ea37-2909-6e62-b7bf-e2fcadebea8d","minor":0,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"gpu","id":"GPU-befd76c3-8a36-7b8a-179c-eae75aa7d9f2","minor":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"gpu","id":"GPU-87a9047b-dade-e08c-c067-7fedfd2e2750","minor":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"gpu","id":"GPU-44a68f77-c18d-85a6-5425-e314c0e8e182","minor":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"gpu","id":"GPU-ac53dc25-2cb7-a11d-417f-ce23331dcea0","minor":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"gpu","id":"GPU-3908dbfd-6e0b-013d-549b-fca246a16fa0","minor":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"gpu","id":"GPU-7a87e98a-a1a7-28bc-c880-28c870bf0c7d","minor":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}},{"type":"gpu","id":"GPU-c3b7de0e-8a41-9bdb-3f71-8175c3438890","minor":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}}]},"status":{}}`)
				var deviceCR schedulingv1alpha1.Device
				_ = json.Unmarshal(data, &deviceCR)
				return &deviceCR
			}(),
			gpuWanted: 4,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:20:00.0",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "1 RDMA with 2 GPUs Per PCIE, 2 NUMA Nodes, assigned 4 GPUs, requests 4 GPUs",
			deviceCR: func() *schedulingv1alpha1.Device {
				var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"annotations":{}},"spec":{"devices":[{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:1f:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:20:00.0"},{"minor":1,"busID":"0000:20:00.1"},{"minor":2,"busID":"0000:20:00.2"},{"minor":3,"busID":"0000:20:00.3"},{"minor":4,"busID":"0000:20:00.4"},{"minor":5,"busID":"0000:20:00.5"},{"minor":6,"busID":"0000:20:00.6"},{"minor":7,"busID":"0000:20:00.7"},{"minor":8,"busID":"0000:20:00.8"},{"minor":9,"busID":"0000:20:00.9"},{"minor":10,"busID":"0000:20:00.a"},{"minor":11,"busID":"0000:20:00.b"},{"minor":12,"busID":"0000:20:00.c"},{"minor":13,"busID":"0000:20:00.d"},{"minor":14,"busID":"0000:20:00.e"},{"minor":15,"busID":"0000:20:00.f"},{"minor":16,"busID":"0000:20:00.10"},{"minor":17,"busID":"0000:20:00.11"},{"minor":18,"busID":"0000:20:00.12"},{"minor":19,"busID":"0000:20:00.13"},{"minor":20,"busID":"0000:20:00.14"},{"minor":21,"busID":"0000:20:00.15"},{"minor":22,"busID":"0000:20:00.16"},{"minor":23,"busID":"0000:20:00.17"},{"minor":24,"busID":"0000:20:00.18"},{"minor":25,"busID":"0000:20:00.19"},{"minor":26,"busID":"0000:20:00.1a"},{"minor":27,"busID":"0000:20:00.1b"},{"minor":28,"busID":"0000:20:00.1c"},{"minor":29,"busID":"0000:20:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:90:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:21:00.0"},{"minor":1,"busID":"0000:21:00.1"},{"minor":2,"busID":"0000:21:00.2"},{"minor":3,"busID":"0000:21:00.3"},{"minor":4,"busID":"0000:21:00.4"},{"minor":5,"busID":"0000:21:00.5"},{"minor":6,"busID":"0000:21:00.6"},{"minor":7,"busID":"0000:21:00.7"},{"minor":8,"busID":"0000:21:00.8"},{"minor":9,"busID":"0000:21:00.9"},{"minor":10,"busID":"0000:21:00.a"},{"minor":11,"busID":"0000:21:00.b"},{"minor":12,"busID":"0000:21:00.c"},{"minor":13,"busID":"0000:21:00.d"},{"minor":14,"busID":"0000:21:00.e"},{"minor":15,"busID":"0000:21:00.f"},{"minor":16,"busID":"0000:21:00.10"},{"minor":17,"busID":"0000:21:00.11"},{"minor":18,"busID":"0000:21:00.12"},{"minor":19,"busID":"0000:21:00.13"},{"minor":20,"busID":"0000:21:00.14"},{"minor":21,"busID":"0000:21:00.15"},{"minor":22,"busID":"0000:21:00.16"},{"minor":23,"busID":"0000:21:00.17"},{"minor":24,"busID":"0000:21:00.18"},{"minor":25,"busID":"0000:21:00.19"},{"minor":26,"busID":"0000:21:00.1a"},{"minor":27,"busID":"0000:21:00.1b"},{"minor":28,"busID":"0000:21:00.1c"},{"minor":29,"busID":"0000:21:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:51:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:22:00.0"},{"minor":1,"busID":"0000:22:00.1"},{"minor":2,"busID":"0000:22:00.2"},{"minor":3,"busID":"0000:22:00.3"},{"minor":4,"busID":"0000:22:00.4"},{"minor":5,"busID":"0000:22:00.5"},{"minor":6,"busID":"0000:22:00.6"},{"minor":7,"busID":"0000:22:00.7"},{"minor":8,"busID":"0000:22:00.8"},{"minor":9,"busID":"0000:22:00.9"},{"minor":10,"busID":"0000:22:00.a"},{"minor":11,"busID":"0000:22:00.b"},{"minor":12,"busID":"0000:22:00.c"},{"minor":13,"busID":"0000:22:00.d"},{"minor":14,"busID":"0000:22:00.e"},{"minor":15,"busID":"0000:22:00.f"},{"minor":16,"busID":"0000:22:00.10"},{"minor":17,"busID":"0000:22:00.11"},{"minor":18,"busID":"0000:22:00.12"},{"minor":19,"busID":"0000:22:00.13"},{"minor":20,"busID":"0000:22:00.14"},{"minor":21,"busID":"0000:22:00.15"},{"minor":22,"busID":"0000:22:00.16"},{"minor":23,"busID":"0000:22:00.17"},{"minor":24,"busID":"0000:22:00.18"},{"minor":25,"busID":"0000:22:00.19"},{"minor":26,"busID":"0000:22:00.1a"},{"minor":27,"busID":"0000:22:00.1b"},{"minor":28,"busID":"0000:22:00.1c"},{"minor":29,"busID":"0000:22:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:b9:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:23:00.0"},{"minor":1,"busID":"0000:23:00.1"},{"minor":2,"busID":"0000:23:00.2"},{"minor":3,"busID":"0000:23:00.3"},{"minor":4,"busID":"0000:23:00.4"},{"minor":5,"busID":"0000:23:00.5"},{"minor":6,"busID":"0000:23:00.6"},{"minor":7,"busID":"0000:23:00.7"},{"minor":8,"busID":"0000:23:00.8"},{"minor":9,"busID":"0000:23:00.9"},{"minor":10,"busID":"0000:23:00.a"},{"minor":11,"busID":"0000:23:00.b"},{"minor":12,"busID":"0000:23:00.c"},{"minor":13,"busID":"0000:23:00.d"},{"minor":14,"busID":"0000:23:00.e"},{"minor":15,"busID":"0000:23:00.f"},{"minor":16,"busID":"0000:23:00.10"},{"minor":17,"busID":"0000:23:00.11"},{"minor":18,"busID":"0000:23:00.12"},{"minor":19,"busID":"0000:23:00.13"},{"minor":20,"busID":"0000:23:00.14"},{"minor":21,"busID":"0000:23:00.15"},{"minor":22,"busID":"0000:23:00.16"},{"minor":23,"busID":"0000:23:00.17"},{"minor":24,"busID":"0000:23:00.18"},{"minor":25,"busID":"0000:23:00.19"},{"minor":26,"busID":"0000:23:00.1a"},{"minor":27,"busID":"0000:23:00.1b"},{"minor":28,"busID":"0000:23:00.1c"},{"minor":29,"busID":"0000:23:00.1d"}]}]},{"type":"gpu","id":"GPU-8c25ea37-2909-6e62-b7bf-e2fcadebea8d","minor":0,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"}},{"type":"gpu","id":"GPU-befd76c3-8a36-7b8a-179c-eae75aa7d9f2","minor":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"}},{"type":"gpu","id":"GPU-87a9047b-dade-e08c-c067-7fedfd2e2750","minor":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"}},{"type":"gpu","id":"GPU-44a68f77-c18d-85a6-5425-e314c0e8e182","minor":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"}},{"type":"gpu","id":"GPU-ac53dc25-2cb7-a11d-417f-ce23331dcea0","minor":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"}},{"type":"gpu","id":"GPU-3908dbfd-6e0b-013d-549b-fca246a16fa0","minor":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"}},{"type":"gpu","id":"GPU-7a87e98a-a1a7-28bc-c880-28c870bf0c7d","minor":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"}},{"type":"gpu","id":"GPU-c3b7de0e-8a41-9bdb-3f71-8175c3438890","minor":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"}}]},"status":{}}`)
				var deviceCR schedulingv1alpha1.Device
				_ = json.Unmarshal(data, &deviceCR)
				return &deviceCR
			}(),
			gpuWanted: 4,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:20:00.0",
									Minor: 0,
								},
							},
						},
					},
				},
			},
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
					{
						Minor:     6,
						Resources: gpuResourceList,
					},
					{
						Minor:     7,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 3,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:22:00.0",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 4,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:23:00.0",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "2 RDMA with 2 GPUs Per PCIE, 2 NUMA Nodes, assigned 4 GPUs, requests 4 GPUs",
			deviceCR: func() *schedulingv1alpha1.Device {
				var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"annotations":{}},"spec":{"devices":[{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:1f:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:20:00.0"},{"minor":1,"busID":"0000:20:00.1"},{"minor":2,"busID":"0000:20:00.2"},{"minor":3,"busID":"0000:20:00.3"},{"minor":4,"busID":"0000:20:00.4"},{"minor":5,"busID":"0000:20:00.5"},{"minor":6,"busID":"0000:20:00.6"},{"minor":7,"busID":"0000:20:00.7"},{"minor":8,"busID":"0000:20:00.8"},{"minor":9,"busID":"0000:20:00.9"},{"minor":10,"busID":"0000:20:00.a"},{"minor":11,"busID":"0000:20:00.b"},{"minor":12,"busID":"0000:20:00.c"},{"minor":13,"busID":"0000:20:00.d"},{"minor":14,"busID":"0000:20:00.e"},{"minor":15,"busID":"0000:20:00.f"},{"minor":16,"busID":"0000:20:00.10"},{"minor":17,"busID":"0000:20:00.11"},{"minor":18,"busID":"0000:20:00.12"},{"minor":19,"busID":"0000:20:00.13"},{"minor":20,"busID":"0000:20:00.14"},{"minor":21,"busID":"0000:20:00.15"},{"minor":22,"busID":"0000:20:00.16"},{"minor":23,"busID":"0000:20:00.17"},{"minor":24,"busID":"0000:20:00.18"},{"minor":25,"busID":"0000:20:00.19"},{"minor":26,"busID":"0000:20:00.1a"},{"minor":27,"busID":"0000:20:00.1b"},{"minor":28,"busID":"0000:20:00.1c"},{"minor":29,"busID":"0000:20:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:90:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:21:00.0"},{"minor":1,"busID":"0000:21:00.1"},{"minor":2,"busID":"0000:21:00.2"},{"minor":3,"busID":"0000:21:00.3"},{"minor":4,"busID":"0000:21:00.4"},{"minor":5,"busID":"0000:21:00.5"},{"minor":6,"busID":"0000:21:00.6"},{"minor":7,"busID":"0000:21:00.7"},{"minor":8,"busID":"0000:21:00.8"},{"minor":9,"busID":"0000:21:00.9"},{"minor":10,"busID":"0000:21:00.a"},{"minor":11,"busID":"0000:21:00.b"},{"minor":12,"busID":"0000:21:00.c"},{"minor":13,"busID":"0000:21:00.d"},{"minor":14,"busID":"0000:21:00.e"},{"minor":15,"busID":"0000:21:00.f"},{"minor":16,"busID":"0000:21:00.10"},{"minor":17,"busID":"0000:21:00.11"},{"minor":18,"busID":"0000:21:00.12"},{"minor":19,"busID":"0000:21:00.13"},{"minor":20,"busID":"0000:21:00.14"},{"minor":21,"busID":"0000:21:00.15"},{"minor":22,"busID":"0000:21:00.16"},{"minor":23,"busID":"0000:21:00.17"},{"minor":24,"busID":"0000:21:00.18"},{"minor":25,"busID":"0000:21:00.19"},{"minor":26,"busID":"0000:21:00.1a"},{"minor":27,"busID":"0000:21:00.1b"},{"minor":28,"busID":"0000:21:00.1c"},{"minor":29,"busID":"0000:21:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:51:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:22:00.0"},{"minor":1,"busID":"0000:22:00.1"},{"minor":2,"busID":"0000:22:00.2"},{"minor":3,"busID":"0000:22:00.3"},{"minor":4,"busID":"0000:22:00.4"},{"minor":5,"busID":"0000:22:00.5"},{"minor":6,"busID":"0000:22:00.6"},{"minor":7,"busID":"0000:22:00.7"},{"minor":8,"busID":"0000:22:00.8"},{"minor":9,"busID":"0000:22:00.9"},{"minor":10,"busID":"0000:22:00.a"},{"minor":11,"busID":"0000:22:00.b"},{"minor":12,"busID":"0000:22:00.c"},{"minor":13,"busID":"0000:22:00.d"},{"minor":14,"busID":"0000:22:00.e"},{"minor":15,"busID":"0000:22:00.f"},{"minor":16,"busID":"0000:22:00.10"},{"minor":17,"busID":"0000:22:00.11"},{"minor":18,"busID":"0000:22:00.12"},{"minor":19,"busID":"0000:22:00.13"},{"minor":20,"busID":"0000:22:00.14"},{"minor":21,"busID":"0000:22:00.15"},{"minor":22,"busID":"0000:22:00.16"},{"minor":23,"busID":"0000:22:00.17"},{"minor":24,"busID":"0000:22:00.18"},{"minor":25,"busID":"0000:22:00.19"},{"minor":26,"busID":"0000:22:00.1a"},{"minor":27,"busID":"0000:22:00.1b"},{"minor":28,"busID":"0000:22:00.1c"},{"minor":29,"busID":"0000:22:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:b9:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:23:00.0"},{"minor":1,"busID":"0000:23:00.1"},{"minor":2,"busID":"0000:23:00.2"},{"minor":3,"busID":"0000:23:00.3"},{"minor":4,"busID":"0000:23:00.4"},{"minor":5,"busID":"0000:23:00.5"},{"minor":6,"busID":"0000:23:00.6"},{"minor":7,"busID":"0000:23:00.7"},{"minor":8,"busID":"0000:23:00.8"},{"minor":9,"busID":"0000:23:00.9"},{"minor":10,"busID":"0000:23:00.a"},{"minor":11,"busID":"0000:23:00.b"},{"minor":12,"busID":"0000:23:00.c"},{"minor":13,"busID":"0000:23:00.d"},{"minor":14,"busID":"0000:23:00.e"},{"minor":15,"busID":"0000:23:00.f"},{"minor":16,"busID":"0000:23:00.10"},{"minor":17,"busID":"0000:23:00.11"},{"minor":18,"busID":"0000:23:00.12"},{"minor":19,"busID":"0000:23:00.13"},{"minor":20,"busID":"0000:23:00.14"},{"minor":21,"busID":"0000:23:00.15"},{"minor":22,"busID":"0000:23:00.16"},{"minor":23,"busID":"0000:23:00.17"},{"minor":24,"busID":"0000:23:00.18"},{"minor":25,"busID":"0000:23:00.19"},{"minor":26,"busID":"0000:23:00.1a"},{"minor":27,"busID":"0000:23:00.1b"},{"minor":28,"busID":"0000:23:00.1c"},{"minor":29,"busID":"0000:23:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0001:1f:00.0","minor":5,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0001:20:00.0"},{"minor":1,"busID":"0001:20:00.1"},{"minor":2,"busID":"0001:20:00.2"},{"minor":3,"busID":"0001:20:00.3"},{"minor":4,"busID":"0001:20:00.4"},{"minor":5,"busID":"0001:20:00.5"},{"minor":6,"busID":"0001:20:00.6"},{"minor":7,"busID":"0001:20:00.7"},{"minor":8,"busID":"0001:20:00.8"},{"minor":9,"busID":"0001:20:00.9"},{"minor":10,"busID":"0001:20:00.a"},{"minor":11,"busID":"0001:20:00.b"},{"minor":12,"busID":"0001:20:00.c"},{"minor":13,"busID":"0001:20:00.d"},{"minor":14,"busID":"0001:20:00.e"},{"minor":15,"busID":"0001:20:00.f"},{"minor":16,"busID":"0001:20:00.10"},{"minor":17,"busID":"0001:20:00.11"},{"minor":18,"busID":"0001:20:00.12"},{"minor":19,"busID":"0001:20:00.13"},{"minor":20,"busID":"0001:20:00.14"},{"minor":21,"busID":"0001:20:00.15"},{"minor":22,"busID":"0001:20:00.16"},{"minor":23,"busID":"0001:20:00.17"},{"minor":24,"busID":"0001:20:00.18"},{"minor":25,"busID":"0001:20:00.19"},{"minor":26,"busID":"0001:20:00.1a"},{"minor":27,"busID":"0001:20:00.1b"},{"minor":28,"busID":"0001:20:00.1c"},{"minor":29,"busID":"0001:20:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0001:90:00.0","minor":6,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0001:21:00.0"},{"minor":1,"busID":"0001:21:00.1"},{"minor":2,"busID":"0001:21:00.2"},{"minor":3,"busID":"0001:21:00.3"},{"minor":4,"busID":"0001:21:00.4"},{"minor":5,"busID":"0001:21:00.5"},{"minor":6,"busID":"0001:21:00.6"},{"minor":7,"busID":"0001:21:00.7"},{"minor":8,"busID":"0001:21:00.8"},{"minor":9,"busID":"0001:21:00.9"},{"minor":10,"busID":"0001:21:00.a"},{"minor":11,"busID":"0001:21:00.b"},{"minor":12,"busID":"0001:21:00.c"},{"minor":13,"busID":"0001:21:00.d"},{"minor":14,"busID":"0001:21:00.e"},{"minor":15,"busID":"0001:21:00.f"},{"minor":16,"busID":"0001:21:00.10"},{"minor":17,"busID":"0001:21:00.11"},{"minor":18,"busID":"0001:21:00.12"},{"minor":19,"busID":"0001:21:00.13"},{"minor":20,"busID":"0001:21:00.14"},{"minor":21,"busID":"0001:21:00.15"},{"minor":22,"busID":"0001:21:00.16"},{"minor":23,"busID":"0001:21:00.17"},{"minor":24,"busID":"0001:21:00.18"},{"minor":25,"busID":"0001:21:00.19"},{"minor":26,"busID":"0001:21:00.1a"},{"minor":27,"busID":"0001:21:00.1b"},{"minor":28,"busID":"0001:21:00.1c"},{"minor":29,"busID":"0001:21:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0001:51:00.0","minor":7,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0001:22:00.0"},{"minor":1,"busID":"0001:22:00.1"},{"minor":2,"busID":"0001:22:00.2"},{"minor":3,"busID":"0001:22:00.3"},{"minor":4,"busID":"0001:22:00.4"},{"minor":5,"busID":"0001:22:00.5"},{"minor":6,"busID":"0001:22:00.6"},{"minor":7,"busID":"0001:22:00.7"},{"minor":8,"busID":"0001:22:00.8"},{"minor":9,"busID":"0001:22:00.9"},{"minor":10,"busID":"0001:22:00.a"},{"minor":11,"busID":"0001:22:00.b"},{"minor":12,"busID":"0001:22:00.c"},{"minor":13,"busID":"0001:22:00.d"},{"minor":14,"busID":"0001:22:00.e"},{"minor":15,"busID":"0001:22:00.f"},{"minor":16,"busID":"0001:22:00.10"},{"minor":17,"busID":"0001:22:00.11"},{"minor":18,"busID":"0001:22:00.12"},{"minor":19,"busID":"0001:22:00.13"},{"minor":20,"busID":"0001:22:00.14"},{"minor":21,"busID":"0001:22:00.15"},{"minor":22,"busID":"0001:22:00.16"},{"minor":23,"busID":"0001:22:00.17"},{"minor":24,"busID":"0001:22:00.18"},{"minor":25,"busID":"0001:22:00.19"},{"minor":26,"busID":"0001:22:00.1a"},{"minor":27,"busID":"0001:22:00.1b"},{"minor":28,"busID":"0001:22:00.1c"},{"minor":29,"busID":"0001:22:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0001:b9:00.0","minor":8,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0001:23:00.0"},{"minor":1,"busID":"0001:23:00.1"},{"minor":2,"busID":"0001:23:00.2"},{"minor":3,"busID":"0001:23:00.3"},{"minor":4,"busID":"0001:23:00.4"},{"minor":5,"busID":"0001:23:00.5"},{"minor":6,"busID":"0001:23:00.6"},{"minor":7,"busID":"0001:23:00.7"},{"minor":8,"busID":"0001:23:00.8"},{"minor":9,"busID":"0001:23:00.9"},{"minor":10,"busID":"0001:23:00.a"},{"minor":11,"busID":"0001:23:00.b"},{"minor":12,"busID":"0001:23:00.c"},{"minor":13,"busID":"0001:23:00.d"},{"minor":14,"busID":"0001:23:00.e"},{"minor":15,"busID":"0001:23:00.f"},{"minor":16,"busID":"0001:23:00.10"},{"minor":17,"busID":"0001:23:00.11"},{"minor":18,"busID":"0001:23:00.12"},{"minor":19,"busID":"0001:23:00.13"},{"minor":20,"busID":"0001:23:00.14"},{"minor":21,"busID":"0001:23:00.15"},{"minor":22,"busID":"0001:23:00.16"},{"minor":23,"busID":"0001:23:00.17"},{"minor":24,"busID":"0001:23:00.18"},{"minor":25,"busID":"0001:23:00.19"},{"minor":26,"busID":"0001:23:00.1a"},{"minor":27,"busID":"0001:23:00.1b"},{"minor":28,"busID":"0001:23:00.1c"},{"minor":29,"busID":"0001:23:00.1d"}]}]},{"type":"gpu","id":"GPU-8c25ea37-2909-6e62-b7bf-e2fcadebea8d","minor":0,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"}},{"type":"gpu","id":"GPU-befd76c3-8a36-7b8a-179c-eae75aa7d9f2","minor":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"}},{"type":"gpu","id":"GPU-87a9047b-dade-e08c-c067-7fedfd2e2750","minor":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"}},{"type":"gpu","id":"GPU-44a68f77-c18d-85a6-5425-e314c0e8e182","minor":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"}},{"type":"gpu","id":"GPU-ac53dc25-2cb7-a11d-417f-ce23331dcea0","minor":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"}},{"type":"gpu","id":"GPU-3908dbfd-6e0b-013d-549b-fca246a16fa0","minor":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"}},{"type":"gpu","id":"GPU-7a87e98a-a1a7-28bc-c880-28c870bf0c7d","minor":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"}},{"type":"gpu","id":"GPU-c3b7de0e-8a41-9bdb-3f71-8175c3438890","minor":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"}}]},"status":{}}`)
				var deviceCR schedulingv1alpha1.Device
				_ = json.Unmarshal(data, &deviceCR)
				return &deviceCR
			}(),
			gpuWanted:  4,
			rdmaWanted: 4,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:20:00.0",
									Minor: 0,
								},
							},
						},
					},
				},
			},
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
					{
						Minor:     6,
						Resources: gpuResourceList,
					},
					{
						Minor:     7,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 3,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:22:00.0",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 4,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:23:00.0",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 7,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:22:00.0",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 8,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:23:00.0",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "1 GPU with hostNetwork and apply for all RDMAs",
			deviceCR: func() *schedulingv1alpha1.Device {
				var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"annotations":{}},"spec":{"devices":[{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:1f:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:90:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:51:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:b9:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}},{"type":"gpu","id":"GPU-8c25ea37-2909-6e62-b7bf-e2fcadebea8d","minor":0,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"gpu","id":"GPU-befd76c3-8a36-7b8a-179c-eae75aa7d9f2","minor":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"gpu","id":"GPU-87a9047b-dade-e08c-c067-7fedfd2e2750","minor":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"gpu","id":"GPU-44a68f77-c18d-85a6-5425-e314c0e8e182","minor":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"gpu","id":"GPU-ac53dc25-2cb7-a11d-417f-ce23331dcea0","minor":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"gpu","id":"GPU-3908dbfd-6e0b-013d-549b-fca246a16fa0","minor":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"gpu","id":"GPU-7a87e98a-a1a7-28bc-c880-28c870bf0c7d","minor":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}},{"type":"gpu","id":"GPU-c3b7de0e-8a41-9bdb-3f71-8175c3438890","minor":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}}]},"status":{}}`)
				var deviceCR schedulingv1alpha1.Device
				_ = json.Unmarshal(data, &deviceCR)
				return &deviceCR
			}(),
			gpuWanted:   4,
			hostNetwork: true,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
					{
						Minor: 3,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
					{
						Minor: 4,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
				},
			},
		},
		{
			name:                       "allocate 8 GPU and 4 VF; secondary device well planned",
			deviceCR:                   fakeDeviceCR,
			gpuWanted:                  8,
			secondaryDeviceWellPlanned: true,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
					{
						Minor:     6,
						Resources: gpuResourceList,
					},
					{
						Minor:     7,
						Resources: gpuResourceList,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deviceCR := tt.deviceCR.DeepCopy()
			deviceCR.ResourceVersion = "1"
			deviceCR.Labels = map[string]string{
				apiext.LabelSecondaryDeviceWellPlanned: fmt.Sprintf("%t", tt.secondaryDeviceWellPlanned),
			}
			koordFakeClient := koordfake.NewSimpleClientset()
			_, err := koordFakeClient.SchedulingV1alpha1().Devices().Create(context.TODO(), deviceCR, metav1.CreateOptions{})
			assert.NoError(t, err)
			koordShareInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordFakeClient, 0)

			kubeFakeClient := kubefake.NewSimpleClientset(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
			})
			sharedInformerFactory := informers.NewSharedInformerFactory(kubeFakeClient, 0)

			if tt.assignedDevices != nil {
				data, err := json.Marshal(tt.assignedDevices)
				assert.NoError(t, err)
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "assigned-pod",
						UID:       uuid.NewUUID(),
						Annotations: map[string]string{
							apiext.AnnotationDeviceAllocated: string(data),
						},
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node-1",
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										apiext.ResourceNvidiaGPU: *resource.NewQuantity(1, resource.DecimalSI),
									},
								},
							},
						},
					},
				}
				_, err = kubeFakeClient.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			deviceCache := newNodeDeviceCache()
			registerDeviceEventHandler(deviceCache, koordShareInformerFactory)
			registerPodEventHandler(deviceCache, sharedInformerFactory, koordShareInformerFactory)

			sharedInformerFactory.Start(nil)
			sharedInformerFactory.WaitForCacheSync(nil)

			nodeDevice := deviceCache.getNodeDevice("test-node-1", false)
			assert.NotNil(t, nodeDevice)

			podRequest := corev1.ResourceList{
				apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
			}
			if tt.rdmaWanted > 0 {
				podRequest[apiext.ResourceRDMA] = *resource.NewQuantity(int64(100*tt.rdmaWanted), resource.DecimalSI)
			}

			if tt.gpuWanted > 0 {
				podRequest[apiext.ResourceNvidiaGPU] = *resource.NewQuantity(int64(tt.gpuWanted), resource.DecimalSI)
			}

			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					HostNetwork: tt.hostNetwork,
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: podRequest,
							},
						},
					},
				},
			}
			if tt.hostNetwork {
				setDefaultTestAllocateHints(t, pod, allocateHintWithAllocateStrategy(schedulingv1alpha1.RDMA, apiext.ApplyForAllDeviceAllocateStrategy))
				setDefaultTestDeviceJointAllocate(t, pod)
			} else {
				if tt.gpuWanted > 0 {
					setDefaultTestAllocateHints(t, pod, allocateHintWithVFType("fakeG"))
					setDefaultTestDeviceJointAllocate(t, pod)
				} else {
					setDefaultTestAllocateHints(t, pod, allocateHintWithVFType("fakeC"))
				}
			}

			state, status := preparePod(pod)
			assert.True(t, status.IsSuccess())

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
			}

			allocator := &AutopilotAllocator{
				state:      state,
				nodeDevice: nodeDevice,
				node:       node,
				pod:        pod,
			}

			allocations, status := allocator.Allocate(nil, nil, nil, nil)
			if !status.IsSuccess() != tt.wantErr {
				t.Errorf("Allocate() error = %v, wantErr %v", status.AsError(), tt.wantErr)
				return
			}
			sortDeviceAllocations(allocations)
			sortDeviceAllocations(tt.want)
			fillGPUTotalMem(allocations, nodeDevice)
			assert.Equal(t, tt.want, allocations)
		})
	}
}

func TestAutopilotAllocatorWithExclusivePolicyAndRequiredScope(t *testing.T) {
	tests := []struct {
		name            string
		deviceCR        *schedulingv1alpha1.Device
		gpuWanted       int
		hostNetwork     bool
		exclusivePolicy apiext.DeviceExclusivePolicy
		assignedDevices apiext.DeviceAllocations
		want            apiext.DeviceAllocations
		wantErr         bool
	}{
		{
			name:            "allocate 0 GPU and 1 VF",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       0,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:            "allocate 1 GPU and 1 VF",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       1,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:            "allocate 2 GPU and 1 VF",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       2,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:            "allocate 3 GPU and 2 VF",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       3,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:            "allocate 4 GPU and 2 VF",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       4,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:            "allocate 6 GPU and 3 VF",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       6,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 3,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:51:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:            "allocate 8 GPU and 4 VF",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       8,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
					{
						Minor:     6,
						Resources: gpuResourceList,
					},
					{
						Minor:     7,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 3,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:51:00.2",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 4,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:b9:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name:            "allocate 2 GPU and 1 VF with assigned devices",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       2,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:90:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		// TODO 需要考虑这个场景真实存在么
		{
			name:            "allocate 3 GPU and 2 VF with assigned devices",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       3,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:1f:00.2",
									Minor: 0,
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "Only 1 RDMA and 4 PCIE with 2 GPUs Per PCIE, allocate 4 GPUs",
			deviceCR: func() *schedulingv1alpha1.Device {
				var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"annotations":{}},"spec":{"devices":[{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:1f:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:20:00.0"},{"minor":1,"busID":"0000:20:00.1"},{"minor":2,"busID":"0000:20:00.2"},{"minor":3,"busID":"0000:20:00.3"},{"minor":4,"busID":"0000:20:00.4"},{"minor":5,"busID":"0000:20:00.5"},{"minor":6,"busID":"0000:20:00.6"},{"minor":7,"busID":"0000:20:00.7"},{"minor":8,"busID":"0000:20:00.8"},{"minor":9,"busID":"0000:20:00.9"},{"minor":10,"busID":"0000:20:00.a"},{"minor":11,"busID":"0000:20:00.b"},{"minor":12,"busID":"0000:20:00.c"},{"minor":13,"busID":"0000:20:00.d"},{"minor":14,"busID":"0000:20:00.e"},{"minor":15,"busID":"0000:20:00.f"},{"minor":16,"busID":"0000:20:00.10"},{"minor":17,"busID":"0000:20:00.11"},{"minor":18,"busID":"0000:20:00.12"},{"minor":19,"busID":"0000:20:00.13"},{"minor":20,"busID":"0000:20:00.14"},{"minor":21,"busID":"0000:20:00.15"},{"minor":22,"busID":"0000:20:00.16"},{"minor":23,"busID":"0000:20:00.17"},{"minor":24,"busID":"0000:20:00.18"},{"minor":25,"busID":"0000:20:00.19"},{"minor":26,"busID":"0000:20:00.1a"},{"minor":27,"busID":"0000:20:00.1b"},{"minor":28,"busID":"0000:20:00.1c"},{"minor":29,"busID":"0000:20:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:90:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:51:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:b9:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}},{"type":"gpu","id":"GPU-8c25ea37-2909-6e62-b7bf-e2fcadebea8d","minor":0,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"gpu","id":"GPU-befd76c3-8a36-7b8a-179c-eae75aa7d9f2","minor":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"gpu","id":"GPU-87a9047b-dade-e08c-c067-7fedfd2e2750","minor":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"gpu","id":"GPU-44a68f77-c18d-85a6-5425-e314c0e8e182","minor":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"gpu","id":"GPU-ac53dc25-2cb7-a11d-417f-ce23331dcea0","minor":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"gpu","id":"GPU-3908dbfd-6e0b-013d-549b-fca246a16fa0","minor":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"gpu","id":"GPU-7a87e98a-a1a7-28bc-c880-28c870bf0c7d","minor":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}},{"type":"gpu","id":"GPU-c3b7de0e-8a41-9bdb-3f71-8175c3438890","minor":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}}]},"status":{}}`)
				var deviceCR schedulingv1alpha1.Device
				_ = json.Unmarshal(data, &deviceCR)
				return &deviceCR
			}(),
			gpuWanted:       4,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:20:00.0",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "1 RDMA with 2 GPUs Per PCIE, 2 NUMA Node, allocated 4 GPUs, request 4 GPUs",
			deviceCR: func() *schedulingv1alpha1.Device {
				var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"annotations":{}},"spec":{"devices":[{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:1f:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:20:00.0"},{"minor":1,"busID":"0000:20:00.1"},{"minor":2,"busID":"0000:20:00.2"},{"minor":3,"busID":"0000:20:00.3"},{"minor":4,"busID":"0000:20:00.4"},{"minor":5,"busID":"0000:20:00.5"},{"minor":6,"busID":"0000:20:00.6"},{"minor":7,"busID":"0000:20:00.7"},{"minor":8,"busID":"0000:20:00.8"},{"minor":9,"busID":"0000:20:00.9"},{"minor":10,"busID":"0000:20:00.a"},{"minor":11,"busID":"0000:20:00.b"},{"minor":12,"busID":"0000:20:00.c"},{"minor":13,"busID":"0000:20:00.d"},{"minor":14,"busID":"0000:20:00.e"},{"minor":15,"busID":"0000:20:00.f"},{"minor":16,"busID":"0000:20:00.10"},{"minor":17,"busID":"0000:20:00.11"},{"minor":18,"busID":"0000:20:00.12"},{"minor":19,"busID":"0000:20:00.13"},{"minor":20,"busID":"0000:20:00.14"},{"minor":21,"busID":"0000:20:00.15"},{"minor":22,"busID":"0000:20:00.16"},{"minor":23,"busID":"0000:20:00.17"},{"minor":24,"busID":"0000:20:00.18"},{"minor":25,"busID":"0000:20:00.19"},{"minor":26,"busID":"0000:20:00.1a"},{"minor":27,"busID":"0000:20:00.1b"},{"minor":28,"busID":"0000:20:00.1c"},{"minor":29,"busID":"0000:20:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:90:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:21:00.0"},{"minor":1,"busID":"0000:21:00.1"},{"minor":2,"busID":"0000:21:00.2"},{"minor":3,"busID":"0000:21:00.3"},{"minor":4,"busID":"0000:21:00.4"},{"minor":5,"busID":"0000:21:00.5"},{"minor":6,"busID":"0000:21:00.6"},{"minor":7,"busID":"0000:21:00.7"},{"minor":8,"busID":"0000:21:00.8"},{"minor":9,"busID":"0000:21:00.9"},{"minor":10,"busID":"0000:21:00.a"},{"minor":11,"busID":"0000:21:00.b"},{"minor":12,"busID":"0000:21:00.c"},{"minor":13,"busID":"0000:21:00.d"},{"minor":14,"busID":"0000:21:00.e"},{"minor":15,"busID":"0000:21:00.f"},{"minor":16,"busID":"0000:21:00.10"},{"minor":17,"busID":"0000:21:00.11"},{"minor":18,"busID":"0000:21:00.12"},{"minor":19,"busID":"0000:21:00.13"},{"minor":20,"busID":"0000:21:00.14"},{"minor":21,"busID":"0000:21:00.15"},{"minor":22,"busID":"0000:21:00.16"},{"minor":23,"busID":"0000:21:00.17"},{"minor":24,"busID":"0000:21:00.18"},{"minor":25,"busID":"0000:21:00.19"},{"minor":26,"busID":"0000:21:00.1a"},{"minor":27,"busID":"0000:21:00.1b"},{"minor":28,"busID":"0000:21:00.1c"},{"minor":29,"busID":"0000:21:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:51:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:22:00.0"},{"minor":1,"busID":"0000:22:00.1"},{"minor":2,"busID":"0000:22:00.2"},{"minor":3,"busID":"0000:22:00.3"},{"minor":4,"busID":"0000:22:00.4"},{"minor":5,"busID":"0000:22:00.5"},{"minor":6,"busID":"0000:22:00.6"},{"minor":7,"busID":"0000:22:00.7"},{"minor":8,"busID":"0000:22:00.8"},{"minor":9,"busID":"0000:22:00.9"},{"minor":10,"busID":"0000:22:00.a"},{"minor":11,"busID":"0000:22:00.b"},{"minor":12,"busID":"0000:22:00.c"},{"minor":13,"busID":"0000:22:00.d"},{"minor":14,"busID":"0000:22:00.e"},{"minor":15,"busID":"0000:22:00.f"},{"minor":16,"busID":"0000:22:00.10"},{"minor":17,"busID":"0000:22:00.11"},{"minor":18,"busID":"0000:22:00.12"},{"minor":19,"busID":"0000:22:00.13"},{"minor":20,"busID":"0000:22:00.14"},{"minor":21,"busID":"0000:22:00.15"},{"minor":22,"busID":"0000:22:00.16"},{"minor":23,"busID":"0000:22:00.17"},{"minor":24,"busID":"0000:22:00.18"},{"minor":25,"busID":"0000:22:00.19"},{"minor":26,"busID":"0000:22:00.1a"},{"minor":27,"busID":"0000:22:00.1b"},{"minor":28,"busID":"0000:22:00.1c"},{"minor":29,"busID":"0000:22:00.1d"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:b9:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"},"vfGroups":[{"labels":{"type":"general"},"vfs":[{"minor":0,"busID":"0000:23:00.0"},{"minor":1,"busID":"0000:23:00.1"},{"minor":2,"busID":"0000:23:00.2"},{"minor":3,"busID":"0000:23:00.3"},{"minor":4,"busID":"0000:23:00.4"},{"minor":5,"busID":"0000:23:00.5"},{"minor":6,"busID":"0000:23:00.6"},{"minor":7,"busID":"0000:23:00.7"},{"minor":8,"busID":"0000:23:00.8"},{"minor":9,"busID":"0000:23:00.9"},{"minor":10,"busID":"0000:23:00.a"},{"minor":11,"busID":"0000:23:00.b"},{"minor":12,"busID":"0000:23:00.c"},{"minor":13,"busID":"0000:23:00.d"},{"minor":14,"busID":"0000:23:00.e"},{"minor":15,"busID":"0000:23:00.f"},{"minor":16,"busID":"0000:23:00.10"},{"minor":17,"busID":"0000:23:00.11"},{"minor":18,"busID":"0000:23:00.12"},{"minor":19,"busID":"0000:23:00.13"},{"minor":20,"busID":"0000:23:00.14"},{"minor":21,"busID":"0000:23:00.15"},{"minor":22,"busID":"0000:23:00.16"},{"minor":23,"busID":"0000:23:00.17"},{"minor":24,"busID":"0000:23:00.18"},{"minor":25,"busID":"0000:23:00.19"},{"minor":26,"busID":"0000:23:00.1a"},{"minor":27,"busID":"0000:23:00.1b"},{"minor":28,"busID":"0000:23:00.1c"},{"minor":29,"busID":"0000:23:00.1d"}]}]},{"type":"gpu","id":"GPU-8c25ea37-2909-6e62-b7bf-e2fcadebea8d","minor":0,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"}},{"type":"gpu","id":"GPU-befd76c3-8a36-7b8a-179c-eae75aa7d9f2","minor":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"}},{"type":"gpu","id":"GPU-87a9047b-dade-e08c-c067-7fedfd2e2750","minor":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"}},{"type":"gpu","id":"GPU-44a68f77-c18d-85a6-5425-e314c0e8e182","minor":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"1"}},{"type":"gpu","id":"GPU-ac53dc25-2cb7-a11d-417f-ce23331dcea0","minor":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"}},{"type":"gpu","id":"GPU-3908dbfd-6e0b-013d-549b-fca246a16fa0","minor":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"2"}},{"type":"gpu","id":"GPU-7a87e98a-a1a7-28bc-c880-28c870bf0c7d","minor":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"}},{"type":"gpu","id":"GPU-c3b7de0e-8a41-9bdb-3f71-8175c3438890","minor":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"3"}}]},"status":{}}`)
				var deviceCR schedulingv1alpha1.Device
				_ = json.Unmarshal(data, &deviceCR)
				return &deviceCR
			}(),
			gpuWanted:       4,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:20:00.0",
									Minor: 0,
								},
							},
						},
					},
				},
			},
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
					{
						Minor:     6,
						Resources: gpuResourceList,
					},
					{
						Minor:     7,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 3,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:22:00.0",
									Minor: 0,
								},
							},
						},
					},
					{
						Minor: 4,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:23:00.0",
									Minor: 0,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "1 GPU with hostNetwork",
			deviceCR: func() *schedulingv1alpha1.Device {
				var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"annotations":{}},"spec":{"devices":[{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:1f:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:90:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:51:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:b9:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}},{"type":"gpu","id":"GPU-8c25ea37-2909-6e62-b7bf-e2fcadebea8d","minor":0,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"gpu","id":"GPU-befd76c3-8a36-7b8a-179c-eae75aa7d9f2","minor":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":0}},{"type":"gpu","id":"GPU-87a9047b-dade-e08c-c067-7fedfd2e2750","minor":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"gpu","id":"GPU-44a68f77-c18d-85a6-5425-e314c0e8e182","minor":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":1}},{"type":"gpu","id":"GPU-ac53dc25-2cb7-a11d-417f-ce23331dcea0","minor":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"gpu","id":"GPU-3908dbfd-6e0b-013d-549b-fca246a16fa0","minor":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":2}},{"type":"gpu","id":"GPU-7a87e98a-a1a7-28bc-c880-28c870bf0c7d","minor":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}},{"type":"gpu","id":"GPU-c3b7de0e-8a41-9bdb-3f71-8175c3438890","minor":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":3}}]},"status":{}}`)
				var deviceCR schedulingv1alpha1.Device
				_ = json.Unmarshal(data, &deviceCR)
				return &deviceCR
			}(),
			gpuWanted:       4,
			hostNetwork:     true,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     0,
						Resources: gpuResourceList,
					},
					{
						Minor:     1,
						Resources: gpuResourceList,
					},
					{
						Minor:     2,
						Resources: gpuResourceList,
					},
					{
						Minor:     3,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
					{
						Minor: 2,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
					{
						Minor: 3,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
					{
						Minor: 4,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deviceCR := tt.deviceCR.DeepCopy()
			deviceCR.ResourceVersion = "1"
			koordFakeClient := koordfake.NewSimpleClientset()
			_, err := koordFakeClient.SchedulingV1alpha1().Devices().Create(context.TODO(), deviceCR, metav1.CreateOptions{})
			assert.NoError(t, err)
			koordShareInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordFakeClient, 0)

			kubeFakeClient := kubefake.NewSimpleClientset(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
			})
			sharedInformerFactory := informers.NewSharedInformerFactory(kubeFakeClient, 0)

			if tt.assignedDevices != nil {
				data, err := json.Marshal(tt.assignedDevices)
				assert.NoError(t, err)
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "assigned-pod",
						UID:       uuid.NewUUID(),
						Annotations: map[string]string{
							apiext.AnnotationDeviceAllocated: string(data),
						},
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node-1",
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										apiext.ResourceNvidiaGPU: *resource.NewQuantity(1, resource.DecimalSI),
									},
								},
							},
						},
					},
				}
				_, err = kubeFakeClient.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			deviceCache := newNodeDeviceCache()
			registerDeviceEventHandler(deviceCache, koordShareInformerFactory)
			registerPodEventHandler(deviceCache, sharedInformerFactory, koordShareInformerFactory)

			sharedInformerFactory.Start(nil)
			sharedInformerFactory.WaitForCacheSync(nil)

			nodeDevice := deviceCache.getNodeDevice("test-node-1", false)
			assert.NotNil(t, nodeDevice)

			podRequest := corev1.ResourceList{
				apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
			}
			if tt.gpuWanted > 0 {
				podRequest[apiext.ResourceNvidiaGPU] = *resource.NewQuantity(int64(tt.gpuWanted), resource.DecimalSI)
			}

			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					HostNetwork: tt.hostNetwork,
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: podRequest,
							},
						},
					},
				},
			}
			if tt.hostNetwork {
				setDefaultTestAllocateHints(t, pod, allocateHintWithAllocateStrategy(schedulingv1alpha1.RDMA, apiext.ApplyForAllDeviceAllocateStrategy))
				setDefaultTestDeviceJointAllocate(t, pod, func(allocate *apiext.DeviceJointAllocate) {
					allocate.RequiredScope = apiext.SamePCIeDeviceJointAllocateScope
				})
			} else {
				if tt.gpuWanted > 0 {
					setDefaultTestAllocateHints(t, pod,
						allocateHintWithVFType("fakeG"),
						allocateHintWithExclusivePolicy(schedulingv1alpha1.GPU, tt.exclusivePolicy),
						allocateHintWithExclusivePolicy(schedulingv1alpha1.RDMA, tt.exclusivePolicy))
					setDefaultTestDeviceJointAllocate(t, pod, func(allocate *apiext.DeviceJointAllocate) {
						allocate.RequiredScope = apiext.SamePCIeDeviceJointAllocateScope
					})
				} else {
					setDefaultTestAllocateHints(t, pod,
						allocateHintWithVFType("fakeC"),
						allocateHintWithExclusivePolicy(schedulingv1alpha1.RDMA, tt.exclusivePolicy))
				}
			}

			state, status := preparePod(pod)
			assert.True(t, status.IsSuccess())

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
			}

			allocator := &AutopilotAllocator{
				state:      state,
				nodeDevice: nodeDevice,
				node:       node,
				pod:        pod,
			}

			allocations, status := allocator.Allocate(nil, nil, nil, nil)
			if !status.IsSuccess() != tt.wantErr {
				t.Errorf("Allocate() error = %v, wantErr %v", status.AsError(), tt.wantErr)
				return
			}
			sortDeviceAllocations(allocations)
			sortDeviceAllocations(tt.want)
			fillGPUTotalMem(allocations, nodeDevice)
			assert.Equal(t, tt.want, allocations)
		})
	}
}

func Test_allocateGPU(t *testing.T) {
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
	})
	nd.deviceInfos = map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
		schedulingv1alpha1.GPU: {
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(1)},
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(2)},
		},
	}

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

	podRequests := map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
		schedulingv1alpha1.GPU: {
			apiext.ResourceGPUCore:        resource.MustParse("50"),
			apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
			apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
		},
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
	state := &preFilterState{
		podRequests:        podRequests,
		preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{"test-node": preemptible},
	}
	state.gpuRequirements, _ = parseGPURequirements(pod, state.podRequests, nil)
	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nd,
		node:       &corev1.Node{},
		pod:        pod,
	}
	allocateResult, status := allocator.Allocate(nil, nil, nil, preemptible)
	assert.True(t, status.IsSuccess())
	expectAllocations := []*apiext.DeviceAllocation{
		{
			Minor: 1,
			Resources: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
	}
	assert.NoError(t, fillGPUTotalMem(allocateResult, nd))
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult[schedulingv1alpha1.GPU]))
}

func Test_allocateGPUWithLeastAllocatedScorer(t *testing.T) {
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
	nd.deviceInfos = map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
		schedulingv1alpha1.GPU: {
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(1)},
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(2)},
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(3)},
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(4)},
		},
	}

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

	podRequests := map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
		schedulingv1alpha1.GPU: {
			apiext.ResourceGPUCore:        resource.MustParse("50"),
			apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
		},
	}
	args := getDefaultArgs()
	args.ScoringStrategy.Type = schedulerconfig.LeastAllocated
	allocationScorer := deviceResourceStrategyTypeMap[args.ScoringStrategy.Type](args)

	state := &preFilterState{
		podRequests: podRequests,
	}
	state.gpuRequirements, _ = parseGPURequirements(pod, state.podRequests, nil)
	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nd,
		node:       &corev1.Node{},
		pod:        pod,
		scorer:     allocationScorer,
	}
	allocateResult, status := allocator.Allocate(nil, nil, nil, nil)
	err := fillGPUTotalMem(allocateResult, nd)
	assert.NoError(t, err)
	assert.True(t, status.IsSuccess())
	expectAllocations := []*apiext.DeviceAllocation{
		{
			Minor: 3,
			Resources: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult[schedulingv1alpha1.GPU]))
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
	nd.deviceInfos = map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
		schedulingv1alpha1.GPU: {
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(1)},
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(2)},
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(3)},
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(4)},
		},
	}

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

	podRequests := map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
		schedulingv1alpha1.GPU: {
			apiext.ResourceGPUCore:        resource.MustParse("50"),
			apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
		},
	}

	args := getDefaultArgs()
	args.ScoringStrategy.Type = schedulerconfig.MostAllocated
	allocationScorer := deviceResourceStrategyTypeMap[args.ScoringStrategy.Type](args)
	state := &preFilterState{
		podRequests: podRequests,
	}
	state.gpuRequirements, _ = parseGPURequirements(pod, state.podRequests, nil)
	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nd,
		node:       &corev1.Node{},
		pod:        pod,
		scorer:     allocationScorer,
	}
	allocateResult, status := allocator.Allocate(nil, nil, nil, nil)
	assert.True(t, status.IsSuccess())
	expectAllocations := []*apiext.DeviceAllocation{
		{
			Minor: 3,
			Resources: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
	}
	fillGPUTotalMem(allocateResult, nd)
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult[schedulingv1alpha1.GPU]))
}

func Test_failedPreemptGPUFromReservation(t *testing.T) {
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

	podRequests := map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
		schedulingv1alpha1.GPU: {
			apiext.ResourceGPUCore:        resource.MustParse("50"),
			apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
		},
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
	state := &preFilterState{
		podRequests:        podRequests,
		preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{"test-node": preemptible},
	}
	state.gpuRequirements, _ = parseGPURequirements(pod, state.podRequests, nil)
	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nd,
		node:       &corev1.Node{},
		pod:        pod,
	}
	allocateResult, status := allocator.Allocate(nil, nil, nil, preemptible)
	assert.Equal(t, status.Message(), fmt.Sprintf("Insufficient %s devices", schedulingv1alpha1.GPU))
	assert.Nil(t, allocateResult)
}

func Test_allocateGPUWithUnhealthyInstance(t *testing.T) {
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
	nd.deviceInfos = map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
		schedulingv1alpha1.GPU: {
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(1)},
			{Type: schedulingv1alpha1.GPU, Health: true, Minor: pointer.Int32(2)},
		},
	}

	podRequests := map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
		schedulingv1alpha1.GPU: {
			apiext.ResourceGPUCore:        resource.MustParse("50"),
			apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
			apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
		},
	}

	state := &preFilterState{
		podRequests: podRequests,
	}
	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nd,
		node:       &corev1.Node{},
		pod:        &corev1.Pod{},
	}
	state.gpuRequirements, _ = parseGPURequirements(allocator.pod, podRequests, nil)
	allocateResult, status := allocator.Allocate(nil, nil, nil, nil)
	assert.True(t, status.IsSuccess())
	expectAllocations := []*apiext.DeviceAllocation{
		{
			Minor: 2,
			Resources: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
	}
	assert.NoError(t, fillGPUTotalMem(allocateResult, nd))
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult[schedulingv1alpha1.GPU]))
}

func Test_allocateRDMA(t *testing.T) {
	nd := newNodeDevice()
	nd.resetDeviceTotal(map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.RDMA: {
			1: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
			2: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("100"),
			},
		},
	})
	nd.deviceInfos = map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
		schedulingv1alpha1.RDMA: {
			{Type: schedulingv1alpha1.RDMA, Health: true, Minor: pointer.Int32(1)},
			{Type: schedulingv1alpha1.RDMA, Health: true, Minor: pointer.Int32(2)},
		},
	}

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

	podRequests := map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
		schedulingv1alpha1.RDMA: {
			apiext.ResourceRDMA: resource.MustParse("50"),
		},
	}
	preemptible := deviceResources{
		1: corev1.ResourceList{
			apiext.ResourceRDMA: resource.MustParse("100"),
		},
		2: corev1.ResourceList{
			apiext.ResourceRDMA: resource.MustParse("100"),
		},
	}
	state := &preFilterState{
		podRequests: podRequests,
		preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{
			"test-node": {
				schedulingv1alpha1.RDMA: preemptible,
			},
		},
	}
	allocator := &AutopilotAllocator{
		state:      state,
		nodeDevice: nd,
		node:       &corev1.Node{},
		pod:        pod,
	}
	allocateResult, status := allocator.Allocate(nil, nil, nil, state.preemptibleDevices["test-node"])
	assert.True(t, status.IsSuccess())
	expectAllocations := []*apiext.DeviceAllocation{
		{
			Minor: 1,
			Resources: corev1.ResourceList{
				apiext.ResourceRDMA: resource.MustParse("50"),
			},
		},
	}
	assert.True(t, equality.Semantic.DeepEqual(expectAllocations, allocateResult[schedulingv1alpha1.RDMA]))
}
