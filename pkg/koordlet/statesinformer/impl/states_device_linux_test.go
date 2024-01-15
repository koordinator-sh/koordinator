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

package impl

import (
	"context"
	"testing"

	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulingfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
)

func Test_reportGPUDevice(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	fakeClient := schedulingfake.NewSimpleClientset().SchedulingV1alpha1().Devices()
	ctl := gomock.NewController(t)
	mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
	var gpuDeviceInfo koordletutil.GPUDevices
	gpuDeviceInfo = []koordletutil.GPUDeviceInfo{
		{UUID: "1", Minor: 1, MemoryTotal: 8000},
		{UUID: "2", Minor: 2, MemoryTotal: 10000},
		{UUID: "3", Minor: 3, MemoryTotal: 8000, BusID: "0000:00:08.0", NodeID: 0, PCIE: "pci0000:00"},
	}
	mockMetricCache.EXPECT().Get(koordletutil.GPUDeviceType).Return(gpuDeviceInfo, true)
	r := &statesInformer{
		deviceClient: fakeClient,
		metricsCache: mockMetricCache,
		states: &PluginState{
			informerPlugins: map[PluginName]informerPlugin{
				nodeInformerName: &nodeInformer{
					node: testNode,
				},
			},
		},
		getGPUDriverAndModelFunc: func() (string, string) {
			return "A100", "470"
		},
	}
	r.reportDevice()
	expectedDevices := []schedulingv1alpha1.DeviceInfo{
		{
			UUID:   "1",
			Minor:  pointer.Int32(1),
			Type:   schedulingv1alpha1.GPU,
			Health: true,
			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
				extension.ResourceGPUMemory:      *resource.NewQuantity(8000, resource.BinarySI),
				extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
			},
		},
		{
			UUID:   "2",
			Minor:  pointer.Int32(2),
			Type:   schedulingv1alpha1.GPU,
			Health: true,
			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
				extension.ResourceGPUMemory:      *resource.NewQuantity(10000, resource.BinarySI),
				extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
			},
		},
		{
			UUID:   "3",
			Minor:  pointer.Int32(3),
			Type:   schedulingv1alpha1.GPU,
			Health: true,
			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
				extension.ResourceGPUMemory:      *resource.NewQuantity(8000, resource.BinarySI),
				extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
			},
			Topology: &schedulingv1alpha1.DeviceTopology{
				SocketID: -1,
				NodeID:   0,
				PCIEID:   "pci0000:00",
				BusID:    "0000:00:08.0",
			},
		},
	}
	device, err := fakeClient.Get(context.TODO(), "test", metav1.GetOptions{})
	assert.Equal(t, nil, err)
	assert.Equal(t, device.Spec.Devices, expectedDevices)

	gpuDeviceInfo = append(gpuDeviceInfo, koordletutil.GPUDeviceInfo{
		UUID:        "4",
		Minor:       4,
		MemoryTotal: 10000,
	})

	mockMetricCache.EXPECT().Get(koordletutil.GPUDeviceType).Return(gpuDeviceInfo, true)
	r.reportDevice()

	expectedDevices = append(expectedDevices, schedulingv1alpha1.DeviceInfo{
		UUID:   "4",
		Minor:  pointer.Int32(4),
		Type:   schedulingv1alpha1.GPU,
		Health: true,
		Resources: map[corev1.ResourceName]resource.Quantity{
			extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
			extension.ResourceGPUMemory:      *resource.NewQuantity(10000, resource.BinarySI),
			extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
		},
	})
	device, err = fakeClient.Get(context.TODO(), "test", metav1.GetOptions{})
	assert.Equal(t, nil, err)
	assert.Equal(t, device.Spec.Devices, expectedDevices)
	assert.Equal(t, device.Labels[extension.LabelGPUModel], "A100")
	assert.Equal(t, device.Labels[extension.LabelGPUDriverVersion], "470")
}
