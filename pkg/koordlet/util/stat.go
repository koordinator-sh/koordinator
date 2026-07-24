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
	"os"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/perf"
	perfgroup "github.com/koordinator-sh/koordinator/pkg/koordlet/util/perf_group"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func readTotalCPUStat(statPath string) (uint64, error) {
	// stat usage: $user + $nice + $system + $irq + $softirq
	rawStats, err := os.ReadFile(statPath)
	if err != nil {
		return 0, err
	}
	stats := strings.Split(string(rawStats), "\n")
	for _, stat := range stats {
		fieldStat := strings.Fields(stat)
		if len(fieldStat) > 0 && fieldStat[0] == "cpu" {
			return parseCPUStatUsageTicks(fieldStat, statPath)
		}
	}
	return 0, fmt.Errorf("%s is illegally formatted", statPath)
}

// parseCPUStatUsageTicks parses the usage ticks from the fields of a "cpu"/"cpuN" line in /proc/stat.
func parseCPUStatUsageTicks(fieldStat []string, statPath string) (uint64, error) {
	if len(fieldStat) <= 7 {
		return 0, fmt.Errorf("%s is illegally formatted", statPath)
	}
	var total uint64 = 0
	// format: cpu $user $nice $system $idle $iowait $irq $softirq
	for _, i := range []int{1, 2, 3, 6, 7} {
		v, err := strconv.ParseUint(fieldStat[i], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse node stat %v, err: %s", fieldStat, err)
		}
		total += v
	}
	return total, nil
}

// readPerCPUStat parses the per-CPU usage ticks from the "cpuN" lines in /proc/stat.
func readPerCPUStat(statPath string) (map[int32]uint64, error) {
	rawStats, err := os.ReadFile(statPath)
	if err != nil {
		return nil, err
	}
	cpuTicks := map[int32]uint64{}
	stats := strings.Split(string(rawStats), "\n")
	for _, stat := range stats {
		fieldStat := strings.Fields(stat)
		// skip the summary "cpu" line and non-cpu lines
		if len(fieldStat) <= 0 || !strings.HasPrefix(fieldStat[0], "cpu") || fieldStat[0] == "cpu" {
			continue
		}
		cpuID, err := strconv.ParseInt(strings.TrimPrefix(fieldStat[0], "cpu"), 10, 32)
		if err != nil {
			continue
		}
		ticks, err := parseCPUStatUsageTicks(fieldStat, statPath)
		if err != nil {
			return nil, err
		}
		cpuTicks[int32(cpuID)] = ticks
	}
	if len(cpuTicks) <= 0 {
		return nil, fmt.Errorf("%s has no per-cpu stat line", statPath)
	}
	return cpuTicks, nil
}

// GetCPUStatUsageTicks returns the node's CPU usage ticks
func GetCPUStatUsageTicks() (uint64, error) {
	statPath := system.GetProcFilePath(system.ProcStatName)
	return readTotalCPUStat(statPath)
}

// GetPerCPUStatUsageTicks returns the CPU usage ticks of each logical CPU, keyed by the CPU ID.
func GetPerCPUStatUsageTicks() (map[int32]uint64, error) {
	statPath := system.GetProcFilePath(system.ProcStatName)
	return readPerCPUStat(statPath)
}

func GetContainerPerfGroupCollector(podCgroupDir string, c *corev1.ContainerStatus, number int32, events []string) (*perfgroup.PerfGroupCollector, error) {
	cpus := make([]int, number)
	for i := range cpus {
		cpus[i] = i
	}
	// get file descriptor for cgroup mode perf_event_open
	containerCgroupFile, err := getContainerCgroupFile(podCgroupDir, c)
	if err != nil {
		return nil, err
	}
	collector, err := perfgroup.GetAndStartPerfGroupCollectorOnContainer(containerCgroupFile, cpus, events)
	if err != nil {
		return nil, err
	}
	return collector, nil
}

func GetContainerPerfCollector(podCgroupDir string, c *corev1.ContainerStatus, number int32) (*perf.PerfCollector, error) {
	cpus := make([]int, number)
	for i := range cpus {
		cpus[i] = i
	}
	// get file descriptor for cgroup mode perf_event_open
	containerCgroupFile, err := getContainerCgroupFile(podCgroupDir, c)
	if err != nil {
		return nil, err
	}
	collector, err := perf.GetAndStartPerfCollectorOnContainer(containerCgroupFile, cpus)
	if err != nil {
		return nil, err
	}
	return collector, nil
}

func GetContainerCyclesAndInstructions(collector perf.Collector) (float64, float64, error) {
	if pc, ok := collector.(*perfgroup.PerfGroupCollector); ok {
		return perfgroup.GetContainerCyclesAndInstructionsGroup(pc)
	} else {
		return perf.GetContainerCyclesAndInstructions(collector.(*perf.PerfCollector))
	}
}

func getContainerCgroupFile(podCgroupDir string, c *corev1.ContainerStatus) (*os.File, error) {
	containerCgroupFilePath, err := GetContainerCgroupPerfPath(podCgroupDir, c)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(containerCgroupFilePath, os.O_RDONLY, os.ModeDir)
	if err != nil {
		return nil, err
	}
	return f, nil
}
