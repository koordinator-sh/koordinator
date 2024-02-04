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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	msgResourceSupportedForTesting = "resource is set supported for testing"
)

var (
	defaultAnolisOSResourcesForTesting = []Resource{
		CPUBurst,
		CPUBVTWarpNs,
		MemoryWmarkRatio,
		MemoryWmarkScaleFactor,
		MemoryWmarkMinAdj,
		MemoryMin,
		MemoryLow,
		MemoryHigh,
		MemoryPriority,
		MemoryUsePriorityOom,
		MemoryOomGroup,
		BlkioIOQoS,
		BlkioIOWeight,
		BlkioReadBps,
		BlkioReadIops,
		BlkioWriteBps,
		BlkioWriteIops,
	}
)

type FileTestUtil struct {
	// Temporary directory to store mock cgroup filesystem.
	TempDir string
	// whether to validate when writing cgroups resources
	ValidateResource bool
	// additional cleanup function for Config to be invoked in Cleanup()
	CleanupFn func(config *Config)

	t testing.TB
}

// NewFileTestUtil creates a new test util for the specified subsystem.
// NOTE: this function should be called only for testing purposes.
func NewFileTestUtil(t testing.TB) *FileTestUtil {
	// NOTE: When $TMPDIR is not set, `t.TempDir()` can use different base directory on Mac OS X and Linux, which may
	// generates too long paths to test unix socket.
	t.Setenv("TMPDIR", "/tmp")
	tempDir := t.TempDir()
	HostSystemInfo.IsAnolisOS = true

	Conf.ProcRootDir = filepath.Join(tempDir, "proc")
	err := os.MkdirAll(Conf.ProcRootDir, 0777)
	assert.NoError(t, err)
	Conf.CgroupRootDir = tempDir
	Conf.SysRootDir = tempDir
	Conf.SysFSRootDir = filepath.Join(tempDir, "fs")
	Conf.VarRunRootDir = tempDir

	InitSupportConfigs()

	return &FileTestUtil{
		TempDir:          tempDir,
		ValidateResource: true,
		t:                t,
	}
}

func (c *FileTestUtil) Cleanup() {
	if c.TempDir != "" {
		err := os.RemoveAll(c.TempDir)
		assert.NoError(c.t, err)
	}
	initCgroupsVersion()
	if c.CleanupFn != nil {
		c.CleanupFn(Conf)
	}
}

func (c *FileTestUtil) SetResourcesSupported(supported bool, resources ...Resource) {
	for _, r := range resources {
		r.WithSupported(supported, msgResourceSupportedForTesting)
	}
}

func (c *FileTestUtil) SetAnolisOSResourcesSupported(supported bool) {
	c.SetResourcesSupported(supported, defaultAnolisOSResourcesForTesting...)
}

func (c *FileTestUtil) SetCgroupsV2(useCgroupsV2 bool) {
	UseCgroupsV2.Store(useCgroupsV2)
}

func (c *FileTestUtil) SetValidateResource(enabled bool) {
	c.ValidateResource = enabled
}

func (c *FileTestUtil) SetConf(setFn, cleanupFn func(conf *Config)) {
	setFn(Conf)
	c.CleanupFn = cleanupFn
}

// if dir contain TempDir, mkdir direct, else join with TempDir and mkdir
func (c *FileTestUtil) MkDirAll(testDir string) {
	dir := testDir
	if !strings.Contains(dir, c.TempDir) {
		dir = filepath.Join(c.TempDir, testDir)
	}
	if err := os.MkdirAll(dir, 0777); err != nil {
		c.t.Fatal(err)
	}
}

// if filePath contain TempDir, createFile direct, else join with TempDir and create
func (c *FileTestUtil) CreateFile(testFilePath string) {
	filePath := testFilePath
	if !strings.Contains(filePath, c.TempDir) {
		filePath = filepath.Join(c.TempDir, testFilePath)
	}
	dir, _ := filepath.Split(filePath)
	if err := os.MkdirAll(dir, 0777); err != nil {
		c.t.Fatal(err)
	}
	if _, err := os.Create(filePath); err != nil {
		c.t.Fatal(err)
	}
}

// if filePath contain TempDir, write direct, else join with TempDir and write
func (c *FileTestUtil) WriteFileContents(testFilePath, contents string) {
	filePath := testFilePath
	if !strings.Contains(filePath, c.TempDir) {
		filePath = filepath.Join(c.TempDir, testFilePath)
	}
	if !FileExists(filePath) {
		c.CreateFile(testFilePath)
	}
	err := os.WriteFile(filePath, []byte(contents), 0644)
	if err != nil {
		c.t.Fatal(err)
	}
}

