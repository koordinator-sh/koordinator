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
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

var (
	isSupportColdMemory = false
)

const (
	kidledScanPeriodInSecondsFileSubPath = "/kernel/mm/kidled/scan_period_in_seconds"
	kidledUseHierarchyFileFileSubPath    = "/kernel/mm/kidled/use_hierarchy"
)

func IsKidledSupported() bool {
	isSupportColdMemory = false
	_, err := os.Stat(GetKidledScanPeriodInSecondsFilePath())
	if err != nil {
		klog.V(4).Infof("file scan_period_in_seconds is not exist err: ", err)
		return false
	}
	str, err := os.ReadFile(GetKidledScanPeriodInSecondsFilePath())
	content := strings.Replace(string(str), "\n", "", -1)
	if err != nil {
		klog.V(4).Infof("read scan_period_in_seconds err: ", err)
		return false
	}
	scanPeriodInSeconds, err := strconv.Atoi(content)
	if err != nil {
		klog.V(4).Infof("string to int scan_period_in_seconds err: %s", err)
		return false
	}
	if scanPeriodInSeconds <= 0 {
		klog.V(4).Infof("scan_period_in_seconds is negative err: ", err)
		return false
	}
	_, err = os.Stat(GetKidledUseHierarchyFilePath())
	if err != nil {
		klog.V(4).Infof("file use_hierarchy is not exist err: ", err)
		return false
	}
	str, err = os.ReadFile(GetKidledUseHierarchyFilePath())
	content = strings.Replace(string(str), "\n", "", -1)
	if err != nil {
		klog.V(4).Infof("read use_hierarchy err: ", err)
		return false
	}
	useHierarchy, err := strconv.Atoi(content)
	if err != nil {
		klog.V(4).Infof("string to int useHierarchy err: ", err)
		return false
	}
	if useHierarchy != 1 {
		klog.V(4).Infof("useHierarchy is not equal to 1 err: ", err)
		return false
	}
	isSupportColdMemory = true
	return true
}

func GetIsSupportColdMemory() bool {
	return isSupportColdMemory
}

func GetKidledScanPeriodInSecondsFilePath() string {
	return filepath.Join(Conf.SysRootDir, kidledScanPeriodInSecondsFileSubPath)
}

func GetKidledUseHierarchyFilePath() string {
	return filepath.Join(Conf.SysRootDir, kidledUseHierarchyFileFileSubPath)
}
