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
	"math"
	"math/bits"
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
	out, err := os.ReadFile(cbmFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ReadResctrlTasksMap reads and returns the map of given resctrl group's task ids
func ReadResctrlTasksMap(groupPath string) (map[int]struct{}, error) {
	tasksPath := GetResctrlTasksFilePath(groupPath)
	rawContent, err := os.ReadFile(tasksPath)
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

func InitCatGroupIfNotExist(group string) error {
	path := GetResctrlGroupRootDirPath(group)
	_, err := os.Stat(path)
	if err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check dir %v for group %s but got unexpected err: %v", path, group, err)
	}
	err = os.Mkdir(path, 0755)
	if err != nil {
		return fmt.Errorf("create dir %v failed for group %s, err: %v", path, group, err)
	}
	return nil
}

func CalculateCatL3MaskValue(cbm uint, startPercent, endPercent int64) (string, error) {
	// check if the parsed cbm value is valid, eg. 0xff, 0x1, 0x7ff, ...
	// NOTE: (Cache Bit Masks) X86 hardware requires that these masks have all the '1' bits in a contiguous block.
	//       ref: https://www.kernel.org/doc/Documentation/x86/intel_rdt_ui.txt
	// since the input cbm here is the cbm value of the resctrl root, every lower bit is required to be `1` additionally
	if bits.OnesCount(cbm+1) != 1 {
		return "", fmt.Errorf("illegal cbm %v", cbm)
	}

	// check if the startPercent and endPercent are valid
	if startPercent < 0 || endPercent > 100 || endPercent <= startPercent {
		return "", fmt.Errorf("illegal l3 cat percent: start %v, end %v", startPercent, endPercent)
	}

	// calculate a bit mask belonging to interval [startPercent% * ways, endPercent% * ways)
	// eg.
	// cbm 0x3ff ('b1111111111), start 10%, end 80%
	// ways 10, l3Mask 0xfe ('b11111110)
	// cbm 0x7ff ('b11111111111), start 10%, end 50%
	// ways 11, l3Mask 0x3c ('b111100)
	// cbm 0x7ff ('b11111111111), start 0%, end 30%
	// ways 11, l3Mask 0xf ('b1111)
	ways := float64(bits.Len(cbm))
	startWay := uint64(math.Ceil(ways * float64(startPercent) / 100))
	endWay := uint64(math.Ceil(ways * float64(endPercent) / 100))

	var l3Mask uint64 = (1 << endWay) - (1 << startWay)
	return strconv.FormatUint(l3Mask, 16), nil
}
