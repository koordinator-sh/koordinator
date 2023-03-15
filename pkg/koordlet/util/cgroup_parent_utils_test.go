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

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_GetPodKubeRelativePath(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Systemd)
	system.Conf = system.NewDsModeConfig()

	assert := assert.New(t)

	testCases := []struct {
		name string
		pod  *corev1.Pod
		path string
	}{
		{
			name: "guaranteed",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID("uid1"),
				},
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSGuaranteed,
				},
			},
			path: "/kubepods-poduid1.slice",
		},
		{
			name: "burstable",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID("uid1"),
				},
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSBurstable,
				},
			},
			path: "kubepods-burstable.slice/kubepods-burstable-poduid1.slice",
		},
		{
			name: "besteffort",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID("uid1"),
				},
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSBestEffort,
				},
			},
			path: "kubepods-besteffort.slice/kubepods-besteffort-poduid1.slice",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := GetPodKubeRelativePath(tc.pod)
			assert.Equal(tc.path, path)
		})
	}
}

func Test_GetPodCgroupPath(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Systemd)

	assert := assert.New(t)

	testCases := []struct {
		name         string
		relativePath string
		path         string
		cgroup       system.Resource
	}{
		{
			name:         "cpuacct",
			relativePath: "pod1",
			path:         "/host-cgroup/cpuacct/kubepods.slice/pod1/cpuacct.usage",
			cgroup:       system.CPUAcctUsage,
		},
		{
			name:         "cpushare",
			relativePath: "pod1",
			path:         "/host-cgroup/cpu/kubepods.slice/pod1/cpu.shares",
			cgroup:       system.CPUShares,
		},
		{
			name:         "cfsperiod",
			relativePath: "pod1",
			path:         "/host-cgroup/cpu/kubepods.slice/pod1/cpu.cfs_period_us",
			cgroup:       system.CPUCFSPeriod,
		},
		{
			name:         "cfsperiod",
			relativePath: "pod1",
			path:         "/host-cgroup/cpu/kubepods.slice/pod1/cpu.cfs_quota_us",
			cgroup:       system.CPUCFSQuota,
		},
		{
			name:         "memorystat",
			relativePath: "pod1",
			path:         "/host-cgroup/memory/kubepods.slice/pod1/memory.stat",
			cgroup:       system.MemoryStat,
		},
		{
			name:         "memorylimit",
			relativePath: "pod1",
			path:         "/host-cgroup/memory/kubepods.slice/pod1/memory.limit_in_bytes",
			cgroup:       system.MemoryLimit,
		},
		{
			name:         "cpustat",
			relativePath: "pod1",
			path:         "/host-cgroup/cpu/kubepods.slice/pod1/cpu.stat",
			cgroup:       system.CPUStat,
		},
		{
			name:         "cpupressure",
			relativePath: "pod1",
			path:         "/host-cgroup/cpuacct/kubepods.slice/pod1/cpu.pressure",
			cgroup:       system.CPUAcctCPUPressure,
		},
		{
			name:         "mempressure",
			relativePath: "pod1",
			path:         "/host-cgroup/cpuacct/kubepods.slice/pod1/memory.pressure",
			cgroup:       system.CPUAcctMemoryPressure,
		},
		{
			name:         "iopressure",
			relativePath: "pod1",
			path:         "/host-cgroup/cpuacct/kubepods.slice/pod1/io.pressure",
			cgroup:       system.CPUAcctIOPressure,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			podPath := GetPodCgroupDirWithKube(tc.relativePath)
			path := system.GetCgroupFilePath(podPath, tc.cgroup)
			assert.Equal(tc.path, path)
		})
	}
}

