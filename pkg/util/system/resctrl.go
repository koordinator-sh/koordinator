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
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

const (
	ResctrlDir string = "resctrl/"
	RdtInfoDir string = "info"
	L3CatDir   string = "L3"

	SchemataFileName      string = "schemata"
	CbmMaskFileName       string = "cbm_mask"
	ResctrlTaskFileName   string = "tasks"
	CPUInfoFileName       string = "cpuinfo"
	KernelCmdlineFileName string = "cmdline"

	ResctrlName string = "resctrl"
)

var (
	initLock         sync.Mutex
	isInit           bool
	isSupportResctrl bool
)

func isCPUSupportResctrl() (bool, error) {
	isCatFlagSet, isMbaFlagSet, err := isResctrlAvailableByCpuInfo(filepath.Join(Conf.ProcRootDir, CPUInfoFileName))
	if err != nil {
		klog.Errorf("isResctrlAvailableByCpuInfo error: %v", err)
		return false, err
	}
	klog.Infof("isResctrlAvailableByCpuInfo result,isCatFlagSet: %v,isMbaFlagSet: %v", isCatFlagSet, isMbaFlagSet)
	isInit = true
	return isCatFlagSet && isMbaFlagSet, nil
}

func isKernelSupportResctrl() (bool, error) {
	isCatFlagSet, isMbaFlagSet, err := isResctrlAvailableByKernelCmd(filepath.Join(Conf.ProcRootDir, KernelCmdlineFileName))
	if err != nil {
		klog.Errorf("isResctrlAvailableByKernelCmd error: %v", err)
		return false, err
	}
	klog.Infof("isResctrlAvailableByKernelCmd result,isCatFlagSet: %v,isMbaFlagSet: %v", isCatFlagSet, isMbaFlagSet)
	isInit = true
	return isCatFlagSet && isMbaFlagSet, nil
}

func IsSupportResctrl() (bool, error) {
	initLock.Lock()
	defer initLock.Unlock()
	if !isInit {
		cpuSupport, err := isCPUSupportResctrl()
		if err != nil {
			return false, err
		}
		kernelSupport, err := isKernelSupportResctrl()
		if err != nil {
			return false, err
		}
		isInit = true
		isSupportResctrl = kernelSupport && cpuSupport
	}
	return isSupportResctrl, nil
}

// @return /sys/fs/resctrl
func GetResctrlSubsystemDirPath() string {
	return filepath.Join(Conf.SysFSRootDir, ResctrlDir)
}

// @groupPath BE
// @return /sys/fs/resctrl/BE
func GetResctrlGroupRootDirPath(groupPath string) string {
	return filepath.Join(Conf.SysFSRootDir, ResctrlDir, groupPath)
}

// @return /sys/fs/resctrl/info/L3/cbm_mask
func GetResctrlL3CbmFilePath() string {
	return filepath.Join(Conf.SysFSRootDir, ResctrlDir, RdtInfoDir, L3CatDir, CbmMaskFileName)
}

// @groupPath BE
// @return /sys/fs/resctrl/BE/schemata
func GetResctrlSchemataFilePath(groupPath string) string {
	return filepath.Join(Conf.SysFSRootDir, ResctrlDir, groupPath, SchemataFileName)
}

// @groupPath BE
// @return /sys/fs/resctrl/BE/tasks
func GetResctrlTasksFilePath(groupPath string) string {
	return filepath.Join(Conf.SysFSRootDir, ResctrlDir, groupPath, ResctrlTaskFileName)
}

// ReadCatL3Cbm reads and returns the value of cat l3 cbm_mask
func ReadCatL3CbmString() (string, error) {
	cbmFile := GetResctrlL3CbmFilePath()
	out, err := ioutil.ReadFile(cbmFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ReadResctrlTasksMap reads and returns the map of given resctrl group's task ids
func ReadResctrlTasksMap(groupPath string) (map[int]struct{}, error) {
	tasksPath := GetResctrlTasksFilePath(groupPath)
	rawContent, err := ioutil.ReadFile(tasksPath)
	if err != nil {
		return nil, err
	}

	tasksMap := map[int]struct{}{}

	lines := strings.Split(string(rawContent), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) <= 0 {
			continue
		}
		task, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		tasksMap[task] = struct{}{}
	}
	return tasksMap, nil
}

// CheckAndTryEnableResctrlCat checks if resctrl and l3_cat are enabled; if not, try to enable the features by mount
// resctrl subsystem; See MountResctrlSubsystem() for the detail.
// It returns whether the resctrl cat is enabled, and the error if failed to enable or to check resctrl interfaces
func CheckAndTryEnableResctrlCat() error {
	// resctrl cat is correctly enabled: l3_cbm path exists
	l3CbmFilePath := GetResctrlL3CbmFilePath()
	_, err := os.Stat(l3CbmFilePath)
	if err == nil {
		return nil
	}
	newMount, err := MountResctrlSubsystem()
	if err != nil {
		return err
	}
	if newMount {
		klog.Infof("mount resctrl successfully, resctrl enabled")
	}
	// double check l3_cbm path to ensure both resctrl and cat are correctly enabled
	l3CbmFilePath = GetResctrlL3CbmFilePath()
	_, err = os.Stat(l3CbmFilePath)
	if err != nil {
		return fmt.Errorf("resctrl cat is not enabled, err: %s", err)
	}
	return nil
}
