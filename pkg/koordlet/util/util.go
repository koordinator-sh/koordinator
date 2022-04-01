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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func DumpJSON(o interface{}) string {
	j, _ := json.Marshal(o)
	return string(j)
}

// MergeCPUSet merges the old cpuset with the new one, and also deduplicate and keeps a desc order by processor ids
// e.g. [1,0], [3,2,2,1] => [3,2,1,0]
func MergeCPUSet(old, new []int32) []int32 {
	cpuMap := map[int32]struct{}{}

	for _, id := range old {
		cpuMap[id] = struct{}{}
	}
	for _, id := range new {
		cpuMap[id] = struct{}{}
	}

	var merged []int32
	for id := range cpuMap {
		merged = append(merged, id)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i] > merged[j]
	})

	return merged
}

// ParseCPUSetStr parses cpuset string into a slice
// eg. "0-5,34,46-48" => [0,1,2,3,4,5,34,46,47,48]
func ParseCPUSetStr(cpusetStr string) ([]int32, error) {
	cpusetStr = strings.Trim(strings.TrimSpace(cpusetStr), "\n")
	if cpusetStr == "" {
		return nil, nil
	}

	// split CPU list string
	// eg. "0-5,34,46-48" => ["0-5", "34", "46-48"]
	ranges := strings.Split(cpusetStr, ",")

	var cpuset []int32
	for _, r := range ranges {
		boundaries := strings.Split(r, "-")
		if len(boundaries) == 1 {
			// only one element case, eg. "46"
			elem, err := strconv.Atoi(boundaries[0])
			if err != nil {
				return nil, err
			}
			cpuset = append(cpuset, int32(elem))
		} else if len(boundaries) == 2 {
			// multi-element case, eg. "0-5"
			start, err := strconv.Atoi(boundaries[0])
			if err != nil {
				return nil, err
			}
			end, err := strconv.Atoi(boundaries[1])
			if err != nil {
				return nil, err
			}
			// add all elements to the result.
			// e.g. "0-5" => [0, 1, 2, 3, 4, 5]
			for e := start; e <= end; e++ {
				cpuset = append(cpuset, int32(e))
			}
		}
	}

	return cpuset, nil
}

// GenerateCPUSetStr generates the cpuset string from the cpuset slice
// eg. [3,2,1,0] => "3,2,1,0"
func GenerateCPUSetStr(cpuset []int32) string {
	return strings.Trim(strings.Join(strings.Fields(fmt.Sprint(cpuset)), ","), "[]")
}

// WriteCgroupCPUSet writes the cgroup cpuset file according to the specified cgroup dir
func WriteCgroupCPUSet(cgroupFileDir, cpusetStr string) error {
	return ioutil.WriteFile(filepath.Join(cgroupFileDir, sysutil.CPUSFileName), []byte(cpusetStr), 0644)
}

// MergeCfg merges the old cfg with the new one, where new's values take precedence over old's when both fields exist.
// It would throw an error if:
//   1. either of the inputs was nil;
//   2. the inputs were not a pointer of the same json struct.
func MergeCfg(old, new interface{}) (interface{}, error) {
	if old == nil || new == nil {
		return nil, fmt.Errorf("illegal input type, neither of the inputs could be nil")
	}
	if reflect.TypeOf(old) != reflect.TypeOf(new) || reflect.TypeOf(old).Kind() != reflect.Ptr {
		return nil, fmt.Errorf("illegal input type, the inputs should be pointers of the same json struct")
	}
	// ignore err for serializing/deserializing the same struct type
	data, _ := json.Marshal(new)
	_ = json.Unmarshal(data, &old)
	return old, nil
}

func MinInt64(i, j int64) int64 {
	if i < j {
		return i
	}
	return j
}

func MaxInt64(i, j int64) int64 {
	if i > j {
		return i
	}
	return j
}
