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

package resourceexecutor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func testingPrepareResctrlL3CatPath(t *testing.T, cbmStr, rootSchemataStr string) {
	resctrlDir := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)
	l3CatDir := filepath.Join(resctrlDir, system.RdtInfoDir, system.L3CatDir)
	err := os.MkdirAll(l3CatDir, 0700)
	assert.NoError(t, err)

	cbmPath := filepath.Join(l3CatDir, system.ResctrlCbmMaskName)
	err = os.WriteFile(cbmPath, []byte(cbmStr), 0666)
	assert.NoError(t, err)

	schemataPath := filepath.Join(resctrlDir, system.ResctrlSchemataName)
	err = os.WriteFile(schemataPath, []byte(rootSchemataStr), 0666)
	assert.NoError(t, err)
}

func testingPrepareResctrlL3CatGroups(t *testing.T, cbmStr, rootSchemataStr string) {
	testingPrepareResctrlL3CatPath(t, cbmStr, rootSchemataStr)
	resctrlDir := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)

	beSchemataData := []byte("    L3:0=f;1=f\n    MB:0=100;1=100")
	beSchemataDir := filepath.Join(resctrlDir, "BE")
	err := os.MkdirAll(beSchemataDir, 0700)
	assert.NoError(t, err)
	beSchemataPath := filepath.Join(beSchemataDir, system.ResctrlSchemataName)
	err = os.WriteFile(beSchemataPath, beSchemataData, 0666)
	assert.NoError(t, err)
	beTasksPath := filepath.Join(beSchemataDir, system.ResctrlTasksName)
	err = os.WriteFile(beTasksPath, []byte{}, 0666)
	assert.NoError(t, err)

	lsSchemataData := []byte("    L3:0=ff;1=ff\n    MB:0=100;1=100")
	lsSchemataDir := filepath.Join(resctrlDir, "LS")
	err = os.MkdirAll(lsSchemataDir, 0700)
	assert.NoError(t, err)
	lsSchemataPath := filepath.Join(lsSchemataDir, system.ResctrlSchemataName)
	err = os.WriteFile(lsSchemataPath, lsSchemataData, 0666)
	assert.NoError(t, err)
	lsTasksPath := filepath.Join(lsSchemataDir, system.ResctrlTasksName)
	err = os.WriteFile(lsTasksPath, []byte{}, 0666)
	assert.NoError(t, err)

	lsrSchemataData := []byte("    L3:0=ff;1=ff\n    MB:0=100;1=100")
	lsrSchemataDir := filepath.Join(resctrlDir, "LSR")
	err = os.MkdirAll(lsrSchemataDir, 0700)
	assert.NoError(t, err)
	lsrSchemataPath := filepath.Join(lsrSchemataDir, system.ResctrlSchemataName)
	err = os.WriteFile(lsrSchemataPath, lsrSchemataData, 0666)
	assert.NoError(t, err)
	lsrTasksPath := filepath.Join(lsrSchemataDir, system.ResctrlTasksName)
	err = os.WriteFile(lsrTasksPath, []byte{}, 0666)
	assert.NoError(t, err)
}

func TestNewResctrlL3SchemataResource(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)
		defer helper.Cleanup()

		sysFSRootDirName := "NewResctrlL3SchemataResource"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)

		testingPrepareResctrlL3CatGroups(t, "7ff", "    L3:0=ff;1=ff\n    MB:0=100;1=100")
		updater := NewResctrlL3SchemataResource("BE", "3c", 2)
		assert.Equal(t, updater.Value(), "L3:0=3c;1=3c;\n")
		err := updater.update()
		assert.NoError(t, err)
	})
}

func TestNewResctrlMbSchemataResource(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)
		defer helper.Cleanup()

		sysFSRootDirName := "NewResctrlMbSchemataResource"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)

		testingPrepareResctrlL3CatGroups(t, "7ff", "    L3:0=ff;1=ff\n    MB:0=100;1=100")
		updater := NewResctrlMbSchemataResource("BE", "90", 2)
		assert.Equal(t, updater.Value(), "MB:0=90;1=90;\n")
		err := updater.update()
		assert.NoError(t, err)
	})
}

func TestNewResctrlSchemataResource(t *testing.T) {
	t.Run("test_all_schemata", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)
		defer helper.Cleanup()

		sysFSRootDirName := "NewResctrlSchemataResource"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)
		testingPrepareResctrlL3CatGroups(t, "7ff", "    L3:0=ff;1=ff\n    MB:0=100;1=100")
		updater, _ := NewResctrlSchemataResource("BE", "L3:0=f;1=f\nMB:0=60;1=60", nil)
		assert.Equal(t, "L3:0=f;1=f;\nMB:0=60;1=60;\n", updater.Value())
		err := updater.update()
		assert.NoError(t, err)
	})

	t.Run("test_LLC_resource", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)
		defer helper.Cleanup()

		sysFSRootDirName := "NewResctrlSchemataResourceSingleLLC"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)
		testingPrepareResctrlL3CatGroups(t, "7ff", "    L3:0=ff;1=ff")
		updater, _ := NewResctrlSchemataResource("BE", "L3:0=f;1=f", nil)
		assert.Equal(t, "L3:0=f;1=f;\n", updater.Value())
		err := updater.update()
		assert.NoError(t, err)
	})

	t.Run("test_MB_resource", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)
		defer helper.Cleanup()

		sysFSRootDirName := "NewResctrlSchemataResourceSingleMB"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)
		testingPrepareResctrlL3CatGroups(t, "", "    MB:0=10;1=10")
		updater, _ := NewResctrlSchemataResource("BE", "MB:0=20;1=20", nil)
		assert.Equal(t, "MB:0=20;1=20;\n", updater.Value())
		err := updater.update()
		assert.NoError(t, err)
	})
}

func TestNewCatGroupResource(t *testing.T) {
	t.Run("test_create_cat_success", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)
		defer helper.Cleanup()

		sysFSRootDirName := "NewCatGroupResource"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)
		resctrlDir := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)
		l3CatDir := filepath.Join(resctrlDir, system.RdtInfoDir, system.L3CatDir)
		err := os.MkdirAll(l3CatDir, 0700)
		assert.NoError(t, err)
		updater, _ := NewCatGroupResource("ga", nil)
		err = updater.update()
		assert.NoError(t, err)
	})

	t.Run("test_create_cat_exist", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)
		defer helper.Cleanup()

		sysFSRootDirName := "NewCatGroupResource"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)
		resctrlDir := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)
		l3CatDir := filepath.Join(resctrlDir, system.RdtInfoDir, system.L3CatDir)
		err := os.MkdirAll(l3CatDir, 0700)
		assert.NoError(t, err)
		err = os.MkdirAll("ga", 0700)
		assert.NoError(t, err)
		updater, _ := NewCatGroupResource("ga", nil)
		err = updater.update()
		assert.NoError(t, err)
	})

	t.Run("test_create_cat_fail", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)
		defer helper.Cleanup()

		sysFSRootDirName := "NewCatGroupResource"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)
		resctrlDir := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)
		l3CatDir := filepath.Join(resctrlDir, system.RdtInfoDir, system.L3CatDir)
		err := os.MkdirAll(l3CatDir, 0700)
		assert.NoError(t, err)
		err = os.MkdirAll("ga", 0700)
		assert.NoError(t, err)
		updater, _ := NewCatGroupResource("ga/ga/", nil)
		err = updater.update()
		assert.Error(t, err)
	})
}
