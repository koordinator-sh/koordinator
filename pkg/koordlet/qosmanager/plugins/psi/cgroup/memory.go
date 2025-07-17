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
	MemoryName string   = "memory"
	_          Resource = &Memory{}
)

type Memory struct {
	Base
	// path is the path of cgroup
	path string
	// pressure [μs] is `memory.pressure`
	pressure Pressure `json:"-"`
	// current [bytes] is `memory.current`
	current int64 `json:"-"`
}

func (memory *Memory) Name() string {
	return MemoryName
}

func (memory *Memory) Format(v int64) Value {
	return Bytes(v)
}

func (memory *Memory) Current() Value {
	return Bytes(memory.Base.Current)
}

func (memory *Memory) Promise() Value {
	return Bytes(memory.Base.Promise)
}

func (memory *Memory) Throttle() Value {
	return Bytes(memory.Base.Throttle)
}

func (memory *Memory) Pressure() UsecRate {
	return UsecRate(memory.Base.Pressure)
}

func (memory *Memory) Pressure10() float64 {
	return memory.pressure.Some.Avg10
}

func (memory *Memory) Pressure60() float64 {
	return memory.pressure.Some.Avg60
}

func (memory *Memory) Pressure300() float64 {
	return memory.pressure.Some.Avg300
}

func (memory *Memory) Update(pressure *Pressure, current, high, min int64) {
	now := time.Now()
	memory.Base.Pressure = slope(memory.pressure.Some.Total, pressure.Some.Total, now.Sub(memory.Timestamp))
	memory.Base.Current = current
	memory.Base.Throttle = high
	memory.Base.Promise = min

	memory.pressure = *pressure
	memory.current = current
	memory.Timestamp = now
}

func (memory *Memory) SetThrottle(high int64) error {
	if err := WriteMemoryHigh(memory.path, high); err != nil {
		return err
	}
	memory.Base.Throttle = high
	return nil
}

func (memory *Memory) SetPromise(min int64) error {
	if err := WriteMemoryMin(memory.path, min); err != nil {
		return err
	}
	memory.Base.Promise = min
	return nil
}