func Test_GetKubeQoSByCgroupParent(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Systemd)
	assert := assert.New(t)

	testCases := []struct {
		name              string
		path              string
		wantPriorityClass corev1.PodQOSClass
	}{
		{
			name:              "burstable",
			path:              "kubepods-burstable.slice/kubepods-poduid1.slice",
			wantPriorityClass: corev1.PodQOSBurstable,
		},
		{
			name:              "besteffort",
			path:              "kubepods-besteffort.slice/kubepods-poduid1.slice",
			wantPriorityClass: corev1.PodQOSBestEffort,
		},
		{
			name:              "guaranteed",
			path:              "kubepods-poduid1.slice",
			wantPriorityClass: corev1.PodQOSGuaranteed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(tc.wantPriorityClass, GetKubeQoSByCgroupParent(tc.path))
		})
	}
}

func TestGetContainerCgroupPathWithKube_SystemdDriver(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Systemd)
	defer system.SetupCgroupPathFormatter(system.Systemd)
	type args struct {
		podParentDir string
		c            *corev1.ContainerStatus
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "docker-container",
			args: args{
				podParentDir: "kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/",
				c: &corev1.ContainerStatus{
					ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
				},
			},
			want:    "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/docker-703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf.scope",
			wantErr: false,
		},
		{
			name: "containerd-container",
			args: args{
				podParentDir: "kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/",
				c: &corev1.ContainerStatus{
					ContainerID: "containerd://413715dc061efe71c16ef989f2f2ff5cf999a7dc905bb7078eda82cbb38210ec",
				},
			},
			want:    "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/cri-containerd-413715dc061efe71c16ef989f2f2ff5cf999a7dc905bb7078eda82cbb38210ec.scope",
			wantErr: false,
		},
		{
			name: "invalid-container",
			args: args{
				podParentDir: "",
				c: &corev1.ContainerStatus{
					ContainerID: "invalid",
				},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "unsupported-container",
			args: args{
				podParentDir: "",
				c: &corev1.ContainerStatus{
					ContainerID: "crio://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
				},
			},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		system.Conf = system.NewDsModeConfig()
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetContainerCgroupPathWithKube(tt.args.podParentDir, tt.args.c)
			if (err != nil) != tt.wantErr {
				t.Errorf("getContainerCgroupPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getContainerCgroupPath() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetContainerCgroupPathWithKube_CgroupfsDriver(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Cgroupfs)
	defer system.SetupCgroupPathFormatter(system.Systemd)
	type args struct {
		podParentDir string
		c            *corev1.ContainerStatus
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "docker-container",
			args: args{
				podParentDir: "besteffort/pod6553a60b-2b97-442a-b6da-a5704d81dd98/",
				c: &corev1.ContainerStatus{
					ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
				},
			},
			want:    "kubepods/besteffort/pod6553a60b-2b97-442a-b6da-a5704d81dd98/703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
			wantErr: false,
		},
		{
			name: "containerd-container",
			args: args{
				podParentDir: "besteffort/pod6553a60b-2b97-442a-b6da-a5704d81dd98/",
				c: &corev1.ContainerStatus{
					ContainerID: "containerd://413715dc061efe71c16ef989f2f2ff5cf999a7dc905bb7078eda82cbb38210ec",
				},
			},
			want:    "kubepods/besteffort/pod6553a60b-2b97-442a-b6da-a5704d81dd98/413715dc061efe71c16ef989f2f2ff5cf999a7dc905bb7078eda82cbb38210ec",
			wantErr: false,
		},
		{
			name: "invalid-container",
			args: args{
				podParentDir: "",
				c: &corev1.ContainerStatus{
					ContainerID: "invalid",
				},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "unsupported-container",
			args: args{
				podParentDir: "",
				c: &corev1.ContainerStatus{
					ContainerID: "crio://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
				},
			},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		system.Conf = system.NewDsModeConfig()
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetContainerCgroupPathWithKube(tt.args.podParentDir, tt.args.c)
			if (err != nil) != tt.wantErr {
				t.Errorf("getContainerCgroupPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getContainerCgroupPath() got = %v, want %v", got, tt.want)
			}
		})
	}
}
