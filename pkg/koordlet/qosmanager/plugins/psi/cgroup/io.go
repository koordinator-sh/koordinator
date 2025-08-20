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
	"time"
)

var (
	BpsName  string   = "bps"
	IopsName string   = "iops"
	_        Resource = &Bps{}
	_        Resource = &Iops{}
)

type Bps struct {
	Base
	// dev is the device of cgroup
	dev string
	// path is the path of cgroup
	path string
	// pressure [μs] is `io.pressure`
	pressure Pressure `json:"-"`
	// rbytes [bytes] is rbytes of `io.stat`
	rbytes int64 `json:"-"`
	// wbytes [bytes] is wbytes of `io.stat`
	wbytes int64 `json:"-"`
	// rbpsMax [bytes] is rbps of `io.max`
	rbpsMax int64 `json:"-"`
	// wbpsMax [bytes] is wbps of `io.max`
	wbpsMax int64 `json:"-"`
	// rbps [bytes] is calculated from Rbytes
	rbps int64 `json:"-"`
	// wbps [bytes] is calculated from Wbytes
	wbps int64 `json:"-"`
}

func (bps *Bps) Name() string {
	return BpsName
}

func (bps *Bps) Format(v int64) Value {
	return Bytes(v)
}

func (bps *Bps) Current() Value {
	return Bytes(bps.Base.Current)
}

func (bps *Bps) Promise() Value {
	return Bytes(bps.Base.Promise)
}

func (bps *Bps) Throttle() Value {
	return Bytes(bps.Base.Throttle)
}

func (bps *Bps) Pressure() UsecRate {
	return UsecRate(bps.Base.Pressure)
}

func (bps *Bps) Pressure10() float64 {
	return bps.pressure.Some.Avg10
}

func (bps *Bps) Pressure60() float64 {
	return bps.pressure.Some.Avg60
}

func (bps *Bps) Pressure300() float64 {
	return bps.pressure.Some.Avg300
}

func (bps *Bps) Update(pressure *Pressure, rbytes, wbytes, rbpsMax, wbpsMax int64) {
	now := time.Now()
	bps.Base.Pressure = slope(bps.pressure.Some.Total, pressure.Some.Total, now.Sub(bps.Timestamp))
	bps.rbps = slope(bps.rbytes, rbytes, now.Sub(bps.Timestamp))
	bps.wbps = slope(bps.wbytes, wbytes, now.Sub(bps.Timestamp))
	bps.Base.Current = max(bps.rbps, bps.wbps)
	bps.Base.Throttle = max(rbpsMax, wbpsMax)
	// bps.Base.Pressure = float64(bps.Base.Pressure) * unit(bps.Percent()) // shunt io pressure

	bps.pressure = *pressure
	bps.rbpsMax = rbpsMax
	bps.wbpsMax = wbpsMax
	bps.rbytes = rbytes
	bps.wbytes = wbytes
	bps.Timestamp = now
}

func (bps *Bps) SetThrottle(newBps int64) error {
	if err := WriteIOMax(bps.path, &IOMax{Dev: bps.dev, Rbps: newBps, Wbps: newBps}); err != nil {
		return err
	}
	bps.rbpsMax = newBps
	bps.wbpsMax = newBps
	bps.Base.Throttle = newBps
	return nil
}

func (bps *Bps) SetPromise(newBps int64) error {
	return fmt.Errorf("not implemented")
}

type Iops struct {
	Base
	// dev is the device of cgroup
	dev string
	// path is the path of cgroup
	path string
	// pressure [μs] is `io.pressure`
	pressure Pressure `json:"-"`
	// rios [1] is rios of `io.stat`
	rios int64 `json:"-"`
	// wios [1] is wios of `io.stat`
	wios int64 `json:"-"`
	// riopsMax [1] is riops of `io.max`
	riopsMax int64 `json:"-"`
	// wiopsMax [1] is wiops of `io.max`
	wiopsMax int64 `json:"-"`
	// riops [1] is calculated from Rios
	riops int64 `json:"-"`
	// wiops [1] is calculated from Wios
	wiops int64 `json:"-"`
}

func (iops *Iops) Name() string {
	return IopsName
}

func (iops *Iops) Format(v int64) Value {
	return Int64(v)
}

func (iops *Iops) Current() Value {
	return Int64(iops.Base.Current)
}

func (iops *Iops) Promise() Value {
	return Int64(iops.Base.Promise)
}

func (iops *Iops) Throttle() Value {
	return Int64(iops.Base.Throttle)
}

func (iops *Iops) Pressure() UsecRate {
	return UsecRate(iops.Base.Pressure)
}

func (iops *Iops) Pressure10() float64 {
	return iops.pressure.Some.Avg10
}

func (iops *Iops) Pressure60() float64 {
	return iops.pressure.Some.Avg60
}

func (iops *Iops) Pressure300() float64 {
	return iops.pressure.Some.Avg300
}

func (iops *Iops) Update(pressure *Pressure, rios, wios, riops, wiops int64) {
	now := time.Now()
	iops.Base.Pressure = slope(iops.pressure.Some.Total, pressure.Some.Total, now.Sub(iops.Timestamp))
	iops.riops = slope(iops.rios, rios, now.Sub(iops.Timestamp))
	iops.wiops = slope(iops.wios, wios, now.Sub(iops.Timestamp))
	iops.Base.Current = max(iops.riops, iops.wiops)
	iops.Base.Throttle = max(riops, wiops)
	// iops.Base.Pressure = float64(iops.Base.Pressure) * unit(iops.Percent()) // shunt io pressure

	iops.pressure = *pressure
	iops.riopsMax = riops
	iops.wiopsMax = wiops
	iops.rios = rios
	iops.wios = wios
	iops.Timestamp = now
}

func (iops *Iops) SetThrottle(newIops int64) error {
	if err := WriteIOMax(iops.path, &IOMax{Dev: iops.dev, Riops: newIops, Wiops: newIops}); err != nil {
		return err
	}
	iops.riopsMax = newIops
	iops.wiopsMax = newIops
	iops.Base.Throttle = newIops
	return nil
}

func (iops *Iops) SetPromise(newIops int64) error {
	return fmt.Errorf("not implemented")
}

func slope(old, new int64, interval time.Duration) int64 {
	return int64(float64(new-old) / interval.Seconds())
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
