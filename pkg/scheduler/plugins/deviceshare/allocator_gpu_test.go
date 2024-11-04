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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

var fakeH800DeviceCR = func() *schedulingv1alpha1.Device {
	var data = []byte(`{"metadata":{"name":"test-node-1","creationTimestamp":null,"annotations":{"internal.scheduling.koordinator.sh/nvidia-driver-versions":"[\"470.141.10\"]"}},"spec":{"devices":[{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:09:00.0","minor":1,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0"},"vfGroups":[{"labels":{"type":"fakeS"},"vfs":[{"minor":0,"busID":"0000:09:00.2"}]},{"labels":{"type":"fakeG"},"vfs":[{"minor":1,"busID":"0000:09:00.3"},{"minor":2,"busID":"0000:09:00.4"},{"minor":3,"busID":"0000:09:00.5"},{"minor":4,"busID":"0000:09:00.6"}]},{"labels":{"type":"fakeC"},"vfs":[{"minor":16,"busID":"0000:09:02.2"},{"minor":17,"busID":"0000:09:02.3"},{"minor":18,"busID":"0000:09:02.4"},{"minor":19,"busID":"0000:09:02.5"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:7f:00.0","minor":2,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"2"},"vfGroups":[{"labels":{"type":"fakeS"},"vfs":[{"minor":0,"busID":"0000:7f:00.2"}]},{"labels":{"type":"fakeG"},"vfs":[{"minor":1,"busID":"0000:7f:00.3"},{"minor":2,"busID":"0000:7f:00.4"},{"minor":3,"busID":"0000:7f:00.5"},{"minor":4,"busID":"0000:7f:00.6"}]},{"labels":{"type":"fakeC"},"vfs":[{"minor":16,"busID":"0000:7f:02.2"},{"minor":17,"busID":"0000:7f:02.3"},{"minor":18,"busID":"0000:7f:02.4"},{"minor":19,"busID":"0000:7f:02.5"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:a3:00.0","minor":3,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"3"},"vfGroups":[{"labels":{"type":"fakeC"},"vfs":[{"minor":16,"busID":"0000:a3:02.2"},{"minor":17,"busID":"0000:a3:02.3"},{"minor":18,"busID":"0000:a3:02.4"},{"minor":19,"busID":"0000:a3:02.5"}]},{"labels":{"type":"fakeS"},"vfs":[{"minor":0,"busID":"0000:a3:00.2"}]},{"labels":{"type":"fakeG"},"vfs":[{"minor":1,"busID":"0000:a3:00.3"},{"minor":2,"busID":"0000:a3:00.4"},{"minor":3,"busID":"0000:a3:00.5"},{"minor":4,"busID":"0000:a3:00.6"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0000:c7:00.0","minor":4,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"4"},"vfGroups":[{"labels":{"type":"fakeC"},"vfs":[{"minor":16,"busID":"0000:c7:02.2"},{"minor":17,"busID":"0000:c7:02.3"},{"minor":18,"busID":"0000:c7:02.4"},{"minor":19,"busID":"0000:c7:02.5"}]},{"labels":{"type":"fakeS"},"vfs":[{"minor":0,"busID":"0000:c7:00.2"}]},{"labels":{"type":"fakeG"},"vfs":[{"minor":1,"busID":"0000:c7:00.3"},{"minor":2,"busID":"0000:c7:00.4"},{"minor":3,"busID":"0000:c7:00.5"},{"minor":4,"busID":"0000:c7:00.6"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0001:08:00.0","minor":5,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"5"},"vfGroups":[{"labels":{"type":"fakeS"},"vfs":[{"minor":0,"busID":"0001:08:00.2"}]},{"labels":{"type":"fakeG"},"vfs":[{"minor":1,"busID":"0001:08:00.3"},{"minor":2,"busID":"0001:08:00.4"},{"minor":3,"busID":"0001:08:00.5"},{"minor":4,"busID":"0001:08:00.6"}]},{"labels":{"type":"fakeC"},"vfs":[{"minor":16,"busID":"0001:08:02.2"},{"minor":17,"busID":"0001:08:02.3"},{"minor":18,"busID":"0001:08:02.4"},{"minor":19,"busID":"0001:08:02.5"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0001:7e:00.0","minor":6,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"6"},"vfGroups":[{"labels":{"type":"fakeS"},"vfs":[{"minor":0,"busID":"0001:7e:00.2"}]},{"labels":{"type":"fakeG"},"vfs":[{"minor":1,"busID":"0001:7e:00.3"},{"minor":2,"busID":"0001:7e:00.4"},{"minor":3,"busID":"0001:7e:00.5"},{"minor":4,"busID":"0001:7e:00.6"}]},{"labels":{"type":"fakeC"},"vfs":[{"minor":16,"busID":"0001:7e:02.2"},{"minor":17,"busID":"0001:7e:02.3"},{"minor":18,"busID":"0001:7e:02.4"},{"minor":19,"busID":"0001:7e:02.5"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0001:a2:00.0","minor":7,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"7"},"vfGroups":[{"labels":{"type":"fakeS"},"vfs":[{"minor":0,"busID":"0001:a2:00.2"}]},{"labels":{"type":"fakeG"},"vfs":[{"minor":1,"busID":"0001:a2:00.3"},{"minor":2,"busID":"0001:a2:00.4"},{"minor":3,"busID":"0001:a2:00.5"},{"minor":4,"busID":"0001:a2:00.6"}]},{"labels":{"type":"fakeC"},"vfs":[{"minor":16,"busID":"0001:a2:02.2"},{"minor":17,"busID":"0001:a2:02.3"},{"minor":18,"busID":"0001:a2:02.4"},{"minor":19,"busID":"0001:a2:02.5"}]}]},{"type":"rdma","labels":{"type":"fakeW"},"id":"0001:c6:00.0","minor":8,"health":true,"resources":{"koordinator.sh/rdma":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"8"},"vfGroups":[{"labels":{"type":"fakeS"},"vfs":[{"minor":0,"busID":"0001:c6:00.2"}]},{"labels":{"type":"fakeG"},"vfs":[{"minor":1,"busID":"0001:c6:00.3"},{"minor":2,"busID":"0001:c6:00.4"},{"minor":3,"busID":"0001:c6:00.5"},{"minor":4,"busID":"0001:c6:00.6"},{"minor":15,"busID":"0001:c6:02.1"}]},{"labels":{"type":"fakeC"},"vfs":[{"minor":16,"busID":"0001:c6:02.2"},{"minor":17,"busID":"0001:c6:02.3"},{"minor":18,"busID":"0001:c6:02.4"},{"minor":19,"busID":"0001:c6:02.5"}]}]},{"type":"gpu","id":"GPU-6b1ff724-4fe2-17b8-adfe-1f4c8a4148d1","minor":0,"moduleID":6,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"0","busID":"0000:08:00.0"}},{"type":"gpu","id":"GPU-702ee422-96de-2dde-438f-b6eda3ef7efc","minor":1,"moduleID":8,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"2","busID":"0000:7e:00.0"}},{"type":"gpu","id":"GPU-e5a60856-a994-ec0b-1b04-eaa18205b00b","minor":2,"moduleID":7,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"3","busID":"0000:a2:00.0"}},{"type":"gpu","id":"GPU-e1f2597e-0996-be81-19c4-8dd19bca3761","minor":3,"moduleID":5,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":0,"nodeID":0,"pcieID":"4","busID":"0000:c6:00.0"}},{"type":"gpu","id":"GPU-02d911ef-e297-3063-1734-b8d3c3b3f5fc","minor":4,"moduleID":1,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"5","busID":"0001:09:00.0"}},{"type":"gpu","id":"GPU-8b1e54b7-84a6-960f-2723-ecee87de9d46","minor":5,"moduleID":3,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"6","busID":"0001:7f:00.0"}},{"type":"gpu","id":"GPU-5a1654e4-2a92-19a4-e97e-6b4ca19509cd","minor":6,"moduleID":4,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"7","busID":"0001:a3:00.0"}},{"type":"gpu","id":"GPU-f1646769-f47c-47b5-fe73-f7635d845bdf","minor":7,"moduleID":2,"health":true,"resources":{"koordinator.sh/gpu-core":"100","koordinator.sh/gpu-memory":"83201216Ki","koordinator.sh/gpu-memory-ratio":"100"},"topology":{"socketID":1,"nodeID":1,"pcieID":"8","busID":"0001:c7:00.0"}}]},"status":{}}`)
	var device schedulingv1alpha1.Device
	err := json.Unmarshal(data, &device)
	if err != nil {
		panic(err)
	}
	return &device
}()

