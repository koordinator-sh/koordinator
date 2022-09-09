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
	"os"
	"path"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

const (
	CPUShareKubeBEValue    int64 = 2
	CPUShareUnitValue      int64 = 1024
	CFSQuotaUnlimitedValue int64 = -1

	// CgroupMaxSymbolStr only appears in `memory.high`, we consider the value as MaxInt64
	CgroupMaxSymbolStr string = "max"
	// CgroupMaxValueStr math.MaxInt64; writing `memory.high` with this do the same as set as "max"
	CgroupMaxValueStr string = "9223372036854775807"
)

const EmptyValueError string = "EmptyValueError"

type CPUStatRaw struct {
	NrPeriod             int64
	NrThrottled          int64
	ThrottledNanoSeconds int64
}

func CgroupFileWriteIfDifferent(cgroupTaskDir string, file CgroupFile, value string) error {
	currentValue, currentErr := CgroupFileRead(cgroupTaskDir, file)
	if currentErr != nil {
		return currentErr
	}
	if value == currentValue || value == CgroupMaxValueStr && currentValue == CgroupMaxSymbolStr {
		// compatible with cgroup valued "max"
		klog.V(6).Infof("read before write %s %s and got str value, considered as MaxInt64", cgroupTaskDir,
			file.ResourceFileName)
		return nil
	}
	return CgroupFileWrite(cgroupTaskDir, file, value)
}

func CgroupFileReadInt(cgroupTaskDir string, file CgroupFile) (*int64, error) {
	if file.IsAnolisOS && !HostSystemInfo.IsAnolisOS {
		return nil, fmt.Errorf("read cgroup config : %s fail, need anolis kernel", file.ResourceFileName)
	}

	dataStr, err := CgroupFileRead(cgroupTaskDir, file)
	if err != nil {
		return nil, err
	}
	if dataStr == "" {
		return nil, fmt.Errorf(EmptyValueError)
	}
	if dataStr == CgroupMaxSymbolStr {
		// compatible with cgroup valued "max"
		data := int64(math.MaxInt64)
		klog.V(6).Infof("read %s %s and got str value, considered as MaxInt64", cgroupTaskDir,
			file.ResourceFileName)
		return &data, nil
	}
	data, err := strconv.ParseInt(strings.TrimSpace(dataStr), 10, 64)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

func CgroupFileRead(cgroupTaskDir string, file CgroupFile) (string, error) {
	if file.IsAnolisOS && !HostSystemInfo.IsAnolisOS {
		return "", fmt.Errorf("read cgroup config : %s fail, need anolis kernel", file.ResourceFileName)
	}

	klog.V(5).Infof("read %s,%s", cgroupTaskDir, file.ResourceFileName)
	filePath := GetCgroupFilePath(cgroupTaskDir, file)

	data, err := os.ReadFile(filePath)
	return strings.Trim(string(data), "\n"), err
}

func CgroupFileWrite(cgroupTaskDir string, file CgroupFile, data string) error {
	if file.IsAnolisOS && !HostSystemInfo.IsAnolisOS {
		return fmt.Errorf("write cgroup config : %v [%s] fail, need anolis kernel", file.ResourceFileName, data)
	}

	klog.V(5).Infof("write %s,%s [%s]", cgroupTaskDir, file.ResourceFileName, data)
	filePath := GetCgroupFilePath(cgroupTaskDir, file)

	return os.WriteFile(filePath, []byte(data), 0644)
}

// @cgroupTaskDir kubepods.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @return /sys/fs/cgroup/cpu/kubepods.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpu.shares
func GetCgroupFilePath(cgroupTaskDir string, file CgroupFile) string {
	return path.Join(Conf.CgroupRootDir, file.Subfs, cgroupTaskDir, file.ResourceFileName)
}

func GetCgroupCurTasks(cgroupPath string) ([]int, error) {
	var tasks []int
	rawContent, err := os.ReadFile(cgroupPath)
	if err != nil {
		return nil, err
	}
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
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func GetCPUStatRaw(cgroupPath string) (*CPUStatRaw, error) {
	content, err := os.ReadFile(cgroupPath)
	if err != nil {
		return nil, err
	}

	cpuThrottledRaw := &CPUStatRaw{}
	lines := strings.Split(string(content), "\n")
	counter := 0
	for _, line := range lines {
		lineItems := strings.Fields(line)
		if len(lineItems) < 2 {
			continue
		}
		key := lineItems[0]
		val := lineItems[1]
		switch key {
		case "nr_periods":
			if cpuThrottledRaw.NrPeriod, err = strconv.ParseInt(val, 10, 64); err != nil {
				return nil, fmt.Errorf("parsec nr_periods field failed, path %s, raw content %s, err: %v",
					cgroupPath, content, err)
			}
			counter++
		case "nr_throttled":
			if cpuThrottledRaw.NrThrottled, err = strconv.ParseInt(val, 10, 64); err != nil {
				return nil, fmt.Errorf("parse nr_throttled field failed, path %s, raw content %s, err: %v",
					cgroupPath, content, err)
			}
			counter++
		case "throttled_time":
			if cpuThrottledRaw.ThrottledNanoSeconds, err = strconv.ParseInt(val, 10, 64); err != nil {
				return nil, fmt.Errorf("parse throttled_time field failed, path %s, raw content %s, err: %v",
					cgroupPath, content, err)
			}
			counter++
		}
	}

	if counter != 3 {
		return nil, fmt.Errorf("parse cpu throttled iterms failed, path %s, raw content %s, err: %v",
			cgroupPath, content, err)
	}

	return cpuThrottledRaw, nil
}

func CalcCPUThrottledRatio(curPoint, prePoint *CPUStatRaw) float64 {
	deltaPeriod := curPoint.NrPeriod - prePoint.NrPeriod
	deltaThrottled := curPoint.NrThrottled - prePoint.NrThrottled
	throttledRatio := float64(0)
	if deltaPeriod > 0 {
		throttledRatio = float64(deltaThrottled) / float64(deltaPeriod)
	}
	return throttledRatio
}
