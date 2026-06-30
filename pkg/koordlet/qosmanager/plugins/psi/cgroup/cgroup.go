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
	"errors"
	"fmt"
	"strconv"
	"strings"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

var InvalidDevice = [2]int64{-1, -1}

type Cgroup struct {
	Path   string   `json:"path"`
	Dev    [2]int64 `json:"dev"`
	Cpu    *Cpu     `json:"cpu"`
	Memory *Memory  `json:"memory"`
	Bps    *Bps     `json:"bps"`
	Iops   *Iops    `json:"iops"`
}

type ResourceMask int

const (
	ResourceMaskCpu ResourceMask = 1 << iota
	ResourceMaskMemory
	ResourceMaskIO
	ResourceMaskAll = ResourceMaskCpu | ResourceMaskMemory | ResourceMaskIO
)

func NewCgroup(path string, dev [2]int64) *Cgroup {
	ioDev := fmt.Sprintf("%d:%d", dev[0], dev[1])
	return &Cgroup{
		Path:   path,
		Dev:    dev,
		Cpu:    newCPU(path),
		Memory: newMemory(path),
		Bps:    newBPS(path, ioDev),
		Iops:   newIOPS(path, ioDev),
	}
}

func (c *Cgroup) hasDevice() bool {
	return c.Dev[0] >= 0 && c.Dev[1] >= 0
}

func (c *Cgroup) Load(mask ResourceMask) (err error) {
	var psi *sysutil.PSIByResource
	if mask != 0 {
		psi, err = readPSI(c.Path)
		if err != nil {
			return err
		}
	}
	// cpu
	if mask&ResourceMaskCpu != 0 {
		err = errors.Join(err, c.loadCpu(psiToPressure(psi.CPU)))
	}
	// memory
	if mask&ResourceMaskMemory != 0 {
		err = errors.Join(err, c.loadMemory(psiToPressure(psi.Mem)))
	}
	// io
	if mask&ResourceMaskIO != 0 {
		err = errors.Join(err, c.loadIO(psiToPressure(psi.IO)))
	}
	return err
}

func (c *Cgroup) LoadCpu() error {
	pressure, err := ReadCpuPressure(c.Path)
	if err != nil {
		return err
	}
	return c.loadCpu(pressure)
}

func (c *Cgroup) loadCpu(pressure *Pressure) error {
	stat, err := ReadCpuStat(c.Path)
	if err != nil {
		return err
	}
	max, err := ReadCpuMax(c.Path)
	if err != nil {
		return err
	}
	weight, err := ReadCpuWeight(c.Path)
	if err != nil {
		return err
	}
	c.Cpu.Update(pressure, stat.UsageUsec, max.QuotaInSecond(), weight)
	return nil
}

func (c *Cgroup) LoadMemory() error {
	pressure, err := ReadMemoryPressure(c.Path)
	if err != nil {
		return err
	}
	return c.loadMemory(pressure)
}

func (c *Cgroup) loadMemory(pressure *Pressure) error {
	current, err := ReadMemoryCurrent(c.Path)
	if err != nil {
		return err
	}
	high, err := ReadMemoryHigh(c.Path)
	if err != nil {
		return err
	}
	min, err := ReadMemoryMin(c.Path)
	if err != nil {
		return err
	}
	c.Memory.Update(pressure, current, high, min)
	return nil
}

func (c *Cgroup) LoadIO() error {
	pressure, err := ReadIOPressure(c.Path)
	if err != nil {
		return err
	}
	return c.loadIO(pressure)
}

func (c *Cgroup) loadIO(pressure *Pressure) error {
	statDevice, err := ReadIOStat(c.Path)
	if err != nil {
		return err
	}
	maxDevice, err := ReadIOMax(c.Path)
	if err != nil {
		return err
	}
	dev, ok := c.selectIODevice(statDevice)
	if !ok {
		c.updateIOPressure(pressure)
		return nil
	}
	stat := statDevice[dev]
	max := maxDevice[dev]
	c.Bps.Update(pressure, stat.Rbytes, stat.Wbytes, max.Rbps, max.Wbps)
	c.Iops.Update(pressure, stat.Rios, stat.Wios, max.Riops, max.Wiops)
	return nil
}

func (c *Cgroup) selectIODevice(statDevice map[string]IOStat) (string, bool) {
	if c.hasDevice() {
		return fmt.Sprintf("%d:%d", c.Dev[0], c.Dev[1]), true
	}
	if len(statDevice) != 1 {
		return "", false
	}
	for dev := range statDevice {
		if parsed, ok := parseDevice(dev); ok {
			c.Dev = parsed
			c.setIODevice(dev)
			return dev, true
		}
	}
	return "", false
}

func (c *Cgroup) setIODevice(dev string) {
	c.Bps.dev = dev
	c.Iops.dev = dev
}

func parseDevice(dev string) ([2]int64, bool) {
	parts := strings.Split(dev, ":")
	if len(parts) != 2 {
		return InvalidDevice, false
	}
	major, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return InvalidDevice, false
	}
	minor, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return InvalidDevice, false
	}
	return [2]int64{major, minor}, true
}

func (c *Cgroup) updateIOPressure(pressure *Pressure) error {
	c.Bps.UpdatePressure(pressure)
	c.Iops.UpdatePressure(pressure)
	return nil
}
