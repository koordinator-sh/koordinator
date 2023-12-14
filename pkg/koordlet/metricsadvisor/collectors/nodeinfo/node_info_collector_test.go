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

package nodeinfo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestNodeInfoCollector(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
		TSDBPath:              t.TempDir(),
		TSDBEnablePromMetrics: false,
	})
	assert.NoError(t, err)
	defer func() {
		err = metricCache.Close()
		assert.NoError(t, err)
	}()
	t.Run("test", func(t *testing.T) {
		opt := &framework.Options{
			Config: &framework.Config{
				CollectNodeCPUInfoInterval: 60 * time.Second,
			},
			MetricCache: metricCache,
		}
		c := New(opt)
		assert.NotNil(t, c)

		c.Setup(nil)
		assert.True(t, c.Enabled())
		assert.False(t, c.Started())

		collector := c.(*nodeInfoCollector)
		assert.NotPanics(t, func() {
			collector.collectNodeInfo()
		})
	})
}

func Test_collectNodeNUMAInfo(t *testing.T) {

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

	testMemInfo0 := &koordletutil.MemInfo{
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
	testMemInfo1 := &koordletutil.MemInfo{
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

	tts := []struct {
		name           string
		initfunc       func(*system.FileTestUtil)
		enableHugePage bool
		expected       *koordletutil.NodeNUMAInfo
	}{
		{
			name: "only memory",
			initfunc: func(helper *system.FileTestUtil) {
				numaMemInfoPath0 := system.GetNUMAMemInfoPath("node0")
				helper.WriteFileContents(numaMemInfoPath0, numaMemInfoContentStr0)
				numaMemInfoPath1 := system.GetNUMAMemInfoPath("node1")
				helper.WriteFileContents(numaMemInfoPath1, numaMemInfoContentStr1)
			},
			enableHugePage: false,
			expected: &koordletutil.NodeNUMAInfo{
				NUMAInfos: []koordletutil.NUMAInfo{
					{
						NUMANodeID: 0,
						MemInfo:    testMemInfo0,
					},
					{
						NUMANodeID: 1,
						MemInfo:    testMemInfo1,
					},
				},
				MemInfoMap: map[int32]*koordletutil.MemInfo{
					0: testMemInfo0,
					1: testMemInfo1,
				},
			},
		},
		{
			name: "memory and hugepage, but featuregate not enable",
			initfunc: func(helper *system.FileTestUtil) {
				numaMemInfoPath0 := system.GetNUMAMemInfoPath("node0")
				helper.WriteFileContents(numaMemInfoPath0, numaMemInfoContentStr0)
				numaMemInfoPath1 := system.GetNUMAMemInfoPath("node1")
				helper.WriteFileContents(numaMemInfoPath1, numaMemInfoContentStr1)
				numaHugePage2MPath0 := system.GetNUMAHugepagesNrPath("node0", "hugepages-1048576kB")
				helper.WriteFileContents(numaHugePage2MPath0, "20")
				numaHugePage2MPath1 := system.GetNUMAHugepagesNrPath("node1", "hugepages-1048576kB")
				helper.WriteFileContents(numaHugePage2MPath1, "40")
			},
			enableHugePage: false,
			expected: &koordletutil.NodeNUMAInfo{
				NUMAInfos: []koordletutil.NUMAInfo{
					{
						NUMANodeID: 0,
						MemInfo:    testMemInfo0,
					},
					{
						NUMANodeID: 1,
						MemInfo:    testMemInfo1,
					},
				},
				MemInfoMap: map[int32]*koordletutil.MemInfo{
					0: testMemInfo0,
					1: testMemInfo1,
				},
			},
		},
		{
			name: "memory and 2M hugepage",
			initfunc: func(helper *system.FileTestUtil) {
				numaMemInfoPath0 := system.GetNUMAMemInfoPath("node0")
				helper.WriteFileContents(numaMemInfoPath0, numaMemInfoContentStr0)
				numaMemInfoPath1 := system.GetNUMAMemInfoPath("node1")
				helper.WriteFileContents(numaMemInfoPath1, numaMemInfoContentStr1)
				numaHugePage2MPath0 := system.GetNUMAHugepagesNrPath("node0", "hugepages-2048kB")
				helper.WriteFileContents(numaHugePage2MPath0, "20")
				numaHugePage2MPath1 := system.GetNUMAHugepagesNrPath("node1", "hugepages-2048kB")
				helper.WriteFileContents(numaHugePage2MPath1, "40")
			},
			enableHugePage: true,
			expected: &koordletutil.NodeNUMAInfo{
				NUMAInfos: []koordletutil.NUMAInfo{
					{
						NUMANodeID: 0,
						MemInfo:    testMemInfo0,
						HugePages: map[uint64]*koordletutil.HugePagesInfo{
							koordletutil.Hugepage2Mkbyte: {
								PageSize: koordletutil.Hugepage2Mkbyte,
								NumPages: 20,
							},
							koordletutil.Hugepage1Gkbyte: {
								PageSize: koordletutil.Hugepage1Gkbyte,
								NumPages: 0,
							},
						},
					},
					{
						NUMANodeID: 1,
						MemInfo:    testMemInfo1,
						HugePages: map[uint64]*koordletutil.HugePagesInfo{
							koordletutil.Hugepage2Mkbyte: {
								PageSize: koordletutil.Hugepage2Mkbyte,
								NumPages: 40,
							},
							koordletutil.Hugepage1Gkbyte: {
								PageSize: koordletutil.Hugepage1Gkbyte,
								NumPages: 0,
							},
						},
					},
				},
				MemInfoMap: map[int32]*koordletutil.MemInfo{
					0: testMemInfo0,
					1: testMemInfo1,
				},
				HugePagesMap: map[int32]map[uint64]*koordletutil.HugePagesInfo{
					0: {
						koordletutil.Hugepage2Mkbyte: {
							PageSize: koordletutil.Hugepage2Mkbyte,
							NumPages: 20,
						},
						koordletutil.Hugepage1Gkbyte: {
							PageSize: koordletutil.Hugepage1Gkbyte,
							NumPages: 0,
						},
					},
					1: {
						koordletutil.Hugepage2Mkbyte: {
							PageSize: koordletutil.Hugepage2Mkbyte,
							NumPages: 40,
						},
						koordletutil.Hugepage1Gkbyte: {
							PageSize: koordletutil.Hugepage1Gkbyte,
							NumPages: 0,
						},
					},
				},
			},
		},
		{
			name: "memory and 1G hugepage",
			initfunc: func(helper *system.FileTestUtil) {
				numaMemInfoPath0 := system.GetNUMAMemInfoPath("node0")
				helper.WriteFileContents(numaMemInfoPath0, numaMemInfoContentStr0)
				numaMemInfoPath1 := system.GetNUMAMemInfoPath("node1")
				helper.WriteFileContents(numaMemInfoPath1, numaMemInfoContentStr1)
				numaHugePage1GPath0 := system.GetNUMAHugepagesNrPath("node0", "hugepages-1048576kB")
				helper.WriteFileContents(numaHugePage1GPath0, "10")

				numaHugePage1GPath1 := system.GetNUMAHugepagesNrPath("node1", "hugepages-1048576kB")
				helper.WriteFileContents(numaHugePage1GPath1, "20")
			},
			enableHugePage: true,
			expected: &koordletutil.NodeNUMAInfo{
				NUMAInfos: []koordletutil.NUMAInfo{
					{
						NUMANodeID: 0,
						MemInfo:    testMemInfo0,
						HugePages: map[uint64]*koordletutil.HugePagesInfo{
							koordletutil.Hugepage2Mkbyte: {
								PageSize: koordletutil.Hugepage2Mkbyte,
								NumPages: 0,
							},
							koordletutil.Hugepage1Gkbyte: {
								PageSize: koordletutil.Hugepage1Gkbyte,
								NumPages: 10,
							},
						},
					},
					{
						NUMANodeID: 1,
						MemInfo:    testMemInfo1,
						HugePages: map[uint64]*koordletutil.HugePagesInfo{
							koordletutil.Hugepage2Mkbyte: {
								PageSize: koordletutil.Hugepage2Mkbyte,
								NumPages: 0,
							},
							koordletutil.Hugepage1Gkbyte: {
								PageSize: koordletutil.Hugepage1Gkbyte,
								NumPages: 20,
							},
						},
					},
				},
				MemInfoMap: map[int32]*koordletutil.MemInfo{
					0: testMemInfo0,
					1: testMemInfo1,
				},
				HugePagesMap: map[int32]map[uint64]*koordletutil.HugePagesInfo{
					0: {
						koordletutil.Hugepage2Mkbyte: {
							PageSize: koordletutil.Hugepage2Mkbyte,
							NumPages: 0,
						},
						koordletutil.Hugepage1Gkbyte: {
							PageSize: koordletutil.Hugepage1Gkbyte,
							NumPages: 10,
						},
					},
					1: {
						koordletutil.Hugepage2Mkbyte: {
							PageSize: koordletutil.Hugepage2Mkbyte,
							NumPages: 0,
						},
						koordletutil.Hugepage1Gkbyte: {
							PageSize: koordletutil.Hugepage1Gkbyte,
							NumPages: 20,
						},
					},
				},
			},
		},
		{
			name: "memory and 2M & 1G hugepage",
			initfunc: func(helper *system.FileTestUtil) {
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
			},
			enableHugePage: true,
			expected: &koordletutil.NodeNUMAInfo{
				NUMAInfos: []koordletutil.NUMAInfo{
					{
						NUMANodeID: 0,
						MemInfo:    testMemInfo0,
						HugePages: map[uint64]*koordletutil.HugePagesInfo{
							koordletutil.Hugepage2Mkbyte: {
								PageSize: koordletutil.Hugepage2Mkbyte,
								NumPages: 20,
							},
							koordletutil.Hugepage1Gkbyte: {
								PageSize: koordletutil.Hugepage1Gkbyte,
								NumPages: 10,
							},
						},
					},
					{
						NUMANodeID: 1,
						MemInfo:    testMemInfo1,
						HugePages: map[uint64]*koordletutil.HugePagesInfo{
							koordletutil.Hugepage2Mkbyte: {
								PageSize: koordletutil.Hugepage2Mkbyte,
								NumPages: 20,
							},
							koordletutil.Hugepage1Gkbyte: {
								PageSize: koordletutil.Hugepage1Gkbyte,
								NumPages: 10,
							},
						},
					},
				},
				MemInfoMap: map[int32]*koordletutil.MemInfo{
					0: testMemInfo0,
					1: testMemInfo1,
				},
				HugePagesMap: map[int32]map[uint64]*koordletutil.HugePagesInfo{
					0: {
						koordletutil.Hugepage2Mkbyte: {
							PageSize: koordletutil.Hugepage2Mkbyte,
							NumPages: 20,
						},
						koordletutil.Hugepage1Gkbyte: {
							PageSize: koordletutil.Hugepage1Gkbyte,
							NumPages: 10,
						},
					},
					1: {
						koordletutil.Hugepage2Mkbyte: {
							PageSize: koordletutil.Hugepage2Mkbyte,
							NumPages: 20,
						},
						koordletutil.Hugepage1Gkbyte: {
							PageSize: koordletutil.Hugepage1Gkbyte,
							NumPages: 10,
						},
					},
				},
			},
		},
	}
	for _, tt := range tts {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()

			enabled := features.DefaultKoordletFeatureGate.Enabled(features.HugePageReport)
			testFeatureGates := map[string]bool{string(features.HugePageReport): tt.enableHugePage}
			err := features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
			assert.NoError(t, err)
			defer func() {
				testFeatureGates[string(features.HugePageReport)] = enabled
				err = features.DefaultMutableKoordletFeatureGate.SetFromMap(testFeatureGates)
				assert.NoError(t, err)
			}()

			metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
				TSDBPath:              t.TempDir(),
				TSDBEnablePromMetrics: false,
			})
			assert.NoError(t, err)
			defer func() {
				err = metricCache.Close()
				assert.NoError(t, err)
			}()

			tt.initfunc(helper)

			c := &nodeInfoCollector{
				collectInterval: 60 * time.Second,
				storage:         metricCache,
				started:         atomic.NewBool(false),
			}
			err = c.collectNodeNUMAInfo()
			assert.NoError(t, err)
			nodeNUMAInfoRaw, ok := c.storage.Get(metriccache.NodeNUMAInfoKey)
			assert.True(t, ok)
			nodeNUMAInfo, ok := nodeNUMAInfoRaw.(*koordletutil.NodeNUMAInfo)
			assert.True(t, ok)
			assert.Equal(t, tt.expected, nodeNUMAInfo)

			// test collect failed when sys files are missing
			helper.Cleanup()
			err = c.collectNodeNUMAInfo()
			assert.Error(t, err)
		})
	}
}
