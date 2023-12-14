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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_readMemInfo(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	tempInvalidMemInfoPath := filepath.Join(helper.TempDir, "no_meminfo")
	tempMemInfoPath := filepath.Join(helper.TempDir, "meminfo")
	memInfoContentStr := `MemTotal:       263432804 kB
MemFree:        254391744 kB
MemAvailable:   256703236 kB
Buffers:          958096 kB
Cached:          3763224 kB
SwapCached:            0 kB
Active:          2786012 kB
Inactive:        2223752 kB
Active(anon):     289488 kB
Inactive(anon):     1300 kB
Active(file):    2496524 kB
Inactive(file):  2222452 kB
Unevictable:           0 kB
Mlocked:               0 kB
SwapTotal:             0 kB
SwapFree:              0 kB
Dirty:               624 kB
Writeback:             0 kB
AnonPages:        281748 kB
Mapped:           495936 kB
Shmem:              2340 kB
Slab:            1097040 kB
SReclaimable:     445164 kB
SUnreclaim:       651876 kB
KernelStack:       20944 kB
PageTables:         7896 kB
NFS_Unstable:          0 kB
Bounce:                0 kB
WritebackTmp:          0 kB
CommitLimit:    131716400 kB
Committed_AS:    3825364 kB
VmallocTotal:   34359738367 kB
VmallocUsed:           0 kB
VmallocChunk:          0 kB
HardwareCorrupted:     0 kB
AnonHugePages:     38912 kB
ShmemHugePages:        0 kB
ShmemPmdMapped:        0 kB
CmaTotal:              0 kB
CmaFree:               0 kB
HugePages_Total:       0
HugePages_Free:        0
HugePages_Rsvd:        0
HugePages_Surp:        0
Hugepagesize:       2048 kB
DirectMap4k:      414760 kB
DirectMap2M:     8876032 kB
DirectMap1G:    261095424 kB`
	helper.WriteFileContents(tempMemInfoPath, memInfoContentStr)
	tempMemInfoPath1 := filepath.Join(helper.TempDir, "meminfo1")
	memInfoContentStr1 := `MemTotal:       263432804 kB
MemFree:        254391744 kB
MemAvailable:   256703236 kB
Buffers:          958096 kB
Cached:                0 kB
SwapCached:            0 kB
Active:          2786012 kB
Inactive:        2223752 kB
Active(anon):     289488 kB
Inactive(anon):     1300 kB
Active(file):    2496524 kB
Inactive(file):  2222452 kB
Unevictable:           0 kB
Mlocked:               0 kB
SwapTotal:             0 kB
SwapFree:              0 kB
Dirty:               624 kB
Writeback:             0 kB
AnonPages:        281748 kB
Mapped:           495936 kB
Shmem:              2340 kB
Slab:            1097040 kB
SReclaimable:     445164 kB
SUnreclaim:       651876 kB
KernelStack:       20944 kB
PageTables:         7896 kB
NFS_Unstable:          0 kB
Bounce:                0 kB
WritebackTmp:          0 kB
CommitLimit:    131716400 kB
Committed_AS:    3825364 kB
VmallocTotal:   34359738367 kB
VmallocUsed:           0 kB
VmallocChunk:          0 kB
HardwareCorrupted:     0 kB
AnonHugePages:     38912 kB
ShmemHugePages:        0 kB
ShmemPmdMapped:        0 kB
CmaTotal:              0 kB
CmaFree:               0 kB
HugePages_Total:       0
HugePages_Free:        0
HugePages_Rsvd:        0
HugePages_Surp:        0
Hugepagesize:       2048 kB
DirectMap4k:      414760 kB
DirectMap2M:     8876032 kB
DirectMap1G:    261095424 kB`
	helper.WriteFileContents(tempMemInfoPath1, memInfoContentStr1)
	numaMemInfoContentStr := `Node 1 MemTotal:       263432804 kB
Node 1 MemFree:        254391744 kB
Node 1 MemAvailable:   256703236 kB
Node 1 Buffers:          958096 kB
Node 1 Cached:                0 kB
Node 1 SwapCached:            0 kB
Node 1 Active:          2786012 kB
Node 1 Inactive:        2223752 kB
Node 1 Active(anon):     289488 kB
Node 1 Inactive(anon):     1300 kB
Node 1 Active(file):    2496524 kB
Node 1 Inactive(file):  2222452 kB
Node 1 Unevictable:           0 kB
Node 1 Mlocked:               0 kB
Node 1 SwapTotal:             0 kB
Node 1 SwapFree:              0 kB
Node 1 Dirty:               624 kB
Node 1 Writeback:             0 kB
Node 1 AnonPages:        281748 kB
Node 1 Mapped:           495936 kB
Node 1 Shmem:              2340 kB
Node 1 Slab:            1097040 kB
Node 1 SReclaimable:     445164 kB
Node 1 SUnreclaim:       651876 kB
Node 1 KernelStack:       20944 kB
Node 1 PageTables:         7896 kB
Node 1 NFS_Unstable:          0 kB
Node 1 Bounce:                0 kB
Node 1 WritebackTmp:          0 kB
Node 1 AnonHugePages:     38912 kB
Node 1 ShmemHugePages:        0 kB
Node 1 ShmemPmdMapped:        0 kB
Node 1 HugePages_Total:       0
Node 1 HugePages_Free:        0
Node 1 HugePages_Rsvd:        0
Node 1 HugePages_Surp:        0`
	tempNUMAMemInfoPath := filepath.Join(helper.TempDir, "node1", "meminfo")
	helper.WriteFileContents(tempNUMAMemInfoPath, numaMemInfoContentStr)
	type args struct {
		path   string
		isNUMA bool
	}
	tests := []struct {
		name    string
		args    args
		want    *MemInfo
		wantErr bool
	}{
		{
			name:    "read illegal mem stat",
			args:    args{path: tempInvalidMemInfoPath},
			want:    nil,
			wantErr: true,
		},
		{
			name: "read test mem stat path",
			args: args{path: tempMemInfoPath},
			want: &MemInfo{
				MemTotal: 263432804, MemFree: 254391744, MemAvailable: 256703236,
				Buffers: 958096, Cached: 3763224, SwapCached: 0,
				Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
				InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
				Unevictable: 0, Mlocked: 0, SwapTotal: 0,
				SwapFree: 0, Dirty: 624, Writeback: 0,
				AnonPages: 281748, Mapped: 495936, Shmem: 2340,
				Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
				KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
				Bounce: 0, WritebackTmp: 0, CommitLimit: 131716400,
				Committed_AS: 3825364, VmallocTotal: 34359738367, VmallocUsed: 0,
				VmallocChunk: 0, HardwareCorrupted: 0, AnonHugePages: 38912,
				HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
				HugePages_Surp: 0, Hugepagesize: 2048, DirectMap4k: 414760,
				DirectMap2M: 8876032, DirectMap1G: 261095424,
			},
			wantErr: false,
		},
		{
			name: "read test mem stat path",
			args: args{path: tempMemInfoPath},
			want: &MemInfo{
				MemTotal: 263432804, MemFree: 254391744, MemAvailable: 256703236,
				Buffers: 958096, Cached: 3763224, SwapCached: 0,
				Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
				InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
				Unevictable: 0, Mlocked: 0, SwapTotal: 0,
				SwapFree: 0, Dirty: 624, Writeback: 0,
				AnonPages: 281748, Mapped: 495936, Shmem: 2340,
				Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
				KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
				Bounce: 0, WritebackTmp: 0, CommitLimit: 131716400,
				Committed_AS: 3825364, VmallocTotal: 34359738367, VmallocUsed: 0,
				VmallocChunk: 0, HardwareCorrupted: 0, AnonHugePages: 38912,
				HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
				HugePages_Surp: 0, Hugepagesize: 2048, DirectMap4k: 414760,
				DirectMap2M: 8876032, DirectMap1G: 261095424,
			},
			wantErr: false,
		},
		{
			name: "read test mem stat path",
			args: args{path: tempMemInfoPath1},
			want: &MemInfo{
				MemTotal: 263432804, MemFree: 254391744, MemAvailable: 256703236,
				Buffers: 958096, Cached: 0, SwapCached: 0,
				Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
				InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
				Unevictable: 0, Mlocked: 0, SwapTotal: 0,
				SwapFree: 0, Dirty: 624, Writeback: 0,
				AnonPages: 281748, Mapped: 495936, Shmem: 2340,
				Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
				KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
				Bounce: 0, WritebackTmp: 0, CommitLimit: 131716400,
				Committed_AS: 3825364, VmallocTotal: 34359738367, VmallocUsed: 0,
				VmallocChunk: 0, HardwareCorrupted: 0, AnonHugePages: 38912,
				HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
				HugePages_Surp: 0, Hugepagesize: 2048, DirectMap4k: 414760,
				DirectMap2M: 8876032, DirectMap1G: 261095424,
			},
			wantErr: false,
		},
		{
			name: "read test numa meminfo path",
			args: args{path: tempNUMAMemInfoPath, isNUMA: true},
			want: &MemInfo{
				MemTotal: 263432804, MemFree: 254391744, MemAvailable: 256703236,
				Buffers: 958096, Cached: 0, SwapCached: 0,
				Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
				InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
				Unevictable: 0, Mlocked: 0, SwapTotal: 0,
				SwapFree: 0, Dirty: 624, Writeback: 0,
				AnonPages: 281748, Mapped: 495936, Shmem: 2340,
				Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
				KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
				Bounce: 0, WritebackTmp: 0, AnonHugePages: 38912,
				HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
				HugePages_Surp: 0,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := readMemInfo(tt.args.path, tt.args.isNUMA)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_GetMemInfoUsageKB(t *testing.T) {
	testMemInfo := `MemTotal:       263432804 kB
MemFree:        254391744 kB
MemAvailable:   256703236 kB
Buffers:          958096 kB
Cached:          3763224 kB
SwapCached:            0 kB
Active:          2786012 kB
Inactive:        2223752 kB
Active(anon):     289488 kB
Inactive(anon):     1300 kB
Active(file):    2496524 kB
Inactive(file):  2222452 kB
Unevictable:           0 kB
Mlocked:               0 kB
SwapTotal:             0 kB
SwapFree:              0 kB
Dirty:               624 kB
Writeback:             0 kB
AnonPages:        281748 kB
Mapped:           495936 kB
Shmem:              2340 kB
Slab:            1097040 kB
SReclaimable:     445164 kB
SUnreclaim:       651876 kB
KernelStack:       20944 kB
PageTables:         7896 kB
NFS_Unstable:          0 kB
Bounce:                0 kB
WritebackTmp:          0 kB
CommitLimit:    131716400 kB
Committed_AS:    3825364 kB
VmallocTotal:   34359738367 kB
VmallocUsed:           0 kB
VmallocChunk:          0 kB
HardwareCorrupted:     0 kB
AnonHugePages:     38912 kB
ShmemHugePages:        0 kB
ShmemPmdMapped:        0 kB
CmaTotal:              0 kB
CmaFree:               0 kB
HugePages_Total:       0
HugePages_Free:        0
HugePages_Rsvd:        0
HugePages_Surp:        0
Hugepagesize:       2048 kB
DirectMap4k:      414760 kB
DirectMap2M:     8876032 kB
DirectMap1G:    261095424 kB`

	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	helper.WriteProcSubFileContents(system.ProcMemInfoName, testMemInfo)

	memInfo, err := GetMemInfo()
	assert.NoError(t, err)
	assert.NotNil(t, memInfo)
	got := memInfo.MemTotalBytes()
	assert.Equal(t, uint64(263432804<<10), got)
	got = memInfo.MemUsageBytes()
	assert.Equal(t, uint64((263432804-256703236)<<10), got)
	got = memInfo.MemUsageWithPageCache()
	assert.Equal(t, uint64((263432804-256703236+2496524+2222452)<<10), got)
}

func TestGetNUMAMemInfo(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	numaMemInfoContentStr0 := `Node 0 MemTotal:       263432804 kB
Node 0 MemFree:        254391744 kB
Node 0 MemAvailable:   256703236 kB
Node 0 Buffers:          958096 kB
Node 0 Cached:                0 kB
Node 0 SwapCached:            0 kB
Node 0 Active:          2786012 kB
Node 0 Inactive:        2223752 kB
Node 0 Active(anon):     289488 kB
Node 0 Inactive(anon):     1300 kB
Node 0 Active(file):    2496524 kB
Node 0 Inactive(file):  2222452 kB
Node 0 Unevictable:           0 kB
Node 0 Mlocked:               0 kB
Node 0 SwapTotal:             0 kB
Node 0 SwapFree:              0 kB
Node 0 Dirty:               624 kB
Node 0 Writeback:             0 kB
Node 0 AnonPages:        281748 kB
Node 0 Mapped:           495936 kB
Node 0 Shmem:              2340 kB
Node 0 Slab:            1097040 kB
Node 0 SReclaimable:     445164 kB
Node 0 SUnreclaim:       651876 kB
Node 0 KernelStack:       20944 kB
Node 0 PageTables:         7896 kB
Node 0 NFS_Unstable:          0 kB
Node 0 Bounce:                0 kB
Node 0 WritebackTmp:          0 kB
Node 0 AnonHugePages:     38912 kB
Node 0 ShmemHugePages:        0 kB
Node 0 ShmemPmdMapped:        0 kB
Node 0 HugePages_Total:       0
Node 0 HugePages_Free:        0
Node 0 HugePages_Rsvd:        0
Node 0 HugePages_Surp:        0`
	numaMemInfoContentStr1 := `Node 1 MemTotal:       263432000 kB
Node 1 MemFree:        254391744 kB
Node 1 MemAvailable:   256703236 kB
Node 1 Buffers:          958096 kB
Node 1 Cached:                0 kB
Node 1 SwapCached:            0 kB
Node 1 Active:          2786012 kB
Node 1 Inactive:        2223752 kB
Node 1 Active(anon):     289488 kB
Node 1 Inactive(anon):     1300 kB
Node 1 Active(file):    2496524 kB
Node 1 Inactive(file):  2222452 kB
Node 1 Unevictable:           0 kB
Node 1 Mlocked:               0 kB
Node 1 SwapTotal:             0 kB
Node 1 SwapFree:              0 kB
Node 1 Dirty:               624 kB
Node 1 Writeback:             0 kB
Node 1 AnonPages:        281748 kB
Node 1 Mapped:           495936 kB
Node 1 Shmem:              2340 kB
Node 1 Slab:            1097040 kB
Node 1 SReclaimable:     445164 kB
Node 1 SUnreclaim:       651876 kB
Node 1 KernelStack:       20944 kB
Node 1 PageTables:         7896 kB
Node 1 NFS_Unstable:          0 kB
Node 1 Bounce:                0 kB
Node 1 WritebackTmp:          0 kB
Node 1 AnonHugePages:     38912 kB
Node 1 ShmemHugePages:        0 kB
Node 1 ShmemPmdMapped:        0 kB
Node 1 HugePages_Total:       0
Node 1 HugePages_Free:        0
Node 1 HugePages_Rsvd:        0
Node 1 HugePages_Surp:        0`
	numaMemInfoPath0 := system.GetNUMAMemInfoPath("node0")
	helper.WriteFileContents(numaMemInfoPath0, numaMemInfoContentStr0)
	numaMemInfoPath1 := system.GetNUMAMemInfoPath("node1")
	helper.WriteFileContents(numaMemInfoPath1, numaMemInfoContentStr1)

	testMemInfo0 := &MemInfo{
		MemTotal: 263432804, MemFree: 254391744, MemAvailable: 256703236,
		Buffers: 958096, Cached: 0, SwapCached: 0,
		Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
		InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
		Unevictable: 0, Mlocked: 0, SwapTotal: 0,
		SwapFree: 0, Dirty: 624, Writeback: 0,
		AnonPages: 281748, Mapped: 495936, Shmem: 2340,
		Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
		KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
		Bounce: 0, WritebackTmp: 0, AnonHugePages: 38912,
		HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
		HugePages_Surp: 0,
	}
	testMemInfo1 := &MemInfo{
		MemTotal: 263432000, MemFree: 254391744, MemAvailable: 256703236,
		Buffers: 958096, Cached: 0, SwapCached: 0,
		Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
		InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
		Unevictable: 0, Mlocked: 0, SwapTotal: 0,
		SwapFree: 0, Dirty: 624, Writeback: 0,
		AnonPages: 281748, Mapped: 495936, Shmem: 2340,
		Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
		KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
		Bounce: 0, WritebackTmp: 0, AnonHugePages: 38912,
		HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
		HugePages_Surp: 0,
	}

	expected := &NodeNUMAInfo{
		NUMAInfos: []NUMAInfo{
			{
				NUMANodeID: 0,
				MemInfo:    testMemInfo0,
			},
			{
				NUMANodeID: 1,
				MemInfo:    testMemInfo1,
			},
		},
		MemInfoMap: map[int32]*MemInfo{
			0: testMemInfo0,
			1: testMemInfo1,
		},
	}

	got, err := GetNodeNUMAInfo()
	assert.NoError(t, err)
	assert.Equal(t, expected, got)

	// test partial failure
	numaMemInfoPath2 := system.GetNUMAMemInfoPath("node2")
	helper.MkDirAll(filepath.Dir(numaMemInfoPath2))
	got, err = GetNodeNUMAInfo()
	assert.Error(t, err)
	assert.Nil(t, got)

	// test path not exist
	helper.Cleanup()
	got, err = GetNodeNUMAInfo()
	assert.Error(t, err)
	assert.Nil(t, got)
}

func TestGetNodeHugePagesInfo(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()

	numaHugePage1GPath0 := system.GetNUMAHugepagesNrPath("node0", "hugepages-1048576kB")
	helper.WriteFileContents(numaHugePage1GPath0, "10")
	numaHugePage2MPath0 := system.GetNUMAHugepagesNrPath("node0", "hugepages-2048kB")
	helper.WriteFileContents(numaHugePage2MPath0, "20")

	numaHugePage1GPath1 := system.GetNUMAHugepagesNrPath("node1", "hugepages-1048576kB")
	helper.WriteFileContents(numaHugePage1GPath1, "10")
	numaHugePage2MPath1 := system.GetNUMAHugepagesNrPath("node1", "hugepages-2048kB")
	helper.WriteFileContents(numaHugePage2MPath1, "20")

	expected := map[int32]map[uint64]*HugePagesInfo{
		0: {
			Hugepage1Gkbyte: {
				NumPages: 10,
				PageSize: Hugepage1Gkbyte,
			},
			Hugepage2Mkbyte: {
				NumPages: 20,
				PageSize: Hugepage2Mkbyte,
			},
		},
		1: {
			Hugepage1Gkbyte: {
				NumPages: 10,
				PageSize: Hugepage1Gkbyte,
			},
			Hugepage2Mkbyte: {
				NumPages: 20,
				PageSize: Hugepage2Mkbyte,
			},
		},
	}

	got, err := GetNodeHugePagesInfo()
	assert.NoError(t, err)
	assert.Equal(t, expected, got)

	// test partial failure
	numaMemInfoPath2 := system.GetNUMAMemInfoPath("node2")
	helper.MkDirAll(filepath.Dir(numaMemInfoPath2))
	got, err = GetNodeHugePagesInfo()
	assert.Error(t, err)
	assert.Nil(t, got)

	// test path not exist
	helper.Cleanup()
	got, err = GetNodeHugePagesInfo()
	assert.Error(t, err)
	assert.Nil(t, got)
}

func TestGetAndMergeHugepageToNumaInfo(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	numaMemInfoContentStr0 := `Node 0 MemTotal:       263432804 kB
Node 0 MemFree:        254391744 kB
Node 0 MemAvailable:   256703236 kB
Node 0 Buffers:          958096 kB
Node 0 Cached:                0 kB
Node 0 SwapCached:            0 kB
Node 0 Active:          2786012 kB
Node 0 Inactive:        2223752 kB
Node 0 Active(anon):     289488 kB
Node 0 Inactive(anon):     1300 kB
Node 0 Active(file):    2496524 kB
Node 0 Inactive(file):  2222452 kB
Node 0 Unevictable:           0 kB
Node 0 Mlocked:               0 kB
Node 0 SwapTotal:             0 kB
Node 0 SwapFree:              0 kB
Node 0 Dirty:               624 kB
Node 0 Writeback:             0 kB
Node 0 AnonPages:        281748 kB
Node 0 Mapped:           495936 kB
Node 0 Shmem:              2340 kB
Node 0 Slab:            1097040 kB
Node 0 SReclaimable:     445164 kB
Node 0 SUnreclaim:       651876 kB
Node 0 KernelStack:       20944 kB
Node 0 PageTables:         7896 kB
Node 0 NFS_Unstable:          0 kB
Node 0 Bounce:                0 kB
Node 0 WritebackTmp:          0 kB
Node 0 AnonHugePages:     38912 kB
Node 0 ShmemHugePages:        0 kB
Node 0 ShmemPmdMapped:        0 kB
Node 0 HugePages_Total:       0
Node 0 HugePages_Free:        0
Node 0 HugePages_Rsvd:        0
Node 0 HugePages_Surp:        0`
	numaMemInfoContentStr1 := `Node 1 MemTotal:       263432000 kB
Node 1 MemFree:        254391744 kB
Node 1 MemAvailable:   256703236 kB
Node 1 Buffers:          958096 kB
Node 1 Cached:                0 kB
Node 1 SwapCached:            0 kB
Node 1 Active:          2786012 kB
Node 1 Inactive:        2223752 kB
Node 1 Active(anon):     289488 kB
Node 1 Inactive(anon):     1300 kB
Node 1 Active(file):    2496524 kB
Node 1 Inactive(file):  2222452 kB
Node 1 Unevictable:           0 kB
Node 1 Mlocked:               0 kB
Node 1 SwapTotal:             0 kB
Node 1 SwapFree:              0 kB
Node 1 Dirty:               624 kB
Node 1 Writeback:             0 kB
Node 1 AnonPages:        281748 kB
Node 1 Mapped:           495936 kB
Node 1 Shmem:              2340 kB
Node 1 Slab:            1097040 kB
Node 1 SReclaimable:     445164 kB
Node 1 SUnreclaim:       651876 kB
Node 1 KernelStack:       20944 kB
Node 1 PageTables:         7896 kB
Node 1 NFS_Unstable:          0 kB
Node 1 Bounce:                0 kB
Node 1 WritebackTmp:          0 kB
Node 1 AnonHugePages:     38912 kB
Node 1 ShmemHugePages:        0 kB
Node 1 ShmemPmdMapped:        0 kB
Node 1 HugePages_Total:       0
Node 1 HugePages_Free:        0
Node 1 HugePages_Rsvd:        0
Node 1 HugePages_Surp:        0`
	numaMemInfoPath0 := system.GetNUMAMemInfoPath("node0")
	helper.WriteFileContents(numaMemInfoPath0, numaMemInfoContentStr0)
	numaMemInfoPath1 := system.GetNUMAMemInfoPath("node1")
	helper.WriteFileContents(numaMemInfoPath1, numaMemInfoContentStr1)

	numaHugePage1GPath0 := system.GetNUMAHugepagesNrPath("node0", "hugepages-1048576kB")
	helper.WriteFileContents(numaHugePage1GPath0, "10")
	numaHugePage2MPath0 := system.GetNUMAHugepagesNrPath("node0", "hugepages-2048kB")
	helper.WriteFileContents(numaHugePage2MPath0, "20")

	numaHugePage1GPath1 := system.GetNUMAHugepagesNrPath("node1", "hugepages-1048576kB")
	helper.WriteFileContents(numaHugePage1GPath1, "10")
	numaHugePage2MPath1 := system.GetNUMAHugepagesNrPath("node1", "hugepages-2048kB")
	helper.WriteFileContents(numaHugePage2MPath1, "20")

	testMemInfo0 := &MemInfo{
		MemTotal: 263432804, MemFree: 254391744, MemAvailable: 256703236,
		Buffers: 958096, Cached: 0, SwapCached: 0,
		Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
		InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
		Unevictable: 0, Mlocked: 0, SwapTotal: 0,
		SwapFree: 0, Dirty: 624, Writeback: 0,
		AnonPages: 281748, Mapped: 495936, Shmem: 2340,
		Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
		KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
		Bounce: 0, WritebackTmp: 0, AnonHugePages: 38912,
		HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
		HugePages_Surp: 0,
	}
	testMemInfo1 := &MemInfo{
		MemTotal: 263432000, MemFree: 254391744, MemAvailable: 256703236,
		Buffers: 958096, Cached: 0, SwapCached: 0,
		Active: 2786012, Inactive: 2223752, ActiveAnon: 289488,
		InactiveAnon: 1300, ActiveFile: 2496524, InactiveFile: 2222452,
		Unevictable: 0, Mlocked: 0, SwapTotal: 0,
		SwapFree: 0, Dirty: 624, Writeback: 0,
		AnonPages: 281748, Mapped: 495936, Shmem: 2340,
		Slab: 1097040, SReclaimable: 445164, SUnreclaim: 651876,
		KernelStack: 20944, PageTables: 7896, NFS_Unstable: 0,
		Bounce: 0, WritebackTmp: 0, AnonHugePages: 38912,
		HugePages_Total: 0, HugePages_Free: 0, HugePages_Rsvd: 0,
		HugePages_Surp: 0,
	}

	testEmptyHugePagesMap0 := map[uint64]*HugePagesInfo{
		Hugepage1Gkbyte: {
			NumPages: 0,
			PageSize: Hugepage1Gkbyte,
		},
		Hugepage2Mkbyte: {
			NumPages: 0,
			PageSize: Hugepage2Mkbyte,
		},
	}

	testHugePagesMap0 := map[uint64]*HugePagesInfo{
		Hugepage1Gkbyte: {
			NumPages: 10,
			PageSize: Hugepage1Gkbyte,
		},
		Hugepage2Mkbyte: {
			NumPages: 20,
			PageSize: Hugepage2Mkbyte,
		},
	}

	testHugePagesMap1 := map[uint64]*HugePagesInfo{
		Hugepage1Gkbyte: {
			NumPages: 10,
			PageSize: Hugepage1Gkbyte,
		},
		Hugepage2Mkbyte: {
			NumPages: 20,
			PageSize: Hugepage2Mkbyte,
		},
	}

	expectedWithoutHugepage := &NodeNUMAInfo{
		NUMAInfos: []NUMAInfo{
			{
				NUMANodeID: 0,
				MemInfo:    testMemInfo0,
			},
			{
				NUMANodeID: 1,
				MemInfo:    testMemInfo1,
			},
		},
		MemInfoMap: map[int32]*MemInfo{
			0: testMemInfo0,
			1: testMemInfo1,
		},
	}

	expectedEmptyHugepage := &NodeNUMAInfo{
		NUMAInfos: []NUMAInfo{
			{
				NUMANodeID: 0,
				MemInfo:    testMemInfo0,
				HugePages:  testEmptyHugePagesMap0,
			},
			{
				NUMANodeID: 1,
				MemInfo:    testMemInfo1,
				HugePages:  testEmptyHugePagesMap0,
			},
		},
		MemInfoMap: map[int32]*MemInfo{
			0: testMemInfo0,
			1: testMemInfo1,
		},
		HugePagesMap: map[int32]map[uint64]*HugePagesInfo{
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
		},
	}

	expected := &NodeNUMAInfo{
		NUMAInfos: []NUMAInfo{
			{
				NUMANodeID: 0,
				MemInfo:    testMemInfo0,
				HugePages:  testHugePagesMap0,
			},
			{
				NUMANodeID: 1,
				MemInfo:    testMemInfo1,
				HugePages:  testHugePagesMap1,
			},
		},
		MemInfoMap: map[int32]*MemInfo{
			0: testMemInfo0,
			1: testMemInfo1,
		},
		HugePagesMap: map[int32]map[uint64]*HugePagesInfo{
			0: {
				Hugepage1Gkbyte: {
					NumPages: 10,
					PageSize: Hugepage1Gkbyte,
				},
				Hugepage2Mkbyte: {
					NumPages: 20,
					PageSize: Hugepage2Mkbyte,
				},
			},
			1: {
				Hugepage1Gkbyte: {
					NumPages: 10,
					PageSize: Hugepage1Gkbyte,
				},
				Hugepage2Mkbyte: {
					NumPages: 20,
					PageSize: Hugepage2Mkbyte,
				},
			},
		},
	}

	numaInfo, err := GetNodeNUMAInfo()
	assert.NoError(t, err)
	assert.Equal(t, expectedWithoutHugepage, numaInfo)

	numaInfo1, err := GetNodeNUMAInfo()
	assert.NoError(t, err)
	assert.Equal(t, expectedWithoutHugepage, numaInfo1)

	numaInfo2, err := GetNodeNUMAInfo()
	assert.NoError(t, err)
	assert.Equal(t, expectedWithoutHugepage, numaInfo2)

	numaInfoWithHugePage := GetAndMergeHugepageToNumaInfo(numaInfo)
	assert.Equal(t, expected, numaInfoWithHugePage)

	// test partial failure

	numaMemInfoPath2 := system.GetNUMAMemInfoPath("node2")
	helper.MkDirAll(filepath.Dir(numaMemInfoPath2))
	fmt.Printf("%v", numaInfo1)
	numaInfoWithHugePageErr := GetAndMergeHugepageToNumaInfo(numaInfo1)
	assert.Equal(t, expectedEmptyHugepage, numaInfoWithHugePageErr)

	// test path not exist
	helper.Cleanup()
	numaInfoWithHugePageErr2 := GetAndMergeHugepageToNumaInfo(numaInfo2)
	assert.Equal(t, expectedEmptyHugepage, numaInfoWithHugePageErr2)
}
