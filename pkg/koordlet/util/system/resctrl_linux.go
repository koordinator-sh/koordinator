//go:build linux
// +build linux

package system

import (
	"fmt"
	"os"
	"syscall"
)

// MountResctrlSubsystem mounts resctrl fs under the sysFSRoot to enable the kernel feature on supported environment
// NOTE: linux kernel (Alibaba Cloud Linux 2, >= 4.10), Intel cpu and bare-mental host are required; Also, Intel RDT
// features should be enabled in kernel configurations and kernel commandline.
// For more info, please see https://github.com/intel/intel-cmt-cat/wiki/resctrl or
func MountResctrlSubsystem() (bool, error) {
	schemataPath := GetResctrlSchemataFilePath("")
	// use schemata path to check since the subsystem root dir could keep exist when unmounted
	_, err := os.Stat(schemataPath)
	if err == nil {
		return false, nil
	}
	subsystemPath := GetResctrlSubsystemDirPath()
	err = syscall.Mount(ResctrlName, subsystemPath, ResctrlName, syscall.MS_RELATIME, "")
	if err != nil {
		return false, err
	}
	_, err = os.Stat(schemataPath)
	if err != nil {
		return false, fmt.Errorf("resctrl subsystem is mounted, but path %s does not exist, err: %s",
			subsystemPath, err)
	}
	return true, nil
}
