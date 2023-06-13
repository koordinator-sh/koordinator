package options

import "time"

const (
	DefaultServerEndpoint          = "/var/run/yarn-copilot/yarn-copilot.sock"
	DefaultYarnContainerCgroupPath = "kubepods/besteffort/hadoop-yarn"
	DefaultSyncCgroupPeriod        = time.Second * 10
	DefaultNodeManagerEndpoint     = "localhost:8042"
	DefaultCgroupRootDir           = "/sys/fs/cgroup/"
)

type Configuration struct {
	ServerEndpoint          string
	YarnContainerCgroupPath string
	SyncMemoryCgroup        bool
	SyncCgroupPeriod        time.Duration
	NodeMangerEndpoint      string
	CgroupRootDir           string
}

func NewConfiguration() *Configuration {
	return &Configuration{
		ServerEndpoint:          DefaultServerEndpoint,
		YarnContainerCgroupPath: DefaultYarnContainerCgroupPath,
		SyncMemoryCgroup:        false,
		SyncCgroupPeriod:        DefaultSyncCgroupPeriod,
		NodeMangerEndpoint:      DefaultNodeManagerEndpoint,
		CgroupRootDir:           DefaultCgroupRootDir,
	}
}
