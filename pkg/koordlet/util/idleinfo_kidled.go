package util

import (
	"os"
	"reflect"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

const ColdPageInfoFileName = "memory.idle_page_stats"
const KidledScanPeriodInSecondsFilePath = "/kernel/mm/kidled/scan_period_in_seconds"
const KidledUseHierarchyFilePath = "/kernel/mm/kidled/use_hierarchy"

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

func KidledColdPageInfo(path string) (*ColdPageInfoByKidled, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	statMap := make(map[string]interface{})
	var info = ColdPageInfoByKidled{}
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

func (i *ColdPageInfoByKidled) NodeMemWithHotPageUsageBytes() (uint64, error) {
	Meminfo, err := GetMemInfo()
	if err != nil {
		return 0, err
	}
	//memWithHotPage=Total-Free-ColdPage
	memWithHotPageUsageBytes := Meminfo.MemTotal*1024 - Meminfo.MemFree*1024 - i.GetColdPageTotalBytes()
	return memWithHotPageUsageBytes, nil
}

func IsKidledSupported(kidledScanPeriodInSecondsFilePath string, kidledUseHierarchyFilePath string) bool {
	_, err := os.Stat(kidledScanPeriodInSecondsFilePath)
	if err != nil {
		klog.Errorf("file scan_period_in_seconds is not exist,err: %s", err)
		return false
	}
	content, err := os.ReadFile(kidledScanPeriodInSecondsFilePath)
	if err != nil {
		klog.Errorf("read scan_period_in_seconds err: %s", err)
		return false
	}
	scanPeriodInSeconds, err := strconv.Atoi(string(content))
	if err != nil {
		klog.Errorf("string to int scan_period_in_seconds err: %s", err)
		return false
	}
	if scanPeriodInSeconds <= 0 {
		klog.Errorf("scan_period_in_seconds is negative,err: %s", err)
		return false
	}
	_, err = os.Stat(kidledUseHierarchyFilePath)
	if err != nil {
		klog.Errorf("file use_hierarchy is not exist,err: %s", err)
		return false
	}
	content, err = os.ReadFile(kidledUseHierarchyFilePath)
	if err != nil {
		klog.Errorf("read use_hierarchy ,err: %s", err)
		return false
	}
	useHierarchy, err := strconv.Atoi(string(content))
	if err != nil {
		klog.Errorf("string to int useHierarchy err: %s", err)
		return false
	}
	if useHierarchy != 1 {
		klog.Errorf("useHierarchy is not equal to 1,err: %s", err)
		return false
	}
	return true
}
