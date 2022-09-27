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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func Test_GetRootCgroupCPUSetDirWithSystemdDriver(t *testing.T) {
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

func Test_GetPodRequest(t *testing.T) {
	type args struct {
		pod *corev1.Pod
	}
	tests := []struct {
		name string
		args args
		want corev1.ResourceList
	}{
		{
			name: "do not panic on an illegal-labeled pod",
			args: args{pod: &corev1.Pod{}},
			want: corev1.ResourceList{},
		},
		{
			name: "get correct pod request",
			args: args{
				pod: &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("4"),
										corev1.ResourceMemory: resource.MustParse("10Gi"),
									},
									Limits: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("4"),
										corev1.ResourceMemory: resource.MustParse("10Gi"),
									},
								},
							},
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("4"),
										corev1.ResourceMemory: resource.MustParse("8Gi"),
									},
									Limits: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("6"),
										corev1.ResourceMemory: resource.MustParse("12Gi"),
									},
								},
							},
						},
					},
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("18Gi"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPodRequest(tt.args.pod)
			if !got.Cpu().Equal(*tt.want.Cpu()) {
				t.Errorf("should get correct cpu request, want %v, got %v",
					tt.want.Cpu(), got.Cpu())
			}
			if !got.Memory().Equal(*tt.want.Memory()) {
				t.Errorf("should get correct memory request, want %v, got %v",
					tt.want.Memory(), got.Memory())
			}
		})
	}
}

func Test_GetRootCgroupCurCPUSet(t *testing.T) {
	// prepare testing tmp files
	cgroupRootDir := t.TempDir()
	dname := filepath.Join(cgroupRootDir, system.CgroupCPUSetDir)
	err := os.MkdirAll(dname, 0700)
	if err != nil {
		t.Errorf("failed to prepare tmpdir in %v, err: %v", "GetRootCgroupCurCPUSet", err)
		return
	}
	fname := filepath.Join(dname, system.CPUSFileName)
	_ = os.WriteFile(fname, []byte{'1', ',', '2'}, 0666)

	system.Conf = &system.Config{
		CgroupRootDir: cgroupRootDir,
	}
	// reset Formatter after testing
	rawParentDir := system.CgroupPathFormatter.ParentDir
	system.CgroupPathFormatter.ParentDir = ""
	defer func() {
		system.CgroupPathFormatter.ParentDir = rawParentDir
	}()

	wantCPUSet := []int32{1, 2}

	gotCPUSet, err := GetRootCgroupCurCPUSet(corev1.PodQOSGuaranteed)
	if err != nil {
		t.Errorf("failed to GetRootCgroupCurCPUSet, err: %v", err)
		return
	}
	if !reflect.DeepEqual(wantCPUSet, gotCPUSet) {
		t.Errorf("failed to GetRootCgroupCurCPUSet, want cpuset %v, got %v", wantCPUSet, gotCPUSet)
		return
	}
}

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

func Test_GetPodCgroupStatPath(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Systemd)

	assert := assert.New(t)

	testCases := []struct {
		name         string
		relativePath string
		path         string
		fn           func(p string) string
	}{
		{
			name:         "cpuacct",
			relativePath: "pod1",
			path:         "/host-cgroup/cpuacct/kubepods.slice/pod1/cpuacct.usage",
			fn: func(p string) string {
				return GetPodCgroupCPUAcctProcUsagePath(p)
			},
		},
		{
			name:         "cpushare",
			relativePath: "pod1",
			path:         "/host-cgroup/cpu/kubepods.slice/pod1/cpu.shares",
			fn: func(p string) string {
				return GetPodCgroupCPUSharePath(p)
			},
		},
		{
			name:         "cfsperiod",
			relativePath: "pod1",
			path:         "/host-cgroup/cpu/kubepods.slice/pod1/cpu.cfs_period_us",
			fn: func(p string) string {
				return GetPodCgroupCFSPeriodPath(p)
			},
		},
		{
			name:         "cfsperiod",
			relativePath: "pod1",
			path:         "/host-cgroup/cpu/kubepods.slice/pod1/cpu.cfs_quota_us",
			fn: func(p string) string {
				return GetPodCgroupCFSQuotaPath(p)
			},
		},
		{
			name:         "memorystat",
			relativePath: "pod1",
			path:         "/host-cgroup/memory/kubepods.slice/pod1/memory.stat",
			fn: func(p string) string {
				return GetPodCgroupMemStatPath(p)
			},
		},
		{
			name:         "memorylimit",
			relativePath: "pod1",
			path:         "/host-cgroup/memory/kubepods.slice/pod1/memory.limit_in_bytes",
			fn: func(p string) string {
				return GetPodCgroupMemLimitPath(p)
			},
		},
		{
			name:         "cpustat",
			relativePath: "pod1",
			path:         "/host-cgroup/cpu/kubepods.slice/pod1/cpu.stat",
			fn: func(p string) string {
				return GetPodCgroupCPUStatPath(p)
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.fn(tc.relativePath)
			assert.Equal(tc.path, path)
		})
	}
}

