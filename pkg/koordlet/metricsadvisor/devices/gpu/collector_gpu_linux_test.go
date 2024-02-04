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

package gpu

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_gpuUsageDetailRecord_GetNodeGPUUsage(t *testing.T) {
	collectTime := time.Now()
	type fields struct {
		deviceCount      int
		devices          []*device
		processesMetrics map[uint32][]*rawGPUMetric
	}
	tests := []struct {
		name   string
		fields fields
		want   []metriccache.MetricSample
	}{
		{
			name: "single device",
			fields: fields{
				deviceCount: 1,
				devices: []*device{
					{Minor: 0, DeviceUUID: "test-device1", MemoryTotal: 8000},
				},
				processesMetrics: map[uint32][]*rawGPUMetric{
					122: {{SMUtil: 70, MemoryUsed: 1500}},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.NodeGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("0", "test-device1"),
					collectTime,
					70,
				),
				buildMetricSample(
					metriccache.NodeGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("0", "test-device1"),
					collectTime,
					1500,
				),
			},
		},
		{
			name: "multiple device",
			fields: fields{
				deviceCount: 2,
				devices: []*device{
					{Minor: 0, DeviceUUID: "test-device1", MemoryTotal: 8000},
					{Minor: 1, DeviceUUID: "test-device2", MemoryTotal: 9000},
				},
				processesMetrics: map[uint32][]*rawGPUMetric{
					122: {{SMUtil: 70, MemoryUsed: 1500}, nil},
					222: {nil, {SMUtil: 50, MemoryUsed: 1000}},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.NodeGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("0", "test-device1"),
					collectTime,
					70,
				),
				buildMetricSample(
					metriccache.NodeGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("0", "test-device1"),
					collectTime,
					1500,
				),
				buildMetricSample(
					metriccache.NodeGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("1", "test-device2"),
					collectTime,
					50,
				),
				buildMetricSample(
					metriccache.NodeGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("1", "test-device2"),
					collectTime,
					1000,
				),
			},
		},
		{
			name: "process on multiple device",
			fields: fields{
				deviceCount: 2,
				devices: []*device{
					{Minor: 0, DeviceUUID: "test-device1", MemoryTotal: 8000},
					{Minor: 1, DeviceUUID: "test-device2", MemoryTotal: 9000},
				},
				processesMetrics: map[uint32][]*rawGPUMetric{
					122: {{SMUtil: 70, MemoryUsed: 1500}, {SMUtil: 30, MemoryUsed: 1000}},
					222: {{SMUtil: 20, MemoryUsed: 1000}, {SMUtil: 50, MemoryUsed: 1000}},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.NodeGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("0", "test-device1"),
					collectTime,
					90,
				),
				buildMetricSample(
					metriccache.NodeGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("0", "test-device1"),
					collectTime,
					2500,
				),
				buildMetricSample(
					metriccache.NodeGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("1", "test-device2"),
					collectTime,
					80,
				),
				buildMetricSample(
					metriccache.NodeGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.GPU("1", "test-device2"),
					collectTime,
					2000,
				),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &gpuDeviceManager{
				collectTime:      collectTime,
				deviceCount:      tt.fields.deviceCount,
				devices:          tt.fields.devices,
				processesMetrics: tt.fields.processesMetrics,
			}
			got := g.getNodeGPUUsage()
			assert.Equal(t, got, tt.want)
		})
	}
}

