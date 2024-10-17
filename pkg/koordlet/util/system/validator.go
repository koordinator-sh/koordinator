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

package system

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

// ResourceValidator validates the resource value
type ResourceValidator interface {
	Validate(value string) (isValid bool, msg string)
}

type RangeValidator struct {
	max int64
	min int64
}

func (r *RangeValidator) Validate(value string) (bool, string) {
	if value == "" {
		return false, fmt.Sprintf("value is nil")
	}
	var v int64
	var err error
	if value == CgroupMaxSymbolStr { // compatible to cgroup-v2 file valued "max"
		v = math.MaxInt64
	} else {
		v, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return false, fmt.Sprintf("value %v is not an integer, err: %v", value, err)
		}
	}
	if v < r.min || v > r.max {
		return false, fmt.Sprintf("value %v is not in [min:%d, max:%d]", value, r.min, r.max)
	}
	return true, ""
}

type CPUSetStrValidator struct{}

func (c *CPUSetStrValidator) Validate(value string) (bool, string) {
	_, err := cpuset.Parse(value)
	if err != nil {
		return false, fmt.Sprintf("value %v is not valid cpuset string", value)
	}
	return true, ""
}

type BlkIORangeValidator struct {
	resource string
	max      int64
	min      int64
}

func (r *BlkIORangeValidator) Validate(value string) (bool, string) {
	if value == "" {
		return false, "value is nil"
	}

	newValues := []string{}
	switch r.resource {
	case BlkioTRBpsName, BlkioTRIopsName, BlkioTWBpsName, BlkioTWIopsName, BlkioIOWeightName:
		// 253:16 2048
		// 253:16 0
		rst := strings.Split(value, " ")
		if len(rst) == 2 {
			newValues = append(newValues, rst[1])
		}
	case BlkioIOQoSName:
		// 253:16 enable=1 ctrl=user rpct=95 rlat=3000 wpct=95 wlat=4000
		// 253:16 enable=0
		rst := strings.Split(value, " ")
		if len(rst) == 7 {
			newValues = append(newValues, []string{rst[3][5:], rst[4][5:], rst[5][5:], rst[6][5:]}...)
		}
	case BlkioIOModelName:
		// 253:16 ctrl=user rbps=3324911720 rseqiops=168274 rrandiops=352545 wbps=2765819289 wseqiops=367565 wrandiops=339390
		// 253:16 ctrl=auto
		rst := strings.Split(value, " ")
		if len(rst) == 8 {
			newValues = append(newValues, []string{rst[2][5:], rst[3][9:], rst[4][10:], rst[5][5:], rst[6][9:], rst[7][10:]}...)
		}
	default:
		return false, "unknown blkio resource name"
	}

	for _, newValue := range newValues {
		var v int64
		var err error
		if newValue == CgroupMaxSymbolStr { // compatible to cgroup-v2 file valued "max"
			v = math.MaxInt64
		} else {
			v, err = strconv.ParseInt(newValue, 10, 64)
			if err != nil {
				return false, fmt.Sprintf("value %v is not an integer, err: %v", newValue, err)
			}
		}
		if v < r.min || v > r.max {
			return false, fmt.Sprintf("value %v is not in [min:%d, max:%d]", newValue, r.min, r.max)
		}
	}

	return true, ""
}

type NetClsRangeValidator struct {
	resource string
}

const (
	maxClassIdDecimal = 41231686041
	maxClassIdHex     = 99999999
)

func (r *NetClsRangeValidator) Validate(value string) (bool, string) {
	if value == "" {
		return false, "value is nil"
	}

	if r.resource == NetClsClassIdName {
		if strings.HasPrefix(value, "0x") {
			value = value[2:]
			// You can write hexadecimal values to net_cls.classid; the format for these values is 0xAAAABBBB;
			// AAAA is the major handle number and BBBB is the minor handle number. Reading net_cls.classid yields a decimal result.
			// so, the max length of this value is 8.
			hexVal, err := strconv.Atoi(value)
			if err != nil {
				return false, err.Error()
			}

			if hexVal < 0 || hexVal > maxClassIdHex {
				return false, "class id is invalid, decimal value must in 0~0x99999999"
			}
		} else {
			decimalVal, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				return false, err.Error()
			}

			if decimalVal > maxClassIdDecimal || decimalVal < 0 {
				return false, fmt.Sprintf("class id is invaild, decimal vaule must in 0~%d", maxClassIdDecimal)
			}
		}
	}

	return true, ""
}
