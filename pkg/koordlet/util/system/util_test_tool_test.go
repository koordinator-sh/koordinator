package system

import (
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_CommonFileFuncs(t *testing.T) {
	helper := NewFileTestUtil(t)
	defer helper.Cleanup()

	testDir := "test"
	helper.MkDirAll(testDir)
	exist, err := PathExists(path.Join(helper.TempDir, testDir))
	assert.True(t, exist, "testMkDirAll", err)

	testFile := path.Join(testDir, "testFile")
	helper.CreateFile(testFile)
	exist = FileExists(path.Join(helper.TempDir, testFile))
	assert.True(t, exist, "CreateFile")

	helper.WriteFileContents(testFile, "testContents")
	gotContents := helper.ReadFileContents(testFile)
	assert.Equal(t, "testContents", gotContents, "testReadFileContents")

}

func Test_CgroupFileFuncs(t *testing.T) {
	helper := NewFileTestUtil(t)
	defer helper.Cleanup()

	helper.CreateCgroupFile("", CPUCFSQuota)
	exist := FileExists(GetCgroupFilePath("", CPUCFSQuota))
	assert.True(t, exist, "CreateCgroupFile")

	helper.WriteCgroupFileContents("", CPUCFSQuota, "100000")
	gotContents := helper.ReadCgroupFileContents("", CPUCFSQuota)
	assert.Equal(t, "100000", gotContents, "testReadCgroupFileContents")

}

func Test_ProcFileFuncs(t *testing.T) {
	helper := NewFileTestUtil(t)
	defer helper.Cleanup()

	procFile := "testfile"
	helper.CreateProcSubFile(procFile)
	exist := FileExists(path.Join(Conf.ProcRootDir, procFile))
	assert.True(t, exist, "CreateProcSubFile")

	helper.WriteProcSubFileContents(procFile, "testContents")
	gotContents := helper.ReadProcSubFileContents(procFile)
	assert.Equal(t, "testContents", gotContents, "testReadProcSubFileContents")

}
