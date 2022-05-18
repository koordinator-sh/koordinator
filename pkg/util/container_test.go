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
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func Test_getContainerCgroupPathWithSystemdDriver(t *testing.T) {
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

func Test_getContainerCgroupPathWithCgroupfsDriver(t *testing.T) {
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

func Test_GetContainerCurTasks(t *testing.T) {
	type args struct {
		podParentDir string
		c            *corev1.ContainerStatus
	}
	type field struct {
		containerParentDir string
		tasksFileStr       string
		invalidPath        bool
	}
	tests := []struct {
		name    string
		args    args
		field   field
		want    []int
		wantErr bool
	}{
		{
			name: "throw an error for empty input",
			args: args{
				c: &corev1.ContainerStatus{},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "parse tasks correctly",
			field: field{
				containerParentDir: "pod0/cri-containerd-1.scope",
				tasksFileStr:       "22264\n22265\n22266\n22267\n29925\n29926\n37587\n41340\n45169\n",
			},
			args: args{
				podParentDir: "pod0",
				c: &corev1.ContainerStatus{
					ContainerID: "containerd://1",
				},
			},
			want:    []int{22264, 22265, 22266, 22267, 29925, 29926, 37587, 41340, 45169},
			wantErr: false,
		},
		{
			name: "throw an error for invalid path",
			field: field{
				containerParentDir: "pod0/cri-containerd-1.scope",
				tasksFileStr:       "22264\n22265\n22266\n22267\n29925\n29926\n37587\n41340\n45169\n",
				invalidPath:        true,
			},
			args: args{
				podParentDir: "pod0",
				c: &corev1.ContainerStatus{
					ContainerID: "containerd://1",
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "parse error",
			field: field{
				containerParentDir: "pod0/cri-containerd-1.scope",
				tasksFileStr:       "22264\n22265\n22266\n22587\nabs",
			},
			args: args{
				podParentDir: "pod0",
				c: &corev1.ContainerStatus{
					ContainerID: "containerd://1",
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "parse empty",
			field: field{
				containerParentDir: "pod0/cri-containerd-1.scope",
				tasksFileStr:       "",
			},
			args: args{
				podParentDir: "pod0",
				c: &corev1.ContainerStatus{
					ContainerID: "containerd://1",
				},
			},
			want:    nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cgroupRootDir := t.TempDir()

			dname := filepath.Join(cgroupRootDir, system.CgroupCPUDir, tt.field.containerParentDir)
			err := os.MkdirAll(dname, 0700)
			assert.NoError(t, err)
			fname := filepath.Join(dname, system.CPUTaskFileName)
			_ = ioutil.WriteFile(fname, []byte(tt.field.tasksFileStr), 0666)

			system.Conf = &system.Config{
				CgroupRootDir: cgroupRootDir,
			}
			// reset Formatter after testing
			rawParentDir := system.CgroupPathFormatter.ParentDir
			system.CgroupPathFormatter.ParentDir = ""
			defer func() {
				system.CgroupPathFormatter.ParentDir = rawParentDir
			}()
			if tt.field.invalidPath {
				system.Conf.CgroupRootDir = "invalidPath"
			}

			got, err := GetContainerCurTasks(tt.args.podParentDir, tt.args.c)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_FindContainerIdAndStatusByName(t *testing.T) {

	type args struct {
		name                  string
		podStatus             *corev1.PodStatus
		containerName         string
		expectContainerStatus *corev1.ContainerStatus
		expectContainerId     string
		expectErr             bool
	}

	tests := []args{
		{
			name: "testValidContainerId",
			podStatus: &corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:        "main",
						ContainerID: fmt.Sprintf("docker://%s", "main"),
					},
					{
						Name:        "sidecar",
						ContainerID: fmt.Sprintf("docker://%s", "sidecar"),
					},
				},
			},
			containerName: "main",
			expectContainerStatus: &corev1.ContainerStatus{
				Name:        "main",
				ContainerID: fmt.Sprintf("docker://%s", "main"),
			},
			expectContainerId: "main",
			expectErr:         false,
		},
		{
			name: "testInValidContainerId",
			podStatus: &corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:        "main",
						ContainerID: "main",
					},
					{
						Name:        "sidecar",
						ContainerID: "sidecar",
					},
				},
			},
			containerName:         "main",
			expectContainerStatus: nil,
			expectContainerId:     "",
			expectErr:             true,
		},
		{
			name: "testNotfoundContainer",
			podStatus: &corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:        "sidecar",
						ContainerID: fmt.Sprintf("docker://%s", "sidecar"),
					},
				},
			},
			containerName:         "main",
			expectContainerStatus: nil,
			expectContainerId:     "",
			expectErr:             true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotContainerId, gotContainerStatus, gotErr := FindContainerIdAndStatusByName(tt.podStatus, tt.containerName)
			assert.Equal(t, tt.expectContainerId, gotContainerId, "checkContainerId")
			assert.Equal(t, tt.expectContainerStatus, gotContainerStatus, "checkContainerStatus")
			assert.Equal(t, tt.expectErr, gotErr != nil, "checkError")
		})
	}
}
