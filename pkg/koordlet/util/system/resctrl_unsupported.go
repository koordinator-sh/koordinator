//go:build !linux
// +build !linux

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

import "fmt"

// MountResctrlSubsystem is not supported for non-linux os
func MountResctrlSubsystem() (bool, error) {
	return false, fmt.Errorf("only support linux")
}

func isResctrlAvailableByCpuInfo(path string) (bool, bool, error) {
	return false, false, nil
}

func GetVendorIDByCPUInfo(path string) (string, error) {
	return "unknown", nil
}

func isResctrlAvailableByKernelCmd(path string) (bool, bool, error) {
	return false, false, nil
}
