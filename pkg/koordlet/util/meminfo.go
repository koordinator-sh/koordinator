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
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	Hugepage1Gkbyte = 1048576
	Hugepage2Mkbyte = 2048
)

// MemInfo is the content of system /proc/meminfo.
// NOTE: the unit of each field is KiB.
type MemInfo struct {
	MemTotal          uint64 `json:"mem_total"`
	MemFree           uint64 `json:"mem_free"`
	MemAvailable      uint64 `json:"mem_available"`
	Buffers           uint64 `json:"buffers"`
	Cached            uint64 `json:"cached"`
	SwapCached        uint64 `json:"swap_cached"`
	Active            uint64 `json:"active"`
	Inactive          uint64 `json:"inactive"`
	ActiveAnon        uint64 `json:"active_anon" field:"Active(anon)"`
	InactiveAnon      uint64 `json:"inactive_anon" field:"Inactive(anon)"`
	ActiveFile        uint64 `json:"active_file" field:"Active(file)"`
	InactiveFile      uint64 `json:"inactive_file" field:"Inactive(file)"`
	Unevictable       uint64 `json:"unevictable"`
	Mlocked           uint64 `json:"mlocked"`
	SwapTotal         uint64 `json:"swap_total"`
	SwapFree          uint64 `json:"swap_free"`
	Dirty             uint64 `json:"dirty"`
	Writeback         uint64 `json:"write_back"`
	AnonPages         uint64 `json:"anon_pages"`
	Mapped            uint64 `json:"mapped"`
	Shmem             uint64 `json:"shmem"`
	Slab              uint64 `json:"slab"`
	SReclaimable      uint64 `json:"s_reclaimable"`
	SUnreclaim        uint64 `json:"s_unclaim"`
	KernelStack       uint64 `json:"kernel_stack"`
	PageTables        uint64 `json:"page_tables"`
	NFS_Unstable      uint64 `json:"nfs_unstable"`
	Bounce            uint64 `json:"bounce"`
	WritebackTmp      uint64 `json:"writeback_tmp"`
	CommitLimit       uint64 `json:"commit_limit"`
	Committed_AS      uint64 `json:"committed_as"`
	VmallocTotal      uint64 `json:"vmalloc_total"`
	VmallocUsed       uint64 `json:"vmalloc_used"`
	VmallocChunk      uint64 `json:"vmalloc_chunk"`
	HardwareCorrupted uint64 `json:"hardware_corrupted"`
	AnonHugePages     uint64 `json:"anon_huge_pages"`
	HugePages_Total   uint64 `json:"huge_pages_total"`
	HugePages_Free    uint64 `json:"huge_pages_free"`
	HugePages_Rsvd    uint64 `json:"huge_pages_rsvd"`
	HugePages_Surp    uint64 `json:"huge_pages_surp"`
	Hugepagesize      uint64 `json:"hugepagesize"`
	DirectMap4k       uint64 `json:"direct_map_4k"`
	DirectMap2M       uint64 `json:"direct_map_2M"`
	DirectMap1G       uint64 `json:"direct_map_1G"`
}

type HugePagesInfo struct {
	NumPages uint64 `json:"numPages,omitempty"`
	PageSize uint64 `json:"pageSize,omitempty"`
}

func (i *HugePagesInfo) MemTotalBytes() uint64 {
	return i.PageSize * i.NumPages * 1024
}

// MemTotalBytes returns the mem info's total bytes.
func (i *MemInfo) MemTotalBytes() uint64 {
	return i.MemTotal * 1024
}

// MemUsageBytes returns the mem info's usage bytes.
func (i *MemInfo) MemUsageBytes() uint64 {
	// total - available
	return (i.MemTotal - i.MemAvailable) * 1024
}

// MemWithPageCacheUsageBytes returns the usage of mem with page cache bytes.
func (i *MemInfo) MemUsageWithPageCache() uint64 {
	// total - free
	return (i.MemTotal - i.MemFree) * 1024
}

// readMemInfo reads and parses the meminfo from the given file.
// If isNUMA=false, it parses each line without a prefix like "Node 0". Otherwise, it parses each line with the NUMA
// node prefix like "Node 0".
func readMemInfo(path string, isNUMA bool) (*MemInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	info := MemInfo{}
	// Maps a meminfo metric to its value (i.e. MemTotal --> 100000)
	statMap := make(map[string]uint64)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		var fields []string

		// trim the NUMA prefix:
		// e.g. `Node 1 MemTotal:       1048576 kB` -> `MemTotal:       1048576 kB`
		if isNUMA {
			fields = strings.SplitN(line, " ", 3)
			if len(fields) < 3 {
				continue
			}
			line = fields[2]
		}

		fields = strings.SplitN(line, ":", 2)
		if len(fields) < 2 {
			continue
		}
		valFields := strings.Fields(fields[1])
		val, _ := strconv.ParseUint(valFields[0], 10, 64)
		statMap[fields[0]] = val
	}

	elem := reflect.ValueOf(&info).Elem()
	typeOfElem := elem.Type()

	for i := 0; i < elem.NumField(); i++ {
		val, ok := statMap[typeOfElem.Field(i).Name]
		if ok {
			elem.Field(i).SetUint(val)
			continue
		}
		val, ok = statMap[typeOfElem.Field(i).Tag.Get("field")]
		if ok {
			elem.Field(i).SetUint(val)
		}
	}

	return &info, nil
}

