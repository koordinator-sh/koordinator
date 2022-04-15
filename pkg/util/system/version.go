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
	"path"
	"path/filepath"

	"k8s.io/klog/v2"
)

var HostSystemInfo = collectVersionInfo()

func collectVersionInfo() VersionInfo {
	return VersionInfo{
		IsAliOS: isAliOS(),
	}
}

type VersionInfo struct {
	IsAliOS bool
}

func isAliOS() bool {
	return isSupportBvtOrWmarRatio()
}

func isSupportBvtOrWmarRatio() bool {
	bvtFilePath := path.Join(Conf.CgroupRootDir, CgroupCPUDir, CPUBVTWarpNsName)
	exists, err := PathExists(bvtFilePath)
	klog.V(2).Infof("PathExists bvt,exists: %v,error:%v", exists, err)
	if err == nil && exists {
		return true
	}

	wmarkRatioPath := path.Join(Conf.CgroupRootDir, CgroupMemDir, "*", MemWmarkRatioFileName)
	matches, err := filepath.Glob(wmarkRatioPath)
	klog.V(2).Infof("PathExists wmark_ratio,exists: %v,error:%v", matches, err)
	if err == nil && len(matches) > 0 {
		return true
	}

	return false
}
