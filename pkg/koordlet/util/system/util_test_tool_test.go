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
	"path"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_CommonFileFuncs(t *testing.T) {
	helper := NewFileTestUtil(t)

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

	helper.CreateCgroupFile("", CPUCFSQuota)
	exist := FileExists(GetCgroupFilePath("", CPUCFSQuota))
	assert.True(t, exist, "CreateCgroupFile")

	helper.WriteCgroupFileContents("", CPUCFSQuota, "100000")
	gotContents := helper.ReadCgroupFileContents("", CPUCFSQuota)
	assert.Equal(t, "100000", gotContents, "testReadCgroupFileContents")

}

func Test_ProcFileFuncs(t *testing.T) {
	helper := NewFileTestUtil(t)

	procFile := "testfile"
	helper.CreateProcSubFile(procFile)
	exist := FileExists(path.Join(Conf.ProcRootDir, procFile))
	assert.True(t, exist, "CreateProcSubFile")

	helper.WriteProcSubFileContents(procFile, "testContents")
	gotContents := helper.ReadProcSubFileContents(procFile)
	assert.Equal(t, "testContents", gotContents, "testReadProcSubFileContents")

}

func Test_TestingPrepareResctrlMondata(t *testing.T) {
	helper := NewFileTestUtil(t)
	defer helper.Cleanup()

	mmds := map[string]MockMonData{
		"": {
			CacheItems: map[int]MockCacheItem{
				0: {
					"llc_occupancy":   1,
					"mbm_local_bytes": 2,
					"mbm_total_bytes": 3,
				},
				1: {
					"llc_occupancy":   4,
					"mbm_local_bytes": 5,
					"mbm_total_bytes": 6,
				},
			},
		},
		"BE": {
			CacheItems: map[int]MockCacheItem{
				0: {
					"llc_occupancy":   11,
					"mbm_local_bytes": 21,
					"mbm_total_bytes": 31,
				},
				1: {
					"llc_occupancy":   41,
					"mbm_local_bytes": 51,
					"mbm_total_bytes": 61,
				},
			},
		},
	}

	for ctrlGrp, mmd := range mmds {
		TestingPrepareResctrlMondata(t, Conf.SysFSRootDir, ctrlGrp, mmd)
	}

	for ctrlGrp, mmd := range mmds {
		for cacheId, cacheItem := range mmd.CacheItems {
			for item, value := range cacheItem {
				itemPath := filepath.Join(Conf.SysFSRootDir, "resctrl", ctrlGrp,
					"mon_data", fmt.Sprintf("mon_L3_%02d", cacheId), item)
				gotContents := helper.ReadFileContents(itemPath)
				data, err := strconv.Atoi(gotContents)
				assert.NoError(t, err)
				assert.Equal(t, value, uint64(data), "testReadFileContents")
			}
		}
	}
}
