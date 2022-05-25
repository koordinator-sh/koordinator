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
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

var (
	CpuacctUsageTypeStat = sets.NewString("user", "nice", "system", "irq", "softirq")
)

func readTotalCPUStat(statPath string) (uint64, error) {
	// stat usage: $user + $nice + $system + $irq + $softirq
	rawStats, err := ioutil.ReadFile(statPath)
	if err != nil {
		return 0, err
	}
	stats := strings.Split(string(rawStats), "\n")
	for _, stat := range stats {
		fieldStat := strings.Fields(stat)
		if fieldStat[0] == "cpu" {
			if len(fieldStat) <= 7 {
				return 0, fmt.Errorf("%s is illegally formatted", statPath)
			}
			var total uint64 = 0
			// format: cpu $user $nice $system $idle $iowait $irq $softirq
			for _, i := range []int{1, 2, 3, 6, 7} {
				v, err := strconv.ParseUint(fieldStat[i], 10, 64)
				if err != nil {
					return 0, fmt.Errorf("failed to parse node stat %s, err: %s", stat, err)
				}
				total += v
			}
			return total, nil
		}
	}
	return 0, fmt.Errorf("%s is illegally formatted", statPath)
}

// GetCPUStatUsageTicks returns the node's CPU usage ticks
func GetCPUStatUsageTicks() (uint64, error) {
	return readTotalCPUStat(system.ProcStatFile.File)
}

func readCPUAcctStatUsageTicks(statPath string) (uint64, error) {
	// format: user $user\nnice $nice\nsystem $system\nidle $idle\niowait $iowait\nirq $irq\nsoftirq $softirq
	rawStats, err := ioutil.ReadFile(statPath)
	if err != nil {
		return 0, err
	}
	var total uint64 = 0
	stats := strings.Split(string(rawStats), "\n")
	for _, stat := range stats {
		fieldStat := strings.Fields(stat)
		// stat usage: $user + $nice + $system + $irq + $softirq
		if len(fieldStat) == 2 && CpuacctUsageTypeStat.Has(fieldStat[0]) {
			v, err := strconv.ParseUint(fieldStat[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("failed to parse pod stats %v, err: %s", stats, err)
			}
			total += v
		}
	}
	return total, nil
}

// GetPodCPUStatUsageTicks returns the pod's CPU usage ticks
func GetPodCPUStatUsageTicks(podCgroupDir string) (uint64, error) {
	podStatPath := GetPodCgroupCPUAcctProcStatPath(podCgroupDir)
	return readCPUAcctStatUsageTicks(podStatPath)
}

func GetContainerCPUStatUsageTicks(podCgroupDir string, c *corev1.ContainerStatus) (uint64, error) {
	containerStatPath, err := GetContainerCgroupCPUAcctProcStatPath(podCgroupDir, c)
	if err != nil {
		return 0, err
	}
	return readCPUAcctStatUsageTicks(containerStatPath)
}

func GetRootCgroupCPUStatUsageTicks(qosClass corev1.PodQOSClass) (uint64, error) {
	rootCgroupParentDir := GetKubeQosRelativePath(qosClass)
	statPath := system.GetCgroupFilePath(rootCgroupParentDir, system.CpuacctStat)
	return readCPUAcctStatUsageTicks(statPath)
}
