//go:build linux
// +build linux

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
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulingfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
)

func Test_getGPUDeviceConditions(t *testing.T) {
	tests := []struct {
		name string
		gpu  *koordletutil.GPUDeviceInfo
	}{
		{
			name: "healthy GPU device",
			gpu: &koordletutil.GPUDeviceInfo{
				UUID:        "test-gpu-uuid",
				Minor:       0,
				MemoryTotal: 8000,
				Status: &koordletutil.DeviceStatus{
					Healthy: true,
				},
			},
		},
		{
			name: "unhealthy GPU device with Xid error",
			gpu: &koordletutil.GPUDeviceInfo{
				UUID:        "test-gpu-uuid-2",
				Minor:       1,
				MemoryTotal: 8000,
				Status: &koordletutil.DeviceStatus{
					Healthy:    false,
					ErrCode:    "Xid44",
					ErrMessage: "Xid error detected on device, error code: 44",
				},
			},
		},
		{
			name: "GPU device with device health check not supported",
			gpu: &koordletutil.GPUDeviceInfo{
				UUID:        "test-gpu-uuid-3",
				Minor:       2,
				MemoryTotal: 8000,
				Status: &koordletutil.DeviceStatus{
					Healthy:    false,
					ErrCode:    "DeviceHealthCheckNotSupported",
					ErrMessage: "device does not support health checking",
				},
			},
		},
		{
			name: "GPU device with Xid error code only",
			gpu: &koordletutil.GPUDeviceInfo{
				UUID:        "test-gpu-uuid-4",
				Minor:       3,
				MemoryTotal: 8000,
				Status: &koordletutil.DeviceStatus{
					Healthy: false,
					ErrCode: "Xid44",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getGPUDeviceConditions(tt.gpu)

			assert.NotNil(t, got, "GPU should return Conditions")
			assert.Equal(t, 1, len(got), "Should have exactly one Condition")
			assert.Equal(t, string(schedulingv1alpha1.DeviceConditionHealthy), got[0].Type)
			assert.NotEmpty(t, got[0].LastTransitionTime, "LastTransitionTime should not be empty")

			if tt.gpu.Status.Healthy {
				assert.Equal(t, metav1.ConditionTrue, got[0].Status)
				assert.Equal(t, "DeviceHealthy", got[0].Reason)
				assert.Equal(t, "device is healthy", got[0].Message)
			} else {
				assert.Equal(t, metav1.ConditionFalse, got[0].Status)

				if tt.gpu.Status.ErrCode == "DeviceHealthCheckNotSupported" {
					assert.Equal(t, "DeviceHealthCheckNotSupported", got[0].Reason)
					assert.Equal(t, tt.gpu.Status.ErrMessage, got[0].Message)
				} else if tt.gpu.Status.ErrCode != "" {
					assert.Equal(t, tt.gpu.Status.ErrCode, got[0].Reason)
					if tt.gpu.Status.ErrMessage != "" {
						assert.Equal(t, tt.gpu.Status.ErrMessage, got[0].Message)
					} else {
						assert.Equal(t, "device is unhealthy", got[0].Message)
					}
				}
			}
		})
	}
}

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
	mockMetricCache.EXPECT().Get(koordletutil.RDMADeviceType).Return(nil, false)
	mockMetricCache.EXPECT().Get(koordletutil.XPUDeviceType).Return(nil, false)
	r := &statesInformer{
		config: &Config{
			XPUEnforceCollectFromDeviceInfos: false,
		},
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
			Minor:  ptr.To[int32](1),
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
			Minor:  ptr.To[int32](2),
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
			Minor:  ptr.To[int32](3),
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

	// Set fixed time for conditions to avoid time comparison issues
	fixedTime := metav1.Now()
	for i := range device.Spec.Devices {
		for j := range device.Spec.Devices[i].Conditions {
			device.Spec.Devices[i].Conditions[j].LastTransitionTime = fixedTime
		}
	}
	for i := range expectedDevices {
		for j := range expectedDevices[i].Conditions {
			expectedDevices[i].Conditions[j].LastTransitionTime = fixedTime
		}
	}

	assert.Equal(t, device.Spec.Devices, expectedDevices)

	gpuDeviceInfo = append(gpuDeviceInfo, koordletutil.GPUDeviceInfo{
		UUID:        "4",
		Minor:       4,
		MemoryTotal: 10000,
	})
	rdmaDeviceInfo := koordletutil.RDMADevices{
		{
			BusID:      "0000:00:09.0",
			DeviceCode: "0000",
			ID:         "0000:00:09.0",
			Labels:     map[string]string{"label1": "value1"},
			Minor:      0,
			NetDev:     "ib0",
			NodeID:     0,
			Health:     true,
		},
	}
	mockMetricCache.EXPECT().Get(koordletutil.GPUDeviceType).Return(gpuDeviceInfo, true)
	mockMetricCache.EXPECT().Get(koordletutil.RDMADeviceType).Return(rdmaDeviceInfo, true)
	mockMetricCache.EXPECT().Get(koordletutil.XPUDeviceType).Return(nil, false)
	r.reportDevice()

	expectedDevices = append(expectedDevices, schedulingv1alpha1.DeviceInfo{
		UUID:   "4",
		Minor:  ptr.To[int32](4),
		Type:   schedulingv1alpha1.GPU,
		Health: true,
		Resources: map[corev1.ResourceName]resource.Quantity{
			extension.ResourceGPUCore:        *resource.NewQuantity(100, resource.DecimalSI),
			extension.ResourceGPUMemory:      *resource.NewQuantity(10000, resource.BinarySI),
			extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
		},
	})
	expectedDevices = append(expectedDevices, schedulingv1alpha1.DeviceInfo{
		UUID:   "0000:00:09.0",
		Minor:  ptr.To[int32](0),
		Type:   schedulingv1alpha1.RDMA,
		Health: true,
		Resources: map[corev1.ResourceName]resource.Quantity{
			extension.ResourceRDMA: *resource.NewQuantity(100, resource.DecimalSI),
		},
		Topology: &schedulingv1alpha1.DeviceTopology{
			SocketID: -1,
			NodeID:   0,
			PCIEID:   "",
			BusID:    "0000:00:09.0",
		},
	})
	device, err = fakeClient.Get(context.TODO(), "test", metav1.GetOptions{})
	assert.Equal(t, nil, err)

	// Set fixed time for conditions to avoid time comparison issues
	fixedTime = metav1.Now()
	for i := range device.Spec.Devices {
		for j := range device.Spec.Devices[i].Conditions {
			device.Spec.Devices[i].Conditions[j].LastTransitionTime = fixedTime
		}
	}
	for i := range expectedDevices {
		for j := range expectedDevices[i].Conditions {
			expectedDevices[i].Conditions[j].LastTransitionTime = fixedTime
		}
	}

	assert.Equal(t, device.Spec.Devices, expectedDevices)
	assert.Equal(t, device.Labels[extension.LabelGPUVendor], extension.GPUVendorNVIDIA)
	assert.Equal(t, device.Labels[extension.LabelGPUModel], "A100")
	assert.Equal(t, device.Labels[extension.LabelGPUDriverVersion], "470")
}

func Test_reportXPUDevice(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
	}
	fakeClient := schedulingfake.NewSimpleClientset().SchedulingV1alpha1().Devices()
	ctl := gomock.NewController(t)
	mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
	xpuDeviceInfo := koordletutil.XPUDevices{
		{
			Vendor: "huawei",
			Model:  "Ascend-910B",
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102003",
			Minor:  "0",
			Resources: map[string]string{
				extension.ResourceHuaweiNPUCore:     "32",
				extension.ResourceHuaweiNPUCPU:      "14",
				string(extension.ResourceGPUMemory): "32Gi",
				extension.ResourceHuaweiNPUDVPP:     "100",
			},
			Topology: &koordletutil.DeviceTopology{
				P2PLinks: []koordletutil.DeviceP2PLink{
					{
						PeerMinor: "1,2,3,g",
						Type:      "HCCS",
					},
				},
				MustHonorPartition: true,
				SocketID:           "0",
				NodeID:             "0",
				PCIEID:             "0000:00:08.0",
				BusID:              "0000:00:08.0",
			},
			Status: &koordletutil.DeviceStatus{
				Healthy: true,
			},
		},
		{
			Vendor: "huawei",
			Model:  "Ascend-910B",
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102001",
			Minor:  "1",
			Resources: map[string]string{
				extension.ResourceHuaweiNPUCore:     "32",
				extension.ResourceHuaweiNPUCPU:      "14",
				string(extension.ResourceGPUMemory): "32Gi",
				extension.ResourceHuaweiNPUDVPP:     "100",
			},
			Topology: &koordletutil.DeviceTopology{
				P2PLinks: []koordletutil.DeviceP2PLink{
					{
						PeerMinor: "0,1,2,3",
						Type:      "HCCS",
					},
				},
				MustHonorPartition: true,
				SocketID:           "0",
				NodeID:             "1",
				PCIEID:             "0000:00:08.0",
				BusID:              "0000:00:08.1",
			},
			Status: &koordletutil.DeviceStatus{
				Healthy: true,
			},
		},
		{
			Vendor: "huawei",
			Model:  "Ascend-910B",
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102002",
			Minor:  "2",
			Resources: map[string]string{
				extension.ResourceHuaweiNPUCore:     "32",
				extension.ResourceHuaweiNPUCPU:      "14",
				string(extension.ResourceGPUMemory): "32Gi",
				extension.ResourceHuaweiNPUDVPP:     "100",
			},
			Topology: &koordletutil.DeviceTopology{
				SocketID: "0",
				NodeID:   "2",
				PCIEID:   "0000:00:08.0",
				BusID:    "0000:00:08.2",
			},
		},
		{
			Vendor: "huawei",
			Model:  "Ascend-910B",
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102004",
			Minor:  "4",
			Resources: map[string]string{
				extension.ResourceHuaweiNPUCore:     "32",
				extension.ResourceHuaweiNPUCPU:      "14",
				string(extension.ResourceGPUMemory): "32Gi",
				extension.ResourceHuaweiNPUDVPP:     "100",
			},
			Topology: &koordletutil.DeviceTopology{
				P2PLinks: []koordletutil.DeviceP2PLink{
					{
						PeerMinor: "5,6,7,7,g",
						Type:      "HCCS",
					},
				},
				MustHonorPartition: true,
				SocketID:           "0",
				NodeID:             "4",
				PCIEID:             "0000:00:08.0",
				BusID:              "0000:00:08.4",
			},
			Status: &koordletutil.DeviceStatus{
				Healthy: false,
				ErrCode: "-8001",
			},
		},
	}

	gpuDeviceInfo := []koordletutil.GPUDeviceInfo{
		{UUID: "1", Minor: 1, MemoryTotal: 8000},
		{UUID: "2", Minor: 2, MemoryTotal: 10000},
		{UUID: "3", Minor: 3, MemoryTotal: 8000, BusID: "0000:00:08.0", NodeID: 0, PCIE: "pci0000:00"},
	}
	mockMetricCache.EXPECT().Get(koordletutil.GPUDeviceType).Return(gpuDeviceInfo, true).AnyTimes()
	mockMetricCache.EXPECT().Get(koordletutil.XPUDeviceType).Return(xpuDeviceInfo, true)
	mockMetricCache.EXPECT().Get(koordletutil.RDMADeviceType).Return(nil, false)
	r := &statesInformer{
		config: &Config{
			XPUEnforceCollectFromDeviceInfos: false,
		},
		deviceClient: fakeClient,
		metricsCache: mockMetricCache,
		states: &PluginState{
			informerPlugins: map[PluginName]informerPlugin{
				nodeInformerName: &nodeInformer{
					node: testNode,
				},
			},
		},
	}
	r.reportDevice()

	npuCoreQuantity, _ := resource.ParseQuantity("32")
	npuCpuQuantity, _ := resource.ParseQuantity("14")
	gpuMemQuantity, _ := resource.ParseQuantity("32Gi")
	dvppQuantity, _ := resource.ParseQuantity("100")
	fixedTime := metav1.Now()
	expectedDevices := []schedulingv1alpha1.DeviceInfo{
		{
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102003",
			Minor:  ptr.To[int32](0),
			Type:   schedulingv1alpha1.GPU,
			Health: true,
			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				extension.ResourceHuaweiNPUCore:  npuCoreQuantity,
				extension.ResourceHuaweiNPUCPU:   npuCpuQuantity,
				extension.ResourceGPUMemory:      gpuMemQuantity,
				extension.ResourceHuaweiNPUDVPP:  dvppQuantity,
			},
			Topology: &schedulingv1alpha1.DeviceTopology{
				SocketID: -1,
				NodeID:   0,
				PCIEID:   "0000:00:08.0",
				BusID:    "0000:00:08.0",
			},
			Conditions: []metav1.Condition{
				{
					Type:               string(schedulingv1alpha1.DeviceConditionHealthy),
					Status:             metav1.ConditionTrue,
					Reason:             "DeviceHealthy",
					Message:            "device is healthy",
					LastTransitionTime: fixedTime,
				},
			},
		},
		{
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102001",
			Minor:  ptr.To[int32](1),
			Type:   schedulingv1alpha1.GPU,
			Health: true,
			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				extension.ResourceHuaweiNPUCore:  npuCoreQuantity,
				extension.ResourceHuaweiNPUCPU:   npuCpuQuantity,
				extension.ResourceGPUMemory:      gpuMemQuantity,
				extension.ResourceHuaweiNPUDVPP:  dvppQuantity,
			},
			Topology: &schedulingv1alpha1.DeviceTopology{
				SocketID: -1,
				NodeID:   1,
				PCIEID:   "0000:00:08.0",
				BusID:    "0000:00:08.1",
			},
			Conditions: []metav1.Condition{
				{
					Type:               string(schedulingv1alpha1.DeviceConditionHealthy),
					Status:             metav1.ConditionTrue,
					Reason:             "DeviceHealthy",
					Message:            "device is healthy",
					LastTransitionTime: fixedTime,
				},
			},
		},
		{
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102002",
			Minor:  ptr.To[int32](2),
			Type:   schedulingv1alpha1.GPU,
			Health: true,
			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				extension.ResourceHuaweiNPUCore:  npuCoreQuantity,
				extension.ResourceHuaweiNPUCPU:   npuCpuQuantity,
				extension.ResourceGPUMemory:      gpuMemQuantity,
				extension.ResourceHuaweiNPUDVPP:  dvppQuantity,
			},
			Topology: &schedulingv1alpha1.DeviceTopology{
				SocketID: -1,
				NodeID:   2,
				PCIEID:   "0000:00:08.0",
				BusID:    "0000:00:08.2",
			},
		},
		{
			UUID:   "185011D4-21104518-A0C4ED94-14CC040A-56102004",
			Minor:  ptr.To[int32](4),
			Type:   schedulingv1alpha1.GPU,
			Health: false,

			Resources: map[corev1.ResourceName]resource.Quantity{
				extension.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				extension.ResourceHuaweiNPUCore:  npuCoreQuantity,
				extension.ResourceHuaweiNPUCPU:   npuCpuQuantity,
				extension.ResourceGPUMemory:      gpuMemQuantity,
				extension.ResourceHuaweiNPUDVPP:  dvppQuantity,
			},
			Topology: &schedulingv1alpha1.DeviceTopology{
				SocketID: -1,
				NodeID:   4,
				PCIEID:   "0000:00:08.0",
				BusID:    "0000:00:08.4",
			},
			Conditions: []metav1.Condition{
				{
					Type:               string(schedulingv1alpha1.DeviceConditionHealthy),
					Status:             metav1.ConditionFalse,
					Reason:             "-8001",
					Message:            "device is unhealthy",
					LastTransitionTime: fixedTime,
				},
			},
		},
	}
	device, err := fakeClient.Get(context.TODO(), "test", metav1.GetOptions{})
	device.Spec.Devices[0].Conditions[0].LastTransitionTime = fixedTime
	device.Spec.Devices[1].Conditions[0].LastTransitionTime = fixedTime
	device.Spec.Devices[3].Conditions[0].LastTransitionTime = fixedTime

	assert.Equal(t, nil, err)
	assert.Equal(t, device.Spec.Devices, expectedDevices)

	assert.Equal(t, device.Labels[extension.LabelGPUModel], "Ascend-910B")
	assert.Equal(t, device.Labels[extension.LabelGPUVendor], "huawei")
	assert.Equal(t, device.Labels[extension.LabelGPUPartitionPolicy], "Honor")
	assert.Equal(t, device.Annotations[extension.AnnotationGPUPartitions], "{\"4\":[{\"minors\":[0,1,2,3],\"gpuLinkType\":\"HCCS\",\"allocationScore\":1},{\"minors\":[4,5,6,7],\"gpuLinkType\":\"HCCS\",\"allocationScore\":1}]}")
}

