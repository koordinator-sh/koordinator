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
)

type Cgroup struct {
	Path   string   `json:"path"`
	Dev    [2]int64 `json:"dev"`
	Cpu    Resource `json:"cpu"`
	Memory Resource `json:"memory"`
	Bps    Resource `json:"bps"`
	Iops   Resource `json:"iops"`
}

type ResourceMask int

const (
	ResourceMaskCpu ResourceMask = 1 << iota
	ResourceMaskMemory
	ResourceMaskIO
	ResourceMaskAll = ResourceMaskCpu | ResourceMaskMemory | ResourceMaskIO
)

func NewCgroup(path string, dev [2]int64) *Cgroup {
	return &Cgroup{
		Path:   path,
		Dev:    dev,
		Cpu:    &Cpu{path: path},
		Memory: &Memory{path: path},
		Bps:    &Bps{path: path, dev: fmt.Sprintf("%d:%d", dev[0], dev[1])},
		Iops:   &Iops{path: path, dev: fmt.Sprintf("%d:%d", dev[0], dev[1])},
	}
}

func (c *Cgroup) Load(mask ResourceMask) (err error) {
	// cpu
	if mask&ResourceMaskCpu != 0 {
		err = errors.Join(err, c.LoadCpu())
	}
	// memory
	if mask&ResourceMaskMemory != 0 {
		err = errors.Join(err, c.LoadMemory())
	}
	// io
	if mask&ResourceMaskIO != 0 {
		err = errors.Join(err, c.LoadIO())
	}
	return err
}

func (c *Cgroup) LoadCpu() error {
	pressure, err := ReadCpuPressure(c.Path)
	if err != nil {
		return err
	}
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
	switch r := c.Cpu.(type) {
	case *Cpu:
		r.Update(pressure, stat.UsageUsec, max.QuotaInSecond(), weight)
	default:
		return fmt.Errorf("resource %T is not Cpu", r)
	}
	return nil
}

func (c *Cgroup) LoadMemory() error {
	pressure, err := ReadMemoryPressure(c.Path)
	if err != nil {
		return err
	}
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
	switch r := c.Memory.(type) {
	case *Memory:
		r.Update(pressure, current, high, min)
	default:
		return fmt.Errorf("resource %T is not Memory", r)
	}
	return nil
}

func (c *Cgroup) LoadIO() error {
	pressure, err := ReadIOPressure(c.Path)
	if err != nil {
		return err
	}
	statDevice, err := ReadIOStat(c.Path)
	if err != nil {
		return err
	}
	maxDevice, err := ReadIOMax(c.Path)
	if err != nil {
		return err
	}
	dev := fmt.Sprintf("%d:%d", c.Dev[0], c.Dev[1])
	stat := statDevice[dev]
	max := maxDevice[dev]
	switch r := c.Bps.(type) {
	case *Bps:
		r.Update(pressure, stat.Rbytes, stat.Wbytes, max.Rbps, max.Wbps)
	default:
		return fmt.Errorf("resource %T is not Bps", r)
	}
	switch r := c.Iops.(type) {
	case *Iops:
		r.Update(pressure, stat.Rios, stat.Wios, max.Riops, max.Wiops)
	default:
		return fmt.Errorf("resource %T is not Iops", r)
	}
	return nil
}
