//go:build linux
// +build linux

package system

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func joinCmdlineStr(opts []string) string {
	return strings.Join(opts, string(byte(0)))
}

func Test_GuessKubeletCgroupDriver(t *testing.T) {
	t.Run("kubelet has stopped", func(t *testing.T) {
		helper := NewFileTestUtil(t)
		defer helper.Cleanup()
		_, err := GuessCgroupDriverFromKubelet()
		assert.Error(t, err)
	})
	tests := []struct {
		name          string
		kubeletConfig string
		cmdLine       func(procDir string) string
		want          CgroupDriverType
		wantErr       bool
	}{
		{
			name:          "kubelet cmdline args is invalid",
			kubeletConfig: "",
			cmdLine: func(procDir string) string {
				return ""
			},
			want:    "",
			wantErr: true,
		},
		{
			name:          "kubelet set driver as systemd in cmdline args",
			kubeletConfig: "",
			cmdLine: func(procDir string) string {
				return joinCmdlineStr([]string{
					"/usr/bin/kubelet",
					"--cgroup-driver=systemd"})
			},
			want:    Systemd,
			wantErr: false,
		},
		{
			name:          "kubelet set driver as cgroupfs in cmdline args",
			kubeletConfig: "",
			cmdLine: func(procDir string) string {
				return joinCmdlineStr([]string{
					"/usr/bin/kubelet",
					"--cgroup-driver=cgroupfs"})
			},
			want:    Cgroupfs,
			wantErr: false,
		},
		{
			name:          "neither args nor kubelet config file is set, fallback cgroupfs as cgroup driver",
			kubeletConfig: "",
			cmdLine: func(procDir string) string {
				return joinCmdlineStr([]string{
					"/usr/bin/kubelet",
					"--network-plugin=cni"})
			},
			want:    Cgroupfs,
			wantErr: false,
		},
		{
			name: "'cgroupDriver' is not set in kubelet config file, fallback cgroupfs as cgroup driver",
			kubeletConfig: `apiVersion: kubelet.config.k8s.io/v1beta1
			kind: KubeletConfiguration
			clusterDomain: cluster.local`,
			cmdLine: func(procDir string) string {
				return joinCmdlineStr([]string{
					"/usr/bin/kubelet",
					fmt.Sprintf("--config=%s", filepath.Join(procDir, "config.yaml"))})
			},
			want:    Cgroupfs,
			wantErr: false,
		},
		{
			name: "kubelet set driver as systemd in kubelet config(absolute path)",
			kubeletConfig: `apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: systemd`,
			cmdLine: func(procDir string) string {
				return joinCmdlineStr([]string{
					"/usr/bin/kubelet",
					fmt.Sprintf("--config=%s", filepath.Join(procDir, "config.yaml"))})
			},
			want:    Systemd,
			wantErr: false,
		},
		{
			name: "kubelet set driver as systemd in kubelet config(relative path)",
			kubeletConfig: `apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: systemd`,
			cmdLine: func(procDir string) string {
				if err := os.Symlink(procDir, filepath.Join(procDir, "cwd")); err != nil {
					t.Error(err)
				}
				return joinCmdlineStr([]string{
					"/usr/bin/kubelet",
					"--config=./config.yaml",
				})
			},
			want:    Systemd,
			wantErr: false,
		},
		{
			name:          "kubelet set driver as systemd in kubelet config(invalid path)",
			kubeletConfig: "",
			cmdLine: func(procDir string) string {
				return joinCmdlineStr([]string{
					"/usr/bin/kubelet",
					fmt.Sprintf("--config=%s", filepath.Join(procDir, "invalid.yaml"))})
			},
			want:    "",
			wantErr: true,
		},
	}
	kubeletPid := 42

	oldPidof := PidOf
	PidOf = func(procRoot, name string) ([]int, error) {
		if name == "kubelet" {
			return []int{kubeletPid}, nil
		} else {
			return []int{}, nil
		}
	}
	defer func() { PidOf = oldPidof }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()

			helper.CreateFile("/proc/1/ns/stub")
			os.Symlink("/proc/1/ns/mnt", helper.GetAbsoluteFilePath("/proc/1/ns/mnt"))

			helper.MkProcSubDirAll(strconv.Itoa(kubeletPid))

			cmdLine := tt.cmdLine(path.Join(Conf.ProcRootDir, strconv.Itoa(kubeletPid)))
			envSetup(tt.kubeletConfig, cmdLine, strconv.Itoa(kubeletPid), helper)

			got, err := GuessCgroupDriverFromKubelet()
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func envSetup(kubeletConfig, kubeletCmd string, pidPath string, helper *FileTestUtil) {
	helper.CreateProcSubFile(path.Join(pidPath, "config.yaml"))
	helper.WriteProcSubFileContents(path.Join(pidPath, "config.yaml"), kubeletConfig)
	helper.CreateProcSubFile(path.Join(pidPath, "cmdline"))
	helper.WriteProcSubFileContents(path.Join(pidPath, "cmdline"), kubeletCmd)
}

func Test_GuessCgroupDriverFromCgroupName(t *testing.T) {
	tests := []struct {
		name     string
		envSetup func(cgroupRoot string)
		want     CgroupDriverType
	}{
		{
			name: "'kubepods' and 'kubepods.slice' both exist",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods"), 0755)
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods.slice"), 0755)
			},
			want: "",
		},
		{
			name:     "neither 'kubepods' nor 'kubepods.slice' exists",
			envSetup: func(cgroupRoot string) {},
			want:     "",
		},
		{
			name: "'kubepods.slice' exist",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods.slice"), 0755)
			},
			want: Systemd,
		},
		{
			name: "'kubepods' exist",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods"), 0755)
			},
			want: Cgroupfs,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpCgroupRoot, _ := ioutil.TempDir("", "cgroup")
			os.MkdirAll(tmpCgroupRoot, 0555)
			defer os.RemoveAll(tmpCgroupRoot)

			Conf = &Config{
				CgroupRootDir: tmpCgroupRoot,
			}

			tt.envSetup(tmpCgroupRoot)
			got := GuessCgroupDriverFromCgroupName()
			assert.Equal(t, tt.want, got)
		})
	}
}
