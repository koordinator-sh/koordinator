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

	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	Max                   int64  = math.MaxInt64
	milliCPUToCPU         int64  = 1000
	Cgroup2CpuStat        string = "cpu.stat"
	Cgroup2CpuMax         string = "cpu.max"
	Cgroup2CpuWeight      string = "cpu.weight"
	Cgroup2CpuPressure    string = "cpu.pressure"
	Cgroup2MemoryCurrent  string = "memory.current"
	Cgroup2MemoryHigh     string = "memory.high"
	Cgroup2MemoryMin      string = "memory.min"
	Cgroup2MemoryPressure string = "memory.pressure"
	Cgroup2IOStat         string = "io.stat"
	Cgroup2IOMax          string = "io.max"
	Cgroup2IOPressure     string = "io.pressure"
)

var cgroupReaderFactory = resourceexecutor.NewCgroupReader

func setCgroupReaderFactoryForTest(factory func() resourceexecutor.CgroupReader) func() {
	old := cgroupReaderFactory
	cgroupReaderFactory = factory
	return func() {
		cgroupReaderFactory = old
	}
}

func getCgroupResource(resourceType sysutil.ResourceType) (sysutil.Resource, error) {
	resource, err := sysutil.GetCgroupResource(resourceType)
	if err != nil {
		return nil, fmt.Errorf("failed to get cgroup resource %s: %w", resourceType, err)
	}
	return resource, nil
}

func readKnownCgroupResource(cgroupPath string, resourceType sysutil.ResourceType) (string, error) {
	resource, err := getCgroupResource(resourceType)
	if err != nil {
		return "", err
	}
	return resourceexecutor.ReadCgroupResource(cgroupPath, resource)
}

func readKnownCgroupResourceInt(cgroupPath string, resourceType sysutil.ResourceType) (int64, error) {
	resource, err := getCgroupResource(resourceType)
	if err != nil {
		return 0, err
	}
	return resourceexecutor.ReadCgroupResourceInt(cgroupPath, resource)
}

func writeKnownCgroupResource(cgroupPath string, resourceType sysutil.ResourceType, content string) error {
	resource, err := getCgroupResource(resourceType)
	if err != nil {
		return err
	}
	return resourceexecutor.WriteCgroupResource(cgroupPath, resource, content)
}

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
	milliCPU := c.Quota * milliCPUToCPU / c.Period
	return GetCpuWeight(milliCPU)
}

func QuotaFromWeight(weight uint64) *CpuQuota {
	milliCPU := GetMilliCPU(weight)
	quotaPeriod := sysutil.CFSBasePeriodValue
	if quotaPeriod <= 0 || milliCPUToCPU <= 0 {
		return &CpuQuota{Quota: 0, Period: 100000}
	}
	return &CpuQuota{Quota: milliCPU * quotaPeriod / milliCPUToCPU, Period: quotaPeriod}
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
	statTxt, err := readKnownCgroupResource(cgroupPath, sysutil.CPUStatName)
	if err != nil {
		return nil, err
	}
	stat, err := sysutil.ParseCPUAcctStatRawV2(statTxt)
	if err != nil {
		return nil, err
	}
	return &CpuStat{
		UsageUsec:  stat.UsageUsec,
		UserUsec:   stat.UserUsec,
		SystemUsec: stat.SystemUSec,
	}, nil
}

func ReadCpuMax(cgroupPath string) (*CpuQuota, error) {
	txt, err := readKnownCgroupResource(cgroupPath, sysutil.CPUCFSQuotaName)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", Cgroup2CpuMax, err)
	}
	quota, err := sysutil.ParseCPUCFSQuotaV2(txt)
	if err != nil {
		return nil, err
	}
	period, err := sysutil.ParseCPUCFSPeriodV2(txt)
	if err != nil {
		return nil, err
	}
	return &CpuQuota{Quota: quota, Period: period}, nil
}

func WriteCpuMax(cgroupPath string, max *CpuQuota) error {
	if err := writeKnownCgroupResource(cgroupPath, sysutil.CPUCFSQuotaName, max.String()); err != nil {
		return fmt.Errorf("failed to write %q to %s: %w", max.String(), Cgroup2CpuMax, err)
	}
	return nil
}

func ReadCpuWeight(cgroupPath string) (uint64, error) {
	weight, err := readKnownCgroupResource(cgroupPath, sysutil.CPUSharesName)
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
	if err := writeKnownCgroupResource(cgroupPath, sysutil.CPUSharesName, fmt.Sprintf("%d", weight)); err != nil {
		return fmt.Errorf("failed to write %d to %s: %w", weight, Cgroup2CpuWeight, err)
	}
	return nil
}

func ReadCpuPressure(cgroupPath string) (*Pressure, error) {
	psi, err := readPSI(cgroupPath)
	if err != nil {
		return nil, err
	}
	return psiToPressure(psi.CPU), nil
}

func ReadMemoryCurrent(cgroupPath string) (int64, error) {
	current, err := readKnownCgroupResourceInt(cgroupPath, sysutil.MemoryUsageName)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", Cgroup2MemoryCurrent, err)
	}
	return current, nil
}

func ReadMemoryHigh(cgroupPath string) (int64, error) {
	high, err := readKnownCgroupResourceInt(cgroupPath, sysutil.MemoryHighName)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", Cgroup2MemoryHigh, err)
	}
	return high, nil
}

