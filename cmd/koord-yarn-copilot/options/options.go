package options

import "time"

const (
	DefaultServerEndpoint          = "/var/run/yarn-copilot/yarn-copilot.sock"
	DefaultYarnContainerCgroupPath = "kubepods/besteffort/hadoop-yarn"
	DefaultSyncCgroupPeriod        = time.Second * 10
	DefaultNodeManagerEndpoint     = "localhost:8042"
)

var (
	ServerEndpoint          string
	YarnContainerCgroupPath string
	SyncMemoryCgroup        bool
	SyncCgroupPeriod        time.Duration
	NodeMangerEndpoint      string
)
