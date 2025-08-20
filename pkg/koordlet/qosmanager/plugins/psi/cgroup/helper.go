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

package cgroup

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/opencontainers/runc/libcontainer/cgroups"
	"k8s.io/kubernetes/pkg/kubelet/cm"
)

const (
	Max                   int64   = math.MaxInt64
	Maxf                  float64 = math.MaxFloat64
	Cgroup2CpuStat        string  = "cpu.stat"
	Cgroup2CpuMax         string  = "cpu.max"
	Cgroup2CpuWeight      string  = "cpu.weight"
	Cgroup2CpuPressure    string  = "cpu.pressure"
	Cgroup2MemoryCurrent  string  = "memory.current"
	Cgroup2MemoryHigh     string  = "memory.high"
	Cgroup2MemoryMin      string  = "memory.min"
	Cgroup2MemoryPressure string  = "memory.pressure"
	Cgroup2IOStat         string  = "io.stat"
	Cgroup2IOMax          string  = "io.max"
	Cgroup2IOPressure     string  = "io.pressure"
)

type CpuStat struct {
	UsageUsec  int64
	UserUsec   int64
	SystemUsec int64
}

type CpuQuota struct {
	Quota  int64
	Period int64
}

func (c *CpuQuota) String() string {
	return fmt.Sprintf("%s %d", Int64(c.Quota), c.Period)
}

func (c *CpuQuota) QuotaInSecond() int64 {
	if c.Quota == Max {
		return Max
	}
	return c.Quota * time.Second.Microseconds() / c.Period
}

func (c *CpuQuota) ToWeight() uint64 {
	milliCPU := c.Quota * cm.MilliCPUToCPU / c.Period
	return GetCpuWeight(milliCPU)
}

func QuotaFromWeight(weight uint64) *CpuQuota {
	milliCPU := GetMilliCPU(weight)
	return &CpuQuota{Quota: milliCPU * cm.QuotaPeriod / cm.MilliCPUToCPU, Period: cm.QuotaPeriod}
}

type IOStat struct {
	Dev    string
	Rbytes int64
	Wbytes int64
	Rios   int64
	Wios   int64
}

type IOMax struct {
	Dev   string
	Rbps  int64
	Wbps  int64
	Riops int64
	Wiops int64
}

func (i *IOMax) String() string {
	return fmt.Sprintf("%s rbps=%d wbps=%d riops=%d wiops=%d", i.Dev, Int64(i.Rbps), Int64(i.Wbps), Int64(i.Riops), Int64(i.Wiops))
}

type PressureItem struct {
	Avg10  float64
	Avg60  float64
	Avg300 float64
	Total  int64
}

type Pressure struct {
	Some, Full PressureItem
}

func ReadCpuStat(cgroupPath string) (*CpuStat, error) {
	stat, err := parseCgroupFlatKeyedFile(cgroupPath, Cgroup2CpuStat)
	if err != nil {
		return nil, err
	}
	return &CpuStat{
		UsageUsec:  stat["usage_usec"],
		UserUsec:   stat["user_usec"],
		SystemUsec: stat["system_usec"],
	}, nil
}

func ReadCpuMax(cgroupPath string) (*CpuQuota, error) {
	txt, err := cgroups.ReadFile(cgroupPath, Cgroup2CpuMax)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", Cgroup2CpuMax, err)
	}
	parts := strings.Fields(txt)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid format: %s", txt)
	}
	quota, err := parseInt64(parts[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", parts[0], err)
	}
	period, err := parseInt64(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", parts[1], err)
	}
	return &CpuQuota{Quota: quota, Period: period}, nil
}

func WriteCpuMax(cgroupPath string, max *CpuQuota) error {
	if err := cgroups.WriteFile(cgroupPath, Cgroup2CpuMax, max.String()); err != nil {
		return fmt.Errorf("failed to write %q to %s: %w", max.String(), Cgroup2CpuMax, err)
	}
	return nil
}

