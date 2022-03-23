package system

import (
	"k8s.io/klog/v2"
	"path"
	"path/filepath"
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