func WriteMemoryHigh(cgroupPath string, high int64) error {
	if err := writeKnownCgroupResource(cgroupPath, sysutil.MemoryHighName, Int64(high).String()); err != nil {
		return fmt.Errorf("failed to write %q to %s: %w", Int64(high).String(), Cgroup2MemoryHigh, err)
	}
	return nil
}

func ReadMemoryMin(cgroupPath string) (int64, error) {
	min, err := readKnownCgroupResourceInt(cgroupPath, sysutil.MemoryMinName)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", Cgroup2MemoryMin, err)
	}
	return min, nil
}

func WriteMemoryMin(cgroupPath string, min int64) error {
	if err := writeKnownCgroupResource(cgroupPath, sysutil.MemoryMinName, Int64(min).String()); err != nil {
		return fmt.Errorf("failed to write %q to %s: %w", Int64(min).String(), Cgroup2MemoryMin, err)
	}
	return nil
}

func ReadMemoryPressure(cgroupPath string) (*Pressure, error) {
	psi, err := readPSI(cgroupPath)
	if err != nil {
		return nil, err
	}
	return psiToPressure(psi.Mem), nil
}

func ReadIOStat(cgroupPath string) (map[string]IOStat, error) {
	stat, err := readCgroupV2NestedKeyedResource(cgroupPath, sysutil.IOStatName)
	if err != nil {
		return nil, err
	}
	res := make(map[string]IOStat, len(stat))
	for dev, stat := range stat {
		res[dev] = IOStat{
			Dev:    dev,
			Rbytes: stat["rbytes"],
			Wbytes: stat["wbytes"],
			Rios:   stat["rios"],
			Wios:   stat["wios"],
		}
	}
	return res, nil
}

func ReadIOMax(cgroupPath string) (map[string]IOMax, error) {
	max, err := readCgroupV2NestedKeyedResource(cgroupPath, sysutil.IOMaxName)
	if err != nil {
		return nil, err
	}
	res := make(map[string]IOMax, len(max))
	for dev, max := range max {
		res[dev] = IOMax{
			Dev:   dev,
			Rbps:  toCgroupMax(max["rbps"]),
			Wbps:  toCgroupMax(max["wbps"]),
			Riops: toCgroupMax(max["riops"]),
			Wiops: toCgroupMax(max["wiops"]),
		}
	}
	return res, nil
}

func WriteIOMax(cgroupPath string, max ...*IOMax) error {
	for _, m := range max {
		if err := writeKnownCgroupResource(cgroupPath, sysutil.IOMaxName, m.String()); err != nil {
			return fmt.Errorf("failed to write %q to %s: %w", m.String(), Cgroup2IOMax, err)
		}
	}
	return nil
}

func ReadIOPressure(cgroupPath string) (*Pressure, error) {
	psi, err := readPSI(cgroupPath)
	if err != nil {
		return nil, err
	}
	return psiToPressure(psi.IO), nil
}

func readPSI(cgroupPath string) (*sysutil.PSIByResource, error) {
	return cgroupReaderFactory().ReadPSI(cgroupPath)
}

func readCgroupV2NestedKeyedResource(cgroupPath string, resourceType sysutil.ResourceType) (map[string]map[string]int64, error) {
	txt, err := readKnownCgroupResource(cgroupPath, resourceType)
	if err != nil {
		return nil, err
	}
	return sysutil.ParseCgroupV2NestedKeyedFile(txt)
}

func parseInt64(s string) (int64, error) {
	s = strings.TrimSpace(strings.Trim(s, "\n"))
	if s == "max" {
		return Max, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func toCgroupMax(x int64) int64 {
	if x == math.MaxInt64 {
		return Max
	}
	return x
}

func GetCpuWeight(milliCPU int64) uint64 {
	cpuShares := uint64(sysutil.MilliCPUToShares(milliCPU))
	return cpuWeightFromShares(cpuShares)
}

func cpuWeightFromShares(cpuShares uint64) uint64 {
	if cpuShares >= 262144 {
		return 10000
	}
	return 1 + ((cpuShares-2)*9999)/262142
}

func GetMilliCPU(weight uint64) int64 {
	cpuShares, err := sysutil.ConvertCPUWeightToShares(int64(weight))
	if err != nil {
		return 0
	}
	return sharesToMilliCPU(cpuShares)
}

func sharesToMilliCPU(shares int64) int64 {
	if shares < sysutil.CPUSharesMinValue {
		return 0
	}
	return int64(math.Ceil(float64(shares*milliCPUToCPU) / float64(sysutil.CPUShareUnitValue)))
}

func psiToPressure(stats sysutil.PSIStats) *Pressure {
	result := &Pressure{}
	if stats.Some != nil {
		result.Some = PressureItem{
			Avg10:  stats.Some.Avg10,
			Avg60:  stats.Some.Avg60,
			Avg300: stats.Some.Avg300,
			Total:  int64(stats.Some.Total),
		}
	}
	if stats.Full != nil {
		result.Full = PressureItem{
			Avg10:  stats.Full.Avg10,
			Avg60:  stats.Full.Avg60,
			Avg300: stats.Full.Avg300,
			Total:  int64(stats.Full.Total),
		}
	}
	return result
}
