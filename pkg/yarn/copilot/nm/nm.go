package nm

import (
	"fmt"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/yarn/copilot/utils"
	"k8s.io/klog/v2"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/pleg"
	"github.com/opencontainers/runc/libcontainer/cgroups"
)

type NodeMangerOperator struct {
	CgroupRoot string
	CgroupPath string

	SyncMemoryCgroup bool

	containerWatch pleg.Watcher
	NMEndpoint     string //localhost:8042
	client         *resty.Client
	ticker         *time.Ticker
}

func NewNodeMangerOperator(cgroupRoot string, cgroupPath string, syncMemoryCgroup bool, endpoint string, syncPeriod time.Duration) (*NodeMangerOperator, error) {
	watcher, err := pleg.NewWatcher()
	if err != nil {
		return nil, err
	}
	cli := resty.New()
	cli.HostURL = endpoint
	return &NodeMangerOperator{
		CgroupRoot:       cgroupRoot,
		CgroupPath:       cgroupPath,
		SyncMemoryCgroup: syncMemoryCgroup,
		containerWatch:   watcher,
		NMEndpoint:       endpoint,
		client:           cli,
		ticker:           time.NewTicker(syncPeriod),
	}, nil
}

func (n *NodeMangerOperator) Run(stop <-chan struct{}) error {
	if n.SyncMemoryCgroup {
		return n.syncMemoryCgroup(stop)
	}
	return nil
}

func (n *NodeMangerOperator) syncMemoryCgroup(stop <-chan struct{}) error {
	if err := n.containerWatch.AddWatch(filepath.Join(n.CgroupRoot, system.CgroupCPUDir, n.CgroupPath)); err != nil {
		return err
	}
	if err := n.ensureMemoryCgroupDir(); err != nil {
		return err
	}
	for {
		select {
		case event := <-n.containerWatch.Event():
			switch pleg.TypeOf(event) {
			case pleg.DirCreated:
				n.createMemoryCgroup(event.Name)
			case pleg.DirRemoved:
				n.removeMemoryCgroup(event.Name)
			default:
				klog.V(5).Infof("skip %v unknown event", event.Name)
			}
		case <-n.ticker.C:
			n.syncAllCgroup()
		case <-stop:
			return nil
		}
	}
}

func (n *NodeMangerOperator) syncAllCgroup() {
	subDirFunc := func(dir string) map[string]struct{} {
		res := map[string]struct{}{}
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				klog.Warningf("ignore file %s error:%s", path, err.Error())
				return err
			}
			if info.IsDir() && path != dir {
				res[path] = struct{}{}
				return filepath.SkipDir
			}
			return nil
		})
		return res
	}
	cpuList := subDirFunc(filepath.Join(n.CgroupRoot, system.CgroupCPUDir, n.CgroupPath))
	memList := subDirFunc(filepath.Join(n.CgroupRoot, system.CgroupMemDir, n.CgroupPath))
	toCreate, toDelete := utils.DiffMap(cpuList, memList)
	for path, _ := range toCreate {
		n.createMemoryCgroup(path)
	}
	for path, _ := range toDelete {
		n.removeMemoryCgroup(path)
	}
}

func (n *NodeMangerOperator) removeMemoryCgroup(fileName string) {
	basename := filepath.Base(fileName)
	if !strings.HasPrefix(basename, "container_") {
		klog.V(5).Infof("skip file %s, which is not a yarn container file", basename)
		return
	}
	memCgroupPath := filepath.Join(n.CgroupRoot, system.CgroupMemDir, n.CgroupPath, basename)
	if err := os.RemoveAll(memCgroupPath); err != nil {
		klog.Error("fail to remove memory dir: %s, error: %s", memCgroupPath, err.Error())
		return
	}
	klog.V(5).Infof("yarn container dir %v removed", basename)
}

