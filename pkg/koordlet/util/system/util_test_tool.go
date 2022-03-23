package system

import (
	"io/ioutil"
	"os"
	"path"
	"runtime"
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

type FileTestUtil struct {
	// Temporary directory to store mock cgroup filesystem.
	TempDir string

	t *testing.T
}

// Creates a new test util for the specified subsystem
func NewFileTestUtil(t *testing.T) *FileTestUtil {
	tempDir, err := ioutil.TempDir("/tmp", "koordlet_test")
	HostSystemInfo.IsAliOS = true

	if err != nil {
		t.Fatal(err)
	}

	Conf.ProcRootDir = path.Join(tempDir, "proc")
	os.MkdirAll(Conf.ProcRootDir, 0777)
	Conf.CgroupRootDir = tempDir
	CommonRootDir = tempDir

	return &FileTestUtil{TempDir: tempDir, t: t}
}

func (c *FileTestUtil) Cleanup() {
	os.RemoveAll(c.TempDir)
}

func (c *FileTestUtil) CreateFiles(files []string) {
	for _, file := range files {
		filePath := path.Join(c.TempDir, file)
		if _, err := os.Create(filePath); err != nil {
			c.t.Fatal(err)
		}
	}
}

func (c *FileTestUtil) MkDirAll(dirRelativePath string) {
	dir := path.Join(c.TempDir, dirRelativePath)
	if err := os.MkdirAll(dir, 0777); err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) CreateFile(fileRelativePath string) {
	filePath := path.Join(c.TempDir, fileRelativePath)
	dir, _ := path.Split(filePath)
	if err := os.MkdirAll(dir, 0777); err != nil {
		c.t.Fatal(err)
	}
	if _, err := os.Create(filePath); err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) WriteFileContents(fileRelativePath, contents string) {
	filePath := path.Join(c.TempDir, fileRelativePath)
	err := ioutil.WriteFile(filePath, []byte(contents), 0644)
	if err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) ReadFileContents(fileRelativePath string) string {
	filePath := path.Join(c.TempDir, fileRelativePath)
	contents, err := ioutil.ReadFile(filePath)
	if err != nil {
		c.t.Fatal(err)
	}
	return string(contents)
}

func (c *FileTestUtil) GetAbsoluteFilePath(fileRelativePath string) string {
	return path.Join(c.TempDir, fileRelativePath)
}

func (c *FileTestUtil) MkProcSubDirAll(dirRelativePath string) {
	dir := path.Join(Conf.ProcRootDir, dirRelativePath)
	if err := os.MkdirAll(dir, 0777); err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) CreateProcSubFile(fileRelativePath string) {
	file := path.Join(Conf.ProcRootDir, fileRelativePath)
	dir, _ := path.Split(file)
	if err := os.MkdirAll(dir, 0777); err != nil {
		c.t.Fatal(err)
	}
	if _, err := os.Create(file); err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) WriteProcSubFileContents(relativeFilePath string, contents string) {
	file := path.Join(Conf.ProcRootDir, relativeFilePath)
	if !FileExists(file) {
		c.CreateProcSubFile(file)
	}
	err := ioutil.WriteFile(file, []byte(contents), 0644)
	if err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) ReadProcSubFileContents(relativeFilePath string) string {
	file := path.Join(Conf.ProcRootDir, relativeFilePath)
	contents, err := ioutil.ReadFile(file)
	if err != nil {
		c.t.Fatal(err)
	}
	return string(contents)
}

func (c *FileTestUtil) CreateCgroupFile(taskDir string, file CgroupFile) {
	filePath := GetCgroupFilePath(taskDir, file)
	dir, _ := path.Split(filePath)
	if err := os.MkdirAll(dir, 0777); err != nil {
		c.t.Fatal(err)
	}
	if _, err := os.Create(filePath); err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) WriteCgroupFileContents(taskDir string, file CgroupFile, contents string) {
	filePath := GetCgroupFilePath(taskDir, file)
	if !FileExists(filePath) {
		c.CreateCgroupFile(taskDir, file)
	}
	err := CgroupFileWrite(taskDir, file, contents)
	if err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) ReadCgroupFileContents(taskDir string, file CgroupFile) string {
	contents, err := CgroupFileRead(taskDir, file)
	if err != nil {
		c.t.Fatal(err)
	}
	return contents
}

type NetworkTestUtil struct {
	t  *testing.T
	ns netns.NsHandle
}

func NewNetworkTestUtil(t *testing.T) *NetworkTestUtil {
	if os.Getuid() != 0 {
		t.Skip("Test requires root privileges.")
	}

	// new temporary namespace so we don't pollute the host
	// lock thread since the namespace is thread local
	runtime.LockOSThread()
	var err error
	ns, err := netns.New()
	if err != nil {
		t.Fatal("Failed to create newns", ns)
	}

	return &NetworkTestUtil{t: t, ns: ns}
}

func (n *NetworkTestUtil) Cleanup() {
	n.ns.Close()
	runtime.UnlockOSThread()
}

func (n *NetworkTestUtil) SetupDummyLinkWithAddr() *netlink.Dummy {
	dummyLink := netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{
			Index: 24,
			Name:  "foo",
		},
	}
	dummyAddr, _ := netlink.ParseAddr("192.168.10.1/24")

	if err := netlink.LinkAdd(&dummyLink); err != nil {
		n.t.Fatal("Failed to create link", err)
	}
	if err := netlink.LinkSetUp(&dummyLink); err != nil {
		n.t.Fatal("Failed to up link", err)
	}
	if err := netlink.AddrAdd(&dummyLink, dummyAddr); err != nil {
		n.t.Fatal("Failed to add address to link", err)
	}
	return &dummyLink
}
