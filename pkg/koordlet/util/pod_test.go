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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_GetRootCgroupCPUSetDirWithSystemdDriver(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(false)
	system.SetupCgroupPathFormatter(system.Systemd)
	defer system.SetupCgroupPathFormatter(system.Systemd)
	tests := []struct {
		name string
		args corev1.PodQOSClass
		want string
	}{
		{
			name: "default",
			args: "",
			want: "/host-cgroup/cpuset/kubepods.slice",
		},
		{
			name: "Guaranteed",
			args: corev1.PodQOSGuaranteed,
			want: "/host-cgroup/cpuset/kubepods.slice",
		},
		{
			name: "Burstable",
			args: corev1.PodQOSBurstable,
			want: "/host-cgroup/cpuset/kubepods.slice/kubepods-burstable.slice",
		},
		{
			name: "Best-effort",
			args: corev1.PodQOSBestEffort,
			want: "/host-cgroup/cpuset/kubepods.slice/kubepods-besteffort.slice",
		},
	}
	for _, tt := range tests {
		system.Conf = system.NewDsModeConfig()
		t.Run(tt.name, func(t *testing.T) {
			got := GetRootCgroupCPUSetDir(tt.args)
			if tt.want != got {
				t.Errorf("getRootCgroupDir want %v but got %v", tt.want, got)
			}
		})
	}
}

func Test_GetRootCgroupCPUSetDirWithCgroupfsDriver(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(false)
	system.SetupCgroupPathFormatter(system.Cgroupfs)
	defer system.SetupCgroupPathFormatter(system.Systemd)
	tests := []struct {
		name string
		args corev1.PodQOSClass
		want string
	}{
		{
			name: "default",
			args: "",
			want: "/host-cgroup/cpuset/kubepods",
		},
		{
			name: "Guaranteed",
			args: corev1.PodQOSGuaranteed,
			want: "/host-cgroup/cpuset/kubepods",
		},
		{
			name: "Burstable",
			args: corev1.PodQOSBurstable,
			want: "/host-cgroup/cpuset/kubepods/burstable",
		},
		{
			name: "Best-effort",
			args: corev1.PodQOSBestEffort,
			want: "/host-cgroup/cpuset/kubepods/besteffort",
		},
	}
	for _, tt := range tests {
		system.Conf = system.NewDsModeConfig()
		t.Run(tt.name, func(t *testing.T) {
			got := GetRootCgroupCPUSetDir(tt.args)
			if tt.want != got {
				t.Errorf("getRootCgroupDir want %v but got %v", tt.want, got)
			}
		})
	}
}

func TestGetPIDsInPod(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.SetCgroupsV2(false)
	system.SetupCgroupPathFormatter(system.Systemd)
	defer system.SetupCgroupPathFormatter(system.Systemd)
	dir := t.TempDir()
	system.Conf.CgroupRootDir = dir

	p1 := "/cpu/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/docker-703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf.scope/cgroup.procs"
	p1CgroupPath := filepath.Join(dir, p1)
	if err := writeCgroupContent(p1CgroupPath, []byte("12\n23")); err != nil {
		t.Fatal(err)
	}

	p2 := "/cpu/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/docker-703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4acff.scope/cgroup.procs"
	p2CgroupPath := filepath.Join(dir, p2)
	if err := writeCgroupContent(p2CgroupPath, []byte("45\n67")); err != nil {
		t.Fatal(err)
	}
	type args struct {
		podParentDir string
		cs           []corev1.ContainerStatus
	}
	tests := []struct {
		name    string
		args    args
		want    []uint32
		wantErr bool
	}{
		{
			name: "cgroup",
			args: args{
				podParentDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod6553a60b_2b97_442a_b6da_a5704d81dd98.slice/",
				cs: []corev1.ContainerStatus{
					{
						ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4abaf",
					},
					{
						ContainerID: "docker://703b1b4e811f56673d68f9531204e5dd4963e734e2929a7056fd5f33fde4acff",
					},
				},
			},
			want:    []uint32{12, 23, 45, 67},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetPIDsInPod(tt.args.podParentDir, tt.args.cs)
			assert.Equal(t, tt.wantErr, err != nil)
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

func TestGetPodSandboxContainerID(t *testing.T) {
	type fields struct {
		otherContaienrIDs []string
	}
	type args struct {
		pod *corev1.Pod
	}
	var tests = []struct {
		name    string
		fields  fields
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "pod-start-no-sandbox-container",
			fields: fields{
				otherContaienrIDs: []string{},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
						UID:  "test-pod-uid",
					},
					Status: corev1.PodStatus{},
				},
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "pod-start-sandbox-no-container",
			fields: fields{
				otherContaienrIDs: []string{
					"containerd://testPodSandboxHashID",
				},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
						UID:  "test-pod-uid",
					},
					Status: corev1.PodStatus{},
				},
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "pod-start-containerd-sandbox-and-container",
			fields: fields{
				otherContaienrIDs: []string{
					"containerd://testPodSandboxHashID",
					"containerd://testContainerHashID",
				},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
						UID:  "test-pod-uid",
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name:        "test-container-name",
								ContainerID: "containerd://testContainerHashID",
								Started:     nil,
							},
						},
					},
				},
			},
			want:    "containerd://testPodSandboxHashID",
			wantErr: false,
		},
		{
			name: "pod-start-docker-sandbox-and-container",
			fields: fields{
				otherContaienrIDs: []string{
					"docker://testPodSandboxHashID",
					"docker://testContainerHashID",
				},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
						UID:  "test-pod-uid",
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name:        "test-container-name",
								ContainerID: "docker://testContainerHashID",
								Started:     nil,
							},
						},
					},
				},
			},
			want:    "docker://testPodSandboxHashID",
			wantErr: false,
		},
		{
			name: "pod-start-new-container-but-status-has-not-synced",
			fields: fields{
				otherContaienrIDs: []string{
					"docker://testPodSandboxHashID",
					"docker://testContainerHashIDNew",
				},
			},
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-pod",
						UID:  "test-pod-uid",
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name:        "test-container-name",
								ContainerID: "docker://testContainerHashID",
								Started:     nil,
							},
						},
					},
				},
			},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHelper := system.NewFileTestUtil(t)

			podCgroupDir := GetPodCgroupParentDir(tt.args.pod)
			testHelper.WriteCgroupFileContents(podCgroupDir, system.CPUSet, "")

			for _, dirCreatedcontainerID := range tt.fields.otherContaienrIDs {
				containerPath, err := GetContainerCgroupParentDirByID(podCgroupDir, dirCreatedcontainerID)
				assert.NoError(t, err)
				testHelper.WriteCgroupFileContents(containerPath, system.CPUSet, "")
			}

			got, err := GetPodSandboxContainerID(tt.args.pod)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetPodSandboxContainerID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetPodSandboxContainerID() got = %v, want %v", got, tt.want)
			}
		})
	}
}
