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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_GetKubeQosRelativePath(t *testing.T) {
	guaranteedPathSystemd := GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
	assert.Equal(t, filepath.Clean(system.KubeRootNameSystemd), guaranteedPathSystemd)

	burstablePathSystemd := GetPodQoSRelativePath(corev1.PodQOSBurstable)
	assert.Equal(t, filepath.Join(system.KubeRootNameSystemd, system.KubeBurstableNameSystemd), burstablePathSystemd)

	besteffortPathSystemd := GetPodQoSRelativePath(corev1.PodQOSBestEffort)
	assert.Equal(t, filepath.Join(system.KubeRootNameSystemd, system.KubeBesteffortNameSystemd), besteffortPathSystemd)

	system.SetupCgroupPathFormatter(system.Cgroupfs)
	guaranteedPathCgroupfs := GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
	assert.Equal(t, filepath.Clean(system.KubeRootNameCgroupfs), guaranteedPathCgroupfs)

	burstablePathCgroupfs := GetPodQoSRelativePath(corev1.PodQOSBurstable)
	assert.Equal(t, filepath.Join(system.KubeRootNameCgroupfs, system.KubeBurstableNameCgroupfs), burstablePathCgroupfs)

	besteffortPathCgroupfs := GetPodQoSRelativePath(corev1.PodQOSBestEffort)
	assert.Equal(t, filepath.Join(system.KubeRootNameCgroupfs, system.KubeBesteffortNameCgroupfs), besteffortPathCgroupfs)
}

