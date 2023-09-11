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
	"bufio"
	"fmt"
	"os"
	"os/user"
	"reflect"
	"strconv"
	"strings"

	"go.uber.org/atomic"
	"k8s.io/klog/v2"
)

var (
	isSupportColdMemory *atomic.Bool = atomic.NewBool(false)
	isStartColdMemory   *atomic.Bool = atomic.NewBool(false)
)

type ColdPageInfoByKidled struct {
	Version             string   `json:"version"`
	PageScans           uint64   `json:"page_scans"`
	SlabScans           uint64   `json:"slab_scans"`
	ScanPeriodInSeconds uint64   `json:"scan_period_in_seconds"`
	UseHierarchy        uint64   `json:"use_hierarchy"`
	Buckets             []uint64 `json:"buckets"`
	Csei                []uint64 `json:"csei"`
	Dsei                []uint64 `json:"dsei"`
	Cfei                []uint64 `json:"cfei"`
	Dfei                []uint64 `json:"dfei"`
	Csui                []uint64 `json:"csui"`
	Dsui                []uint64 `json:"dsui"`
	Cfui                []uint64 `json:"cfui"`
	Dfui                []uint64 `json:"dfui"`
	Csea                []uint64 `json:"csea"`
	Dsea                []uint64 `json:"dsea"`
	Cfea                []uint64 `json:"cfea"`
	Dfea                []uint64 `json:"dfea"`
	Csua                []uint64 `json:"csua"`
	Dsua                []uint64 `json:"dsua"`
	Cfua                []uint64 `json:"cfua"`
	Dfua                []uint64 `json:"dfua"`
	Slab                []uint64 `json:"slab"`
}

type KidledConfig struct {
	ScanPeriodInseconds uint32
	UseHierarchy        uint8
}

func ParseMemoryIdlePageStats(content string) (*ColdPageInfoByKidled, error) {
	lines := strings.Split(content, "\n")
	statMap := make(map[string]interface{})
	var info = ColdPageInfoByKidled{}
	if (len(lines)) != 31 {
		return nil, fmt.Errorf("format err")
	}
	for i, line := range lines {
		if i == 0 {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			statMap[fields[1][:len(fields[1])-1]] = fields[2]
		} else if i < 5 {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			val, _ := strconv.ParseUint(fields[2], 10, 64)
			statMap[fields[1][:len(fields[1])-1]] = val
		} else if i == 5 {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			s := strings.Split(fields[2], ",")
			var val = make([]uint64, len(s))
			for k, v := range s {
				val[k], _ = strconv.ParseUint(v, 10, 64)
			}
			statMap[fields[1][:len(fields[1])-1]] = val
		} else if i >= 14 {
			fields := strings.Fields(line)
			if len(fields) < 1 {
				continue
			}
			var val = make([]uint64, len(fields)-1)
			for i := 1; i < len(fields); i++ {
				val[i-1], _ = strconv.ParseUint(fields[i], 10, 64)
			}
			statMap[fields[0]] = val
		}
	}
	elem := reflect.ValueOf(&info).Elem()
	typeOfElem := elem.Type()
	for i := 0; i < elem.NumField(); i++ {
		val, ok := statMap[typeOfElem.Field(i).Tag.Get("json")]
		if ok {
			if typeOfElem.Field(i).Type.Kind() == reflect.String {
				elem.Field(i).SetString(val.(string))
			} else if typeOfElem.Field(i).Type.Kind() == reflect.Uint64 {
				elem.Field(i).SetUint(val.(uint64))
			} else if typeOfElem.Field(i).Type.Kind() == reflect.Slice {
				sliceValue := reflect.ValueOf(val)
				elem.Field(i).Set(sliceValue)
			}
		}
	}
	return &info, nil
}

func (i *ColdPageInfoByKidled) GetColdPageTotalBytes() uint64 {
	sum := func(nums ...[]uint64) uint64 {
		var total uint64
		for _, v := range nums {
			for _, num := range v {
				total += num
			}
		}
		return total
	}
	return sum(i.Csei, i.Dsei, i.Cfei, i.Dfei, i.Csui, i.Dsui, i.Cfui, i.Dfui, i.Csea, i.Dsea, i.Cfea, i.Dfea, i.Csua, i.Dsua, i.Cfua, i.Dfua, i.Slab)
}

// check kidled and set var isSupportColdSupport
func IsKidledSupport() bool {
	isSupportColdMemory.Store(false)
	isSupport, str := KidledScanPeriodInSeconds.IsSupported("")
	if !isSupport {
		klog.V(4).Infof("file scan_period_in_seconds is not exist %s", str)
		return isSupportColdMemory.Load()
	}
	isSupport, str = KidledUseHierarchy.IsSupported("")
	if !isSupport {
		klog.V(4).Infof("file use_hierarchy is not exist %s", str)
		return isSupportColdMemory.Load()
	}
	isSupportColdMemory.Store(true)
	return isSupportColdMemory.Load()
}

func GetIsSupportColdMemory() bool {
	return isSupportColdMemory.Load()
}

func SetIsSupportColdMemory(flag bool) {
	isSupportColdMemory.Store(flag)
}

func GetIsStartColdMemory() bool {
	return isStartColdMemory.Load()
}

func SetIsStartColdMemory(flag bool) {
	isStartColdMemory.Store(flag)
}

func SetKidledScanPeriodInSeconds(period uint32) error {
	path := KidledScanPeriodInSeconds.Path("")
	usr, _ := user.Current()
	klog.V(4).Infof("usrname", usr.Username)
	klog.V(4).Infof("groupname", usr.Name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		scanpath, _ := os.Stat(path)
		klog.V(4).Infof("scanpath", scanpath.Mode().Perm())
		rootdir, _ := os.Stat(GetSysRootDir())
		klog.V(4).Infof("rootpath", rootdir.Mode().Perm())
		return err
	}
	defer file.Close()
	write := bufio.NewWriter(file)
	write.WriteString(fmt.Sprintf("%d", period))
	write.Flush()
	return nil
}
func SetKidledUseHierarchy(useHierarchy uint8) error {
	path := KidledUseHierarchy.Path("")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	write := bufio.NewWriter(file)
	write.WriteString(fmt.Sprintf("%d", useHierarchy))
	write.Flush()
	return nil
}
func NewDefaultKidledConfig() *KidledConfig {
	return &KidledConfig{
		ScanPeriodInseconds: 5,
		UseHierarchy:        1,
	}
}
