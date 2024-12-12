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

func major(dev uint64) int64 {
	return int64((dev>>8)&0xff) | int64((dev>>12)&0xfff00)
}

func minor(dev uint64) int64 {
	return int64(dev&0xff) | int64((dev>>12)&0xffffff00)
}

func GetDeviceNumbers(devicePath string) ([]int64, error) {
	fileInfo, err := os.Stat(devicePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat device file: %v", err)
	}
	deviceNumber := fileInfo.Sys().(*syscall.Stat_t).Rdev
	major := major(deviceNumber)
	minor := minor(deviceNumber)
	return []int64{major, minor}, nil
}