func ReadCpuWeight(cgroupPath string) (uint64, error) {
	weight, err := cgroups.ReadFile(cgroupPath, Cgroup2CpuWeight)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", Cgroup2CpuWeight, err)
	}
	val, err := parseInt64(weight)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %s: %w", weight, err)
	}
	return uint64(val), nil
}

func WriteCpuWeight(cgroupPath string, weight uint64) error {
	if weight < 1 {
		weight = 1
	}
	if weight > 10000 {
		weight = 10000
	}
	if err := cgroups.WriteFile(cgroupPath, Cgroup2CpuWeight, fmt.Sprintf("%d", weight)); err != nil {
		return fmt.Errorf("failed to write %d to %s: %w", weight, Cgroup2CpuWeight, err)
	}
	return nil
}

func ReadCpuPressure(cgroupPath string) (*Pressure, error) {
	return parsePressure(cgroupPath, Cgroup2CpuPressure)
}

func ReadMemoryCurrent(cgroupPath string) (int64, error) {
	current, err := cgroups.ReadFile(cgroupPath, Cgroup2MemoryCurrent)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", Cgroup2MemoryCurrent, err)
	}
	val, err := parseInt64(current)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %s: %w", current, err)
	}
	return val, nil
}

func ReadMemoryHigh(cgroupPath string) (int64, error) {
	high, err := cgroups.ReadFile(cgroupPath, Cgroup2MemoryHigh)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", Cgroup2MemoryHigh, err)
	}
	val, err := parseInt64(high)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %s: %w", high, err)
	}
	return val, nil
}

func WriteMemoryHigh(cgroupPath string, high int64) error {
	if err := cgroups.WriteFile(cgroupPath, Cgroup2MemoryHigh, Int64(high).String()); err != nil {
		return fmt.Errorf("failed to write %q to %s: %w", Int64(high).String(), Cgroup2MemoryHigh, err)
	}
	return nil
}

func ReadMemoryMin(cgroupPath string) (int64, error) {
	min, err := cgroups.ReadFile(cgroupPath, Cgroup2MemoryMin)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", Cgroup2MemoryMin, err)
	}
	val, err := parseInt64(min)
	if err != nil {
		return 0, fmt.Errorf("failed to parse %s: %w", min, err)
	}
	return val, nil
}

func WriteMemoryMin(cgroupPath string, min int64) error {
	if err := cgroups.WriteFile(cgroupPath, Cgroup2MemoryMin, Int64(min).String()); err != nil {
		return fmt.Errorf("failed to write %q to %s: %w", Int64(min).String(), Cgroup2MemoryMin, err)
	}
	return nil
}

func ReadMemoryPressure(cgroupPath string) (*Pressure, error) {
	return parsePressure(cgroupPath, Cgroup2MemoryPressure)
}

func ReadIOStat(cgroupPath string) (map[string]IOStat, error) {
	stat, err := parseCgroupNestedKeyedFile(cgroupPath, Cgroup2IOStat)
	if err != nil {
		return nil, err
	}
	res := make(map[string]IOStat, len(stat))
	for dev, stat := range stat {
		res[dev] = IOStat{
			Dev:    dev,
			Rbytes: toInt64(stat["rbytes"]),
			Wbytes: toInt64(stat["wbytes"]),
			Rios:   toInt64(stat["rios"]),
			Wios:   toInt64(stat["wios"]),
		}
	}
	return res, nil
}

func ReadIOMax(cgroupPath string) (map[string]IOMax, error) {
	max, err := parseCgroupNestedKeyedFile(cgroupPath, Cgroup2IOMax)
	if err != nil {
		return nil, err
	}
	res := make(map[string]IOMax, len(max))
	for dev, max := range max {
		res[dev] = IOMax{
			Dev:   dev,
			Rbps:  toInt64(max["rbps"]),
			Wbps:  toInt64(max["wbps"]),
			Riops: toInt64(max["riops"]),
			Wiops: toInt64(max["wiops"]),
		}
	}
	return res, nil
}

