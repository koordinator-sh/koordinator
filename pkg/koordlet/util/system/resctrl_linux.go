//go:build linux
// +build linux

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
