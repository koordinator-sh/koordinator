package system

import (
	"flag"
	"os"
)

const (
	DS_MODE   = "dsMode"
	HOST_MODE = "hostMode"
)

var Conf = NewDsModeConfig()
var AgentMode = DS_MODE

type Config struct {
	CgroupRootDir    string
	CgroupKubePath   string
	SysRootDir       string
	SysFSRootDir     string
	ProcRootDir      string
	VarRunRootDir    string
	NodeNameOverride string

	ContainerdEndPoint string
	DockerEndPoint     string
}

func NewHostModeConfig() *Config {
	return &Config{
		CgroupKubePath: "kubepods/",
		CgroupRootDir:  "/sys/fs/cgroup/",
		ProcRootDir:    "/proc/",
		SysRootDir:     "/sys/",
		SysFSRootDir:   "/sys/fs/",
		VarRunRootDir:  "/var/run/",
	}
}

func NewDsModeConfig() *Config {
	return &Config{
		CgroupKubePath: "kubepods/",
		CgroupRootDir:  "/host-cgroup/",
		// some dirs are not covered by ns, or unused with `hostPID` is on
		ProcRootDir:   "/proc/",
		SysRootDir:    "/host-sys/",
		SysFSRootDir:  "/host-sys-fs/",
		VarRunRootDir: "/host-var-run/",
	}
}

func init() {
	Conf = NewDsModeConfig()
	agentMode := os.Getenv("agent_mode")
	if agentMode == HOST_MODE {
		Conf = NewHostModeConfig()
		AgentMode = agentMode
	}
	initFilePath()
}

func SetConf(config Config) {
	Conf = &config
	HostSystemInfo = collectVersionInfo()
	initFilePath()
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.CgroupRootDir, "CgroupRootDir", c.CgroupRootDir, "Cgroup root dir")
	fs.StringVar(&c.SysFSRootDir, "SysRootDir", c.SysFSRootDir, "host /sys dir in container")
	fs.StringVar(&c.SysFSRootDir, "SysFSRootDir", c.SysFSRootDir, "host /sys/fs dir in container, used by resctrl fs")
	fs.StringVar(&c.ProcRootDir, "ProcRootDir", c.ProcRootDir, "host /proc dir in container")
	fs.StringVar(&c.VarRunRootDir, "VarRunRootDir", c.VarRunRootDir, "host /var/run dir in container")

	fs.StringVar(&c.CgroupKubePath, "CgroupKubeDir", c.CgroupKubePath, "Cgroup kube dir")
	fs.StringVar(&c.NodeNameOverride, "node-name-override", c.NodeNameOverride, "If non-empty, will use this string as identification instead of the actual machine name. ")
	fs.StringVar(&c.ContainerdEndPoint, "containerdEndPoint", c.ContainerdEndPoint, "containerd endPoint")
	fs.StringVar(&c.DockerEndPoint, "dockerEndPoint", c.DockerEndPoint, "docker endPoint")

	HostSystemInfo = collectVersionInfo()
	initFilePath()
}