func Test_GetKubeQosClass(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Systemd)
	assert := assert.New(t)

	testCases := []struct {
		name              string
		pod               *corev1.Pod
		wantPriorityClass corev1.PodQOSClass
	}{
		{
			name: "Guaranteed from status",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSGuaranteed,
				},
			},
			wantPriorityClass: corev1.PodQOSGuaranteed,
		},
		{
			name: "Besteffort from status",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					QOSClass: corev1.PodQOSBestEffort,
				},
			},
			wantPriorityClass: corev1.PodQOSBestEffort,
		},
		{
			name: "Besteffort from resource",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{},
				},
			},
			wantPriorityClass: corev1.PodQOSBestEffort,
		},
		{
			name: "Burstable from resource",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("8"),
								},
							},
						},
					},
				},
			},
			wantPriorityClass: corev1.PodQOSBurstable,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(tc.wantPriorityClass, GetKubeQosClass(tc.pod))
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

func Test_GetPodBEMilliCPURequest(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Systemd)
	assert := assert.New(t)

	testCases := []struct {
		name        string
		pod         *corev1.Pod
		wantRequest int64
		wantLimit   int64
	}{
		{
			name: "one container",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU: resource.MustParse("2000"),
								},
								Limits: corev1.ResourceList{
									apiext.BatchCPU: resource.MustParse("4000"),
								},
							},
						},
					},
				},
			},
			wantRequest: 2000,
			wantLimit:   4000,
		},
		{
			name: "multiple container",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU: resource.MustParse("4000"),
								},
								Limits: corev1.ResourceList{
									apiext.BatchCPU: resource.MustParse("4000"),
								},
							},
						},
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU: resource.MustParse("2000"),
								},
								Limits: corev1.ResourceList{
									apiext.BatchCPU: resource.MustParse("4000"),
								},
							},
						},
					},
				},
			},
			wantRequest: 6000,
			wantLimit:   8000,
		},
		{
			name: "empty resource",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{},
						},
					},
				},
			},
			wantRequest: 0,
			wantLimit:   -1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(tc.wantRequest, GetPodBEMilliCPURequest(tc.pod))
			assert.Equal(tc.wantLimit, GetPodBEMilliCPULimit(tc.pod))
		})
	}
}

func Test_GetPodBEMemoryRequest(t *testing.T) {
	system.SetupCgroupPathFormatter(system.Systemd)
	assert := assert.New(t)

	testCases := []struct {
		name        string
		pod         *corev1.Pod
		wantRequest int64
		wantLimit   int64
	}{
		{
			name: "one container",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchMemory: resource.MustParse("2Mi"),
								},
								Limits: corev1.ResourceList{
									apiext.BatchMemory: resource.MustParse("4Mi"),
								},
							},
						},
					},
				},
			},
			wantRequest: 2097152,
			wantLimit:   4194304,
		},
		{
			name: "multiple container",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchMemory: resource.MustParse("2Mi"),
								},
								Limits: corev1.ResourceList{
									apiext.BatchMemory: resource.MustParse("4Mi"),
								},
							},
						},
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchMemory: resource.MustParse("2Mi"),
								},
								Limits: corev1.ResourceList{
									apiext.BatchMemory: resource.MustParse("2Mi"),
								},
							},
						},
					},
				},
			},
			wantRequest: 4194304,
			wantLimit:   6291456,
		},
		{
			name: "empty resource",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{},
						},
					},
				},
			},
			wantRequest: 0,
			wantLimit:   -1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(tc.wantRequest, GetPodBEMemoryByteRequestIgnoreUnlimited(tc.pod))
			assert.Equal(tc.wantLimit, GetPodBEMemoryByteLimit(tc.pod))
		})
	}
}

func Test_GetCPUSetFromPod(t *testing.T) {
	type args struct {
		podAnnotations map[string]string
		podAlloc       *apiext.ResourceStatus
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "get cpuset from annotation",
			args: args{
				podAnnotations: map[string]string{},
				podAlloc: &apiext.ResourceStatus{
					CPUSet: "2-4",
				},
			},
			want:    "2-4",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.args.podAlloc != nil {
				podAllocJson := DumpJSON(tt.args.podAlloc)
				tt.args.podAnnotations[apiext.AnnotationResourceStatus] = podAllocJson
			}
			got, err := GetCPUSetFromPod(tt.args.podAnnotations)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}