func Test_reportGPUDeviceUnhealthy(t *testing.T) {
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
		{UUID: "healthy-gpu-1", BusID: "0000:44:00.0", NodeID: 0, PCIE: "pci0000:3a", Minor: 0, MemoryTotal: 8000},
		{UUID: "healthy-gpu-2", BusID: "0000:4d:00.0", NodeID: 0, PCIE: "pci0000:4a", Minor: 1, MemoryTotal: 8000},
		{UUID: "unhealthy-gpu", BusID: "0000:58:00.0", NodeID: 0, PCIE: "pci0000:54", Minor: 2, MemoryTotal: 8000},
	}
	mockMetricCache.EXPECT().Get(koordletutil.GPUDeviceType).Return(gpuDeviceInfo, true)
	mockMetricCache.EXPECT().Get(koordletutil.RDMADeviceType).Return(nil, false)
	mockMetricCache.EXPECT().Get(koordletutil.XPUDeviceType).Return(nil, false)
	r := &statesInformer{
		config: &Config{
			XPUEnforceCollectFromDeviceInfos: false,
		},
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

	// Mark a GPU as unhealthy
	r.unhealthyGPU = map[string]*unhealthyGPUInfo{
		"0000:58:00.0": {
			errCode:    "Xid44",
			errMessage: "Xid error detected on device, error code: 44",
		},
	}

	r.reportDevice()
	device, err := fakeClient.Get(context.TODO(), "test", metav1.GetOptions{})
	assert.Equal(t, nil, err)

	// Verify the number of Devices
	assert.Equal(t, 3, len(device.Spec.Devices))

	for _, dev := range device.Spec.Devices {
		if dev.Topology.BusID == "0000:58:00.0" {
			// Unhealthy GPUs should have conditions
			assert.NotNil(t, dev.Conditions)
			assert.Equal(t, 1, len(dev.Conditions))
			condition := dev.Conditions[0]
			assert.Equal(t, string(schedulingv1alpha1.DeviceConditionHealthy), condition.Type)
			assert.Equal(t, metav1.ConditionFalse, condition.Status)
			assert.Equal(t, "Xid44", condition.Reason)
			assert.Equal(t, "Xid error detected on device, error code: 44", condition.Message)
			assert.NotEmpty(t, condition.LastTransitionTime)
		} else {
			// Healthy GPUs should have nil Conditions
			assert.Nil(t, dev.Conditions)
		}
	}
}
