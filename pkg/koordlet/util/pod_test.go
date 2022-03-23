package util

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_GetRootCgroupCPUSetDirWithSystemdDriver(t *testing.T) {
	sysutil.SetupCgroupPathFormatter(sysutil.Systemd)
	defer sysutil.SetupCgroupPathFormatter(sysutil.Systemd)
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
		sysutil.Conf = sysutil.NewDsModeConfig()
		t.Run(tt.name, func(t *testing.T) {
			got := GetRootCgroupCPUSetDir(tt.args)
			if tt.want != got {
				t.Errorf("getRootCgroupDir want %v but got %v", tt.want, got)
			}
		})
	}
}

func Test_GetRootCgroupCPUSetDirWithCgroupfsDriver(t *testing.T) {
	sysutil.SetupCgroupPathFormatter(sysutil.Cgroupfs)
	defer sysutil.SetupCgroupPathFormatter(sysutil.Systemd)
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
		sysutil.Conf = sysutil.NewDsModeConfig()
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
	var cgroupRootDir string
	cgroupRootDir, _ = ioutil.TempDir("", "GetRootCgroupCurCPUSet")
	dname := filepath.Join(cgroupRootDir, sysutil.CgroupCPUSetDir)
	err := os.MkdirAll(dname, 0700)
	if err != nil {
		t.Errorf("failed to prepare tmpdir in %v, err: %v", "GetRootCgroupCurCPUSet", err)
		return
	}
	fname := filepath.Join(dname, sysutil.CPUSFileName)
	_ = ioutil.WriteFile(fname, []byte{'1', ',', '2'}, 0666)

	sysutil.Conf = &sysutil.Config{
		CgroupRootDir: cgroupRootDir,
	}
	// reset Formatter after testing
	rawParentDir := sysutil.CgroupPathFormatter.ParentDir
	sysutil.CgroupPathFormatter.ParentDir = ""
	defer func() {
		sysutil.CgroupPathFormatter.ParentDir = rawParentDir
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