func WriteIOMax(cgroupPath string, max ...*IOMax) error {
	for _, m := range max {
		if err := cgroups.WriteFile(cgroupPath, Cgroup2IOMax, m.String()); err != nil {
			return fmt.Errorf("failed to write %q to %s: %w", m.String(), Cgroup2IOMax, err)
		}
	}
	return nil
}

func ReadIOPressure(cgroupPath string) (*Pressure, error) {
	return parsePressure(cgroupPath, Cgroup2IOPressure)
}

func parsePressure(dir string, file string) (*Pressure, error) {
	p, err := parseCgroupNestedKeyedFile(dir, file)
	if err != nil {
		return nil, err
	}
	some, full := p["some"], p["full"]
	return &Pressure{
		Some: PressureItem{
			Avg10:  some["avg10"],
			Avg60:  some["avg60"],
			Avg300: some["avg300"],
			Total:  toInt64(some["total"]),
		},
		Full: PressureItem{
			Avg10:  full["avg10"],
			Avg60:  full["avg60"],
			Avg300: full["avg300"],
			Total:  toInt64(full["total"]),
		},
	}, nil
}

// parseCgroupNestedKeyedFile parses the content of a cgroup v2 nested keyed file.
// The form of a nested keyed file is as follows:
//
//	0:0 <key1=value1> <key2=value2> ...
//	0:1 <key1=value1> <key2=value2> ...
//	...
func parseCgroupNestedKeyedFile(dir string, file string) (map[string]map[string]float64, error) {
	txt, err := cgroups.ReadFile(dir, file)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", file, err)
	}

	// Initialize the nested key value map
	nestedValues := make(map[string]map[string]float64)

	lines := strings.Split(txt, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue // skip empty lines
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid line format: %s", line)
		}
		nested := make(map[string]float64)
		for _, part := range parts[1:] {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				return nil, fmt.Errorf("invalid key=value pair: %s", part)
			}
			val, err := parseFloat64(kv[1])
			if err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", kv[1], err)
			}
			nested[kv[0]] = val
		}
		nestedValues[parts[0]] = nested // the first part is the key like "0:0"
	}
	return nestedValues, nil
}

// parseCgroupFlatKeyedFile parses the content of a cgroup v2 flat keyed file.
// The form of a flat keyed file is as follows:
//
//	default 100 (optional)
//	key1 value1
//	key2 value2
//	...
func parseCgroupFlatKeyedFile(dir string, file string) (map[string]int64, error) {
	txt, err := cgroups.ReadFile(dir, file)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %v", file, err)
	}

	// Initialize the flat key value map
	flatValues := make(map[string]int64)

	lines := strings.Split(txt, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue // skip empty lines
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid line format: %s", line)
		}
		val, err := parseInt64(parts[1])
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", parts[1], err)
		}
		flatValues[parts[0]] = val
	}
	return flatValues, nil
}

func parseInt64(s string) (int64, error) {
	s = strings.TrimSpace(strings.Trim(s, "\n"))
	if s == "max" {
		return Max, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func parseFloat64(s string) (float64, error) {
	s = strings.TrimSpace(strings.Trim(s, "\n"))
	if s == "max" {
		return Maxf, nil
	}
	return strconv.ParseFloat(s, 64)
}

func toInt64(x float64) int64 {
	if x == Maxf {
		return Max
	}
	return int64(x)
}

func GetCpuWeight(milliCPU int64) uint64 {
	cpuShares := cm.MilliCPUToShares(milliCPU)
	return getCpuWeight(&cpuShares)
}

// getCpuWeight converts from the range [2, 262144] to [1, 10000]
// refers to k8s.io/kubernetes/pkg/kubelet/cm/cgroup_manager_linux.go
func getCpuWeight(cpuShares *uint64) uint64 {
	if cpuShares == nil {
		return 0
	}
	if *cpuShares >= 262144 {
		return 10000
	}
	return 1 + ((*cpuShares-2)*9999)/262142
}

func GetMilliCPU(weight uint64) int64 {
	cpuShares := (weight-1)*262142/9999 + 2
	return int64(cpuShares) * cm.MilliCPUToCPU / cm.SharesPerCPU
}