var (
	fakeH100DeviceCR = fakeH800DeviceCR.DeepCopy()
)

func TestAllocateByPartition(t *testing.T) {
	tests := []struct {
		name               string
		deviceCR           *schedulingv1alpha1.Device
		gpuPartitionPolicy apiext.GPUPartitionPolicy
		gpuWanted          int
		hostNetwork        bool
		assignedDevices    apiext.DeviceAllocations
		modelSeries        string
		want               apiext.DeviceAllocations
		wantErr            bool
	}{
		{
			name:        "allocate 0 GPU and 1 VF",
			deviceCR:    fakeH800DeviceCR,
			modelSeries: "H800",
			gpuWanted:   0,
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
									BusID: "0000:09:02.2",
									Minor: 16,
								},
							},
						},
					},
				},
			},
		},
		{
			name:        "allocate 1 GPU and 1 VF",
			deviceCR:    fakeH800DeviceCR,
			modelSeries: "H800",
			gpuWanted:   1,
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
									BusID: "0000:09:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
		},
		{
			name:        "allocate 2 GPU and 2 VF",
			deviceCR:    fakeH800DeviceCR,
			modelSeries: "H800",
			gpuWanted:   2,
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
									BusID: "0000:09:00.3",
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
									BusID: "0000:7f:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
		},
		{
			name:        "allocate 3 GPU",
			deviceCR:    fakeH800DeviceCR,
			modelSeries: "H800",
			gpuWanted:   3,
			wantErr:     true,
		},
		{
			name:        "allocate 4 GPU and 4 VF",
			deviceCR:    fakeH800DeviceCR,
			modelSeries: "H800",
			gpuWanted:   4,
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
									BusID: "0000:09:00.3",
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
									BusID: "0000:7f:00.3",
									Minor: 1,
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
									BusID: "0000:a3:00.3",
									Minor: 1,
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
									BusID: "0000:c7:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
		},
		{
			name:        "allocate 6 GPU and 3 VF",
			deviceCR:    fakeH800DeviceCR,
			gpuWanted:   6,
			modelSeries: "H800",
			wantErr:     true,
		},
		{
			name:        "allocate 8 GPU and 8 VF",
			deviceCR:    fakeH800DeviceCR,
			modelSeries: "H800",
			gpuWanted:   8,
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
									BusID: "0000:09:00.3",
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
									BusID: "0000:7f:00.3",
									Minor: 1,
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
									BusID: "0000:a3:00.3",
									Minor: 1,
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
									BusID: "0000:c7:00.3",
									Minor: 1,
								},
							},
						},
					},
					{
						Minor: 5,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:08:00.3",
									Minor: 1,
								},
							},
						},
					},
					{
						Minor: 6,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:7e:00.3",
									Minor: 1,
								},
							},
						},
					},
					{
						Minor: 7,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:a2:00.3",
									Minor: 1,
								},
							},
						},
					},
					{
						Minor: 8,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:c6:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
		},
		{
			name:        "allocate 2 GPU and 2 VF with assigned devices",
			deviceCR:    fakeH800DeviceCR,
			modelSeries: "H800",
			gpuWanted:   2,
			assignedDevices: apiext.DeviceAllocations{
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
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:09:00.3",
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
									BusID: "0000:7f:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
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
									BusID: "0000:09:00.4",
									Minor: 2,
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
									BusID: "0000:7f:00.4",
									Minor: 2,
								},
							},
						},
					},
				},
			},
		},
		{
			name:        "allocate 2 GPU and 2 VF with assigned devices; BinPack",
			deviceCR:    fakeH800DeviceCR,
			modelSeries: "H800",
			gpuWanted:   2,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
				},

				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 5,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:08:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
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
						Minor: 7,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:a2:00.3",
									Minor: 1,
								},
							},
						},
					},
					{
						Minor: 8,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:c6:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
		},
		{
			name:        "allocate 1 GPU and 1 VF with assigned devices; BinPack",
			deviceCR:    fakeH800DeviceCR,
			modelSeries: "H800",
			gpuWanted:   1,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     4,
						Resources: gpuResourceList,
					},
				},

				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 5,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:08:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
			want: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 6,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:7e:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
		},
		{
			name:        "allocate 2 GPU and 2 VF with assigned devices; H100",
			deviceCR:    fakeH100DeviceCR,
			modelSeries: "H100",
			gpuWanted:   2,
			assignedDevices: apiext.DeviceAllocations{
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
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:09:00.3",
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
									BusID: "0000:7f:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
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
									BusID: "0000:09:00.4",
									Minor: 2,
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
									BusID: "0000:7f:00.4",
									Minor: 2,
								},
							},
						},
					},
				},
			},
		},
		{
			name:               "allocate 2 GPU and 2 VF with assigned devices; H100; gpuPartitionPolicyPrefer",
			deviceCR:           fakeH100DeviceCR,
			modelSeries:        "H100",
			gpuWanted:          3,
			gpuPartitionPolicy: apiext.GPUPartitionPolicyPrefer,
			assignedDevices: apiext.DeviceAllocations{
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
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0000:09:00.3",
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
									BusID: "0000:7f:00.3",
									Minor: 1,
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
				},
				schedulingv1alpha1.RDMA: []*apiext.DeviceAllocation{
					{
						Minor: 5,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:08:00.3",
									Minor: 1,
								},
							},
						},
					},
					{
						Minor: 6,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:7e:00.3",
									Minor: 1,
								},
							},
						},
					},
					{
						Minor: 7,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: *resource.NewQuantity(1, resource.DecimalSI),
						},
						Extension: &apiext.DeviceAllocationExtension{
							VirtualFunctions: []apiext.VirtualFunction{
								{
									BusID: "0001:a2:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			koordFakeClient := koordfake.NewSimpleClientset()
			deviceCR := tt.deviceCR.DeepCopy()
			_, err := koordFakeClient.SchedulingV1alpha1().Devices().Create(context.TODO(), deviceCR, metav1.CreateOptions{})
			assert.NoError(t, err)
			koordShareInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordFakeClient, 0)

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
					Labels: map[string]string{
						apiext.LabelGPUModel:           tt.modelSeries,
						apiext.LabelGPUPartitionPolicy: string(apiext.GPUPartitionPolicyHonor),
					},
				},
			}
			if tt.gpuPartitionPolicy != "" {
				node.Labels[apiext.LabelGPUPartitionPolicy] = string(tt.gpuPartitionPolicy)
			}
			kubeFakeClient := kubefake.NewSimpleClientset(node)
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

			podRequest := corev1.ResourceList{}
			if tt.gpuWanted > 0 {
				podRequest[apiext.ResourceNvidiaGPU] = *resource.NewQuantity(int64(tt.gpuWanted), resource.DecimalSI)
				combination, err := ValidateDeviceRequest(podRequest)
				assert.NoError(t, err)
				podRequest = ConvertDeviceRequest(podRequest, combination)
			}

			podRequest[apiext.ResourceRDMA] = *resource.NewQuantity(1, resource.DecimalSI)

			nodeDevice.lock.Lock()
			defer nodeDevice.lock.Unlock()

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
				setDefaultTestAllocateHints(t, pod)
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

			allocator := &AutopilotAllocator{
				state:      state,
				nodeDevice: nodeDevice,
				node:       node,
				pod:        pod,
			}

			allocations, status := allocator.Allocate(nil, nil, nil, nil)
			if !status.IsSuccess() != tt.wantErr {
				t.Errorf("Allocate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			fillGPUTotalMem(allocations, nodeDevice)
			sortDeviceAllocations(allocations)
			sortDeviceAllocations(tt.want)
			assert.Equal(t, tt.want, allocations)
		})
	}
}

