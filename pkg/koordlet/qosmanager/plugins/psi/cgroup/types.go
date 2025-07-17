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
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

type Value interface {
	Int64() int64
	IsMax() bool
	String() string
	MarshalJSON() ([]byte, error)
}

type Resource interface {
	// Name is the name of this resource
	Name() string
	// Current is absolute value of resource usage
	Current() Value
	// Promise is absolute promise value of resource usage, which is expected to be reached without pressure
	Promise() Value
	// Throttle is absolute throttle value of resource usage, which is a reason of pressure
	Throttle() Value
	// Pressure [μs] is the pressure increment of resource usage since last sample
	Pressure() UsecRate
	// Pressure10 [0~1] is the average pressure of resource usage since last 10s
	Pressure10() float64
	// Pressure60 [0~1] is the average pressure of resource usage since last 60s
	Pressure60() float64
	// Pressure300 [0~1] is the average pressure of resource usage since last 300s
	Pressure300() float64

	// SetPromise sets the promise value of resource usage
	SetPromise(int64) error
	// SetThrottle sets the throttle value of resource usage
	SetThrottle(int64) error

	// Format formats the value in style of this resource
	Format(v int64) Value
	// Resource returns the raw resource
	Raw() Base
}

type Base struct {
	// Current is absolute value of resource usage
	Current int64 `json:"current"`
	// Promise is absolute promise value of resource usage, which is expected to be reached without pressure
	Promise int64 `json:"promise"`
	// Throttle is absolute throttle value of resource usage, which is a reason of pressure
	Throttle int64 `json:"throttle"`
	// Pressure [μs] is the pressure increment of resource usage since last second
	Pressure int64 `json:"pressure"`
	// Timestamp is the time when the sample is taken
	Timestamp time.Time `json:"timestamp"`
}

func (r Base) Raw() Base {
	return r
}

type Int64 int64

func (i Int64) Int64() int64 {
	return int64(i)
}

func (i Int64) IsMax() bool {
	return int64(i) == Max
}

func (i Int64) String() string {
	if int64(i) == Max {
		return "max"
	}
	return fmt.Sprintf("%d", i)
}

func (i Int64) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.String())
}

type UsecRate int64

func (ur UsecRate) Int64() int64 {
	return int64(ur)
}

func (ur UsecRate) IsMax() bool {
	return int64(ur) == Max
}

// Rate returns the percentage of the usec in second.
func (ur UsecRate) Rate() float64 {
	return float64(ur) / float64(time.Second.Microseconds())
}

func (ur UsecRate) String() string {
	if int64(ur) == Max {
		return "max"
	}
	return fmt.Sprintf("%.2f%%", ur.Rate()*100)
}

func (ur UsecRate) MarshalJSON() ([]byte, error) {
	return json.Marshal(ur.String())
}

type Bytes int64

func (b Bytes) Int64() int64 {
	return int64(b)
}

func (b Bytes) IsMax() bool {
	return int64(b) == Max
}

func (b Bytes) String() string {
	if int64(b) == Max {
		return "max"
	}
	return resource.NewQuantity(int64(b), resource.BinarySI).String()
}

func (b Bytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.String())
}

type Usec int64

func (u Usec) Int64() int64 {
	return int64(u)
}

func (u Usec) IsMax() bool {
	return int64(u) == Max
}

func (u Usec) String() string {
	if int64(u) == Max {
		return "max"
	}
	return (time.Duration(u) * time.Microsecond).String()
}

func (u Usec) MarshalJSON() ([]byte, error) {
	return json.Marshal(u.String())
}
