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

package system

import (
	"flag"
	"os"

	"go.uber.org/atomic"
	"k8s.io/klog/v2"
)

const (
	DS_MODE   = "dsMode"
	HOST_MODE = "hostMode"
)

var Conf = NewDsModeConfig()
var AgentMode = DS_MODE

var UseCgroupsV2 = atomic.NewBool(false)

type Config struct {
	CgroupRootDir         string
	SysRootDir            string
	SysFSRootDir          string
	ProcRootDir           string
	VarRunRootDir         string
	VarLibKubeletRootDir  string
	RunRootDir            string
	RuntimeHooksConfigDir string

	ContainerdEndPoint           string
	PouchEndpoint                string
	DockerEndPoint               string
	CrioEndPoint                 string
	DefaultRuntimeType           string
	HAMICoreLibraryDirectoryPath string
}

func init() {
	agentMode := os.Getenv("agent_mode")
	if agentMode == HOST_MODE {
		Conf = NewHostModeConfig()
		AgentMode = agentMode
	}
}

// InitSupportConfigs initializes the system support status.
// e.g. the cgroup version, resctrl capability
func InitSupportConfigs() {
	// $ getconf CLK_TCK > jiffies
	if err := initJiffies(); err != nil {
		klog.Warningf("failed to get Jiffies, use the default %v, err: %v", Jiffies, err)
	}
	initCgroupsVersion()
	HostSystemInfo = collectVersionInfo()
	if isResctrlSupported, err := IsSupportResctrl(); err != nil {
		klog.Warningf("failed to check resctrl support status, use %v, err: %v", isResctrlSupported, err)
	} else {
		klog.V(4).Infof("resctrl supported: %v", isResctrlSupported)
	}
}

func NewHostModeConfig() *Config {
	return &Config{
		CgroupRootDir:                "/sys/fs/cgroup/",
		ProcRootDir:                  "/proc/",
		SysRootDir:                   "/sys/",
		SysFSRootDir:                 "/sys/fs/",
		VarRunRootDir:                "/var/run/",
		VarLibKubeletRootDir:         "/var/lib/kubelet/",
		RunRootDir:                   "/run/",
		RuntimeHooksConfigDir:        "/etc/runtime/hookserver.d",
		DefaultRuntimeType:           "containerd",
		HAMICoreLibraryDirectoryPath: "/usr/local/vgpu/libvgpu.so",
	}
}

func NewDsModeConfig() *Config {
	return &Config{
		CgroupRootDir: "/host-cgroup/",
		// some dirs are not covered by ns, or unused with `hostPID` is on
		ProcRootDir:                  "/proc/",
		SysRootDir:                   "/host-sys/",
		SysFSRootDir:                 "/host-sys-fs/",
		VarRunRootDir:                "/host-var-run/",
		VarLibKubeletRootDir:         "/var/lib/kubelet/",
		RunRootDir:                   "/host-run/",
		RuntimeHooksConfigDir:        "/host-etc-hookserver/",
		DefaultRuntimeType:           "containerd",
		HAMICoreLibraryDirectoryPath: "/usr/local/vgpu/libvgpu.so",
	}
}

func SetConf(config Config) {
	Conf = &config
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.CgroupRootDir, "cgroup-root-dir", c.CgroupRootDir, "Cgroup root dir")
	fs.StringVar(&c.SysRootDir, "sys-root-dir", c.SysRootDir, "host /sys dir in container")
	fs.StringVar(&c.SysFSRootDir, "sys-fs-root-dir", c.SysFSRootDir, "host /sys/fs dir in container, used by resctrl fs")
	fs.StringVar(&c.ProcRootDir, "proc-root-dir", c.ProcRootDir, "host /proc dir in container")
	fs.StringVar(&c.VarRunRootDir, "var-run-root-dir", c.VarRunRootDir, "host /var/run dir in container")
	fs.StringVar(&c.VarLibKubeletRootDir, "var-lib-kubelet-dir", c.VarLibKubeletRootDir, "host /var/lib/kubelet dir in container")
	fs.StringVar(&c.RunRootDir, "run-root-dir", c.RunRootDir, "host /run dir in container")

	fs.StringVar(&c.ContainerdEndPoint, "containerd-endpoint", c.ContainerdEndPoint, "containerd endPoint")
	fs.StringVar(&c.DockerEndPoint, "docker-endpoint", c.DockerEndPoint, "docker endPoint")
	fs.StringVar(&c.PouchEndpoint, "pouch-endpoint", c.PouchEndpoint, "pouch endPoint")

	fs.StringVar(&c.DefaultRuntimeType, "default-runtime-type", c.DefaultRuntimeType, "default runtime type during runtime hooks handle request, candidates are containerd/docker/pouch.")
	fs.StringVar(&c.HAMICoreLibraryDirectoryPath, "hami-core-library-directory-path", c.HAMICoreLibraryDirectoryPath, "path of hami core library")
}