func TestAllocateByTopology(t *testing.T) {
	tests := []struct {
		name                  string
		deviceCR              *schedulingv1alpha1.Device
		gpuWanted             int
		hostNetwork           bool
		exclusivePolicy       apiext.DeviceExclusivePolicy
		assignedDevices       apiext.DeviceAllocations
		requiredTopologyScope apiext.DeviceTopologyScope
		want                  apiext.DeviceAllocations
		wantErr               bool
	}{
		{
			name:            "allocate 1 GPU and 1 VF with assigned devices; topology scope BinPack",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       1,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     5,
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
									BusID: "0000:51:00.2",
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
									BusID: "0000:51:00.3",
									Minor: 1,
								},
							},
						},
					},
				},
			},
		},
		{
			name:            "allocate 2 GPU and 1 VF with assigned devices; topology scope BinPack",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       2,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     5,
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
									BusID: "0000:51:00.2",
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
			name:                  "allocate 1 GPU and 1 VF with assigned devices; requiredTopologyScope PCIE and BinPack",
			deviceCR:              fakeDeviceCR,
			gpuWanted:             2,
			requiredTopologyScope: apiext.DeviceTopologyScopePCIe,
			exclusivePolicy:       apiext.PCIExpressLevelDeviceExclusivePolicy,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     5,
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
									BusID: "0000:51:00.2",
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
			name:                  "allocate 1 GPU and 1 VF with assigned devices; requiredTopologyScope NUMANode",
			deviceCR:              fakeDeviceCR,
			gpuWanted:             4,
			requiredTopologyScope: apiext.DeviceTopologyScopeNUMANode,
			exclusivePolicy:       apiext.PCIExpressLevelDeviceExclusivePolicy,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     5,
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
									BusID: "0000:51:00.2",
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
			name:                  "allocate 1 GPU and 1 VF with assigned devices; requiredTopologyScope NUMANode, insufficient devices",
			deviceCR:              fakeDeviceCR,
			gpuWanted:             4,
			requiredTopologyScope: apiext.DeviceTopologyScopeNUMANode,
			exclusivePolicy:       apiext.PCIExpressLevelDeviceExclusivePolicy,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     5,
						Resources: gpuResourceList,
					},
					{
						Minor:     0,
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
									BusID: "0000:51:00.2",
									Minor: 0,
								},
							},
						},
					},
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
			wantErr: true,
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
						allocateHintWithRequiredTopologyScope(schedulingv1alpha1.GPU, tt.requiredTopologyScope),
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

