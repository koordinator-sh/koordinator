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
	"time"
)

var (
	CpuName string   = "cpu"
	_       Resource = &Cpu{}
)

type Cpu struct {
	// Base of cpu presents usage usec in 1 second
	Base
	// path is the path of cgroup
	path string
	// pressure [μs] is `cpu.pressure`
	pressure Pressure `json:"-"`
	// usageUsec [μs] is usage_usec of `cpu.stat`
	usageUsec int64 `json:"-"`
	// quota [μs] is quota of `cpu.max` in 1 second
	quota int64 `json:"-"`
}

func (cpu *Cpu) Name() string {
	return CpuName
}

func (cpu *Cpu) Format(v int64) Value {
	return Usec(v)
}

func (cpu *Cpu) Current() Value {
	return Usec(cpu.Base.Current)
}

func (cpu *Cpu) Promise() Value {
	return Usec(cpu.Base.Promise)
}

func (cpu *Cpu) Throttle() Value {
	return Usec(cpu.Base.Throttle)
}

func (cpu *Cpu) Pressure() UsecRate {
	return UsecRate(cpu.Base.Pressure)
}

func (cpu *Cpu) Pressure10() float64 {
	return cpu.pressure.Some.Avg10
}

func (cpu *Cpu) Pressure60() float64 {
	return cpu.pressure.Some.Avg60
}

func (cpu *Cpu) Pressure300() float64 {
	return cpu.pressure.Some.Avg300
}

func (cpu *Cpu) Update(pressure *Pressure, usageUsec, quotaInSecond int64, weight uint64) {
	now := time.Now()
	cpu.Base.Pressure = slope(cpu.pressure.Some.Total, pressure.Some.Total, now.Sub(cpu.Timestamp))
	cpu.Base.Current = slope(cpu.usageUsec, usageUsec, now.Sub(cpu.Timestamp))
	cpu.Base.Throttle = quotaInSecond
	cpu.Base.Promise = QuotaFromWeight(weight).QuotaInSecond()

	cpu.pressure = *pressure
	cpu.usageUsec = usageUsec
	cpu.quota = quotaInSecond
	cpu.Timestamp = now
}

func (cpu *Cpu) SetThrottle(quotaInSecond int64) error {
	if err := WriteCpuMax(cpu.path, &CpuQuota{Quota: quotaInSecond / 10, Period: 100000}); err != nil {
		return err
	}
	cpu.quota = quotaInSecond
	cpu.Base.Throttle = quotaInSecond
	return nil
}

func (cpu *Cpu) SetPromise(quotaInSecond int64) error {
	m := CpuQuota{Quota: quotaInSecond, Period: time.Second.Microseconds()}
	return WriteCpuWeight(cpu.path, m.ToWeight())
}