// if filePath contain TempDir, read direct, else join with TempDir and read
func (c *FileTestUtil) ReadFileContents(testFilePath string) string {
	filePath := testFilePath
	if !strings.Contains(filePath, c.TempDir) {
		filePath = filepath.Join(c.TempDir, testFilePath)
	}
	contents, err := os.ReadFile(filePath)
	if err != nil {
		c.t.Fatal(err)
	}
	return string(contents)
}

func (c *FileTestUtil) CreateProcSubFile(fileRelativePath string) {
	file := filepath.Join(Conf.ProcRootDir, fileRelativePath)
	dir, _ := filepath.Split(file)
	if err := os.MkdirAll(dir, 0777); err != nil {
		c.t.Fatal(err)
	}
	if _, err := os.Create(file); err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) WriteProcSubFileContents(relativeFilePath string, contents string) {
	file := filepath.Join(Conf.ProcRootDir, relativeFilePath)
	if !FileExists(file) {
		c.CreateProcSubFile(relativeFilePath)
	}
	err := os.WriteFile(file, []byte(contents), 0644)
	if err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) ReadProcSubFileContents(relativeFilePath string) string {
	file := filepath.Join(Conf.ProcRootDir, relativeFilePath)
	contents, err := os.ReadFile(file)
	if err != nil {
		c.t.Fatal(err)
	}
	return string(contents)
}

func (c *FileTestUtil) CreateCgroupFile(taskDir string, r Resource) {

	c.SetCgroupsV2(IsCgroupV2Resource(r))

	filePath := GetCgroupFilePath(taskDir, r)
	dir, _ := filepath.Split(filePath)
	if err := os.MkdirAll(dir, 0777); err != nil {
		c.t.Fatal(err)
	}
	if _, err := os.Create(filePath); err != nil {
		c.t.Fatal(err)
	}
}

// WriteCgroupFileContents is only intended for test functions. For specific read/write functionalities, please refer
// to the executor package.
func (c *FileTestUtil) WriteCgroupFileContents(taskDir string, r Resource, contents string) {
	c.SetCgroupsV2(IsCgroupV2Resource(r))

	filePath := GetCgroupFilePath(taskDir, r)
	if !FileExists(filePath) {
		c.CreateCgroupFile(taskDir, r)
	}

	if c.ValidateResource {
		if supported, msg := r.IsSupported(taskDir); !supported {
			err := ResourceUnsupportedErr(fmt.Sprintf("write cgroup %s failed, msg: %s", r.ResourceType(), msg))
			c.t.Fatal(err)
		}
		if valid, msg := r.IsValid(contents); !valid {
			err := fmt.Errorf("write cgroup %s failed, value[%v] not valid, msg: %s", r.ResourceType(), contents, msg)
			c.t.Fatal(err)
		}
	}
	filePath = r.Path(taskDir)
	c.t.Logf("write %s [%s]", filePath, contents)

	err := os.WriteFile(filePath, []byte(contents), 0644)
	if err != nil {
		c.t.Fatal(err)
	}
}

func (c *FileTestUtil) ReadCgroupFileContentsInt(taskDir string, r Resource) *int64 {
	c.SetCgroupsV2(IsCgroupV2Resource(r))

	if supported, msg := r.IsSupported(taskDir); !supported {
		err := ResourceUnsupportedErr(fmt.Sprintf("write cgroup %s failed, msg: %s", r.ResourceType(), msg))
		c.t.Fatal(err)
	}

	filePath := r.Path(taskDir)
	contents, err := os.ReadFile(filePath)
	if err != nil {
		c.t.Fatal(err)
	}

	data, err := strconv.ParseInt(strings.TrimSpace(string(contents)), 10, 64)
	if err != nil {
		c.t.Fatal(err)
	}

	return &data
}

func (c *FileTestUtil) ReadCgroupFileContents(taskDir string, r Resource) string {
	c.SetCgroupsV2(IsCgroupV2Resource(r))

	if supported, msg := r.IsSupported(taskDir); !supported {
		err := ResourceUnsupportedErr(fmt.Sprintf("write cgroup %s failed, msg: %s", r.ResourceType(), msg))
		c.t.Fatal(err)
	}

	filePath := r.Path(taskDir)
	data, err := os.ReadFile(filePath)
	if err != nil {
		c.t.Fatal(err)
	}
	contents := strings.Trim(string(data), "\n")
	return contents
}

func (c *FileTestUtil) stripPrefix(path string) string {
	stripped := strings.TrimPrefix(path, c.TempDir)
	if stripped == "" {
		return "/"
	}
	return stripped
}