func (n *NodeMangerOperator) createMemoryCgroup(fileName string) {
	basename := filepath.Base(fileName)
	if !strings.HasPrefix(basename, "container_") {
		klog.V(5).Infof("skip file %s, which is not a yarn container file", basename)
		return
	}
	memCgroupPath := filepath.Join(n.CgroupRoot, system.CgroupMemDir, n.CgroupPath, basename)
	if err := os.Mkdir(memCgroupPath, 0644); err != nil {
		klog.Error("fail to create memory dir: %s, error: %s", memCgroupPath, err.Error())
		return
	}
	cpuCgroupPath := filepath.Join(n.CgroupRoot, system.CgroupCPUDir, n.CgroupPath, basename)
	read, err := system.CommonFileRead(filepath.Join(cpuCgroupPath, system.CPUProcsName))
	if err != nil {
		klog.Error(err)
		return
	}
	_, err = system.CommonFileWriteIfDifferent(filepath.Join(memCgroupPath, system.CPUProcsName), read)
	if err != nil {
		klog.Error(err)
		return
	}
	klog.V(5).Infof("yarn container dir %v created, sync pid", memCgroupPath)
}

func (n *NodeMangerOperator) ensureMemoryCgroupDir() error {
	memoryDir := filepath.Join(n.CgroupRoot, system.CgroupMemDir, n.CgroupPath)
	_, err := os.Open(memoryDir)
	if err != nil && os.IsNotExist(err) {
		return err
	}
	if err == nil {
		return nil
	}
	return os.MkdirAll(memoryDir, 0644)
}

// KillContainer kill process group for target container
func (n *NodeMangerOperator) KillContainer(containerID string) error {
	processGroupID := n.getProcessGroupID(containerID)
	if processGroupID <= 1 {
		return fmt.Errorf("invalid process group pid(%d) for container %s", processGroupID, containerID)
	}
	return syscall.Kill(-processGroupID, syscall.SIGKILL)
}

func (n *NodeMangerOperator) getProcessGroupID(containerID string) int {
	containerCgroupPath := filepath.Join(n.CgroupRoot, "cpu", n.CgroupPath, containerID)
	pids, err := cgroups.GetPids(containerCgroupPath)
	if err != nil {
		klog.Error(err)
		return 0
	}
	if len(pids) == 0 {
		return 0
	}
	return pids[0]
}

type Containers struct {
	Containers struct {
		Items []YarnContainer `json:"container"`
	} `json:"containers"`
}

type YarnContainer struct {
	Id                  string   `json:"id"`
	Appid               string   `json:"appid"`
	State               string   `json:"state"`
	ExitCode            int      `json:"exitCode"`
	Diagnostics         string   `json:"diagnostics"`
	User                string   `json:"user"`
	TotalMemoryNeededMB int      `json:"totalMemoryNeededMB"`
	TotalVCoresNeeded   int      `json:"totalVCoresNeeded"`
	ContainerLogsLink   string   `json:"containerLogsLink"`
	NodeId              string   `json:"nodeId"`
	MemUsed             float64  `json:"memUsed"`
	MemMaxed            float64  `json:"memMaxed"`
	CpuUsed             float64  `json:"cpuUsed"`
	CpuMaxed            float64  `json:"cpuMaxed"`
	ContainerLogFiles   []string `json:"containerLogFiles"`
}

func (n *NodeMangerOperator) ListContainers() (*Containers, error) {
	var res Containers
	resp, err := n.client.R().SetResult(&res).Get("/ws/v1/node/containers")
	if err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("code for ListContainer is %d", resp.StatusCode())
	}
	return &res, nil
}

func (n *NodeMangerOperator) GetContainer(containerID string) (*YarnContainer, error) {
	listContainers, err := n.ListContainers()
	if err != nil {
		return nil, err
	}
	for _, c := range listContainers.Containers.Items {
		if c.Id == containerID {
			return &c, nil
		}
	}
	return nil, fmt.Errorf("container Not Found")
}

func (n *NodeMangerOperator) GenerateCgroupPath(containerID string) string {
	return filepath.Join(n.CgroupPath, containerID)
}