func TestAllocateSharedGPU(t *testing.T) {
	tests := []struct {
		name                  string
		deviceCR              *schedulingv1alpha1.Device
		gpuWanted             int
		hostNetwork           bool
		exclusivePolicy       apiext.DeviceExclusivePolicy
		assignedDevices       apiext.DeviceAllocations
		requiredTopologyScope apiext.DeviceTopologyScope
		want                  apiext.DeviceAllocations
		wantErr               bool
	}{
		{
			name:            "allocate 1 GPU and 1 VF with assigned devices; topology scope BinPack",
			deviceCR:        fakeDeviceCR,
			gpuWanted:       1,
			exclusivePolicy: apiext.PCIExpressLevelDeviceExclusivePolicy,
			assignedDevices: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor:     5,
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
									BusID: "0000:51:00.2",
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
						Resources: gpuSharedResourceList,
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
									BusID: "0000:51:00.3",
									Minor: 1,
								},
							},
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
				podRequest[apiext.ResourceGPUShared] = *resource.NewQuantity(int64(tt.gpuWanted), resource.DecimalSI)
				podRequest[apiext.ResourceGPUMemoryRatio] = *resource.NewQuantity(int64(50), resource.DecimalSI)
				podRequest[apiext.ResourceGPUCore] = *resource.NewQuantity(int64(50), resource.DecimalSI)
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
						allocateHintWithRequiredTopologyScope(schedulingv1alpha1.GPU, tt.requiredTopologyScope),
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

func allocateHintWithRequiredTopologyScope(deviceType schedulingv1alpha1.DeviceType, topologyScope apiext.DeviceTopologyScope) func(hints apiext.DeviceAllocateHints) {
	return func(hints apiext.DeviceAllocateHints) {
		hint := hints[deviceType]
		if hint == nil {
			hint = &apiext.DeviceHint{}
			hints[deviceType] = hint
		}
		hint.RequiredTopologyScope = topologyScope
	}
}

func Test_removeZeroDevice(t *testing.T) {
	tests := []struct {
		name              string
		originalResources deviceResources
		want              deviceResources
	}{
		{
			name: "remove zero",
			originalResources: deviceResources{
				0: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
				1: corev1.ResourceList{},
				2: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
			},
			want: deviceResources{
				0: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
				2: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, removeZeroDevice(tt.originalResources))
		})
	}
}
