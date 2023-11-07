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
package util

import (
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
)

func GetNodeMemUsageWithHotPageCache(coldPageUsageBytes uint64) (uint64, error) {
	memInfo, err := GetMemInfo()
	if err != nil {
		return 0, err
	}
	return memInfo.MemTotal*1024 - memInfo.MemAvailable*1024 + memInfo.ActiveFile*1024 + memInfo.InactiveFile*1024 - coldPageUsageBytes, nil
}

func GetCgroupMemUsageWithHotPageCache(cgroupReader resourceexecutor.CgroupReader, parentDir string, coldPageUsageBytes uint64) (uint64, error) {
	memStat, err := cgroupReader.ReadMemoryStat(parentDir)
	if err != nil {
		return 0, err
	}
	return uint64(memStat.UsageWithPageCache()) - coldPageUsageBytes, nil
}