func Test_gpuUsageDetailRecord_getPodOrContinerTotalGPUUsageOfPIDs(t *testing.T) {
	collectTime := time.Now()
	type fields struct {
		deviceCount      int
		devices          []*device
		processesMetrics map[uint32][]*rawGPUMetric
	}
	type args struct {
		id      string
		isPodID bool
		pids    []uint32
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []metriccache.MetricSample
	}{
		{
			name: "single device",
			args: args{
				id:      "test-pod",
				isPodID: true,
				pids:    []uint32{122},
			},
			fields: fields{
				deviceCount: 1,
				devices: []*device{
					{Minor: 0, DeviceUUID: "test-device1", MemoryTotal: 14000},
				},
				processesMetrics: map[uint32][]*rawGPUMetric{
					122: {{SMUtil: 70, MemoryUsed: 1500}},
					123: {{SMUtil: 20, MemoryUsed: 1000}},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "test-device1"),
					collectTime,
					70,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "test-device1"),
					collectTime,
					1500,
				),
			},
		},
		{
			name: "multiple device",
			args: args{
				id:      "test-pod",
				isPodID: true,
				pids:    []uint32{122, 222},
			},
			fields: fields{
				deviceCount: 2,
				devices: []*device{
					{Minor: 0, DeviceUUID: "test-device1", MemoryTotal: 14000},
					{Minor: 1, DeviceUUID: "test-device2", MemoryTotal: 24000},
				},
				processesMetrics: map[uint32][]*rawGPUMetric{
					122: {{SMUtil: 70, MemoryUsed: 1500}, nil},
					222: {nil, {SMUtil: 50, MemoryUsed: 1000}},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "test-device1"),
					collectTime,
					70,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "test-device1"),
					collectTime,
					1500,
				),
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "1", "test-device2"),
					collectTime,
					50,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "1", "test-device2"),
					collectTime,
					1000,
				),
			},
		},
		{
			name: "multiple device-1",
			args: args{
				id:      "test-pod",
				isPodID: true,
				pids:    []uint32{122},
			},
			fields: fields{
				deviceCount: 2,
				devices: []*device{
					{Minor: 0, DeviceUUID: "test-device1", MemoryTotal: 14000},
					{Minor: 1, DeviceUUID: "test-device2", MemoryTotal: 24000},
				},
				processesMetrics: map[uint32][]*rawGPUMetric{
					122: {{SMUtil: 70, MemoryUsed: 1500}, nil},
					222: {nil, {SMUtil: 50, MemoryUsed: 1000}},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "test-device1"),
					collectTime,
					70,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "test-device1"),
					collectTime,
					1500,
				),
			},
		},
		{
			name: "multiple device and multiple processes",
			args: args{
				id:      "test-pod",
				isPodID: true,
				pids:    []uint32{122, 222},
			},
			fields: fields{
				deviceCount: 2,
				devices: []*device{
					{Minor: 0, DeviceUUID: "test-device1", MemoryTotal: 14000},
					{Minor: 1, DeviceUUID: "test-device2", MemoryTotal: 24000},
				},
				processesMetrics: map[uint32][]*rawGPUMetric{
					122: {{SMUtil: 70, MemoryUsed: 1500}, {SMUtil: 50, MemoryUsed: 1000}},
					222: {{SMUtil: 10, MemoryUsed: 1000}, {SMUtil: 40, MemoryUsed: 3000}},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "test-device1"),
					collectTime,
					80,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "test-device1"),
					collectTime,
					2500,
				),
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "1", "test-device2"),
					collectTime,
					90,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-pod", "1", "test-device2"),
					collectTime,
					4000,
				),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &gpuDeviceManager{
				collectTime:      collectTime,
				deviceCount:      tt.fields.deviceCount,
				devices:          tt.fields.devices,
				processesMetrics: tt.fields.processesMetrics,
			}
			got := g.getPodOrContainerTotalGPUUsageOfPIDs(tt.args.id, tt.args.isPodID, tt.args.pids)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_gpuDeviceManager_getPodGPUUsage(t *testing.T) {
	collectTime := time.Now()
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(false)
	system.SetupCgroupPathFormatter(system.Systemd)
	defer system.SetupCgroupPathFormatter(system.Systemd)
	dir := t.TempDir()
	system.Conf.CgroupRootDir = dir

	p1 := "/cpu/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/docker-703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf.scope/cgroup.procs"
	p1CgroupPath := filepath.Join(dir, p1)
	if err := writeCgroupContent(p1CgroupPath, []byte("122\n222")); err != nil {
		t.Fatal(err)
	}

	p2 := "/cpu/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/docker-703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4acff.scope/cgroup.procs"
	p2CgroupPath := filepath.Join(dir, p2)
	if err := writeCgroupContent(p2CgroupPath, []byte("45\n67")); err != nil {
		t.Fatal(err)
	}

	type fields struct {
		gpuDeviceManager GPUDeviceManager
	}
	type args struct {
		podParentDir string
		uid          string
		cs           []corev1.ContainerStatus
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []metriccache.MetricSample
		wantErr bool
	}{
		{
			name: "multiple processes and multiple device",
			fields: fields{
				gpuDeviceManager: &gpuDeviceManager{
					collectTime: collectTime,
					deviceCount: 2,
					devices: []*device{
						{Minor: 0, DeviceUUID: "12", MemoryTotal: 14000},
						{Minor: 1, DeviceUUID: "23", MemoryTotal: 24000},
					},
					processesMetrics: map[uint32][]*rawGPUMetric{
						122: {{SMUtil: 70, MemoryUsed: 1500}, {SMUtil: 50, MemoryUsed: 1000}},
						222: {{SMUtil: 10, MemoryUsed: 1000}, {SMUtil: 40, MemoryUsed: 3000}},
					},
				},
			},
			args: args{
				podParentDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/",
				uid:          "test-1",
				cs: []corev1.ContainerStatus{
					{
						ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: v1.NewTime(time.Now()),
							},
						},
					},
					{
						ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4acff",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: v1.NewTime(time.Now()),
							},
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "0", "12"),
					collectTime,
					80,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "0", "12"),
					collectTime,
					2500,
				),
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "1", "23"),
					collectTime,
					90,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "1", "23"),
					collectTime,
					4000,
				),
			},
			wantErr: false,
		},
		{
			name: "multiple processes",
			fields: fields{
				gpuDeviceManager: &gpuDeviceManager{
					collectTime: collectTime,
					deviceCount: 2,
					devices: []*device{
						{Minor: 0, DeviceUUID: "12", MemoryTotal: 14000},
						{Minor: 1, DeviceUUID: "23", MemoryTotal: 24000},
					},
					processesMetrics: map[uint32][]*rawGPUMetric{
						122: {{SMUtil: 70, MemoryUsed: 1500}, nil},
						222: {nil, {SMUtil: 40, MemoryUsed: 3000}},
					},
				},
			},
			args: args{
				podParentDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/",
				uid:          "test-1",
				cs: []corev1.ContainerStatus{
					{
						ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: v1.NewTime(time.Now()),
							},
						},
					},
					{
						ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4acff",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: v1.NewTime(time.Now()),
							},
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "0", "12"),
					collectTime,
					70,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "0", "12"),
					collectTime,
					1500,
				),
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "1", "23"),
					collectTime,
					40,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "1", "23"),
					collectTime,
					3000,
				),
			},
			wantErr: false,
		},

		{
			name: "single processes",
			fields: fields{
				gpuDeviceManager: &gpuDeviceManager{
					collectTime: collectTime,
					deviceCount: 2,
					devices: []*device{
						{Minor: 0, DeviceUUID: "12", MemoryTotal: 14000},
						{Minor: 1, DeviceUUID: "23", MemoryTotal: 24000},
					},
					processesMetrics: map[uint32][]*rawGPUMetric{
						122: {nil, {SMUtil: 70, MemoryUsed: 1500}},
						222: {nil, {SMUtil: 20, MemoryUsed: 3000}},
					},
				},
			},
			args: args{
				podParentDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/",
				uid:          "test-1",
				cs: []corev1.ContainerStatus{
					{
						ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: v1.NewTime(time.Now()),
							},
						},
					},
					{
						ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4acff",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: v1.NewTime(time.Now()),
							},
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.PodGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "1", "23"),
					collectTime,
					90,
				),
				buildMetricSample(
					metriccache.PodGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.PodGPU("test-1", "1", "23"),
					collectTime,
					4500,
				),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.fields.gpuDeviceManager
			got, err := g.getPodGPUUsage(tt.args.uid, tt.args.podParentDir, tt.args.cs)
			if (err != nil) != tt.wantErr {
				t.Errorf("gpuDeviceManager.getPodGPUUsage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("gpuDeviceManager.getPodGPUUsage() = %v, want %v", got, tt.want)
			}
		})
	}
}
func Test_gpuDeviceManager_getContainerGPUUsage(t *testing.T) {
	collectTime := time.Now()
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(false)
	system.SetupCgroupPathFormatter(system.Systemd)
	defer system.SetupCgroupPathFormatter(system.Systemd)
	dir := t.TempDir()
	system.Conf.CgroupRootDir = dir

	p1 := "/cpu/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/docker-703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf.scope/cgroup.procs"
	p1CgroupPath := filepath.Join(dir, p1)
	if err := writeCgroupContent(p1CgroupPath, []byte("122\n222")); err != nil {
		t.Fatal(err)
	}

	p2 := "/cpu/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/docker-703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4acff.scope/cgroup.procs"
	p2CgroupPath := filepath.Join(dir, p2)
	if err := writeCgroupContent(p2CgroupPath, []byte("122")); err != nil {
		t.Fatal(err)
	}

	type fields struct {
		gpuDeviceManager GPUDeviceManager
	}
	type args struct {
		podParentDir string
		containerID  string
		c            *corev1.ContainerStatus
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []metriccache.MetricSample
		wantErr bool
	}{
		{
			name: "multiple processes and multiple device",
			fields: fields{
				gpuDeviceManager: &gpuDeviceManager{
					collectTime: collectTime,
					deviceCount: 2,
					devices: []*device{
						{Minor: 0, DeviceUUID: "12", MemoryTotal: 14000},
						{Minor: 1, DeviceUUID: "23", MemoryTotal: 24000},
					},
					processesMetrics: map[uint32][]*rawGPUMetric{
						122: {{SMUtil: 70, MemoryUsed: 1500}, {SMUtil: 50, MemoryUsed: 1000}},
						222: {{SMUtil: 10, MemoryUsed: 1000}, {SMUtil: 40, MemoryUsed: 3000}},
					},
				},
			},
			args: args{
				podParentDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/",
				containerID:  "test-1",
				c: &corev1.ContainerStatus{
					ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{
							StartedAt: v1.NewTime(time.Now()),
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.ContainerGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.ContainerGPU("test-1", "0", "12"),
					collectTime,
					80,
				),
				buildMetricSample(
					metriccache.ContainerGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.ContainerGPU("test-1", "0", "12"),
					collectTime,
					2500,
				),
				buildMetricSample(
					metriccache.ContainerGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.ContainerGPU("test-1", "1", "23"),
					collectTime,
					90,
				),
				buildMetricSample(
					metriccache.ContainerGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.ContainerGPU("test-1", "1", "23"),
					collectTime,
					4000,
				),
			},
			wantErr: false,
		},
		{
			name: "single processes and multiple device",
			fields: fields{
				gpuDeviceManager: &gpuDeviceManager{
					collectTime: collectTime,
					deviceCount: 2,
					devices: []*device{
						{Minor: 0, DeviceUUID: "12", MemoryTotal: 14000},
						{Minor: 1, DeviceUUID: "23", MemoryTotal: 24000},
					},
					processesMetrics: map[uint32][]*rawGPUMetric{
						122: {{SMUtil: 70, MemoryUsed: 1500}, {SMUtil: 50, MemoryUsed: 1000}},
						222: {{SMUtil: 10, MemoryUsed: 1000}, {SMUtil: 40, MemoryUsed: 3000}},
					},
				},
			},
			args: args{
				podParentDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/",
				containerID:  "test-1",
				c: &corev1.ContainerStatus{
					ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4acff",
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{
							StartedAt: v1.NewTime(time.Now()),
						},
					},
				},
			},
			want: []metriccache.MetricSample{
				buildMetricSample(
					metriccache.ContainerGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.ContainerGPU("test-1", "0", "12"),
					collectTime,
					70,
				),
				buildMetricSample(
					metriccache.ContainerGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.ContainerGPU("test-1", "0", "12"),
					collectTime,
					1500,
				),
				buildMetricSample(
					metriccache.ContainerGPUCoreUsageMetric,
					metriccache.MetricPropertiesFunc.ContainerGPU("test-1", "1", "23"),
					collectTime,
					50,
				),
				buildMetricSample(
					metriccache.ContainerGPUMemUsageMetric,
					metriccache.MetricPropertiesFunc.ContainerGPU("test-1", "1", "23"),
					collectTime,
					1000,
				),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.fields.gpuDeviceManager
			got, err := g.getContainerGPUUsage(tt.args.containerID, tt.args.podParentDir, tt.args.c)
			if (err != nil) != tt.wantErr {
				t.Errorf("gpuDeviceManager.getContainerGPUUsage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func writeCgroupContent(filePath string, content []byte) error {
	err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, content, 0655)
}

func Test_buildMetricSample(t *testing.T) {
	collectTime := time.Now()
	type args struct {
		mr    metriccache.MetricResource
		minor string
		uuid  string
		t     time.Time
		val   float64
	}
	tests := []struct {
		name string
		args args
		want metriccache.MetricSample
	}{
		{
			name: "GPUCore",
			args: args{
				mr:    metriccache.NodeGPUCoreUsageMetric,
				minor: "1",
				uuid:  "test1",
				t:     collectTime,
				val:   95,
			},
		},
		{
			name: "GPUMem",
			args: args{
				mr:    metriccache.NodeGPUMemUsageMetric,
				minor: "1",
				uuid:  "test1",
				t:     collectTime,
				val:   1200,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metricSample, err := tt.args.mr.GenerateSample(
				metriccache.MetricPropertiesFunc.GPU(tt.args.minor, tt.args.uuid),
				collectTime,
				tt.args.val,
			)
			assert.NoError(t, err)
			tt.want = metricSample
			assert.Equalf(t, tt.want, buildMetricSample(tt.args.mr, metriccache.MetricPropertiesFunc.GPU(tt.args.minor, tt.args.uuid), tt.args.t, tt.args.val), "buildMetricSample(%v, %v, %v, %v, %v)", tt.args.mr, tt.args.minor, tt.args.uuid, tt.args.t, tt.args.val)
		})
	}
}

func Test_gpuDeviceManager_deviceInfos(t *testing.T) {
	type fields struct {
		deviceCount int
		devices     []*device
	}
	tests := []struct {
		name   string
		fields fields
		want   metriccache.Devices
	}{
		{
			name: "empty device",
			fields: fields{
				deviceCount: 0,
				devices:     nil,
			},
			want: util.GPUDevices{},
		},
		{
			name: "device",
			fields: fields{
				deviceCount: 2,
				devices: []*device{
					{DeviceUUID: "1", Minor: 1, MemoryTotal: 2000},
					{DeviceUUID: "2", Minor: 2, MemoryTotal: 3000},
				},
			},
			want: util.GPUDevices{
				util.GPUDeviceInfo{UUID: "1", Minor: 1, MemoryTotal: 2000},
				util.GPUDeviceInfo{UUID: "2", Minor: 2, MemoryTotal: 3000},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &gpuDeviceManager{
				RWMutex:     sync.RWMutex{},
				deviceCount: tt.fields.deviceCount,
				devices:     tt.fields.devices,
			}
			assert.Equalf(t, tt.want, g.deviceInfos(), "deviceInfos()")
		})
	}
}