func TestGetCgroupPathsByTargetDepth(t *testing.T) {
	type fields struct {
		prepareFn func(helper *system.FileTestUtil)
	}
	type args struct {
		qos           corev1.PodQOSClass
		resourceType  system.ResourceType
		relativeDepth int
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []string
		wantErr bool
	}{
		{
			name: "get cgroup resource failed",
			args: args{
				qos:           corev1.PodQOSGuaranteed,
				resourceType:  "unknown_resource_xxx",
				relativeDepth: 1,
			},
			wantErr: true,
		},
		{
			name: "get root cgroup path failed",
			args: args{
				qos:           corev1.PodQOSGuaranteed,
				resourceType:  system.CPUSetCPUSName,
				relativeDepth: 1,
			},
			wantErr: true,
		},
		{
			name: "get qos-level cgroup path successfully",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.SetCgroupsV2(false)
					cpuset, _ := system.GetCgroupResource(system.CPUSetCPUSName)
					qosCgroupDir := GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					helper.WriteCgroupFileContents(qosCgroupDir, cpuset, "0-63")
				},
			},
			args: args{
				qos:           corev1.PodQOSGuaranteed,
				resourceType:  system.CPUSetCPUSName,
				relativeDepth: 0,
			},
			wantErr: false,
			want: []string{
				GetPodQoSRelativePath(corev1.PodQOSGuaranteed),
			},
		},
		{
			name: "get pod-level cgroup path successfully",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.SetCgroupsV2(false)
					cpuset, _ := system.GetCgroupResource(system.CPUSetCPUSName)
					qosCgroupDir := GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					qosCgroupDir1 := GetPodQoSRelativePath(corev1.PodQOSBurstable)
					qosCgroupDir2 := GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(qosCgroupDir, cpuset, "0-63")
					helper.WriteCgroupFileContents(qosCgroupDir1, cpuset, "0-63")
					helper.WriteCgroupFileContents(qosCgroupDir2, cpuset, "0-63")
					podCgroupDir := filepath.Join(qosCgroupDir, "/kubepods-test-guaranteed-pod.slice")
					podCgroupDir1 := filepath.Join(qosCgroupDir2, "/kubepods-test-besteffort-pod-0.slice")
					podCgroupDir2 := filepath.Join(qosCgroupDir2, "/kubepods-test-besteffort-pod-1.slice")
					helper.WriteCgroupFileContents(podCgroupDir, cpuset, "0-63")
					helper.WriteCgroupFileContents(podCgroupDir1, cpuset, "0-63")
					helper.WriteCgroupFileContents(podCgroupDir2, cpuset, "0-31")
				},
			},
			args: args{
				qos:           corev1.PodQOSBestEffort,
				resourceType:  system.CPUSetCPUSName,
				relativeDepth: PodCgroupPathRelativeDepth,
			},
			wantErr: false,
			want: []string{
				filepath.Join(GetPodQoSRelativePath(corev1.PodQOSBestEffort), "/kubepods-test-besteffort-pod-0.slice"),
				filepath.Join(GetPodQoSRelativePath(corev1.PodQOSBestEffort), "/kubepods-test-besteffort-pod-1.slice"),
			},
		},
		{
			name: "get container-level cgroup path successfully",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.SetCgroupsV2(false)
					cpuset, _ := system.GetCgroupResource(system.CPUSetCPUSName)
					qosCgroupDir := GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					qosCgroupDir1 := GetPodQoSRelativePath(corev1.PodQOSBurstable)
					qosCgroupDir2 := GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(qosCgroupDir, cpuset, "0-63")
					helper.WriteCgroupFileContents(qosCgroupDir1, cpuset, "0-63")
					helper.WriteCgroupFileContents(qosCgroupDir2, cpuset, "0-63")
					containerCgroupDir := filepath.Join(qosCgroupDir, "/kubepods-test-guaranteed-pod.slice/cri-containerd-xxx.scope")
					containerCgroupDir1 := filepath.Join(qosCgroupDir2, "/kubepods-test-besteffort-pod-0.slice/cri-containerd-yyy.scope")
					containerCgroupDir2 := filepath.Join(qosCgroupDir2, "/kubepods-test-besteffort-pod-1.slice/cri-containerd-zzz.scope")
					helper.WriteCgroupFileContents(containerCgroupDir, cpuset, "0-63")
					helper.WriteCgroupFileContents(containerCgroupDir1, cpuset, "0-63")
					helper.WriteCgroupFileContents(containerCgroupDir2, cpuset, "0-31")
				},
			},
			args: args{
				qos:           corev1.PodQOSBestEffort,
				resourceType:  system.CPUSetCPUSName,
				relativeDepth: ContainerCgroupPathRelativeDepth,
			},
			wantErr: false,
			want: []string{
				filepath.Join(GetPodQoSRelativePath(corev1.PodQOSBestEffort), "/kubepods-test-besteffort-pod-0.slice/cri-containerd-yyy.scope"),
				filepath.Join(GetPodQoSRelativePath(corev1.PodQOSBestEffort), "/kubepods-test-besteffort-pod-1.slice/cri-containerd-zzz.scope"),
			},
		},
		{
			name: "get container-level cgroup path successfully on cgroup-v2",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.SetCgroupsV2(true)
					memoryLimit, _ := system.GetCgroupResource(system.MemoryLimitName)
					qosCgroupDir := GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					qosCgroupDir1 := GetPodQoSRelativePath(corev1.PodQOSBurstable)
					qosCgroupDir2 := GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(qosCgroupDir, memoryLimit, "107374182400")
					helper.WriteCgroupFileContents(qosCgroupDir1, memoryLimit, "107374182400")
					helper.WriteCgroupFileContents(qosCgroupDir2, memoryLimit, "107374182400")
					containerCgroupDir := filepath.Join(qosCgroupDir, "/kubepods-test-guaranteed-pod.slice/cri-containerd-xxx.scope")
					containerCgroupDir1 := filepath.Join(qosCgroupDir2, "/kubepods-test-besteffort-pod-0.slice/cri-containerd-yyy.scope")
					containerCgroupDir2 := filepath.Join(qosCgroupDir2, "/kubepods-test-besteffort-pod-1.slice/cri-containerd-zzz.scope")
					helper.WriteCgroupFileContents(containerCgroupDir, memoryLimit, "1073741824")
					helper.WriteCgroupFileContents(containerCgroupDir1, memoryLimit, "1073741824")
					helper.WriteCgroupFileContents(containerCgroupDir2, memoryLimit, "1073741824")
				},
			},
			args: args{
				qos:           corev1.PodQOSBestEffort,
				resourceType:  system.MemoryLimitName,
				relativeDepth: ContainerCgroupPathRelativeDepth,
			},
			wantErr: false,
			want: []string{
				filepath.Join(GetPodQoSRelativePath(corev1.PodQOSBestEffort), "/kubepods-test-besteffort-pod-0.slice/cri-containerd-yyy.scope"),
				filepath.Join(GetPodQoSRelativePath(corev1.PodQOSBestEffort), "/kubepods-test-besteffort-pod-1.slice/cri-containerd-zzz.scope"),
			},
		},
		{
			name: "get pod-level cgroup path for guaranteed successfully",
			fields: fields{
				prepareFn: func(helper *system.FileTestUtil) {
					helper.SetCgroupsV2(false)
					cpuset, _ := system.GetCgroupResource(system.CPUSetCPUSName)
					qosCgroupDir := GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
					qosCgroupDir1 := GetPodQoSRelativePath(corev1.PodQOSBurstable)
					qosCgroupDir2 := GetPodQoSRelativePath(corev1.PodQOSBestEffort)
					helper.WriteCgroupFileContents(qosCgroupDir, cpuset, "0-63")
					helper.WriteCgroupFileContents(qosCgroupDir1, cpuset, "0-63")
					helper.WriteCgroupFileContents(qosCgroupDir2, cpuset, "0-63")
					containerCgroupDir := filepath.Join(qosCgroupDir, "/kubepods-test-guaranteed-pod.slice")
					containerCgroupDir1 := filepath.Join(qosCgroupDir2, "/kubepods-test-besteffort-pod-0.slice")
					containerCgroupDir2 := filepath.Join(qosCgroupDir2, "/kubepods-test-besteffort-pod-1.slice")
					helper.WriteCgroupFileContents(containerCgroupDir, cpuset, "0-63")
					helper.WriteCgroupFileContents(containerCgroupDir1, cpuset, "0-63")
					helper.WriteCgroupFileContents(containerCgroupDir2, cpuset, "0-31")
				},
			},
			args: args{
				qos:           corev1.PodQOSGuaranteed,
				resourceType:  system.CPUSetCPUSName,
				relativeDepth: PodCgroupPathRelativeDepth,
			},
			wantErr: false,
			want: []string{
				GetPodQoSRelativePath(corev1.PodQOSBestEffort),
				GetPodQoSRelativePath(corev1.PodQOSBurstable),
				filepath.Join(GetPodQoSRelativePath(corev1.PodQOSGuaranteed), "/kubepods-test-guaranteed-pod.slice"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}

			got, gotErr := GetCgroupPathsByTargetDepth(tt.args.qos, tt.args.resourceType, tt.args.relativeDepth)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
		})
	}
}