func GetHugePagesInfo(nodeDir string) (map[uint64]*HugePagesInfo, error) {
	hugePagesInfo := map[uint64]*HugePagesInfo{
		Hugepage1Gkbyte: {
			NumPages: 0,
			PageSize: Hugepage1Gkbyte,
		},
		Hugepage2Mkbyte: {
			NumPages: 0,
			PageSize: Hugepage2Mkbyte,
		},
	}
	hugepageDir := system.GetNUMAHugepagesDir(nodeDir)
	hugeDirs, err := os.ReadDir(hugepageDir)
	if err != nil {
		klog.Warningf("failed to read hugepage dir %s, err: %w", hugepageDir, err)
		return hugePagesInfo, err
	}

	for _, st := range hugeDirs {
		nameArray := strings.Split(st.Name(), "-")
		if len(nameArray) < 2 {
			klog.Warningf("Split '-' failed, the directory name %s is invalid, it must be either hugepages-1048576kB or hugepages-2048kB, nameArray: %v, len(nameArray): %d", st.Name(), nameArray, len(nameArray))
			continue
		}
		pageSizeArray := strings.Split(nameArray[1], "kB")
		if len(pageSizeArray) < 1 {
			klog.Warningf("Split 'kB' failed, the directory name %s is invalid, it must be either hugepages-1048576kB or hugepages-2048kB, pageSizeArray: %v, len(pageSizeArray): %d", st.Name(), pageSizeArray, len(pageSizeArray))
			continue
		}
		pageSize, err := strconv.ParseUint(pageSizeArray[0], 10, 64)
		if err != nil {
			klog.Warningf("hugepage pageSize parse failed, it must be either hugepages-1048576kB or hugepages-2048kB, the dir: %s, origin data: %s, err: %v", st.Name(), pageSizeArray[0], err)
			continue
		}

		if _, ok := hugePagesInfo[pageSize]; !ok {
			klog.Warningf("An abnormal hugepage %d, the dir: %s", pageSize, st.Name())
			continue
		}

		nrPath := system.GetNUMAHugepagesNrPath(nodeDir, st.Name())
		val, err := os.ReadFile(nrPath)
		if err != nil {
			return hugePagesInfo, err
		}

		var numPages uint64
		// we use sscanf as the file as a new-line that trips up ParseUint
		// it returns the number of tokens successfully parsed, so if
		// n != 1, it means we were unable to parse a number from the file
		n, err := fmt.Sscanf(string(val), "%d", &numPages)
		if err != nil || n != 1 {
			klog.Warningf("could not parse file nr_hugepage for %s, contents %q", st.Name(), string(val))
			continue
		}

		hugePagesInfo[pageSize].NumPages = numPages
	}

	return hugePagesInfo, nil
}

func GetMemInfo() (*MemInfo, error) {
	memInfoPath := system.GetProcFilePath(system.ProcMemInfoName)
	memInfo, err := readMemInfo(memInfoPath, false)
	if err != nil {
		return nil, err
	}
	return memInfo, nil
}

type NUMAInfo struct {
	NUMANodeID int32                     `json:"numaNodeID,omitempty"`
	MemInfo    *MemInfo                  `json:"memInfo,omitempty"`
	HugePages  map[uint64]*HugePagesInfo `json:"hugePages,omitempty"`
}

// NodeNUMAInfo represents the node NUMA information.
// Currently, it just contains the meminfo for each NUMA node.
type NodeNUMAInfo struct {
	NUMAInfos    []NUMAInfo                          `json:"numaInfos,omitempty"`
	MemInfoMap   map[int32]*MemInfo                  `json:"memInfoMap,omitempty"` // NUMANodeID -> MemInfo
	HugePagesMap map[int32]map[uint64]*HugePagesInfo `json:"hugePagesMap,omitempty"`
}

// GetNodeNUMAInfo gets the node NUMA information with the pre-configured sysfs path.
func GetNodeNUMAInfo() (*NodeNUMAInfo, error) {
	numaNodeParentDir := system.GetSysNUMADir()
	nodeDirs, err := os.ReadDir(numaNodeParentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read NUMA dir, err: %w", err)
	}

	result := &NodeNUMAInfo{
		MemInfoMap: map[int32]*MemInfo{},
	}
	maxNodeID := int32(-1)
	for _, n := range nodeDirs {
		dirName := n.Name() // assert string pattern `nodeX`
		if len(dirName) < 4 || dirName[:4] != "node" {
			klog.V(4).Infof("failed to get node NUMA info, err: invalid dir name %s", dirName)
			continue
		}

		nodeIDRaw, err := strconv.ParseInt(dirName[4:], 10, 32)
		if err != nil {
			klog.V(4).Infof("failed to parse NUMA ID, err: invalid dir name %s, err %v", dirName, err)
			continue
		}
		nodeID := int32(nodeIDRaw)

		numaMemInfoPath := system.GetNUMAMemInfoPath(dirName)
		memInfo, err := readMemInfo(numaMemInfoPath, true)
		if err != nil {
			klog.V(4).Infof("failed to read NUMA info, dir %s, err: %v", dirName, err)
			continue
		}

		numaInfo := NUMAInfo{
			NUMANodeID: nodeID,
			MemInfo:    memInfo,
		}
		result.NUMAInfos = append(result.NUMAInfos, numaInfo)
		result.MemInfoMap[nodeID] = memInfo
		if nodeID > maxNodeID {
			maxNodeID = nodeID
		}
	}

	if len(nodeDirs) != len(result.NUMAInfos) {
		return nil, fmt.Errorf("invalid number of NUMA meminfo, dir %v, parsed %v",
			len(nodeDirs), len(result.NUMAInfos))
	}
	if len(result.NUMAInfos) != int(maxNodeID+1) {
		return nil, fmt.Errorf("unexpected number of NUMA node, max ID %v, parsed %v",
			maxNodeID, len(result.NUMAInfos))
	}

	// sort NUMA infos by the order of node id
	sort.Slice(result.NUMAInfos, func(i, j int) bool {
		return result.NUMAInfos[i].NUMANodeID < result.NUMAInfos[j].NUMANodeID
	})

	return result, nil
}

// GetNodeHugePagesInfo gets the node NUMA hugepage information with pre-configured sysfs path.
func GetNodeHugePagesInfo() (map[int32]map[uint64]*HugePagesInfo, error) {
	numaNodeParentDir := system.GetSysNUMADir()
	nodeDirs, err := os.ReadDir(numaNodeParentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read NUMA dir, err: %w", err)
	}

	hugePagesMap := make(map[int32]map[uint64]*HugePagesInfo)
	for _, n := range nodeDirs {
		dirName := n.Name() // assert string pattern `nodeX`
		if len(dirName) < 4 || dirName[:4] != "node" {
			klog.V(4).Infof("failed to get node NUMA info, err: invalid dir name %s", dirName)
			return nil, fmt.Errorf("failed to get node NUMA info, err: invalid dir name %s", dirName)
		}

		nodeIDRaw, err := strconv.ParseInt(dirName[4:], 10, 32)
		if err != nil {
			klog.V(4).Infof("failed to parse NUMA ID, err: invalid dir name %s, err %v", dirName, err)
			return nil, fmt.Errorf("failed to get node NUMA info, err: invalid dir name %s", dirName)
		}
		nodeID := int32(nodeIDRaw)
		hugepageInfos, err := GetHugePagesInfo(dirName)
		if err != nil {
			klog.V(4).Infof("failed to read hugepage info, just set empty hugepage info, dir %s, err: %v", dirName, err)
			return nil, fmt.Errorf("failed to read hugepage info, just set empty hugepage info, dir %s, err: %v", dirName, err)
		}
		hugePagesMap[nodeID] = hugepageInfos
	}
	return hugePagesMap, nil
}

func GetAndMergeHugepageToNumaInfo(numaInfo *NodeNUMAInfo) *NodeNUMAInfo {
	var hugePagesMap map[int32]map[uint64]*HugePagesInfo
	var err error
	hugePagesMap, err = GetNodeHugePagesInfo()
	if err != nil {
		hugePagesMap = map[int32]map[uint64]*HugePagesInfo{
			0: {
				Hugepage1Gkbyte: {
					NumPages: 0,
					PageSize: Hugepage1Gkbyte,
				},
				Hugepage2Mkbyte: {
					NumPages: 0,
					PageSize: Hugepage2Mkbyte,
				},
			},
			1: {
				Hugepage1Gkbyte: {
					NumPages: 0,
					PageSize: Hugepage1Gkbyte,
				},
				Hugepage2Mkbyte: {
					NumPages: 0,
					PageSize: Hugepage2Mkbyte,
				},
			},
		}
	}
	numaInfo.HugePagesMap = hugePagesMap
	for i, info := range numaInfo.NUMAInfos {
		if _, ok := hugePagesMap[info.NUMANodeID]; ok {
			numaInfo.NUMAInfos[i].HugePages = hugePagesMap[info.NUMANodeID]
		}
	}

	return numaInfo
}
